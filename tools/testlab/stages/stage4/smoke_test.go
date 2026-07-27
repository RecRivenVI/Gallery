package stage4

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/bootstrap"
	"github.com/RecRivenVI/gallery/internal/config"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/descriptor"
	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/corpus"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/environment"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/report"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/seeding"
	"github.com/RecRivenVI/gallery/tools/testlab/internal/sourceguard"
	"github.com/RecRivenVI/gallery/tools/testlab/stages/stage4/media"
	"github.com/RecRivenVI/gallery/tools/testlab/stages/stage4/query"
)

const stage4SmokeScale = 1_000

// stage4SmokeSources 让 smoke 语料分布到多个 Source。
//
// 单 Source 语料永远不会执行 catalog.cloneUnchangedSources 的 12 条全量搬运语句
// （WHERE source_id<>?），因此在此之前，"重扫其中一个 Source 时按比例复制其余全部
// Source 的投影与 FTS5 索引"这条发布路径在 testlab 的任何规模下都没有被执行过。三个
// Source 使最后一次发布搬运 2/3 的语料，同时让浏览、排序与游标分页必须真正跨 Source
// 合并（corpus.SourceIndex 按取模交错分配，各 Source 的作品在排序键上完全交叉）。
const stage4SmokeSources = 3

var stage4QueryCursorFindingNames = []string{
	"cursor/continuation-succeeds",
	"cursor/first-page-issues-cursor",
	"cursor/forged-publication-id-rejected",
	"cursor/hmac-tamper-rejected",
	"cursor/reuse-with-different-query-rejected",
	"cursor/reuse-with-different-sort-rejected",
	"filter/AND(provider,tag)",
	"filter/NOT(media.kind=video)",
	"filter/OR(two providers)",
	"filter/bad value type rejected",
	"filter/duplicate condition",
	"filter/empty-all-rejected",
	"filter/empty-any-rejected",
	"filter/library.id",
	"filter/media.contentVerificationState=content_verified",
	"filter/media.contentVerificationState=located_unverified",
	"filter/media.kind=video",
	"filter/media.locationAvailable",
	"filter/nested AND(OR,NOT)",
	"filter/overlay.favorite",
	"filter/overlay.hidden=true",
	"filter/overlay.progress gte 0.5",
	"filter/provider.id[0]",
	"filter/provider.id[1]",
	"filter/provider.id[2]",
	"filter/provider.id[3]",
	"filter/provider.id[4]",
	"filter/source.id",
	"filter/tag",
	"filter/unknown field rejected",
	"filter/unknown operator rejected",
	"ranking/match-spans-in-bounds",
	"ranking/matches-present-for-search",
	"ranking/rankProtocolVersion-present",
	"search/empty-query-is-browse",
	"search/filename-infix-recall",
	"search/latin-casefold-recall",
	"search/selective-cjk-exact-recall",
	"search/single-cjk-char-rejected",
	"search/wide-query-paginates",
	"sort/no-duplicate-across-pages",
	"sort/pagination-walks-without-error",
	"total/exact-mode",
	"total/exact-mode-for-bounded-browse",
	"total/omitted-mode",
}

var stage4MediaFindingNames = []string{
	"derived/content-readable",
	"derived/create-job-accepted",
	"derived/historical-snapshot-unverified-rejected",
	"derived/job-completed-with-asset-key",
	"derived/singleflight-cache-hit",
	"derived/unknown-transform-rejected",
	"media/current-mode-content-readable",
	"media/first-index-scan-completed",
	"media/historical-publication-verification-rejected",
	"media/if-none-match-304",
	"media/if-range-hit-206",
	"media/if-range-miss-falls-back-200",
	"media/illegal-range-416",
	"media/initial-state-located-unverified",
	"media/only-target-confirmed-siblings-untouched",
	"media/range-206",
	"media/repeat-verification-conflict",
	"media/snapshot-mode-reads-old-state",
	"media/unverified-content-rejected",
	"media/verification-job-completed",
}

