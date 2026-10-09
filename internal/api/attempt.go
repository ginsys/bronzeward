package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// attemptRefused is an attempt refused by one of execution-recovery.md §3.3's comparisons, named by
// its number, with what failed and, for comparison 3, the observation that showed it.
type attemptRefused struct {
	Comparison, Cause, Observation string
}

func (r *attemptRefused) Error() string {
	return fmt.Sprintf("attempt refused by comparison %s: %s", r.Comparison, r.Cause)
}

func refuseAttempt(comparison, cause string) error {
	return &attemptRefused{Comparison: comparison, Cause: cause}
}

// attempted is what an attempt recorded: its id and number, the operation it moved to sending and
// the attempt entry's revision.
type attempted struct {
	Attempt, Operation string
	Number             int
	Revision           int64
}

// recordAttempt is T6's attempt transaction (execution-recovery.md §3.3, persistence-api.md §5) for
// a committed apply-config operation, as this process at generation, the owner generation it holds.
// It holds, in persistence-api.md §5 rule 5's order, the installation state FOR SHARE, the machine
// FOR UPDATE, its assignment head, the approver's principal, the approval and the plan's state FOR
// SHARE, and the operation's row through comparison 7's write; reads the time after the last lock
// (rule 4); compares 1, 2, 3, 6, 7 and 9 in order; and records the attempt, its identity, owner
// token, route and absolute deadlines, as one attempt entry, before anything is sent. A refusal
// rolls back and is recorded, and settled, by a separate transaction (§4.1, PA §8.1). The first
// attempt only: comparison 8 and retries are ginsys/bronzeward#26's, and nothing here sends.
func (a *API) recordAttempt(ctx context.Context, operation string, generation int64) (attempted, error) {
	if a.d.owner.ID == "" {
		return attempted{}, errNotController
	}
	var r attempted
	var machine, plan string
	err := a.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		r, machine, plan, err = a.recordAttemptTx(ctx, tx, operation, generation)
		return err
	})
	var refused *attemptRefused
	if !errors.As(err, &refused) || machine == "" {
		return r, err
	}
	if serr := a.settleAttemptRefusal(ctx, machine, plan, operation, generation, refused); serr != nil {
		// Only a recorded refusal answers *attemptRefused: an unrecorded one is attempted again.
		return attempted{}, fmt.Errorf("%v, but its refusal was not recorded: %w", err, serr)
	}
	return attempted{}, err
}

