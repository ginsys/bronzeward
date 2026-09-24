package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Operation states (specification §4) and observation purposes.
const (
	stateCommitted  = "committed"
	stateSending    = "sending"
	stateVerifying  = "verifying"
	stateCompleted  = "completed"
	stateRejected   = "rejected"
	stateFailed     = "failed"
	stateCancelled  = "cancelled"
	stateUnresolved = "unresolved"

	purposeEvidence   = "evidence"   // §3.1 item 1, before commitment
	purposeRecovery   = "recovery"   // taken by a new owner, or before a retry
	purposeCompletion = "completion" // §4: after every recorded attempt is accounted for
)

// uncertainStates hold the machine scope (§3.2 comparison 4, invariant 1).
const uncertainStates = `('committed','sending','verifying','unresolved')`

// Mode selects the protocol or one of its controls. Each control removes exactly one mechanism,
// so that its row can show the failure the mechanism prevents.
type Mode string

const (
	ModeProtocol Mode = "protocol"
	// ModeNaive checks the approval once, before the evidence, as a separate read (§3.1 alone):
	// neither the commitment nor the attempt transaction compares it again.
	ModeNaive Mode = "naive"
	// ModeNoFence records the attempt on the executor's own belief that it owns a committed
	// operation: no owner, generation or state comparison in the attempt transaction.
	ModeNoFence Mode = "nofence"
)

// Comparisons 1-6 are §3.2's. The rest are the §3.3 attempt-transaction checks, numbered here.
const (
	comparisonExists   = 0 // idempotency: an operation for this plan already exists
	comparisonOwner    = 7 // the recorder is the current owner at the recorded generation
	comparisonRetry    = 8 // nothing recorded after the safe-to-retry classification
	comparisonAttempts = 9 // attempts left under the bound maximum
	comparisonComplete = 10
)

// Refusal is a comparison that failed; nothing it guarded was committed.
type Refusal struct {
	Comparison int
	Reason     string
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("refused: comparison %d: %s", r.Comparison, r.Reason)
}

func refuse(c int, format string, a ...any) error {
	return &Refusal{Comparison: c, Reason: fmt.Sprintf(format, a...)}
}

type Store struct {
	db  *sql.DB
	log *Log
}

func OpenStore(ctx context.Context, dsn string, log *Log) (*Store, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, log: log}, nil
}

func (s *Store) Close() error { return s.db.Close() }

var schema = []string{
	`CREATE TABLE control (id int PRIMARY KEY CHECK (id = 1), epoch int NOT NULL, recovery boolean NOT NULL)`,
	`CREATE TABLE machines (id text PRIMARY KEY, assignment_rev int NOT NULL, baseline_rev int NOT NULL,
		applied_digest text NOT NULL, frozen boolean NOT NULL)`,
	`CREATE TABLE plans (id text PRIMARY KEY, machine text NOT NULL REFERENCES machines,
		artifact_path text NOT NULL, artifact_digest text NOT NULL, pre_digest text NOT NULL,
		assignment_rev int NOT NULL, baseline_rev int NOT NULL, operation text NOT NULL, mode text NOT NULL,
		expires_at timestamptz NOT NULL, max_attempts int NOT NULL, verify_ms bigint NOT NULL,
		max_age_ms bigint NOT NULL, transport_ms bigint NOT NULL)`,
	`CREATE TABLE approvals (plan_id text PRIMARY KEY REFERENCES plans, approved_at timestamptz NOT NULL,
		epoch int NOT NULL, revoked_at timestamptz, revoked_by text)`,
	`CREATE TABLE operations (id text PRIMARY KEY REFERENCES plans, machine text NOT NULL REFERENCES machines,
		state text NOT NULL, owner text NOT NULL, owner_gen int NOT NULL)`,
	// basis_rev is the operation's last timeline revision visible when the read began, so an
	// observation can be ordered after the facts it must follow (§4, completion observation).
	`CREATE TABLE observations (id bigserial PRIMARY KEY, op_id text NOT NULL, machine text NOT NULL,
		purpose text NOT NULL, digest text, error text, basis_rev bigint NOT NULL,
		read_began timestamptz NOT NULL, recorded_at timestamptz NOT NULL DEFAULT clock_timestamp())`,
	`CREATE TABLE attempts (op_id text NOT NULL REFERENCES operations, n int NOT NULL, owner text NOT NULL,
		owner_gen int NOT NULL, deadline timestamptz NOT NULL, recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
		response text, response_detail text, response_rev bigint,
		accounted_by text, accounted_evidence text, accounted_rev bigint, PRIMARY KEY (op_id, n))`,
	`CREATE TABLE timeline (rev bigserial PRIMARY KEY, op_id text NOT NULL, at timestamptz NOT NULL DEFAULT clock_timestamp(),
		actor text NOT NULL, kind text NOT NULL, detail text NOT NULL DEFAULT '')`,
}

