// Package staging holds a staging claim's statements (compilation.md §3): its creation, the
// owner's heartbeat, the encrypted payload, release and abandonment. Every statement after
// creation locks the claim's row, then is one conditional UPDATE whose predicate is the owner
// fence (persistence-api.md §5.1): no read decides a write, and a read after a refused write only
// names the reason.
package staging

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Owner is the process holding claims: its owner string "<instance>/<pid>/<start token>" and
// the epoch it read at its start.
type Owner struct {
	ID    string
	Epoch string
}

// Timers are the claim's lease and absolute expiry (compilation §3.2), from the configuration.
type Timers struct{ Lease, AbsoluteExpiry time.Duration }

// Claim is one claim as its owner knows it. Kind is import, with its Machine, or draft-update,
// with its Draft (persistence-api §9.3).
type Claim struct {
	ID, Kind, Mode, Cluster, Machine, Draft string
	Gen                                     int64
}

var (
	// ErrFenced refuses an owner statement on a claim that is not this owner generation's, not
	// live, or whose lease or absolute expiry has passed.
	ErrFenced = errors.New("staging: the claim is not this owner's, or its lease or expiry has passed")
	// ErrEpochSuperseded refuses a statement from a process whose epoch is no longer current.
	ErrEpochSuperseded = errors.New("staging: this process's epoch is not current")
)

// fence is the owner predicate every statement after creation carries, over $1 claim, $2 owner,
// $3 owner generation and $4 owner epoch, after lock. It compares the deadlines with the current
// time, not the transaction's start (compilation §3.5): a claim that lapsed while its transaction
// ran is no longer this owner's.
const fence = `id = $1 AND owner = $2 AND owner_gen = $3 AND owner_epoch = $4
	AND $4 = (SELECT epoch FROM installation_state FOR SHARE)
	AND state IN ('held', 'resumed') AND lease_until > clock_timestamp() AND expires_at > clock_timestamp()`

// lock takes the installation state FOR SHARE, then the claim's row FOR UPDATE, in the caller's
// transaction and the order every claim transaction takes them. The installation state is held
// to the end of the transaction (persistence-api §5.1): recovery-mode entry cannot commit a new
// epoch while the statement waits for the claim or its transaction commits. The fence runs after
// any wait for the claim: an UPDATE evaluates its predicate before it waits for a row and does not
// evaluate it again when the holder ends without changing the row, so a deadline that passed in
// the wait would go unseen.
func lock(ctx context.Context, tx *sql.Tx, claim string) error {
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM installation_state FOR SHARE`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `SELECT 1 FROM staging_claim WHERE id = $1 FOR UPDATE`, claim)
	return err
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Create inserts c held at generation 1 for o, with a lease of t.Lease capped at the absolute
// expiry t.AbsoluteExpiry from now (T11's claim half), in the caller's transaction. The INSERT
// selects from installation_state, so a process whose epoch is not current creates nothing.
func Create(ctx context.Context, tx *sql.Tx, o Owner, t Timers, c Claim, principal, key string) error {
	if o.ID == "" || o.Epoch == "" || c.Gen != 1 {
		return errors.New("staging: a claim is created by an owner with its epoch, at generation 1")
	}
	if t.Lease <= 0 || t.Lease >= t.AbsoluteExpiry {
		return errors.New("staging: the lease must be positive and shorter than the absolute expiry")
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO staging_claim (id, mode, state, owner, owner_gen, owner_epoch, lease_until,
		expires_at, principal, idempotency_key, cluster, machine, draft, kind, created_at)
		SELECT $1, $2, 'held', $3, 1, $4, now() + $5::bigint * interval '1 microsecond',
		now() + $6::bigint * interval '1 microsecond', $7, $8, $9, NULLIF($10, ''), NULLIF($11, ''), $12, now()
		FROM installation_state WHERE epoch = $4`,
		c.ID, c.Mode, o.ID, o.Epoch, t.Lease.Microseconds(), t.AbsoluteExpiry.Microseconds(), principal, key, c.Cluster, c.Machine,
		c.Draft, c.Kind)
	if err != nil {
		return fmt.Errorf("staging: create the claim: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("staging: create the claim: %w", err)
	}
	if n != 1 {
		return ErrEpochSuperseded
	}
	return nil
}

