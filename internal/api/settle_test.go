package api

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/talos"
)

// cobs is one observation of the operation's machine as a controller records it, by default a
// completion observation showing what a completed apply leaves: the machine's identity, the release's
// artifact digest, the plan's assignment revision and the contract minor.
type cobs struct {
	purpose    string
	operation  any    // the operation the observation's start names; the operation under test for a completion
	digest     []byte // nil is the artifact's digest
	assignment any    // nil is the plan's assignment revision
	version    string
	resource   string
	unread     string
	shift      time.Duration // the observation's start and record, from now
	began      time.Duration // the observation's start alone, from now, when it is not shift
	atPlan     bool          // a start naming the plan though its purpose needs none
	scope      bool          // a recovery observation of the scope, naming no plan or operation (§7.3 step 1)
	unreached  bool          // access was obtained but no node value was read
	node       string        // the Talos node ID the observation read, when it is not the machine's
	uuid       string        // the SMBIOS UUID the observation read, when it is not the machine's
}

// observed records o's observation and answers its id.
func (ae *attemptEnv) observed(t *testing.T, change func(*cobs)) string {
	t.Helper()
	s := cobs{purpose: "completion", operation: ae.op, version: "v1.13.6", resource: "rv1", unread: "{}"}
	if change != nil {
		change(&s)
	}
	var route string
	var uuid, nodeID sql.NullString
	var cluster string
	var artifact []byte
	var assignment string
	if err := ae.db.QueryRow(`SELECT p.route::text, m.smbios_uuid::text, m.talos_node_id, c.talos_cluster_id, rm.configuration_digest,
			p.assignment_revision
		FROM plan p JOIN machine m ON m.id = p.machine JOIN cluster c ON c.id = p.cluster
			JOIN release_machine rm ON rm.release = p.release AND rm.machine = p.machine WHERE p.id = $1`, ae.pid).Scan(&route,
		&uuid, &nodeID, &cluster, &artifact, &assignment); err != nil {
		t.Fatal(err)
	}
	if s.digest == nil {
		s.digest = artifact
	}
	var asg any = assignment
	if s.assignment != nil {
		asg = s.assignment
	}
	var startPlan any
	switch s.purpose {
	case "evidence", "completion", "recovery":
		startPlan = ae.pid
	}
	var startOperation any
	if s.purpose == "completion" || s.purpose == "recovery" {
		startOperation = s.operation
	}
	if s.scope {
		startPlan, startOperation = nil, nil
	}
	tx, err := ae.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var basis, rev int64
	if err := tx.QueryRow(`UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1 RETURNING revision_counter`,
		ae.machine).Scan(&basis); err != nil {
		t.Fatal(err)
	}
	secs, began := s.shift.Seconds(), s.shift.Seconds()
	if s.began != 0 {
		began = s.began.Seconds()
	}
	if s.node != "" {
		nodeID = sql.NullString{String: s.node, Valid: true}
	}
	if s.uuid != "" {
		uuid = sql.NullString{String: s.uuid, Valid: true}
	}
	vals := []any{uuid, nodeID, cluster, asg, s.version, s.digest, s.resource}
	if s.unreached {
		vals = make([]any, len(vals))
		s.unread = `{"identity":{"cause":"node-unreachable"},"runningVersion":{"cause":"node-unreachable"},` +
			`"configuration":{"cause":"node-unreachable"},"health":{"cause":"none-bound"}}`
	}
	mustExec(t, tx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		SELECT $1, $2, epoch, 'observation-started', jsonb_build_object('purpose', $3::text, 'plan', $4::text, 'endpoint', $5::text,
			'controller', $6::text), now() + make_interval(secs => $7)
		FROM installation_state`, ae.machine, basis, s.purpose, startPlan, route, ae.a.d.owner.ID, began)
	mustExec(t, tx, `INSERT INTO observation_start (machine, revision, purpose, plan, operation, endpoint, controller)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, ae.machine, basis, s.purpose, startPlan, startOperation, route, ae.a.d.owner.ID)
	obs := id.New(id.Observation)
	if err := tx.QueryRow(`UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1 RETURNING revision_counter`,
		ae.machine).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	mustExec(t, tx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		SELECT $1, $2, epoch, 'observation', jsonb_build_object('observation', $3::text, 'purpose', $4::text, 'basis', $5::bigint),
			now() + make_interval(secs => $6)
		FROM installation_state`, ae.machine, rev, obs, s.purpose, basis, secs)
	mustExec(t, tx, `INSERT INTO observation (id, machine, basis, revision, access_path, access_version, access_created, smbios_uuid,
			talos_node_id, talos_cluster_id, assignment_evidence, running_version, configuration_digest, resource_version, unread, at)
		SELECT $1, $2, $3, $4, 'access/talos/' || cluster, 3, '2026-10-01T00:00:00Z', $5::uuid, $6, $7, $8, $9, $10, $11, $12::jsonb,
			now() + make_interval(secs => $13)
		FROM machine WHERE id = $2`, obs, ae.machine, basis, rev, vals[0], vals[1], vals[2], vals[3], vals[4], vals[5], vals[6],
		s.unread, secs)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return obs
}

// takenOverBy answers a second controller that took the operation over from generation 1.
func (ae *attemptEnv) takenOverBy(t *testing.T) *API {
	t.Helper()
	next := observer(t, ae.env, &fakeExecutor{}, options{})
	if _, err := next.takeOver(context.Background(), ae.op, 1, "controller-start"); err != nil {
		t.Fatal(err)
	}
	return next
}

// decide records a recovery-admin's decision accounting attempt, by the human h-approver, as the
// effect of its request: the act is written after the effect in the transaction.
func (ae *attemptEnv) decide(t *testing.T, a *API, attempt, start, end string, facts map[string]any) (accounted, error) {
	t.Helper()
	var human string
	if err := ae.db.QueryRow(`SELECT id FROM principal WHERE sub = 'h-approver'`).Scan(&human); err != nil {
		t.Fatal(err)
	}
	var r accounted
	err := a.inTx(context.Background(), func(tx *sql.Tx) error {
		act := id.New(id.Act)
		var err error
		r, err = a.accountByDecision(context.Background(), tx, accountingDecision{Attempt: attempt, DecidedBy: human, Act: act,
			Start: start, End: end, Facts: facts})
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(context.Background(), `INSERT INTO act (id, principal, principal_kind, via, action, subjects, epoch, at)
			SELECT $1, $2, 'human', 'tool', 'account-attempt', '{}', epoch, now() FROM installation_state`, act, human)
		return err
	})
	return r, err
}

// settleWindow is long enough after an attempt's transport deadline (60 s) for the settle floor (30 s).
const settleWindow = 2 * time.Minute

// The accounting decision (execution-recovery.md §5.2): a lost response is accounted only once its
// executor is taken over, the settle floor has elapsed since the latest of the transport deadline
// and the takeover, a recovery observation read the machine-configuration resource version at both
// ends of the settle, and a change of that version is explained. It is one accounting entry and
// record by decision and changes no state.
func TestAccountByDecision(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (*attemptEnv, *API, string) {
		ae := newAttemptEnv(t, "")
		att := ae.sent(t)
		if _, err := ae.a.recordResponse(context.Background(), att.Attempt, answer(talos.ClassTransport)); err != nil {
			t.Fatal(err)
		}
		return ae, ae.takenOverBy(t), att.Attempt
	}
	t.Run("accounted", func(t *testing.T) {
		t.Parallel()
		ae, next, att := setup(t)
		start := ae.observed(t, func(c *cobs) { c.purpose, c.shift = "recovery", settleWindow })
		end := ae.observed(t, func(c *cobs) { c.purpose, c.shift = "recovery", settleWindow+time.Minute })
		r, err := ae.decide(t, next, att, start, end, map[string]any{"basis": "the executor was taken over"})
		if err != nil {
			t.Fatal(err)
		}
		if n := count(t, ae.db, `SELECT count(*) FROM attempt_accounting c JOIN machine_event e ON e.machine = c.machine
				AND e.revision = c.revision AND e.kind = 'accounting'
			WHERE c.attempt = $1 AND c.basis = 'decision' AND c.role = 'recovery-admin' AND c.start_observation = $2
				AND c.end_observation = $3 AND c.revision = $4 AND c.facts->>'judgement' = 'true'
				AND c.facts->>'startVersion' = 'rv1' AND c.facts->>'endVersion' = 'rv1' AND c.facts ? 'settleFloor'
				AND e.entry->>'attempt' = $1 AND e.entry->>'basis' = 'decision'
				AND e.entry->'facts' = c.facts AND e.entry->>'start' = $2 AND e.entry->>'end' = $3`, att, start, end, r.Revision); n != 1 {
			t.Fatal("no decision accounting record and entry as recorded")
		}
		if state, _, _ := ae.state(t); state != "unresolved" {
			t.Fatalf("the operation is %s", state)
		}
		if _, err := ae.decide(t, next, att, start, end, nil); err == nil {
			t.Fatal("a second accounting of the attempt was recorded")
		}
	})
	t.Run("version moved and explained", func(t *testing.T) {
		t.Parallel()
		ae, next, att := setup(t)
		start := ae.observed(t, func(c *cobs) { c.purpose, c.shift = "recovery", settleWindow })
		end := ae.observed(t, func(c *cobs) { c.purpose, c.shift, c.resource = "recovery", settleWindow+time.Minute, "rv2" })
		var refused *outcomeRefused
		if _, err := ae.decide(t, next, att, start, end, nil); !errors.As(err, &refused) {
			t.Fatalf("a moved version with no explanation: %v; want a refusal", err)
		}
		for _, blank := range []any{"", " \t\n", 7, nil} {
			if _, err := ae.decide(t, next, att, start, end, map[string]any{"explanation": blank}); !errors.As(err, &refused) {
				t.Fatalf("a moved version explained by %#v: %v; want a refusal", blank, err)
			}
		}
		if _, err := ae.decide(t, next, att, start, end, map[string]any{"explanation": "the landed apply of the abandoned request"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, c := range []struct {
		name       string
		start, end time.Duration
		purpose    string
		owner      bool // the attempt's own executor decides
		cause      string
	}{
		{"floor not elapsed", time.Minute + 5*time.Second, time.Minute + 20*time.Second, "recovery", false, "floor"},
		{"not a recovery observation", settleWindow, settleWindow + time.Minute, "completion", false, "recovery observation"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ae, next, att := setup(t)
			start := ae.observed(t, func(o *cobs) { o.purpose, o.shift = "recovery", c.start })
			end := ae.observed(t, func(o *cobs) { o.purpose, o.shift = c.purpose, c.end })
			var refused *outcomeRefused
			if _, err := ae.decide(t, next, att, start, end, nil); !errors.As(err, &refused) {
				t.Fatalf("%s: %v; want a refusal", c.name, err)
			}
			if n := count(t, ae.db, `SELECT count(*) FROM attempt_accounting`); n != 0 {
				t.Fatal("an accounting was recorded")
			}
		})
	}
	// The end observation must be taken after the floor: one begun inside it and only recorded after
	// it does not satisfy §5.2 condition 3.
	t.Run("end observation begun inside the floor", func(t *testing.T) {
		t.Parallel()
		ae, next, att := setup(t)
		start := ae.observed(t, func(c *cobs) { c.purpose, c.shift = "recovery", settleWindow })
		end := ae.observed(t, func(c *cobs) {
			c.purpose, c.began, c.shift = "recovery", settleWindow+5*time.Second, settleWindow+time.Minute
		})
		var refused *outcomeRefused
		if _, err := ae.decide(t, next, att, start, end, nil); !errors.As(err, &refused) {
			t.Fatalf("an end observation begun 5 s after the first read: %v; want a refusal", err)
		}
		late := ae.observed(t, func(c *cobs) {
			c.purpose, c.began, c.shift = "recovery", settleWindow+time.Minute, settleWindow+90*time.Second
		})
		if _, err := ae.decide(t, next, att, start, late, nil); err != nil {
			t.Fatalf("an end observation begun after the floor: %v", err)
		}
	})
	// The first successful read of the machine after the deadline starts the floor: an observation
	// that obtained access but reached no node value is not one.
	t.Run("an unreachable observation starts no floor", func(t *testing.T) {
		t.Parallel()
		ae, next, att := setup(t)
		ae.observed(t, func(c *cobs) { c.purpose, c.shift, c.unreached = "recovery", 70*time.Second, true })
		start := ae.observed(t, func(c *cobs) { c.purpose, c.shift = "recovery", settleWindow })
		end := ae.observed(t, func(c *cobs) { c.purpose, c.shift = "recovery", settleWindow+5*time.Second })
		var refused *outcomeRefused
		if _, err := ae.decide(t, next, att, start, end, nil); !errors.As(err, &refused) {
			t.Fatalf("a floor counted from an unreachable observation: %v; want a refusal", err)
		}
	})
	// The settle starts at the first successful read after the deadline: a start that is a later read
	// would hide a change of the version between that read and itself.
	t.Run("start is not the first successful read", func(t *testing.T) {
		t.Parallel()
		ae, next, att := setup(t)
		first := ae.observed(t, func(c *cobs) { c.purpose, c.shift = "recovery", 70*time.Second })
		start := ae.observed(t, func(c *cobs) { c.purpose, c.shift, c.resource = "recovery", settleWindow, "rv2" })
		end := ae.observed(t, func(c *cobs) { c.purpose, c.shift, c.resource = "recovery", settleWindow+time.Minute, "rv2" })
		var refused *outcomeRefused
		if _, err := ae.decide(t, next, att, start, end, nil); !errors.As(err, &refused) {
			t.Fatalf("a start later than the first read hid a version move: %v; want a refusal", err)
		}
		if _, err := ae.decide(t, next, att, first, end, map[string]any{"explanation": "the landed apply of the abandoned request"}); err != nil {
			t.Fatalf("the first read as the start: %v", err)
		}
	})
	// The first read is the first begun, not the first recorded (§4.1 orders observations by their
	// start): one begun first and recorded after a later one is still the settle's start.
	t.Run("first read recorded after a later one", func(t *testing.T) {
		t.Parallel()
		ae, next, att := setup(t)
		first := ae.observed(t, func(c *cobs) {
			c.purpose, c.began, c.shift = "recovery", 65*time.Second, settleWindow
		})
		later := ae.observed(t, func(c *cobs) { c.purpose, c.shift, c.resource = "recovery", 80*time.Second, "rv2" })
		end := ae.observed(t, func(c *cobs) { c.purpose, c.shift, c.resource = "recovery", settleWindow+time.Minute, "rv2" })
		var refused *outcomeRefused
		if _, err := ae.decide(t, next, att, later, end, nil); !errors.As(err, &refused) {
			t.Fatalf("a read begun after the first as the start: %v; want a refusal", err)
		}
		if _, err := ae.decide(t, next, att, first, end, map[string]any{"explanation": "the landed apply of the abandoned request"}); err != nil {
			t.Fatalf("the first read begun, recorded last, as the start: %v", err)
		}
	})
	// The two ends of the settle are two observations, the end begun after the start was recorded:
	// one observation as both ends reads a version against itself and can never show it moved.
	t.Run("one observation at both ends", func(t *testing.T) {
		t.Parallel()
		ae, next, att := setup(t)
		ae.observed(t, func(c *cobs) { c.purpose, c.shift = "recovery", settleWindow-20*time.Second })
		both := ae.observed(t, func(c *cobs) {
			c.purpose, c.began, c.shift = "recovery", settleWindow+30*time.Second, settleWindow+time.Minute
		})
		var refused *outcomeRefused
		if _, err := ae.decide(t, next, att, both, both, nil); !errors.As(err, &refused) {
			t.Fatalf("one observation as both ends: %v; want a refusal", err)
		}
	})
	t.Run("end begun before the start was recorded", func(t *testing.T) {
		t.Parallel()
		ae, next, att := setup(t)
		ae.observed(t, func(c *cobs) { c.purpose, c.shift = "recovery", settleWindow-20*time.Second })
		start := ae.observed(t, func(c *cobs) {
			c.purpose, c.began, c.shift = "recovery", settleWindow-10*time.Second, settleWindow+80*time.Second
		})
		end := ae.observed(t, func(c *cobs) {
			c.purpose, c.began, c.shift = "recovery", settleWindow+30*time.Second, settleWindow+2*time.Minute
		})
		var refused *outcomeRefused
		if _, err := ae.decide(t, next, att, start, end, nil); !errors.As(err, &refused) {
			t.Fatalf("an end begun before the start was recorded: %v; want a refusal", err)
		}
	})
	t.Run("executor not taken over", func(t *testing.T) {
		t.Parallel()
		ae := newAttemptEnv(t, "")
		att := ae.sent(t)
		start := ae.observed(t, func(c *cobs) { c.purpose, c.shift = "recovery", settleWindow })
		end := ae.observed(t, func(c *cobs) { c.purpose, c.shift = "recovery", settleWindow+time.Minute })
		var refused *outcomeRefused
		if _, err := ae.decide(t, ae.a, att.Attempt, start, end, nil); !errors.As(err, &refused) {
			t.Fatalf("a decision while the executor runs: %v; want a refusal", err)
		}
	})
}

// reliedSQL is the classification's relied set a settle records for the observation row o: the
// revisions of the operation's attempt responses and accountings and of the observation, ascending.
const reliedSQL = `(SELECT jsonb_agg(x.r ORDER BY x.r) FROM (
		SELECT p.revision r FROM attempt_response p JOIN attempt a ON a.id = p.attempt WHERE a.operation = c.operation
		UNION SELECT k.revision FROM attempt_accounting k JOIN attempt a ON a.id = k.attempt WHERE a.operation = c.operation
		UNION SELECT o.revision) x)`

// The completion observation's decision (execution-recovery.md §4, §5, §5.1, choice §10.10).
func TestSettle(t *testing.T) {
	t.Parallel()
	// verifying: the controller sent, the node accepted. unresolved: the response was lost, the
	// executor taken over and the attempt accounted by a decision, as its new owner at generation 2.
	verifying := func(t *testing.T) (*attemptEnv, *API, int64) {
		ae := newAttemptEnv(t, "")
		ae.reach(t, "verifying")
		return ae, ae.a, 1
	}
	lostWith := func(t *testing.T, extra string) (*attemptEnv, *API, int64) {
		ae := newAttemptEnv(t, extra)
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
		return ae, next, 2
	}
	lost := func(t *testing.T) (*attemptEnv, *API, int64) { return lostWith(t, "") }
	// preDispatch is an observation showing the plan's pre-dispatch configuration.
	preDispatch := func(t *testing.T, ae *attemptEnv) string {
		return ae.observed(t, func(c *cobs) {
			if err := ae.db.QueryRow(`SELECT expected_digest FROM plan WHERE id = $1`, ae.pid).Scan(&c.digest); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("completed", func(t *testing.T) {
		t.Parallel()
		for name, setup := range map[string]func(*testing.T) (*attemptEnv, *API, int64){"verifying": verifying, "unresolved": lost} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				ae, a, gen := setup(t)
				obs := ae.observed(t, nil)
				r, err := a.settleOperation(context.Background(), ae.op, gen, obs)
				if err != nil {
					t.Fatal(err)
				}
				if r.State != "completed" || r.Outcome != "completed" {
					t.Fatalf("settled %+v", r)
				}
				if n := count(t, ae.db, `SELECT count(*) FROM operation o JOIN plan p ON p.id = o.plan
						JOIN release_machine rm ON rm.release = p.release AND rm.machine = p.machine
					WHERE o.id = $1 AND o.state = 'completed' AND o.error IS NULL AND o.result->>'appliedRelease' = p.release
						AND o.result->>'observation' = $2 AND o.result->>'digest' = encode(rm.configuration_digest, 'hex')`,
					ae.op, obs); n != 1 {
					t.Fatal("no completed operation with its result")
				}
				if n := count(t, ae.db, `SELECT count(*) FROM machine_state ms JOIN plan p ON p.machine = ms.machine
						JOIN release_machine rm ON rm.release = p.release AND rm.machine = p.machine
					WHERE p.id = $1 AND ms.applied_release = p.release AND ms.applied_digest = rm.configuration_digest
						AND ms.applied_source = 'operation' AND ms.baseline_revision = p.baseline_revision + 1
						AND ms.desired IS NOT DISTINCT FROM p.desired_release`, ae.pid); n != 1 {
					t.Fatal("Applied is not the plan's release and digest, the baseline advanced")
				}
				if n := count(t, ae.db, `SELECT count(*) FROM machine_event e JOIN machine m ON m.id = e.machine
						AND m.revision_counter = e.revision JOIN plan p ON p.id = $2
					WHERE e.machine = $1 AND e.kind = 'applied-change' AND e.entry->>'source' = 'operation'
						AND e.entry->>'plan' = p.id AND e.entry->>'to' = p.release`, ae.machine, ae.pid); n != 1 {
					t.Fatal("the applied change entry is not the machine's last")
				}
				if n := count(t, ae.db, `SELECT count(*) FROM operation_classification c JOIN machine_event e ON e.machine = c.machine
							AND e.revision = c.revision AND e.kind = 'classification' JOIN observation o ON o.id = c.observation
						WHERE c.operation = $1 AND c.outcome = 'completed' AND c.observation = $2 AND c.relied = `+reliedSQL+`
								AND e.entry->'relied' = c.relied AND jsonb_array_length(c.relied) >= 2
							AND c.owner_gen = $3 AND e.entry->>'outcome' = 'completed' AND e.entry->>'observation' = $2`,
					ae.op, obs, gen); n != 1 {
					t.Fatal("no completed classification record and entry as recorded")
				}
			})
		}
	})
	t.Run("unaccounted attempt", func(t *testing.T) {
		t.Parallel()
		ae := newAttemptEnv(t, "")
		ae.seedAttempt(t)
		mustExec(t, ae.db, `UPDATE operation SET state = 'sending' WHERE id = $1`, ae.op)
		mustExec(t, ae.db, `UPDATE operation SET state = 'verifying' WHERE id = $1`, ae.op)
		r, err := ae.a.settleOperation(context.Background(), ae.op, 1, ae.observed(t, nil))
		if err != nil {
			t.Fatal(err)
		}
		if r.State != "unresolved" || r.Outcome != "unresolved" {
			t.Fatalf("settled %+v", r)
		}
		if state, _, _ := ae.state(t); state != "unresolved" {
			t.Fatalf("the operation is %s", state)
		}
	})
	t.Run("contradicted", func(t *testing.T) {
		t.Parallel()
		ae, a, gen := verifying(t)
		obs := ae.observed(t, func(c *cobs) { c.digest = bytes.Repeat([]byte{0xee}, 32) })
		r, err := a.settleOperation(context.Background(), ae.op, gen, obs)
		if err != nil {
			t.Fatal(err)
		}
		if r.State != "failed" || r.Outcome != "failed" {
			t.Fatalf("settled %+v", r)
		}
		if n := count(t, ae.db, `SELECT count(*) FROM operation WHERE id = $1 AND state = 'failed' AND result IS NULL
			AND error->>'outcome' = 'failed' AND error->>'observation' = $2`, ae.op, obs); n != 1 {
			t.Fatal("no failed operation with its error")
		}
		if n := count(t, ae.db, `SELECT count(*) FROM operation_classification c JOIN machine_event e ON e.machine = c.machine
				AND e.revision = c.revision AND e.kind = 'classification' JOIN observation o ON o.id = c.observation
			WHERE c.operation = $1 AND c.outcome = 'failed' AND c.observation = $2 AND c.relied = `+reliedSQL+`
				AND e.entry->'relied' = c.relied AND jsonb_array_length(c.relied) >= 2
				AND c.owner_gen = $3 AND e.entry->>'outcome' = 'failed'`, ae.op, obs, gen); n != 1 {
			t.Fatal("no failed classification record and entry as recorded")
		}
		if n := count(t, ae.db, `SELECT count(*) FROM machine_state ms JOIN plan p ON p.machine = ms.machine
			WHERE p.id = $1 AND ms.applied_release IS NOT DISTINCT FROM (SELECT applied_release FROM machine_state WHERE machine = $2)
				AND ms.baseline_revision = p.baseline_revision`, ae.pid, ae.machine); n != 1 {
			t.Fatal("Applied changed")
		}
		// ER §4, §6.1: releasing the scope on a digest other than Applied's opens a drift record in
		// the same transaction, with its opening entry.
		if r.Drift != driftOpened {
			t.Fatalf("drift change %q", r.Drift)
		}
		if n := count(t, ae.db, `SELECT count(*) FROM drift_record d JOIN machine_state s ON s.machine = d.machine
				JOIN machine_event e ON e.machine = d.machine AND e.revision = d.opened AND e.kind = 'drift'
			WHERE d.machine = $1 AND d.closed IS NULL AND d.observation = $2 AND d.last_observation = $2
				AND d.applied_digest = s.applied_digest AND d.observed_digest = $3 AND d.late_landing IS NULL
				AND e.entry->>'change' = 'opened' AND (e.entry->>'record')::bigint = d.opened AND e.entry->>'observation' = $2`,
			ae.machine, obs, bytes.Repeat([]byte{0xee}, 32)); n != 1 {
			t.Fatal("no open drift record with its opening entry")
		}
	})
	// ER §6.1: a failure accounted by decision opens a record on its contradicting digest; a later
	// observation of its artifact is recorded on that record as its possible late landing, and a
	// further unrelated digest replaces the record's link with none.
	t.Run("late landing on an open record", func(t *testing.T) {
		t.Parallel()
		ae, a, gen := lost(t)
		failing := ae.observed(t, func(c *cobs) { c.digest = bytes.Repeat([]byte{0xee}, 32) })
		if r, err := a.settleOperation(context.Background(), ae.op, gen, failing); err != nil || r.Drift != driftOpened {
			t.Fatalf("settled %+v: %v", r, err)
		}
		observe := func(obs string) {
			t.Helper()
			tx, err := ae.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			mustExec(t, tx, `SELECT 1 FROM machine WHERE id = $1 FOR UPDATE`, ae.machine)
			if change, err := applyDrift(context.Background(), tx, ae.machine, epoch(t, ae.db), obs, false); err != nil ||
				change != driftObserved {
				t.Fatalf("drift change %q: %v", change, err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}
		landed := ae.observed(t, nil)
		observe(landed)
		if n := count(t, ae.db, `SELECT count(*) FROM drift_record d JOIN machine_event e ON e.machine = d.machine AND e.kind = 'drift'
				AND e.entry->>'change' = 'observed' AND e.entry->>'lateLanding' = $3
			WHERE d.machine = $1 AND d.observation = $2 AND d.last_observation = $4 AND d.late_landing = $3`,
			ae.machine, failing, ae.op, landed); n != 1 {
			t.Fatal("the late landing is not recorded on the open record")
		}
		unrelated := ae.observed(t, func(c *cobs) { c.digest = bytes.Repeat([]byte{0xdd}, 32) })
		observe(unrelated)
		if n := count(t, ae.db, `SELECT count(*) FROM drift_record WHERE machine = $1 AND last_observation = $2
			AND late_landing IS NULL`, ae.machine, unrelated); n != 1 {
			t.Fatal("the record keeps a late landing its latest observation cannot be")
		}
	})
	t.Run("safe to retry", func(t *testing.T) {
		t.Parallel()
		ae, a, gen := lost(t)
		obs := ae.observed(t, func(c *cobs) {
			var d []byte
			if err := ae.db.QueryRow(`SELECT expected_digest FROM plan WHERE id = $1`, ae.pid).Scan(&d); err != nil {
				t.Fatal(err)
			}
			c.digest = d
		})
		r, err := a.settleOperation(context.Background(), ae.op, gen, obs)
		if err != nil {
			t.Fatal(err)
		}
		if r.State != "unresolved" || r.Outcome != "safe-to-retry" || r.Revision == 0 {
			t.Fatalf("settled %+v", r)
		}
		if n := count(t, ae.db, `SELECT count(*) FROM operation_classification c JOIN machine_event e ON e.machine = c.machine
				AND e.revision = c.revision AND e.kind = 'classification' JOIN observation o ON o.id = c.observation
			WHERE c.operation = $1 AND c.outcome = 'safe-to-retry' AND c.observation = $2 AND c.revision = $3
				AND c.relied = `+reliedSQL+` AND e.entry->'relied' = c.relied AND jsonb_array_length(c.relied) >= 3
				AND c.owner_gen = 2 AND e.entry->>'outcome' = 'safe-to-retry'`, ae.op, obs, r.Revision); n != 1 {
			t.Fatal("no classification record and entry as recorded")
		}
	})
	// ER §9.2: the bound exhausted after a lost response leaves no further attempt and ends failed,
	// on the completion observation that would otherwise classify it safe to retry.
	t.Run("bound exhausted after a lost response", func(t *testing.T) {
		t.Parallel()
		ae, a, gen := lostWith(t, `,"maxAttempts":1`)
		obs := preDispatch(t, ae)
		r, err := a.settleOperation(context.Background(), ae.op, gen, obs)
		if err != nil {
			t.Fatal(err)
		}
		if r.State != "failed" || r.Outcome != "failed" {
			t.Fatalf("settled %+v", r)
		}
		if n := count(t, ae.db, `SELECT count(*) FROM operation_classification WHERE operation = $1 AND outcome = 'safe-to-retry'`,
			ae.op); n != 0 {
			t.Fatalf("%d safe-to-retry classifications", n)
		}
		if n := count(t, ae.db, `SELECT count(*) FROM attempt WHERE operation = $1`, ae.op); n != 1 {
			t.Fatalf("%d attempts", n)
		}
	})
	// ER §9.2, the §5 precedence: a completion observation that would classify a lost response safe
	// to retry fails the operation, with no retry classified, when the approval no longer passes or the
	// scope gate is closed. The bound exhausted is the case above; a definitive rejection is terminal
	// at its response (TestRecordResponse).
	for name, block := range map[string]func(*testing.T, *attemptEnv){
		"approval revoked": func(t *testing.T, ae *attemptEnv) {
			if rec := ae.revokeApproval(ae.human("h-recovery"), "k-revoke-approval-0123", ae.approval, `{"reason":"wrong"}`); rec.Code != http.StatusCreated {
				t.Fatalf("revocation: %d %s", rec.Code, rec.Body)
			}
		},
		"scope frozen": func(t *testing.T, ae *attemptEnv) {
			mustExec(t, ae.db, `UPDATE machine SET frozen = true WHERE id = $1`, ae.machine)
		},
	} {
		t.Run("no retry: "+name, func(t *testing.T) {
			t.Parallel()
			ae, a, gen := lost(t)
			block(t, ae)
			r, err := a.settleOperation(context.Background(), ae.op, gen, preDispatch(t, ae))
			if err != nil {
				t.Fatal(err)
			}
			if r.State != "failed" || r.Outcome != "failed" {
				t.Fatalf("settled %+v", r)
			}
			if n := count(t, ae.db, `SELECT count(*) FROM operation_classification WHERE operation = $1 AND outcome = 'safe-to-retry'`,
				ae.op); n != 0 {
				t.Fatalf("%d safe-to-retry classifications", n)
			}
		})
	}
	// ER §9.2, PA §16: an identity revocation after an attempt refuses its retry by comparison 1,
	// though the operation was classified safe to retry before it.
	t.Run("identity revoked after an attempt", func(t *testing.T) {
		t.Parallel()
		ae, a, gen := lost(t)
		if r, err := a.settleOperation(context.Background(), ae.op, gen, preDispatch(t, ae)); err != nil || r.Outcome != "safe-to-retry" {
			t.Fatalf("settled %+v: %v", r, err)
		}
		rec := revoke(ae.env, ae.human("h-recovery"), "k-revoke-identity-0123",
			`{"identity":"`+ae.principalOf("h-approver")+`","reason":"left"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("revocation: %d %s", rec.Code, rec.Body)
		}
		_, err := a.recordAttempt(context.Background(), ae.op, gen)
		var refused *attemptRefused
		if !errors.As(err, &refused) || refused.Comparison != "1" || refused.Cause != "the approver's identity is revoked" {
			t.Fatalf("retry after the revocation: %v", err)
		}
		if n := count(t, ae.db, `SELECT count(*) FROM attempt WHERE operation = $1`, ae.op); n != 1 {
			t.Fatalf("%d attempts", n)
		}
	})
	// ER §9.2: a retry's attempt on a machine recorded by SMBIOS UUID is refused by comparison 3,
	// nothing recorded, when an observation after its classification's shows another UUID, none, or
	// another Talos cluster ID; the control is a matching one, after which the retry is recorded.
	// The node-ID cases share the commitment's identityMatch expression (TestExecutorCommitmentIdentityKeys).
	for name, c := range map[string]func(*evSeed){
		"another SMBIOS UUID":      func(s *evSeed) { s.uuid = "1b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b" },
		"no SMBIOS UUID":           func(s *evSeed) { s.uuid = nil },
		"another Talos cluster":    func(s *evSeed) { s.clusterID = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" },
		"a matching one (control)": nil,
	} {
		t.Run("retry evidence identity: "+name, func(t *testing.T) {
			t.Parallel()
			ae, a, gen := lost(t)
			if r, err := a.settleOperation(context.Background(), ae.op, gen, preDispatch(t, ae)); err != nil || r.Outcome != "safe-to-retry" {
				t.Fatalf("settled %+v: %v", r, err)
			}
			ae.evidence(t, ae.pid, func(s *evSeed) {
				s.controller = a.d.owner.ID
				if c != nil {
					c(s)
				}
			})
			_, err := a.recordAttempt(context.Background(), ae.op, gen)
			if c == nil {
				if err != nil {
					t.Fatalf("the retry on matching evidence: %v", err)
				}
				return
			}
			var refused *attemptRefused
			if !errors.As(err, &refused) || refused.Comparison != "3" || refused.Cause != "a newer observation contradicts the evidence" {
				t.Fatalf("the retry on another identity: %v", err)
			}
			if n := count(t, ae.db, `SELECT count(*) FROM attempt WHERE operation = $1`, ae.op); n != 1 {
				t.Fatalf("%d attempts", n)
			}
		})
	}
	t.Run("safe to retry on a recovery observation", func(t *testing.T) {
		t.Parallel()
		ae, a, gen := lost(t)
		obs := ae.observed(t, func(c *cobs) {
			c.purpose = "recovery"
			if err := ae.db.QueryRow(`SELECT expected_digest FROM plan WHERE id = $1`, ae.pid).Scan(&c.digest); err != nil {
				t.Fatal(err)
			}
		})
		r, err := a.settleOperation(context.Background(), ae.op, gen, obs)
		if err != nil {
			t.Fatal(err)
		}
		if r.Outcome != "safe-to-retry" {
			t.Fatalf("settled %+v", r)
		}
	})
	// A takeover before the first attempt moves a committed operation to unresolved with no attempt;
	// a plan whose artifact digest is its pre-dispatch digest is then neither contradicted nor
	// completed by a recovery observation, and its first attempt is admitted by a classification.
	t.Run("safe to retry before the first attempt", func(t *testing.T) {
		t.Parallel()
		ae := newAttemptEnv(t, "")
		mustExec(t, ae.db, `ALTER TABLE release_machine DISABLE TRIGGER immutable_rows`)
		mustExec(t, ae.db, `UPDATE release_machine SET configuration_digest = p.expected_digest FROM plan p
			WHERE p.id = $1 AND release_machine.release = p.release AND release_machine.machine = p.machine`, ae.pid)
		next := ae.takenOverBy(t)
		obs := ae.observed(t, func(c *cobs) {
			c.purpose = "recovery"
			if err := ae.db.QueryRow(`SELECT expected_digest FROM plan WHERE id = $1`, ae.pid).Scan(&c.digest); err != nil {
				t.Fatal(err)
			}
		})
		r, err := next.settleOperation(context.Background(), ae.op, 2, obs)
		if err != nil {
			t.Fatal(err)
		}
		if r.State != "unresolved" || r.Outcome != "safe-to-retry" || r.Revision == 0 {
			t.Fatalf("settled %+v", r)
		}
		if n := count(t, ae.db, `SELECT count(*) FROM operation_classification WHERE operation = $1 AND observation = $2
			AND outcome = 'safe-to-retry'`, ae.op, obs); n != 1 {
			t.Fatal("no classification recorded")
		}
	})
	// ER §9.2: an operation with no recorded attempt, its artifact then applied out of band, is not
	// completed by a completion observation that shows the artifact; "completed" above is the same
	// observation completing an operation that sent.
	t.Run("no attempt not completed by its artifact applied", func(t *testing.T) {
		t.Parallel()
		ae := newAttemptEnv(t, "")
		next := ae.takenOverBy(t)
		obs := ae.observed(t, nil)
		r, err := next.settleOperation(context.Background(), ae.op, 2, obs)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == "completed" || r.Outcome == "completed" {
			t.Fatalf("settled %+v", r)
		}
		if n := count(t, ae.db, `SELECT count(*) FROM attempt WHERE operation = $1`, ae.op); n != 0 {
			t.Fatalf("%d attempts recorded", n)
		}
		if n := count(t, ae.db, `SELECT count(*) FROM operation_classification WHERE operation = $1 AND outcome = 'completed'`,
			ae.op); n != 0 {
			t.Fatal("a completed classification was recorded")
		}
		if n := count(t, ae.db, `SELECT count(*) FROM machine_state ms JOIN plan p ON p.machine = ms.machine
			WHERE p.id = $1 AND ms.applied_release IS NOT DISTINCT FROM p.release`, ae.pid); n != 0 {
			t.Fatal("Applied names the plan's release")
		}
		t.Logf("no attempt, completion observation of the artifact: %s/%s, not completed", r.State, r.Outcome)
	})
	t.Run("a recovery observation fails nothing", func(t *testing.T) {
		t.Parallel()
		ae, a, gen := lost(t)
		obs := ae.observed(t, func(c *cobs) { c.purpose, c.digest = "recovery", bytes.Repeat([]byte{0xee}, 32) })
		r, err := a.settleOperation(context.Background(), ae.op, gen, obs)
		if err != nil {
			t.Fatal(err)
		}
		if r.Outcome != "pending" || r.State != "unresolved" {
			t.Fatalf("settled %+v", r)
		}
	})
	t.Run("pending", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			name   string
			change func(*cobs)
		}{
			{"unread configuration", func(c *cobs) { c.unread = `{"configuration": {"cause": "unreadable"}}` }},
			{"another purpose", func(c *cobs) { c.purpose = "evidence" }},
		} {
			t.Run(c.name, func(t *testing.T) {
				t.Parallel()
				ae, a, gen := verifying(t)
				r, err := a.settleOperation(context.Background(), ae.op, gen, ae.observed(t, c.change))
				if err != nil {
					t.Fatal(err)
				}
				if r.Outcome != "pending" || r.State != "verifying" {
					t.Fatalf("settled %+v", r)
				}
				if state, _, _ := ae.state(t); state != "verifying" {
					t.Fatalf("the operation is %s", state)
				}
			})
		}
		t.Run("ordered before the last accounting", func(t *testing.T) {
			t.Parallel()
			ae := newAttemptEnv(t, "")
			att := ae.sent(t)
			obs := ae.observed(t, nil)
			if _, err := ae.a.recordResponse(context.Background(), att.Attempt, answer(talos.ClassAccepted)); err != nil {
				t.Fatal(err)
			}
			r, err := ae.a.settleOperation(context.Background(), ae.op, 1, obs)
			if err != nil {
				t.Fatal(err)
			}
			if r.Outcome != "pending" || r.State != "verifying" {
				t.Fatalf("settled %+v", r)
			}
		})
		// ER §4, §9.2: a recorded higher-basis read differing on a bound postcondition blocks the
		// completion read, for the digest, the identity and the assignment revision (the PoC binds no
		// health check, choice §10.33).
		for name, differ := range map[string]func(*cobs){
			"a later observation disagrees":                 func(c *cobs) { c.digest = bytes.Repeat([]byte{0xee}, 32) },
			"a later observation of another machine":        func(c *cobs) { c.uuid = "1b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b" },
			"a later observation of another assignment rev": func(c *cobs) { c.assignment = "999" },
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				ae, a, gen := verifying(t)
				obs := ae.observed(t, nil)
				ae.observed(t, func(c *cobs) { c.purpose = "drift"; differ(c) })
				r, err := a.settleOperation(context.Background(), ae.op, gen, obs)
				if err != nil {
					t.Fatal(err)
				}
				if r.Outcome != "pending" || r.State != "verifying" {
					t.Fatalf("settled %+v", r)
				}
			})
		}
	})
	// ER §4, §9.2: higher-basis reads agreeing with a completion read that contradicts the
	// postconditions block nothing, so repeated reads confirming the failure let the operation fail.
	t.Run("a failure confirmed by later agreeing reads", func(t *testing.T) {
		t.Parallel()
		ae, a, gen := verifying(t)
		other := func(c *cobs) { c.digest = bytes.Repeat([]byte{0xee}, 32) }
		obs := ae.observed(t, other)
		for range 2 {
			ae.observed(t, func(c *cobs) { c.purpose = "drift"; other(c) })
		}
		r, err := a.settleOperation(context.Background(), ae.op, gen, obs)
		if err != nil {
			t.Fatal(err)
		}
		if r.Outcome != "failed" || r.State != "failed" {
			t.Fatalf("settled %+v", r)
		}
	})
	// A later observation disagrees on the identity under the machine's own key (§3.3 comparison 3):
	// a machine keyed by its SMBIOS UUID is not another machine for reading another Talos node ID.
	t.Run("a later observation of another node ID under the SMBIOS key", func(t *testing.T) {
		t.Parallel()
		ae, a, gen := verifying(t)
		mustExec(t, ae.db, `UPDATE machine SET smbios_uuid = COALESCE(smbios_uuid, gen_random_uuid()), talos_node_id = NULL
			WHERE id = $1`, ae.machine)
		obs := ae.observed(t, nil)
		ae.observed(t, func(c *cobs) { c.purpose, c.node = "drift", "another-node" })
		r, err := a.settleOperation(context.Background(), ae.op, gen, obs)
		if err != nil {
			t.Fatal(err)
		}
		if r.Outcome != "completed" || r.State != "completed" {
			t.Fatalf("settled %+v; a later observation of the same machine blocked the completion", r)
		}
	})
	t.Run("past the verification deadline", func(t *testing.T) {
		t.Parallel()
		ae, a, gen := verifying(t)
		obs := ae.observed(t, func(c *cobs) {
			c.shift, c.unread = 11*time.Minute, `{"configuration": {"cause": "unreadable"}}`
		})
		r, err := a.settleOperation(context.Background(), ae.op, gen, obs)
		if err != nil {
			t.Fatal(err)
		}
		if r.Outcome != "unresolved" || r.State != "unresolved" {
			t.Fatalf("settled %+v", r)
		}
	})
	t.Run("refusals", func(t *testing.T) {
		t.Parallel()
		ae, _, _ := verifying(t)
		next := observer(t, ae.env, &fakeExecutor{}, options{})
		obs := ae.observed(t, nil)
		var refused *outcomeRefused
		if _, err := next.settleOperation(context.Background(), ae.op, 1, obs); !errors.As(err, &refused) {
			t.Fatalf("settle by a process that does not own the operation: %v", err)
		}
		if _, err := ae.a.settleOperation(context.Background(), ae.op, 2, obs); !errors.As(err, &refused) {
			t.Fatalf("settle naming another generation: %v", err)
		}
	})
}