// recordAttemptTx is recordAttempt's transaction. It answers the operation's machine and plan once
// known, so a refusal can be recorded and settled on its timeline.
func (a *API) recordAttemptTx(ctx context.Context, tx *sql.Tx, operation string, generation int64) (attempted, string,
	string, error) {
	r := attempted{Operation: operation}
	var current string
	var recovery bool
	if err := tx.QueryRowContext(ctx, `SELECT epoch, recovery_mode FROM installation_state FOR SHARE`).Scan(&current,
		&recovery); err != nil {
		return r, "", "", err
	}
	if current != a.d.owner.Epoch {
		return r, "", "", errNotController
	}
	// An operation's plan and the plan row are immutable, so their bindings are read before the
	// machine's lock.
	var plan, machine, assignment string
	var baseline sql.NullInt64
	var maxAgeMicros int64
	var maxAttempts sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT p.id, p.machine, p.assignment_revision, p.baseline_revision,
			(extract(epoch FROM p.max_observation_age) * 1000000)::bigint, p.max_attempts
		FROM operation o JOIN plan p ON p.id = o.plan WHERE o.id = $1 AND o.kind = 'apply-config'`, operation).Scan(&plan,
		&machine, &assignment, &baseline, &maxAgeMicros, &maxAttempts)
	if err != nil {
		return r, "", "", fmt.Errorf("attempt of operation %s: %w", operation, err)
	}
	maxAge := time.Duration(maxAgeMicros) * time.Microsecond
	var scope string
	var frozen bool
	if err := tx.QueryRowContext(ctx, `SELECT scope_state, frozen FROM machine WHERE id = $1 FOR UPDATE`, machine).Scan(&scope,
		&frozen); err != nil {
		return r, machine, plan, err
	}
	var msBaseline sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT baseline_revision FROM machine_state WHERE machine = $1`,
		machine).Scan(&msBaseline); err != nil {
		return r, machine, plan, err
	}
	var head sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT head_revision_id FROM assignment WHERE machine = $1 FOR SHARE`, machine).Scan(&head)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return r, machine, plan, err
	}
	// Every writer of a plan's state, and of its cancellation and revocations, holds its machine
	// first, so the approval read here is the one the locks below hold.
	var approval, approver sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT s.approval, a.approver FROM plan_state s LEFT JOIN approval a ON a.id = s.approval
		WHERE s.plan = $1`, plan).Scan(&approval, &approver); err != nil {
		return r, machine, plan, err
	}
	if approver.Valid {
		if _, err := tx.ExecContext(ctx, `SELECT FROM principal WHERE id = $1 FOR SHARE`, approver.String); err != nil {
			return r, machine, plan, err
		}
	}
	if approval.Valid {
		if _, err := tx.ExecContext(ctx, `SELECT FROM approval WHERE id = $1 FOR SHARE`, approval.String); err != nil {
			return r, machine, plan, err
		}
	}
	var committedTo sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT operation FROM plan_state WHERE plan = $1 FOR SHARE`, plan).Scan(
		&committedTo); err != nil {
		return r, machine, plan, err
	}
	if committedTo.String != operation {
		return r, machine, plan, fmt.Errorf("attempt of operation %s: plan %s is not committed to it", operation, plan)
	}
	// 7's write takes the operation's row, the last lock, so the time read after it follows every
	// lock wait (rule 4). Its outcome is compared seventh; a refusal rolls it back.
	var moved bool
	if a.o.readThenWrite {
		// The control: the owner token read first and written without, so a takeover committed
		// between the read and the write is not seen.
		var owner, epoch, state sql.NullString
		var gen int64
		if err := tx.QueryRowContext(ctx, `SELECT owner, owner_gen, owner_epoch, state FROM operation WHERE id = $1`,
			operation).Scan(&owner, &gen, &epoch, &state); err != nil {
			return r, machine, plan, err
		}
		moved = owner.String == a.d.owner.ID && gen == generation && epoch.String == current && state.String == "committed"
		if moved {
			if _, err := tx.ExecContext(ctx, `UPDATE operation SET state = 'sending' WHERE id = $1`, operation); err != nil {
				return r, machine, plan, err
			}
		}
	} else {
		res, err := tx.ExecContext(ctx, `UPDATE operation SET state = 'sending'
			WHERE id = $1 AND owner = $2 AND owner_gen = $3 AND owner_epoch = $4 AND state = 'committed'`,
			operation, a.d.owner.ID, generation, current)
		if err != nil {
			return r, machine, plan, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return r, machine, plan, err
		}
		moved = n == 1
	}
	var at time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&at); err != nil {
		return r, machine, plan, err
	}

	// 1: unexpired, not cancelled, the approval and its identity not revoked, and the approval of
	// the current epoch. A committed plan keeps its state, so each is read from its own record.
	cause, err := finalPlanCause(ctx, tx, plan, current, at)
	if err != nil {
		return r, machine, plan, err
	}
	if cause != "" {
		return r, machine, plan, refuseAttempt("1", cause)
	}
	// 2: the assignment head and the baseline revision are the bound ones. Desired is not compared:
	// a publication after the commitment does not stop the committed operation.
	switch {
	case !head.Valid || head.String != assignment:
		return r, machine, plan, refuseAttempt("2", "the machine's assignment revision changed")
	case msBaseline != baseline:
		return r, machine, plan, refuseAttempt("2", "the machine's baseline revision changed")
	}
	// 3: the commitment's evidence, still within the plan's age at this attempt's time, and no later
	// observation that read the identity, the configuration or the running minor otherwise.
	var obs string
	if err := tx.QueryRowContext(ctx, `SELECT entry->>'observation' FROM machine_event
		WHERE machine = $1 AND kind = 'commitment' AND entry->>'operation' = $2`, machine, operation).Scan(&obs); err != nil {
		return r, machine, plan, fmt.Errorf("attempt of operation %s: its commitment entry: %w", operation, err)
	}
	var basis int64
	var startedAt time.Time
	var identityMatches, digestMatches, minorMatches bool
	if err := tx.QueryRowContext(ctx, `SELECT o.basis, se.at, `+identityMatch+`,
			o.configuration_digest IS NOT DISTINCT FROM p.expected_digest, `+minorMatch+`
		FROM observation o JOIN machine_event se ON se.machine = o.machine AND se.revision = o.basis
			JOIN plan p ON p.id = $2 JOIN release rl ON rl.id = p.release
			JOIN machine m ON m.id = o.machine JOIN cluster c ON c.id = m.cluster
		WHERE o.id = $1`, obs, plan).Scan(&basis, &startedAt, &identityMatches, &digestMatches, &minorMatches); err != nil {
		return r, machine, plan, err
	}
	newer, err := newerContradiction(ctx, tx, machine, plan, basis)
	if err != nil {
		return r, machine, plan, err
	}
	refused := func(cause, observation string) error {
		return &attemptRefused{Comparison: "3", Cause: cause, Observation: observation}
	}
	switch {
	case at.Sub(startedAt) > maxAge:
		return r, machine, plan, refused("the evidence is older than the plan allows", obs)
	case !identityMatches:
		return r, machine, plan, refused("the evidence shows another machine identity", obs)
	case !digestMatches:
		return r, machine, plan, refused("the evidence shows a configuration digest other than the plan's", obs)
	case !minorMatches:
		return r, machine, plan, refused("the evidence shows a running Talos minor other than the release's contract", obs)
	case newer.Valid:
		return r, machine, plan, refused("a newer observation contradicts the evidence", newer.String)
	}
	// 6: the scope gate.
	if cause := scopeGate(frozen, scope, recovery); cause != "" {
		return r, machine, plan, refuseAttempt("6", cause)
	}
	// 7: the operation was committed and owned by this process's token, as its write read it.
	if !moved {
		var owner, epoch, state sql.NullString
		var gen int64
		if err := tx.QueryRowContext(ctx, `SELECT owner, owner_gen, owner_epoch, state FROM operation WHERE id = $1`,
			operation).Scan(&owner, &gen, &epoch, &state); err != nil {
			return r, machine, plan, err
		}
		if owner.String != a.d.owner.ID || gen != generation || epoch.String != current {
			return r, machine, plan, refuseAttempt("7", "the operation's owner token is not this controller's")
		}
		return r, machine, plan, refuseAttempt("7", "the operation is "+state.String)
	}
	// 9: attempts remain. Every attempt is recorded under the machine's lock, so the count is current.
	var made int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM attempt WHERE operation = $1`, operation).Scan(&made); err != nil {
		return r, machine, plan, err
	}
	if !maxAttempts.Valid || int64(made) >= maxAttempts.Int64 {
		return r, machine, plan, refuseAttempt("9", "the operation has no attempts left")
	}

	r.Attempt, r.Number = id.New(id.Attempt), made+1
	if err := tx.QueryRowContext(ctx, `UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1
		RETURNING revision_counter`, machine).Scan(&r.Revision); err != nil {
		return r, machine, plan, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		SELECT $1, $2, $3, 'attempt', jsonb_build_object('attempt', $4::text, 'operation', $5::text, 'number', $6::integer,
			'controller', $7::text, 'generation', $8::bigint, 'route', p.route::text,
			'transportDeadline', $9::timestamptz + p.transport_deadline,
			'verificationDeadline', $9::timestamptz + p.verification_deadline, 'observation', $10::text), $9
		FROM plan p WHERE p.id = $11`,
		machine, r.Revision, current, r.Attempt, operation, r.Number, a.d.owner.ID, generation, at, obs, plan); err != nil {
		return r, machine, plan, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO attempt (id, operation, plan, machine, number, owner, owner_gen, owner_epoch, route,
			observation, transport_deadline, verification_deadline, at, revision)
		SELECT $1, $2, p.id, p.machine, $3, $4, $5, $6, p.route, $7, $8::timestamptz + p.transport_deadline,
			$8::timestamptz + p.verification_deadline, $8, $9
		FROM plan p WHERE p.id = $10`,
		r.Attempt, operation, r.Number, a.d.owner.ID, generation, current, obs, at, r.Revision, plan)
	return r, machine, plan, err
}

