package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/talos"
)

// post is a recovery-admin's request, h-recovery, to one of an operation's recovery routes on a.
func (ae *attemptEnv) post(a *API, key, path, body string) *httptest.ResponseRecorder {
	return ae.do(a, call{method: "POST", path: prefix + "/operations/" + ae.op + path, token: ae.human("h-recovery"),
		key: "k-recovery-" + key + "-0123456789", body: body})
}

// actRow counts the act of an API request: its action, its role and the subjects it names.
func (ae *attemptEnv) actRow(t *testing.T, act, action string, subjects ...string) int {
	t.Helper()
	return count(t, ae.db, `SELECT count(*) FROM act WHERE id = $1 AND action = $2 AND role = 'recovery-admin' AND via = 'api'
		AND principal = $3 AND subjects = $4`, act, action, ae.principalOf("h-recovery"), subjects)
}

// A recovery-admin's takeover request (execution-recovery.md §3.4, persistence-api.md §9.2): the
// serving controller becomes the owner at the next generation and the operation is unresolved, with
// one ownership transition entry that names the requester and the act. A generation that moved, or
// an operation that ended, is refused and records nothing.
func TestTakeoverRequest(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	next := observer(t, ae.env, &fakeExecutor{}, options{})
	rec := ae.post(next, "take-1", "/takeovers", `{"generation":1,"reason":"the controller was lost"}`)
	b := decode[takeoverBody](t, rec, http.StatusCreated)
	if b.Operation != ae.op || b.State != "unresolved" || b.Generation != 2 || b.RequestedBy != ae.principalOf("h-recovery") ||
		b.Role != "recovery-admin" || b.Reason != "the controller was lost" {
		t.Fatalf("answered %+v", b)
	}
	if state, gen, owner := ae.state(t); state != "unresolved" || gen != 2 || owner != next.d.owner.ID {
		t.Fatalf("the operation is %s at generation %d owned by %s", state, gen, owner)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND revision = $2 AND kind = 'ownership-transition'
			AND entry->>'operation' = $3 AND entry->>'to' = $4 AND (entry->>'generation')::bigint = 2
			AND entry->>'requestedBy' = $5 AND entry->>'role' = 'recovery-admin' AND entry->>'act' = $6
			AND entry->>'reason' = 'the controller was lost'`,
		ae.machine, b.Revision, ae.op, next.d.owner.ID, b.RequestedBy, b.Act); n != 1 {
		t.Fatal("no ownership transition entry naming the request")
	}
	if n := ae.actRow(t, b.Act, "operation.takeover", ae.op, ae.machine); n != 1 {
		t.Fatal("no act of the request")
	}
	// The generation the requester read is no longer current.
	wantProblem(t, ae.post(next, "take-2", "/takeovers", `{"generation":1,"reason":"again"}`), http.StatusConflict, "conflict")
	if _, gen, _ := ae.state(t); gen != 2 {
		t.Fatalf("a stale request moved the generation to %d", gen)
	}
	wantProblem(t, ae.post(next, "take-3", "/takeovers", `{"generation":0,"reason":"zero"}`), http.StatusBadRequest, "invalid-request")
	other := ae.do(next, call{method: "POST", path: prefix + "/operations/op_aaaaaaaaaaaaaaaaaaaaaaaaaa/takeovers",
		token: ae.human("h-recovery"), key: "k-recovery-take-4-0123456789", body: `{"generation":1,"reason":"none"}`})
	wantProblem(t, other, http.StatusNotFound, "not-found")
}

// An accounting decision by request (execution-recovery.md §5.2): the attempt whose response was
// lost is accounted by the recovery-admin once its executor is taken over, the floor elapsed and a
// recovery observation ended the settle. The route passes the facts, so an explained version change
// is accepted and an unexplained one is a conflict; a second decision, another operation's attempt
// and an observation of another machine's are refused.
func TestAccountingRequest(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	att := ae.sent(t)
	if _, err := ae.a.recordResponse(context.Background(), att.Attempt, answer(talos.ClassTransport)); err != nil {
		t.Fatal(err)
	}
	next := ae.takenOverBy(t)
	start := ae.observed(t, func(c *cobs) { c.purpose, c.shift = "recovery", settleWindow })
	end := ae.observed(t, func(c *cobs) { c.purpose, c.shift, c.resource = "recovery", settleWindow+time.Minute, "rv2" })
	path := "/attempts/" + att.Attempt + "/accountings"
	body := func(facts string) string {
		return fmt.Sprintf(`{"start":%q,"end":%q,"facts":%s}`, start, end, facts)
	}
	wantProblem(t, ae.post(next, "acc-1", path, body(`{}`)), http.StatusConflict, "conflict")
	if n := count(t, ae.db, `SELECT count(*) FROM attempt_accounting`); n != 0 {
		t.Fatal("an unexplained version change was accounted")
	}
	// An observation of another machine is not the operation's: the otherwise eligible decision is
	// refused before any accounting.
	ae.second(t)
	own := ae.machine
	ae.machine = ae.machine2
	foreign := ae.observed(t, func(c *cobs) { c.purpose = "drift" })
	ae.machine = own
	elsewhere := fmt.Sprintf(`{"start":%q,"end":%q,"facts":{"explanation":"the abandoned apply landed"}}`, start, foreign)
	wantProblem(t, ae.post(next, "acc-x2", path, elsewhere), http.StatusNotFound, "not-found")
	if n := count(t, ae.db, `SELECT count(*) FROM attempt_accounting`) + count(t, ae.db,
		`SELECT count(*) FROM act WHERE action = 'attempt.account'`); n != 0 {
		t.Fatal("a refused cross-object request was accounted")
	}
	b := decode[accountingBody](t, ae.post(next, "acc-2", path, body(`{"explanation":"the abandoned apply landed"}`)), http.StatusCreated)
	if b.Operation != ae.op || b.Attempt != att.Attempt || b.DecidedBy != ae.principalOf("h-recovery") || b.Role != "recovery-admin" ||
		b.Start != start || b.End != end {
		t.Fatalf("answered %+v", b)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM attempt_accounting c JOIN machine_event e ON e.machine = c.machine
			AND e.revision = c.revision AND e.kind = 'accounting'
		WHERE c.attempt = $1 AND c.basis = 'decision' AND c.decided_by = $2 AND c.role = 'recovery-admin' AND c.act = $3
			AND c.revision = $4 AND c.facts->>'explanation' = 'the abandoned apply landed' AND c.facts->>'judgement' = 'true'
			AND e.entry->>'decidedBy' = $2`, att.Attempt, b.DecidedBy, b.Act, b.Revision); n != 1 {
		t.Fatal("no decision accounting by the requester")
	}
	if n := ae.actRow(t, b.Act, "attempt.account", ae.op, att.Attempt, ae.machine); n != 1 {
		t.Fatal("no act of the request")
	}
	wantProblem(t, ae.post(next, "acc-3", path, body(`{"explanation":"again"}`)), http.StatusConflict, "conflict")
	wantProblem(t, ae.post(next, "acc-4", "/attempts/att_aaaaaaaaaaaaaaaaaaaaaaaaaa/accountings", body(`{}`)), http.StatusNotFound, "not-found")
	unknown := fmt.Sprintf(`{"start":%q,"end":"obs_aaaaaaaaaaaaaaaaaaaaaaaaaa"}`, start)
	wantProblem(t, ae.post(next, "acc-5", path, unknown), http.StatusNotFound, "not-found")
	wantProblem(t, ae.post(next, "acc-6", path, `{"start":"x","end":"y"}`), http.StatusBadRequest, "invalid-request")
}

