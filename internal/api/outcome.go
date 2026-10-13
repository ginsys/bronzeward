package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ginsys/bronzeward/internal/talos"
)

// outcomeRefused is a transaction after an attempt (execution-recovery.md §3.4, §4, §5) refused: the
// transaction and why. It records nothing; the caller decides what a refusal means.
type outcomeRefused struct {
	Transaction, Cause string
}

func (r *outcomeRefused) Error() string { return r.Transaction + " refused: " + r.Cause }

func refuseOutcome(transaction, cause string) error {
	return &outcomeRefused{Transaction: transaction, Cause: cause}
}

// operationLock is an apply-config operation as the transactions after its attempt read it, under
// persistence-api.md §5 rule 5's locks: the installation state FOR SHARE, whose epoch is this
// process's; the machine FOR UPDATE; the operation FOR UPDATE.
type operationLock struct {
	id, machine, plan, state string
	owner, ownerEpoch        sql.NullString
	gen                      int64
	current                  string // the installation's epoch
}

// lockOperation takes those locks for operation, which must be an apply-config operation.
func (a *API) lockOperation(ctx context.Context, tx *sql.Tx, operation string) (operationLock, error) {
	l := operationLock{id: operation}
	if err := tx.QueryRowContext(ctx, `SELECT epoch FROM installation_state FOR SHARE`).Scan(&l.current); err != nil {
		return l, err
	}
	if l.current != a.d.owner.Epoch {
		return l, errNotController
	}
	// An operation's machine and plan never change, so they are read before the machine's lock.
	if err := tx.QueryRowContext(ctx, `SELECT machine, plan FROM operation WHERE id = $1 AND kind = 'apply-config'`,
		operation).Scan(&l.machine, &l.plan); err != nil {
		return l, fmt.Errorf("operation %s: %w", operation, err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT FROM machine WHERE id = $1 FOR UPDATE`, l.machine); err != nil {
		return l, err
	}
	err := tx.QueryRowContext(ctx, `SELECT state, owner, owner_gen, owner_epoch FROM operation WHERE id = $1 FOR UPDATE`,
		operation).Scan(&l.state, &l.owner, &l.gen, &l.ownerEpoch)
	return l, err
}

// owns reports whether this process holds the operation's owner token at generation.
func (a *API) owns(l operationLock, generation int64) bool {
	return l.owner.Valid && l.owner.String == a.d.owner.ID && l.gen == generation && l.ownerEpoch.String == l.current
}

// appendEntry records one entry of kind on the operation's machine at its next revision, at a time
// read after the locks the caller holds (persistence-api.md §5 rule 4).
func (a *API) appendEntry(ctx context.Context, tx *sql.Tx, l operationLock, kind string, entry map[string]any) (int64, time.Time, error) {
	var rev int64
	at, err := nextEntry(ctx, tx, l.machine, &rev)
	if err != nil {
		return 0, at, err
	}
	body, err := json.Marshal(entry)
	if err != nil {
		return 0, at, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6)`, l.machine, rev, l.current, kind, string(body), at)
	return rev, at, err
}

// moveOperation moves the locked operation to state to, as one operation state entry that names the
// cause. A terminal state carries its result or error with it (the outcome check of the schema); the
// owner token stays as the record of who decided.
func (a *API) moveOperation(ctx context.Context, tx *sql.Tx, l *operationLock, to, cause string, result, failure map[string]any) error {
	doc := func(m map[string]any) (any, error) {
		if m == nil {
			return nil, nil
		}
		b, err := json.Marshal(m)
		return string(b), err
	}
	res, err := doc(result)
	if err != nil {
		return err
	}
	fail, err := doc(failure)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE operation SET state = $2, result = $3::jsonb, error = $4::jsonb WHERE id = $1`,
		l.id, to, res, fail); err != nil {
		return err
	}
	// The entry names the operation's own token; a late response moves an operation another process
	// owns, so the process that wrote it is named apart.
	if _, _, err := a.appendEntry(ctx, tx, *l, "operation-state", map[string]any{"operation": l.id, "from": l.state, "to": to,
		"controller": l.owner.String, "generation": l.gen, "decidedBy": a.d.owner.ID, "cause": cause}); err != nil {
		return err
	}
	l.state = to
	return nil
}

// classify records the §5 outcome the controller decided as one classification entry and its record,
// under the token of the process that decided it at generation: the observation it rests on, if
// any, and the revisions of the entries it relied on. It precedes the state entry the outcome moves
// the operation by. A rejection rests on responses alone, so it names no observation.
func (a *API) classify(ctx context.Context, tx *sql.Tx, l operationLock, outcome string, observation sql.NullString,
	relied []int64, generation int64) (int64, error) {
	var obs any
	if observation.Valid {
		obs = observation.String
	}
	rev, _, err := a.appendEntry(ctx, tx, l, "classification", map[string]any{"operation": l.id, "outcome": outcome,
		"observation": obs, "relied": relied, "controller": a.d.owner.ID, "generation": generation})
	if err != nil {
		return 0, err
	}
	reliedDoc, err := json.Marshal(relied)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO operation_classification (operation, machine, revision, outcome, observation,
			owner, owner_gen, owner_epoch, relied, at)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, at FROM machine_event WHERE machine = $2 AND revision = $3`,
		l.id, l.machine, rev, outcome, obs, a.d.owner.ID, generation, l.current, string(reliedDoc))
	return rev, err
}