// finalPlanCause is §3.3 comparison 1's cause for a committed plan at time at, or "" when it
// passes: the plan expired, cancelled, its approval revoked, the approver's identity revoked, or
// the approval recorded in an epoch other than current. None of these ever passes again.
func finalPlanCause(ctx context.Context, tx *sql.Tx, plan, current string, at time.Time) (string, error) {
	var cause string
	err := tx.QueryRowContext(ctx, `SELECT CASE
			WHEN p.expires_at <= $2 THEN 'the plan has expired'
			WHEN EXISTS (SELECT FROM plan_cancellation WHERE plan = p.id) THEN 'the plan is cancelled'
			WHEN EXISTS (SELECT FROM approval_revocation WHERE approval = s.approval) THEN 'the approval is revoked'
			WHEN ap.revoked THEN 'the approver''s identity is revoked'
			WHEN a.epoch <> $3 THEN 'the approval is of an earlier epoch'
			ELSE '' END
		FROM plan p JOIN plan_state s ON s.plan = p.id JOIN approval a ON a.id = s.approval
			JOIN principal ap ON ap.id = a.approver
		WHERE p.id = $1`, plan, at, current).Scan(&cause)
	return cause, err
}

// settleAttemptRefusal records a refused attempt (§4.1) after its transaction rolled back, and
// settles the operation (persistence-api.md §8.1), under the same epoch check and machine lock and,
// for a refusal that may settle, the operation's lock, all taken before its time is read:
// the refusal entry; after a refusal by 1, 2, 3 or 6, an operation still committed under this
// process's token moves to unresolved; and an unresolved one it owns, so moved or left unresolved
// by an earlier refusal, with no attempt and whose plan fails comparison 1, re-read here, moves on
// to cancelled. Each move is an operation state entry. A
// refusal by 7 or 9, or by a process that does not own the operation, records the entry only.
func (a *API) settleAttemptRefusal(ctx context.Context, machine, plan, operation string, generation int64,
	refused *attemptRefused) error {
	return a.inTx(ctx, func(tx *sql.Tx) error {
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT epoch FROM installation_state FOR SHARE`).Scan(&current); err != nil {
			return err
		}
		if current != a.d.owner.Epoch {
			return errNotController
		}
		if _, err := tx.ExecContext(ctx, `SELECT FROM machine WHERE id = $1 FOR UPDATE`, machine); err != nil {
			return err
		}
		// A refusal that may settle takes the operation's lock before the time is read (rule 4), so
		// comparison 1's expiry is judged after any wait for it.
		settles := false
		switch refused.Comparison {
		case "1", "2", "3", "6":
			settles = true
		}
		var owned bool
		var state string
		if settles {
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(owner = $2 AND owner_gen = $3 AND owner_epoch = $4, false),
				state FROM operation WHERE id = $1 FOR UPDATE`, operation, a.d.owner.ID, generation, current).Scan(&owned,
				&state); err != nil {
				return err
			}
		}
		var rev int64
		at, err := nextEntry(ctx, tx, machine, &rev)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
			VALUES ($1, $2, $3, 'refusal', jsonb_strip_nulls(jsonb_build_object('transaction', 'T6', 'comparison', $4::text,
				'plan', $5::text, 'operation', $6::text, 'cause', $7::text, 'controller', $8::text,
				'generation', $11::bigint, 'observation', NULLIF($9::text, ''))), $10)`,
			machine, rev, current, refused.Comparison, plan, operation, refused.Cause, a.d.owner.ID, refused.Observation,
			at, generation); err != nil {
			return err
		}
		if !settles || !owned || (state != "committed" && state != "unresolved") {
			return nil
		}
		move := func(from, to, comparison, cause string) error {
			if _, err := tx.ExecContext(ctx, `UPDATE operation SET state = $2 WHERE id = $1`, operation, to); err != nil {
				return err
			}
			at, err := nextEntry(ctx, tx, machine, &rev)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
				VALUES ($1, $2, $3, 'operation-state', jsonb_build_object('operation', $4::text, 'from', $5::text,
					'to', $6::text, 'controller', $7::text, 'generation', $8::bigint, 'comparison', $9::text,
					'cause', $10::text), $11)`,
				machine, rev, current, operation, from, to, a.d.owner.ID, generation, comparison, cause, at)
			return err
		}
		if state == "committed" {
			if err := move("committed", "unresolved", refused.Comparison, refused.Cause); err != nil {
				return err
			}
		}
		// Cancelled is recorded only for an operation with no attempt: it never proves nothing was
		// sent otherwise (§4).
		var attempts bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM attempt WHERE operation = $1)`,
			operation).Scan(&attempts); err != nil {
			return err
		}
		if attempts {
			return nil
		}
		cause, err := finalPlanCause(ctx, tx, plan, current, at)
		if err != nil || cause == "" {
			return err
		}
		return move("unresolved", "cancelled", "1", cause)
	})
}
