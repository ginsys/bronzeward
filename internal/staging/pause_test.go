package staging

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// pauseRow is a claim's pause-relevant columns and its running operation's lease.
type pauseRow struct {
	row
	review     sql.NullString
	owner      string
	opLease    sql.NullTime
	opOwner    sql.NullString
	opOwnerGen sql.NullInt64
}

func (f fixture) pauseRow(t *testing.T, claim string) pauseRow {
	t.Helper()
	r := pauseRow{row: f.row(t, claim)}
	if err := f.db.QueryRow(`SELECT c.review, c.owner, o.lease_until, o.owner, o.owner_gen FROM staging_claim c
		LEFT JOIN operation o ON o.ingestion = c.id AND o.state = 'running' WHERE c.id = $1`, claim).
		Scan(&r.review, &r.owner, &r.opLease, &r.opOwner, &r.opOwnerGen); err != nil {
		t.Fatal(err)
	}
	return r
}

// Compilation §3.6 item 1, PA §5.1: the pause stores the envelope, sets the claim paused and ends
// its lease and its operation's at the pause instant, in one transaction; the owner fields stay,
// the operation stays running and the review pending.
func TestPause(t *testing.T) {
	f := setup(t)
	o, c := f.createWith(t, "encrypted", "pending")
	ct, sum := []byte("vault:v1:staged"), sha256.Sum256([]byte("envelope"))
	var at time.Time
	if err := inTx(t.Context(), f.db, func(tx *sql.Tx) error {
		if err := Pause(t.Context(), tx, o, c, ct, sum); err != nil {
			return err
		}
		return tx.QueryRow(`SELECT clock_timestamp()`).Scan(&at)
	}); err != nil {
		t.Fatal(err)
	}
	r := f.pauseRow(t, c.ID)
	if r.state != "paused" || r.gen != 1 || string(r.payload) != string(ct) || string(r.digest) != string(sum[:]) ||
		r.review.String != "pending" || r.owner != o.ID {
		t.Fatalf("claim %+v", r)
	}
	if r.lease.After(at) || !r.opLease.Valid || !r.opLease.Time.Equal(r.lease) || r.opOwner.String != o.ID || r.opOwnerGen.Int64 != 1 {
		t.Fatalf("lease %v (pause at %v), operation lease %v owner %v/%v", r.lease, at, r.opLease, r.opOwner, r.opOwnerGen)
	}
}

// Compilation §3.6 item 1: a taken-over claim pauses with its stored envelope unchanged (nil).
func TestPauseKeepsStoredEnvelope(t *testing.T) {
	f := setup(t)
	o, c := f.createWith(t, "encrypted", "pending")
	ct, sum := []byte("vault:v1:staged"), sha256.Sum256([]byte("envelope"))
	if err := inTx(t.Context(), f.db, func(tx *sql.Tx) error { return StorePayload(t.Context(), tx, o, c, ct, sum) }); err != nil {
		t.Fatal(err)
	}
	if err := inTx(t.Context(), f.db, func(tx *sql.Tx) error { return Pause(t.Context(), tx, o, c, nil, [32]byte{}) }); err != nil {
		t.Fatal(err)
	}
	if r := f.row(t, c.ID); r.state != "paused" || string(r.payload) != string(ct) || string(r.digest) != string(sum[:]) {
		t.Fatalf("claim %+v", r)
	}
}

