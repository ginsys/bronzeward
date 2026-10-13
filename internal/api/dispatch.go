package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/seam"
	"github.com/ginsys/bronzeward/internal/talos"
)

// useTime is what a passed use-time check holds for the dispatch that made it: the Talos access
// read once, and the bound artifact decrypted. Neither is stored or logged.
type useTime struct {
	access   provider.TalosAccess
	artifact provider.Artifact
}

// errUseTime is a use-time check one dependency failed: nothing was committed and nothing sent.
var errUseTime = errors.New("the use-time check failed")

// errAccessMoved is a retry whose Talos access, unreadable when the loop looked, was read at another
// version than its observation's: nothing was sent, and the next pass observes the machine again.
var errAccessMoved = errors.New("the Talos access moved since the retry's observation")

// dispatchPlan takes an approved apply-config plan to its sent attempt (execution-recovery.md
// §3.1 to §3.5): the Talos access read once and the bound artifact decrypted, recorded as the
// use-time check; an evidence observation taken with that access; the commitment; and the attempt.
// An operation another loop committed first is that loop's.
func (a *API) dispatchPlan(ctx context.Context, c dispatchCandidate) error {
	u, err := a.useTimeCheck(ctx, c.machine, c.cluster, c.plan, "", nil, time.Time{})
	if err != nil {
		return err
	}
	if _, err := a.observe(ctx, c.machine, observeFor{purpose: "evidence", plan: c.plan, access: &u.access}); err != nil {
		return err
	}
	seam.At("dispatch-evidence") // the use-time check and the evidence recorded, nothing committed
	cm, err := a.commitPlan(ctx, c.plan)
	if err != nil {
		return err
	}
	seam.At("dispatch-committed") // the operation committed, no attempt recorded
	return a.attemptAndSend(ctx, cm.Operation, 1, c.machine, c.plan, u)
}

// advance carries one operation this process owns a step on: a committed one to its attempt; a
// verifying one to its completion observation; an unresolved one to a recovery observation, and,
// classified safe to retry, to a new attempt.
func (a *API) advance(ctx context.Context, op heldOperation) error {
	if !op.sent && op.state != "verifying" {
		if done, err := a.cancelUnattempted(ctx, op); done || err != nil {
			return err
		}
	}
	switch op.state {
	case "committed":
		u, err := a.useTimeCheck(ctx, op.machine, op.cluster, op.plan, op.id, nil, time.Time{})
		if err != nil {
			return err
		}
		return a.attemptAndSend(ctx, op.id, op.gen, op.machine, op.plan, u)
	case "verifying":
		return a.verify(ctx, op.id, op.gen, op.machine, op.plan, "completion")
	}
	// Unresolved: the access is read once for the observation and, if it classifies the operation
	// safe to retry, for the attempt made on it (§3.3). An operation with a recorded attempt is
	// observed for its completion, which alone can complete or fail it (§4); one with none, for
	// recovery. The check made on that access runs its window from before this read.
	began, err := a.dbClock(ctx)
	if err != nil {
		return err
	}
	acc, err := a.d.exe.TalosAccess(ctx, op.cluster)
	var have *provider.TalosAccess
	if err == nil {
		have = &acc
	}
	if op.retry && (have == nil || have.Version().Version == op.retryAccess) {
		// A classified retry rests on its observation, which read this access version; one made
		// against another version is observed and classified again below.
		u, err := a.useTimeCheck(ctx, op.machine, op.cluster, op.plan, op.id, have, began)
		if err != nil {
			return err
		}
		if u.access.Version().Version != op.retryAccess {
			return fmt.Errorf("operation %s: %w", op.id, errAccessMoved)
		}
		return a.attemptAndSend(ctx, op.id, op.gen, op.machine, op.plan, u)
	}
	purpose := "recovery"
	if op.sent {
		purpose = "completion"
	}
	o, err := a.observe(ctx, op.machine, observeFor{purpose: purpose, plan: op.plan, operation: op.id, access: have})
	if err != nil {
		return err
	}
	s, err := a.settleOperation(ctx, op.id, op.gen, o.ID)
	if err != nil {
		return err
	}
	a.o.logf("operation %s: %s observation %s: %s: %s", op.id, purpose, o.ID, s.Outcome, s.Cause)
	if s.Outcome != "safe-to-retry" || have == nil {
		return nil
	}
	u, err := a.useTimeCheck(ctx, op.machine, op.cluster, op.plan, op.id, have, began)
	if err != nil {
		return err
	}
	return a.attemptAndSend(ctx, op.id, op.gen, op.machine, op.plan, u)
}

