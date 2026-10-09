package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// commitRefused is a commitment refused by one of execution-recovery.md §3.2's comparisons, named
// by its number ("1" to "6"), with what failed and, for comparison 3, the observation that showed
// it. Comparison 0 fails only on a plan already committed, which is not a refusal
// (errAlreadyCommitted).
type commitRefused struct {
	Comparison, Cause, Observation string
}

func (r *commitRefused) Error() string {
	return fmt.Sprintf("commitment refused by comparison %s: %s", r.Comparison, r.Cause)
}

func refuseCommitment(comparison, cause string) error {
	return &commitRefused{Comparison: comparison, Cause: cause}
}

// errAlreadyCommitted is a commitment not attempted because the plan already has its operation,
// committed by an attempt that took the machine's lock first: the plan's outcome, not a refusal
// (§3.2 comparison 0, DS row 009).
var errAlreadyCommitted = errors.New("the plan is already committed")

// committed is what a commitment created: the plan, its operation, the evidence observation it
// linked and the commitment entry's revision.
type committed struct {
	Plan, Operation, Observation string
	Revision                     int64
}

// commitPlan commits an approved apply-config plan (T6, execution-recovery.md §3.2,
// persistence-api.md §5) as this process, the controller of the current epoch. It holds, in
// persistence-api.md §5 rule 5's order, the installation state FOR SHARE, the machine FOR UPDATE,
// its assignment head, the approver's principal and the approval FOR SHARE, the plan's state FOR
// UPDATE and the cluster's rollout lock (choice §10.29); reads the time after the last lock (rule
// 4); compares 0 to 6 in order; and creates the plan's apply-config operation in committed, owned
// by this process at generation 1 in the current epoch, commits the plan to it and appends the
// commitment entry. A refusal rolls back and is
// recorded as a refusal entry by a separate transaction (§4.1). It sends nothing: §3.1 item 2's
// use-time check and the attempt are the dispatch's (ginsys/bronzeward#26).
func (a *API) commitPlan(ctx context.Context, plan string) (committed, error) {
	if a.d.owner.ID == "" {
		return committed{}, errNotController
	}
	var r committed
	var machine string
	err := a.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		r, machine, err = a.commitTx(ctx, tx, plan)
		return err
	})
	var refused *commitRefused
	if !errors.As(err, &refused) || machine == "" {
		return r, err
	}
	switch rerr := a.recordCommitRefusal(ctx, machine, plan, refused); {
	case errors.Is(rerr, errAlreadyCommitted): // committed by another attempt since this one rolled back
		return committed{}, rerr
	case rerr != nil:
		// Only a recorded refusal answers *commitRefused: an unrecorded one is attempted again.
		return committed{}, fmt.Errorf("%v, but its refusal entry was not recorded: %w", err, rerr)
	}
	return committed{}, err
}

