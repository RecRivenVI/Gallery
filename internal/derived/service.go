package derived

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/ports"
	"github.com/RecRivenVI/gallery/internal/rules"
)

const (
	ManifestSchemaVersion = 1
	ReadLeaseDuration     = 5 * time.Minute
)

type Request struct {
	Blob             domain.ContentBlobRef
	TransformID      string
	TransformVersion string
	Parameters       []byte
	OverlayInputHash string
}

type Asset struct {
	Key              string
	Blob             domain.ContentBlobRef
	TransformID      string
	TransformVersion string
	ParametersHash   string
	OverlayInputHash string
	RelativePath     string
	OutputDigest     string
	OutputSize       int64
	OutputMIME       string
	TakenOver        bool
}

type Generator func(context.Context, io.Writer) (mimeType string, err error)

type manifest struct {
	SchemaVersion    int    `json:"schemaVersion"`
	AssetKey         string `json:"assetKey"`
	BlobAlgorithm    string `json:"blobAlgorithm"`
	BlobDigest       string `json:"blobDigest"`
	TransformID      string `json:"transformId"`
	TransformVersion string `json:"transformVersion"`
	ParametersHash   string `json:"parametersHash"`
	OverlayInputHash string `json:"overlayInputHash"`
	OutputDigest     string `json:"outputDigest"`
	OutputSize       int64  `json:"outputSize"`
	OutputMIME       string `json:"outputMime"`
}

type keySpec struct {
	Key              string
	Blob             domain.ContentBlobRef
	TransformID      string
	TransformVersion string
	ParametersHash   string
	OverlayInputHash string
	RelativePath     string
}

type flight struct {
	done  chan struct{}
	asset Asset
	err   error
}

type Service struct {
	db        *sql.DB
	cacheRoot string
	clock     ports.Clock
	random    io.Reader
	mu        sync.Mutex
	flights   map[string]*flight
}

func New(db *sql.DB, cacheRoot string, clock ports.Clock, random io.Reader) (*Service, error) {
	if db == nil || cacheRoot == "" || clock == nil {
		return nil, fmt.Errorf("DerivedAsset Service 缺少依赖")
	}
	if random == nil {
		random = rand.Reader
	}
	root := filepath.Join(cacheRoot, "derived")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fault.New(fault.CodeDerivedAssetFailed, true, err)
	}
	return &Service{db: db, cacheRoot: filepath.Clean(cacheRoot), clock: clock, random: random, flights: make(map[string]*flight)}, nil
}

func OverlayInputHash(input []byte) (string, error) {
	if len(input) == 0 {
		input = []byte("{}")
	}
	canonical, err := rules.CanonicalJSON(input)
	if err != nil {
		return "", fault.New(fault.CodeDerivedAssetInvalid, false, err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Service) GetOrCreate(ctx context.Context, request Request, generator Generator) (Asset, error) {
	spec, err := computeKey(request)
	if err != nil {
		return Asset{}, err
	}
	if asset, ok, err := s.registered(ctx, spec); err != nil || ok {
		return asset, err
	}
	s.mu.Lock()
	if existing := s.flights[spec.Key]; existing != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return Asset{}, fault.New(fault.CodeDerivedAssetFailed, true, ctx.Err())
		case <-existing.done:
			return existing.asset, existing.err
		}
	}
	current := &flight{done: make(chan struct{})}
	s.flights[spec.Key] = current
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.flights, spec.Key)
		close(current.done)
		s.mu.Unlock()
	}()
	if asset, ok, err := s.takeOver(ctx, spec); err != nil {
		current.err = err
		return Asset{}, err
	} else if ok {
		current.asset = asset
		return asset, nil
	}
	if generator == nil {
		current.err = fault.New(fault.CodeDerivedAssetFailed, true, nil)
		return Asset{}, current.err
	}
	asset, err := s.generate(ctx, spec, generator)
	current.asset, current.err = asset, err
	return asset, err
}

