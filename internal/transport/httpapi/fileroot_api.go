package httpapi

import (
	"net/http"
	"strconv"

	api "github.com/RecRivenVI/gallery/api"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/fileroot"
)

// listFileRoots 返回已登记的只读文件根。
//
// 授权用**独立**的 `files.browse` capability，不复用 `library.read`：后者的 scope 在实践中总是
// Library 或 Source，而文件根既不是二者之一，请求只能以 global scope 发出；而 global 请求不匹配
// 任何非 global grant，于是「禁止此人读某 Library」的 deny 完全不会约束文件根浏览——文件根恰恰
// 是那些 Library 底层文件的父目录。
//
// 当前文件根授权只有全有或全无（global scope）。这是已知限制，明确记录在契约里，不用 global scope
// 冒充细粒度授权。
func (s *Server) listFileRoots(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "files.browse"); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	roots := s.fileRoots.List()
	items := make([]api.FileRoot, 0, len(roots))
	for _, root := range roots {
		// 刻意不返回 root.Path：绝对路径不得出现在任何对外响应中。
		items = append(items, api.FileRoot{Id: root.ID, Name: root.Name, Order: root.Order})
	}
	writeJSON(w, http.StatusOK, api.FileRootListResponse{FileRoots: items})
}

// listFileRootEntries 列举文件根下某个目录的一页内容。
//
// 分页语义与 Catalog 查询**不同且不可混用**：文件系统是实时的，没有 publication 快照，因此续页
// 只保证「从锚点之后继续」，不保证可重复读。这一点在契约中显式声明，不假装提供快照一致性。
func (s *Server) listFileRootEntries(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireCapability(r, "files.browse"); err != nil {
		s.writeRequestError(w, concealForbidden(err))
		return
	}
	root, ok := s.fileRoots.Lookup(r.PathValue("rootId"))
	if !ok {
		s.writeRequestError(w, fault.New(fault.CodeNotFound, false, nil))
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			s.writeRequestError(w, fault.WithField(fault.CodeValidation, "limit", nil))
			return
		}
		limit = parsed
	}
	page, err := fileroot.ListEntries(root, r.URL.Query().Get("path"), r.URL.Query().Get("after"), limit)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	items := make([]api.FileRootEntry, 0, len(page.Entries))
	for _, entry := range page.Entries {
		items = append(items, api.FileRootEntry{
			Name: entry.Name, RelativePath: entry.RelativePath,
			Kind:      api.FileRootEntryKind(entry.Kind),
			SizeBytes: optionalInt(entry.SizeBytes), ModifiedUnix: optionalInt(entry.ModifiedUnix),
		})
	}
	response := api.FileRootEntryListResponse{RootId: root.ID, Entries: items}
	if page.NextAfter != "" {
		next := page.NextAfter
		response.NextAfter = &next
	}
	writeJSON(w, http.StatusOK, response)
}

// optionalInt 把可选的 int64 转成生成 DTO 使用的 *int。nil 保持 nil：目录与链接没有大小，
// 必须表达为 null 而不是 0。
func optionalInt(value *int64) *int {
	if value == nil {
		return nil
	}
	converted := int(*value)
	return &converted
}
