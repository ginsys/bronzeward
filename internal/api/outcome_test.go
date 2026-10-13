package api

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/ginsys/bronzeward/internal/talos"
)

// answer is a Talos response of class, as the applier reports one.
func answer(class talos.Class) talos.Response {
	code := codes.OK
	switch class {
	case talos.ClassRejected, talos.ClassInvalidArgument:
		code = codes.InvalidArgument
	case talos.ClassCode:
		code = codes.PermissionDenied
	case talos.ClassTransport:
		code = codes.Unavailable
	case talos.ClassUnrecognised:
		code = codes.Unknown
	}
	return talos.Response{Class: class, Code: code, Text: "a fixed notice of class " + string(class)}
}

// sent records the operation's first attempt as its owner and answers it.
func (ae *attemptEnv) sent(t *testing.T) attempted {
	t.Helper()
	r, err := ae.a.recordAttempt(context.Background(), ae.op, 1)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// state answers the operation's state, owner generation and owner token.
func (ae *attemptEnv) state(t *testing.T) (state string, gen int64, owner string) {
	t.Helper()
	if err := ae.db.QueryRow(`SELECT state, owner_gen, owner FROM operation WHERE id = $1`, ae.op).Scan(&state, &gen, &owner); err != nil {
		t.Fatal(err)
	}
	return state, gen, owner
}

// reach brings the operation to state with a recorded attempt, as its owner: sending, verifying
// after an acceptance, or unresolved after a transport outcome. committed leaves it untouched.
func (ae *attemptEnv) reach(t *testing.T, state string) attempted {
	t.Helper()
	if state == "committed" {
		return attempted{}
	}
	r := ae.sent(t)
	switch state {
	case "sending":
	case "verifying":
		if _, err := ae.a.recordResponse(context.Background(), r.Attempt, answer(talos.ClassAccepted)); err != nil {
			t.Fatal(err)
		}
	case "unresolved":
		if _, err := ae.a.recordResponse(context.Background(), r.Attempt, answer(talos.ClassTransport)); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("no way to reach %s", state)
	}
	return r
}

// A takeover (execution-recovery.md §3.4) sets a new owner at the next generation and epoch in one
// conditional UPDATE and moves a committed, sending or verifying operation to unresolved; an
// unresolved one stays so. The ownership transition is one entry naming the tokens it moved between,
// and a state change one operation state entry, the last of the machine's timeline. It sends and
// classifies nothing, and the old owner's later attempt is refused by comparison 7.
func TestTakeOver(t *testing.T) {
	t.Parallel()
	for _, from := range []string{"committed", "sending", "verifying", "unresolved"} {
		t.Run(from, func(t *testing.T) {
			t.Parallel()
			ae := newAttemptEnv(t, "")
			ae.reach(t, from)
			old := ae.a.d.owner.ID
			next := observer(t, ae.env, &fakeExecutor{}, options{})
			r, err := next.takeOver(context.Background(), ae.op, 1, "controller-start")
			if err != nil {
				t.Fatal(err)
			}
			if r.Operation != ae.op || r.Generation != 2 || r.State != "unresolved" {
				t.Fatalf("took over %+v", r)
			}
			ep := epoch(t, ae.db)
			if n := count(t, ae.db, `SELECT count(*) FROM operation WHERE id = $1 AND state = 'unresolved' AND owner = $2
				AND owner_gen = 2 AND owner_epoch = $3`, ae.op, next.d.owner.ID, ep); n != 1 {
				t.Fatal("the operation's owner token is not the new one, unresolved")
			}
			if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND revision = $2 AND kind = 'ownership-transition'
					AND epoch = $3 AND entry = jsonb_build_object('operation', $4::text, 'from', $5::text, 'to', $6::text, 'generation', 2,
						'fromGeneration', 1, 'fromEpoch', $3::text, 'epoch', $3::text, 'reason', 'controller-start')`,
				ae.machine, r.Revision, ep, ae.op, old, next.d.owner.ID); n != 1 {
				t.Fatal("no ownership transition entry as recorded")
			}
			last := r.Revision
			if from != "unresolved" {
				last++
				if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND revision = $2 AND kind = 'operation-state'
						AND entry = jsonb_build_object('operation', $3::text, 'from', $4::text, 'to', 'unresolved', 'controller', $5::text,
							'generation', 2, 'cause', 'takeover')`, ae.machine, last, ae.op, from, next.d.owner.ID); n != 1 {
					t.Fatal("no operation state entry as recorded")
				}
			}
			if n := count(t, ae.db, `SELECT count(*) FROM machine WHERE id = $1 AND revision_counter = $2`, ae.machine, last); n != 1 {
				t.Fatal("the takeover's entries are not the machine's last")
			}
			// The fence: the old owner records no attempt and sends nothing.
			_, err = ae.a.recordAttempt(context.Background(), ae.op, 1)
			var refused *attemptRefused
			if !errors.As(err, &refused) || refused.Comparison != "7" {
				t.Fatalf("attempt of the replaced owner: %v; want comparison 7", err)
			}
		})
	}
}

