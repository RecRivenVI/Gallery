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
	"github.com/RecRivenVI/gallery/internal/creators"
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

type CanonicalWork struct {
	ID       string
	Title    string
	Creators []CanonicalCreator
	Media    map[string]CanonicalMedia
}

type CanonicalCreator struct {
	ID   string
	Name string
}

type CanonicalMedia struct {
	ID      string
	WorkID  string
	Ordinal int
}

type QueryOverlay struct {
	TitleOverride      string
	ManualTags         []string
	Hidden             bool
	CustomCoverMediaID string
}

type DiscoveredWork struct {
	SourceKey  string
	ProviderID string
	ExternalID string
	Title      string
	Creator    DiscoveredCreator
	MediaKeys  []string
	Media      []DiscoveredMedia
}

type DiscoveredCreator struct {
	SourceKey  string
	ProviderID string
	ExternalID string
	Name       string
}

type DiscoveredMedia struct {
	SourceKey string
	RuleKey   string
	Algorithm string
	Digest    string
	Ordinal   int
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

func (r *Resources) EnsureCanonical(ctx context.Context, sourceID string, discovered []DiscoveredWork) (map[string]CanonicalWork, error) {
	tx, err := r.control.BeginTx(ctx, nil)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	var generation int64
	if err := tx.QueryRowContext(ctx, `UPDATE gallery_binding_sequence SET generation=generation+1
WHERE singleton=1 RETURNING generation`).Scan(&generation); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	now := r.clock.Now().UTC().Unix()
	result := make(map[string]CanonicalWork, len(discovered))
	seenSourceKeys := make(map[string]struct{}, len(discovered))
	seenKeys := make(map[string]struct{}, len(discovered))
	seenExternal := make(map[string]string)
	for _, item := range discovered {
		if item.SourceKey == "" || item.Title == "" || len([]rune(item.SourceKey)) > 4096 || containsControl(item.SourceKey) {
			return nil, fault.WithField(fault.CodeValidation, "sourceKey", nil)
		}
		if len([]rune(item.ProviderID)) > 128 || len([]rune(item.ExternalID)) > 512 ||
			containsControl(item.ProviderID) || containsControl(item.ExternalID) {
			return nil, fault.WithField(fault.CodeValidation, "externalId", nil)
		}
		if _, duplicate := seenSourceKeys[item.SourceKey]; duplicate {
			return nil, r.recordBindingIssue(ctx, tx, bindingIssueInput{
				sourceID: sourceID, entityType: "work", sourceKey: item.SourceKey,
				providerID: item.ProviderID, externalID: item.ExternalID,
				candidateKind: "work", matchSignal: "duplicate_source_key", candidateCount: 2,
			}, now)
		}
		seenSourceKeys[item.SourceKey] = struct{}{}
		seenKeys[item.SourceKey] = struct{}{}
		if item.ExternalID != "" {
			key := item.ProviderID + "\x00" + item.ExternalID
			if previous, duplicate := seenExternal[key]; duplicate && previous != item.SourceKey {
				return nil, r.recordBindingIssue(ctx, tx, bindingIssueInput{
					sourceID: sourceID, entityType: "work", sourceKey: item.SourceKey,
					providerID: item.ProviderID, externalID: item.ExternalID,
					candidateKind: "work", matchSignal: "duplicate_external_id", matchValue: item.ExternalID,
					candidateCount: 2,
				}, now)
			}
			seenExternal[key] = item.SourceKey
		}
		workID, title, err := r.resolveWork(ctx, tx, sourceID, item, generation, now)
		if err != nil {
			return nil, err
		}
		mediaItems := item.Media
		if len(mediaItems) == 0 {
			mediaItems = make([]DiscoveredMedia, len(item.MediaKeys))
			for ordinal, sourceKey := range item.MediaKeys {
				mediaItems[ordinal] = DiscoveredMedia{SourceKey: sourceKey, RuleKey: sourceKey, Ordinal: ordinal}
			}
		}
		work := CanonicalWork{ID: workID, Title: title, Media: make(map[string]CanonicalMedia, len(mediaItems))}
		if item.Creator.Name != "" {
			seenKeys[item.Creator.SourceKey] = struct{}{}
			creatorID, creatorName, err := r.resolveCreator(ctx, tx, sourceID, item.Creator, generation, now)
			if err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO work_creators
(work_id, creator_id, role, ordinal, created_at) VALUES (?, ?, 'primary', 0, ?)
ON CONFLICT(work_id, role, ordinal) DO UPDATE SET creator_id=excluded.creator_id`, workID, creatorID, now); err != nil {
				return nil, fault.New(fault.CodeInternal, true, err)
			}
			work.Creators = append(work.Creators, CanonicalCreator{ID: creatorID, Name: creatorName})
		}
		for ordinal := range mediaItems {
			mediaItem := mediaItems[ordinal]
			if mediaItem.Ordinal < 0 {
				mediaItem.Ordinal = ordinal
			}
			seenKeys[mediaItem.SourceKey] = struct{}{}
			mediaID, canonicalOrdinal, err := r.resolveMedia(ctx, tx, sourceID, workID, item.SourceKey, mediaItem, generation, now)
			if err != nil {
				return nil, err
			}
			work.Media[mediaItem.SourceKey] = CanonicalMedia{ID: mediaID, WorkID: workID, Ordinal: canonicalOrdinal}
		}
		result[item.SourceKey] = work
	}
	if _, err := tx.ExecContext(ctx, `UPDATE work_bindings SET status='orphaned', updated_at=?
WHERE source_id=? AND status='active' AND last_seen_generation<>?`, now, sourceID, generation); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE media_bindings SET status='orphaned', updated_at=?
WHERE source_id=? AND status='active' AND last_seen_generation<>?`, now, sourceID, generation); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE creator_bindings SET status='orphaned', updated_at=?
WHERE source_id=? AND status='active' AND last_seen_generation<>?`, now, sourceID, generation); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	if err := r.reconcileOpenIssues(ctx, tx, sourceID, seenKeys, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

func (r *Resources) resolveWork(ctx context.Context, tx *sql.Tx, sourceID string, item DiscoveredWork, generation, now int64) (string, string, error) {
	blocked, err := stringSet(ctx, tx, `SELECT work_id FROM work_bindings
WHERE source_id=? AND source_key=? AND status='manual_unbound'`, sourceID, item.SourceKey)
	if err != nil {
		return "", "", err
	}
	candidates, err := stringSet(ctx, tx, `SELECT DISTINCT work_id FROM work_bindings
WHERE source_id=? AND source_key=? AND status IN ('active', 'orphaned')`, sourceID, item.SourceKey)
	if err != nil {
		return "", "", err
	}
	removeSet(candidates, blocked)
	matchSignal, matchValue := "source_key", item.SourceKey
	if len(candidates) == 0 && item.ExternalID != "" {
		candidates, err = stringSet(ctx, tx, `SELECT DISTINCT work_id FROM work_bindings
WHERE source_id=? AND provider_id=? AND external_id=? AND status IN ('active', 'orphaned')`,
			sourceID, item.ProviderID, item.ExternalID)
		if err != nil {
			return "", "", err
		}
		removeSet(candidates, blocked)
		matchSignal, matchValue = "external_id", item.ExternalID
	}
	if len(candidates) > 1 {
		return "", "", r.recordBindingIssue(ctx, tx, bindingIssueInput{
			sourceID: sourceID, entityType: "work", sourceKey: item.SourceKey,
			providerID: item.ProviderID, externalID: item.ExternalID,
			candidateIDs: sortedKeys(candidates), candidateKind: "work",
			matchSignal: matchSignal, matchValue: matchValue,
		}, now)
	}
	var workID, title string
	for workID = range candidates {
		if err := tx.QueryRowContext(ctx, "SELECT title FROM canonical_works WHERE work_id=?", workID).Scan(&title); err != nil {
			return "", "", fault.New(fault.CodeInternal, true, err)
		}
	}
	if workID == "" {
		id, err := r.ids.New(domain.IDCanonicalWork)
		if err != nil {
			return "", "", fault.New(fault.CodeInternal, true, err)
		}
		workID, title = id.String(), item.Title
		if _, err := tx.ExecContext(ctx, "INSERT INTO canonical_works (work_id, title, created_at) VALUES (?, ?, ?)", workID, title, now); err != nil {
			return "", "", fault.New(fault.CodeInternal, true, err)
		}
	}
	var bindingID string
	err = tx.QueryRowContext(ctx, `SELECT binding_id FROM work_bindings
WHERE source_id=? AND source_key=? AND work_id=? AND status IN ('active', 'orphaned')
ORDER BY CASE status WHEN 'active' THEN 0 ELSE 1 END, created_at LIMIT 1`, sourceID, item.SourceKey, workID).Scan(&bindingID)
	if errors.Is(err, sql.ErrNoRows) {
		id, idErr := r.ids.New(domain.IDWorkBinding)
		if idErr != nil {
			return "", "", fault.New(fault.CodeInternal, true, idErr)
		}
		bindingID = id.String()
		_, err = tx.ExecContext(ctx, `INSERT INTO work_bindings
(binding_id, source_id, provider_id, external_id, source_key, work_id, identity_version,
 status, last_seen_generation, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, 1, 'active', ?, ?, ?)`, bindingID, sourceID, item.ProviderID,
			item.ExternalID, item.SourceKey, workID, generation, now, now)
	} else if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE work_bindings SET provider_id=?, external_id=?, status='active',
last_seen_generation=?, updated_at=? WHERE binding_id=?`, item.ProviderID, item.ExternalID, generation, now, bindingID)
	}
	if err != nil {
		return "", "", fault.New(fault.CodeInternal, true, err)
	}
	return workID, title, nil
}

