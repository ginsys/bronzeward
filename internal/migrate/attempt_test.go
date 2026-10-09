package migrate

import (
	"database/sql"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// attempts is planRows' apply-config plan committed to op, with an evidence observation obs of its
// machine at revisions 4 and 5 that its attempts rely on.
type attempts struct {
	plans
	op, obs string
}

func attemptRows(t *testing.T, db *sql.DB) attempts {
	t.Helper()
	a := attempts{plans: planRows(t, db), obs: id.New(id.Observation)}
	a.op = a.commit(t, db, a.apply)
	commitRows(t, db, a.entry(a.machine, 4, "observation-started"), a.startRow(4, "evidence", a.apply, nil).stmt(),
		a.entry(a.machine, 5, "observation"), a.observationRow(a.obs, 4, 5).stmt())
	return a
}

// attemptRow is attempt number n of the operation at timeline revision rev, recorded now under the
// commitment's owner token with the plan's route and deadlines.
func (a attempts) attemptRow(n, rev int) record {
	now := time.Now()
	return newRecord("attempt", "id", id.New(id.Attempt), "operation", a.op, "plan", a.apply, "machine", a.machine,
		"number", n, "owner", owner, "owner_gen", 1, "owner_epoch", a.epoch, "route", "10.55.0.3:50000",
		"observation", a.obs, "transport_deadline", now.Add(time.Minute), "verification_deadline", now.Add(5*time.Minute),
		"at", now, "revision", rev)
}

// attempted records r with its attempt entry, then sets the operation's state, in one transaction.
func (a attempts) attempted(r record, state string) []stmt {
	return []stmt{a.entry(a.machine, r.vals["revision"].(int), "attempt"), r.stmt(), a.state(state)}
}

func (a attempts) state(state string) stmt {
	return stmt{`UPDATE operation SET state = $2 WHERE id = $1`, []any{a.op, state}}
}

// Execution and recovery §3.3, §4, §4.1: an attempt binds its operation's plan, the plan's machine
// and route, an observation of that machine and ordered deadlines, once per number; it is immutable;
// an apply-config operation moves only along §4's transitions, reaching a state after a sent request
// only with an attempt and cancelled only without one.
func TestAttempts(t *testing.T) {
	db, _ := installed(t)
	a := attemptRows(t, db)
	r := a.attemptRow(1, 6)
	accepted(t, db, "first attempt", a.attempted(r, "sending")...)

	otherObs := id.New(id.Observation)
	commitRows(t, db, a.entry(a.otherMachine, 1, "observation-started"),
		a.startRow(1, "drift", nil, nil).with("machine", a.otherMachine).stmt(), a.entry(a.otherMachine, 2, "observation"),
		a.observationRow(otherObs, 1, 2).with("machine", a.otherMachine).stmt())
	for _, c := range []struct {
		name, want string
		r          record
	}{
		{"another route than the plan's", "23503", r.with("route", "10.55.0.4:50000")},
		{"another machine's observation", "23503", r.with("observation", otherObs)},
		{"another plan than the operation's", "23503", r.with("plan", a.adopt)},
		{"a publish operation", "23503", r.with("operation", a.publish)},
		{"transport after verification", "23514/attempt_deadlines",
			r.with("transport_deadline", time.Now().Add(10*time.Minute))},
		{"transport not after the attempt", "23514/attempt_deadlines", r.with("transport_deadline", time.Now().Add(-time.Minute))},
		{"number 0", "23514", r.with("number", 0)},
		{"generation 0", "23514", r.with("owner_gen", 0)},
	} {
		refused(t, db, "attempt with "+c.name, c.want, a.attempted(c.r, "sending")...)
	}
	refused(t, db, "sending with no attempt", "23514/operation_apply_config_attempt", a.state("sending"))
	commitRows(t, db, a.state("unresolved"))
	for _, state := range []string{"completed", "failed", "rejected", "sending"} {
		refused(t, db, "unresolved to "+state+" with no attempt", "23514/operation_apply_config_attempt", a.state(state))
	}
	accepted(t, db, "unresolved to cancelled with no attempt", a.state("cancelled"))
	refused(t, db, "attempt after cancellation", "23514/attempt_operation_cancelled",
		append([]stmt{a.state("cancelled")}, a.attempted(r, "cancelled")[:2]...)...)
	refused(t, db, "unresolved to verifying", "23514/operation_apply_config_transition", a.state("verifying"))
	refused(t, db, "unresolved to committed", "23514/operation_apply_config_transition", a.state("committed"))

	commitRows(t, db, a.attempted(r, "sending")...)
	refused(t, db, "attempt number taken", "23505", a.attempted(a.attemptRow(1, 7), "sending")[:2]...)
	accepted(t, db, "attempt 2", a.attempted(a.attemptRow(2, 7), "sending")[:2]...)
	refused(t, db, "sending to committed", "23514/operation_apply_config_transition", a.state("committed"))
	refused(t, db, "sending to completed", "23514/operation_apply_config_transition", a.state("completed"))
	commitRows(t, db, a.state("unresolved"))
	refused(t, db, "unresolved to cancelled with an attempt", "23514/operation_apply_config_transition", a.state("cancelled"))
	for _, step := range []string{"sending", "verifying", "completed"} {
		mustExec(t, db, `UPDATE operation SET state = $2 WHERE id = $1`, a.op, step)
	}
	for _, state := range []string{"unresolved", "failed", "cancelled", "sending"} {
		refused(t, db, "completed to "+state, "23514/operation_apply_config_transition", a.state(state))
	}
	// A terminal operation's owner may still be cleared: no state changes.
	mustExec(t, db, `UPDATE operation SET owner = NULL, owner_gen = 0, owner_epoch = NULL WHERE id = $1`, a.op)

	for _, q := range []string{"UPDATE attempt SET number = number", "DELETE FROM attempt", "TRUNCATE attempt CASCADE"} {
		if _, err := db.Exec(q); sqlState(err) != ImmutableSQLState {
			t.Errorf("%s: %v; want SQLSTATE %s", q, err, ImmutableSQLState)
		}
	}
	if count(t, db, "attempt") != 1 {
		t.Error("a refused statement changed the attempts")
	}
}

// Execution and recovery §3.2, §4: an apply-config operation is created committed, and reaches every
// later state along §4's transitions.
func TestApplyConfigCreatedCommitted(t *testing.T) {
	db, _ := installed(t)
	p := planRows(t, db)
	created := func(state string) []stmt {
		return []stmt{{insertPlanOperation, []any{id.New(id.Operation), "apply-config", state, owner, p.apply, p.machine, p.cluster}},
			{`UPDATE plan_state SET state = 'committed', operation = (SELECT id FROM operation WHERE plan = $1),
				revision = revision + 1 WHERE plan = $1`, []any{p.apply}}}
	}
	for _, state := range []string{"sending", "verifying", "unresolved", "completed", "failed", "rejected", "cancelled"} {
		refused(t, db, "apply-config created "+state, "23514/operation_apply_config_transition", created(state)...)
	}
	accepted(t, db, "apply-config created committed", created("committed")...)
	// The control: with the trigger dropped, an operation created sending with no attempt commits.
	accepted(t, db, "after DROP TRIGGER created ON operation",
		append([]stmt{{"DROP TRIGGER created ON operation", nil}}, created("sending")...)...)
}

// A cancellation and an attempt racing on one operation: the attempt waits for the cancellation's
// operation lock and then sees it cancelled (execution and recovery §4: cancelled only with no attempt).
func TestAttemptWaitsForCancellation(t *testing.T) {
	db, _ := installed(t)
	a := attemptRows(t, db)
	commitRows(t, db, a.state("unresolved"))
	cancel, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cancel.Rollback() }()
	mustExec(t, cancel, a.state("cancelled").q, a.state("cancelled").args...)
	done := make(chan error, 1)
	go func() {
		tx, err := db.Begin()
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = tx.Rollback() }()
		for _, s := range a.attempted(a.attemptRow(1, 6), "sending")[:2] {
			if _, err := tx.Exec(s.q, s.args...); err != nil {
				done <- err
				return
			}
		}
		done <- tx.Commit()
	}()
	time.Sleep(500 * time.Millisecond)
	if err := cancel.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; sqlState(err) != "23514" {
		t.Errorf("attempt racing a cancellation: %v; want SQLSTATE 23514", err)
	}
	if count(t, db, "attempt") != 0 {
		t.Error("a cancelled operation has an attempt")
	}
}