// The pause is an owner transition (compilation §3.2): a claim with no pending review, another
// owner generation's, or one whose lease lapsed is refused and left as it was.
func TestPauseRefused(t *testing.T) {
	ct, sum := []byte("vault:v1:staged"), sha256.Sum256([]byte("envelope"))
	for _, tc := range []struct {
		name, review string
		prepare      func(t *testing.T, f fixture, c *Claim)
	}{
		{"no review", "", func(*testing.T, fixture, *Claim) {}},
		{"stale generation", "pending", func(_ *testing.T, _ fixture, c *Claim) { c.Gen = 2 }},
		{"lapsed lease", "pending", func(t *testing.T, f fixture, c *Claim) {
			exec(t, f.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, c.ID)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			o, c := f.createWith(t, "encrypted", tc.review)
			tc.prepare(t, f, &c)
			err := inTx(t.Context(), f.db, func(tx *sql.Tx) error { return Pause(t.Context(), tx, o, c, ct, sum) })
			if !errors.Is(err, ErrFenced) {
				t.Fatalf("pause: %v, want ErrFenced", err)
			}
			if r := f.row(t, c.ID); r.state != "held" || r.payload != nil {
				t.Fatalf("claim %+v", r)
			}
		})
	}
}

// Compilation §3.6 item 1, PA §16: a paused claim's owner transitions all fail, its pausing run's
// heartbeat included, even with its lease still in the future.
func TestPausedOwnerTransitionsRefused(t *testing.T) {
	f := setup(t)
	o, c := f.createWith(t, "encrypted", "pending")
	if err := inTx(t.Context(), f.db, func(tx *sql.Tx) error {
		return Pause(t.Context(), tx, o, c, []byte("vault:v1:staged"), sha256.Sum256([]byte("envelope")))
	}); err != nil {
		t.Fatal(err)
	}
	exec(t, f.db, `UPDATE staging_claim SET lease_until = now() + interval '1 minute' WHERE id = $1`, c.ID)
	if err := Heartbeat(t.Context(), f.db, o, c, timers.Lease); !errors.Is(err, ErrFenced) {
		t.Fatalf("heartbeat: %v, want ErrFenced", err)
	}
	for name, fn := range map[string]func(*sql.Tx) error{
		"store": func(tx *sql.Tx) error {
			return StorePayload(t.Context(), tx, o, c, []byte("vault:v1:other"), sha256.Sum256([]byte("other")))
		},
		"hold":    func(tx *sql.Tx) error { return Hold(t.Context(), tx, o, c) },
		"release": func(tx *sql.Tx) error { return Release(t.Context(), tx, o, c) },
		"abandon": func(tx *sql.Tx) error { return Abandon(t.Context(), tx, o, c) },
		"pause":   func(tx *sql.Tx) error { return Pause(t.Context(), tx, o, c, nil, [32]byte{}) },
	} {
		if err := inTx(t.Context(), f.db, fn); !errors.Is(err, ErrFenced) {
			t.Errorf("%s: %v, want ErrFenced", name, err)
		}
	}
	if r := f.row(t, c.ID); r.state != "paused" || string(r.payload) != "vault:v1:staged" {
		t.Fatalf("claim %+v", r)
	}
}

// Compilation §3.4: a paused claim is not taken over, whether or not its lease lapsed.
func TestTakeOverPausedRefused(t *testing.T) {
	f := setup(t)
	o, c := f.createWith(t, "encrypted", "pending")
	if err := inTx(t.Context(), f.db, func(tx *sql.Tx) error {
		return Pause(t.Context(), tx, o, c, []byte("vault:v1:staged"), sha256.Sum256([]byte("envelope")))
	}); err != nil {
		t.Fatal(err)
	}
	taker := Owner{ID: "b/7/start-2", Epoch: o.Epoch}
	err := inTx(t.Context(), f.db, func(tx *sql.Tx) error {
		_, err := TakeOver(t.Context(), tx, taker, timers.Lease, c.ID)
		return err
	})
	if !errors.Is(err, ErrPaused) || !errors.Is(err, ErrNotEligible) {
		t.Fatalf("takeover: %v, want ErrPaused", err)
	}
	if r := f.pauseRow(t, c.ID); r.state != "paused" || r.gen != 1 || r.owner != o.ID {
		t.Fatalf("claim %+v", r)
	}
}

