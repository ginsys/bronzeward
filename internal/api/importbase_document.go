package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/ginsys/bronzeward/internal/id"
)

// importBaseDocumentBody is the import base text read's answer (§9.3, choice §17.41): the stored
// sanitized document and nothing it was made from. No declarations, digest, baseline ciphertext or
// generation path: the document's own text carries the references it holds.
type importBaseDocumentBody struct {
	ID       string `json:"id"`
	Machine  string `json:"machine"`
	Document string `json:"document"`
}

// getImportBaseDocument is GET /import-base-revisions/{id}/document (§9.2, choice §17.41): the
// sanitized document of an import base revision, to the author who made the import from a file they
// provided and only when that same author continued its operator review, under no-store, recording
// no act. The document
// can still hold a value nobody marked, so a caller who is not the importer is refused before
// anything of the review is revealed, and a refused request never selects the text.
func getImportBaseDocument(a *API, w http.ResponseWriter, q *request) {
	w.Header().Set("Cache-Control", "no-store")
	rev := q.r.PathValue("id")
	var body importBaseDocumentBody
	err := readTx(a, q, func(ctx context.Context, tx *sql.Tx) error {
		if q.r.URL.RawQuery != "" {
			return refuse(http.StatusBadRequest, "invalid-request", "an item read takes no query")
		}
		if id.MustHave(rev, id.ImportBase) != nil {
			return refuse(http.StatusNotFound, "not-found", "no such import base revision")
		}
		// The importer, and the claim that carried the import: its kind, its source, its review and who
		// continued it. A revision with no ingest operation or claim, and a claim with no recorded source,
		// read empty strings, which no check below admits.
		var author, kind, source, review, continuedBy string
		err := tx.QueryRowContext(ctx, `SELECT r.machine, r.author, coalesce(c.kind, ''), coalesce(c.source, ''), coalesce(c.review, ''),
				coalesce(c.continued_by, '')
			FROM import_base_revision r
			LEFT JOIN operation o ON o.kind = 'ingest' AND o.result ->> 'importBaseRevision' = r.id
			LEFT JOIN staging_claim c ON c.id = o.ingestion
			WHERE r.id = $1`, rev).Scan(&body.Machine, &author, &kind, &source, &review, &continuedBy)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return refuse(http.StatusNotFound, "not-found", "no such import base revision")
		case err != nil:
			return err
		case q.principal.ID != author:
			// Whatever the review state: the refusal tells another identity nothing about it.
			return refuse(http.StatusForbidden, "forbidden", "only the author of this import reads its document").
				with("rule", "not-the-importer")
		case source != "document":
			// Only a file a person provided has a provider: a machine import read a node, and a claim
			// with no recorded source cannot say (fail closed).
			return refuse(http.StatusConflict, "conflict", "this import's configuration was not provided as a file").
				with("rule", "import-not-reviewed")
		case kind != "import" || review != "continued" || continuedBy != author:
			return refuse(http.StatusConflict, "conflict", "this import was not continued by you after an operator review").
				with("rule", "import-not-reviewed")
		}
		body.ID = rev
		return tx.QueryRowContext(ctx, `SELECT document FROM import_base_revision WHERE id = $1`, rev).Scan(&body.Document)
	})
	setEpoch(w, q) // the epoch the read held; a 401 drops it again
	if err != nil {
		a.fail(w, q, err)
		return
	}
	writeJSON(w, "application/json", http.StatusOK, body)
}
