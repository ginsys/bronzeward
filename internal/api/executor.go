package api

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// dispatchCandidate is an approved apply-config plan the executor loop may dispatch, its machine and
// cluster.
type dispatchCandidate struct {
	plan, machine, cluster string
}

// heldOperation is an apply-config operation this process owns and the loop may advance: its state,
// the owner generation it holds, whether an attempt was recorded for it, whether one of its
// attempts is not accounted for, and whether its latest classification is a retry this token may
// attempt (execution-recovery.md §3.3 comparison 8).
type heldOperation struct {
	id, machine, cluster, plan, state string
	gen                               int64
	retryAccess                       int
	sent, unaccounted, retry          bool
}

// executorState is what the loop keeps between passes: whether the controller-start read of the
// operations to take over is still owed, the ones read and not yet taken over, the retry times of
// what failed short of a refusal, and the plans a refusal ended.
type executorState struct {
	takeover bool
	owed     []heldOperation
	retry    map[string]time.Time
	ended    map[string]bool
}

// startExecutor starts this process's executor loop, which runs until life ends; a process with no
// executor, or no controller identity, has none.
func (a *API) startExecutor() {
	if a.d.exe == nil || a.d.owner.ID == "" {
		return
	}
	a.d.runs.Add(1)
	go func() {
		defer a.d.runs.Done()
		a.executorLoop(a.d.life)
	}()
}

// wakeExecutor tells this process's executor loop a plan was approved; a wake already pending
// covers it.
func (a *API) wakeExecutor() {
	select {
	case a.d.dispatch <- struct{}{}:
	default:
	}
}

// wakeLoops is a plan approval's wake: the adoption loop takes an adopt plan, the executor loop an
// apply-config plan.
func (a *API) wakeLoops() {
	a.wakeAdopter()
	a.wakeExecutor()
}

// executorLoop dispatches each approved apply-config plan (execution-recovery.md §3) and carries
// the operations this process owns to their outcome, a pass at its start, after each approval's
// wake and every heartbeat. Its first pass takes over every non-terminal operation another
// process owned (§3.4): a restarted controller is the only takeover, no timer takes one. A
// superseded epoch ends it: such a process dispatches nothing until it is restarted.
func (a *API) executorLoop(ctx context.Context) {
	t := time.NewTicker(a.d.timers.Heartbeat)
	defer t.Stop()
	st := &executorState{takeover: true, retry: map[string]time.Time{}, ended: map[string]bool{}}
	for a.executorPass(ctx, st) {
		if a.o.onExecIdle != nil {
			a.o.onExecIdle()
		}
		select {
		case <-ctx.Done():
			return
		case <-a.d.dispatch:
		case <-t.C:
		}
	}
}

// executorPass runs one pass and answers false when the loop ends. Work that failed short of a
// refusal waits a lease before it is attempted again.
func (a *API) executorPass(ctx context.Context, st *executorState) bool {
	step := func(key string, err error) bool {
		var refused *commitRefused
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return false
		case errors.Is(err, errNotController):
			a.o.logf("executor: the epoch is superseded; it dispatches nothing until restarted")
			return false
		case errors.Is(err, errAlreadyCommitted):
			a.o.logf("executor: %v", err)
		case errors.As(err, &refused):
			a.o.logf("dispatch of %s: %v", key, err)
			// A plan whose approval, bindings or evidence a refusal showed is dead for this process
			// (§3.2: a refused record ends the attempts for that plan, and a new plan is the retry);
			// a held scope or a gate is retried.
			switch refused.Comparison {
			case "1", "2", "3":
				st.ended[key] = true
			default:
				st.retry[key] = time.Now().Add(a.d.timers.Lease)
			}
		default:
			a.o.logf("dispatch of %s: %v; retried after %s", key, err, a.d.timers.Lease)
			st.retry[key] = time.Now().Add(a.d.timers.Lease)
		}
		return true
	}
	if st.takeover {
		ops, err := a.foreignOperations(ctx)
		if err != nil {
			return step("takeover", err) // the first pass is repeated until it has read them
		}
		st.takeover, st.owed = false, ops
	}
	// A takeover that failed short of a refusal is kept, with the generation read, and retried after
	// the lease: the operation is not this process's until it has been taken over.
	var kept []heldOperation
	for _, op := range st.owed {
		if time.Now().Before(st.retry[op.id]) {
			kept = append(kept, op)
			continue
		}
		delete(st.retry, op.id)
		_, err := a.takeOver(ctx, op.id, op.gen, "controller-start")
		var refused *outcomeRefused
		if errors.As(err, &refused) { // another process took it over first, or it ended
			continue
		}
		if err != nil {
			kept = append(kept, op)
		}
		if !step(op.id, err) {
			return false
		}
	}
	st.owed = kept
	held, err := a.heldOperations(ctx)
	if err != nil {
		return step("operations", err)
	}
	for _, op := range held {
		if time.Now().Before(st.retry[op.id]) {
			continue
		}
		delete(st.retry, op.id)
		if !step(op.id, a.advance(ctx, op)) {
			return false
		}
	}
	cs, err := a.dispatchCandidates(ctx, "")
	if err != nil {
		return step("candidates", err)
	}
	for _, c := range cs {
		if st.ended[c.plan] || time.Now().Before(st.retry[c.plan]) {
			continue
		}
		delete(st.retry, c.plan)
		if !step(c.plan, a.dispatchPlan(ctx, c)) {
			return false
		}
	}
	for k, at := range st.retry { // what is no longer a candidate keeps no time
		if !time.Now().Before(at) {
			delete(st.retry, k)
		}
	}
	return true
}