// scopeIndex is comparison 4 (and 5: the PoC rollout scope is this one machine at limit one, so a
// rollout slot is the machine scope). The A-after-B control runs without it.
const scopeIndex = `CREATE UNIQUE INDEX operations_one_uncertain ON operations (machine) WHERE state IN ` + uncertainStates

// Setup gives the database an empty schema with one machine whose Applied digest is the one
// observed by the caller (an adoption baseline, §6), at assignment and baseline revision 1.
func (s *Store) Setup(ctx context.Context, machine, applied string, noScope bool) error {
	stmts := append([]string{`DROP SCHEMA public CASCADE`, `CREATE SCHEMA public`}, schema...)
	if !noScope {
		stmts = append(stmts, scopeIndex)
	}
	stmts = append(stmts, `INSERT INTO control VALUES (1, 1, false)`)
	for _, q := range stmts {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", strings.Fields(q)[0], err)
		}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO machines VALUES ($1, 1, 1, $2, false)`, machine, applied)
	return err
}

func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

type execer interface {
	QueryRowContext(ctx context.Context, q string, a ...any) *sql.Row
}

func timeline(ctx context.Context, x execer, op, actor, kind, detail string) (int64, error) {
	var rev int64
	err := x.QueryRowContext(ctx, `INSERT INTO timeline (op_id, actor, kind, detail) VALUES ($1, $2, $3, $4) RETURNING rev`,
		op, actor, kind, detail).Scan(&rev)
	return rev, err
}

// PlanSpec is what a plan binds that this prototype varies. The pre-dispatch digest, assignment
// and baseline revision are read from the machine when the plan is made.
type PlanSpec struct {
	ID, Machine, ArtifactPath, ArtifactDigest string
	Expires, Verify, MaxAge, Transport        time.Duration
	MaxAttempts                               int
}

type Plan struct {
	PlanSpec
	PreDigest     string
	AssignmentRev int
	BaselineRev   int
}

func (s *Store) Plan(ctx context.Context, p PlanSpec) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO plans SELECT $1, m.id, $3, $4, m.applied_digest, m.assignment_rev,
			m.baseline_rev, 'apply-config', 'no-reboot', clock_timestamp() + $5 * interval '1 millisecond',
			$6, $7, $8, $9 FROM machines m WHERE m.id = $2`,
			p.ID, p.Machine, p.ArtifactPath, p.ArtifactDigest, p.Expires.Milliseconds(), p.MaxAttempts,
			p.Verify.Milliseconds(), p.MaxAge.Milliseconds(), p.Transport.Milliseconds())
		if err != nil {
			return err
		}
		_, err = timeline(ctx, tx, p.ID, s.log.actor, "planned", "artifact="+short(p.ArtifactDigest))
		return err
	})
}

func (s *Store) LoadPlan(ctx context.Context, id string) (Plan, error) {
	var p Plan
	var verify, maxAge, transport int64
	var expires time.Time
	err := s.db.QueryRowContext(ctx, `SELECT id, machine, artifact_path, artifact_digest, pre_digest, assignment_rev,
		baseline_rev, expires_at, max_attempts, verify_ms, max_age_ms, transport_ms FROM plans WHERE id = $1`, id).
		Scan(&p.ID, &p.Machine, &p.ArtifactPath, &p.ArtifactDigest, &p.PreDigest, &p.AssignmentRev, &p.BaselineRev,
			&expires, &p.MaxAttempts, &verify, &maxAge, &transport)
	p.Verify = time.Duration(verify) * time.Millisecond
	p.MaxAge = time.Duration(maxAge) * time.Millisecond
	p.Transport = time.Duration(transport) * time.Millisecond
	return p, err
}