// Heartbeat extends the claim's lease to a lease from when it holds the claim, never past its
// absolute expiry, and its running ingest operation's lease with it, in a transaction of its own.
func Heartbeat(ctx context.Context, db *sql.DB, o Owner, c Claim, lease time.Duration) error {
	if lease <= 0 {
		return errors.New("staging: the lease must be positive")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("staging: heartbeat: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // a no-op once committed
	if err := lock(ctx, tx, c.ID); err != nil {
		return fmt.Errorf("staging: heartbeat: %w", err)
	}
	var n int
	err = tx.QueryRowContext(ctx, `WITH c AS (
		UPDATE staging_claim SET lease_until = least(clock_timestamp() + $5::bigint * interval '1 microsecond', expires_at)
		WHERE `+fence+` RETURNING id, lease_until),
	op AS (
		UPDATE operation SET lease_until = c.lease_until FROM c
		WHERE operation.ingestion = c.id AND operation.state = 'running' AND operation.owner = $2 RETURNING 1)
	SELECT count(*) FROM c`, c.ID, o.ID, c.Gen, o.Epoch, lease.Microseconds()).Scan(&n)
	if err != nil {
		return fmt.Errorf("staging: heartbeat: %w", err)
	}
	if err := refused(ctx, tx, o, n); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("staging: heartbeat: %w", err)
	}
	return nil
}

// StorePayload writes an encrypted claim's envelope ciphertext and its plaintext's SHA-256
// (compilation §3: after step 8). A transient claim never holds one: the schema refuses it
// whatever mode the caller's Claim names. It runs in the caller's transaction.
func StorePayload(ctx context.Context, tx *sql.Tx, o Owner, c Claim, ct []byte, sum [32]byte) error {
	if c.Mode != "encrypted" || len(ct) == 0 {
		return errors.New("staging: only an encrypted claim holds a payload, and it is not empty")
	}
	if err := lock(ctx, tx, c.ID); err != nil {
		return fmt.Errorf("staging: store the payload: %w", err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE staging_claim SET payload = $5, payload_digest = $6
		WHERE `+fence, c.ID, o.ID, c.Gen, o.Epoch, ct, sum[:])
	if err != nil {
		return fmt.Errorf("staging: store the payload: %w", err)
	}
	return affected(ctx, tx, o, res)
}

// Hold confirms the claim is still this owner's, in the caller's transaction, and keeps it
// locked to the transaction's end without changing it: a statement the owner commits beside it
// (an operation event) is fenced as a claim statement is.
func Hold(ctx context.Context, tx *sql.Tx, o Owner, c Claim) error {
	if err := lock(ctx, tx, c.ID); err != nil {
		return fmt.Errorf("staging: hold: %w", err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE staging_claim SET lease_until = lease_until
		WHERE `+fence, c.ID, o.ID, c.Gen, o.Epoch)
	if err != nil {
		return fmt.Errorf("staging: hold: %w", err)
	}
	return affected(ctx, tx, o, res)
}

// Release ends the claim released, in the caller's draft transaction (T1's claim half).
func Release(ctx context.Context, tx *sql.Tx, o Owner, c Claim) error {
	return end(ctx, tx, o, c, "released")
}

// Abandon ends the claim abandoned, in the caller's transaction: the owner's own refusal.
func Abandon(ctx context.Context, tx *sql.Tx, o Owner, c Claim) error {
	return end(ctx, tx, o, c, "abandoned")
}

// end moves the claim to state and clears its payload with its digest (compilation §3).
func end(ctx context.Context, tx *sql.Tx, o Owner, c Claim, state string) error {
	if err := lock(ctx, tx, c.ID); err != nil {
		return fmt.Errorf("staging: %s: %w", state, err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE staging_claim SET state = $5, payload = NULL, payload_digest = NULL
		WHERE `+fence, c.ID, o.ID, c.Gen, o.Epoch, state)
	if err != nil {
		return fmt.Errorf("staging: %s: %w", state, err)
	}
	return affected(ctx, tx, o, res)
}

func affected(ctx context.Context, q execer, o Owner, res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("staging: %w", err)
	}
	return refused(ctx, q, o, int(n))
}

// refused names a refusal after the write: ErrEpochSuperseded when the current epoch is not the
// owner's, ErrFenced otherwise. It never decides the write, which has already been refused.
func refused(ctx context.Context, q execer, o Owner, n int) error {
	if n == 1 {
		return nil
	}
	var current string
	if err := q.QueryRowContext(ctx, `SELECT epoch FROM installation_state`).Scan(&current); err != nil {
		return fmt.Errorf("staging: read the epoch of a refused statement: %w", err)
	}
	if current != o.Epoch {
		return ErrEpochSuperseded
	}
	return ErrFenced
}
