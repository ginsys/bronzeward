package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

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