func (s *Store) Approve(ctx context.Context, plan string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO approvals SELECT $1, clock_timestamp(), epoch, NULL, NULL FROM control`, plan); err != nil {
			return err
		}
		_, err := timeline(ctx, tx, plan, s.log.actor, "approved", "")
		return err
	})
}

// ApprovalHolds is comparison 1 as a read on its own, the naive control's only approval check.
func (s *Store) ApprovalHolds(ctx context.Context, plan string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error { return checkApproval(ctx, tx, plan) })
}

// errResponded is a response already recorded for the attempt: after a commit whose outcome the
// client did not learn, the retry finds its own earlier write.
var errResponded = errors.New("attempt already has a response")

// Revoke takes the approval row's lock, so it waits for a commitment or attempt transaction that
// holds the row FOR SHARE and lands after it.
func (s *Store) Revoke(ctx context.Context, plan, actor string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE approvals SET revoked_at = clock_timestamp(), revoked_by = $2
			WHERE plan_id = $1 AND revoked_at IS NULL`, plan, actor)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return errors.New("no unrevoked approval")
		}
		_, err = timeline(ctx, tx, plan, actor, "revoked", "")
		return err
	})
}

// checkApproval is comparison 1, holding the approval row FOR SHARE until the transaction ends.
func checkApproval(ctx context.Context, tx *sql.Tx, plan string) error {
	var revoked sql.NullTime
	var epoch, current int
	var expired bool
	err := tx.QueryRowContext(ctx, `SELECT a.revoked_at, a.epoch, c.epoch, p.expires_at <= clock_timestamp()
		FROM approvals a JOIN plans p ON p.id = a.plan_id CROSS JOIN control c
		WHERE a.plan_id = $1 FOR SHARE OF a, c`, plan).Scan(&revoked, &epoch, &current, &expired)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return refuse(1, "not approved")
	case err != nil:
		return err
	case revoked.Valid:
		return refuse(1, "approval revoked")
	case expired:
		return refuse(1, "plan expired")
	case epoch != current:
		return refuse(1, "approval recorded in recovery epoch %d, current %d", epoch, current)
	}
	return nil
}

// checkMachine is comparisons 2 and 6.
func checkMachine(ctx context.Context, tx *sql.Tx, p Plan) error {
	var assignment, baseline int
	var frozen, recovery bool
	err := tx.QueryRowContext(ctx, `SELECT m.assignment_rev, m.baseline_rev, m.frozen, c.recovery
		FROM machines m CROSS JOIN control c WHERE m.id = $1 FOR SHARE OF m, c`, p.Machine).
		Scan(&assignment, &baseline, &frozen, &recovery)
	switch {
	case err != nil:
		return err
	case assignment != p.AssignmentRev || baseline != p.BaselineRev:
		return refuse(2, "machine at assignment %d baseline %d, plan bound %d/%d", assignment, baseline,
			p.AssignmentRev, p.BaselineRev)
	case frozen || recovery:
		return refuse(6, "scope gate closed")
	}
	return nil
}

