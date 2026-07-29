package catalog

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
)

// AggregateCoverScope 是聚合封面的作用域类型。
const (
	AggregateScopeCreator = "creator"
	AggregateScopeSource  = "source"
	AggregateScopeLibrary = "library"
)

// AggregateCover 是一个作用域的聚合封面事实。PublishedAtNanos 是被选中作品的发布时刻，
// 保留它使聚合结果可被解释与复核。
type AggregateCover struct {
	ScopeKind        string
	ScopeID          string
	CoverMediaID     string
	PublishedAtNanos int64
}

// computeAggregateCovers 从**完整**的 work_projections 集合整体重算三级聚合封面。
//
// 必须整体重算而不是增量维护，有两个各自独立的原因：
//
//  1. creator_projections 用 `INSERT OR IGNORE` 逐 Work 写入，等于「第一个遇到的 Work 决定作者」，
//     与「最新日期的作品决定作者封面」不是一回事；
//  2. cloneUnchangedSources 按 `source_id<>?` 继承其它 Source 的事实，而作者与资料库天然横跨多个
//     Source。单 Source 重扫后，受影响作者的聚合封面必须重算，简单继承会让它停留在旧值。
//
// 因此本函数在 candidate validation 的 IMMEDIATE 事务内、投影已经完全就位之后执行一次，先清空
// 本 revision 的既有聚合行再重建，使结果只由当前投影决定，不受执行次数与历史残留影响。验证成功
// 后写入持久封印，短 publication 事务只确认该封印；这既保持聚合与快照同代次，也避免全量窗口
// 计算进入目标 P95 <250 ms 的指针切换事务。
//
// **层级依赖**：作者取其最新作品的有效封面；平台（Source）取该 Source 内的最新作者；资料库取
// 其最新平台。Creator 与 Source 共享一次物化的候选集：前者按 Creator 全局分组，后者按 Source
// 分组。Source 不能直接复用全局 Creator 胜出行，否则同一 Creator 横跨多个 Source 时，会让
// Source A 的封面引用 Source B 的媒体，既破坏资源边界，也会让只含该共享 Creator 的 Source A
// 丢失自身聚合封面。Library 则可以安全复用 Source 已算好的 published_at_ns。
//
// **tie-break**：`ORDER BY published_at_ns DESC, <id> DESC`。没有发布时间的作品 published_at_ns
// 为 0，因此永远排在有日期的作品之后；全都没有日期时由 ID 决定，仍然确定。缺少确定性 tie-break
// 会让聚合封面在两次重扫之间漂移，违反 revision 快照的一致承诺。
//
// **有效封面而非规则封面**：这里读的是 `cover_media_id`（已解析完 CustomCover 回退），因此用户为
// 某个作品设置的自定义封面会自然向上传播到它所属的作者/平台/资料库。这是最不意外的行为，也让
// 聚合封面与作品列表看到的封面一致。
func computeAggregateCovers(ctx context.Context, tx *sql.Tx, catalogRevisionID, overlayRevisionID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM aggregate_cover_projections
WHERE catalog_revision_id=? AND overlay_revision_id=?`, catalogRevisionID, overlayRevisionID); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM creator_source_cover_projections
WHERE catalog_revision_id=? AND overlay_revision_id=?`, catalogRevisionID, overlayRevisionID); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}

	// 先把关系表与 WorkProjection 恰好连接一次，持久化每个 Creator/Source 内的唯一最佳
	// 候选。它既是全局聚合的窄输入，也是逐主体授权回退的 publication 冻结事实。
	if _, err := tx.ExecContext(ctx, aggregateCreatorSourceCandidateStatement,
		catalogRevisionID, overlayRevisionID); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}

	// Creator 与 Source 两级共用上一步的窄候选，不再连接宽 WorkProjection。Source 排序的
	// (published_at_ns, creator_id, work_id) 与「先为 Source 内每个 Creator 选其最新 Work，
	// 再从这些 Creator 中选最新者」严格等价。
	if _, err := tx.ExecContext(ctx, aggregateCreatorSourceStatement,
		catalogRevisionID, overlayRevisionID, catalogRevisionID, overlayRevisionID); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}

	// 资料库级：取该 Library 下最新的**平台**。同样复用上一步的结果。
	if _, err := tx.ExecContext(ctx, `INSERT INTO aggregate_cover_projections
(catalog_revision_id, overlay_revision_id, scope_kind, scope_id,
 cover_media_id, published_at_ns, source_id)
SELECT catalog_revision_id, overlay_revision_id, 'library', library_id,
       cover_media_id, published_at_ns, source_id
FROM (
    SELECT a.catalog_revision_id,
           a.overlay_revision_id,
           m.library_id,
           a.cover_media_id,
           a.published_at_ns,
           a.source_id,
           row_number() OVER (
               PARTITION BY a.catalog_revision_id, a.overlay_revision_id, m.library_id
               ORDER BY a.published_at_ns DESC, a.scope_id DESC
           ) AS rank_in_scope
    FROM aggregate_cover_projections AS a
    JOIN catalog_revision_sources AS m
      ON m.catalog_revision_id = a.catalog_revision_id
     AND m.source_id = a.scope_id
    WHERE a.catalog_revision_id = ? AND a.overlay_revision_id = ? AND a.scope_kind = 'source'
)
WHERE rank_in_scope = 1`, catalogRevisionID, overlayRevisionID); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	return nil
}

