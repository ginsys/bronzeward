package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
)

// The statements 0004's tests insert with; each takes the installation's epoch where it needs one.
const (
	// Every cluster has a Talos cluster ID (0008); each row's is derived from its identifier.
	insertCluster = `INSERT INTO cluster (id, name, endpoint, contract, talos_cluster_id, created_at)
		VALUES ($1, $2, $3, $4, translate(encode(sha256(convert_to($1::text, 'UTF8')), 'base64'), '+/', '-_'), now())`
	// Every machine has a Talos endpoint (0006); these rows share one.
	insertMachine = `INSERT INTO machine (id, cluster, smbios_uuid, serial, scope_state, talos_endpoint, created_at)
		VALUES ($1, $2, $3, $4, $5, '10.55.0.3:50000', now())`
	insertMachineState = `INSERT INTO machine_state (machine, applied_release, applied_digest, applied_source, baseline_revision)
		VALUES ($1, $2, $3, $4, $5)`
	insertImportBase = `INSERT INTO import_base_revision (id, machine, document, baseline_ciphertext, baseline_digest,
		baseline_digest_key, configuration_digest, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, now())`
	insertReference = `INSERT INTO import_base_reference (revision, name, kind, version, encoding, generation)
		VALUES ($1, $2, $3, $4, $5, $6)`
	insertDraft = `INSERT INTO draft (id, cluster, title, state, revision, etag_token, created_at)
		VALUES ($1, $2, $3, $4, 1, $5, now())`
	insertEntry = `INSERT INTO draft_entry (draft, cluster, kind, machine, import_base_revision) VALUES ($1, $2, $3, $4, $5)`
	// A claim's subject (0007) is not what these rows test: each imports the first machine by id,
	// and a payload carries its digest. TestStagingSubject tests the subject columns.
	insertClaim = `INSERT INTO staging_claim (id, mode, state, owner, owner_gen, owner_epoch, lease_until, expires_at,
		payload, payload_digest, principal, idempotency_key, cluster, machine, kind, created_at)
		SELECT $1, $2, $3, 'run-1/4242/start-1', 1, epoch, now() + interval '1 minute', now() + interval '1 hour',
		$4, sha256($4::bytea), $5, $6, m.cluster, m.id, 'import', now()
		FROM installation_state, (SELECT id, cluster FROM machine ORDER BY id LIMIT 1) m`
	insertOperation = `INSERT INTO operation (id, kind, state, epoch, owner, owner_gen, owner_epoch, lease_until,
		draft, draft_revision, ingestion, created_by, created_by_kind, created_role, created_at, result, error)
		SELECT $1, $2, $3, epoch, $4, $5, CASE WHEN $4::text IS NULL THEN NULL ELSE epoch END,
		CASE WHEN $4::text IS NULL THEN NULL ELSE now() + interval '1 minute' END,
		$6, $7, $8, $9, CASE WHEN $9::text IS NULL THEN NULL ELSE 'human' END,
		CASE WHEN $9::text IS NULL THEN NULL WHEN $2 = 'publish' THEN 'publisher' ELSE 'author' END, now(), $10::jsonb,
		$11::jsonb FROM installation_state`
	// The event names its operation's kind (0005), which the key on (operation, kind) checks.
	insertEvent = `INSERT INTO operation_event (operation, number, epoch, entry, at, kind)
		SELECT $1, $2, epoch, $3::jsonb, now(), $4 FROM installation_state`
)

// adoption holds one of each 0004 row, inserted by adoptionRows.
type adoption struct {
	human, cluster, other, machine, otherMachine, ibr, otherIBR, draft, draft2, claim, ingest, applyConfig, adopt string
}

