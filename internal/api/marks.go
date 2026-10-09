package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/staging"
)

// markInput is a mark's body (§9.3): 1 to maxMarks compilation §2.2 paths. A path can spell an
// extracted value, so it is never quoted back: one that does not parse is named by its position.
type markInput struct {
	Marks []string `json:"marks"`
	marks []ingest.Path
}

func (in *markInput) check(*API) error {
	if len(in.Marks) == 0 || len(in.Marks) > maxMarks {
		return fmt.Errorf("marks must hold 1 to %d paths", maxMarks)
	}
	in.marks = make([]ingest.Path, len(in.Marks))
	for i, s := range in.Marks {
		p, err := ingest.ParsePath(s)
		if err != nil {
			return refuse(http.StatusUnprocessableEntity, "validation-failed", "a mark is not a compilation §2.2 path").
				with("position", i)
		}
		in.marks[i] = p
	}
	return nil
}

// keyedDigest covers the marks, which are the request's unextracted input (§7.1).
func (in *markInput) keyedDigest(ctx context.Context, h ingest.HMAC, material []byte, version int) (provider.Digest, error) {
	return ingest.FingerprintMarks(ctx, h, material, in.Marks, version)
}

// POST /ingestions/{id}/marks is an operator's mark (§9.3, compilation §3.6 item 3), T11: the
// paused claim taken at the next owner generation with its running operation and the marked
// event, answered 202 with that generation; the run decrypts the envelope, re-extracts with the
// marks and pauses again. The marks live only in the job, never in a row, an event or the body.
func ingestionMark() effectRoute {
	return effectRoute{action: "ingestion.mark", input: func() input { return &markInput{} }, keyed: "marks", effect: markIngestion}
}

func markIngestion(ctx context.Context, a *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*markInput)
	claim := q.r.PathValue("id")
	notFound := refuse(http.StatusNotFound, "not-found", "no such ingestion")
	if id.MustHave(claim, id.Ingestion) != nil {
		return result{}, notFound
	}
	if a.d.owner.ID == "" || a.d.ing == nil {
		return result{}, refuse(http.StatusServiceUnavailable, "dependency-unavailable", "ingestion is not configured; nothing was committed")
	}
	tk, err := staging.Mark(ctx, tx, a.d.owner, a.d.timers.Lease, claim)
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
	j := job{claim: tk.Claim, marks: in.marks, resume: &staged{ct: provider.Ciphertext(tk.Payload), sum: tk.Digest}}
	if err := tx.QueryRowContext(ctx, `SELECT id, draft, draft_revision FROM operation WHERE ingestion = $1 AND state = 'running'`,
		claim).Scan(&j.op, &j.draft, &j.draftRev); err != nil {
		return result{}, err
	}
	if err := a.event(ctx, tx, j, map[string]any{"type": "marked", "generation": tk.Claim.Gen, "paths": len(in.marks)}); err != nil {
		return result{}, err
	}
	return result{status: http.StatusAccepted, location: prefix + "/operations/" + j.op,
		body:     map[string]any{"operation": j.op, "ingestion": claim, "generation": tk.Claim.Gen},
		subjects: []string{j.op, claim, j.draft, tk.Claim.Machine}, operation: j.op,
		afterCommit: func() { a.startRunner(j) },
		// The absolute expiry passing in the act-order wait refuses the mark, as it would before its
		// write.
		atActOrder: func(ctx context.Context, tx *sql.Tx) error {
			err := staging.Restart(ctx, tx, claim, staging.Timers{Lease: a.d.timers.Lease}, false)
			if errors.Is(err, staging.ErrEnded) {
				_, err = conflict("the ingestion has ended")
			}
			return err
		}}, nil
}