// Compilation §3.5: a paused claim is due at its absolute expiry, not when its ended lease is in
// the past; the sweep then abandons it, clears the payload and fails its operation.
func TestPausedDueAtExpiry(t *testing.T) {
	f := setup(t)
	o, c := f.createWith(t, "encrypted", "pending")
	if err := inTx(t.Context(), f.db, func(tx *sql.Tx) error {
		return Pause(t.Context(), tx, o, c, []byte("vault:v1:staged"), sha256.Sum256([]byte("envelope")))
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := Sweep(t.Context(), f.db); err != nil || n != 0 {
		t.Fatalf("sweep before expiry: %d %v", n, err)
	}
	if s := effective(t, f, c.ID); s != "paused" {
		t.Fatalf("effective state %s before expiry", s)
	}
	exec(t, f.db, `UPDATE staging_claim SET lease_until = now() - interval '2 seconds', expires_at = now() - interval '1 second'
		WHERE id = $1`, c.ID)
	if s := effective(t, f, c.ID); s != "abandoned" {
		t.Fatalf("effective state %s past expiry", s)
	}
	if n, err := Sweep(t.Context(), f.db); err != nil || n != 1 {
		t.Fatalf("sweep past expiry: %d %v", n, err)
	}
	var opState string
	if err := f.db.QueryRow(`SELECT state FROM operation WHERE ingestion = $1`, c.ID).Scan(&opState); err != nil {
		t.Fatal(err)
	}
	if r := f.row(t, c.ID); r.state != "abandoned" || r.payload != nil || r.digest != nil || opState != "failed" {
		t.Fatalf("claim %+v, operation %s", r, opState)
	}
}

// The schema (compilation §3.1, §3.6): review only under encrypted staging, a paused claim holds
// its payload and a pending review, and a paused claim keeps its key live.
func TestPauseSchema(t *testing.T) {
	f := setup(t)
	for name, q := range map[string]string{
		"review on transient":    `UPDATE staging_claim SET review = 'pending' WHERE id = $1 AND mode = 'transient'`,
		"paused without payload": `UPDATE staging_claim SET state = 'paused', review = 'pending' WHERE id = $1 AND mode = 'encrypted'`,
		"paused without review": `UPDATE staging_claim SET state = 'paused', payload = '\x01', payload_digest = sha256('\x01')
			WHERE id = $1 AND mode = 'encrypted'`,
		"unknown review": `UPDATE staging_claim SET review = 'done' WHERE id = $1`,
	} {
		_, transient := f.create(t, "transient")
		_, encrypted := f.create(t, "encrypted")
		for _, c := range []string{transient.ID, encrypted.ID} {
			res, err := f.db.Exec(q, c)
			if err == nil {
				if n, _ := res.RowsAffected(); n == 1 {
					t.Errorf("%s: accepted", name)
				}
			}
		}
	}
	// Control: a paused claim with its payload and a pending review is accepted.
	_, c := f.createWith(t, "encrypted", "pending")
	exec(t, f.db, `UPDATE staging_claim SET state = 'paused', payload = '\x01', payload_digest = sha256('\x01') WHERE id = $1`, c.ID)
	// A second live claim under the paused claim's key is refused by the live-key index.
	if _, err := f.db.Exec(`INSERT INTO staging_claim (id, mode, state, owner, owner_gen, owner_epoch, lease_until, expires_at,
		principal, idempotency_key, cluster, machine, kind, created_at)
		SELECT 'ing_aaaaaaaaaaaaaaaaaaaaaaaaaa', 'transient', 'held', owner, 1, owner_epoch, lease_until, expires_at + interval '1 hour',
			principal, idempotency_key, cluster, machine, kind, now() FROM staging_claim WHERE id = $1`, c.ID); err == nil {
		t.Fatal("a second live claim under a paused claim's key was accepted")
	}
}

func effective(t *testing.T, f fixture, claim string) string {
	t.Helper()
	var s string
	if err := f.db.QueryRow(`SELECT `+EffectiveStateSQL+` FROM staging_claim WHERE id = $1`, claim).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}
