package migrate

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ginsys/bronzeward/internal/id"
)

// The statements the plan tests insert with.
const (
	insertActAs = `INSERT INTO act (id, principal, principal_kind, via, role, action, subjects, request_id, epoch, at)
		SELECT $1, $2, kind, 'api', $3, $4, ARRAY[$5], $6, s.epoch, now() FROM principal, installation_state s WHERE id = $2`
	// An apply-config or adopt operation, owned at generation 1 when it has an owner.
	insertPlanOperation = `INSERT INTO operation (id, kind, state, epoch, owner, owner_gen, owner_epoch, plan, machine, cluster,
		created_at) SELECT $1, $2, $3, epoch, $4, CASE WHEN $4::text IS NULL THEN 0 ELSE 1 END,
		CASE WHEN $4::text IS NULL THEN NULL ELSE epoch END, $5, $6, $7, now() FROM installation_state`
	insertPlanState = `INSERT INTO plan_state (plan, state, updated_at) VALUES ($1, 'proposed', now())`
	owner           = "run-1/4242/exec-1"
)

// record is one row of a table: its columns in order, with a value for each.
type record struct {
	table string
	cols  []string
	vals  map[string]any
}

// with returns a copy with the columns named in kv (name, value, ...) replaced.
func (r record) with(kv ...any) record {
	c := record{table: r.table, cols: r.cols, vals: maps.Clone(r.vals)}
	for i := 0; i+1 < len(kv); i += 2 {
		c.vals[kv[i].(string)] = kv[i+1]
	}
	return c
}

