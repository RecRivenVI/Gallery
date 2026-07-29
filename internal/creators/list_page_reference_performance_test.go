package creators_test

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/creators"
	"github.com/RecRivenVI/gallery/internal/querytext"
)

// TestCreatorListPageReferencePerformance 是显式 opt-in 的工作站证据，不是固定墙钟门禁。
// 它构造大量无合并 Creator，验证常见路径由排序索引驱动并只为当前页探测 Binding；正式
// Reference Gate 仍以实施计划中的多样本/并发/Degradation 矩阵为准。
func TestCreatorListPageReferencePerformance(t *testing.T) {
	if os.Getenv("GALLERY_RUN_CREATOR_LIST_PERF") != "1" {
		t.Skip("设置 GALLERY_RUN_CREATOR_LIST_PERF=1 才运行 Creator 列表性能证据")
	}
	count := 100_000
	if raw := os.Getenv("GALLERY_CREATOR_LIST_PERF_COUNT"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			t.Fatalf("GALLERY_CREATOR_LIST_PERF_COUNT 无效: %q", raw)
		}
		count = parsed
	}
	f := setupTwoSources(t)
	tx, err := f.store.Control.SQL().BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	creatorInsert, err := tx.PrepareContext(f.ctx, `INSERT INTO canonical_creators
(creator_id, name, sort_name_key, created_at) VALUES (?, ?, ?, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	bindingInsert, err := tx.PrepareContext(f.ctx, `INSERT INTO creator_bindings
(binding_id, source_id, provider_id, external_id, source_key, creator_id, identity_version,
 status, last_seen_generation, created_at, updated_at)
VALUES (?, ?, '', '', ?, ?, 1, 'active', 1, 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	buildStarted := time.Now()
	for index := 0; index < count; index++ {
		creatorID := fmt.Sprintf("ctr_018f47d2-5c16-7a44-a8a0-%012x", index)
		bindingID := fmt.Sprintf("crb_018f47d2-5c16-7a44-a8a0-%012x", index)
		name := fmt.Sprintf("Creator %09d", index)
		if _, err := creatorInsert.ExecContext(f.ctx, creatorID, name, querytext.NaturalSortKey(name)); err != nil {
			t.Fatal(err)
		}
		if _, err := bindingInsert.ExecContext(f.ctx, bindingID, f.source1.ID, "creator/"+strconv.Itoa(index), creatorID); err != nil {
			t.Fatal(err)
		}
	}
	if err := creatorInsert.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bindingInsert.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	request := creators.ListPageRequest{
		AllowedSourceIDs: []string{f.source1.ID}, IncludeMerged: false, Sort: "name_asc", Limit: 48,
	}
	if _, err := f.creators.ListPage(f.ctx, request); err != nil {
		t.Fatal(err)
	}
	durations := make([]time.Duration, 20)
	for index := range durations {
		started := time.Now()
		page, err := f.creators.ListPage(f.ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 48 || page.NextCursor == "" {
			t.Fatalf("性能 fixture 分页结果错误: items=%d cursor=%t", len(page.Items), page.NextCursor != "")
		}
		durations[index] = time.Since(started)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	t.Logf("CreatorList count=%d build=%s p50=%s p95=%s max=%s",
		count, time.Since(buildStarted), durations[len(durations)/2], durations[18], durations[19])

	if count > 1 {
		rootID := "ctr_018f47d2-5c16-7a44-a8a0-000000000000"
		absorbedID := fmt.Sprintf("ctr_018f47d2-5c16-7a44-a8a0-%012x", count-1)
		if _, err := f.store.Control.SQL().ExecContext(f.ctx,
			"UPDATE canonical_creators SET merged_into=? WHERE creator_id=?", rootID, absorbedID); err != nil {
			t.Fatal(err)
		}
		mergedDurations := make([]time.Duration, 10)
		for index := range mergedDurations {
			started := time.Now()
			page, err := f.creators.ListPage(f.ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != 48 || page.NextCursor == "" {
				t.Fatalf("合并图性能 fixture 分页结果错误: items=%d cursor=%t", len(page.Items), page.NextCursor != "")
			}
			mergedDurations[index] = time.Since(started)
		}
		sort.Slice(mergedDurations, func(i, j int) bool { return mergedDurations[i] < mergedDurations[j] })
		t.Logf("CreatorList merged_graph count=%d p50=%s p90=%s max=%s",
			count, mergedDurations[5], mergedDurations[8], mergedDurations[9])
	}
}