// checkEvidence is comparison 3: this operation's latest successful evidence or recovery
// observation (on a retry, also completion), inside the bound age, with no newer observation of
// the machine contradicting it, showing the bound pre-dispatch digest or, on a retry, the bound
// artifact's (§3.2 item 3).
func checkEvidence(ctx context.Context, tx *sql.Tx, p Plan, retry bool) error {
	var id int64
	var digest string
	var stale bool
	// A retry may rest on the completion observation that showed the pre-dispatch digest.
	purposes := []string{purposeEvidence, purposeRecovery}
	if retry {
		purposes = append(purposes, purposeCompletion)
	}
	err := tx.QueryRowContext(ctx, `SELECT id, digest, read_began < clock_timestamp() - $2 * interval '1 millisecond'
		FROM observations WHERE op_id = $1 AND purpose = ANY($3) AND digest IS NOT NULL
		ORDER BY id DESC LIMIT 1`, p.ID, p.MaxAge.Milliseconds(), pq.Array(purposes)).Scan(&id, &digest, &stale)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return refuse(3, "no execution-time observation")
	case err != nil:
		return err
	case stale:
		return refuse(3, "observation %d older than the bound maximum age %s", id, p.MaxAge)
	case digest != p.PreDigest && !(retry && digest == p.ArtifactDigest):
		return refuse(3, "observed digest %s, bound pre-dispatch digest %s", short(digest), short(p.PreDigest))
	}
	var contradicting int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM observations WHERE machine = $1 AND id > $2
		AND digest IS NOT NULL AND digest <> $3`, p.Machine, id, digest).Scan(&contradicting); err != nil {
		return err
	}
	if contradicting > 0 {
		return refuse(3, "a newer observation contradicts observation %d", id)
	}
	return nil
}

// Commit is the §3.2 commitment transaction. Its COMMIT is the dispatch commitment boundary: a
// revocation committed before it prevents dispatch; one after it is recorded on the timeline and
// stops every attempt not yet recorded. beforeCommit runs inside the transaction, after every
// comparison, with the rows it compared still locked.
func (s *Store) Commit(ctx context.Context, plan, owner string, mode Mode, beforeCommit func()) (int, error) {
	p, err := s.LoadPlan(ctx, plan)
	if err != nil {
		return 0, err
	}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if mode != ModeNaive {
			if err := checkApproval(ctx, tx, plan); err != nil {
				return err
			}
		}
		if err := checkMachine(ctx, tx, p); err != nil {
			return err
		}
		if err := checkEvidence(ctx, tx, p, false); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO operations VALUES ($1, $2, 'committed', $3, 1)`, plan, p.Machine, owner)
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			if pqErr.Constraint == "operations_one_uncertain" {
				return refuse(4, "machine %s scope is held by another operation", p.Machine)
			}
			return refuse(comparisonExists, "an operation for plan %s exists", plan)
		}
		if err != nil {
			return err
		}
		if _, err := timeline(ctx, tx, plan, owner, "committed", fmt.Sprintf("owner=%s gen=1 mode=%s", owner, mode)); err != nil {
			return err
		}
		if beforeCommit != nil {
			beforeCommit()
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return 1, nil
}

// CancelUncommitted records that a plan whose approval failed comparison 1 before commitment is
// cancelled: no operation was committed and nothing was sent.
func (s *Store) CancelUncommitted(ctx context.Context, plan, actor, reason string) error {
	p, err := s.LoadPlan(ctx, plan)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO operations VALUES ($1, $2, 'cancelled', $3, 0) ON CONFLICT (id) DO NOTHING`,
			plan, p.Machine, actor)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errors.New("an operation exists; nothing to cancel before commitment")
		}
		_, err = timeline(ctx, tx, plan, actor, "cancelled", "before commitment: "+reason)
		return err
	})
}

type opRow struct {
	state, owner string
	gen          int
}

func lockOp(ctx context.Context, tx *sql.Tx, op string) (opRow, error) {
	var o opRow
	err := tx.QueryRowContext(ctx, `SELECT state, owner, owner_gen FROM operations WHERE id = $1 FOR UPDATE`, op).
		Scan(&o.state, &o.owner, &o.gen)
	return o, err
}

func setState(ctx context.Context, tx *sql.Tx, op, actor, state, detail string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE operations SET state = $2 WHERE id = $1`, op, state); err != nil {
		return err
	}
	_, err := timeline(ctx, tx, op, actor, state, detail)
	return err
}