// A takeover names the generation it read: a stale one, a terminal operation, another kind of
// operation and a process that is no controller of the epoch are refused, changing nothing. Two
// takeovers of one generation elect exactly one winner (§3.4).
func TestTakeOverRefusals(t *testing.T) {
	t.Parallel()
	t.Run("stale generation", func(t *testing.T) {
		t.Parallel()
		ae := newAttemptEnv(t, "")
		next := observer(t, ae.env, &fakeExecutor{}, options{})
		_, err := next.takeOver(context.Background(), ae.op, 2, "controller-start")
		var refused *outcomeRefused
		if !errors.As(err, &refused) {
			t.Fatalf("takeover naming generation 2: %v; want a refusal", err)
		}
		if state, gen, owner := ae.state(t); state != "committed" || gen != 1 || owner != ae.a.d.owner.ID {
			t.Fatalf("the operation changed: %s, generation %d, owner %s", state, gen, owner)
		}
	})
	t.Run("terminal", func(t *testing.T) {
		t.Parallel()
		ae := newAttemptEnv(t, "")
		mustExec(t, ae.db, `UPDATE operation SET state = 'unresolved' WHERE id = $1`, ae.op)
		mustExec(t, ae.db, `UPDATE operation SET state = 'cancelled' WHERE id = $1`, ae.op)
		next := observer(t, ae.env, &fakeExecutor{}, options{})
		var refused *outcomeRefused
		if _, err := next.takeOver(context.Background(), ae.op, 1, "controller-start"); !errors.As(err, &refused) {
			t.Fatalf("takeover of a cancelled operation: %v; want a refusal", err)
		}
	})
	t.Run("no controller", func(t *testing.T) {
		t.Parallel()
		ae := newAttemptEnv(t, "")
		ae.a.d.owner.ID = ""
		if _, err := ae.a.takeOver(context.Background(), ae.op, 1, "controller-start"); !errors.Is(err, errNotController) {
			t.Fatalf("takeover by a process with no owner: %v", err)
		}
	})
	t.Run("one winner", func(t *testing.T) {
		t.Parallel()
		ae := newAttemptEnv(t, "")
		ae.reach(t, "sending")
		contenders := []*API{observer(t, ae.env, &fakeExecutor{}, options{}), observer(t, ae.env, &fakeExecutor{}, options{})}
		errs := make([]error, len(contenders))
		var wg sync.WaitGroup
		for i, c := range contenders {
			wg.Go(func() {
				_, errs[i] = c.takeOver(context.Background(), ae.op, 1, "controller-start")
			})
		}
		wg.Wait()
		won := 0
		for _, err := range errs {
			var refused *outcomeRefused
			switch {
			case err == nil:
				won++
			case !errors.As(err, &refused):
				t.Fatalf("takeover: %v", err)
			}
		}
		if won != 1 {
			t.Fatalf("%d winners of one generation", won)
		}
		if _, gen, _ := ae.state(t); gen != 2 {
			t.Fatalf("generation %d after two takeovers of generation 1", gen)
		}
	})
}

