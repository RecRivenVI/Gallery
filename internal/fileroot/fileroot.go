// Package fileroot 实现只读的「文件根」浏览：直接列举文件系统目录，不产生任何 Catalog 事实。
//
// **文件根是独立于 Source 的领域概念**，不是 Source 的一种变体。三条各自独立的理由：
//
//  1. 物理上不可能作为 Source 存在。真实规则把 `F:\Gallery` 声明为文件根，而各平台 Source 是
//     `F:\Gallery\gallery-dl\Galleries\pixiv` 等——AppDirs 的 ValidateDisjoint 做的是**双向包含**
//     判定，祖先关系必然命中 `SOURCE_ROOTS_OVERLAP`。放宽那个判定不可接受：同一个函数同时承担
//     AppDirs 保护，削弱它等于削弱「数据库、缓存、日志不得落在 Source 内」这条边界。
//
//  2. Source 承载的领域义务文件根一概不需要：Source 必须归属 Library、是扫描与规则绑定的单位、
//     是 Catalog revision 成员事实的主体。文件根是**实时只读视图**，不产生 Work、不进 publication、
//     不绑定规则、不被扫描。塞进 Source 会立刻在成员完整性复核与扫描调度上产生「是成员却无投影」
//     的歧义。
//
//  3. 声明形状本身不同：文件根的 id 是配置提供的稳定字符串（如 `files`），不是 `src_` UUIDv7。
//
// 与 Source 共享的是**路径安全原语**而不是领域类型：相对路径校验、句柄式安全打开与根内包含判定
// 全部复用 internal/media 中已经过审计的实现。
package fileroot

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/media"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/querytext"
)

// Root 是一个已登记的只读文件根。
type Root struct {
	// ID 是配置提供的稳定标识，不是领域 UUID。
	ID string
	// Name 是显示名。
	Name string
	// Path 是解析后的绝对根路径。它**只在服务端使用**，绝不出现在任何对外响应或日志中。
	Path string
	// Order 是展示顺序。
	Order int
}

// EntryKind 区分目录项类型。
//
// `link` 是独立的第三态，不是 file 也不是 directory：Windows junction 的 `IsDir()` 为 false 且
// `Size()` 无意义，若把它并入 file，客户端会看到一个 0 字节的普通文件——那是**错误信息**，比不显示
// 更糟。同时浏览是「呈现真实文件系统」，静默隐藏用户在资源管理器里看得见的目录项本身就是缺陷，
// 因此这里选择「可见但不可下降」，与扫描器的「跳过」是不同场景下的不同正确答案。
const (
	EntryKindFile      = "file"
	EntryKindDirectory = "directory"
	EntryKindLink      = "link"
)

// Entry 是一个目录项。
//
// 刻意不含绝对路径、不含链接目标：`EvalSymlinks` 的结果是绝对路径，把它返回给客户端会直接违反
// 「错误与响应不泄露绝对路径」这条边界。客户端只需要知道「这是一个链接」，不需要知道它指向哪。
type Entry struct {
	Name string `json:"name"`
	// RelativePath 是相对文件根的斜杠路径，可直接用于下一次列举。
	RelativePath string `json:"relativePath"`
	Kind         string `json:"kind"`
	// SizeBytes 只对普通文件有意义；目录与链接为 nil，不用 0 冒充。
	SizeBytes *int64 `json:"sizeBytes,omitempty"`
	// ModifiedUnix 是最后修改时间；无法取得时为 nil。
	ModifiedUnix *int64 `json:"modifiedUnix,omitempty"`
	// sortKey 是自然排序键，不对外暴露。
	sortKey string
}

// Registry 持有当前进程可浏览的文件根集合。
type Registry struct {
	roots []Root
}

