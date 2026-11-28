package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/fixtures/oidc/issuer"
	"github.com/ginsys/bronzeward/internal/auth"
)

// Ginsys/bronzeward#1276: negative cases on the real handlers for the fields that carry authority.
// Ownership, generation, epoch, principal, role and approval provenance are the server's, derived
// from the verified credential, the claim row and the installation state; no request member, query
// parameter or body sets, replaces or clears them. Each case runs with a credential that is
// otherwise authorized for the route, so a role denial cannot hide a missing member check, and each
// refusal is followed by a read of the rows that must not have moved.

// authorityTables are the tables a refused request must leave as they were: acts and idempotency
// records, principals, drafts, ingestion claims, operations, plans, approvals and their timeline.
var authorityTables = []string{"act", "idempotency_record", "principal", "identity_revocation", "draft", "staging_claim",
	"operation", "plan", "plan_state", "approval", "approval_revocation", "machine_event", "machine_state"}

// authorityState is a count and a digest of every row of each authorityTables table.
func authorityState(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, tb := range authorityTables {
		var n int
		var sum sql.NullString
		if err := db.QueryRow(`SELECT count(*), md5(string_agg(x::text, '|' ORDER BY x::text)) FROM `+tb+` x`).Scan(&n, &sum); err != nil {
			fatalf(t, "%s: %v", tb, err)
			return got
		}
		got[tb] = fmt.Sprintf("%d rows, digest %s", n, sum.String)
	}
	return got
}

// wantSameState fails for each table whose rows differ from before.
func wantSameState(t *testing.T, db *sql.DB, before map[string]string, what string) {
	t.Helper()
	after := authorityState(t, db)
	for _, tb := range authorityTables {
		if before[tb] != after[tb] {
			t.Errorf("%s changed %s: %s, then %s", what, tb, before[tb], after[tb])
		}
	}
}

// authEnv serves every route: the entry route on a recovery start, which is the only process that
// serves it before its entry commits (§12.2), and the rest on the normal one.
type authEnv struct {
	*env
	entry *API
	n     atomic.Int64
}

func newAuthEnv(t *testing.T) *authEnv {
	t.Helper()
	e := newEnv(t, options{})
	var entered atomic.Int32
	return &authEnv{env: e, entry: recoveryStart(t, e, &entered)}
}

func (a *authEnv) handler(rt *route) http.Handler {
	if rt.pattern == entryPattern {
		return a.entry
	}
	return a.api
}

// routePath fills rt's wildcards with well-formed identifiers or names.
func routePath(pattern string) string {
	return path(strings.NewReplacer("{kind}", "profile").Replace(pattern))
}

// send sends c to rt's route with a fresh Idempotency-Key and an If-Match.
func (a *authEnv) send(rt *route, c call) *httptest.ResponseRecorder {
	c.method = rt.method
	if c.path == "" {
		c.path = routePath(rt.pattern)
	}
	c.key = fmt.Sprintf("k-authority-%016d", a.n.Add(1))
	c.ifMatch = `"1-x"`
	return a.do(a.handler(rt), c)
}

var roleHumans = map[auth.Role]string{auth.Viewer: "h-viewer", auth.Author: "h-author", auth.Publisher: "h-publisher",
	auth.Approver: "h-approver", auth.RecoveryAdmin: "h-recovery"}

// qualifying is a human holding the route's first listed role.
func (a *authEnv) qualifying(rt *route) string { return a.human(roleHumans[rt.roles[0]]) }

func isNoBody(rt *route) bool {
	_, ok := rt.input().(*noBody)
	return ok
}

// bodyRoutes are the routes that decode a request body into an input.
func bodyRoutes() []*route {
	var rs []*route
	for _, rt := range routes() {
		if rt.method != http.MethodGet && rt.input != nil && !isNoBody(rt) {
			rs = append(rs, rt)
		}
	}
	return rs
}

// writeRoutes are the routes that record an act: mutating ones, a read-only POST excluded.
func writeRoutes() []*route {
	var rs []*route
	for _, rt := range routes() {
		if rt.mutating() {
			rs = append(rs, rt)
		}
	}
	return rs
}

