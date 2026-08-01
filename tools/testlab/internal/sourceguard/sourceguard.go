// Package sourceguard 提供针对真实、授权只读 Source 的有界清单与零写入验证：在
// 触碰真实 Source 前生成一份只读清单及其排序后的 SHA-256 摘要，操作结束后重新
// 生成一份并比较，证明扫描/规则/媒体读取没有以任何方式修改 Source 本身。所有
// 阶段（stage3/stage4/未来阶段）对真实 Source 的验证都必须经由本包，不各自
// 重新实现清单遍历逻辑。
//
// 本包只依赖标准库：它同时被 testlabprobe 使用，而 probe 的既定边界是「只导入
// api 与标准库」。因此链接判定在这里本地实现，语义与
// internal/platform/filesystem.IsLink 完全一致，并由 sourceguard_link_parity_test.go
// 逐 mode 位锁定二者不漂移。
package sourceguard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/RecRivenVI/gallery/tools/testlab/internal/bounds"
)

// 清单条目的三种形态。链接单独成一类：它既不是可以安全下降的普通目录，也不是
// 普通文件，把它折叠进任何一类都会让「链接被替换成真实目录」这类改动逃过 guard。
const (
	KindFile = "file"
	KindDir  = "dir"
	KindLink = "link"
)

// isLink 报告某个目录项是否是「指向别处的链接」，而不是普通文件或普通目录。
//
// 与 internal/platform/filesystem.IsLink 同语义：Unix symlink 与 Windows symbolic
// link 报告为 fs.ModeSymlink；**Windows 的 junction（目录联接）与 volume mount point
// 报告为 fs.ModeIrregular 且 IsDir() 为 false**。只判断 fs.ModeSymlink 会完全漏掉
// junction —— 这正是 EV-46 `LINK-1` 的根因。
func isLink(mode fs.FileMode) bool {
	return mode&(fs.ModeSymlink|fs.ModeIrregular) != 0
}

// Entry 是内存中的清单条目：保留相对路径供进程内诊断，但**绝不落盘**（落盘形态见
// PersistedEntry，只写路径摘要）。不包含 metadata 原文或媒体内容。
type Entry struct {
	RelativePath  string
	Kind          string
	SizeBytes     int64
	ModUnixNanos  int64
	ContentSHA256 string
}

// PersistedEntry 是清单的落盘形态：真实作者名与作品目录名被替换为路径的 SHA-256
// 十六进制摘要。摘要仍然逐条唯一，因此可以回答「哪一条变了、变了多少条」，但不再
// 泄露任何真实名字。
type PersistedEntry struct {
	PathSHA256    string `json:"pathSha256"`
	Kind          string `json:"kind"`
	SizeBytes     int64  `json:"sizeBytes"`
	ModUnixNanos  int64  `json:"modUnixNanos"`
	ContentSHA256 string `json:"contentSha256,omitempty"`
}

// Manifest 汇总只读清单与其排序后的 SHA-256 guard。
type Manifest struct {
	GeneratedAt     string
	RootAlias       string
	FileCount       int
	DirCount        int
	LinkCount       int
	TotalBytes      int64
	HashedFileCount int
	HashedBytes     int64
	// HashStopReason 非空表示内容哈希**因边界停止**，本次清单只对前若干个文件覆盖了
	// 内容改写检测，不得声称已全量校验内容。
	HashStopReason string
	GuardSHA256    string
	Entries        []Entry
}

// PersistedManifest 是 Manifest 的落盘/回读形态。
type PersistedManifest struct {
	SchemaVersion   int              `json:"schemaVersion"`
	GeneratedAt     string           `json:"generatedAt"`
	RootAlias       string           `json:"rootAlias"`
	FileCount       int              `json:"fileCount"`
	DirCount        int              `json:"dirCount"`
	LinkCount       int              `json:"linkCount"`
	TotalBytes      int64            `json:"totalBytes"`
	HashedFileCount int              `json:"hashedFileCount"`
	HashedBytes     int64            `json:"hashedBytes"`
	HashStopReason  string           `json:"hashStoppedByBound,omitempty"`
	GuardSHA256     string           `json:"guardSha256"`
	Entries         []PersistedEntry `json:"entries"`
}

