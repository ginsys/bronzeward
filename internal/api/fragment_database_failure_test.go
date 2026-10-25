package api

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// PA §14 row 13: a synchronous draft update whose T1 did not commit answers
// 503, keeps the draft unchanged and leaves its claim and provider orphans for
// lease recovery. The same request without the database failure commits once.
func TestFragmentPutDatabaseFailure(t *testing.T) {
	for _, failCommit := range []bool{true, false} {
		name := "successful commit"
		if failCommit {
			name = "failed commit"
		}
		t.Run(name, func(t *testing.T) {
			inT1, intercepted := false, false
			ie := newFragmentEnv(t, options{
				afterEffect: func() { inT1 = true },
				commit: func(tx *sql.Tx) error {
					if inT1 {
						intercepted = true
						if failCommit {
							if err := tx.Rollback(); err != nil {
								return err
							}
							return &pgconn.PgError{Code: "08006", Message: "synthetic T1 connection loss"}
						}
					}
					return tx.Commit()
				},
			})
			before := ie.etag
			acts := count(t, ie.db, `SELECT count(*) FROM act`)
			records := count(t, ie.db, `SELECT count(*) FROM idempotency_record`)
			rec := ie.putFragment("registries", fragmentPut(t, "cluster", labelDoc, []string{labelMark}, nil),
				before, "k-fragment-database-01")
			if !intercepted {
				t.Fatal("the fragment update never reached T1 COMMIT")
			}
			calls, created := ie.f.paths()
			if calls != 1 || len(created) != 1 {
				t.Fatalf("provider calls %d, generations %v; want one generation created before T1", calls, created)
			}
			if n := count(t, ie.db, `SELECT count(*) FROM staging_claim`); n != 1 {
				t.Fatalf("%d claims, want one", n)
			}
			var claim string
			if err := ie.db.QueryRow(`SELECT id FROM staging_claim WHERE kind = 'draft-update' AND draft = $1`, ie.draft).Scan(&claim); err != nil {
				t.Fatal(err)
			}
			state, gen, payload := claimRow(t, ie.db, claim)
			if gen != 1 || payload || !strings.Contains(created[0], "/"+claim+"/") {
				t.Fatalf("claim generation %d, payload %v, generation %s; want this transient claim's generation", gen, payload, created[0])
			}
			if n := count(t, ie.db, `SELECT count(*) FROM operation`); n != 0 {
				t.Fatalf("%d operations for a synchronous update", n)
			}
			if failCommit {
				p := wantProblem(t, rec, http.StatusServiceUnavailable, "dependency-unavailable")
				if p["detail"] != "the database failed; nothing of the request's own transaction was committed" || strings.Contains(rec.Body.String(), "synthetic T1") {
					t.Fatalf("database refusal is not sanitized: %s", rec.Body)
				}
				ie.draftUnchanged(t, before)
				if state != "held" || count(t, ie.db, `SELECT count(*) FROM fragment_revision`) != 0 ||
					count(t, ie.db, `SELECT count(*) FROM fragment_reference`) != 0 ||
					count(t, ie.db, `SELECT count(*) FROM draft_source_entry`) != 0 ||
					count(t, ie.db, `SELECT count(*) FROM act`) != acts ||
					count(t, ie.db, `SELECT count(*) FROM idempotency_record`) != records {
					t.Fatal("failed T1 released its claim or persisted a fragment, draft entry, act or replay record")
				}
			} else {
				b := ie.fragmentOK(t, rec)
				refs := fragmentRefs(t, ie, *b.Entry.Revision)
				if state != "released" || b.Ingestion != claim || len(refs) != 1 || refs[0].generation != created[0] ||
					count(t, ie.db, `SELECT count(*) FROM draft_source_entry WHERE draft = $1 AND fragment_revision = $2`, ie.draft, *b.Entry.Revision) != 1 ||
					count(t, ie.db, `SELECT count(*) FROM act`) != acts+1 ||
					count(t, ie.db, `SELECT count(*) FROM idempotency_record`) != records+1 {
					t.Fatal("successful control did not atomically commit its fragment, claim release, draft entry, act and replay record")
				}
			}
			assertAbsent(t, ie, runLabel)
		})
	}
}