// NewRegistry 校验并登记文件根。
//
// 校验刻意**不复用** AppDirs 的 ValidateDisjoint：那个函数的契约是「传入的路径两两不相交」，
// 而文件根与平台 Source 之间正是祖先关系。把文件根混进那个集合会立刻误判为重叠；把它改成可以
// 豁免又会削弱 AppDirs 保护。因此这里用一组方向明确的独立规则：
//
//   - 文件根必须与全部 AppDirs 可写根不相交（暴露 AppDirs 会泄露数据库、备份与会话状态）；
//   - 文件根之间必须两两不相交（否则同一文件有多个身份，分页与去重都会歧义）；
//   - 文件根**可以**是 Source 的祖先（真实配置的形状），但**不得**是 Source 的后代——后者会造成
//     「同一目录树既在文件根内又在文件根外」的不可解释状态。
func NewRegistry(fileSystem interface {
	Abs(string) (string, error)
	EvalSymlinks(string) (string, error)
}, declared []Root, appDirsWriteRoots, sourceRoots []string) (*Registry, error) {
	resolvedWrites, err := canonicalizeAll(fileSystem, appDirsWriteRoots)
	if err != nil {
		return nil, err
	}
	resolvedSources, err := canonicalizeAll(fileSystem, sourceRoots)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	result := make([]Root, 0, len(declared))
	for _, root := range declared {
		if root.ID == "" {
			return nil, fault.WithField(fault.CodeConfigInvalid, "id", errors.New("文件根缺少 id"))
		}
		if _, duplicate := seen[root.ID]; duplicate {
			return nil, fault.WithField(fault.CodeConfigInvalid, "id", errors.New("文件根 id 重复"))
		}
		seen[root.ID] = struct{}{}
		resolved, err := canonicalize(fileSystem, root.Path)
		if err != nil {
			return nil, err
		}
		for _, write := range resolvedWrites {
			if overlaps(resolved, write) {
				return nil, fault.New(fault.CodeAppDirsOverlap, false, nil)
			}
		}
		for _, existing := range result {
			if overlaps(resolved, existing.Path) {
				return nil, fault.New(fault.CodeSourceRootsOverlap, false, nil)
			}
		}
		for _, source := range resolvedSources {
			// 文件根是 Source 的后代属于不可解释的配置；祖先与无关都允许。
			if contains(source, resolved) && !equalPath(source, resolved) {
				return nil, fault.New(fault.CodeSourceRootsOverlap, false, nil)
			}
		}
		root.Path = resolved
		result = append(result, root)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Order != result[j].Order {
			return result[i].Order < result[j].Order
		}
		return result[i].ID < result[j].ID
	})
	return &Registry{roots: result}, nil
}

// List 返回全部已登记文件根，按展示顺序排列。
func (r *Registry) List() []Root {
	if r == nil {
		return nil
	}
	return append([]Root(nil), r.roots...)
}

// Lookup 按 ID 取文件根。
func (r *Registry) Lookup(id string) (Root, bool) {
	if r == nil {
		return Root{}, false
	}
	for _, root := range r.roots {
		if root.ID == id {
			return root, true
		}
	}
	return Root{}, false
}

// maxEntriesPerPage 是单页条目上限。目录列举直接读文件系统，没有 publication 快照可依托，
// 因此必须由服务端封顶，不能让客户端用一个巨大的 limit 把整个目录一次读进内存。
const maxEntriesPerPage = 500

// Page 是一页目录内容。
type Page struct {
	Entries []Entry
	// NextAfter 是下一页的续页锚点（上一页最后一项的排序键与名称）。为空表示已到末尾。
	NextAfter string
}

