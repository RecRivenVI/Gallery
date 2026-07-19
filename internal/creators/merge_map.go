package creators

import (
	"context"
	"database/sql"
	"sort"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/querytext"
)

// Querier 允许 ReadMergePairs 复用 *sql.DB 或活动读事务 *sql.Tx，让扫描能在与
// watermark 一致的快照事务中读取合并映射。
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// ReadMergePairs 读取 control.db 中所有已生效合并，并把每个被并创作者解析到其有效
// 根 target，返回投影阶段所需的重定向对。合并链（target 之后又被并入他者）也会被
// 折叠到最终根，因此 catalog.ApplyCreatorMerges 可与顺序无关地直接重定向。
func ReadMergePairs(ctx context.Context, q Querier) ([]domain.CreatorMergePair, error) {
	rows, err := q.QueryContext(ctx, `SELECT creator_id, merged_into, name FROM canonical_creators`)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	parents := make(map[string]string)
	names := make(map[string]string)
	for rows.Next() {
		var id, name string
		var mergedInto sql.NullString
		if err := rows.Scan(&id, &mergedInto, &name); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		names[id] = name
		if mergedInto.Valid {
			parents[id] = mergedInto.String
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	pairs := make([]domain.CreatorMergePair, 0, len(parents))
	for absorbed := range parents {
		root := resolveRoot(parents, absorbed)
		if root == absorbed {
			continue
		}
		pairs = append(pairs, domain.CreatorMergePair{
			Absorbed: absorbed, Target: root,
			TargetName: names[root], TargetSortKey: querytext.NaturalSortKey(names[root]),
		})
	}
	return pairs, nil
}

// ResolveEquivalenceGroup 返回 id（可以是合并根，也可以是已被合并的旧 ID）所在合并链的
// 全部成员，包括根自身。供查询按 Creator 过滤时命中全部有效成员作品，不修改任何
// work_creator_relations 数据，因此撤销合并后过滤语义随即自动恢复。id 不存在时返回
// CodeNotFound；未参与任何合并的 Creator 返回只含自身的单元素组。
func ResolveEquivalenceGroup(ctx context.Context, q Querier, id string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT creator_id, merged_into FROM canonical_creators`)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	parents := make(map[string]string)
	children := make(map[string][]string)
	found := false
	for rows.Next() {
		var creatorID string
		var mergedInto sql.NullString
		if err := rows.Scan(&creatorID, &mergedInto); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		if creatorID == id {
			found = true
		}
		if mergedInto.Valid {
			parents[creatorID] = mergedInto.String
			children[mergedInto.String] = append(children[mergedInto.String], creatorID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	if !found {
		return nil, fault.New(fault.CodeNotFound, false, nil)
	}
	root := resolveRoot(parents, id)
	group := collectGroup(children, root)
	sort.Strings(group)
	return group, nil
}

func collectGroup(children map[string][]string, root string) []string {
	result := []string{root}
	queue := []string{root}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		result = append(result, children[current]...)
		queue = append(queue, children[current]...)
	}
	return result
}

// resolveRoot 沿 merged_into 链找到有效创作者。合并规则保证不出现环（target 必须
// live），visited 仅作防御，避免异常数据造成死循环。
func resolveRoot(parents map[string]string, id string) string {
	visited := make(map[string]struct{})
	for {
		parent, ok := parents[id]
		if !ok {
			return id
		}
		if _, seen := visited[id]; seen {
			return id
		}
		visited[id] = struct{}{}
		id = parent
	}
}
