package api

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// commitEnv is a planEnv with an apply-config plan of the machine's Desired, and a to commit it
// as the controller of the current epoch.
type commitEnv struct {
	*planEnv
	a                          *API
	pid, approval              string
	expires                    time.Time
	keys                       int
	machine2, plan2, approval2 string // a second machine of the cluster and its approved apply-config plan
}

func newCommitEnv(t *testing.T, extra string) *commitEnv {
	t.Helper()
	ce := &commitEnv{planEnv: newPlanEnv(t)}
	b := decode[planBody](t, ce.plan(ce.robot, "k-apply-0123456789ab", applyBody(ce.target.rel, ce.machine, extra)), http.StatusCreated)
	ce.pid, ce.expires = b.ID, b.ExpiresAt
	ce.a = observer(t, ce.env, &fakeExecutor{}, options{})
	return ce
}

func (ce *commitEnv) key() string {
	ce.keys++
	return "k-commitment-" + string(rune('a'+ce.keys)) + "-0123456789"
}

// approveIt approves the plan through T5a.
func (ce *commitEnv) approveIt(t *testing.T) {
	t.Helper()
	ce.approval = decode[approvalBody](t, ce.approve(ce.human("h-approver"), ce.key(), ce.pid), http.StatusCreated).ID
}

// second gives the target release's second machine an Applied and Desired of the target release
// and an approved apply-config plan of it: another plan of the cluster's rollout scope.
func (ce *commitEnv) second(t *testing.T) {
	t.Helper()
	ce.machine2 = ce.target.machine2
	mustExec(t, ce.db, `UPDATE machine_state SET desired = $2, applied_release = $2, applied_digest = $3, applied_source = 'adoption',
		baseline_revision = 1 WHERE machine = $1`, ce.machine2, ce.target.rel, bytes.Repeat([]byte{8}, 32))
	ce.plan2 = decode[planBody](t, ce.plan(ce.robot, "k-apply2-0123456789ab", applyBody(ce.target.rel, ce.machine2, "")),
		http.StatusCreated).ID
	ce.approval2 = decode[approvalBody](t, ce.approve(ce.human("h-approver"), ce.key(), ce.plan2), http.StatusCreated).ID
}

// evSeed is one observation of a plan's machine as an observer records it: its start entry, begun
// age ago, and unless startOnly its observation entry, showing these values.
type evSeed struct {
	age                       time.Duration
	purpose, controller, plan string
	uuid, nodeID              any
	clusterID                 string
	digest                    []byte
	version                   string
	startOnly                 bool
	unread                    string // the observation's unread object, "{}" when empty
}

// evidence records an observation of plan's machine for its dispatch, by ce.a, showing the
// machine's identity and cluster, the plan's expected digest and a running version of the
// release's contract minor, as change leaves them, and answers its id ("" for a start only).
func (ce *commitEnv) evidence(t *testing.T, plan string, change func(*evSeed)) string {
	t.Helper()
	var machine, route string
	s := evSeed{purpose: "evidence", controller: ce.a.d.owner.ID, plan: plan, version: "v1.13.6", unread: "{}"}
	var uuid, nodeID sql.NullString
	if err := ce.db.QueryRow(`SELECT p.machine, p.route::text, m.smbios_uuid::text, m.talos_node_id, c.talos_cluster_id,
			p.expected_digest
		FROM plan p JOIN machine m ON m.id = p.machine JOIN cluster c ON c.id = p.cluster WHERE p.id = $1`, plan).Scan(
		&machine, &route, &uuid, &nodeID, &s.clusterID, &s.digest); err != nil {
		t.Fatal(err)
	}
	s.uuid, s.nodeID = uuid, nodeID
	if change != nil {
		change(&s)
	}
	var startPlan any
	if s.purpose == "evidence" || s.purpose == "recovery" { // a recovery observation may name a plan
		startPlan = s.plan
	}
	tx, err := ce.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var basis int64
	if err := tx.QueryRow(`UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1 RETURNING revision_counter`,
		machine).Scan(&basis); err != nil {
		t.Fatal(err)
	}
	mustExec(t, tx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		SELECT $1, $2, epoch, 'observation-started', jsonb_build_object('purpose', $3::text, 'plan', $4::text, 'endpoint', $5::text,
			'controller', $6::text), now() - make_interval(secs => $7)
		FROM installation_state`, machine, basis, s.purpose, startPlan, route, s.controller, s.age.Seconds())
	mustExec(t, tx, `INSERT INTO observation_start (machine, revision, purpose, plan, endpoint, controller)
		VALUES ($1, $2, $3, $4, $5, $6)`, machine, basis, s.purpose, startPlan, route, s.controller)
	obs := ""
	if !s.startOnly {
		obs = id.New(id.Observation)
		var rev int64
		if err := tx.QueryRow(`UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1 RETURNING revision_counter`,
			machine).Scan(&rev); err != nil {
			t.Fatal(err)
		}
		mustExec(t, tx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
			SELECT $1, $2, epoch, 'observation', jsonb_build_object('observation', $3::text, 'purpose', $4::text, 'basis', $5::bigint), now()
			FROM installation_state`, machine, rev, obs, s.purpose, basis)
		mustExec(t, tx, `INSERT INTO observation (id, machine, basis, revision, access_path, access_version, access_created,
				smbios_uuid, talos_node_id, talos_cluster_id, running_version, configuration_digest, unread, at)
			SELECT $1, $2, $3, $4, 'access/talos/' || cluster, 3, '2026-10-01T00:00:00Z', $5::uuid, $6, NULLIF($7, ''), NULLIF($8, ''), $9,
				$10::jsonb, now()
			FROM machine WHERE id = $2`,
			obs, machine, basis, rev, s.uuid, s.nodeID, s.clusterID, s.version, s.digest, s.unread)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return obs
}

