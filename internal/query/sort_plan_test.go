package query_test

import (
	"context"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/platform/appdirs"
	"github.com/RecRivenVI/gallery/internal/platform/filesystem"
	galleryquery "github.com/RecRivenVI/gallery/internal/query"
	"github.com/RecRivenVI/gallery/internal/storage"
)

func TestBrowseSortsSelectMatchingScopeIndexes(t *testing.T) {
	ctx := context.Background()
	dirs := appdirs.UnderRoot(t.TempDir())
	if err := dirs.Ensure(filesystem.OS{}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, dirs)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for _, test := range []struct {
		name, sort, sourceID, libraryID, index string
	}{
		{name: "global-title", sort: "title_asc", index: "work_projections_query_idx"},
		{name: "global-date", sort: "date_desc", index: "work_projections_published_idx"},
		{name: "global-progress", sort: "progress_desc", index: "work_projections_progress_idx"},
		{name: "library-title", sort: "title_asc", libraryID: "lib", index: "work_projections_library_query_idx"},
		{name: "library-date", sort: "date_desc", libraryID: "lib", index: "work_projections_library_published_idx"},
		{name: "library-progress", sort: "progress_desc", libraryID: "lib", index: "work_projections_library_progress_idx"},
		{name: "source-title", sort: "title_asc", sourceID: "src", index: "work_projections_source_query_idx"},
		{name: "source-date", sort: "date_desc", sourceID: "src", index: "work_projections_source_published_idx"},
		{name: "source-progress", sort: "progress_desc", sourceID: "src", index: "work_projections_source_progress_idx"},
	} {
		t.Run(test.name, func(t *testing.T) {
			statement, args, err := galleryquery.BuildPageStatementForTest(ctx, "cat", "ovr",
				[]string{"src"}, []string{"src"}, galleryquery.Request{
					Sort: test.sort, SourceID: test.sourceID, LibraryID: test.libraryID, Limit: 20,
				}, galleryquery.EmptyCursorClaimsForTest())
			if err != nil {
				t.Fatal(err)
			}
			rows, err := store.Catalog.SQL().QueryContext(ctx, "EXPLAIN QUERY PLAN "+statement, args...)
			if err != nil {
				t.Fatal(err)
			}
			var details []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				details = append(details, detail)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			plan := strings.Join(details, "\n")
			if !strings.Contains(plan, test.index) {
				t.Fatalf("%s 未使用匹配索引 %s:\n%s\nSQL:\n%s", test.name, test.index, plan, statement)
			}
		})
	}
}
