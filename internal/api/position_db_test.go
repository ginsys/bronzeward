package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ginsys/bronzeward/internal/id"
)

// position reads GET /machines/{machine}/position as a viewer and decodes the answer.
func (e *env) position(t *testing.T, machine string) positionBody {
	t.Helper()
	return decode[positionBody](t, e.do(e.api, call{method: "GET", path: prefix + "/machines/" + machine + "/position",
		token: e.human("h-viewer")}), http.StatusOK)
}

// wantStep reads the position and holds it to step, with plan and operation as given ("" for null).
func (e *env) wantStep(t *testing.T, machine, step, plan, operation string) positionBody {
	t.Helper()
	b := e.position(t, machine)
	gotPlan, gotOp := "", ""
	if b.Plan != nil {
		gotPlan = b.Plan.ID
	}
	if b.Operation != nil {
		gotOp = b.Operation.ID
	}
	if b.Step != step || gotPlan != plan || gotOp != operation {
		t.Fatalf("step %q, plan %q, operation %q; want %q, %q, %q", b.Step, gotPlan, gotOp, step, plan, operation)
	}
	return b
}

// claimSeed is one ingestion claim of a machine, written directly with its ingest operation.
type claimSeed struct {
	state, mode string
	lease       time.Duration // from now; negative is lapsed
	expires     time.Duration // from now; negative is past
	age         time.Duration // how long ago it was created
}

// claim writes a claim of machine in seed's state, its draft (open) and its ingest operation, and
// answers the claim's and the draft's identifiers.
func (p *planEnv) claim(t *testing.T, machine string, s claimSeed) (claim, draft string) {
	t.Helper()
	claim, op := id.New(id.Ingestion), id.New(id.Operation)
	draft = p.secondDraft("k-claimdraft-" + claim)
	var payload, digest, review any
	if s.state == "paused" {
		payload, digest, review = []byte{1}, make([]byte, 32), "pending"
	}
	mustExec(t, p.db, `INSERT INTO staging_claim (id, mode, state, owner, owner_gen, owner_epoch, lease_until, expires_at,
			cluster, machine, kind, created_at, payload, payload_digest, review)
		SELECT $1, $2, $3, 'run/1/start', 1, epoch, clock_timestamp() + $4 * interval '1 microsecond',
			clock_timestamp() + $5 * interval '1 microsecond', $6, $7, 'import',
			clock_timestamp() - $8 * interval '1 microsecond', $9, $10, $11 FROM installation_state`,
		claim, s.mode, s.state, s.lease.Microseconds(), s.expires.Microseconds(), p.cluster, machine, s.age.Microseconds(),
		payload, digest, review)
	opState, result, opError := "running", any(nil), any(nil)
	switch s.state {
	case "released":
		opState, result = "succeeded", "{}"
	case "abandoned":
		opState, opError = "failed", "{}"
	}
	mustExec(t, p.db, `INSERT INTO operation (id, kind, state, epoch, owner, owner_gen, owner_epoch, lease_until, last_event,
			draft, draft_revision, ingestion, created_by, created_by_kind, created_role, created_at, result, error)
		SELECT $1, 'ingest', $2::text, epoch, CASE WHEN $2::text = 'running' THEN 'run/1/start' END,
			CASE WHEN $2::text = 'running' THEN 1 ELSE 0 END,
			CASE WHEN $2::text = 'running' THEN epoch END, CASE WHEN $2::text = 'running' THEN now() + interval '1 minute' END, 2, $3, 1, $4,
			$5, 'service', 'publisher', clock_timestamp() - $6 * interval '1 microsecond', $7::jsonb, $8::jsonb
		FROM installation_state`, op, opState, draft, claim, p.robotID, s.age.Microseconds(), result, opError)
	return claim, draft
}

// §9.3: an unknown machine, and an identifier of another entity, are 404; the route takes no query.
func TestPositionNotFound(t *testing.T) {
	t.Parallel()
	ce := newCommitEnv(t, "")
	viewer := ce.human("h-viewer")
	for _, path := range []string{"/machines/" + id.New(id.Machine) + "/position", "/machines/" + ce.cluster + "/position"} {
		wantProblem(t, ce.do(ce.api, call{method: "GET", path: prefix + path, token: viewer}), http.StatusNotFound, "not-found")
	}
	wantProblem(t, ce.do(ce.api, call{method: "GET", path: prefix + "/machines/" + ce.machine + "/position?x=1", token: viewer}),
		http.StatusBadRequest, "invalid-request")
}

