package migrate

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/dbtest"
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
		"observation", a.obs, "classification", nil, "transport_deadline", now.Add(time.Minute), "verification_deadline", now.Add(5*time.Minute),
		"at", now, "revision", rev)
}

// attempted records r with its attempt entry, then sets the operation's state, in one transaction.
func (a attempts) attempted(r record, state string) []stmt {
	return []stmt{a.entry(a.machine, r.vals["revision"].(int), "attempt"), r.stmt(), a.state(state)}
}

func (a attempts) state(state string) stmt {
	return stmt{`UPDATE operation SET state = $2 WHERE id = $1`, []any{a.op, state}}
}

// completed moves the operation to completed with the result an apply-config operation names.
func (a attempts) completed() stmt {
	return stmt{`UPDATE operation SET state = 'completed', result = '{"appliedRelease": "rel"}' WHERE id = $1`, []any{a.op}}
}

// finish moves the operation to a state ending it, with the result or error that state names.
func (a attempts) finish(state string) stmt {
	switch state {
	case "completed":
		return a.completed()
	case "failed", "rejected":
		return stmt{`UPDATE operation SET state = $2, error = '{"code": "conflict"}' WHERE id = $1`, []any{a.op, state}}
	}
	return a.state(state)
}

// entryOf is the machine timeline entry of kind at revision rev with the given JSON entry.
func (a attempts) entryOf(rev int, kind, entry string) stmt {
	return stmt{insertMachineEvent, []any{a.machine, rev, kind, entry}}
}

// classified is the operation's classification at revision rev: its entry and its record.
func (a attempts) classified(rev int, outcome string, observation any) []stmt {
	r := newRecord("operation_classification", "operation", a.op, "machine", a.machine, "revision", rev, "outcome", outcome,
		"observation", observation, "owner", owner, "owner_gen", 1, "owner_epoch", a.epoch, "relied", `[]`, "at", time.Now())
	obs := `null`
	if s, ok := observation.(string); ok {
		obs = `"` + s + `"`
	}
	return []stmt{a.entryOf(rev, "classification", `{"operation": "`+a.op+`", "outcome": "`+outcome+`", "observation": `+obs+
		`, "relied": []}`), r.stmt()}
}

// answered is attempt att's response of class at revision rev, from the target, and the accounting
// it gives at rev+1.
func (a attempts) answered(att string, rev int, class string) []stmt {
	return answered(a.machine, a.op, a.epoch, att, rev, class)
}