// A response (execution-recovery.md §3.5, §4, §5.1) is one response entry and record under the
// attempt's token, with the accounting of a response from the target as the next entry, and moves
// the operation along §4: acceptance to verifying; a definitive rejection of every attempt to
// rejected, with its error; any other response, including a transport outcome that accounts for
// nothing, to unresolved.
func TestRecordResponse(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		class     talos.Class
		state     string
		accounted bool
		failure   bool // the operation records an error
	}{
		{talos.ClassAccepted, "verifying", true, false},
		{talos.ClassRejected, "rejected", true, true},
		{talos.ClassInvalidArgument, "unresolved", true, false},
		{talos.ClassCode, "unresolved", true, false},
		{talos.ClassTransport, "unresolved", false, false},
		{talos.ClassUnrecognised, "unresolved", false, false},
	} {
		t.Run(string(c.class), func(t *testing.T) {
			t.Parallel()
			ae := newAttemptEnv(t, "")
			att := ae.sent(t)
			resp := answer(c.class)
			r, err := ae.a.recordResponse(context.Background(), att.Attempt, resp)
			if err != nil {
				t.Fatal(err)
			}
			if r.Attempt != att.Attempt || r.State != c.state || r.Accounted != c.accounted {
				t.Fatalf("recorded %+v", r)
			}
			ep := epoch(t, ae.db)
			if n := count(t, ae.db, `SELECT count(*) FROM attempt_response r JOIN machine_event e ON e.machine = r.machine
					AND e.revision = r.revision AND e.kind = 'response'
				WHERE r.attempt = $1 AND r.class = $2 AND r.code = $3 AND r.text = $4 AND r.owner = $5 AND r.owner_gen = 1
					AND r.owner_epoch = $6 AND r.revision = $7 AND e.epoch = $6 AND e.at = r.at
					AND e.entry = jsonb_build_object('operation', $8::text, 'attempt', $1::text, 'number', 1, 'class', $2::text,
						'code', $3::integer, 'text', $4::text, 'controller', $5::text, 'generation', 1)`,
				att.Attempt, string(resp.Class), int(resp.Code), resp.Text, ae.a.d.owner.ID, ep, r.Revision, ae.op); n != 1 {
				t.Fatal("no response record and entry as recorded")
			}
			want := 0
			if c.accounted {
				want = 1
				if n := count(t, ae.db, `SELECT count(*) FROM attempt_accounting c JOIN machine_event e ON e.machine = c.machine
						AND e.revision = c.revision AND e.kind = 'accounting'
					WHERE c.attempt = $1 AND c.basis = 'response' AND c.response_from_target AND c.revision = $2 AND c.epoch = $3
						AND e.entry = jsonb_build_object('operation', $4::text, 'attempt', $1::text, 'basis', 'response',
							'class', $5::text)`, att.Attempt, r.Revision+1, ep, ae.op, string(resp.Class)); n != 1 {
					t.Fatal("no accounting record and entry as recorded")
				}
			}
			if n := count(t, ae.db, `SELECT count(*) FROM attempt_accounting WHERE attempt = $1`, att.Attempt); n != want {
				t.Fatalf("%d accounting records, want %d", n, want)
			}
			if state, _, _ := ae.state(t); state != c.state {
				t.Fatalf("the operation is %s, want %s", state, c.state)
			}
			if n := count(t, ae.db, `SELECT count(*) FROM operation WHERE id = $1 AND (error IS NOT NULL) = $2
				AND ($2 = false OR error->>'outcome' = 'rejected')`, ae.op, c.failure); n != 1 {
				t.Fatal("the operation's error is not as the outcome needs")
			}
			if c.state != "" && c.state != "sending" {
				if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'operation-state'
						AND entry->>'operation' = $2 AND entry->>'from' = 'sending' AND entry->>'to' = $3`,
					ae.machine, ae.op, c.state); n != 1 {
					t.Fatal("no operation state entry")
				}
			}
		})
	}
}

// A response records once, and only by the controller that sent the attempt; one that arrives
// after a takeover is a recorded fact that accounts for its attempt and leaves an unresolved
// operation to a completion observation, except a definitive rejection of every attempt, which
// ends it (§4: unresolved to rejected).
func TestRecordResponseRefusalsAndLateResponses(t *testing.T) {
	t.Parallel()
	t.Run("twice", func(t *testing.T) {
		t.Parallel()
		ae := newAttemptEnv(t, "")
		att := ae.sent(t)
		if _, err := ae.a.recordResponse(context.Background(), att.Attempt, answer(talos.ClassTransport)); err != nil {
			t.Fatal(err)
		}
		var refused *outcomeRefused
		if _, err := ae.a.recordResponse(context.Background(), att.Attempt, answer(talos.ClassAccepted)); !errors.As(err, &refused) {
			t.Fatalf("second response: %v; want a refusal", err)
		}
	})
	t.Run("another controller", func(t *testing.T) {
		t.Parallel()
		ae := newAttemptEnv(t, "")
		att := ae.sent(t)
		other := observer(t, ae.env, &fakeExecutor{}, options{})
		var refused *outcomeRefused
		if _, err := other.recordResponse(context.Background(), att.Attempt, answer(talos.ClassAccepted)); !errors.As(err, &refused) {
			t.Fatalf("response by another controller: %v; want a refusal", err)
		}
		if n := count(t, ae.db, `SELECT count(*) FROM attempt_response`); n != 0 {
			t.Fatal("a response was recorded")
		}
	})
	t.Run("late acceptance", func(t *testing.T) {
		t.Parallel()
		ae := newAttemptEnv(t, "")
		att := ae.sent(t)
		next := observer(t, ae.env, &fakeExecutor{}, options{})
		if _, err := next.takeOver(context.Background(), ae.op, 1, "controller-start"); err != nil {
			t.Fatal(err)
		}
		r, err := ae.a.recordResponse(context.Background(), att.Attempt, answer(talos.ClassAccepted))
		if err != nil {
			t.Fatal(err)
		}
		if r.State != "unresolved" || !r.Accounted {
			t.Fatalf("late acceptance %+v", r)
		}
		if state, gen, owner := ae.state(t); state != "unresolved" || gen != 2 || owner != next.d.owner.ID {
			t.Fatalf("the operation is %s at generation %d, owner %s", state, gen, owner)
		}
	})
	t.Run("late rejection", func(t *testing.T) {
		t.Parallel()
		ae := newAttemptEnv(t, "")
		att := ae.sent(t)
		next := observer(t, ae.env, &fakeExecutor{}, options{})
		if _, err := next.takeOver(context.Background(), ae.op, 1, "controller-start"); err != nil {
			t.Fatal(err)
		}
		r, err := ae.a.recordResponse(context.Background(), att.Attempt, answer(talos.ClassRejected))
		if err != nil {
			t.Fatal(err)
		}
		if r.State != "rejected" {
			t.Fatalf("late rejection left the operation %s", r.State)
		}
	})
}

// classified brings the operation to unresolved and classified safe to retry by its new owner, at
// generation 2, after a lost response: sent, taken over, accounted by a decision, and settled on a
// recovery observation at the plan's pre-dispatch digest. It answers that owner and the
// classification's revision.
func (ae *attemptEnv) classified(t *testing.T) (*API, int64) {
	t.Helper()
	att := ae.sent(t)
	if _, err := ae.a.recordResponse(context.Background(), att.Attempt, answer(talos.ClassTransport)); err != nil {
		t.Fatal(err)
	}
	next := ae.takenOverBy(t)
	start := ae.observed(t, func(c *cobs) { c.purpose, c.shift = "recovery", settleWindow })
	end := ae.observed(t, func(c *cobs) { c.purpose, c.shift = "recovery", settleWindow+time.Minute })
	if _, err := ae.decide(t, next, att.Attempt, start, end, nil); err != nil {
		t.Fatal(err)
	}
	obs := ae.observed(t, func(c *cobs) {
		var d []byte
		if err := ae.db.QueryRow(`SELECT expected_digest FROM plan WHERE id = $1`, ae.pid).Scan(&d); err != nil {
			t.Fatal(err)
		}
		c.digest = d
	})
	r, err := next.settleOperation(context.Background(), ae.op, 2, obs)
	if err != nil {
		t.Fatal(err)
	}
	if r.Outcome != "safe-to-retry" {
		t.Fatalf("settled %+v", r)
	}
	return next, r.Revision
}

// A retry (execution-recovery.md §3.3 comparisons 3, 7 and 8, §4): an unresolved operation classified
// safe to retry by this token goes to sending with one more attempt that names the classification
// by revision, on its entry and its record, and relies on the classification's observation.
func TestAttemptRetry(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	next, cls := ae.classified(t)
	r, err := next.recordAttempt(context.Background(), ae.op, 2)
	if err != nil {
		t.Fatal(err)
	}
	if r.Number != 2 {
		t.Fatalf("recorded %+v", r)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM attempt a JOIN operation o ON o.id = a.operation
			JOIN operation_classification c ON c.operation = a.operation AND c.revision = a.classification
			JOIN machine_event e ON e.machine = a.machine AND e.revision = a.revision
		WHERE a.id = $1 AND a.number = 2 AND a.classification = $2 AND a.observation = c.observation AND a.owner = $3
			AND a.owner_gen = 2 AND o.state = 'sending' AND (e.entry->>'classification')::bigint = $2`,
		r.Attempt, cls, next.d.owner.ID); n != 1 {
		t.Fatal("no retry attempt, bound to its classification, with the operation sending")
	}
}

// A retry is refused where its comparisons fail: by the previous owner (7), after an entry that
// follows the classification (8), once the classification has admitted an attempt (8), and by an
// operation classified by another token (7).
func TestAttemptRetryRefusals(t *testing.T) {
	t.Parallel()
	t.Run("previous owner", func(t *testing.T) {
		t.Parallel()
		ae := newAttemptEnv(t, "")
		ae.classified(t)
		_, err := ae.a.recordAttempt(context.Background(), ae.op, 1)
		var refused *attemptRefused
		if !errors.As(err, &refused) || refused.Comparison != "7" {
			t.Fatalf("retry by the replaced owner: %v; want comparison 7", err)
		}
	})
	t.Run("entry after the classification", func(t *testing.T) {
		t.Parallel()
		ae := newAttemptEnv(t, "")
		next, _ := ae.classified(t)
		mustExec(t, ae.db, `UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1`, ae.machine)
		mustExec(t, ae.db, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
			SELECT m.id, m.revision_counter, i.epoch, 'ownership-transition', jsonb_build_object('operation', $2::text, 'from', 'a',
				'to', 'b', 'generation', 2, 'reason', 'test'), now()
			FROM machine m, installation_state i WHERE m.id = $1`, ae.machine, ae.op)
		_, err := next.recordAttempt(context.Background(), ae.op, 2)
		var refused *attemptRefused
		if !errors.As(err, &refused) || refused.Comparison != "8" {
			t.Fatalf("retry after a later entry: %v; want comparison 8", err)
		}
		if state, _, _ := ae.state(t); state != "unresolved" {
			t.Fatalf("the operation is %s", state)
		}
	})
	t.Run("one attempt per classification", func(t *testing.T) {
		t.Parallel()
		ae := newAttemptEnv(t, "")
		next, _ := ae.classified(t)
		r, err := next.recordAttempt(context.Background(), ae.op, 2)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := next.recordResponse(context.Background(), r.Attempt, answer(talos.ClassTransport)); err != nil {
			t.Fatal(err)
		}
		_, err = next.recordAttempt(context.Background(), ae.op, 2)
		var refused *attemptRefused
		if !errors.As(err, &refused) || refused.Comparison != "8" {
			t.Fatalf("second retry of one classification: %v; want comparison 8", err)
		}
	})
}