// §12.2: before a recovery start's entry commits, the position read is refused like every request but
// the entry.
func TestPositionRefusedBeforeEntry(t *testing.T) {
	e := newEnv(t, options{})
	e.api.d.preEntry.Store(true)
	wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/machines/" + id.New(id.Machine) + "/position",
		token: e.human("h-viewer")}), http.StatusConflict, "recovery-mode-active")
}

// The plan steps in one machine's life: proposed, approved, and approved in an earlier epoch (case 3:
// awaiting-approval, never awaiting-dispatch).
func TestPositionPlanSteps(t *testing.T) {
	t.Parallel()
	ce := newCommitEnv(t, "")
	b := ce.wantStep(t, ce.machine, "awaiting-approval", ce.pid, "")
	if b.Plan.State != "proposed" || b.Plan.ApprovalCurrent != nil || b.Plan.Operation != "apply-config" || b.Plan.Release != ce.target.rel {
		t.Fatalf("plan %+v", b.Plan)
	}
	ce.approveIt(t)
	b = ce.wantStep(t, ce.machine, "awaiting-dispatch", ce.pid, "")
	if b.Plan.State != "approved" || b.Plan.ApprovalCurrent == nil || !*b.Plan.ApprovalCurrent {
		t.Fatalf("plan %+v", b.Plan)
	}
	newEpoch(t, ce.db)
	b = ce.wantStep(t, ce.machine, "awaiting-approval", ce.pid, "")
	if b.Plan.State != "approved" || b.Plan.ApprovalCurrent == nil || *b.Plan.ApprovalCurrent {
		t.Fatalf("plan %+v", b.Plan)
	}
}

// A machine with a Desired release that is not yet applied and no plan reads needs-plan, by its own
// fields (the machine member is the one GET /machines/{id} answers).
func TestPositionNeedsPlan(t *testing.T) {
	t.Parallel()
	p := newPlanEnv(t)
	b := p.wantStep(t, p.machine, "needs-plan", "", "")
	if !b.Machine.PendingConvergence || b.Machine.Desired == nil || b.Ingestion != nil || b.Draft != nil || b.RolloutHeldBy != nil {
		t.Fatalf("position %+v", b)
	}
	got := decode[machineBody](t, p.do(p.api, call{method: "GET", path: prefix + "/machines/" + p.machine, token: p.human("h-viewer")}),
		http.StatusOK)
	if got.ID != b.Machine.ID || got.PendingConvergence != b.Machine.PendingConvergence {
		t.Fatalf("the machine member %+v is not the machine read %+v", b.Machine, got)
	}
}

// A committed plan is never picked (its state is final), and the operation holding the scope is the
// step: committed, sending and verifying are in flight, and an unresolved one says so (case 2: one
// state at a time, never null while the scope is held).
func TestPositionOperation(t *testing.T) {
	t.Parallel()
	ce := newCommitEnv(t, "")
	ce.ready(t)
	committed, err := ce.a.commitPlan(context.Background(), ce.pid)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ state, step string }{
		{"committed", "operation-in-flight"}, {"sending", "operation-in-flight"}, {"verifying", "operation-in-flight"},
		{"unresolved", "operation-unresolved"},
	} {
		setOperationState(t, ce.db, committed.Operation, c.state)
		b := ce.wantStep(t, ce.machine, c.step, "", committed.Operation)
		if b.Operation.State != c.state || b.Operation.Kind != "apply-config" || b.Operation.Plan != ce.pid {
			t.Fatalf("operation %+v", b.Operation)
		}
	}
}