func adoptionRows(t *testing.T, db *sql.DB) adoption {
	t.Helper()
	a := adoption{human: id.New(id.Principal), cluster: id.New(id.Cluster), other: id.New(id.Cluster),
		machine: id.New(id.Machine), otherMachine: id.New(id.Machine), ibr: id.New(id.ImportBase), otherIBR: id.New(id.ImportBase),
		draft: id.New(id.Draft), draft2: id.New(id.Draft), claim: id.New(id.Ingestion), ingest: id.New(id.Operation),
		applyConfig: id.New(id.Operation), adopt: id.New(id.Operation)}
	mustExec(t, db, `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', 'https://idp.test', 'alice', now())`, a.human)
	mustExec(t, db, insertCluster, a.cluster, "office", "https://cp.example.test:6443", "v1.13")
	mustExec(t, db, insertCluster, a.other, "lab", "https://lab.example.test:6443", "v1.13")
	mustExec(t, db, insertMachine, a.machine, a.cluster, "0b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b", "SN-1", "normal")
	mustExec(t, db, insertMachine, a.otherMachine, a.other, "1c6b7d2f-3a4e-4f6a-9b0c-1d2e3f4a5b6c", nil, "normal")
	mustExec(t, db, insertMachineState, a.machine, nil, nil, nil, nil)
	mustExec(t, db, insertImportBase, a.ibr, a.machine, "machine:\n  type: worker\n", []byte{1}, digest(1), "transit/baseline-digest:1", digest(2))
	mustExec(t, db, insertImportBase, a.otherIBR, a.otherMachine, "machine:\n  type: worker\n", []byte{1}, digest(1), "transit/baseline-digest:1", digest(2))
	mustExec(t, db, insertReference, a.ibr, "registry/example-pass", "string", 1, nil, generation(a.cluster, a.claim))
	mustExec(t, db, insertDraft, a.draft, a.cluster, "import", "open", "m3oxmlfh6phr7aigshdydcb4ji")
	mustExec(t, db, insertDraft, a.draft2, a.cluster, "second", "open", "m3oxmlfh6phr7aigshdydcb4ji")
	mustExec(t, db, insertEntry, a.draft, a.cluster, "import-base", a.machine, a.ibr)
	mustExec(t, db, insertClaim, a.claim, "transient", "held", nil, a.human, "k0123456789abcdef")
	mustExec(t, db, insertOperation, a.ingest, "ingest", "running", "run-1/4242/start-1", 1, a.draft, 1, a.claim, a.human, nil, nil)
	mustExec(t, db, insertEvent, a.ingest, 1, `{"type":"started"}`, "ingest")
	mustExec(t, db, insertOperation, a.applyConfig, "apply-config", "committed", "run-1/4242/start-1", 1, nil, nil, nil, nil, nil, nil)
	mustExec(t, db, insertOperation, a.adopt, "adopt", "completed", nil, 0, nil, nil, nil, nil, nil, nil)
	return a
}

