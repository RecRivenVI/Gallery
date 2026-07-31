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

func TestStructuredPerformanceShapesUseProductionPlans(t *testing.T) {
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
		name   string
		filter string
	}{
		{name: "structured-and", filter: `{"all":[{"field":"provider.id","op":"eq","value":"provider-0"},{"field":"media.kind","op":"eq","value":"image"}]}`},
		{name: "structured-or", filter: `{"any":[{"field":"provider.id","op":"eq","value":"provider-0"},{"field":"provider.id","op":"eq","value":"provider-1"}]}`},
		{name: "overlay-favorite", filter: `{"field":"overlay.favorite","op":"eq","value":true}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := galleryquery.Request{Filter: test.filter, Limit: 200}
			pageSQL, pageArgs, err := galleryquery.BuildPageStatementForTest(ctx, "cat", "ovr",
				[]string{"src"}, []string{"src"}, request, galleryquery.EmptyCursorClaimsForTest())
			if err != nil {
				t.Fatal(err)
			}
			totalSQL, totalArgs, err := galleryquery.BuildTotalStatementForTest(ctx, "cat", "ovr",
				[]string{"src"}, []string{"src"}, request)
			if err != nil {
				t.Fatal(err)
			}
			pagePlan := explainPlan(t, store.Catalog.SQL(), pageSQL, pageArgs...)
			totalPlan := explainPlan(t, store.Catalog.SQL(), totalSQL, totalArgs...)
			for _, plan := range []string{pagePlan, totalPlan} {
				assertPlanContains(t, plan, "SEARCH w USING INDEX work_projections_query_idx")
				assertPlanExcludes(t, plan, "SCAN w")
				assertPlanExcludes(t, plan, "USE TEMP B-TREE")
			}

			switch test.name {
			case "structured-and":
				for _, plan := range []string{pagePlan, totalPlan} {
					assertPlanContains(t, plan, "SEARCH m EXISTS USING INDEX media_projections_work_idx")
					assertPlanContains(t, plan, "SEARCH sw EXISTS USING INDEX sqlite_autoindex_source_works_1")
				}
				assertCorrelatedCount(t, pagePlan, 1)
				assertCorrelatedCount(t, totalPlan, 0)
			case "structured-or":
				for _, plan := range []string{pagePlan, totalPlan} {
					if got := strings.Count(plan, "sqlite_autoindex_source_works_1"); got != 2 {
						t.Fatalf("query plan source identity index probes=%d want=2:\n%s", got, plan)
					}
				}
				assertCorrelatedCount(t, pagePlan, 3)
				assertCorrelatedCount(t, totalPlan, 2)
			case "overlay-favorite":
				for _, plan := range []string{pagePlan, totalPlan} {
					assertPlanExcludes(t, plan, "source_works")
				}
				assertCorrelatedCount(t, pagePlan, 1)
				assertCorrelatedCount(t, totalPlan, 0)
			}
		})
	}
}