// TestStage4CorrectnessSmoke 把此前只能由 testlabprobe 人工触发的阶段 4 查询、
// Cursor、媒体与 DerivedAsset Correctness 断言接入普通 go test。它使用临时
// AppDirs、确定性 1k 语料、合成可写夹具和真实 production bootstrap + loopback
// HTTP；不读取真实 Source，也不把 smoke 结果冒充 500k Reference/Degradation Gate。
func TestStage4CorrectnessSmoke(t *testing.T) {
	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	seedSourceRoot := filepath.Join(root, "query-source")
	manifest, err := seeding.Run(context.Background(), seeding.Config{
		AppRoot: appRoot, SourceRoot: seedSourceRoot, Scale: stage4SmokeScale, BatchSize: 257,
		Sources: stage4SmokeSources,
	})
	if err != nil {
		t.Fatalf("构建阶段 4 smoke publication: %v", err)
	}
	if manifest.Scale != stage4SmokeScale {
		t.Fatalf("manifest.Scale = %d, want %d", manifest.Scale, stage4SmokeScale)
	}
	if manifest.SourceCount() != stage4SmokeSources {
		t.Fatalf("manifest.SourceCount() = %d, want %d", manifest.SourceCount(), stage4SmokeSources)
	}

	sess := startGalleryd(t, appRoot, seedSourceRoot)
	seedSource, err := sess.Client.GetSourceWithResponse(context.Background(), manifest.SourceID, sess.SameOrigin)
	if err != nil || seedSource.JSON200 == nil || seedSource.JSON200.LibraryId != manifest.LibraryID {
		t.Fatalf("阶段 4 smoke 的 control/Catalog Source 成员不一致: err=%v status=%d", err, environment.StatusOf(seedSource))
	}
	queryReport := &report.Report{
		SchemaVersion: 2, Scenario: "stage4-query-cursor-smoke", Tier: "smoke",
		Scale: manifest.Scale, Transport: "loopback-http",
	}
	query.RunStructuredFilterCorrectness(queryReport, sess, manifest)
	query.RunSearchRecallCorrectness(queryReport, sess, manifest.Stats)
	query.RunRankingAndMatchesCorrectness(queryReport, sess)
	query.RunTotalTriStateCorrectness(queryReport, sess, manifest.Stats)
	query.RunSortCorrectness(queryReport, sess)
	query.RunCursorCorrectness(queryReport, sess)
	assertReportPassed(t, queryReport)
	assertExactFindingNames(t, queryReport, stage4QueryCursorFindingNames)
	assertEverySourceIsQueryable(t, sess, manifest)
	assertLimitations(t, queryReport, []string{"filter/creator.id"})
	if err := queryReport.Save(filepath.Join(root, "stage4-query-cursor-smoke.json")); err != nil {
		t.Fatalf("保存脱敏 query/cursor smoke 报告: %v", err)
	}

	mediaReport := &report.Report{
		SchemaVersion: 2, Scenario: "stage4-media-smoke", Tier: "smoke",
		Transport: "loopback-http",
	}
	mediaSourceRoot := filepath.Join(root, "media-source")
	libraryID, sourceID, workCount, err := media.SetupMediaSource(mediaReport, sess, mediaSourceRoot)
	if err != nil {
		t.Fatalf("建立阶段 4 合成媒体 Source: %v", err)
	}
	sourceBefore, err := sourceguard.Walk(mediaSourceRoot)
	if err != nil {
		t.Fatalf("记录合成媒体 Source guard: %v", err)
	}
	media.RunMediaCorrectness(mediaReport, sess, libraryID, sourceID, workCount)
	assertReportPassed(t, mediaReport)
	assertExactFindingNames(t, mediaReport, stage4MediaFindingNames)
	if len(mediaReport.Limitations) != 0 {
		t.Fatalf("阶段 4 合成 media smoke 不应跳过任何场景: %v", mediaReport.Limitations)
	}
	sourceAfter, err := sourceguard.Walk(mediaSourceRoot)
	if err != nil {
		t.Fatalf("复核合成媒体 Source guard: %v", err)
	}
	if !sourceBefore.Equal(sourceAfter) {
		t.Fatalf("阶段 4 media smoke 对 Source 产生了写入: before=%s after=%s", sourceBefore.GuardSHA256, sourceAfter.GuardSHA256)
	}
	if err := mediaReport.Save(filepath.Join(root, "stage4-media-smoke.json")); err != nil {
		t.Fatalf("保存脱敏 media smoke 报告: %v", err)
	}
}