// Attempt is the §3.3 attempt transaction. It repeats comparisons 1-3 and 6, confirms the recorder
// owns the operation at its generation, bounds the attempts, and on a retry refuses if anything
// was recorded after the classification at retryRev. It returns the attempt number.
func (s *Store) Attempt(ctx context.Context, op, owner string, gen int, mode Mode, retryRev int64, beforeCommit func()) (int, error) {
	p, err := s.LoadPlan(ctx, op)
	if err != nil {
		return 0, err
	}
	var n int
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		if mode != ModeNaive {
			if err := checkApproval(ctx, tx, op); err != nil {
				return err
			}
		}
		if err := checkMachine(ctx, tx, p); err != nil {
			return err
		}
		o, err := lockOp(ctx, tx, op)
		if err != nil {
			return err
		}
		if mode != ModeNoFence {
			if o.owner != owner || o.gen != gen {
				return refuse(comparisonOwner, "owner is %s at generation %d, not %s at %d", o.owner, o.gen, owner, gen)
			}
			if retryRev == 0 && o.state != stateCommitted || retryRev != 0 && o.state != stateUnresolved {
				return refuse(comparisonOwner, "operation is %s", o.state)
			}
		}
		if retryRev != 0 {
			var later int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM timeline WHERE op_id = $1 AND rev > $2
				AND kind IN ('response', 'ownership', 'classified', 'accounted')`, op, retryRev).Scan(&later); err != nil {
				return err
			}
			if later > 0 {
				return refuse(comparisonRetry, "%d facts recorded after the classification at revision %d", later, retryRev)
			}
		}
		if err := checkEvidence(ctx, tx, p, retryRev != 0); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM attempts WHERE op_id = $1`, op).Scan(&n); err != nil {
			return err
		}
		if n >= p.MaxAttempts {
			return refuse(comparisonAttempts, "%d of %d attempts used", n, p.MaxAttempts)
		}
		n++
		var deadline time.Time
		if err := tx.QueryRowContext(ctx, `INSERT INTO attempts (op_id, n, owner, owner_gen, deadline)
			VALUES ($1, $2, $3, $4, clock_timestamp() + $5 * interval '1 millisecond') RETURNING deadline`,
			op, n, owner, gen, p.Verify.Milliseconds()).Scan(&deadline); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE operations SET state = 'sending' WHERE id = $1`, op); err != nil {
			return err
		}
		if _, err := timeline(ctx, tx, op, owner, "attempt", fmt.Sprintf("n=%d owner=%s gen=%d deadline=%s mode=%s",
			n, owner, gen, deadline.UTC().Format(time.RFC3339Nano), mode)); err != nil {
			return err
		}
		if beforeCommit != nil {
			beforeCommit()
		}
		return nil
	})
	var r *Refusal
	if errors.As(err, &r) && (r.Comparison == 1 || r.Comparison == 6) {
		// A revocation, expiry or closed gate after commitment: the owner records it (§4,
		// committed -> unresolved) and, if no attempt ever committed and none can, cancels.
		if serr := s.stopAfterRefusal(ctx, op, owner, gen, r); serr != nil {
			return 0, errors.Join(err, serr)
		}
	}
	return n, err
}

func (s *Store) stopAfterRefusal(ctx context.Context, op, owner string, gen int, r *Refusal) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		o, err := lockOp(ctx, tx, op)
		if err != nil || o.owner != owner || o.gen != gen {
			return err
		}
		if o.state == stateCommitted || o.state == stateSending || o.state == stateVerifying {
			if err := setState(ctx, tx, op, owner, stateUnresolved, r.Reason); err != nil {
				return err
			}
		}
		var attempts int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM attempts WHERE op_id = $1`, op).Scan(&attempts); err != nil {
			return err
		}
		if attempts == 0 && r.Comparison == 1 {
			return setState(ctx, tx, op, owner, stateCancelled, "no attempt ever committed and none can: "+r.Reason)
		}
		return nil
	})
}

type AttemptRow struct {
	N        int
	Owner    string
	Gen      int
	Deadline time.Time
	Response sql.NullString
	Account  sql.NullString
}

func (a AttemptRow) accounted() bool {
	return a.Response.Valid && a.Response.String != respUnknown || a.Account.Valid
}

