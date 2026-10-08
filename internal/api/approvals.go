package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

func planApproval() effectRoute {
	return effectRoute{action: "plan.approve", input: func() input { return &approvalInput{} }, effect: approvePlan}
}

// approvalInput is T5a's request, {} (§9.2): the plan is the path's and the approver the caller.
type approvalInput struct{}

func (*approvalInput) check(*API) error { return nil }

// approvalBy is who approved and in which role.
type approvalBy struct {
	Principal string `json:"principal"`
	Role      string `json:"role"`
}

// selfApproval is an approval's self-approval mark (§10.5): marked when any reason holds, with
// every reason that holds in §10.5's table order.
type selfApproval struct {
	Marked  bool     `json:"marked"`
	Reasons []string `json:"reasons"`
}

// approvalBody is §9.3's approval resource.
type approvalBody struct {
	ID           string       `json:"id"`
	Plan         string       `json:"plan"`
	Approver     approvalBy   `json:"approver"`
	Epoch        string       `json:"epoch"`
	SelfApproval selfApproval `json:"selfApproval"`
}

// approvePlan is T5a (persistence-api.md §5): the plan's machine FOR UPDATE, its scope not
// pre-restore unaccounted; the approver's principal FOR SHARE, not revoked (rule 2); the plan's
// state FOR UPDATE, proposed, or approved by an approval of an earlier epoch, and unexpired once
// the act-order lock is held (rule 4). The approval is one approval entry on the machine's
// timeline, and the plan becomes approved under it.
func approvePlan(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
	plan := q.r.PathValue("id")
	if id.MustHave(plan, id.Plan) != nil {
		return result{}, refuse(http.StatusNotFound, "not-found", "no such plan")
	}
	b := approvalBody{ID: id.New(id.Approval), Plan: plan, Approver: approvalBy{Principal: q.principal.ID, Role: string(q.role)},
		Epoch: q.epoch, SelfApproval: selfApproval{Reasons: []string{}}}
	// A plan row is immutable, so its machine is read before the machine's lock.
	var machine, scope string
	err := tx.QueryRowContext(ctx, `SELECT machine FROM plan WHERE id = $1`, plan).Scan(&machine)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return result{}, refuse(http.StatusNotFound, "not-found", "no such plan").with("plan", plan)
	case err != nil:
		return result{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT scope_state FROM machine WHERE id = $1 FOR UPDATE`, machine).Scan(&scope); err != nil {
		return result{}, err
	}
	if scope == "pre-restore-unaccounted" {
		return result{}, refuse(http.StatusConflict, "recovery-mode-active",
			"the machine's scope is still pre-restore unaccounted").with("machine", machine).with("scope", scope)
	}
	var revoked bool
	if err := tx.QueryRowContext(ctx, `SELECT revoked FROM principal WHERE id = $1 FOR SHARE`, q.principal.ID).Scan(&revoked); err != nil {
		return result{}, err
	}
	if revoked {
		return result{}, refuse(http.StatusForbidden, "identity-revoked", "")
	}
	// The state as selectPlan reads it, but for the expiry, which is judged after the act-order
	// wait; an approval of an earlier epoch is void (execution-recovery.md §2).
	var state string
	var earlier bool
	if err := tx.QueryRowContext(ctx, `SELECT CASE WHEN s.state = 'approved' AND ap.revoked AND (r.at IS NULL OR r.at < p.expires_at)
				THEN 'revoked' ELSE s.state END, a.epoch IS NOT NULL AND a.epoch <> $2
		FROM plan_state s JOIN plan p ON p.id = s.plan LEFT JOIN approval a ON a.id = s.approval
			LEFT JOIN principal ap ON ap.id = a.approver LEFT JOIN identity_revocation r ON r.identity = ap.id
		WHERE s.plan = $1 FOR UPDATE OF s`, plan, q.epoch).Scan(&state, &earlier); err != nil {
		return result{}, err
	}
	if state != "proposed" && (state != "approved" || !earlier) {
		return result{}, refuse(http.StatusConflict, "conflict",
			"the plan is neither proposed nor awaiting approval in the current epoch").with("plan", plan).with("state", state)
	}
	mark, err := json.Marshal(b.SelfApproval)
	if err != nil {
		return result{}, err
	}
	// PA §5 rule 4: the approval's time, and the expiry it is judged against, follow every lock
	// wait, the act-order lock's included.
	write := func(ctx context.Context, tx *sql.Tx) error {
		var at time.Time
		var expired bool
		if err := tx.QueryRowContext(ctx, `SELECT c.t, p.expires_at <= c.t FROM plan p, (SELECT clock_timestamp() AS t) c
			WHERE p.id = $1`, plan).Scan(&at, &expired); err != nil {
			return err
		}
		if expired {
			return refuse(http.StatusConflict, "conflict", "the plan has expired").with("plan", plan).with("state", "expired")
		}
		var rev int64
		if err := tx.QueryRowContext(ctx, `UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1
			RETURNING revision_counter`, machine).Scan(&rev); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
			VALUES ($1, $2, $3, 'approval', jsonb_build_object('plan', $4::text, 'approval', $5::text, 'principal', $6::text,
				'role', $7::text, 'selfApproval', $8::jsonb), $9)`,
			machine, rev, q.epoch, plan, b.ID, b.Approver.Principal, b.Approver.Role, mark, at); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO approval (id, plan, machine, approver, role, epoch, self_approval, act, at, revision)
			VALUES ($1, $2, $3, $4, $5, $6, string_to_array($7, ','), $8, $9, $10)`,
			b.ID, plan, machine, b.Approver.Principal, b.Approver.Role, q.epoch, strings.Join(b.SelfApproval.Reasons, ","),
			q.actID, at, rev); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE plan_state SET state = 'approved', approval = $2, revision = revision + 1,
			updated_at = $3 WHERE plan = $1`, plan, b.ID, at)
		return err
	}
	return result{status: http.StatusCreated, location: prefix + "/approvals/" + b.ID, body: &b,
		subjects: []string{b.ID, plan, machine}, atActOrder: write}, nil
}
