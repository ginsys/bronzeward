package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/staging"
)

// ingestionReviewBody is the review route's answer (§9.3): the staged sanitized document and its
// declarations in the draft-update body's form, never the baseline, a generation path or the
// digest.
type ingestionReviewBody struct {
	Ingestion    string               `json:"ingestion"`
	Document     string               `json:"document"`
	Declarations fragmentDeclarations `json:"declarations"`
}

// getIngestionReview is GET /ingestions/{id}/review (§9.2, compilation §3.6 step 2): the one read
// that answers input text, the staged sanitized document of a paused claim, to an author under
// no-store (§9.1, choice §17.36). The claim's state and envelope are read in a read transaction;
// the envelope is decrypted with the ingestion identity after that transaction ends, so no
// provider call holds the installation state. It records no act and writes nothing: a decryption
// failure is 503 and an integrity failure 500, the claim unchanged either way.
func getIngestionReview(a *API, w http.ResponseWriter, q *request) {
	w.Header().Set("Cache-Control", "no-store")
	claim := q.r.PathValue("id")
	var ct provider.Ciphertext
	var sum [32]byte
	err := readTx(a, q, func(ctx context.Context, tx *sql.Tx) error {
		if q.r.URL.RawQuery != "" {
			return refuse(http.StatusBadRequest, "invalid-request", "an item read takes no query")
		}
		if id.MustHave(claim, id.Ingestion) != nil {
			return refuse(http.StatusNotFound, "not-found", "no such ingestion")
		}
		var state string
		var payload, digest []byte
		err := tx.QueryRowContext(ctx, `SELECT `+staging.EffectiveStateSQL+`, payload, payload_digest FROM staging_claim WHERE id = $1`,
			claim).Scan(&state, &payload, &digest)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return refuse(http.StatusNotFound, "not-found", "no such ingestion")
		case err != nil:
			return err
		case state != "paused":
			return refuse(http.StatusConflict, "conflict", "only a paused ingestion is reviewed").with("ingestion", claim)
		case len(payload) == 0 || len(digest) != len(sum):
			return errors.New("a paused claim without a complete payload")
		}
		ct, sum = provider.Ciphertext(payload), [32]byte(digest)
		return nil
	})
	setEpoch(w, q) // the epoch the read held; a 401 drops it again
	if err != nil {
		a.fail(w, q, err)
		return
	}
	if a.d.ing == nil {
		a.fail(w, q, refuse(http.StatusServiceUnavailable, "dependency-unavailable", "ingestion is not configured"))
		return
	}
	plain, err := a.d.ing.DecryptStaging(q.r.Context(), ct)
	if err != nil {
		a.o.logf("%s: ingestion %s: decrypting the staged envelope for the review: %v", q.id, claim, err)
		a.fail(w, q, refuse(http.StatusServiceUnavailable, "dependency-unavailable", "the staged envelope could not be decrypted; the claim is unchanged"))
		return
	}
	st, err := ingest.Open(plain, sum)
	if err != nil {
		a.o.logf("%s: ingestion %s: opening the staged envelope for the review: %v", q.id, claim, err)
		a.fail(w, q, refuse(http.StatusInternalServerError, "internal-error", "the staged envelope is incomplete or does not match its digest; the claim is unchanged"))
		return
	}
	decl := st.Sanitized.Declarations()
	body := ingestionReviewBody{Ingestion: claim, Document: string(st.Sanitized.Documents()),
		Declarations: fragmentDeclarations{References: decl.References, Embedded: decl.Embedded}}
	if body.Declarations.References == nil {
		body.Declarations.References = map[string]ingest.Reference{}
	}
	if body.Declarations.Embedded == nil {
		body.Declarations.Embedded = []ingest.Embedded{}
	}
	writeJSON(w, "application/json", http.StatusOK, body)
}
