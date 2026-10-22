package api

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/fixtures/oidc/issuer"
	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/id"
)

// identityMembers is §9.2's answer of GET /identity, every member always present.
var identityMembers = []string{"epoch", "expiresAt", "issuer", "kind", "principal", "roles", "service", "subject"}

// identity reads GET /identity with tok, checks the answer's member set and its epoch against
// its Bronzeward-Epoch header, and returns it.
func (e *env) identity(tok string) (identityBody, string) {
	e.t.Helper()
	rec := e.do(e.api, call{method: "GET", path: prefix + "/identity", token: tok})
	if rec.Code != http.StatusOK {
		e.t.Fatalf("identity: %d %s", rec.Code, rec.Body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		e.t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(raw)); !slices.Equal(got, identityMembers) {
		e.t.Fatalf("members %v; want %v", got, identityMembers)
	}
	var b identityBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		e.t.Fatal(err)
	}
	if h := rec.Header().Get("Bronzeward-Epoch"); b.Epoch == "" || b.Epoch != h {
		e.t.Fatalf("epoch %q, header %q", b.Epoch, h)
	}
	if rec.Header().Get("ETag") != "" {
		e.t.Fatal("the identity read sent an ETag")
	}
	return b, rec.Body.String()
}

// §9.2, §10.3 decision R1: every fixture human and the automation identity are served their own
// kind, identity, roles, expiry and epoch, and nothing of the credential beyond them.
func TestIdentityServesEachPrincipal(t *testing.T) {
	e := newEnv(t, options{})
	acts, principals := count(t, e.db, "SELECT count(*) FROM act"), count(t, e.db, "SELECT count(*) FROM principal")
	want := map[string][]auth.Role{
		"h-author": {auth.Author}, "h-publisher": {auth.Publisher}, "h-approver": {auth.Approver},
		"h-recovery": {auth.RecoveryAdmin}, "h-viewer": {auth.Viewer}, "h-all": {auth.Author, auth.Publisher, auth.Approver},
	}
	if len(want) != len(issuer.Humans) {
		t.Fatalf("the test knows %d humans, the issuer %d", len(want), len(issuer.Humans))
	}
	var hAll string // the robot's responsible human, which has a row (helpers_test.go)
	if err := e.db.QueryRow(`SELECT id FROM principal WHERE kind = 'human' AND sub = 'h-all'`).Scan(&hAll); err != nil {
		t.Fatal(err)
	}
	for human, roles := range want {
		before := time.Now()
		tok := e.human(human)
		b, body := e.identity(tok)
		if b.Kind != auth.Human || b.Issuer == nil || *b.Issuer != e.iss.URL || b.Subject == nil || *b.Subject != human ||
			b.Service != nil || !slices.Equal(b.Roles, roles) || b.Epoch != epoch(t, e.db) {
			t.Errorf("%s: %s", human, body)
		}
		// The fixture's tokens live issuer.Lifetime, exp in whole seconds.
		if exp := before.Add(issuer.Lifetime).Truncate(time.Second); b.ExpiresAt.Before(exp) || b.ExpiresAt.After(exp.Add(2*time.Second)) || b.ExpiresAt.Location() != time.UTC {
			t.Errorf("%s: expiresAt %v; want about %v, UTC", human, b.ExpiresAt, exp)
		}
		switch {
		case human == "h-all" && (b.Principal == nil || *b.Principal != hAll):
			t.Errorf("h-all: principal %v; want %s", b.Principal, hAll)
		case human != "h-all" && b.Principal != nil:
			t.Errorf("%s: principal %s for a human with no row", human, *b.Principal)
		}
		if strings.Contains(body, tok) || strings.Contains(body, "bw-") {
			t.Errorf("%s: the answer echoes the token or its groups: %s", human, body)
		}
	}

	b, body := e.identity(e.robot)
	tokID, _, _ := strings.Cut(strings.TrimPrefix(e.robot, "bwt_"), ".")
	var expires time.Time
	if err := e.db.QueryRow(`SELECT expires_at FROM automation_token WHERE owner = $1`, e.robotID).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if b.Kind != auth.Service || b.Issuer != nil || b.Subject != nil || b.Service == nil || *b.Service != "ci" ||
		b.Principal == nil || *b.Principal != e.robotID || !slices.Equal(b.Roles, []auth.Role{auth.Author, auth.Publisher}) ||
		!b.ExpiresAt.Equal(expires) || b.ExpiresAt.Location() != time.UTC {
		t.Errorf("automation: %s", body)
	}
	if strings.Contains(body, tokID) || strings.Contains(body, "h-all") || strings.Contains(body, hAll) {
		t.Errorf("automation: the answer names the token or its responsible human: %s", body)
	}

	if n := count(t, e.db, "SELECT count(*) FROM act"); n != acts {
		t.Fatalf("acts %d → %d from identity reads", acts, n)
	}
	if n := count(t, e.db, "SELECT count(*) FROM principal"); n != principals {
		t.Fatalf("principal rows %d → %d from identity reads", principals, n)
	}
}

