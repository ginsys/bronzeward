package staging

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// paused is an encrypted claim with a pending review, paused by its run with its envelope
// stored: a mark's case (compilation §3.6 item 3).
func (f fixture) paused(t *testing.T) (Owner, Claim) {
	t.Helper()
	o, c := f.createWith(t, "encrypted", "pending")
	if err := inTx(t.Context(), f.db, func(tx *sql.Tx) error { return Pause(t.Context(), tx, o, c, sealed, digest) }); err != nil {
		t.Fatal(err)
	}
	return o, c
}

// markWith is mark in a transaction of its own.
func (f fixture) markWith(t *testing.T, o Owner, claim string, opts takeoverOptions) (Taken, error) {
	t.Helper()
	var tk Taken
	err := inTx(t.Context(), f.db, func(tx *sql.Tx) error {
		var err error
		tk, err = mark(t.Context(), tx, o, timers.Lease, claim, "pending", opts)
		return err
	})
	return tk, err
}

// Compilation §3.6 item 3, persistence-api T11: a mark takes a paused claim: the serving
// instance becomes its owner at the next generation with a new lease, the state held, the
// review still pending, and the claim's running ingest operation moves with it. The payload
// and digest come back for the run to decrypt. The pausing run stays fenced out.
func TestMark(t *testing.T) {
	f := setup(t)
	o, c := f.paused(t)
	b := f.taker(t)
	var tk Taken
	err := inTx(t.Context(), f.db, func(tx *sql.Tx) error {
		var err error
		tk, err = Mark(t.Context(), tx, b, timers.Lease, c.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := Claim{ID: c.ID, Kind: "import", Mode: "encrypted", Cluster: f.cluster, Machine: f.machine, Review: "pending", Gen: 2}
	if tk.Claim != want || string(tk.Payload) != string(sealed) || tk.Digest != digest {
		t.Fatalf("taken %+v; want %+v with the payload", tk, want)
	}
	r := f.pauseRow(t, c.ID)
	if r.state != "held" || r.gen != 2 || r.owner != b.ID || r.review.String != "pending" ||
		time.Until(r.lease) < timers.Lease-5*time.Second || string(r.payload) != string(sealed) {
		t.Errorf("claim after the mark: %+v", r)
	}
	if op := f.opOwner(t, c.ID); op.state != "running" || op.owner != b.ID || op.gen != 2 || op.epoch != b.Epoch ||
		!op.lease.Equal(r.lease) {
		t.Errorf("operation after the mark: %+v (claim lease %v)", op, r.lease)
	}
	if err := Heartbeat(t.Context(), f.db, o, c, timers.Lease); !errors.Is(err, ErrFenced) {
		t.Errorf("the pausing run's heartbeat: %v", err)
	}
	if err := Heartbeat(t.Context(), f.db, b, tk.Claim, timers.Lease); err != nil {
		t.Errorf("the marking run's heartbeat: %v", err)
	}
	// The marking run pauses the claim again, as item 1 does.
	if err := inTx(t.Context(), f.db, func(tx *sql.Tx) error { return Pause(t.Context(), tx, b, tk.Claim, nil, [32]byte{}) }); err != nil {
		t.Fatalf("pause after the mark: %v", err)
	}
	// Its new lease stops at the absolute expiry.
	exec(t, f.db, `UPDATE staging_claim SET expires_at = now() + interval '5 seconds' WHERE id = $1`, c.ID)
	again, err := f.markWith(t, b, c.ID, takeoverOptions{})
	if err != nil || again.Claim.Gen != 3 {
		t.Fatalf("second mark: %+v, %v", again, err)
	}
	if r := f.row(t, c.ID); !r.lease.Equal(r.expires) {
		t.Errorf("lease %v past the absolute expiry %v", r.lease, r.expires)
	}
}

// Compilation §3.6 item 3: a mark on a claim not paused, past its absolute expiry or of an
// earlier epoch is refused with its reason, and the claim and its operation are left as they
// were.
func TestMarkRefusals(t *testing.T) {
	f := setup(t)
	for _, tc := range []struct {
		name  string
		claim func(t *testing.T) Claim
		tweak func(t *testing.T, c Claim, b *Owner)
		want  error
	}{
		{"a held claim with a live lease", func(t *testing.T) Claim { _, c := f.createWith(t, "encrypted", "pending"); return c },
			func(*testing.T, Claim, *Owner) {}, ErrNotPaused},
		{"a held claim whose lease lapsed", func(t *testing.T) Claim { _, c := f.staged(t); return c },
			func(*testing.T, Claim, *Owner) {}, ErrNotPaused},
		{"a transient claim", func(t *testing.T) Claim { _, c := f.create(t, "transient"); return c },
			func(*testing.T, Claim, *Owner) {}, ErrNotPaused},
		{"a paused claim past its absolute expiry", func(t *testing.T) Claim { _, c := f.paused(t); return c },
			func(t *testing.T, c Claim, _ *Owner) {
				exec(t, f.db, `UPDATE staging_claim SET expires_at = now() - interval '1 second' WHERE id = $1`, c.ID)
			}, ErrEnded},
		{"a released claim", func(t *testing.T) Claim { _, c := f.paused(t); return c },
			func(t *testing.T, c Claim, _ *Owner) {
				exec(t, f.db, `UPDATE staging_claim SET state = 'released', payload = NULL, payload_digest = NULL WHERE id = $1`, c.ID)
			}, ErrEnded},
		{"an abandoned claim", func(t *testing.T) Claim { _, c := f.paused(t); return c },
			func(t *testing.T, c Claim, _ *Owner) {
				exec(t, f.db, `UPDATE staging_claim SET state = 'abandoned', payload = NULL, payload_digest = NULL WHERE id = $1`, c.ID)
			}, ErrEnded},
		{"a paused claim of an earlier epoch", func(t *testing.T) Claim { _, c := f.paused(t); return c },
			func(t *testing.T, _ Claim, b *Owner) {
				newEpoch(t, f.db)
				b.Epoch = currentEpoch(t, f.db)
			}, ErrClaimEpoch},
		{"a marking process of a superseded epoch", func(t *testing.T) Claim { _, c := f.paused(t); return c },
			func(t *testing.T, _ Claim, _ *Owner) { newEpoch(t, f.db) }, ErrEpochSuperseded},
		{"a marking process of a superseded epoch, the claim of the current one", func(t *testing.T) Claim { _, c := f.paused(t); return c },
			func(t *testing.T, c Claim, _ *Owner) {
				newEpoch(t, f.db)
				exec(t, f.db, `UPDATE staging_claim SET owner_epoch = (SELECT epoch FROM installation_state) WHERE id = $1`, c.ID)
			}, ErrEpochSuperseded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.claim(t)
			b := f.taker(t)
			tc.tweak(t, c, &b)
			before, beforeOp := f.pauseRow(t, c.ID), f.opOwner(t, c.ID)
			_, err := f.markWith(t, b, c.ID, takeoverOptions{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("%v; want %v", err, tc.want)
			}
			if after := f.pauseRow(t, c.ID); after.state != before.state || after.gen != before.gen || after.owner != before.owner ||
				!after.lease.Equal(before.lease) {
				t.Errorf("the claim changed: %+v -> %+v", before, after)
			}
			if after := f.opOwner(t, c.ID); after != beforeOp {
				t.Errorf("the operation changed: %+v -> %+v", beforeOp, after)
			}
		})
	}
	if _, err := f.markWith(t, f.taker(t), id.New(id.Ingestion), takeoverOptions{}); !errors.Is(err, ErrNoClaim) {
		t.Errorf("no such claim: %v", err)
	}
}

// PA §16: two marks racing one paused claim, exactly one taking it; the other is refused on the
// claim the winner holds.
func TestMarkConcurrent(t *testing.T) {
	f := setup(t)
	_, c := f.paused(t)
	winner := Owner{ID: "c/9/start-3", Epoch: currentEpoch(t, f.db)}
	locked := make(chan struct{})
	first := make(chan error, 1)
	go func() {
		_, err := f.markWith(t, winner, c.ID, takeoverOptions{afterLock: func() { close(locked); time.Sleep(200 * time.Millisecond) }})
		first <- err
	}()
	<-locked
	_, second := f.markWith(t, f.taker(t), c.ID, takeoverOptions{})
	if err := <-first; err != nil {
		t.Fatalf("first mark: %v", err)
	}
	if !errors.Is(second, ErrNotPaused) {
		t.Errorf("second mark: %v; want ErrNotPaused", second)
	}
	if r := f.row(t, c.ID); r.gen != 2 {
		t.Errorf("generation %d; want 2", r.gen)
	}
	if op := f.opOwner(t, c.ID); op.owner != winner.ID || op.gen != 2 {
		t.Errorf("operation %+v; want the first mark's", op)
	}
}

// Compilation §3.5, §3.6 item 3: a mark that starts before a paused claim's absolute expiry but
// waits on its row lock until after it is refused: the expiry is compared with the time the mark
// holds the lock.
func TestMarkRefusesAnExpiryPassedInTheLockWait(t *testing.T) {
	f := setup(t)
	_, c := f.paused(t)
	b := f.taker(t)
	err := f.lapseWhileLocked(t, c, func(ctx context.Context) error {
		return inTx(ctx, f.db, func(tx *sql.Tx) error {
			_, err := Mark(ctx, tx, b, timers.Lease, c.ID)
			return err
		})
	})
	if !errors.Is(err, ErrEnded) {
		t.Fatalf("a mark after the expiry passed in the lock wait: %v, want ErrEnded", err)
	}
	if r := f.pauseRow(t, c.ID); r.state != "paused" || r.gen != 1 {
		t.Errorf("the claim was written: %+v", r)
	}
}
