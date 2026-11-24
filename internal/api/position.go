package api

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// A machine's position (persistence-api §9.3): where one machine stands in import, publish, plan,
// approve and apply, every member read in one REPEATABLE READ snapshot at one clock reading.

type positionIngestion struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	State       string    `json:"state"`
	Draft       string    `json:"draft"`
	Operation   *string   `json:"operation"`
	CreatedAt   time.Time `json:"createdAt"`
	LeaseLapsed bool      `json:"leaseLapsed"`
}

type positionDraft struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Revision int    `json:"revision"`
}

type positionPlan struct {
	ID              string    `json:"id"`
	State           string    `json:"state"`
	Operation       string    `json:"operation"`
	Release         string    `json:"release"`
	ApprovalCurrent *bool     `json:"approvalCurrent"`
	CreatedAt       time.Time `json:"createdAt"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

type positionOperation struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	State     string    `json:"state"`
	Plan      string    `json:"plan"`
	CreatedAt time.Time `json:"createdAt"`
}

type positionHolder struct {
	ID      string `json:"id"`
	Machine string `json:"machine"`
}

type positionBody struct {
	Machine       machineBody        `json:"machine"`
	Ingestion     *positionIngestion `json:"ingestion"`
	Draft         *positionDraft     `json:"draft"`
	Plan          *positionPlan      `json:"plan"`
	Operation     *positionOperation `json:"operation"`
	RolloutHeldBy *positionHolder    `json:"rolloutHeldBy"`
	// HostnameHeldBy is the other machine whose Applied release holds the node name the plan's
	// release gives this machine (execution-recovery.md §3.2 comparison 6a): why an approved plan
	// has not been sent. It names no hostname.
	HostnameHeldBy *positionNameHolder `json:"hostnameHeldBy"`
	Step           string              `json:"step"`
}

// positionNameHolder is a machine as the position names it: its identifier and its name, null when
// it has none.
type positionNameHolder struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}

// positionStep is the first row of §9.3's table whose facts hold in b.
func positionStep(b *positionBody) string {
	in, pl, op, m := b.Ingestion, b.Plan, b.Operation, &b.Machine
	switch {
	case op != nil && op.State == "unresolved":
		return "operation-unresolved"
	case op != nil:
		return "operation-in-flight"
	case pl != nil && pl.State == "approved" && pl.ApprovalCurrent != nil && *pl.ApprovalCurrent:
		return "awaiting-dispatch"
	case pl != nil:
		return "awaiting-approval"
	case in != nil && in.State == "paused":
		return "import-paused"
	case in != nil && (in.State == "held" || in.State == "resumed") && in.LeaseLapsed:
		return "import-stalled"
	case in != nil && (in.State == "held" || in.State == "resumed"):
		return "import-running"
	case in != nil && in.State == "released" && b.Draft != nil && b.Draft.State == "open":
		return "draft-to-publish"
	case m.OpenDrift != nil:
		return "drift-open"
	case m.Desired != nil && (m.Applied == nil || m.PendingConvergence):
		return "needs-plan"
	case m.Desired == nil && m.Applied == nil:
		return "not-handed-over"
	}
	return "no-step"
}

var getPosition = itemOn(readSnapshot, id.Machine, func(ctx context.Context, tx *sql.Tx, machine string) (string, any, error) {
	b, err := readPosition(ctx, tx, machine)
	return "", b, err
})

// readPosition reads every member in tx, which holds the installation state FOR SHARE in a
// REPEATABLE READ snapshot, with one clock reading taken after the lock.
func readPosition(ctx context.Context, tx *sql.Tx, machine string) (*positionBody, error) {
	b := &positionBody{}
	var err error
	if b.Machine, err = scanMachine(tx.QueryRowContext(ctx, selectMachine+` WHERE m.id = $1`, machine)); err != nil {
		return nil, err // sql.ErrNoRows is 404
	}
	var clock time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&clock); err != nil {
		return nil, err
	}
	if b.Ingestion, err = positionIngestionAt(ctx, tx, machine, clock); err != nil {
		return nil, err
	}
	if b.Ingestion != nil {
		b.Draft = &positionDraft{}
		if err := tx.QueryRowContext(ctx, `SELECT id, state, revision FROM draft WHERE id = $1`, b.Ingestion.Draft).Scan(
			&b.Draft.ID, &b.Draft.State, &b.Draft.Revision); err != nil {
			return nil, err
		}
	}
	if b.Plan, err = positionPlanAt(ctx, tx, &b.Machine, clock); err != nil {
		return nil, err
	}
	if b.Operation, b.RolloutHeldBy, err = positionScopes(ctx, tx, &b.Machine); err != nil {
		return nil, err
	}
	if b.Plan != nil && b.Plan.Operation == "apply-config" {
		holder, err := hostnameHeldBy(ctx, tx, machine, b.Plan.Release)
		if err != nil {
			return nil, err
		}
		if holder != nil {
			b.HostnameHeldBy = &positionNameHolder{ID: holder.ID}
			if holder.Name != "" {
				b.HostnameHeldBy.Name = &holder.Name
			}
		}
	}
	b.Step = positionStep(b)
	return b, nil
}

// positionIngestionAt is the machine's newest live ingestion, else its newest of any state, by
// creation time then identifier, with each claim's state read at clock.
func positionIngestionAt(ctx context.Context, tx *sql.Tx, machine string, clock time.Time) (*positionIngestion, error) {
	c, err := scanIngestion(tx.QueryRowContext(ctx, selectIngestionsAt("$2")+` WHERE c.machine = $1
		ORDER BY c.state IN ('held', 'paused', 'resumed') DESC, c.created_at DESC, c.id DESC LIMIT 1`, machine, clock))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, err
	}
	return &positionIngestion{ID: c.ID, Kind: c.Kind, State: c.State, Draft: c.Draft, Operation: c.Operation,
		CreatedAt: c.CreatedAt.UTC(),
		// A transient claim past its lease reads abandoned, so this is an encrypted claim whose runner stopped.
		LeaseLapsed: (c.State == "held" || c.State == "resumed") && !c.LeaseUntil.After(clock)}, nil
}

// positionPlanAt is the newest plan of the machine that reads proposed or approved at clock and
// that would not be refused for its binding. The candidates are selected in SQL (the plan's state
// at clock, newest first, an adopt plan a refusal entry names left out); the comparison is the
// commitment's own Go (comparison2, driftCause), so the read follows it when it gains a term.
func positionPlanAt(ctx context.Context, tx *sql.Tx, m *machineBody, clock time.Time) (*positionPlan, error) {
	var head, desired sql.NullString
	var baseline sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT s.desired, s.baseline_revision, a.head_revision_id FROM machine_state s
		LEFT JOIN assignment a ON a.machine = s.machine WHERE s.machine = $1`, m.ID).Scan(&desired, &baseline, &head); err != nil {
		return nil, err
	}
	open := sql.NullInt64{}
	if m.OpenDrift != nil {
		open = sql.NullInt64{Int64: m.OpenDrift.Revision, Valid: true}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, state, kind, release, assignment_revision, desired_release, baseline_revision,
			drift_record, approval_current, created_at, expires_at
		FROM (SELECT p.id, `+planStateAt("$2")+` AS state, p.kind, p.release, p.assignment_revision, p.desired_release,
				p.baseline_revision, p.drift_record, a.epoch = (SELECT epoch FROM installation_state) AS approval_current,
				p.created_at, p.expires_at
			`+planFrom+`
			WHERE p.machine = $1 AND NOT (p.kind = 'adopt' AND EXISTS (SELECT FROM machine_event
				WHERE machine = $1 AND kind = 'refusal' AND entry->>'plan' = p.id))) c
		WHERE state IN ('proposed', 'approved') ORDER BY created_at DESC, id DESC`, m.ID, clock)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p positionPlan
		var assignment string
		var pDesired sql.NullString
		var pBaseline, pDrift sql.NullInt64
		var current sql.NullBool
		if err := rows.Scan(&p.ID, &p.State, &p.Operation, &p.Release, &assignment, &pDesired, &pBaseline, &pDrift,
			&current, &p.CreatedAt, &p.ExpiresAt); err != nil {
			return nil, err
		}
		if driftCause(open, pDrift) != "" ||
			comparison2(machineBinding{head: head, desired: desired, baseline: baseline},
				planBinding{assignment: assignment, desired: pDesired, baseline: pBaseline}) != "" {
			continue
		}
		p.CreatedAt, p.ExpiresAt = p.CreatedAt.UTC(), p.ExpiresAt.UTC()
		if current.Valid {
			p.ApprovalCurrent = &current.Bool
		}
		return &p, nil
	}
	return nil, rows.Err()
}

// positionScopes reads the apply-config operation holding the machine scope, and the one holding
// the machine's cluster rollout scope when it belongs to another machine.
func positionScopes(ctx context.Context, tx *sql.Tx, m *machineBody) (*positionOperation, *positionHolder, error) {
	var op *positionOperation
	var held *positionHolder
	o := positionOperation{}
	switch err := tx.QueryRowContext(ctx, `SELECT id, kind, state, plan, created_at FROM operation
		WHERE machine = $1 AND kind = 'apply-config' AND state IN ('committed', 'sending', 'verifying', 'unresolved')`, m.ID).Scan(
		&o.ID, &o.Kind, &o.State, &o.Plan, &o.CreatedAt); {
	case err == nil:
		o.CreatedAt = o.CreatedAt.UTC()
		op = &o
	case !errors.Is(err, sql.ErrNoRows):
		return nil, nil, err
	}
	h := positionHolder{}
	switch err := tx.QueryRowContext(ctx, `SELECT id, machine FROM operation
		WHERE cluster = $1 AND machine <> $2 AND kind = 'apply-config' AND state IN ('committed', 'sending', 'verifying', 'unresolved')`,
		m.Cluster, m.ID).Scan(&h.ID, &h.Machine); {
	case err == nil:
		held = &h
	case !errors.Is(err, sql.ErrNoRows):
		return nil, nil, err
	}
	return op, held, nil
}