// §9.2: roles are served in design §13.7's order, whatever the order of the groups claim.
func TestIdentityRolesInDesignOrder(t *testing.T) {
	e := newEnv(t, options{})
	now := time.Now()
	tok, err := e.iss.Sign(map[string]any{"iss": e.iss.URL, "aud": "bronzeward", "sub": "h-mixed",
		"groups": []any{"bw-recovery", "bw-approvers", "bw-publishers", "bw-authors", "bw-viewers"},
		"iat":    now.Unix(), "nbf": now.Unix(), "exp": now.Add(5 * time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if b, body := e.identity(tok); !slices.Equal(b.Roles, anyRole) {
		t.Fatalf("roles: %s; want %v", body, anyRole)
	}
}

// Decision R1: a credential that maps to no role is served its identity with no roles, and is
// still refused every other route.
func TestIdentityServesRolelessHuman(t *testing.T) {
	e := newEnv(t, options{})
	tok, err := e.iss.MintWithoutGroup("h-viewer", "bw-viewers", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, body := e.identity(tok)
	if b.Kind != auth.Human || *b.Subject != "h-viewer" || b.Roles == nil || len(b.Roles) != 0 || !strings.Contains(body, `"roles":[]`) {
		t.Fatalf("role-less: %s", body)
	}
	wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/acts", token: tok}), http.StatusForbidden, "forbidden")
	// The route records no role, so a role-less read leaves no act to attribute.
	if n := count(t, e.db, "SELECT count(*) FROM act WHERE via = 'api'"); n != 0 {
		t.Fatalf("%d acts", n)
	}
}

// §10: a human's row comes from its first admitted mutating request, never from the read; once it
// exists the read names it.
func TestIdentityPrincipalAfterFirstWrite(t *testing.T) {
	e := newEnv(t, options{})
	if b, body := e.identity(e.human("h-author")); b.Principal != nil {
		t.Fatalf("before any write: %s", body)
	}
	if n := count(t, e.db, "SELECT count(*) FROM principal WHERE sub = 'h-author'"); n != 0 {
		t.Fatalf("the read created %d rows", n)
	}
	// Admitted, then refused inside its transaction: the row stays (§10).
	e.do(e.api, call{method: "POST", path: prefix + "/drafts", token: e.human("h-author"), key: "k-identity-0123456789",
		body: `{"cluster":"` + id.New(id.Cluster) + `","title":"x"}`})
	var idn string
	if err := e.db.QueryRow(`SELECT id FROM principal WHERE kind = 'human' AND sub = 'h-author'`).Scan(&idn); err != nil {
		t.Fatal(err)
	}
	if b, body := e.identity(e.human("h-author")); b.Principal == nil || *b.Principal != idn {
		t.Fatalf("after the write: %s; want principal %s", body, idn)
	}
}

// The read's refusals are every read's (§9.1, §9.4, §10, §12.2), with no new problem code.
func TestIdentityRefusals(t *testing.T) {
	e := newEnv(t, options{})
	get := func(a http.Handler, tok, query string) *httptest.ResponseRecorder {
		return e.do(a, call{method: "GET", path: prefix + "/identity" + query, token: tok})
	}
	noEpoch := func(name string, rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Header().Get("Bronzeward-Epoch") != "" || rec.Header().Get("Bronzeward-Recovery-Mode") != "" {
			t.Errorf("%s: an unauthenticated caller was told the epoch: %v", name, rec.Header())
		}
	}
	for _, d := range issuer.Defects {
		tok, err := e.iss.Mint("h-all", d, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		rec := get(e.api, tok, "")
		wantProblem(t, rec, http.StatusUnauthorized, "unauthenticated")
		noEpoch(d, rec)
	}
	rec := get(e.api, "", "")
	wantProblem(t, rec, http.StatusUnauthorized, "unauthenticated")
	noEpoch("no token", rec)

	wantProblem(t, get(e.api, e.human("h-viewer"), "?limit=1"), http.StatusBadRequest, "invalid-request")

	// A denied subject, then a revoked service identity: 403 identity-revoked.
	cfg := e.cfg
	cfg.DeniedSubjects = []config.DeniedSubject{{Iss: e.iss.URL, Sub: "h-viewer"}}
	denied := newAPI(e.db, auth.NewVerifier(cfg, e.db, auth.Discover(cfg.OIDC)), cfg, deps{}, options{logf: e.logf})
	rec = get(denied, e.human("h-viewer"), "")
	wantProblem(t, rec, http.StatusForbidden, "identity-revoked")
	noEpoch("denied", rec)

	// An expired, then a revoked, then an earlier epoch's automation token: 401.
	mustExec(t, e.db, `UPDATE automation_token SET issued_at = now() - interval '2 hours', expires_at = now() - interval '1 hour' WHERE owner = $1`, e.robotID)
	wantProblem(t, get(e.api, e.robot, ""), http.StatusUnauthorized, "unauthenticated")
	mustExec(t, e.db, `UPDATE automation_token SET issued_at = now(), expires_at = now() + interval '1 hour', revoked_at = now() WHERE owner = $1`, e.robotID)
	wantProblem(t, get(e.api, e.robot, ""), http.StatusUnauthorized, "unauthenticated")
	mustExec(t, e.db, `UPDATE automation_token SET revoked_at = NULL WHERE owner = $1`, e.robotID)
	if rec := get(e.api, e.robot, ""); rec.Code != http.StatusOK {
		t.Fatalf("control, the token restored: %d %s", rec.Code, rec.Body)
	}
	mustExec(t, e.db, `UPDATE principal SET revoked = true WHERE id = $1`, e.robotID)
	rec = get(e.api, e.robot, "")
	wantProblem(t, rec, http.StatusForbidden, "identity-revoked")
	noEpoch("revoked identity", rec)
	mustExec(t, e.db, `UPDATE principal SET revoked = false WHERE id = $1`, e.robotID)
	newEpoch(t, e.db)
	rec = get(e.api, e.robot, "")
	wantProblem(t, rec, http.StatusUnauthorized, "unauthenticated")
	noEpoch("earlier epoch", rec)
}

// §10.2: a service token is rechecked against the epoch at routing and inside the read, and a
// human's answer carries the epoch the read held.
func TestIdentityEpochChange(t *testing.T) {
	e := newEnv(t, options{})
	old := epoch(t, e.db)
	newEpoch(t, e.db)
	stale := newAPI(e.db, stubAuthn{p: auth.Principal{Kind: auth.Service, ID: e.robotID, Epoch: old,
		Roles: []auth.Role{auth.Author, auth.Publisher}, Expiry: time.Now().Add(time.Hour)}}, e.cfg, deps{}, options{logf: e.logf})
	wantProblem(t, e.do(stale, call{method: "GET", path: prefix + "/identity", token: "x"}), http.StatusUnauthorized, "unauthenticated")

	// A fresh installation, so the token is current at routing and only the read's recheck can
	// refuse it.
	f := newEnv(t, options{})
	var entered string
	f.api = f.build(options{beforeRead: func() {
		if entered == "" {
			entered = newEpoch(t, f.db)
		}
	}})
	rec := f.do(f.api, call{method: "GET", path: prefix + "/identity", token: f.robot})
	wantProblem(t, rec, http.StatusUnauthorized, "unauthenticated")
	if entered == "" {
		t.Fatal("the token was refused before the read")
	}
	if rec.Header().Get("Bronzeward-Epoch") != "" {
		t.Fatalf("a stale token was told the epoch: %v", rec.Header())
	}
	if b, body := f.identity(f.human("h-viewer")); b.Epoch != entered {
		t.Fatalf("human after the entry: %s; want epoch %s", body, entered)
	}
}

// §9.1, §9.2: recovery mode is served as the header, not a member, and a database that cannot be
// read answers 503 to both kinds of credential.
func TestIdentityRecoveryModeAndUnreadableDatabase(t *testing.T) {
	e := newEnv(t, options{})
	mustExec(t, e.db, "UPDATE installation_state SET recovery_mode = true")
	for _, tok := range []string{e.robot, e.human("h-viewer")} {
		rec := e.do(e.api, call{method: "GET", path: prefix + "/identity", token: tok})
		if rec.Code != http.StatusOK || rec.Header().Get("Bronzeward-Recovery-Mode") != "true" {
			t.Fatalf("in recovery mode: %d %v; want 200 with the header", rec.Code, rec.Header())
		}
		e.identity(tok)
	}
	mustExec(t, e.db, "ALTER TABLE installation_state RENAME TO installation_state_away")
	t.Cleanup(func() { mustExec(t, e.db, "ALTER TABLE installation_state_away RENAME TO installation_state") })
	for _, tok := range []string{e.robot, e.human("h-viewer")} {
		wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/identity", token: tok}),
			http.StatusServiceUnavailable, "dependency-unavailable")
	}
}

// §12.2: before a recovery start's entry commits, the identity read is refused like every request
// but the entry.
func TestIdentityRefusedBeforeEntry(t *testing.T) {
	e := newEnv(t, options{})
	e.api.d.preEntry.Store(true)
	wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/identity", token: e.human("h-recovery")}),
		http.StatusConflict, "recovery-mode-active")
	e.api.d.preEntry.Store(false)
	e.identity(e.human("h-recovery"))
}