// Each import step by the claim's state at the snapshot's clock, with the draft it names, and the
// claim chosen: the newest live one, else the newest of any state, by creation time.
func TestPositionIngestionSteps(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		seed  claimSeed
		step  string
		state string // as the read treats the claim
		lapse bool
	}{
		{"held", claimSeed{state: "held", mode: "transient", lease: time.Hour, expires: time.Hour}, "import-running", "held", false},
		{"resumed", claimSeed{state: "resumed", mode: "encrypted", lease: time.Hour, expires: time.Hour}, "import-running", "resumed", false},
		// Case 6: an encrypted claim whose lease lapsed stays live until its absolute expiry.
		{"encrypted, lease lapsed", claimSeed{state: "held", mode: "encrypted", lease: -time.Minute, expires: time.Hour}, "import-stalled", "held", true},
		{"resumed, lease lapsed", claimSeed{state: "resumed", mode: "encrypted", lease: -time.Minute, expires: time.Hour}, "import-stalled", "resumed", true},
		{"paused", claimSeed{state: "paused", mode: "encrypted", lease: -time.Minute, expires: time.Hour}, "import-paused", "paused", false},
		// A transient claim past its lease, and any claim past its expiry, read abandoned.
		{"transient, lease lapsed", claimSeed{state: "held", mode: "transient", lease: -time.Minute, expires: time.Hour}, "needs-plan", "abandoned", false},
		{"past its expiry", claimSeed{state: "held", mode: "encrypted", lease: time.Hour, expires: -time.Minute}, "needs-plan", "abandoned", false},
		{"released", claimSeed{state: "released", mode: "transient", lease: time.Hour, expires: time.Hour}, "draft-to-publish", "released", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := newPlanEnv(t)
			claim, draft := p.claim(t, p.machine, c.seed)
			b := p.wantStep(t, p.machine, c.step, "", "")
			if b.Ingestion == nil || b.Ingestion.ID != claim || b.Ingestion.State != c.state || b.Ingestion.LeaseLapsed != c.lapse ||
				b.Ingestion.Kind != "import" || b.Ingestion.Draft != draft || b.Ingestion.Operation == nil {
				t.Fatalf("ingestion %+v, want claim %s in state %s", b.Ingestion, claim, c.state)
			}
			if b.Draft == nil || b.Draft.ID != draft || b.Draft.State != "open" {
				t.Fatalf("draft %+v", b.Draft)
			}
		})
	}
}

// The machine's newest live ingestion wins over a newer one that is not live; with none live, the
// newest of any state, by creation time, then identifier.
func TestPositionIngestionChoice(t *testing.T) {
	t.Parallel()
	p := newPlanEnv(t)
	if b := p.position(t, p.machine); b.Ingestion != nil || b.Draft != nil {
		t.Fatalf("an ingestion %+v of a machine with none", b.Ingestion)
	}
	live, _ := p.claim(t, p.machine, claimSeed{state: "held", mode: "transient", lease: time.Hour, expires: time.Hour, age: 2 * time.Hour})
	newer, _ := p.claim(t, p.machine, claimSeed{state: "released", mode: "transient", lease: time.Hour, expires: time.Hour, age: time.Hour})
	if b := p.position(t, p.machine); b.Ingestion == nil || b.Ingestion.ID != live || b.Step != "import-running" {
		t.Fatalf("ingestion %+v, want the live %s over the newer %s", b.Ingestion, live, newer)
	}
	mustExec(t, p.db, `UPDATE staging_claim SET state = 'released', payload = NULL, payload_digest = NULL WHERE id = $1`, live)
	if b := p.position(t, p.machine); b.Ingestion == nil || b.Ingestion.ID != newer {
		t.Fatalf("ingestion %+v, want the newest %s once none is live", b.Ingestion, newer)
	}
}

