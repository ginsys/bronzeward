package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

func (p *planEnv) cancel(token, k, plan, body string) *httptest.ResponseRecorder {
	return p.do(p.api, call{method: "POST", path: prefix + "/plans/" + plan + "/cancellations", token: token, key: k, body: body})
}

// commitAs records plan committed under its current approval, with an apply-config operation
// owned as execution-recovery §3.2 creates it, which no route makes yet.
func (p *planEnv) commitAs(t *testing.T, plan string) {
	t.Helper()
	op := id.New(id.Operation)
	tx, err := p.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO operation (id, kind, state, epoch, owner, owner_gen, owner_epoch, created_at, plan, machine, cluster)
		SELECT $1, 'apply-config', 'committed', epoch, 'test-owner', 1, epoch, now(), $2, $3, $4 FROM installation_state`,
		op, plan, p.machine, p.cluster); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE plan_state SET state = 'committed', operation = $2, revision = revision + 1 WHERE plan = $1`,
		plan, op); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// T11's plan cancellation (persistence-api.md §5, §9.2, §10.3; execution-recovery.md §2): the
// publisher that created a proposed plan cancels it with a reason, one plan-cancellation entry on
// the plan's machine's timeline under the current epoch; the plan reads cancelled at its next
// revision, and the act names the plan and the machine. A replay answers the same cancellation.
func TestPlanCancellation(t *testing.T) {
	t.Parallel()
	p := newPlanEnv(t)
	plan := decode[planBody](t, p.plan(p.robot, "k-plan-0123456789ab", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
	rec := p.cancel(p.robot, "k-cancel-0123456789ab", plan.ID, `{"reason":"superseded"}`)
	b := decode[cancellationBody](t, rec, http.StatusOK)
	if id.MustHave(b.Act, id.Act) != nil {
		t.Fatalf("act %q", b.Act)
	}
	ep := epoch(t, p.db)
	want := cancellationBody{Plan: plan.ID, Machine: p.machine, CancelledBy: p.robotID, Role: "publisher", Reason: "superseded",
		Act: b.Act, Epoch: ep, At: b.At}
	if !reflect.DeepEqual(b, want) {
		t.Fatalf("cancellation %+v, want %+v", b, want)
	}
	var (
		rev              int64
		entryAt, updated time.Time
		entry            string
	)
	if err := p.db.QueryRow(`SELECT c.revision, e.at, e.entry::text, s.updated_at
		FROM plan_cancellation c JOIN machine_event e ON e.machine = c.machine AND e.revision = c.revision AND e.kind = 'plan-cancellation'
			JOIN plan_state s ON s.plan = c.plan
		WHERE c.plan = $1 AND c.machine = $2 AND c.cancelled_by = $3 AND c.cancelled_by_kind = 'service' AND c.role = 'publisher'
			AND c.reason = 'superseded' AND c.act = $4 AND c.epoch = $5 AND c.at = $6 AND e.epoch = $5
			AND s.state = 'cancelled' AND s.reason = 'cancelled' AND s.approval IS NULL AND s.revision = 2`,
		plan.ID, p.machine, p.robotID, b.Act, ep, b.At).Scan(&rev, &entryAt, &entry, &updated); err != nil {
		t.Fatal(err)
	}
	if rev != plan.TimelineRevision+1 || !entryAt.Equal(b.At) || !updated.Equal(b.At) {
		t.Fatalf("cancellation revision %d after plan %d, entry at %s, state at %s, cancelled %s", rev, plan.TimelineRevision,
			entryAt, updated, b.At)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(entry), &got); err != nil {
		t.Fatal(err)
	}
	wantEntry := map[string]any{"plan": plan.ID, "principal": p.robotID, "role": "publisher", "reason": "superseded"}
	if !reflect.DeepEqual(got, wantEntry) {
		t.Fatalf("timeline entry %v, want %v", got, wantEntry)
	}
	if n := count(t, p.db, `SELECT count(*) FROM act WHERE id = $1 AND action = 'plan.cancel' AND principal = $2 AND role = 'publisher'
		AND subjects = ARRAY[$3, $4]`, b.Act, p.robotID, plan.ID, p.machine); n != 1 {
		t.Fatalf("%d cancellation acts", n)
	}
	read := decode[planBody](t, p.do(p.api, call{method: "GET", path: prefix + "/plans/" + plan.ID, token: p.human("h-viewer")}),
		http.StatusOK)
	if read.State != "cancelled" || read.Approval != nil || read.Revision != 2 {
		t.Fatalf("plan after cancellation: state %q, approval %v, revision %d", read.State, read.Approval, read.Revision)
	}
	again := decode[cancellationBody](t, p.cancel(p.robot, "k-cancel-0123456789ab", plan.ID, `{"reason":"superseded"}`), http.StatusOK)
	if !reflect.DeepEqual(again, b) {
		t.Fatalf("replay %+v, want %+v", again, b)
	}
}

// wantCancelled fails unless plan has one cancellation by principal under role and its stored
// state is state at revision rev.
func (p *planEnv) wantCancelled(t *testing.T, plan, principal, role, state string, rev int) {
	t.Helper()
	if n := count(t, p.db, `SELECT count(*) FROM plan_cancellation WHERE plan = $1 AND cancelled_by = $2 AND role = $3`,
		plan, principal, role); n != 1 {
		t.Fatalf("plan %s: no cancellation by %s as %s", plan, principal, role)
	}
	if n := count(t, p.db, `SELECT count(*) FROM plan_state WHERE plan = $1 AND state = $2 AND revision = $3`, plan, state, rev); n != 1 {
		t.Fatalf("plan %s is not %s at revision %d", plan, state, rev)
	}
}

// §10.3, choice §17.20: a cancellation's role is the first qualifying one in the route's order,
// where publisher qualifies only for the plan's creator. An approver cancels an approved plan,
// which keeps nothing of its approval; a recovery admin cancels another's plan; a human holding
// every role cancels its own plan as publisher and another's as approver; a committed plan's
// cancellation is recorded and the plan stays committed (execution-recovery.md §2).
func TestPlanCancellationRoles(t *testing.T) {
	t.Parallel()
	p := newPlanEnv(t)
	plan := func(creator, k string) string {
		return decode[planBody](t, p.plan(creator, k, applyBody(p.target.rel, p.machine, "")), http.StatusCreated).ID
	}
	approver, all := p.human("h-approver"), p.human("h-all")
	reason := `{"reason":"superseded"}`

	approved := plan(p.robot, "k-plan-approved-012345")
	decode[approvalBody](t, p.approve(approver, "k-approve-approved-012", approved), http.StatusCreated)
	decode[cancellationBody](t, p.cancel(approver, "k-cancel-approved-0123", approved, reason), http.StatusOK)
	p.wantCancelled(t, approved, p.principalOf("h-approver"), "approver", "cancelled", 3)
	if n := count(t, p.db, `SELECT count(*) FROM plan_state WHERE plan = $1 AND approval IS NULL AND reason = 'cancelled'`, approved); n != 1 {
		t.Fatal("the cancelled plan still names its approval")
	}

	recovered := plan(p.robot, "k-plan-recovered-01234")
	decode[cancellationBody](t, p.cancel(p.human("h-recovery"), "k-cancel-recovered-012", recovered, reason), http.StatusOK)
	p.wantCancelled(t, recovered, p.principalOf("h-recovery"), "recovery-admin", "cancelled", 2)

	own := plan(all, "k-plan-own-0123456789a")
	decode[cancellationBody](t, p.cancel(all, "k-cancel-own-012345678", own, reason), http.StatusOK)
	p.wantCancelled(t, own, p.principalOf("h-all"), "publisher", "cancelled", 2)
	others := plan(p.robot, "k-plan-others-01234567")
	b := decode[cancellationBody](t, p.cancel(all, "k-cancel-others-012345", others, reason), http.StatusOK)
	if b.Role != "approver" {
		t.Fatalf("a cancellation of another's plan by every role answered role %q", b.Role)
	}
	p.wantCancelled(t, others, p.principalOf("h-all"), "approver", "cancelled", 2)
	if n := count(t, p.db, `SELECT count(*) FROM act WHERE action = 'plan.cancel' AND principal = $1 AND role = 'approver'
		AND subjects[1] = $2`, p.principalOf("h-all"), others); n != 1 {
		t.Fatalf("%d approver acts for the cancellation of another's plan", n)
	}

	committed := plan(p.robot, "k-plan-committed-01234")
	decode[approvalBody](t, p.approve(approver, "k-approve-committed-01", committed), http.StatusCreated)
	p.commitAs(t, committed)
	decode[cancellationBody](t, p.cancel(p.robot, "k-cancel-committed-012", committed, reason), http.StatusOK)
	p.wantCancelled(t, committed, p.robotID, "publisher", "committed", 3)
	// A committed plan keeps its state, so a second cancellation is refused for the one recorded.
	if doc := wantProblem(t, p.cancel(approver, "k-cancel-committed-two", committed, reason), http.StatusConflict,
		"conflict"); doc["plan"] != committed || doc["state"] != "committed" {
		t.Fatalf("a second cancellation of a committed plan refused with %v", doc)
	}
	p.wantCancelled(t, committed, p.robotID, "publisher", "committed", 3)
}

// A cancellation's refusals (§9.4): a plan that does not exist; a reason missing, blank or longer
// than 1024 bytes; a publisher that did not create the plan, and a role that cannot cancel; a
// plan that reads revoked, cancelled or expired, named as a read names it. None writes anything.
func TestPlanCancellationRefusals(t *testing.T) {
	t.Parallel()
	p := newPlanEnv(t)
	plan := func(k, extra string) planBody {
		return decode[planBody](t, p.plan(p.robot, k, applyBody(p.target.rel, p.machine, extra)), http.StatusCreated)
	}
	approver := p.human("h-approver")
	reason := `{"reason":"superseded"}`
	open := plan("k-plan-open-0123456789", "").ID
	wantProblem(t, p.cancel(p.robot, "k-cancel-unknown-01234", id.New(id.Plan), reason), http.StatusNotFound, "not-found")
	wantProblem(t, p.cancel(p.robot, "k-cancel-malformed-012", "pln_x", reason), http.StatusNotFound, "not-found")
	for i, body := range []string{`{}`, `{"reason":""}`, `{"reason":"  "}`, `{"reason":"` + strings.Repeat("x", 1025) + `"}`} {
		wantProblem(t, p.cancel(p.robot, fmt.Sprintf("k-cancel-body-%07d", i), open, body), http.StatusBadRequest, "invalid-request")
	}
	if doc := wantProblem(t, p.cancel(p.human("h-publisher"), "k-cancel-other-012345", open, reason), http.StatusForbidden,
		"forbidden"); doc["plan"] != open {
		t.Fatalf("another publisher's cancellation refused with %v", doc)
	}
	for _, who := range []string{"h-author", "h-viewer"} {
		wantProblem(t, p.cancel(p.human(who), "k-cancel-role-0123456", open, reason), http.StatusForbidden, "forbidden")
	}
	if n := count(t, p.db, `SELECT count(*) FROM plan_state WHERE plan = $1 AND state = 'proposed' AND revision = 1`, open); n != 1 {
		t.Fatal("a refused cancellation changed the plan's state")
	}

	cancelled := plan("k-plan-cancelled-012345", "").ID
	decode[cancellationBody](t, p.cancel(p.robot, "k-cancel-first-0123456", cancelled, reason), http.StatusOK)
	revoked := plan("k-plan-revoked-01234567", "").ID
	apr := decode[approvalBody](t, p.approve(approver, "k-approve-revoked-0123", revoked), http.StatusCreated)
	decode[approvalRevocationBody](t, p.revokeApproval(approver, "k-revoke-0123456789ab", apr.ID, reason), http.StatusCreated)
	expired := plan("k-plan-expired-0123456", `,"expiresInSeconds":1`)
	time.Sleep(time.Until(expired.ExpiresAt.Add(100 * time.Millisecond)))
	for _, c := range []struct{ plan, state, k string }{
		{cancelled, "cancelled", "k-cancel-cancelled-012"}, {revoked, "revoked", "k-cancel-revoked-01234"},
		{expired.ID, "expired", "k-cancel-expired-01234"},
	} {
		if doc := wantProblem(t, p.cancel(approver, c.k, c.plan, reason), http.StatusConflict, "conflict"); doc["state"] != c.state ||
			doc["plan"] != c.plan {
			t.Fatalf("cancellation of a %s plan refused with %v", c.state, doc)
		}
	}
	if n := count(t, p.db, `SELECT count(*) FROM plan_cancellation`); n != 1 {
		t.Fatalf("%d cancellations after refusals", n)
	}
	if n := count(t, p.db, `SELECT count(*) FROM plan_state WHERE plan = $1 AND state = 'proposed'`, expired.ID); n != 1 {
		t.Fatal("the refused cancellation wrote the expired plan's state")
	}
}

// Rule 4: a cancellation's time, and the expiry it is judged against, follow the act-order wait.
// One queued there past its plan's expiry is refused naming the plan expired and writes nothing.
func TestPlanCancellationAfterActOrderWait(t *testing.T) {
	t.Parallel()
	p := newPlanEnv(t)
	plan := decode[planBody](t, p.plan(p.robot, "k-plan-late-0123456789", applyBody(p.target.rel, p.machine, `,"expiresInSeconds":1`)),
		http.StatusCreated)
	lock := holdActOrder(t, p.db)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- p.cancel(p.robot, "k-cancel-late-0123456", plan.ID, `{"reason":"superseded"}`) }()
	waitForLockWaits(t, p.db, 1)
	time.Sleep(time.Until(plan.ExpiresAt.Add(200 * time.Millisecond)))
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	if doc := wantProblem(t, <-done, http.StatusConflict, "conflict"); doc["state"] != "expired" {
		t.Fatalf("cancellation queued past expiry refused with %v", doc)
	}
	if n := count(t, p.db, `SELECT count(*) FROM plan_cancellation`); n != 0 {
		t.Fatalf("%d cancellations after a refusal", n)
	}
}

// Rule 2: a cancelling identity revoked while the cancellation waits on the machine's lock is
// refused once the cancellation holds its principal, and nothing is written.
func TestPlanCancellationCancellerRevokedInWait(t *testing.T) {
	t.Parallel()
	p := newPlanEnv(t)
	plan := decode[planBody](t, p.plan(p.robot, "k-plan-0123456789ab", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
	canceller, recovery := p.human("h-all"), p.human("h-recovery")
	lock, err := p.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback() }()
	if _, err := lock.Exec(`SELECT 1 FROM machine WHERE id = $1 FOR UPDATE`, p.machine); err != nil {
		t.Fatal(err)
	}
	// T5c locks every machine row too, so the revocation queues on the row first and the
	// cancellation behind it; row-lock waiters are granted in order.
	revoked := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		revoked <- revoke(p.env, recovery, key, `{"identity":"`+p.principalOf("h-all")+`","reason":"left"}`)
	}()
	waitForLockWaits(t, p.db, 1)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- p.cancel(canceller, "k-cancel-0123456789ab", plan.ID, `{"reason":"wrong release"}`) }()
	waitForLockWaits(t, p.db, 2)
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	revocationOf(t, <-revoked)
	wantProblem(t, <-done, http.StatusForbidden, "identity-revoked")
	if n := count(t, p.db, `SELECT count(*) FROM plan_cancellation`); n != 0 {
		t.Fatalf("%d cancellations by a revoked identity", n)
	}
}
