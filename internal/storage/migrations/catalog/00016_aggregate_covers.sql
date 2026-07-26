-- 作者、平台与资料库三级聚合封面。
--
-- 真实规则声明的是**聚合策略**（`author=latest_dated_work`、`platform=latest_dated_author`、
-- `library=latest_dated_platform`），不是聚合结果。《规则系统》把「全局聚合」明确划在规则表达式
-- 之外，由核心服务承担；规则层的封面输出仍只有作品级 `RuleResult.Work.CoverPath`。因此这里存的
-- 是 Catalog 投影计算出的结果。
--
-- **为什么是一张表而不是三张。** 三个层级的行形状完全相同（一个作用域 → 一个封面 + 一个代表时刻），
-- 差别只在作用域类型。合成一张表使 Overlay 重发布 clone、单 Source 重扫 clone、行数对齐校验与完整性
-- 校验各自只需要一条语句而不是三条——而 clone 语句正是投影加列时最容易静默丢数据的地方。
--
-- **为什么不加到 creator_projections 上。** 那张表用 `INSERT OR IGNORE` 逐 Work 写入，等于「第一个
-- 遇到的 Work 决定作者封面」，与「最新日期的作品决定作者封面」不是一回事；而且 `cloneUnchangedSources`
-- 按 `source_id<>?` 过滤继承，作者与资料库天然横跨多个 Source，简单继承会让重扫后的聚合封面停留在旧
-- 值。聚合必须在全部 Work（含从 active publication 继承来的其它 Source）就位之后整体重算。
--
-- scope_id 的含义随 scope_kind 而定：creator 为 CanonicalCreator ID，source 为 Source ID，
-- library 为 Library ID。published_at_ns 是被选中作品的发布时刻，保留它使聚合结果可被解释、可被
-- 复核，也让上层聚合（platform 取最新作者、library 取最新平台）无需回查 work_projections。
CREATE TABLE aggregate_cover_projections (
    catalog_revision_id TEXT NOT NULL,
    overlay_revision_id TEXT NOT NULL,
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('creator', 'source', 'library')),
    scope_id TEXT NOT NULL,
    cover_media_id TEXT NOT NULL,
    published_at_ns INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (catalog_revision_id, overlay_revision_id, scope_kind, scope_id),
    FOREIGN KEY (overlay_revision_id) REFERENCES overlay_projection_revisions(overlay_revision_id) ON DELETE CASCADE
) STRICT;