func (s *Service) registered(ctx context.Context, spec keySpec) (Asset, bool, error) {
	var asset Asset
	var algorithm, digest, status string
	err := s.db.QueryRowContext(ctx, `SELECT asset_key, blob_algorithm, blob_digest, transform_id,
transform_version, parameters_hash, overlay_input_hash, relative_path, output_digest, output_size,
output_mime, status FROM derived_assets WHERE asset_key=?`, spec.Key).Scan(&asset.Key, &algorithm,
		&digest, &asset.TransformID, &asset.TransformVersion, &asset.ParametersHash,
		&asset.OverlayInputHash, &asset.RelativePath, &asset.OutputDigest, &asset.OutputSize,
		&asset.OutputMIME, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return Asset{}, false, nil
	}
	if err != nil {
		return Asset{}, false, fault.New(fault.CodeInternal, true, err)
	}
	if status != "ready" {
		return Asset{}, false, nil
	}
	asset.Blob = domain.ContentBlobRef{Algorithm: algorithm, Digest: digest}
	if asset.TransformID != spec.TransformID || asset.TransformVersion != spec.TransformVersion ||
		asset.ParametersHash != spec.ParametersHash || asset.OverlayInputHash != spec.OverlayInputHash ||
		asset.RelativePath != spec.RelativePath {
		return Asset{}, false, fault.New(fault.CodeDerivedAssetInvalid, false, nil)
	}
	manifestValue, manifestErr := readManifest(filepath.Join(s.directory(spec.Key), "manifest.json"))
	if manifestErr != nil || !manifestMatches(manifestValue, spec) ||
		manifestValue.OutputDigest != asset.OutputDigest || manifestValue.OutputSize != asset.OutputSize ||
		manifestValue.OutputMIME != asset.OutputMIME {
		return Asset{}, false, nil
	}
	if info, err := os.Stat(s.absolute(asset.RelativePath)); err != nil || !info.Mode().IsRegular() || info.Size() != asset.OutputSize {
		return Asset{}, false, nil
	}
	_, _ = s.db.ExecContext(ctx, "UPDATE derived_assets SET last_accessed_at=? WHERE asset_key=?", s.clock.Now().UTC().Unix(), spec.Key)
	return asset, true, nil
}

