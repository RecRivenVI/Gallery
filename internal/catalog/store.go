package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/ports"
	"github.com/RecRivenVI/gallery/internal/querytext"
)

type Candidate struct {
	CatalogRevisionID string
	OverlayRevisionID string
	JobID             string
	SourceID          string
	ControlWatermark  int64
}

type OverlayCandidate struct {
	CatalogRevisionID     string
	BaseOverlayRevisionID string
	OverlayRevisionID     string
	JobID                 string
	ControlWatermark      int64
}

type OverlayFact struct {
	TitleOverride      string
	ManualTags         []string
	Hidden             bool
	CustomCoverMediaID string
}

type WorkFact struct {
	SourceID           string
	LibraryID          string
	SourceKey          string
	SourceTitle        string
	SourceTags         []string
	Title              string
	Creator            string
	Tags               []string
	Filenames          []string
	Hidden             bool
	CustomCoverMediaID string
	WorkID             string
}

type MediaFact struct {
	SourceID      string
	SourceKey     string
	WorkSourceKey string
	RelativePath  string
	Kind          string
	MIME          string
	Size          int64
	Algorithm     string
	Digest        string
	LocationKey   string
	MediaID       string
	WorkID        string
	Ordinal       int
}

type Publication struct {
	ID                string
	CatalogRevisionID string
	OverlayRevisionID string
	JobID             string
	ControlWatermark  int64
	CreatedAt         time.Time
}

type Work struct {
	ID         string
	Title      string
	Creator    string
	Tags       []string
	MediaCount int
}

type Media struct {
	ID             string
	WorkID         string
	SourceID       string
	Kind           string
	MIME           string
	Size           int64
	Algorithm      string
	Digest         string
	LocationStatus string
	Ordinal        int
	RelativePath   string
}

type Store struct {
	db    *sql.DB
	clock ports.Clock
	ids   ports.IDGenerator
}

func NewStore(db *sql.DB, clock ports.Clock, ids ports.IDGenerator) (*Store, error) {
	if db == nil || clock == nil || ids == nil {
		return nil, fmt.Errorf("Catalog Store 缺少依赖")
	}
	return &Store{db: db, clock: clock, ids: ids}, nil
}

