package api

import (
	"bytes"
	"context"
	"net/http"
	"testing"
)

// applyDriftNow applies the drift rules to obs under the machine's lock, as a recording transaction
// does, and answers the change made.
func (ce *commitEnv) applyDriftNow(t *testing.T, obs string) string {
	t.Helper()
	tx, err := ce.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(t, tx, `SELECT 1 FROM machine WHERE id = $1 FOR UPDATE`, ce.machine)
	change, err := applyDrift(context.Background(), tx, ce.machine, epoch(t, ce.db), obs, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return change
}

// Execution and recovery §3.2 (choice §10.23), §6.1, §6.5: a commitment refused on its evidence's
// digest opens a drift record after the refusal entry, and a further one is recorded on it; while
// it is open comparison 6 refuses an ordinary plan; evidence equal to Applied's closes it returned,
// and the plan then commits. The machine resource serves the open record.
func TestCommitmentDrift(t *testing.T) {
	t.Parallel()
	ce := newCommitEnv(t, "")
	ce.approveIt(t)
	first := ce.evidence(t, ce.pid, func(s *evSeed) { s.digest = bytes.Repeat([]byte{9}, 32) })
	_, err := ce.a.commitPlan(context.Background(), ce.pid)
	ce.wantRefused(t, err, "3", "the evidence shows a configuration digest other than the plan's", first)
	if n := count(t, ce.db, `SELECT count(*) FROM drift_record d JOIN machine m ON m.id = d.machine AND m.revision_counter = d.opened
			JOIN machine_event r ON r.machine = d.machine AND r.revision = d.opened - 1 AND r.kind = 'refusal'
		WHERE d.machine = $1 AND d.closed IS NULL AND d.observation = $2 AND d.observed_digest = $3`,
		ce.machine, first, bytes.Repeat([]byte{9}, 32)); n != 1 {
		t.Fatal("no drift record opened by the entry after the refusal")
	}
	got := decode[machineBody](t, ce.do(ce.api, call{method: "GET", path: prefix + "/machines/" + ce.machine,
		token: ce.human("h-viewer")}), http.StatusOK)
	if got.OpenDrift == nil || got.OpenDrift.Observation != first || got.OpenDrift.LastObservation != first ||
		got.OpenDrift.LateLanding != nil {
		t.Fatalf("openDrift %+v", got.OpenDrift)
	}

	second := ce.evidence(t, ce.pid, func(s *evSeed) { s.digest = bytes.Repeat([]byte{10}, 32) })
	_, err = ce.a.commitPlan(context.Background(), ce.pid)
	ce.wantRefused(t, err, "3", "the evidence shows a configuration digest other than the plan's", second)
	if n := count(t, ce.db, `SELECT count(*) FROM drift_record d JOIN machine_event e ON e.machine = d.machine
			AND (e.entry->>'record')::bigint = d.opened AND e.kind = 'drift' AND e.entry->>'change' = 'observed'
			AND e.entry->>'observation' = $2
		WHERE d.machine = $1 AND d.closed IS NULL AND d.observation = $3 AND d.last_observation = $2 AND d.observed_digest = $4`,
		ce.machine, second, first, bytes.Repeat([]byte{10}, 32)); n != 1 {
		t.Fatal("the second digest is not recorded on the open record")
	}
	if n := count(t, ce.db, `SELECT count(*) FROM drift_record WHERE machine = $1`, ce.machine); n != 1 {
		t.Fatalf("%d drift records", n)
	}

	equal := ce.evidence(t, ce.pid, nil)
	_, err = ce.a.commitPlan(context.Background(), ce.pid)
	ce.wantRefused(t, err, "6", "a drift record is open on the machine", "")

	if change := ce.applyDriftNow(t, equal); change != driftReturned {
		t.Fatalf("drift change %q", change)
	}
	if n := count(t, ce.db, `SELECT count(*) FROM drift_record d JOIN machine_event e ON e.machine = d.machine AND e.revision = d.closed
		WHERE d.machine = $1 AND d.resolution = 'returned' AND e.kind = 'drift' AND e.entry->>'change' = 'closed'
			AND e.entry->>'resolution' = 'returned' AND e.entry->>'observation' = $2`, ce.machine, equal); n != 1 {
		t.Fatal("the record is not closed returned by its closing entry")
	}
	if _, err := ce.a.commitPlan(context.Background(), ce.pid); err != nil {
		t.Fatal(err)
	}
}

// §6.1's basis rules: an observation superseded by a later read of the configuration, or begun
// before Applied changed, or read while an operation took the scope, changes no drift record.
func TestDriftExcluded(t *testing.T) {
	t.Parallel()
	other := func(s *evSeed) { s.digest = bytes.Repeat([]byte{9}, 32) }
	for _, c := range []struct {
		name  string
		after func(*testing.T, *commitEnv)
	}{
		{name: "a later read", after: func(t *testing.T, ce *commitEnv) { ce.evidence(t, ce.pid, nil) }},
		{name: "Applied changed", after: func(t *testing.T, ce *commitEnv) {
			mustExec(t, ce.db, `WITH r AS (UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1
					RETURNING revision_counter)
				INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
				SELECT $1, r.revision_counter, epoch, 'applied-change', '{"source":"adoption"}', now() FROM r, installation_state`,
				ce.machine)
		}},
		{name: "scope taken", after: func(t *testing.T, ce *commitEnv) { ce.commitAs(t, ce.pid) }},
		{name: "scope held and released", after: func(t *testing.T, ce *commitEnv) { // no operation holds it now
			mustExec(t, ce.db, `WITH r AS (UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1
					RETURNING revision_counter)
				INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
				SELECT $1, r.revision_counter, epoch, 'operation-state',
					'{"operation":"op_aaaaaaaaaaaaaaaaaaaaaaaaaa","from":"verifying","to":"failed"}', now() FROM r, installation_state`,
				ce.machine)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ce := newCommitEnv(t, "")
			ce.approveIt(t)
			obs := ce.evidence(t, ce.pid, other)
			c.after(t, ce)
			if change := ce.applyDriftNow(t, obs); change != "" {
				t.Fatalf("drift change %q", change)
			}
			if n := count(t, ce.db, `SELECT count(*) FROM drift_record WHERE machine = $1`, ce.machine); n != 0 {
				t.Fatalf("%d drift records", n)
			}
		})
	}
}
