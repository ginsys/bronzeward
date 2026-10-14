package api

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// attemptEnv is a commitEnv whose plan ce.a committed to op on the evidence observation obs.
// seeded counts the attempts a test recorded directly.
type attemptEnv struct {
	*commitEnv
	op, obs string
	seeded  int
	gen     int64 // the owner generation the attempt submitted; 0 is 1
}

func newAttemptEnv(t *testing.T, extra string) *attemptEnv {
	t.Helper()
	ae := &attemptEnv{commitEnv: newCommitEnv(t, extra)}
	ae.commit(t)
	return ae
}

func (ae *attemptEnv) commit(t *testing.T) {
	t.Helper()
	ae.obs = ae.ready(t)
	r, err := ae.a.commitPlan(context.Background(), ae.pid)
	if err != nil {
		t.Fatal(err)
	}
	ae.op = r.Operation
}

// hookedAttemptEnv is newAttemptEnv whose controller commits its transactions through commit once
// armed is set.
func hookedAttemptEnv(t *testing.T, commit func(*attemptEnv, *sql.Tx) error) (*attemptEnv, *atomic.Bool) {
	t.Helper()
	ae := &attemptEnv{commitEnv: newCommitEnv(t, "")}
	var armed atomic.Bool
	ae.a = observer(t, ae.env, &fakeExecutor{}, options{commit: func(tx *sql.Tx) error {
		if !armed.Load() {
			return tx.Commit()
		}
		return commit(ae, tx)
	}})
	ae.commit(t)
	return ae, &armed
}