func digest(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func generation(cluster, claim string) string { return "gen/" + cluster + "/" + claim + "/v1" }

// PA §3, §5.1, §7.3, §8; compilation §3.2, §5: what the schema itself refuses in 0004.
func TestAdoptionConstraints(t *testing.T) {
	db, _ := installed(t)
	a := adoptionRows(t, db)
	uuid := "2d7c8e3a-4b5f-4a7b-8c1d-2e3f4a5b6c7d"
	claim2, claim3, claim4, claim5 := id.New(id.Ingestion), id.New(id.Ingestion), id.New(id.Ingestion), id.New(id.Ingestion)
	for _, c := range []string{claim2, claim3, claim4, claim5} {
		mustExec(t, db, insertClaim, c, "transient", "held", nil, nil, nil)
	}
	op := func() string { return id.New(id.Operation) }
	for _, c := range []struct {
		name, q string
		args    []any
		want    string
	}{
		{"cluster id of another prefix", insertCluster, []any{id.New(id.Machine), "x", "https://x.test", "v1.13"}, "23514"},
		{"blank cluster name", insertCluster, []any{id.New(id.Cluster), " ", "https://x.test", "v1.13"}, "23514"},
		{"plain-http endpoint", insertCluster, []any{id.New(id.Cluster), "x", "http://x.test", "v1.13"}, "23514"},
		{"contract with a patch", insertCluster, []any{id.New(id.Cluster), "x", "https://x.test", "v1.13.6"}, "23514"},
		{"machine of no cluster", insertMachine, []any{id.New(id.Machine), id.New(id.Cluster), uuid, nil, "normal"}, "23503"},
		{"second machine with one SMBIOS UUID", insertMachine, []any{id.New(id.Machine), a.other, "0b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b", nil, "normal"}, "23505"},
		{"the same UUID upper-cased", insertMachine, []any{id.New(id.Machine), a.other, "0B5A6C1E-2F3D-4E5F-8A9B-0C1D2E3F4A5B", nil, "normal"}, "23505"},
		{"unknown scope state", insertMachine, []any{id.New(id.Machine), a.cluster, uuid, nil, "frozen"}, "23514"},
		{"second machine state", insertMachineState, []any{a.machine, nil, nil, nil, nil}, "23505"},
		{"applied without its digest", insertMachineState, []any{a.otherMachine, id.New(id.Release), nil, "operation", 1}, "23514"},
		{"applied from an unknown source", insertMachineState, []any{a.otherMachine, id.New(id.Release), digest(3), "restore", 1}, "23514"},
		{"applied without a baseline revision", insertMachineState, []any{a.otherMachine, id.New(id.Release), digest(3), "adoption", nil}, "23514"},
		{"baseline revision without Applied", insertMachineState, []any{a.otherMachine, nil, nil, nil, 1}, "23514"},
		{"baseline revision 0", insertMachineState, []any{a.otherMachine, id.New(id.Release), digest(3), "adoption", 0}, "23514"},
		{"31-byte configuration digest", insertImportBase, []any{id.New(id.ImportBase), a.machine, "x", []byte{1}, digest(1), "k:1", digest(2)[:31]}, "23514"},
		{"import base with no key identity", insertImportBase, []any{id.New(id.ImportBase), a.machine, "x", []byte{1}, digest(1), "", digest(2)}, "23514"},
		{"import base of no machine", insertImportBase, []any{id.New(id.ImportBase), id.New(id.Machine), "x", []byte{1}, digest(1), "k:1", digest(2)}, "23503"},
		{"reference name with an upper-case letter", insertReference, []any{a.ibr, "Registry/pass", "string", 1, nil, generation(a.cluster, a.claim)}, "23514"},
		{"reference name ending in a hyphen", insertReference, []any{a.ibr, "registry/pass-", "string", 1, nil, generation(a.cluster, a.claim)}, "23514"},
		{"reference kind float", insertReference, []any{a.ibr, "registry/other", "float", 1, nil, generation(a.cluster, a.claim)}, "23514"},
		{"reference version 0", insertReference, []any{a.ibr, "registry/other", "string", 0, nil, generation(a.cluster, a.claim)}, "23514"},
		{"reference encoding hex", insertReference, []any{a.ibr, "registry/other", "string", 1, "hex", generation(a.cluster, a.claim)}, "23514"},
		// base64 places a string secret's bytes (compilation §5.2); it modifies no other kind.
		{"base64 integer reference", insertReference, []any{a.ibr, "registry/b64-integer", "integer", 1, "base64", generation(a.cluster, a.claim)}, "23514"},
		{"base64 boolean reference", insertReference, []any{a.ibr, "registry/b64-boolean", "boolean", 1, "base64", generation(a.cluster, a.claim)}, "23514"},
		{"base64 mapping reference", insertReference, []any{a.ibr, "registry/b64-mapping", "mapping", 1, "base64", generation(a.cluster, a.claim)}, "23514"},
		{"generation path of another shape", insertReference, []any{a.ibr, "registry/other", "string", 1, nil, "secret/registry"}, "23514"},
		{"second declaration of a name", insertReference, []any{a.ibr, "registry/example-pass", "string", 1, nil, generation(a.cluster, a.claim)}, "23505"},
		{"draft state merged", insertDraft, []any{id.New(id.Draft), a.cluster, "x", "merged", "m3oxmlfh6phr7aigshdydcb4ji"}, "23514"},
		{"draft ETag token of 25 characters", insertDraft, []any{id.New(id.Draft), a.cluster, "x", "open", "m3oxmlfh6phr7aigshdydcb4j"}, "23514"},
		{"draft of no cluster", insertDraft, []any{id.New(id.Draft), id.New(id.Cluster), "x", "open", "m3oxmlfh6phr7aigshdydcb4ji"}, "23503"},
		{"entry of a fragment", insertEntry, []any{a.draft, a.cluster, "fragment", a.otherMachine, a.otherIBR}, "23514"},
		{"entry for another cluster's machine", insertEntry, []any{a.draft, a.cluster, "import-base", a.otherMachine, a.otherIBR}, "23503"},
		{"entry naming another machine's import base", insertEntry, []any{a.draft2, a.cluster, "import-base", a.machine, a.otherIBR}, "23503"},
		{"second import base entry for a machine", insertEntry, []any{a.draft, a.cluster, "import-base", a.machine, a.ibr}, "23505"},
		{"claim state pending", insertClaim, []any{id.New(id.Ingestion), "transient", "pending", nil, nil, nil}, "23514"},
		{"transient claim with a payload", insertClaim, []any{id.New(id.Ingestion), "transient", "held", []byte{1}, nil, nil}, "23514"},
		{"resumed transient claim", insertClaim, []any{id.New(id.Ingestion), "transient", "resumed", nil, nil, nil}, "23514"},
		{"released claim with a payload", insertClaim, []any{id.New(id.Ingestion), "encrypted", "released", []byte{1}, nil, nil}, "23514"},
		{"claim with a key and no principal", insertClaim, []any{id.New(id.Ingestion), "transient", "held", nil, nil, "k0123456789abcdeX"}, "23514"},
		{"second live claim for a key", insertClaim, []any{id.New(id.Ingestion), "encrypted", "held", []byte{1}, a.human, "k0123456789abcdef"}, "23505"},
		{"operation kind import", insertOperation, []any{op(), "import", "running", "o", 1, a.draft, 2, claim2, a.human, nil, nil}, "23514"},
		{"queued ingest", insertOperation, []any{op(), "ingest", "queued", nil, 0, a.draft, 2, claim2, a.human, nil, nil}, "23514"},
		{"publish in an execution state", insertOperation, []any{op(), "publish", "sending", "o", 1, a.draft, 2, nil, a.human, nil, nil}, "23514"},
		{"adopt not completed", insertOperation, []any{op(), "adopt", "committed", "o", 1, nil, nil, nil, nil, nil, nil}, "23514"},
		{"adopt with a draft revision and no draft", insertOperation, []any{op(), "adopt", "completed", nil, 0, nil, 1, nil, nil, nil, nil}, "23514"},
		{"adopt with a draft and no revision", insertOperation, []any{op(), "adopt", "completed", nil, 0, a.draft, nil, nil, nil, nil, nil}, "23514"},
		{"ingest with no claim", insertOperation, []any{op(), "ingest", "running", "o", 1, a.draft, 2, nil, a.human, nil, nil}, "23514"},
		{"publish with a claim", insertOperation, []any{op(), "publish", "running", "o", 1, a.draft, 2, claim2, a.human, nil, nil}, "23514"},
		{"ingest with no draft revision", insertOperation, []any{op(), "ingest", "running", "o", 1, a.draft, nil, claim2, a.human, nil, nil}, "23514"},
		{"ingest by nobody", insertOperation, []any{op(), "ingest", "running", "o", 1, a.draft, 2, claim2, nil, nil, nil}, "23514"},
		{"running job with no owner", insertOperation, []any{op(), "publish", "running", nil, 0, a.draft, 2, nil, a.human, nil, nil}, "23514"},
		{"succeeded job with no result", insertOperation, []any{op(), "publish", "succeeded", nil, 0, a.draft, 2, nil, a.human, nil, nil}, "23514"},
		{"failed job with no error", insertOperation, []any{op(), "ingest", "failed", nil, 0, a.draft, 2, claim2, a.human, nil, nil}, "23514"},
		{"succeeded job with a JSON null result", insertOperation, []any{op(), "publish", "succeeded", nil, 0, a.draft, 2, nil, a.human, "null", nil}, "23514"},
		{"failed job with a JSON null error", insertOperation, []any{op(), "ingest", "failed", nil, 0, a.draft, 2, claim4, a.human, nil, "null"}, "23514"},
		{"failed job with an array error", insertOperation, []any{op(), "ingest", "failed", nil, 0, a.draft, 2, claim5, a.human, nil, "[]"}, "23514"},
		{"second operation of a claim", insertOperation, []any{op(), "ingest", "succeeded", nil, 0, a.draft, 2, a.claim, a.human, `{"draft":"x"}`, nil}, "23505"},
		{"second running ingest of a draft revision", insertOperation, []any{op(), "ingest", "running", "o", 1, a.draft, 1, claim2, a.human, nil, nil}, "23505"},
		{"event 0", insertEvent, []any{a.ingest, 0, `{}`, "ingest"}, "23514"},
		{"second event 1", insertEvent, []any{a.ingest, 1, `{}`, "ingest"}, "23505"},
		{"event of no operation", insertEvent, []any{op(), 1, `{}`, "ingest"}, "23503"},
		{"event with a JSON null entry", insertEvent, []any{a.ingest, 2, "null", "ingest"}, "23514"},
		{"event with an array entry", insertEvent, []any{a.ingest, 3, "[]", "ingest"}, "23514"},
		// 0005, issue ginsys/bronzeward#71: execution and recovery §3.2 commits an apply-config
		// operation owned, and §3.4's takeover fence compares that owner, in every state up to a
		// terminal one.
		{"committed apply-config with no owner", insertOperation, []any{op(), "apply-config", "committed", nil, 0, nil, nil, nil, nil, nil, nil}, "23514"},
		{"sending apply-config with no owner", insertOperation, []any{op(), "apply-config", "sending", nil, 0, nil, nil, nil, nil, nil, nil}, "23514"},
		{"verifying apply-config with no owner", insertOperation, []any{op(), "apply-config", "verifying", nil, 0, nil, nil, nil, nil, nil, nil}, "23514"},
		{"unresolved apply-config with no owner", insertOperation, []any{op(), "apply-config", "unresolved", nil, 0, nil, nil, nil, nil, nil, nil}, "23514"},
		// PA §3 TimelineEvent: apply-config and adopt operations are on the machine timeline, so
		// operation_event holds a publish or ingest operation's entries only, under its true kind.
		{"event of an apply-config operation", insertEvent, []any{a.applyConfig, 1, `{}`, "apply-config"}, "23514"},
		{"event of an adopt operation", insertEvent, []any{a.adopt, 1, `{}`, "adopt"}, "23514"},
		{"apply-config event named an ingest", insertEvent, []any{a.applyConfig, 2, `{}`, "ingest"}, "23503"},
		{"ingest event named a publish", insertEvent, []any{a.ingest, 2, `{}`, "publish"}, "23503"},
		{"record naming no operation", `INSERT INTO idempotency_record (principal, key, fingerprint, request_id, epoch, status, body, operation_id, created_at)
			SELECT $1, 'k0123456789abcdeY', $2, $3, epoch, 202, '{}', $4, now() FROM installation_state`,
			[]any{a.human, digest(4), id.New(id.Request), op()}, "23503"},
	} {
		if _, err := db.Exec(c.q, c.args...); sqlState(err) != c.want {
			t.Errorf("%s: %v; want SQLSTATE %s", c.name, err, c.want)
		}
	}
	// Positive controls beside the refusals: a running ingest of another revision, a publish of
	// the same revision, and a live claim for the key once the first is released.
	mustExec(t, db, insertOperation, op(), "ingest", "running", "o", 1, a.draft, 2, claim2, a.human, nil, nil)
	publish := op()
	mustExec(t, db, insertOperation, publish, "publish", "queued", nil, 0, a.draft, 1, nil, a.human, nil, nil)
	mustExec(t, db, insertEvent, publish, 1, `{"type":"queued"}`, "publish")
	// Owned apply-config operations in each fenced state commit; the fence ends at a terminal state.
	for _, state := range []string{"sending", "verifying", "unresolved"} {
		mustExec(t, db, insertOperation, op(), "apply-config", state, "run-1/4242/start-1", 2, nil, nil, nil, nil, nil, nil)
	}
	mustExec(t, db, insertOperation, op(), "apply-config", "failed", nil, 0, nil, nil, nil, nil, nil, nil)
	mustExec(t, db, insertOperation, op(), "ingest", "failed", nil, 0, a.draft, 1, claim3, a.human, nil, `{"type":"urn:bronzeward:problem:x"}`)
	mustExec(t, db, insertOperation, op(), "adopt", "completed", nil, 0, nil, nil, nil, nil, nil, nil)
	mustExec(t, db, "UPDATE staging_claim SET state = 'released' WHERE id = $1", a.claim)
	mustExec(t, db, insertClaim, id.New(id.Ingestion), "encrypted", "held", []byte{1}, a.human, "k0123456789abcdef")
	mustExec(t, db, insertClaim, id.New(id.Ingestion), "encrypted", "resumed", []byte{1}, nil, nil)
	mustExec(t, db, insertReference, a.ibr, "pki/extra-ca", "string", 1, "base64", generation(a.cluster, a.claim))
	// The baseline revision's counter control needs releases, so it is TestReleaseConstraints'.
}

// The control for 0005: with each of its constraints dropped, the row TestAdoptionConstraints
// expects that constraint to refuse commits, so it is that constraint, not another, that refuses.
func TestOperationFenceControl(t *testing.T) {
	db, _ := installed(t)
	a := adoptionRows(t, db)
	for _, c := range []struct {
		drop, q string
		args    []any
	}{
		{"ALTER TABLE operation DROP CONSTRAINT operation_apply_config_owned", insertOperation,
			[]any{id.New(id.Operation), "apply-config", "sending", nil, 0, nil, nil, nil, nil, nil, nil}},
		{"ALTER TABLE operation_event DROP CONSTRAINT operation_event_job_kind", insertEvent,
			[]any{a.applyConfig, 1, `{}`, "apply-config"}},
		{"ALTER TABLE operation_event DROP CONSTRAINT operation_event_operation_kind", insertEvent,
			[]any{a.ingest, 2, `{}`, "publish"}},
	} {
		func() {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.Exec(c.drop); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(c.q, c.args...); err != nil {
				t.Errorf("after %s: %v; want it to commit", c.drop, err)
			}
		}()
	}
}

// 0005 refuses an installation whose operation_event already holds rows, which carry no kind to
// check, and leaves it at 0004; the control upgrades the same installation without the row.
func TestOperationFenceRefusesKindlessEvents(t *testing.T) {
	ctx := context.Background()
	ms, err := Embedded()
	if err != nil || len(ms) < 5 || ms[3].Version != 4 {
		t.Fatalf("embedded migrations: %v; want 0004 then 0005", err)
	}
	for _, withEvent := range []bool{true, false} {
		db, _ := dbtest.New(t)
		if _, err := Apply(ctx, db, ms[:4]); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Install(ctx, db); err != nil {
			t.Fatal(err)
		}
		adopt := id.New(id.Operation)
		mustExec(t, db, `INSERT INTO operation (id, kind, state, epoch, created_at)
			SELECT $1, 'adopt', 'completed', epoch, now() FROM installation_state`, adopt)
		if withEvent {
			mustExec(t, db, `INSERT INTO operation_event (operation, number, epoch, entry, at)
				SELECT $1, 1, epoch, '{}', now() FROM installation_state`, adopt)
		}
		got, err := Apply(ctx, db, ms)
		var top int
		if err := db.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&top); err != nil {
			t.Fatal(err)
		}
		switch {
		// The message, not only the failure: without the guard, the NOT NULL column would still fail
		// on the row, with an error naming neither the cause nor the remedy.
		case withEvent && (err == nil || !strings.Contains(err.Error(), "operation_event holds rows") || len(got) != 0 || top != 4):
			t.Errorf("with an event: applied %v, %v, at %d; want 0005 refused and the installation at 0004", got, err, top)
		case !withEvent && (err != nil || top < 5):
			t.Errorf("without an event: applied %v, %v, at %d; want the upgrade to apply", got, err, top)
		}
	}
}