// Case 4: a live ingestion of the machine is its step before an open drift record, and a drift
// record with none is drift-open. Case 5: an approved ordinary plan, current and unbound, with a drift
// record opened since, is not picked; the bound plan of a drift adoption is, and reads by its state.
func TestPositionDrift(t *testing.T) {
	t.Parallel()
	t.Run("ordinary plan, then a drift record", func(t *testing.T) {
		t.Parallel()
		ce := newCommitEnv(t, "")
		ce.approveIt(t)
		ce.wantStep(t, ce.machine, "awaiting-dispatch", ce.pid, "")
		openDriftOn(t, ce.db, ce.machine)
		b := ce.wantStep(t, ce.machine, "drift-open", "", "")
		if b.Machine.OpenDrift == nil {
			t.Fatal("no open drift on the machine member")
		}
		_, _ = ce.claim(t, ce.machine, claimSeed{state: "held", mode: "transient", lease: time.Hour, expires: time.Hour})
		ce.wantStep(t, ce.machine, "import-running", "", "")
	})
	t.Run("a live ingestion before drift", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			seed claimSeed
			step string
		}{
			{claimSeed{state: "held", mode: "transient", lease: time.Hour, expires: time.Hour}, "import-running"},
			{claimSeed{state: "held", mode: "encrypted", lease: -time.Minute, expires: time.Hour}, "import-stalled"},
			{claimSeed{state: "paused", mode: "encrypted", lease: -time.Minute, expires: time.Hour}, "import-paused"},
		} {
			p := newPlanEnv(t)
			openDriftOn(t, p.db, p.machine)
			p.claim(t, p.machine, c.seed)
			p.wantStep(t, p.machine, c.step, "", "")
		}
	})
	t.Run("the plan that binds the record", func(t *testing.T) {
		t.Parallel()
		ae := newDriftedAdoptEnv(t, "")
		ae.wantStep(t, ae.machine, "awaiting-approval", ae.pid, "")
		ae.approveIt(t)
		b := ae.wantStep(t, ae.machine, "awaiting-dispatch", ae.pid, "")
		if b.Plan.Operation != "adopt" || b.Machine.OpenDrift == nil {
			t.Fatalf("plan %+v, drift %+v", b.Plan, b.Machine.OpenDrift)
		}
		closeDriftOn(t, ae.db, ae.machine, ae.drift, "adopted")
		ae.wantStep(t, ae.machine, "needs-plan", "", "") // the record it binds is closed
	})
}

// Comparison 2's three terms, each alone, drop an approved plan from the answer and leave the step to
// the machine's own fields, with nothing said of the stale plan (case 9 is the baseline term).
func TestPositionComparison2(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		change func(*testing.T, *commitEnv)
	}{
		{"baseline revision", func(t *testing.T, ce *commitEnv) {
			mustExec(t, ce.db, `UPDATE machine_state SET baseline_revision = baseline_revision + 1 WHERE machine = $1`, ce.machine)
		}},
		{"Desired release", func(t *testing.T, ce *commitEnv) {
			mustExec(t, ce.db, `UPDATE machine_state SET desired = $2 WHERE machine = $1`, ce.machine, ce.applied.rel)
		}},
		{"assignment revision", func(t *testing.T, ce *commitEnv) {
			rev := id.New(id.AssignmentRevision)
			mustExec(t, ce.db, `INSERT INTO assignment_revision (id, cluster, machine, author, created_at) VALUES ($1, $2, $3, $4, now())`,
				rev, ce.cluster, ce.machine, ce.seed)
			mustExec(t, ce.db, `UPDATE assignment SET head_revision_id = $2, head_revision = 2 WHERE machine = $1`, ce.machine, rev)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ce := newCommitEnv(t, "")
			ce.approveIt(t)
			ce.wantStep(t, ce.machine, "awaiting-dispatch", ce.pid, "")
			c.change(t, ce)
			b := ce.position(t, ce.machine)
			if b.Plan != nil || b.Step == "awaiting-dispatch" || b.Step == "awaiting-approval" {
				t.Fatalf("plan %+v, step %q after the %s changed", b.Plan, b.Step, c.name)
			}
		})
	}
}

// Case 9: two plans approved for one Desired release; the first commits and completes (the baseline
// revision advances), and the other, still approved and bound to the earlier baseline, is not picked.
func TestPositionSecondPlanAfterCompletion(t *testing.T) {
	t.Parallel()
	ce := newCommitEnv(t, "")
	ce.ready(t)
	second := decode[planBody](t, ce.plan(ce.robot, "k-apply-second-0123456", applyBody(ce.target.rel, ce.machine, "")),
		http.StatusCreated).ID
	ce.approveIt2(t, second)
	ce.wantStep(t, ce.machine, "awaiting-dispatch", second, "") // the newer of the two approved plans
	committed, err := ce.a.commitPlan(context.Background(), ce.pid)
	if err != nil {
		t.Fatal(err)
	}
	ce.wantStep(t, ce.machine, "operation-in-flight", second, committed.Operation) // not yet complete: the baseline is the bound one
	// The first plan's operation completes, and the baseline revision advances with it.
	setOperationState(t, ce.db, committed.Operation, "completed")
	mustExec(t, ce.db, `UPDATE machine_state SET baseline_revision = baseline_revision + 1 WHERE machine = $1`, ce.machine)
	b := ce.position(t, ce.machine)
	if b.Plan != nil || b.Operation != nil || b.Step == "awaiting-dispatch" || b.Step == "awaiting-approval" {
		t.Fatalf("plan %+v, operation %+v, step %q: the stale second plan is picked", b.Plan, b.Operation, b.Step)
	}
}