func (r record) stmt() stmt {
	marks := make([]string, len(r.cols))
	args := make([]any, len(r.cols))
	for i, c := range r.cols {
		marks[i] = fmt.Sprintf("$%d", i+1)
		args[i] = r.vals[c]
	}
	return stmt{fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", r.table, strings.Join(r.cols, ", "), strings.Join(marks, ", ")), args}
}

func newRecord(table string, kv ...any) record {
	r := record{table: table, vals: map[string]any{}}
	for i := 0; i+1 < len(kv); i += 2 {
		r.cols = append(r.cols, kv[i].(string))
		r.vals[kv[i].(string)] = kv[i+1]
	}
	return r
}

// plans holds releaseRows' release with an approved apply-config plan and a proposed adopt plan of
// its machine, inserted by planRows. Revisions 1 to 3 of the machine's timeline are theirs.
type plans struct {
	release
	epoch, bob, service, apply, adopt, approval string
}

// entry is the machine timeline entry a record is keyed by.
func (p plans) entry(machine string, revision int, kind string) stmt {
	return stmt{insertMachineEvent, []any{machine, revision, kind, `{}`}}
}

func (p plans) act(actID, principal, role string) stmt {
	return stmt{insertActAs, []any{actID, principal, role, "plan", p.machine, id.New(id.Request)}}
}

// planRow is an apply-config plan of the release's machine at timeline revision rev.
func (p plans) planRow(planID string, rev int) record {
	now := time.Now()
	return newRecord("plan", "id", planID, "cluster", p.cluster, "machine", p.machine, "kind", "apply-config",
		"mode", "no-reboot", "release", p.rel, "assignment_revision", p.asr, "desired_release", p.rel,
		"baseline_revision", 1, "expected_digest", digest(5), "route", "10.55.0.3:50000",
		"max_observation_age", "5 minutes", "check_validity", "10 minutes", "transport_deadline", "1 minute",
		"verification_deadline", "5 minutes", "max_attempts", 3, "rollout_limit", 1, "expires_at", now.Add(time.Hour),
		"idempotency_key", "plan-"+planID[4:20], "approval_policy", "one-approver", "created_by", p.human,
		"created_by_kind", "human", "created_role", "publisher", "epoch", p.epoch, "created_at", now, "evidence", `{}`,
		"revision", rev)
}

// adoptRow is an adopt plan of the release: the handover, with no baseline revision.
func (p plans) adoptRow(planID string, rev int) record {
	return p.planRow(planID, rev).with("kind", "adopt", "mode", nil, "baseline_revision", nil, "expected_digest", nil,
		"transport_deadline", nil, "verification_deadline", nil, "max_attempts", nil)
}

func (p plans) approvalRow(approvalID, planID string, rev int, actID string) record {
	return newRecord("approval", "id", approvalID, "plan", planID, "machine", p.machine, "revision", rev,
		"approver", p.human, "role", "approver", "epoch", p.epoch, "self_approval", "{created-plan}", "act", actID,
		"at", time.Now())
}

func (p plans) revocationRow(approvalID, planID string, rev int, actID string) record {
	return newRecord("approval_revocation", "approval", approvalID, "plan", planID, "machine", p.machine, "revision", rev,
		"revoked_by", p.human, "role", "approver", "reason", "wrong window", "act", actID, "epoch", p.epoch, "at", time.Now())
}

func (p plans) cancellationRow(planID string, rev int, actID string) record {
	return newRecord("plan_cancellation", "plan", planID, "machine", p.machine, "revision", rev,
		"cancelled_by", p.human, "cancelled_by_kind", "human", "role", "publisher", "reason", "superseded", "act", actID,
		"epoch", p.epoch, "at", time.Now())
}

func (p plans) startRow(rev int, purpose string, planID, operation any) record {
	return newRecord("observation_start", "machine", p.machine, "revision", rev, "purpose", purpose, "plan", planID,
		"operation", operation, "endpoint", "10.55.0.3:50000", "controller", owner)
}

func (p plans) observationRow(obsID string, basis, rev int) record {
	return newRecord("observation", "id", obsID, "machine", p.machine, "basis", basis, "revision", rev,
		"access_path", "access/talos/"+p.cluster, "access_version", 1, "access_created", created,
		"talos_cluster_id", nil, "smbios_uuid", "0b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b", "running_version", "v1.13.6",
		"configuration_digest", digest(1), "resource_version", "3", "health", `{}`, "unread", `{}`, "at", time.Now())
}

// approve records an approval of planID at revision rev and moves its state to approved.
func (p plans) approve(t *testing.T, db *sql.DB, planID string, rev int) string {
	t.Helper()
	apr, act := id.New(id.Approval), id.New(id.Act)
	commitRows(t, db, p.entry(p.machine, rev, "approval"), p.approvalRow(apr, planID, rev, act).stmt(), p.act(act, p.human, "approver"),
		stmt{`UPDATE plan_state SET state = 'approved', approval = $2, revision = revision + 1 WHERE plan = $1`, []any{planID, apr}})
	return apr
}

// propose records plan r at its revision with its proposed state.
func (p plans) propose(t *testing.T, db *sql.DB, r record) {
	t.Helper()
	commitRows(t, db, p.entry(r.vals["machine"].(string), r.vals["revision"].(int), "plan"), r.stmt(),
		stmt{insertPlanState, []any{r.vals["id"]}})
}

// commit creates planID's operation committed and moves its plan state to committed.
func (p plans) commit(t *testing.T, db *sql.DB, planID string) string {
	t.Helper()
	op := id.New(id.Operation)
	commitRows(t, db, stmt{insertPlanOperation, []any{op, "apply-config", "committed", owner, planID, p.machine, p.cluster}},
		stmt{`UPDATE plan_state SET state = 'committed', operation = $2, revision = revision + 1 WHERE plan = $1`, []any{planID, op}})
	return op
}

func commitRows(t *testing.T, db *sql.DB, rows ...stmt) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, s := range rows {
		mustExec(t, tx, s.q, s.args...)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func planRows(t *testing.T, db *sql.DB) plans {
	t.Helper()
	p := plans{release: releaseRows(t, db), bob: id.New(id.Principal), service: id.New(id.Principal),
		apply: id.New(id.Plan), adopt: id.New(id.Plan)}
	if err := db.QueryRow(`SELECT epoch FROM installation_state`).Scan(&p.epoch); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', 'https://idp.test', 'bob', now())`, p.bob)
	mustExec(t, db, `INSERT INTO principal (id, kind, name, responsible, created_at) VALUES ($1, 'service', 'ci', $2, now())`,
		p.service, p.human)
	p.propose(t, db, p.planRow(p.apply, 1))
	p.approval = p.approve(t, db, p.apply, 2)
	p.propose(t, db, p.adoptRow(p.adopt, 3))
	return p
}

// refused runs rows in one transaction and wants the first error, or the commit's when every row
// was accepted (a deferred check), to carry SQLSTATE want, or "code/constraint" to name both.
func refused(t *testing.T, db *sql.DB, name, want string, rows ...stmt) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, s := range rows {
		if _, err = tx.Exec(s.q, s.args...); err != nil {
			break
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	got := sqlState(err)
	if strings.Contains(want, "/") {
		var pe *pgconn.PgError
		if errors.As(err, &pe) {
			got += "/" + pe.ConstraintName
		}
	}
	if got != want {
		t.Errorf("%s: %v; want %s", name, err, want)
	}
}

// accepted runs rows and the deferred checks, as a commit would, then rolls back: a control that
// leaves the rows of the test as they were.
func accepted(t *testing.T, db *sql.DB, name string, rows ...stmt) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, s := range append(rows, stmt{`SET CONSTRAINTS ALL IMMEDIATE`, nil}) {
		if _, err := tx.Exec(s.q, s.args...); err != nil {
			t.Errorf("%s: %v; want it accepted", name, err)
			return
		}
	}
}

// PA §3, §8.1; execution and recovery §2, §3.2, §6.3: what the plan, approval, revocation,
// cancellation and observation tables refuse.
func TestPlanConstraints(t *testing.T) {
	db, _ := installed(t)
	p := planRows(t, db)
	// A second machine of the cluster, with an assignment revision and no release covering it.
	m2, asr2 := id.New(id.Machine), id.New(id.AssignmentRevision)
	mustExec(t, db, insertMachine, m2, p.cluster, "3e8d9f4b-5c6a-4b8c-9d2e-3f4a5b6c7d8e", nil, "normal")
	mustExec(t, db, insertAssignmentRevision, asr2, p.cluster, m2, p.human)
	apply, adopt := p.planRow(id.New(id.Plan), 100), p.adoptRow(id.New(id.Plan), 100)
	at := p.entry(p.machine, 100, "plan")
	state := func(r record) stmt { return stmt{insertPlanState, []any{r.vals["id"]}} }
	plan := func(name, want string, r record) {
		t.Helper()
		refused(t, db, name, want, at, r.stmt(), state(r))
	}
	plan("plan named by an approval id", "23514", apply.with("id", id.New(id.Approval)))
	plan("plan kind publish", "23514", apply.with("kind", "publish"))
	plan("apply-config without a mode", "23514/plan_kind_bindings", apply.with("mode", nil))
	plan("apply-config in mode reboot", "23514", apply.with("mode", "reboot"))
	plan("adopt with a mode", "23514/plan_kind_bindings", adopt.with("mode", "no-reboot"))
	plan("apply-config not desiring its release", "23514/plan_kind_bindings", apply.with("desired_release", nil))
	plan("apply-config with no baseline revision", "23514/plan_kind_bindings", apply.with("baseline_revision", nil))
	plan("adopt with a baseline revision", "23514/plan_kind_bindings", adopt.with("baseline_revision", 1))
	plan("apply-config with no expected digest", "23514/plan_kind_bindings", apply.with("expected_digest", nil))
	plan("adopt with an expected digest", "23514/plan_kind_bindings", adopt.with("expected_digest", digest(5)))
	plan("apply-config with no transport deadline", "23514/plan_kind_bindings", apply.with("transport_deadline", nil))
	plan("apply-config with no verification deadline", "23514/plan_kind_bindings", apply.with("verification_deadline", nil))
	plan("transport deadline after the verification deadline", "23514/plan_kind_bindings",
		apply.with("transport_deadline", "10 minutes"))
	plan("apply-config with no attempt limit", "23514/plan_kind_bindings", apply.with("max_attempts", nil))
	plan("adopt with a transport deadline", "23514/plan_kind_bindings", adopt.with("transport_deadline", "1 minute"))
	plan("adopt with an attempt limit", "23514/plan_kind_bindings", adopt.with("max_attempts", 3))
	plan("no attempt", "23514", apply.with("max_attempts", 0))
	plan("no observation age", "23514", apply.with("max_observation_age", "0 seconds"))
	plan("no check validity", "23514", apply.with("check_validity", "-1 minute"))
	plan("no transport deadline", "23514", apply.with("transport_deadline", "0 seconds"))
	plan("rollout limit 2", "23514", apply.with("rollout_limit", 2))
	plan("approval policy of two approvers", "23514", apply.with("approval_policy", "two-approvers"))
	plan("plan created as author", "23514", apply.with("created_role", "author"))
	plan("plan expiring at its creation", "23514", apply.with("expires_at", apply.vals["created_at"]))
	plan("evidence as an array", "23514", apply.with("evidence", `[]`))
	plan("evidence as JSON null", "23514", apply.with("evidence", `null`))
	plan("route by DNS name", "23514", apply.with("route", "cp.example.test:50000"))
	plan("idempotency key of 15 characters", "23514", apply.with("idempotency_key", strings.Repeat("k", 15)))
	plan("machine of another cluster", "23503", apply.with("cluster", p.other))
	// m2 has its own timeline entry, so only the release's coverage can refuse the row.
	uncovered := apply.with("machine", m2, "assignment_revision", asr2, "desired_release", nil, "kind", "adopt",
		"mode", nil, "baseline_revision", nil, "expected_digest", nil, "transport_deadline", nil,
		"verification_deadline", nil, "max_attempts", nil)
	refused(t, db, "release not covering the machine", "23503/plan_release_machine_fkey",
		p.entry(m2, 100, "plan"), uncovered.stmt(), state(uncovered))
	plan("assignment revision of another machine", "23503", apply.with("assignment_revision", asr2))
	plan("creator who is no principal", "23503", apply.with("created_by", id.New(id.Principal)))
	plan("creator of another kind", "23503", apply.with("created_by_kind", "service"))
	plan("second plan under one creator's idempotency key", "23505",
		apply.with("idempotency_key", "plan-"+p.apply[4:20]))
	refused(t, db, "plan on an entry of another kind", "23503", p.entry(p.machine, 100, "approval"), apply.stmt(), state(apply))
	refused(t, db, "plan with no state", "23514/plan_state_required", at, apply.stmt())
	refused(t, db, "state of no plan", "23503", stmt{insertPlanState, []any{id.New(id.Plan)}})
	refused(t, db, "plan created approved", "23514/plan_state_transition", at, apply.stmt(),
		stmt{`INSERT INTO plan_state (plan, state, approval, updated_at) VALUES ($1, 'approved', $2, now())`,
			[]any{apply.vals["id"], p.approval}})

	// Approvals (T5a): one per plan and epoch, by a human approver, with a known self-approval mark.
	apr, act := id.New(id.Approval), id.New(id.Act)
	approval := p.approvalRow(apr, p.adopt, 100, act)
	approvalAt, approvalAct := p.entry(p.machine, 100, "approval"), p.act(act, p.human, "approver")
	approve := func(name, want string, r record) {
		t.Helper()
		refused(t, db, name, want, approvalAt, r.stmt(), approvalAct)
	}
	approve("approval named by a plan id", "23514", approval.with("id", id.New(id.Plan)))
	approve("second approval of a plan in one epoch", "23505", approval.with("plan", p.apply))
	approve("approval by a service identity", "23503", approval.with("approver", p.service))
	approve("approval in role publisher", "23514", approval.with("role", "publisher"))
	approve("unknown self-approval reason", "23514/approval_self_approval", approval.with("self_approval", "{reviewed}"))
	approve("self-approval reason twice", "23514/approval_self_approval",
		approval.with("self_approval", "{created-plan,created-plan}"))
	approve("approval on another machine's timeline", "23503", approval.with("machine", m2))
	refused(t, db, "approval with no act", "23503", approvalAt, approval.stmt())
	refused(t, db, "approval on an entry of another kind", "23503", p.entry(p.machine, 100, "plan"), approval.stmt(), approvalAct)
	// Each record is its own timeline entry (execution and recovery §4.1).
	act2 := id.New(id.Act)
	refused(t, db, "two approvals on one entry", "23505/approval_machine_revision_key", p.entry(p.machine, 99, "plan"),
		apply.with("revision", 99).stmt(), state(apply), approvalAt, approval.stmt(), approvalAct,
		p.approvalRow(id.New(id.Approval), apply.vals["id"].(string), 100, act2).stmt(), p.act(act2, p.human, "approver"))
	refused(t, db, "two plans on one entry", "23505/plan_machine_revision_key", at, apply.stmt(), state(apply),
		adopt.with("idempotency_key", "plan-second-entry").stmt(), state(adopt))

	// Revocations (T5b) and cancellations (T11), each its own timeline entry.
	ract := id.New(id.Act)
	revocation := p.revocationRow(p.approval, p.apply, 100, ract)
	revoke := func(name, want string, r record) {
		t.Helper()
		refused(t, db, name, want, p.entry(p.machine, 100, "approval-revocation"), r.stmt(), p.act(ract, p.human, "approver"))
	}
	revoke("revocation in role publisher", "23514", revocation.with("role", "publisher"))
	revoke("revocation naming another plan", "23503", revocation.with("plan", p.adopt))
	revoke("revocation by a service identity", "23503", revocation.with("revoked_by", p.service))
	revoke("revocation with no reason", "23514", revocation.with("reason", " "))
	cact := id.New(id.Act)
	cancellation := p.cancellationRow(p.adopt, 100, cact)
	cancel := func(name, want string, r record) {
		t.Helper()
		refused(t, db, name, want, p.entry(p.machine, 100, "plan-cancellation"), r.stmt(), p.act(cact, p.human, "publisher"))
	}
	cancel("cancellation by a publisher who did not create the plan", "23503", cancellation.with("cancelled_by", p.bob))
	cancel("cancellation by a service identity as approver", "23514",
		cancellation.with("cancelled_by", p.service, "cancelled_by_kind", "service", "role", "approver"))
	cancel("cancellation in role author", "23514", cancellation.with("role", "author"))
	cancel("cancellation with no reason", "23514", cancellation.with("reason", ""))
	cancel("cancellation on another machine's timeline", "23503", cancellation.with("machine", m2))

	// Observations (execution and recovery §4.1): the started entry, then the result recorded after it.
	start := func(name, want string, r record) {
		t.Helper()
		refused(t, db, name, want, p.entry(p.machine, 100, "observation-started"), r.stmt())
	}
	start("evidence observation for no plan", "23514/observation_start_purpose", p.startRow(100, "evidence", nil, nil))
	start("drift observation for a plan", "23514/observation_start_purpose", p.startRow(100, "drift", p.apply, nil))
	start("completion observation for no operation", "23514/observation_start_purpose",
		p.startRow(100, "completion", p.apply, nil))
	start("unknown purpose", "23514", p.startRow(100, "curiosity", nil, nil))
	start("observation dialing another endpoint than the plan's route", "23503",
		p.startRow(100, "evidence", p.apply, nil).with("endpoint", "10.55.0.4:50000"))
	start("observation for an operation of no plan", "23514", p.startRow(100, "recovery", nil, id.New(id.Operation)))
	// An existing operation of p.apply, named for p.adopt, then for its own plan as the control.
	applyOp := id.New(id.Operation)
	applyCommitted := []stmt{{insertPlanOperation, []any{applyOp, "apply-config", "committed", owner, p.apply, p.machine, p.cluster}},
		{`UPDATE plan_state SET state = 'committed', operation = $2, revision = revision + 1 WHERE plan = $1`,
			[]any{p.apply, applyOp}}, p.entry(p.machine, 100, "observation-started")}
	refused(t, db, "observation for another plan's operation", "23503/observation_start_operation_plan_fkey",
		append(applyCommitted, p.startRow(100, "recovery", p.adopt, applyOp).stmt())...)
	accepted(t, db, "observation for its plan's operation", append(applyCommitted, p.startRow(100, "recovery", p.apply, applyOp).stmt())...)
	refused(t, db, "observation started on an entry of another kind", "23503", p.entry(p.machine, 100, "observation"),
		p.startRow(100, "drift", nil, nil).stmt())
	started := []stmt{p.entry(p.machine, 100, "observation-started"), p.startRow(100, "drift", nil, nil).stmt(),
		p.entry(p.machine, 101, "observation")}
	obs := p.observationRow(id.New(id.Observation), 100, 101)
	observe := func(name, want string, r record) {
		t.Helper()
		refused(t, db, name, want, append(append([]stmt{}, started...), r.stmt())...)
	}
	observe("observation named by a plan id", "23514", obs.with("id", id.New(id.Plan)))
	observe("observation of no start", "23503", obs.with("basis", 99))
	observe("observation recorded at its basis", "23514", obs.with("revision", 100))
	observe("failed access read with a digest", "23514/observation_unavailable",
		obs.with("access_path", nil, "access_version", nil, "access_created", nil))
	observe("access version without its path", "23514", obs.with("access_path", nil))
	observe("access path of no cluster", "23514", obs.with("access_path", "access/talos/x"))
	observe("unread values as an array", "23514", obs.with("unread", `[]`))
	observe("health as an array", "23514", obs.with("health", `[]`))
	observe("digest of 31 bytes", "23514", obs.with("configuration_digest", digest(1)[:31]))
	refused(t, db, "second observation of one start", "23505", append(append([]stmt{}, started...), obs.stmt(),
		p.entry(p.machine, 102, "observation"), obs.with("id", id.New(id.Observation), "revision", 102).stmt())...)
	// The positive control: a failed access read records no value and its cause.
	commitRows(t, db, append(append([]stmt{}, started...), obs.with("access_path", nil, "access_version", nil, "access_created", nil,
		"smbios_uuid", nil, "running_version", nil, "configuration_digest", nil, "resource_version", nil, "health", nil,
		"unread", `{"talos-access":"talos-access-unavailable"}`).stmt())...)
}

// PA §8.1: a plan's state moves only along execution and recovery §2's transitions, and its
// terminal states are final.
func TestPlanStateTransitions(t *testing.T) {
	db, _ := installed(t)
	p := planRows(t, db)
	set := func(planID, set string, args ...any) stmt {
		return stmt{`UPDATE plan_state SET ` + set + `, revision = revision + 1 WHERE plan = $1`, append([]any{planID}, args...)}
	}
	refused(t, db, "proposed to committed", "23514/plan_state_transition", set(p.adopt, "state = 'committed'"))
	refused(t, db, "proposed to revoked", "23514/plan_state_transition",
		set(p.adopt, "state = 'revoked', approval = $2, reason = 'approval-revoked'", p.approval))
	refused(t, db, "approved back to proposed", "23514/plan_state_transition", set(p.apply, "state = 'proposed', approval = NULL"))
	refused(t, db, "approved again by the same approval", "23514/plan_state_transition", set(p.apply, "state = 'approved'"))
	refused(t, db, "revoked with another approval", "23514/plan_state_transition",
		set(p.apply, "state = 'revoked', approval = NULL, reason = 'approval-revoked'"))
	refused(t, db, "state moved to another plan", "23514/plan_state_transition", set(p.apply, "plan = $2", p.adopt))
	refused(t, db, "committed with no operation", "23514/plan_state_operation", set(p.apply, "state = 'committed'"))
	refused(t, db, "approved with no approval", "23514", set(p.adopt, "state = 'approved'"))
	refused(t, db, "approved with another plan's approval", "23503", set(p.adopt, "state = 'approved', approval = $2", p.approval))
	refused(t, db, "cancelled with no reason", "23514", set(p.adopt, "state = 'cancelled'"))
	refused(t, db, "expired for a revocation", "23514", set(p.adopt, "state = 'expired', reason = 'approval-revoked'"))
	apr0, act0 := id.New(id.Approval), id.New(id.Act)
	refused(t, db, "approved with a reason", "23514/plan_state_reason", p.entry(p.machine, 4, "approval"),
		p.approvalRow(apr0, p.adopt, 4, act0).stmt(), p.act(act0, p.human, "approver"),
		set(p.adopt, "state = 'approved', approval = $2, reason = 'expired'", apr0))
	refused(t, db, "state deleted", ImmutableSQLState, stmt{`DELETE FROM plan_state WHERE plan = $1`, []any{p.adopt}})
	refused(t, db, "states truncated", ImmutableSQLState, stmt{`TRUNCATE plan_state CASCADE`, nil})
	// A terminal state is final.
	mustExec(t, db, set(p.adopt, "state = 'expired', reason = 'expired'").q, p.adopt)
	for _, to := range []string{"proposed", "approved", "cancelled"} {
		refused(t, db, "expired to "+to, "23514/plan_state_transition", set(p.adopt, "state = $2", to))
	}
	// A restore enters a new epoch (§2): the earlier epoch's approval no longer commits the plan.
	epoch := id.New(id.Epoch)
	mustExec(t, db, `INSERT INTO recovery_epoch (epoch, entered_at) VALUES ($1, now())`, epoch)
	mustExec(t, db, `UPDATE installation_state SET epoch = $1`, epoch)
	stale := id.New(id.Operation)
	refused(t, db, "committed under an approval of an earlier epoch", "23514/plan_state_approval_epoch",
		stmt{insertPlanOperation, []any{stale, "apply-config", "committed", owner, p.apply, p.machine, p.cluster}},
		set(p.apply, "state = 'committed', operation = $2", stale))
	// Positive controls: an approval in the new epoch replaces the earlier one, and a revocation of
	// the current approval revokes the plan.
	apr, act := id.New(id.Approval), id.New(id.Act)
	commitRows(t, db, p.entry(p.machine, 4, "approval"), p.approvalRow(apr, p.apply, 4, act).with("epoch", epoch).stmt(),
		p.act(act, p.human, "approver"), set(p.apply, "approval = $2", apr))
	// The earlier approval never returns.
	refused(t, db, "approval of an earlier epoch restored", "23514/plan_state_approval_epoch",
		set(p.apply, "approval = $2", p.approval))
	commitRows(t, db, set(p.apply, "state = 'revoked', reason = 'approval-revoked'"))
}

// Execution and recovery §3.2 comparisons 0, 4 and 5; §6.3: an apply-config or adopt operation is
// its plan's one operation, created with the commitment, on the plan's machine; one committed,
// sending, verifying or unresolved apply-config operation holds the machine and the cluster.
func TestPlanOperations(t *testing.T) {
	db, _ := installed(t)
	p := planRows(t, db)
	op := func(kind, state, own, planID, machine string) stmt {
		var o any
		if own != "" {
			o = own
		}
		return stmt{insertPlanOperation, []any{id.New(id.Operation), kind, state, o, planID, machine, p.cluster}}
	}
	committed := func(planID string) stmt {
		return stmt{`UPDATE plan_state SET state = 'committed', operation = (SELECT id FROM operation WHERE plan = $1),
			revision = revision + 1 WHERE plan = $1`, []any{planID}}
	}
	refused(t, db, "apply-config with no plan", "23514/operation_plan", op("apply-config", "committed", owner, "", p.machine).with(4, nil))
	refused(t, db, "adopt with no plan", "23514/operation_plan", op("adopt", "completed", "", "", p.machine).with(4, nil))
	refused(t, db, "publish naming a plan", "23514/operation_plan", stmt{`INSERT INTO operation (id, kind, state, epoch,
		draft, draft_revision, created_by, created_by_kind, created_role, created_at, plan, machine, cluster)
		SELECT $1, 'publish', 'queued', epoch, $2, 1, $3, 'human', 'publisher', now(), $4, $5, $6 FROM installation_state`,
		[]any{id.New(id.Operation), p.draft, p.human, p.apply, p.machine, p.cluster}})
	refused(t, db, "plan with no machine", "23514/operation_plan", op("apply-config", "committed", owner, p.apply, "").with(5, nil))
	refused(t, db, "adopt operation of an apply-config plan", "23503", op("adopt", "completed", "", p.apply, p.machine))
	refused(t, db, "operation on another machine than its plan's", "23503",
		op("apply-config", "committed", owner, p.apply, id.New(id.Machine)))
	refused(t, db, "operation whose plan is not committed to it", "23514/operation_plan_committed",
		op("apply-config", "committed", owner, p.apply, p.machine))
	for _, state := range []string{"committed", "sending", "verifying", "unresolved"} {
		refused(t, db, state+" apply-config with no owner", "23514/operation_apply_config_owned",
			op("apply-config", state, "", p.apply, p.machine), committed(p.apply))
	}
	// §6.3: an adopt operation is created completed, and binds no draft revision.
	refused(t, db, "adopt not completed", "23514", op("adopt", "committed", owner, p.adopt, p.machine))
	adoptDraft := func(draft, revision any) stmt {
		return stmt{`INSERT INTO operation (id, kind, state, epoch, draft, draft_revision, plan, machine, cluster, created_at)
			SELECT $1, 'adopt', 'completed', epoch, $2, $3, $4, $5, $6, now() FROM installation_state`,
			[]any{id.New(id.Operation), draft, revision, p.adopt, p.machine, p.cluster}}
	}
	refused(t, db, "adopt with a draft revision and no draft", "23514", adoptDraft(nil, 1))
	refused(t, db, "adopt with a draft and no revision", "23514", adoptDraft(p.draft, nil))
	refused(t, db, "adopt with a draft revision", "23514", adoptDraft(p.draft, 1))
	// Execution and recovery §3.2: an operation is committed in its approval's epoch; p.commit below
	// is the control, in the approval's.
	stale, staleOp := id.New(id.Epoch), id.New(id.Operation)
	mustExec(t, db, `INSERT INTO recovery_epoch (epoch, entered_at) VALUES ($1, now())`, stale)
	refused(t, db, "operation of another epoch than its plan's approval", "23514/operation_plan_committed",
		stmt{`INSERT INTO operation (id, kind, state, epoch, owner, owner_gen, owner_epoch, plan, machine, cluster, created_at)
			SELECT $1, 'apply-config', 'committed', $2, $3, 1, epoch, $4, $5, $6, now() FROM installation_state`,
			[]any{staleOp, stale, owner, p.apply, p.machine, p.cluster}},
		stmt{`UPDATE plan_state SET state = 'committed', operation = $2, revision = revision + 1 WHERE plan = $1`,
			[]any{p.apply, staleOp}})
	// PA §3 TimelineEvent: an apply-config operation's entries are on the machine timeline.
	opID := p.commit(t, db, p.apply)
	refused(t, db, "event of an apply-config operation", "23514/operation_event_job_kind",
		stmt{insertEvent, []any{opID, 1, `{}`, "apply-config"}})
	refused(t, db, "apply-config event named an ingest", "23503", stmt{insertEvent, []any{opID, 1, `{}`, "ingest"}})
	refused(t, db, "second operation of a plan", "23505", op("apply-config", "committed", owner, p.apply, p.machine))
	// What an operation is and was created for is fixed: an UPDATE cannot turn the release's
	// publish operation into an adopt operation of a proposed plan, or move an operation's scope.
	rebind := func(name, which, set string, args ...any) {
		t.Helper()
		refused(t, db, name, "BW001", stmt{`UPDATE operation SET ` + set + ` WHERE id = $1`, append([]any{which}, args...)})
	}
	rebind("publish operation rebound to an adopt plan", p.publish, `kind = 'adopt', state = 'completed', draft = NULL,
		draft_revision = NULL, created_by = NULL, created_by_kind = NULL, created_role = NULL, owner = NULL, owner_gen = 0,
		owner_epoch = NULL, lease_until = NULL, plan = $2, machine = $3, cluster = $4`, p.adopt, p.machine, p.cluster)
	rebind("operation moved to another plan", opID, "plan = $2", p.adopt)
	rebind("operation moved to another machine", opID, "machine = $2", p.otherMachine)
	rebind("operation moved to another cluster", opID, "cluster = $2", p.other)
	rebind("operation given another creator", p.publish, "created_by = $2", p.bob)
	rebind("operation redated", p.publish, "created_at = created_at - interval '1 second'")

	// The adopt plan's commitment: approved, observed, then its operation completed with its record.
	apr := p.approve(t, db, p.adopt, 4)
	obs := id.New(id.Observation)
	commitRows(t, db, p.entry(p.machine, 5, "observation-started"), p.startRow(5, "evidence", p.adopt, nil).stmt(),
		p.entry(p.machine, 6, "observation"), p.observationRow(obs, 5, 6).stmt())
	adoptOp := id.New(id.Operation)
	adopted := newRecord("adoption_record", "plan", p.adopt, "machine", p.machine, "cluster", p.cluster,
		"operation", adoptOp, "approval", apr, "observation", obs, "revision", 7, "epoch", p.epoch, "at", time.Now(),
		"baseline_digest", bytes.Repeat([]byte{7}, 32))
	adoptOpRow := stmt{insertPlanOperation, []any{adoptOp, "adopt", "completed", nil, p.adopt, p.machine, p.cluster}}
	adoption := func(name, want string, r record) {
		t.Helper()
		refused(t, db, name, want, adoptOpRow, committed(p.adopt), p.entry(p.machine, 7, "adoption"), r.stmt())
	}
	adoption("adoption record of an apply-config plan", "23503", adopted.with("plan", p.apply))
	adoption("adoption record of another plan's operation", "23503", adopted.with("operation", opID))
	adoption("adoption record relying on another plan's approval", "23503", adopted.with("approval", p.approval))
	adoption("adoption record relying on no observation", "23503", adopted.with("observation", id.New(id.Observation)))
	adoption("adoption record naming no baseline digest", "23502", adopted.with("baseline_digest", nil))
	adoption("adoption record with a short baseline digest", "23514", adopted.with("baseline_digest", bytes.Repeat([]byte{7}, 31)))
	refused(t, db, "adopt operation with no adoption record", "23514/operation_plan_committed", adoptOpRow, committed(p.adopt))
	refused(t, db, "adoption record on an entry of another kind", "23503", adoptOpRow, committed(p.adopt),
		p.entry(p.machine, 7, "plan"), adopted.stmt())
	// The record names the commitment its operation was created with: the state's approval, in the
	// epoch of the operation and of the approval.
	old, oldApr, oldAct := stale, id.New(id.Approval), id.New(id.Act)
	commitRows(t, db, p.entry(p.machine, 20, "approval"), p.approvalRow(oldApr, p.adopt, 20, oldAct).with("epoch", old).stmt(),
		p.act(oldAct, p.human, "approver"))
	adoption("adoption record relying on an approval of another epoch", "23503", adopted.with("approval", oldApr))
	adoption("adoption record of another epoch than its operation", "23503", adopted.with("epoch", old))
	// With the operation, the record and the approval all of that epoch, the state's approval is of
	// another, so the operation is not committed in its epoch.
	oldOpRow := stmt{`INSERT INTO operation (id, kind, state, epoch, plan, machine, cluster, created_at)
		VALUES ($1, 'adopt', 'completed', $2, $3, $4, $5, now())`, []any{adoptOp, old, p.adopt, p.machine, p.cluster}}
	refused(t, db, "adoption record naming an approval its commitment does not", "23514/operation_plan_committed",
		oldOpRow, committed(p.adopt), p.entry(p.machine, 7, "adoption"), adopted.with("approval", oldApr, "epoch", old).stmt())
	// The adopt operation is completed: it holds no scope while the apply-config operation does.
	commitRows(t, db, adoptOpRow, committed(p.adopt), p.entry(p.machine, 7, "adoption"), adopted.stmt())
	refused(t, db, "event of an adopt operation", "23514/operation_event_job_kind",
		stmt{insertEvent, []any{adoptOp, 1, `{}`, "adopt"}})
	// A refused commitment's entry names the transaction and the comparison that failed (§4.1).
	refusal := func(entry string) stmt {
		return stmt{insertMachineEvent, []any{p.machine, 90, "refusal", entry}}
	}
	refused(t, db, "refusal entry naming no comparison", "23514/machine_event_refusal", refusal(`{"transaction": "T6"}`))
	refused(t, db, "refusal entry naming no transaction", "23514/machine_event_refusal", refusal(`{"comparison": "4.4"}`))
	commitRows(t, db, refusal(`{"transaction": "T6", "comparison": "4.4", "plan": "`+p.adopt+`"}`))
	// A commitment's entry names the operation it created, its plan and the evidence it links (§4.1).
	commitment := func(entry string) stmt {
		return stmt{insertMachineEvent, []any{p.machine, 91, "commitment", entry}}
	}
	for name, entry := range map[string]string{
		"no operation":   `{"plan": "pln_x", "observation": "obs_x"}`,
		"no plan":        `{"operation": "op_x", "observation": "obs_x"}`,
		"no observation": `{"operation": "op_x", "plan": "pln_x"}`,
		"a number":       `{"operation": "op_x", "plan": "pln_x", "observation": 1}`,
	} {
		refused(t, db, "commitment entry naming "+name, "23514/machine_event_commitment", commitment(entry))
	}
	commitRows(t, db, commitment(`{"operation": "op_x", "plan": "pln_x", "observation": "obs_x"}`))

	// Comparisons 4 and 5: a second plan of the machine cannot commit while the first holds it;
	// each index refuses it alone.
	second := id.New(id.Plan)
	p.propose(t, db, p.planRow(second, 8))
	p.approve(t, db, second, 9)
	commitSecond := []stmt{op("apply-config", "committed", owner, second, p.machine), committed(second)}
	refused(t, db, "second committed apply-config of the machine", "23505", commitSecond...)
	for _, c := range []struct{ drop, want string }{
		{"DROP INDEX operation_machine_scope", "23505/operation_rollout_scope"},
		{"DROP INDEX operation_rollout_scope", "23505/operation_machine_scope"},
	} {
		refused(t, db, "after "+c.drop, c.want, append([]stmt{{c.drop, nil}}, commitSecond...)...)
	}
	// Issue ginsys/bronzeward#71: an owned operation moves through each fenced state, and the fence
	// and the scopes end at a terminal state, which may drop the owner. Its attempt is what lets it
	// reach sending (execution and recovery §4).
	now := time.Now()
	commitRows(t, db, p.entry(p.machine, 30, "attempt"), newRecord("attempt", "id", id.New(id.Attempt), "operation", opID,
		"plan", p.apply, "machine", p.machine, "number", 1, "owner", owner, "owner_gen", 1, "owner_epoch", p.epoch,
		"route", "10.55.0.3:50000", "observation", obs, "transport_deadline", now.Add(time.Minute),
		"verification_deadline", now.Add(5*time.Minute), "at", now, "revision", 30).stmt())
	for _, state := range []string{"sending", "verifying", "unresolved"} {
		mustExec(t, db, `UPDATE operation SET state = $2 WHERE id = $1`, opID, state)
	}
	mustExec(t, db, `UPDATE operation SET state = 'failed', owner = NULL, owner_gen = 0, owner_epoch = NULL WHERE id = $1`, opID)
	commitRows(t, db, commitSecond...)
}

// with returns the statement with argument i replaced.
func (s stmt) with(i int, v any) stmt {
	args := append([]any{}, s.args...)
	args[i] = v
	return stmt{s.q, args}
}

// Choice §17.3: each immutable plan table refuses UPDATE, DELETE and TRUNCATE; the machine
// identity and the cluster's Talos cluster ID a plan binds never change.
func TestPlanImmutableTables(t *testing.T) {
	db, _ := installed(t)
	p := planRows(t, db)
	act := id.New(id.Act)
	commitRows(t, db, p.entry(p.machine, 4, "approval-revocation"), p.revocationRow(p.approval, p.apply, 4, act).stmt(),
		p.act(act, p.human, "approver"))
	act = id.New(id.Act)
	commitRows(t, db, p.entry(p.machine, 5, "plan-cancellation"), p.cancellationRow(p.adopt, 5, act).stmt(),
		p.act(act, p.human, "publisher"))
	commitRows(t, db, p.entry(p.machine, 6, "observation-started"), p.startRow(6, "drift", nil, nil).stmt(),
		p.entry(p.machine, 7, "observation"), p.observationRow(id.New(id.Observation), 6, 7).stmt())
	tables := []string{"plan", "approval", "approval_revocation", "plan_cancellation", "observation_start", "observation"}
	for _, table := range tables {
		for _, q := range []string{"UPDATE " + table + " SET machine = machine", "DELETE FROM " + table, "TRUNCATE " + table + " CASCADE"} {
			if _, err := db.Exec(q); sqlState(err) != ImmutableSQLState {
				t.Errorf("%s: %v; want SQLSTATE %s", q, err, ImmutableSQLState)
			}
		}
		if count(t, db, table) == 0 {
			t.Errorf("%s: a refused statement removed its rows", table)
		}
	}
	for _, q := range []string{
		"UPDATE machine SET smbios_uuid = '4f9e0a5c-6d7b-4c9d-8e3f-4a5b6c7d8e9f'",
		"UPDATE machine SET smbios_uuid = NULL, talos_node_id = 'node-1' WHERE id = '" + p.machine + "'",
		"UPDATE machine SET cluster = '" + p.other + "' WHERE id = '" + p.machine + "'",
		"UPDATE cluster SET talos_cluster_id = translate(encode(sha256('x'), 'base64'), '+/', '-_') WHERE id = '" + p.cluster + "'",
	} {
		if _, err := db.Exec(q); sqlState(err) != ImmutableSQLState {
			t.Errorf("%s: %v; want SQLSTATE %s", q, err, ImmutableSQLState)
		}
	}
	// The endpoint and the scope fields stay mutable (§3.3).
	mustExec(t, db, `UPDATE machine SET talos_endpoint = '10.55.0.9:50000', frozen = true WHERE id = $1`, p.machine)
	mustExec(t, db, `UPDATE cluster SET name = 'renamed' WHERE id = $1`, p.cluster)
}

// The control for the checks above: with each named constraint dropped, a row it refuses commits,
// so that constraint, not another, refuses it.
func TestPlanConstraintControl(t *testing.T) {
	db, _ := installed(t)
	p := planRows(t, db)
	apply := p.planRow(id.New(id.Plan), 100)
	at := p.entry(p.machine, 100, "plan")
	state := stmt{insertPlanState, []any{apply.vals["id"]}}
	act := id.New(id.Act)
	for _, c := range []struct {
		drop string
		rows []stmt
	}{
		{"ALTER TABLE plan DROP CONSTRAINT plan_kind_bindings", []stmt{at, apply.with("mode", nil).stmt(), state}},
		{"ALTER TABLE plan_state DROP CONSTRAINT plan_state_operation",
			[]stmt{{`UPDATE plan_state SET state = 'committed' WHERE plan = $1`, []any{p.apply}}}},
		{"DROP TRIGGER transition ON plan_state", []stmt{{`UPDATE plan_state SET state = 'proposed', approval = NULL WHERE plan = $1`,
			[]any{p.apply}}}},
		{"DROP TRIGGER plan_state_required ON plan", []stmt{at, apply.stmt()}},
		{"ALTER TABLE approval DROP CONSTRAINT approval_self_approval", []stmt{p.entry(p.machine, 100, "approval"),
			p.approvalRow(id.New(id.Approval), p.adopt, 100, act).with("self_approval", "{reviewed}").stmt(),
			p.act(act, p.human, "approver")}},
		{"ALTER TABLE observation_start DROP CONSTRAINT observation_start_purpose", []stmt{
			p.entry(p.machine, 100, "observation-started"), p.startRow(100, "drift", p.apply, nil).stmt()}},
		{"ALTER TABLE observation DROP CONSTRAINT observation_unavailable", []stmt{
			p.entry(p.machine, 100, "observation-started"), p.startRow(100, "drift", nil, nil).stmt(),
			p.entry(p.machine, 101, "observation"),
			p.observationRow(id.New(id.Observation), 100, 101).with("access_path", nil, "access_version", nil,
				"access_created", nil).stmt()}},
		{"ALTER TABLE operation DROP CONSTRAINT operation_plan", []stmt{
			{insertPlanOperation, []any{id.New(id.Operation), "apply-config", "committed", owner, nil, nil, nil}}}},
		{"ALTER TABLE operation DROP CONSTRAINT operation_apply_config_owned", []stmt{
			{insertPlanOperation, []any{id.New(id.Operation), "apply-config", "sending", nil, p.apply, p.machine, p.cluster}},
			{`UPDATE plan_state SET state = 'committed', operation = (SELECT id FROM operation WHERE plan = $1) WHERE plan = $1`,
				[]any{p.apply}}}},
		{"ALTER TABLE operation_event DROP CONSTRAINT operation_event_job_kind", []stmt{
			{insertPlanOperation, []any{"op_aaaaaaaaaaaaaaaaaaaaaaaaaa", "apply-config", "committed", owner, p.apply, p.machine, p.cluster}},
			{`UPDATE plan_state SET state = 'committed', operation = 'op_aaaaaaaaaaaaaaaaaaaaaaaaaa' WHERE plan = $1`, []any{p.apply}},
			{insertEvent, []any{"op_aaaaaaaaaaaaaaaaaaaaaaaaaa", 1, `{}`, "apply-config"}}}},
		{"ALTER TABLE plan_state DROP CONSTRAINT plan_state_reason", []stmt{p.entry(p.machine, 100, "approval"),
			p.approvalRow("apr_aaaaaaaaaaaaaaaaaaaaaaaaaa", p.adopt, 100, "act_aaaaaaaaaaaaaaaaaaaaaaaaaa").stmt(),
			p.act("act_aaaaaaaaaaaaaaaaaaaaaaaaaa", p.human, "approver"),
			{`UPDATE plan_state SET state = 'approved', approval = 'apr_aaaaaaaaaaaaaaaaaaaaaaaaaa', reason = 'expired'
				WHERE plan = $1`, []any{p.adopt}}}},
		{"DROP TRIGGER binding ON operation", []stmt{{`UPDATE operation SET created_at = created_at - interval '1 second'
			WHERE id = $1`, []any{p.publish}}}},
		{"DROP TRIGGER plan_committed ON operation", []stmt{
			{insertPlanOperation, []any{id.New(id.Operation), "apply-config", "committed", owner, p.apply, p.machine, p.cluster}}}},
		{"DROP TRIGGER identity ON machine", []stmt{{`UPDATE machine SET smbios_uuid = NULL, talos_node_id = 'node-1' WHERE id = $1`,
			[]any{p.otherMachine}}}},
		{"DROP TRIGGER identity ON cluster", []stmt{{`UPDATE cluster SET talos_cluster_id = translate(encode(sha256('x'), 'base64'), '+/', '-_')
			WHERE id = $1`, []any{p.other}}}},
	} {
		func() {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			for _, s := range append([]stmt{{c.drop, nil}}, c.rows...) {
				if _, err := tx.Exec(s.q, s.args...); err != nil {
					t.Errorf("after %s: %v; want it to commit", c.drop, err)
					return
				}
			}
			// Runs the deferred checks as a commit would, then rolls back, so each case starts from
			// planRows' state.
			if _, err := tx.Exec(`SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
				t.Errorf("after %s: deferred checks: %v", c.drop, err)
			}
		}()
	}
}

// Execution and recovery §4.1: every table whose rows are machine timeline entries (an
// entry_kind column) holds at most one row per entry, so no two records share one.
func TestPlanEntriesUnique(t *testing.T) {
	db, _ := migrated(t)
	rows, err := db.Query(`SELECT c.relname, EXISTS (SELECT FROM pg_constraint k WHERE k.conrelid = c.oid
		AND k.contype IN ('p', 'u') AND (SELECT array_agg(attname::text ORDER BY attname) FROM pg_attribute
		WHERE attrelid = c.oid AND attnum = ANY (k.conkey)) = ARRAY['machine', 'revision'])
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace JOIN pg_attribute a ON a.attrelid = c.oid
		WHERE n.nspname = 'public' AND c.relkind = 'r' AND a.attname = 'entry_kind' ORDER BY c.relname`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		var unique bool
		if err := rows.Scan(&name, &unique); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
		if !unique {
			t.Errorf("%s: no key on (machine, revision)", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// The query found the tables it is about, so a renamed column cannot make it pass empty.
	want := []string{"adoption_record", "approval", "approval_revocation", "attempt", "observation", "observation_start", "plan",
		"plan_cancellation"}
	if !slices.Equal(tables, want) {
		t.Errorf("timeline record tables %v; want %v", tables, want)
	}
}
