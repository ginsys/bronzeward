package api

import (
	"context"
	"errors"
	"time"
)

// startDrifter starts this process's drift loop, which runs until life ends; a process with no
// executor, or no controller identity, has none.
func (a *API) startDrifter() {
	if a.d.exe == nil || a.d.owner.ID == "" {
		return
	}
	a.d.runs.Add(1)
	go func() {
		defer a.d.runs.Done()
		a.driftLoop(a.d.life)
	}()
}

// driftLoop observes each managed machine periodically (execution-recovery.md §6.1, choice
// §10.36): a pass every execution.driftInterval, the first one interval after the loop starts. A
// superseded epoch ends it: such a process observes nothing until it is restarted.
func (a *API) driftLoop(ctx context.Context) {
	t := time.NewTicker(a.d.exec.DriftInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !a.driftPass(ctx) {
			return
		}
		if a.o.onDriftIdle != nil {
			a.o.onDriftIdle()
		}
	}
}

// driftPass takes a drift observation of each machine driftCandidates names, whose recording
// transaction applies the drift rules, and answers false when the loop ends. A failed
// observation is logged; the next pass observes the machine again.
func (a *API) driftPass(ctx context.Context) bool {
	ms, err := a.driftCandidates(ctx)
	switch {
	case ctx.Err() != nil:
		return false
	case errors.Is(err, errNotController):
		a.o.logf("drift: the epoch is superseded; it observes nothing until restarted")
		return false
	case err != nil:
		a.o.logf("drift: the candidates: %v", err)
		return true
	}
	for _, m := range ms {
		_, err := a.observe(ctx, m, observeFor{purpose: "drift"})
		switch {
		case ctx.Err() != nil:
			return false
		case errors.Is(err, errNotController):
			a.o.logf("drift: the epoch is superseded; it observes nothing until restarted")
			return false
		case err != nil:
			a.o.logf("drift observation of machine %s: %v", m, err)
		}
	}
	return true
}

// driftCandidates reads the machines a drift observation could open or close a record for: those
// with an Applied to drift from, whose scope no operation holds (§6.1) and recovery mode leaves
// open, as the adoption loop's scope test reads it. It answers errNotController when the epoch is
// not this process's.
func (a *API) driftCandidates(ctx context.Context) ([]string, error) {
	var current string
	if err := a.db.QueryRowContext(ctx, `SELECT epoch FROM installation_state`).Scan(&current); err != nil {
		return nil, err
	}
	if current != a.d.owner.Epoch {
		return nil, errNotController
	}
	rows, err := a.db.QueryContext(ctx, `SELECT m.id
		FROM machine m JOIN machine_state s ON s.machine = m.id CROSS JOIN installation_state i
		WHERE s.applied_digest IS NOT NULL AND i.epoch = $1
			AND m.scope_state IN ('normal', 'released') AND NOT (i.recovery_mode AND m.scope_state <> 'released')
			AND NOT EXISTS (SELECT FROM operation o WHERE o.machine = m.id
				AND o.state IN ('committed', 'sending', 'verifying', 'unresolved'))
		ORDER BY m.id`, current)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ms []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		ms = append(ms, m)
	}
	return ms, rows.Err()
}