// ready approves the plan and records its evidence.
func (ce *commitEnv) ready(t *testing.T) string {
	t.Helper()
	ce.approveIt(t)
	return ce.evidence(t, ce.pid, nil)
}

var allComparisons = []string{"0", "1", "2", "3", "4", "5", "6"}

// T6's commitment (persistence-api.md §5; execution-recovery.md §3.2): an approved apply-config plan
// whose evidence, recorded for it by this controller after the approval and within the plan's age,
// shows the machine's identity, the plan's expected digest and the release's contract minor is
// committed: one apply-config operation in committed, owned by the controller at generation 1 in
// the current epoch; the plan committed to it; and one commitment entry naming the plan, the
// operation, the approval, the evidence observation, the controller, the generation and the seven
// comparisons. Nothing else is written: no refusal, no machine state change.
func TestCommitment(t *testing.T) {
	t.Parallel()
	ce := newCommitEnv(t, "")
	obs := ce.ready(t)
	ce.evidence(t, ce.pid, func(s *evSeed) { s.startOnly = true }) // a start with no observation does not count
	var before string
	if err := ce.db.QueryRow(`SELECT row_to_json(s)::text FROM machine_state s WHERE machine = $1`, ce.machine).Scan(&before); err != nil {
		t.Fatal(err)
	}
	r, err := ce.a.commitPlan(context.Background(), ce.pid)
	if err != nil {
		t.Fatal(err)
	}
	if r.Plan != ce.pid || r.Observation != obs || id.MustHave(r.Operation, id.Operation) != nil {
		t.Fatalf("committed %+v, observation %s", r, obs)
	}
	ep := epoch(t, ce.db)
	if n := count(t, ce.db, `SELECT count(*) FROM machine_event e
			JOIN operation o ON o.id = $5
			JOIN plan_state s ON s.plan = $4
		WHERE e.machine = $1 AND e.revision = $2 AND e.kind = 'commitment' AND e.epoch = $3
			AND e.entry = jsonb_build_object('plan', $4::text, 'operation', $5::text, 'approval', $6::text, 'observation', $7::text,
				'controller', $8::text, 'generation', 1, 'comparisons', to_jsonb($9::text[]))
			AND o.kind = 'apply-config' AND o.state = 'committed' AND o.epoch = $3 AND o.owner = $8 AND o.owner_gen = 1
			AND o.owner_epoch = $3 AND o.lease_until IS NULL AND o.plan = $4 AND o.machine = $1 AND o.cluster = $10
			AND o.created_at = e.at AND o.result IS NULL AND o.error IS NULL AND o.created_by IS NULL
			AND s.state = 'committed' AND s.operation = $5 AND s.approval = $6 AND s.revision = 3 AND s.updated_at = e.at`,
		ce.machine, r.Revision, ep, ce.pid, r.Operation, ce.approval, obs, ce.a.d.owner.ID, allComparisons, ce.cluster); n != 1 {
		t.Fatal("no commitment entry, operation and plan state as committed")
	}
	if n := count(t, ce.db, `SELECT count(*) FROM machine WHERE id = $1 AND revision_counter = $2`, ce.machine, r.Revision); n != 1 {
		t.Fatal("the revision counter is not the commitment entry's")
	}
	if n := count(t, ce.db, `SELECT count(*) FROM machine_event WHERE kind = 'refusal'`); n != 0 {
		t.Fatalf("%d refusal entries", n)
	}
	if n := count(t, ce.db, `SELECT count(*) FROM machine_state s WHERE machine = $1 AND row_to_json(s)::text = $2`,
		ce.machine, before); n != 1 {
		t.Fatal("the machine state changed")
	}
}

