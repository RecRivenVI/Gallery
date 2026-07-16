package query

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/RecRivenVI/gallery/internal/contract/fault"
	contractquery "github.com/RecRivenVI/gallery/internal/contract/query"
	"github.com/RecRivenVI/gallery/internal/domain"
	"github.com/RecRivenVI/gallery/internal/ports"
	"github.com/RecRivenVI/gallery/internal/querytext"
)

const CursorLeaseDuration = 5 * time.Minute

type Request struct {
	Search             string
	Tag                string
	LibraryID          string
	SourceID           string
	SortDirection      string
	Limit              int
	Cursor             string
	QueryPublicationID string
	AuthorizationScope string
}

type Result struct {
	QueryPublicationID        string `json:"queryPublicationId"`
	CatalogRevision           string `json:"catalogRevision"`
	OverlayProjectionRevision string `json:"overlayProjectionRevision"`
	SortProtocolVersion       int    `json:"sortProtocolVersion"`
	Items                     []Work `json:"items"`
	NextCursor                string `json:"nextCursor,omitempty"`
}

type Work struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Creator    string   `json:"creator,omitempty"`
	Tags       []string `json:"tags"`
	MediaCount int      `json:"mediaCount"`
	SortKey    string   `json:"-"`
}

type publication struct{ ID, CatalogRevision, OverlayRevision string }

type Service struct {
	control *sql.DB
	catalog *sql.DB
	clock   ports.Clock
	random  io.Reader
	signer  *contractquery.CursorSigner
}

func NewService(ctx context.Context, control, catalog *sql.DB, clock ports.Clock, random io.Reader) (*Service, error) {
	if control == nil || catalog == nil || clock == nil {
		return nil, fmt.Errorf("Query Service 缺少依赖")
	}
	if random == nil {
		random = rand.Reader
	}
	key, err := loadOrCreateSigningKey(ctx, control, clock, random)
	if err != nil {
		return nil, err
	}
	signer, err := contractquery.NewCursorSigner(key, clock)
	if err != nil {
		return nil, err
	}
	return &Service{control: control, catalog: catalog, clock: clock, random: random, signer: signer}, nil
}

func (s *Service) Search(ctx context.Context, request Request) (Result, error) {
	if request.Limit == 0 {
		request.Limit = 50
	}
	if request.Limit < 1 || request.Limit > 200 {
		return Result{}, fault.WithField(fault.CodeValidation, "limit", nil)
	}
	if request.SortDirection == "" {
		request.SortDirection = "asc"
	}
	if request.SortDirection != "asc" && request.SortDirection != "desc" {
		return Result{}, fault.WithField(fault.CodeValidation, "sortDirection", nil)
	}
	plan := querytext.PlanSearch(request.Search)
	if plan.TooShort {
		return Result{}, fault.WithField(fault.CodeQueryTooShort, "q", nil)
	}
	queryFingerprint := fingerprint(map[string]any{"q": plan.NormalizedQuery, "tag": request.Tag, "libraryId": request.LibraryID, "sourceId": request.SourceID, "sort": "title", "direction": request.SortDirection, "limit": request.Limit})
	authHash := fingerprint(strings.Split(request.AuthorizationScope, "\x00"))
	var claims contractquery.CursorClaims
	var pub publication
	var leaseID string
	var err error
	if request.Cursor != "" {
		claims, err = s.signer.Verify(request.Cursor)
		if err != nil {
			return Result{}, err
		}
		if request.QueryPublicationID != "" && request.QueryPublicationID != claims.QueryPublicationID {
			return Result{}, fault.New(fault.CodeCursorExpired, true, nil)
		}
		if claims.QueryFingerprint != queryFingerprint || claims.AuthorizationScopeHash != authHash {
			return Result{}, fault.New(fault.CodeCursorExpired, true, nil)
		}
		pub, err = s.publication(ctx, claims.QueryPublicationID)
		if err != nil {
			return Result{}, asExpired(err)
		}
		if err := s.verifyLease(ctx, claims.LeaseID, pub.ID, authHash); err != nil {
			return Result{}, err
		}
		leaseID = claims.LeaseID
	} else {
		if request.QueryPublicationID != "" {
			pub, err = s.publication(ctx, request.QueryPublicationID)
		} else {
			pub, err = s.currentPublication(ctx)
		}
		if err != nil {
			return Result{}, err
		}
		leaseID, err = s.createLease(ctx, pub.ID, authHash)
		if err != nil {
			return Result{}, err
		}
	}
	items, more, err := s.query(ctx, pub, request, plan, claims)
	if err != nil {
		return Result{}, err
	}
	result := Result{QueryPublicationID: pub.ID, CatalogRevision: pub.CatalogRevision, OverlayProjectionRevision: pub.OverlayRevision, SortProtocolVersion: contractquery.SortProtocolVersion, Items: items}
	if more && len(items) > 0 {
		last := items[len(items)-1]
		now := s.clock.Now().UTC()
		result.NextCursor, err = s.signer.Issue(contractquery.CursorClaims{
			QueryFingerprint: queryFingerprint, SortProtocolVersion: contractquery.SortProtocolVersion,
			QueryPublicationID: pub.ID, AuthorizationScopeHash: authHash, LastSortKey: last.SortKey,
			LastCanonicalWorkID: last.ID, IssuedAt: now, LeaseID: leaseID, ExpiresAt: now.Add(CursorLeaseDuration),
		})
		if err != nil {
			return Result{}, err
		}
	}
	return result, nil
}

