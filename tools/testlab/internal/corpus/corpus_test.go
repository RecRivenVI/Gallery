package corpus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestComputeStatsMatchesManualCounting(t *testing.T) {
	const n = 12345
	stats := ComputeStats(n)
	if stats.N != n {
		t.Fatalf("N = %d, want %d", stats.N, n)
	}

	var wantHidden, wantFavorite int
	var wantVisibleN, wantVideo, wantVerified, wantCJK, wantLatin, wantFilename int
	wantCreator := map[string]int{}
	wantProvider := map[string]int{}
	wantTag := map[string]int{}
	for i := 0; i < n; i++ {
		if Hidden(i) {
			wantHidden++
		}
		if Favorite(i) {
			wantFavorite++
		}
		if Hidden(i) {
			continue
		}
		wantVisibleN++
		if i%10 == 0 {
			wantVideo++
		}
		if i%3 != 0 {
			wantVerified++
		}
		if i%1000 == specialCJKOffset {
			wantCJK++
		}
		if i%1000 == specialLatinOffset {
			wantLatin++
		}
		if i%500 == uniqueFilenameOffset {
			wantFilename++
		}
		wantCreator[CreatorName(CreatorIndex(i))]++
		wantProvider[ProviderID(ProviderIndex(i))]++
		a, b := TagSlots(i)
		wantTag[TagName(a)]++
		if b != a {
			wantTag[TagName(b)]++
		}
	}

	if stats.HiddenCount != wantHidden {
		t.Errorf("HiddenCount = %d, want %d", stats.HiddenCount, wantHidden)
	}
	if stats.FavoriteCount != wantFavorite {
		t.Errorf("FavoriteCount = %d, want %d", stats.FavoriteCount, wantFavorite)
	}
	if stats.VisibleN != wantVisibleN {
		t.Errorf("VisibleN = %d, want %d", stats.VisibleN, wantVisibleN)
	}
	if stats.VisibleN != n-wantHidden {
		t.Errorf("VisibleN = %d, want N-HiddenCount = %d", stats.VisibleN, n-wantHidden)
	}
	if stats.VisibleVideoCount != wantVideo || stats.VisibleImageCount != wantVisibleN-wantVideo {
		t.Errorf("VisibleVideoCount/VisibleImageCount = %d/%d, want %d/%d", stats.VisibleVideoCount, stats.VisibleImageCount, wantVideo, wantVisibleN-wantVideo)
	}
	if stats.VisibleContentVerifiedCount != wantVerified || stats.VisibleLocatedUnverifiedCount != wantVisibleN-wantVerified {
		t.Errorf("VisibleContentVerifiedCount/VisibleLocatedUnverifiedCount = %d/%d, want %d/%d", stats.VisibleContentVerifiedCount, stats.VisibleLocatedUnverifiedCount, wantVerified, wantVisibleN-wantVerified)
	}
	if stats.VisibleSpecialCJKCount != wantCJK {
		t.Errorf("VisibleSpecialCJKCount = %d, want %d", stats.VisibleSpecialCJKCount, wantCJK)
	}
	if stats.VisibleSpecialLatinCount != wantLatin {
		t.Errorf("VisibleSpecialLatinCount = %d, want %d", stats.VisibleSpecialLatinCount, wantLatin)
	}
	if stats.VisibleUniqueFilenameCount != wantFilename {
		t.Errorf("VisibleUniqueFilenameCount = %d, want %d", stats.VisibleUniqueFilenameCount, wantFilename)
	}
	if !reflect.DeepEqual(stats.VisibleCreatorCounts, wantCreator) {
		t.Errorf("VisibleCreatorCounts mismatch")
	}
	if !reflect.DeepEqual(stats.VisibleProviderCounts, wantProvider) {
		t.Errorf("VisibleProviderCounts mismatch")
	}
	if !reflect.DeepEqual(stats.VisibleTagCounts, wantTag) {
		t.Errorf("VisibleTagCounts mismatch")
	}
}

func TestComputeStatsIsDeterministicAcrossRuns(t *testing.T) {
	a := ComputeStats(5000)
	b := ComputeStats(5000)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("ComputeStats(5000) produced different results on repeated calls")
	}
}

// TestSpecialMarkersNeverCoincideWithHidden 是此前真实出现过的一个测试夹具缺陷的
// 回归测试：SpecialCJKMarker/UniqueFilenameMarker 曾经使用余数偏移 0，而 1000 和
// 500 都是 Hidden 周期（50）的倍数，导致带标记的作品恒为 Hidden、恒被默认查询的
// 隐式 hidden=0 过滤器排除，使搜索命中数系统性地被清零。偏移量必须与 50 互不整除。
func TestSpecialMarkersNeverCoincideWithHidden(t *testing.T) {
	for i := 0; i < 100000; i++ {
		isCJK := i%1000 == specialCJKOffset
		isLatin := i%1000 == specialLatinOffset
		isFilename := i%500 == uniqueFilenameOffset
		if !isCJK && !isLatin && !isFilename {
			continue
		}
		if Hidden(i) {
			t.Fatalf("marker at i=%d (cjk=%v latin=%v filename=%v) unexpectedly coincides with Hidden(i)", i, isCJK, isLatin, isFilename)
		}
	}
}

func TestTitleFilenameMarkersAreExclusiveAndDeterministic(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 3000; i++ {
		title := Title(i)
		if seen[title] {
			t.Fatalf("duplicate title at i=%d: %q", i, title)
		}
		seen[title] = true
		if title != Title(i) {
			t.Fatalf("Title(%d) not deterministic", i)
		}
	}
}

func TestManifestRoundTrip(t *testing.T) {
	original := Manifest{
		SchemaVersion: 2, Scale: 42, LibraryID: "lib_test", SourceID: "src_test", JobID: "job_test",
		CreatorIDs: []string{"ctr_a", "ctr_b"}, QueryPublicationID: "qpub_test", CatalogRevisionID: "crev_test",
		StageDurationMs: 1, OverlayDurationMs: 2, PublishDurationMs: 3, TotalDurationMs: 6,
		Stats: ComputeStats(42),
	}
	path := filepath.Join(t.TempDir(), "manifest.json")
	encoded, err := json.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, loaded) {
		t.Fatalf("round-tripped manifest differs:\noriginal=%+v\nloaded=%+v", original, loaded)
	}
}

func TestLoadManifestMissingFile(t *testing.T) {
	if _, err := LoadManifest(filepath.Join(t.TempDir(), "does-not-exist.json")); err == nil {
		t.Fatal("expected error for missing manifest file")
	}
}
