package sourceguard

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/RecRivenVI/gallery/tools/testlab/internal/bounds"
)

// Census 是一次有界枚举的结果。它只含计数、字节数与深度，不含任何名字。
type Census struct {
	Outcome    bounds.Outcome `json:"outcome"`
	Links      int            `json:"links"`
	TotalBytes int64          `json:"totalBytes"`
	MaxDepth   int            `json:"maxDepth"`
	// TopLevelDirs 是根的直接子目录数（author_work 结构下即作者目录数），在被边界
	// 截断时仍然准确，因为第一层总是完整读完的。
	TopLevelDirs int `json:"topLevelDirectories"`
}

// TakeCensus 在给定上限内枚举 root，一旦触顶立即停止并如实记录停止原因。
//
// 它刻意**不复用 Walk**：Walk 必须完整遍历才能产出可比较的 guard 摘要，而 census 的
// 全部意义就是允许不完整。把两者合并会诱使调用方拿一份被截断的清单当 guard 基线用。
func TakeCensus(root string, limits bounds.Limits) (Census, error) {
	rootInfo, err := os.Stat(root)
	if err != nil {
		return Census{}, fmt.Errorf("census 根不可读: %w", err)
	}
	if !rootInfo.IsDir() {
		return Census{}, fmt.Errorf("census 根不是目录（跟随链接后仍为 %s）", rootInfo.Mode().Type())
	}

	started := time.Now()
	budget := limits.Start(time.Now)
	census := Census{}
	err = censusDirectory(root, 1, budget, &census)
	census.Outcome = budget.Outcome(time.Since(started))
	if err != nil {
		return census, err
	}
	if census.Outcome.Dirs == 0 && census.Outcome.Files == 0 && census.Links == 0 {
		return census, fmt.Errorf("census 结果为空（0 文件、0 目录、0 链接）；根不可用或边界设为 0")
	}
	return census, nil
}

func censusDirectory(dir string, depth int, budget *bounds.Budget, census *Census) error {
	if depth > census.MaxDepth {
		census.MaxDepth = depth
	}
	children, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("census 读取目录失败: %w", err)
	}
	names := make([]string, 0, len(children))
	for _, child := range children {
		names = append(names, child.Name())
	}
	sort.Strings(names)
	if depth == 1 {
		for _, name := range names {
			info, statErr := os.Lstat(filepath.Join(dir, name))
			if statErr == nil && info.IsDir() && !isLink(info.Mode()) {
				census.TopLevelDirs++
			}
		}
	}
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return fmt.Errorf("census 读取条目属性失败: %w", statErr)
		}
		switch {
		case isLink(info.Mode()):
			census.Links++
		case info.IsDir():
			if !budget.AddDir() {
				return nil
			}
			if err := censusDirectory(path, depth+1, budget, census); err != nil {
				return err
			}
			if budget.Stopped() {
				return nil
			}
		default:
			if !budget.AddFile() {
				return nil
			}
			census.TotalBytes += info.Size()
		}
	}
	return nil
}