// ManifestSchemaVersion 随落盘结构变化递增。版本 2 起：条目只写路径摘要、区分
// file/dir/link 三类、可选内容摘要。
const ManifestSchemaVersion = 2

// Options 控制一次清单遍历的额外证据强度。
//
// 默认（零值）**关闭内容哈希**，只记录路径、类型、大小与 mtime——这足以发现增删、
// 改名、截断和大多数改写，但发现不了保持大小与 mtime 不变的原地内容改写。需要覆盖
// 这一类时显式启用 HashContent。
//
// MaxHashFiles/MaxHashBytes 是**硬边界**：触顶立即停止哈希，并在
// Manifest.HashStopReason 上留下停止原因。调用方必须把它带进报告，不得把「只哈希了
// 前 N 个文件」说成「已全量校验内容」。HDD 平台上不设边界会让一次 guard 退化成对整个
// 来源做完整读取。
type Options struct {
	// HashContent 为真时对文件补充完整 SHA-256 内容摘要。默认关闭。
	HashContent bool
	// MaxHashFiles 限制实际参与内容哈希的文件数；<=0 表示不限制。
	// 选择顺序按相对路径排序后取前 N 个，因此同一棵树上确定可复现。
	MaxHashFiles int
	// MaxHashBytes 限制内容哈希累计读取的字节数；<=0 表示不限制。
	MaxHashBytes int64
}

// Equal 报告两份清单在计数、总字节数与排序后摘要上是否完全一致，即 Source 在两次
// 清单之间没有发生任何可观察的写入。
func (m Manifest) Equal(other Manifest) bool {
	return m.GuardSHA256 == other.GuardSHA256 && m.FileCount == other.FileCount &&
		m.DirCount == other.DirCount && m.LinkCount == other.LinkCount &&
		m.TotalBytes == other.TotalBytes
}

// EqualPersisted 与 Equal 同语义，但比较对象是回读的落盘清单：verify 的基线来自
// 文件，不应为了比较而要求调用方重新遍历一次基线所在的树。
func (m Manifest) EqualPersisted(baseline PersistedManifest) bool {
	return m.GuardSHA256 == baseline.GuardSHA256 && m.FileCount == baseline.FileCount &&
		m.DirCount == baseline.DirCount && m.LinkCount == baseline.LinkCount &&
		m.TotalBytes == baseline.TotalBytes
}

// IsEmpty 报告清单是否一条记录都没有。
//
// 空清单永远是缺陷而不是有效结论：真实 Source 根不可能为空，而空清单与空清单自比
// 必然相等，会把「什么都没有守护」伪装成「已验证没有写入」。
func (m Manifest) IsEmpty() bool {
	return m.FileCount == 0 && m.DirCount == 0 && m.LinkCount == 0
}

// Diff 是两份清单之间的逐条差异计数。只报告数量，不报告任何名字。
type Diff struct {
	Added    int
	Removed  int
	Modified int
}

// Changed 报告是否存在任何逐条差异。
func (d Diff) Changed() bool { return d.Added != 0 || d.Removed != 0 || d.Modified != 0 }

// DiffPersisted 按路径摘要逐条比较两份落盘清单，给出新增/删除/修改的条目数。
// 它让基线文件里的条目真正被读回并参与判定，而不是只当作一堆无用的附加数据。
func DiffPersisted(before, after PersistedManifest) Diff {
	index := make(map[string]PersistedEntry, len(before.Entries))
	for _, entry := range before.Entries {
		index[entry.PathSHA256] = entry
	}
	var diff Diff
	for _, entry := range after.Entries {
		previous, ok := index[entry.PathSHA256]
		if !ok {
			diff.Added++
			continue
		}
		delete(index, entry.PathSHA256)
		if previous != entry {
			diff.Modified++
		}
	}
	diff.Removed = len(index)
	return diff
}

