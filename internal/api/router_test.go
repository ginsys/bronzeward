package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/fixtures/oidc/issuer"
	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/id"
)

// specRoutes transcribes PA §9.2: method, path, qualifying roles in listed order, then "human"
// for human only and "if-match" where If-Match is required. A difference from routes() is a
// transcription error in one of the two.
var specRoutes = []string{
	"GET /clusters any", "GET /clusters/{id} any", "GET /machines any", "GET /machines/{id} any",
	"GET /machines/{id}/observations any", "GET /machines/{id}/timeline any",
	"GET /fragments any", "GET /fragments/{id} any", "GET /fragments/{id}/revisions any", "GET /fragment-revisions/{id} any",
	"GET /profiles any", "GET /profiles/{id} any", "GET /profiles/{id}/revisions any", "GET /profile-revisions/{id} any",
	"GET /assignments any", "GET /assignments/{id} any", "GET /assignments/{id}/revisions any", "GET /assignment-revisions/{id} any",
	"GET /drafts any", "GET /drafts/{id} any", "GET /ingestions/{id} any", "GET /releases any", "GET /releases/{id} any",
	"GET /releases/{id}/machines/{m}/review any",
	"GET /plans any", "GET /plans/{id} any", "GET /approvals/{id} any", "GET /operations any", "GET /operations/{id} any",
	"GET /operations/{id}/events any", "GET /acts any", "GET /recovery any",
	"POST /ingestions author human if-match",
	"POST /ingestions/{id}/marks author human", "POST /ingestions/{id}/takeovers author human",
	"POST /ingestions/{id}/abandonments author human",
	"POST /clusters author human", "POST /machines author human", "POST /machines/{id}/talos-endpoints author human",
	"POST /drafts author",
	"PUT /drafts/{id}/fragments/{name} author if-match", "DELETE /drafts/{id}/fragments/{name} author if-match",
	"PUT /drafts/{id}/profiles/{name} author if-match", "DELETE /drafts/{id}/profiles/{name} author if-match",
	"PUT /drafts/{id}/assignments/{machine} author if-match", "DELETE /drafts/{id}/assignments/{machine} author if-match",
	"POST /drafts/{id}/discard author if-match",
	"POST /drafts/{id}/publications publisher if-match",
	"POST /plans publisher",
	"POST /plans/{id}/cancellations publisher,approver,recovery-admin",
	"POST /plans/{id}/approvals approver human",
	"POST /approvals/{id}/revocations approver,recovery-admin",
	"POST /machines/{id}/freezes author,publisher,approver,recovery-admin",
	"POST /machines/{id}/unfreezes approver",
	"POST /recovery/entries recovery-admin human", "POST /recovery/exits recovery-admin human",
	"POST /recovery/scopes/{machine}/marks recovery-admin human", "POST /recovery/scopes/{machine}/releases recovery-admin human",
	"POST /recovery/accountings recovery-admin human",
	"POST /operations/{id}/attempts/{attempt}/accountings recovery-admin human",
	"POST /operations/{id}/takeovers recovery-admin human", "POST /operations/{id}/resolutions recovery-admin human",
	"POST /identity-revocations recovery-admin human",
}

func describe(rt *route) string {
	roles := make([]string, len(rt.roles))
	for i, r := range rt.roles {
		roles[i] = string(r)
	}
	s := rt.method + " " + rt.pattern + " " + strings.Join(roles, ",")
	if slices.Equal(rt.roles, anyRole) {
		s = rt.method + " " + rt.pattern + " any"
	}
	if rt.humanOnly {
		s += " human"
	}
	if rt.ifMatch {
		s += " if-match"
	}
	return s
}

func TestRouteTable(t *testing.T) {
	var got []string
	for _, rt := range routes() {
		got = append(got, describe(rt))
	}
	if !slices.Equal(got, specRoutes) {
		for _, s := range specRoutes {
			if !slices.Contains(got, s) {
				t.Errorf("missing or different: %s", s)
			}
		}
		for _, s := range got {
			if !slices.Contains(specRoutes, s) {
				t.Errorf("not in §9.2: %s", s)
			}
		}
		t.Fatalf("%d routes, §9.2 has %d (order matters too)", len(got), len(specRoutes))
	}
}

// Every problem code this package answers is a row of §9.4 (Task 1 adds 500 and 501).
func TestProblemCodesAreSpecified(t *testing.T) {
	spec, err := os.ReadFile("../../docs/spec/persistence-api.md")
	if err != nil {
		t.Fatal(err)
	}
	for code := range titles {
		if !regexp.MustCompile("(?m)^\\| [0-9]{3} \\|[^\\n]*`" + regexp.QuoteMeta(code) + "`").Match(spec) {
			t.Errorf("%s is not in §9.4's table", code)
		}
	}
}

