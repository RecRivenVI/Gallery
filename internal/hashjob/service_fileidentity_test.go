//go:build windows || aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package hashjob_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/RecRivenVI/gallery/internal/hashjob"
	"github.com/RecRivenVI/gallery/internal/media"
	"github.com/RecRivenVI/gallery/internal/platform/fileidentity"
	"github.com/RecRivenVI/gallery/internal/ports"
)

func TestHashJobPersistsPlatformIdentityObservation(t *testing.T) {
	ctx, service, jobStore, sourceID, filePath, cleanup := newHashFixture(t, []byte("persist hash identity"))
	defer cleanup()
	provider := fileidentity.OS{}
	service.SetFileIdentityProvider(provider)
	root := filepath.Dir(filepath.Dir(filePath))
	located, err := media.LocateSourceFileWithIdentity(root, "work/media.bin", provider)
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.Create(ctx, hashjob.Request{
		SourceID: sourceID, RelativePath: "work/media.bin",
		ExpectedSize: located.Size, ExpectedModTimeNanos: located.ModTimeNanos, HasExpectedIdentity: true,
		ExpectedPlatformIdentityKind:  located.PlatformIdentityKind,
		ExpectedPlatformIdentityValue: located.PlatformIdentityValue,
	}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	service.Start(job.ID)
	result, err := service.WaitResult(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.PlatformIdentityKind != ports.FileIdentityKindV1 ||
		result.PlatformIdentityValue != located.PlatformIdentityValue ||
		!result.HasObservedModTime || result.ModTimeNanos != located.ModTimeNanos {
		t.Fatalf("WaitResult 丢失最终身份观察: locate=%+v result=%+v", located, result)
	}
	stored, err := jobStore.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var persisted hashjob.Result
	if err := json.Unmarshal(stored.ResultJSON, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.PlatformIdentityKind != result.PlatformIdentityKind ||
		persisted.PlatformIdentityValue != result.PlatformIdentityValue ||
		!persisted.HasObservedModTime || persisted.ModTimeNanos != result.ModTimeNanos {
		t.Fatalf("Hash Job ResultJSON 未持久化最终身份观察: %+v", persisted)
	}
}
