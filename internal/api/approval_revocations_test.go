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

	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
)

func (p *planEnv) revokeApproval(token, k, approval, body string) *httptest.ResponseRecorder {
	return p.do(p.api, call{method: "POST", path: prefix + "/approvals/" + approval + "/revocations", token: token, key: k, body: body})
}

// T5b (persistence-api.md §5, §9.2; execution-recovery.md §2): a recovery admin's revocation of
// the approval authorizing an approved plan is one approval-revocation entry on the plan's
// machine's timeline, recorded with its reason under the current epoch; the plan reads revoked,
// still naming the approval, at its next revision, and the act names the approval, the plan and
// the machine. A replay answers the same revocation.
func TestApprovalRevocation(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	plan := decode[planBody](t, p.plan(p.robot, "k-plan-0123456789ab", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
	apr := decode[approvalBody](t, p.approve(p.human("h-approver"), "k-approve-0123456789", plan.ID), http.StatusCreated)
	bearer := p.human("h-recovery")
	rec := p.revokeApproval(bearer, "k-revoke-0123456789ab", apr.ID, `{"reason":"wrong release"}`)
	b := decode[approvalRevocationBody](t, rec, http.StatusCreated)
	if loc := rec.Header().Get("Location"); loc != "" || id.MustHave(b.Act, id.Act) != nil {
		t.Fatalf("Location %q, act %q", loc, b.Act)
	}
	recovery, ep := p.principalOf("h-recovery"), epoch(t, p.db)
	want := approvalRevocationBody{Approval: apr.ID, Plan: plan.ID, Machine: p.machine, RevokedBy: recovery, Role: "recovery-admin",
		Reason: "wrong release", Act: b.Act, Epoch: ep, At: b.At}
	if !reflect.DeepEqual(b, want) {
		t.Fatalf("revocation %+v, want %+v", b, want)
	}
	var (
		rev, approved    int64
		entryAt, updated time.Time
		entry            string
	)
	if err := p.db.QueryRow(`SELECT r.revision, a.revision, e.at, e.entry::text, s.updated_at
		FROM approval_revocation r JOIN approval a ON a.id = r.approval
			JOIN machine_event e ON e.machine = r.machine AND e.revision = r.revision AND e.kind = 'approval-revocation'
			JOIN plan_state s ON s.plan = r.plan
		WHERE r.approval = $1 AND r.plan = $2 AND r.machine = $3 AND r.revoked_by = $4 AND r.role = 'recovery-admin'
			AND r.reason = 'wrong release' AND r.act = $5 AND r.epoch = $6 AND r.at = $7 AND e.epoch = $6
			AND s.state = 'revoked' AND s.reason = 'approval-revoked' AND s.approval = $1 AND s.revision = 3`,
		apr.ID, plan.ID, p.machine, recovery, b.Act, ep, b.At).Scan(&rev, &approved, &entryAt, &entry, &updated); err != nil {
		t.Fatal(err)
	}
	if rev != approved+1 || !entryAt.Equal(b.At) || !updated.Equal(b.At) {
		t.Fatalf("revocation revision %d after approval %d, entry at %s, state at %s, revoked %s", rev, approved, entryAt, updated, b.At)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(entry), &got); err != nil {
		t.Fatal(err)
	}
	wantEntry := map[string]any{"approval": apr.ID, "plan": plan.ID, "principal": recovery, "role": "recovery-admin",
		"reason": "wrong release"}
	if !reflect.DeepEqual(got, wantEntry) {
		t.Fatalf("timeline entry %v, want %v", got, wantEntry)
	}
	if n := count(t, p.db, `SELECT count(*) FROM act WHERE id = $1 AND action = 'approval.revoke' AND principal = $2
		AND role = 'recovery-admin' AND subjects = ARRAY[$3, $4, $5]`, b.Act, recovery, apr.ID, plan.ID, p.machine); n != 1 {
		t.Fatalf("%d revocation acts", n)
	}
	read := decode[planBody](t, p.do(p.api, call{method: "GET", path: prefix + "/plans/" + plan.ID, token: p.human("h-viewer")}),
		http.StatusOK)
	if read.State != "revoked" || read.Approval == nil || *read.Approval != apr.ID || read.Revision != 3 {
		t.Fatalf("plan after revocation: state %q, approval %v, revision %d", read.State, read.Approval, read.Revision)
	}
	again := decode[approvalRevocationBody](t, p.revokeApproval(bearer, "k-revoke-0123456789ab", apr.ID, `{"reason":"wrong release"}`),
		http.StatusCreated)
	if !reflect.DeepEqual(again, b) {
		t.Fatalf("replay %+v, want %+v", again, b)
	}
}

// wantRevocation fails unless approval has one revocation and its plan's stored state is still
// state, naming approval, at revision rev.
func (p *planEnv) wantRevocation(t *testing.T, approval, plan, state, named string, rev int) {
	t.Helper()
	if n := count(t, p.db, `SELECT count(*) FROM approval_revocation WHERE approval = $1`, approval); n != 1 {
		t.Fatalf("%d revocations of %s", n, approval)
	}
	if n := count(t, p.db, `SELECT count(*) FROM plan_state WHERE plan = $1 AND state = $2 AND approval IS NOT DISTINCT FROM
		NULLIF($3, '') AND revision = $4`, plan, state, named, rev); n != 1 {
		t.Fatalf("plan %s is no longer %s naming %q at revision %d", plan, state, named, rev)
	}
}

// T5b's refusals (§9.4): an approval that does not exist; a reason missing, blank or longer than
// 1024 bytes; a role other than approver or recovery admin; a second revocation of one approval.
// None writes anything.
func TestApprovalRevocationRefusals(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	approver := p.human("h-approver")
	plan := decode[planBody](t, p.plan(p.robot, "k-plan-0123456789ab", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
	apr := decode[approvalBody](t, p.approve(approver, "k-approve-0123456789", plan.ID), http.StatusCreated)
	reason := `{"reason":"wrong release"}`
	wantProblem(t, p.revokeApproval(approver, "k-revoke-unknown-0123", id.New(id.Approval), reason), http.StatusNotFound, "not-found")
	wantProblem(t, p.revokeApproval(approver, "k-revoke-malformed-01", plan.ID, reason), http.StatusNotFound, "not-found")
	for i, body := range []string{`{}`, `{"reason":" \t"}`, `{"reason":"` + strings.Repeat("x", 1025) + `"}`, `{"reason":"x","why":"y"}`} {
		wantProblem(t, p.revokeApproval(approver, fmt.Sprintf("k-revoke-body-%07d", i), apr.ID, body), http.StatusBadRequest,
			"invalid-request")
	}
	for _, who := range []string{"h-author", "h-publisher", "h-viewer"} {
		wantProblem(t, p.revokeApproval(p.human(who), "k-revoke-role-012345", apr.ID, reason), http.StatusForbidden, "forbidden")
	}
	wantProblem(t, p.revokeApproval(p.robot, "k-revoke-robot-01234", apr.ID, reason), http.StatusForbidden, "forbidden")
	if n := count(t, p.db, `SELECT count(*) FROM approval_revocation`); n != 0 {
		t.Fatalf("%d revocations after refusals", n)
	}

	decode[approvalRevocationBody](t, p.revokeApproval(approver, "k-revoke-first-01234", apr.ID, reason), http.StatusCreated)
	doc := wantProblem(t, p.revokeApproval(p.human("h-recovery"), "k-revoke-second-0123", apr.ID, reason), http.StatusConflict,
		"conflict")
	if doc["approval"] != apr.ID {
		t.Fatalf("second revocation refused with %v", doc)
	}
	p.wantRevocation(t, apr.ID, plan.ID, "revoked", apr.ID, 3)
	if n := count(t, p.db, `SELECT count(*) FROM act WHERE action = 'approval.revoke'`); n != 1 {
		t.Fatalf("%d revocation acts", n)
	}
}

// T5b records a revocation of any approval (execution-recovery.md §2) but revokes the plan only
// while the approval authorizes it: an approval of an earlier epoch that a current one replaced,
// a plan already cancelled, a plan whose approver's identity is revoked and a plan past its expiry
// each keep their state.
func TestApprovalRevocationKeepsState(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	plan := func(k, extra string) planBody {
		return decode[planBody](t, p.plan(p.robot, k, applyBody(p.target.rel, p.machine, extra)), http.StatusCreated)
	}
	approver, recovery := p.human("h-approver"), p.human("h-recovery")
	reason := `{"reason":"wrong release"}`
	revoked := func(k, approval string) {
		t.Helper()
		decode[approvalRevocationBody](t, p.revokeApproval(recovery, k, approval, reason), http.StatusCreated)
	}

	replaced := plan("k-plan-replaced-0123456", "").ID
	earlier := decode[approvalBody](t, p.approve(approver, "k-approve-earlier-01234", replaced), http.StatusCreated)
	cancelled := plan("k-plan-cancelled-012345", "").ID
	before := decode[approvalBody](t, p.approve(approver, "k-approve-cancelled-01", cancelled), http.StatusCreated)
	gone := p.principalOf("h-approver-gone")
	orphan := plan("k-plan-orphan-01234567", "").ID
	p.approveAs(t, orphan, gone)
	lapsed := plan("k-plan-lapsed-01234567", `,"expiresInSeconds":1`)
	late := decode[approvalBody](t, p.approve(approver, "k-approve-lapsed-01234", lapsed.ID), http.StatusCreated)

	newEpoch(t, p.db)
	current := decode[approvalBody](t, p.approve(approver, "k-approve-current-01234", replaced), http.StatusCreated)
	revoked("k-revoke-replaced-0123", earlier.ID)
	p.wantRevocation(t, earlier.ID, replaced, "approved", current.ID, 3)

	mustExec(t, p.db, `UPDATE plan_state SET state = 'cancelled', reason = 'cancelled', approval = NULL, revision = 3 WHERE plan = $1`,
		cancelled)
	revoked("k-revoke-cancelled-012", before.ID)
	p.wantRevocation(t, before.ID, cancelled, "cancelled", "", 3)

	revocationOf(t, revoke(p.env, recovery, key, `{"identity":"`+gone+`","reason":"left"}`))
	var orphaned string
	if err := p.db.QueryRow(`SELECT approval FROM plan_state WHERE plan = $1`, orphan).Scan(&orphaned); err != nil {
		t.Fatal(err)
	}
	revoked("k-revoke-orphan-012345", orphaned)
	p.wantRevocation(t, orphaned, orphan, "approved", orphaned, 2)

	time.Sleep(time.Until(lapsed.ExpiresAt.Add(100 * time.Millisecond)))
	revoked("k-revoke-lapsed-012345", late.ID)
	p.wantRevocation(t, late.ID, lapsed.ID, "approved", late.ID, 2)
	read := decode[planBody](t, p.do(p.api, call{method: "GET", path: prefix + "/plans/" + lapsed.ID, token: p.human("h-viewer")}),
		http.StatusOK)
	if read.State != "expired" {
		t.Fatalf("lapsed plan reads %q after its approval's revocation", read.State)
	}
}

// Rule 4: whether the approval still authorizes the plan is judged after the act-order wait. A
// revocation queued there past the plan's expiry is recorded and the plan keeps its state, read
// expired.
func TestApprovalRevocationAfterActOrderWait(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	plan := decode[planBody](t, p.plan(p.robot, "k-plan-late-0123456789", applyBody(p.target.rel, p.machine, `,"expiresInSeconds":1`)),
		http.StatusCreated)
	apr := decode[approvalBody](t, p.approve(p.human("h-approver"), "k-approve-0123456789", plan.ID), http.StatusCreated)
	lock := holdActOrder(t, p.db)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- p.revokeApproval(p.human("h-recovery"), "k-revoke-late-0123456", apr.ID, `{"reason":"wrong release"}`)
	}()
	dbtest.WaitForLockWait(t, p.db)
	time.Sleep(time.Until(plan.ExpiresAt.Add(200 * time.Millisecond)))
	released := time.Now()
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	b := decode[approvalRevocationBody](t, <-done, http.StatusCreated)
	if !b.At.After(released) {
		t.Fatalf("revocation at %s, lock released %s", b.At, released)
	}
	p.wantRevocation(t, apr.ID, plan.ID, "approved", apr.ID, 2)
}

// T5b takes the approval FOR UPDATE (PA §5), so it waits for a commitment or attempt holding the
// approval FOR SHARE (§1.2 item 3) and is recorded once that ends.
func TestApprovalRevocationWaitsForApproval(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	plan := decode[planBody](t, p.plan(p.robot, "k-plan-0123456789ab", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
	apr := decode[approvalBody](t, p.approve(p.human("h-approver"), "k-approve-0123456789", plan.ID), http.StatusCreated)
	lock, err := p.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback() }()
	if _, err := lock.Exec(`SELECT 1 FROM approval WHERE id = $1 FOR SHARE`, apr.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- p.revokeApproval(p.human("h-recovery"), "k-revoke-0123456789ab", apr.ID, `{"reason":"wrong release"}`)
	}()
	dbtest.WaitForLockWait(t, p.db)
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	decode[approvalRevocationBody](t, <-done, http.StatusCreated)
	p.wantRevocation(t, apr.ID, plan.ID, "revoked", apr.ID, 3)
}

// Rule 2: a revoking human revoked while the revocation waits on the machine's lock is refused
// once the revocation holds its principal, and nothing is written.
func TestApprovalRevocationRevokerRevokedInWait(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	plan := decode[planBody](t, p.plan(p.robot, "k-plan-0123456789ab", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
	apr := decode[approvalBody](t, p.approve(p.human("h-approver"), "k-approve-0123456789", plan.ID), http.StatusCreated)
	revoker, recovery := p.human("h-all"), p.human("h-recovery")
	lock, err := p.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback() }()
	if _, err := lock.Exec(`SELECT 1 FROM machine WHERE id = $1 FOR UPDATE`, p.machine); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- p.revokeApproval(revoker, "k-revoke-0123456789ab", apr.ID, `{"reason":"wrong release"}`)
	}()
	dbtest.WaitForLockWait(t, p.db)
	revocationOf(t, revoke(p.env, recovery, key, `{"identity":"`+p.principalOf("h-all")+`","reason":"left"}`))
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, <-done, http.StatusForbidden, "identity-revoked")
	if n := count(t, p.db, `SELECT count(*) FROM approval_revocation`); n != 0 {
		t.Fatalf("%d revocations by a revoked identity", n)
	}
}