// §10.1, §14 Auth rows: no credential and every token defect is 401 with WWW-Authenticate, carries no
// epoch and creates no principal row.
func TestUnauthenticated(t *testing.T) {
	e := newEnv(t, options{})
	before := count(t, e.db, "SELECT count(*) FROM principal")
	// The last character replaced by another, so the secret always differs.
	last := "A"
	if strings.HasSuffix(e.robot, "A") {
		last = "B"
	}
	tokens := map[string]string{"none": "", "garbage": "not-a-token", "automation, wrong secret": e.robot[:len(e.robot)-1] + last}
	for _, d := range issuer.Defects {
		tok, err := e.iss.Mint("h-recovery", d, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		tokens[d] = tok
	}
	for name, tok := range tokens {
		for _, c := range []call{
			{method: "GET", path: prefix + "/acts", token: tok},
			{method: "POST", path: prefix + "/identity-revocations", token: tok, key: "k0123456789abcdef", body: `{}`},
			{method: "GET", path: "/elsewhere", token: tok},
		} {
			rec := e.do(e.api, c)
			wantProblem(t, rec, http.StatusUnauthorized, "unauthenticated")
			if rec.Header().Get("WWW-Authenticate") != `Bearer error="invalid_token"` || rec.Header().Get("Bronzeward-Epoch") != "" {
				t.Errorf("%s %s: headers %v", name, c.path, rec.Header())
			}
		}
	}
	if n := count(t, e.db, "SELECT count(*) FROM principal"); n != before {
		t.Fatalf("principal rows %d → %d", before, n)
	}
}

func TestRevokedCredentialIs403(t *testing.T) {
	e := newEnv(t, options{})
	mustExec(t, e.db, "UPDATE principal SET revoked = true WHERE id = $1", e.robotID)
	rec := e.do(e.api, call{method: "GET", path: prefix + "/acts", token: e.robot})
	wantProblem(t, rec, http.StatusForbidden, "identity-revoked")
	if rec.Header().Get("Bronzeward-Epoch") != "" {
		t.Fatal("a revoked identity was told the epoch")
	}
}

type stubAuthn struct {
	p   auth.Principal
	err error
}

func (s stubAuthn) Authenticate(context.Context, string) (auth.Principal, error) { return s.p, s.err }

func TestAuthenticationUnavailableIs503(t *testing.T) {
	e := newEnv(t, options{})
	a := newAPI(e.db, stubAuthn{err: fmt.Errorf("%w: discovery", auth.ErrUnavailable)}, e.cfg, deps{}, options{logf: e.logf})
	wantProblem(t, e.do(a, call{method: "GET", path: prefix + "/acts", token: "x"}), http.StatusServiceUnavailable, "dependency-unavailable")
}

// AP S0 step 5, §9.2: no route dispatches, issues or lists tokens, or grants roles.
func TestNoDispatchTokenOrRoleRoute(t *testing.T) {
	e := newEnv(t, options{})
	all := e.human("h-all")
	for _, c := range []call{
		{method: "POST", path: prefix + "/dispatch"}, {method: "POST", path: prefix + "/plans/" + pln + "/dispatch"},
		{method: "POST", path: prefix + "/operations/" + op + "/dispatch"},
		{method: "POST", path: prefix + "/tokens"}, {method: "GET", path: prefix + "/tokens"},
		{method: "DELETE", path: prefix + "/tokens/" + id.New(id.Token)},
		{method: "POST", path: prefix + "/identities/" + e.robotID + "/tokens"},
		{method: "POST", path: prefix + "/roles"}, {method: "PUT", path: prefix + "/principals/" + e.robotID + "/roles"},
		{method: "DELETE", path: prefix + "/acts"}, {method: "POST", path: prefix + "/acts"},
		{method: "GET", path: "/"},
	} {
		for _, tok := range []string{all, e.robot} {
			c.token, c.key = tok, "k0123456789abcdef"
			rec := e.do(e.api, c)
			wantProblem(t, rec, http.StatusNotFound, "not-found")
			if rec.Header().Get("Bronzeward-Epoch") != epoch(t, e.db) {
				t.Errorf("%s %s: no epoch header on an authenticated 404", c.method, c.path)
			}
		}
	}
}

// §6.4, §16: no route serves the orphan report; it is the operator command alone. Control: a
// registered report route answers, so the 404s are not the router refusing every unknown path.
func TestNoOrphanReportRoute(t *testing.T) {
	for _, rt := range routes() {
		if strings.Contains(strings.ToLower(rt.pattern), "orphan") {
			t.Fatalf("route %s %s names the orphan report", rt.method, rt.pattern)
		}
	}
	reportPaths := []string{prefix + "/orphans", prefix + "/orphan-report", prefix + "/clusters/" + id.New(id.Cluster) + "/orphans"}
	e := newEnv(t, options{})
	all := e.human("h-all")
	for _, p := range reportPaths {
		for _, tok := range []string{all, e.robot} {
			wantProblem(t, e.do(e.api, call{method: "GET", path: p, token: tok}), http.StatusNotFound, "not-found")
		}
	}
	c := newEnv(t, options{extra: []*route{{method: http.MethodGet, pattern: "/orphans", roles: anyRole}}})
	if rec := c.do(c.api, call{method: "GET", path: reportPaths[0], token: c.human("h-all")}); rec.Code == http.StatusNotFound {
		t.Fatal("control: a registered report route answered 404")
	}
	t.Logf("no route names the orphan report; %d report paths answer 404 to a human and automation; control (registered route): answered", len(reportPaths))
}

// §10.3: no qualifying role, or automation on a human-only route, is 403 forbidden naming the roles.
func TestForbidden(t *testing.T) {
	e := newEnv(t, options{})
	rec := e.do(e.api, call{method: "POST", path: prefix + "/drafts", token: e.human("h-viewer"), key: "k0123456789abcdef", body: `{}`})
	doc := wantProblem(t, rec, http.StatusForbidden, "forbidden")
	if roles, _ := doc["roles"].([]any); len(roles) != 1 || roles[0] != "author" {
		t.Errorf("roles %v; want [author]", doc["roles"])
	}
	rec = e.do(e.api, call{method: "POST", path: prefix + "/clusters", token: e.robot, key: "k0123456789abcdef", body: `{}`})
	if doc := wantProblem(t, rec, http.StatusForbidden, "forbidden"); doc["humanOnly"] != true {
		t.Errorf("automation on a human-only route: %v", doc)
	}
	// A credential with no role at all has no read access either.
	tok, err := e.iss.Sign(map[string]any{"iss": e.iss.URL, "aud": "bronzeward", "sub": "h-nobody",
		"iat": time.Now().Unix(), "nbf": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/acts", token: tok}), http.StatusForbidden, "forbidden")
	if n := count(t, e.db, "SELECT count(*) FROM principal WHERE sub IN ('h-viewer', 'h-nobody')"); n != 0 {
		t.Fatalf("%d principal rows for refused requests", n)
	}
}

// Decision 1: an unbuilt route is authenticated and role-checked, then answers 501; a mutating
// one checks its headers first. Nothing is written.
func TestNotImplemented(t *testing.T) {
	e := newEnv(t, options{})
	pub := e.human("h-publisher")
	wantProblem(t, e.do(e.api, call{method: "POST", path: prefix + "/plans", token: pub}), http.StatusPreconditionRequired, "idempotency-key-required")
	wantProblem(t, e.do(e.api, call{method: "POST", path: prefix + "/plans", token: pub, key: "short"}), http.StatusBadRequest, "invalid-request")
	wantProblem(t, e.do(e.api, call{method: "POST", path: path("/drafts/{id}/publications"), token: pub, key: "k0123456789abcdef"}),
		http.StatusPreconditionRequired, "precondition-required")
	wantProblem(t, e.do(e.api, call{method: "POST", path: prefix + "/plans", token: pub, key: "k0123456789abcdef", body: `{}`}),
		http.StatusNotImplemented, "not-implemented")
	wantProblem(t, e.do(e.api, call{method: "GET", path: path("/machines/{id}/timeline"), token: e.human("h-viewer")}),
		http.StatusNotImplemented, "not-implemented")
	if n := count(t, e.db, "SELECT count(*) FROM principal WHERE sub = 'h-publisher'"); n != 0 {
		t.Fatal("an unbuilt route created a principal row")
	}
}

// §9.4: a problem's instance is also written to the server log, whichever check refused.
func TestEveryProblemIsLogged(t *testing.T) {
	e := newEnv(t, options{extra: []*route{testRoute(nil)}})
	author, viewer := e.human("h-author"), e.human("h-viewer")
	e.do(e.api, post(author, key, `{"note":"a"}`))
	for name, c := range map[string]call{
		"401":        {method: "GET", path: prefix + "/acts"},
		"403 role":   {method: "POST", path: prefix + "/drafts", token: viewer, key: key, body: `{}`},
		"404":        {method: "GET", path: prefix + "/nowhere", token: viewer},
		"428":        {method: "POST", path: prefix + "/plans", token: e.human("h-publisher")},
		"400 body":   post(author, "k-other-0123456789", `{"note":1}`),
		"501":        {method: "GET", path: prefix + "/plans", token: viewer},
		"422 reused": post(author, key, `{"note":"b"}`),
		"400 cursor": {method: "GET", path: prefix + "/acts?cursor=x", token: viewer},
	} {
		rec := e.do(e.api, c)
		_, doc := problem(t, rec)
		if !e.logged(doc["instance"].(string)) {
			t.Errorf("%s: instance %v is not in the server log", name, doc["instance"])
		}
	}
}

// §9.1: the epoch and the recovery-mode flag on every authenticated response.
func TestEpochHeaders(t *testing.T) {
	e := newEnv(t, options{})
	v := e.human("h-viewer")
	rec := e.do(e.api, call{method: "GET", path: prefix + "/clusters", token: v})
	if rec.Header().Get("Bronzeward-Epoch") != epoch(t, e.db) || rec.Header().Get("Bronzeward-Recovery-Mode") != "" {
		t.Fatalf("headers %v", rec.Header())
	}
	ep := newEpoch(t, e.db)
	mustExec(t, e.db, "UPDATE installation_state SET recovery_mode = true")
	rec = e.do(e.api, call{method: "GET", path: prefix + "/clusters", token: v})
	if rec.Header().Get("Bronzeward-Epoch") != ep || rec.Header().Get("Bronzeward-Recovery-Mode") != "true" {
		t.Fatalf("headers %v", rec.Header())
	}
}
