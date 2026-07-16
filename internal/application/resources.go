package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/ports"
	"github.com/RecRivenVI/gallery/internal/rules"
)

type Library struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

type Source struct {
	ID          string
	LibraryID   string
	DisplayName string
	RootPath    string
	CreatedAt   time.Time
}

type RuleVersion struct {
	RuleSetID    string
	Version      string
	PackageHash  string
	SemanticHash string
	RuleIRHash   string
	Canonical    []byte
	IR           rules.RuleIR
	CreatedAt    time.Time
}

type SourceRuleBinding struct {
	ID           string
	SourceID     string
	SemanticHash string
	Parameters   []byte
	Priority     int
	RuleIRHash   string
	IR           rules.RuleIR
	CreatedAt    time.Time
}

type Resources struct {
	control *sql.DB
	dirs    appdirs.Dirs
	fs      ports.FileSystem
	clock   ports.Clock
	ids     ports.IDGenerator
}

func NewResources(control *sql.DB, dirs appdirs.Dirs, fileSystem ports.FileSystem, clock ports.Clock, ids ports.IDGenerator) (*Resources, error) {
	if control == nil || fileSystem == nil || clock == nil || ids == nil {
		return nil, fmt.Errorf("Resources 缺少依赖")
	}
	return &Resources{control: control, dirs: dirs, fs: fileSystem, clock: clock, ids: ids}, nil
}