// An attempt named under an operation it is not of is not found: the first operation's attempt,
// rejected, under the cluster's next operation.
func TestAccountingRequestOtherOperation(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	att := ae.sent(t)
	if _, err := ae.a.recordResponse(context.Background(), att.Attempt, answer(talos.ClassRejected)); err != nil {
		t.Fatal(err)
	}
	ae.second(t)
	ae.evidence(t, ae.plan2, nil)
	r, err := ae.a.commitPlan(context.Background(), ae.plan2)
	if err != nil {
		t.Fatal(err)
	}
	start := ae.observed(t, func(c *cobs) { c.purpose, c.shift = "recovery", settleWindow })
	end := ae.observed(t, func(c *cobs) { c.purpose, c.shift = "recovery", settleWindow+time.Minute })
	rec := ae.do(ae.a, call{method: "POST", path: prefix + "/operations/" + r.Operation + "/attempts/" + att.Attempt + "/accountings",
		token: ae.human("h-recovery"), key: "k-recovery-acc-other-0123456789",
		body: fmt.Sprintf(`{"start":%q,"end":%q}`, start, end)})
	wantProblem(t, rec, http.StatusNotFound, "not-found")
	if n := count(t, ae.db, `SELECT count(*) FROM act WHERE action = 'attempt.account'`); n != 0 {
		t.Fatal("a refused request recorded an act")
	}
}

