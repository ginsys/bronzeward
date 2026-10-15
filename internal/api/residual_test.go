package api

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// startedDrift records the start of a drift observation of machine, as a controller does before
// it reads (execution and recovery §4.1), and answers its basis: the read has begun, its result is
// not recorded.
func startedDrift(t *testing.T, db *sql.DB, machine string) int64 {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var basis int64
	if err := tx.QueryRow(`UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1 RETURNING revision_counter`,
		machine).Scan(&basis); err != nil {
		t.Fatal(err)
	}
	mustExec(t, tx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		SELECT m.id, $2, epoch, 'observation-started', jsonb_build_object('purpose', 'drift', 'endpoint', m.talos_endpoint::text,
			'controller', 'test-controller'), now()
		FROM installation_state, machine m WHERE m.id = $1`, machine, basis)
	mustExec(t, tx, `INSERT INTO observation_start (machine, revision, purpose, endpoint, controller)
		SELECT id, $2, 'drift', talos_endpoint, 'test-controller' FROM machine WHERE id = $1`, machine, basis)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return basis
}

// recordedDrift records the result of the drift observation started at basis, reading digest, and
// passes it to the drift rule as the observation's recording transaction does (§6.1), under the
// machine's lock. It answers the drift change.
func recordedDrift(t *testing.T, db *sql.DB, machine string, basis int64, digest []byte) string {
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
	obs := id.New(id.Observation)
	mustExec(t, tx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		SELECT $1, $2, epoch, 'observation', jsonb_build_object('observation', $3::text, 'purpose', 'drift', 'basis', $4::bigint), now()
		FROM installation_state`, machine, rev, obs, basis)
	mustExec(t, tx, `INSERT INTO observation (id, machine, basis, revision, access_path, access_version, access_created,
			configuration_digest, unread, at)
		SELECT $1, id, $3, $4, 'access/talos/' || cluster, 3, '2026-10-01T00:00:00Z', $5, '{}', now()
		FROM machine WHERE id = $2`, obs, machine, basis, rev, digest)
	var ep string
	if err := tx.QueryRow(`SELECT epoch FROM installation_state`).Scan(&ep); err != nil {
		t.Fatal(err)
	}
	change, err := applyDrift(context.Background(), tx, machine, ep, obs, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return change
}

// The residual's evidence (execution and recovery §4, §9.2): a completion and an adoption are
// committed while a contradicting higher-basis read has started but not recorded its result, which
// blocks neither. Recorded after the Applied change, that result opens no drift record; the next
// drift observation, reading the same digest, does.
func TestResidualLateRead(t *testing.T) {
	t.Parallel()
	changed := bytes.Repeat([]byte{0xee}, 32)
	late := func(t *testing.T, db *sql.DB, machine string, basis int64) {
		t.Helper()
		if change := recordedDrift(t, db, machine, basis, changed); change != "" {
			t.Fatalf("the late result made drift change %q", change)
		}
		if n := count(t, db, `SELECT count(*) FROM drift_record WHERE machine = $1 AND closed IS NULL`, machine); n != 0 {
			t.Fatal("the late result opened a drift record")
		}
		if change := recordedDrift(t, db, machine, startedDrift(t, db, machine), changed); change != driftOpened {
			t.Fatalf("the next drift observation made drift change %q", change)
		}
	}
	t.Run("completion", func(t *testing.T) {
		t.Parallel()
		ae := newAttemptEnv(t, "")
		ae.reach(t, "verifying")
		obs := ae.observed(t, nil)
		basis := startedDrift(t, ae.db, ae.machine)
		r, err := ae.a.settleOperation(context.Background(), ae.op, 1, obs)
		if err != nil {
			t.Fatal(err)
		}
		if r.Outcome != "completed" {
			t.Fatalf("settled %+v; an unrecorded read blocked the completion", r)
		}
		late(t, ae.db, ae.machine, basis)
	})
	t.Run("adoption", func(t *testing.T) {
		t.Parallel()
		ae := newDriftedAdoptEnv(t, "")
		ae.approveIt(t)
		ae.observed(t, nil)
		basis := startedDrift(t, ae.db, ae.machine)
		if _, err := ae.a.commitAdoption(context.Background(), ae.pid); err != nil {
			t.Fatalf("an unrecorded read blocked the adoption: %v", err)
		}
		late(t, ae.db, ae.machine, basis)
	})
}