// seedAttempt records the operation's next attempt as its owner would, leaving its state.
func (ae *attemptEnv) seedAttempt(t *testing.T) {
	t.Helper()
	tx, err := ae.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var rev int64
	if err := tx.QueryRow(`UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1 RETURNING revision_counter`,
		ae.machine).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	att := id.New(id.Attempt)
	mustExec(t, tx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		SELECT $1, $2, epoch, 'attempt', jsonb_build_object('attempt', $3::text, 'operation', $4::text), now() FROM installation_state`,
		ae.machine, rev, att, ae.op)
	mustExec(t, tx, `INSERT INTO attempt (id, operation, plan, machine, number, owner, owner_gen, owner_epoch, route, observation,
			transport_deadline, verification_deadline, at, revision)
		SELECT $1, o.id, p.id, p.machine, $5, o.owner, o.owner_gen, o.owner_epoch, p.route, $3, now() + interval '1 minute',
			now() + interval '5 minutes', now(), $4
		FROM operation o JOIN plan p ON p.id = o.plan WHERE o.id = $2`, att, ae.op, ae.obs, rev, ae.seeded+1)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ae.seeded++
}

// T6's attempt transaction (execution-recovery.md §3.3): the first attempt of a committed
// operation, recorded by its owner at generation 1, moves it to sending and records one attempt
// numbered 1, under the owner token, with the plan's route, the commitment's evidence observation
// and the plan's deadlines counted from the attempt's time, as one attempt entry that is the
// machine's last. Nothing else is written: no refusal, no operation state entry, no plan change.
func TestAttempt(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	r, err := ae.a.recordAttempt(context.Background(), ae.op, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.Operation != ae.op || r.Number != 1 || id.MustHave(r.Attempt, id.Attempt) != nil {
		t.Fatalf("recorded %+v", r)
	}
	ep := epoch(t, ae.db)
	if n := count(t, ae.db, `SELECT count(*) FROM attempt a
			JOIN machine_event e ON e.machine = a.machine AND e.revision = a.revision
			JOIN machine m ON m.id = a.machine AND m.revision_counter = a.revision
			JOIN plan p ON p.id = a.plan JOIN operation o ON o.id = a.operation
		WHERE a.id = $1 AND a.operation = $2 AND a.plan = $3 AND a.machine = $4 AND a.number = 1 AND a.owner = $5
			AND a.owner_gen = 1 AND a.owner_epoch = $6 AND a.route = p.route AND a.observation = $7
			AND a.transport_deadline = a.at + p.transport_deadline AND a.verification_deadline = a.at + p.verification_deadline
			AND a.revision = $8 AND e.kind = 'attempt' AND e.epoch = $6 AND e.at = a.at
			AND e.entry - 'transportDeadline' - 'verificationDeadline' = jsonb_build_object('attempt', $1::text,
				'operation', $2::text, 'number', 1, 'controller', $5::text, 'generation', 1, 'route', p.route::text,
				'observation', $7::text)
			AND (e.entry->>'transportDeadline')::timestamptz = a.transport_deadline
			AND (e.entry->>'verificationDeadline')::timestamptz = a.verification_deadline
			AND o.state = 'sending' AND o.owner = $5 AND o.owner_gen = 1 AND o.owner_epoch = $6`,
		r.Attempt, ae.op, ae.pid, ae.machine, ae.a.d.owner.ID, ep, ae.obs, r.Revision); n != 1 {
		t.Fatal("no attempt, attempt entry and sending operation as recorded")
	}
	if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE kind IN ('refusal', 'operation-state')`); n != 0 {
		t.Fatalf("%d refusal or operation state entries", n)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM plan_state WHERE plan = $1 AND state = 'committed' AND operation = $2
		AND revision = 3`, ae.pid, ae.op); n != 1 {
		t.Fatal("the plan's state changed")
	}
}

// An attempt does not repeat comparison 2's Desired term: a publication after the commitment does
// not stop the committed operation (§3.3). A newer agreeing observation does not contradict the
// evidence, and a recovery-mode machine whose scope is released passes the gate.
func TestAttemptAccepts(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		setup func(*testing.T, *attemptEnv)
	}{
		{"Desired changed", func(t *testing.T, ae *attemptEnv) {
			mustExec(t, ae.db, `UPDATE machine_state SET desired = $2 WHERE machine = $1`, ae.machine, ae.applied.rel)
		}},
		{"a newer agreeing observation", func(t *testing.T, ae *attemptEnv) {
			ae.evidence(t, ae.pid, func(s *evSeed) { s.purpose, s.controller = "drift", "another" })
		}},
		{"recovery mode released", func(t *testing.T, ae *attemptEnv) {
			mustExec(t, ae.db, `UPDATE installation_state SET recovery_mode = true`)
			mustExec(t, ae.db, `UPDATE machine SET scope_state = 'released' WHERE id = $1`, ae.machine)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ae := newAttemptEnv(t, "")
			c.setup(t, ae)
			if _, err := ae.a.recordAttempt(context.Background(), ae.op, 1); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// settled is one operation state change a refusal's settle records.
type settled struct{ to, comparison, cause string }

// wantAttemptRefused holds err to a refusal by comparison with cause, naming observation when not
// empty, and the timeline to its record (§4.1, PA §8.1): a refusal entry naming T6, the comparison,
// the plan, the operation, the cause and the controller, followed by one operation state entry per
// step of states, from the operation's state from on, each naming the owner's generation and the
// comparison and cause it settles, the last of which is the machine's last entry; the operation
// left in the last state; and no attempt but the seeded ones.
func (ae *attemptEnv) wantAttemptRefused(t *testing.T, err error, comparison, cause, observation, from string, states ...settled) {
	t.Helper()
	var refused *attemptRefused
	if !errors.As(err, &refused) || refused.Comparison != comparison || refused.Cause != cause || refused.Observation != observation {
		t.Fatalf("got %v, want an attempt refusal by comparison %s: %s (%q)", err, comparison, cause, observation)
	}
	var obs any
	if observation != "" {
		obs = observation
	}
	gen := ae.gen
	if gen == 0 {
		gen = 1
	}
	var rev int64
	if err := ae.db.QueryRow(`SELECT revision FROM machine_event WHERE machine = $1 AND kind = 'refusal'
			AND epoch = (SELECT epoch FROM installation_state)
			AND entry = jsonb_strip_nulls(jsonb_build_object('transaction', 'T6', 'comparison', $2::text, 'plan', $3::text,
				'operation', $4::text, 'cause', $5::text, 'controller', $6::text, 'generation', $8::bigint,
				'observation', $7::text))`,
		ae.machine, comparison, ae.pid, ae.op, cause, ae.a.d.owner.ID, obs, gen).Scan(&rev); err != nil {
		t.Fatalf("no refusal entry: %v", err)
	}
	for i, s := range states {
		if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND revision = $2 AND kind = 'operation-state'
				AND epoch = (SELECT epoch FROM installation_state)
				AND entry = jsonb_build_object('operation', $3::text, 'from', $4::text, 'to', $5::text, 'controller', $6::text,
					'generation', $9::bigint, 'comparison', $7::text, 'cause', $8::text)`,
			ae.machine, rev+int64(i)+1, ae.op, from, s.to, ae.a.d.owner.ID, s.comparison, s.cause, gen); n != 1 {
			t.Fatalf("no operation state entry %s to %s at revision %d", from, s.to, rev+int64(i)+1)
		}
		from = s.to
	}
	if n := count(t, ae.db, `SELECT count(*) FROM machine WHERE id = $1 AND revision_counter = $2`, ae.machine,
		rev+int64(len(states))); n != 1 {
		t.Fatal("the refusal and its operation state entries are not the machine's last entries")
	}
	if n := count(t, ae.db, `SELECT count(*) FROM operation WHERE id = $1 AND state = $2`, ae.op, from); n != 1 {
		t.Fatalf("the operation is not %s", from)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM attempt WHERE operation = $1`, ae.op); n != ae.seeded {
		t.Fatalf("%d attempts, %d seeded", n, ae.seeded)
	}
}

// T6's attempt refusals (§3.3) and their settle (PA §8.1): a refusal by comparison 1, 2, 3 or 6
// moves the committed operation to unresolved, and a refusal by comparison 1, whose terms never
// pass again, then cancels it when no attempt is recorded; a refusal by 7 or 9 changes no state.
func TestAttemptRefusals(t *testing.T) {
	t.Parallel()
	unresolved := func(comparison, cause string) []settled { return []settled{{"unresolved", comparison, cause}} }
	cancelled := func(cause string) []settled { return []settled{{"unresolved", "1", cause}, {"cancelled", "1", cause}} }
	stale := "the operation's owner token is not this controller's"
	for _, c := range []struct {
		name, extra, comparison, cause string
		from                           string // the operation's state before; committed when empty
		gen                            int64  // the generation the controller names; 1 when 0
		setup                          func(*testing.T, *attemptEnv) string
		states                         []settled
	}{
		{name: "expired", extra: `,"expiresInSeconds":3`, comparison: "1", cause: "the plan has expired",
			setup: func(t *testing.T, ae *attemptEnv) string {
				time.Sleep(time.Until(ae.expires.Add(100 * time.Millisecond)))
				return ""
			}, states: cancelled("the plan has expired")},
		{name: "cancelled", comparison: "1", cause: "the plan is cancelled", setup: func(t *testing.T, ae *attemptEnv) string {
			decode[cancellationBody](t, ae.cancel(ae.human("h-approver"), ae.key(), ae.pid, `{"reason":"not now"}`), http.StatusOK)
			return ""
		}, states: cancelled("the plan is cancelled")},
		{name: "approval revoked", comparison: "1", cause: "the approval is revoked", setup: func(t *testing.T, ae *attemptEnv) string {
			if rec := ae.revokeApproval(ae.human("h-recovery"), ae.key(), ae.approval, `{"reason":"wrong"}`); rec.Code != http.StatusCreated {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			return ""
		}, states: cancelled("the approval is revoked")},
		{name: "approver revoked", comparison: "1", cause: "the approver's identity is revoked",
			setup: func(t *testing.T, ae *attemptEnv) string {
				revocationOf(t, revoke(ae.env, ae.human("h-recovery"), ae.key(),
					`{"identity":"`+ae.principalOf("h-approver")+`","reason":"left"}`))
				return ""
			}, states: cancelled("the approver's identity is revoked")},
		{name: "expired with an attempt", extra: `,"expiresInSeconds":3,"maxAttempts":2`, comparison: "1",
			cause: "the plan has expired", setup: func(t *testing.T, ae *attemptEnv) string {
				ae.seedAttempt(t) // an operation with an attempt is never cancelled
				time.Sleep(time.Until(ae.expires.Add(100 * time.Millisecond)))
				return ""
			}, states: unresolved("1", "the plan has expired")},
		{name: "assignment changed", comparison: "2", cause: "the machine's assignment revision changed",
			setup: func(t *testing.T, ae *attemptEnv) string {
				rev := id.New(id.AssignmentRevision)
				mustExec(t, ae.db, `INSERT INTO assignment_revision (id, cluster, machine, author, created_at) VALUES ($1, $2, $3, $4, now())`,
					rev, ae.cluster, ae.machine, ae.seed)
				mustExec(t, ae.db, `UPDATE assignment SET head_revision_id = $2, head_revision = 2 WHERE machine = $1`, ae.machine, rev)
				return ""
			}, states: unresolved("2", "the machine's assignment revision changed")},
		{name: "baseline changed", comparison: "2", cause: "the machine's baseline revision changed",
			setup: func(t *testing.T, ae *attemptEnv) string {
				mustExec(t, ae.db, `UPDATE machine_state SET baseline_revision = 5 WHERE machine = $1`, ae.machine)
				return ""
			}, states: unresolved("2", "the machine's baseline revision changed")},
		{name: "evidence too old", extra: `,"maxObservationAgeSeconds":3`, comparison: "3",
			cause: "the evidence is older than the plan allows", setup: func(t *testing.T, ae *attemptEnv) string {
				var started time.Time
				if err := ae.db.QueryRow(`SELECT e.at FROM observation o JOIN machine_event e ON e.machine = o.machine
					AND e.revision = o.basis WHERE o.id = $1`, ae.obs).Scan(&started); err != nil {
					t.Fatal(err)
				}
				time.Sleep(time.Until(started.Add(3100 * time.Millisecond)))
				return ae.obs
			}, states: unresolved("3", "the evidence is older than the plan allows")},
		{name: "a newer observation shows another digest", comparison: "3", cause: "a newer observation contradicts the evidence",
			setup: func(t *testing.T, ae *attemptEnv) string {
				return ae.evidence(t, ae.pid, func(s *evSeed) {
					s.purpose, s.controller, s.digest = "drift", "another", bytes.Repeat([]byte{9}, 32)
				})
			}, states: unresolved("3", "a newer observation contradicts the evidence")},
		{name: "frozen", comparison: "6", cause: "the machine scope is frozen", setup: func(t *testing.T, ae *attemptEnv) string {
			mustExec(t, ae.db, `UPDATE machine SET frozen = true WHERE id = $1`, ae.machine)
			return ""
		}, states: unresolved("6", "the machine scope is frozen")},
		{name: "scope blocked", comparison: "6", cause: "the machine scope is blocked", setup: func(t *testing.T, ae *attemptEnv) string {
			mustExec(t, ae.db, `UPDATE machine SET scope_state = 'blocked' WHERE id = $1`, ae.machine)
			return ""
		}, states: unresolved("6", "the machine scope is blocked")},
		{name: "recovery mode", comparison: "6", cause: "recovery mode is in effect and the scope is not released",
			setup: func(t *testing.T, ae *attemptEnv) string {
				mustExec(t, ae.db, `UPDATE installation_state SET recovery_mode = true`)
				return ""
			}, states: unresolved("6", "recovery mode is in effect and the scope is not released")},
		{name: "another generation", gen: 2, comparison: "7", cause: stale},
		{name: "taken over", comparison: "7", cause: stale, setup: func(t *testing.T, ae *attemptEnv) string {
			mustExec(t, ae.db, `UPDATE operation SET owner = 'another', owner_gen = 2 WHERE id = $1`, ae.op)
			return ""
		}},
		{name: "another controller at the owner's generation", comparison: "7", cause: stale,
			setup: func(t *testing.T, ae *attemptEnv) string {
				ae.a = observer(t, ae.env, &fakeExecutor{}, options{})
				return ""
			}},
		{name: "unresolved", from: "unresolved", comparison: "7", cause: "the operation is unresolved",
			setup: func(t *testing.T, ae *attemptEnv) string {
				mustExec(t, ae.db, `UPDATE operation SET state = 'unresolved' WHERE id = $1`, ae.op)
				return ""
			}},
		{name: "unresolved, then cancelled", from: "unresolved", comparison: "1", cause: "the plan is cancelled",
			setup: func(t *testing.T, ae *attemptEnv) string {
				mustExec(t, ae.db, `UPDATE operation SET state = 'unresolved' WHERE id = $1`, ae.op)
				decode[cancellationBody](t, ae.cancel(ae.human("h-approver"), ae.key(), ae.pid, `{"reason":"not now"}`), http.StatusOK)
				return ""
			}, states: []settled{{"cancelled", "1", "the plan is cancelled"}}},
		{name: "sending, then cancelled", from: "sending", comparison: "1", cause: "the plan is cancelled",
			setup: func(t *testing.T, ae *attemptEnv) string {
				ae.seedAttempt(t)
				mustExec(t, ae.db, `UPDATE operation SET state = 'sending' WHERE id = $1`, ae.op)
				decode[cancellationBody](t, ae.cancel(ae.human("h-approver"), ae.key(), ae.pid, `{"reason":"not now"}`), http.StatusOK)
				return ""
			}},
		{name: "cancelled operation", from: "cancelled", comparison: "1", cause: "the plan is cancelled",
			setup: func(t *testing.T, ae *attemptEnv) string {
				mustExec(t, ae.db, `UPDATE operation SET state = 'unresolved' WHERE id = $1`, ae.op)
				mustExec(t, ae.db, `UPDATE operation SET state = 'cancelled' WHERE id = $1`, ae.op)
				decode[cancellationBody](t, ae.cancel(ae.human("h-approver"), ae.key(), ae.pid, `{"reason":"not now"}`), http.StatusOK)
				return ""
			}},
		// A terminal operation's owner may be cleared; settling still records the refusal.
		{name: "cancelled operation with no owner", from: "cancelled", comparison: "1", cause: "the plan is cancelled",
			setup: func(t *testing.T, ae *attemptEnv) string {
				mustExec(t, ae.db, `UPDATE operation SET state = 'unresolved' WHERE id = $1`, ae.op)
				mustExec(t, ae.db, `UPDATE operation SET state = 'cancelled' WHERE id = $1`, ae.op)
				mustExec(t, ae.db, `UPDATE operation SET owner = NULL, owner_epoch = NULL WHERE id = $1`, ae.op)
				decode[cancellationBody](t, ae.cancel(ae.human("h-approver"), ae.key(), ae.pid, `{"reason":"not now"}`), http.StatusOK)
				return ""
			}},
		{name: "another generation, then cancelled", gen: 2, comparison: "1", cause: "the plan is cancelled",
			setup: func(t *testing.T, ae *attemptEnv) string {
				decode[cancellationBody](t, ae.cancel(ae.human("h-approver"), ae.key(), ae.pid, `{"reason":"not now"}`), http.StatusOK)
				return ""
			}},
		{name: "no attempts left", extra: `,"maxAttempts":1`, comparison: "9", cause: "the operation has no attempts left",
			setup: func(t *testing.T, ae *attemptEnv) string {
				ae.seedAttempt(t)
				return ""
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ae := newAttemptEnv(t, c.extra)
			named := ""
			if c.setup != nil {
				named = c.setup(t, ae)
			}
			gen, from := c.gen, c.from
			if gen == 0 {
				gen = 1
			}
			if from == "" {
				from = "committed"
			}
			ae.gen = gen
			_, err := ae.a.recordAttempt(context.Background(), ae.op, gen)
			ae.wantAttemptRefused(t, err, c.comparison, c.cause, named, from, c.states...)
		})
	}
}

// A controller that is not the operation's owner records a refusal and settles nothing: in a new
// epoch, the earlier epoch's approval fails comparison 1 for the new controller, and the operation,
// owned in the earlier epoch, stays committed. A controller of an earlier epoch records nothing at
// all (PA §5.1).
func TestAttemptEpochs(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	stale := ae.a
	newEpoch(t, ae.db)
	if _, err := stale.recordAttempt(context.Background(), ae.op, 1); err != errNotController {
		t.Fatalf("stale controller: %v", err)
	}
	if err := stale.settleAttemptRefusal(context.Background(), ae.machine, ae.pid, ae.op, 1,
		&attemptRefused{Comparison: "1", Cause: "the plan is cancelled"}); err != errNotController {
		t.Fatalf("stale controller's settle: %v", err)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE kind IN ('refusal', 'attempt', 'operation-state')`); n != 0 {
		t.Fatalf("%d entries by a stale controller", n)
	}
	ae.a = observer(t, ae.env, &fakeExecutor{}, options{})
	_, err := ae.a.recordAttempt(context.Background(), ae.op, 1)
	ae.wantAttemptRefused(t, err, "1", "the approval is of an earlier epoch", "", "committed")
}

