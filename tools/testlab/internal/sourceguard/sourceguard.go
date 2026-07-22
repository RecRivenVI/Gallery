// Package sourceguard 提供针对真实、授权只读 Source 的有界清单与零写入验证：在
// 触碰真实 Source 前生成一份只读清单及其排序后的 SHA-256 摘要，操作结束后重新
// 生成一份并比较，证明扫描/规则/媒体读取没有以任何方式修改 Source 本身。所有
// 阶段（stage3/stage4/未来阶段）对真实 Source 的验证都必须经由本包，不各自
// 重新实现清单遍历逻辑。
package sourceguard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Entry 是只读清单里的一条记录：只保留相对路径、大小、mtime 和类型，不包含
// metadata 原文或媒体内容。
type Entry struct {
	RelativePath string `json:"relativePath"`
	IsDir        bool   `json:"isDir"`
	SizeBytes    int64  `json:"sizeBytes"`
	ModUnixNanos int64  `json:"modUnixNanos"`
}

// Manifest 汇总只读清单与其排序后的 SHA-256 guard。
type Manifest struct {
	GeneratedAt string  `json:"generatedAt"`
	RootAlias   string  `json:"rootAlias"`
	FileCount   int     `json:"fileCount"`
	DirCount    int     `json:"dirCount"`
	TotalBytes  int64   `json:"totalBytes"`
	GuardSHA256 string  `json:"guardSha256"`
	Entries     []Entry `json:"-"`
}

// Equal 报告两份清单在文件数/目录数/总字节数/排序后摘要上是否完全一致，即
// Source 在两次清单之间没有发生任何可观察的写入。
func (m Manifest) Equal(other Manifest) bool {
	return m.GuardSHA256 == other.GuardSHA256 && m.FileCount == other.FileCount &&
		m.DirCount == other.DirCount && m.TotalBytes == other.TotalBytes
}

// Walk 对给定根做只读递归清单，不修改任何文件的访问时间以外的元数据（Go 的
// os.Stat 本身只读，不触碰 atime/mtime）。
func Walk(root string) (Manifest, error) {
	var entries []Entry
	var totalBytes int64
	fileCount, dirCount := 0, 0
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		entry := Entry{RelativePath: rel, IsDir: info.IsDir(), ModUnixNanos: info.ModTime().UnixNano()}
		if info.IsDir() {
			dirCount++
		} else {
			fileCount++
			entry.SizeBytes = info.Size()
			totalBytes += info.Size()
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return Manifest{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].RelativePath < entries[j].RelativePath })

	hasher := sha256.New()
	for _, entry := range entries {
		fmt.Fprintf(hasher, "%s|%v|%d|%d\n", entry.RelativePath, entry.IsDir, entry.SizeBytes, entry.ModUnixNanos)
	}
	manifest := Manifest{
		FileCount: fileCount, DirCount: dirCount, TotalBytes: totalBytes,
		GuardSHA256: hex.EncodeToString(hasher.Sum(nil)), Entries: entries,
	}
	return manifest, nil
}

// SaveManifest 把清单写为脱敏摘要 + 完整条目列表的 JSON（RootAlias 由调用方传入
// 一个不透露物理路径的逻辑代号，不写真实 Source 路径本身）。
func SaveManifest(manifest Manifest, path string) error {
	manifest.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	encoded, err := json.MarshalIndent(struct {
		Manifest
		Entries []Entry `json:"entries"`
	}{manifest, manifest.Entries}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o644)
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
