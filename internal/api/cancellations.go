package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/id"
)

func planCancellation() effectRoute {
	return effectRoute{action: "plan.cancel", input: func() input { return &reasonInput{} }, effect: cancelPlan}
}

// cancellationBody is §9.3's plan cancellation.
type cancellationBody struct {
	Plan        string    `json:"plan"`
	Machine     string    `json:"machine"`
	CancelledBy string    `json:"cancelledBy"`
	Role        string    `json:"role"`
	Reason      string    `json:"reason"`
	Act         string    `json:"act"`
	Epoch       string    `json:"epoch"`
	At          time.Time `json:"at"`
}

// projectedState is a clock reading and a plan's state as selectPlan reads it at that time (§8.1).
const projectedState = `SELECT c.t, CASE WHEN s.state = 'approved' AND ap.revoked AND (r.at IS NULL OR r.at < p.expires_at)
		THEN 'revoked' WHEN s.state IN ('proposed', 'approved') AND p.expires_at <= c.t THEN 'expired'
		ELSE s.state END
	FROM plan_state s JOIN plan p ON p.id = s.plan LEFT JOIN approval a ON a.id = s.approval
		LEFT JOIN principal ap ON ap.id = a.approver LEFT JOIN identity_revocation r ON r.identity = ap.id,
		(SELECT clock_timestamp() AS t) c
	WHERE s.plan = $1`

// cancelPlan is T11's plan cancellation (persistence-api.md §5, §10.3): the plan's machine FOR
// UPDATE; the cancelling identity's principal FOR SHARE, not revoked (rule 2); the plan's state FOR
// UPDATE, reading proposed, approved or committed once the act-order lock is held (rule 4). The
// cancellation is one plan-cancellation entry on the machine's timeline; a proposed or approved
// plan becomes cancelled, and a committed one keeps its state, the cancellation reaching its
// operation through execution-recovery §3.3.
func cancelPlan(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*reasonInput)
	plan := q.r.PathValue("id")
	if id.MustHave(plan, id.Plan) != nil {
		return result{}, refuse(http.StatusNotFound, "not-found", "no such plan")
	}
	// A plan row is immutable, so its machine and creator are read before the machine's lock.
	var machine, creator string
	err := tx.QueryRowContext(ctx, `SELECT machine, created_by FROM plan WHERE id = $1`, plan).Scan(&machine, &creator)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return result{}, refuse(http.StatusNotFound, "not-found", "no such plan").with("plan", plan)
	case err != nil:
		return result{}, err
	}
	// §10.3, choice §17.20: the first qualifying role in the route's order, where publisher
	// qualifies only for the plan's creator.
	q.role = ""
	for _, r := range q.route.roles {
		if slices.Contains(q.principal.Roles, r) && (r != auth.Publisher || q.principal.ID == creator) {
			q.role = r
			break
		}
	}
	if q.role == "" {
		return result{}, refuse(http.StatusForbidden, "forbidden", "a publisher cancels only the plans it created").
			with("plan", plan).with("roles", q.route.roles)
	}
	b := cancellationBody{Plan: plan, Machine: machine, CancelledBy: q.principal.ID, Role: string(q.role), Reason: in.Reason,
		Act: q.actID, Epoch: q.epoch}
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM machine WHERE id = $1 FOR UPDATE`, machine); err != nil {
		return result{}, err
	}
	var revoked bool
	if err := tx.QueryRowContext(ctx, `SELECT revoked FROM principal WHERE id = $1 FOR SHARE`, q.principal.ID).Scan(&revoked); err != nil {
		return result{}, err
	}
	if revoked {
		return result{}, refuse(http.StatusForbidden, "identity-revoked", "")
	}
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM plan_state WHERE plan = $1 FOR UPDATE`, plan); err != nil {
		return result{}, err
	}
	// cancellable reads the plan's state, refusing one that reads revoked, cancelled or expired, or a
	// committed plan whose cancellation is already recorded, and the time it was read at.
	cancellable := func(ctx context.Context, tx *sql.Tx) (string, time.Time, error) {
		var state string
		var at time.Time
		if err := tx.QueryRowContext(ctx, projectedState, plan).Scan(&at, &state); err != nil {
			return "", at, err
		}
		if state != "proposed" && state != "approved" && state != "committed" {
			return "", at, refuse(http.StatusConflict, "conflict", "the plan is already "+state).with("plan", plan).with("state", state)
		}
		var recorded bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM plan_cancellation WHERE plan = $1)`, plan).Scan(&recorded); err != nil {
			return "", at, err
		}
		if recorded {
			return "", at, refuse(http.StatusConflict, "conflict", "the plan's cancellation is already recorded").
				with("plan", plan).with("state", state)
		}
		return state, at, nil
	}
	if _, _, err := cancellable(ctx, tx); err != nil {
		return result{}, err
	}
	// PA §5 rule 4: the cancellation's time, and the state it is judged against, follow every lock
	// wait, the act-order lock's included.
	write := func(ctx context.Context, tx *sql.Tx) error {
		state, at, err := cancellable(ctx, tx)
		if err != nil {
			return err
		}
		b.At = at
		var rev int64
		if err := tx.QueryRowContext(ctx, `UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1
			RETURNING revision_counter`, machine).Scan(&rev); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
			VALUES ($1, $2, $3, 'plan-cancellation', jsonb_build_object('plan', $4::text, 'principal', $5::text, 'role', $6::text,
				'reason', $7::text), $8)`,
			machine, rev, q.epoch, plan, b.CancelledBy, b.Role, b.Reason, b.At); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO plan_cancellation (plan, machine, cancelled_by, cancelled_by_kind, role, reason,
			act, epoch, at, revision) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			plan, machine, b.CancelledBy, string(q.principal.Kind), b.Role, b.Reason, q.actID, q.epoch, b.At, rev); err != nil {
			return err
		}
		if state == "committed" {
			return nil
		}
		_, err = tx.ExecContext(ctx, `UPDATE plan_state SET state = 'cancelled', reason = 'cancelled', approval = NULL,
			revision = revision + 1, updated_at = $2 WHERE plan = $1`, plan, b.At)
		return err
	}
	return result{status: http.StatusOK, body: &b, subjects: []string{plan, machine}, atActOrder: write}, nil
}
