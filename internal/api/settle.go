package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"
)

// accountingDecision is a recovery-admin's decision that an attempt is accounted for
// (execution-recovery.md §5.2): who decided under which act, the observations read at the start and
// at the end of the settle, and the facts the decision holds, among them how a change of the
// machine-configuration resource version during the settle is explained.
type accountingDecision struct {
	Attempt, DecidedBy, Act, Start, End string
	Facts                               map[string]any
}

// accounted is what a decision recorded: the accounting entry's revision.
type accounted struct {
	Attempt  string
	Revision int64
}

// accountByDecision is the accounting transaction of §5.2, as the effect of the recovery-admin's
// request in tx, which holds the act: it records the accounting entry and record of the attempt,
// and changes no state. It requires, as recorded facts and reading them under the operation's and
// machine's locks: the attempt's executor is stopped or taken over (its owner generation or epoch
// is no longer the operation's); at least the settle floor from the latest of the attempt's transport
// deadline and the takeover, and the first successful read of the machine after that deadline (one
// that read a node value), to the start of the end observation, a recovery observation of this
// machine that read the machine-configuration resource version at both its start and end; and,
// when that version moved, a nonblank explanation among the facts. The act is written after this
// effect in the same transaction.
func (a *API) accountByDecision(ctx context.Context, tx *sql.Tx, d accountingDecision) (accounted, error) {
	r := accounted{Attempt: d.Attempt}
	refuse := func(cause string) (accounted, error) { return r, refuseOutcome("accounting", cause) }
	var operation string
	if err := tx.QueryRowContext(ctx, `SELECT operation FROM attempt WHERE id = $1`, d.Attempt).Scan(&operation); err != nil {
		return r, fmt.Errorf("accounting of attempt %s: %w", d.Attempt, err)
	}
	l, err := a.lockOperation(ctx, tx, operation)
	if err != nil {
		return r, err
	}
	var already bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM attempt_accounting WHERE attempt = $1)`, d.Attempt).Scan(&already); err != nil {
		return r, err
	}
	if already {
		return refuse("the attempt is already accounted for")
	}
	// 1: the executor is stopped or taken over. A takeover moves the owner generation or epoch
	// past the attempt's token; no other record shows an executor stopped.
	var stopped bool
	var transport time.Time
	if err := tx.QueryRowContext(ctx, `SELECT a.owner_gen < $2 OR a.owner_epoch <> $3 OR a.owner <> $4, a.transport_deadline
		FROM attempt a WHERE a.id = $1`, d.Attempt, l.gen, l.current, l.owner.String).Scan(&stopped, &transport); err != nil {
		return r, err
	}
	if !stopped {
		return refuse("the attempt's executor is not stopped or taken over")
	}
	// 2: the settle floor, from the latest of the transport deadline, the takeover and the first
	// successful read after the deadline, to the end observation's start.
	anchor := transport
	var takeover, firstRead sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT max(e.at) FROM machine_event e WHERE e.machine = $1 AND e.kind = 'ownership-transition'
		AND e.entry->>'operation' = $2`, l.machine, operation).Scan(&takeover); err != nil {
		return r, err
	}
	if takeover.Valid && takeover.Time.After(anchor) {
		anchor = takeover.Time
	}
	// A successful read of the machine is one that read a node value; access obtained and the node
	// unreachable is not one.
	if err := tx.QueryRowContext(ctx, `SELECT min(o.at) FROM observation o WHERE o.machine = $1 AND o.at > $2
		AND (o.unread->'identity' IS NULL OR o.unread->'runningVersion' IS NULL OR o.unread->'configuration' IS NULL)`,
		l.machine, transport).Scan(&firstRead); err != nil {
		return r, err
	}
	if firstRead.Valid && firstRead.Time.After(anchor) {
		anchor = firstRead.Time
	}
	floor := a.d.exec.SettleFloor
	var startAt, endBegan, endAt time.Time
	var startVersion, endVersion sql.NullString
	var purpose string
	if err := tx.QueryRowContext(ctx, `SELECT se.at, o.resource_version FROM observation o
		JOIN machine_event se ON se.machine = o.machine AND se.revision = o.basis WHERE o.id = $1 AND o.machine = $2`,
		d.Start, l.machine).Scan(&startAt, &startVersion); err != nil {
		return r, fmt.Errorf("accounting of attempt %s: its start observation: %w", d.Attempt, err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT se.at, o.at, o.resource_version, s.purpose FROM observation o
		JOIN machine_event se ON se.machine = o.machine AND se.revision = o.basis
		JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis WHERE o.id = $1 AND o.machine = $2`,
		d.End, l.machine).Scan(&endBegan, &endAt, &endVersion, &purpose); err != nil {
		return r, fmt.Errorf("accounting of attempt %s: its end observation: %w", d.Attempt, err)
	}
	switch {
	case purpose != "recovery":
		return refuse("the end observation is not a recovery observation")
	case !startVersion.Valid || !endVersion.Valid:
		return refuse("the machine-configuration resource version was not read at both ends of the settle")
	case !endAt.After(startAt):
		return refuse("the end observation does not follow the start observation")
	case endBegan.Before(anchor.Add(floor)):
		return refuse(fmt.Sprintf("the end observation began before the settle floor of %s had elapsed since %s", floor,
			anchor.UTC().Format(time.RFC3339Nano)))
	}
	moved := startVersion.String != endVersion.String
	if moved {
		if text, ok := d.Facts["explanation"].(string); !ok || strings.TrimSpace(text) == "" {
			return refuse("the machine-configuration resource version changed during the settle and no explanation is recorded")
		}
	}
	facts := map[string]any{}
	maps.Copy(facts, d.Facts)
	facts["judgement"] = true
	facts["settleFloor"] = floor.String()
	facts["anchor"] = anchor.UTC().Format(time.RFC3339Nano)
	facts["startVersion"], facts["endVersion"] = startVersion.String, endVersion.String
	body, err := json.Marshal(facts)
	if err != nil {
		return r, err
	}
	rev, at, err := a.appendEntry(ctx, tx, l, "accounting", map[string]any{"operation": operation, "attempt": d.Attempt,
		"basis": "decision", "decidedBy": d.DecidedBy, "role": "recovery-admin"})
	if err != nil {
		return r, err
	}
	r.Revision = rev
	_, err = tx.ExecContext(ctx, `INSERT INTO attempt_accounting (attempt, machine, basis, decided_by, role, act, start_observation,
			end_observation, facts, epoch, at, revision) VALUES ($1, $2, 'decision', $3, 'recovery-admin', $4, $5, $6, $7::jsonb, $8, $9, $10)`,
		d.Attempt, l.machine, d.DecidedBy, d.Act, d.Start, d.End, string(body), l.current, at, rev)
	return r, err
}

// settlement is what a settle decided: the operation's state after, the outcome (completed, failed,
// safe-to-retry, unresolved, or pending when the observation settles nothing) and why.
type settlement struct {
	Operation, State, Outcome, Cause string
	Revision                         int64
}

// settleOperation is the completion observation's decision (execution-recovery.md §4, §5, §5.1,
// choice §10.10) as the owner of the operation at generation, on a recorded observation: an
// operation with an attempt that is not accounted for stays, or becomes, unresolved; otherwise a
// completion observation that establishes the identity, assignment revision and configuration
// digest completes it, one that contradicts them fails it, or classifies it safe to retry when
// the evidence, the bound and the gates allow; and one that neither establishes nor contradicts
// them, is ordered before the last accounting, is not tied to the operation or is contradicted by a
// recorded observation of a higher basis settles nothing. Past the verification deadline, a
// verifying operation that is neither established nor contradicted becomes unresolved. Completion
// records `Applied` and its entry in the same transaction. A drift record opened by a failure is
// ginsys/bronzeward#27's.
func (a *API) settleOperation(ctx context.Context, operation string, generation int64, observation string) (settlement, error) {
	if a.d.owner.ID == "" {
		return settlement{}, errNotController
	}
	r := settlement{Operation: operation}
	err := a.inTx(ctx, func(tx *sql.Tx) error {
		l, err := a.lockOperation(ctx, tx, operation)
		if err != nil {
			return err
		}
		if !a.owns(l, generation) {
			return refuseOutcome("settle", "the operation's owner token is not this controller's")
		}
		if l.state != "verifying" && l.state != "unresolved" {
			return refuseOutcome("settle", "the operation is "+l.state)
		}
		r.State = l.state
		var attempts, unaccounted int
		var lastDeadline sql.NullTime
		var lastAccountingRev sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT count(*), count(*) FILTER (WHERE c.attempt IS NULL), max(c.revision),
				max(a.verification_deadline) FROM attempt a LEFT JOIN attempt_accounting c ON c.attempt = a.id
			WHERE a.operation = $1`, operation).Scan(&attempts, &unaccounted, &lastAccountingRev, &lastDeadline); err != nil {
			return err
		}
		stay := func(outcome, cause string) error {
			r.Outcome, r.Cause = outcome, cause
			return nil
		}
		if unaccounted > 0 {
			if l.state == "verifying" {
				if err := a.moveOperation(ctx, tx, &l, "unresolved", "an attempt is not accounted for", nil, nil); err != nil {
					return err
				}
				r.State = l.state
			}
			return stay("unresolved", "an attempt is not accounted for")
		}
		// The observation: recorded, of this machine, a completion observation taken for this operation
		// or, for a safe-to-retry classification, a recovery one, ordered after the last accounting.
		var plan, purpose string
		var forOperation sql.NullString
		var basis, revision int64
		var obsAt time.Time
		var identity, digest, assignment, atExpected, minor sql.NullBool
		err = tx.QueryRowContext(ctx, `SELECT s.purpose, s.operation, o.basis, o.revision, o.at,
				CASE WHEN o.unread->'identity' IS NULL THEN `+identityMatch+` END,
				CASE WHEN o.unread->'configuration' IS NULL THEN o.configuration_digest IS NOT DISTINCT FROM rm.configuration_digest END,
				CASE WHEN o.unread->'assignmentEvidence' IS NULL THEN o.assignment_evidence IS NOT DISTINCT FROM p.assignment_revision END,
				CASE WHEN o.unread->'configuration' IS NULL THEN o.configuration_digest IS NOT DISTINCT FROM p.expected_digest END,
				CASE WHEN o.unread->'runningVersion' IS NULL THEN `+minorMatch+` END, p.id
			FROM observation o JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis
				JOIN machine m ON m.id = o.machine JOIN cluster c ON c.id = m.cluster
				JOIN operation op ON op.id = $2 JOIN plan p ON p.id = op.plan JOIN release rl ON rl.id = p.release
				JOIN release_machine rm ON rm.release = p.release AND rm.machine = p.machine
			WHERE o.id = $1 AND o.machine = $3`, observation, operation, l.machine).Scan(&purpose, &forOperation, &basis, &revision,
			&obsAt, &identity, &digest, &assignment, &atExpected, &minor, &plan)
		if errors.Is(err, sql.ErrNoRows) {
			return refuseOutcome("settle", "no such observation of the operation's machine")
		}
		if err != nil {
			return err
		}
		switch {
		case purpose != "completion" && purpose != "recovery":
			return stay("pending", "the observation is not a completion or recovery observation")
		case purpose == "completion" && forOperation.String != operation:
			return stay("pending", "the observation is not tied to this operation")
		case lastAccountingRev.Valid && basis < lastAccountingRev.Int64:
			return stay("pending", "the observation is ordered before the last accounting")
		}
		var blocking sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT o2.id FROM observation c JOIN observation o2 ON o2.machine = c.machine AND o2.basis > c.basis
			WHERE c.id = $1 AND (
				(c.unread->'identity' IS NULL AND o2.unread->'identity' IS NULL AND (o2.smbios_uuid IS DISTINCT FROM c.smbios_uuid
					OR o2.talos_node_id IS DISTINCT FROM c.talos_node_id OR o2.talos_cluster_id IS DISTINCT FROM c.talos_cluster_id))
				OR (c.unread->'configuration' IS NULL AND o2.unread->'configuration' IS NULL
					AND o2.configuration_digest IS DISTINCT FROM c.configuration_digest)
				OR (c.unread->'assignmentEvidence' IS NULL AND o2.unread->'assignmentEvidence' IS NULL
					AND o2.assignment_evidence IS DISTINCT FROM c.assignment_evidence))
			ORDER BY o2.basis LIMIT 1`, observation).Scan(&blocking); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if blocking.Valid {
			return stay("pending", "a later recorded observation reports another value: "+blocking.String)
		}
		contradicted := (identity.Valid && !identity.Bool) || (digest.Valid && !digest.Bool) || (assignment.Valid && !assignment.Bool)
		established := identity.Valid && identity.Bool && digest.Valid && digest.Bool && assignment.Valid && assignment.Bool
		if established && purpose == "completion" && attempts > 0 {
			return a.complete(ctx, tx, &l, plan, observation, &r)
		}
		if !contradicted && attempts > 0 {
			// Neither, or established by a read that completes nothing (a recovery observation). A
			// verifying operation past its recorded verification deadline is unresolved. An operation
			// with no attempt (taken over before its first) goes on: the observation may admit it.
			if l.state == "verifying" && lastDeadline.Valid && obsAt.After(lastDeadline.Time) {
				if err := a.moveOperation(ctx, tx, &l, "unresolved", "no completion observation by the verification deadline",
					nil, nil); err != nil {
					return err
				}
				r.State = l.state
				return stay("unresolved", "the postconditions are neither established nor contradicted by the verification deadline")
			}
			return stay("pending", "the observation neither establishes nor contradicts the postconditions")
		}
		// Contradicted: safe to retry on the §5.1 evidence, or, by a completion observation of an
		// operation that sent, failed.
		retry, why, err := a.safeToRetry(ctx, tx, l, plan, attempts, obsAt, identity, assignment, atExpected, minor)
		if err != nil {
			return err
		}
		if retry {
			rev, _, err := a.appendEntry(ctx, tx, l, "classification", map[string]any{"operation": operation, "outcome": "safe-to-retry",
				"observation": observation, "relied": []int64{revision}, "controller": a.d.owner.ID, "generation": generation})
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO operation_classification (operation, machine, revision, outcome, observation,
					owner, owner_gen, owner_epoch, relied, at)
				SELECT $1, $2, $3, 'safe-to-retry', $4, $5, $6, $7, to_jsonb(ARRAY[$8::bigint]), at FROM machine_event
				WHERE machine = $2 AND revision = $3`, operation, l.machine, rev, observation, a.d.owner.ID, generation, l.current,
				revision); err != nil {
				return err
			}
			if l.state == "verifying" {
				if err := a.moveOperation(ctx, tx, &l, "unresolved", "classified safe to retry", nil, nil); err != nil {
					return err
				}
			}
			r.State, r.Revision = l.state, rev
			return stay("safe-to-retry", "the observation shows the pre-dispatch configuration")
		}
		if attempts == 0 || purpose != "completion" {
			return stay("pending", "the observation does not fail the operation: "+why)
		}
		cause := "a completion observation contradicts the postconditions"
		if err := a.moveOperation(ctx, tx, &l, "failed", cause, nil, map[string]any{"outcome": "failed", "cause": cause,
			"observation": observation, "attempts": attempts}); err != nil {
			return err
		}
		r.State = l.state
		return stay("failed", cause)
	})
	return r, err
}