// injectedMembers are names a client might use to supply what the server derives.
var injectedMembers = []string{"owner", "ownerGeneration", "generation", "controller", "epoch", "approver", "principal", "role",
	"createdBy", "requestedBy", "actor", "approval", "provenance", "metadata"}

// injectedValues set a member to a string, replace it with an object that copies a recorded
// approver, and clear it.
var injectedValues = []string{`"pr_x"`, `{"principal":"pr_x","role":"approver"}`, `null`}

// nestedCase is a body that carries one unknown member inside the nested object at path.
type nestedCase struct{ path, body string }

// nestedCases lists, for each nested struct of t reached through fields, slices and maps, one body
// that puts an unknown member in it, with the path the refusal names. A type that decodes itself is
// left to that type, as the decoder does.
func nestedCases(t reflect.Type) []nestedCase {
	var out []nestedCase
	var walk func(t reflect.Type, path string, wrap func(string) string)
	walk = func(t reflect.Type, path string, wrap func(string) string) {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if pt := reflect.PointerTo(t); pt.Implements(jsonUnmarshaler) || pt.Implements(textUnmarshaler) {
			return
		}
		switch t.Kind() {
		case reflect.Struct:
			if path != "" {
				out = append(out, nestedCase{strings.TrimSuffix(path, "."), wrap(`{"owner":"pr_x"}`)})
			}
			fields := fieldTypes(t)
			for _, n := range slices.Sorted(maps.Keys(fields)) {
				walk(fields[n], path+n+".", func(inner string) string { return wrap(`{"` + n + `":` + inner + `}`) })
			}
		case reflect.Slice, reflect.Array:
			if t.Elem().Kind() != reflect.Uint8 {
				walk(t.Elem(), strings.TrimSuffix(path, ".")+"[0].", func(inner string) string { return wrap("[" + inner + "]") })
			}
		case reflect.Map:
			walk(t.Elem(), path+"*.", func(inner string) string { return wrap(`{"k":` + inner + `}`) })
		}
	}
	walk(t, "", func(inner string) string { return inner })
	return out
}

func detailOf(doc map[string]any) string {
	d, _ := doc["detail"].(string)
	return d
}

// A request member that sets, replaces or clears what the server derives is refused 400
// invalid-request, at the top level and inside every nested object of the route's body, and the
// rows do not move. The credential is the one the route needs, so the refusal is the member's. The
// control, a member the route reads, is not refused for its name, so the refusals are not the route
// refusing everything (§9.1).
func TestAuthorityMemberInjection(t *testing.T) {
	a := newAuthEnv(t)
	total := 0
	for _, rt := range bodyRoutes() {
		t.Run(rt.method+" "+rt.pattern, func(t *testing.T) {
			tok := a.qualifying(rt)
			typ := reflect.TypeOf(rt.input()).Elem()
			real := fieldTypes(typ)
			before := authorityState(t, a.db)
			sent := 0
			for _, name := range injectedMembers {
				if _, ok := real[name]; ok {
					continue // a member the route reads
				}
				for _, v := range injectedValues {
					doc := wantProblem(t, a.send(rt, call{token: tok, body: `{"` + name + `":` + v + `}`}), http.StatusBadRequest, "invalid-request")
					if want := fmt.Sprintf("unknown member %q", name); detailOf(doc) != want {
						t.Errorf("%s = %s: refused for %q, want %q", name, v, detailOf(doc), want)
					}
					sent++
				}
			}
			for _, nc := range nestedCases(typ) {
				doc := wantProblem(t, a.send(rt, call{token: tok, body: nc.body}), http.StatusBadRequest, "invalid-request")
				if want := fmt.Sprintf("an unknown or differently cased member in %q", nc.path); detailOf(doc) != want {
					t.Errorf("nested %s: refused for %q, want %q", nc.path, detailOf(doc), want)
				}
				sent++
			}
			wantSameState(t, a.db, before, "the refused injections")
			// The control: a member the route reads, or none, passes the body's shape checks.
			body := `{}`
			if names := slices.Sorted(maps.Keys(real)); len(names) > 0 {
				body = `{"` + names[0] + `":null}`
			}
			ctl := a.send(rt, call{token: tok, body: body})
			if d := detailOf(decodeAny(ctl)); strings.Contains(d, "unknown member") || strings.Contains(d, "differently cased") || ctl.Code == http.StatusForbidden {
				t.Errorf("control %s: %d %s", body, ctl.Code, ctl.Body)
			}
			total += sent
			t.Logf("%s %s: %d refused, rows unchanged; control %s answered %d", rt.method, rt.pattern, sent, body, ctl.Code)
		})
	}
	if total == 0 {
		t.Fatal("no injection was sent")
	}
	t.Logf("%d injections refused over %d routes", total, len(bodyRoutes()))
}