func (s *Service) query(ctx context.Context, pub publication, request Request, plan querytext.SearchPlan, claims contractquery.CursorClaims) ([]Work, bool, error) {
	args := []any{pub.CatalogRevision, pub.OverlayRevision}
	where := []string{"w.catalog_revision_id = ?", "w.overlay_revision_id = ?", "w.hidden = 0"}
	join := ""
	if request.LibraryID != "" {
		where = append(where, "w.library_id = ?")
		args = append(args, request.LibraryID)
	}
	if request.SourceID != "" {
		where = append(where, "w.source_id = ?")
		args = append(args, request.SourceID)
	}
	if request.Tag != "" {
		where = append(where, "EXISTS (SELECT 1 FROM json_each(w.tags_json) WHERE value = ?)")
		args = append(args, request.Tag)
	}
	if plan.NormalizedQuery != "" {
		where = append(where, "instr(w.normalized_original_text, ?) > 0")
		args = append(args, plan.NormalizedQuery)
		if plan.FTSQuery != "" {
			join = " JOIN work_search ON work_search.catalog_revision_id=w.catalog_revision_id AND work_search.overlay_revision_id=w.overlay_revision_id AND work_search.work_id=w.work_id"
			where = append(where, "work_search MATCH ?")
			args = append(args, plan.FTSQuery)
		}
	}
	operator, direction := ">", "ASC"
	if request.SortDirection == "desc" {
		operator, direction = "<", "DESC"
	}
	if claims.LastSortKey != "" {
		where = append(where, fmt.Sprintf("(w.sort_title_key %s ? OR (w.sort_title_key = ? AND w.work_id %s ?))", operator, operator))
		args = append(args, claims.LastSortKey, claims.LastSortKey, claims.LastCanonicalWorkID)
	}
	args = append(args, request.Limit+1)
	statement := `SELECT w.work_id, w.title, w.creator, w.tags_json, w.sort_title_key,
(SELECT count(*) FROM media_projections m WHERE m.catalog_revision_id=w.catalog_revision_id AND m.overlay_revision_id=w.overlay_revision_id AND m.work_id=w.work_id AND m.hidden=0)
FROM work_projections w` + join + ` WHERE ` + strings.Join(where, " AND ") +
		fmt.Sprintf(" ORDER BY w.sort_title_key %s, w.work_id %s LIMIT ?", direction, direction)
	rows, err := s.catalog.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, false, fault.New(fault.CodeInternal, true, err)
	}
	defer rows.Close()
	items := make([]Work, 0, request.Limit+1)
	for rows.Next() {
		var work Work
		var tags string
		if err := rows.Scan(&work.ID, &work.Title, &work.Creator, &tags, &work.SortKey, &work.MediaCount); err != nil {
			return nil, false, fault.New(fault.CodeInternal, true, err)
		}
		_ = json.Unmarshal([]byte(tags), &work.Tags)
		if work.Tags == nil {
			work.Tags = []string{}
		}
		items = append(items, work)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fault.New(fault.CodeInternal, true, err)
	}
	more := len(items) > request.Limit
	if more {
		items = items[:request.Limit]
	}
	return items, more, nil
}

