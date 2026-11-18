package staging

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// Compilation §3.6 item 4, persistence-api T11: a continuation takes a paused claim as a mark
// does and records the review as continued: the serving instance becomes its owner at the next
// generation with a new lease, the state held, and the running ingest operation moves with it.
// The pausing run stays fenced out, and the run does not pause the claim again.
func TestContinue(t *testing.T) {
	f := setup(t)
	o, c := f.paused(t)
	b := f.taker(t)
	var tk Taken
	err := inTx(t.Context(), f.db, func(tx *sql.Tx) error {
		var err error
		tk, err = Continue(t.Context(), tx, b, timers.Lease, c.ID, f.human)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := Claim{ID: c.ID, Kind: "import", Mode: "encrypted", Cluster: f.cluster, Machine: f.machine, Review: "continued", Gen: 2}
	if tk.Claim != want || string(tk.Payload) != string(sealed) || tk.Digest != digest {
		t.Fatalf("taken %+v; want %+v with the payload", tk, want)
	}
	r := f.pauseRow(t, c.ID)
	if r.state != "held" || r.gen != 2 || r.owner != b.ID || r.review.String != "continued" ||
		time.Until(r.lease) < timers.Lease-5*time.Second || string(r.payload) != string(sealed) {
		t.Errorf("claim after the continuation: %+v", r)
	}
	if op := f.opOwner(t, c.ID); op.state != "running" || op.owner != b.ID || op.gen != 2 || op.epoch != b.Epoch ||
		!op.lease.Equal(r.lease) {
		t.Errorf("operation after the continuation: %+v (claim lease %v)", op, r.lease)
	}
	if got := f.continuedBy(t, c.ID); got != f.human {
		t.Errorf("continued_by %q; want the continuing principal %q", got, f.human)
	}
	if err := Heartbeat(t.Context(), f.db, o, c, timers.Lease); !errors.Is(err, ErrFenced) {
		t.Errorf("the pausing run's heartbeat: %v", err)
	}
	// A continued review is not paused again: only a pending one is (compilation §3.6 item 1).
	err = inTx(t.Context(), f.db, func(tx *sql.Tx) error { return Pause(t.Context(), tx, b, tk.Claim, nil, [32]byte{}) })
	if err == nil {
		t.Error("a continued claim was paused again")
	}
}

// Compilation §3.6 item 4: a continuation on a claim not paused or past its absolute expiry is
// refused with its reason, and nothing changes.
func TestContinueRefusals(t *testing.T) {
	f := setup(t)
	b := f.taker(t)
	_, hc := f.createWith(t, "encrypted", "pending")
	if _, err := f.continueWith(t, b, hc.ID); !errors.Is(err, ErrNotPaused) {
		t.Errorf("continuation of a held claim: %v, want ErrNotPaused", err)
	}
	if r := f.pauseRow(t, hc.ID); r.state != "held" || r.gen != 1 || r.review.String != "pending" {
		t.Errorf("held claim written: %+v", r)
	}
	_, pc := f.paused(t)
	exec(t, f.db, `UPDATE staging_claim SET expires_at = now() - interval '1 second' WHERE id = $1`, pc.ID)
	if _, err := f.continueWith(t, b, pc.ID); !errors.Is(err, ErrEnded) {
		t.Errorf("continuation past the absolute expiry: %v, want ErrEnded", err)
	}
	if r := f.pauseRow(t, pc.ID); r.state != "paused" || r.gen != 1 || r.review.String != "pending" {
		t.Errorf("expired claim written: %+v", r)
	}
}

// Compilation §3.5, §3.6 item 4: a continuation that waits on the claim's row lock until after
// its absolute expiry is refused: the expiry is compared with the time it holds the lock.
func TestContinueRefusesAnExpiryPassedInTheLockWait(t *testing.T) {
	f := setup(t)
	_, c := f.paused(t)
	b := f.taker(t)
	err := f.lapseWhileLocked(t, c, func(ctx context.Context) error {
		return inTx(ctx, f.db, func(tx *sql.Tx) error {
			_, err := Continue(ctx, tx, b, timers.Lease, c.ID, f.human)
			return err
		})
	})
	if !errors.Is(err, ErrEnded) {
		t.Fatalf("a continuation after the expiry passed in the lock wait: %v, want ErrEnded", err)
	}
	if r := f.pauseRow(t, c.ID); r.state != "paused" || r.gen != 1 || r.review.String != "pending" {
		t.Errorf("the claim was written: %+v", r)
	}
}

// continuedBy is the principal a claim records as having continued its review, "" for none.
func (f fixture) continuedBy(t *testing.T, claim string) string {
	t.Helper()
	var by sql.NullString
	if err := f.db.QueryRow(`SELECT continued_by FROM staging_claim WHERE id = $1`, claim).Scan(&by); err != nil {
		t.Fatal(err)
	}
	return by.String
}

// Owner's decision of ginsys/bronzeward#858: only a continuation records who continued, and a
// mark, which leaves the review pending, records none. A takeover of the continued claim keeps the
// record, and a continuation that names no principal is refused with nothing written.
func TestContinuedByIsRecordedOnlyByContinue(t *testing.T) {
	f := setup(t)
	b := f.taker(t)
	_, mc := f.paused(t)
	if err := inTx(t.Context(), f.db, func(tx *sql.Tx) error {
		_, err := Mark(t.Context(), tx, b, timers.Lease, mc.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := f.continuedBy(t, mc.ID); got != "" {
		t.Errorf("a mark recorded continued_by %q", got)
	}

	_, nc := f.paused(t)
	err := inTx(t.Context(), f.db, func(tx *sql.Tx) error {
		_, err := Continue(t.Context(), tx, b, timers.Lease, nc.ID, "")
		return err
	})
	if err == nil {
		t.Fatal("a continuation naming no principal was taken")
	}
	if r := f.pauseRow(t, nc.ID); r.state != "paused" || r.gen != 1 || r.review.String != "pending" || f.continuedBy(t, nc.ID) != "" {
		t.Errorf("the claim was written by the refused continuation: %+v", r)
	}

	if _, err := f.continueWith(t, b, nc.ID); err != nil {
		t.Fatal(err)
	}
	f.lapse(t, nc.ID)
	if _, err := f.take(t, Owner{ID: "c/9/start-3", Epoch: b.Epoch}, nc.ID); err != nil {
		t.Fatal(err)
	}
	if r := f.pauseRow(t, nc.ID); r.state != "resumed" || r.review.String != "continued" || f.continuedBy(t, nc.ID) != f.human {
		t.Errorf("after the takeover: %+v, continued_by %q", r, f.continuedBy(t, nc.ID))
	}
}

// The schema holds continued_by to a continued review: a pending one, or none, cannot carry it.
func TestContinuedByNeedsAContinuedReview(t *testing.T) {
	f := setup(t)
	_, pending := f.paused(t)
	if _, err := f.db.Exec(`UPDATE staging_claim SET continued_by = $2 WHERE id = $1`, pending.ID, f.human); err == nil {
		t.Error("a pending review took a continued_by")
	}
	if got := f.continuedBy(t, pending.ID); got != "" {
		t.Errorf("continued_by %q after the refused update", got)
	}
	if _, err := f.continueWith(t, f.taker(t), pending.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE staging_claim SET continued_by = NULL WHERE id = $1`, pending.ID); err != nil {
		t.Errorf("a continued review refused a null continued_by: %v", err)
	}
	exec(t, f.db, `UPDATE staging_claim SET continued_by = $2 WHERE id = $1`, pending.ID, f.human)
	if _, err := f.db.Exec(`UPDATE staging_claim SET review = 'pending' WHERE id = $1`, pending.ID); err == nil {
		t.Error("a claim with a continued_by went back to a pending review")
	}
	// A null review is no continued review either: the check reads NULL as passing unless it is
	// written against the null.
	if _, err := f.db.Exec(`UPDATE staging_claim SET review = NULL WHERE id = $1`, pending.ID); err == nil {
		t.Error("a claim with a continued_by lost its review")
	}
}

// continueWith is Continue in a transaction of its own.
func (f fixture) continueWith(t *testing.T, o Owner, claim string) (Taken, error) {
	t.Helper()
	var tk Taken
	err := inTx(t.Context(), f.db, func(tx *sql.Tx) error {
		var err error
		tk, err = Continue(t.Context(), tx, o, timers.Lease, claim, f.human)
		return err
	})
	return tk, err
}
