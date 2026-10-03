package staging

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

var (
	sealed = []byte("vault:v1:ZW52ZWxvcGU=")
	digest = sha256.Sum256([]byte("envelope"))
)

// taker is a second ingestion process: another owner string, the current epoch.
func (f fixture) taker(t *testing.T) Owner {
	t.Helper()
	return Owner{ID: "b/7/start-2", Epoch: currentEpoch(t, f.db)}
}

// staged is an encrypted claim whose envelope was stored and whose lease then lapsed: a taker's
// case (compilation §3.4).
func (f fixture) staged(t *testing.T) (Owner, Claim) {
	t.Helper()
	o, c := f.create(t, "encrypted")
	if err := inTx(t.Context(), f.db, func(tx *sql.Tx) error { return StorePayload(t.Context(), tx, o, c, sealed, digest) }); err != nil {
		t.Fatal(err)
	}
	f.lapse(t, c.ID)
	return o, c
}

func (f fixture) lapse(t *testing.T, claim string) {
	t.Helper()
	exec(t, f.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, claim)
}

// take is TakeOver in a transaction of its own.
func (f fixture) take(t *testing.T, o Owner, claim string) (Taken, error) {
	t.Helper()
	return f.takeWith(t, o, claim, takeoverOptions{})
}

func (f fixture) takeWith(t *testing.T, o Owner, claim string, opts takeoverOptions) (Taken, error) {
	t.Helper()
	var tk Taken
	err := inTx(t.Context(), f.db, func(tx *sql.Tx) error {
		var err error
		tk, err = takeOver(t.Context(), tx, o, timers.Lease, claim, opts)
		return err
	})
	return tk, err
}

// opOwner is the owner fields of a claim's ingest operation.
type opOwner struct {
	state, owner, epoch string
	gen                 int64
	lease               time.Time
}

func (f fixture) opOwner(t *testing.T, claim string) opOwner {
	t.Helper()
	var r opOwner
	var owner, epoch sql.NullString
	var lease sql.NullTime
	if err := f.db.QueryRow(`SELECT state, owner, owner_gen, owner_epoch, lease_until FROM operation WHERE ingestion = $1`,
		claim).Scan(&r.state, &owner, &r.gen, &epoch, &lease); err != nil {
		t.Fatal(err)
	}
	r.owner, r.epoch, r.lease = owner.String, epoch.String, lease.Time
	return r
}