func (r *Resources) resolveCreator(ctx context.Context, tx *sql.Tx, sourceID string, item DiscoveredCreator, generation, now int64) (string, string, error) {
	if item.SourceKey == "" || item.Name == "" || len([]rune(item.SourceKey)) > 4096 || containsControl(item.SourceKey) {
		return "", "", fault.WithField(fault.CodeValidation, "creator.sourceKey", nil)
	}
	if len([]rune(item.ProviderID)) > 128 || len([]rune(item.ExternalID)) > 512 ||
		containsControl(item.ProviderID) || containsControl(item.ExternalID) {
		return "", "", fault.WithField(fault.CodeValidation, "creator.externalId", nil)
	}
	blocked, err := stringSet(ctx, tx, `SELECT creator_id FROM creator_bindings
WHERE source_id=? AND source_key=? AND status='manual_unbound'`, sourceID, item.SourceKey)
	if err != nil {
		return "", "", err
	}
	candidates, err := stringSet(ctx, tx, `SELECT DISTINCT creator_id FROM creator_bindings
WHERE source_id=? AND source_key=? AND status IN ('active', 'orphaned')`, sourceID, item.SourceKey)
	if err != nil {
		return "", "", err
	}
	removeSet(candidates, blocked)
	matchSignal, matchValue := "source_key", item.SourceKey
	if len(candidates) == 0 && item.ExternalID != "" {
		candidates, err = stringSet(ctx, tx, `SELECT DISTINCT creator_id FROM creator_bindings
WHERE source_id=? AND provider_id=? AND external_id=? AND status IN ('active', 'orphaned')`,
			sourceID, item.ProviderID, item.ExternalID)
		if err != nil {
			return "", "", err
		}
		removeSet(candidates, blocked)
		matchSignal, matchValue = "external_id", item.ExternalID
	}
	if len(candidates) > 1 {
		return "", "", r.recordBindingIssue(ctx, tx, bindingIssueInput{
			sourceID: sourceID, entityType: "creator", sourceKey: item.SourceKey,
			providerID: item.ProviderID, externalID: item.ExternalID,
			candidateIDs: sortedKeys(candidates), candidateKind: "creator",
			matchSignal: matchSignal, matchValue: matchValue,
		}, now)
	}
	var creatorID, name string
	for creatorID = range candidates {
		if err := tx.QueryRowContext(ctx, "SELECT name FROM canonical_creators WHERE creator_id=?", creatorID).Scan(&name); err != nil {
			return "", "", fault.New(fault.CodeInternal, true, err)
		}
	}
	if creatorID == "" {
		id, err := r.ids.New(domain.IDCanonicalCreator)
		if err != nil {
			return "", "", fault.New(fault.CodeInternal, true, err)
		}
		creatorID, name = id.String(), item.Name
		if _, err := tx.ExecContext(ctx, "INSERT INTO canonical_creators (creator_id, name, created_at) VALUES (?, ?, ?)", creatorID, name, now); err != nil {
			return "", "", fault.New(fault.CodeInternal, true, err)
		}
	}
	var bindingID string
	err = tx.QueryRowContext(ctx, `SELECT binding_id FROM creator_bindings
WHERE source_id=? AND source_key=? AND creator_id=? AND status IN ('active', 'orphaned')
ORDER BY CASE status WHEN 'active' THEN 0 ELSE 1 END, created_at LIMIT 1`, sourceID, item.SourceKey, creatorID).Scan(&bindingID)
	if errors.Is(err, sql.ErrNoRows) {
		id, idErr := r.ids.New(domain.IDCreatorBinding)
		if idErr != nil {
			return "", "", fault.New(fault.CodeInternal, true, idErr)
		}
		bindingID = id.String()
		_, err = tx.ExecContext(ctx, `INSERT INTO creator_bindings
(binding_id, source_id, provider_id, external_id, source_key, creator_id, identity_version,
 status, last_seen_generation, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, 1, 'active', ?, ?, ?)`, bindingID, sourceID, item.ProviderID,
			item.ExternalID, item.SourceKey, creatorID, generation, now, now)
	} else if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE creator_bindings SET provider_id=?, external_id=?, status='active',
last_seen_generation=?, updated_at=? WHERE binding_id=?`, item.ProviderID, item.ExternalID, generation, now, bindingID)
	}
	if err != nil {
		return "", "", fault.New(fault.CodeInternal, true, err)
	}
	return creatorID, name, nil
}

func (r *Resources) resolveMedia(ctx context.Context, tx *sql.Tx, sourceID, workID, workSourceKey string, item DiscoveredMedia, generation, now int64) (string, int, error) {
	if item.SourceKey == "" {
		return "", 0, fault.WithField(fault.CodeValidation, "media.sourceKey", nil)
	}
	blocked, err := stringSet(ctx, tx, `SELECT media_id FROM media_bindings
WHERE source_id=? AND source_key=? AND status='manual_unbound'`, sourceID, item.SourceKey)
	if err != nil {
		return "", 0, err
	}
	candidates, err := stringSet(ctx, tx, `SELECT DISTINCT media_id FROM media_bindings
WHERE source_id=? AND work_id=? AND source_key=? AND status IN ('active', 'orphaned')`, sourceID, workID, item.SourceKey)
	if err != nil {
		return "", 0, err
	}
	removeSet(candidates, blocked)
	matchSignal, matchValue := "source_key", item.SourceKey
	if len(candidates) == 0 && item.RuleKey != "" {
		candidates, err = stringSet(ctx, tx, `SELECT DISTINCT media_id FROM media_bindings
WHERE source_id=? AND work_id=? AND rule_key=? AND status IN ('active', 'orphaned')`, sourceID, workID, item.RuleKey)
		if err != nil {
			return "", 0, err
		}
		removeSet(candidates, blocked)
		matchSignal, matchValue = "rule_key", item.RuleKey
	}
	if len(candidates) == 0 && item.Digest != "" {
		candidates, err = stringSet(ctx, tx, `SELECT DISTINCT media_id FROM media_bindings
WHERE source_id=? AND work_id=? AND algorithm=? AND digest=? AND occurrence_ordinal=?
AND status IN ('active', 'orphaned')`, sourceID, workID, item.Algorithm, item.Digest, item.Ordinal)
		if err != nil {
			return "", 0, err
		}
		removeSet(candidates, blocked)
		matchSignal, matchValue = "blob_digest", item.Algorithm
	}
	if len(candidates) > 1 {
		return "", 0, r.recordBindingIssue(ctx, tx, bindingIssueInput{
			sourceID: sourceID, entityType: "media", sourceKey: item.SourceKey, workSourceKey: workSourceKey,
			candidateIDs: sortedKeys(candidates), candidateKind: "media",
			matchSignal: matchSignal, matchValue: matchValue,
		}, now)
	}
	var mediaID string
	canonicalOrdinal := item.Ordinal
	for mediaID = range candidates {
		if err := tx.QueryRowContext(ctx, "SELECT ordinal FROM canonical_media WHERE media_id=? AND work_id=?", mediaID, workID).Scan(&canonicalOrdinal); err != nil {
			return "", 0, fault.New(fault.CodeInternal, true, err)
		}
	}
	if mediaID == "" {
		id, err := r.ids.New(domain.IDCanonicalMedia)
		if err != nil {
			return "", 0, fault.New(fault.CodeInternal, true, err)
		}
		mediaID = id.String()
		// 手动解绑后重扫会在同一 Work 内为来源媒体建立新 occurrence；旧的孤立 CanonicalMedia
		// 仍占据其 ordinal，因此新 occurrence 取该 Work 内下一个空闲 ordinal，避免唯一约束冲突。
		canonicalOrdinal = item.Ordinal
		var taken int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM canonical_media WHERE work_id=? AND ordinal=?", workID, item.Ordinal).Scan(&taken); err != nil {
			return "", 0, fault.New(fault.CodeInternal, true, err)
		}
		if taken > 0 {
			if err := tx.QueryRowContext(ctx, "SELECT COALESCE(max(ordinal), -1)+1 FROM canonical_media WHERE work_id=?", workID).Scan(&canonicalOrdinal); err != nil {
				return "", 0, fault.New(fault.CodeInternal, true, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO canonical_media
(media_id, work_id, role, ordinal, created_at) VALUES (?, ?, 'content', ?, ?)`, mediaID, workID, canonicalOrdinal, now); err != nil {
			return "", 0, fault.New(fault.CodeInternal, true, err)
		}
	}
	var bindingID string
	err = tx.QueryRowContext(ctx, `SELECT binding_id FROM media_bindings
WHERE source_id=? AND source_key=? AND media_id=? AND status IN ('active', 'orphaned')
ORDER BY CASE status WHEN 'active' THEN 0 ELSE 1 END, created_at LIMIT 1`, sourceID, item.SourceKey, mediaID).Scan(&bindingID)
	if errors.Is(err, sql.ErrNoRows) {
		id, idErr := r.ids.New(domain.IDMediaBinding)
		if idErr != nil {
			return "", 0, fault.New(fault.CodeInternal, true, idErr)
		}
		bindingID = id.String()
		_, err = tx.ExecContext(ctx, `INSERT INTO media_bindings
(binding_id, source_id, source_key, rule_key, media_id, work_id, algorithm, digest,
 occurrence_ordinal, identity_version, status, last_seen_generation, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 'active', ?, ?, ?)`, bindingID, sourceID, item.SourceKey,
			item.RuleKey, mediaID, workID, item.Algorithm, item.Digest, item.Ordinal, generation, now, now)
	} else if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE media_bindings SET rule_key=?, algorithm=?, digest=?,
