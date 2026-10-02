package staging

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// due is a live claim past its absolute expiry, or a transient claim past its lease (compilation
// §3.5). An encrypted claim whose lease lapsed stays live until its expiry: a takeover may resume
// it (§3.4).
const due = `state IN ('held', 'resumed') AND (expires_at <= now() OR (mode = 'transient' AND lease_until <= now()))`

// EffectiveStateSQL is a claim's state as every read and transition treats it (compilation
// §3.5): abandoned once due, whether or not a sweep has written it yet; the stored state otherwise.
const EffectiveStateSQL = `CASE WHEN ` + due + ` THEN 'abandoned' ELSE state END`

// AbandonedTitle is the title of the ingestion-abandoned problem (persistence-api §9.4).
const AbandonedTitle = "The ingestion was abandoned"

// Sweep writes each due claim abandoned and clears its payload, in a transaction of its own that
// also fails the claim's running ingest operation ingestion-abandoned with its terminal event
// (persistence-api §8.2, T8). It returns how many claims it abandoned.
func Sweep(ctx context.Context, db *sql.DB) (int, error) {
	return sweep(ctx, db, nil)
}

// sweep is Sweep; afterScan, when set, runs between the candidate scan and the first write.
func sweep(ctx context.Context, db *sql.DB, afterScan func()) (int, error) {
	ids, err := candidates(ctx, db)
	if err != nil {
		return 0, err
	}
	if afterScan != nil {
		afterScan()
	}
	n := 0
	for _, id := range ids {
		ok, err := abandonDue(ctx, db, id)
		if err != nil {
			return n, err
		}
		if ok {
			n++
		}
	}
	return n, nil
}

// candidates lists the claims due now. It decides nothing: each write re-states the condition.
func candidates(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM staging_claim WHERE `+due+` ORDER BY expires_at, id`)
	if err != nil {
		return nil, fmt.Errorf("staging: sweep: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("staging: sweep: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("staging: sweep: %w", err)
	}
	return ids, nil
}

// abandonDue abandons claim if it is still due when its row is locked: a claim released, taken
// over or extended since the scan is left as it is. It takes the installation state FOR SHARE,
// then the claim, then its operation, in the order every claim transaction takes them, so its
// terminal event carries the epoch still current when it commits.
func abandonDue(ctx context.Context, db *sql.DB, claim string) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("staging: sweep: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // a no-op once committed
	var epoch string
	if err := tx.QueryRowContext(ctx, `SELECT epoch FROM installation_state FOR SHARE`).Scan(&epoch); err != nil {
		return false, fmt.Errorf("staging: sweep %s: %w", claim, err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE staging_claim SET state = 'abandoned', payload = NULL, payload_digest = NULL
		WHERE id = $1 AND `+due, claim)
	if err != nil {
		return false, fmt.Errorf("staging: sweep %s: %w", claim, err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return false, err
	}
	var op string
	var number int
	err = tx.QueryRowContext(ctx, `UPDATE operation SET state = 'failed',
		error = jsonb_build_object('type', 'urn:bronzeward:problem:ingestion-abandoned', 'title', $2::text, 'status', 409,
			'instance', id, 'detail', 'the staging claim passed its lease or its absolute expiry; ingest the input again'),
		owner = NULL, owner_epoch = NULL, lease_until = NULL, last_event = last_event + 1
		WHERE ingestion = $1 AND state = 'running' RETURNING id, last_event`, claim, AbandonedTitle).Scan(&op, &number)
	switch {
	case errors.Is(err, sql.ErrNoRows): // no operation left running: the claim alone is written
	case err != nil:
		return false, fmt.Errorf("staging: sweep %s: fail its operation: %w", claim, err)
	default:
		if _, err := tx.ExecContext(ctx, `INSERT INTO operation_event (operation, number, epoch, kind, entry, at)
			VALUES ($1, $2, $3, 'ingest', '{"type":"failed","code":"ingestion-abandoned"}', now())`,
			op, number, epoch); err != nil {
			return false, fmt.Errorf("staging: sweep %s: the terminal event: %w", claim, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("staging: sweep %s: %w", claim, err)
	}
	return true, nil
}
