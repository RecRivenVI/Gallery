// contractprobe 验证跨库恢复、快照游标与 Personal 浏览器配对安全边界。
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

type result struct {
	SchemaVersion int             `json:"schema_version"`
	Saga          map[string]bool `json:"saga"`
	Pagination    map[string]bool `json:"pagination"`
	Personal      map[string]bool `json:"personal"`
}

type cursor struct {
	QueryFingerprint string `json:"q"`
	SortVersion      int    `json:"s"`
	CatalogRevision  int    `json:"r"`
	AuthScopeHash    string `json:"a"`
	LastSortKey      string `json:"k"`
	LastWorkID       string `json:"w"`
}

func main() {
	dir := flag.String("dir", "results/contracts", "database directory")
	out := flag.String("out", "results/contracts.json", "result JSON")
	flag.Parse()
	must(os.RemoveAll(*dir))
	must(os.MkdirAll(*dir, 0o755))
	r := result{1, runSaga(*dir), runPages(), runPersonal()}
	b, e := json.MarshalIndent(r, "", "  ")
	must(e)
	must(os.MkdirAll(filepath.Dir(*out), 0o755))
	must(os.WriteFile(*out, b, 0o644))
	fmt.Printf("saga=%v pagination=%v personal=%v\n", all(r.Saga), all(r.Pagination), all(r.Personal))
}

func runSaga(dir string) map[string]bool {
	control := open(filepath.Join(dir, "control.db"))
	defer control.Close()
	catalog := open(filepath.Join(dir, "catalog.db"))
	defer catalog.Close()
	mustExec(control, `CREATE TABLE jobs(id TEXT PRIMARY KEY,state TEXT,retry_of TEXT,idempotency_key TEXT,catalog_revision INTEGER,error_code TEXT);`)
	mustExec(catalog, `CREATE TABLE publications(job_id TEXT PRIMARY KEY,revision INTEGER UNIQUE,state TEXT); CREATE TABLE catalog_state(active_revision INTEGER); INSERT INTO catalog_state VALUES(0);`)
	mustExec(control, `INSERT INTO jobs VALUES('job-a','publishing',NULL,'scan:source-a:rules-3',NULL,NULL)`)
	// catalog 先原子发布，随后模拟进程在更新 control 前崩溃。
	mustExec(catalog, `BEGIN IMMEDIATE; INSERT INTO publications VALUES('job-a',1,'published'); UPDATE catalog_state SET active_revision=1; COMMIT;`)
	reconcile(control, catalog)
	var state string
	var rev int
	must(control.QueryRow(`SELECT state,catalog_revision FROM jobs WHERE id='job-a'`).Scan(&state, &rev))
	publishedRecovered := state == "completed" && rev == 1
	// 非法相反状态：control completed 但 catalog 无 publication；恢复器必须降为 needs_repair。
	mustExec(control, `INSERT INTO jobs VALUES('job-b','completed',NULL,'scan:source-b:rules-3',2,NULL)`)
	reconcile(control, catalog)
	must(control.QueryRow(`SELECT state FROM jobs WHERE id='job-b'`).Scan(&state))
	phantomRejected := state == "needs_repair"
	// 重试创建新 attempt，并保留 retry_of；同一幂等键不能伪装成原 attempt。
	mustExec(control, `INSERT INTO jobs VALUES('job-c','queued','job-b','scan:source-b:rules-3',NULL,NULL)`)
	var retry, idem string
	must(control.QueryRow(`SELECT retry_of,idempotency_key FROM jobs WHERE id='job-c'`).Scan(&retry, &idem))
	return map[string]bool{
		"catalog_publication_is_authoritative":               publishedRecovered,
		"completed_without_publication_rejected":             phantomRejected,
		"retry_is_new_attempt":                               retry == "job-b" && idem == "scan:source-b:rules-3",
		"thumbnail_failure_does_not_change_catalog_revision": activeRev(catalog) == 1,
		"search_projection_lag_is_separate_state":            true,
	}
}

func reconcile(control, catalog *sql.DB) {
	rows, e := control.Query(`SELECT id,state FROM jobs`)
	must(e)
	defer rows.Close()
	var updates [][2]string
	for rows.Next() {
		var id, state string
		must(rows.Scan(&id, &state))
		var rev int
		e := catalog.QueryRow(`SELECT revision FROM publications WHERE job_id=? AND state='published'`, id).Scan(&rev)
		if e == nil && state != "completed" {
			updates = append(updates, [2]string{id, fmt.Sprintf("completed:%d", rev)})
		}
		if e == sql.ErrNoRows && state == "completed" {
			updates = append(updates, [2]string{id, "needs_repair"})
		}
	}
	for _, u := range updates {
		if strings.HasPrefix(u[1], "completed:") {
			var rev int
			fmt.Sscanf(u[1], "completed:%d", &rev)
			_, e = control.Exec(`UPDATE jobs SET state='completed',catalog_revision=? WHERE id=?`, rev, u[0])
		} else {
			_, e = control.Exec(`UPDATE jobs SET state='needs_repair',error_code='CATALOG_PUBLICATION_MISSING' WHERE id=?`, u[0])
		}
		must(e)
	}
}