func (s *Store) Attempts(ctx context.Context, op string) ([]AttemptRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT n, owner, owner_gen, deadline, response, accounted_evidence
		FROM attempts WHERE op_id = $1 ORDER BY n`, op)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AttemptRow
	for rows.Next() {
		var a AttemptRow
		if err := rows.Scan(&a.N, &a.Owner, &a.Gen, &a.Deadline, &a.Response, &a.Account); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// RecordResponse records what the sender learned. The response is a fact about the attempt and is
// recorded whoever owns the operation now; only the current owner moves the state.
func (s *Store) RecordResponse(ctx context.Context, op string, n int, owner string, gen int, r Response) (string, error) {
	var state string
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		o, err := lockOp(ctx, tx, op)
		if err != nil {
			return err
		}
		state = o.state
		rev, err := timeline(ctx, tx, op, owner, "response", fmt.Sprintf("n=%d class=%s gen=%d elapsed_ms=%d %s",
			n, r.Class, gen, r.Elapsed.Milliseconds(), r.Detail))
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE attempts SET response = $3, response_detail = $4, response_rev = $5
			WHERE op_id = $1 AND n = $2 AND response IS NULL`, op, n, r.Class, r.Detail, rev)
		if err != nil {
			return err
		}
		if k, _ := res.RowsAffected(); k != 1 {
			return fmt.Errorf("attempt %d: %w", n, errResponded)
		}
		if o.state != stateSending || o.owner != owner || o.gen != gen {
			return nil
		}
		switch r.Class {
		case respAccepted:
			state = stateVerifying
		case respRejected:
			var others int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM attempts WHERE op_id = $1 AND n <> $2
				AND response IS DISTINCT FROM 'rejected'`, op, n).Scan(&others); err != nil {
				return err
			}
			state = stateRejected
			if others > 0 {
				state = stateUnresolved
			}
		default:
			state = stateUnresolved
		}
		return setState(ctx, tx, op, owner, state, fmt.Sprintf("attempt %d %s", n, r.Class))
	})
	return state, err
}

// Account records evidence that an attempt's sender can no longer send (§4, accounted for).
// What counts as such evidence is what this experiment measures; the caller supplies it.
func (s *Store) Account(ctx context.Context, op string, n int, actor, evidence string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := lockOp(ctx, tx, op); err != nil {
			return err
		}
		rev, err := timeline(ctx, tx, op, actor, "accounted", fmt.Sprintf("n=%d %s", n, evidence))
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE attempts SET accounted_by = $3, accounted_evidence = $4, accounted_rev = $5
			WHERE op_id = $1 AND n = $2 AND accounted_rev IS NULL`, op, n, actor, evidence, rev)
		if err != nil {
			return err
		}
		if k, _ := res.RowsAffected(); k != 1 {
			return fmt.Errorf("attempt %d does not exist or is already accounted for", n)
		}
		return nil
	})
}

// Takeover moves ownership to a new owner at the next generation. An operation in flight becomes
// unresolved (§4: ownership loss).
func (s *Store) Takeover(ctx context.Context, op, owner string) (int, error) {
	var gen int
	var state string
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `UPDATE operations SET owner = $2, owner_gen = owner_gen + 1,
			state = CASE WHEN state IN ('committed','sending','verifying') THEN 'unresolved' ELSE state END
			WHERE id = $1 AND state IN `+uncertainStates+` RETURNING owner_gen, state`, op, owner).Scan(&gen, &state)
		if errors.Is(err, sql.ErrNoRows) {
			return refuse(comparisonOwner, "no operation %s in a non-terminal state", op)
		}
		if err != nil {
			return err
		}
		_, err = timeline(ctx, tx, op, owner, "ownership", fmt.Sprintf("owner=%s gen=%d state=%s", owner, gen, state))
		return err
	})
	return gen, err
}

// Observation is one recorded read-back. Digest is empty when the read failed.
type Observation struct {
	ID       int64
	Digest   string
	BasisRev int64
	Err      error
}