func (r *Resources) CreateLibrary(ctx context.Context, name string) (Library, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 256 {
		return Library{}, fault.WithField(fault.CodeValidation, "name", nil)
	}
	id, err := r.ids.New(domain.IDLibrary)
	if err != nil {
		return Library{}, fault.New(fault.CodeInternal, true, err)
	}
	result := Library{ID: id.String(), Name: name, CreatedAt: r.clock.Now().UTC()}
	if _, err := r.control.ExecContext(ctx,
		"INSERT INTO libraries (library_id, name, created_at) VALUES (?, ?, ?)",
		result.ID, result.Name, result.CreatedAt.Unix(),
	); err != nil {
		return Library{}, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

func (r *Resources) GetLibrary(ctx context.Context, id string) (Library, error) {
	if _, err := domain.ParseID(domain.IDLibrary, id); err != nil {
		return Library{}, fault.New(fault.CodeNotFound, false, nil)
	}
	var result Library
	var createdAt int64
	err := r.control.QueryRowContext(ctx,
		"SELECT library_id, name, created_at FROM libraries WHERE library_id = ?", id,
	).Scan(&result.ID, &result.Name, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Library{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return Library{}, fault.New(fault.CodeInternal, true, err)
	}
	result.CreatedAt = time.Unix(createdAt, 0).UTC()
	return result, nil
}

func (r *Resources) CreateSource(ctx context.Context, libraryID, displayName, root string) (Source, error) {
	if _, err := r.GetLibrary(ctx, libraryID); err != nil {
		return Source{}, err
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" || len(displayName) > 256 {
		return Source{}, fault.WithField(fault.CodeValidation, "displayName", nil)
	}
	canonical, key, err := r.canonicalSourceRoot(root)
	if err != nil {
		return Source{}, err
	}
	registered, err := r.sourceRoots(ctx)
	if err != nil {
		return Source{}, err
	}
	if err := r.dirs.ValidateDisjoint(r.fs, append(registered, canonical)); err != nil {
		return Source{}, err
	}
	id, err := r.ids.New(domain.IDSource)
	if err != nil {
		return Source{}, fault.New(fault.CodeInternal, true, err)
	}
	result := Source{
		ID: id.String(), LibraryID: libraryID, DisplayName: displayName,
		RootPath: canonical, CreatedAt: r.clock.Now().UTC(),
	}
	if _, err := r.control.ExecContext(ctx, `
INSERT INTO sources (source_id, library_id, display_name, root_path, root_key, created_at)
VALUES (?, ?, ?, ?, ?, ?)`, result.ID, result.LibraryID, result.DisplayName, result.RootPath, key, result.CreatedAt.Unix()); err != nil {
		return Source{}, fault.New(fault.CodeConflict, false, err)
	}
	return result, nil
}

func (r *Resources) GetSource(ctx context.Context, id string) (Source, error) {
	if _, err := domain.ParseID(domain.IDSource, id); err != nil {
		return Source{}, fault.New(fault.CodeNotFound, false, nil)
	}
	var result Source
	var createdAt int64
	err := r.control.QueryRowContext(ctx, `
SELECT source_id, library_id, display_name, root_path, created_at FROM sources WHERE source_id = ?`, id,
	).Scan(&result.ID, &result.LibraryID, &result.DisplayName, &result.RootPath, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return Source{}, fault.New(fault.CodeInternal, true, err)
	}
	result.CreatedAt = time.Unix(createdAt, 0).UTC()
	return result, nil
}

func (r *Resources) SourceAvailable(source Source) bool {
	info, err := r.fs.Stat(source.RootPath)
	return err == nil && info.IsDir()
}

func (r *Resources) CreateRuleVersion(ctx context.Context, input []byte) (RuleVersion, error) {
	compiled, err := rules.CompilePackage(input)
	if err != nil {
		return RuleVersion{}, fault.New(fault.CodeRuleSchemaInvalid, false, err)
	}
	irJSON, err := rules.CanonicalJSON(mustJSON(compiled.IR))
	if err != nil {
		return RuleVersion{}, fault.New(fault.CodeRuleSchemaInvalid, false, err)
	}
	now := r.clock.Now().UTC()
	_, err = r.control.ExecContext(ctx, `
INSERT OR IGNORE INTO rule_versions
(semantic_hash, rule_set_id, version, package_hash, canonical_json, compiler_version, rule_ir_hash, compiled_ir_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, compiled.SemanticHash, compiled.RuleSetID, compiled.Version,
		compiled.PackageHash, string(compiled.Canonical), rules.CompilerVersion, compiled.RuleIRHash, string(irJSON), now.Unix())
	if err != nil {
		return RuleVersion{}, fault.New(fault.CodeConflict, false, err)
	}
	return r.GetRuleVersion(ctx, compiled.SemanticHash)
}

func (r *Resources) GetRuleVersion(ctx context.Context, semanticHash string) (RuleVersion, error) {
	var result RuleVersion
	var canonical, irJSON string
	var createdAt int64
	err := r.control.QueryRowContext(ctx, `
SELECT rule_set_id, version, package_hash, semantic_hash, rule_ir_hash, canonical_json, compiled_ir_json, created_at
FROM rule_versions WHERE semantic_hash = ?`, semanticHash).Scan(
		&result.RuleSetID, &result.Version, &result.PackageHash, &result.SemanticHash,
		&result.RuleIRHash, &canonical, &irJSON, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RuleVersion{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return RuleVersion{}, fault.New(fault.CodeInternal, true, err)
	}
	result.IR, err = rules.DecodeIR([]byte(irJSON))
	if err != nil {
		return RuleVersion{}, fault.New(fault.CodeInternal, false, err)
	}
	result.Canonical = []byte(canonical)
	result.CreatedAt = time.Unix(createdAt, 0).UTC()
	return result, nil
}

func (r *Resources) CreateSourceRuleBinding(ctx context.Context, sourceID, semanticHash string, parameters []byte, priority int) (SourceRuleBinding, error) {
	if priority < 0 || priority > 10000 {
		return SourceRuleBinding{}, fault.WithField(fault.CodeValidation, "priority", nil)
	}
	if _, err := r.GetSource(ctx, sourceID); err != nil {
		return SourceRuleBinding{}, err
	}
	version, err := r.GetRuleVersion(ctx, semanticHash)
	if err != nil {
		return SourceRuleBinding{}, err
	}
	compiled, err := rules.CompilePackage(version.Canonical)
	if err != nil {
		return SourceRuleBinding{}, fault.New(fault.CodeRuleSchemaInvalid, false, err)
	}
	ir, irHash, canonicalParameters, err := rules.CompileBinding(compiled, parameters)
	if err != nil {
		return SourceRuleBinding{}, fault.New(fault.CodeRuleParameterInvalid, false, err)
	}
	irJSON, _ := rules.CanonicalJSON(mustJSON(ir))
	id, err := r.ids.New(domain.IDSourceRuleBinding)
	if err != nil {
		return SourceRuleBinding{}, fault.New(fault.CodeInternal, true, err)
	}
	result := SourceRuleBinding{
		ID: id.String(), SourceID: sourceID, SemanticHash: semanticHash, Parameters: canonicalParameters,
		Priority: priority, RuleIRHash: irHash, IR: ir, CreatedAt: r.clock.Now().UTC(),
	}
	if _, err := r.control.ExecContext(ctx, `
INSERT INTO source_rule_bindings
(binding_id, source_id, semantic_hash, parameters_json, priority, rule_ir_hash, compiled_ir_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, result.ID, result.SourceID, result.SemanticHash, string(result.Parameters),
		result.Priority, result.RuleIRHash, string(irJSON), result.CreatedAt.Unix()); err != nil {
		return SourceRuleBinding{}, fault.New(fault.CodeConflict, false, err)
	}
	return result, nil
}

func (r *Resources) GetSourceRuleBinding(ctx context.Context, id string) (SourceRuleBinding, error) {
	if _, err := domain.ParseID(domain.IDSourceRuleBinding, id); err != nil {
		return SourceRuleBinding{}, fault.New(fault.CodeNotFound, false, nil)
	}
	var result SourceRuleBinding
	var parameters, irJSON string
	var createdAt int64
	err := r.control.QueryRowContext(ctx, `
SELECT binding_id, source_id, semantic_hash, parameters_json, priority, rule_ir_hash, compiled_ir_json, created_at
FROM source_rule_bindings WHERE binding_id = ?`, id).Scan(
		&result.ID, &result.SourceID, &result.SemanticHash, &parameters, &result.Priority,
		&result.RuleIRHash, &irJSON, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceRuleBinding{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return SourceRuleBinding{}, fault.New(fault.CodeInternal, true, err)
	}
	result.Parameters = []byte(parameters)
	result.IR, err = rules.DecodeIR([]byte(irJSON))
	if err != nil {
		return SourceRuleBinding{}, fault.New(fault.CodeInternal, false, err)
	}
	result.CreatedAt = time.Unix(createdAt, 0).UTC()
	return result, nil
}

func (r *Resources) BindingForSource(ctx context.Context, sourceID string) (SourceRuleBinding, error) {
	var id string
	err := r.control.QueryRowContext(ctx,
		"SELECT binding_id FROM source_rule_bindings WHERE source_id = ? ORDER BY priority, binding_id LIMIT 1", sourceID,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceRuleBinding{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return SourceRuleBinding{}, fault.New(fault.CodeInternal, true, err)
	}
	return r.GetSourceRuleBinding(ctx, id)
}

func (r *Resources) sourceRoots(ctx context.Context) ([]string, error) {
	rows, err := r.control.QueryContext(ctx, "SELECT root_path FROM sources ORDER BY source_id")
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var root string
		if err := rows.Scan(&root); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result = append(result, root)
	}
	return result, rows.Err()
}

func (r *Resources) canonicalSourceRoot(root string) (string, string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", "", fault.WithField(fault.CodeSourcePathInvalid, "rootPath", nil)
	}
	abs, err := r.fs.Abs(root)
	if err != nil {
		return "", "", fault.WithField(fault.CodeSourcePathInvalid, "rootPath", err)
	}
	real, err := r.fs.EvalSymlinks(filepath.Clean(abs))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", "", fault.WithField(fault.CodeSourcePathInvalid, "rootPath", nil)
		}
		return "", "", fault.WithField(fault.CodeSourcePathInvalid, "rootPath", err)
	}
	info, err := r.fs.Stat(real)
	if err != nil || !info.IsDir() {
		return "", "", fault.WithField(fault.CodeSourcePathInvalid, "rootPath", err)
	}
	canonical := filepath.Clean(real)
	key := canonical
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return canonical, key, nil
}

func mustJSON(value any) []byte {
	result, _ := json.Marshal(value)
	return result
}