// commitTx is commitPlan's transaction. It answers the plan's machine once known, so a refusal can
// be recorded on its timeline.
func (a *API) commitTx(ctx context.Context, tx *sql.Tx, plan string) (committed, string, error) {
	r := committed{Plan: plan}
	var current string
	var recovery bool
	if err := tx.QueryRowContext(ctx, `SELECT epoch, recovery_mode FROM installation_state FOR SHARE`).Scan(&current,
		&recovery); err != nil {
		return r, "", err
	}
	if current != a.d.owner.Epoch {
		return r, "", errNotController
	}
	// A plan row and its release are immutable, so their bindings are read before the machine's lock.
	var machine, cluster, kind, assignment, contract string
	var desired sql.NullString
	var baseline sql.NullInt64
	var maxAgeMicros int64
	err := tx.QueryRowContext(ctx, `SELECT p.machine, p.cluster, p.kind, p.assignment_revision, p.desired_release,
			p.baseline_revision, (extract(epoch FROM p.max_observation_age) * 1000000)::bigint, r.contract
		FROM plan p JOIN release r ON r.id = p.release WHERE p.id = $1`, plan).Scan(&machine, &cluster, &kind, &assignment,
		&desired, &baseline, &maxAgeMicros, &contract)
	if err != nil {
		return r, "", fmt.Errorf("commitment of plan %s: %w", plan, err)
	}
	if kind != "apply-config" {
		return r, "", fmt.Errorf("commitment of plan %s: a plan of kind %s", plan, kind)
	}
	maxAge := time.Duration(maxAgeMicros) * time.Microsecond
	// The machine's lock is the one a publication's selection takes (publish.go), so Desired, read
	// under it, is the one a publication committed before it or will change only after it.
	lock := ` FOR UPDATE`
	if a.o.noMachineLock {
		lock = ``
	}
	var scope string
	var frozen bool
	if err := tx.QueryRowContext(ctx, `SELECT scope_state, frozen FROM machine WHERE id = $1`+lock, machine).Scan(&scope,
		&frozen); err != nil {
		return r, machine, err
	}
	var msDesired sql.NullString
	var msBaseline sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT desired, baseline_revision FROM machine_state WHERE machine = $1`,
		machine).Scan(&msDesired, &msBaseline); err != nil {
		return r, machine, err
	}
	var head sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT head_revision_id FROM assignment WHERE machine = $1 FOR SHARE`, machine).Scan(&head)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return r, machine, err
	}
	// Every writer of a plan's state holds its machine first, so the approval read here is the one
	// the state lock below reads.
	var approval, approver sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT s.approval, a.approver FROM plan_state s LEFT JOIN approval a ON a.id = s.approval
		WHERE s.plan = $1`, plan).Scan(&approval, &approver); err != nil {
		return r, machine, err
	}
	if approver.Valid {
		if _, err := tx.ExecContext(ctx, `SELECT FROM principal WHERE id = $1 FOR SHARE`, approver.String); err != nil {
			return r, machine, err
		}
	}
	var approvalEpoch string
	var approvalRevision int64
	if approval.Valid {
		if err := tx.QueryRowContext(ctx, `SELECT epoch, revision FROM approval WHERE id = $1 FOR SHARE`,
			approval.String).Scan(&approvalEpoch, &approvalRevision); err != nil {
			return r, machine, err
		}
	}
	var state string
	var operation sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT state, operation FROM plan_state WHERE plan = $1 FOR UPDATE`, plan).Scan(&state,
		&operation); err != nil {
		return r, machine, err
	}
	// The cluster's rollout lock, which only commitments take, last: another machine's commitment
	// holding the rollout scope uncommitted is waited for here, before the time is read, not at the
	// operation's insert after it.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1, hashtext($2))`, rolloutLockClass,
		cluster); err != nil {
		return r, machine, err
	}
	// PA §5 rule 4: the commitment's time, and the expiry and age judged against it, follow every
	// lock. The plan's state is the one §8.1 reads at that time, so a refusal names what a reader sees.
	var at time.Time
	var reads string
	if err := tx.QueryRowContext(ctx, `SELECT c.t,
			CASE WHEN s.state = 'approved' AND ap.revoked AND (r.at IS NULL OR r.at < p.expires_at) THEN 'revoked'
				WHEN s.state IN ('proposed', 'approved') AND p.expires_at <= c.t THEN 'expired' ELSE s.state END
		FROM plan p CROSS JOIN (SELECT clock_timestamp() AS t) c JOIN plan_state s ON s.plan = p.id
			LEFT JOIN approval a ON a.id = s.approval LEFT JOIN principal ap ON ap.id = a.approver
			LEFT JOIN identity_revocation r ON r.identity = ap.id
		WHERE p.id = $1`, plan).Scan(&at, &reads); err != nil {
		return r, machine, err
	}

	// 0: no operation exists for the plan. One that exists was committed by an attempt that took the
	// machine's lock first: the plan's outcome, rolled back without a refusal entry.
	if operation.Valid || state == "committed" {
		return r, machine, fmt.Errorf("commitment of plan %s: %w", plan, errAlreadyCommitted)
	}
	// 1: approved, unexpired, not cancelled, the approval not revoked, its identity not revoked, and
	// the approval recorded in the current epoch.
	switch {
	case reads == "expired":
		return r, machine, refuseCommitment("1", "the plan has expired")
	case reads == "revoked" && state == "approved":
		return r, machine, refuseCommitment("1", "the approver's identity is revoked")
	case reads != "approved":
		return r, machine, refuseCommitment("1", "the plan is "+reads)
	case approvalEpoch != current:
		return r, machine, refuseCommitment("1", "the approval is of an earlier epoch")
	}
	// 2: the assignment head, the baseline revision and Desired are the bound ones. The plan row
	// fixes the artifact, operation, mode and parameters, and keeps its route through an endpoint
	// change (persistence-api.md §3.3).
	switch {
	case !head.Valid || head.String != assignment:
		return r, machine, refuseCommitment("2", "the machine's assignment revision changed")
	case msBaseline != baseline:
		return r, machine, refuseCommitment("2", "the machine's baseline revision changed")
	case msDesired != desired:
		return r, machine, refuseCommitment("2", "the machine's Desired release changed")
	}
	// 3: the evidence is the latest observation recorded for this plan's dispatch by this process
	// (§3.1 item 1), begun after the approval and no older than the plan binds (measured from its
	// start entry, when the read began). A start with no recorded observation does not count.
	// Evidence that is missing, begun before the approval, too old or left the identity, the
	// configuration or the running version unread is no evidence: nothing is refused, and fresh
	// evidence decides (as an adoption's, choice §10.27). The PoC binds no health precondition.
	var obs sql.NullString
	var basis int64
	var startedAt time.Time
	var identityMatches, digestMatches, minorMatches bool
	var unread sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT o.id, o.basis, se.at,
			(SELECT string_agg(k, ', ' ORDER BY k) FROM jsonb_object_keys(o.unread) k
				WHERE k IN ('identity', 'configuration', 'runningVersion')),
			`+identityMatch+`, o.configuration_digest IS NOT DISTINCT FROM p.expected_digest, `+minorMatch+`
		FROM observation o JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis
			JOIN machine_event se ON se.machine = o.machine AND se.revision = o.basis
			JOIN plan p ON p.id = s.plan JOIN release rl ON rl.id = p.release
			JOIN machine m ON m.id = o.machine JOIN cluster c ON c.id = m.cluster
		WHERE o.machine = $1 AND s.purpose = 'evidence' AND s.plan = $2 AND s.controller = $3
		ORDER BY o.basis DESC LIMIT 1`, machine, plan, a.d.owner.ID).Scan(&obs, &basis, &startedAt, &unread, &identityMatches,
		&digestMatches, &minorMatches)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return r, machine, err
	}
	switch {
	case !obs.Valid:
		return r, machine, fmt.Errorf("commitment of plan %s: no evidence is recorded for it: %w", plan, errNoEvidence)
	case unread.Valid:
		return r, machine, fmt.Errorf("commitment of plan %s: evidence %s did not read the %s: %w", plan, obs.String,
			unread.String, errNoEvidence)
	case basis <= approvalRevision:
		return r, machine, fmt.Errorf("commitment of plan %s: evidence %s began before the approval: %w", plan, obs.String,
			errNoEvidence)
	case at.Sub(startedAt) > maxAge:
		return r, machine, fmt.Errorf("commitment of plan %s: evidence %s is older than the plan allows: %w", plan,
			obs.String, errNoEvidence)
	}
	// The evidence's identity, digest and running minor, then any later observation, of any purpose
	// and by any process, that read one of them otherwise. A refusal names the observation that
	// showed it, not the digest: no read answers a digest (choice §17.38).
	newer, err := newerContradiction(ctx, tx, machine, plan, basis)
	if err != nil {
		return r, machine, err
	}
	refused := func(cause, observation string) error {
		return &commitRefused{Comparison: "3", Cause: cause, Observation: observation}
	}
	switch {
	case !identityMatches:
		return r, machine, refused("the evidence shows another machine identity", obs.String)
	case !digestMatches:
		return r, machine, refused("the evidence shows a configuration digest other than the plan's", obs.String)
	case !minorMatches:
		return r, machine, refused("the evidence shows a running Talos minor other than the release's contract", obs.String)
	case newer.Valid:
		return r, machine, refused("a newer observation contradicts the evidence", newer.String)
	}
	// 4 and 5: no operation holds the machine scope or the cluster's rollout scope. The machine's
	// lock orders 4 and the rollout lock 5. Only a commitment enters the rollout scope, so the scope's
	// unique index, which still refuses a second holder, is not reached.
	var machineHeld, rolloutHeld bool
	if err := tx.QueryRowContext(ctx, `SELECT
			EXISTS (SELECT FROM operation WHERE machine = $1 AND kind = 'apply-config'
				AND state IN ('committed', 'sending', 'verifying', 'unresolved')),
			EXISTS (SELECT FROM operation WHERE cluster = $2 AND kind = 'apply-config'
				AND state IN ('committed', 'sending', 'verifying', 'unresolved'))`, machine, cluster).Scan(&machineHeld,
		&rolloutHeld); err != nil {
		return r, machine, err
	}
	switch {
	case machineHeld:
		return r, machine, refuseCommitment("4", "an operation holds the machine scope")
	case rolloutHeld:
		return r, machine, refuseCommitment("5", rolloutCause)
	}
	// 6: the scope gate.
	if cause := scopeGate(frozen, scope, recovery); cause != "" {
		return r, machine, refuseCommitment("6", cause)
	}

	r.Operation, r.Observation = id.New(id.Operation), obs.String
	_, err = tx.ExecContext(ctx, `INSERT INTO operation (id, kind, state, epoch, owner, owner_gen, owner_epoch, plan, machine,
			cluster, created_at)
		VALUES ($1, 'apply-config', 'committed', $2, $3, 1, $2, $4, $5, $6, $7)`,
		r.Operation, current, a.d.owner.ID, plan, machine, cluster, at)
	if err != nil {
		return r, machine, err
	}
	if err := tx.QueryRowContext(ctx, `UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1
		RETURNING revision_counter`, machine).Scan(&r.Revision); err != nil {
		return r, machine, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		VALUES ($1, $2, $3, 'commitment', jsonb_build_object('plan', $4::text, 'operation', $5::text, 'approval', $6::text,
			'observation', $7::text, 'controller', $8::text, 'generation', 1,
			'comparisons', jsonb_build_array('0', '1', '2', '3', '4', '5', '6')), $9)`,
		machine, r.Revision, current, plan, r.Operation, approval.String, r.Observation, a.d.owner.ID, at); err != nil {
		return r, machine, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE plan_state SET state = 'committed', operation = $2, revision = revision + 1,
		updated_at = $3 WHERE plan = $1`, plan, r.Operation, at)
	return r, machine, err
}

const rolloutCause = "an operation holds the cluster's rollout scope"

// rolloutLockClass is the rollout lock's class in the two-int4 advisory key space; the cluster's
// hashed id is its object. Two clusters whose ids hash alike only share a lock.
const rolloutLockClass = 0x62777273 // "bwrs"

// identityMatch and minorMatch compare an observation o of machine m in cluster c with what they
// bind (§3.2 comparison 3, choice §10.26): the identity under the machine's identity key and the
// cluster's Talos ID; the running minor with release rl's contract.
const (
	identityMatch = `(CASE WHEN m.smbios_uuid IS NOT NULL THEN o.smbios_uuid IS NOT DISTINCT FROM m.smbios_uuid
			ELSE o.talos_node_id IS NOT DISTINCT FROM m.talos_node_id AND o.smbios_uuid IS NULL END
		AND o.talos_cluster_id IS NOT DISTINCT FROM c.talos_cluster_id)`
	minorMatch = `(substring(o.running_version FROM '^v[0-9]+\.[0-9]+') IS NOT DISTINCT FROM rl.contract)`
)

// newerContradiction answers the first observation of machine after basis, of any purpose and by
// any process, that read the identity, the configuration digest or the running minor otherwise
// than plan binds (§3.2 and §3.3 comparison 3).
func newerContradiction(ctx context.Context, tx *sql.Tx, machine, plan string, basis int64) (sql.NullString, error) {
	var newer sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT o.id FROM observation o JOIN machine m ON m.id = o.machine
			JOIN cluster c ON c.id = m.cluster JOIN plan p ON p.id = $2 JOIN release rl ON rl.id = p.release
		WHERE o.machine = $1 AND o.basis > $3 AND (
			(o.unread->'identity' IS NULL AND NOT (`+identityMatch+`))
			OR (o.unread->'configuration' IS NULL AND o.configuration_digest IS DISTINCT FROM p.expected_digest)
			OR (o.unread->'runningVersion' IS NULL AND NOT `+minorMatch+`))
		ORDER BY o.basis LIMIT 1`, machine, plan, basis).Scan(&newer)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return newer, err
}

// scopeGate is §3.2 and §3.3 comparison 6's cause, or "" when the gate passes. No drift record
// exists before ginsys/bronzeward#27, so its terms hold.
func scopeGate(frozen bool, scope string, recovery bool) string {
	switch {
	case frozen:
		return "the machine scope is frozen"
	case scope != "normal" && scope != "released":
		return "the machine scope is " + scope
	case recovery && scope != "released":
		return "recovery mode is in effect and the scope is not released"
	}
	return ""
}

// recordCommitRefusal is a refused commitment's refusal entry (execution-recovery.md §4.1),
// recorded after the refused transaction rolled back. Unlike an adoption's, a commitment's refusal
// is not the plan's outcome: each refused attempt records one, unless the plan was committed since.
func (a *API) recordCommitRefusal(ctx context.Context, machine, plan string, refused *commitRefused) error {
	return a.recordRefusalEntry(ctx, machine, plan, "commitment", refused.Comparison, refused.Cause, refused.Observation,
		errAlreadyCommitted, false)
}
