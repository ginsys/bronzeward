package migrate

import (
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// Execution and recovery §3.4, §4.1, §5.1, §5.2: what follows a recorded attempt is a response, an
// accounting, a classification or an ownership transition, each one entry on the machine's timeline
// that names its operation, and an accounting rests either on a response from the target or on a
// recovery-admin's decision with its facts.
func TestOutcomeRecords(t *testing.T) {
	db, _ := installed(t)
	a := attemptRows(t, db)
	r := a.attemptRow(1, 6)
	commitRows(t, db, a.attempted(r, "sending")...)
	att := r.vals["id"].(string)

	respond := func(rev int, attempt, class string, code int, text string) []stmt {
		resp := newRecord("attempt_response", "attempt", attempt, "machine", a.machine, "class", class, "code", code,
			"text", text, "owner", owner, "owner_gen", 1, "owner_epoch", a.epoch, "at", time.Now(), "revision", rev)
		return []stmt{a.entryOf(rev, "response", `{"operation": "`+a.op+`", "attempt": "`+attempt+`"}`), resp.stmt()}
	}
	for _, c := range []struct {
		name, want, attempt, class, text string
		code                             int
	}{
		{"an unknown class", "23514", att, "refused", "no", 0},
		{"a code past the last gRPC code", "23514", att, "grpc-code", "no", 17},
		{"a blank notice", "23514", att, "accepted", "  ", 0},
		{"an attempt that is not recorded", "23503", id.New(id.Attempt), "accepted", "ok", 0},
	} {
		refused(t, db, "response with "+c.name, c.want, respond(7, c.attempt, c.class, c.code, c.text)...)
	}
	accepted(t, db, "transport response", respond(7, att, "transport", 14, "the node was unreachable")...)
	commitRows(t, db, respond(7, att, "transport", 14, "the node was unreachable")...)
	refused(t, db, "second response of the attempt", "23505", respond(8, att, "accepted", 0, "ok")...)

	// An accounting is one entry, named for its attempt.
	account := func(rev int, basis string, fromTarget any) record {
		return newRecord("attempt_accounting", "attempt", att, "machine", a.machine, "basis", basis,
			"response_from_target", fromTarget, "epoch", a.epoch, "at", time.Now(), "revision", rev)
	}
	entry := func(rev int) stmt {
		return a.entryOf(rev, "accounting", `{"operation": "`+a.op+`", "attempt": "`+att+`"}`)
	}
	refused(t, db, "accounting entry naming no attempt", "23514/machine_event_attempt_entry",
		a.entryOf(8, "accounting", `{"operation": "`+a.op+`"}`))
	refused(t, db, "response accounting of a transport response", "23503", entry(8), account(8, "response", true).stmt())
	refused(t, db, "response accounting naming no response flag", "23514/attempt_accounting_basis",
		entry(8), account(8, "response", nil).stmt())
	refused(t, db, "decision accounting with no decider", "23514/attempt_accounting_basis",
		entry(8), account(8, "decision", nil).stmt())

	actID := id.New(id.Act)
	decision := newRecord("attempt_accounting", "attempt", att, "machine", a.machine, "basis", "decision",
		"response_from_target", nil, "decided_by", a.human, "role", "recovery-admin", "act", actID,
		"start_observation", a.obs, "end_observation", a.obs, "facts", `{"floor": "30s"}`, "epoch", a.epoch,
		"at", time.Now(), "revision", 8)
	refused(t, db, "decision accounting citing a response", "23514/attempt_accounting_basis",
		entry(8), decision.with("response_from_target", true).stmt(), a.act(actID, a.human, "recovery-admin"))
	refused(t, db, "decision accounting by a role other than recovery-admin", "23514",
		entry(8), decision.with("role", "approver").stmt(), a.act(actID, a.human, "approver"))
	refused(t, db, "decision accounting citing no act", "23503", entry(8), decision.stmt())
	accepted(t, db, "decision accounting", entry(8), decision.stmt(), a.act(actID, a.human, "recovery-admin"))
	// A decision stays the one account of its attempt.
	commitRows(t, db, entry(8), decision.stmt(), a.act(actID, a.human, "recovery-admin"))
	refused(t, db, "second accounting of the attempt", "23505", entry(9), decision.with("revision", 9, "act", id.New(id.Act)).stmt())

	// A classification names its operation; safe-to-retry rests on an observation; cancelled is
	// recorded by the operation's state entry, not here.
	refused(t, db, "classification entry naming no operation", "23514/machine_event_operation_entry",
		a.entryOf(10, "classification", `{}`))
	refused(t, db, "safe-to-retry on no observation", "23514", a.classified(10, "safe-to-retry", nil)...)
	refused(t, db, "a cancelled classification", "23514", a.classified(10, "cancelled", nil)...)
	commitRows(t, db, a.classified(10, "unresolved", nil)...)

	// A retry names a safe-to-retry classification earlier on the timeline.
	retry := func(rev, classification int) []stmt {
		return a.attempted(a.attemptRow(2, rev).with("classification", classification), "sending")[:2]
	}
	refused(t, db, "retry under an unresolved classification", "23503", retry(11, 10)...)
	refused(t, db, "retry recorded before the classification that admits it", "23514/attempt_classified_before",
		append(a.classified(12, "safe-to-retry", a.obs), retry(11, 12)...)...)
	accepted(t, db, "retry under a safe-to-retry classification",
		append(a.classified(11, "safe-to-retry", a.obs), retry(12, 11)...)...)

	// An ownership transition names its operation, the tokens it moved between, the generation it
	// reached and why.
	transition := func(entry string) stmt { return a.entryOf(13, "ownership-transition", entry) }
	for name, e := range map[string]string{
		"no operation":  `{"from": "a", "to": "b", "generation": 2, "reason": "lease"}`,
		"no from":       `{"operation": "x", "to": "b", "generation": 2, "reason": "lease"}`,
		"no generation": `{"operation": "x", "from": "a", "to": "b", "reason": "lease"}`,
		"a number from": `{"operation": "x", "from": 1, "to": "b", "generation": 2, "reason": "lease"}`,
		"no reason":     `{"operation": "x", "from": "a", "to": "b", "generation": 2}`,
	} {
		refused(t, db, "ownership transition entry naming "+name, "23514", transition(e))
	}
	accepted(t, db, "ownership transition entry",
		transition(`{"operation": "x", "from": "a", "to": "b", "generation": 2, "reason": "lease"}`))

	// The controls: without each constraint, the row it refuses commits.
	for _, c := range []struct {
		name, drop string
		rows       []stmt
	}{
		{"attempt_retry_classified", "ALTER TABLE attempt DROP CONSTRAINT attempt_retry_classified",
			a.attempted(a.attemptRow(2, 13), "sending")[:2]},
		{"attempt_classified_before", "ALTER TABLE attempt DROP CONSTRAINT attempt_classified_before",
			append(a.classified(15, "safe-to-retry", a.obs), retry(14, 15)...)},
		{"machine_event_attempt_entry", "ALTER TABLE machine_event DROP CONSTRAINT machine_event_attempt_entry",
			[]stmt{a.entryOf(13, "accounting", `{"operation": "`+a.op+`"}`)}},
		{"machine_event_ownership_transition", "ALTER TABLE machine_event DROP CONSTRAINT machine_event_ownership_transition",
			[]stmt{transition(`{"operation": "x"}`)}},
	} {
		accepted(t, db, "after "+c.drop, append([]stmt{{c.drop, nil}}, c.rows...)...)
	}
}

// Execution and recovery §4, §8 invariant 5: an apply-config operation completes with the result it
// applied and only once every attempt is accounted. The controls: without each constraint, an
// operation completes without.
func TestOperationOutcomeControls(t *testing.T) {
	db, _ := installed(t)
	a := attemptRows(t, db)
	r := a.attemptRow(1, 6)
	commitRows(t, db, a.attempted(r, "sending")...)
	commitRows(t, db, a.state("verifying"))
	att := r.vals["id"].(string)

	accepted(t, db, "after DROP CONSTRAINT operation_apply_outcome",
		append([]stmt{{"ALTER TABLE operation DROP CONSTRAINT operation_apply_outcome", nil}},
			append(a.answered(att, 7, "accepted"), a.state("completed"))...)...)
	accepted(t, db, "after DROP TRIGGER accounted ON operation",
		stmt{"DROP TRIGGER accounted ON operation", nil}, a.completed())
}
