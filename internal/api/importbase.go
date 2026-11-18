package api

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// importBaseBody is the import base summary (§3.2, §9.3, choice §17.38): that a revision exists,
// whose it is, when it was written and how much it holds, and nothing it holds. It carries no
// document text, no digest, no reference name and no generation path: documentBytes is the
// stored document's size, references and embedded are counts.
type importBaseBody struct {
	ID            string    `json:"id"`
	Machine       string    `json:"machine"`
	Author        string    `json:"author"`
	CreatedAt     time.Time `json:"createdAt"`
	DocumentBytes int       `json:"documentBytes"`
	References    int       `json:"references"`
	Embedded      int       `json:"embedded"`
}

// getImportBase is GET /import-base-revisions/{id} (§9.2): the one read of an import base
// revision. It selects the document's size and never the document, so the text cannot reach the
// answer, and answers an author under no-store like the review read.
var getImportBase = func() readFunc {
	read := item(id.ImportBase, func(ctx context.Context, tx *sql.Tx, v string) (string, any, error) {
		var b importBaseBody
		err := tx.QueryRowContext(ctx, `SELECT r.id, r.machine, r.author, r.created_at, octet_length(r.document),
				(SELECT count(*) FROM import_base_reference c WHERE c.revision = r.id), jsonb_array_length(r.embedded)
			FROM import_base_revision r WHERE r.id = $1`, v).
			Scan(&b.ID, &b.Machine, &b.Author, &b.CreatedAt, &b.DocumentBytes, &b.References, &b.Embedded)
		b.CreatedAt = b.CreatedAt.UTC()
		return "", b, err
	})
	return func(a *API, w http.ResponseWriter, q *request) {
		w.Header().Set("Cache-Control", "no-store")
		read(a, w, q)
	}
}()
