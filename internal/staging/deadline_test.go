package staging

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// lapseWhileLocked holds c's row lock while its lease and expiry are set to lapse in a second,
// starts run, waits until run is blocked on the lock, checks that run's transaction started
// before both deadlines, keeps holding the lock until the database clock has passed them, then
// lets run proceed without changing the row, and returns run's result.
func (f fixture) lapseWhileLocked(t *testing.T, c Claim, run func(context.Context) error) error {
	t.Helper()
	exec(t, f.db, `UPDATE staging_claim SET lease_until = now() + interval '1 second',
		expires_at = now() + interval '1 second' WHERE id = $1`, c.ID)
	holder, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	if _, err := holder.Exec(`SELECT 1 FROM staging_claim WHERE id = $1 FOR UPDATE`, c.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- run(context.Background()) }()
	pid := waitBlocked(t, f.db)
	// The statement's transaction began while the claim was live: its now() would pass the fence.
	var live bool
	if err := f.db.QueryRow(`SELECT a.xact_start < c.lease_until AND a.xact_start < c.expires_at
		FROM pg_stat_activity a, staging_claim c WHERE a.pid = $1 AND c.id = $2`, pid, c.ID).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if !live {
		t.Fatal("the statement started after the claim lapsed: the test exercises no lock wait across the lapse")
	}
	f.waitLapsed(t, c.ID) // the expiry is the same instant
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	return <-done
}

// waitLapsed waits until the database clock has passed the claim's lease.
func (f fixture) waitLapsed(t *testing.T, claim string) {
	t.Helper()
	for range 300 {
		var lapsed bool
		if err := f.db.QueryRow(`SELECT clock_timestamp() > lease_until FROM staging_claim WHERE id = $1`,
			claim).Scan(&lapsed); err != nil {
			t.Fatal(err)
		}
		if lapsed {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the claim never lapsed")
}

// Compilation §3.5: an owner statement that starts before its claim's lease and expiry pass but
// waits on the claim's row lock until after them is refused. Its deadlines are compared with the
// time it holds the lock, not with its transaction's start: a heartbeat must not revive a claim
// every read already treats as abandoned, and T1 must not release one.
func TestOwnerStatementsRefuseADeadlinePassedInTheLockWait(t *testing.T) {
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
			err := f.lapseWhileLocked(t, c, func(ctx context.Context) error { return run(ctx, f.db, o, c) })
			if !errors.Is(err, ErrFenced) {
				t.Fatalf("%s after its claim lapsed in the lock wait: %v, want ErrFenced", name, err)
			}
			if r := f.row(t, c.ID); r.state != "held" || r.lease.After(r.expires) {
				t.Errorf("the claim was written: %+v", r)
			}
			if got := f.effective(t, c.ID); got != "abandoned" {
				t.Errorf("effective state %s, want abandoned", got)
			}
		})
	}
	// Control: the same wait with live deadlines is not refused.
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
	go func() { done <- Heartbeat(context.Background(), f.db, o, c, timers.Lease) }()
	waitBlocked(t, f.db)
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("control: a heartbeat after a lock wait with live deadlines: %v", err)
	}
}

// The heartbeat extends the lease from the time it holds the lock, not from its start: a lease
// extended after a long lock wait still runs a full lease from then.
func TestHeartbeatExtendsFromTheLockTime(t *testing.T) {
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
	go func() { done <- Heartbeat(context.Background(), f.db, o, c, 2*time.Second) }()
	waitBlocked(t, f.db)
	time.Sleep(time.Second)
	// The heartbeat holds the claim no earlier than this instant, a second after it started.
	var released string
	if err := holder.QueryRow(`SELECT clock_timestamp()::text`).Scan(&released); err != nil {
		t.Fatal(err)
	}
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var full bool
	var short float64
	if err := f.db.QueryRow(`SELECT lease_until >= $2::timestamptz + interval '2 seconds',
		extract(epoch FROM $2::timestamptz + interval '2 seconds' - lease_until) FROM staging_claim WHERE id = $1`,
		c.ID, released).Scan(&full, &short); err != nil {
		t.Fatal(err)
	}
	if !full {
		t.Fatalf("the lease ends %.3fs before a full lease from the lock", short)
	}
}

// Compilation §3.5, the other direction: inside a transaction that started before a claim's
// lease passed, the claim is due once it has passed. A transaction's start time would keep a lapsed
// claim live for as long as the transaction runs.
func TestDueAtTheStatementTime(t *testing.T) {
	f := setup(t)
	_, c := f.create(t, "transient")
	exec(t, f.db, `UPDATE staging_claim SET lease_until = now() + interval '500 milliseconds' WHERE id = $1`, c.ID)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var epoch string
	var live bool
	if err := tx.QueryRow(`SELECT epoch, now() < (SELECT lease_until FROM staging_claim WHERE id = $1)
		FROM installation_state FOR SHARE`, c.ID).Scan(&epoch, &live); err != nil {
		t.Fatal(err)
	}
	if !live {
		t.Fatal("the transaction started after the lapse: the test exercises no transaction older than it")
	}
	f.waitLapsed(t, c.ID)
	var state string
	if err := tx.QueryRow(`SELECT `+EffectiveStateSQL+` FROM staging_claim WHERE id = $1`, c.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "abandoned" {
		t.Errorf("effective state %s in a transaction older than the lapse, want abandoned", state)
	}
	ok, err := AbandonDue(t.Context(), tx, c.ID, epoch)
	if err != nil || !ok {
		t.Fatalf("AbandonDue in a transaction older than the lapse: %v, %v; want written", ok, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.wantAbandoned(t, c.ID, 0)
}
