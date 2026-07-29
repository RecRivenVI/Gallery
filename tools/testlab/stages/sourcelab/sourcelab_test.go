package sourcelab_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/bootstrap"
	appconfig "github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/descriptor"
	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/bounds"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/environment"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/legacyrules"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/ruleindex"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/sourceguard"
	"github.com/RecRivenVI/gallery/tools/testlab/stages/sourcelab"
)

// syntheticCreators/syntheticWorks 是刻意可识别的合成名字：它们只存在于本测试建的临时
// 目录里，用来证明「报告里不会出现路径分隔符之后的真实名字」。
var syntheticCreators = []string{"creator-Kagamine-Rin-77", "creator-Megurine-Luka-13"}

var syntheticWorks = []string{
	"2024-03-05_06-07-08_1001",
	"2024-03-06_07-08-09_1002",
	"2023-12-31_23-59-59_1003",
}

const syntheticPlatformID = "sourcelab-synthetic-platform"

// syntheticLegacyConfig 与真实 `gallery-rules.json` 同形（schema_version 3），但全部取值
// 由本测试构造，不含任何真实来源信息。%s 是合成 Source 根。
//
// 刻意声明 `metadata.author_id` 并让**同一作者拥有多个作品**：若转换器把 author_id 映射成
// 作品的 `external_id`，同一作者的多个作品就会共享同一个 external_id，扫描解析阶段命中
// `duplicate_external_id` 并以 `BINDING_REVIEW_REQUIRED` 阻塞整个 Source 的 publication。
// 这条形状让那类回归在本端到端测试里直接暴露；结构侧的对应断言见 legacyrules 的
// TestConvertNeverMapsAuthorIDToWorkExternalID。
const syntheticLegacyConfig = `{
  "schema_version": 3,
  "library": {"id": "main", "metadata_file": "metadata.json", "path_case": "preserve"},
  "time": {"storage_timezone": "UTC", "display_timezone": "Asia/Shanghai",
           "display_format": "YYYY-MM-DD HH:mm:ss", "naive_timestamp_timezone": "UTC",
           "directory_timestamp_timezone": "UTC"},
  "media": {"image_extensions": ["jpg", "png"], "video_extensions": ["mp4"],
            "hidden_name_globs": [".*"]},
  "cover": {"disable_marker": ".nocover", "explicit_globs": ["cover.*"],
            "leaf_fallback": "first_natural_media",
            "aggregate": {"author": "latest_dated_work", "platform": "latest_dated_author",
                          "library": "latest_dated_platform"}},
  "file_roots": [{"id": "files", "name": "全部", "path": %s, "enabled": true, "order": 10}],
  "sort": {"collation": "zh-CN", "work_default": "date_desc",
           "work_options": ["date_desc", "date_asc", "title_asc", "title_desc", "name_asc", "name_desc"],
           "author_default": "name_asc",
           "author_options": ["name_asc", "name_desc", "latest_desc", "latest_asc", "posts_desc", "posts_asc"],
           "browse_default": "natural_asc", "browse_options": ["natural_asc", "natural_desc"]},
  "platforms": [
    {"id": "` + syntheticPlatformID + `", "enabled": true, "path": %s, "order": 1, "scan_order": 1,
     "ui": {"name": "合成平台", "description": "仅用于自动化测试", "show_in_sidebar": true,
            "show_in_manager": true, "author_label": "作者",
            "icon": {"kind": "glyph", "glyph": "S", "background": "#1688f0", "color": "#ffffff", "border": "transparent"}},
     "structure": {"mode": "author_work", "author_pattern": "{author}", "work_pattern": "{author}/{work}",
                   "work_detection": "leaf_with_visible_media"},
     "metadata": {"categories": ["synthetic"], "title": ["title", "$path.work"],
                  "author": ["user.name", "$path.author"], "author_id": ["user.id"],
                  "description": ["caption"], "tags": ["tags"],
                  "date": ["date", "$path.datetime"], "source_url": ["postUrl", "url"],
                  "time": {"input_timezone": "UTC", "output_timezone": "UTC", "display_timezone": "inherit"}}}
  ],
  "badges": [
    {"id": "image", "enabled": true, "order": 1, "position": "cover_top_right", "label": "图片",
     "color": "#f1f1f1", "background": "#121316", "when": {"suffix": ["jpg", "JPG"]}}
  ]
}`