// setOperationState puts an operation in state as a fixture, past the transition and attempt triggers
// (the rows a real run reaches along execution and recovery §4's path, which this read does not
// exercise): a completed operation carries the empty result its schema check requires.
func setOperationState(t *testing.T, db execer, op, state string) {
	t.Helper()
	mustExec(t, db, `ALTER TABLE operation DISABLE TRIGGER transition`)
	mustExec(t, db, `ALTER TABLE operation DISABLE TRIGGER attempted`)
	mustExec(t, db, `UPDATE operation SET state = $2, result = CASE WHEN $2::text = 'completed' THEN '{}'::jsonb END WHERE id = $1`, op, state)
	mustExec(t, db, `ALTER TABLE operation ENABLE TRIGGER transition`)
	mustExec(t, db, `ALTER TABLE operation ENABLE TRIGGER attempted`)
}

// approveIt2 approves plan through T5a.
func (ce *commitEnv) approveIt2(t *testing.T, plan string) {
	t.Helper()
	decode[approvalBody](t, ce.approve(ce.human("h-approver"), ce.key(), plan), http.StatusCreated)
}

// A plan that expired, or whose approval was revoked, is not picked, though its stored state still
// reads approved; the time is the snapshot's own.
func TestPositionPlanStateAtTheClock(t *testing.T) {
	t.Parallel()
	t.Run("expired", func(t *testing.T) {
		t.Parallel()
		ce := newCommitEnv(t, "")
		ce.approveIt(t)
		ce.expireIn(t, 0)
		ce.wantStep(t, ce.machine, "needs-plan", "", "")
	})
	t.Run("approval revoked", func(t *testing.T) {
		t.Parallel()
		ce := newCommitEnv(t, "")
		ce.approveIt(t)
		if rec := ce.revokeApproval(ce.human("h-recovery"), ce.key(), ce.approval, `{"reason":"wrong"}`); rec.Code != http.StatusCreated {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		ce.wantStep(t, ce.machine, "needs-plan", "", "")
	})
}

// The adopt plans: case 1 (a committed adopt plan whose operation completed: plan and operation null),
// case 7 (an approved adopt plan a refusal entry names is not picked, and a refusal on an apply-config
// plan does not exclude it) and case 8 (a handover: no Desired and no Applied, an approved adopt plan
// that bound no Desired is picked).
func TestPositionAdoptPlans(t *testing.T) {
	t.Parallel()
	refusal := func(t *testing.T, e *planEnv, machine, plan string) {
		t.Helper()
		mustExec(t, e.db, `UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1`, machine)
		mustExec(t, e.db, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
			SELECT m.id, m.revision_counter, i.epoch, 'refusal', jsonb_build_object('transaction', 'T6', 'comparison', '4.4',
				'plan', $2::text, 'cause', 'test', 'controller', 'test-controller'), now()
			FROM machine m, installation_state i WHERE m.id = $1`, machine, plan)
	}
	t.Run("handover", func(t *testing.T) {
		t.Parallel()
		ae := newAdoptEnv(t, "")
		b := ae.wantStep(t, ae.machine, "awaiting-approval", ae.pid, "")
		if b.Machine.Desired != nil || b.Machine.Applied != nil {
			t.Fatalf("machine %+v", b.Machine)
		}
		ae.approveIt(t)
		ae.wantStep(t, ae.machine, "awaiting-dispatch", ae.pid, "")
	})
	t.Run("refused adoption", func(t *testing.T) {
		t.Parallel()
		ae := newAdoptEnv(t, "")
		ae.approveIt(t)
		refusal(t, ae.planEnv, ae.machine, ae.pid)
		ae.wantStep(t, ae.machine, "not-handed-over", "", "")
	})
	t.Run("refused apply-config plan", func(t *testing.T) {
		t.Parallel()
		ce := newCommitEnv(t, "")
		ce.approveIt(t)
		refusal(t, ce.planEnv, ce.machine, ce.pid)
		ce.wantStep(t, ce.machine, "awaiting-dispatch", ce.pid, "")
	})
	t.Run("committed adoption", func(t *testing.T) {
		t.Parallel()
		ae := newAdoptEnv(t, "")
		ae.approveIt(t)
		ae.observed(t, nil)
		if _, err := ae.a.commitAdoption(context.Background(), ae.pid); err != nil {
			t.Fatal(err)
		}
		b := ae.position(t, ae.machine)
		want := "no-step"
		if b.Machine.PendingConvergence {
			want = "needs-plan"
		}
		if b.Plan != nil || b.Operation != nil || b.Step != want {
			t.Fatalf("plan %+v, operation %+v, step %q; want none, none, %q", b.Plan, b.Operation, b.Step, want)
		}
	})
}

// rolloutHeldBy names the operation another machine of the cluster holds the rollout scope with,
// while this machine's own plan is still picked.
func TestPositionRolloutHeldBy(t *testing.T) {
	t.Parallel()
	ce := newCommitEnv(t, "")
	ce.approveIt(t)
	ce.second(t)
	ce.evidence(t, ce.plan2, nil)
	other, err := ce.a.commitPlan(context.Background(), ce.plan2)
	if err != nil {
		t.Fatal(err)
	}
	b := ce.wantStep(t, ce.machine, "awaiting-dispatch", ce.pid, "")
	if b.RolloutHeldBy == nil || b.RolloutHeldBy.ID != other.Operation || b.RolloutHeldBy.Machine != ce.machine2 {
		t.Fatalf("rolloutHeldBy %+v, want %s of %s", b.RolloutHeldBy, other.Operation, ce.machine2)
	}
	if b2 := ce.position(t, ce.machine2); b2.RolloutHeldBy != nil || b2.Operation == nil || b2.Operation.ID != other.Operation {
		t.Fatalf("the holder's own position: rolloutHeldBy %+v, operation %+v", b2.RolloutHeldBy, b2.Operation)
	}
}

// A serialization failure of the snapshot repeats the whole transaction, up to four attempts, then
// answers 503 transient-conflict; no attempt's partial answer is served.
func TestPositionSnapshotRetry(t *testing.T) {
	t.Parallel()
	failing := func(upTo int, attempts *[]int) options {
		return options{inSnapshot: func(n int) error {
			*attempts = append(*attempts, n)
			if n <= upTo {
				return &pgconn.PgError{Code: "40001", Message: "could not serialize access"}
			}
			return nil
		}}
	}
	t.Run("three failures then an answer", func(t *testing.T) {
		t.Parallel()
		ce := newCommitEnv(t, "")
		var attempts []int
		api := ce.build(failing(3, &attempts))
		rec := ce.do(api, call{method: "GET", path: prefix + "/machines/" + ce.machine + "/position", token: ce.human("h-viewer")})
		if b := decode[positionBody](t, rec, http.StatusOK); b.Machine.ID != ce.machine || b.Step != "awaiting-approval" {
			t.Fatalf("position %+v", b)
		}
		if len(attempts) != 4 {
			t.Fatalf("attempts %v, want four", attempts)
		}
	})
	t.Run("four failures", func(t *testing.T) {
		t.Parallel()
		ce := newCommitEnv(t, "")
		var attempts []int
		api := ce.build(failing(4, &attempts))
		doc := wantProblem(t, ce.do(api, call{method: "GET", path: prefix + "/machines/" + ce.machine + "/position", token: ce.human("h-viewer")}),
			http.StatusServiceUnavailable, "transient-conflict")
		if len(attempts) != 4 || !strings.Contains(doc["detail"].(string), "snapshot") {
			t.Fatalf("attempts %v, problem %v", attempts, doc)
		}
	})
}