// decodeAny is the answer's problem document, or nil for an answer that is not one.
func decodeAny(rec *httptest.ResponseRecorder) map[string]any {
	var doc map[string]any
	if rec.Header().Get("Content-Type") != "application/problem+json" || json.Unmarshal(rec.Body.Bytes(), &doc) != nil {
		return nil
	}
	return doc
}

// A mutating request takes no query but a scoped route's scope, once; a no-body route takes no
// body of any shape. Each is refused 400 with the rows unchanged, and the control, the route's own
// form, is not refused for the query or the body.
func TestAuthorityQueryAndNoBody(t *testing.T) {
	a := newAuthEnv(t)
	for _, rt := range writeRoutes() {
		t.Run(rt.method+" "+rt.pattern, func(t *testing.T) {
			tok := a.qualifying(rt)
			before := authorityState(t, a.db)
			queries := []string{"?owner=pr_x", "?scope=cluster&owner=pr_x", "?epoch=ep_x", "?principal=pr_x"}
			if rt.scoped {
				queries = append(queries, "?scope=cluster&scope=library", "?scope=other", "?scope=")
			} else {
				queries = append(queries, "?scope=cluster", "?scope=library")
			}
			body := ""
			if !isNoBody(rt) {
				body = `{}`
			}
			for _, q := range queries {
				doc := wantProblem(t, a.send(rt, call{token: tok, path: routePath(rt.pattern) + q, body: body}), http.StatusBadRequest, "invalid-request")
				if d := detailOf(doc); !strings.Contains(d, "query") {
					t.Errorf("%s: refused for %q, want the query", q, d)
				}
			}
			if isNoBody(rt) {
				for _, b := range []string{`{}`, `{"owner":"pr_x"}`, `null`, `x`} {
					doc := wantProblem(t, a.send(rt, call{token: tok, body: b}), http.StatusBadRequest, "invalid-request")
					if d := detailOf(doc); d != "this route takes no body" {
						t.Errorf("body %s: refused for %q", b, d)
					}
				}
			}
			wantSameState(t, a.db, before, "the refused queries and bodies")
			if rt.scoped { // the control: the one query a scoped route takes
				ctl := a.send(rt, call{token: tok, path: routePath(rt.pattern) + "?scope=cluster", body: body})
				if d := detailOf(decodeAny(ctl)); strings.Contains(d, "query") || ctl.Code == http.StatusForbidden {
					t.Errorf("control ?scope=cluster: %d %s", ctl.Code, ctl.Body)
				}
			}
		})
	}
}

// who is a credential and the roles the server reads from it.
type who struct {
	name, token string
	service     bool
	roles       []auth.Role
}

