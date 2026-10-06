package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ginsys/bronzeward/internal/staging"
)

// The publish worker (persistence-api.md §5.1, §6, §8.2): it claims a queued publish job, or a
// running one whose lease lapsed, under this process's owner at the next generation, extends the
// lease while it compiles, and ends the job with T3 or its failure transaction. Every write is
// fenced on the generation it claimed, so a worker superseded by a later claim writes nothing.

// claimPublish claims the oldest eligible publish job (§5.1) and records the claim as an event.
// It reports false when no job is eligible, and staging.ErrEpochSuperseded when this process's
// epoch is no longer the current one: such a process claims nothing until it is restarted.
func (a *API) claimPublish(ctx context.Context) (publishJob, bool, error) {
	var j publishJob
	var ok bool
	err := a.inTx(ctx, func(tx *sql.Tx) error {
		j, ok = publishJob{}, false
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT epoch FROM installation_state FOR SHARE`).Scan(&current); err != nil {
			return err
		}
		if current != a.d.owner.Epoch {
			return staging.ErrEpochSuperseded
		}
		// The eligibility is re-checked in the UPDATE's own predicate: a job another claimer took
		// first no longer matches it once its lock is released (DB row 020).
		var n int
		switch err := tx.QueryRowContext(ctx, `UPDATE operation SET state = 'running', owner = $1, owner_gen = owner_gen + 1,
				owner_epoch = $2, lease_until = clock_timestamp() + $3::bigint * interval '1 microsecond', last_event = last_event + 1
			WHERE id = (SELECT id FROM operation WHERE kind = 'publish'
					AND (state = 'queued' OR (state = 'running' AND lease_until < clock_timestamp())) ORDER BY seq LIMIT 1)
				AND (state = 'queued' OR (state = 'running' AND lease_until < clock_timestamp()))
			RETURNING id, draft, draft_revision, owner_gen, last_event`,
			a.d.owner.ID, a.d.owner.Epoch, a.d.timers.Lease.Microseconds()).Scan(&j.op, &j.draft, &j.draftRev, &j.gen, &n); {
		case errors.Is(err, sql.ErrNoRows):
			return nil
		case err != nil:
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT cluster FROM draft WHERE id = $1`, j.draft).Scan(&j.cluster); err != nil {
			return err
		}
		ok = true
		_, err := tx.ExecContext(ctx, `INSERT INTO operation_event (operation, number, epoch, kind, entry, at)
			VALUES ($1, $2, $3, 'publish', $4, now())`, j.op, n, a.d.owner.Epoch, fmt.Sprintf(`{"type": "claimed", "generation": %d}`, j.gen))
		return err
	})
	return j, ok, err
}

// extendPublish extends j's lease by its owner, while the lease is still live (§5.1): a lapsed
// lease is never extended by its old owner, and a superseded generation answers staging.ErrFenced.
func (a *API) extendPublish(ctx context.Context, j publishJob) error {
	return a.inTx(ctx, func(tx *sql.Tx) error {
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT epoch FROM installation_state FOR SHARE`).Scan(&current); err != nil {
			return err
		}
		if current != a.d.owner.Epoch {
			return staging.ErrEpochSuperseded
		}
		res, err := tx.ExecContext(ctx, `UPDATE operation SET lease_until = clock_timestamp() + $5::bigint * interval '1 microsecond'
			WHERE id = $1 AND owner = $2 AND owner_gen = $3 AND owner_epoch = $4
				AND state = 'running' AND lease_until > clock_timestamp()`,
			j.op, a.d.owner.ID, j.gen, a.d.owner.Epoch, a.d.timers.Lease.Microseconds())
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return staging.ErrFenced
		}
		return nil
	})
}
