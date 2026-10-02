package staging

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// effective reads the claim's state as every read does (compilation §3.5).
func (f fixture) effective(t *testing.T, claim string) string {
	t.Helper()
	var s string
	if err := f.db.QueryRow(`SELECT `+EffectiveStateSQL+` FROM staging_claim WHERE id = $1`, claim).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

type opState struct {
	id, state         string
	owner, lease      bool // owner and lease_until are set
	problem           map[string]any
	events            []map[string]any
	lastEvent, epochs int
}

func (f fixture) op(t *testing.T, claim string) opState {
	t.Helper()
	var o opState
	var problem []byte
	if err := f.db.QueryRow(`SELECT id, state, owner IS NOT NULL, lease_until IS NOT NULL, error, last_event
		FROM operation WHERE ingestion = $1`, claim).Scan(&o.id, &o.state, &o.owner, &o.lease, &problem, &o.lastEvent); err != nil {
		t.Fatal(err)
	}
	if problem != nil {
		if err := json.Unmarshal(problem, &o.problem); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := f.db.Query(`SELECT number, entry, epoch = (SELECT epoch FROM installation_state) FROM operation_event
		WHERE operation = $1 ORDER BY number`, o.id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var n int
		var entry []byte
		var current bool
		if err := rows.Scan(&n, &entry, &current); err != nil {
			t.Fatal(err)
		}
		var e map[string]any
		if err := json.Unmarshal(entry, &e); err != nil {
			t.Fatal(err)
		}
		if n != len(o.events)+1 {
			t.Fatalf("event %d out of sequence", n)
		}
		if current {
			o.epochs++
		}
		o.events = append(o.events, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return o
}

// wantAbandoned checks an abandonment as compilation §3.5 and persistence-api §8.2 write it: the
// claim abandoned with no payload, the operation failed ingestion-abandoned with its owner and
// lease cleared, and one terminal event at the next number, in the current epoch.
func (f fixture) wantAbandoned(t *testing.T, claim string, eventsBefore int) {
	t.Helper()
	if r := f.row(t, claim); r.state != "abandoned" || r.payload != nil || r.digest != nil {
		t.Errorf("claim %s, payload %v, digest %v", r.state, r.payload != nil, r.digest != nil)
	}
	o := f.op(t, claim)
	if o.state != "failed" || o.owner || o.lease {
		t.Errorf("operation %s, owner %v, lease %v", o.state, o.owner, o.lease)
	}
	want := map[string]any{"type": "urn:bronzeward:problem:ingestion-abandoned", "title": AbandonedTitle, "status": float64(409), "instance": o.id}
	for k, v := range want {
		if o.problem[k] != v {
			t.Errorf("problem %s = %v, want %v", k, o.problem[k], v)
		}
	}
	if len(o.events) != eventsBefore+1 || o.lastEvent != eventsBefore+1 || o.epochs != len(o.events) {
		t.Fatalf("events %v, last_event %d, in the current epoch %d", o.events, o.lastEvent, o.epochs)
	}
	if last := o.events[eventsBefore]; last["type"] != "failed" || last["code"] != "ingestion-abandoned" || len(last) != 2 {
		t.Errorf("terminal event %v", last)
	}
}

func (f fixture) wantUntouched(t *testing.T, claim, state string) {
	t.Helper()
	if r := f.row(t, claim); r.state != state {
		t.Errorf("claim %s, want %s", r.state, state)
	}
	if o := f.op(t, claim); o.state != "running" || len(o.events) != 0 {
		t.Errorf("operation %s with %d events", o.state, len(o.events))
	}
}

// Compilation §3.5: a claim past its absolute expiry, or a transient claim past its lease, is
// abandoned at read time and by the sweep; an encrypted claim whose lease lapsed waits for a
// takeover until its expiry; a live or ended claim is left as it is.
func TestSweepMatrix(t *testing.T) {
	f := setup(t)
	_, transientLapsed := f.create(t, "transient")
	exec(t, f.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, transientLapsed.ID)
	_, transientExpired := f.create(t, "transient")
	exec(t, f.db, `UPDATE staging_claim SET lease_until = now() - interval '2 seconds', expires_at = now() - interval '1 second'
		WHERE id = $1`, transientExpired.ID)
	o, encryptedLapsed := f.create(t, "encrypted")
	sum := sha256.Sum256([]byte("envelope"))
	if err := StorePayload(t.Context(), f.db, o, encryptedLapsed, []byte("vault:v1:sealed"), sum); err != nil {
		t.Fatal(err)
	}
	exec(t, f.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, encryptedLapsed.ID)
	o, encryptedExpired := f.create(t, "encrypted")
	if err := StorePayload(t.Context(), f.db, o, encryptedExpired, []byte("vault:v1:sealed"), sum); err != nil {
		t.Fatal(err)
	}
	// The started event an earlier step wrote: the terminal one is numbered after it.
	exec(t, f.db, `UPDATE operation SET last_event = 1 WHERE ingestion = $1`, encryptedExpired.ID)
	exec(t, f.db, `INSERT INTO operation_event (operation, number, epoch, kind, entry, at)
		SELECT id, 1, epoch, 'ingest', '{"type":"started"}', now() FROM operation WHERE ingestion = $1`, encryptedExpired.ID)
	exec(t, f.db, `UPDATE staging_claim SET lease_until = now() - interval '2 seconds', expires_at = now() - interval '1 second'
		WHERE id = $1`, encryptedExpired.ID)
	_, live := f.create(t, "transient")
	o, released := f.create(t, "transient")
	if err := inTx(t.Context(), f.db, func(tx *sql.Tx) error { return Release(t.Context(), tx, o, released) }); err != nil {
		t.Fatal(err)
	}
	exec(t, f.db, `UPDATE staging_claim SET lease_until = now() - interval '2 seconds', expires_at = now() - interval '1 second'
		WHERE id = $1`, released.ID)

	for claim, want := range map[string]string{
		transientLapsed.ID: "abandoned", transientExpired.ID: "abandoned", encryptedLapsed.ID: "held",
		encryptedExpired.ID: "abandoned", live.ID: "held", released.ID: "released",
	} {
		if got := f.effective(t, claim); got != want {
			t.Errorf("effective state of %s: %s, want %s", claim, got, want)
		}
	}

	n, err := Sweep(t.Context(), f.db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("swept %d, want 3", n)
	}
	f.wantAbandoned(t, transientLapsed.ID, 0)
	f.wantAbandoned(t, transientExpired.ID, 0)
	f.wantAbandoned(t, encryptedExpired.ID, 1)
	f.wantUntouched(t, encryptedLapsed.ID, "held")
	if r := f.row(t, encryptedLapsed.ID); r.payload == nil {
		t.Error("the lapsed encrypted claim's payload was cleared before its expiry")
	}
	f.wantUntouched(t, live.ID, "held")
	if r := f.row(t, released.ID); r.state != "released" {
		t.Errorf("released claim %s", r.state)
	}
}

// The candidate scan decides nothing: a claim that changed after it was listed, its lease
// extended by a heartbeat that started before the lapse or its run released, is not abandoned.
func TestSweepLosesToHeartbeat(t *testing.T) {
	f := setup(t)
	_, extended := f.create(t, "transient")
	_, released := f.create(t, "transient")
	for _, c := range []string{extended.ID, released.ID} {
		exec(t, f.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, c)
	}
	n, err := sweep(t.Context(), f.db, func() {
		exec(t, f.db, `UPDATE staging_claim SET lease_until = now() + interval '1 minute' WHERE id = $1`, extended.ID)
		exec(t, f.db, `UPDATE staging_claim SET state = 'released' WHERE id = $1`, released.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("swept %d, want 0", n)
	}
	f.wantUntouched(t, extended.ID, "held")
	if r := f.row(t, released.ID); r.state != "released" {
		t.Errorf("released claim %s", r.state)
	}
}

// A claim whose abandonment fails does not hold back the others: the scan lists it first, the
// sweep reports its error and still abandons the claims after it.
func TestSweepContinuesPastAFailure(t *testing.T) {
	f := setup(t)
	_, stuck := f.create(t, "transient")
	_, next := f.create(t, "transient")
	exec(t, f.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second', expires_at = now() + interval '5 minutes'
		WHERE id = $1`, stuck.ID)
	exec(t, f.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, next.ID)
	// An event the operation's last_event does not count: the terminal event's number is taken.
	exec(t, f.db, `INSERT INTO operation_event (operation, number, epoch, kind, entry, at)
		SELECT id, 1, epoch, 'ingest', '{"type":"started"}', now() FROM operation WHERE ingestion = $1`, stuck.ID)
	n, err := Sweep(t.Context(), f.db)
	if err == nil || !strings.Contains(err.Error(), stuck.ID) {
		t.Fatalf("sweep error %v, want one naming %s", err, stuck.ID)
	}
	if n != 1 {
		t.Errorf("swept %d, want 1", n)
	}
	f.wantAbandoned(t, next.ID, 0)
	if r := f.row(t, stuck.ID); r.state != "held" {
		t.Errorf("the failed claim is %s, its transaction rolled back", r.state)
	}
}

// A sweep cancelled after its scan stops there, and reports the cancellation once, not once per
// claim it did not reach.
func TestSweepStopsWhenCancelled(t *testing.T) {
	f := setup(t)
	for range 3 {
		_, c := f.create(t, "transient")
		exec(t, f.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, c.ID)
	}
	ctx, cancel := context.WithCancel(t.Context())
	n, err := sweep(ctx, f.db, cancel)
	if n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("swept %d, %v; want 0 and the cancellation", n, err)
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok && len(j.Unwrap()) != 1 {
		t.Errorf("%d errors, want the cancellation once: %v", len(j.Unwrap()), err)
	}
}

// A second sweep finds nothing to write, and leaves the first one's records as they are.
func TestSweepIdempotent(t *testing.T) {
	f := setup(t)
	_, c := f.create(t, "transient")
	exec(t, f.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, c.ID)
	if n, err := Sweep(t.Context(), f.db); err != nil || n != 1 {
		t.Fatalf("first sweep %d, %v", n, err)
	}
	if n, err := Sweep(t.Context(), f.db); err != nil || n != 0 {
		t.Fatalf("second sweep %d, %v", n, err)
	}
	f.wantAbandoned(t, c.ID, 0)
}