// Choice §17.3: each immutable 0004 table's trigger fires on UPDATE, DELETE and TRUNCATE.
func TestAdoptionImmutableTables(t *testing.T) {
	db, _ := installed(t)
	adoptionRows(t, db)
	for _, stmt := range []string{
		"UPDATE import_base_revision SET document = document", "DELETE FROM import_base_revision",
		"TRUNCATE import_base_revision CASCADE",
		"UPDATE import_base_reference SET version = version", "DELETE FROM import_base_reference",
		"TRUNCATE import_base_reference",
		"UPDATE operation_event SET entry = entry", "DELETE FROM operation_event", "TRUNCATE operation_event",
	} {
		if _, err := db.Exec(stmt); sqlState(err) != ImmutableSQLState {
			t.Errorf("%s: %v; want SQLSTATE %s", stmt, err, ImmutableSQLState)
		}
	}
	for _, table := range []string{"import_base_revision", "import_base_reference", "operation_event"} {
		if count(t, db, table) == 0 {
			t.Errorf("%s: a refused statement removed its rows", table)
		}
	}
}

// The control: with each table's row trigger dropped, the UPDATE and DELETE the test above refuses
// succeed, so it is the trigger that refuses them.
func TestAdoptionImmutableControl(t *testing.T) {
	db, _ := installed(t)
	a := adoptionRows(t, db)
	for _, c := range []struct{ table, update, del string }{
		{"import_base_reference", "UPDATE import_base_reference SET version = version", "DELETE FROM import_base_reference"},
		{"operation_event", "UPDATE operation_event SET entry = entry", "DELETE FROM operation_event"},
		// The one no draft entry or reference row names.
		{"import_base_revision", "UPDATE import_base_revision SET document = document", "DELETE FROM import_base_revision WHERE id = '" + a.otherIBR + "'"},
	} {
		func() {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.Exec("DROP TRIGGER immutable_rows ON " + c.table); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(c.update); err != nil {
				t.Errorf("%s without the trigger: %v", c.update, err)
			}
			if _, err := tx.Exec(c.del); err != nil {
				t.Errorf("%s without the trigger: %v", c.del, err)
			}
		}()
	}
}

