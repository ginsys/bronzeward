package dbtest

import (
	"database/sql"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

type publicationDB interface {
	QueryRow(string, ...any) *sql.Row
	Exec(string, ...any) (sql.Result, error)
}

// PublicationObservation seeds explicit synthetic evidence for tests that insert
// releases directly. Builder/worker tests use the real observation path instead.
func PublicationObservation(t testing.TB, db publicationDB, machine, operation string) string {
	t.Helper()
	observation := id.New(id.Observation)
	var revision int64
	if err := db.QueryRow(`UPDATE machine SET revision_counter = revision_counter + 2 WHERE id = $1
  RETURNING revision_counter`, machine).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	statements := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
   SELECT $1, $2, epoch, 'observation-started', jsonb_build_object('purpose', 'publication', 'operation', $3::text), now()
   FROM installation_state`, []any{machine, revision - 1, operation}},
		{`INSERT INTO observation_start (machine, revision, purpose, operation, publication_generation, endpoint, controller)
   SELECT m.id, $2, 'publication', o.id, o.owner_gen, m.talos_endpoint, o.owner FROM machine m, operation o
   WHERE m.id = $1 AND o.id = $3`, []any{machine, revision - 1, operation}},
		{`INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
   SELECT $1, $2, epoch, 'observation', jsonb_build_object('observation', $3::text, 'purpose', 'publication'), now()
   FROM installation_state`, []any{machine, revision, observation}},
		{`INSERT INTO observation (id, machine, basis, revision, access_path, access_version, access_created,
   smbios_uuid, talos_node_id, talos_cluster_id, running_version, unread, at)
   SELECT $1, m.id, $3-1, $3, 'access/talos/' || m.cluster, 1, '2026-10-19T00:00:00Z',
   m.smbios_uuid, coalesce(m.talos_node_id, 'fixture-node'), c.talos_cluster_id, c.contract || '.0',
   '{"configuration":{"cause":"fixture"},"health":{"cause":"none-bound"}}', now()
   FROM machine m JOIN cluster c ON c.id = m.cluster WHERE m.id = $2`, []any{observation, machine, revision}},
	}
	for _, s := range statements {
		if _, err := db.Exec(s.q, s.args...); err != nil {
			t.Fatal(err)
		}
	}
	return observation
}