func (s *Store) BeginCandidate(ctx context.Context, jobID, sourceID string, controlWatermark int64) (Candidate, error) {
	catalogID, err := s.ids.New(domain.IDCatalogRevision)
	if err != nil {
		return Candidate{}, fault.New(fault.CodeInternal, true, err)
	}
	overlayID, err := s.ids.New(domain.IDOverlayRevision)
	if err != nil {
		return Candidate{}, fault.New(fault.CodeInternal, true, err)
	}
	candidate := Candidate{CatalogRevisionID: catalogID.String(), OverlayRevisionID: overlayID.String(), JobID: jobID, SourceID: sourceID, ControlWatermark: controlWatermark}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Candidate{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	now := s.clock.Now().Unix()
	if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_revisions
(catalog_revision_id, job_id, source_id, status, created_at) VALUES (?, ?, ?, 'staging', ?)`, candidate.CatalogRevisionID, jobID, sourceID, now); err != nil {
		return Candidate{}, fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at) VALUES (?, ?, ?, 'staging', ?)`, candidate.OverlayRevisionID, candidate.CatalogRevisionID, controlWatermark, now); err != nil {
		return Candidate{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := cloneUnchangedSources(ctx, tx, candidate); err != nil {
		return Candidate{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return Candidate{}, fault.New(fault.CodeInternal, true, err)
	}
	return candidate, nil
}

func (s *Store) Stage(ctx context.Context, candidate Candidate, works []WorkFact, media []MediaFact) error {
	for start := 0; start < len(works); start += 100 {
		end := min(start+100, len(works))
		if err := s.stageWorks(ctx, candidate, works[start:end]); err != nil {
			return err
		}
	}
	for start := 0; start < len(media); start += 100 {
		end := min(start+100, len(media))
		if err := s.stageMedia(ctx, candidate, media[start:end]); err != nil {
			return err
		}
	}
	for _, work := range works {
		if work.CustomCoverMediaID == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE media_projections SET ordinal=-1
WHERE catalog_revision_id=? AND overlay_revision_id=? AND work_id=? AND media_id=?`,
			candidate.CatalogRevisionID, candidate.OverlayRevisionID, work.WorkID, work.CustomCoverMediaID); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
	}
	return nil
}

func (s *Store) ValidateCandidate(ctx context.Context, candidate Candidate) error {
	var workCount, mediaCount, invalidBlob, searchCount int
	if err := s.db.QueryRowContext(ctx, `SELECT
  (SELECT count(*) FROM source_works WHERE catalog_revision_id = ? AND source_id = ?),
  (SELECT count(*) FROM source_media WHERE catalog_revision_id = ? AND source_id = ?),
  (SELECT count(*) FROM content_blobs WHERE catalog_revision_id = ? AND (algorithm <> 'sha256-v1' OR length(digest) <> 64)),
  (SELECT count(*) FROM work_search WHERE catalog_revision_id = ? AND overlay_revision_id = ?)`,
		candidate.CatalogRevisionID, candidate.SourceID, candidate.CatalogRevisionID, candidate.SourceID, candidate.CatalogRevisionID,
		candidate.CatalogRevisionID, candidate.OverlayRevisionID,
	).Scan(&workCount, &mediaCount, &invalidBlob, &searchCount); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if workCount == 0 || mediaCount == 0 || invalidBlob != 0 || searchCount < workCount {
		return fault.New(fault.CodeCatalogCandidateInvalid, false, nil)
	}
	var orphan int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM media_projections m
LEFT JOIN work_projections w ON w.catalog_revision_id = m.catalog_revision_id
 AND w.overlay_revision_id = m.overlay_revision_id AND w.work_id = m.work_id
WHERE m.catalog_revision_id = ? AND w.work_id IS NULL`, candidate.CatalogRevisionID).Scan(&orphan); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if orphan != 0 {
		return fault.New(fault.CodeCatalogCandidateInvalid, false, nil)
	}
	return nil
}

func (s *Store) Publish(ctx context.Context, candidate Candidate) (Publication, error) {
	publicationID, err := s.ids.New(domain.IDQueryPublication)
	if err != nil {
		return Publication{}, fault.New(fault.CodeInternal, true, err)
	}
	publication := Publication{ID: publicationID.String(), CatalogRevisionID: candidate.CatalogRevisionID, OverlayRevisionID: candidate.OverlayRevisionID, JobID: candidate.JobID, ControlWatermark: candidate.ControlWatermark, CreatedAt: s.clock.Now().UTC()}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Publication{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	var catalogStatus, overlayStatus string
	if err := tx.QueryRowContext(ctx, `SELECT c.status, o.status FROM catalog_revisions c
JOIN overlay_projection_revisions o ON o.catalog_revision_id = c.catalog_revision_id
WHERE c.catalog_revision_id = ? AND o.overlay_revision_id = ?`, candidate.CatalogRevisionID, candidate.OverlayRevisionID).Scan(&catalogStatus, &overlayStatus); err != nil {
		return Publication{}, fault.New(fault.CodeCatalogCandidateInvalid, false, err)
	}
	if catalogStatus != "staging" || overlayStatus != "staging" {
		return Publication{}, fault.New(fault.CodeCatalogCandidateInvalid, false, nil)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO query_publications
(query_publication_id, catalog_revision_id, overlay_revision_id, job_id, control_watermark, created_at)
VALUES (?, ?, ?, ?, ?, ?)`, publication.ID, publication.CatalogRevisionID, publication.OverlayRevisionID, publication.JobID, publication.ControlWatermark, publication.CreatedAt.Unix()); err != nil {
		return Publication{}, fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE catalog_revisions SET status = 'published', published_at = ? WHERE catalog_revision_id = ?", publication.CreatedAt.Unix(), publication.CatalogRevisionID); err != nil {
		return Publication{}, fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE overlay_projection_revisions SET status = 'published', published_at = ? WHERE overlay_revision_id = ?", publication.CreatedAt.Unix(), publication.OverlayRevisionID); err != nil {
		return Publication{}, fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO active_query_publication (singleton, query_publication_id) VALUES (1, ?)
ON CONFLICT(singleton) DO UPDATE SET query_publication_id = excluded.query_publication_id`, publication.ID); err != nil {
		return Publication{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return Publication{}, fault.New(fault.CodeInternal, true, err)
	}
	return publication, nil
}

// BeginOverlayCandidate 固定当前合法 publication 的 Catalog 与 Overlay 基线，
// 只在新的 Overlay revision 中构造查询投影，不改动 Source-derived 事实。
func (s *Store) BeginOverlayCandidate(ctx context.Context, jobID, catalogRevisionID string, controlWatermark int64) (OverlayCandidate, error) {
	current, err := s.Current(ctx)
	if err != nil {
		return OverlayCandidate{}, err
	}
	if current.CatalogRevisionID != catalogRevisionID || controlWatermark <= current.ControlWatermark {
		return OverlayCandidate{}, fault.New(fault.CodeConflict, true, nil)
	}
	overlayID, err := s.ids.New(domain.IDOverlayRevision)
	if err != nil {
		return OverlayCandidate{}, fault.New(fault.CodeInternal, true, err)
	}
	candidate := OverlayCandidate{
		CatalogRevisionID: catalogRevisionID, BaseOverlayRevisionID: current.OverlayRevisionID,
		OverlayRevisionID: overlayID.String(), JobID: jobID, ControlWatermark: controlWatermark,
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OverlayCandidate{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	now := s.clock.Now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, projection_job_id)
VALUES (?, ?, ?, 'staging', ?, ?)`, candidate.OverlayRevisionID, candidate.CatalogRevisionID, candidate.ControlWatermark, now, candidate.JobID); err != nil {
		return OverlayCandidate{}, fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO work_projections
SELECT catalog_revision_id, ?, work_id, source_id, source_key, library_id, title, creator,
tags_json, filenames_text, normalized_original_text, cjk_bigram_token_text,
latin_trigram_token_text, sort_title_key, hidden
FROM work_projections WHERE catalog_revision_id=? AND overlay_revision_id=?`,
		candidate.OverlayRevisionID, candidate.CatalogRevisionID, candidate.BaseOverlayRevisionID); err != nil {
		return OverlayCandidate{}, fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO media_projections
SELECT catalog_revision_id, ?, media_id, work_id, source_id, source_key, relative_path,
media_kind, mime_type, size_bytes, algorithm, digest, location_status, base_ordinal,
hidden, base_ordinal
FROM media_projections WHERE catalog_revision_id=? AND overlay_revision_id=?`,
		candidate.OverlayRevisionID, candidate.CatalogRevisionID, candidate.BaseOverlayRevisionID); err != nil {
		return OverlayCandidate{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return OverlayCandidate{}, fault.New(fault.CodeInternal, true, err)
	}
	return candidate, nil
}

func (s *Store) ApplyOverlayFacts(ctx context.Context, candidate OverlayCandidate, facts map[string]OverlayFact) error {
	rows, err := s.db.QueryContext(ctx, `SELECT w.work_id, sw.title, sw.creator, sw.tags_json, sw.filenames_text
FROM work_projections w JOIN source_works sw
ON sw.catalog_revision_id=w.catalog_revision_id AND sw.source_id=w.source_id AND sw.source_key=w.source_key
WHERE w.catalog_revision_id=? AND w.overlay_revision_id=? ORDER BY w.work_id`, candidate.CatalogRevisionID, candidate.OverlayRevisionID)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	type baseWork struct{ id, title, creator, tags, filenames string }
	var works []baseWork
	for rows.Next() {
		var item baseWork
		if err := rows.Scan(&item.id, &item.title, &item.creator, &item.tags, &item.filenames); err != nil {
			rows.Close()
			return fault.New(fault.CodeInternal, true, err)
		}
		works = append(works, item)
	}
	if err := rows.Close(); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	for _, item := range works {
		var tags, filenames []string
		_ = json.Unmarshal([]byte(item.tags), &tags)
		_ = json.Unmarshal([]byte(item.filenames), &filenames)
		fact := facts[item.id]
		title := item.title
		if fact.TitleOverride != "" {
			title = fact.TitleOverride
		}
		tags = mergeStrings(tags, fact.ManualTags)
		tagsJSON, _ := json.Marshal(tags)
		document := querytext.BuildDocument(title, item.creator, tags, filenames)
		hidden := 0
		if fact.Hidden {
			hidden = 1
		}
		if _, err := tx.ExecContext(ctx, `UPDATE work_projections SET title=?, creator=?, tags_json=?,
normalized_original_text=?, cjk_bigram_token_text=?, latin_trigram_token_text=?,
sort_title_key=?, hidden=? WHERE catalog_revision_id=? AND overlay_revision_id=? AND work_id=?`,
			title, item.creator, string(tagsJSON), document.NormalizedOriginal, document.CJKTokens,
			document.LatinTokens, document.SortTitleKey, hidden, candidate.CatalogRevisionID,
			candidate.OverlayRevisionID, item.id); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		if fact.CustomCoverMediaID != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE media_projections SET ordinal=-1
WHERE catalog_revision_id=? AND overlay_revision_id=? AND work_id=? AND media_id=?`,
				candidate.CatalogRevisionID, candidate.OverlayRevisionID, item.id, fact.CustomCoverMediaID); err != nil {
				return fault.New(fault.CodeInternal, true, err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO work_search
(catalog_revision_id, overlay_revision_id, work_id, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text)
SELECT catalog_revision_id, overlay_revision_id, work_id, normalized_original_text,
cjk_bigram_token_text, latin_trigram_token_text FROM work_projections
WHERE catalog_revision_id=? AND overlay_revision_id=?`, candidate.CatalogRevisionID, candidate.OverlayRevisionID); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func (s *Store) ApplyCatalogCandidateOverlays(ctx context.Context, candidate Candidate, facts map[string]OverlayFact) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM work_search
WHERE catalog_revision_id=? AND overlay_revision_id=?`, candidate.CatalogRevisionID, candidate.OverlayRevisionID); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return s.ApplyOverlayFacts(ctx, OverlayCandidate{
		CatalogRevisionID: candidate.CatalogRevisionID, OverlayRevisionID: candidate.OverlayRevisionID,
		JobID: candidate.JobID, ControlWatermark: candidate.ControlWatermark,
	}, facts)
}

func (s *Store) ValidateOverlayCandidate(ctx context.Context, candidate OverlayCandidate) error {
	var baseWorks, works, baseMedia, media, search int
	err := s.db.QueryRowContext(ctx, `SELECT
(SELECT count(*) FROM work_projections WHERE catalog_revision_id=? AND overlay_revision_id=?),
(SELECT count(*) FROM work_projections WHERE catalog_revision_id=? AND overlay_revision_id=?),
(SELECT count(*) FROM media_projections WHERE catalog_revision_id=? AND overlay_revision_id=?),
(SELECT count(*) FROM media_projections WHERE catalog_revision_id=? AND overlay_revision_id=?),
(SELECT count(*) FROM work_search WHERE catalog_revision_id=? AND overlay_revision_id=?)`,
		candidate.CatalogRevisionID, candidate.BaseOverlayRevisionID,
		candidate.CatalogRevisionID, candidate.OverlayRevisionID,
		candidate.CatalogRevisionID, candidate.BaseOverlayRevisionID,
		candidate.CatalogRevisionID, candidate.OverlayRevisionID,
		candidate.CatalogRevisionID, candidate.OverlayRevisionID).Scan(&baseWorks, &works, &baseMedia, &media, &search)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if works != baseWorks || media != baseMedia || search != works {
		return fault.New(fault.CodeCatalogCandidateInvalid, false, nil)
	}
	return nil
}

func (s *Store) PublishOverlay(ctx context.Context, candidate OverlayCandidate) (Publication, error) {
	publicationID, err := s.ids.New(domain.IDQueryPublication)
	if err != nil {
		return Publication{}, fault.New(fault.CodeInternal, true, err)
	}
	publication := Publication{ID: publicationID.String(), CatalogRevisionID: candidate.CatalogRevisionID,
		OverlayRevisionID: candidate.OverlayRevisionID, JobID: candidate.JobID,
		ControlWatermark: candidate.ControlWatermark, CreatedAt: s.clock.Now().UTC()}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Publication{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	var activeCatalog, activeOverlay, candidateStatus, catalogStatus string
	var activeWatermark int64
	if err := tx.QueryRowContext(ctx, `SELECT q.catalog_revision_id, q.overlay_revision_id, q.control_watermark,
o.status, c.status FROM active_query_publication a
JOIN query_publications q ON q.query_publication_id=a.query_publication_id
JOIN overlay_projection_revisions o ON o.overlay_revision_id=?
JOIN catalog_revisions c ON c.catalog_revision_id=o.catalog_revision_id
WHERE a.singleton=1`, candidate.OverlayRevisionID).Scan(&activeCatalog, &activeOverlay, &activeWatermark, &candidateStatus, &catalogStatus); err != nil {
		return Publication{}, fault.New(fault.CodeCatalogCandidateInvalid, false, err)
	}
	if activeCatalog != candidate.CatalogRevisionID || activeOverlay != candidate.BaseOverlayRevisionID ||
		activeWatermark >= candidate.ControlWatermark || candidateStatus != "staging" || catalogStatus != "published" {
		return Publication{}, fault.New(fault.CodeConflict, true, nil)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO query_publications
(query_publication_id, catalog_revision_id, overlay_revision_id, job_id, control_watermark, created_at)
VALUES (?, ?, ?, ?, ?, ?)`, publication.ID, publication.CatalogRevisionID, publication.OverlayRevisionID,
		publication.JobID, publication.ControlWatermark, publication.CreatedAt.Unix()); err != nil {
		return Publication{}, fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE overlay_projection_revisions SET status='published', published_at=?
WHERE overlay_revision_id=? AND status='staging'`, publication.CreatedAt.Unix(), publication.OverlayRevisionID); err != nil {
		return Publication{}, fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE active_query_publication SET query_publication_id=? WHERE singleton=1`, publication.ID); err != nil {
		return Publication{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return Publication{}, fault.New(fault.CodeInternal, true, err)
	}
	return publication, nil
}

func (s *Store) FinishOverlayCandidate(ctx context.Context, candidate OverlayCandidate, status string) error {
	if status != "aborted" && status != "superseded" {
		return fault.New(fault.CodeValidation, false, nil)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE overlay_projection_revisions SET status=?
WHERE overlay_revision_id=? AND status='staging'`, status, candidate.OverlayRevisionID)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func (s *Store) AbortOverlayCandidatesForJob(ctx context.Context, jobID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE overlay_projection_revisions SET status='aborted'
WHERE status='staging' AND projection_job_id=?`, jobID)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func (s *Store) Current(ctx context.Context) (Publication, error) {
	var publication Publication
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `SELECT q.query_publication_id, q.catalog_revision_id, q.overlay_revision_id,
q.job_id, q.control_watermark, q.created_at FROM active_query_publication a
JOIN query_publications q ON q.query_publication_id = a.query_publication_id WHERE a.singleton = 1`).Scan(
		&publication.ID, &publication.CatalogRevisionID, &publication.OverlayRevisionID,
		&publication.JobID, &publication.ControlWatermark, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Publication{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return Publication{}, fault.New(fault.CodeInternal, true, err)
	}
	publication.CreatedAt = time.Unix(createdAt, 0).UTC()
	return publication, nil
}

func (s *Store) ListWorks(ctx context.Context) (Publication, []Work, error) {
	publication, err := s.Current(ctx)
	if err != nil {
		return Publication{}, nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT w.work_id, w.title, w.creator, w.tags_json, count(m.media_id)
FROM work_projections w LEFT JOIN media_projections m
 ON m.catalog_revision_id = w.catalog_revision_id AND m.overlay_revision_id = w.overlay_revision_id AND m.work_id = w.work_id
WHERE w.catalog_revision_id = ? AND w.overlay_revision_id = ?
GROUP BY w.work_id, w.title, w.creator, w.tags_json ORDER BY w.sort_title_key, w.work_id`, publication.CatalogRevisionID, publication.OverlayRevisionID)
	if err != nil {
		return Publication{}, nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	var works []Work
	for rows.Next() {
		var work Work
		var tags string
		if err := rows.Scan(&work.ID, &work.Title, &work.Creator, &tags, &work.MediaCount); err != nil {
			return Publication{}, nil, fault.New(fault.CodeInternal, true, err)
		}
		_ = json.Unmarshal([]byte(tags), &work.Tags)
		works = append(works, work)
	}
	return publication, works, rows.Err()
}

func (s *Store) GetWork(ctx context.Context, id string) (Publication, Work, error) {
	publication, err := s.Current(ctx)
	if err != nil {
		return Publication{}, Work{}, err
	}
	var work Work
	var tags string
	err = s.db.QueryRowContext(ctx, `SELECT w.work_id, w.title, w.creator, w.tags_json, count(m.media_id)
FROM work_projections w LEFT JOIN media_projections m
 ON m.catalog_revision_id = w.catalog_revision_id AND m.overlay_revision_id = w.overlay_revision_id AND m.work_id = w.work_id
WHERE w.catalog_revision_id = ? AND w.overlay_revision_id = ? AND w.work_id = ? GROUP BY w.work_id, w.title, w.creator, w.tags_json`, publication.CatalogRevisionID, publication.OverlayRevisionID, id).Scan(&work.ID, &work.Title, &work.Creator, &tags, &work.MediaCount)
	if errors.Is(err, sql.ErrNoRows) {
		return Publication{}, Work{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return Publication{}, Work{}, fault.New(fault.CodeInternal, true, err)
	}
	_ = json.Unmarshal([]byte(tags), &work.Tags)
	return publication, work, nil
}

func (s *Store) GetMedia(ctx context.Context, id string) (Publication, Media, error) {
	publication, err := s.Current(ctx)
	if err != nil {
		return Publication{}, Media{}, err
	}
	var media Media
	err = s.db.QueryRowContext(ctx, `SELECT media_id, work_id, source_id, media_kind, mime_type, size_bytes,
algorithm, digest, location_status, ordinal, relative_path FROM media_projections
WHERE catalog_revision_id = ? AND overlay_revision_id = ? AND media_id = ?`, publication.CatalogRevisionID, publication.OverlayRevisionID, id).Scan(
		&media.ID, &media.WorkID, &media.SourceID, &media.Kind, &media.MIME, &media.Size,
		&media.Algorithm, &media.Digest, &media.LocationStatus, &media.Ordinal, &media.RelativePath,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Publication{}, Media{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return Publication{}, Media{}, fault.New(fault.CodeInternal, true, err)
	}
	return publication, media, nil
}

func (s *Store) ListMediaForWork(ctx context.Context, workID string) (Publication, []Media, error) {
	publication, err := s.Current(ctx)
	if err != nil {
		return Publication{}, nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT media_id, work_id, source_id, media_kind, mime_type, size_bytes,
algorithm, digest, location_status, ordinal, relative_path FROM media_projections
WHERE catalog_revision_id = ? AND overlay_revision_id = ? AND work_id = ? ORDER BY ordinal, media_id`, publication.CatalogRevisionID, publication.OverlayRevisionID, workID)
	if err != nil {
		return Publication{}, nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	var items []Media
	for rows.Next() {
		var media Media
		if err := rows.Scan(&media.ID, &media.WorkID, &media.SourceID, &media.Kind, &media.MIME, &media.Size, &media.Algorithm, &media.Digest, &media.LocationStatus, &media.Ordinal, &media.RelativePath); err != nil {
			return Publication{}, nil, fault.New(fault.CodeInternal, true, err)
		}
		items = append(items, media)
	}
	if err := rows.Err(); err != nil {
		return Publication{}, nil, fault.New(fault.CodeInternal, true, err)
	}
	if len(items) == 0 {
		return Publication{}, nil, fault.New(fault.CodeNotFound, false, nil)
	}
	return publication, items, nil
}

func (s *Store) PublicationForJob(ctx context.Context, jobID string) (Publication, error) {
	var publication Publication
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `SELECT query_publication_id, catalog_revision_id, overlay_revision_id, job_id, control_watermark, created_at
FROM query_publications WHERE job_id = ?`, jobID).Scan(&publication.ID, &publication.CatalogRevisionID, &publication.OverlayRevisionID, &publication.JobID, &publication.ControlWatermark, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Publication{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return Publication{}, fault.New(fault.CodeInternal, true, err)
	}
	publication.CreatedAt = time.Unix(createdAt, 0).UTC()
	return publication, nil
}

func (s *Store) AbortCandidate(ctx context.Context, jobID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE catalog_revisions SET status = 'aborted' WHERE job_id = ? AND status = 'staging'`, jobID)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	_, err = s.db.ExecContext(ctx, `UPDATE overlay_projection_revisions SET status = 'aborted' WHERE catalog_revision_id IN
(SELECT catalog_revision_id FROM catalog_revisions WHERE job_id = ? AND status = 'aborted') AND status = 'staging'`, jobID)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func (s *Store) stageWorks(ctx context.Context, candidate Candidate, works []WorkFact) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	for _, work := range works {
		tagsJSON, _ := json.Marshal(work.Tags)
		sourceTitle, sourceTags := work.Title, work.Tags
		if work.SourceTitle != "" {
			sourceTitle, sourceTags = work.SourceTitle, work.SourceTags
		}
		sourceTagsJSON, _ := json.Marshal(sourceTags)
		filenamesJSON, _ := json.Marshal(work.Filenames)
		document := querytext.BuildDocument(work.Title, work.Creator, work.Tags, work.Filenames)
		hidden := 0
		if work.Hidden {
			hidden = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO source_works
(catalog_revision_id, source_id, source_key, title, creator, tags_json, filenames_text) VALUES (?, ?, ?, ?, ?, ?, ?)`, candidate.CatalogRevisionID, work.SourceID, work.SourceKey, sourceTitle, work.Creator, string(sourceTagsJSON), string(filenamesJSON)); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator, tags_json, filenames_text,
 normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text, sort_title_key, hidden)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, candidate.CatalogRevisionID, candidate.OverlayRevisionID, work.WorkID, work.SourceID, work.SourceKey, work.LibraryID, work.Title, work.Creator, string(tagsJSON), string(filenamesJSON), document.NormalizedOriginal, document.CJKTokens, document.LatinTokens, document.SortTitleKey, hidden); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_search
(catalog_revision_id, overlay_revision_id, work_id, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text)
VALUES (?, ?, ?, ?, ?, ?)`, candidate.CatalogRevisionID, candidate.OverlayRevisionID, work.WorkID, document.NormalizedOriginal, document.CJKTokens, document.LatinTokens); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func (s *Store) stageMedia(ctx context.Context, candidate Candidate, items []MediaFact) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	for _, item := range items {
		if _, err := tx.ExecContext(ctx, `INSERT INTO source_media
(catalog_revision_id, source_id, source_key, work_source_key, relative_path, media_kind, mime_type, size_bytes)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, candidate.CatalogRevisionID, item.SourceID, item.SourceKey, item.WorkSourceKey, item.RelativePath, item.Kind, item.MIME, item.Size); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO content_blobs
(catalog_revision_id, algorithm, digest, size_bytes) VALUES (?, ?, ?, ?)`, candidate.CatalogRevisionID, item.Algorithm, item.Digest, item.Size); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO file_locations
(catalog_revision_id, source_id, source_key, location_key, relative_path, algorithm, digest, status)
VALUES (?, ?, ?, ?, ?, ?, ?, 'present')`, candidate.CatalogRevisionID, item.SourceID, item.SourceKey, item.LocationKey, item.RelativePath, item.Algorithm, item.Digest); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO media_projections
(catalog_revision_id, overlay_revision_id, media_id, work_id, source_id, source_key, relative_path,
 media_kind, mime_type, size_bytes, algorithm, digest, location_status, ordinal, base_ordinal)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'present', ?, ?)`, candidate.CatalogRevisionID, candidate.OverlayRevisionID, item.MediaID, item.WorkID, item.SourceID, item.SourceKey, item.RelativePath, item.Kind, item.MIME, item.Size, item.Algorithm, item.Digest, item.Ordinal, item.Ordinal); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func cloneUnchangedSources(ctx context.Context, tx *sql.Tx, candidate Candidate) error {
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO source_works SELECT ?, w.source_id, w.source_key, w.title, w.creator, w.tags_json, w.filenames_text FROM source_works w
JOIN active_query_publication a ON a.singleton=1 JOIN query_publications q ON q.query_publication_id=a.query_publication_id
WHERE w.catalog_revision_id=q.catalog_revision_id AND w.source_id<>?`, []any{candidate.CatalogRevisionID, candidate.SourceID}},
		{`INSERT INTO source_media SELECT ?, m.source_id, m.source_key, m.work_source_key, m.relative_path, m.media_kind, m.mime_type, m.size_bytes FROM source_media m
JOIN active_query_publication a ON a.singleton=1 JOIN query_publications q ON q.query_publication_id=a.query_publication_id
WHERE m.catalog_revision_id=q.catalog_revision_id AND m.source_id<>?`, []any{candidate.CatalogRevisionID, candidate.SourceID}},
		{`INSERT INTO content_blobs SELECT DISTINCT ?, b.algorithm, b.digest, b.size_bytes FROM content_blobs b
JOIN active_query_publication a ON a.singleton=1 JOIN query_publications q ON q.query_publication_id=a.query_publication_id
JOIN media_projections m ON m.catalog_revision_id=q.catalog_revision_id AND m.overlay_revision_id=q.overlay_revision_id AND m.algorithm=b.algorithm AND m.digest=b.digest
WHERE b.catalog_revision_id=q.catalog_revision_id AND m.source_id<>?`, []any{candidate.CatalogRevisionID, candidate.SourceID}},
		{`INSERT INTO file_locations SELECT ?, f.source_id, f.source_key, f.location_key, f.relative_path, f.algorithm, f.digest, f.status FROM file_locations f
JOIN active_query_publication a ON a.singleton=1 JOIN query_publications q ON q.query_publication_id=a.query_publication_id
WHERE f.catalog_revision_id=q.catalog_revision_id AND f.source_id<>?`, []any{candidate.CatalogRevisionID, candidate.SourceID}},
		{`INSERT INTO work_projections SELECT ?, ?, w.work_id, w.source_id, w.source_key, w.library_id, w.title, w.creator, w.tags_json, w.filenames_text, w.normalized_original_text, w.cjk_bigram_token_text, w.latin_trigram_token_text, w.sort_title_key, w.hidden FROM work_projections w
JOIN active_query_publication a ON a.singleton=1 JOIN query_publications q ON q.query_publication_id=a.query_publication_id
WHERE w.catalog_revision_id=q.catalog_revision_id AND w.overlay_revision_id=q.overlay_revision_id AND w.source_id<>?`, []any{candidate.CatalogRevisionID, candidate.OverlayRevisionID, candidate.SourceID}},
		{`INSERT INTO media_projections SELECT ?, ?, m.media_id, m.work_id, m.source_id, m.source_key, m.relative_path, m.media_kind, m.mime_type, m.size_bytes, m.algorithm, m.digest, m.location_status, m.ordinal, m.hidden, m.base_ordinal FROM media_projections m
JOIN active_query_publication a ON a.singleton=1 JOIN query_publications q ON q.query_publication_id=a.query_publication_id
WHERE m.catalog_revision_id=q.catalog_revision_id AND m.overlay_revision_id=q.overlay_revision_id AND m.source_id<>?`, []any{candidate.CatalogRevisionID, candidate.OverlayRevisionID, candidate.SourceID}},
		{`INSERT INTO work_search (catalog_revision_id, overlay_revision_id, work_id, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text)
SELECT ?, ?, s.work_id, s.normalized_original_text, s.cjk_bigram_token_text, s.latin_trigram_token_text FROM work_search s
JOIN active_query_publication a ON a.singleton=1 JOIN query_publications q ON q.query_publication_id=a.query_publication_id
JOIN work_projections w ON w.catalog_revision_id=q.catalog_revision_id AND w.overlay_revision_id=q.overlay_revision_id AND w.work_id=s.work_id
WHERE s.catalog_revision_id=q.catalog_revision_id AND s.overlay_revision_id=q.overlay_revision_id AND w.source_id<>?`, []any{candidate.CatalogRevisionID, candidate.OverlayRevisionID, candidate.SourceID}},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return err
		}
	}
	return nil
}

func mergeStrings(base, added []string) []string {
	seen := make(map[string]struct{}, len(base)+len(added))
	result := make([]string, 0, len(base)+len(added))
	for _, group := range [][]string{base, added} {
		for _, value := range group {
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