// useTimeCheck is §3.1 item 2: the cluster's Talos access, unless the caller read it, and the
// decryption of the bound artifact under the bound key identity and version, recorded with their
// results and the time as one use-time check entry on the machine's timeline, never the values. A
// failed dependency fails the check: errUseTime, with nothing committed or sent. The check's window
// runs from started, the database time before the first dependency read: the zero time when the
// check itself makes every read, the caller's own when it read the access first.
func (a *API) useTimeCheck(ctx context.Context, machine, cluster, plan, operation string, have *provider.TalosAccess,
	started time.Time) (useTime, error) {
	var u useTime
	if started.IsZero() {
		var err error
		if started, err = a.dbClock(ctx); err != nil {
			return u, err
		}
	}
	deps := map[string]any{}
	failed := false
	fail := func(name, cause string) {
		deps[name] = map[string]any{"result": "failed", "cause": cause}
		failed = true
	}
	if have != nil {
		u.access = *have
	} else if acc, err := a.d.exe.TalosAccess(ctx, cluster); err != nil {
		if ctx.Err() != nil {
			return u, ctx.Err()
		}
		fail("access", accessCause(err))
	} else {
		u.access = acc
	}
	if !failed {
		v := u.access.Version()
		deps["access"] = map[string]any{"result": "passed", "path": v.Path, "version": v.Version,
			"created": v.CreatedTime.UTC().Format(time.RFC3339Nano)}
	}
	var ct, key string
	var version int
	if err := a.db.QueryRowContext(ctx, `SELECT rm.ciphertext, rm.key_version, rm.key_name FROM plan p
		JOIN release_machine rm ON rm.release = p.release AND rm.machine = p.machine WHERE p.id = $1`, plan).Scan(&ct, &version,
		&key); err != nil {
		return u, err
	}
	switch {
	case key != a.d.exe.ArtifactKey():
		fail("artifact", "key-mismatch") // the bound key is not the one the executor decrypts under
	default:
		art, err := a.d.exe.DecryptArtifact(ctx, provider.Ciphertext(ct), version)
		switch {
		case err == nil:
			u.artifact = art
			deps["artifact"] = map[string]any{"result": "passed", "key": key, "version": version}
		case ctx.Err() != nil:
			return u, ctx.Err()
		case errors.Is(err, provider.ErrUnavailable):
			fail("artifact", "provider-unavailable")
		default:
			c := accessCause(err)
			if c == "provider-unavailable" { // not a provider status: the ciphertext or the version was refused
				c = "refused"
			}
			fail("artifact", c)
		}
	}
	result := "passed"
	if failed {
		result = "failed"
	}
	entry := map[string]any{"plan": plan, "controller": a.d.owner.ID, "result": result, "dependencies": deps,
		"started": started.UTC().Format(time.RFC3339Nano)}
	if operation != "" {
		entry["operation"] = operation
	}
	if err := a.recordUseTimeCheck(ctx, machine, entry); err != nil {
		return useTime{}, err
	}
	if failed {
		return useTime{}, fmt.Errorf("plan %s: %w", plan, errUseTime)
	}
	return u, nil
}

// dbClock is the database's current time, the one clock the use-time check, the evidence and every
// timeline entry are compared on.
func (a *API) dbClock(ctx context.Context) (time.Time, error) {
	var t time.Time
	err := a.db.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&t)
	return t, err
}

