package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Drift changes a machine's drift record makes (execution-recovery.md §6.5).
const (
	driftOpened   = "opened"
	driftObserved = "observed"
	driftReturned = "returned"
)

// applyDrift applies §6.1 and §6.5 to observation of machine, in epoch, under the machine's lock the
// caller holds: a configuration digest other than Applied's opens a drift record, or is recorded on
// the one already open, and one equal to Applied's closes an open record as returned. The basis
// rules exclude an observation whose basis precedes a change of Applied, one a recorded observation
// of a higher basis that read the digest supersedes, and one read while an operation held or took
// the machine scope at or after its basis. failing is the completion observation that makes an
// operation failed in this transaction, which those rules do not exclude (§4): it opens or records,
// and closes nothing. It answers the change made, or "" for none.
func applyDrift(ctx context.Context, tx *sql.Tx, machine, epoch, observation string, failing bool) (string, error) {
	var basis int64
	var digest, applied []byte
	err := tx.QueryRowContext(ctx, `SELECT o.basis, o.configuration_digest, s.applied_digest
		FROM observation o JOIN machine_state s ON s.machine = o.machine
		WHERE o.id = $1 AND o.machine = $2 AND o.unread->'configuration' IS NULL`, observation, machine).Scan(&basis,
		&digest, &applied)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (digest == nil || applied == nil)) {
		return "", nil // the configuration was not read, or the machine has no Applied to drift from
	}
	if err != nil {
		return "", err
	}
	var excluded bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM machine_event WHERE machine = $1 AND kind = 'drift'
			AND entry->>'observation' = $3)
		OR (NOT $4 AND (
			EXISTS (SELECT FROM machine_event WHERE machine = $1 AND revision > $2 AND kind = 'applied-change')
			OR EXISTS (SELECT FROM observation WHERE machine = $1 AND basis > $2 AND unread->'configuration' IS NULL
				AND configuration_digest IS NOT NULL)
			OR EXISTS (SELECT FROM machine_event WHERE machine = $1 AND revision >= $2
				AND kind IN ('commitment', 'operation-state'))
			OR EXISTS (SELECT FROM operation WHERE machine = $1 AND kind = 'apply-config'
				AND state IN ('committed', 'sending', 'verifying', 'unresolved'))))`,
		machine, basis, observation, failing).Scan(&excluded); err != nil {
		return "", err
	}
	if excluded {
		return "", nil
	}
	var open sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT opened FROM drift_record WHERE machine = $1 AND closed IS NULL FOR UPDATE`,
		machine).Scan(&open); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	differs := !bytes.Equal(digest, applied)
	// A differing digest that is the artifact of an operation accounted by decision may be its late
	// landing, whether it opens the record or is recorded on it; the record links the latest one's.
	var late sql.NullString
	if differs {
		if err := tx.QueryRowContext(ctx, `SELECT op.id FROM operation op JOIN plan p ON p.id = op.plan
				JOIN release_machine rm ON rm.release = p.release AND rm.machine = p.machine
			WHERE op.machine = $1 AND rm.configuration_digest = $2 AND EXISTS (SELECT FROM attempt a
				JOIN attempt_accounting c ON c.attempt = a.id WHERE a.operation = op.id AND c.basis = 'decision')
			ORDER BY op.seq DESC LIMIT 1`, machine, digest).Scan(&late); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	switch {
	case differs && !open.Valid:
		rev, err := driftEntry(ctx, tx, machine, epoch, 0, driftOpened, "", observation, late.String)
		if err != nil {
			return "", err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO drift_record (machine, opened, observation, applied_digest, observed_digest,
				last_observation, late_landing)
			VALUES ($1, $2, $3, $4, $5, $3, NULLIF($6, ''))`, machine, rev, observation, applied, digest, late.String)
		return driftOpened, err
	case differs:
		if _, err := driftEntry(ctx, tx, machine, epoch, open.Int64, driftObserved, "", observation, late.String); err != nil {
			return "", err
		}
		_, err := tx.ExecContext(ctx, `UPDATE drift_record SET observed_digest = $3, last_observation = $4,
				late_landing = NULLIF($5, '')
			WHERE machine = $1 AND opened = $2`, machine, open.Int64, digest, observation, late.String)
		return driftObserved, err
	case open.Valid && !failing:
		rev, err := driftEntry(ctx, tx, machine, epoch, open.Int64, "closed", driftReturned, observation, "")
		if err != nil {
			return "", err
		}
		_, err = tx.ExecContext(ctx, `UPDATE drift_record SET closed = $3, resolution = 'returned'
			WHERE machine = $1 AND opened = $2`, machine, open.Int64, rev)
		return driftReturned, err
	}
	return "", nil
}

// driftEntry records one drift entry on machine's timeline: the record it changes (its own revision
// when it opens one), the change, the observation it rests on, a closing's resolution and a
// differing digest's possible late landing.
func driftEntry(ctx context.Context, tx *sql.Tx, machine, epoch string, record int64, change, resolution, observation,
	late string) (int64, error) {
	var rev int64
	at, err := nextEntry(ctx, tx, machine, &rev)
	if err != nil {
		return 0, err
	}
	if record == 0 {
		record = rev
	}
	entry := map[string]any{"record": record, "change": change, "observation": observation}
	if resolution != "" {
		entry["resolution"] = resolution
	}
	if late != "" {
		entry["lateLanding"] = late
	}
	body, err := json.Marshal(entry)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		VALUES ($1, $2, $3, 'drift', $4::jsonb, $5)`, machine, rev, epoch, string(body), at)
	return rev, err
}

// openDrift answers whether a drift record is open on machine. A caller holding the machine's lock
// reads it in a statement after the locking one, so its snapshot includes a record opened by the
// transaction it waited for.
func openDrift(ctx context.Context, tx *sql.Tx, machine string) (bool, error) {
	var open bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM drift_record WHERE machine = $1 AND closed IS NULL)`,
		machine).Scan(&open)
	return open, err
}

// openDriftBody is a machine's open drift record as §9.3's machine resource serves it: the revision
// of its opening entry, the observation that opened it, the latest one recorded on it and a possible
// late landing (execution-recovery.md choice §10.35).
type openDriftBody struct {
	Revision        int64   `json:"revision"`
	Observation     string  `json:"observation"`
	LastObservation string  `json:"lastObservation"`
	LateLanding     *string `json:"lateLanding"`
}