// assertEverySourceIsQueryable 逐个 Source 复核最终 publication 里它的作品都能被查到，
// 且各 Source 的可见命中数合计等于整份语料的可见总数。
//
// 这是 cloneUnchangedSources 的端到端证明：最后一次发布只 Stage 了 1/N 的语料，其余
// (N-1)/N 的 work_projections、media_projections 与 FTS5 行只可能来自那 12 条搬运语句。
// 如果哪一条被漏掉或写错，这里会直接看到某个 Source 查不到或数量对不上，而不是等到
// 500,000 规模的正式实测才暴露。
func assertEverySourceIsQueryable(t *testing.T, sess *environment.Session, manifest corpus.Manifest) {
	t.Helper()
	if len(manifest.SourceIDs) != len(manifest.SourceVisibleWorkCounts) {
		t.Fatalf("manifest 的 Source ID 数 %d 与逐 Source 计数 %d 不一致",
			len(manifest.SourceIDs), len(manifest.SourceVisibleWorkCounts))
	}
	total := 0
	for slot, sourceID := range manifest.SourceIDs {
		expected := manifest.SourceVisibleWorkCounts[slot]
		resp, err := sess.ListWorks(api.ListWorksParams{SourceId: &sourceID, Limit: ptrOf(1)})
		if err != nil || resp.JSON200 == nil {
			t.Fatalf("按 Source %d 查询失败: err=%v status=%d", slot, err, environment.StatusOf(resp))
		}
		if resp.JSON200.Total.Value == nil || int(*resp.JSON200.Total.Value) != expected {
			t.Fatalf("Source %d 的可见作品数 = %v, want %d（mode=%s）",
				slot, resp.JSON200.Total.Value, expected, resp.JSON200.Total.Mode)
		}
		total += expected
	}
	if total != manifest.Stats.VisibleN {
		t.Fatalf("各 Source 可见数合计 %d != 语料 VisibleN %d", total, manifest.Stats.VisibleN)
	}
}

func ptrOf[T any](v T) *T { return &v }

func startGalleryd(t *testing.T, appRoot, sourceRoot string) *environment.Session {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan descriptor.Descriptor, 1)
	done := make(chan error, 1)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() {
		done <- bootstrap.RunWithReady(ctx, config.Config{
			Mode: config.ModePersonal, Listen: "127.0.0.1:0",
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
				t.Errorf("关闭阶段 4 smoke galleryd: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("关闭阶段 4 smoke galleryd 超时")
		}
	})

	var runtimeDescriptor descriptor.Descriptor
	select {
	case runtimeDescriptor = <-ready:
	case err := <-done:
		stopped = true
		cancel()
		t.Fatalf("阶段 4 smoke galleryd 在 ready 前退出: %v", err)
	case <-time.After(60 * time.Second):
		cancel()
		t.Fatal("阶段 4 smoke galleryd 启动超时")
	}
	sess, err := environment.NewSession("http://" + runtimeDescriptor.Address)
	if err != nil {
		t.Fatalf("建立阶段 4 smoke Personal Session: %v", err)
	}
	return sess
}

func assertReportPassed(t *testing.T, rep *report.Report) {
	t.Helper()
	if rep.FailureCount == 0 {
		return
	}
	failures := make([]string, 0, rep.FailureCount)
	for _, finding := range rep.Findings {
		if !finding.Pass {
			failures = append(failures, fmt.Sprintf("%s: %s", finding.Name, finding.Detail))
		}
	}
	sort.Strings(failures)
	t.Fatalf("阶段 4 smoke 有 %d 项失败:\n%s", rep.FailureCount, strings.Join(failures, "\n"))
}

func assertExactFindingNames(t *testing.T, rep *report.Report, expected []string) {
	t.Helper()
	actual := make([]string, 0, len(rep.Findings))
	for _, finding := range rep.Findings {
		actual = append(actual, finding.Name)
	}
	want := append([]string(nil), expected...)
	sort.Strings(actual)
	sort.Strings(want)
	if len(actual) == len(want) {
		equal := true
		for i := range actual {
			if actual[i] != want[i] {
				equal = false
				break
			}
		}
		if equal {
			return
		}
	}
	t.Fatalf("阶段 4 smoke finding 集合漂移；actual(%d)=%s；want(%d)=%s",
		len(actual), strings.Join(actual, ", "), len(want), strings.Join(want, ", "))
}

func assertLimitations(t *testing.T, rep *report.Report, expectedSubstrings []string) {
	t.Helper()
	if len(rep.Limitations) != len(expectedSubstrings) {
		t.Fatalf("阶段 4 smoke limitations = %v, want %d 项", rep.Limitations, len(expectedSubstrings))
	}
	for _, expected := range expectedSubstrings {
		found := false
		for _, limitation := range rep.Limitations {
			if strings.Contains(limitation, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("阶段 4 smoke 未记录已知限制 %q: %v", expected, rep.Limitations)
		}
	}
}