// Observe takes the basis first, then reads, then records: the read began after every fact at or
// below the basis revision was committed.
func (s *Store) Observe(ctx context.Context, op, machine, purpose string, read func(context.Context) (string, error)) (Observation, error) {
	var o Observation
	var began time.Time
	if err := s.db.QueryRowContext(ctx, `SELECT coalesce(max(rev), 0), clock_timestamp() FROM timeline WHERE op_id = $1`,
		op).Scan(&o.BasisRev, &began); err != nil {
		return o, err
	}
	o.Digest, o.Err = read(ctx)
	var digest, readErr sql.NullString
	if o.Err != nil {
		readErr = sql.NullString{String: o.Err.Error(), Valid: true}
	} else {
		digest = sql.NullString{String: o.Digest, Valid: true}
	}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `INSERT INTO observations (op_id, machine, purpose, digest, error, basis_rev, read_began)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, op, machine, purpose, digest, readErr, o.BasisRev, began).Scan(&o.ID); err != nil {
			return err
		}
		detail := fmt.Sprintf("id=%d purpose=%s basis=%d digest=%s", o.ID, purpose, o.BasisRev, short(o.Digest))
		if o.Err != nil {
			detail = fmt.Sprintf("id=%d purpose=%s basis=%d read failed", o.ID, purpose, o.BasisRev)
		}
		_, err := timeline(ctx, tx, op, s.log.actor, "observation", detail)
		return err
	})
	return o, err
}

// RecordObservation records a reading taken by the caller (the tests), with the basis taken now.
func (s *Store) RecordObservation(ctx context.Context, op, machine, purpose, digest string, readErr error) (int64, error) {
	o, err := s.Observe(ctx, op, machine, purpose, func(context.Context) (string, error) { return digest, readErr })
	return o.ID, err
}

// accountedBasis is the revision every attempt's accounting was recorded at, or a refusal naming
// the first attempt that is not accounted for.
func accountedBasis(ctx context.Context, tx *sql.Tx, op string) (int64, int, error) {
	var unaccounted, total int
	var basis int64
	err := tx.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE NOT ((response IS NOT NULL AND response <> 'unknown')
		OR accounted_rev IS NOT NULL)), count(*), coalesce(max(greatest(coalesce(response_rev, 0), coalesce(accounted_rev, 0))), 0)
		FROM attempts WHERE op_id = $1`, op).Scan(&unaccounted, &total, &basis)
	if err != nil {
		return 0, 0, err
	}
	if unaccounted > 0 {
		return 0, total, refuse(comparisonComplete, "%d of %d attempts are not accounted for", unaccounted, total)
	}
	return basis, total, nil
}

// latestAfter is the operation's latest successful observation of one of the purposes whose read
// began after basis.
func latestAfter(ctx context.Context, tx *sql.Tx, op string, basis int64, purposes ...string) (int64, string, error) {
	var id int64
	var digest string
	err := tx.QueryRowContext(ctx, `SELECT id, digest FROM observations WHERE op_id = $1 AND basis_rev >= $2
		AND digest IS NOT NULL AND purpose = ANY($3) ORDER BY id DESC LIMIT 1`, op, basis, pq.Array(purposes)).Scan(&id, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", refuse(comparisonComplete, "no successful %s observation after revision %d", strings.Join(purposes, "/"), basis)
	}
	return id, digest, err
}

// Complete classifies from a completion observation taken after every attempt was accounted for:
// the bound artifact's digest completes the operation and updates Applied; another digest fails it.
func (s *Store) Complete(ctx context.Context, op, owner string, gen int) (string, error) {
	p, err := s.LoadPlan(ctx, op)
	if err != nil {
		return "", err
	}
	var state string
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		o, err := lockOp(ctx, tx, op)
		if err != nil {
			return err
		}
		if o.owner != owner || o.gen != gen {
			return refuse(comparisonOwner, "owner is %s at generation %d", o.owner, o.gen)
		}
		if o.state != stateVerifying && o.state != stateUnresolved {
			return refuse(comparisonOwner, "operation is %s", o.state)
		}
		basis, total, err := accountedBasis(ctx, tx, op)
		if err != nil {
			return err
		}
		if total == 0 {
			return refuse(comparisonComplete, "no attempt was ever recorded")
		}
		id, digest, err := latestAfter(ctx, tx, op, basis, purposeCompletion)
		if err != nil {
			return err
		}
		if digest != p.ArtifactDigest {
			state = stateFailed
			return setState(ctx, tx, op, owner, state, fmt.Sprintf("observation %d digest %s, bound artifact %s",
				id, short(digest), short(p.ArtifactDigest)))
		}
		if _, err := tx.ExecContext(ctx, `UPDATE machines SET applied_digest = $2, baseline_rev = baseline_rev + 1
			WHERE id = $1`, p.Machine, p.ArtifactDigest); err != nil {
			return err
		}
		state = stateCompleted
		return setState(ctx, tx, op, owner, state, fmt.Sprintf("observation %d matches; Applied updated", id))
	})
	return state, err
}