// takenOver is what a takeover recorded: the operation, the owner generation it now holds and its
// state, and the ownership transition entry's revision.
type takenOver struct {
	Operation, State string
	Generation       int64
	Revision         int64
}

// takeOver is T8's takeover of an apply-config operation (execution-recovery.md §3.4, persistence-api.md
// §5) as this process, naming the owner generation it read: in one conditional UPDATE on that
// generation it sets this process as owner at the next generation and epoch, and moves a committed,
// sending or verifying operation to unresolved; an unresolved one stays so. Concurrent takeovers of
// one generation elect one winner: the others find the generation moved. It sends nothing and
// classifies nothing. The ownership transition is one entry, followed by an operation state entry
// when the state moved.
func (a *API) takeOver(ctx context.Context, operation string, generation int64, reason string) (takenOver, error) {
	if a.d.owner.ID == "" {
		return takenOver{}, errNotController
	}
	r := takenOver{Operation: operation}
	err := a.inTx(ctx, func(tx *sql.Tx) error {
		l, err := a.lockOperation(ctx, tx, operation)
		if err != nil {
			return err
		}
		switch l.state {
		case "committed", "sending", "verifying", "unresolved":
		default:
			return refuseOutcome("takeover", "the operation is "+l.state)
		}
		from, fromEpoch := l.owner.String, l.ownerEpoch.String
		res, err := tx.ExecContext(ctx, `UPDATE operation SET state = 'unresolved', owner = $2, owner_gen = owner_gen + 1,
				owner_epoch = $3
			WHERE id = $1 AND owner_gen = $4 AND state IN ('committed', 'sending', 'verifying', 'unresolved')`,
			operation, a.d.owner.ID, l.current, generation)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return refuseOutcome("takeover", "the operation's owner generation is not "+fmt.Sprint(generation))
		}
		r.Generation, r.State = generation+1, "unresolved"
		if r.Revision, _, err = a.appendEntry(ctx, tx, l, "ownership-transition", map[string]any{"operation": operation,
			"from": from, "to": a.d.owner.ID, "generation": r.Generation, "fromGeneration": generation, "fromEpoch": fromEpoch,
			"epoch": l.current, "reason": reason}); err != nil {
			return err
		}
		if l.state == "unresolved" {
			return nil
		}
		_, _, err = a.appendEntry(ctx, tx, l, "operation-state", map[string]any{"operation": operation, "from": l.state,
			"to": "unresolved", "controller": a.d.owner.ID, "generation": r.Generation, "cause": "takeover"})
		return err
	})
	return r, err
}

// responded is what a response recorded: its attempt, the response entry's revision, whether the
// response accounts for the attempt and the operation's state after.
type responded struct {
	Attempt, State string
	Revision       int64
	Accounted      bool
}