// Walk 对给定根做只读递归清单，只记录路径、类型、大小与 mtime。
func Walk(root string) (Manifest, error) {
	return WalkWithOptions(root, Options{})
}

// WalkWithOptions 对给定根做只读递归清单。
//
// 三条与正确性直接相关的规则：
//
//  1. **根按 os.Stat（跟随链接）判定**。用户把一个 Windows junction 显式登记为平台
//     根，就是要求把它当目录使用。若这里改用 Lstat，junction 根会被判成
//     fs.ModeIrregular 的「非目录」，遍历只产出它自己，清单恒为空，guard 完全空转。
//  2. **子树内部遇到的链接跳过且不递归，但必须计入清单**。不跟随是 EV-46 `LINK-1`
//     的既有裁决（跟随会导致重复计数与环）；计入是为了让「链接被替换成真实目录」
//     这类改动改变 guard 摘要，而不是悄悄通过。
//  3. **空清单直接判失败**。空清单自比必然相等，任何「PASS」都是假的。
func WalkWithOptions(root string, options Options) (Manifest, error) {
	rootInfo, err := os.Stat(root)
	if err != nil {
		return Manifest{}, fmt.Errorf("guard 根不可读: %w", err)
	}
	if !rootInfo.IsDir() {
		return Manifest{}, fmt.Errorf("guard 根不是目录（跟随链接后仍为 %s），拒绝生成清单", rootInfo.Mode().Type())
	}

	manifest := Manifest{}
	if err := walkDirectory(root, "", &manifest); err != nil {
		return Manifest{}, err
	}
	sort.Slice(manifest.Entries, func(i, j int) bool {
		return manifest.Entries[i].RelativePath < manifest.Entries[j].RelativePath
	})
	if manifest.IsEmpty() {
		return Manifest{}, fmt.Errorf("清单为空（0 文件、0 目录、0 链接），guard 无效；拒绝把空清单当作可比较基线")
	}
	if err := hashContents(root, &manifest, options); err != nil {
		return Manifest{}, err
	}
	manifest.GuardSHA256 = guardDigest(manifest.Entries)
	return manifest, nil
}

// walkDirectory 递归收集 dir 下的条目。relPrefix 是 dir 相对于 guard 根的路径。
func walkDirectory(dir, relPrefix string, manifest *Manifest) error {
	children, err := os.ReadDir(dir)
	if err != nil {
		// 读不动的目录一律判失败：静默跳过等价于缩小 guard 覆盖范围，而调用方无从
		// 得知本次证明其实少覆盖了一整棵子树。
		return fmt.Errorf("读取 guard 子目录失败: %w", err)
	}
	for _, child := range children {
		relative := child.Name()
		if relPrefix != "" {
			relative = filepath.Join(relPrefix, child.Name())
		}
		// 刻意使用 os.Lstat 而不是 DirEntry.Info()：Windows 上 ReadDir 返回的属性来自
		// **父目录的目录项缓存**，子目录的 mtime 在那里是惰性刷新的——同一棵未被修改的
		// 树连续遍历两次会得到不同的目录 mtime，进而让 guard 摘要在没有任何写入时也不
		// 相等（假阳性）。Lstat 直接查询对象本身，结果稳定；每项多一次系统调用是这条
		// 正确性必须付的代价。
		info, infoErr := os.Lstat(filepath.Join(dir, child.Name()))
		if infoErr != nil {
			return fmt.Errorf("读取 guard 条目属性失败: %w", infoErr)
		}
		entry := Entry{RelativePath: relative, ModUnixNanos: info.ModTime().UnixNano()}
		switch {
		case isLink(info.Mode()):
			entry.Kind = KindLink
			manifest.LinkCount++
			manifest.Entries = append(manifest.Entries, entry)
			// 不递归：不跟随链接是既有裁决。但条目本身已经进入清单。
		case info.IsDir():
			entry.Kind = KindDir
			manifest.DirCount++
			manifest.Entries = append(manifest.Entries, entry)
			if err := walkDirectory(filepath.Join(dir, child.Name()), relative, manifest); err != nil {
				return err
			}
		default:
			entry.Kind = KindFile
			entry.SizeBytes = info.Size()
			manifest.FileCount++
			manifest.TotalBytes += info.Size()
			manifest.Entries = append(manifest.Entries, entry)
		}
	}
	return nil
}