// aggregateCreatorSourceCandidateStatement 只连接一次 Work/Creator 基础事实，并把每个
// Creator/Source 内的最佳 Work 收窄为一行。保持为包级常量供生产计划测试直接检查。
const aggregateCreatorSourceCandidateStatement = `INSERT INTO creator_source_cover_projections
(catalog_revision_id, overlay_revision_id, creator_id, source_id,
 cover_media_id, published_at_ns, work_id)
SELECT catalog_revision_id, overlay_revision_id, creator_id, source_id,
       cover_media_id, published_at_ns, work_id
FROM (
    SELECT r.catalog_revision_id,
           r.overlay_revision_id,
           r.creator_id,
           w.source_id,
           w.cover_media_id,
           w.published_at_ns,
           w.work_id,
           row_number() OVER (
               PARTITION BY r.catalog_revision_id, r.overlay_revision_id, r.creator_id, w.source_id
               ORDER BY w.published_at_ns DESC, w.work_id DESC
           ) AS rank_in_scope
    FROM work_creator_relations AS r
    JOIN work_projections AS w
      ON w.catalog_revision_id = r.catalog_revision_id
     AND w.overlay_revision_id = r.overlay_revision_id
     AND w.work_id = r.work_id
    WHERE r.catalog_revision_id = ?
      AND r.overlay_revision_id = ?
      AND w.cover_media_id <> ''
)
WHERE rank_in_scope = 1`

// aggregateCreatorSourceStatement 从持久窄候选同时生成 Creator 与 Source 两级聚合。
// 不得把 WorkProjection 或 work_creator_relations 重新引入这一步。
const aggregateCreatorSourceStatement = `WITH creator_ranked AS (
    SELECT catalog_revision_id,
           overlay_revision_id,
           creator_id AS scope_id,
           cover_media_id,
           published_at_ns,
           source_id,
           row_number() OVER (
               PARTITION BY catalog_revision_id, overlay_revision_id, creator_id
               ORDER BY published_at_ns DESC, work_id DESC
           ) AS rank_in_scope
    FROM creator_source_cover_projections
    WHERE catalog_revision_id=? AND overlay_revision_id=?
),
source_ranked AS (
    SELECT catalog_revision_id,
           overlay_revision_id,
           source_id AS scope_id,
           cover_media_id,
           published_at_ns,
           source_id,
           row_number() OVER (
               PARTITION BY catalog_revision_id, overlay_revision_id, source_id
               ORDER BY published_at_ns DESC, creator_id DESC, work_id DESC
           ) AS rank_in_scope
    FROM creator_source_cover_projections
    WHERE catalog_revision_id=? AND overlay_revision_id=?
)
INSERT INTO aggregate_cover_projections
(catalog_revision_id, overlay_revision_id, scope_kind, scope_id,
 cover_media_id, published_at_ns, source_id)
SELECT catalog_revision_id, overlay_revision_id, 'creator', scope_id,
       cover_media_id, published_at_ns, source_id
FROM creator_ranked
WHERE rank_in_scope = 1
UNION ALL
SELECT catalog_revision_id, overlay_revision_id, 'source', scope_id,
       cover_media_id, published_at_ns, source_id
FROM source_ranked
WHERE rank_in_scope = 1`

// AggregateCoverAt 读取某个 publication 下一个作用域的聚合封面。作用域不存在或没有可用封面时
// 返回零值而不是错误：「这个作者还没有任何带封面的作品」是常态，不是失败。
func (s *Store) AggregateCoverAt(ctx context.Context, publicationID, scopeKind, scopeID string) (Publication, AggregateCover, error) {
	publication, err := s.resolvePublication(ctx, publicationID)
	if err != nil {
		return Publication{}, AggregateCover{}, err
	}
	result := AggregateCover{ScopeKind: scopeKind, ScopeID: scopeID}
	err = s.db.QueryRowContext(ctx, `SELECT cover_media_id, published_at_ns FROM aggregate_cover_projections
WHERE catalog_revision_id=? AND overlay_revision_id=? AND scope_kind=? AND scope_id=?`,
		publication.CatalogRevisionID, publication.OverlayRevisionID, scopeKind, scopeID).
		Scan(&result.CoverMediaID, &result.PublishedAtNanos)
	if err != nil && err != sql.ErrNoRows {
		return Publication{}, AggregateCover{}, fault.New(fault.CodeInternal, true, err)
	}
	return publication, result, nil
}

