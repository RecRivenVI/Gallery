package ruleindex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func sampleBundle() (Index, map[string]json.RawMessage) {
	code := Code("some-platform")
	index := Index{
		LegacySchemaVersion: 3, FileRootCount: 1,
		Entries: []Entry{{
			PlatformCode: code, RuleSetID: RuleSetID("some-platform"),
			SourceRoot: filepath.Join("X:", "real", "platform-root"), PrimitiveCount: 12,
		}},
		UnconvertedByField: map[string]int{"media.hide": 1},
	}
	packages := map[string]json.RawMessage{code: json.RawMessage(`{"rule_set_id":"x","primitives":[]}`)}
	return index, packages
}

func TestCodeIsStableAndHidesPlatformName(t *testing.T) {
	code := Code("微博_Legacy")
	if code != Code("微博_Legacy") {
		t.Fatal("同一平台必须得到同一代号")
	}
	if strings.Contains(code, "微博") || code == "微博_Legacy" {
		t.Fatalf("代号泄露了平台名: %q", code)
	}
	if Code("微博") == code {
		t.Fatal("不同平台必须得到不同代号")
	}
}

// TestRuleSetIDIsDeterministicAndSchemaValid 锁定两条性质：确定性（重复导入复用同一
// 规则集，否则「续跑没有重做」无从谈起）与合法性（必须满足规则包 Schema 的 pattern）。
func TestRuleSetIDIsDeterministicAndSchemaValid(t *testing.T) {
	pattern := regexp.MustCompile(`^rset_[0-9a-f-]{36}$`)
	for _, platform := range []string{"alpha", "微博", "X"} {
		id := RuleSetID(platform)
		if id != RuleSetID(platform) {
			t.Fatalf("平台 %q 的 rule_set_id 不确定", platform)
		}
		if !pattern.MatchString(id) {
			t.Fatalf("rule_set_id %q 不满足规则包 Schema 的 pattern", id)
		}
	}
	if RuleSetID("alpha") == RuleSetID("beta") {
		t.Fatal("不同平台必须得到不同 rule_set_id")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "artefacts")
	index, packages := sampleBundle()
	if err := Save(index, packages, dir); err != nil {
		t.Fatal(err)
	}
	loaded, loadedDir, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loadedDir != dir {
		t.Fatalf("目录 = %q want %q", loadedDir, dir)
	}
	entry, ok := loaded.Find(Code("some-platform"))
	if !ok {
		t.Fatalf("找不到平台条目；可用: %v", loaded.Codes())
	}
	if entry.SourceRoot != index.Entries[0].SourceRoot {
		t.Fatal("只读根未被完整保留")
	}
	body, err := entry.LoadPackage(loadedDir)
	if err != nil {
		t.Fatal(err)
	}
	if body["rule_set_id"] != "x" {
		t.Fatalf("规则包内容错误: %+v", body)
	}
}

// TestSaveRefusesGitWorktree 是防止真实路径被提交的结构性保护：索引逐条记录真实平台根，
// 一旦落在仓库内就随时可能被 `git add -A` 带走。
func TestSaveRefusesGitWorktree(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	index, packages := sampleBundle()
	if err := Save(index, packages, filepath.Join(repo, "nested", "out")); err == nil {
		t.Fatal("写入 Git 工作树必须被拒绝")
	}
	if err := EnsureOutsideRepository(filepath.Join(repo, "deep", "nested")); err == nil {
		t.Fatal("嵌套目录同样必须被拒绝")
	}
}

func TestLoadRejectsMissingAndMalformedIndex(t *testing.T) {
	if _, _, err := Load(""); err == nil {
		t.Fatal("空路径必须被拒绝，不得猜测")
	}
	dir := t.TempDir()
	if _, _, err := Load(dir); err == nil {
		t.Fatal("缺失索引必须被拒绝")
	}
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(`{"schemaVersion":99,"entries":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(path); err == nil {
		t.Fatal("不支持的 schemaVersion 必须被拒绝")
	}
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"entries":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(path); err == nil {
		t.Fatal("没有平台条目的索引必须被拒绝")
	}
}

// TestLoadPackageRejectsPathEscape 保证索引里的文件名不能把读取带出产物目录。
func TestLoadPackageRejectsPathEscape(t *testing.T) {
	entry := Entry{PlatformCode: "p-1", PackageFile: filepath.Join("..", "escape.json")}
	if _, err := entry.LoadPackage(t.TempDir()); err == nil {
		t.Fatal("带分隔符的规则包文件名必须被拒绝")
	}
}