// PA §7.3's machine key: one SMBIOS UUID is one record. The control drops the unique index and
// the same second insert commits, so it is the index that refuses it.
func TestSMBIOSUniqueControl(t *testing.T) {
	db, _ := installed(t)
	a := adoptionRows(t, db)
	dup := []any{id.New(id.Machine), a.other, "0b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b", nil, "normal"}
	if _, err := db.Exec(insertMachine, dup...); sqlState(err) != "23505" {
		t.Fatalf("duplicate with the index: %v; want 23505", err)
	}
	mustExec(t, db, "DROP INDEX machine_smbios_uuid")
	if _, err := db.Exec(insertMachine, dup...); err != nil {
		t.Fatalf("duplicate without the index: %v; want it to commit", err)
	}
}

// §11: the embedded migrations apply on a fresh database, a second run applies nothing, and the
// startup check accepts the result; an installation at 0003 upgrades to the binary's schema.
func TestEmbeddedApplyTwiceAndUpgrade(t *testing.T) {
	db, ms := installed(t)
	ctx := context.Background()
	if got, err := Apply(ctx, db, ms); err != nil || len(got) != 0 {
		t.Fatalf("second run: %v, %v", got, err)
	}
	if err := Check(ctx, db, ms); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cluster", "machine", "machine_state", "import_base_revision", "import_base_reference",
		"draft", "draft_entry", "staging_claim", "operation", "operation_event"} {
		if !tableExists(t, db, name) {
			t.Errorf("%s missing", name)
		}
	}

	fresh, _ := dbtest.New(t)
	if _, err := Apply(ctx, fresh, ms[:3]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Install(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	// Every migration from 0004 on, so the check stays true as later migrations are added.
	var want []int
	for _, m := range ms[3:] {
		want = append(want, m.Version)
	}
	if got, err := Apply(ctx, fresh, ms); err != nil || want[0] != 4 || !slices.Equal(got, want) {
		t.Fatalf("upgrade: %v, %v; want %v", got, err, want)
	}
	if _, _, err := Install(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if err := Check(ctx, fresh, ms); err != nil {
		t.Fatalf("after the upgrade: %v", err)
	}
}