func (s *Service) generate(ctx context.Context, spec keySpec, generator Generator) (Asset, error) {
	now := s.clock.Now().UTC().Unix()
	_, err := s.db.ExecContext(ctx, `INSERT INTO derived_assets
(asset_key, blob_algorithm, blob_digest, transform_id, transform_version, parameters_hash,
 overlay_input_hash, status, relative_path, created_at, updated_at, last_accessed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, 'generating', ?, ?, ?, ?)
ON CONFLICT(asset_key) DO UPDATE SET status='generating', updated_at=excluded.updated_at`, spec.Key,
		spec.Blob.Algorithm, spec.Blob.Digest, spec.TransformID, spec.TransformVersion,
		spec.ParametersHash, spec.OverlayInputHash, spec.RelativePath, now, now, now)
	if err != nil {
		return Asset{}, fault.New(fault.CodeInternal, true, err)
	}
	directory := s.directory(spec.Key)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return Asset{}, s.fail(ctx, spec.Key, err)
	}
	s.removeCorrupt(spec.Key)
	temporary, err := os.CreateTemp(directory, "asset-*.tmp")
	if err != nil {
		return Asset{}, s.fail(ctx, spec.Key, err)
	}
	cleanup := func() { _ = temporary.Close(); _ = os.Remove(temporary.Name()) }
	if err := temporary.Chmod(0o600); err != nil {
		cleanup()
		return Asset{}, s.fail(ctx, spec.Key, err)
	}
	hasher := sha256.New()
	counter := &countWriter{writer: io.MultiWriter(temporary, hasher)}
	mimeType, generateErr := generator(ctx, counter)
	if generateErr != nil || mimeType == "" {
		cleanup()
		if generateErr == nil {
			generateErr = fmt.Errorf("派生生成器未返回 MIME")
		}
		return Asset{}, s.fail(ctx, spec.Key, generateErr)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return Asset{}, s.fail(ctx, spec.Key, err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporary.Name())
		return Asset{}, s.fail(ctx, spec.Key, err)
	}
	outputDigest := hex.EncodeToString(hasher.Sum(nil))
	assetPath := s.absolute(spec.RelativePath)
	_ = os.Remove(assetPath)
	if err := os.Rename(temporary.Name(), assetPath); err != nil {
		_ = os.Remove(temporary.Name())
		return Asset{}, s.fail(ctx, spec.Key, err)
	}
	manifestValue := manifest{SchemaVersion: ManifestSchemaVersion, AssetKey: spec.Key,
		BlobAlgorithm: spec.Blob.Algorithm, BlobDigest: spec.Blob.Digest, TransformID: spec.TransformID,
		TransformVersion: spec.TransformVersion, ParametersHash: spec.ParametersHash,
		OverlayInputHash: spec.OverlayInputHash, OutputDigest: outputDigest,
		OutputSize: counter.count, OutputMIME: mimeType}
	if err := writeManifest(directory, manifestValue); err != nil {
		_ = os.Remove(assetPath)
		return Asset{}, s.fail(ctx, spec.Key, err)
	}
	_, err = s.db.ExecContext(ctx, `UPDATE derived_assets SET status='ready', output_digest=?,
output_size=?, output_mime=?, updated_at=?, last_accessed_at=? WHERE asset_key=?`, outputDigest,
		counter.count, mimeType, now, now, spec.Key)
	if err != nil {
		// 文件和 manifest 已完整发布；重启后可由 takeover 恢复登记。
		return Asset{}, fault.New(fault.CodeInternal, true, err)
	}
	return assetFrom(spec, manifestValue, false), nil
}

