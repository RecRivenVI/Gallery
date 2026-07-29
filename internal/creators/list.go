package creators

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/domain"
)

const creatorListCursorVersion = 1

var creatorListCursorEncoding = base64.RawURLEncoding.Strict()

type ListPageRequest struct {
	AllowedSourceIDs []string
	SourceID         string
	IncludeMerged    bool
	Sort             string
	Limit            int
	Cursor           string
}

type ListPage struct {
	Items      []Creator
	NextCursor string
}

type creatorListCursor struct {
	Version          int    `json:"version"`
	QueryFingerprint string `json:"queryFingerprint"`
	LastSortKey      string `json:"lastSortKey"`
	LastCreatorID    string `json:"lastCreatorId"`
}

// BindingSourceIDs 返回当前 Creator Binding 引用的 Source 小集合。调用方先对这个集合做
// 一次批量 effective capability 判定，再把允许集合传回分页查询，避免按 Creator/Binding
// 产生授权 N+1。activeOnly 只供用户浏览页使用；治理兼容列表仍需看见非 active 证据。
func (s *Service) BindingSourceIDs(ctx context.Context, activeOnly bool) ([]string, error) {
	query := "SELECT DISTINCT source_id FROM creator_bindings"
	if activeOnly {
		query += " WHERE status='active'"
	}
	query += " ORDER BY source_id"
	rows, err := s.control.QueryContext(ctx, query)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var sourceID string
		if err := rows.Scan(&sourceID); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result = append(result, sourceID)
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

// AllSourceBindings 用一次有序读取取得全部 Creator 的 Binding 证据，供未携带任何分页参数
// 的兼容列表保留原有“全部 CanonicalCreator”语义，同时消除逐 Creator Get 的数据库 N+1。
func (s *Service) AllSourceBindings(ctx context.Context) (map[string][]SourceBinding, error) {
	rows, err := s.control.QueryContext(ctx, `SELECT creator_id, binding_id, source_id, provider_id, external_id, source_key, status
FROM creator_bindings ORDER BY creator_id, source_id, source_key, binding_id`)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	result := make(map[string][]SourceBinding)
	for rows.Next() {
		var creatorID string
		var binding SourceBinding
		if err := rows.Scan(&creatorID, &binding.BindingID, &binding.SourceID, &binding.ProviderID,
			&binding.ExternalID, &binding.SourceKey, &binding.Status); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result[creatorID] = append(result[creatorID], binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

// ListPage 是面向用户浏览的 live CanonicalCreator keyset 页。它与 publication-bound Work
// 查询有意不同：身份与 Binding 是 control.db 当前事实，因此游标只提供稳定锚点，不承诺
// 跨写入的可重复读。每次请求仍重新计算 allowed Source，游标不能成为授权凭据。
//
// IncludeMerged=false 时按当前 merged_into 图折叠为有效根；根的 SourceCount 和可见性都由
// 等价组全部 active Binding 汇总，避免“根没有本 Source Binding、被并成员有”时整位作者消失。
func (s *Service) ListPage(ctx context.Context, request ListPageRequest) (ListPage, error) {
	if request.Limit < 1 || request.Limit > 200 {
		return ListPage{}, fault.WithField(fault.CodeValidation, "limit", nil)
	}
	switch request.Sort {
	case "", "name_asc":
		request.Sort = "name_asc"
	case "name_desc":
	default:
		return ListPage{}, fault.WithField(fault.CodeValidation, "sort", nil)
	}
	allowed := normalizeSourceIDs(request.AllowedSourceIDs)
	if request.SourceID != "" {
		if _, err := domain.ParseID(domain.IDSource, request.SourceID); err != nil {
			return ListPage{}, fault.WithField(fault.CodeValidation, "sourceId", err)
		}
		if !containsSorted(allowed, request.SourceID) {
			return ListPage{Items: []Creator{}}, nil
		}
		allowed = []string{request.SourceID}
	}
	if len(allowed) == 0 {
		return ListPage{Items: []Creator{}}, nil
	}
	fingerprint := creatorPageFingerprint(request, allowed)
	var anchor creatorListCursor
	if request.Cursor != "" {
		decoded, err := decodeCreatorListCursor(request.Cursor)
		if err != nil {
			return ListPage{}, err
		}
		if decoded.QueryFingerprint != fingerprint {
			return ListPage{}, fault.New(fault.CodeCursorExpired, true, nil)
		}
		anchor = decoded
	}
	allowedJSON, err := json.Marshal(allowed)
	if err != nil {
		return ListPage{}, fault.New(fault.CodeInternal, true, err)
	}

	hasMerges := false
	if !request.IncludeMerged {
		if err := s.control.QueryRowContext(ctx,
			"SELECT EXISTS(SELECT 1 FROM canonical_creators WHERE merged_into IS NOT NULL LIMIT 1)").Scan(&hasMerges); err != nil {
			return ListPage{}, fault.New(fault.CodeInternal, true, err)
		}
	}
	query, args := creatorPageStatement(request.IncludeMerged, hasMerges, request.Sort, anchor.LastCreatorID != "")
	args = append([]any{string(allowedJSON)}, args...)
	if anchor.LastCreatorID != "" {
		args = append(args, anchor.LastSortKey, anchor.LastSortKey, anchor.LastCreatorID)
	}
	args = append(args, request.Limit+1)
	rows, err := s.control.QueryContext(ctx, query, args...)
	if err != nil {
		return ListPage{}, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	items := make([]Creator, 0, request.Limit+1)
	for rows.Next() {
		var item Creator
		var mergedInto sql.NullString
		var createdAt int64
		if err := rows.Scan(&item.ID, &item.Name, &item.SortNameKey, &mergedInto, &createdAt, &item.SourceCount); err != nil {
			return ListPage{}, fault.New(fault.CodeInternal, true, err)
		}
		item.MergedInto = mergedInto.String
		item.EffectiveID = item.ID
		item.CreatedAt = unixUTC(createdAt)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ListPage{}, fault.New(fault.CodeInternal, true, err)
	}

	page := ListPage{Items: items}
	if len(items) > request.Limit {
		last := items[request.Limit-1]
		page.Items = items[:request.Limit]
		page.NextCursor, err = encodeCreatorListCursor(creatorListCursor{
			Version: creatorListCursorVersion, QueryFingerprint: fingerprint,
			LastSortKey: last.SortNameKey, LastCreatorID: last.ID,
		})
		if err != nil {
			return ListPage{}, err
		}
	}
	if !request.IncludeMerged && len(page.Items) > 0 {
		members, err := s.equivalenceMembers(ctx, creatorIDs(page.Items))
		if err != nil {
			return ListPage{}, err
		}
		for index := range page.Items {
			page.Items[index].MemberIDs = members[page.Items[index].ID]
		}
	} else {
		for index := range page.Items {
			page.Items[index].MemberIDs = []string{page.Items[index].ID}
		}
	}
	return page, nil
}

func creatorPageStatement(includeMerged, hasMerges bool, order string, hasAnchor bool) (string, []any) {
	direction, comparator := "ASC", ">"
	if order == "name_desc" {
		direction, comparator = "DESC", "<"
	}
	var query string
	if includeMerged || !hasMerges {
		query = `WITH allowed_sources(source_id) AS MATERIALIZED (
    SELECT DISTINCT CAST(value AS TEXT) FROM json_each(?)
)
SELECT c.creator_id, c.name, c.sort_name_key, c.merged_into, c.created_at,
       (SELECT count(DISTINCT binding.source_id)
        FROM creator_bindings AS binding
        JOIN allowed_sources AS allowed ON allowed.source_id=binding.source_id
        WHERE binding.creator_id=c.creator_id AND binding.status='active') AS source_count
FROM canonical_creators AS c
WHERE EXISTS (
    SELECT 1 FROM creator_bindings AS binding
    JOIN allowed_sources AS allowed ON allowed.source_id=binding.source_id
    WHERE binding.creator_id=c.creator_id AND binding.status='active'
)`
		if !includeMerged {
			query += " AND c.merged_into IS NULL"
		}
	} else {
		query = `WITH RECURSIVE allowed_sources(source_id) AS MATERIALIZED (
    SELECT DISTINCT CAST(value AS TEXT) FROM json_each(?)
), merged_groups(base_creator_id, effective_creator_id) AS (
    SELECT root.creator_id, root.creator_id
    FROM canonical_creators AS root
    WHERE root.merged_into IS NULL
      AND EXISTS (SELECT 1 FROM canonical_creators AS child WHERE child.merged_into=root.creator_id)
    UNION ALL
    SELECT child.creator_id, groups.effective_creator_id
    FROM canonical_creators AS child
    JOIN merged_groups AS groups ON child.merged_into=groups.base_creator_id
)
SELECT c.creator_id, c.name, c.sort_name_key, c.merged_into, c.created_at,
       (SELECT count(*) FROM (
           SELECT binding.source_id
           FROM creator_bindings AS binding
           JOIN allowed_sources AS allowed ON allowed.source_id=binding.source_id
           WHERE binding.creator_id=c.creator_id AND binding.status='active'
           UNION
           SELECT binding.source_id
           FROM merged_groups AS groups
           JOIN creator_bindings AS binding
             ON binding.creator_id=groups.base_creator_id AND binding.status='active'
           JOIN allowed_sources AS allowed ON allowed.source_id=binding.source_id
           WHERE groups.effective_creator_id=c.creator_id
       )) AS source_count
FROM canonical_creators AS c
WHERE c.merged_into IS NULL
  AND (
      EXISTS (
          SELECT 1 FROM creator_bindings AS binding
          JOIN allowed_sources AS allowed ON allowed.source_id=binding.source_id
          WHERE binding.creator_id=c.creator_id AND binding.status='active'
      )
      OR EXISTS (
          SELECT 1 FROM merged_groups AS groups
          JOIN creator_bindings AS binding
            ON binding.creator_id=groups.base_creator_id AND binding.status='active'
          JOIN allowed_sources AS allowed ON allowed.source_id=binding.source_id
          WHERE groups.effective_creator_id=c.creator_id
      )
  )`
	}
	if hasAnchor {
		query += " AND"
		query += " (c.sort_name_key " + comparator + " ? OR (c.sort_name_key = ? AND c.creator_id " + comparator + " ?))"
	}
	query += " ORDER BY c.sort_name_key " + direction + ", c.creator_id " + direction + " LIMIT ?"
	return query, nil
}

func (s *Service) equivalenceMembers(ctx context.Context, roots []string) (map[string][]string, error) {
	encoded, err := json.Marshal(roots)
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	rows, err := s.control.QueryContext(ctx, `WITH RECURSIVE requested(root_id) AS MATERIALIZED (
    SELECT DISTINCT CAST(value AS TEXT) FROM json_each(?)
), members(root_id, creator_id) AS (
    SELECT root_id, root_id FROM requested
    UNION ALL
    SELECT members.root_id, child.creator_id
    FROM members JOIN canonical_creators AS child ON child.merged_into=members.creator_id
)
SELECT root_id, creator_id FROM members ORDER BY root_id, creator_id`, string(encoded))
	if err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	result := make(map[string][]string, len(roots))
	for rows.Next() {
		var rootID, creatorID string
		if err := rows.Scan(&rootID, &creatorID); err != nil {
			return nil, fault.New(fault.CodeInternal, true, err)
		}
		result[rootID] = append(result[rootID], creatorID)
	}
	if err := rows.Err(); err != nil {
		return nil, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

func normalizeSourceIDs(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	write := 0
	for _, value := range result {
		if value == "" || (write > 0 && result[write-1] == value) {
			continue
		}
		result[write] = value
		write++
	}
	return result[:write]
}

func containsSorted(values []string, target string) bool {
	index := sort.SearchStrings(values, target)
	return index < len(values) && values[index] == target
}

func creatorPageFingerprint(request ListPageRequest, allowed []string) string {
	payload, _ := json.Marshal(struct {
		AllowedSourceIDs []string `json:"allowedSourceIds"`
		SourceID         string   `json:"sourceId"`
		IncludeMerged    bool     `json:"includeMerged"`
		Sort             string   `json:"sort"`
		Limit            int      `json:"limit"`
	}{allowed, request.SourceID, request.IncludeMerged, request.Sort, request.Limit})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func encodeCreatorListCursor(value creatorListCursor) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	return creatorListCursorEncoding.EncodeToString(payload), nil
}

func decodeCreatorListCursor(cursor string) (creatorListCursor, error) {
	if len(cursor) == 0 || len(cursor) > 32768 {
		return creatorListCursor{}, fault.New(fault.CodeCursorInvalid, false, nil)
	}
	raw, err := creatorListCursorEncoding.DecodeString(cursor)
	if err != nil || creatorListCursorEncoding.EncodeToString(raw) != cursor {
		return creatorListCursor{}, fault.New(fault.CodeCursorInvalid, false, err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var result creatorListCursor
	if err := decoder.Decode(&result); err != nil {
		return creatorListCursor{}, fault.New(fault.CodeCursorInvalid, false, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return creatorListCursor{}, fault.New(fault.CodeCursorInvalid, false, err)
	}
	if result.Version != creatorListCursorVersion {
		return creatorListCursor{}, fault.New(fault.CodeCursorExpired, true, nil)
	}
	if len(result.QueryFingerprint) != sha256.Size*2 || len(result.LastSortKey) > 32768 {
		return creatorListCursor{}, fault.New(fault.CodeCursorInvalid, false, nil)
	}
	if _, err := hex.DecodeString(result.QueryFingerprint); err != nil || strings.ToLower(result.QueryFingerprint) != result.QueryFingerprint {
		return creatorListCursor{}, fault.New(fault.CodeCursorInvalid, false, err)
	}
	if _, err := domain.ParseID(domain.IDCanonicalCreator, result.LastCreatorID); err != nil {
		return creatorListCursor{}, fault.New(fault.CodeCursorInvalid, false, err)
	}
	return result, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("cursor 含多个 JSON 值")
	}
	return err
}

func creatorIDs(items []Creator) []string {
	result := make([]string, len(items))
	for index := range items {
		result[index] = items[index].ID
	}
	return result
}

func unixUTC(value int64) (resultTime time.Time) {
	return time.Unix(value, 0).UTC()
}
