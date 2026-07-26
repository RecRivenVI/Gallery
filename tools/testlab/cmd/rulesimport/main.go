// Command testlabrulesimport 把一份真实旧配置（`gallery-rules.json`，schema_version 3）
// 一次性转换成逐平台 Gallery 规则包，并写出供 testlabprobe 消费的转换产物索引。
//
// 它取代了此前手写的 tools/testlab/fixtures/rules/<来源>/bounded-subdir-v1.json：手写夹具
// 与用户真实配置之间没有任何同步机制，一旦漂移，「规则验证通过」证明的只是夹具自洽。
//
// 三条不可放宽的边界：
//
//   - 旧配置路径必须由 -legacy-config 显式给出，本命令不猜测、不扫描磁盘、不内置默认路径；
//   - 产物含真实平台根路径，因此 -out-dir 必须位于授权测试根，写入任何 Git 工作树都会被拒绝；
//   - 标准输出只打印平台代号与计数，绝不打印平台名、路径或配置内容。
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/RecRivenVI/gallery/tools/testlab/internal/legacyrules"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/ruleindex"
)

func main() {
	os.Exit(run())
}

func run() int {
	legacyConfig := flag.String("legacy-config", "", "真实旧配置文件路径（gallery-rules.json，schema_version 3）；必填，不得省略")
	outDir := flag.String("out-dir", "", "转换产物输出目录；必须位于授权测试根内，不得位于任何 Git 工作树")
	flag.Parse()

	if *legacyConfig == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "必须指定 -legacy-config 与 -out-dir")
		return 2
	}
	if err := ruleindex.EnsureOutsideRepository(*outDir); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 2
	}

	bundle, err := legacyrules.ConvertFile(*legacyConfig)
	if err != nil {
		// 转换错误可能引用平台 ID；legacyrules 已经把它们换成代号，这里直接透传。
		fmt.Fprintf(os.Stderr, "convert: %v\n", err)
		return 1
	}
	if err := ruleindex.Save(bundle.Index, bundle.Packages, *outDir); err != nil {
		fmt.Fprintf(os.Stderr, "save: %v\n", err)
		return 1
	}

	fmt.Printf("testlabrulesimport: legacySchemaVersion=%d platforms=%d fileRoots=%d unconvertedFields=%d\n",
		bundle.Index.LegacySchemaVersion, len(bundle.Index.Entries), bundle.Index.FileRootCount,
		len(bundle.Index.UnconvertedByField))
	for _, entry := range bundle.Index.Entries {
		fmt.Printf("  平台 %s: primitives=%d\n", entry.PlatformCode, entry.PrimitiveCount)
	}
	fields := make([]string, 0, len(bundle.Index.UnconvertedByField))
	for field := range bundle.Index.UnconvertedByField {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		fmt.Printf("  未转换 %s ×%d\n", field, bundle.Index.UnconvertedByField[field])
	}
	fmt.Printf("  索引: <out-dir>/%s（含真实根路径，属本地制品，不得提交）\n", ruleindex.FileName)
	return 0
}
