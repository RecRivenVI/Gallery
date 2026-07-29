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

	// Creator 与 Source 两级共用同一份窄候选集。MATERIALIZED 是这里的结构性性能约束：关系表与
	// WorkProjection 的连接只执行一次，两个窗口各自扫描窄临时结果；不得把它改回「先生成 Creator
	// 聚合，再从每个 Creator 扇出其全部作品」的二次关系扫描。
	//
	// Source 排序的 (published_at_ns, creator_id, work_id) 与「先为 Source 内每个 Creator 选其最新
	// Work，再从这些 Creator 中选最新者」严格等价，但无需建立只服务本次发布的中间持久实体。
	if _, err := tx.ExecContext(ctx, aggregateCreatorSourceStatement,
		catalogRevisionID, overlayRevisionID); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}

	// 资料库级：取该 Library 下最新的**平台**。同样复用上一步的结果。
	if _, err := tx.ExecContext(ctx, `INSERT INTO aggregate_cover_projections
(catalog_revision_id, overlay_revision_id, scope_kind, scope_id, cover_media_id, published_at_ns)
SELECT catalog_revision_id, overlay_revision_id, 'library', library_id, cover_media_id, published_at_ns
FROM (
    SELECT a.catalog_revision_id,
           a.overlay_revision_id,
           m.library_id,
           a.cover_media_id,
           a.published_at_ns,
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

// aggregateCreatorSourceStatement 同时生成 Creator 与 Source 两级聚合封面。保持为包级常量，
// 让测试可以直接对生产 SQL 执行 EXPLAIN QUERY PLAN，避免用一份手写近似 SQL 产生假保护。
const aggregateCreatorSourceStatement = `WITH candidate_covers AS MATERIALIZED (
    SELECT r.catalog_revision_id,
           r.overlay_revision_id,
           r.creator_id,
           w.source_id,
           w.work_id,
           w.cover_media_id,
           w.published_at_ns
    FROM work_creator_relations AS r
    JOIN work_projections AS w
      ON w.catalog_revision_id = r.catalog_revision_id
     AND w.overlay_revision_id = r.overlay_revision_id
     AND w.work_id = r.work_id
    WHERE r.catalog_revision_id = ?
      AND r.overlay_revision_id = ?
      AND w.cover_media_id <> ''
),
creator_ranked AS (
    SELECT catalog_revision_id,
           overlay_revision_id,
           creator_id AS scope_id,
           cover_media_id,
           published_at_ns,
           row_number() OVER (
               PARTITION BY catalog_revision_id, overlay_revision_id, creator_id
               ORDER BY published_at_ns DESC, work_id DESC
           ) AS rank_in_scope
    FROM candidate_covers
),
source_ranked AS (
    SELECT catalog_revision_id,
           overlay_revision_id,
           source_id AS scope_id,
           cover_media_id,
           published_at_ns,
           row_number() OVER (
               PARTITION BY catalog_revision_id, overlay_revision_id, source_id
               ORDER BY published_at_ns DESC, creator_id DESC, work_id DESC
           ) AS rank_in_scope
    FROM candidate_covers
)
INSERT INTO aggregate_cover_projections
(catalog_revision_id, overlay_revision_id, scope_kind, scope_id, cover_media_id, published_at_ns)
SELECT catalog_revision_id, overlay_revision_id, 'creator', scope_id, cover_media_id, published_at_ns
FROM creator_ranked
WHERE rank_in_scope = 1
UNION ALL
SELECT catalog_revision_id, overlay_revision_id, 'source', scope_id, cover_media_id, published_at_ns
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
	sourceIDsJSON, err := json.Marshal(sourceIDs)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}

	var statement string
	switch scopeKind {
	case AggregateScopeCreator:
		statement = `WITH allowed_sources(source_id) AS (
    SELECT CAST(value AS TEXT) FROM json_each(?)
), ranked AS (
    SELECT r.creator_id AS scope_id,
           w.cover_media_id,
           w.published_at_ns,
           row_number() OVER (
               PARTITION BY r.creator_id
               ORDER BY w.published_at_ns DESC, w.work_id DESC
           ) AS rank_in_scope
    FROM work_creator_relations AS r
    JOIN work_projections AS w
      ON w.catalog_revision_id = r.catalog_revision_id
     AND w.overlay_revision_id = r.overlay_revision_id
     AND w.work_id = r.work_id
    JOIN allowed_sources AS a ON a.source_id = w.source_id
    WHERE r.catalog_revision_id=? AND r.overlay_revision_id=? AND w.cover_media_id<>''
)
SELECT scope_id, cover_media_id, published_at_ns
FROM ranked WHERE rank_in_scope=1
ORDER BY scope_id`
	case AggregateScopeLibrary:
		statement = `WITH allowed_sources(source_id) AS (
    SELECT CAST(value AS TEXT) FROM json_each(?)
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