func answered(machine, op, epoch, att string, rev int, class string) []stmt {
	resp := newRecord("attempt_response", "attempt", att, "machine", machine, "class", class, "code", 0,
		"text", "the node accepted the configuration in no-reboot mode", "owner", owner, "owner_gen", 1,
		"owner_epoch", epoch, "at", time.Now(), "revision", rev)
	acc := newRecord("attempt_accounting", "attempt", att, "machine", machine, "basis", "response",
		"response_from_target", true, "epoch", epoch, "at", time.Now(), "revision", rev+1)
	entry := func(rev int, kind, basis string) stmt {
		return stmt{insertMachineEvent, []any{machine, rev, kind,
			`{"operation": "` + op + `", "attempt": "` + att + `", "class": "` + class + `", "basis": "` + basis + `"}`}}
	}
	return []stmt{entry(rev, "response", ""), resp.stmt(), entry(rev+1, "accounting", "response"), acc.stmt()}
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
		refused(t, db, "unresolved to "+state+" with no attempt", "23514/operation_apply_config_attempt", a.finish(state))
	}
	accepted(t, db, "unresolved to cancelled with no attempt", a.state("cancelled"))
	refused(t, db, "attempt after cancellation", "23514/attempt_operation_cancelled",
		append([]stmt{a.state("cancelled")}, a.attempted(r, "cancelled")[:2]...)...)
	refused(t, db, "unresolved to verifying", "23514/operation_apply_config_transition", a.state("verifying"))
	refused(t, db, "unresolved to committed", "23514/operation_apply_config_transition", a.state("committed"))

	commitRows(t, db, a.attempted(r, "sending")...)
	refused(t, db, "attempt number taken", "23505", a.attempted(a.attemptRow(1, 7), "sending")[:2]...)
	// An attempt after the first names the safe-to-retry classification, earlier on the timeline,
	// that admitted it (§3.3 comparison 8).
	retry := a.attemptRow(2, 8)
	refused(t, db, "retry with no classification", "23514/attempt_retry_classified", a.attempted(retry, "sending")[:2]...)
	refused(t, db, "retry naming no classification", "23503", a.attempted(retry.with("classification", 7), "sending")[:2]...)
	accepted(t, db, "attempt 2", append(a.classified(7, "safe-to-retry", a.obs),
		a.attempted(retry.with("classification", 7), "sending")[:2]...)...)
	commitRows(t, db, append(a.classified(7, "safe-to-retry", a.obs),
		a.attempted(retry.with("classification", 7), "sending")[:2]...)...)
	refused(t, db, "sending to committed", "23514/operation_apply_config_transition", a.state("committed"))
	refused(t, db, "sending to completed", "23514/operation_apply_config_transition", a.state("completed"))
	commitRows(t, db, a.state("unresolved"))
	refused(t, db, "unresolved to cancelled with an attempt", "23514/operation_apply_config_transition", a.state("cancelled"))
	for _, step := range []string{"sending", "verifying"} {
		mustExec(t, db, `UPDATE operation SET state = $2 WHERE id = $1`, a.op, step)
	}
	// Completed names what it applied, and every attempt is accounted for (§4, §8 invariant 5).
	refused(t, db, "completed with no result", "23514/operation_apply_outcome", a.state("completed"))
	refused(t, db, "completed with unaccounted attempts", "23514/operation_apply_config_accounted", a.completed())
	commitRows(t, db, append(append(a.answered(r.vals["id"].(string), 9, "accepted"),
		a.answered(retry.vals["id"].(string), 11, "accepted")...), a.completed())...)
	for _, state := range []string{"unresolved", "failed", "cancelled", "sending"} {
		refused(t, db, "completed to "+state, "23514/operation_apply_config_transition", a.state(state))
	}
	// A terminal operation takes no attempt: a third, unaccounted one naming the old classification
	// would leave a completed operation with an attempt not accounted for (§8 invariant 5).
	refused(t, db, "attempt after completion", "23514/attempt_operation_terminal",
		a.attempted(a.attemptRow(3, 20).with("classification", 7), "completed")[:2]...)
	// A terminal operation's owner may still be cleared: no state changes.
	mustExec(t, db, `UPDATE operation SET owner = NULL, owner_gen = 0, owner_epoch = NULL WHERE id = $1`, a.op)

	for _, q := range []string{"UPDATE attempt SET number = number", "DELETE FROM attempt", "TRUNCATE attempt CASCADE"} {
		if _, err := db.Exec(q); sqlState(err) != ImmutableSQLState {
			t.Errorf("%s: %v; want SQLSTATE %s", q, err, ImmutableSQLState)
		}
	}
	if count(t, db, "attempt") != 2 {
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
	a, cancel := cancelling(t, db)
	done := make(chan error, 1)
	go func() { done <- recordAttempt(db, a) }()
	dbtest.WaitForLockWait(t, db)
	if err := cancel.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; sqlState(err) != "23514" {
		t.Errorf("attempt racing a cancellation: %v; want SQLSTATE 23514", err)
	}
	if count(t, db, "attempt") != 0 {
		t.Error("a cancelled operation has an attempt")
	}

	// The control: with the operation read without its share lock, the attempt does not wait, and
	// both it and the cancellation commit.
	db, _ = installed(t)
	var def string
	if err := db.QueryRow(`SELECT pg_get_functiondef('refuse_cancelled_attempt'::regproc)`).Scan(&def); err != nil {
		t.Fatal(err)
	}
	unlocked := strings.Replace(def, " FOR SHARE", "", 1)
	if unlocked == def {
		t.Fatal("refuse_cancelled_attempt takes no share lock to remove")
	}
	mustExec(t, db, unlocked)
	a, cancel = cancelling(t, db)
	if err := recordAttempt(db, a); err != nil {
		t.Fatalf("control: attempt during the cancellation: %v; want it committed", err)
	}
	if err := cancel.Commit(); err != nil {
		t.Fatalf("control: cancellation: %v; want it committed", err)
	}
	if count(t, db, "attempt") != 1 {
		t.Error("control: the cancelled operation has no attempt")
	}
}

// cancelling is attemptRows' operation moved to unresolved, with an open transaction that has
// cancelled it and holds its row lock.
func cancelling(t *testing.T, db *sql.DB) (attempts, *sql.Tx) {
	t.Helper()
	a := attemptRows(t, db)
	commitRows(t, db, a.state("unresolved"))
	cancel, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cancel.Rollback() })
	mustExec(t, cancel, a.state("cancelled").q, a.state("cancelled").args...)
	return a, cancel
}

// recordAttempt commits attempt 1 of a's operation with its entry, leaving the state as it is.
func recordAttempt(db *sql.DB, a attempts) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, s := range a.attempted(a.attemptRow(1, 6), "sending")[:2] {
		if _, err := tx.Exec(s.q, s.args...); err != nil {
			return err
		}
	}
	return tx.Commit()
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
