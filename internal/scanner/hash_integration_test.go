package scanner_test

import (
	"context"
	"testing"

	"github.com/RecRivenVI/gallery/internal/hashjob"
	"github.com/RecRivenVI/gallery/internal/jobs"
	"github.com/RecRivenVI/gallery/internal/scanner"
)

func TestScanDelegatesFullHashToPersistentHashJob(t *testing.T) {
	fixture := []byte("scanner persistent hash delegation")
	resources, jobStore, _, service, source, store := setup(t, fixture)
	defer store.Close()
	hashService, err := hashjob.New(context.Background(), resources, jobStore)
	if err != nil {
		t.Fatal(err)
	}
	service.SetHashService(hashService)
	// 首次扫描无既往 publication 时默认自动选 index（不建立 Hash Job）；本测试要验证
	// Hash Job 委托链路，因此显式请求 incremental。
	job, err := service.CreateScanWithProfile(context.Background(), source.ID, "personal-owner", "", scanner.ScanProfileIncremental)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Execute(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	var hashJobID string
	if err := store.Control.SQL().QueryRowContext(context.Background(), "SELECT job_id FROM jobs WHERE job_type='hash' AND request_json LIKE ?", "%work-one/media.bin%").Scan(&hashJobID); err != nil {
		t.Fatal(err)
	}
	hashJob, err := jobStore.Get(context.Background(), hashJobID)
	if err != nil || hashJob.Status != jobs.StatusCompleted || hashJob.ProgressBytes != int64(len(fixture)) {
		t.Fatalf("扫描未产生已完成持久 Hash Job: %+v %v", hashJob, err)
	}
	attempts, err := jobStore.ListAttempts(context.Background(), hashJobID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != "completed" {
		t.Fatalf("Hash Job attempt 未完成: %+v %v", attempts, err)
	}
}