// ListEntries 列举文件根下某个相对目录的一页内容。
//
// **分页语义与 Catalog 查询不同，且必须如实声明**：文件系统是实时的，没有 publication 快照，
// 因此续页只保证「从锚点之后继续」，不保证可重复读——两次请求之间目录发生变化时可能漏项或重复。
// Catalog 查询那套 publication + 租约的一致性承诺在这里做不到，也不应假装做到。
//
// 排序固定为自然序（与作品媒体顺序同一套 querytext.NaturalSortKey），使 `2` 排在 `10` 之前。
func ListEntries(root Root, relative, after string, limit int) (Page, error) {
	if limit <= 0 || limit > maxEntriesPerPage {
		limit = maxEntriesPerPage
	}
	target := root.Path
	normalized := ""
	if relative != "" {
		var err error
		normalized, err = media.ValidateRelativePath(relative)
		if err != nil {
			return Page{}, err
		}
		target = filepath.Join(root.Path, filepath.FromSlash(normalized))
	}
	// 显式拒绝下降进链接，**不依赖越界判定**：指向根内另一位置的链接会通过包含检查，从而制造
	// 无限递归路径与重复条目。这里用 Lstat 判定目标本身是不是链接，与列举时的判定同一套语义。
	if normalized != "" {
		if info, err := os.Lstat(target); err == nil && filesystem.IsLink(info.Mode()) {
			return Page{}, fault.New(fault.CodePathEscape, false, nil)
		}
	}
	// 与媒体读取同一条越界防线：解析后再判定包含，避免「用已解析的根比未解析的目标」这一经典错配。
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return Page{}, readFault(err)
	}
	if !contains(root.Path, resolved) {
		return Page{}, fault.New(fault.CodePathEscape, false, nil)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return Page{}, readFault(err)
	}
	if !info.IsDir() {
		return Page{}, fault.New(fault.CodeValidation, false, nil)
	}
	children, err := os.ReadDir(resolved)
	if err != nil {
		return Page{}, readFault(err)
	}
	entries := make([]Entry, 0, len(children))
	for _, child := range children {
		entry := Entry{
			Name:         child.Name(),
			RelativePath: path.Join(normalized, child.Name()),
			sortKey:      querytext.NaturalSortKey(child.Name()),
		}
		switch {
		case filesystem.IsLink(child.Type()):
			// 链接可见但不可下降，且不暴露大小与目标。
			entry.Kind = EntryKindLink
		case child.IsDir():
			entry.Kind = EntryKindDirectory
		default:
			entry.Kind = EntryKindFile
			if childInfo, infoErr := child.Info(); infoErr == nil {
				size := childInfo.Size()
				modified := childInfo.ModTime().Unix()
				entry.SizeBytes, entry.ModifiedUnix = &size, &modified
			}
		}
		if entry.Kind != EntryKindFile {
			if childInfo, infoErr := child.Info(); infoErr == nil {
				modified := childInfo.ModTime().Unix()
				entry.ModifiedUnix = &modified
			}
		}
		entries = append(entries, entry)
	}
	// 目录在前、同类按自然序，末尾以名称做确定性并列判据。
	sort.SliceStable(entries, func(i, j int) bool {
		left, right := entries[i], entries[j]
		if directoryFirstRank(left.Kind) != directoryFirstRank(right.Kind) {
			return directoryFirstRank(left.Kind) < directoryFirstRank(right.Kind)
		}
		if left.sortKey != right.sortKey {
			return left.sortKey < right.sortKey
		}
		return left.Name < right.Name
	})
	start := 0
	if after != "" {
		for index, entry := range entries {
			if pageAnchor(entry) > after {
				start = index
				break
			}
			start = index + 1
		}
	}
	if start > len(entries) {
		start = len(entries)
	}
	end := start + limit
	if end > len(entries) {
		end = len(entries)
	}
	page := Page{Entries: entries[start:end]}
	if end < len(entries) {
		page.NextAfter = pageAnchor(entries[end-1])
	}
	for index := range page.Entries {
		page.Entries[index].sortKey = ""
	}
	return page, nil
}

// pageAnchor 把一个条目编码为续页锚点。它只包含排序所需的信息，不含绝对路径。
func pageAnchor(entry Entry) string {
	return directoryFirstRankString(entry.Kind) + "\x00" + entry.sortKey + "\x00" + entry.Name
}

func directoryFirstRank(kind string) int {
	if kind == EntryKindDirectory {
		return 0
	}
	return 1
}

func directoryFirstRankString(kind string) string {
	if kind == EntryKindDirectory {
		return "0"
	}
	return "1"
}

func canonicalizeAll(fileSystem interface {
	Abs(string) (string, error)
	EvalSymlinks(string) (string, error)
}, paths []string) ([]string, error) {
	result := make([]string, 0, len(paths))
	for _, item := range paths {
		resolved, err := canonicalize(fileSystem, item)
		if err != nil {
			return nil, err
		}
		result = append(result, resolved)
	}
	return result, nil
}

// canonicalize 解析为绝对路径并展开链接，随后在 Windows 上折叠大小写。
//
// 大小写折叠不可省略：`filepath.Rel` 在 Windows 上是大小写敏感的字符串运算，而文件根与 Source
// 的路径来自不同配置来源，大小写未必一致。AppDirs 的守卫做了同样的折叠，这里必须保持一致，
// 否则 `F:\Gallery` 与 `f:\gallery` 会被判为互不包含。
func canonicalize(fileSystem interface {
	Abs(string) (string, error)
	EvalSymlinks(string) (string, error)
}, value string) (string, error) {
	absolute, err := fileSystem.Abs(value)
	if err != nil {
		return "", fault.WithField(fault.CodeConfigInvalid, "path", err)
	}
	absolute = filepath.Clean(absolute)
	if resolved, err := fileSystem.EvalSymlinks(absolute); err == nil {
		absolute = filepath.Clean(resolved)
	}
	return compareForm(absolute), nil
}

func compareForm(value string) string {
	if filepath.Separator == '\\' {
		return strings.ToLower(value)
	}
	return value
}

func overlaps(left, right string) bool { return contains(left, right) || contains(right, left) }

func contains(parent, child string) bool {
	relative, err := filepath.Rel(compareForm(parent), compareForm(child))
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}

func equalPath(left, right string) bool { return compareForm(left) == compareForm(right) }

func readFault(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fault.New(fault.CodeNotFound, false, nil)
	}
	if errors.Is(err, fs.ErrPermission) {
		return fault.New(fault.CodeSourceReadFailed, true, nil)
	}
	return fault.New(fault.CodeSourceUnavailable, true, err)
}