// TestSourcelabDrivesConvertedRulesOverSyntheticSource 是本工作线的端到端断言：
//
//   - 验证入口消费 internal/rules/legacy.Convert 的转换产物（而不是手写规则夹具）；
//   - 每个真实 Source 操作前后都做 guard 快照与校验，全程零写入；
//   - 有界模式、全量 index 模式、incremental 续跑与 verify 对照组各自成立；
//   - 报告只含代号与计数，不含任何目录名。
//
// 它使用临时 AppDirs、合成 Source 与真实 production bootstrap + loopback HTTP；不读取
// 任何真实来源，也不代表任何真实规模结论。
func TestSourcelabDrivesConvertedRulesOverSyntheticSource(t *testing.T) {
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "synthetic-source")
	buildSyntheticSource(t, sourceRoot)

	rulesDir := filepath.Join(root, "rules")
	entry := importSyntheticRules(t, sourceRoot, rulesDir)
	if entry.SourceRoot != sourceRoot {
		t.Fatalf("转换产物中的只读根与合成 Source 根不一致")
	}
	rulePackage, err := entry.LoadPackage(rulesDir)
	if err != nil {
		t.Fatalf("读取转换产出的规则包: %v", err)
	}

	base := sourcelab.Config{
		Entry: entry, Package: rulePackage, MaxMediaItems: 4, StorageClass: "ssd",
		GuardOptions: sourceguard.Options{HashContent: true, MaxHashFiles: 8},
	}

	// 1) 有界模式用独立 AppDirs：`index` 档案只对首次扫描有效（Source 已发布后服务端
	// 稳定拒绝为 CONFLICT），因此两条链路必须各自从一个干净的 Source 开始，否则第二条
	// 链路只会走「复用既有索引」分支，真正的 index 路径就没被覆盖。
	boundedSession := startGalleryd(t, filepath.Join(root, "app-bounded"), sourceRoot)
	boundedConfig := base
	boundedConfig.Mode = sourcelab.ModeBounded
	boundedConfig.Limits = bounds.Limits{MaxDirs: 64, MaxFiles: 256, MaxWallClock: 5 * time.Minute}
	boundedReport := runMode(t, boundedSession, boundedConfig, filepath.Join(root, "state", "bounded.json"), "sourcelab-bounded")
	requireFinding(t, boundedReport, "sourcelab/bounded-index-publishes-unverified")
	requireFinding(t, boundedReport, "sourcelab/bounded-on-demand-verification")

	sess := startGalleryd(t, filepath.Join(root, "app-main"), sourceRoot)
	statePath := filepath.Join(root, "state", "run.json")

	// 2) 全量 index 模式：完整枚举 + metadata 解析 + publication + 全量内容哈希。
	indexConfig := base
	indexConfig.Mode = sourcelab.ModeIndex
	indexConfig.HashScope = sourcelab.HashFull
	indexReport := runMode(t, sess, indexConfig, statePath, "sourcelab-index")
	requireFinding(t, indexReport, "sourcelab/index-scan")
	requireFinding(t, indexReport, "sourcelab/index-publishes-unverified")
	requireFinding(t, indexReport, "sourcelab/index-metadata-parsed")
	requireFinding(t, indexReport, "sourcelab/full-content-hash")

	// 3) incremental：证明续跑不重做已完成工作。
	incrementalConfig := base
	incrementalConfig.Mode = sourcelab.ModeIncremental
	incrementalReport := runMode(t, sess, incrementalConfig, statePath, "sourcelab-incremental")
	requireFinding(t, incrementalReport, "sourcelab/incremental-does-not-rehash")
	requireFinding(t, incrementalReport, "sourcelab/incremental-preserves-identity")
	requireFinding(t, incrementalReport, "sourcelab/resumes-previous-run")

	// 4) verify：对照组，必须真正推进确认时间且身份不漂移。
	verifyConfig := base
	verifyConfig.Mode = sourcelab.ModeVerify
	verifyReport := runMode(t, sess, verifyConfig, statePath, "sourcelab-verify")
	requireFinding(t, verifyReport, "sourcelab/verify-advances-confirmation-time")
	requireFinding(t, verifyReport, "sourcelab/verify-preserves-identity")

	// 5) 再跑一次 index：必须走「复用既有索引」分支而不是失败，这是续跑的基本形态。
	resumeReport := runMode(t, sess, indexConfig, statePath, "sourcelab-index-resume")
	requireFinding(t, resumeReport, "sourcelab/index-reused-existing-index")

	// 全程零写入：合成 Source 在全部运行之后必须与最初完全一致。
	final, err := sourceguard.Walk(sourceRoot)
	if err != nil {
		t.Fatalf("收尾 guard 遍历: %v", err)
	}
	if final.IsEmpty() {
		t.Fatal("收尾 guard 清单为空")
	}
}

