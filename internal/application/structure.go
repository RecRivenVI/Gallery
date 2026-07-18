package application

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
)

// SourceWork 拆分/合并首先是 Source-derived 结构变化，不自动等同于 CanonicalWork 的拆分或合并。
// 检测只使用稳定来源证据（ContentBlob digest），不依赖路径相似度做不可逆判断；无法安全自动处理
// 时复用现有 Binding issue 体系产生审查记录并阻塞该 Source publication，等待人工决策。
const (
	structureSplitCode = "SOURCE_WORK_SPLIT_REVIEW_REQUIRED"
	structureMergeCode = "SOURCE_WORK_MERGE_REVIEW_REQUIRED"

	// 结构候选证据的两种 match_signal：origin_canonical 指向拆分/合并涉及的原 CanonicalWork（可作为
	// 继承/绑定目标）；new_source_work 指向本次扫描新出现的 SourceWork 稳定键。
	signalOriginCanonical = "origin_canonical"
	signalNewSourceWork   = "new_source_work"
)

type structureCluster struct {
	kind             string // "split" | "merge"
	representative   string // 去重键：split 用 origin source_key，merge 用 new source_key
	originSourceKeys []string
	originWorkIDs    []string
	newSourceKeys    []string
}

type priorWorkFacts struct {
	sourceKey string
	workID    string
	digests   map[string]struct{}
}

// detectSourceStructureChange 在 EnsureCanonical 事务内、逐项解析之前运行。它用 ContentBlob digest
// 证据比较本次发现的 SourceWork 与既有 live WorkBinding：一个原 SourceWork 的媒体分散到多个新
// SourceWork 视为“可能拆分”，多个原 SourceWork 的媒体汇聚到一个新 SourceWork 视为“可能合并”。
// 检测到且无对应已应用决策时，记录 SOURCE_WORK_*_REVIEW_REQUIRED 审查 issue 并提交事务、返回
// review-required；调用方必须立即返回该错误。返回 blocked=false 表示无需人工审查，扫描继续。
func (r *Resources) detectSourceStructureChange(ctx context.Context, tx *sql.Tx, sourceID string, discovered []DiscoveredWork, now int64) (bool, error) {
	discoveredKeys := make(map[string]struct{}, len(discovered))
	discoveredDigests := make(map[string]map[string]struct{}, len(discovered))
	for _, work := range discovered {
		discoveredKeys[work.SourceKey] = struct{}{}
		digests := make(map[string]struct{})
		for _, item := range work.Media {
			if item.Digest != "" {
				digests[item.Algorithm+":"+item.Digest] = struct{}{}
			}
		}
		discoveredDigests[work.SourceKey] = digests
	}

	boundKeys, err := stringSet(ctx, tx, `SELECT DISTINCT source_key FROM work_bindings WHERE source_id=?`, sourceID)
	if err != nil {
		return false, err
	}
	// 只有既无既有 Binding、又携带 Blob 证据的新 source_key 才可能是拆分/合并的新 SourceWork。
	newKeys := make([]string, 0)
	for key := range discoveredKeys {
		if _, bound := boundKeys[key]; bound {
			continue
		}
		if len(discoveredDigests[key]) > 0 {
			newKeys = append(newKeys, key)
		}
	}
	if len(newKeys) == 0 {
		return false, nil
	}

	prior, err := r.priorLiveWorks(ctx, tx, sourceID)
	if err != nil {
		return false, err
	}
	missing := make([]priorWorkFacts, 0, len(prior))
	for _, work := range prior {
		if _, seen := discoveredKeys[work.sourceKey]; seen {
			continue
		}
		if len(work.digests) > 0 {
			missing = append(missing, work)
		}
	}
	if len(missing) == 0 {
		return false, nil
	}

	// 以任一共享 digest 建立新旧 SourceWork 之间的关联。
	newToOrigins := make(map[string]map[string]priorWorkFacts)
	originToNews := make(map[string]map[string]struct{})
	for _, nk := range newKeys {
		nkDigests := discoveredDigests[nk]
		for _, mp := range missing {
			if intersects(nkDigests, mp.digests) {
				if newToOrigins[nk] == nil {
					newToOrigins[nk] = make(map[string]priorWorkFacts)
				}
				newToOrigins[nk][mp.sourceKey] = mp
				if originToNews[mp.sourceKey] == nil {
					originToNews[mp.sourceKey] = make(map[string]struct{})
				}
				originToNews[mp.sourceKey][nk] = struct{}{}
			}
		}
	}

	// 优先处理拆分（一个原 SourceWork → 多个新 SourceWork），再处理合并，保证确定性。
	if cluster, ok := firstSplitCluster(originToNews, missingByKey(missing)); ok {
		return true, r.recordStructureIssue(ctx, tx, sourceID, cluster, now)
	}
	if cluster, ok := firstMergeCluster(newToOrigins); ok {
		return true, r.recordStructureIssue(ctx, tx, sourceID, cluster, now)
	}
	return false, nil
}