// useTimeCheckCause is why the latest use-time check this controller recorded for the plan does not
// hold at time at, or "" when it does (§3.2 comparison 3, §3.3 comparison 3): the check failed, or
// the plan's validity window, measured from before its dependency reads, has run out. A dispatch
// always records its check before its evidence,
// so a plan with none is not one this controller dispatched, and nothing here refuses it.
func (a *API) useTimeCheckCause(ctx context.Context, tx *sql.Tx, machine, plan string, at time.Time) (string, error) {
	var cause string
	err := tx.QueryRowContext(ctx, `SELECT CASE WHEN e.entry->>'result' <> 'passed' THEN 'the use-time check failed'
				WHEN $4::timestamptz - LEAST(e.at, (e.entry->>'started')::timestamptz) > p.check_validity THEN 'the use-time check is older than the plan allows' ELSE '' END
			FROM machine_event e JOIN plan p ON p.id = $2
			WHERE e.machine = $1 AND e.kind = 'use-time-check' AND e.entry->>'plan' = $2 AND e.entry->>'controller' = $3
			ORDER BY e.revision DESC LIMIT 1`, machine, plan, a.d.owner.ID, at).Scan(&cause)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return cause, err
}

// recordUseTimeCheck appends the use-time check entry under the same epoch check and machine lock
// as an observation's.
func (a *API) recordUseTimeCheck(ctx context.Context, machine string, entry map[string]any) error {
	body, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return a.inTx(ctx, func(tx *sql.Tx) error {
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT epoch FROM installation_state FOR SHARE`).Scan(&current); err != nil {
			return err
		}
		if current != a.d.owner.Epoch {
			return errNotController
		}
		var rev int64
		at, err := nextEntry(ctx, tx, machine, &rev)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
			VALUES ($1, $2, $3, 'use-time-check', $4::jsonb, $5)`, machine, rev, current, string(body), at)
		return err
	})
}

// attemptAndSend records the operation's attempt and, only if it is recorded, makes its one
// request under the attempt's absolute transport deadline; records the response; and, when the
// node accepted, takes the completion observation. An attempt that is refused sent nothing, and
// its refusal is recorded and settled by the attempt itself.
func (a *API) attemptAndSend(ctx context.Context, operation string, gen int64, machine, plan string, u useTime) error {
	at, err := a.recordAttempt(ctx, operation, gen)
	if err != nil {
		return err
	}
	seam.At("dispatch-attempted") // the attempt recorded, nothing sent
	sctx, cancel := context.WithDeadline(ctx, at.Deadline)
	resp := a.send(sctx, u.access.Talosconfig(), at.Route, u.artifact)
	cancel()
	seam.At("dispatch-sent") // the request made, no response recorded
	return a.respond(ctx, at.Attempt, operation, gen, machine, plan, resp)
}

// respond records the attempt's response. An in-process failure to record it leaves the operation
// sending until a controller start takes it over (§3.4): no timer does.
func (a *API) respond(ctx context.Context, attempt, operation string, gen int64, machine, plan string, resp talos.Response) error {
	r, err := a.recordResponse(ctx, attempt, resp)
	if err != nil {
		return err
	}
	if r.State != "verifying" {
		return nil
	}
	return a.verify(ctx, operation, gen, machine, plan, "completion")
}

// verify takes an observation for operation of the given purpose and settles the operation on it.
func (a *API) verify(ctx context.Context, operation string, gen int64, machine, plan, purpose string) error {
	o, err := a.observe(ctx, machine, observeFor{purpose: purpose, plan: plan, operation: operation})
	if err != nil {
		return err
	}
	s, err := a.settleOperation(ctx, operation, gen, o.ID)
	if err != nil {
		return err
	}
	a.o.logf("operation %s: %s observation %s: %s: %s", operation, purpose, o.ID, s.Outcome, s.Cause)
	return nil
}
