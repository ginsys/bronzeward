package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/staging"
)

// continuationInput is a continuation's body (§9.3): empty, as a takeover's.
type continuationInput struct{}

func (*continuationInput) check(*API) error { return nil }

// POST /ingestions/{id}/continuations is an operator's continuation (§9.3, compilation §3.6 item
// 4), T11: the paused claim taken at the next owner generation with its running operation, its
// review recorded as continued, and the continued event; the run decrypts the envelope and runs
// the draft transaction (T1), whose draft checks fail the operation and abandon the claim.
func ingestionContinuation() effectRoute {
	return effectRoute{action: "ingestion.continue", input: func() input { return &continuationInput{} }, effect: continueIngestion}
}

func continueIngestion(ctx context.Context, a *API, tx *sql.Tx, q *request) (result, error) {
	claim := q.r.PathValue("id")
	notFound := refuse(http.StatusNotFound, "not-found", "no such ingestion")
	if id.MustHave(claim, id.Ingestion) != nil {
		return result{}, notFound
	}
	if a.d.owner.ID == "" || a.d.ing == nil {
		return result{}, refuse(http.StatusServiceUnavailable, "dependency-unavailable", "ingestion is not configured; nothing was committed")
	}
	tk, err := staging.Continue(ctx, tx, a.d.owner, a.d.timers.Lease, claim)
	conflict := func(detail string) (result, error) {
		return result{}, refuse(http.StatusConflict, "conflict", detail).with("ingestion", claim)
	}
	switch {
	case errors.Is(err, staging.ErrNoClaim):
		return result{}, notFound
	case errors.Is(err, staging.ErrNotPaused):
		return conflict("the ingestion is not paused for the operator's review")
	case errors.Is(err, staging.ErrEnded):
		return conflict("the ingestion has ended")
	case errors.Is(err, staging.ErrClaimEpoch):
		return conflict("the claim predates the current epoch")
	case err != nil:
		return result{}, err
	}
	j := job{claim: tk.Claim, resume: &staged{ct: provider.Ciphertext(tk.Payload), sum: tk.Digest}}
	if err := tx.QueryRowContext(ctx, `SELECT id, draft, draft_revision FROM operation WHERE ingestion = $1 AND state = 'running'`,
		claim).Scan(&j.op, &j.draft, &j.draftRev); err != nil {
		return result{}, err
	}
	if err := a.event(ctx, tx, j, map[string]any{"type": "continued", "generation": tk.Claim.Gen}); err != nil {
		return result{}, err
	}
	return result{status: http.StatusAccepted, location: prefix + "/operations/" + j.op,
		body:     map[string]any{"operation": j.op, "ingestion": claim},
		subjects: []string{j.op, claim, j.draft, tk.Claim.Machine}, operation: j.op,
		afterCommit: func() { a.startRunner(j) },
		// The absolute expiry passing in the act-order wait refuses the continuation, as it would
		// before its write.
		atActOrder: func(ctx context.Context, tx *sql.Tx) error {
			err := staging.Restart(ctx, tx, claim, staging.Timers{Lease: a.d.timers.Lease}, false)
			if errors.Is(err, staging.ErrEnded) {
				_, err = conflict("the ingestion has ended")
			}
			return err
		}}, nil
}
