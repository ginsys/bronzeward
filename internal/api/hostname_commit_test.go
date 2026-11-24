package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The hostname gate at the commitment (execution-recovery.md §3.2 comparison 6a, persistence-api.md §5
// rule 5): an apply-config plan whose release gives the machine a Kubernetes node name another machine's
// Applied release holds is refused and left approved, unless the machine's own Applied release already
// holds that name. The names here are synthetic.

// hostnameCommit is a commitEnv whose approved apply-config plan is ready to commit, whose machine 2 has
// the target release as its Applied release, and whose node names are the records given: machine 1's own
// Applied record, machine 1's record in the target release, and machine 2's (the holder's). The holder is
// named name, or has no name for "".
func hostnameCommit(t *testing.T, appliedOwn, target, holder nodeRecord, name string) *commitEnv {
	t.Helper()
	ce := newCommitEnv(t, "")
	ce.setNode(ce.applied.rel, ce.machine, appliedOwn.recorded, appliedOwn.name)
	ce.setNode(ce.target.rel, ce.machine, target.recorded, target.name)
	ce.setNode(ce.target.rel, ce.target.machine2, holder.recorded, holder.name)
	mustExec(t, ce.db, `UPDATE machine_state SET applied_release = $2, applied_digest = $3, applied_source = 'adoption',
		baseline_revision = 1 WHERE machine = $1`, ce.target.machine2, ce.target.rel, make([]byte, 32))
	mustExec(t, ce.db, `UPDATE machine SET name = NULLIF($2, '') WHERE id = $1`, ce.target.machine2, name)
	ce.ready(t)
	return ce
}

// wantStillApproved holds the plan approved and unreferenced by any operation.
func (ce *commitEnv) wantStillApproved(t *testing.T, plan string) {
	t.Helper()
	if n := count(t, ce.db, `SELECT count(*) FROM plan_state WHERE plan = $1 AND state = 'approved'`, plan); n != 1 {
		t.Fatalf("plan %s is not approved", plan)
	}
}

const clashCause = " holds the Kubernetes node name this release gives the machine"

// The comparison's cases: a refusal naming the other machine by its name, or by its identifier where it
// has none; the machine's own Applied holding the name is not a clash; a name that is null or not
// recorded on either side is not compared. A refused plan stays approved and its entry names no node
// name.
func TestCommitmentHostnameGate(t *testing.T) {
	t.Parallel()
	a, b := "node-a", "node-b"
	for _, c := range []struct {
		label                  string
		appliedOwn, target, by nodeRecord
		name                   string // the holder's name, "" for none
		held                   bool
	}{
		{"a named holder", nodeRecord{}, nodeRecord{true, &a}, nodeRecord{true, &a}, "web-9", true},
		{"a holder with no name", nodeRecord{}, nodeRecord{true, &a}, nodeRecord{true, &a}, "", true},
		{"this machine's own Applied holds the name", nodeRecord{true, &a}, nodeRecord{true, &a}, nodeRecord{true, &a}, "web-9", false},
		{"this machine's own Applied holds another name", nodeRecord{true, &b}, nodeRecord{true, &a}, nodeRecord{true, &a}, "web-9", true},
		{"the holder records another name", nodeRecord{}, nodeRecord{true, &a}, nodeRecord{true, &b}, "web-9", false},
		{"the holder's name is not compared", nodeRecord{}, nodeRecord{true, &a}, nodeRecord{true, nil}, "web-9", false},
		{"the holder's name is not recorded", nodeRecord{}, nodeRecord{true, &a}, nodeRecord{}, "web-9", false},
		{"this machine's name is not compared", nodeRecord{}, nodeRecord{true, nil}, nodeRecord{true, &a}, "web-9", false},
		{"this machine's name is not recorded", nodeRecord{}, nodeRecord{}, nodeRecord{true, &a}, "web-9", false},
	} {
		t.Run(c.label, func(t *testing.T) {
			t.Parallel()
			ce := hostnameCommit(t, c.appliedOwn, c.target, c.by, c.name)
			_, err := ce.a.commitPlan(context.Background(), ce.pid)
			if !c.held {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			who := c.name
			if who == "" {
				who = ce.target.machine2
			}
			// The entry names the other machine by its identifier alone, so a rename leaves no stale name in it, and
			// the node name the release gives this machine, which is never a secret (client contracts A9 item 8).
			ce.wantRefusedBy(t, err, "6a", "the Applied release of machine "+who+clashCause, "", ce.target.machine2, a)
			ce.wantStillApproved(t, ce.pid)
			var entry string
			if err := ce.db.QueryRow(`SELECT entry::text FROM machine_event WHERE machine = $1 AND kind = 'refusal'`, ce.machine).Scan(&entry); err != nil {
				t.Fatal(err)
			}
			if (c.name != "" && strings.Contains(entry, "\""+c.name+"\"")) || strings.Contains(entry, "hostname") {
				t.Fatalf("the refusal entry holds the holder's name or a hostname member: %s", entry)
			}
		})
	}
}

// Only comparison 6a's entry holds `holder` and `nodeName`: the other comparisons' entries are as they were, with the
// members the entry always had and no more (wantRefused compares the whole entry, with neither member).
func TestOtherRefusalEntriesHoldNoHolder(t *testing.T) {
	t.Parallel()
	ce := newCommitEnv(t, "")
	ce.ready(t)
	mustExec(t, ce.db, `UPDATE machine SET frozen = true WHERE id = $1`, ce.machine)
	_, err := ce.a.commitPlan(context.Background(), ce.pid)
	ce.wantRefused(t, err, "6", "the machine scope is frozen", "")
	if n := count(t, ce.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'refusal'
		AND entry ?| array['holder', 'nodeName']`, ce.machine); n != 0 {
		t.Fatalf("%d refusal entries of another comparison hold a holder or a node name", n)
	}
}

// A plan the gate refused is not ended: it commits once the other machine's Applied release stops
// holding the name, and the commitment entry names comparison 6a among the comparisons it passed.
func TestCommitmentHostnameGateOpens(t *testing.T) {
	t.Parallel()
	a := "node-a"
	ce := hostnameCommit(t, nodeRecord{}, nodeRecord{true, &a}, nodeRecord{true, &a}, "web-9")
	for range 2 {
		_, err := ce.a.commitPlan(context.Background(), ce.pid)
		ce.wantRefusedBy(t, err, "6a", "the Applied release of machine web-9"+clashCause, "", ce.target.machine2, a)
	}
	if n := count(t, ce.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'refusal'`, ce.machine); n != 2 {
		t.Fatalf("%d refusal entries", n)
	}
	b := "node-b" // the holder's Applied release now records another name
	ce.setNode(ce.target.rel, ce.target.machine2, true, &b)
	if _, err := ce.a.commitPlan(context.Background(), ce.pid); err != nil {
		t.Fatal(err)
	}
	if n := count(t, ce.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'commitment'
		AND entry->'comparisons' = jsonb_build_array('0','1','2','3','4','5','6','6a')`, ce.machine); n != 1 {
		t.Fatal("the commitment entry does not name comparison 6a")
	}
}

// An adopt plan is not subject to the gate: its commitment records the machine's Applied and compares
// nothing about node names.
func TestAdoptionIsNotSubjectToTheHostnameGate(t *testing.T) {
	t.Parallel()
	a := "node-a"
	ae := newAdoptEnv(t, "")
	ae.setNode(ae.target.rel, ae.machine, true, &a)
	ae.setNode(ae.target.rel, ae.target.machine2, true, &a)
	mustExec(t, ae.db, `UPDATE machine_state SET applied_release = $2, applied_digest = $3, applied_source = 'adoption',
		baseline_revision = 1 WHERE machine = $1`, ae.target.machine2, ae.target.rel, make([]byte, 32))
	ae.approveIt(t)
	ae.observed(t, nil)
	if _, err := ae.a.commitAdoption(context.Background(), ae.pid); err != nil {
		t.Fatalf("an adoption whose name another machine holds: %v", err)
	}
}

// deadlocks is the count PostgreSQL reports for the test's own database, read after the sessions that
// might have detected one had time to flush it (a backend flushes its counters when it goes idle).
func deadlocks(t *testing.T, db *sql.DB) int {
	t.Helper()
	time.Sleep(1500 * time.Millisecond)
	return count(t, db, `SELECT deadlocks FROM pg_stat_database WHERE datname = current_database()`)
}

// copyReleaseMachine gives release to a row for machine copied from the same machine's row in release
// from, so a machine the seeded releases cover in one is covered by both.
func copyReleaseMachine(t *testing.T, db *sql.DB, from, to, machine string) {
	t.Helper()
	var cols string
	if err := db.QueryRow(`SELECT string_agg(quote_ident(column_name), ', ' ORDER BY ordinal_position) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'release_machine' AND is_generated = 'NEVER' AND column_name <> 'release'`).
		Scan(&cols); err != nil {
		t.Fatal(err)
	}
	// The triggers refuse a row added to a committed release and check what publication wrote.
	mustExec(t, db, `ALTER TABLE release_machine DISABLE TRIGGER USER`)
	// The copy names no assignment revision: the release's source rows name one machine's revision each.
	sel := strings.Replace(cols, "assignment_revision", "NULL", 1)
	mustExec(t, db, `INSERT INTO release_machine (release, `+cols+`) SELECT $2, `+sel+` FROM release_machine
		WHERE release = $1 AND machine = $3`, from, to, machine)
}

// symmetric is a commitEnv of two machines that each give the name the other's Applied release holds:
// machine 1's target release names N1 and machine 2's Applied release holds N1, and the reverse. A swap
// like this cannot be published (publication refuses one of the two releases), so the case sets both
// Applied records directly: it stands for the two plans that were each made against a release of their
// own and committed in either order. Both plans are approved and have evidence.
func symmetric(t *testing.T, o options) *commitEnv {
	t.Helper()
	n1, n2 := "node-1", "node-2"
	ce := newCommitEnv(t, "")
	ce.a = observer(t, ce.env, &fakeExecutor{}, o)
	ce.machine2 = ce.target.machine2
	copyReleaseMachine(t, ce.db, ce.target.rel, ce.applied.rel, ce.machine2) // the Applied release covers both machines
	ce.setNode(ce.applied.rel, ce.machine, true, &n2)
	ce.setNode(ce.applied.rel, ce.machine2, true, &n1)
	ce.setNode(ce.target.rel, ce.machine, true, &n1)
	ce.setNode(ce.target.rel, ce.machine2, true, &n2)
	mustExec(t, ce.db, `UPDATE machine_state SET desired = $2, applied_release = $3, applied_digest = $4, applied_source = 'adoption',
		baseline_revision = 1 WHERE machine = $1`, ce.machine2, ce.target.rel, ce.applied.rel, make([]byte, 32))
	ce.plan2 = decode[planBody](t, ce.plan(ce.robot, "k-apply2-0123456789ab", applyBody(ce.target.rel, ce.machine2, "")),
		http.StatusCreated).ID
	ce.approval2 = decode[approvalBody](t, ce.approve(ce.human("h-approver"), ce.key(), ce.plan2), http.StatusCreated).ID
	ce.ready(t)
	ce.evidence(t, ce.plan2, nil)
	return ce
}

// Two commitments in one cluster, each refused by the other's name: both wait, neither passes, and
// the two never deadlock, which is what taking the cluster's rows in one id-ordered pass buys (a pass
// that took its own row first and the others after would hold each plan's own row against the other).
// The pair runs repeatedly because a refusal leaves both plans approved; every round must refuse both.
func TestCommitmentHostnameGateTwoCommitments(t *testing.T) {
	t.Parallel()
	ce := symmetric(t, options{})
	before := deadlocks(t, ce.db)
	const rounds = 30
	for range rounds {
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, plan := range []string{ce.pid, ce.plan2} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = ce.a.commitPlan(context.Background(), plan)
			}()
		}
		wg.Wait()
		for i, err := range errs {
			var refused *commitRefused
			if !errors.As(err, &refused) || refused.Comparison != "6a" {
				t.Fatalf("commitment %d: %v; want a refusal by comparison 6a", i, err)
			}
		}
	}
	if got := deadlocks(t, ce.db) - before; got != 0 {
		t.Fatalf("%d deadlocks between two commitments", got)
	}
	if n := count(t, ce.db, `SELECT count(*) FROM operation WHERE kind = 'apply-config'`); n != 0 {
		t.Fatalf("%d operations: one of the two passed", n)
	}
	ce.wantStillApproved(t, ce.pid)
	ce.wantStillApproved(t, ce.plan2)
}

// The control of TestCommitmentHostnameGateTwoCommitments and TestCommitmentHostnameGateRace: without
// the share locks on the other machines, a commitment reads the other machine's Applied while an
// adoption, which writes it under that machine's row, is uncommitted, and passes on the record before it.
func TestCommitmentHostnameGateRace(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		control bool
	}{{"locked", false}, {"control", true}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			a := "node-a"
			o := options{}
			if c.control {
				o.noReadLock = "other machines"
			}
			ce := hostnameCommit(t, nodeRecord{}, nodeRecord{true, &a}, nodeRecord{true, &a}, "web-9")
			ce.a = observer(t, ce.env, &fakeExecutor{}, o)
			// The other machine has no Applied that holds the name yet: an adoption of it is in flight.
			mustExec(t, ce.db, `UPDATE machine_state SET applied_release = NULL, applied_digest = NULL, applied_source = NULL,
				baseline_revision = NULL, desired = NULL WHERE machine = $1`, ce.target.machine2)
			ce.evidence(t, ce.pid, nil)
			adopt, err := ce.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = adopt.Rollback() }()
			// adoption.go's write: the machine row first, then its state to the release.
			mustExec(t, adopt, `SELECT FROM machine WHERE id = $1 FOR UPDATE`, ce.target.machine2)
			mustExec(t, adopt, `UPDATE machine_state SET applied_release = $2, applied_digest = $3, applied_source = 'adoption',
				baseline_revision = 1, desired = $2 WHERE machine = $1`, ce.target.machine2, ce.target.rel, make([]byte, 32))
			done := make(chan error, 1)
			go func() {
				_, err := ce.a.commitPlan(context.Background(), ce.pid)
				done <- err
			}()
			if c.control {
				// Nothing holds the commitment back: it commits before the adoption does.
				if err := <-done; err != nil {
					t.Fatalf("control: %v", err)
				}
				if err := adopt.Commit(); err != nil {
					t.Fatal(err)
				}
				return
			}
			waitForLockWaits(t, ce.db, 1)
			if err := adopt.Commit(); err != nil {
				t.Fatal(err)
			}
			err = <-done
			ce.wantRefusedBy(t, err, "6a", "the Applied release of machine web-9"+clashCause, "", ce.target.machine2, a)
		})
	}
}

// A revocation of an identity and a recovery-mode entry take the installation state FOR UPDATE, and a
// commitment holds it FOR SHARE for its whole transaction, so they serialize with a commitment whatever
// rows its pass waits for. Here the pass waits for another machine's row while the revocation (or the
// entry) waits behind the installation state: once the row is free, both finish, and nothing
// deadlocks. The cases are named for what keeps them apart, the installation state's lock, and not for
// an order of machine rows.
func TestCommitmentHostnameGatePassSerializesWithRevocation(t *testing.T) {
	t.Parallel()
	ce := newCommitEnv(t, "")
	ce.ready(t)
	ce.principalOf("h-approver")
	before := deadlocks(t, ce.db)
	holder, err := ce.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	// Another machine's row, held FOR UPDATE, makes the pass wait at it.
	mustExec(t, holder, `SELECT FROM machine WHERE id = $1 FOR UPDATE`, ce.target.machine2)
	committed := make(chan error, 1)
	go func() {
		_, err := ce.a.commitPlan(context.Background(), ce.pid)
		committed <- err
	}()
	waitForLockWaits(t, ce.db, 1)
	revoked := make(chan int, 1)
	go func() {
		rec := revoke(ce.env, ce.human("h-recovery"), "k-revoke-identity-0123",
			`{"identity":"`+ce.principalOf("h-approver")+`","reason":"left"}`)
		revoked <- rec.Code
	}()
	waitForLockWaits(t, ce.db, 2)
	if err := holder.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-committed; err != nil {
		t.Fatalf("commitment: %v", err)
	}
	if code := <-revoked; code != http.StatusCreated {
		t.Fatalf("revocation answered %d", code)
	}
	if got := deadlocks(t, ce.db) - before; got != 0 {
		t.Fatalf("%d deadlocks between a commitment and a revocation", got)
	}
}

// The recovery-mode entry's side of the same serialization.
func TestCommitmentHostnameGatePassSerializesWithRecoveryEntry(t *testing.T) {
	t.Parallel()
	ce := newCommitEnv(t, "")
	ce.ready(t)
	before := deadlocks(t, ce.db)
	holder, err := ce.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	mustExec(t, holder, `SELECT FROM machine WHERE id = $1 FOR UPDATE`, ce.target.machine2)
	committed := make(chan error, 1)
	go func() {
		_, err := ce.a.commitPlan(context.Background(), ce.pid)
		committed <- err
	}()
	waitForLockWaits(t, ce.db, 1)
	var entered atomic.Int32
	r := recoveryStart(t, ce.env, &entered)
	entry := make(chan int, 1)
	go func() { entry <- ce.enter(r, "1", entryBodyText).Code }()
	waitForLockWaits(t, ce.db, 2)
	if err := holder.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-committed; err != nil {
		t.Fatalf("commitment: %v", err)
	}
	if code := <-entry; code != http.StatusCreated {
		t.Fatalf("the recovery-mode entry answered %d", code)
	}
	if got := deadlocks(t, ce.db) - before; got != 0 {
		t.Fatalf("%d deadlocks between a commitment and a recovery-mode entry", got)
	}
}

// The executor reads the gate as the commitment does: a plan behind a closed gate is not a candidate, so
// the loop writes no refusal entry for it however many leases pass, and it becomes a candidate in the pass
// after the other machine's Applied release stops holding the name.
func TestExecutorWaitsForTheHostnameGate(t *testing.T) {
	t.Parallel()
	a := "node-a"
	xe := newExecEnv(t)
	xe.setNode(xe.target.rel, xe.machine, true, &a)
	xe.setNode(xe.target.rel, xe.target.machine2, true, &a)
	mustExec(t, xe.db, `UPDATE machine_state SET applied_release = $2, applied_digest = $3, applied_source = 'adoption',
		baseline_revision = 1 WHERE machine = $1`, xe.target.machine2, xe.target.rel, make([]byte, 32))
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	x := xe.executor(t, life, idle, options{})
	x.startExecutor()
	defer stopping(x, stop)
	waitIdle(t, idle, 1)
	xe.approveThrough(t, x)
	for range 5 {
		time.Sleep(5 * time.Millisecond) // the lease is a millisecond: each pass is a new lease
		passEnds(t, x, idle)
	}
	if cs, err := x.dispatchCandidates(context.Background(), ""); err != nil || len(cs) != 0 {
		t.Fatalf("candidates %v, %v; want none behind the closed gate", cs, err)
	}
	if n := xe.entries(t, "refusal"); n != 0 {
		t.Fatalf("%d refusal entries across five leases", n)
	}
	if s := xe.opState(t); s != "" {
		t.Fatalf("the operation is %s", s)
	}
	xe.wantNothingSent(t)
	b := "node-b" // the holder's Applied release now records another name
	xe.setNode(xe.target.rel, xe.target.machine2, true, &b)
	until(t, x, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
}
