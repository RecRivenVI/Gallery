package sourceguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// identifiableNames 是本测试刻意植入的、形似真实作者/作品目录名的合成名字。它们全部
// 由本测试生成，不来自任何真实来源。
var identifiableNames = []string{
	"creator-Kagamine-Rin",
	"2024-03-05_06-07-08_123456789",
	"very-specific-work-title",
	"photo-of-something.jpg",
	"metadata.json",
}

// TestSaveManifestDoesNotPersistRealNames 是 GUARD-2 的核心回归：落盘清单里不得出现
// 任何真实相对路径分量。
//
// 缺陷形态：SaveManifest 把 Entries 全量写进 JSON，每条含 RelativePath——即真实作者名
// 与作品目录名。而这些名字对验证毫无作用：Manifest.Entries 的 JSON tag 是 `-`，回读时
// 根本读不回来，Equal 也只比较计数与 guard 摘要。纯泄露面、零收益。
func TestSaveManifestDoesNotPersistRealNames(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root,
		identifiableNames[0]+"/"+identifiableNames[1]+"/"+identifiableNames[3],
		identifiableNames[0]+"/"+identifiableNames[1]+"/"+identifiableNames[4],
		identifiableNames[0]+"/"+identifiableNames[2]+"/"+identifiableNames[3],
	)
	manifest, err := Walk(root)
	if err != nil {
		t.Fatal(err)
	}
	manifest.RootAlias = "platform-alias"

	out := filepath.Join(t.TempDir(), "manifest.json")
	if err := SaveManifest(manifest, out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, name := range identifiableNames {
		if strings.Contains(text, name) {
			t.Fatalf("落盘清单包含真实名字 %q", name)
		}
	}
	// 路径分隔符本身也不应出现：条目只写摘要，没有任何层级路径。
	if strings.Contains(text, `\\`) || strings.Contains(text, `/2024`) {
		t.Fatal("落盘清单包含路径形态文本")
	}
}

// TestLoadManifestReadsBackEntriesAndDiffs 证明落盘条目**真的被读回并参与判定**：
// 修复前 decodeManifest 读不回任何条目，逐条差异无从谈起。
func TestLoadManifestReadsBackEntriesAndDiffs(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, "authorA/work1/a.jpg", "authorA/work1/metadata.json", "authorB/work2/b.jpg")
	before, err := Walk(root)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "manifest.json")
	if err := SaveManifest(before, out); err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadManifest(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Entries) != len(before.Entries) {
		t.Fatalf("回读条目数 = %d want %d", len(baseline.Entries), len(before.Entries))
	}
	if diff := DiffPersisted(baseline, before.Persisted()); diff.Changed() {
		t.Fatalf("同一棵树的逐条差异应为空: %+v", diff)
	}

	writeTree(t, root, "authorC/work3/c.jpg")
	if err := os.Remove(filepath.Join(root, "authorB", "work2", "b.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "authorA", "work1", "a.jpg"), []byte("changed-size"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := Walk(root)
	if err != nil {
		t.Fatal(err)
	}
	diff := DiffPersisted(baseline, after.Persisted())
	// 新增 authorC 目录 + authorC/work3 目录 + c.jpg = 3 条新增；删除 b.jpg = 1 条；
	// 至少 a.jpg 的大小与 mtime 变化算一条修改（受影响目录自身的 mtime 也会变，因此
	// 修改数只断言下界，不写死一个依赖文件系统目录时间戳语义的精确值）。
	if diff.Added != 3 || diff.Removed != 1 || diff.Modified < 1 {
		t.Fatalf("逐条差异 = %+v want {Added:3 Removed:1 Modified:>=1}", diff)
	}
	if after.EqualPersisted(baseline) {
		t.Fatal("发生写入后 guard 仍判定一致")
	}
}

// TestLoadManifestRejectsEmptyBaseline 保证一份空基线不会被当作可用基线继续比较。
func TestLoadManifestRejectsEmptyBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":2,"fileCount":0,"dirCount":0,"linkCount":0,"entries":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(path); err == nil {
		t.Fatal("空基线必须被拒绝")
	}
}

// TestContentHashingDetectsSameSizeSameMtimeRewrite 说明默认清单的检测边界，并证明
// 启用内容哈希后能覆盖它：保持大小与 mtime 不变的原地改写，只靠 size/mtime 发现不了。
func TestContentHashingDetectsSameSizeSameMtimeRewrite(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "authorA", "work1", "a.bin")
	writeTree(t, root, "authorA/work1/a.bin")
	if err := os.WriteFile(target, []byte("AAAA"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	stamp := info.ModTime()

	plainBefore, err := Walk(root)
	if err != nil {
		t.Fatal(err)
	}
	hashedBefore, err := WalkWithOptions(root, Options{HashContent: true})
	if err != nil {
		t.Fatal(err)
	}
	if hashedBefore.HashedFileCount != 1 {
		t.Fatalf("hashedFileCount = %d want 1", hashedBefore.HashedFileCount)
	}

	if err := os.WriteFile(target, []byte("BBBB"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(target, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	plainAfter, err := Walk(root)
	if err != nil {
		t.Fatal(err)
	}
	if !plainBefore.Equal(plainAfter) {
		t.Skip("本文件系统的 mtime 粒度或行为使改写仍可被 size/mtime 发现，跳过该边界说明")
	}
	hashedAfter, err := WalkWithOptions(root, Options{HashContent: true})
	if err != nil {
		t.Fatal(err)
	}
	if hashedBefore.Equal(hashedAfter) {
		t.Fatal("启用内容哈希后仍未发现同大小同 mtime 的原地改写")
	}
}

// TestContentHashingRespectsBounds 锁定有界哈希：超过文件数上限后不再继续读取内容，
// 且选择顺序按相对路径排序，因此同一棵树上可复现。
func TestContentHashingRespectsBounds(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, "a/1.bin", "a/2.bin", "a/3.bin", "b/4.bin")
	manifest, err := WalkWithOptions(root, Options{HashContent: true, MaxHashFiles: 2})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.HashedFileCount != 2 {
		t.Fatalf("hashedFileCount = %d want 2", manifest.HashedFileCount)
	}
	hashed := map[string]bool{}
	for _, entry := range manifest.Entries {
		if entry.ContentSHA256 != "" {
			hashed[entry.RelativePath] = true
		}
	}
	for _, wanted := range []string{filepath.Join("a", "1.bin"), filepath.Join("a", "2.bin")} {
		if !hashed[wanted] {
			t.Fatalf("有界哈希未按排序顺序选择: %+v", hashed)
		}
	}

	repeat, err := WalkWithOptions(root, Options{HashContent: true, MaxHashFiles: 2})
	if err != nil {
		t.Fatal(err)
	}
	if repeat.GuardSHA256 != manifest.GuardSHA256 {
		t.Fatal("同一棵树上的有界哈希必须可复现")
	}
}

// TestGuardDigestIsDerivableFromPersistedForm 保证 guard 摘要只依赖落盘形态：拿到清单
// 文件的人可以独立重算，而不需要真实路径。
func TestGuardDigestIsDerivableFromPersistedForm(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, "authorA/work1/a.jpg")
	manifest, err := Walk(root)
	if err != nil {
		t.Fatal(err)
	}
	persisted := manifest.Persisted()
	if persisted.GuardSHA256 != manifest.GuardSHA256 {
		t.Fatal("落盘形态与内存形态的 guard 摘要不一致")
	}
	if persisted.SchemaVersion != ManifestSchemaVersion {
		t.Fatalf("schemaVersion = %d want %d", persisted.SchemaVersion, ManifestSchemaVersion)
	}
}