func (a *authEnv) identities(t *testing.T) []who {
	t.Helper()
	none, err := a.iss.Sign(map[string]any{"iss": a.iss.URL, "aud": "bronzeward", "sub": "h-nobody", "jti": issuer.JTI(),
		"iat": time.Now().Unix(), "nbf": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return []who{
		{"viewer", a.human("h-viewer"), false, []auth.Role{auth.Viewer}},
		{"author", a.human("h-author"), false, []auth.Role{auth.Author}},
		{"publisher", a.human("h-publisher"), false, []auth.Role{auth.Publisher}},
		{"approver", a.human("h-approver"), false, []auth.Role{auth.Approver}},
		{"recovery-admin", a.human("h-recovery"), false, []auth.Role{auth.RecoveryAdmin}},
		{"service", a.robot, true, []auth.Role{auth.Author, auth.Publisher}},
		{"no role", none, false, nil},
	}
}

// Every route that records an act or reads a body, against every identity: the identities §10.3
// denies are refused 403 forbidden, naming the route's roles and, for automation on a human-only
// route, humanOnly, and nothing moves; the identities it admits are not refused for their roles,
// so the matrix is not a router that refuses everything.
func TestAuthorityIdentityMatrix(t *testing.T) {
	a := newAuthEnv(t)
	ids := a.identities(t)
	var rs []*route
	for _, rt := range routes() {
		if rt.method != http.MethodGet {
			rs = append(rs, rt)
		}
	}
	admitted := func(rt *route, id who) bool {
		return slices.ContainsFunc(rt.roles, func(r auth.Role) bool { return slices.Contains(id.roles, r) }) && !(rt.humanOnly && id.service)
	}
	before := authorityState(t, a.db)
	denied, allowed := 0, 0
	for _, rt := range rs {
		for _, id := range ids {
			if admitted(rt, id) {
				continue
			}
			doc := wantProblem(t, a.send(rt, call{token: id.token, body: `{}`}), http.StatusForbidden, "forbidden")
			if fmt.Sprint(doc["roles"]) != fmt.Sprint(rt.roles) {
				t.Errorf("%s %s as %s: roles %v, want %v", rt.method, rt.pattern, id.name, doc["roles"], rt.roles)
			}
			if rt.humanOnly && id.service && doc["humanOnly"] != true {
				t.Errorf("%s %s as service: humanOnly %v", rt.method, rt.pattern, doc["humanOnly"])
			}
			denied++
		}
	}
	wantSameState(t, a.db, before, "the denied requests")
	for _, rt := range rs {
		for _, id := range ids {
			if !admitted(rt, id) {
				continue
			}
			rec := a.send(rt, call{token: id.token, body: `{}`})
			if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
				t.Errorf("%s %s as %s: %d %s", rt.method, rt.pattern, id.name, rec.Code, rec.Body)
			}
			allowed++
		}
	}
	if denied == 0 || allowed == 0 {
		t.Fatalf("%d denied and %d admitted", denied, allowed)
	}
	t.Logf("%d routes, %d identities: %d denied with 403 forbidden and no row moved, %d admitted past the role check", len(rs), len(ids), denied, allowed)
}

// Plans and approvals on the real routes. The creator, the approver and the revoker are the
// verified caller, never a member: each injected body is refused with the plan, its approval and
// every other row where they were, and so is each identity the route does not admit, a token
// renewed without the approver group included. The controls then succeed, and the recorded
// principal, role and plan are the caller's, so a refusal here is not the route refusing
// everything. The same key under another subject is not a replay of the first subject's approval.
func TestAuthorityPlanApprovalRevocation(t *testing.T) {
	t.Parallel()
	p := newPlanEnv(t)
	n := 0
	refused := func(what, method, p2, token, body string, status int, code string) {
		t.Helper()
		n++
		before := authorityState(t, p.db)
		wantProblem(t, p.do(p.api, call{method: method, path: prefix + p2, token: token, key: fmt.Sprintf("k-authority-real-%04d", n), body: body}),
			status, code)
		wantSameState(t, p.db, before, what)
	}
	// Plan creation: createdBy, state and approval are the server's.
	for _, extra := range []string{`,"createdBy":"pr_x"`, `,"principal":"pr_x"`, `,"role":"approver"`, `,"epoch":"ep_x"`,
		`,"state":"approved"`, `,"approval":"apr_x"`, `,"owner":"pr_x"`, `,"expiresAt":"2099-01-01T00:00:00Z"`} {
		refused("a plan with "+extra, "POST", "/plans", p.robot, applyBody(p.target.rel, p.machine, extra), http.StatusBadRequest, "invalid-request")
	}
	plan := decode[planBody](t, p.plan(p.robot, "k-plan-0123456789ab", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
	if n := count(t, p.db, `SELECT count(*) FROM plan WHERE id = $1 AND created_by = $2`, plan.ID, p.robotID); n != 1 {
		t.Fatalf("the plan is not recorded as the automation identity's")
	}
	if n := count(t, p.db, `SELECT count(*) FROM act WHERE action = 'plan.create' AND principal = $1 AND role = 'publisher'`, p.robotID); n != 1 {
		t.Fatalf("%d plan acts of the automation identity as publisher", n)
	}
	// Approval.
	approvePath := "/plans/" + plan.ID + "/approvals"
	approver := p.human("h-approver")
	for _, body := range []string{`{"approver":{"principal":"pr_x","role":"approver"}}`, `{"approver":null}`, `{"principal":"pr_x"}`,
		`{"role":"recovery-admin"}`, `{"selfApproval":{"marked":false,"reasons":[]}}`, `{"approval":"apr_x"}`, `{"epoch":"ep_x"}`, `{"plan":"` + plan.ID + `"}`} {
		refused("approval with "+body, "POST", approvePath, approver, body, http.StatusBadRequest, "invalid-request")
	}
	none, err := p.iss.Sign(map[string]any{"iss": p.iss.URL, "aud": "bronzeward", "sub": "h-nobody", "jti": issuer.JTI(),
		"iat": time.Now().Unix(), "nbf": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	renewedWithout, err := p.iss.MintWithoutGroup("h-all", "bw-approvers", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for name, tok := range map[string]string{"viewer": p.human("h-viewer"), "author": p.human("h-author"), "publisher": p.human("h-publisher"),
		"recovery-admin": p.human("h-recovery"), "service": p.robot, "no role": none, "h-all renewed without the approver group": renewedWithout} {
		refused("approval by "+name, "POST", approvePath, tok, `{}`, http.StatusForbidden, "forbidden")
	}
	p.wantUnapprovedState(t, plan.ID, "proposed")
	// The control: the same subject with its current groups approves, as itself.
	approved := decode[approvalBody](t, p.approve(p.human("h-all"), "k-approve-0123456789", plan.ID), http.StatusCreated)
	hAll := p.principalOf("h-all")
	if approved.Approver.Principal != hAll || approved.Approver.Role != "approver" {
		t.Fatalf("approval by %+v, want %s as approver", approved.Approver, hAll)
	}
	if n := count(t, p.db, `SELECT count(*) FROM approval WHERE id = $1 AND approver = $2 AND role = 'approver'`, approved.ID, hAll); n != 1 {
		t.Fatalf("the approval row does not name the caller")
	}
	// The same key under another subject executes for that subject, here refused by the plan's state, and is no replay.
	rec := p.approve(approver, "k-approve-0123456789", plan.ID)
	wantProblem(t, rec, http.StatusConflict, "conflict")
	if rec.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("another subject's request replayed %s's approval", "h-all")
	}
	if n := count(t, p.db, `SELECT count(*) FROM approval WHERE plan = $1`, plan.ID); n != 1 {
		t.Fatalf("%d approvals", n)
	}
	// Revocation: revokedBy and role are the server's, and only an approver or a recovery admin revokes.
	revokePath := "/approvals/" + approved.ID + "/revocations"
	for _, body := range []string{`{"reason":"wrong","revokedBy":"pr_x"}`, `{"reason":"wrong","principal":"pr_x"}`, `{"reason":"wrong","role":"approver"}`,
		`{"reason":"wrong","epoch":"ep_x"}`, `{"reason":"wrong","plan":"` + plan.ID + `"}`, `{"reason":"wrong","approval":null}`} {
		refused("revocation with "+body, "POST", revokePath, p.human("h-recovery"), body, http.StatusBadRequest, "invalid-request")
	}
	for name, tok := range map[string]string{"viewer": p.human("h-viewer"), "author": p.human("h-author"), "publisher": p.human("h-publisher"), "service": p.robot} {
		refused("revocation by "+name, "POST", revokePath, tok, `{"reason":"wrong"}`, http.StatusForbidden, "forbidden")
	}
	p.wantUnapprovedState(t, plan.ID, "approved")
	b := decode[approvalRevocationBody](t, p.revokeApproval(p.human("h-recovery"), "k-revoke-0123456789ab", approved.ID, `{"reason":"wrong"}`), http.StatusCreated)
	if b.RevokedBy != p.principalOf("h-recovery") || b.Role != "recovery-admin" {
		t.Fatalf("revocation by %s as %s", b.RevokedBy, b.Role)
	}
	// A revoked subject is refused whatever its roles.
	mustExec(t, p.db, `UPDATE principal SET revoked = true WHERE id = $1`, p.principalOf("h-approver"))
	refused("a revoked subject's approval", "POST", approvePath, approver, `{}`, http.StatusForbidden, "identity-revoked")
}

// wantUnapprovedState fails unless the plan's state is want.
func (p *planEnv) wantUnapprovedState(t *testing.T, plan, want string) {
	t.Helper()
	var state string
	if err := p.db.QueryRow(`SELECT state FROM plan_state WHERE plan = $1`, plan).Scan(&state); err != nil || state != want {
		t.Fatalf("plan %s is %q (%v), want %q", plan, state, err, want)
	}
}

// The operation routes of a recovery admin: the requester, the owner and the generation of a
// takeover are the server's (the generation a request names is the one it read, and must still be
// current), so an injected requester, owner or controller is refused with the operation's state,
// generation and owner where they were, and an identity that is not a recovery admin is refused 403.
// The control takes over, and the owner is the serving controller's, the requester the caller.
func TestAuthorityOperationTakeover(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	next := observer(t, ae.env, &fakeExecutor{}, options{})
	n := 0
	state0, gen0, owner0 := ae.state(t)
	refused := func(what string, token, p2, body string, status int, code string) {
		t.Helper()
		n++
		before := authorityState(t, ae.db)
		wantProblem(t, ae.do(next, call{method: "POST", path: prefix + "/operations/" + ae.op + p2, token: token,
			key: fmt.Sprintf("k-authority-op-%04d", n), body: body}), status, code)
		wantSameState(t, ae.db, before, what)
		if s, g, o := ae.state(t); s != state0 || g != gen0 || o != owner0 {
			t.Errorf("%s: the operation is %s at generation %d owned by %s, was %s at %d by %s", what, s, g, o, state0, gen0, owner0)
		}
	}
	rec := ae.human("h-recovery")
	for _, body := range []string{`{"generation":1,"reason":"lost","owner":"pr_x"}`, `{"generation":1,"reason":"lost","controller":"pr_x"}`,
		`{"generation":1,"reason":"lost","requestedBy":"pr_x"}`, `{"generation":1,"reason":"lost","role":"approver"}`,
		`{"generation":1,"reason":"lost","ownerGeneration":9}`, `{"generation":1,"reason":"lost","epoch":"ep_x"}`,
		`{"generation":1,"reason":"lost","metadata":{"owner":"pr_x"}}`} {
		refused("a takeover with "+body, rec, "/takeovers", body, http.StatusBadRequest, "invalid-request")
	}
	for _, body := range []string{`{"to":"cancelled","reason":"r","resolvedBy":"pr_x"}`, `{"to":"cancelled","reason":"r","owner":"pr_x"}`,
		`{"to":"cancelled","reason":"r","role":"approver"}`} {
		refused("a resolution with "+body, rec, "/resolutions", body, http.StatusBadRequest, "invalid-request")
	}
	for name, tok := range map[string]string{"viewer": ae.human("h-viewer"), "author": ae.human("h-author"), "publisher": ae.human("h-publisher"),
		"approver": ae.human("h-approver"), "service": ae.robot} {
		refused("a takeover by "+name, tok, "/takeovers", `{"generation":1,"reason":"lost"}`, http.StatusForbidden, "forbidden")
		refused("a resolution by "+name, tok, "/resolutions", `{"to":"cancelled","reason":"r"}`, http.StatusForbidden, "forbidden")
	}
	// Text that reads like authority, in the one member that carries free text, is recorded as text.
	lookalike := `generation 99 owner pr_x requestedBy pr_x`
	b := decode[takeoverBody](t, ae.post(next, "take-authority", "/takeovers", `{"generation":1,"reason":"`+lookalike+`"}`), http.StatusCreated)
	if b.RequestedBy != ae.principalOf("h-recovery") || b.Role != "recovery-admin" || b.Generation != 2 || b.Reason != lookalike {
		t.Fatalf("takeover answered %+v", b)
	}
	if s, g, o := ae.state(t); s != "unresolved" || g != 2 || o != next.d.owner.ID {
		t.Fatalf("the operation is %s at generation %d owned by %s; want unresolved, 2 and %s", s, g, o, next.d.owner.ID)
	}
}

// The ingestion routes: the owner and the generation of a claim are the server's and the claim row's.
// A start or a continuation, takeover or abandonment of a paused claim that carries an owner, a
// generation or a controller is refused with the claim, its operation, the jobs handed to the runner
// and every other row where they were. The control continues the claim: it is taken by the serving
// process at the next generation, whatever the refused bodies named.
func TestAuthorityIngestionOwnership(t *testing.T) {
	ie := newIngestEnv(t, options{})
	author := ie.human("h-author")
	n := 0
	refused := func(what string, c call) {
		t.Helper()
		n++
		c.key = fmt.Sprintf("k-authority-ing-%04d", n)
		before, jobs := authorityState(t, ie.db), len(ie.runs())
		wantProblem(t, ie.do(ie.api, c), http.StatusBadRequest, "invalid-request")
		wantSameState(t, ie.db, before, what)
		if len(ie.runs()) != jobs {
			t.Errorf("%s handed a job to the runner", what)
		}
	}
	// Start: the claim's owner and generation come from the server.
	for _, over := range []map[string]any{{"owner": "pr_x"}, {"ownerGeneration": 7}, {"generation": 7}, {"controller": "pr_x"}, {"principal": "pr_x"},
		{"epoch": "ep_x"}, {"operation": "op_x"}, {"claim": "ing_x"}} {
		refused(fmt.Sprintf("a start with %v", over), ingestCall(author, "", ie.etag, ie.body(over)))
	}
	if claims, ops := ie.rowCounts(t); claims != 0 || ops != 0 {
		t.Fatalf("%d claims and %d operations after refused starts", claims, ops)
	}
	op, j, first := ie.pausedForContinue(t)
	claimPath := prefix + "/ingestions/" + j.claim.ID
	same := func(what string) {
		t.Helper()
		r := ie.pausedRow(t, j.claim.ID)
		if r.state != first.state || r.review != first.review || r.gen != first.gen || r.owner != first.owner || r.opState != first.opState ||
			r.opGen != first.opGen || r.opOwner != first.opOwner || string(r.digest) != string(first.digest) {
			t.Errorf("%s: claim %s, was %s", what, r, first)
		}
	}
	for _, sub := range []string{"/continuations", "/takeovers", "/abandonments"} {
		for _, body := range []string{`{"owner":"pr_x"}`, `{"generation":5}`, `{"ownerGeneration":5}`, `{"controller":"pr_x"}`, `{"owner":null}`,
			`{"principal":"pr_x","role":"recovery-admin"}`} {
			refused(sub+" with "+body, call{method: "POST", path: claimPath + sub, token: author, body: body})
			same(sub + " with " + body)
		}
	}
	// The control: a continuation by the author takes the claim at the next generation, as the serving process.
	ie.continueReview(t, "k-continue-authority-1", j.claim.ID, op, 2)
	r := ie.pausedRow(t, j.claim.ID)
	if r.state != "held" || r.gen != 2 || r.owner != ie.d.owner.ID || r.opOwner != ie.d.owner.ID || r.opGen != 2 {
		t.Fatalf("after the continuation: claim %s, owner %q, operation owner %q; want held at generation 2 owned by %q", r, r.owner, r.opOwner, ie.d.owner.ID)
	}
}
