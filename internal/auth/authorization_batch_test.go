package auth_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/RecRivenVI/gallery/internal/auth"
)

func TestAuthorizeSessionSourcesMatchesScalarAuthorizationMatrix(t *testing.T) {
	ctx := context.Background()
	manager, store, _ := newSecurityManager(t)
	db := store.Control.SQL()

	const (
		libraryA      = "lib_00000000-0000-7000-8000-000000000001"
		libraryB      = "lib_00000000-0000-7000-8000-000000000002"
		sourceA1      = "src_00000000-0000-7000-8000-000000000001"
		sourceA2      = "src_00000000-0000-7000-8000-000000000002"
		sourceB1      = "src_00000000-0000-7000-8000-000000000003"
		missingSource = "src_00000000-0000-7000-8000-000000000004"
		missingOther  = "src_00000000-0000-7000-8000-000000000005"
		viewerID      = "usr_00000000-0000-7000-8000-000000000001"
	)

	for _, library := range []string{libraryA, libraryB} {
		if _, err := db.ExecContext(ctx, "INSERT INTO libraries (library_id, name, created_at) VALUES (?, ?, 0)", library, library); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct {
		id, library string
	}{
		{sourceA1, libraryA},
		{sourceA2, libraryA},
		{sourceB1, libraryB},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO sources
(source_id, library_id, display_name, root_path, root_key, created_at)
VALUES (?, ?, ?, ?, ?, 0)`, item.id, item.library, item.id, "root-"+item.id, "key-"+item.id); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO security_principals
(principal_id, principal_kind, display_name, status, security_version, created_at, updated_at)
VALUES (?, 'local_user', 'Batch Viewer', 'active', 1, 0, 0)`, viewerID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO principal_roles
(principal_id, role_id, assigned_by, assigned_at) VALUES (?, 'viewer', ?, 0)`, viewerID, auth.PersonalOwnerID); err != nil {
		t.Fatal(err)
	}

	grantSequence := 0
	addGrant := func(principalID, effect, capability, kind, id string, revoked bool) {
		t.Helper()
		grantSequence++
		var revokedAt any
		if revoked {
			revokedAt = int64(1)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO authorization_grants
(grant_id, principal_id, effect, capability, scope_kind, scope_id, created_by, created_at, revoked_at)
VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?)`, fmt.Sprintf("batch-grant-%02d", grantSequence), principalID,
			effect, capability, kind, id, auth.PersonalOwnerID, revokedAt); err != nil {
			t.Fatal(err)
		}
	}

	// Owner 的隐式 allow 仍会被较窄 deny 减去；同一 Source 上 allow 与 deny 并存时 deny 优先。
	addGrant(auth.PersonalOwnerID, "deny", "library.read", "library", libraryB, false)
	addGrant(auth.PersonalOwnerID, "allow", "library.read", "source", sourceA2, false)
	addGrant(auth.PersonalOwnerID, "deny", "library.read", "source", sourceA2, false)
	// global deny 不能被较窄 allow 恢复，即使 Principal 是 Owner。
	addGrant(auth.PersonalOwnerID, "deny", "library.write", "global", "", false)
	addGrant(auth.PersonalOwnerID, "allow", "library.write", "source", sourceA1, false)

	// Viewer 的 role capability 只是上限；Library allow 向 Source 继承，Source deny 再减去成员。
	addGrant(viewerID, "allow", "library.read", "library", libraryA, false)
	addGrant(viewerID, "allow", "library.read", "source", missingSource, false)
	addGrant(viewerID, "allow", "library.read", "source", sourceA2, false)
	addGrant(viewerID, "deny", "library.read", "source", sourceA2, false)
	// 已撤销的 global deny 不得参与授权。
	addGrant(viewerID, "deny", "library.read", "global", "", true)
	// active global deny 必须压过窄 allow。
	addGrant(viewerID, "deny", "media.read", "global", "", false)
	addGrant(viewerID, "allow", "media.read", "source", sourceA1, false)

	candidates := []string{sourceB1, missingOther, sourceA2, sourceA1, missingSource, sourceA1}
	owner := auth.Session{
		PrincipalID:  auth.PersonalOwnerID,
		Capabilities: append([]string(nil), auth.PersonalOwnerCapabilities...),
	}
	viewer := auth.Session{PrincipalID: viewerID, Capabilities: []string{"library.read", "media.read"}}

	tests := []struct {
		name         string
		session      auth.Session
		capabilities []string
		want         []string
	}{
		{
			name:    "owner narrow deny and missing source baseline",
			session: owner, capabilities: []string{"library.read"},
			want: []string{sourceA1, missingSource, missingOther},
		},
		{
			name:    "owner global deny beats narrow allow",
			session: owner, capabilities: []string{"library.write"},
			want: []string{},
		},
		{
			name:    "all required capabilities are intersected",
			session: owner, capabilities: []string{"library.write", "library.read", "library.read"},
			want: []string{},
		},
		{
			name:    "viewer library inheritance source deny and missing exact source allow",
			session: viewer, capabilities: []string{"library.read"},
			want: []string{sourceA1, missingSource},
		},
		{
			name:    "viewer global deny beats source allow",
			session: viewer, capabilities: []string{"media.read"},
			want: []string{},
		},
		{
			name:         "session capability is an upper bound",
			session:      auth.Session{PrincipalID: viewerID, Capabilities: []string{"media.read"}},
			capabilities: []string{"library.read"}, want: []string{},
		},
		{
			name:         "database role capability is also an upper bound",
			session:      auth.Session{PrincipalID: viewerID, Capabilities: []string{"library.write"}},
			capabilities: []string{"library.write"}, want: []string{},
		},
		{
			name: "token library scope inherits only to existing member sources",
			session: auth.Session{PrincipalID: viewerID, TokenID: "tok_batch_library", Capabilities: []string{"library.read"},
				TokenScopes: []auth.ResourceScope{{Kind: "library", ID: libraryA}}},
			capabilities: []string{"library.read"}, want: []string{sourceA1},
		},
		{
			name: "token source scope can match a missing control source exactly",
			session: auth.Session{PrincipalID: viewerID, TokenID: "tok_batch_source", Capabilities: []string{"library.read"},
				TokenScopes: []auth.ResourceScope{{Kind: "source", ID: missingSource}}},
			capabilities: []string{"library.read"}, want: []string{missingSource},
		},
		{
			name: "token global scope preserves principal result",
			session: auth.Session{PrincipalID: viewerID, TokenID: "tok_batch_global", Capabilities: []string{"library.read"},
				TokenScopes: []auth.ResourceScope{{Kind: "global"}}},
			capabilities: []string{"library.read"}, want: []string{sourceA1, missingSource},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := manager.AuthorizeSessionSources(ctx, test.session, test.capabilities, candidates)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("批量授权结果 = %v, want %v", got, test.want)
			}

			canonicalCandidates := append([]string(nil), candidates...)
			slices.Sort(canonicalCandidates)
			canonicalCandidates = slices.Compact(canonicalCandidates)
			batchSet := make(map[string]struct{}, len(got))
			for _, sourceID := range got {
				batchSet[sourceID] = struct{}{}
			}
			for _, sourceID := range canonicalCandidates {
				scalarAllowed := true
				for _, capability := range compactSorted(test.capabilities) {
					allowed, authorizeErr := manager.AuthorizeSession(ctx, test.session, capability, auth.ResourceScope{Kind: "source", ID: sourceID})
					if authorizeErr != nil {
						t.Fatal(authorizeErr)
					}
					if !allowed {
						scalarAllowed = false
						break
					}
				}
				_, batchAllowed := batchSet[sourceID]
				if batchAllowed != scalarAllowed {
					t.Fatalf("Source %s 批量/标量授权不一致: batch=%v scalar=%v", sourceID, batchAllowed, scalarAllowed)
				}
			}
		})
	}
}

func TestAuthorizeSessionSourcesEmptyInputIsFailClosed(t *testing.T) {
	manager, _, _ := newSecurityManager(t)
	session := auth.Session{PrincipalID: auth.PersonalOwnerID, Capabilities: append([]string(nil), auth.PersonalOwnerCapabilities...)}
	for _, test := range []struct {
		name         string
		capabilities []string
		sources      []string
	}{
		{name: "empty capabilities", sources: []string{"src_00000000-0000-7000-8000-000000000001"}},
		{name: "empty sources", capabilities: []string{"library.read"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := manager.AuthorizeSessionSources(context.Background(), session, test.capabilities, test.sources)
			if err != nil {
				t.Fatal(err)
			}
			if got == nil || len(got) != 0 {
				t.Fatalf("空输入必须返回非 nil 空允许集，得到 %#v", got)
			}
		})
	}
}

func compactSorted(values []string) []string {
	result := append([]string(nil), values...)
	slices.Sort(result)
	return slices.Compact(result)
}