occurrence_ordinal=?, status='active', last_seen_generation=?, updated_at=? WHERE binding_id=?`,
			item.RuleKey, item.Algorithm, item.Digest, item.Ordinal, generation, now, bindingID)
	}
	if err != nil {
		return "", 0, fault.New(fault.CodeInternal, true, err)
	}
	return mediaID, canonicalOrdinal, nil
}

func (r *Resources) ManualUnbindWork(ctx context.Context, sourceID, sourceKey string) (string, error) {
	tx, err := r.control.BeginTx(ctx, nil)
	if err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	var workID string
	if err := tx.QueryRowContext(ctx, `SELECT work_id FROM work_bindings
WHERE source_id=? AND source_key=? AND status='active'`, sourceID, sourceKey).Scan(&workID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fault.New(fault.CodeNotFound, false, nil)
		}
		return "", fault.New(fault.CodeInternal, true, err)
	}
	now := r.clock.Now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE work_bindings SET status='manual_unbound', updated_at=?
WHERE source_id=? AND source_key=? AND status='active'`, now, sourceID, sourceKey); err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE media_bindings SET status='manual_unbound', updated_at=?
WHERE source_id=? AND work_id=? AND status='active'`, now, sourceID, workID); err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	return workID, nil
}

func stringSet(ctx context.Context, tx *sql.Tx, query string, args ...any) (map[string]struct{}, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	result := make(map[string]struct{})
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result[value] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

func removeSet(target, removed map[string]struct{}) {
	for value := range removed {
		delete(target, value)
	}
}

func containsControl(value string) bool {
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return true
		}
	}
	return false
}

// QueryOverlaySnapshot 从同一个 control.db 读事务取得 watermark、Overlay 事实与
// 创作者合并映射，避免把尚未进入快照的并发写错误标记为已投影，并让扫描应用与该
// watermark 一致的合并集合。
func (r *Resources) QueryOverlaySnapshot(ctx context.Context, workIDs []string) (map[string]QueryOverlay, []domain.CreatorMergePair, int64, error) {
	tx, err := r.control.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, 0, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	var watermark int64
	if err := tx.QueryRowContext(ctx, "SELECT watermark FROM gallery_control_sequence WHERE singleton=1").Scan(&watermark); err != nil {
		return nil, nil, 0, fault.New(fault.CodeInternal, true, err)
	}
	merges, err := creators.ReadMergePairs(ctx, tx)
	if err != nil {
		return nil, nil, 0, err
	}
	wanted := make(map[string]struct{}, len(workIDs))
	for _, id := range workIDs {
		wanted[id] = struct{}{}
	}
	rows, err := tx.QueryContext(ctx, `SELECT work_id, title_override, manual_tags_json, hidden, custom_cover_media_id
