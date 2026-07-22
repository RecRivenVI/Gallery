// Command testlabinventory 打印两个测试根（SSD/HDD）当前 stages/*/{manifests,
// reports,runs} 下已有内容的结构化摘要：每个阶段目录下有哪些场景别名、对应的
// manifest/report 文件是否存在、文件大小。用于在开始新一轮测试前快速确认已有
// 哪些结果，避免每次都手工用 PowerShell 罗列目录。不读取、不解释 metadata 原文，
// 只报告文件名和大小；不扫描配置之外的路径。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/RecRivenVI/gallery/tools/testlab/internal/config"
)

func main() {
	os.Exit(run())
}

func run() int {
	configPath := flag.String("config", "", "本地测试配置路径（通常是 Documents/本地/testlab.local.json）")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		return 1
	}

	roots := []struct {
		label string
		path  string
	}{
		{"SSD", cfg.TestRoots.SSD},
		{"HDD", cfg.TestRoots.HDD},
	}
	for _, root := range roots {
		fmt.Printf("=== %s 测试根 ===\n", root.label)
		markerPath := filepath.Join(root.path, ".gallery-test-lab-root.json")
		if _, err := os.Stat(markerPath); err != nil {
			fmt.Printf("  marker 缺失或不可读: %v\n", err)
			continue
		}
		for _, stage := range []string{"stage3", "stage4"} {
			stageDir := filepath.Join(root.path, "stages", stage)
			if _, err := os.Stat(stageDir); err != nil {
				continue
			}
			fmt.Printf("  stages/%s:\n", stage)
			for _, sub := range []string{"manifests", "reports", "runs"} {
				subDir := filepath.Join(stageDir, sub)
				entries, err := listFilesRecursive(subDir)
				if err != nil {
					continue
				}
				if len(entries) == 0 {
					continue
				}
				fmt.Printf("    %s/:\n", sub)
				for _, e := range entries {
					fmt.Printf("      %-60s %10d bytes\n", e.rel, e.size)
				}
			}
		}
	}
	return 0
}

type fileEntry struct {
	rel  string
	size int64
}

func listFilesRecursive(root string) ([]fileEntry, error) {
	var entries []fileEntry
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		entries = append(entries, fileEntry{rel: rel, size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	return entries, nil
}