func (s *Service) takeOver(ctx context.Context, spec keySpec) (Asset, bool, error) {
	value, err := readManifest(filepath.Join(s.directory(spec.Key), "manifest.json"))
	if errors.Is(err, os.ErrNotExist) {
		return Asset{}, false, nil
	}
	if err != nil || !manifestMatches(value, spec) {
		s.removeCorrupt(spec.Key)
		return Asset{}, false, nil
	}
	file, err := os.Open(s.absolute(spec.RelativePath))
	if err != nil {
		s.removeCorrupt(spec.Key)
		return Asset{}, false, nil
	}
	hasher := sha256.New()
	size, copyErr := io.Copy(hasher, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || size != value.OutputSize || hex.EncodeToString(hasher.Sum(nil)) != value.OutputDigest {
		s.removeCorrupt(spec.Key)
		return Asset{}, false, nil
	}
	now := s.clock.Now().UTC().Unix()
	_, err = s.db.ExecContext(ctx, `INSERT INTO derived_assets
(asset_key, blob_algorithm, blob_digest, transform_id, transform_version, parameters_hash,
 overlay_input_hash, status, relative_path, output_digest, output_size, output_mime,
 created_at, updated_at, last_accessed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, 'ready', ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(asset_key) DO UPDATE SET status='ready', relative_path=excluded.relative_path,
output_digest=excluded.output_digest, output_size=excluded.output_size, output_mime=excluded.output_mime,
updated_at=excluded.updated_at, last_accessed_at=excluded.last_accessed_at`, spec.Key,
		spec.Blob.Algorithm, spec.Blob.Digest, spec.TransformID, spec.TransformVersion,
		spec.ParametersHash, spec.OverlayInputHash, spec.RelativePath, value.OutputDigest,
		value.OutputSize, value.OutputMIME, now, now, now)
	if err != nil {
		return Asset{}, false, fault.New(fault.CodeInternal, true, err)
	}
	return assetFrom(spec, value, true), true, nil
}

type Lease struct {
	File   *os.File
	Asset  Asset
	db     *sql.DB
	id     string
	closed sync.Once
}

// InputBlob 返回已就绪 DerivedAsset 的稳定输入 Blob，供传输层在建立文件读取租约前完成
// 资源授权。它不打开缓存文件、不刷新状态，也不创建 lease。
func (s *Service) InputBlob(ctx context.Context, assetKey string) (domain.ContentBlobRef, error) {
	if !isSHA256(assetKey) {
		return domain.ContentBlobRef{}, fault.New(fault.CodeDerivedAssetInvalid, false, nil)
	}
	var blob domain.ContentBlobRef
	var status string
	err := s.db.QueryRowContext(ctx,
		"SELECT blob_algorithm, blob_digest, status FROM derived_assets WHERE asset_key=?", assetKey).
		Scan(&blob.Algorithm, &blob.Digest, &status)
	if errors.Is(err, sql.ErrNoRows) || status != "ready" {
		return domain.ContentBlobRef{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return domain.ContentBlobRef{}, fault.New(fault.CodeInternal, true, err)
	}
	return blob, nil
}

func (s *Service) Open(ctx context.Context, assetKey string) (*Lease, error) {
	if !isSHA256(assetKey) {
		return nil, fault.New(fault.CodeDerivedAssetInvalid, false, nil)
	}
	var spec keySpec
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT asset_key, blob_algorithm, blob_digest, transform_id,
transform_version, parameters_hash, overlay_input_hash, relative_path, status
FROM derived_assets WHERE asset_key=?`, assetKey).Scan(&spec.Key, &spec.Blob.Algorithm,
		&spec.Blob.Digest, &spec.TransformID, &spec.TransformVersion, &spec.ParametersHash,
		&spec.OverlayInputHash, &spec.RelativePath, &status)
	if errors.Is(err, sql.ErrNoRows) || status != "ready" {
		return nil, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	asset, ok, err := s.registered(ctx, spec)
	if err != nil || !ok {
		if err == nil {
			err = fault.New(fault.CodeDerivedAssetInvalid, false, nil)
		}
		return nil, err
	}
	leaseID, err := randomID(s.random)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	now := s.clock.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO derived_asset_leases
(lease_id, asset_key, expires_at, created_at) VALUES (?, ?, ?, ?)`, leaseID, assetKey,
		now.Add(ReadLeaseDuration).Unix(), now.Unix()); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	file, err := os.Open(s.absolute(asset.RelativePath))
	if err != nil {
		_, _ = s.db.ExecContext(ctx, "DELETE FROM derived_asset_leases WHERE lease_id=?", leaseID)
		return nil, fault.New(fault.CodeDerivedAssetFailed, true, err)
	}
	return &Lease{File: file, Asset: asset, db: s.db, id: leaseID}, nil
}

func (l *Lease) Close() error {
	if l == nil {
		return nil
	}
	var result error
	l.closed.Do(func() {
		if l.File != nil {
			result = l.File.Close()
		}
		if l.db != nil && l.id != "" {
			_, deleteErr := l.db.Exec("DELETE FROM derived_asset_leases WHERE lease_id=?", l.id)
			if result == nil {
				result = deleteErr
			}
		}
	})
	return result
}

func (s *Service) MarkObsolete(ctx context.Context, assetKey string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE derived_assets SET status='obsolete', updated_at=?
WHERE asset_key=? AND status='ready'`, s.clock.Now().UTC().Unix(), assetKey)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return fault.New(fault.CodeNotFound, false, nil)
	}
	return nil
}

func (s *Service) SweepObsolete(ctx context.Context, olderThan time.Time) (int, error) {
	now := s.clock.Now().UTC().Unix()
	if _, err := s.db.ExecContext(ctx, "DELETE FROM derived_asset_leases WHERE expires_at<=?", now); err != nil {
		return 0, fault.New(fault.CodeInternal, true, err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.asset_key FROM derived_assets a
WHERE a.status='obsolete' AND a.pinned=0 AND a.updated_at<?
AND NOT EXISTS (SELECT 1 FROM derived_asset_leases l WHERE l.asset_key=a.asset_key AND l.expires_at>?)
ORDER BY a.asset_key`, olderThan.UTC().Unix(), now)
	if err != nil {
		return 0, fault.New(fault.CodeInternal, true, err)
	}
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return 0, fault.New(fault.CodeInternal, true, err)
		}
		keys = append(keys, key)
	}
	if err := rows.Close(); err != nil {
		return 0, fault.New(fault.CodeInternal, true, err)
	}
	removed := 0
	for _, key := range keys {
		if err := os.RemoveAll(s.directory(key)); err != nil {
			return removed, fault.New(fault.CodeDerivedAssetFailed, true, err)
		}
		if _, err := s.db.ExecContext(ctx, "DELETE FROM derived_assets WHERE asset_key=? AND status='obsolete'", key); err != nil {
			return removed, fault.New(fault.CodeInternal, true, err)
		}
		removed++
	}
	return removed, nil
}