// recordResponse is the response transaction of an attempt (execution-recovery.md §3.5, §4, §4.1,
// §5.1), by the controller that sent it, under the attempt's owner token: one response entry and
// record of its class, gRPC code and fixed notice; and, for a response from the target, one
// accounting entry and record of the attempt unless a decision accounted it already. The
// operation's state follows only from the operation's latest attempt, except that a definitive
// rejection may end an unresolved operation: an acceptance moves sending to verifying; a definitive
// rejection moves sending or unresolved to rejected when every attempt has one, and sending to
// unresolved otherwise; any other response moves sending to unresolved. An operation that took
// over or ended since is not touched: its late response is a recorded fact only.
func (a *API) recordResponse(ctx context.Context, attempt string, resp talos.Response) (responded, error) {
	if a.d.owner.ID == "" {
		return responded{}, errNotController
	}
	r := responded{Attempt: attempt}
	err := a.inTx(ctx, func(tx *sql.Tx) error {
		// An attempt row is immutable: its operation and token are read before the locks.
		var operation, owner, ownerEpoch string
		var number int
		var gen int64
		if err := tx.QueryRowContext(ctx, `SELECT operation, number, owner, owner_gen, owner_epoch FROM attempt WHERE id = $1`,
			attempt).Scan(&operation, &number, &owner, &gen, &ownerEpoch); err != nil {
			return fmt.Errorf("response of attempt %s: %w", attempt, err)
		}
		if owner != a.d.owner.ID {
			return refuseOutcome("response", "the attempt was sent by another controller")
		}
		l, err := a.lockOperation(ctx, tx, operation)
		if err != nil {
			return err
		}
		var recorded bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM attempt_response WHERE attempt = $1)`, attempt).Scan(&recorded); err != nil {
			return err
		}
		if recorded {
			return refuseOutcome("response", "the attempt's response is already recorded")
		}
		entry := map[string]any{"operation": operation, "attempt": attempt, "number": number, "class": string(resp.Class),
			"code": int(resp.Code), "text": resp.Text, "controller": a.d.owner.ID, "generation": gen}
		rev, at, err := a.appendEntry(ctx, tx, l, "response", entry)
		if err != nil {
			return err
		}
		r.Revision = rev
		if _, err := tx.ExecContext(ctx, `INSERT INTO attempt_response (attempt, machine, class, code, text, owner, owner_gen,
				owner_epoch, at, revision) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			attempt, l.machine, string(resp.Class), int(resp.Code), resp.Text, owner, gen, ownerEpoch, at, rev); err != nil {
			return err
		}
		if resp.Accounts() {
			var accounted bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM attempt_accounting WHERE attempt = $1)`,
				attempt).Scan(&accounted); err != nil {
				return err
			}
			if !accounted {
				accRev, accAt, err := a.appendEntry(ctx, tx, l, "accounting", map[string]any{"operation": operation,
					"attempt": attempt, "basis": "response", "class": string(resp.Class)})
				if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO attempt_accounting (attempt, machine, basis, response_from_target, epoch, at,
						revision) VALUES ($1, $2, 'response', true, $3, $4, $5)`, attempt, l.machine, l.current, accAt, accRev); err != nil {
					return err
				}
				r.Accounted = true
			}
		}
		var latest, allRejected bool
		if err := tx.QueryRowContext(ctx, `SELECT $2 = (SELECT max(number) FROM attempt WHERE operation = $1),
				NOT EXISTS (SELECT FROM attempt a WHERE a.operation = $1 AND NOT EXISTS
					(SELECT FROM attempt_response p WHERE p.attempt = a.id AND p.class = 'rejected'))`,
			operation, number).Scan(&latest, &allRejected); err != nil {
			return err
		}
		to, cause := "", ""
		switch {
		case l.state == "sending" && latest && resp.Class == talos.ClassAccepted:
			to, cause = "verifying", "the node accepted the configuration"
		case (l.state == "sending" && latest || l.state == "unresolved") && resp.Class == talos.ClassRejected && allRejected:
			to, cause = "rejected", "the node refused every attempt as invalid, which precedes any mutation"
		case l.state == "sending" && latest:
			to, cause = "unresolved", "the response does not settle the outcome"
		}
		r.State = l.state
		switch to {
		case "":
		case "rejected":
			var attempts int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM attempt WHERE operation = $1`, operation).Scan(&attempts); err != nil {
				return err
			}
			// The rejection rests on every attempt's response (§5.1), this one included, and on no
			// observation; its classification precedes the state entry.
			var reliedDoc string
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(p.revision ORDER BY p.revision), '[]')::text
				FROM attempt_response p JOIN attempt a ON a.id = p.attempt WHERE a.operation = $1`, operation).Scan(&reliedDoc); err != nil {
				return err
			}
			var relied []int64
			if err := json.Unmarshal([]byte(reliedDoc), &relied); err != nil {
				return err
			}
			if _, err := a.classify(ctx, tx, l, "rejected", sql.NullString{}, relied, gen); err != nil {
				return err
			}
			if err := a.moveOperation(ctx, tx, &l, to, cause, nil, map[string]any{"outcome": "rejected", "cause": cause,
				"attempts": attempts}); err != nil {
				return err
			}
		default:
			if err := a.moveOperation(ctx, tx, &l, to, cause, nil, nil); err != nil {
				return err
			}
		}
		r.State = l.state
		return nil
	})
	return r, err
}