FROM work_overlays WHERE query_watermark<=? ORDER BY work_id`, watermark)
	if err != nil {
		return nil, nil, 0, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	result := make(map[string]QueryOverlay)
	for rows.Next() {
		var id, tagsJSON string
		var title string
		var hidden int
		var cover sql.NullString
		if err := rows.Scan(&id, &title, &tagsJSON, &hidden, &cover); err != nil {
			return nil, nil, 0, fault.New(fault.CodeInternal, true, err)
		}
		if len(wanted) > 0 {
			if _, ok := wanted[id]; !ok {
				continue
			}
		}
		var tags []string
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			return nil, nil, 0, fault.New(fault.CodeInternal, false, err)
		}
		result[id] = QueryOverlay{TitleOverride: title, ManualTags: tags, Hidden: hidden != 0, CustomCoverMediaID: cover.String}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, 0, fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, 0, fault.New(fault.CodeInternal, true, err)
	}
	return result, merges, watermark, nil
}

func (r *Resources) MarkOverlaySnapshotPublished(ctx context.Context, watermark int64, publicationID string) error {
	if watermark < 0 || publicationID == "" {
		return fault.New(fault.CodeValidation, false, nil)
	}
	_, err := r.control.ExecContext(ctx, `UPDATE work_overlays SET projection_status='published',
projected_watermark=query_watermark, published_query_publication_id=?, issue_code=NULL, updated_at=?
WHERE query_watermark<=?`, publicationID, r.clock.Now().UTC().Unix(), watermark)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func (r *Resources) TempRoot() string { return r.dirs.Temp }

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