// A resolution (execution-recovery.md §4): an unresolved operation that recorded no attempt becomes
// cancelled on a recorded reason, with one operation state entry that names the resolver and the
// act; any other state, an operation with an attempt and any other target are refused.
func TestResolutionRequest(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	next := observer(t, ae.env, &fakeExecutor{}, options{})
	// committed: not unresolved.
	wantProblem(t, ae.post(next, "res-1", "/resolutions", `{"to":"cancelled","reason":"nothing sent"}`), http.StatusConflict, "conflict")
	if _, err := next.takeOver(context.Background(), ae.op, 1, "controller-start"); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, ae.post(next, "res-2", "/resolutions", `{"to":"completed","reason":"it landed"}`), http.StatusBadRequest, "invalid-request")
	b := decode[resolutionBody](t, ae.post(next, "res-3", "/resolutions", `{"to":"cancelled","reason":"the plan was withdrawn"}`),
		http.StatusCreated)
	if b.Operation != ae.op || b.State != "cancelled" || b.ResolvedBy != ae.principalOf("h-recovery") || b.Role != "recovery-admin" {
		t.Fatalf("answered %+v", b)
	}
	if state, gen, owner := ae.state(t); state != "cancelled" || gen != 2 || owner != next.d.owner.ID {
		t.Fatalf("the operation is %s at generation %d owned by %s", state, gen, owner)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND revision = $2 AND kind = 'operation-state'
			AND entry->>'operation' = $3 AND entry->>'from' = 'unresolved' AND entry->>'to' = 'cancelled'
			AND entry->>'cause' = 'resolution' AND entry->>'resolvedBy' = $4 AND entry->>'role' = 'recovery-admin'
			AND entry->>'reason' = 'the plan was withdrawn' AND entry->>'act' = $5`,
		ae.machine, b.Revision, ae.op, b.ResolvedBy, b.Act); n != 1 {
		t.Fatal("no operation state entry naming the resolution")
	}
	if n := ae.actRow(t, b.Act, "operation.resolve", ae.op, ae.machine); n != 1 {
		t.Fatal("no act of the request")
	}
	wantProblem(t, ae.post(next, "res-4", "/resolutions", `{"to":"cancelled","reason":"again"}`), http.StatusConflict, "conflict")
	// An operation that ended takes no further takeover.
	wantProblem(t, ae.post(next, "res-5", "/takeovers", `{"generation":2,"reason":"too late"}`), http.StatusConflict, "conflict")
}

// An operation with a recorded attempt is never cancelled by a resolution: cancelled is not proof
// that nothing was sent (§4).
func TestResolutionRequestAttempted(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	att := ae.sent(t)
	if _, err := ae.a.recordResponse(context.Background(), att.Attempt, answer(talos.ClassTransport)); err != nil {
		t.Fatal(err)
	}
	next := ae.takenOverBy(t)
	wantProblem(t, ae.post(next, "res-att", "/resolutions", `{"to":"cancelled","reason":"nothing was sent"}`), http.StatusConflict, "conflict")
	if state, _, _ := ae.state(t); state != "unresolved" {
		t.Fatalf("the operation is %s", state)
	}
}