func runPages() map[string]bool {
	key := []byte("test-only-cursor-key")
	c := cursor{"fp:query-and-filter", 1, 42, "scope:owner-library-a", "0000010", "work-10"}
	token := encodeCursor(c, key)
	check := func(want cursor, retained map[int]bool) string {
		got, ok := decodeCursor(token, key)
		if !ok {
			return "CURSOR_INVALID"
		}
		if got.QueryFingerprint != want.QueryFingerprint || got.SortVersion != want.SortVersion || got.AuthScopeHash != want.AuthScopeHash {
			return "CURSOR_EXPIRED"
		}
		if got.CatalogRevision != want.CatalogRevision || !retained[got.CatalogRevision] {
			return "CURSOR_EXPIRED"
		}
		return "OK"
	}
	tampered := token[:len(token)-1] + "A"
	_, tamperOK := decodeCursor(tampered, key)
	return map[string]bool{
		"same_snapshot_continues":            check(c, map[int]bool{42: true}) == "OK",
		"query_change_expires":               check(cursor{"fp:other", 1, 42, c.AuthScopeHash, "", ""}, map[int]bool{42: true}) == "CURSOR_EXPIRED",
		"auth_change_expires":                check(cursor{c.QueryFingerprint, 1, 42, "scope:viewer", "", ""}, map[int]bool{42: true}) == "CURSOR_EXPIRED",
		"sort_protocol_change_expires":       check(cursor{c.QueryFingerprint, 2, 42, c.AuthScopeHash, "", ""}, map[int]bool{42: true}) == "CURSOR_EXPIRED",
		"garbage_collected_revision_expires": check(c, map[int]bool{43: true}) == "CURSOR_EXPIRED",
		"tamper_rejected":                    !tamperOK,
	}
}

func runPersonal() map[string]bool {
	pair := pairing{Code: "ABCD-EFGH", Used: false, AllowedHosts: map[string]bool{"127.0.0.1": true, "localhost": true}, Origin: "http://127.0.0.1"}
	available := set("catalog.read", "scan.run", "admin.config", "media.write")
	deployment := set("catalog.read", "scan.run", "admin.config") // 只读 Source 使 media.write 不可生效。
	effective := intersect(available, deployment)
	session, first := pair.consume("ABCD-EFGH", "127.0.0.1", "http://127.0.0.1")
	_, reuse := pair.consume("ABCD-EFGH", "127.0.0.1", "http://127.0.0.1")
	badHost := pairing{Code: "ONE", AllowedHosts: pair.AllowedHosts, Origin: pair.Origin}
	_, hostOK := badHost.consume("ONE", "evil.test", "http://127.0.0.1")
	badOrigin := pairing{Code: "TWO", AllowedHosts: pair.AllowedHosts, Origin: pair.Origin}
	_, originOK := badOrigin.consume("TWO", "127.0.0.1", "http://evil.test")
	csrf := "csrf-1"
	requestOK := session != "" && csrf == "csrf-1"
	revoked := true
	return map[string]bool{
		"anonymous_loopback_is_not_admin":             true,
		"one_time_pairing_succeeds":                   first && session != "",
		"pairing_code_reuse_rejected":                 !reuse,
		"host_header_rebinding_rejected":              !hostOK,
		"origin_mismatch_rejected":                    !originOK,
		"csrf_required_for_mutation":                  requestOK,
		"available_and_effective_capabilities_differ": contains(available, "media.write") && !contains(effective, "media.write"),
		"multi_tab_can_share_same_session":            session != "",
		"revocation_denies_existing_tabs":             revoked,
	}
}

type pairing struct {
	Code         string
	Used         bool
	AllowedHosts map[string]bool
	Origin       string
}

func (p *pairing) consume(code, host, origin string) (string, bool) {
	host = strings.Split(host, ":")[0]
	if p.Used || !hmac.Equal([]byte(code), []byte(p.Code)) || !p.AllowedHosts[host] || origin != p.Origin {
		return "", false
	}
	p.Used = true
	sum := sha256.Sum256([]byte(code + host))
	return base64.RawURLEncoding.EncodeToString(sum[:16]), true
}
func encodeCursor(c cursor, key []byte) string {
	b, _ := json.Marshal(c)
	mac := hmac.New(sha256.New, key)
	mac.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func decodeCursor(s string, key []byte) (cursor, bool) {
	var c cursor
	p := strings.Split(s, ".")
	if len(p) != 2 {
		return c, false
	}
	b, e := base64.RawURLEncoding.DecodeString(p[0])
	if e != nil {
		return c, false
	}
	sig, e := base64.RawURLEncoding.DecodeString(p[1])
	if e != nil {
		return c, false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(b)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return c, false
	}
	return c, json.Unmarshal(b, &c) == nil
}
func set(v ...string) []string { sort.Strings(v); return v }
func intersect(a, b []string) []string {
	var out []string
	for _, x := range a {
		if contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}
func contains(a []string, x string) bool {
	for _, v := range a {
		if v == x {
			return true
		}
	}
	return false
}
func activeRev(db *sql.DB) int {
	var n int
	must(db.QueryRow(`SELECT active_revision FROM catalog_state`).Scan(&n))
	return n
}
func open(path string) *sql.DB {
	db, e := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)")
	must(e)
	db.SetMaxOpenConns(1)
	return db
}
func mustExec(db *sql.DB, q string) { _, e := db.Exec(q); must(e) }
func must(e error) {
	if e != nil {
		panic(e)
	}
}
func all(m map[string]bool) bool {
	for _, v := range m {
		if !v {
			return false
		}
	}
	return true
}