// Each record comparisons 1, 2 and 6 read is held until the attempt commits (§3.3, PA §5 rule 5):
// a transaction that would change it, standing in for a cancellation, an approval or identity
// revocation, a freeze, a recovery-mode entry or an assignment change, waits for the attempt, and
// then reads the operation sending.
func TestAttemptHoldsItsReads(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, lock string
		arg        func(*attemptEnv) any
	}{
		{"machine", `SELECT FROM machine WHERE id = $1 FOR UPDATE`, func(ae *attemptEnv) any { return ae.machine }},
		{"plan state", `SELECT FROM plan_state WHERE plan = $1 FOR UPDATE`, func(ae *attemptEnv) any { return ae.pid }},
		{"approval", `SELECT FROM approval WHERE id = $1 FOR UPDATE`, func(ae *attemptEnv) any { return ae.approval }},
		{"principal", `SELECT FROM principal WHERE id = $1 FOR UPDATE`, func(ae *attemptEnv) any { return ae.principalOf("h-approver") }},
		{"installation state", `SELECT FROM installation_state WHERE $1::text IS NOT NULL FOR UPDATE`,
			func(*attemptEnv) any { return "" }},
		{"assignment head", `SELECT FROM assignment WHERE machine = $1 FOR UPDATE`, func(ae *attemptEnv) any { return ae.machine }},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			done := make(chan string, 1)
			var arg any
			ae, armed := hookedAttemptEnv(t, func(ae *attemptEnv, tx *sql.Tx) error {
				go func() {
					state := "not read"
					defer func() { done <- state }()
					stand, err := ae.db.Begin()
					if err != nil {
						return
					}
					defer func() { _ = stand.Rollback() }()
					if _, err := stand.Exec(c.lock, arg); err != nil {
						return
					}
					_ = stand.QueryRow(`SELECT state FROM operation WHERE id = $1`, ae.op).Scan(&state)
				}()
				waitForLockWaits(t, ae.db, 1)
				return tx.Commit()
			})
			arg = c.arg(ae)
			armed.Store(true)
			if _, err := ae.a.recordAttempt(context.Background(), ae.op, 1); err != nil {
				t.Fatal(err)
			}
			if state := <-done; state != "sending" {
				t.Fatalf("the stand-in read the operation %s", state)
			}
		})
	}
}

