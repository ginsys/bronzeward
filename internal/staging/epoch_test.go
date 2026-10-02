package staging

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// entryLock takes recovery-mode entry's lock (T9), waiting at most 200ms.
func entryLock(t *testing.T, db *sql.DB) error {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SET LOCAL lock_timeout = '200ms'`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(`SELECT 1 FROM installation_state FOR UPDATE`)
	return err
}

// waitBlocked waits until a statement on staging_claim waits for a lock.
func waitBlocked(t *testing.T, db *sql.DB) {
	t.Helper()
	for range 200 {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock'
			AND query LIKE '%UPDATE staging_claim%' AND pid <> pg_backend_pid()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the owner statement never waited for the claim lock")
}

// An owner statement holds the installation state FOR SHARE from its epoch check to its end
// (persistence-api.md §5.1): while it waits for the claim's lock, recovery-mode entry cannot
// commit a new epoch, so the statement never writes under an epoch that is no longer current.
func TestOwnerStatementsHoldTheEpoch(t *testing.T) {
	for name, run := range map[string]func(context.Context, *sql.DB, Owner, Claim) error{
		"heartbeat": func(ctx context.Context, db *sql.DB, o Owner, c Claim) error {
			return Heartbeat(ctx, db, o, c, timers.Lease)
		},
		"release": func(ctx context.Context, db *sql.DB, o Owner, c Claim) error {
			return inTx(ctx, db, func(tx *sql.Tx) error { return Release(ctx, tx, o, c) })
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			o, c := f.create(t, "transient")
			holder, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			if _, err := holder.Exec(`SELECT 1 FROM staging_claim WHERE id = $1 FOR UPDATE`, c.ID); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- run(context.Background(), f.db, o, c) }()
			waitBlocked(t, f.db)
			var pg *pgconn.PgError
			if err := entryLock(t, f.db); !errors.As(err, &pg) || pg.Code != "55P03" {
				t.Fatalf("recovery-mode entry while an owner statement waited: %v, want a lock timeout", err)
			}
			if err := holder.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatalf("the owner statement under the still-current epoch: %v", err)
			}
			if err := entryLock(t, f.db); err != nil {
				t.Fatalf("recovery-mode entry after the owner statement ended: %v", err)
			}
		})
	}
}

// The sweep holds the installation state FOR SHARE from before the claim's lock to its commit, so
// its terminal event carries the epoch that is current when it commits (persistence-api §8: an
// event id is `<epoch>:<number>`).
func TestSweepHoldsTheEpoch(t *testing.T) {
	f := setup(t)
	_, c := f.create(t, "transient")
	exec(t, f.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, c.ID)
	holder, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	if _, err := holder.Exec(`SELECT 1 FROM staging_claim WHERE id = $1 FOR UPDATE`, c.ID); err != nil {
		t.Fatal(err)
	}
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, err := Sweep(context.Background(), f.db)
		done <- result{n, err}
	}()
	waitBlocked(t, f.db)
	var pg *pgconn.PgError
	if err := entryLock(t, f.db); !errors.As(err, &pg) || pg.Code != "55P03" {
		t.Fatalf("recovery-mode entry while the sweep waited: %v, want a lock timeout", err)
	}
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	if r := <-done; r.err != nil || r.n != 1 {
		t.Fatalf("sweep %d, %v", r.n, r.err)
	}
	f.wantAbandoned(t, c.ID, 0)
	if err := entryLock(t, f.db); err != nil {
		t.Fatalf("recovery-mode entry after the sweep committed: %v", err)
	}
}

// A refused owner statement whose epoch read fails reports that failure, not a fence refusal.
func TestRefusedReportsTheEpochRead(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := refused(ctx, f.db, Owner{ID: "a/1/x", Epoch: currentEpoch(t, f.db)}, 0)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrFenced) || errors.Is(err, ErrEpochSuperseded) {
		t.Fatalf("refused with a failed epoch read: %v", err)
	}
}
