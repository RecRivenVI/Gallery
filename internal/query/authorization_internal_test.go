package query

import (
	"context"
	"strings"
	"testing"

	"github.com/RecRivenVI/gallery/internal/querytext"
)

func TestBaseFilterAuthorizationModes(t *testing.T) {
	tests := []struct {
		name        string
		authorize   sourceAuthorization
		want        string
		wantArgs    int
		forbidParts []string
	}{
		{
			name: "missing-membership-fails-closed",
			authorize: sourceAuthorization{
				CandidateSourceIDs: []string{}, AllowedSourceIDs: []string{},
			},
			want: "1=0", wantArgs: 2,
		},
		{
			name: "all-allowed",
			authorize: sourceAuthorization{
				CandidateSourceIDs: []string{"src_a", "src_b"},
				AllowedSourceIDs:   []string{"src_a", "src_b"},
			},
			wantArgs: 2, forbidParts: []string{"json_each", "1=0"},
		},
		{
			name: "all-denied",
			authorize: sourceAuthorization{
				CandidateSourceIDs: []string{"src_a", "src_b"},
				DeniedSourceIDs:    []string{"src_a", "src_b"},
			},
			want: "1=0", wantArgs: 2,
		},
		{
			name: "smaller-denied-set",
			authorize: sourceAuthorization{
				CandidateSourceIDs: []string{"src_a", "src_b", "src_c"},
				AllowedSourceIDs:   []string{"src_a", "src_b"},
				DeniedSourceIDs:    []string{"src_c"},
			},
			want: "w.source_id <> ?", wantArgs: 3,
		},
		{
			name: "smaller-allowed-set",
			authorize: sourceAuthorization{
				CandidateSourceIDs: []string{"src_a", "src_b", "src_c"},
				AllowedSourceIDs:   []string{"src_a"},
				DeniedSourceIDs:    []string{"src_b", "src_c"},
			},
			want: "w.source_id = ?", wantArgs: 3,
		},
		{
			name: "multi-denied-list",
			authorize: sourceAuthorization{
				CandidateSourceIDs: []string{"src_a", "src_b", "src_c", "src_d", "src_e"},
				AllowedSourceIDs:   []string{"src_a", "src_b", "src_c"},
				DeniedSourceIDs:    []string{"src_d", "src_e"},
			},
			want: "w.source_id NOT IN (SELECT value FROM json_each(?))", wantArgs: 3,
		},
		{
			name: "multi-allowed-list",
			authorize: sourceAuthorization{
				CandidateSourceIDs: []string{"src_a", "src_b", "src_c", "src_d", "src_e"},
				AllowedSourceIDs:   []string{"src_a", "src_b"},
				DeniedSourceIDs:    []string{"src_c", "src_d", "src_e"},
			},
			want: "w.source_id IN (SELECT value FROM json_each(?))", wantArgs: 3,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &Service{}
			where, _, args, err := service.baseFilter(context.Background(), publication{
				CatalogRevision: "cat", OverlayRevision: "ovr",
			}, test.authorize, Request{}, querytext.SearchPlan{}, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(where, " AND ")
			if test.want != "" && !strings.Contains(joined, test.want) {
				t.Fatalf("WHERE 缺少 %q: %s", test.want, joined)
			}
			for _, forbidden := range test.forbidParts {
				if strings.Contains(joined, forbidden) {
					t.Fatalf("WHERE 不应包含 %q: %s", forbidden, joined)
				}
			}
			if len(args) != test.wantArgs {
				t.Fatalf("args=%d want=%d (%v)", len(args), test.wantArgs, args)
			}
		})
	}
}

func TestBaseFilterSearchDoesNotForceBrowseIndex(t *testing.T) {
	service := &Service{}
	authorization := sourceAuthorization{
		CandidateSourceIDs: []string{"src_a"},
		AllowedSourceIDs:   []string{"src_a"},
	}
	_, fromSuffix, _, err := service.baseFilter(context.Background(), publication{
		CatalogRevision: "cat", OverlayRevision: "ovr",
	}, authorization, Request{}, querytext.PlanSearch("特别作品"), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fromSuffix, "INDEXED BY") {
		t.Fatalf("FTS 搜索不得继承无搜索 browse 的强制索引: %s", fromSuffix)
	}
	if !strings.Contains(fromSuffix, "JOIN work_search") {
		t.Fatalf("FTS 搜索缺少 work_search 驱动关系: %s", fromSuffix)
	}
}
