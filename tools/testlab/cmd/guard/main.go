// Command testlabguard 独立执行真实只读 Source 的零写入 guard：在触碰真实 Source
// 前后各生成一份 internal/sourceguard 清单，并比较二者，证明测试过程没有以任何
// 方式修改 Source 本身。可用于在没有完整 testlabprobe 运行的情况下单独复核某次
// 真实 Source 操作前后的状态，或作为其它脚本调用的构建块。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/RecRivenVI/gallery/tools/testlab/internal/sourceguard"
)

func decodeManifest(data []byte, out *sourceguard.Manifest) error {
	return json.Unmarshal(data, out)
}

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) < 2 {
		usage()
		return 2
	}
	switch os.Args[1] {
	case "snapshot":
		return runSnapshot(os.Args[2:])
	case "verify":
		return runVerify(os.Args[2:])
	default:
		usage()
		return 2
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "用法:")
	fmt.Fprintln(os.Stderr, "  testlabguard snapshot -root <真实 Source 根或有界子目录> -out <manifest.json> [-alias <脱敏代号>]")
	fmt.Fprintln(os.Stderr, "  testlabguard verify   -root <同上> -baseline <snapshot 产出的 manifest.json>")
}

func runSnapshot(args []string) int {
	fs := flag.NewFlagSet("snapshot", flag.ExitOnError)
	root := fs.String("root", "", "要生成清单的真实只读 Source 根或有界子目录")
	out := fs.String("out", "", "清单 JSON 输出路径")
	alias := fs.String("alias", "", "写入清单的脱敏来源代号（不写真实路径）")
	fs.Parse(args)
	if *root == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "snapshot 必须指定 -root 与 -out")
		return 2
	}
	manifest, err := sourceguard.Walk(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "walk: %v\n", err)
		return 1
	}
	manifest.RootAlias = *alias
	if err := sourceguard.SaveManifest(manifest, *out); err != nil {
		fmt.Fprintf(os.Stderr, "save manifest: %v\n", err)
		return 1
	}
	fmt.Printf("testlabguard snapshot: fileCount=%d dirCount=%d totalBytes=%d guardSha256=%s\n",
		manifest.FileCount, manifest.DirCount, manifest.TotalBytes, manifest.GuardSHA256)
	return 0
}

func runVerify(args []string) int {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	root := fs.String("root", "", "要重新生成清单并与基线比较的真实只读 Source 根或有界子目录")
	baseline := fs.String("baseline", "", "snapshot 产出的基线 manifest JSON 路径")
	fs.Parse(args)
	if *root == "" || *baseline == "" {
		fmt.Fprintln(os.Stderr, "verify 必须指定 -root 与 -baseline")
		return 2
	}
	baselineData, err := os.ReadFile(*baseline)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read baseline: %v\n", err)
		return 1
	}
	var before sourceguard.Manifest
	if err := decodeManifest(baselineData, &before); err != nil {
		fmt.Fprintf(os.Stderr, "decode baseline: %v\n", err)
		return 1
	}
	after, err := sourceguard.Walk(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "walk: %v\n", err)
		return 1
	}
	if before.Equal(after) {
		fmt.Printf("testlabguard verify: PASS fileCount=%d dirCount=%d totalBytes=%d\n", after.FileCount, after.DirCount, after.TotalBytes)
		return 0
	}
	fmt.Printf("testlabguard verify: FAIL baselineFiles=%d nowFiles=%d baselineDirs=%d nowDirs=%d baselineBytes=%d nowBytes=%d baselineGuard=%s nowGuard=%s\n",
		before.FileCount, after.FileCount, before.DirCount, after.DirCount, before.TotalBytes, after.TotalBytes, before.GuardSHA256, after.GuardSHA256)
	return 1
}