// safeToRetry is §5.1's safe-to-retry evidence and §10.10's bound: the observation read the
// identity, assignment and running minor as bound and the configuration at the plan's pre-dispatch
// digest; no attempt was accepted or definitively rejected; attempts are left; the approval passes
// comparison 1 at the observation's time of settle; and the scope gate is open. It answers the
// reason when not.
func (a *API) safeToRetry(ctx context.Context, tx *sql.Tx, l operationLock, plan string, attempts int, _ time.Time,
	identity, assignment, atExpected, minor sql.NullBool) (bool, string, error) {
	if !(identity.Valid && identity.Bool && assignment.Valid && assignment.Bool && atExpected.Valid && atExpected.Bool &&
		minor.Valid && minor.Bool) {
		return false, "the observation does not show the bound identity, assignment and running minor at the pre-dispatch digest", nil
	}
	var answered bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM attempt_response r JOIN attempt a ON a.id = r.attempt
		WHERE a.operation = $1 AND r.class IN ('accepted', 'rejected'))`, l.id).Scan(&answered); err != nil {
		return false, "", err
	}
	if answered {
		return false, "an attempt was accepted or definitively rejected", nil
	}
	var max sql.NullInt64
	var scope string
	var frozen, recovery bool
	var at time.Time
	if err := tx.QueryRowContext(ctx, `SELECT p.max_attempts, m.scope_state, m.frozen, i.recovery_mode, clock_timestamp()
		FROM plan p JOIN machine m ON m.id = p.machine, installation_state i WHERE p.id = $1`, plan).Scan(&max, &scope, &frozen,
		&recovery, &at); err != nil {
		return false, "", err
	}
	if !max.Valid || int64(attempts) >= max.Int64 {
		return false, "the operation has no attempts left", nil
	}
	cause, err := finalPlanCause(ctx, tx, plan, l.current, at)
	if err != nil {
		return false, "", err
	}
	if cause != "" {
		return false, cause, nil
	}
	if cause := scopeGate(frozen, scope, recovery); cause != "" {
		return false, cause, nil
	}
	return true, "", nil
}

// complete records a completed operation (execution-recovery.md §4): its result, `Applied` set to
// the plan's release and the artifact's digest from source operation with the baseline revision
// advanced, and one `Applied` change entry, as one transaction with the state's own entry.
func (a *API) complete(ctx context.Context, tx *sql.Tx, l *operationLock, plan, observation string, r *settlement) error {
	var release string
	var digest []byte
	var from sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT p.release, rm.configuration_digest, ms.applied_release FROM plan p
		JOIN release_machine rm ON rm.release = p.release AND rm.machine = p.machine
		JOIN machine_state ms ON ms.machine = p.machine WHERE p.id = $1 FOR UPDATE OF ms`, plan).Scan(&release, &digest, &from); err != nil {
		return err
	}
	cause := "the observation establishes the bound postconditions"
	if err := a.moveOperation(ctx, tx, l, "completed", cause, map[string]any{"appliedRelease": release,
		"digest": fmt.Sprintf("%x", digest), "observation": observation}, nil); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE machine_state SET applied_release = $2, applied_digest = $3, applied_source = 'operation',
			baseline_revision = COALESCE(baseline_revision, 0) + 1, revision = revision + 1 WHERE machine = $1`,
		l.machine, release, digest); err != nil {
		return err
	}
	var was any
	if from.Valid {
		was = from.String
	}
	rev, _, err := a.appendEntry(ctx, tx, *l, "applied-change", map[string]any{"from": was, "to": release,
		"digest": fmt.Sprintf("%x", digest), "source": "operation", "plan": plan, "operation": l.id})
	r.State, r.Outcome, r.Cause, r.Revision = l.state, "completed", cause, rev
	return err
}
