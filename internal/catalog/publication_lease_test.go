package catalog_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/storage"
)

// TestPublicationReadLeaseProtectsFromGarbageCollect 覆盖阶段 4 媒体快照绑定读取引入的
// PublicationReadLease：显式 queryPublicationId 读取在请求处理期间必须阻止该（此刻可能
// 已经不是 active 的）publication 被 GarbageCollect 回收，Close 释放（或过期）后 GC
// 才能正常回收。复用既有 query_publication_leases 表与既有 GC 保护判据，不引入第二套
// lease 表或保护语义。
func TestPublicationReadLeaseProtectsFromGarbageCollect(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	fixed := clock.Fixed{Time: now}
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), fixed, fixedIDs{})
	if err != nil {
		t.Fatal(err)
	}

	oldPublication, _, _ := seedGCPublication(t, store, 1, true)
	seedGCPublication(t, store, 2, false)

	lease, err := catalogStore.AcquirePublicationLease(ctx, oldPublication, "auth-scope-hash")
	if err != nil {
		t.Fatal(err)
	}

	result, err := catalogStore.GarbageCollect(ctx, 0)
	if err != nil || result.Publications != 0 {
		t.Fatalf("PublicationReadLease 未保护旧快照: %+v %v", result, err)
	}
	assertPublicationCount(t, store, oldPublication, 1)

	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	result, err = catalogStore.GarbageCollect(ctx, 0)
	if err != nil || result.Publications != 1 {
		t.Fatalf("释放 lease 后旧快照应被正常回收: %+v %v", result, err)
	}
	assertPublicationCount(t, store, oldPublication, 0)
}

// TestPublicationByIDReturnsCursorExpiredForUnknownOrGCedPublication 覆盖不存在（或已被
// GC 回收）的 queryPublicationId 必须返回稳定 CURSOR_EXPIRED，不能与"格式非法"或"内部
// 错误"混淆，也不能静默返回零值。
func TestPublicationByIDReturnsCursorExpiredForUnknownOrGCedPublication(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	fixed := clock.Fixed{Time: now}
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), fixed, fixedIDs{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = catalogStore.PublicationByID(ctx, "qpub_018f47d2-5c16-7a44-a8a0-0000000000ff")
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeCursorExpired {
		t.Fatalf("不存在的 publication 应返回结构化 CURSOR_EXPIRED: %v", err)
	}
}
