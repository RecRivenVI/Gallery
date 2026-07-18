package maintenance_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/RecRivenVI/gallery/internal/catalog"
	"github.com/RecRivenVI/gallery/internal/contract/fault"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/maintenance"
	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/clock"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	"github.com/RecRivenVI/gallery/internal/platform/identity"
	"github.com/RecRivenVI/gallery/internal/ports"
	"github.com/RecRivenVI/gallery/internal/storage"
)

type spaceChecker struct{ free int64 }

func (s spaceChecker) FreeBytes(string) (int64, error) { return s.free, nil }

func TestPreflightRejectsInsufficientAppDirsSpace(t *testing.T) {
	dirs := appdirs.UnderRoot(filepath.Join(t.TempDir(), "app"))
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := clock.Fixed{Time: time.Date(2026, 7, 18, 5, 0, 0, 0, time.UTC)}
	ids := identity.NewGenerator(now)
	jobStore, err := jobs.NewStore(store.Control.SQL(), now, ids)
	if err != nil {
		t.Fatal(err)
	}
	catalogStore, err := catalog.NewStore(store.Catalog.SQL(), now, ids)
	if err != nil {
		t.Fatal(err)
	}
	service, err := maintenance.New(context.Background(), store.Control.SQL(), catalogStore, jobStore, nil, dirs, spaceChecker{free: 10}, now)
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Preflight(context.Background(), 11)
	if err == nil || report.Sufficient {
		t.Fatalf("空间不足未被拒绝: %+v %v", report, err)
	}
	var structured *fault.Error
	if !errors.As(err, &structured) || structured.Code != fault.CodeDiskSpaceInsufficient {
		t.Fatalf("空间不足错误码错误: %v", err)
	}
	if report.FreeBytes != 10 || report.RequiredBytes != 11 {
		t.Fatalf("空间报告不完整: %+v", report)
	}
	estimate, estimateErr := service.Estimate(context.Background(), "catalog_vacuum")
	if estimateErr == nil || estimate.Operation != "catalog_vacuum" || !estimate.Conservative ||
		estimate.RequiredBytes <= 11 || estimate.FreeBytes != 10 {
		t.Fatalf("服务端保守估算错误: %+v %v", estimate, estimateErr)
	}
	if _, createErr := service.CreateGC(context.Background(), "owner", maintenance.Request{}); createErr == nil {
		t.Fatal("空间不足仍创建了维护 Job")
	}
	var count int
	if err := store.Control.SQL().QueryRow("SELECT COUNT(*) FROM jobs").Scan(&count); err != nil || count != 0 {
		t.Fatalf("空间预检失败污染了 Job 表: count=%d err=%v", count, err)
	}
}

var _ ports.SpaceChecker = spaceChecker{}