// An operation-state entry names its operation and both states (execution and recovery §4.1).
func TestOperationStateEntry(t *testing.T) {
	db, _ := installed(t)
	p := planRows(t, db)
	entry := func(e string) stmt { return stmt{insertMachineEvent, []any{p.machine, 4, "operation-state", e}} }
	for name, e := range map[string]string{
		"no operation": `{"from": "committed", "to": "unresolved"}`,
		"no from":      `{"operation": "op_x", "to": "unresolved"}`,
		"no to":        `{"operation": "op_x", "from": "committed"}`,
		"a number to":  `{"operation": "op_x", "from": "committed", "to": 1}`,
	} {
		refused(t, db, "operation-state entry with "+name, "23514/machine_event_operation_state", entry(e))
	}
	accepted(t, db, "operation-state entry", entry(`{"operation": "op_x", "from": "committed", "to": "unresolved"}`))
}

// The control for the checks above: with each new constraint or trigger dropped, a row it refuses
// commits.
func TestAttemptConstraintControl(t *testing.T) {
	db, _ := installed(t)
	a := attemptRows(t, db)
	r := a.attemptRow(1, 6)
	for _, c := range []struct {
		drop string
		rows []stmt
	}{
		{"ALTER TABLE attempt DROP CONSTRAINT attempt_deadlines",
			a.attempted(r.with("transport_deadline", time.Now().Add(10*time.Minute)), "sending")},
		{"DROP TRIGGER attempted ON operation", []stmt{a.state("sending")}},
		{"DROP TRIGGER transition ON operation", []stmt{a.state("cancelled")}},
		{"DROP TRIGGER cancelled ON attempt", append([]stmt{a.state("unresolved"), a.state("cancelled")},
			a.attempted(r, "cancelled")[:2]...)},
		{"ALTER TABLE machine_event DROP CONSTRAINT machine_event_operation_state",
			[]stmt{{insertMachineEvent, []any{a.machine, 6, "operation-state", `{}`}}}},
	} {
		accepted(t, db, "after "+c.drop, append([]stmt{{c.drop, nil}}, c.rows...)...)
	}
}