// Compilation §3.4, persistence-api §8.2 (T8): a takeover of a held or a resumed encrypted claim
// after its lease lapsed sets the taker as owner at the next generation, the state resumed and a
// fresh lease, moves the operation's owner fields with the claim's, and hands back the payload.
// The old owner's statements are then refused on their generation.
func TestTakeOver(t *testing.T) {
	f := setup(t)
	o, c := f.staged(t)
	b := f.taker(t)
	tk, err := f.take(t, b, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := Claim{ID: c.ID, Mode: "encrypted", Cluster: f.cluster, Machine: f.machine, Gen: 2}
	if tk.Claim != want || string(tk.Payload) != string(sealed) || tk.Digest != digest {
		t.Fatalf("taken %+v; want %+v with the payload", tk, want)
	}
	r := f.row(t, c.ID)
	if r.state != "resumed" || r.gen != 2 || time.Until(r.lease) < timers.Lease-5*time.Second || r.payload == nil {
		t.Errorf("claim after the takeover: %+v", r)
	}
	if op := f.opOwner(t, c.ID); op.state != "running" || op.owner != b.ID || op.gen != 2 || op.epoch != b.Epoch ||
		!op.lease.Equal(r.lease) {
		t.Errorf("operation after the takeover: %+v (claim lease %v)", op, r.lease)
	}
	// The old owner: heartbeat, payload, hold, release and abandonment all refused on its generation.
	if err := Heartbeat(t.Context(), f.db, o, c, timers.Lease); !errors.Is(err, ErrFenced) {
		t.Errorf("old owner heartbeat: %v", err)
	}
	for name, stmt := range map[string]func(*sql.Tx) error{
		"StorePayload": func(tx *sql.Tx) error { return StorePayload(t.Context(), tx, o, c, sealed, digest) },
		"Hold":         func(tx *sql.Tx) error { return Hold(t.Context(), tx, o, c) },
		"Release":      func(tx *sql.Tx) error { return Release(t.Context(), tx, o, c) },
		"Abandon":      func(tx *sql.Tx) error { return Abandon(t.Context(), tx, o, c) },
	} {
		if err := inTx(t.Context(), f.db, stmt); !errors.Is(err, ErrFenced) {
			t.Errorf("old owner %s: %v", name, err)
		}
	}
	// The taker holds it: its heartbeat passes.
	if err := Heartbeat(t.Context(), f.db, b, tk.Claim, timers.Lease); err != nil {
		t.Errorf("taker heartbeat: %v", err)
	}
	// A resumed claim whose lease lapsed is taken over again (§3.4: both states), by the same
	// owner string too: there is no owner-inequality term. Its fresh lease stops at the absolute
	// expiry.
	f.lapse(t, c.ID)
	exec(t, f.db, `UPDATE staging_claim SET expires_at = now() + interval '5 seconds' WHERE id = $1`, c.ID)
	again, err := f.take(t, b, c.ID)
	if err != nil || again.Claim.Gen != 3 {
		t.Fatalf("second takeover: %+v, %v", again, err)
	}
	if r := f.row(t, c.ID); !r.lease.Equal(r.expires) {
		t.Errorf("lease %v past the absolute expiry %v", r.lease, r.expires)
	}
	if err := Heartbeat(t.Context(), f.db, b, tk.Claim, timers.Lease); !errors.Is(err, ErrFenced) {
		t.Errorf("the first taking generation after the second takeover: %v", err)
	}
	if op := f.opOwner(t, c.ID); op.gen != 3 {
		t.Errorf("operation generation %d; want 3", op.gen)
	}
}

// Compilation §3.4: each precondition refuses on its own, naming its reason, and the claim and its
// operation are left as they were.
func TestTakeOverRefusals(t *testing.T) {
	f := setup(t)
	for _, tc := range []struct {
		name  string
		mode  string
		tweak func(t *testing.T, c Claim, b *Owner)
		want  error
	}{
		{"a live lease", "encrypted", func(t *testing.T, c Claim, _ *Owner) {
			exec(t, f.db, `UPDATE staging_claim SET lease_until = now() + interval '1 minute' WHERE id = $1`, c.ID)
		}, ErrLeaseLive},
		{"a transient claim", "transient", func(t *testing.T, c Claim, _ *Owner) {
			// Lapsed but not yet swept: a transient claim is never taken over.
			exec(t, f.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, c.ID)
		}, ErrNotEncrypted},
		{"past its absolute expiry", "encrypted", func(t *testing.T, c Claim, _ *Owner) {
			exec(t, f.db, `UPDATE staging_claim SET expires_at = now() - interval '1 second' WHERE id = $1`, c.ID)
		}, ErrEnded},
		{"released", "encrypted", func(t *testing.T, c Claim, _ *Owner) {
			exec(t, f.db, `UPDATE staging_claim SET state = 'released', payload = NULL, payload_digest = NULL WHERE id = $1`, c.ID)
		}, ErrEnded},
		{"abandoned", "encrypted", func(t *testing.T, c Claim, _ *Owner) {
			exec(t, f.db, `UPDATE staging_claim SET state = 'abandoned', payload = NULL, payload_digest = NULL WHERE id = $1`, c.ID)
		}, ErrEnded},
		{"a claim of an earlier epoch", "encrypted", func(t *testing.T, c Claim, b *Owner) {
			newEpoch(t, f.db)
			b.Epoch = currentEpoch(t, f.db)
		}, ErrClaimEpoch},
		{"a taker of a superseded epoch", "encrypted", func(t *testing.T, c Claim, _ *Owner) {
			newEpoch(t, f.db)
		}, ErrEpochSuperseded},
		{"a taker of a superseded epoch, the claim of the current one", "encrypted", func(t *testing.T, c Claim, _ *Owner) {
			newEpoch(t, f.db)
			exec(t, f.db, `UPDATE staging_claim SET owner_epoch = (SELECT epoch FROM installation_state) WHERE id = $1`, c.ID)
		}, ErrEpochSuperseded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var c Claim
			if tc.mode == "encrypted" {
				_, c = f.staged(t)
			} else {
				_, c = f.create(t, tc.mode)
			}
			b := f.taker(t)
			tc.tweak(t, c, &b)
			before, beforeOp := f.row(t, c.ID), f.opOwner(t, c.ID)
			_, err := f.take(t, b, c.ID)
			if !errors.Is(err, tc.want) {
				t.Fatalf("%v; want %v", err, tc.want)
			}
			if tc.want != ErrEpochSuperseded && !errors.Is(err, ErrNotEligible) {
				t.Errorf("%v does not wrap ErrNotEligible", err)
			}
			if after := f.row(t, c.ID); after.state != before.state || after.gen != before.gen || !after.lease.Equal(before.lease) {
				t.Errorf("the claim changed: %+v -> %+v", before, after)
			}
			if after := f.opOwner(t, c.ID); after != beforeOp {
				t.Errorf("the operation changed: %+v -> %+v", beforeOp, after)
			}
		})
	}
	if _, err := f.take(t, f.taker(t), id.New(id.Ingestion)); !errors.Is(err, ErrNoClaim) {
		t.Errorf("no such claim: %v", err)
	}
}

// Persistence-api §8.2 (T8): the operation's owner fields move only from the generation the
// takeover moved the claim from. An operation at another generation refuses the takeover whole.
func TestTakeOverMovesOnlyTheClaimsOperation(t *testing.T) {
	f := setup(t)
	_, c := f.staged(t)
	exec(t, f.db, `UPDATE operation SET owner_gen = 7 WHERE ingestion = $1`, c.ID)
	if _, err := f.take(t, f.taker(t), c.ID); err == nil {
		t.Fatal("taken over with its operation at another generation")
	}
	if r := f.row(t, c.ID); r.state != "held" || r.gen != 1 {
		t.Errorf("claim %+v; want it as it was", r)
	}
}

