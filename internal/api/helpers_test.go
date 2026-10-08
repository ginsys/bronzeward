package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/fixtures/oidc/issuer"
	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/migrate"
)

// env is one test's database, fixture issuer and API. robot is the automation identity of AP §2:
// author and publisher, h-all responsible.
type env struct {
	t       *testing.T
	db      *sql.DB
	iss     *issuer.Issuer
	cfg     config.Auth
	api     *API
	d       deps
	robot   string // its bearer token
	robotID string
	mu      sync.Mutex
	logs    []string
}

func newEnv(t *testing.T, o options) *env {
	t.Helper()
	return newEnvWith(t, deps{}, o)
}

// newEnvWith is newEnv with ingestion's dependencies.
func newEnvWith(t *testing.T, d deps, o options) *env {
	t.Helper()
	e := &env{t: t, db: migrated(t), d: d}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := issuer.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if e.iss, err = issuer.New("http://"+l.Addr().String(), "bronzeward", key); err != nil {
		t.Fatal(err)
	}
	srv := &httptest.Server{Listener: l, Config: &http.Server{Handler: e.iss.Handler()}}
	srv.Start()
	t.Cleanup(srv.Close)
	e.cfg = testAuth(e.iss.URL)
	tok, err := auth.NewStore(e.db, e.cfg).Issue(t.Context(), "ci", []auth.Role{auth.Author, auth.Publisher}, auth.DefaultExpiry, "h-all", "h-all")
	if err != nil {
		t.Fatal(err)
	}
	e.robot, e.robotID = tok.Token, tok.Identity
	e.api = e.build(o)
	return e
}

// build returns an API over e's database and issuer, as serve builds it, with test options.
func (e *env) build(o options) *API {
	return e.buildWith(e.d, o)
}

// buildWith is build with other ingestion dependencies: another process over the same database.
func (e *env) buildWith(d deps, o options) *API {
	if o.logf == nil {
		o.logf = e.logf
	}
	if d.exec.MaxTransportDeadline == 0 {
		d.exec = testExecution()
	}
	return newAPI(e.db, auth.NewVerifier(e.cfg, e.db, auth.Discover(e.cfg.OIDC)), e.cfg, d, o)
}

func (e *env) logf(format string, args ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.logs = append(e.logs, fmt.Sprintf(format, args...))
}

