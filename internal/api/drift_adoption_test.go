package api

import (
	"database/sql"
	"net/http"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// openDriftOn opens a drift record on machine as a drift observation's recording transaction does
// (execution and recovery §6.1): the observation's start, the observation, the opening entry and the
// record. It answers the opening entry's revision.
func openDriftOn(t *testing.T, db *sql.DB, machine string) int64 {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var rev int64
	if err := tx.QueryRow(`UPDATE machine SET revision_counter = revision_counter + 3 WHERE id = $1 RETURNING revision_counter`,
		machine).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	basis, obs := rev-2, id.New(id.Observation)
	mustExec(t, tx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		SELECT m.id, $2, epoch, 'observation-started', jsonb_build_object('purpose', 'drift', 'endpoint', m.talos_endpoint::text,
			'controller', 'test-controller'), now()
		FROM installation_state, machine m WHERE m.id = $1`, machine, basis)
	mustExec(t, tx, `INSERT INTO observation_start (machine, revision, purpose, endpoint, controller)
		SELECT id, $2, 'drift', talos_endpoint, 'test-controller' FROM machine WHERE id = $1`, machine, basis)
	mustExec(t, tx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		SELECT $1, $2, epoch, 'observation', jsonb_build_object('observation', $3::text, 'purpose', 'drift', 'basis', $4::bigint), now()
		FROM installation_state`, machine, rev-1, obs, basis)
	mustExec(t, tx, `INSERT INTO observation (id, machine, basis, revision, access_path, access_version, access_created,
			configuration_digest, unread, at)
		SELECT $1, id, $3, $4, 'access/talos/' || cluster, 3, '2026-10-01T00:00:00Z', decode(repeat('09', 32), 'hex'), '{}', now()
		FROM machine WHERE id = $2`, obs, machine, basis, rev-1)
	mustExec(t, tx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		SELECT $1, $2, epoch, 'drift', jsonb_build_object('record', $2::bigint, 'change', 'opened', 'observation', $3::text), now()
		FROM installation_state`, machine, rev, obs)
	mustExec(t, tx, `INSERT INTO drift_record (machine, opened, observation, applied_digest, observed_digest, last_observation)
		VALUES ($1, $2, $3, decode(repeat('01', 32), 'hex'), decode(repeat('09', 32), 'hex'), $3)`, machine, rev, obs)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return rev
}

// closeDriftOn closes machine's drift record opened at revision opened, as returned.
func closeDriftOn(t *testing.T, db *sql.DB, machine string, opened int64) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var rev int64
	if err := tx.QueryRow(`UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1 RETURNING revision_counter`,
		machine).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	mustExec(t, tx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		SELECT $1, $2, epoch, 'drift', jsonb_build_object('record', $3::bigint, 'change', 'closed', 'observation',
			(SELECT observation FROM drift_record WHERE machine = $1 AND opened = $3), 'resolution', 'returned'), now()
		FROM installation_state`, machine, rev, opened)
	mustExec(t, tx, `UPDATE drift_record SET closed = $3, resolution = 'returned' WHERE machine = $1 AND opened = $2`,
		machine, opened, rev)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// driftAdoption is an ingestion request body's members for a drift adoption of the node.
var driftAdoption = map[string]any{"kind": "drift-adoption", "source": "machine", "document": nil, "staging": "encrypted"}

// Execution and recovery §6.3 step 1, choice §10.37: a drift adoption of a machine with an open
// drift record reads the node and ingests it as an import does, under a claim of kind
// drift-adoption that the ingestion resource serves: the sanitized document becomes the machine's
// new import base. The ingestion binds no drift record, so the record stays open.
func TestDriftAdoptionIngestion(t *testing.T) {
	me := newMachineEnv(t)
	opened := openDriftOn(t, me.db, me.machine)
	op, j := me.startJob(t, driftAdoption)
	if j.node == nil || j.claim.Kind != "drift-adoption" {
		t.Fatalf("job %+v", j)
	}
	me.runWith(t, options{}, j)
	if r := readOp(t, me.db, op); r.state != "succeeded" {
		t.Fatalf("operation %+v", r)
	}
	var kind string
	if err := me.db.QueryRow(`SELECT kind FROM staging_claim WHERE id = $1`, j.claim.ID).Scan(&kind); err != nil || kind != "drift-adoption" {
		t.Fatalf("claim kind %q, %v", kind, err)
	}
	if n := count(t, me.db, `SELECT count(*) FROM import_base_revision WHERE machine = $1`, me.machine); n != 1 {
		t.Fatalf("%d import base revisions", n)
	}
	b := decode[map[string]any](t, me.do(me.api, getIngestionCall(me.human("h-author"), j.claim.ID)), http.StatusOK)
	if b["kind"] != "drift-adoption" || b["machine"] != me.machine {
		t.Fatalf("ingestion %v", b)
	}
	if n := count(t, me.db, `SELECT count(*) FROM drift_record WHERE machine = $1 AND opened = $2 AND closed IS NULL`,
		me.machine, opened); n != 1 {
		t.Fatal("the drift adoption closed the drift record")
	}
}

// Choice §10.37: a drift adoption needs the machine's open drift record when it starts; with none,
// or with the record closed, it is 409 conflict naming the machine, and no claim, operation or job
// exists. Its source is the node only.
func TestDriftAdoptionRefusals(t *testing.T) {
	ie := newIngestEnv(t, options{})
	author := ie.human("h-author")
	start := func(k string) map[string]any {
		t.Helper()
		return wantProblem(t, ie.do(ie.api, ingestCall(author, k, ie.etag, ie.body(driftAdoption))), http.StatusConflict, "conflict")
	}
	if doc := start("k-drift-none-0123456"); doc["machine"] != ie.machine {
		t.Fatalf("the refusal names %v; want %s", doc["machine"], ie.machine)
	}
	closeDriftOn(t, ie.db, ie.machine, openDriftOn(t, ie.db, ie.machine))
	start("k-drift-closed-01234")
	// Another machine's open record does not admit this one.
	openDriftOn(t, ie.db, ie.otherMachine)
	start("k-drift-other-012345")
	if claims, ops := ie.rowCounts(t); claims != 0 || ops != 0 || len(ie.runs()) != 0 {
		t.Fatalf("%d claims, %d operations, %d jobs", claims, ops, len(ie.runs()))
	}
}