// A cancellation, or a revocation of the approval or the approver's identity, that arrives while
// the attempt holds its locks waits for it, and is recorded after the attempt.
func TestAttemptRevocationsWait(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		act  func(ae *attemptEnv) func() *httptest.ResponseRecorder
		code int
	}{
		{"cancellation", func(ae *attemptEnv) func() *httptest.ResponseRecorder {
			token, key := ae.human("h-approver"), ae.key()
			return func() *httptest.ResponseRecorder {
				return ae.cancel(token, key, ae.pid, `{"reason":"not now"}`)
			}
		}, http.StatusOK},
		{"approval revocation", func(ae *attemptEnv) func() *httptest.ResponseRecorder {
			token, key := ae.human("h-recovery"), ae.key()
			return func() *httptest.ResponseRecorder {
				return ae.revokeApproval(token, key, ae.approval, `{"reason":"wrong"}`)
			}
		}, http.StatusCreated},
		{"identity revocation", func(ae *attemptEnv) func() *httptest.ResponseRecorder {
			token, key := ae.human("h-recovery"), ae.key()
			body := `{"identity":"` + ae.principalOf("h-approver") + `","reason":"left"}`
			return func() *httptest.ResponseRecorder { return revoke(ae.env, token, key, body) }
		}, http.StatusCreated},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			done := make(chan *httptest.ResponseRecorder, 1)
			var act func() *httptest.ResponseRecorder
			ae, armed := hookedAttemptEnv(t, func(ae *attemptEnv, tx *sql.Tx) error {
				go func() { done <- act() }()
				waitForLockWaits(t, ae.db, 1)
				return tx.Commit()
			})
			act = c.act(ae)
			armed.Store(true)
			r, err := ae.a.recordAttempt(context.Background(), ae.op, 1)
			if err != nil {
				t.Fatal(err)
			}
			if rec := <-done; rec.Code != c.code {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND revision > $2
					AND kind IN ('plan-cancellation', 'approval-revocation', 'identity-revocation')`, ae.machine, r.Revision); n != 1 {
				t.Fatal("the act was not recorded after the attempt")
			}
		})
	}
}

// The attempt's time follows its last lock (PA §5 rule 4): a plan that expires while the attempt
// waits for the approval's lock is refused as expired, not admitted at the time it began.
func TestAttemptTimeFollowsLocks(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, `,"expiresInSeconds":4`)
	stand, err := ae.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stand.Rollback() }()
	mustExec(t, stand, `SELECT FROM approval WHERE id = $1 FOR UPDATE`, ae.approval)
	if time.Until(ae.expires) <= 0 {
		t.Fatal("the plan expired before the attempt began")
	}
	errs := make(chan error, 1)
	go func() {
		_, err := ae.a.recordAttempt(context.Background(), ae.op, 1)
		errs <- err
	}()
	waitForLockWaits(t, ae.db, 1)
	time.Sleep(time.Until(ae.expires.Add(100 * time.Millisecond)))
	if err := stand.Rollback(); err != nil {
		t.Fatal(err)
	}
	ae.wantAttemptRefused(t, <-errs, "1", "the plan has expired", "", "committed",
		settled{"unresolved", "1", "the plan has expired"}, settled{"cancelled", "1", "the plan has expired"})
}

// Settling reads its time after the operation lock (PA §5 rule 4): a refusal settled just before
// the plan expires, whose settle waits for the operation past expiry, cancels the operation.
func TestAttemptSettleTimeFollowsLocks(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, `,"expiresInSeconds":4`)
	stand, err := ae.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stand.Rollback() }()
	mustExec(t, stand, `SELECT FROM operation WHERE id = $1 FOR UPDATE`, ae.op)
	if time.Until(ae.expires) <= 0 {
		t.Fatal("the plan expired before the settle began")
	}
	refused := &attemptRefused{Comparison: "6", Cause: "the machine scope is frozen"}
	errs := make(chan error, 1)
	go func() { errs <- ae.a.settleAttemptRefusal(context.Background(), ae.machine, ae.pid, ae.op, 1, refused) }()
	waitForLockWaits(t, ae.db, 1)
	time.Sleep(time.Until(ae.expires.Add(100 * time.Millisecond)))
	if err := stand.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	ae.wantAttemptRefused(t, refused, "6", "the machine scope is frozen", "", "committed",
		settled{"unresolved", "6", "the machine scope is frozen"}, settled{"cancelled", "1", "the plan has expired"})
}

// The attempt reads the machine under its lock: a freeze that holds the machine when the attempt
// begins, and commits while the attempt waits, closes the gate for it (comparison 6).
func TestAttemptWaitsForTheMachine(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	stand, err := ae.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stand.Rollback() }()
	mustExec(t, stand, `UPDATE machine SET frozen = true WHERE id = $1`, ae.machine)
	errs := make(chan error, 1)
	go func() {
		_, err := ae.a.recordAttempt(context.Background(), ae.op, 1)
		errs <- err
	}()
	waitForLockWaits(t, ae.db, 1)
	if err := stand.Commit(); err != nil {
		t.Fatal(err)
	}
	ae.wantAttemptRefused(t, <-errs, "6", "the machine scope is frozen", "", "committed",
		settled{"unresolved", "6", "the machine scope is frozen"})
}

// The attempt relies on its own operation's commitment: an earlier commitment entry on the
// machine, naming another operation and an observation that does not exist, is not read.
func TestAttemptReadsItsOwnCommitment(t *testing.T) {
	t.Parallel()
	ae := &attemptEnv{commitEnv: newCommitEnv(t, "")}
	tx, err := ae.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var rev int64
	if err := tx.QueryRow(`UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1 RETURNING revision_counter`,
		ae.machine).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	mustExec(t, tx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		SELECT $1, $2, epoch, 'commitment', jsonb_build_object('operation', $3::text, 'plan', $4::text, 'observation', $5::text),
			now() FROM installation_state`, ae.machine, rev, id.New(id.Operation), ae.pid, id.New(id.Observation))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ae.commit(t)
	r, err := ae.a.recordAttempt(context.Background(), ae.op, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM attempt WHERE id = $1 AND observation = $2`, r.Attempt, ae.obs); n != 1 {
		t.Fatal("the attempt does not name its commitment's observation")
	}
}

// Comparison 7 is checked inside the write that moves the operation to sending (§3.3, DS row 018):
// a takeover that commits while the attempt waits on the operation row, standing in for one that
// holds no machine lock, refuses the attempt. The control reads the owner token first and writes
// without it: it records an attempt under the token the takeover replaced.
func TestAttemptStaleOwner(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		control bool
	}{{"conditional write", false}, {"control", true}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ae := newAttemptEnv(t, "")
			ae.a.o.readThenWrite = c.control
			takeover, err := ae.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = takeover.Rollback() }()
			mustExec(t, takeover, `UPDATE operation SET owner = 'another', owner_gen = 2 WHERE id = $1`, ae.op)
			done := make(chan error, 1)
			go func() {
				_, err := ae.a.recordAttempt(context.Background(), ae.op, 1)
				done <- err
			}()
			waitForLockWaits(t, ae.db, 1)
			if err := takeover.Commit(); err != nil {
				t.Fatal(err)
			}
			err = <-done
			if c.control {
				if err != nil {
					t.Fatalf("control: %v", err)
				}
				if n := count(t, ae.db, `SELECT count(*) FROM attempt a JOIN operation o ON o.id = a.operation
					WHERE a.operation = $1 AND a.owner_gen = 1 AND o.owner_gen = 2`, ae.op); n != 1 {
					t.Fatal("control: no attempt under the replaced token")
				}
				return
			}
			ae.wantAttemptRefused(t, err, "7", "the operation's owner token is not this controller's", "", "committed")
		})
	}
}