// ClassifySafeToRetry records that a retry is safe: every attempt accounted for, none accepted,
// and an observation taken after that shows the bound pre-dispatch digest. The returned revision
// binds the retry's attempt transaction.
func (s *Store) ClassifySafeToRetry(ctx context.Context, op, owner string, gen int, reason string) (int64, error) {
	p, err := s.LoadPlan(ctx, op)
	if err != nil {
		return 0, err
	}
	var rev int64
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		o, err := lockOp(ctx, tx, op)
		if err != nil {
			return err
		}
		if o.owner != owner || o.gen != gen || o.state != stateUnresolved {
			return refuse(comparisonOwner, "operation is %s, owned by %s at %d", o.state, o.owner, o.gen)
		}
		basis, _, err := accountedBasis(ctx, tx, op)
		if err != nil {
			return err
		}
		var accepted int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM attempts WHERE op_id = $1 AND response = 'accepted'`,
			op).Scan(&accepted); err != nil {
			return err
		}
		if accepted > 0 {
			return refuse(comparisonRetry, "an attempt was accepted")
		}
		id, digest, err := latestAfter(ctx, tx, op, basis, purposeRecovery, purposeCompletion)
		if err != nil {
			return err
		}
		if digest != p.PreDigest {
			return refuse(comparisonRetry, "observation %d digest %s is not the pre-dispatch digest", id, short(digest))
		}
		rev, err = timeline(ctx, tx, op, owner, "classified", fmt.Sprintf("safe-to-retry observation=%d %s", id, reason))
		return err
	})
	return rev, err
}

// MarkUnresolved is the owner's stop when evidence cannot establish a terminal state.
func (s *Store) MarkUnresolved(ctx context.Context, op, owner string, gen int, reason string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		o, err := lockOp(ctx, tx, op)
		if err != nil || o.owner != owner || o.gen != gen {
			return err
		}
		if o.state != stateCommitted && o.state != stateSending && o.state != stateVerifying {
			return nil
		}
		return setState(ctx, tx, op, owner, stateUnresolved, reason)
	})
}

// CancelUnattempted is §4's unresolved -> cancelled: no attempt ever committed, and comparison 1
// now fails, so none can.
func (s *Store) CancelUnattempted(ctx context.Context, op, owner string, gen int) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		o, err := lockOp(ctx, tx, op)
		if err != nil {
			return err
		}
		if o.owner != owner || o.gen != gen || o.state != stateUnresolved {
			return refuse(comparisonOwner, "operation is %s, owned by %s at %d", o.state, o.owner, o.gen)
		}
		var attempts int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM attempts WHERE op_id = $1`, op).Scan(&attempts); err != nil {
			return err
		}
		if attempts > 0 {
			return refuse(comparisonAttempts, "%d attempts were recorded", attempts)
		}
		err = checkApproval(ctx, tx, op)
		var r *Refusal
		if !errors.As(err, &r) {
			return refuse(1, "the approval still holds")
		}
		return setState(ctx, tx, op, owner, stateCancelled, "no attempt ever committed and none can: "+r.Reason)
	})
}

// Op is the operation's current projection.
func (s *Store) Op(ctx context.Context, op string) (state, owner string, gen int, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT state, owner, owner_gen FROM operations WHERE id = $1`, op).Scan(&state, &owner, &gen)
	return
}

func (s *Store) State(ctx context.Context, op string) (string, error) {
	state, _, _, err := s.Op(ctx, op)
	return state, err
}

func (s *Store) Timeline(ctx context.Context, op, kind, detail string) error {
	_, err := timeline(ctx, s.db, op, s.log.actor, kind, detail)
	return err
}

func short(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}
