package api

import (
	"context"
	"errors"
	"time"
)

// startRecoveryReads starts the loop taking the recovery observations the post-restore accounting
// decision names as each scope's settle (execution-recovery.md §7.3 step 1, ruling S3): no
// observation route exists, so the controller takes them, the recovery start once its entry has
// committed and every normal start, since the normal restart entry allows (§7.2) must take them
// too. A process with no executor, or no controller identity, takes none.
func (a *API) startRecoveryReads() {
	if a.d.exe == nil || a.d.owner.ID == "" {
		return
	}
	a.d.runs.Add(1)
	go func() {
		defer a.d.runs.Done()
		a.recoveryReadLoop(a.d.life)
	}()
}

// recoveryReadLoop runs a pass every third of the settle floor until life ends, the epoch is
// superseded or recovery mode is not in effect. It does not end when no scope is pre-restore
// unaccounted: a machine inventoried later in recovery mode starts so (§7.4).
func (a *API) recoveryReadLoop(ctx context.Context) {
	t := time.NewTicker(max(a.d.exec.SettleFloor/3, time.Second))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !a.recoveryReadPass(ctx) {
			return
		}
	}
}

// recoveryReadPass takes a recovery observation, naming no plan or operation, of each scope
// recoveryReadsDue names, and answers false when the loop ends. A failed observation is logged; a
// later pass observes the machine again.
func (a *API) recoveryReadPass(ctx context.Context) bool {
	ms, more, err := a.recoveryReadsDue(ctx)
	switch {
	case ctx.Err() != nil:
		return false
	case errors.Is(err, errNotController):
		a.o.logf("recovery reads: the epoch is superseded")
		return false
	case err != nil:
		a.o.logf("recovery reads: the scopes: %v", err)
		return true
	}
	for _, m := range ms {
		o, err := a.observe(ctx, m, observeFor{purpose: "recovery", paced: true})
		switch {
		case ctx.Err() != nil:
			return false
		case errors.Is(err, errReadNotDue):
		case errors.Is(err, errNotController):
			a.o.logf("recovery reads: the epoch is superseded")
			return false
		case err != nil:
			a.o.logf("recovery observation of machine %s: %v", m, err)
		default:
			a.o.logf("recovery observation %s of machine %s: the scope is pre-restore unaccounted", o.ID, m)
		}
	}
	if more && a.d.recoveryStart {
		a.reclassifyRestored(ctx)
	}
	return more
}

// reclassifyRestored is §7.3 step 5 in a recovery start, which runs no executor (ruling R8b-1): each
// operation this process owns with no attempt, whose approval can never pass comparison 1 again, ends
// cancelled, as the executor would end it, so that its scope can be marked and released before the
// normal restart. Nothing is sent. One with an attempt waits for the normal restart's executor.
func (a *API) reclassifyRestored(ctx context.Context) {
	ops, err := a.heldOperations(ctx)
	if err != nil {
		a.o.logf("recovery reclassification: the operations: %v", err)
		return
	}
	for _, op := range ops {
		if op.sent || op.state == "verifying" {
			continue
		}
		if _, err := a.cancelUnattempted(ctx, op); err != nil && ctx.Err() == nil {
			a.o.logf("recovery reclassification of operation %s: %v", op.id, err)
		}
	}
}

// recoveryReadsDue reads the pre-restore unaccounted scopes due a recovery observation, and whether
// recovery mode is in effect. One is due once the transport deadline the decision counts
// from has passed (the stop recorded at entry plus the maximum transport deadline, or a restored
// unaccounted attempt's later one), and then once per settle floor after the last read was recorded,
// as recoveryReadDue paces an operation's; a read still in progress counts from its start. It
// answers errNotController when the epoch is not this process's.
func (a *API) recoveryReadsDue(ctx context.Context) ([]string, bool, error) {
	var current string
	var recovery bool
	if err := a.db.QueryRowContext(ctx, `SELECT epoch, recovery_mode FROM installation_state`).Scan(&current, &recovery); err != nil {
		return nil, false, err
	}
	if current != a.d.owner.Epoch {
		return nil, false, errNotController
	}
	if !recovery {
		return nil, false, nil
	}
	rows, err := a.db.QueryContext(ctx, `WITH d AS (SELECT m.id, greatest(r.stopped_at + make_interval(secs => $2),
				(SELECT max(a.transport_deadline) FROM attempt a WHERE a.machine = m.id
					AND NOT EXISTS (SELECT FROM attempt_accounting c WHERE c.attempt = a.id))) AS at
			FROM machine m JOIN recovery_epoch r ON r.epoch = $1 WHERE m.scope_state = 'pre-restore-unaccounted')
		SELECT d.id, clock_timestamp() > d.at AND NOT EXISTS (SELECT FROM observation_start s
				JOIN machine_event e ON e.machine = s.machine AND e.revision = s.revision
				LEFT JOIN observation o ON o.machine = s.machine AND o.basis = s.revision
			WHERE s.machine = d.id AND s.purpose = 'recovery' AND e.at > d.at
				AND coalesce(o.at, e.at) > clock_timestamp() - make_interval(secs => $3))
		FROM d ORDER BY d.id`, current, a.d.exec.MaxTransportDeadline.Seconds(), a.d.exec.SettleFloor.Seconds())
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	var ms []string
	for rows.Next() {
		var m string
		var due bool
		if err := rows.Scan(&m, &due); err != nil {
			return nil, false, err
		}
		if due {
			ms = append(ms, m)
		}
	}
	return ms, true, rows.Err()
}