// A machine in recovery mode commits only once its scope is released in the current epoch (§3.2
// comparison 6); a newer observation that agrees with the evidence, or that left a value unread,
// does not contradict it (comparison 3).
func TestCommitmentAccepts(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		setup func(*testing.T, *commitEnv)
	}{
		{"recovery mode released", func(t *testing.T, ce *commitEnv) {
			ce.ready(t)
			mustExec(t, ce.db, `UPDATE installation_state SET recovery_mode = true`)
			mustExec(t, ce.db, `UPDATE machine SET scope_state = 'released' WHERE id = $1`, ce.machine)
		}},
		{"a newer agreeing observation", func(t *testing.T, ce *commitEnv) {
			ce.ready(t)
			ce.evidence(t, ce.pid, func(s *evSeed) { s.purpose, s.controller = "drift", "another" })
		}},
		{"a newer observation reading nothing", func(t *testing.T, ce *commitEnv) {
			ce.ready(t)
			ce.evidence(t, ce.pid, func(s *evSeed) {
				s.purpose, s.uuid, s.nodeID, s.clusterID, s.digest, s.version = "drift", nil, nil, "", nil, ""
				s.unread = `{"identity":{"cause":"x"},"configuration":{"cause":"x"},"runningVersion":{"cause":"x"}}`
			})
		}},
		{"an older contradicting observation", func(t *testing.T, ce *commitEnv) {
			ce.approveIt(t)
			ce.evidence(t, ce.pid, func(s *evSeed) { s.purpose, s.controller, s.digest = "drift", "another", bytes.Repeat([]byte{9}, 32) })
			ce.evidence(t, ce.pid, nil)
		}},
		{"another controller's evidence for another plan", func(t *testing.T, ce *commitEnv) {
			ce.ready(t)
			ce.second(t)
			ce.evidence(t, ce.plan2, func(s *evSeed) { s.controller = "another" })
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ce := newCommitEnv(t, "")
			c.setup(t, ce)
			if _, err := ce.a.commitPlan(context.Background(), ce.pid); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// wantRefused holds err to a refusal by comparison with cause, naming observation when it is not
// empty, recorded as the machine's last timeline entry: a refusal entry naming T6, the comparison,
// the plan, the cause and the controller. Nothing else is written: no operation for the plan, which
// is not committed.
func (ce *commitEnv) wantRefused(t *testing.T, err error, comparison, cause, observation string) {
	t.Helper()
	var refused *commitRefused
	if !errors.As(err, &refused) || refused.Comparison != comparison || refused.Cause != cause || refused.Observation != observation {
		t.Fatalf("got %v, want a refusal by comparison %s: %s (%q)", err, comparison, cause, observation)
	}
	var obs any
	if observation != "" {
		obs = observation
	}
	if n := count(t, ce.db, `SELECT count(*) FROM machine_event e JOIN machine m ON m.id = e.machine AND m.revision_counter = e.revision
		WHERE e.machine = $1 AND e.kind = 'refusal' AND e.epoch = (SELECT epoch FROM installation_state)
			AND e.entry = jsonb_strip_nulls(jsonb_build_object('transaction', 'T6', 'comparison', $2::text, 'plan', $3::text,
				'cause', $4::text, 'controller', $5::text, 'observation', $6::text))`,
		ce.machine, comparison, ce.pid, cause, ce.a.d.owner.ID, obs); n != 1 {
		t.Fatal("the machine's last entry is not the refusal entry")
	}
	if n := count(t, ce.db, `SELECT count(*) FROM operation WHERE plan = $1`, ce.pid); n != 0 {
		t.Fatalf("%d operations for the plan", n)
	}
	if n := count(t, ce.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'commitment'`, ce.machine); n != 0 {
		t.Fatalf("%d commitment entries", n)
	}
}

// T6's refusals (execution-recovery.md §3.2): each comparison that fails is a refusal entry recorded
// after the refused transaction, and nothing else is written.
func TestCommitmentRefusals(t *testing.T) {
	t.Parallel()
	ready := func(t *testing.T, ce *commitEnv) { ce.ready(t) }
	evWith := func(change func(*evSeed)) func(*testing.T, *commitEnv) string {
		return func(t *testing.T, ce *commitEnv) string {
			ce.approveIt(t)
			return ce.evidence(t, ce.pid, change)
		}
	}
	revokeApprover := func(t *testing.T, ce *commitEnv) {
		revocationOf(t, revoke(ce.env, ce.human("h-recovery"), ce.key(),
			`{"identity":"`+ce.principalOf("h-approver")+`","reason":"left"}`))
	}
	for _, c := range []struct {
		name, extra, comparison, cause string
		setup                          func(*testing.T, *commitEnv)
		named                          func(*testing.T, *commitEnv) string // the refusal names this observation
	}{
		{name: "proposed", comparison: "1", cause: "the plan is proposed", setup: func(t *testing.T, ce *commitEnv) {
			ce.evidence(t, ce.pid, nil)
		}},
		{name: "approval revoked", comparison: "1", cause: "the plan is revoked", setup: func(t *testing.T, ce *commitEnv) {
			ready(t, ce)
			if rec := ce.revokeApproval(ce.human("h-recovery"), ce.key(), ce.approval, `{"reason":"wrong"}`); rec.Code != http.StatusCreated {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
		}},
		{name: "cancelled", comparison: "1", cause: "the plan is cancelled", setup: func(t *testing.T, ce *commitEnv) {
			ready(t, ce)
			decode[cancellationBody](t, ce.cancel(ce.human("h-approver"), ce.key(), ce.pid, `{"reason":"not now"}`), http.StatusOK)
		}},
		{name: "approver revoked", comparison: "1", cause: "the approver's identity is revoked", setup: func(t *testing.T, ce *commitEnv) {
			ready(t, ce)
			revokeApprover(t, ce)
		}},
		{name: "expired", extra: `,"expiresInSeconds":1`, comparison: "1", cause: "the plan has expired",
			setup: func(t *testing.T, ce *commitEnv) {
				ready(t, ce)
				time.Sleep(time.Until(ce.expires.Add(100 * time.Millisecond)))
			}},
		{name: "assignment changed", comparison: "2", cause: "the machine's assignment revision changed",
			setup: func(t *testing.T, ce *commitEnv) {
				ready(t, ce)
				rev := id.New(id.AssignmentRevision)
				mustExec(t, ce.db, `INSERT INTO assignment_revision (id, cluster, machine, author, created_at) VALUES ($1, $2, $3, $4, now())`,
					rev, ce.cluster, ce.machine, ce.seed)
				mustExec(t, ce.db, `UPDATE assignment SET head_revision_id = $2, head_revision = 2 WHERE machine = $1`, ce.machine, rev)
			}},
		{name: "Desired changed", comparison: "2", cause: "the machine's Desired release changed", setup: func(t *testing.T, ce *commitEnv) {
			ready(t, ce)
			mustExec(t, ce.db, `UPDATE machine_state SET desired = $2 WHERE machine = $1`, ce.machine, ce.applied.rel)
		}},
		{name: "baseline changed", comparison: "2", cause: "the machine's baseline revision changed",
			setup: func(t *testing.T, ce *commitEnv) {
				ready(t, ce)
				mustExec(t, ce.db, `UPDATE machine_state SET baseline_revision = 5 WHERE machine = $1`, ce.machine)
			}},
		{name: "another machine", comparison: "3", cause: "the evidence shows another machine identity",
			named: evWith(func(s *evSeed) { s.uuid = "1b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b" })},
		{name: "no identity", comparison: "3", cause: "the evidence shows another machine identity",
			named: evWith(func(s *evSeed) { s.uuid = nil })},
		{name: "another cluster", comparison: "3", cause: "the evidence shows another machine identity",
			named: evWith(func(s *evSeed) { s.clusterID = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" })},
		{name: "another digest", comparison: "3", cause: "the evidence shows a configuration digest other than the plan's",
			named: evWith(func(s *evSeed) { s.digest = bytes.Repeat([]byte{9}, 32) })},
		{name: "no digest", comparison: "3", cause: "the evidence shows a configuration digest other than the plan's",
			named: evWith(func(s *evSeed) { s.digest = nil })},
		{name: "another minor", comparison: "3", cause: "the evidence shows a running Talos minor other than the release's contract",
			named: evWith(func(s *evSeed) { s.version = "v1.12.4" })},
		{name: "no version", comparison: "3", cause: "the evidence shows a running Talos minor other than the release's contract",
			named: evWith(func(s *evSeed) { s.version = "" })},
		{name: "a newer observation shows another digest", comparison: "3", cause: "a newer observation contradicts the evidence",
			named: func(t *testing.T, ce *commitEnv) string {
				ce.ready(t)
				return ce.evidence(t, ce.pid, func(s *evSeed) {
					s.purpose, s.controller, s.digest = "drift", "another", bytes.Repeat([]byte{9}, 32)
				})
			}},
		{name: "a newer observation shows another machine", comparison: "3", cause: "a newer observation contradicts the evidence",
			named: func(t *testing.T, ce *commitEnv) string {
				ce.ready(t)
				return ce.evidence(t, ce.pid, func(s *evSeed) {
					s.purpose, s.controller, s.nodeID, s.uuid = "drift", "another", "node-b", "1b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b"
				})
			}},
		{name: "a newer observation shows another minor", comparison: "3", cause: "a newer observation contradicts the evidence",
			named: func(t *testing.T, ce *commitEnv) string {
				ce.ready(t)
				return ce.evidence(t, ce.pid, func(s *evSeed) { s.purpose, s.controller, s.version = "drift", "another", "v1.14.0" })
			}},
		{name: "scope held", comparison: "4", cause: "an operation holds the machine scope", setup: func(t *testing.T, ce *commitEnv) {
			other := decode[planBody](t, ce.plan(ce.robot, "k-apply-other-0123456", applyBody(ce.target.rel, ce.machine, "")),
				http.StatusCreated).ID
			decode[approvalBody](t, ce.approve(ce.human("h-approver"), ce.key(), other), http.StatusCreated)
			ce.commitAs(t, other)
			ready(t, ce)
		}},
		{name: "rollout scope held", comparison: "5", cause: "an operation holds the cluster's rollout scope",
			setup: func(t *testing.T, ce *commitEnv) {
				ce.second(t)
				ce.evidence(t, ce.plan2, nil)
				if _, err := ce.a.commitPlan(context.Background(), ce.plan2); err != nil {
					t.Fatal(err)
				}
				ready(t, ce)
			}},
		{name: "rollout scope held and frozen", comparison: "5", cause: "an operation holds the cluster's rollout scope",
			setup: func(t *testing.T, ce *commitEnv) { // comparisons run in order: 5 before 6
				ce.second(t)
				ce.evidence(t, ce.plan2, nil)
				if _, err := ce.a.commitPlan(context.Background(), ce.plan2); err != nil {
					t.Fatal(err)
				}
				ready(t, ce)
				mustExec(t, ce.db, `UPDATE machine SET frozen = true WHERE id = $1`, ce.machine)
			}},
		{name: "frozen", comparison: "6", cause: "the machine scope is frozen", setup: func(t *testing.T, ce *commitEnv) {
			ready(t, ce)
			mustExec(t, ce.db, `UPDATE machine SET frozen = true WHERE id = $1`, ce.machine)
		}},
		{name: "scope blocked", comparison: "6", cause: "the machine scope is blocked", setup: func(t *testing.T, ce *commitEnv) {
			ready(t, ce)
			mustExec(t, ce.db, `UPDATE machine SET scope_state = 'blocked' WHERE id = $1`, ce.machine)
		}},
		{name: "recovery mode", comparison: "6", cause: "recovery mode is in effect and the scope is not released",
			setup: func(t *testing.T, ce *commitEnv) {
				ready(t, ce)
				mustExec(t, ce.db, `UPDATE installation_state SET recovery_mode = true`)
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ce := newCommitEnv(t, c.extra)
			named := ""
			if c.named != nil {
				named = c.named(t, ce)
			} else {
				c.setup(t, ce)
			}
			_, err := ce.a.commitPlan(context.Background(), ce.pid)
			ce.wantRefused(t, err, c.comparison, c.cause, named)
		})
	}
}

// A refusal is not the plan's outcome (unlike an adoption's): a refused commitment records its
// refusal each time it is refused, and commits once the comparison passes.
func TestCommitmentAfterRefusal(t *testing.T) {
	t.Parallel()
	ce := newCommitEnv(t, "")
	ce.ready(t)
	mustExec(t, ce.db, `UPDATE machine SET frozen = true WHERE id = $1`, ce.machine)
	for range 2 {
		_, err := ce.a.commitPlan(context.Background(), ce.pid)
		ce.wantRefused(t, err, "6", "the machine scope is frozen", "")
	}
	if n := count(t, ce.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'refusal'`, ce.machine); n != 2 {
		t.Fatalf("%d refusal entries", n)
	}
	mustExec(t, ce.db, `UPDATE machine SET frozen = false WHERE id = $1`, ce.machine)
	if _, err := ce.a.commitPlan(context.Background(), ce.pid); err != nil {
		t.Fatal(err)
	}
}

// wantAlreadyCommitted checks that err found the plan already committed (comparison 0): not a
// refusal, no refusal entry, and the one operation left.
func (ce *commitEnv) wantAlreadyCommitted(t *testing.T, err error) {
	t.Helper()
	var refused *commitRefused
	if !errors.Is(err, errAlreadyCommitted) || errors.As(err, &refused) {
		t.Fatalf("got %v, want the plan already committed", err)
	}
	if n := count(t, ce.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'refusal'`, ce.machine); n != 0 {
		t.Fatalf("%d refusal entries", n)
	}
	if n := count(t, ce.db, `SELECT count(*) FROM operation WHERE plan = $1`, ce.pid); n != 1 {
		t.Fatalf("%d operations", n)
	}
}

// Comparison 0: a second commitment of a committed plan, sequential or racing, finds its operation
// (DS row 009) and records nothing.
func TestCommitmentSecond(t *testing.T) {
	t.Parallel()
	t.Run("sequential", func(t *testing.T) {
		t.Parallel()
		ce := newCommitEnv(t, "")
		ce.ready(t)
		if _, err := ce.a.commitPlan(context.Background(), ce.pid); err != nil {
			t.Fatal(err)
		}
		_, err := ce.a.commitPlan(context.Background(), ce.pid)
		ce.wantAlreadyCommitted(t, err)
	})
	t.Run("racing", func(t *testing.T) {
		t.Parallel()
		ce := newCommitEnv(t, "")
		ce.ready(t)
		errs := make(chan error, 2)
		for range 2 {
			go func() {
				_, err := ce.a.commitPlan(context.Background(), ce.pid)
				errs <- err
			}()
		}
		var failed error
		for range 2 {
			if err := <-errs; err != nil {
				if failed != nil {
					t.Fatalf("both commitments failed: %v; %v", failed, err)
				}
				failed = err
			}
		}
		ce.wantAlreadyCommitted(t, failed)
	})
}

// A refusal whose commitment rolled back before another committed the plan is not recorded after
// it: the refusal entry's transaction finds the plan committed under the machine's lock.
func TestCommitmentRefusalAfterCommitment(t *testing.T) {
	t.Parallel()
	ce := newCommitEnv(t, "")
	ce.ready(t)
	if _, err := ce.a.commitPlan(context.Background(), ce.pid); err != nil {
		t.Fatal(err)
	}
	err := ce.a.recordCommitRefusal(context.Background(), ce.machine, ce.pid,
		&commitRefused{Comparison: "6", Cause: "the machine scope is frozen"})
	ce.wantAlreadyCommitted(t, err)
}

// wantNoEvidence checks that err rolled the commitment back for want of evidence: not a refusal, no
// refusal entry and no operation for the plan.
func (ce *commitEnv) wantNoEvidence(t *testing.T, err error) {
	t.Helper()
	var refused *commitRefused
	if !errors.Is(err, errNoEvidence) || errors.As(err, &refused) {
		t.Fatalf("got %v, want no evidence and no refusal", err)
	}
	if n := count(t, ce.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'refusal'`, ce.machine); n != 0 {
		t.Fatalf("%d refusal entries", n)
	}
	if n := count(t, ce.db, `SELECT count(*) FROM operation WHERE plan = $1`, ce.pid); n != 0 {
		t.Fatalf("%d operations for the plan", n)
	}
}

// Evidence that is missing, recorded for another plan or by another controller, begun before the
// approval, older than the plan allows or that left the identity, the configuration or the running
// version unread is no evidence (§3.1 item 1, §3.2 comparison 3): the commitment rolls back without
// a refusal entry, and fresh evidence commits it.
func TestCommitmentNoEvidence(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		setup func(*testing.T, *commitEnv)
	}{
		{"no observation", func(t *testing.T, ce *commitEnv) { ce.approveIt(t) }},
		{"start only", func(t *testing.T, ce *commitEnv) {
			ce.approveIt(t)
			ce.evidence(t, ce.pid, func(s *evSeed) { s.startOnly = true })
		}},
		{"a drift observation", func(t *testing.T, ce *commitEnv) {
			ce.approveIt(t)
			ce.evidence(t, ce.pid, func(s *evSeed) { s.purpose = "drift" })
		}},
		{"a recovery observation naming the plan", func(t *testing.T, ce *commitEnv) {
			ce.approveIt(t)
			ce.evidence(t, ce.pid, func(s *evSeed) { s.purpose = "recovery" })
		}},
		{"another plan's evidence", func(t *testing.T, ce *commitEnv) {
			other := decode[planBody](t, ce.plan(ce.robot, "k-apply-other-0123456", applyBody(ce.target.rel, ce.machine, "")),
				http.StatusCreated).ID
			ce.approveIt(t)
			ce.evidence(t, other, nil)
		}},
		{"another controller's evidence", func(t *testing.T, ce *commitEnv) {
			ce.approveIt(t)
			ce.evidence(t, ce.pid, func(s *evSeed) { s.controller = "another" })
		}},
		{"observed before the approval", func(t *testing.T, ce *commitEnv) {
			ce.evidence(t, ce.pid, nil)
			ce.approveIt(t)
		}},
		{"too old", func(t *testing.T, ce *commitEnv) {
			ce.approveIt(t)
			ce.evidence(t, ce.pid, func(s *evSeed) { s.age = 10 * time.Minute })
		}},
		{"identity unread", func(t *testing.T, ce *commitEnv) {
			ce.ready(t)
			ce.evidence(t, ce.pid, func(s *evSeed) { s.uuid, s.nodeID, s.unread = nil, nil, `{"identity":{"cause":"identity-read"}}` })
		}},
		{"configuration unread", func(t *testing.T, ce *commitEnv) {
			ce.ready(t)
			ce.evidence(t, ce.pid, func(s *evSeed) { s.digest, s.unread = nil, `{"configuration":{"cause":"machine-config"}}` })
		}},
		{"running version unread", func(t *testing.T, ce *commitEnv) {
			ce.ready(t)
			ce.evidence(t, ce.pid, func(s *evSeed) { s.version, s.unread = "", `{"runningVersion":{"cause":"version"}}` })
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ce := newCommitEnv(t, "")
			c.setup(t, ce)
			_, err := ce.a.commitPlan(context.Background(), ce.pid)
			ce.wantNoEvidence(t, err)
			ce.evidence(t, ce.pid, nil)
			if _, err := ce.a.commitPlan(context.Background(), ce.pid); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Comparison 1's epoch term: an approval of an earlier epoch is refused in the current one; a
// controller of an earlier epoch records nothing, not even a refusal entry (PA §16).
func TestCommitmentEpochs(t *testing.T) {
	t.Parallel()
	ce := newCommitEnv(t, "")
	ce.approveIt(t)
	stale := ce.a
	newEpoch(t, ce.db)
	ce.a = observer(t, ce.env, &fakeExecutor{}, options{})
	ce.evidence(t, ce.pid, nil)
	ce.evidence(t, ce.pid, func(s *evSeed) { s.controller = stale.d.owner.ID })
	if _, err := stale.commitPlan(context.Background(), ce.pid); !errors.Is(err, errNotController) {
		t.Fatalf("stale controller: %v", err)
	}
	if n := count(t, ce.db, `SELECT count(*) FROM machine_event WHERE kind IN ('refusal', 'commitment')`); n != 0 {
		t.Fatalf("%d refusal or commitment entries by a stale controller", n)
	}
	_, err := ce.a.commitPlan(context.Background(), ce.pid)
	ce.wantRefused(t, err, "1", "the approval is of an earlier epoch", "")
}

// Each record comparison 1 and 2 read is held until the commitment commits (§3.2; DS row 003 for
// the approval): a transaction that would change it, standing in for an approval revocation, an
// identity revocation, a recovery-mode entry or an assignment change, waits for the commitment, and
// then reads the plan committed. Each waits on that record's lock alone.
func TestCommitmentHoldsItsReads(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, lock string
		arg        func(*commitEnv) any
	}{
		{"approval", `SELECT FROM approval WHERE id = $1 FOR UPDATE`, func(ce *commitEnv) any { return ce.approval }},
		{"principal", `SELECT FROM principal WHERE id = $1 FOR UPDATE`, func(ce *commitEnv) any { return ce.principalOf("h-approver") }},
		{"installation state", `SELECT FROM installation_state WHERE $1::text IS NOT NULL FOR UPDATE`,
			func(*commitEnv) any { return "" }},
		{"assignment head", `SELECT FROM assignment WHERE machine = $1 FOR UPDATE`, func(ce *commitEnv) any { return ce.machine }},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ce := newCommitEnv(t, "")
			ce.ready(t)
			arg := c.arg(ce)
			done := make(chan string, 1)
			ce.a = observer(t, ce.env, &fakeExecutor{}, options{commit: func(tx *sql.Tx) error {
				go func() {
					state := "error"
					defer func() { done <- state }()
					stand, err := ce.db.Begin()
					if err != nil {
						return
					}
					defer func() { _ = stand.Rollback() }()
					if _, err := stand.Exec(c.lock, arg); err != nil {
						return
					}
					_ = stand.QueryRow(`SELECT state FROM plan_state WHERE plan = $1`, ce.pid).Scan(&state)
				}()
				waitForLockWaits(t, ce.db, 1)
				return tx.Commit()
			}})
			ce.evidence(t, ce.pid, nil)
			if _, err := ce.a.commitPlan(context.Background(), ce.pid); err != nil {
				t.Fatal(err)
			}
			if state := <-done; state != "committed" {
				t.Fatalf("the stand-in read the plan %s", state)
			}
		})
	}
}

// The commitment's time follows its last lock (PA §5 rule 4): a plan that expires while the
// commitment waits for the approval's lock is refused as expired, not committed at the time the
// transaction began.
func TestCommitmentTimeFollowsLocks(t *testing.T) {
	t.Parallel()
	ce := newCommitEnv(t, `,"expiresInSeconds":4`)
	ce.ready(t)
	stand, err := ce.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stand.Rollback() }()
	mustExec(t, stand, `SELECT FROM approval WHERE id = $1 FOR UPDATE`, ce.approval)
	if time.Until(ce.expires) <= 0 {
		t.Fatal("the plan expired before the commitment began")
	}
	errs := make(chan error, 1)
	go func() {
		_, err := ce.a.commitPlan(context.Background(), ce.pid)
		errs <- err
	}()
	waitForLockWaits(t, ce.db, 1)
	time.Sleep(time.Until(ce.expires.Add(100 * time.Millisecond)))
	if err := stand.Rollback(); err != nil {
		t.Fatal(err)
	}
	ce.wantRefused(t, <-errs, "1", "the plan has expired", "")
}

// A revocation of the approval, or of the approver's identity, that arrives while the commitment
// holds its locks waits for it: the plan is committed under an approval that was in force, and the
// revocation finds it committed.
func TestCommitmentRevocationsWait(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		revoke func(ce *commitEnv) *httptest.ResponseRecorder
	}{
		{"approval", func(ce *commitEnv) *httptest.ResponseRecorder {
			return ce.revokeApproval(ce.human("h-recovery"), "k-revoke-approval-0123", ce.approval, `{"reason":"wrong"}`)
		}},
		{"identity", func(ce *commitEnv) *httptest.ResponseRecorder {
			return revoke(ce.env, ce.human("h-recovery"), "k-revoke-identity-0123",
				`{"identity":"`+ce.principalOf("h-approver")+`","reason":"left"}`)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ce := newCommitEnv(t, "")
			ce.approveIt(t)
			ce.principalOf("h-approver")
			done := make(chan *httptest.ResponseRecorder, 1)
			ce.a = observer(t, ce.env, &fakeExecutor{}, options{commit: func(tx *sql.Tx) error {
				go func() { done <- c.revoke(ce) }()
				waitForLockWaits(t, ce.db, 1)
				return tx.Commit()
			}})
			ce.evidence(t, ce.pid, nil)
			r, err := ce.a.commitPlan(context.Background(), ce.pid)
			if err != nil {
				t.Fatal(err)
			}
			rec := <-done
			var committedAt time.Time
			if err := ce.db.QueryRow(`SELECT created_at FROM operation WHERE id = $1`, r.Operation).Scan(&committedAt); err != nil {
				t.Fatal(err)
			}
			if b := decode[struct {
				At time.Time `json:"at"`
			}](t, rec, http.StatusCreated); !b.At.After(committedAt) {
				t.Fatalf("revocation at %s, commitment at %s", b.At, committedAt)
			}
		})
	}
}

// Comparison 2's Desired term (choice §10.24): a publication that selected the machine before the
// commitment took its lock changes Desired first, and the commitment, waiting on the machine row
// the publication holds, reads the new Desired and is refused. The control reads Desired without
// the machine's lock: it commits the plan of a release the machine no longer desires.
func TestCommitmentPublicationRace(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		control bool
	}{{"locked", false}, {"control", true}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ce := newCommitEnv(t, "")
			ce.a = observer(t, ce.env, &fakeExecutor{}, options{noMachineLock: c.control})
			ce.ready(t)
			pub, err := ce.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = pub.Rollback() }()
			// publish.go's selection: the covered machines FOR SHARE, then every machine state FOR UPDATE.
			mustExec(t, pub, `SELECT FROM machine WHERE id = $1 FOR SHARE`, ce.machine)
			mustExec(t, pub, `SELECT FROM machine_state WHERE machine = $1 FOR UPDATE`, ce.machine)
			mustExec(t, pub, `UPDATE machine_state SET desired = $2 WHERE machine = $1`, ce.machine, ce.applied.rel)
			done := make(chan error, 1)
			go func() {
				_, err := ce.a.commitPlan(context.Background(), ce.pid)
				done <- err
			}()
			waitForLockWaits(t, ce.db, 1)
			if err := pub.Commit(); err != nil {
				t.Fatal(err)
			}
			err = <-done
			if c.control {
				if err != nil {
					t.Fatalf("control: %v", err)
				}
				return
			}
			ce.wantRefused(t, err, "2", "the machine's Desired release changed", "")
		})
	}
}

// Comparison 5 at the PoC limit of one (choice §10.5): two commitments on two machines of one
// cluster, neither seeing the other's uncommitted operation, are ordered by the rollout scope's
// unique index; the later one is refused by comparison 5.
func TestCommitmentRolloutRace(t *testing.T) {
	t.Parallel()
	ce := newCommitEnv(t, "")
	ce.ready(t)
	ce.second(t)
	done := make(chan error, 1)
	first := observer(t, ce.env, &fakeExecutor{}, options{commit: func(tx *sql.Tx) error {
		go func() {
			_, err := ce.a.commitPlan(context.Background(), ce.pid)
			done <- err
		}()
		waitForLockWaits(t, ce.db, 1)
		return tx.Commit()
	}})
	ce.evidence(t, ce.plan2, func(s *evSeed) { s.controller = first.d.owner.ID })
	if _, err := first.commitPlan(context.Background(), ce.plan2); err != nil {
		t.Fatal(err)
	}
	ce.wantRefused(t, <-done, "5", "an operation holds the cluster's rollout scope", "")
}
