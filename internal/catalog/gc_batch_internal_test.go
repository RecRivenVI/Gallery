package catalog

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/storage"
)

// TestGarbageCollectCommitsBetweenProjectionBatches 锁定 500k 维护矩阵暴露的核心
// 退化边界：GC 清大 revision 时必须在有界批次之间释放 SQLite 写者，让仍活动的
// publication 可以签发下一页 lease。若重新退回单一大事务，这个确定性写入会超时。
func TestGarbageCollectCommitsBetweenProjectionBatches(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	dbs, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer dbs.Close()
	fixed := clock.Fixed{Time: time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)}
	store, err := NewStore(dbs.Catalog.SQL(), fixed, gcNoIDs{})
	if err != nil {
		t.Fatal(err)
	}
	store.SetGarbageCollectBatchPolicy(1)

	for _, row := range []struct {
		catalog, overlay, publication, job string
	}{
		{"cat-stale", "overlay-stale", "publication-stale", "job-stale"},
		{"cat-active", "overlay-active", "publication-active", "job-active"},
	} {
		if _, err := dbs.Catalog.SQL().ExecContext(ctx, `INSERT INTO catalog_revisions
(catalog_revision_id, job_id, source_id, status, created_at, published_at)
VALUES (?, ?, 'source', 'published', 1, 1)`, row.catalog, row.job); err != nil {
			t.Fatal(err)
		}
		if _, err := dbs.Catalog.SQL().ExecContext(ctx, `INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES (?, ?, 1, 'published', 1, 1)`, row.overlay, row.catalog); err != nil {
			t.Fatal(err)
		}
		if _, err := dbs.Catalog.SQL().ExecContext(ctx, `INSERT INTO query_publications
(query_publication_id, catalog_revision_id, overlay_revision_id, job_id, control_watermark, created_at)
VALUES (?, ?, ?, ?, 1, 1)`, row.publication, row.catalog, row.overlay, row.job); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := dbs.Catalog.SQL().ExecContext(ctx,
		"INSERT INTO active_query_publication(singleton, query_publication_id) VALUES (1, 'publication-active')"); err != nil {
		t.Fatal(err)
	}
	for _, sourceKey := range []string{"work-1", "work-2"} {
		if _, err := dbs.Catalog.SQL().ExecContext(ctx, `INSERT INTO source_works
(catalog_revision_id, source_id, source_key, title) VALUES ('cat-stale', 'source', ?, ?)`, sourceKey, sourceKey); err != nil {
			t.Fatal(err)
		}
	}

	reachedBatchGap := make(chan struct{})
	releaseGC := make(chan struct{})
	var once sync.Once
	store.maintenanceObserver = func(table string, count int64, _ time.Duration) {
		if table == "source_works" && count == 1 {
			once.Do(func() {
				close(reachedBatchGap)
				<-releaseGC
			})
		}
	}
	done := make(chan struct {
		result GCResult
		err    error
	}, 1)
	go func() {
		result, err := store.GarbageCollect(ctx, 0)
		done <- struct {
			result GCResult
			err    error
		}{result: result, err: err}
	}()

	select {
	case <-reachedBatchGap:
	case <-time.After(5 * time.Second):
		close(releaseGC)
		t.Fatal("GC 未到达已提交的 source_works 批次间隙")
	}
	var duringAutomerge, duringCrisismerge int
	if err := dbs.Catalog.SQL().QueryRowContext(ctx,
		"SELECT v FROM work_search_config WHERE k='automerge'").Scan(&duringAutomerge); err != nil {
		t.Fatal(err)
	}
	if err := dbs.Catalog.SQL().QueryRowContext(ctx,
		"SELECT v FROM work_search_config WHERE k='crisismerge'").Scan(&duringCrisismerge); err != nil {
		t.Fatal(err)
	}
	if duringAutomerge != 0 || duringCrisismerge != 65_536 {
		t.Fatalf("GC 窗口 FTS policy automerge=%d crisismerge=%d", duringAutomerge, duringCrisismerge)
	}
	writeCtx, cancel := context.WithTimeout(ctx, time.Second)
	_, writeErr := dbs.Catalog.SQL().ExecContext(writeCtx, `INSERT INTO query_publication_leases
(lease_id, query_publication_id, authorization_scope_hash, expires_at, created_at)
VALUES ('lease-active', 'publication-active', 'scope', ?, ?)`, fixed.Now().Add(time.Minute).Unix(), fixed.Now().Unix())
	cancel()
	close(releaseGC)
	if writeErr != nil {
		t.Fatalf("GC 批次间活动 publication lease 写入失败: %v", writeErr)
	}

	select {
	case outcome := <-done:
		if outcome.err != nil || outcome.result.Publications != 1 || outcome.result.OverlayRevisions != 1 || outcome.result.CatalogRevisions != 1 {
			t.Fatalf("GC 结果错误: %+v err=%v", outcome.result, outcome.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("释放批次间隙后 GC 未收敛")
	}
	var activePublications, activeLeases, staleWorks, afterAutomerge, afterCrisismerge int
	if err := dbs.Catalog.SQL().QueryRowContext(ctx,
		"SELECT count(*) FROM query_publications WHERE query_publication_id='publication-active'").Scan(&activePublications); err != nil {
		t.Fatal(err)
	}
	if err := dbs.Catalog.SQL().QueryRowContext(ctx,
		"SELECT count(*) FROM query_publication_leases WHERE lease_id='lease-active'").Scan(&activeLeases); err != nil {
		t.Fatal(err)
	}
	if err := dbs.Catalog.SQL().QueryRowContext(ctx,
		"SELECT count(*) FROM source_works WHERE catalog_revision_id='cat-stale'").Scan(&staleWorks); err != nil {
		t.Fatal(err)
	}
	if err := dbs.Catalog.SQL().QueryRowContext(ctx,
		"SELECT v FROM work_search_config WHERE k='automerge'").Scan(&afterAutomerge); err != nil {
		t.Fatal(err)
	}
	if err := dbs.Catalog.SQL().QueryRowContext(ctx,
		"SELECT v FROM work_search_config WHERE k='crisismerge'").Scan(&afterCrisismerge); err != nil {
		t.Fatal(err)
	}
	if activePublications != 1 || activeLeases != 1 || staleWorks != 0 {
		t.Fatalf("GC 后状态错误: activePublications=%d activeLeases=%d staleWorks=%d", activePublications, activeLeases, staleWorks)
	}
	if afterAutomerge != 4 || afterCrisismerge != 16 {
		t.Fatalf("GC 后 FTS policy automerge=%d crisismerge=%d", afterAutomerge, afterCrisismerge)
	}
}

func TestNewStoreRestoresAutomergeAfterInterruptedGC(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	dbs, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer dbs.Close()
	if _, err := dbs.Catalog.SQL().ExecContext(ctx,
		"INSERT INTO work_search(work_search, rank) VALUES('automerge', 0)"); err != nil {
		t.Fatal(err)
	}
	if _, err := dbs.Catalog.SQL().ExecContext(ctx,
		"INSERT INTO work_search(work_search, rank) VALUES('crisismerge', 65536)"); err != nil {
		t.Fatal(err)
	}
	fixed := clock.Fixed{Time: time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)}
	if _, err := NewStore(dbs.Catalog.SQL(), fixed, gcNoIDs{}); err != nil {
		t.Fatal(err)
	}
	var automerge, crisismerge int
	if err := dbs.Catalog.SQL().QueryRowContext(ctx,
		"SELECT v FROM work_search_config WHERE k='automerge'").Scan(&automerge); err != nil {
		t.Fatal(err)
	}
	if err := dbs.Catalog.SQL().QueryRowContext(ctx,
		"SELECT v FROM work_search_config WHERE k='crisismerge'").Scan(&crisismerge); err != nil {
		t.Fatal(err)
	}
	if automerge != 4 || crisismerge != 16 {
		t.Fatalf("启动恢复后的 FTS policy automerge=%d crisismerge=%d", automerge, crisismerge)
	}
}

// TestGarbageCollectResumesPublishedOverlayWithoutPublication 锁定分批 GC 的恢复点：
// publication 已提交删除、Overlay root 尚未删除时发生取消或强杀，下一轮必须回收
// 这个 published 但不可达的 Overlay，同时保留同 Catalog 的活动 publication。
func TestGarbageCollectResumesPublishedOverlayWithoutPublication(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	dbs, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer dbs.Close()
	fixed := clock.Fixed{Time: time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)}
	store, err := NewStore(dbs.Catalog.SQL(), fixed, gcNoIDs{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dbs.Catalog.SQL().ExecContext(ctx, `INSERT INTO catalog_revisions
(catalog_revision_id, job_id, source_id, status, created_at, published_at)
VALUES ('cat-shared', 'job-shared', 'source', 'published', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	for _, overlay := range []string{"overlay-orphan", "overlay-active"} {
		if _, err := dbs.Catalog.SQL().ExecContext(ctx, `INSERT INTO overlay_projection_revisions
(overlay_revision_id, catalog_revision_id, control_watermark, status, created_at, published_at)
VALUES (?, 'cat-shared', 1, 'published', 1, 1)`, overlay); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := dbs.Catalog.SQL().ExecContext(ctx, `INSERT INTO query_publications
(query_publication_id, catalog_revision_id, overlay_revision_id, job_id, control_watermark, created_at)
VALUES ('publication-active', 'cat-shared', 'overlay-active', 'job-active', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := dbs.Catalog.SQL().ExecContext(ctx,
		"INSERT INTO active_query_publication(singleton, query_publication_id) VALUES (1, 'publication-active')"); err != nil {
		t.Fatal(err)
	}

	result, err := store.GarbageCollect(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Publications != 0 || result.OverlayRevisions != 1 || result.CatalogRevisions != 0 {
		t.Fatalf("恢复 GC 结果错误: %+v", result)
	}
	var orphan, active int
	if err := dbs.Catalog.SQL().QueryRowContext(ctx,
		"SELECT count(*) FROM overlay_projection_revisions WHERE overlay_revision_id='overlay-orphan'").Scan(&orphan); err != nil {
		t.Fatal(err)
	}
	if err := dbs.Catalog.SQL().QueryRowContext(ctx,
		"SELECT count(*) FROM query_publications WHERE query_publication_id='publication-active'").Scan(&active); err != nil {
		t.Fatal(err)
	}
	if orphan != 0 || active != 1 {
		t.Fatalf("恢复 GC 后 orphan=%d activePublication=%d", orphan, active)
	}
}

type gcNoIDs struct{}

func (gcNoIDs) New(domain.IDKind) (domain.ID, error) {
	return domain.ID{}, errors.New("GC 不应生成 ID")
}