func (s *Service) Reconcile(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT asset_key, blob_algorithm, blob_digest, transform_id,
transform_version, parameters_hash, overlay_input_hash, relative_path FROM derived_assets
WHERE status='generating' ORDER BY asset_key`)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	var specs []keySpec
	for rows.Next() {
		var item keySpec
		if err := rows.Scan(&item.Key, &item.Blob.Algorithm, &item.Blob.Digest, &item.TransformID,
			&item.TransformVersion, &item.ParametersHash, &item.OverlayInputHash, &item.RelativePath); err != nil {
			rows.Close()
			return fault.New(fault.CodeInternal, true, err)
		}
		specs = append(specs, item)
	}
	_ = rows.Close()
	for _, spec := range specs {
		if _, ok, err := s.takeOver(ctx, spec); err != nil {
			return err
		} else if !ok {
			s.removeTemporary(spec.Key)
			if _, err := s.db.ExecContext(ctx, `UPDATE derived_assets SET status='failed', updated_at=?
WHERE asset_key=? AND status='generating'`, s.clock.Now().UTC().Unix(), spec.Key); err != nil {
				return fault.New(fault.CodeInternal, true, err)
			}
		}
	}
	return nil
}

func computeKey(request Request) (keySpec, error) {
	blob, err := domain.ParseContentBlobRef(request.Blob.Algorithm, request.Blob.Digest)
	if err != nil || request.TransformID == "" || request.TransformVersion == "" ||
		len(request.TransformID) > 128 || len(request.TransformVersion) > 128 ||
		strings.ContainsAny(request.TransformID+request.TransformVersion, "\x00\r\n") {
		return keySpec{}, fault.New(fault.CodeDerivedAssetInvalid, false, err)
	}
	parameters := request.Parameters
	if len(parameters) == 0 {
		parameters = []byte("{}")
	}
	canonical, err := rules.CanonicalJSON(parameters)
	if err != nil {
		return keySpec{}, fault.New(fault.CodeDerivedAssetInvalid, false, err)
	}
	parameterSum := sha256.Sum256(canonical)
	parametersHash := hex.EncodeToString(parameterSum[:])
	overlayHash := request.OverlayInputHash
	if overlayHash == "" {
		overlayHash = strings.Repeat("0", sha256.Size*2)
	}
	if !isSHA256(overlayHash) {
		return keySpec{}, fault.New(fault.CodeDerivedAssetInvalid, false, nil)
	}
	identity := struct {
		BlobAlgorithm    string `json:"blobAlgorithm"`
		BlobDigest       string `json:"blobDigest"`
		TransformID      string `json:"transformId"`
		TransformVersion string `json:"transformVersion"`
		ParametersHash   string `json:"parametersHash"`
		OverlayInputHash string `json:"overlayInputHash"`
	}{blob.Algorithm, blob.Digest, request.TransformID, request.TransformVersion, parametersHash, overlayHash}
	encoded, _ := json.Marshal(identity)
	keySum := sha256.Sum256(encoded)
	key := hex.EncodeToString(keySum[:])
	return keySpec{Key: key, Blob: blob, TransformID: request.TransformID,
		TransformVersion: request.TransformVersion, ParametersHash: parametersHash,
		OverlayInputHash: overlayHash, RelativePath: filepath.ToSlash(filepath.Join("derived", key[:2], key, "asset.bin"))}, nil
}

func (s *Service) fail(ctx context.Context, key string, cause error) error {
	_, _ = s.db.ExecContext(ctx, `UPDATE derived_assets SET status='failed', updated_at=? WHERE asset_key=?`, s.clock.Now().UTC().Unix(), key)
	return fault.New(fault.CodeDerivedAssetFailed, true, cause)
}

func (s *Service) directory(key string) string {
	return filepath.Join(s.cacheRoot, "derived", key[:2], key)
}

func (s *Service) absolute(relative string) string {
	return filepath.Join(s.cacheRoot, filepath.FromSlash(relative))
}

func (s *Service) removeCorrupt(key string) {
	directory := s.directory(key)
	_ = os.Remove(filepath.Join(directory, "asset.bin"))
	_ = os.Remove(filepath.Join(directory, "manifest.json"))
	s.removeTemporary(key)
}

func (s *Service) removeTemporary(key string) {
	items, _ := filepath.Glob(filepath.Join(s.directory(key), "*.tmp"))
	for _, item := range items {
		_ = os.Remove(item)
	}
}

func writeManifest(directory string, value manifest) error {
	temporary, err := os.CreateTemp(directory, "manifest-*.tmp")
	if err != nil {
		return err
	}
	cleanup := func() { _ = temporary.Close(); _ = os.Remove(temporary.Name()) }
	encoder := json.NewEncoder(temporary)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporary.Name())
		return err
	}
	target := filepath.Join(directory, "manifest.json")
	_ = os.Remove(target)
	if err := os.Rename(temporary.Name(), target); err != nil {
		_ = os.Remove(temporary.Name())
		return err
	}
	return nil
}

func readManifest(path string) (manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return manifest{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	var value manifest
	if err := decoder.Decode(&value); err != nil {
		return manifest{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return manifest{}, fmt.Errorf("manifest 包含额外 JSON")
	}
	return value, nil
}

func manifestMatches(value manifest, spec keySpec) bool {
	return value.SchemaVersion == ManifestSchemaVersion && value.AssetKey == spec.Key &&
		value.BlobAlgorithm == spec.Blob.Algorithm && value.BlobDigest == spec.Blob.Digest &&
		value.TransformID == spec.TransformID && value.TransformVersion == spec.TransformVersion &&
		value.ParametersHash == spec.ParametersHash && value.OverlayInputHash == spec.OverlayInputHash &&
		isSHA256(value.OutputDigest) && value.OutputSize >= 0 && value.OutputMIME != ""
}

func assetFrom(spec keySpec, value manifest, takenOver bool) Asset {
	return Asset{Key: spec.Key, Blob: spec.Blob, TransformID: spec.TransformID,
		TransformVersion: spec.TransformVersion, ParametersHash: spec.ParametersHash,
		OverlayInputHash: spec.OverlayInputHash, RelativePath: spec.RelativePath,
		OutputDigest: value.OutputDigest, OutputSize: value.OutputSize,
		OutputMIME: value.OutputMIME, TakenOver: takenOver}
}

func isSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

func randomID(reader io.Reader) (string, error) {
	buffer := make([]byte, 16)
	if _, err := io.ReadFull(reader, buffer); err != nil {
		return "", err
	}
	return "dlease_" + hex.EncodeToString(buffer), nil
}

type countWriter struct {
	writer io.Writer
	count  int64
}

func (w *countWriter) Write(value []byte) (int, error) {
	written, err := w.writer.Write(value)
	w.count += int64(written)
	return written, err
}
