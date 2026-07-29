package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestCreatorListModeRoutingKeepsGovernanceCursorOnGovernancePath(t *testing.T) {
	tests := []struct {
		query      string
		wantBrowse bool
	}{
		{"", false},
		{"?limit=50", false},
		{"?cursor=opaque", false},
		{"?limit=50&cursor=opaque", false},
		{"?includeMerged=false", true},
		{"?sort=name_asc", true},
		{"?sourceId=src_018f47d2-5c16-7a44-a8a0-900000000000", true},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/api/v1/creators"+tt.query, nil)
			if got := creatorBrowseRequested(request); got != tt.wantBrowse {
				t.Fatalf("creatorBrowseRequested(%q)=%v want=%v", tt.query, got, tt.wantBrowse)
			}
		})
	}
}
