package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

func approvalRevocation() effectRoute {
	return effectRoute{action: "approval.revoke", input: func() input { return &reasonInput{} }, effect: revokeApproval}
}

// reasonInput is the body of an approval revocation and of a plan cancellation (§9.3): the reason
// the act records.
type reasonInput struct {
	Reason string `json:"reason"`
}

func (in *reasonInput) check(*API) error {
	if strings.TrimSpace(in.Reason) == "" || len(in.Reason) > maxReason {
		return fmt.Errorf("reason must be 1 to %d bytes and not blank", maxReason)
	}
	return nil
}

// approvalRevocationBody is §9.3's approval revocation.
type approvalRevocationBody struct {
	Approval  string    `json:"approval"`
	Plan      string    `json:"plan"`
	Machine   string    `json:"machine"`
	RevokedBy string    `json:"revokedBy"`
	Role      string    `json:"role"`
	Reason    string    `json:"reason"`
	Act       string    `json:"act"`
	Epoch     string    `json:"epoch"`
	At        time.Time `json:"at"`
}

// revokeApproval is T5b (persistence-api.md §5): the approval's machine FOR UPDATE; the revoking
// human's principal FOR SHARE, not revoked (rule 2); the approval FOR UPDATE, which waits for a
// commitment or attempt holding it FOR SHARE; the plan's state FOR UPDATE. The revocation is one
// approval-revocation entry on the machine's timeline, recorded for any approval, after the
// commitment too (execution-recovery.md §2). The plan becomes revoked only while the approval
// authorizes it: its state approved by this approval, and neither expired nor revoked once the
// act-order lock is held (rule 4, §8.1).
func revokeApproval(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*reasonInput)
	approval := q.r.PathValue("id")
	if id.MustHave(approval, id.Approval) != nil {
		return result{}, refuse(http.StatusNotFound, "not-found", "no such approval")
	}
	b := approvalRevocationBody{Approval: approval, RevokedBy: q.principal.ID, Role: string(q.role), Reason: in.Reason,
		Act: q.actID, Epoch: q.epoch}
	// An approval row is immutable, so its plan and machine are read before the machine's lock.
	err := tx.QueryRowContext(ctx, `SELECT plan, machine FROM approval WHERE id = $1`, approval).Scan(&b.Plan, &b.Machine)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return result{}, refuse(http.StatusNotFound, "not-found", "no such approval").with("approval", approval)
	case err != nil:
		return result{}, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM machine WHERE id = $1 FOR UPDATE`, b.Machine); err != nil {
		return result{}, err
	}
	var revoked bool
	if err := tx.QueryRowContext(ctx, `SELECT revoked FROM principal WHERE id = $1 FOR SHARE`, q.principal.ID).Scan(&revoked); err != nil {
		return result{}, err
	}
	if revoked {
		return result{}, refuse(http.StatusForbidden, "identity-revoked", "")
	}
	var again bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM approval_revocation WHERE approval = a.id)
		FROM approval a WHERE a.id = $1 FOR UPDATE`, approval).Scan(&again); err != nil {
		return result{}, err
	}
	if again {
		return result{}, refuse(http.StatusConflict, "conflict", "the approval is already revoked; a revocation is permanent").
			with("approval", approval)
	}
	var current bool
	if err := tx.QueryRowContext(ctx, `SELECT state = 'approved' AND approval = $2 FROM plan_state WHERE plan = $1 FOR UPDATE`,
		b.Plan, approval).Scan(&current); err != nil {
		return result{}, err
	}
	// PA §5 rule 4: the revocation's time, and whether the plan still reads approved by it, follow
	// every lock wait, the act-order lock's included.
	write := func(ctx context.Context, tx *sql.Tx) error {
		var authorizes bool
		if err := tx.QueryRowContext(ctx, `SELECT c.t, p.expires_at > c.t AND NOT ap.revoked
			FROM plan p JOIN approval a ON a.plan = p.id JOIN principal ap ON ap.id = a.approver, (SELECT clock_timestamp() AS t) c
			WHERE a.id = $1`, approval).Scan(&b.At, &authorizes); err != nil {
			return err
		}
		var rev int64
		if err := tx.QueryRowContext(ctx, `UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1
			RETURNING revision_counter`, b.Machine).Scan(&rev); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
			VALUES ($1, $2, $3, 'approval-revocation', jsonb_build_object('approval', $4::text, 'plan', $5::text,
				'principal', $6::text, 'role', $7::text, 'reason', $8::text), $9)`,
			b.Machine, rev, q.epoch, approval, b.Plan, b.RevokedBy, b.Role, b.Reason, b.At); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO approval_revocation (approval, plan, machine, revoked_by, role, reason, act,
			epoch, at, revision) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			approval, b.Plan, b.Machine, b.RevokedBy, b.Role, b.Reason, q.actID, q.epoch, b.At, rev); err != nil {
			return err
		}
		if !current || !authorizes {
			return nil
		}
		_, err := tx.ExecContext(ctx, `UPDATE plan_state SET state = 'revoked', reason = 'approval-revoked',
			revision = revision + 1, updated_at = $2 WHERE plan = $1`, b.Plan, b.At)
		return err
	}
	return result{status: http.StatusCreated, body: &b, subjects: []string{approval, b.Plan, b.Machine}, atActOrder: write}, nil
}