// Compilation §3.4: a claim whose payload was never written has nothing to decrypt; the takeover
// abandons it under the new generation and reports so. The caller fails its operation.
func TestTakeOverNothingToDecrypt(t *testing.T) {
	f := setup(t)
	_, c := f.create(t, "encrypted")
	f.lapse(t, c.ID)
	b := f.taker(t)
	tk, err := f.take(t, b, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Payload != nil || tk.Claim.Gen != 2 {
		t.Fatalf("taken %+v; want no payload at generation 2", tk)
	}
	if r := f.row(t, c.ID); r.state != "abandoned" || r.gen != 2 || r.payload != nil || r.digest != nil {
		t.Errorf("claim %+v", r)
	}
	if op := f.opOwner(t, c.ID); op.state != "running" || op.owner != b.ID || op.gen != 2 {
		t.Errorf("operation %+v; want it running under the taker for the caller to fail", op)
	}

	// Eligibility is settled by the takeover's write under the lock: a payload-less claim whose
	// absolute expiry passes after that write is still abandoned, never refused by a later fence.
	_, c = f.create(t, "encrypted")
	f.lapse(t, c.ID)
	exec(t, f.db, `UPDATE staging_claim SET expires_at = clock_timestamp() + interval '1 second' WHERE id = $1`, c.ID)
	var expires time.Time
	if err := f.db.QueryRow(`SELECT expires_at FROM staging_claim WHERE id = $1`, c.ID).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	expired := false
	tk, err = f.takeWith(t, b, c.ID, takeoverOptions{afterWrite: func() {
		time.Sleep(time.Until(expires) + 100*time.Millisecond)
		expired = true
	}})
	if !expired {
		t.Fatalf("the takeover never wrote the claim: %v", err)
	}
	if err != nil || tk.Payload != nil || tk.Claim.Gen != 2 {
		t.Fatalf("taken %+v, %v; want it abandoned at generation 2", tk, err)
	}
	if r := f.row(t, c.ID); r.state != "abandoned" || r.gen != 2 || r.payload != nil || r.digest != nil {
		t.Errorf("claim expiring after the write %+v", r)
	}
}

// Persistence-api §5.1 and DB row 020: the takeover's eligibility is its UPDATE's own predicate,
// evaluated at the current time after the claim is locked, not a read before it. A claim whose
// absolute expiry passes between the lock and the write is refused; the control decides by a read
// before the wait and resumes it past its expiry.
func TestTakeOverRechecksEligibility(t *testing.T) {
	f := setup(t)
	expiring := func(t *testing.T) Claim {
		_, c := f.staged(t)
		exec(t, f.db, `UPDATE staging_claim SET expires_at = clock_timestamp() + interval '300 milliseconds' WHERE id = $1`, c.ID)
		return c
	}
	wait := func() { time.Sleep(500 * time.Millisecond) }

	c := expiring(t)
	if _, err := f.takeWith(t, f.taker(t), c.ID, takeoverOptions{afterLock: wait}); !errors.Is(err, ErrEnded) {
		t.Errorf("expired while it waited: %v; want ErrEnded", err)
	}
	if r := f.row(t, c.ID); r.state != "held" || r.gen != 1 {
		t.Errorf("claim %+v", r)
	}

	c = expiring(t)
	if _, err := f.takeWith(t, f.taker(t), c.ID, takeoverOptions{afterLock: wait, noRecheck: true}); err != nil {
		t.Fatalf("control: %v", err)
	}
	if r := f.row(t, c.ID); r.state != "resumed" || !r.expires.Before(time.Now()) {
		t.Fatalf("control: claim %+v; the re-check is not what refuses", r)
	}
}

// Compilation §3.4: two takers of one lapsed claim: exactly one wins and the generation goes up by
// exactly one; the other is refused on the winner's live lease.
func TestTakeOverConcurrentTakers(t *testing.T) {
	f := setup(t)
	_, c := f.staged(t)
	winner := Owner{ID: "c/9/start-3", Epoch: currentEpoch(t, f.db)}
	locked := make(chan struct{})
	first := make(chan error, 1)
	go func() {
		_, err := f.takeWith(t, winner, c.ID, takeoverOptions{afterLock: func() { close(locked); time.Sleep(200 * time.Millisecond) }})
		first <- err
	}()
	<-locked
	_, second := f.take(t, f.taker(t), c.ID)
	if err := <-first; err != nil {
		t.Fatalf("first taker: %v", err)
	}
	if !errors.Is(second, ErrLeaseLive) {
		t.Errorf("second taker: %v; want ErrLeaseLive", second)
	}
	if r := f.row(t, c.ID); r.gen != 2 {
		t.Errorf("generation %d; want 2", r.gen)
	}
	if op := f.opOwner(t, c.ID); op.owner != winner.ID || op.gen != 2 {
		t.Errorf("operation %+v; want the first taker's", op)
	}
}
