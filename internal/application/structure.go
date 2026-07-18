package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

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

// SourceStructureDecision 汇报一次拆分/合并人工决策的结果，供 API 返回与查询。
type SourceStructureDecision struct {
	DecisionID      string
	IssueID         string
	SourceID        string
	Kind            string
	Action          string
	TargetSourceKey string
	TargetWorkID    string
	Status          string
	Version         int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// structureActionKind 返回某决策动作所属的结构类别，非法动作返回空串。
func structureActionKind(action string) string {
	switch action {
	case "split_inherit", "split_keep_same", "split_create_new":
		return "split"
	case "merge_bind_existing", "merge_create_new":
		return "merge"
	}
	return ""
}

func structureResolutionSummary(action string) string {
	switch action {
	case "split_create_new", "merge_create_new":
		return "create_new"
	default:
		return "bind_existing"
	}
}

// ResolveSourceStructureIssue 对一个 SOURCE_WORK_SPLIT/MERGE_REVIEW_REQUIRED 审查 issue 应用人工
// 决策。决策不直接改写 Canonical 用户事实，而是以 control.db 的 pre-seed WorkBinding 表达（继承写
// inactive，排除写 manual_unbound）；下一次扫描据此复用既有解析机制得到正确 Source→Canonical 映射。
// 决策与其 pre-seed Binding 溯源持久化在 source_structure_decisions，支持乐观并发与可靠撤销。
func (r *Resources) ResolveSourceStructureIssue(ctx context.Context, issueID, decidedBy, action, targetSourceKey, targetWorkID string, version int) (SourceStructureDecision, error) {
	if strings.TrimSpace(decidedBy) == "" {
		return SourceStructureDecision{}, fault.New(fault.CodeValidation, false, nil)
	}
	kind := structureActionKind(action)
	if kind == "" {
		return SourceStructureDecision{}, fault.WithField(fault.CodeValidation, "action", nil)
	}
	if _, err := domain.ParseID(domain.IDBindingIssue, issueID); err != nil {
		return SourceStructureDecision{}, fault.New(fault.CodeNotFound, false, nil)
	}
	tx, err := r.control.BeginTx(ctx, nil)
	if err != nil {
		return SourceStructureDecision{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()

	var srcID, status, structureKind string
	var currentVersion int
	err = tx.QueryRowContext(ctx, `SELECT source_id, status, COALESCE(structure_kind, ''), version
FROM binding_issues WHERE issue_id=?`, issueID).Scan(&srcID, &status, &structureKind, &currentVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceStructureDecision{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return SourceStructureDecision{}, fault.New(fault.CodeInternal, true, err)
	}
	if structureKind == "" {
		return SourceStructureDecision{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if structureKind != kind {
		return SourceStructureDecision{}, fault.WithField(fault.CodeValidation, "action", nil)
	}
	if currentVersion != version {
		return SourceStructureDecision{}, fault.New(fault.CodeConflict, false, nil)
	}
	if status != "open" && status != "dismissed" {
		return SourceStructureDecision{}, fault.New(fault.CodeConflict, false, nil)
	}

	originWorkIDs, originSourceKeys, newSourceKeys, err := structureCandidates(ctx, tx, issueID)
	if err != nil {
		return SourceStructureDecision{}, err
	}
	now := r.clock.Now().UTC().Unix()

	decisionID, err := r.ids.New(domain.IDStructureDecision)
	if err != nil {
		return SourceStructureDecision{}, fault.New(fault.CodeInternal, true, err)
	}
	plan, err := planStructureSeeds(action, originWorkIDs, originSourceKeys, newSourceKeys, targetSourceKey, targetWorkID)
	if err != nil {
		return SourceStructureDecision{}, err
	}

	// 先插入决策父行，再插入 pre-seed Binding 溯源，满足外键顺序。
	fingerprint := structureFingerprint(structureCluster{kind: kind, originSourceKeys: originSourceKeys, newSourceKeys: newSourceKeys})
	if _, err := tx.ExecContext(ctx, `INSERT INTO source_structure_decisions
(decision_id, issue_id, source_id, kind, action, fingerprint, origin_source_keys, origin_work_ids,
 new_source_keys, target_source_key, target_work_id, decided_by, status, version, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'applied', 1, ?, ?)`,
		decisionID.String(), issueID, srcID, kind, action, fingerprint,
		encodeStringList(originSourceKeys), encodeStringList(originWorkIDs), encodeStringList(newSourceKeys),
		plan.targetSourceKey, plan.targetWorkID, decidedBy, now, now); err != nil {
		return SourceStructureDecision{}, bindConflict(err)
	}
	for _, seed := range plan.seeds {
		bindingID, err := r.seedWorkBinding(ctx, tx, srcID, seed.sourceKey, seed.workID, seed.status, now)
		if err != nil {
			return SourceStructureDecision{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO source_structure_decision_bindings
(decision_id, entity_type, binding_id, seed_kind) VALUES (?, 'work', ?, ?)`,
			decisionID.String(), bindingID, seed.seedKind); err != nil {
			return SourceStructureDecision{}, fault.New(fault.CodeInternal, true, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE binding_issues SET status='resolved', resolution=?,
resolved_target_id=?, resolved_by=?, version=version+1, updated_at=?, resolved_at=? WHERE issue_id=? AND version=?`,
		structureResolutionSummary(action), nullString(plan.targetWorkID), decidedBy, now, now, issueID, version); err != nil {
		return SourceStructureDecision{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return SourceStructureDecision{}, fault.New(fault.CodeInternal, true, err)
	}
	return SourceStructureDecision{DecisionID: decisionID.String(), IssueID: issueID, SourceID: srcID,
		Kind: kind, Action: action, TargetSourceKey: plan.targetSourceKey, TargetWorkID: plan.targetWorkID,
		Status: "applied", Version: 1, CreatedAt: time.Unix(now, 0).UTC(), UpdatedAt: time.Unix(now, 0).UTC()}, nil
}

// GetSourceStructureDecision 读取一条拆分/合并决策记录。
func (r *Resources) GetSourceStructureDecision(ctx context.Context, decisionID string) (SourceStructureDecision, error) {
	if _, err := domain.ParseID(domain.IDStructureDecision, decisionID); err != nil {
		return SourceStructureDecision{}, fault.New(fault.CodeNotFound, false, nil)
	}
	row := r.control.QueryRowContext(ctx, `SELECT decision_id, issue_id, source_id, kind, action,
target_source_key, target_work_id, status, version, created_at, updated_at
FROM source_structure_decisions WHERE decision_id=?`, decisionID)
	decision, err := scanStructureDecision(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceStructureDecision{}, fault.New(fault.CodeNotFound, false, nil)
	}
	return decision, err
}

// ListSourceStructureDecisions 按 Source 与状态列出拆分/合并决策，供审查与运维查询。
func (r *Resources) ListSourceStructureDecisions(ctx context.Context, sourceID, status string, limit int) ([]SourceStructureDecision, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	conditions := []string{"1=1"}
	args := []any{}
	if sourceID != "" {
		if _, err := domain.ParseID(domain.IDSource, sourceID); err != nil {
			return nil, fault.WithField(fault.CodeValidation, "sourceId", nil)
		}
		conditions = append(conditions, "source_id=?")
		args = append(args, sourceID)
	}
	if status != "" {
		if status != "applied" && status != "undone" {
			return nil, fault.WithField(fault.CodeValidation, "status", nil)
		}
		conditions = append(conditions, "status=?")
		args = append(args, status)
	}
	query := `SELECT decision_id, issue_id, source_id, kind, action, target_source_key, target_work_id,
status, version, created_at, updated_at FROM source_structure_decisions WHERE ` +
		strings.Join(conditions, " AND ") + ` ORDER BY created_at DESC, decision_id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := r.control.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	result := make([]SourceStructureDecision, 0)
	for rows.Next() {
		decision, err := scanStructureDecision(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, decision)
	}
	return result, rows.Err()
}

func scanStructureDecision(row rowScanner) (SourceStructureDecision, error) {
	var decision SourceStructureDecision
	var createdAt, updatedAt int64
	if err := row.Scan(&decision.DecisionID, &decision.IssueID, &decision.SourceID, &decision.Kind,
		&decision.Action, &decision.TargetSourceKey, &decision.TargetWorkID, &decision.Status,
		&decision.Version, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SourceStructureDecision{}, err
		}
		return SourceStructureDecision{}, fault.New(fault.CodeInternal, true, err)
	}
	decision.CreatedAt = time.Unix(createdAt, 0).UTC()
	decision.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return decision, nil
}

// UndoSourceStructureDecision 撤销一次尚未被扫描消费的拆分/合并决策：删除其 pre-seed Binding，将
// 决策标为 undone，并把对应审查 issue 重新打开，使结构变化恢复为待审查状态。若决策已被后续成功
// 扫描消费（新 source_key 已产生 active Binding，即已发生新 Binding 与可能的 Overlay/Favorite/
// Progress 依赖），返回结构化 CONFLICT，不做不可靠的逆向重建。
func (r *Resources) UndoSourceStructureDecision(ctx context.Context, decisionID, undoneBy string, version int) (SourceStructureDecision, error) {
	if strings.TrimSpace(undoneBy) == "" {
		return SourceStructureDecision{}, fault.New(fault.CodeValidation, false, nil)
	}
	if _, err := domain.ParseID(domain.IDStructureDecision, decisionID); err != nil {
		return SourceStructureDecision{}, fault.New(fault.CodeNotFound, false, nil)
	}
	tx, err := r.control.BeginTx(ctx, nil)
	if err != nil {
		return SourceStructureDecision{}, fault.New(fault.CodeInternal, true, err)
	}
	defer tx.Rollback()

	var issueID, srcID, status, newKeysJSON string
	var currentVersion int
	err = tx.QueryRowContext(ctx, `SELECT issue_id, source_id, status, new_source_keys, version
FROM source_structure_decisions WHERE decision_id=?`, decisionID).Scan(&issueID, &srcID, &status, &newKeysJSON, &currentVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceStructureDecision{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return SourceStructureDecision{}, fault.New(fault.CodeInternal, true, err)
	}
	if currentVersion != version {
		return SourceStructureDecision{}, fault.New(fault.CodeConflict, false, nil)
	}
	if status != "applied" {
		return SourceStructureDecision{}, fault.New(fault.CodeConflict, false, nil)
	}

	// 后续依赖检查：任一新 source_key 已产生 active Binding，说明决策已被扫描消费。
	for _, newKey := range decodeStringList(newKeysJSON) {
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM work_bindings
WHERE source_id=? AND source_key=? AND status='active'`, srcID, newKey).Scan(&active); err != nil {
			return SourceStructureDecision{}, fault.New(fault.CodeInternal, true, err)
		}
		if active > 0 {
			return SourceStructureDecision{}, fault.WithField(fault.CodeConflict, "consumedByScan", nil)
		}
	}

	// 删除 pre-seed Binding 及其溯源，把决策状态改为 undone。
	seedRows, err := tx.QueryContext(ctx, `SELECT binding_id FROM source_structure_decision_bindings
WHERE decision_id=? AND entity_type='work'`, decisionID)
	if err != nil {
		return SourceStructureDecision{}, fault.New(fault.CodeInternal, true, err)
	}
	var seedBindingIDs []string
	for seedRows.Next() {
		var bindingID string
		if err := seedRows.Scan(&bindingID); err != nil {
			seedRows.Close()
			return SourceStructureDecision{}, fault.New(fault.CodeInternal, true, err)
		}
		seedBindingIDs = append(seedBindingIDs, bindingID)
	}
	if err := seedRows.Err(); err != nil {
		seedRows.Close()
		return SourceStructureDecision{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := seedRows.Close(); err != nil {
		return SourceStructureDecision{}, fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM source_structure_decision_bindings WHERE decision_id=?`, decisionID); err != nil {
		return SourceStructureDecision{}, fault.New(fault.CodeInternal, true, err)
	}
	for _, bindingID := range seedBindingIDs {
		// 仅删除仍处于 pre-seed（非 active）状态的行，避免误删被其他流程改写的 Binding。
		if _, err := tx.ExecContext(ctx, `DELETE FROM work_bindings WHERE binding_id=? AND status<>'active'`, bindingID); err != nil {
			return SourceStructureDecision{}, fault.New(fault.CodeInternal, true, err)
		}
	}
	now := r.clock.Now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE source_structure_decisions SET status='undone',
version=version+1, updated_at=?, undone_at=? WHERE decision_id=? AND version=?`, now, now, decisionID, version); err != nil {
		return SourceStructureDecision{}, fault.New(fault.CodeInternal, true, err)
	}
	// 恢复审查 issue：重新打开，使结构变化再次可审查（若 issue 已被更晚证据 superseded 则保持不变）。
	if _, err := tx.ExecContext(ctx, `UPDATE binding_issues SET status='open', resolution=NULL,
resolved_target_id=NULL, resolved_by=?, resolved_at=NULL, version=version+1, updated_at=?
WHERE issue_id=? AND status='resolved'`, undoneBy, now, issueID); err != nil {
		return SourceStructureDecision{}, fault.New(fault.CodeInternal, true, err)
	}
	if err := tx.Commit(); err != nil {
		return SourceStructureDecision{}, fault.New(fault.CodeInternal, true, err)
	}
	return r.GetSourceStructureDecision(ctx, decisionID)
}

type structureSeed struct {
	sourceKey string
	workID    string
	status    string // 'inactive' | 'manual_unbound'
	seedKind  string // 'seed_inherit' | 'seed_exclude'
}

type structurePlan struct {
	seeds           []structureSeed
	targetSourceKey string
	targetWorkID    string
}

func inheritSeed(sourceKey, workID string) structureSeed {
	return structureSeed{sourceKey: sourceKey, workID: workID, status: "inactive", seedKind: "seed_inherit"}
}

func excludeSeed(sourceKey, workID string) structureSeed {
	return structureSeed{sourceKey: sourceKey, workID: workID, status: "manual_unbound", seedKind: "seed_exclude"}
}

// planStructureSeeds 依据决策动作把结构簇翻译为一组 pre-seed WorkBinding。继承写 inactive，使下一次
// 扫描按 source_key 命中并复用原 CanonicalWork；排除写 manual_unbound，阻止新 source_key 经任何证据
// 命中被排除的 CanonicalWork，从而创建新的 CanonicalWork。
func planStructureSeeds(action string, originWorkIDs, originSourceKeys, newSourceKeys []string, targetSourceKey, targetWorkID string) (structurePlan, error) {
	plan := structurePlan{}
	switch action {
	case "split_inherit":
		if len(originWorkIDs) != 1 {
			return structurePlan{}, fault.New(fault.CodeConflict, false, nil)
		}
		if !containsString(newSourceKeys, targetSourceKey) {
			return structurePlan{}, fault.WithField(fault.CodeValidation, "targetSourceKey", nil)
		}
		originWork := originWorkIDs[0]
		for _, key := range newSourceKeys {
			if key == targetSourceKey {
				plan.seeds = append(plan.seeds, inheritSeed(key, originWork))
			} else {
				plan.seeds = append(plan.seeds, excludeSeed(key, originWork))
			}
		}
		plan.targetSourceKey, plan.targetWorkID = targetSourceKey, originWork
	case "split_keep_same":
		if len(originWorkIDs) != 1 {
			return structurePlan{}, fault.New(fault.CodeConflict, false, nil)
		}
		originWork := originWorkIDs[0]
		for _, key := range newSourceKeys {
			plan.seeds = append(plan.seeds, inheritSeed(key, originWork))
		}
		plan.targetWorkID = originWork
	case "split_create_new":
		if len(originWorkIDs) != 1 {
			return structurePlan{}, fault.New(fault.CodeConflict, false, nil)
		}
		originWork := originWorkIDs[0]
		for _, key := range newSourceKeys {
			plan.seeds = append(plan.seeds, excludeSeed(key, originWork))
		}
	case "merge_bind_existing":
		if len(newSourceKeys) != 1 {
			return structurePlan{}, fault.New(fault.CodeConflict, false, nil)
		}
		if !containsString(originWorkIDs, targetWorkID) {
			return structurePlan{}, fault.WithField(fault.CodeValidation, "targetWorkId", nil)
		}
		newKey := newSourceKeys[0]
		for _, workID := range originWorkIDs {
			if workID == targetWorkID {
				plan.seeds = append(plan.seeds, inheritSeed(newKey, workID))
			} else {
				plan.seeds = append(plan.seeds, excludeSeed(newKey, workID))
			}
		}
		plan.targetSourceKey, plan.targetWorkID = newKey, targetWorkID
	case "merge_create_new":
		if len(newSourceKeys) != 1 {
			return structurePlan{}, fault.New(fault.CodeConflict, false, nil)
		}
		newKey := newSourceKeys[0]
		for _, workID := range originWorkIDs {
			plan.seeds = append(plan.seeds, excludeSeed(newKey, workID))
		}
		plan.targetSourceKey = newKey
	default:
		return structurePlan{}, fault.WithField(fault.CodeValidation, "action", nil)
	}
	return plan, nil
}

// seedWorkBinding 为某 source_key 插入一条非 active 的 pre-seed WorkBinding，返回其 binding_id。
func (r *Resources) seedWorkBinding(ctx context.Context, tx *sql.Tx, sourceID, sourceKey, workID, status string, now int64) (string, error) {
	id, err := r.ids.New(domain.IDWorkBinding)
	if err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO work_bindings
(binding_id, source_id, source_key, work_id, identity_version, status, last_seen_generation, created_at, updated_at)
VALUES (?, ?, ?, ?, 1, ?, 0, ?, ?)`, id.String(), sourceID, sourceKey, workID, status, now, now); err != nil {
		return "", bindConflict(err)
	}
	return id.String(), nil
}

// structureCandidates 从 issue 候选还原 origin CanonicalWork ID、origin source_key 与新 source_key。
func structureCandidates(ctx context.Context, tx *sql.Tx, issueID string) (originWorkIDs, originSourceKeys, newSourceKeys []string, err error) {
	rows, err := tx.QueryContext(ctx, `SELECT candidate_id, match_signal, match_value FROM binding_issue_candidates
WHERE issue_id=? ORDER BY ordinal`, issueID)
	if err != nil {
		return nil, nil, nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	for rows.Next() {
		var candidateID, signal, value string
		if err := rows.Scan(&candidateID, &signal, &value); err != nil {
			return nil, nil, nil, fault.New(fault.CodeInternal, true, err)
		}
		switch signal {
		case signalOriginCanonical:
			originWorkIDs = append(originWorkIDs, candidateID)
			originSourceKeys = append(originSourceKeys, value)
		case signalNewSourceWork:
			newSourceKeys = append(newSourceKeys, candidateID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, fault.New(fault.CodeInternal, true, err)
	}
	return originWorkIDs, originSourceKeys, newSourceKeys, nil
}

func encodeStringList(values []string) string {
	data, _ := json.Marshal(values)
	return string(data)
}

func decodeStringList(value string) []string {
	var result []string
	_ = json.Unmarshal([]byte(value), &result)
	return result
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
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
