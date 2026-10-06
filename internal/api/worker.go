package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

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
		// The oldest eligible job is locked first: an UPDATE forms its new row, the lease
		// included, before it waits for the row, so a lease counted there could lapse in the wait.
		// The eligibility is re-checked in the UPDATE's own predicate: a job another claimer took
		// first no longer matches it once its lock is released (DB row 020).
		var op string
		switch err := tx.QueryRowContext(ctx, `SELECT id FROM operation WHERE kind = 'publish'
				AND (state = 'queued' OR (state = 'running' AND lease_until < clock_timestamp())) ORDER BY seq LIMIT 1
			FOR UPDATE`).Scan(&op); {
		case errors.Is(err, sql.ErrNoRows):
			return nil
		case err != nil:
			return err
		}
		var n int
		switch err := tx.QueryRowContext(ctx, `UPDATE operation SET state = 'running', owner = $1, owner_gen = owner_gen + 1,
				owner_epoch = $2, lease_until = clock_timestamp() + $3::bigint * interval '1 microsecond', last_event = last_event + 1
			WHERE id = $4 AND (state = 'queued' OR (state = 'running' AND lease_until < clock_timestamp()))
			RETURNING id, draft, draft_revision, owner_gen, last_event`,
			a.d.owner.ID, a.d.owner.Epoch, a.d.timers.Lease.Microseconds(), op).Scan(&j.op, &j.draft, &j.draftRev, &j.gen, &n); {
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
		if err := lockOperation(ctx, tx, j.op); err != nil {
			return err
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

// lockOperation locks the operation's row before an owner's fenced UPDATE of it: an UPDATE
// evaluates its predicate before it waits for a row and does not evaluate it again when the
// holder ends without changing the row, so a lease that lapsed in the wait would go unseen (as
// staging's lock does for a claim).
func lockOperation(ctx context.Context, tx *sql.Tx, op string) error {
	_, err := tx.ExecContext(ctx, `SELECT 1 FROM operation WHERE id = $1 FOR UPDATE`, op)
	return err
}

// startPublisher starts this process's one publish worker, which runs until life ends; a process
// without a provider has none.
func (a *API) startPublisher() {
	if a.d.pub == nil || a.d.owner.ID == "" {
		return
	}
	a.d.runs.Add(1)
	go func() {
		defer a.d.runs.Done()
		a.publishLoop(a.d.life)
	}()
}

// publishLoop claims and runs one job at a time, while any is eligible; then it waits for T2's
// wake or the next poll, every heartbeat, which finds a lease that lapsed (ruling R39). A
// superseded epoch ends it: such a process claims nothing until it is restarted.
func (a *API) publishLoop(ctx context.Context) {
	t := time.NewTicker(a.d.timers.Heartbeat)
	defer t.Stop()
	for {
		j, ok, err := a.claimPublish(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, staging.ErrEpochSuperseded):
			a.o.logf("publish worker: the epoch is superseded; it claims nothing until restarted")
			return
		case err != nil:
			a.o.logf("publish worker: the claim: %v", err)
		case ok:
			a.runPublish(ctx, j)
			continue
		}
		if a.o.onIdle != nil {
			a.o.onIdle()
		}
		select {
		case <-ctx.Done():
			return
		case <-a.d.wake:
		case <-t.C:
		}
	}
}

// runPublish builds j's release and commits it (T3), heartbeating the lease every
// timers.Heartbeat until it returns. A refusal is recorded as the operation's failure; an error,
// or a heartbeat the fence refuses, stops the run and leaves the job to its lease.
func (a *API) runPublish(ctx context.Context, j publishJob) {
	ctx, cancel := context.WithCancel(ctx)
	var beat sync.WaitGroup
	beat.Add(1)
	go func() {
		defer beat.Done()
		a.publishHeartbeat(ctx, cancel, j)
	}()
	defer func() {
		cancel()
		beat.Wait()
	}()
	u, ref, err := a.buildRelease(ctx, j, *a.d.pub)
	switch {
	case ctx.Err() != nil:
		a.o.logf("publication %s: stopped before the publication commit", j.op)
		return
	case err != nil:
		a.o.logf("publication %s: %v", j.op, err)
		return
	case ref != nil:
		if err := a.inTx(ctx, func(tx *sql.Tx) error {
			return a.finishPublish(ctx, tx, j, "failed", nil, problemDoc(j.op, ref), map[string]any{"type": "failed", "code": ref.code})
		}); err != nil {
			a.o.logf("publication %s: the failure was not recorded: %v", j.op, err)
		}
		return
	}
	if _, _, err := a.publishCommit(ctx, j, u); err != nil {
		a.o.logf("publication %s: the publication commit: %v", j.op, err)
	}
}

// publishHeartbeat extends j's lease until ctx ends; an extension the fence refuses stops the run.
func (a *API) publishHeartbeat(ctx context.Context, stop context.CancelFunc, j publishJob) {
	t := time.NewTicker(a.d.timers.Heartbeat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		err := a.extendPublish(ctx, j)
		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, staging.ErrFenced), errors.Is(err, staging.ErrEpochSuperseded):
			a.o.logf("publication %s: heartbeat refused (%v); the run stops", j.op, err)
			stop()
			return
		case err != nil: // the lease may still hold; the next heartbeat tries again
			a.o.logf("publication %s: %v", j.op, err)
		}
	}
}
