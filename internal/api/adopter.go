package api

import (
	"context"
	"errors"
	"time"
)

// adoptCandidate is an approved adopt plan the adoption loop may record, and its machine.
type adoptCandidate struct {
	plan, machine string
}

// startAdopter starts this process's adoption loop, which runs until life ends; a process with no
// executor, or no controller identity, has none.
func (a *API) startAdopter() {
	if a.d.exe == nil || a.d.owner.ID == "" {
		return
	}
	a.d.runs.Add(1)
	go func() {
		defer a.d.runs.Done()
		a.adoptLoop(a.d.life)
	}()
}

// wakeAdopter tells this process's adoption loop a plan was approved; a wake already pending
// covers it.
func (a *API) wakeAdopter() {
	select {
	case a.d.adopt <- struct{}{}:
	default:
	}
}

// adoptLoop records the adoption of each approved adopt plan (execution-recovery.md §6.3 step 4),
// a pass at its start, after each approval's wake and every heartbeat. A superseded epoch ends
// it: such a process adopts nothing until it is restarted.
func (a *API) adoptLoop(ctx context.Context) {
	t := time.NewTicker(a.d.timers.Heartbeat)
	defer t.Stop()
	retry := map[string]time.Time{}
	for a.adoptPass(ctx, retry) {
		if a.o.onAdoptIdle != nil {
			a.o.onAdoptIdle()
		}
		select {
		case <-ctx.Done():
			return
		case <-a.d.adopt:
		case <-t.C:
		}
	}
}

// adoptPass attempts every candidate not waiting for its retry time, and answers false when the
// loop ends. An observation that is no evidence, or any other failure short of a refusal,
// waits a lease before the plan is attempted again; a refusal is recorded by its refusal entry,
// which ends the attempts for the plan, as does an adoption another instance recorded first.
func (a *API) adoptPass(ctx context.Context, retry map[string]time.Time) bool {
	cs, err := a.adoptCandidates(ctx)
	switch {
	case ctx.Err() != nil:
		return false
	case errors.Is(err, errNotController):
		a.o.logf("adoption: the epoch is superseded; it adopts nothing until restarted")
		return false
	case err != nil:
		a.o.logf("adoption: the candidates: %v", err)
		return true
	}
	for _, c := range cs {
		if time.Now().Before(retry[c.plan]) {
			continue
		}
		delete(retry, c.plan)
		err := a.adoptOne(ctx, c)
		var refused *adoptionRefused
		switch {
		case ctx.Err() != nil:
			return false
		case errors.Is(err, errNotController):
			a.o.logf("adoption: the epoch is superseded; it adopts nothing until restarted")
			return false
		case errors.As(err, &refused):
			a.o.logf("adoption of plan %s: %v", c.plan, err)
		case errors.Is(err, errAlreadyAdopted), errors.Is(err, errAlreadyRefused):
			// Another instance's loop recorded its outcome first; the plan is no longer a candidate.
			a.o.logf("%v", err)
		case err != nil:
			a.o.logf("adoption of plan %s: %v; retried after %s", c.plan, err, a.d.timers.Lease)
			retry[c.plan] = time.Now().Add(a.d.timers.Lease)
		}
	}
	// A plan left out of this pass, its scope closed for now, keeps its time until it passes.
	for p, at := range retry {
		if !time.Now().Before(at) {
			delete(retry, p)
		}
	}
	return true
}

// adoptCandidates reads, at the database's current time, the adopt plans the loop may record now:
// approved in the current epoch and not expired, their approver not revoked, no refusal entry
// naming them, and their machine's scope open to an adoption (requirement 4.5), which the loop
// waits for rather than have refused. It answers errNotController when the epoch is not this
// process's.
func (a *API) adoptCandidates(ctx context.Context) ([]adoptCandidate, error) {
	var current string
	if err := a.db.QueryRowContext(ctx, `SELECT epoch FROM installation_state`).Scan(&current); err != nil {
		return nil, err
	}
	if current != a.d.owner.Epoch {
		return nil, errNotController
	}
	rows, err := a.db.QueryContext(ctx, `SELECT p.id, p.machine
		FROM plan p JOIN plan_state s ON s.plan = p.id JOIN approval ap ON ap.id = s.approval
			JOIN principal pr ON pr.id = ap.approver JOIN machine m ON m.id = p.machine CROSS JOIN installation_state i
		WHERE p.kind = 'adopt' AND s.state = 'approved' AND p.expires_at > clock_timestamp()
			AND NOT pr.revoked AND ap.epoch = i.epoch AND i.epoch = $1
			AND m.scope_state IN ('normal', 'released') AND NOT (i.recovery_mode AND m.scope_state <> 'released')
			AND NOT EXISTS (SELECT FROM operation o WHERE o.machine = p.machine
				AND o.state IN ('committed', 'sending', 'verifying', 'unresolved'))
			AND NOT EXISTS (SELECT FROM machine_event e WHERE e.machine = p.machine AND e.kind = 'refusal'
				AND e.entry->>'plan' = p.id)
		ORDER BY p.id`, current)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var cs []adoptCandidate
	for rows.Next() {
		var c adoptCandidate
		if err := rows.Scan(&c.plan, &c.machine); err != nil {
			return nil, err
		}
		cs = append(cs, c)
	}
	return cs, rows.Err()
}

// adoptOne takes an evidence observation of c's machine for its plan, at the plan's route, then
// records the adoption (T6), which relies on the recorded observation with the highest basis. T6
// answers errNoEvidence, not a refusal, when that observation began before the approval, is older
// than the plan allows or left the identity, the configuration or the assignment evidence unread,
// and errAlreadyAdopted or errAlreadyRefused when another attempt recorded the plan's outcome first.
func (a *API) adoptOne(ctx context.Context, c adoptCandidate) error {
	if _, err := a.observe(ctx, c.machine, observeFor{purpose: "evidence", plan: c.plan}); err != nil {
		return err
	}
	r, err := a.commitAdoption(ctx, c.plan)
	if err != nil {
		return err
	}
	a.o.logf("adoption of plan %s: recorded as operation %s, relying on observation %s", c.plan, r.Operation, r.Observation)
	return nil
}
