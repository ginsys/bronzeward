package migrate

import (
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// insertSubjectClaim names every 0007 column, so each refusal below changes one of them.
const insertSubjectClaim = `INSERT INTO staging_claim (id, mode, state, owner, owner_gen, owner_epoch, lease_until,
	expires_at, payload, payload_digest, cluster, machine, kind, created_at)
	SELECT $1, $2, 'held', 'a/4242/start-1', 1, epoch, now() + interval '1 minute', now() + interval '1 hour',
	$3, $4, $5, $6, $7, now() FROM installation_state`

// 0007 (compilation §2.3 step 6, §3.4): a claim names the cluster its generations are created
// under and the machine it imports, and an encrypted payload carries the digest a resume checks.
func TestStagingSubject(t *testing.T) {
	db, _ := installed(t)
	a := adoptionRows(t, db)
	for _, c := range []struct {
		name string
		args []any
		want string
	}{
		{"machine of another cluster", []any{"transient", nil, nil, a.cluster, a.otherMachine, "import"}, "23503"},
		{"no machine", []any{"transient", nil, nil, a.cluster, nil, "import"}, "23502"},
		{"no cluster", []any{"transient", nil, nil, nil, a.machine, "import"}, "23502"},
		{"no kind", []any{"transient", nil, nil, a.cluster, a.machine, nil}, "23502"},
		{"kind drift-adoption", []any{"transient", nil, nil, a.cluster, a.machine, "drift-adoption"}, "23514"},
		{"payload without its digest", []any{"encrypted", []byte{1}, nil, a.cluster, a.machine, "import"}, "23514"},
		{"digest without a payload", []any{"encrypted", nil, digest(5), a.cluster, a.machine, "import"}, "23514"},
		{"31-byte payload digest", []any{"encrypted", []byte{1}, digest(5)[:31], a.cluster, a.machine, "import"}, "23514"},
	} {
		args := append([]any{id.New(id.Ingestion)}, c.args...)
		if _, err := db.Exec(insertSubjectClaim, args...); sqlState(err) != c.want {
			t.Errorf("%s: %v; want SQLSTATE %s", c.name, err, c.want)
		}
	}
	// Positive controls: a transient claim with neither, an encrypted one with both.
	mustExec(t, db, insertSubjectClaim, id.New(id.Ingestion), "transient", nil, nil, a.cluster, a.machine, "import")
	mustExec(t, db, insertSubjectClaim, id.New(id.Ingestion), "encrypted", []byte{1}, digest(5), a.cluster, a.machine, "import")
	// Clearing the payload clears its digest with it, as release and abandonment do.
	enc := id.New(id.Ingestion)
	mustExec(t, db, insertSubjectClaim, enc, "encrypted", []byte{1}, digest(5), a.cluster, a.machine, "import")
	if _, err := db.Exec(`UPDATE staging_claim SET state = 'released', payload = NULL WHERE id = $1`, enc); sqlState(err) != "23514" {
		t.Errorf("payload cleared, digest kept: %v; want SQLSTATE 23514", err)
	}
	mustExec(t, db, `UPDATE staging_claim SET state = 'released', payload = NULL, payload_digest = NULL WHERE id = $1`, enc)
}