// AggregateCoversAt 批量读取某个 publication 下某一类作用域的全部聚合封面，供列表端点使用，
// 避免逐项查询产生 N+1。
func (s *Store) AggregateCoversAt(ctx context.Context, publicationID, scopeKind string) (map[string]AggregateCover, error) {
	publication, err := s.resolvePublication(ctx, publicationID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT scope_id, cover_media_id, published_at_ns
FROM aggregate_cover_projections
WHERE catalog_revision_id=? AND overlay_revision_id=? AND scope_kind=?`,
		publication.CatalogRevisionID, publication.OverlayRevisionID, scopeKind)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	result := map[string]AggregateCover{}
	for rows.Next() {
		item := AggregateCover{ScopeKind: scopeKind}
		if err := rows.Scan(&item.ScopeID, &item.CoverMediaID, &item.PublishedAtNanos); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result[item.ScopeID] = item
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

// SourceIDsAt 返回一个 publication 冻结的完整 Source 成员集合。授权层先对这组稳定成员做
// 批量 effective capability 判定，再把允许集合交给 AggregateCoversForSourcesAt 重选封面；
// 不得从 control.db 的当前 Source 列表猜测历史 publication 成员。
func (s *Store) SourceIDsAt(ctx context.Context, publicationID string) (Publication, []string, error) {
	publication, err := s.resolvePublication(ctx, publicationID)
	if err != nil {
		return Publication{}, nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT source_id
FROM catalog_revision_sources
WHERE catalog_revision_id=?
ORDER BY source_id`, publication.CatalogRevisionID)
	if err != nil {
		return Publication{}, nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	sourceIDs := make([]string, 0)
	for rows.Next() {
		var sourceID string
		if err := rows.Scan(&sourceID); err != nil {
			return Publication{}, nil, fault.New(fault.CodeInternal, true, err)
		}
		sourceIDs = append(sourceIDs, sourceID)
	}
	if err := rows.Err(); err != nil {
		return Publication{}, nil, fault.New(fault.CodeInternal, true, err)
	}
	return publication, sourceIDs, nil
}

// AggregateCoversForSourcesAt 在一个不可变 publication 内，只使用调用方已经授权的 Source
// 重新选择聚合封面。预计算的 Creator/Library 行只保存全局胜出项；直接把它们附到资源限定
// 主体的 DTO 上，会在胜出媒体所在 Source 被 deny、落在 Token scope 外或缺少 media.read 时
// 泄露 CanonicalMedia ID，也无法回退到下一条仍可见的候选。
//
// Creator 从允许 Source 中的 Work 候选按正式 tie-break 重选；Library 复用 Source-local 聚合行，
// 再从允许 Source 中重选代表平台。这样既保留既有确定性语义，也不会把授权规则写进 Catalog。
func (s *Store) AggregateCoversForSourcesAt(ctx context.Context, publicationID, scopeKind string, sourceIDs []string) (map[string]AggregateCover, error) {
	publication, err := s.resolvePublication(ctx, publicationID)
	if err != nil {
		return nil, err
	}
	result := make(map[string]AggregateCover)
	if len(sourceIDs) == 0 {
		return result, nil
	}
	if scopeKind == AggregateScopeCreator {
		return s.creatorAggregateCoversForSourcesAt(ctx, publication, sourceIDs)
	}
	sourceIDsJSON, err := json.Marshal(sourceIDs)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}

	var statement string
	switch scopeKind {
	case AggregateScopeLibrary:
		statement = `WITH allowed_sources(source_id) AS (
    SELECT DISTINCT CAST(value AS TEXT) FROM json_each(?)
), ranked AS (
    SELECT m.library_id AS scope_id,
           a.cover_media_id,
           a.published_at_ns,
           row_number() OVER (
               PARTITION BY m.library_id
               ORDER BY a.published_at_ns DESC, a.scope_id DESC
           ) AS rank_in_scope
    FROM aggregate_cover_projections AS a
    JOIN catalog_revision_sources AS m
      ON m.catalog_revision_id = a.catalog_revision_id
     AND m.source_id = a.scope_id
    JOIN allowed_sources AS allowed ON allowed.source_id = a.scope_id
    WHERE a.catalog_revision_id=? AND a.overlay_revision_id=? AND a.scope_kind='source'
)
SELECT scope_id, cover_media_id, published_at_ns
FROM ranked WHERE rank_in_scope=1
ORDER BY scope_id`
	default:
		return nil, fault.WithField(fault.CodeValidation, "scopeKind", nil)
	}
	rows, err := s.db.QueryContext(ctx, statement, string(sourceIDsJSON), publication.CatalogRevisionID, publication.OverlayRevisionID)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	for rows.Next() {
		item := AggregateCover{ScopeKind: scopeKind}
		if err := rows.Scan(&item.ScopeID, &item.CoverMediaID, &item.PublishedAtNanos); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result[item.ScopeID] = item
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

// creatorAggregateCoversForSourcesAt 只读取 publication 持久化的 Creator/Source 窄候选。
// 小 allowed 集合从 Source 索引驱动后做窄窗口；小 deny 集合直接复用仍获授权的
// 全局胜出项，只为被拒绝的 Creator 沿正式 rank 索引寻找第一条允许候选。
func (s *Store) creatorAggregateCoversForSourcesAt(ctx context.Context, publication Publication, sourceIDs []string) (map[string]AggregateCover, error) {
	sourceIDsJSON, err := json.Marshal(sourceIDs)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	var totalSources int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM catalog_revision_sources
WHERE catalog_revision_id=?`, publication.CatalogRevisionID).Scan(&totalSources); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	result := make(map[string]AggregateCover)
	statement := creatorCoversSmallAllowedStatement
	if len(sourceIDs)*2 > totalSources {
		statement = creatorCoversSmallDeniedStatement
	}
	rows, err := s.db.QueryContext(ctx, statement, string(sourceIDsJSON), publication.CatalogRevisionID, publication.OverlayRevisionID)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	for rows.Next() {
		item := AggregateCover{ScopeKind: AggregateScopeCreator}
		if err := rows.Scan(&item.ScopeID, &item.CoverMediaID, &item.PublishedAtNanos); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result[item.ScopeID] = item
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

const creatorCoversSmallAllowedStatement = `WITH allowed_sources(source_id) AS MATERIALIZED (
    SELECT DISTINCT CAST(value AS TEXT) FROM json_each(?)
), ranked AS (
    SELECT c.creator_id AS scope_id,
           c.cover_media_id,
           c.published_at_ns,
           row_number() OVER (
               PARTITION BY c.creator_id
               ORDER BY c.published_at_ns DESC, c.work_id DESC
           ) AS rank_in_scope
    FROM allowed_sources AS a
    CROSS JOIN creator_source_cover_projections AS c INDEXED BY creator_source_cover_source_idx
    WHERE c.catalog_revision_id=? AND c.overlay_revision_id=? AND c.source_id=a.source_id
)
SELECT scope_id, cover_media_id, published_at_ns
FROM ranked WHERE rank_in_scope=1
ORDER BY scope_id`

const creatorCoversSmallDeniedStatement = `WITH allowed_sources(source_id) AS MATERIALIZED (
    SELECT DISTINCT CAST(value AS TEXT) FROM json_each(?)
), global_covers AS MATERIALIZED (
    SELECT catalog_revision_id,
           overlay_revision_id,
           scope_id,
           cover_media_id,
           published_at_ns,
           source_id
    FROM aggregate_cover_projections
    WHERE catalog_revision_id=? AND overlay_revision_id=? AND scope_kind='creator'
), affected_covers AS MATERIALIZED (
    SELECT *
    FROM global_covers
    WHERE source_id NOT IN (SELECT source_id FROM allowed_sources)
)
SELECT scope_id, cover_media_id, published_at_ns
FROM global_covers
WHERE source_id IN (SELECT source_id FROM allowed_sources)
UNION ALL
SELECT a.scope_id, c.cover_media_id, c.published_at_ns
FROM affected_covers AS a
JOIN creator_source_cover_projections AS c ON c.rowid = (
    SELECT candidate.rowid
    FROM creator_source_cover_projections AS candidate INDEXED BY creator_source_cover_rank_idx
    WHERE candidate.catalog_revision_id=a.catalog_revision_id
      AND candidate.overlay_revision_id=a.overlay_revision_id
      AND candidate.creator_id=a.scope_id
      AND candidate.source_id IN (SELECT source_id FROM allowed_sources)
    ORDER BY candidate.published_at_ns DESC, candidate.work_id DESC
    LIMIT 1
)
ORDER BY scope_id`