// TestSourcelabReportNeverContainsSourceNames 是脱敏要求的独立回归：把四次运行的报告
// 全部落盘，逐字节确认不出现任何合成目录名、文件名或路径分隔形态。
//
// 「用合成数据」正是关键：这些名字确实存在于被扫描的树里，如果实现把它们带进 detail，
// 本测试必然失败。
func TestSourcelabReportNeverContainsSourceNames(t *testing.T) {
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "synthetic-source")
	buildSyntheticSource(t, sourceRoot)
	rulesDir := filepath.Join(root, "rules")
	entry := importSyntheticRules(t, sourceRoot, rulesDir)
	rulePackage, err := entry.LoadPackage(rulesDir)
	if err != nil {
		t.Fatal(err)
	}

	sess := startGalleryd(t, filepath.Join(root, "app"), sourceRoot)
	cfg := sourcelab.Config{
		Entry: entry, Package: rulePackage, Mode: sourcelab.ModeIndex,
		HashScope: sourcelab.HashBounded, MaxMediaItems: 3, StorageClass: "hdd",
		Limits: bounds.Limits{},
	}
	rep := &report.Report{SchemaVersion: 2, Scenario: "sourcelab-redaction", Transport: "loopback-http"}
	if _, err := sourcelab.Run(rep, sess, cfg, nil); err != nil {
		t.Fatalf("运行 sourcelab: %v", err)
	}
	out := filepath.Join(root, "report.json")
	if err := rep.Save(out); err != nil {
		t.Fatalf("保存报告: %v", err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)

	forbidden := append([]string{}, syntheticCreators...)
	forbidden = append(forbidden, syntheticWorks...)
	forbidden = append(forbidden, "synthetic-source", "1.jpg", "cover.jpg", "metadata.json", syntheticPlatformID)
	for _, name := range forbidden {
		if strings.Contains(text, name) {
			t.Fatalf("报告包含来自 Source 的名字 %q", name)
		}
	}
	for _, separator := range []string{`\\`, `:\`, "://"} {
		if strings.Contains(text, separator) {
			t.Fatalf("报告包含路径/URL 形态文本 %q", separator)
		}
	}
	// 平台标识必须是代号形态，而不是平台 ID 本身。
	if !strings.Contains(text, ruleindex.Code(syntheticPlatformID)) {
		t.Fatal("报告缺少平台代号，无法跨运行对照")
	}
}

// TestSourcelabRejectsUnboundedBoundedMode 锁定「有界模式必须真的有界」。
func TestSourcelabRejectsUnboundedBoundedMode(t *testing.T) {
	cfg := sourcelab.Config{
		Entry:   ruleindex.Entry{PlatformCode: "p-00000000", SourceRoot: "x"},
		Package: map[string]any{"rule_set_id": "x"},
		Mode:    sourcelab.ModeBounded,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("全不设限的 bounded 模式必须被拒绝")
	}
}

func runMode(t *testing.T, sess *environment.Session, cfg sourcelab.Config, statePath, scenario string) *report.Report {
	t.Helper()
	previous, err := sourcelab.LoadState(statePath)
	if err != nil {
		t.Fatalf("%s 读取续跑状态: %v", scenario, err)
	}
	rep := &report.Report{SchemaVersion: 2, Scenario: scenario, Transport: "loopback-http"}
	state, err := sourcelab.Run(rep, sess, cfg, previous)
	if err != nil {
		t.Fatalf("%s 运行失败: %v（findings=%+v）", scenario, err, rep.Findings)
	}
	requireSourcelabBindingPaused(t, sess, state)
	if err := sourcelab.SaveState(state, statePath); err != nil {
		t.Fatalf("%s 保存续跑状态: %v", scenario, err)
	}
	for _, finding := range rep.Findings {
		if !finding.Pass {
			t.Fatalf("%s 断言失败: %s (%s)", scenario, finding.Name, finding.Detail)
		}
	}
	if rep.FailureCount != 0 {
		t.Fatalf("%s failureCount = %d", scenario, rep.FailureCount)
	}
	// 每一步都必须留下 guard 结论，且必须全部通过。
	guardChecks := 0
	for _, finding := range rep.Findings {
		if strings.Contains(finding.Name, "guard") {
			guardChecks++
		}
	}
	if guardChecks < 2 {
		t.Fatalf("%s 只留下 %d 条 guard 结论，真实 Source 操作没有被 guard 包住", scenario, guardChecks)
	}
	return rep
}

func requireSourcelabBindingPaused(t *testing.T, sess *environment.Session, state *sourcelab.State) {
	t.Helper()
	if state == nil || state.SourceID == "" || state.BindingID == "" {
		t.Fatalf("sourcelab 状态缺少 Source/Binding 身份: %+v", state)
	}
	sourceID := api.SourceId(state.SourceID)
	response, err := sess.Client.ListSourceRuleBindingsWithResponse(context.Background(),
		&api.ListSourceRuleBindingsParams{SourceId: &sourceID}, sess.SameOrigin)
	if err != nil || response.JSON200 == nil {
		t.Fatalf("读取 sourcelab Binding 状态失败: status=%d err=%v", environment.StatusOf(response), err)
	}
	for _, binding := range response.JSON200.Bindings {
		if string(binding.Id) != state.BindingID {
			continue
		}
		if binding.Status == nil || *binding.Status != api.SourceRuleBindingStatusPaused {
			t.Fatalf("sourcelab Binding 在显式 Job 创建后必须暂停，实际=%v", binding.Status)
		}
		return
	}
	t.Fatalf("未找到 sourcelab Binding %s", state.BindingID)
}

func requireFinding(t *testing.T, rep *report.Report, name string) {
	t.Helper()
	for _, finding := range rep.Findings {
		if finding.Name == name {
			if !finding.Pass {
				t.Fatalf("finding %s 未通过: %s", name, finding.Detail)
			}
			return
		}
	}
	t.Fatalf("报告缺少 finding %s；实际: %s", name, findingNames(rep))
}

func findingNames(rep *report.Report) string {
	names := make([]string, 0, len(rep.Findings))
	for _, finding := range rep.Findings {
		names = append(names, finding.Name)
	}
	return strings.Join(names, ",")
}

// buildSyntheticSource 造一棵 author_work 形状的合成只读树。
func buildSyntheticSource(t *testing.T, root string) {
	t.Helper()
	for creatorIndex, creator := range syntheticCreators {
		for workIndex, work := range syntheticWorks {
			dir := filepath.Join(root, creator, work)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			write := func(name string, body []byte) {
				if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			write("1.jpg", []byte(fmt.Sprintf("synthetic-image-%d-%d-aaaaaaaaaaaaaaaa", creatorIndex, workIndex)))
			write("2.png", []byte(fmt.Sprintf("synthetic-image-%d-%d-bbbbbbbbbbbbbbbb", creatorIndex, workIndex)))
			write("cover.jpg", []byte(fmt.Sprintf("synthetic-cover-%d-%d", creatorIndex, workIndex)))
			metadata := map[string]any{
				"title":   fmt.Sprintf("合成作品 %d-%d", creatorIndex, workIndex),
				"caption": "合成描述",
				"tags":    []string{"合成", "测试"},
				"date":    "2024-03-05T06:07:08Z",
				"postUrl": "https://example.invalid/synthetic",
				"user":    map[string]any{"name": fmt.Sprintf("作者%d", creatorIndex), "id": fmt.Sprintf("u%03d", creatorIndex)},
			}
			encoded, err := json.Marshal(metadata)
			if err != nil {
				t.Fatal(err)
			}
			write("metadata.json", encoded)
		}
	}
}

// importSyntheticRules 走完整的转换链路：合成旧配置 → legacy.Convert → 落盘索引 →
// 由 ruleindex 回读。测试刻意不手写规则包：手写会绕过被验证的那条链路。
func importSyntheticRules(t *testing.T, sourceRoot, outDir string) ruleindex.Entry {
	t.Helper()
	quoted, err := json.Marshal(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(syntheticLegacyConfig, string(quoted), string(quoted))
	bundle, err := legacyrules.Convert([]byte(body))
	if err != nil {
		t.Fatalf("转换合成旧配置: %v", err)
	}
	if len(bundle.Index.Entries) != 1 {
		t.Fatalf("转换产出 %d 个平台，want 1", len(bundle.Index.Entries))
	}
	if err := ruleindex.Save(bundle.Index, bundle.Packages, outDir); err != nil {
		t.Fatalf("保存转换产物: %v", err)
	}
	index, dir, err := ruleindex.Load(outDir)
	if err != nil {
		t.Fatalf("回读转换产物索引: %v", err)
	}
	if dir != outDir {
		t.Fatalf("索引目录 = %q want %q", dir, outDir)
	}
	entry, ok := index.Find(ruleindex.Code(syntheticPlatformID))
	if !ok {
		t.Fatalf("索引缺少合成平台代号；可用: %v", index.Codes())
	}
	return entry
}

func startGalleryd(t *testing.T, appRoot, sourceRoot string) *environment.Session {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan descriptor.Descriptor, 1)
	done := make(chan error, 1)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() {
		done <- bootstrap.RunWithReady(ctx, appconfig.Config{
			Mode: appconfig.ModePersonal, Listen: "127.0.0.1:0",
			AppDirs: appdirs.UnderRoot(appRoot), SourceRoots: []string{sourceRoot},
		}, logger, ready)
	}()

	stopped := false
	t.Cleanup(func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("关闭 sourcelab 测试 galleryd: %v", err)
			}
		case <-time.After(20 * time.Second):
			t.Error("关闭 sourcelab 测试 galleryd 超时")
		}
	})

	var runtimeDescriptor descriptor.Descriptor
	select {
	case runtimeDescriptor = <-ready:
	case err := <-done:
		stopped = true
		t.Fatalf("galleryd 在就绪前退出: %v", err)
	case <-time.After(60 * time.Second):
		t.Fatal("等待 galleryd 就绪超时")
	}

	sess, err := environment.NewSession("http://" + runtimeDescriptor.Address)
	if err != nil {
		t.Fatalf("建立 Session: %v", err)
	}
	return sess
}
