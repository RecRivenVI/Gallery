package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
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
	Favorite           bool
	Progress           float64
}

type WorkFact struct {
	SourceID           string
	LibraryID          string
	SourceKey          string
	ProviderID         string
	ExternalID         string
	SourceTitle        string
	SourceTags         []string
	Title              string
	Creator            string
	CreatorID          string
	CreatorSourceKey   string
	CreatorProviderID  string
	CreatorExternalID  string
	SourceCreatorName  string
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
	RuleKey       string
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

	// ContentVerificationState 是 "located_unverified" 或 "content_verified"；未确认媒体
	// 的 Algorithm/Digest/LocationKey 为空，不写入 content_blobs/file_locations。
	ContentVerificationState string
	MTimeNanos               int64
	PlatformIdentityKind     string
	PlatformIdentityValue    string
	ContainerSignature       string
	LastConfirmedAlgorithm   string
	LastConfirmedDigest      string
	LastConfirmedAt          time.Time
}

const (
	ContentVerificationStateLocatedUnverified = "located_unverified"
	ContentVerificationStateContentVerified   = "content_verified"
)

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
	SourceID   string
	LibraryID  string
	Title      string
	Creator    string
	Tags       []string
	MediaCount int
}

type Media struct {
	ID        string
	WorkID    string
	SourceID  string
	Kind      string
	MIME      string
	Size      int64
	Algorithm string
	Digest    string
	// LocationStatus 只表达位置可用性（present/offline/missing/inaccessible），与内容是否
	// 已完整确认正交；不得再借用它表达 located_unverified。
	LocationStatus string
	// ContentVerificationState 是 "located_unverified" 或 "content_verified"；前者的
	// Algorithm/Digest 为空，不指向任何已确认 ContentBlob。
	ContentVerificationState string
	// VerifiedAt 是媒体最近一次真正完成完整 SHA-256 确认的时间；located_unverified 媒体
	// 恒为零值，API 必须据此返回 null 而不是伪造时间。
	VerifiedAt   time.Time
	Ordinal      int
	RelativePath string
}

// BlobLocation 是一个已发布 ContentBlob 的只读 occurrence。它只携带服务端打开媒体所需的
// Source/相对路径与公开媒体类型，不暴露 Catalog 内部 row ID。
type BlobLocation struct {
	SourceID     string
	RelativePath string
	MIME         string
	Size         int64
	Algorithm    string
	Digest       string
}

type GCResult struct {
	ExpiredQueryLeases int
	ExpiredBlobLeases  int
	Publications       int
	OverlayRevisions   int
	CatalogRevisions   int
	StagingAborted     int
	SkippedActive      int
	DryRun             bool
}

