// Command testlabguard 独立执行真实只读 Source 的零写入 guard：在触碰真实 Source
// 前后各生成一份 internal/sourceguard 清单，并比较二者，证明测试过程没有以任何
// 方式修改 Source 本身。可用于在没有完整 testlabprobe 运行的情况下单独复核某次
// 真实 Source 操作前后的状态，或作为其它脚本调用的构建块。
//
// 两条不可放宽的判定：清单为空一律非零退出（空清单自比必然相等，会把「什么都没有
// 守护」伪装成 PASS）；verify 的基线来自落盘清单，除计数与 guard 摘要外还逐条比较
// 路径摘要，给出新增/删除/修改的条目数。
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/RecRivenVI/gallery/tools/testlab/internal/sourceguard"
)

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
	fmt.Fprintln(os.Stderr, "  testlabguard snapshot -root <真实 Source 根或有界子目录> -out <manifest.json> [-alias <脱敏代号>] [-hash-content] [-max-hash-files N] [-max-hash-bytes N]")
	fmt.Fprintln(os.Stderr, "  testlabguard verify   -root <同上> -baseline <snapshot 产出的 manifest.json> [-hash-content] [-max-hash-files N] [-max-hash-bytes N]")
}

func hashFlags(fs *flag.FlagSet) func() sourceguard.Options {
	hashContent := fs.Bool("hash-content", false, "补充完整内容 SHA-256（发现保持大小与 mtime 不变的原地改写）")
	maxHashFiles := fs.Int("max-hash-files", 0, "参与内容哈希的文件数上限；0 表示不限制")
	maxHashBytes := fs.Int64("max-hash-bytes", 0, "内容哈希累计读取字节上限；0 表示不限制")
	return func() sourceguard.Options {
		return sourceguard.Options{
			HashContent: *hashContent, MaxHashFiles: *maxHashFiles, MaxHashBytes: *maxHashBytes,
		}
	}
}

// reportHashBound 如实说明本次内容哈希是否因边界停止。触顶时**必须**看得出来：
// 「只哈希了前 N 个文件」与「已全量校验内容」是两个完全不同的结论。
func reportHashBound(manifest sourceguard.Manifest) {
	if manifest.HashStopReason == "" {
		return
	}
	fmt.Printf("testlabguard: 内容哈希因边界停止（reason=%s hashedFiles=%d/%d hashedBytes=%d）；"+
		"本次只对已哈希子集覆盖「同大小同 mtime 原地改写」检测，未全量校验内容\n",
		manifest.HashStopReason, manifest.HashedFileCount, manifest.FileCount, manifest.HashedBytes)
}

func runSnapshot(args []string) int {
	fs := flag.NewFlagSet("snapshot", flag.ExitOnError)
	root := fs.String("root", "", "要生成清单的真实只读 Source 根或有界子目录")
	out := fs.String("out", "", "清单 JSON 输出路径")
	alias := fs.String("alias", "", "写入清单的脱敏来源代号（不写真实路径）")
	options := hashFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *root == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "snapshot 必须指定 -root 与 -out")
		return 2
	}
	manifest, err := sourceguard.WalkWithOptions(*root, options())
	if err != nil {
		fmt.Fprintf(os.Stderr, "walk: %v\n", err)
		return 1
	}
	manifest.RootAlias = *alias
	if err := sourceguard.SaveManifest(manifest, *out); err != nil {
		fmt.Fprintf(os.Stderr, "save manifest: %v\n", err)
		return 1
	}
	fmt.Printf("testlabguard snapshot: fileCount=%d dirCount=%d linkCount=%d totalBytes=%d hashedFiles=%d hashedBytes=%d guardSha256=%s\n",
		manifest.FileCount, manifest.DirCount, manifest.LinkCount, manifest.TotalBytes,
		manifest.HashedFileCount, manifest.HashedBytes, manifest.GuardSHA256)
	reportHashBound(manifest)
	return 0
}

func runVerify(args []string) int {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	root := fs.String("root", "", "要重新生成清单并与基线比较的真实只读 Source 根或有界子目录")
	baseline := fs.String("baseline", "", "snapshot 产出的基线 manifest JSON 路径")
	options := hashFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *root == "" || *baseline == "" {
		fmt.Fprintln(os.Stderr, "verify 必须指定 -root 与 -baseline")
		return 2
	}
	before, err := sourceguard.LoadManifest(*baseline)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load baseline: %v\n", err)
		return 1
	}
	after, err := sourceguard.WalkWithOptions(*root, options())
	if err != nil {
		fmt.Fprintf(os.Stderr, "walk: %v\n", err)
		return 1
	}
	diff := sourceguard.DiffPersisted(before, after.Persisted())
	if after.EqualPersisted(before) && !diff.Changed() {
		fmt.Printf("testlabguard verify: PASS fileCount=%d dirCount=%d linkCount=%d totalBytes=%d\n",
			after.FileCount, after.DirCount, after.LinkCount, after.TotalBytes)
		reportHashBound(after)
		return 0
	}
	fmt.Printf("testlabguard verify: FAIL baselineFiles=%d nowFiles=%d baselineDirs=%d nowDirs=%d baselineLinks=%d nowLinks=%d baselineBytes=%d nowBytes=%d added=%d removed=%d modified=%d\n",
		before.FileCount, after.FileCount, before.DirCount, after.DirCount, before.LinkCount, after.LinkCount,
		before.TotalBytes, after.TotalBytes, diff.Added, diff.Removed, diff.Modified)
	return 1
}