func (e *env) logged(sub string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, l := range e.logs {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// human returns a valid access token for one of issuer.Humans.
func (e *env) human(name string) string {
	e.t.Helper()
	tok, err := e.iss.Mint(name, "", time.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}

// call is one request; path is a full path (prefix + … or path(…)).
type call struct {
	method, path, token, key, ifMatch, body string
}

func (e *env) do(h http.Handler, c call) *httptest.ResponseRecorder {
	var body io.Reader
	if c.body != "" {
		body = strings.NewReader(c.body)
	}
	r := httptest.NewRequest(c.method, c.path, body)
	if c.token != "" {
		r.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.key != "" {
		r.Header.Set("Idempotency-Key", c.key)
	}
	if c.ifMatch != "" {
		r.Header.Set("If-Match", c.ifMatch)
	}
	if c.body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// problem decodes rec's problem document and returns its code, failing the test if rec is not one.
func problem(t *testing.T, rec *httptest.ResponseRecorder) (code string, doc map[string]any) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("status %d, Content-Type %q, body %s: not a problem", rec.Code, ct, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	typ, _ := doc["type"].(string)
	instance, _ := doc["instance"].(string)
	code, ok := strings.CutPrefix(typ, "urn:bronzeward:problem:")
	if !ok || doc["status"] != float64(rec.Code) || !strings.HasPrefix(instance, "req_") {
		t.Fatalf("malformed problem %s", rec.Body)
	}
	return code, doc
}

func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) map[string]any {
	t.Helper()
	got, doc := problem(t, rec)
	if rec.Code != status || got != code {
		t.Fatalf("got %d %s (%s); want %d %s", rec.Code, got, rec.Body, status, code)
	}
	return doc
}

// migrated returns a fresh database at the binary's schema, installed.
func migrated(t *testing.T) *sql.DB {
	t.Helper()
	db, _ := dbtest.New(t)
	ms, err := migrate.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Apply(t.Context(), db, ms); err != nil {
		t.Fatal(err)
	}
	if _, _, err := migrate.Install(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

// testAuth is examples/bronzeward.yaml's auth block with issuerURL.
func testAuth(issuerURL string) config.Auth {
	return config.Auth{
		OIDC: config.OIDC{Issuer: issuerURL, Audience: "bronzeward", GroupsClaim: "groups", MaxTokenLifetime: 15 * time.Minute},
		Roles: config.Roles{
			Viewer: []string{"bw-viewers"}, Author: []string{"bw-authors"}, Publisher: []string{"bw-publishers"},
			Approver: []string{"bw-approvers"}, RecoveryAdmin: []string{"bw-recovery"},
		},
	}
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// execer is a *sql.DB or a *sql.Tx.
type execer interface {
	Exec(string, ...any) (sql.Result, error)
}

func mustExec(t *testing.T, db execer, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// storeImportBase writes import base revision ibr of machine, of the text 'machine: {}', declaring
// registry/pass as kind with encoding (nil for none) at version 1 in generation, in one
// transaction: a revision's rows are written with it (refuse_late_revision_row).
func storeImportBase(t *testing.T, db *sql.DB, ibr, machine, kind string, encoding any, generation string) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(t, tx, `INSERT INTO import_base_revision (id, machine, document, embedded, baseline_ciphertext, baseline_digest,
		baseline_digest_key, configuration_digest, created_at, author) VALUES ($1, $2, 'machine: {}', '[]', '\x01', $3, 'transit/baseline-digest:1', $3, now(),
		(SELECT min(id) FROM principal))`,
		ibr, machine, make([]byte, 32))
	mustExec(t, tx, `INSERT INTO import_base_reference (revision, name, kind, version, encoding, generation)
		VALUES ($1, 'registry/pass', $2, 1, $3, $4)`, ibr, kind, encoding, generation)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// newEpoch records a new current epoch, as a recovery-mode entry does (§12.1).
func newEpoch(t *testing.T, db *sql.DB) string {
	t.Helper()
	ep := id.New(id.Epoch)
	mustExec(t, db, "INSERT INTO recovery_epoch (epoch, entered_at) VALUES ($1, now())", ep)
	mustExec(t, db, "UPDATE installation_state SET epoch = $1", ep)
	return ep
}

func epoch(t *testing.T, db *sql.DB) string {
	t.Helper()
	var ep string
	if err := db.QueryRow("SELECT epoch FROM installation_state").Scan(&ep); err != nil {
		t.Fatal(err)
	}
	return ep
}

// Well-formed identifiers whose records need not exist (AP S0 step 4).
var (
	drf, pln, apr = id.New(id.Draft), id.New(id.Plan), id.New(id.Approval)
	mch, op, att  = id.New(id.Machine), id.New(id.Operation), id.New(id.Attempt)
	ing           = id.New(id.Ingestion)
)

// path fills a route pattern's wildcards with well-formed identifiers or names.
func path(pattern string) string {
	r := strings.NewReplacer("{id}", drf, "{name}", "base", "{machine}", mch, "{m}", mch, "{attempt}", att)
	switch {
	case strings.HasPrefix(pattern, "/plans/"):
		r = strings.NewReplacer("{id}", pln)
	case strings.HasPrefix(pattern, "/approvals/"):
		r = strings.NewReplacer("{id}", apr)
	case strings.HasPrefix(pattern, "/machines/"):
		r = strings.NewReplacer("{id}", mch)
	case strings.HasPrefix(pattern, "/operations/"):
		r = strings.NewReplacer("{id}", op, "{attempt}", att)
	case strings.HasPrefix(pattern, "/ingestions/"):
		r = strings.NewReplacer("{id}", ing)
	}
	return prefix + r.Replace(pattern)
}
