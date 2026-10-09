package api

import (
	"net/http"
	"testing"

	"github.com/ginsys/bronzeward/internal/config"
)

// AP S0 step 4 and its pass criteria, through the real router: each row design §13.7 says role alone
// denies answers 403 forbidden and writes no act and no principal row, whether or not the route's
// handler has landed; then the automation identity calls every human-only route; last, the operator
// lists h-all in deniedSubjects and h-recovery records its revocation.
func TestS0DeniedWalk(t *testing.T) {
	e := newEnv(t, options{})
	principals := count(t, e.db, "SELECT count(*) FROM principal")
	robot := e.robot
	h := map[string]string{}
	for _, n := range []string{"h-author", "h-publisher", "h-approver", "h-recovery", "h-viewer"} {
		h[n] = e.human(n)
	}
	type step struct {
		who, token, method, pattern string
	}
	walk := []step{
		{"h-author", h["h-author"], "POST", "/drafts/{id}/publications"},
		{"h-viewer", h["h-viewer"], "PUT", "/drafts/{id}/fragments/{name}"},
		{"h-viewer", h["h-viewer"], "POST", "/drafts/{id}/publications"},
		{"h-approver", h["h-approver"], "POST", "/plans"},
		{"automation", robot, "POST", "/plans/{id}/approvals"},
		{"h-recovery", h["h-recovery"], "POST", "/plans/{id}/approvals"},
		{"automation", robot, "POST", "/approvals/{id}/revocations"},
		{"h-author", h["h-author"], "POST", "/approvals/{id}/revocations"},
		{"h-publisher", h["h-publisher"], "POST", "/approvals/{id}/revocations"},
		{"h-approver", h["h-approver"], "POST", "/identity-revocations"},
		{"h-viewer", h["h-viewer"], "POST", "/machines/{id}/freezes"},
		{"h-author", h["h-author"], "POST", "/machines/{id}/unfreezes"},
		{"h-publisher", h["h-publisher"], "POST", "/machines/{id}/unfreezes"},
		{"h-recovery", h["h-recovery"], "POST", "/machines/{id}/unfreezes"},
		{"h-approver", h["h-approver"], "POST", "/recovery/scopes/{machine}/releases"},
		{"h-approver", h["h-approver"], "POST", "/operations/{id}/resolutions"},
		{"automation", robot, "POST", "/recovery/entries"},
		{"automation", robot, "POST", "/recovery/exits"},
	}
	humanOnly := 0
	for _, rt := range routes() {
		if rt.humanOnly {
			walk = append(walk, step{"automation", robot, rt.method, rt.pattern})
			humanOnly++
		}
	}
	if humanOnly != 18 {
		t.Fatalf("%d human-only routes; §9.2 marks 18", humanOnly)
	}
	for _, s := range walk {
		rec := e.do(e.api, call{method: s.method, path: path(s.pattern), token: s.token, key: "k0123456789abcdef", ifMatch: `"1-x"`, body: `{}`})
		wantProblem(t, rec, http.StatusForbidden, "forbidden")
		t.Logf("%-11s %-6s %-48s %d forbidden", s.who, s.method, s.pattern, rec.Code) // the retained walk table (AP S0)
	}
	if n := count(t, e.db, "SELECT count(*) FROM act WHERE via = 'api'"); n != 0 {
		t.Fatalf("%d acts from denied requests", n)
	}
	if n := count(t, e.db, "SELECT count(*) FROM principal"); n != principals {
		t.Fatalf("principal rows %d → %d from denied requests", principals, n)
	}

	// PA §10.4's order: list first, then record the revocation.
	e.cfg.DeniedSubjects = []config.DeniedSubject{{Iss: e.iss.URL, Sub: "h-all"}}
	e.api = e.build(options{})
	b := revocationOf(t, revoke(e, h["h-recovery"], "k-s0-revoke-h-all", `{"iss":"`+e.iss.URL+`","sub":"h-all","reason":"S0"}`))
	if !b.DeniedSubjectsListed || b.Role != "recovery-admin" {
		t.Fatalf("revocation %+v", b)
	}
	wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/acts", token: e.human("h-all")}), http.StatusForbidden, "identity-revoked")
}