func (s *Service) currentPublication(ctx context.Context) (publication, error) {
	var id string
	err := s.catalog.QueryRowContext(ctx, "SELECT query_publication_id FROM active_query_publication WHERE singleton=1").Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return publication{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return publication{}, fault.New(fault.CodeInternal, true, err)
	}
	return s.publication(ctx, id)
}

func (s *Service) publication(ctx context.Context, id string) (publication, error) {
	if _, err := domain.ParseID(domain.IDQueryPublication, id); err != nil {
		return publication{}, fault.New(fault.CodeNotFound, false, nil)
	}
	var result publication
	err := s.catalog.QueryRowContext(ctx, "SELECT query_publication_id, catalog_revision_id, overlay_revision_id FROM query_publications WHERE query_publication_id=?", id).Scan(&result.ID, &result.CatalogRevision, &result.OverlayRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return publication{}, fault.New(fault.CodeNotFound, false, nil)
	}
	if err != nil {
		return publication{}, fault.New(fault.CodeInternal, true, err)
	}
	return result, nil
}

func (s *Service) createLease(ctx context.Context, publicationID, authHash string) (string, error) {
	buffer := make([]byte, 16)
	if _, err := io.ReadFull(s.random, buffer); err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	id := "lease_" + hex.EncodeToString(buffer)
	now := s.clock.Now().UTC()
	_, err := s.catalog.ExecContext(ctx, "INSERT INTO query_publication_leases (lease_id, query_publication_id, authorization_scope_hash, expires_at, created_at) VALUES (?, ?, ?, ?, ?)", id, publicationID, authHash, now.Add(CursorLeaseDuration).Unix(), now.Unix())
	if err != nil {
		return "", fault.New(fault.CodeInternal, true, err)
	}
	return id, nil
}

func (s *Service) verifyLease(ctx context.Context, leaseID, publicationID, authHash string) error {
	var expires int64
	err := s.catalog.QueryRowContext(ctx, "SELECT expires_at FROM query_publication_leases WHERE lease_id=? AND query_publication_id=? AND authorization_scope_hash=?", leaseID, publicationID, authHash).Scan(&expires)
	if err != nil || s.clock.Now().Unix() >= expires {
		return fault.New(fault.CodeCursorExpired, true, nil)
	}
	return nil
}

func loadOrCreateSigningKey(ctx context.Context, db *sql.DB, clock ports.Clock, random io.Reader) ([]byte, error) {
	var key []byte
	err := db.QueryRowContext(ctx, "SELECT key_bytes FROM query_signing_keys WHERE key_version=1").Scan(&key)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	key = make([]byte, 32)
	if _, err := io.ReadFull(random, key); err != nil {
		return nil, err
	}
	_, err = db.ExecContext(ctx, "INSERT OR IGNORE INTO query_signing_keys (key_version, key_bytes, created_at) VALUES (1, ?, ?)", key, clock.Now().Unix())
	if err != nil {
		return nil, err
	}
	err = db.QueryRowContext(ctx, "SELECT key_bytes FROM query_signing_keys WHERE key_version=1").Scan(&key)
	return key, err
}

func fingerprint(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func asExpired(err error) error {
	var structured *fault.Error
	if errors.As(err, &structured) && structured.Code == fault.CodeNotFound {
		return fault.New(fault.CodeCursorExpired, true, nil)
	}
	return err
}

func AuthorizationScope(principal string, capabilities []string) string {
	copyCapabilities := append([]string(nil), capabilities...)
	sort.Strings(copyCapabilities)
	return principal + "\x00" + strings.Join(copyCapabilities, "\x00")
}