type GCOptions struct {
	Retention time.Duration
	// ActiveJobIDs 保护这些 Job 拥有的 staging candidate 不被当作遗留 staging 中止。
	ActiveJobIDs []string
	// ProtectedBlobs 是调用方（通常是 maintenance.Service，结合 jobs.Store 的非终态/退避
	// 等待中 DerivedAsset Job）显式声明的仍在使用中的 ContentBlob 摘要：即便这些摘要当前
	// 没有任何有效的 blob_read_leases 行（租约可能已按固定 TTL 过期，但引用它的 Job 仍在
	// 排队、执行或退避等待、随时可能重新读取），持有它的 catalog_revision 也不得被本轮
	// GC 回收。不引入第二套保护表，只是把既有 content_blobs 判据的输入来源之一从纯租约
	// 扩展为"租约 OR 调用方显式声明仍在使用"。
	ProtectedBlobs []domain.ContentBlobRef
	DryRun         bool
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

// BeginCandidate 是同一逻辑 Job 多 Attempt 下候选构建的幂等入口：Candidate 归 Job 所有
// （同一 job_id 任一时刻至多一行 catalog_revisions），而不是归某次 Attempt 所有。调用前
// 先查询该 Job 既有的 catalog_revisions 行：
//
//   - 不存在：正常创建全新 staging candidate（与此前行为一致）；
//   - 存在且 status 为 staging 或 aborted（上次 Attempt 未完成或已被清理）：视为可安全重建，
//     在同一事务内先删除该行（级联清理全部 Source-derived 事实与查询投影，并显式清理不受
//     外键管理的 FTS5 work_search 行），再插入全新 revision，不复用可能部分写入的 staging
//     结果；
//   - 存在且 status 为 published：说明该 Job 已经真正完成过发布，只是 control 侧尚未收到
//     completed（Saga gap）。此时绝不能再次构建或再次发布，返回
//     CodeCatalogCandidatePublished 交由调用方通过 PublicationForJob 对账为 completed。
//
// job_id 上的 UNIQUE 约束因此保持不变：它表达的是“同一 Job 任一时刻至多一个 Catalog
// revision 归属”，而不是“该 Job 一辈子只能拥有一行”。
func (s *Store) BeginCandidate(ctx context.Context, jobID, sourceID string, controlWatermark int64) (Candidate, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Candidate{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()

	var existingRevisionID, existingStatus string
	err = tx.QueryRowContext(ctx, `SELECT catalog_revision_id, status FROM catalog_revisions WHERE job_id=?`, jobID).
		Scan(&existingRevisionID, &existingStatus)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// 该 Job 从未拥有过 candidate，按原有路径创建。
	case err != nil:
		return Candidate{}, fault.New(fault.CodeInternal, true, err)
	case existingStatus == "published":
		return Candidate{}, fault.New(fault.CodeCatalogCandidatePublished, false, nil)
	case existingStatus == "staging" || existingStatus == "aborted":
		if err := resetCandidateRevision(ctx, tx, existingRevisionID); err != nil {
			return Candidate{}, fault.New(fault.CodeInternal, true, err)
		}
	default:
		return Candidate{}, fault.New(fault.CodeCatalogCandidateInvalid, false, nil)
	}

	catalogID, err := s.ids.New(domain.IDCatalogRevision)
	if err != nil {
		return Candidate{}, fault.New(fault.CodeInternal, true, err)
	}
	overlayID, err := s.ids.New(domain.IDOverlayRevision)
	if err != nil {
		return Candidate{}, fault.New(fault.CodeInternal, true, err)
	}
	candidate := Candidate{CatalogRevisionID: catalogID.String(), OverlayRevisionID: overlayID.String(), JobID: jobID, SourceID: sourceID, ControlWatermark: controlWatermark}
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

// resetCandidateRevision 删除一个尚未发布（staging 或 aborted）的 catalog_revisions 行。
// 外键级联清理 overlay_projection_revisions、source_works/source_media/source_creators、
// content_blobs/file_locations、work_projections/media_projections/creator_projections、
// work_creator_relations；work_search 是 FTS5 虚表，SQLite 不对其应用外键级联，必须在同一
// 事务内显式删除，否则会残留孤儿全文索引行。调用方必须保证该 revision 从未进入
// published 状态——已发布 revision 由 query_publications 的 ON DELETE RESTRICT 外键保护，
// 误删会直接失败而不是静默丢失数据。
func resetCandidateRevision(ctx context.Context, tx *sql.Tx, catalogRevisionID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM work_search WHERE catalog_revision_id=?`, catalogRevisionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_revisions WHERE catalog_revision_id=?`, catalogRevisionID); err != nil {
		return err
	}
	return nil
}

// validateSearchFieldValues 拒绝 Tag/文件名中出现 querytext.FieldSeparator（U+001F）：
// search_tags_norm/search_filenames_norm 用该字符拼接多个取值，一旦某个取值本身携带
// 分隔符就会在存储层伪装成两个取值，破坏 ranking/highlight 的取值边界。这里覆盖规则
// 与 metadata 产生的 Tag（不止用户手动输入才算输入来源）；文件名来自实际扫描到的
// 相对路径 basename，理论上只受宿主文件系统限制，但同样在权威边界统一拒绝。
func validateSearchFieldValues(work WorkFact) error {
	for _, tag := range work.Tags {
		if strings.Contains(tag, querytext.FieldSeparator) {
			return fault.WithField(fault.CodeValidation, "tags", nil)
		}
	}
	for _, filename := range work.Filenames {
		if strings.Contains(filename, querytext.FieldSeparator) {
			return fault.WithField(fault.CodeValidation, "filenames", nil)
		}
	}
	return nil
}

func (s *Store) Stage(ctx context.Context, candidate Candidate, works []WorkFact, media []MediaFact) error {
	for _, work := range works {
		if err := validateSearchFieldValues(work); err != nil {
			return err
		}
	}
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
	var orphan, orphanCreator int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM media_projections m
LEFT JOIN work_projections w ON w.catalog_revision_id = m.catalog_revision_id
 AND w.overlay_revision_id = m.overlay_revision_id AND w.work_id = m.work_id
WHERE m.catalog_revision_id = ? AND w.work_id IS NULL`, candidate.CatalogRevisionID).Scan(&orphan); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if orphan != 0 {
		return fault.New(fault.CodeCatalogCandidateInvalid, false, nil)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM work_creator_relations r
LEFT JOIN work_projections w ON w.catalog_revision_id=r.catalog_revision_id
 AND w.overlay_revision_id=r.overlay_revision_id AND w.work_id=r.work_id
LEFT JOIN creator_projections c ON c.catalog_revision_id=r.catalog_revision_id
 AND c.overlay_revision_id=r.overlay_revision_id AND c.creator_id=r.creator_id
WHERE r.catalog_revision_id=? AND (w.work_id IS NULL OR c.creator_id IS NULL)`,
		candidate.CatalogRevisionID).Scan(&orphanCreator); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if orphanCreator != 0 {
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
latin_trigram_token_text, sort_title_key, hidden, favorite, progress,
search_title_norm, search_creator_norm, search_tags_norm, search_filenames_norm
FROM work_projections WHERE catalog_revision_id=? AND overlay_revision_id=?`,
		candidate.OverlayRevisionID, candidate.CatalogRevisionID, candidate.BaseOverlayRevisionID); err != nil {
		return OverlayCandidate{}, fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO media_projections
(catalog_revision_id, overlay_revision_id, media_id, work_id, source_id, source_key, relative_path,
 media_kind, mime_type, size_bytes, algorithm, digest, location_status, ordinal, hidden, base_ordinal,
 content_verification_state, verified_at)
SELECT catalog_revision_id, ?, media_id, work_id, source_id, source_key, relative_path,
media_kind, mime_type, size_bytes, algorithm, digest, location_status, base_ordinal,
hidden, base_ordinal, content_verification_state, verified_at
FROM media_projections WHERE catalog_revision_id=? AND overlay_revision_id=?`,
		candidate.OverlayRevisionID, candidate.CatalogRevisionID, candidate.BaseOverlayRevisionID); err != nil {
		return OverlayCandidate{}, fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO creator_projections
SELECT catalog_revision_id, ?, creator_id, name, sort_name_key
FROM creator_projections WHERE catalog_revision_id=? AND overlay_revision_id=?`,
		candidate.OverlayRevisionID, candidate.CatalogRevisionID, candidate.BaseOverlayRevisionID); err != nil {
		return OverlayCandidate{}, fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO work_creator_relations
SELECT catalog_revision_id, ?, work_id, creator_id, role, ordinal
FROM work_creator_relations WHERE catalog_revision_id=? AND overlay_revision_id=?`,
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
		hidden, favorite := 0, 0
		if fact.Hidden {
			hidden = 1
		}
		if fact.Favorite {
			favorite = 1
		}
		if _, err := tx.ExecContext(ctx, `UPDATE work_projections SET title=?, creator=?, tags_json=?,
normalized_original_text=?, cjk_bigram_token_text=?, latin_trigram_token_text=?,
sort_title_key=?, hidden=?, favorite=?, progress=?,
search_title_norm=?, search_creator_norm=?, search_tags_norm=?, search_filenames_norm=?
WHERE catalog_revision_id=? AND overlay_revision_id=? AND work_id=?`,
			title, item.creator, string(tagsJSON), document.NormalizedOriginal, document.CJKTokens,
			document.LatinTokens, document.SortTitleKey, hidden, favorite, fact.Progress,
			document.TitleNorm, document.CreatorNorm, document.TagsNorm, document.FilenamesNorm,
			candidate.CatalogRevisionID, candidate.OverlayRevisionID, item.id); err != nil {
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

// ApplyCreatorMerges 让查询投影的展示层反映 control.db 中已生效的创作者合并：把
// 主创作者 base 身份属于被合并集合的作品的规范化创作者名重写为 target 名，并重建这
// 些作品的 work_search 文档。它在 ApplyOverlayFacts 之后运行——后者已把整个 revision
// 的 work_projections.creator 复位为 source_works 的 base 名，因此本方法只需按当前合并
// 映射覆盖受影响作品；撤销合并即传入空映射，展示名自然回落到 base，投影可靠恢复。
//
// work_creator_relations 与 creator_projections 保持 base（原始绑定）身份不变：它们当前
// 不参与任何查询路径，把有效创作者折叠进投影会破坏撤销可逆性；待未来“按创作者浏览”
// 查询需要时，再引入 base_creator_id 单独物化有效关系。该方法对 Scan 的 Candidate 与
// Overlay 重投影通用，因此以显式 revision 传参。
func (s *Store) ApplyCreatorMerges(ctx context.Context, catalogRevisionID, overlayRevisionID string, merges []domain.CreatorMergePair) error {
	if len(merges) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()
	type affectedWork struct{ workID, title, tagsJSON, filenames, creator string }
	var affected []affectedWork
	for _, pair := range merges {
		rows, err := tx.QueryContext(ctx, `SELECT w.work_id, w.title, w.tags_json, w.filenames_text
FROM work_creator_relations r JOIN work_projections w
 ON w.catalog_revision_id=r.catalog_revision_id AND w.overlay_revision_id=r.overlay_revision_id AND w.work_id=r.work_id
WHERE r.catalog_revision_id=? AND r.overlay_revision_id=? AND r.creator_id=? AND r.role='primary' AND r.ordinal=0`,
			catalogRevisionID, overlayRevisionID, pair.Absorbed)
		if err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		for rows.Next() {
			item := affectedWork{creator: pair.TargetName}
			if err := rows.Scan(&item.workID, &item.title, &item.tagsJSON, &item.filenames); err != nil {
				rows.Close()
				return fault.New(fault.CodeInternal, true, err)
			}
			affected = append(affected, item)
		}
		if err := rows.Close(); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
	}
	for _, item := range affected {
		var tags, filenames []string
		_ = json.Unmarshal([]byte(item.tagsJSON), &tags)
		_ = json.Unmarshal([]byte(item.filenames), &filenames)
		document := querytext.BuildDocument(item.title, item.creator, tags, filenames)
		if _, err := tx.ExecContext(ctx, `UPDATE work_projections SET creator=?,
normalized_original_text=?, cjk_bigram_token_text=?, latin_trigram_token_text=?, sort_title_key=?
WHERE catalog_revision_id=? AND overlay_revision_id=? AND work_id=?`,
			item.creator, document.NormalizedOriginal, document.CJKTokens, document.LatinTokens,
			document.SortTitleKey, catalogRevisionID, overlayRevisionID, item.workID); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM work_search
WHERE catalog_revision_id=? AND overlay_revision_id=? AND work_id=?`,
			catalogRevisionID, overlayRevisionID, item.workID); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_search
(catalog_revision_id, overlay_revision_id, work_id, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text)
VALUES (?, ?, ?, ?, ?, ?)`, catalogRevisionID, overlayRevisionID, item.workID,
			document.NormalizedOriginal, document.CJKTokens, document.LatinTokens); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func (s *Store) ValidateOverlayCandidate(ctx context.Context, candidate OverlayCandidate) error {
	var baseWorks, works, baseMedia, media, baseCreators, creators, baseRelations, relations, search int
	err := s.db.QueryRowContext(ctx, `SELECT
(SELECT count(*) FROM work_projections WHERE catalog_revision_id=? AND overlay_revision_id=?),
(SELECT count(*) FROM work_projections WHERE catalog_revision_id=? AND overlay_revision_id=?),
(SELECT count(*) FROM media_projections WHERE catalog_revision_id=? AND overlay_revision_id=?),
(SELECT count(*) FROM media_projections WHERE catalog_revision_id=? AND overlay_revision_id=?),
(SELECT count(*) FROM creator_projections WHERE catalog_revision_id=? AND overlay_revision_id=?),
(SELECT count(*) FROM creator_projections WHERE catalog_revision_id=? AND overlay_revision_id=?),
(SELECT count(*) FROM work_creator_relations WHERE catalog_revision_id=? AND overlay_revision_id=?),
(SELECT count(*) FROM work_creator_relations WHERE catalog_revision_id=? AND overlay_revision_id=?),
(SELECT count(*) FROM work_search WHERE catalog_revision_id=? AND overlay_revision_id=?)`,
		candidate.CatalogRevisionID, candidate.BaseOverlayRevisionID,
		candidate.CatalogRevisionID, candidate.OverlayRevisionID,
		candidate.CatalogRevisionID, candidate.BaseOverlayRevisionID,
		candidate.CatalogRevisionID, candidate.OverlayRevisionID,
		candidate.CatalogRevisionID, candidate.BaseOverlayRevisionID,
		candidate.CatalogRevisionID, candidate.OverlayRevisionID,
		candidate.CatalogRevisionID, candidate.BaseOverlayRevisionID,
		candidate.CatalogRevisionID, candidate.OverlayRevisionID,
		candidate.CatalogRevisionID, candidate.OverlayRevisionID).Scan(
		&baseWorks, &works, &baseMedia, &media, &baseCreators, &creators, &baseRelations, &relations, &search)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if works != baseWorks || media != baseMedia || creators != baseCreators || relations != baseRelations || search != works {
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

// PublicationByID 按显式 query_publication_id 解析一个（可能不是当前 active 的）历史
// publication；不存在或已被 GC 一律返回 CodeCursorExpired，与游标过期使用同一稳定错误，
// 不向调用方暴露"从未存在"与"已被回收"的区别。
func (s *Store) PublicationByID(ctx context.Context, id string) (Publication, error) {
	if _, err := domain.ParseID(domain.IDQueryPublication, id); err != nil {
		return Publication{}, fault.New(fault.CodeCursorExpired, true, nil)
	}
	var publication Publication
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `SELECT query_publication_id, catalog_revision_id, overlay_revision_id,
job_id, control_watermark, created_at FROM query_publications WHERE query_publication_id=?`, id).Scan(
		&publication.ID, &publication.CatalogRevisionID, &publication.OverlayRevisionID,
		&publication.JobID, &publication.ControlWatermark, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Publication{}, fault.New(fault.CodeCursorExpired, true, nil)
	}
	if err != nil {
		return Publication{}, fault.New(fault.CodeInternal, true, err)
	}
	publication.CreatedAt = time.Unix(createdAt, 0).UTC()
	return publication, nil
}

// resolvePublication 是媒体/Work 读取端点快照绑定的唯一解析入口：空 publicationID 表示
// 客户端未显式指定，退回当前 active publication（"current" 模式）；非空则必须精确解析为
// 该 ID 对应的历史 publication（"snapshot" 模式），不得静默回退到 active，即使该 ID 已
// 过期或不存在。
func (s *Store) resolvePublication(ctx context.Context, publicationID string) (Publication, error) {
	if publicationID == "" {
		return s.Current(ctx)
	}
	return s.PublicationByID(ctx, publicationID)
}

func (s *Store) GetWork(ctx context.Context, id string) (Publication, Work, error) {
	return s.GetWorkAt(ctx, "", id)
}

// GetWorkAt 与 GetMediaAt 使用相同 publication 解析语义。
func (s *Store) GetWorkAt(ctx context.Context, publicationID, id string) (Publication, Work, error) {
	publication, err := s.resolvePublication(ctx, publicationID)
	if err != nil {
		return Publication{}, Work{}, err
	}
	var work Work
	var tags string
	err = s.db.QueryRowContext(ctx, `SELECT w.work_id, w.source_id, w.library_id, w.title, w.creator, w.tags_json, count(m.media_id)
FROM work_projections w LEFT JOIN media_projections m
 ON m.catalog_revision_id = w.catalog_revision_id AND m.overlay_revision_id = w.overlay_revision_id AND m.work_id = w.work_id
WHERE w.catalog_revision_id = ? AND w.overlay_revision_id = ? AND w.work_id = ? GROUP BY w.work_id, w.source_id, w.library_id, w.title, w.creator, w.tags_json`, publication.CatalogRevisionID, publication.OverlayRevisionID, id).Scan(&work.ID, &work.SourceID, &work.LibraryID, &work.Title, &work.Creator, &tags, &work.MediaCount)
	if errors.Is(err, sql.ErrNoRows) {
		return Publication{}, Work{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return Publication{}, Work{}, fault.New(fault.CodeInternal, true, err)
	}
	_ = json.Unmarshal([]byte(tags), &work.Tags)
	return publication, work, nil
}

// BlobLocations 返回仍被已发布 revision 引用的 present occurrence，供固定 Blob 分享在
// active publication 已切换后继续解析稳定内容身份。调用方仍须通过 media.PrepareSnapshot
// 校验真实字节摘要；Catalog 中的 present 状态不能替代读取时校验。
func (s *Store) BlobLocations(ctx context.Context, blob domain.ContentBlobRef) ([]BlobLocation, error) {
	if _, err := domain.ParseContentBlobRef(blob.Algorithm, blob.Digest); err != nil {
		return nil, fault.New(fault.CodeValidation, false, err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT f.source_id, f.relative_path,
COALESCE(sm.mime_type, 'application/octet-stream'), b.size_bytes
FROM query_publications q
JOIN content_blobs b ON b.catalog_revision_id=q.catalog_revision_id
JOIN file_locations f ON f.catalog_revision_id=b.catalog_revision_id
 AND f.algorithm=b.algorithm AND f.digest=b.digest AND f.status='present'
LEFT JOIN source_media sm ON sm.catalog_revision_id=f.catalog_revision_id
 AND sm.source_id=f.source_id AND sm.source_key=f.source_key
WHERE b.algorithm=? AND b.digest=?
ORDER BY q.created_at DESC, f.source_id, f.location_key`, blob.Algorithm, blob.Digest)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	seen := map[string]struct{}{}
	var result []BlobLocation
	for rows.Next() {
		var item BlobLocation
		if err := rows.Scan(&item.SourceID, &item.RelativePath, &item.MIME, &item.Size); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		key := item.SourceID + "\x00" + item.RelativePath
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		item.Algorithm, item.Digest = blob.Algorithm, blob.Digest
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	if len(result) == 0 {
		return nil, fault.New(fault.CodeNotFound, false, nil)
	}
	return result, nil
}

// GetMedia 解析当前 active publication 中的媒体，等价于 GetMediaAt(ctx, "", id)。
func (s *Store) GetMedia(ctx context.Context, id string) (Publication, Media, error) {
	return s.GetMediaAt(ctx, "", id)
}

// GetMediaAt 是媒体读取的快照绑定入口：publicationID 为空时退回当前 active publication
// （"current" 模式，与既有行为一致）；非空时必须精确解析该历史 publication（"snapshot"
// 模式），媒体必须真实存在于该 publication 的 revision 组合中，不得静默改读 active。
func (s *Store) GetMediaAt(ctx context.Context, publicationID, id string) (Publication, Media, error) {
	publication, err := s.resolvePublication(ctx, publicationID)
	if err != nil {
		return Publication{}, Media{}, err
	}
	var media Media
	var verifiedAt sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT media_id, work_id, source_id, media_kind, mime_type, size_bytes,
algorithm, digest, location_status, content_verification_state, verified_at, ordinal, relative_path FROM media_projections
WHERE catalog_revision_id = ? AND overlay_revision_id = ? AND media_id = ?`, publication.CatalogRevisionID, publication.OverlayRevisionID, id).Scan(
		&media.ID, &media.WorkID, &media.SourceID, &media.Kind, &media.MIME, &media.Size,
		&media.Algorithm, &media.Digest, &media.LocationStatus, &media.ContentVerificationState, &verifiedAt, &media.Ordinal, &media.RelativePath,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Publication{}, Media{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return Publication{}, Media{}, fault.New(fault.CodeInternal, true, err)
	}
	if verifiedAt.Valid {
		media.VerifiedAt = time.Unix(verifiedAt.Int64, 0).UTC()
	}
	return publication, media, nil
}

// ListMediaForWork 列出当前 active publication 中某作品的媒体，等价于
// ListMediaForWorkAt(ctx, "", workID)。
func (s *Store) ListMediaForWork(ctx context.Context, workID string) (Publication, []Media, error) {
	return s.ListMediaForWorkAt(ctx, "", workID)
}

// ListMediaForWorkAt 是 ListMediaForWork 的快照绑定版本，语义同 GetMediaAt。
func (s *Store) ListMediaForWorkAt(ctx context.Context, publicationID, workID string) (Publication, []Media, error) {
	publication, err := s.resolvePublication(ctx, publicationID)
	if err != nil {
		return Publication{}, nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT media_id, work_id, source_id, media_kind, mime_type, size_bytes,
algorithm, digest, location_status, content_verification_state, verified_at, ordinal, relative_path FROM media_projections
WHERE catalog_revision_id = ? AND overlay_revision_id = ? AND work_id = ? ORDER BY ordinal, media_id`, publication.CatalogRevisionID, publication.OverlayRevisionID, workID)
	if err != nil {
		return Publication{}, nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	var items []Media
	for rows.Next() {
		var media Media
		var verifiedAt sql.NullInt64
		if err := rows.Scan(&media.ID, &media.WorkID, &media.SourceID, &media.Kind, &media.MIME, &media.Size, &media.Algorithm, &media.Digest, &media.LocationStatus, &media.ContentVerificationState, &verifiedAt, &media.Ordinal, &media.RelativePath); err != nil {
			return Publication{}, nil, fault.New(fault.CodeInternal, true, err)
		}
		if verifiedAt.Valid {
			media.VerifiedAt = time.Unix(verifiedAt.Int64, 0).UTC()
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

// protectedBlobClause 为一组调用方显式声明的仍在使用中的 ContentBlob 摘要构造 SQL 排除
// 条件：任何仍被非终态或退避等待中的 DerivedAsset Job 引用的摘要，即便没有当前有效的
// blob_read_leases 行，也不得被本轮 GC 回收。revisionColumn 是该子查询里引用的
// catalog_revision_id 列表达式；没有受保护摘要时返回空字符串和 nil 参数，不影响原有查询。
func protectedBlobClause(revisionColumn string, blobs []domain.ContentBlobRef) (string, []any) {
	if len(blobs) == 0 {
		return "", nil
	}
	conditions := make([]string, 0, len(blobs))
	args := make([]any, 0, len(blobs)*2)
	for _, blob := range blobs {
		conditions = append(conditions, "(pb.algorithm=? AND pb.digest=?)")
		args = append(args, blob.Algorithm, blob.Digest)
	}
	return fmt.Sprintf(" AND NOT EXISTS (SELECT 1 FROM content_blobs pb WHERE pb.catalog_revision_id=%s AND (%s))",
		revisionColumn, strings.Join(conditions, " OR ")), args
}

// GarbageCollect 回收超过保留期且未被活动 publication、游标租约或 Blob
// 读取租约保护的查询快照。FTS5 表不受外键级联管理，必须与对应 Overlay
// revision 在同一事务中显式删除。
func (s *Store) GarbageCollect(ctx context.Context, retention time.Duration) (GCResult, error) {
	return s.garbageCollect(ctx, retention, nil)
}

func (s *Store) garbageCollect(ctx context.Context, retention time.Duration, protectedBlobs []domain.ContentBlobRef) (GCResult, error) {
	if retention < 0 {
		return GCResult{}, fault.New(fault.CodeValidation, false, nil)
	}
	now := s.clock.Now().UTC().Unix()
	cutoff := s.clock.Now().UTC().Add(-retention).Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GCResult{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()

	var result GCResult
	if result.ExpiredQueryLeases, err = deleteCount(ctx, tx,
		"DELETE FROM query_publication_leases WHERE expires_at<=?", now); err != nil {
		return GCResult{}, fault.New(fault.CodeInternal, true, err)
	}
	if result.ExpiredBlobLeases, err = deleteCount(ctx, tx,
		"DELETE FROM blob_read_leases WHERE expires_at<=?", now); err != nil {
		return GCResult{}, fault.New(fault.CodeInternal, true, err)
	}

	publicationProtectedClause, publicationProtectedArgs := protectedBlobClause("q.catalog_revision_id", protectedBlobs)
	type snapshot struct{ publication, catalog, overlay string }
	rows, err := tx.QueryContext(ctx, `SELECT q.query_publication_id, q.catalog_revision_id, q.overlay_revision_id
FROM query_publications q
LEFT JOIN active_query_publication a ON a.query_publication_id=q.query_publication_id
WHERE a.query_publication_id IS NULL AND q.created_at<=?
AND NOT EXISTS (
  SELECT 1 FROM query_publication_leases l
  WHERE l.query_publication_id=q.query_publication_id AND l.expires_at>?
)
AND NOT EXISTS (
  SELECT 1 FROM content_blobs b JOIN blob_read_leases l
    ON l.blob_algorithm=b.algorithm AND l.blob_digest=b.digest
  WHERE b.catalog_revision_id=q.catalog_revision_id AND l.expires_at>?
)`+publicationProtectedClause+`
ORDER BY q.created_at, q.query_publication_id`, append([]any{cutoff, now, now}, publicationProtectedArgs...)...)
	if err != nil {
		return GCResult{}, fault.New(fault.CodeInternal, true, err)
	}
	var snapshots []snapshot
	for rows.Next() {
		var item snapshot
		if err := rows.Scan(&item.publication, &item.catalog, &item.overlay); err != nil {
			rows.Close()
			return GCResult{}, fault.New(fault.CodeInternal, true, err)
		}
		snapshots = append(snapshots, item)
	}
	if err := rows.Close(); err != nil {
		return GCResult{}, fault.New(fault.CodeInternal, true, err)
	}

	for _, item := range snapshots {
		if _, err := tx.ExecContext(ctx, "DELETE FROM query_publications WHERE query_publication_id=?", item.publication); err != nil {
			return GCResult{}, fault.New(fault.CodeInternal, true, err)
		}
		result.Publications++
		var references int
		if err := tx.QueryRowContext(ctx,
			"SELECT count(*) FROM query_publications WHERE catalog_revision_id=? AND overlay_revision_id=?",
			item.catalog, item.overlay).Scan(&references); err != nil {
			return GCResult{}, fault.New(fault.CodeInternal, true, err)
		}
		if references == 0 {
			if _, err := tx.ExecContext(ctx,
				"DELETE FROM work_search WHERE catalog_revision_id=? AND overlay_revision_id=?",
				item.catalog, item.overlay); err != nil {
				return GCResult{}, fault.New(fault.CodeInternal, true, err)
			}
			count, err := deleteCount(ctx, tx,
				"DELETE FROM overlay_projection_revisions WHERE catalog_revision_id=? AND overlay_revision_id=?",
				item.catalog, item.overlay)
			if err != nil {
				return GCResult{}, fault.New(fault.CodeInternal, true, err)
			}
			result.OverlayRevisions += count
		}
	}

	revisionProtectedClause, revisionProtectedArgs := protectedBlobClause("catalog_revisions.catalog_revision_id", protectedBlobs)
	result.CatalogRevisions, err = deleteCount(ctx, tx, `DELETE FROM catalog_revisions
WHERE status IN ('published', 'aborted') AND created_at<=?
AND NOT EXISTS (SELECT 1 FROM query_publications q WHERE q.catalog_revision_id=catalog_revisions.catalog_revision_id)
AND NOT EXISTS (
  SELECT 1 FROM content_blobs b JOIN blob_read_leases l
    ON l.blob_algorithm=b.algorithm AND l.blob_digest=b.digest
  WHERE b.catalog_revision_id=catalog_revisions.catalog_revision_id AND l.expires_at>?
)`+revisionProtectedClause, append([]any{cutoff, now}, revisionProtectedArgs...)...)
	if err != nil {
		return GCResult{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return GCResult{}, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

// GarbageCollectWithOptions 在已有 GC 保护之外收敛遗留 staging candidate。活动 Job 的
// candidate 永不 abort；DryRun 只返回可处理数量，供维护 API 做空间和影响预览。
func (s *Store) GarbageCollectWithOptions(ctx context.Context, options GCOptions) (GCResult, error) {
	if options.Retention < 0 {
		return GCResult{}, fault.New(fault.CodeValidation, false, nil)
	}
	cutoff := s.clock.Now().UTC().Add(-options.Retention).Unix()
	args := []any{cutoff}
	query := `SELECT count(*) FROM catalog_revisions WHERE status='staging' AND created_at<=?`
	if len(options.ActiveJobIDs) > 0 {
		placeholders := make([]string, len(options.ActiveJobIDs))
		for index, id := range options.ActiveJobIDs {
			placeholders[index] = "?"
			args = append(args, id)
		}
		query += " AND job_id NOT IN (" + strings.Join(placeholders, ",") + ")"
	}
	var stale int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&stale); err != nil {
		return GCResult{}, fault.New(fault.CodeInternal, true, err)
	}
	skipped := 0
	if len(options.ActiveJobIDs) > 0 {
		activeArgs := []any{cutoff}
		placeholders := make([]string, len(options.ActiveJobIDs))
		for index, id := range options.ActiveJobIDs {
			placeholders[index] = "?"
			activeArgs = append(activeArgs, id)
		}
		activeQuery := `SELECT count(*) FROM catalog_revisions WHERE status='staging' AND created_at<=? AND job_id IN (` + strings.Join(placeholders, ",") + `)`
		if err := s.db.QueryRowContext(ctx, activeQuery, activeArgs...).Scan(&skipped); err != nil {
			return GCResult{}, fault.New(fault.CodeInternal, true, err)
		}
	}
	result := GCResult{DryRun: options.DryRun, StagingAborted: stale, SkippedActive: skipped}
	if options.DryRun {
		return result, nil
	}
	if stale > 0 {
		args = []any{cutoff}
		query = `UPDATE catalog_revisions SET status='aborted' WHERE status='staging' AND created_at<=?`
		if len(options.ActiveJobIDs) > 0 {
			placeholders := make([]string, len(options.ActiveJobIDs))
			for index, id := range options.ActiveJobIDs {
				placeholders[index] = "?"
				args = append(args, id)
			}
			query += " AND job_id NOT IN (" + strings.Join(placeholders, ",") + ")"
		}
		if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
			return GCResult{}, fault.New(fault.CodeInternal, true, err)
		}
	}
	cleaned, err := s.garbageCollect(ctx, options.Retention, options.ProtectedBlobs)
	if err != nil {
		return GCResult{}, err
	}
	cleaned.StagingAborted = result.StagingAborted
	cleaned.SkippedActive = result.SkippedActive
	return cleaned, nil
}

func (s *Store) Checkpoint(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func (s *Store) Vacuum(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "VACUUM"); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

func deleteCount(ctx context.Context, tx *sql.Tx, query string, args ...any) (int, error) {
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return int(count), err
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
(catalog_revision_id, source_id, source_key, title, creator, tags_json, filenames_text, provider_id, external_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, candidate.CatalogRevisionID, work.SourceID, work.SourceKey,
			sourceTitle, work.Creator, string(sourceTagsJSON), string(filenamesJSON), work.ProviderID, work.ExternalID); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_projections
(catalog_revision_id, overlay_revision_id, work_id, source_id, source_key, library_id, title, creator, tags_json, filenames_text,
 normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text, sort_title_key, hidden,
 search_title_norm, search_creator_norm, search_tags_norm, search_filenames_norm)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, candidate.CatalogRevisionID, candidate.OverlayRevisionID, work.WorkID, work.SourceID, work.SourceKey, work.LibraryID, work.Title, work.Creator, string(tagsJSON), string(filenamesJSON), document.NormalizedOriginal, document.CJKTokens, document.LatinTokens, document.SortTitleKey, hidden,
			document.TitleNorm, document.CreatorNorm, document.TagsNorm, document.FilenamesNorm); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		if work.CreatorID != "" {
			sourceCreatorName := work.SourceCreatorName
			if sourceCreatorName == "" {
				sourceCreatorName = work.Creator
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO source_creators
(catalog_revision_id, source_id, source_key, provider_id, external_id, name)
VALUES (?, ?, ?, ?, ?, ?)`, candidate.CatalogRevisionID, work.SourceID, work.CreatorSourceKey,
				work.CreatorProviderID, work.CreatorExternalID, sourceCreatorName); err != nil {
				return fault.New(fault.CodeInternal, true, err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO creator_projections
(catalog_revision_id, overlay_revision_id, creator_id, name, sort_name_key)
VALUES (?, ?, ?, ?, ?)`, candidate.CatalogRevisionID, candidate.OverlayRevisionID,
				work.CreatorID, work.Creator, querytext.NaturalSortKey(work.Creator)); err != nil {
				return fault.New(fault.CodeInternal, true, err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO work_creator_relations
(catalog_revision_id, overlay_revision_id, work_id, creator_id, role, ordinal)
VALUES (?, ?, ?, ?, 'primary', 0)`, candidate.CatalogRevisionID, candidate.OverlayRevisionID,
				work.WorkID, work.CreatorID); err != nil {
				return fault.New(fault.CodeInternal, true, err)
			}
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
		state := item.ContentVerificationState
		if state == "" {
			state = ContentVerificationStateContentVerified
		}
		verified := state == ContentVerificationStateContentVerified
		var lastConfirmedAt any
		if !item.LastConfirmedAt.IsZero() {
			lastConfirmedAt = item.LastConfirmedAt.UTC().Unix()
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO source_media
(catalog_revision_id, source_id, source_key, work_source_key, relative_path, media_kind, mime_type, size_bytes, rule_key,
 mtime_ns, platform_identity_kind, platform_identity_value, container_signature, content_verification_state,
 last_confirmed_algorithm, last_confirmed_digest, last_confirmed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, candidate.CatalogRevisionID, item.SourceID, item.SourceKey,
			item.WorkSourceKey, item.RelativePath, item.Kind, item.MIME, item.Size, item.RuleKey,
			item.MTimeNanos, item.PlatformIdentityKind, item.PlatformIdentityValue, item.ContainerSignature, state,
			item.LastConfirmedAlgorithm, item.LastConfirmedDigest, lastConfirmedAt); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		// 未确认媒体没有已确认字节，不写入 content_blobs/file_locations；这两张表继续只
		// 承载真正完成完整 SHA-256 确认的内容事实，不引入伪造或占位 digest。
		if verified {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO content_blobs
(catalog_revision_id, algorithm, digest, size_bytes) VALUES (?, ?, ?, ?)`, candidate.CatalogRevisionID, item.Algorithm, item.Digest, item.Size); err != nil {
				return fault.New(fault.CodeInternal, true, err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO file_locations
(catalog_revision_id, source_id, source_key, location_key, relative_path, algorithm, digest, status)
VALUES (?, ?, ?, ?, ?, ?, ?, 'present')`, candidate.CatalogRevisionID, item.SourceID, item.SourceKey, item.LocationKey, item.RelativePath, item.Algorithm, item.Digest); err != nil {
				return fault.New(fault.CodeInternal, true, err)
			}
		}
		// location_status 只表达位置可用性；扫描发现的文件位置始终 present，无论内容是否
		// 已完整确认，两者是正交语义，不得再把 located_unverified 混进 location_status。
		locationStatus := "present"
		var verifiedAt any
		if verified && !item.LastConfirmedAt.IsZero() {
			verifiedAt = item.LastConfirmedAt.UTC().Unix()
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO media_projections
(catalog_revision_id, overlay_revision_id, media_id, work_id, source_id, source_key, relative_path,
 media_kind, mime_type, size_bytes, algorithm, digest, location_status, content_verification_state, verified_at, ordinal, base_ordinal)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, candidate.CatalogRevisionID, candidate.OverlayRevisionID, item.MediaID, item.WorkID, item.SourceID, item.SourceKey, item.RelativePath, item.Kind, item.MIME, item.Size, item.Algorithm, item.Digest, locationStatus, state, verifiedAt, item.Ordinal, item.Ordinal); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

// PriorObservation 是 incremental 扫描档案复用判断所需的既往观察证据；Found=false 表示
// 当前活动 publication 中不存在该 Source 下这个相对路径的既往记录（新文件）。
type PriorObservation struct {
	Found                    bool
	Size                     int64
	MTimeNanos               int64
	ContentVerificationState string
	Algorithm                string
	Digest                   string
	// LastConfirmedAt 是既往完成完整 SHA-256 确认的时间；复用摘要时必须原样保留这个时间，
	// 不得因为本次扫描只是复用旧摘要就把它当作新一轮确认时间。
	LastConfirmedAt time.Time
}

// LookupPriorObservation 在当前活动 query publication 内按 (source_id, relative_path) 查找
// 既往观察，供 incremental 扫描档案组合 size/mtime 证据判断是否可复用已确认 digest，
// 不产生任何文件 I/O。真实规模下依赖 source_media_identity_idx 索引，不做全表扫描。
func (s *Store) LookupPriorObservation(ctx context.Context, sourceID, relativePath string) (PriorObservation, error) {
	var size, mtimeNs, lastConfirmedAt sql.NullInt64
	var state, algorithm, digest sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT m.size_bytes, m.mtime_ns, m.content_verification_state, m.last_confirmed_algorithm, m.last_confirmed_digest, m.last_confirmed_at
FROM source_media m
JOIN active_query_publication a ON a.singleton=1 JOIN query_publications q ON q.query_publication_id=a.query_publication_id
WHERE m.catalog_revision_id=q.catalog_revision_id AND m.source_id=? AND m.relative_path=?`, sourceID, relativePath).
		Scan(&size, &mtimeNs, &state, &algorithm, &digest, &lastConfirmedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PriorObservation{}, nil
	}
	if err != nil {
		return PriorObservation{}, fault.New(fault.CodeInternal, true, err)
	}
	observation := PriorObservation{
		Found: true, Size: size.Int64, MTimeNanos: mtimeNs.Int64,
		ContentVerificationState: state.String, Algorithm: algorithm.String, Digest: digest.String,
	}
	if lastConfirmedAt.Valid {
		observation.LastConfirmedAt = time.Unix(lastConfirmedAt.Int64, 0).UTC()
	}
	return observation, nil
}

// LookupObservationAt 在指定的具体 query publication 内按 (source_id, relative_path)
// 查找既往观察，是 VerificationTarget 冻结身份在执行阶段真正校验所需的权威入口：必须
// 读取请求当时冻结的那个 publication，绝不能像 LookupPriorObservation 那样隐式改读
// 执行时刻恰好处于 active 的 publication——两者在 publication 切换后可能描述完全不同
// 的 observation，混用会让确认结果绑定到用户从未请求过的快照。publicationID 必须是
// 调用方已经确认存在的合法 publication（例如经 PublicationByID 或与 Current() 比对）；
// 若该 publication 此刻已经不存在（GC 或从未存在），返回的 Found=false 与"该
// publication 内没有此 SourceMedia 记录"无法区分，调用方需要区分二者时应自行先调用
// PublicationByID。
func (s *Store) LookupObservationAt(ctx context.Context, publicationID, sourceID, relativePath string) (PriorObservation, error) {
	var size, mtimeNs, lastConfirmedAt sql.NullInt64
	var state, algorithm, digest sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT m.size_bytes, m.mtime_ns, m.content_verification_state, m.last_confirmed_algorithm, m.last_confirmed_digest, m.last_confirmed_at
FROM source_media m
JOIN query_publications q ON q.query_publication_id=?
WHERE m.catalog_revision_id=q.catalog_revision_id AND m.source_id=? AND m.relative_path=?`, publicationID, sourceID, relativePath).
		Scan(&size, &mtimeNs, &state, &algorithm, &digest, &lastConfirmedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PriorObservation{}, nil
	}
	if err != nil {
		return PriorObservation{}, fault.New(fault.CodeInternal, true, err)
	}
	observation := PriorObservation{
		Found: true, Size: size.Int64, MTimeNanos: mtimeNs.Int64,
		ContentVerificationState: state.String, Algorithm: algorithm.String, Digest: digest.String,
	}
	if lastConfirmedAt.Valid {
		observation.LastConfirmedAt = time.Unix(lastConfirmedAt.Int64, 0).UTC()
	}
	return observation, nil
}

// LocateBlobFile 把一个 ContentBlob 引用（algorithm+digest）解析为一个仍然 present 的
// 源文件位置。DerivedAsset 的输入按内容寻址（见 derivedjob.Request 只携带 Blob，不携带
// publication 引用）：创建请求时锁定的是这个 Blob，不是创建时刻恰好 active 的那个
// publication；因此这里刻意不限定只查当前 active_query_publication——只要该 digest 在
// 任意一个仍未被 GC 回收的 catalog_revision 中还有 present occurrence，就是有效输入，
// active publication 在请求排队后切换到其它 revision 不应改变这次生成的可解析性。
// 调用方必须在生成前后持有覆盖该 digest 的 media.BlobReadLease，防止这里读到的
// occurrence 所在 revision 在生成完成前被 GarbageCollect 回收（GC 对任一未过期
// blob_read_lease 覆盖的 revision 一律跳过，见 Store.GarbageCollect）。多个 revision
// 都仍持有该 digest 时按 catalog_revision_id（UUIDv7，天然按创建时间排序）取最新一个，
// 只是为了确定性，不代表其它仍然有效的 occurrence 不可用。
func (s *Store) LocateBlobFile(ctx context.Context, algorithm, digest string) (sourceID, relativePath string, size int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT m.source_id, m.relative_path, m.size_bytes
FROM media_projections m
JOIN file_locations f ON f.catalog_revision_id=m.catalog_revision_id AND f.source_id=m.source_id AND f.source_key=m.source_key AND f.status='present'
WHERE m.algorithm=? AND m.digest=? AND m.content_verification_state='content_verified'
ORDER BY m.catalog_revision_id DESC
LIMIT 1`, algorithm, digest).Scan(&sourceID, &relativePath, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", 0, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return "", "", 0, fault.New(fault.CodeInternal, true, err)
	}
	return sourceID, relativePath, size, nil
}

// queryDependencyBackfillMetaKey 标记 migration 00010（v9→v10，新增 favorite/progress/
// search_*_norm 快照列）后是否已经触发过一次性 Overlay 重投影回填。ALTER TABLE ADD
// COLUMN 只能给既有 revision 的这些新列填入静态默认值（0/”），已经发布的旧 revision
// 不会自动重新计算；这些字段真正的权威数据（favorite/progress 来自 control.db，
// search_*_norm 可从同一 revision 里已有的 title/creator/tags_json/filenames_text 重新
// 计算）必须通过一次真实的 Overlay 投影 Job 重新物化到当前 active revision，否则升级
// 后重启的服务会用默认零值静默提供错误的过滤/排序/高亮结果。
const queryDependencyBackfillMetaKey = "query_dependency_backfill_triggered"

// NeedsQueryDependencyBackfill 报告是否仍需要触发一次性回填：只在从未触发过时为真。
// 触发后无论当时是否存在 active publication 都会记录标记（见
// MarkQueryDependencyBackfillTriggered），避免在此后的每次启动重复判断整个 Catalog；
// 全新安装从建库起就使用已经包含这些列的 schema，没有需要回填的历史数据，检查本身
// 代价是一次单行 SELECT，可以安全地在每次启动无条件执行。
func (s *Store) NeedsQueryDependencyBackfill(ctx context.Context) (bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM gallery_catalog_meta WHERE key=?", queryDependencyBackfillMetaKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fault.New(fault.CodeInternal, true, err)
	}
	return false, nil
}

// MarkQueryDependencyBackfillTriggered 记录一次性回填已经触发，此后启动不再重复检查。
// 调用方在成功排队（或确认当前没有 active publication、无需回填）之后调用；即使排队
// 与标记之间跨进程重启也是安全的——重新触发只会让 EnqueueOverlayProjectionTx 合并出
// 一个等价的 no-op 投影 Job，不会产生错误结果或第二套状态机。
func (s *Store) MarkQueryDependencyBackfillTriggered(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO gallery_catalog_meta (key, value) VALUES (?, '1')
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, queryDependencyBackfillMetaKey)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

// SourcePublished 报告该 Source 是否已在当前活动 query publication 中拥有至少一条
// Source-derived 数据，即是否已经完成过至少一次成功扫描发布。尚无任何活动 publication
// （产品首次启动、从未有任何 Source 发布成功）视为未发布，不是错误。
func (s *Store) SourcePublished(ctx context.Context, sourceID string) (bool, error) {
	publication, err := s.Current(ctx)
	if err != nil {
		if isNotFoundErr(err) {
			return false, nil
		}
		return false, err
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM source_works w
WHERE w.catalog_revision_id=? AND w.source_id=?`, publication.CatalogRevisionID, sourceID).Scan(&count); err != nil {
		return false, fault.New(fault.CodeInternal, true, err)
	}
	return count > 0, nil
}

func isNotFoundErr(err error) bool {
	var structured *fault.Error
	return errors.As(err, &structured) && structured.Code == fault.CodeNotFound
}

func cloneUnchangedSources(ctx context.Context, tx *sql.Tx, candidate Candidate) error {
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO source_works SELECT ?, w.source_id, w.source_key, w.title, w.creator, w.tags_json, w.filenames_text, w.provider_id, w.external_id FROM source_works w
JOIN active_query_publication a ON a.singleton=1 JOIN query_publications q ON q.query_publication_id=a.query_publication_id
WHERE w.catalog_revision_id=q.catalog_revision_id AND w.source_id<>?`, []any{candidate.CatalogRevisionID, candidate.SourceID}},
		{`INSERT INTO source_media SELECT ?, m.source_id, m.source_key, m.work_source_key, m.relative_path, m.media_kind, m.mime_type, m.size_bytes, m.rule_key,
m.mtime_ns, m.platform_identity_kind, m.platform_identity_value, m.container_signature, m.content_verification_state,
m.last_confirmed_algorithm, m.last_confirmed_digest, m.last_confirmed_at FROM source_media m
JOIN active_query_publication a ON a.singleton=1 JOIN query_publications q ON q.query_publication_id=a.query_publication_id
WHERE m.catalog_revision_id=q.catalog_revision_id AND m.source_id<>?`, []any{candidate.CatalogRevisionID, candidate.SourceID}},
		{`INSERT INTO source_creators SELECT ?, c.source_id, c.source_key, c.provider_id, c.external_id, c.name FROM source_creators c
JOIN active_query_publication a ON a.singleton=1 JOIN query_publications q ON q.query_publication_id=a.query_publication_id
WHERE c.catalog_revision_id=q.catalog_revision_id AND c.source_id<>?`, []any{candidate.CatalogRevisionID, candidate.SourceID}},
		{`INSERT INTO content_blobs SELECT DISTINCT ?, b.algorithm, b.digest, b.size_bytes FROM content_blobs b
JOIN active_query_publication a ON a.singleton=1 JOIN query_publications q ON q.query_publication_id=a.query_publication_id
JOIN media_projections m ON m.catalog_revision_id=q.catalog_revision_id AND m.overlay_revision_id=q.overlay_revision_id AND m.algorithm=b.algorithm AND m.digest=b.digest
WHERE b.catalog_revision_id=q.catalog_revision_id AND m.source_id<>?`, []any{candidate.CatalogRevisionID, candidate.SourceID}},
		{`INSERT INTO file_locations SELECT ?, f.source_id, f.source_key, f.location_key, f.relative_path, f.algorithm, f.digest, f.status FROM file_locations f
JOIN active_query_publication a ON a.singleton=1 JOIN query_publications q ON q.query_publication_id=a.query_publication_id
WHERE f.catalog_revision_id=q.catalog_revision_id AND f.source_id<>?`, []any{candidate.CatalogRevisionID, candidate.SourceID}},
		{`INSERT INTO work_projections SELECT ?, ?, w.work_id, w.source_id, w.source_key, w.library_id, w.title, w.creator, w.tags_json, w.filenames_text, w.normalized_original_text, w.cjk_bigram_token_text, w.latin_trigram_token_text, w.sort_title_key, w.hidden, w.favorite, w.progress, w.search_title_norm, w.search_creator_norm, w.search_tags_norm, w.search_filenames_norm FROM work_projections w
JOIN active_query_publication a ON a.singleton=1 JOIN query_publications q ON q.query_publication_id=a.query_publication_id
WHERE w.catalog_revision_id=q.catalog_revision_id AND w.overlay_revision_id=q.overlay_revision_id AND w.source_id<>?`, []any{candidate.CatalogRevisionID, candidate.OverlayRevisionID, candidate.SourceID}},
		{`INSERT OR IGNORE INTO creator_projections SELECT ?, ?, c.creator_id, c.name, c.sort_name_key FROM creator_projections c
JOIN active_query_publication a ON a.singleton=1 JOIN query_publications q ON q.query_publication_id=a.query_publication_id
JOIN work_creator_relations r ON r.catalog_revision_id=q.catalog_revision_id AND r.overlay_revision_id=q.overlay_revision_id AND r.creator_id=c.creator_id
JOIN work_projections w ON w.catalog_revision_id=r.catalog_revision_id AND w.overlay_revision_id=r.overlay_revision_id AND w.work_id=r.work_id
WHERE c.catalog_revision_id=q.catalog_revision_id AND c.overlay_revision_id=q.overlay_revision_id AND w.source_id<>?`, []any{candidate.CatalogRevisionID, candidate.OverlayRevisionID, candidate.SourceID}},
		{`INSERT INTO media_projections
(catalog_revision_id, overlay_revision_id, media_id, work_id, source_id, source_key, relative_path,
 media_kind, mime_type, size_bytes, algorithm, digest, location_status, ordinal, hidden, base_ordinal,
 content_verification_state, verified_at)
SELECT ?, ?, m.media_id, m.work_id, m.source_id, m.source_key, m.relative_path, m.media_kind, m.mime_type, m.size_bytes, m.algorithm, m.digest, m.location_status, m.ordinal, m.hidden, m.base_ordinal, m.content_verification_state, m.verified_at FROM media_projections m
JOIN active_query_publication a ON a.singleton=1 JOIN query_publications q ON q.query_publication_id=a.query_publication_id
WHERE m.catalog_revision_id=q.catalog_revision_id AND m.overlay_revision_id=q.overlay_revision_id AND m.source_id<>?`, []any{candidate.CatalogRevisionID, candidate.OverlayRevisionID, candidate.SourceID}},
		{`INSERT INTO work_search (catalog_revision_id, overlay_revision_id, work_id, normalized_original_text, cjk_bigram_token_text, latin_trigram_token_text)
SELECT ?, ?, s.work_id, s.normalized_original_text, s.cjk_bigram_token_text, s.latin_trigram_token_text FROM work_search s
JOIN active_query_publication a ON a.singleton=1 JOIN query_publications q ON q.query_publication_id=a.query_publication_id
JOIN work_projections w ON w.catalog_revision_id=q.catalog_revision_id AND w.overlay_revision_id=q.overlay_revision_id AND w.work_id=s.work_id
WHERE s.catalog_revision_id=q.catalog_revision_id AND s.overlay_revision_id=q.overlay_revision_id AND w.source_id<>?`, []any{candidate.CatalogRevisionID, candidate.OverlayRevisionID, candidate.SourceID}},
		{`INSERT INTO work_creator_relations SELECT ?, ?, r.work_id, r.creator_id, r.role, r.ordinal FROM work_creator_relations r
JOIN active_query_publication a ON a.singleton=1 JOIN query_publications q ON q.query_publication_id=a.query_publication_id
JOIN work_projections w ON w.catalog_revision_id=r.catalog_revision_id AND w.overlay_revision_id=r.overlay_revision_id AND w.work_id=r.work_id
WHERE r.catalog_revision_id=q.catalog_revision_id AND r.overlay_revision_id=q.overlay_revision_id AND w.source_id<>?`, []any{candidate.CatalogRevisionID, candidate.OverlayRevisionID, candidate.SourceID}},
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