// currentEpoch answers the installation's epoch, or errNotController when it is not this
// process's.
func (a *API) currentEpoch(ctx context.Context) (string, error) {
	var current string
	if err := a.db.QueryRowContext(ctx, `SELECT epoch FROM installation_state`).Scan(&current); err != nil {
		return "", err
	}
	if current != a.d.owner.Epoch {
		return "", errNotController
	}
	return current, nil
}

// foreignOperations are the non-terminal apply-config operations another process owns, which a
// starting controller takes over once.
func (a *API) foreignOperations(ctx context.Context) ([]heldOperation, error) {
	if _, err := a.currentEpoch(ctx); err != nil {
		return nil, err
	}
	rows, err := a.db.QueryContext(ctx, `SELECT id, owner_gen FROM operation WHERE kind = 'apply-config'
		AND state IN ('committed', 'sending', 'verifying', 'unresolved') AND owner IS DISTINCT FROM $1 ORDER BY created_at, id`,
		a.d.owner.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ops []heldOperation
	for rows.Next() {
		var op heldOperation
		if err := rows.Scan(&op.id, &op.gen); err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return ops, rows.Err()
}

// heldOperations are the operations this process owns that the loop advances: committed (its
// attempt not yet recorded), verifying and unresolved. A sending one is not: only a takeover moves
// it, as no timer does (§3.4). sent marks an operation with a recorded attempt. retry marks an
// unresolved operation whose latest classification is this token's safe-to-retry, its observation
// still within the plan's age, with no attempt, response, takeover or classification since: one
// that has aged out is observed and classified again. retryAccess is the Talos access version that
// observation read.
func (a *API) heldOperations(ctx context.Context) ([]heldOperation, error) {
	current, err := a.currentEpoch(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := a.db.QueryContext(ctx, `SELECT o.id, o.machine, o.cluster, o.plan, o.state, o.owner_gen,
			EXISTS (SELECT FROM attempt WHERE operation = o.id),
			EXISTS (SELECT FROM attempt t WHERE t.operation = o.id
				AND NOT EXISTS (SELECT FROM attempt_accounting c WHERE c.attempt = t.id)),
			(SELECT ob.access_version FROM operation_classification c JOIN observation ob ON ob.id = c.observation
					JOIN machine_event se ON se.machine = ob.machine AND se.revision = ob.basis
				WHERE c.operation = o.id AND c.outcome = 'safe-to-retry'
					AND c.owner = o.owner AND c.owner_gen = o.owner_gen AND c.owner_epoch = o.owner_epoch
					AND c.revision = (SELECT max(revision) FROM operation_classification WHERE operation = o.id)
					AND clock_timestamp() - se.at <= p.max_observation_age
					AND NOT EXISTS (SELECT FROM machine_event e WHERE e.machine = o.machine AND e.revision > c.revision
						AND e.kind IN ('attempt', 'response', 'ownership-transition', 'classification') AND e.entry->>'operation' = o.id)
				AND ob.access_version IS NOT NULL)
		FROM operation o JOIN plan p ON p.id = o.plan WHERE o.kind = 'apply-config'
				AND o.state IN ('committed', 'verifying', 'unresolved')
				AND o.owner = $1 AND o.owner_epoch = $2 ORDER BY o.created_at, o.id`, a.d.owner.ID, current)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ops []heldOperation
	for rows.Next() {
		var op heldOperation
		var retryAccess sql.NullInt64
		if err := rows.Scan(&op.id, &op.machine, &op.cluster, &op.plan, &op.state, &op.gen, &op.sent, &op.unaccounted,
			&retryAccess); err != nil {
			return nil, err
		}
		op.retry, op.retryAccess = retryAccess.Valid, int(retryAccess.Int64)
		ops = append(ops, op)
	}
	return ops, rows.Err()
}

// dispatchCandidates are the apply-config plans the loop may commit now, read at the database's
// current time: approved in the current epoch and unexpired, the approval and its approver not
// revoked, the machine's scope open and no operation holding it or the cluster's rollout scope,
// which the loop waits for rather than have refused. A plan names the one candidate to read again
// immediately before its dispatch: an earlier dispatch in the pass, or a revocation, may have
// closed it since the list was read.
func (a *API) dispatchCandidates(ctx context.Context, plan string) ([]dispatchCandidate, error) {
	current, err := a.currentEpoch(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := a.db.QueryContext(ctx, `SELECT p.id, p.machine, p.cluster
		FROM plan p JOIN plan_state s ON s.plan = p.id JOIN approval ap ON ap.id = s.approval
			JOIN principal pr ON pr.id = ap.approver JOIN machine m ON m.id = p.machine CROSS JOIN installation_state i
		WHERE p.kind = 'apply-config' AND s.state = 'approved' AND p.expires_at > clock_timestamp()
			AND NOT pr.revoked AND ap.epoch = i.epoch AND i.epoch = $1
			AND NOT EXISTS (SELECT FROM approval_revocation r WHERE r.approval = ap.id)
			AND m.scope_state IN ('normal', 'released') AND NOT m.frozen AND NOT (i.recovery_mode AND m.scope_state <> 'released')
			AND NOT EXISTS (SELECT FROM operation o WHERE o.kind = 'apply-config'
				AND o.state IN ('committed', 'sending', 'verifying', 'unresolved') AND (o.machine = p.machine OR o.cluster = p.cluster))
			AND ($2 = '' OR p.id = $2)
		ORDER BY p.id`, current, plan)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var cs []dispatchCandidate
	for rows.Next() {
		var c dispatchCandidate
		if err := rows.Scan(&c.plan, &c.machine, &c.cluster); err != nil {
			return nil, err
		}
		cs = append(cs, c)
	}
	return cs, rows.Err()
}