func (r *Resources) priorLiveWorks(ctx context.Context, tx *sql.Tx, sourceID string) ([]priorWorkFacts, error) {
	rows, err := tx.QueryContext(ctx, `SELECT source_key, work_id FROM work_bindings
WHERE source_id=? AND status IN ('active', 'inactive', 'orphan_candidate')
ORDER BY source_key`, sourceID)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	type keyed struct{ sourceKey, workID string }
	var works []keyed
	for rows.Next() {
		var item keyed
		if err := rows.Scan(&item.sourceKey, &item.workID); err != nil {
			rows.Close()
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		works = append(works, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	if err := rows.Close(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	result := make([]priorWorkFacts, 0, len(works))
	for _, work := range works {
		digests, err := r.workDigests(ctx, tx, sourceID, work.workID)
		if err != nil {
			return nil, err
		}
		result = append(result, priorWorkFacts{sourceKey: work.sourceKey, workID: work.workID, digests: digests})
	}
	return result, nil
}

func (r *Resources) workDigests(ctx context.Context, tx *sql.Tx, sourceID, workID string) (map[string]struct{}, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT algorithm, digest FROM media_bindings
WHERE source_id=? AND work_id=? AND digest<>'' AND status IN ('active', 'inactive', 'orphan_candidate')`,
		sourceID, workID)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	result := make(map[string]struct{})
	for rows.Next() {
		var algorithm, digest string
		if err := rows.Scan(&algorithm, &digest); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result[algorithm+":"+digest] = struct{}{}
	}
	return result, rows.Err()
}

func missingByKey(missing []priorWorkFacts) map[string]priorWorkFacts {
	result := make(map[string]priorWorkFacts, len(missing))
	for _, work := range missing {
		result[work.sourceKey] = work
	}
	return result
}

// firstSplitCluster 返回按 origin source_key 字典序的第一个拆分簇（一个原 SourceWork 关联 ≥2 个
// 新 SourceWork）。
func firstSplitCluster(originToNews map[string]map[string]struct{}, missing map[string]priorWorkFacts) (structureCluster, bool) {
	origins := make([]string, 0, len(originToNews))
	for origin, news := range originToNews {
		if len(news) >= 2 {
			origins = append(origins, origin)
		}
	}
	if len(origins) == 0 {
		return structureCluster{}, false
	}
	sort.Strings(origins)
	origin := origins[0]
	news := sortedKeys(originToNews[origin])
	return structureCluster{
		kind: "split", representative: origin,
		originSourceKeys: []string{origin}, originWorkIDs: []string{missing[origin].workID},
		newSourceKeys: news,
	}, true
}

// firstMergeCluster 返回按 new source_key 字典序的第一个合并簇（一个新 SourceWork 关联 ≥2 个原
// SourceWork）。
func firstMergeCluster(newToOrigins map[string]map[string]priorWorkFacts) (structureCluster, bool) {
	newKeys := make([]string, 0, len(newToOrigins))
	for nk, origins := range newToOrigins {
		if len(origins) >= 2 {
			newKeys = append(newKeys, nk)
		}
	}
	if len(newKeys) == 0 {
		return structureCluster{}, false
	}
	sort.Strings(newKeys)
	nk := newKeys[0]
	originKeys := sortedKeys(keysOf(newToOrigins[nk]))
	workIDs := make([]string, 0, len(originKeys))
	for _, key := range originKeys {
		workIDs = append(workIDs, newToOrigins[nk][key].workID)
	}
	return structureCluster{
		kind: "merge", representative: nk,
		originSourceKeys: originKeys, originWorkIDs: workIDs,
		newSourceKeys: []string{nk},
	}, true
}

// recordStructureIssue 在扫描事务内持久化或复用一个结构审查 issue 后提交并返回 review-required。
// 去重键为 (source_id, code, source_key) 且 fingerprint 一致；证据变化时旧 issue superseded。
func (r *Resources) recordStructureIssue(ctx context.Context, tx *sql.Tx, srcID string, cluster structureCluster, now int64) error {
	code := structureSplitCode
	if cluster.kind == "merge" {
		code = structureMergeCode
	}
	fingerprint := structureFingerprint(cluster)
	var existingID, existingFingerprint string
	err := tx.QueryRowContext(ctx, `SELECT issue_id, candidate_fingerprint FROM binding_issues
WHERE source_id=? AND entity_type='work' AND code=? AND source_key=? AND status IN ('open', 'dismissed')
ORDER BY created_at DESC, issue_id DESC LIMIT 1`, srcID, code, cluster.representative).
		Scan(&existingID, &existingFingerprint)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fault.New(fault.CodeInternal, true, err)
	}
	if err == nil {
		if existingFingerprint == fingerprint {
			if _, err := tx.ExecContext(ctx, `UPDATE binding_issues SET updated_at=? WHERE issue_id=?`, now, existingID); err != nil {
				return fault.New(fault.CodeInternal, true, err)
			}
			return r.commitStructureIssue(tx, cluster.kind)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE binding_issues SET status='superseded', updated_at=? WHERE issue_id=?`, now, existingID); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
	}
	return r.insertStructureIssue(ctx, tx, srcID, cluster, code, fingerprint, now)
}

func (r *Resources) insertStructureIssue(ctx context.Context, tx *sql.Tx, srcID string, cluster structureCluster, code, fingerprint string, now int64) error {
	id, err := r.ids.New(domain.IDBindingIssue)
	if err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	candidateCount := len(cluster.originWorkIDs) + len(cluster.newSourceKeys)
	if _, err := tx.ExecContext(ctx, `INSERT INTO binding_issues
(issue_id, source_id, entity_type, structure_kind, source_key, code,
 candidate_fingerprint, candidate_count, status, version, created_at, updated_at)
VALUES (?, ?, 'work', ?, ?, ?, ?, ?, 'open', 1, ?, ?)`,
		id.String(), srcID, cluster.kind, cluster.representative, code,
		fingerprint, candidateCount, now, now); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	ordinal := 0
	for index, workID := range cluster.originWorkIDs {
		label, err := candidateLabel(ctx, tx, "work", workID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO binding_issue_candidates
(issue_id, ordinal, candidate_id, candidate_kind, match_signal, match_value, label)
VALUES (?, ?, ?, 'work', ?, ?, ?)`, id.String(), ordinal, workID, signalOriginCanonical,
			cluster.originSourceKeys[index], label); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		ordinal++
	}
	for _, newKey := range cluster.newSourceKeys {
		if _, err := tx.ExecContext(ctx, `INSERT INTO binding_issue_candidates
(issue_id, ordinal, candidate_id, candidate_kind, match_signal, match_value, label)
VALUES (?, ?, ?, 'work', ?, '', '')`, id.String(), ordinal, newKey, signalNewSourceWork); err != nil {
			return fault.New(fault.CodeInternal, true, err)
		}
		ordinal++
	}
	return r.commitStructureIssue(tx, cluster.kind)
}

func (r *Resources) commitStructureIssue(tx *sql.Tx, kind string) error {
	if err := tx.Commit(); err != nil {
		return fault.New(fault.CodeInternal, true, err)
	}
	field := "sourceWorkSplit"
	if kind == "merge" {
		field = "sourceWorkMerge"
	}
	return fault.WithField(fault.CodeBindingReviewRequired, field, nil)
}

func structureFingerprint(cluster structureCluster) string {
	origins := append([]string(nil), cluster.originSourceKeys...)
	news := append([]string(nil), cluster.newSourceKeys...)
	sort.Strings(origins)
	sort.Strings(news)
	return cluster.kind + "|" + strings.Join(origins, "\x00") + "|" + strings.Join(news, "\x00")
}

func intersects(a, b map[string]struct{}) bool {
	small, large := a, b
	if len(b) < len(a) {
		small, large = b, a
	}
	for key := range small {
		if _, ok := large[key]; ok {
			return true
		}
	}
	return false
}

func keysOf[V any](m map[string]V) map[string]struct{} {
	result := make(map[string]struct{}, len(m))
	for key := range m {
		result[key] = struct{}{}
	}
	return result
}