// hashContents 按 Options 给定的边界为已排序条目中的文件补充完整 SHA-256。
func hashContents(root string, manifest *Manifest, options Options) error {
	if !options.HashContent {
		return nil
	}
	for index := range manifest.Entries {
		entry := &manifest.Entries[index]
		if entry.Kind != KindFile {
			continue
		}
		if options.MaxHashFiles > 0 && manifest.HashedFileCount >= options.MaxHashFiles {
			manifest.HashStopReason = bounds.ReasonMaxHashFiles
			break
		}
		if options.MaxHashBytes > 0 && manifest.HashedBytes+entry.SizeBytes > options.MaxHashBytes {
			manifest.HashStopReason = bounds.ReasonMaxHashBytes
			break
		}
		digest, err := fileDigest(filepath.Join(root, entry.RelativePath))
		if err != nil {
			return err
		}
		entry.ContentSHA256 = digest
		manifest.HashedFileCount++
		manifest.HashedBytes += entry.SizeBytes
	}
	return nil
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("读取 guard 文件内容失败: %w", err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("计算 guard 内容摘要失败: %w", err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// pathDigest 是相对路径的 SHA-256 十六进制摘要，落盘时代替真实名字。
func pathDigest(relativePath string) string {
	sum := sha256.Sum256([]byte(relativePath))
	return hex.EncodeToString(sum[:])
}

// guardDigest 只对**落盘形态**求摘要，因此任何持有清单文件的人都能独立重算 guard，
// 而不需要拿到真实路径。
func guardDigest(entries []Entry) string {
	hasher := sha256.New()
	for _, entry := range entries {
		fmt.Fprintf(hasher, "%s|%s|%d|%d|%s\n",
			pathDigest(entry.RelativePath), entry.Kind, entry.SizeBytes, entry.ModUnixNanos, entry.ContentSHA256)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// Persisted 把内存清单转成落盘形态：逐条相对路径替换为其 SHA-256 摘要。
func (m Manifest) Persisted() PersistedManifest {
	entries := make([]PersistedEntry, 0, len(m.Entries))
	for _, entry := range m.Entries {
		entries = append(entries, PersistedEntry{
			PathSHA256: pathDigest(entry.RelativePath), Kind: entry.Kind,
			SizeBytes: entry.SizeBytes, ModUnixNanos: entry.ModUnixNanos,
			ContentSHA256: entry.ContentSHA256,
		})
	}
	return PersistedManifest{
		SchemaVersion: ManifestSchemaVersion, GeneratedAt: m.GeneratedAt, RootAlias: m.RootAlias,
		FileCount: m.FileCount, DirCount: m.DirCount, LinkCount: m.LinkCount,
		TotalBytes: m.TotalBytes, HashedFileCount: m.HashedFileCount, HashedBytes: m.HashedBytes,
		HashStopReason: m.HashStopReason, GuardSHA256: m.GuardSHA256, Entries: entries,
	}
}

// SaveManifest 把清单写为脱敏摘要 + 逐条**路径摘要**列表的 JSON。
//
// 落盘条目刻意不含真实相对路径：那些名字对验证毫无作用（比较只用摘要与计数），却是
// 纯粹的泄露面——它们正是真实作者名与作品目录名。RootAlias 由调用方传入一个不透露
// 物理路径的逻辑代号。
func SaveManifest(manifest Manifest, path string) error {
	if manifest.IsEmpty() {
		return fmt.Errorf("拒绝保存空清单：guard 无效")
	}
	manifest.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	encoded, err := json.MarshalIndent(manifest.Persisted(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o644)
}

// LoadManifest 回读落盘清单，并拒绝空清单基线。
func LoadManifest(path string) (PersistedManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PersistedManifest{}, err
	}
	var manifest PersistedManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return PersistedManifest{}, err
	}
	if manifest.FileCount == 0 && manifest.DirCount == 0 && manifest.LinkCount == 0 {
		return PersistedManifest{}, fmt.Errorf("基线清单为空（0 文件、0 目录、0 链接），guard 无效")
	}
	if len(manifest.Entries) == 0 {
		return PersistedManifest{}, fmt.Errorf("基线清单没有逐条记录，无法做逐条比较")
	}
	return manifest, nil
}

// errStopWalk 是 SelectBoundedSubdirectory 内部用于提前中止 filepath.Walk 的哨兵
// 错误，不代表真实的遍历失败。
var errStopWalk = fmt.Errorf("bounded walk stopped early")

// countFilesBounded 递归统计 dir 下的文件数，超过 maxFiles 立即停止（不遍历整个
// 可能很大的候选目录），返回统计到的数量（可能等于 maxFiles+1，表示"至少超限"）。
func countFilesBounded(dir string, maxFiles int) (int, error) {
	count := 0
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			count++
		}
		if count > maxFiles {
			return errStopWalk
		}
		return nil
	})
	if walkErr != nil && walkErr != errStopWalk {
		return count, walkErr
	}
	return count, nil
}

func sortedSubdirNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// SelectBoundedSubdirectory 在 root 下按名称排序做有界广度优先搜索，寻找第一个
// 文件总数（递归）落在 [minFiles, maxFiles] 区间内的目录，最多检查 maxCandidates
// 个候选、最多下探 maxDepth 层，保证选择过程本身也是有界、可重复、不遍历整个真实
// Source 的。真实来源的目录层级深度不总是"根 -> 作者"两层（例如某些来源在作者层
// 之上还有一层归类/桶目录，使根的直接子目录本身就聚合了成千上万个文件）：候选目录
// 递归文件数超过 maxFiles 时，将其直接子目录加入下一层候选继续搜索，而不是直接
// 判定失败；候选目录文件数低于 minFiles 时不再下探（更深层只会更小）。选择规则：
// 按层广度优先，同层内按名称排序，第一个满足区间条件的候选即为本次场景使用的
// 有界子集。
func SelectBoundedSubdirectory(root string, minFiles, maxFiles, maxCandidates int) (string, int, error) {
	const maxDepth = 4
	type queueItem struct {
		path  string
		depth int
	}
	names, err := sortedSubdirNames(root)
	if err != nil {
		return "", 0, err
	}
	queue := make([]queueItem, 0, len(names))
	for _, name := range names {
		queue = append(queue, queueItem{path: filepath.Join(root, name), depth: 1})
	}

	checked := 0
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		if checked >= maxCandidates {
			break
		}
		checked++
		count, err := countFilesBounded(item.path, maxFiles)
		if err != nil {
			return "", 0, err
		}
		childNames, childErr := sortedSubdirNames(item.path)
		hasSubdirs := childErr == nil && len(childNames) > 0
		// 候选必须自身还含有子目录（代表其下仍有 work 目录可供 author_work 规则的
		// work_directory glob 命中），否则会选中"work 本身"这一叶子目录作为
		// Source root，导致规则在其下找不到任何符合 glob 的子目录、判定为零候选
		// 作品（RULE_EVAL_ERROR），这不是产品缺陷，而是本选择算法此前遗漏的约束。
		if count >= minFiles && count <= maxFiles && hasSubdirs {
			return item.path, count, nil
		}
		if hasSubdirs && item.depth < maxDepth {
			for _, name := range childNames {
				queue = append(queue, queueItem{path: filepath.Join(item.path, name), depth: item.depth + 1})
			}
		}
	}
	return "", 0, fmt.Errorf("在前 %d 个候选目录（最多下探 %d 层）中未找到文件数落在 [%d,%d] 的有界子目录", maxCandidates, maxDepth, minFiles, maxFiles)
}
