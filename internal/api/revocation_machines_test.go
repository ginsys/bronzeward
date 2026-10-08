package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

// wantIdentityEntries checks the identity revocation entries on machine: none when plans is empty,
// else one naming the identity, plans in id order, the revoking human and its role and reason, at
// the revocation's time, revision and epoch (PA §5 T5c; execution-recovery.md §4.1).
func (p *planEnv) wantIdentityEntries(t *testing.T, machine, identity, revoker string, plans ...string) {
	t.Helper()
	slices.Sort(plans)
	want := 0
	if len(plans) > 0 {
		want = 1
	}
	named, err := json.Marshal(plans)
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, p.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'identity-revocation'`, machine); n != want {
		t.Fatalf("%d identity revocation entries on %s, want %d", n, machine, want)
	}
	if want == 0 {
		return
	}
	if n := count(t, p.db, `SELECT count(*) FROM machine_event v JOIN identity_revocation r ON r.identity = $2
		WHERE v.machine = $1 AND v.kind = 'identity-revocation' AND v.at = r.at AND v.epoch = r.epoch
		AND v.revision = (SELECT revision_counter FROM machine WHERE id = $1)
		AND v.entry = jsonb_build_object('identity', $2::text, 'plans', $3::jsonb, 'principal', $4::text,
			'role', 'recovery-admin', 'reason', 'left')`, machine, identity, string(named), revoker); n != 1 {
		var got string
		_ = p.db.QueryRow(`SELECT entry::text FROM machine_event WHERE machine = $1 AND kind = 'identity-revocation'`, machine).Scan(&got)
		t.Fatalf("identity revocation entry on %s is %s, want plans %s by %s", machine, got, named, revoker)
	}
}

// T5c's machine part: an identity revocation appends one entry to the timeline of each machine
// with a plan that identity approved whose plan or operation is not terminal, naming those plans,
// and none to a machine without one (PA §5 T5c). An approved plan past its expiry, a committed
// one whose operation completed, a cancelled one and one another identity approved are not named.
// A replay writes nothing more.
func TestIdentityRevocationMachineEntries(t *testing.T) {
	t.Parallel()
	p := newPlanEnv(t)
	other := decode[machineBody](t, p.do(p.api, machineCall(p.human("h-author"), "k-machine-other-012345", p.cluster,
		"0b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5c")), http.StatusCreated).ID
	approver, all := p.human("h-approver"), p.human("h-all")
	plan := func(k, extra string) planBody {
		return decode[planBody](t, p.plan(p.robot, k, applyBody(p.target.rel, p.machine, extra)), http.StatusCreated)
	}
	reason := `{"reason":"superseded"}`
	live := plan("k-plan-live-0123456789", "").ID
	decode[approvalBody](t, p.approve(approver, "k-approve-live-0123456", live), http.StatusCreated)
	completed := plan("k-plan-completed-01234", "").ID
	decode[approvalBody](t, p.approve(approver, "k-approve-completed-01", completed), http.StatusCreated)
	p.commitAs(t, completed)
	mustExec(t, p.db, `UPDATE operation SET state = 'completed' WHERE plan = $1`, completed)
	committed := plan("k-plan-committed-01234", "").ID
	decode[approvalBody](t, p.approve(approver, "k-approve-committed-01", committed), http.StatusCreated)
	p.commitAs(t, committed)
	cancelled := plan("k-plan-cancelled-012345", "").ID
	decode[approvalBody](t, p.approve(approver, "k-approve-cancelled-01", cancelled), http.StatusCreated)
	decode[cancellationBody](t, p.cancel(p.robot, "k-cancel-cancelled-012", cancelled, reason), http.StatusOK)
	others := plan("k-plan-others-01234567", "").ID
	decode[approvalBody](t, p.approve(all, "k-approve-others-01234", others), http.StatusCreated)
	expired := plan("k-plan-expired-0123456", `,"expiresInSeconds":2`)
	decode[approvalBody](t, p.approve(approver, "k-approve-expired-0123", expired.ID), http.StatusCreated)
	time.Sleep(time.Until(expired.ExpiresAt.Add(100 * time.Millisecond)))

	recovery, revokedID := p.human("h-recovery"), p.principalOf("h-approver")
	body := `{"identity":"` + revokedID + `","reason":"left"}`
	first := revocationOf(t, revoke(p.env, recovery, "k-revoke-machines-0123", body))
	p.wantIdentityEntries(t, p.machine, revokedID, first.RevokedBy, live, committed)
	p.wantIdentityEntries(t, other, revokedID, first.RevokedBy)
	if replay := revocationOf(t, revoke(p.env, recovery, "k-revoke-machines-0123", body)); replay != first {
		t.Fatalf("replay answered %+v, want %+v", replay, first)
	}
	p.wantIdentityEntries(t, p.machine, revokedID, first.RevokedBy, live, committed)

	// h-all approved only the plan cancelled below: no entry.
	decode[cancellationBody](t, p.cancel(p.robot, "k-cancel-others-012345", others, reason), http.StatusOK)
	allID := p.principalOf("h-all")
	revocationOf(t, revoke(p.env, recovery, "k-revoke-all-012345678", `{"identity":"`+allID+`","reason":"left"}`))
	if n := count(t, p.db, `SELECT count(*) FROM machine_event WHERE kind = 'identity-revocation' AND entry->>'identity' = $1`,
		allID); n != 0 {
		t.Fatalf("%d identity revocation entries for an identity whose plans are all terminal", n)
	}
}

// T5c takes the installation state FOR UPDATE before the machine rows, so no machine is inserted
// between its machine locks and its commit: an inventory started while it waits on a machine row
// waits for it, rather than adding a machine whose plans the revocation reads but never locked.
func TestIdentityRevocationHoldsInventory(t *testing.T) {
	t.Parallel()
	p := newPlanEnv(t)
	hold, err := p.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback() }()
	if _, err := hold.Exec(`SELECT 1 FROM machine WHERE id = $1 FOR UPDATE`, p.machine); err != nil {
		t.Fatal(err)
	}
	recovery, author := p.human("h-recovery"), p.human("h-author")
	revoked := make(chan revocationBody, 1)
	go func() {
		revoked <- revocationOf(t, revoke(p.env, recovery, "k-revoke-inventory-012",
			`{"identity":"`+p.principalOf("h-approver")+`","reason":"left"}`))
	}()
	waitForLockWaits(t, p.db, 1)
	inventoried := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		inventoried <- p.do(p.api, machineCall(author, "k-machine-during-0123", p.cluster, "0b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5d"))
	}()
	waitForLockWaits(t, p.db, 2)
	if err := hold.Rollback(); err != nil {
		t.Fatal(err)
	}
	<-revoked
	decode[machineBody](t, <-inventoried, http.StatusCreated)
}

// T5c reads which plans an entry names under the machine locks: a revocation queued behind a
// machine row whose holder then cancels a plan the identity approved names only what is still
// live once it holds the row (PA §5 T5c, rule 5).
func TestIdentityRevocationMachineEntriesAfterLockWait(t *testing.T) {
	t.Parallel()
	p := newPlanEnv(t)
	approver := p.human("h-approver")
	plan := func(k string) string {
		return decode[planBody](t, p.plan(p.robot, k, applyBody(p.target.rel, p.machine, "")), http.StatusCreated).ID
	}
	live := plan("k-plan-live-0123456789")
	decode[approvalBody](t, p.approve(approver, "k-approve-live-0123456", live), http.StatusCreated)
	committed := plan("k-plan-committed-01234")
	decode[approvalBody](t, p.approve(approver, "k-approve-committed-01", committed), http.StatusCreated)
	p.commitAs(t, committed)

	hold, err := p.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback() }()
	if _, err := hold.Exec(`SELECT 1 FROM machine WHERE id = $1 FOR UPDATE`, p.machine); err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(`UPDATE plan_state SET state = 'cancelled', reason = 'cancelled', approval = NULL,
		revision = revision + 1 WHERE plan = $1`, live); err != nil {
		t.Fatal(err)
	}
	recovery, revokedID := p.human("h-recovery"), p.principalOf("h-approver")
	done := make(chan revocationBody, 1)
	go func() {
		done <- revocationOf(t, revoke(p.env, recovery, "k-revoke-held-01234567", `{"identity":"`+revokedID+`","reason":"left"}`))
	}()
	waitForLockWaits(t, p.db, 1)
	if err := hold.Commit(); err != nil {
		t.Fatal(err)
	}
	b := <-done
	p.wantIdentityEntries(t, p.machine, revokedID, b.RevokedBy, committed)
}
