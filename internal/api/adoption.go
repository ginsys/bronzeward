package api

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// adoptionRefused is an adoption record refused by one of execution-recovery.md §6.3's step-4
// requirements, named by its number ("4.2" to "4.5"), with what failed. Requirement 4.1 fails only on
// an adoption already recorded, which is not a refusal (errAlreadyAdopted).
type adoptionRefused struct {
	Comparison, Cause string
}

func (r *adoptionRefused) Error() string {
	return fmt.Sprintf("adoption refused by requirement %s: %s", r.Comparison, r.Cause)
}

// errNoEvidence is an adoption record not attempted for want of evidence: the machine has no
// recorded observation, or the one the record relies on began before the approval, is older than
// the plan allows or left the identity, the configuration or the assignment evidence unread. A
// fresh observation can supply it, so nothing is refused (§6.3 step 4, choice §10.27).
var errNoEvidence = errors.New("no evidence for the adoption")

// errAlreadyAdopted is an adoption record not attempted because the plan's adoption is already
// recorded, by another attempt that took the machine's lock first: the plan's outcome, not a
// refusal (§6.3 step 4 requirement 1, choice §10.27).
var errAlreadyAdopted = errors.New("the plan's adoption is already recorded")

// errAlreadyRefused is an adoption record not attempted because the plan's refusal entry is already
// recorded: a refusal is the plan's outcome, so a later attempt on matching evidence writes nothing
// (§6.3 step 4, choice §10.27).
var errAlreadyRefused = errors.New("the plan's adoption was refused")

func refuseAdoption(comparison, cause string) error {
	return &adoptionRefused{Comparison: comparison, Cause: cause}
}

// adopted is what an adoption record committed: the plan, its completed operation, the
// observation it relied on and the adoption entry's revision.
type adopted struct {
	Plan, Operation, Observation string
	Revision                     int64
}

// commitAdoption records an approved adopt plan's adoption (T6, execution-recovery.md §6.3 step 4,
// persistence-api.md §5) as this process, the controller of the current epoch. It holds, in
// persistence-api.md §5 rule 5's order, the installation state FOR SHARE, the machine FOR UPDATE,
// its assignment head, the approver's principal and the approval FOR SHARE and the plan's state FOR
// UPDATE; reads the time after the last lock (rule 4); checks requirements 4.1 to 4.5 in order;
// and creates the plan's adopt operation directly completed with its adoption record, commits the
// plan, sets Applied to the adopted release with the baseline's digest and Desired to that release,
// starts the baseline revision and appends the adoption and Applied-change entries. A refusal rolls
// back and is recorded as a refusal entry by a separate transaction (§4.1). It sends nothing.
func (a *API) commitAdoption(ctx context.Context, plan string) (adopted, error) {
	if a.d.owner.ID == "" {
		return adopted{}, errNotController
	}
	var r adopted
	var machine string
	err := a.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		r, machine, err = a.adoptionTx(ctx, tx, plan)
		return err
	})
	var refused *adoptionRefused
	if !errors.As(err, &refused) || machine == "" {
		return r, err
	}
	switch rerr := a.recordRefusal(ctx, machine, plan, refused); {
	case errors.Is(rerr, errAlreadyAdopted): // adopted by another attempt since this one rolled back
		return adopted{}, rerr
	case rerr != nil:
		// Only a recorded refusal answers *adoptionRefused: an unrecorded one is attempted again.
		return adopted{}, fmt.Errorf("%v, but its refusal entry was not recorded: %w", err, rerr)
	}
	return adopted{}, err
}

// adoptionTx is commitAdoption's transaction. It answers the plan's machine once known, so a
// refusal can be recorded on its timeline.
func (a *API) adoptionTx(ctx context.Context, tx *sql.Tx, plan string) (adopted, string, error) {
	r := adopted{Plan: plan}
	var current string
	var recovery bool
	if err := tx.QueryRowContext(ctx, `SELECT epoch, recovery_mode FROM installation_state FOR SHARE`).Scan(&current,
		&recovery); err != nil {
		return r, "", err
	}
	if current != a.d.owner.Epoch {
		return r, "", errNotController
	}
	// A plan row is immutable, so its binding is read before the machine's lock.
	var machine, cluster, kind, release, assignment string
	var desired sql.NullString
	var maxAge time.Duration
	var maxAgeMicros int64
	err := tx.QueryRowContext(ctx, `SELECT machine, cluster, kind, release, assignment_revision, desired_release,
			(extract(epoch FROM max_observation_age) * 1000000)::bigint
		FROM plan WHERE id = $1`, plan).Scan(&machine, &cluster, &kind, &release, &assignment, &desired, &maxAgeMicros)
	if err != nil {
		return r, "", fmt.Errorf("adoption of plan %s: %w", plan, err)
	}
	if kind != "adopt" {
		return r, "", fmt.Errorf("adoption of plan %s: a plan of kind %s", plan, kind)
	}
	maxAge = time.Duration(maxAgeMicros) * time.Microsecond
	// A freeze does not block an adoption (requirement 4.5), so only the scope state is read.
	var scope string
	if err := tx.QueryRowContext(ctx, `SELECT scope_state FROM machine WHERE id = $1 FOR UPDATE`, machine).Scan(&scope); err != nil {
		return r, machine, err
	}
	// The machine state is written below; a publication holds every machine state of its cluster
	// (publish.go), so it is taken here, before the time is read, not at the write.
	var applied, msDesired sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT applied_release, desired FROM machine_state WHERE machine = $1 FOR UPDATE`,
		machine).Scan(&applied, &msDesired); err != nil {
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
	// A refusal entry, appended under the machine's lock held here, is the plan's outcome too.
	var holding, refusedBefore bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM operation WHERE machine = $1
			AND state IN ('committed', 'sending', 'verifying', 'unresolved')),
		EXISTS (SELECT FROM machine_event WHERE machine = $1 AND kind = 'refusal' AND entry->>'plan' = $2)`,
		machine, plan).Scan(&holding, &refusedBefore); err != nil {
		return r, machine, err
	}
	// PA §5 rule 4: the record's time, and the expiry and age judged against it, follow every lock.
	// The plan's state is the one §8.1 reads at that time, so a refusal names what a reader sees.
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

	// 4.1: no operation exists for the plan (§3.2 comparison 0). An adopt plan's only operation is
	// its adoption, so one that exists is the plan's outcome, recorded by an attempt that took the
	// machine's lock first: rolled back without a refusal entry, which would follow a success.
	if operation.Valid || state == "committed" {
		return r, machine, fmt.Errorf("adoption of plan %s: %w", plan, errAlreadyAdopted)
	}
	if refusedBefore { // refused by an earlier attempt: a new plan is the retry
		return r, machine, fmt.Errorf("adoption of plan %s: %w", plan, errAlreadyRefused)
	}
	// 4.2: the approval passes comparison 1: approved, unexpired, not revoked, its identity not
	// revoked, and recorded in the current epoch.
	switch {
	case reads == "expired":
		return r, machine, refuseAdoption("4.2", "the plan has expired")
	case reads == "revoked" && state == "approved":
		return r, machine, refuseAdoption("4.2", "the approver's identity is revoked")
	case reads != "approved":
		return r, machine, refuseAdoption("4.2", "the plan is "+reads)
	case approvalEpoch != current:
		return r, machine, refuseAdoption("4.2", "the approval is of an earlier epoch")
	}
	// 4.3: the assignment revision and Desired equal the bound ones; a handover machine still has
	// no Applied.
	switch {
	case !head.Valid || head.String != assignment:
		return r, machine, refuseAdoption("4.3", "the machine's assignment revision changed")
	case msDesired != desired:
		return r, machine, refuseAdoption("4.3", "the machine's Desired release changed")
	case applied.Valid:
		return r, machine, refuseAdoption("4.3", "the machine has an Applied release")
	}
	// 4.4: the recorded observation with the highest basis, of any purpose: begun after the
	// approval, no older than the plan binds (measured from its start entry, when the read began),
	// showing the machine's identity, the baseline's configuration digest and the bound assignment
	// revision. A start with no recorded observation does not count. An observation that is
	// missing, begun before the approval, too old or left any of those three values unread is no
	// evidence, so the record is not attempted and nothing is refused; only one that shows another
	// value is (choice §10.27).
	var obs sql.NullString
	var basis int64
	var startedAt time.Time
	var identityMatches, digestMatches bool
	var evidence, unread sql.NullString
	var baseline []byte
	err = tx.QueryRowContext(ctx, `SELECT o.id, o.basis, se.at,
			(SELECT string_agg(k, ', ' ORDER BY k) FROM jsonb_object_keys(o.unread) k
				WHERE k IN ('identity', 'configuration', 'assignmentEvidence')),
			CASE WHEN m.smbios_uuid IS NOT NULL THEN o.smbios_uuid IS NOT DISTINCT FROM m.smbios_uuid
				ELSE o.talos_node_id IS NOT DISTINCT FROM m.talos_node_id AND o.smbios_uuid IS NULL END
			AND o.talos_cluster_id IS NOT DISTINCT FROM c.talos_cluster_id,
			o.configuration_digest IS NOT DISTINCT FROM b.configuration_digest, o.assignment_evidence, b.configuration_digest
		FROM observation o JOIN machine_event se ON se.machine = o.machine AND se.revision = o.basis
			JOIN machine m ON m.id = o.machine JOIN cluster c ON c.id = m.cluster
			JOIN release_machine rm ON rm.release = $2 AND rm.machine = o.machine
			JOIN import_base_revision b ON b.id = rm.import_base_revision
		WHERE o.machine = $1 ORDER BY o.basis DESC LIMIT 1`, machine, release).Scan(&obs, &basis, &startedAt, &unread,
		&identityMatches, &digestMatches, &evidence, &baseline)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return r, machine, err
	}
	// No evidence rolls back without a refusal entry, whatever else the observation shows: a fresh
	// one decides. Only evidence that contradicts the plan is refused.
	switch {
	case !obs.Valid:
		return r, machine, fmt.Errorf("adoption of plan %s: the machine has no recorded observation: %w", plan, errNoEvidence)
	case unread.Valid:
		return r, machine, fmt.Errorf("adoption of plan %s: observation %s did not read the %s: %w", plan, obs.String,
			unread.String, errNoEvidence)
	case basis <= approvalRevision:
		return r, machine, fmt.Errorf("adoption of plan %s: observation %s began before the approval: %w", plan, obs.String,
			errNoEvidence)
	case at.Sub(startedAt) > maxAge:
		return r, machine, fmt.Errorf("adoption of plan %s: observation %s is older than the plan allows: %w", plan,
			obs.String, errNoEvidence)
	case !identityMatches:
		return r, machine, refuseAdoption("4.4", "the latest observation shows another machine identity")
	case !digestMatches:
		return r, machine, refuseAdoption("4.4", "the latest observation's configuration digest is not the baseline's")
	case !evidence.Valid || evidence.String != assignment:
		return r, machine, refuseAdoption("4.4", "the latest observation shows another assignment revision")
	}
	// 4.5: no operation holds the machine scope, and the scope gate is open apart from a freeze:
	// recovery mode without a release of the scope in the current epoch closes it (§7.5).
	switch {
	case holding:
		return r, machine, refuseAdoption("4.5", "an operation holds the machine scope")
	case scope != "normal" && scope != "released":
		return r, machine, refuseAdoption("4.5", "the machine scope is "+scope)
	case recovery && scope != "released":
		return r, machine, refuseAdoption("4.5", "recovery mode is in effect and the scope is not released")
	}

	r.Operation, r.Observation = id.New(id.Operation), obs.String
	if err := tx.QueryRowContext(ctx, `UPDATE machine SET revision_counter = revision_counter + 2 WHERE id = $1
		RETURNING revision_counter - 1`, machine).Scan(&r.Revision); err != nil {
		return r, machine, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		VALUES ($1, $2, $3, 'adoption', jsonb_build_object('plan', $4::text, 'operation', $5::text, 'approval', $6::text,
			'observation', $7::text, 'controller', $8::text, 'comparisons', jsonb_build_array('4.1', '4.2', '4.3', '4.4', '4.5')), $9)`,
		machine, r.Revision, current, plan, r.Operation, approval.String, r.Observation, a.d.owner.ID, at); err != nil {
		return r, machine, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation (id, kind, state, epoch, plan, machine, cluster, created_at, result)
		VALUES ($1, 'adopt', 'completed', $2, $3, $4, $5, $6, jsonb_build_object('adoptionRecord', $3::text))`,
		r.Operation, current, plan, machine, cluster, at); err != nil {
		return r, machine, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO adoption_record (plan, machine, cluster, operation, approval, observation, epoch,
			at, revision, baseline_digest)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		plan, machine, cluster, r.Operation, approval.String, r.Observation, current, at, r.Revision, baseline); err != nil {
		return r, machine, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE plan_state SET state = 'committed', operation = $2, revision = revision + 1,
		updated_at = $3 WHERE plan = $1`, plan, r.Operation, at); err != nil {
		return r, machine, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE machine_state SET desired = $2, applied_release = $2, applied_digest = $3,
		applied_source = 'adoption', baseline_revision = 1, revision = revision + 1 WHERE machine = $1`,
		machine, release, baseline); err != nil {
		return r, machine, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		VALUES ($1, $2, $3, 'applied-change', jsonb_build_object('from', NULL::text, 'to', $4::text, 'digest', $5::text,
			'source', 'adoption', 'plan', $6::text), $7)`,
		machine, r.Revision+1, current, release, hex.EncodeToString(baseline), plan, at)
	return r, machine, err
}

// recordRefusal is a refused adoption's refusal entry (execution-recovery.md §4.1), recorded after
// the refused transaction rolled back, under the same epoch check and machine lock (T7).
func (a *API) recordRefusal(ctx context.Context, machine, plan string, refused *adoptionRefused) error {
	return a.inTx(ctx, func(tx *sql.Tx) error {
		var current string
		if err := tx.QueryRowContext(ctx, `SELECT epoch FROM installation_state FOR SHARE`).Scan(&current); err != nil {
			return err
		}
		if current != a.d.owner.Epoch {
			return errNotController
		}
		if _, err := tx.ExecContext(ctx, `SELECT 1 FROM machine WHERE id = $1 FOR UPDATE`, machine); err != nil {
			return err
		}
		// The attempt rolled back before this lock, so another may have adopted or refused the plan
		// since: every adoption and refusal entry holds the machine first, so both are read here.
		var adoptedSince, refusedSince bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM plan_state WHERE plan = $2
				AND (operation IS NOT NULL OR state = 'committed')),
			EXISTS (SELECT FROM machine_event WHERE machine = $1 AND kind = 'refusal' AND entry->>'plan' = $2)`,
			machine, plan).Scan(&adoptedSince, &refusedSince); err != nil {
			return err
		}
		switch {
		case adoptedSince:
			return fmt.Errorf("adoption of plan %s: %w", plan, errAlreadyAdopted)
		case refusedSince: // recorded once, by whichever attempt locked first
			return nil
		}
		var rev int64
		at, err := nextEntry(ctx, tx, machine, &rev)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
			VALUES ($1, $2, $3, 'refusal', jsonb_build_object('transaction', 'T6', 'comparison', $4::text, 'plan', $5::text,
				'cause', $6::text, 'controller', $7::text), $8)`,
			machine, rev, current, refused.Comparison, plan, refused.Cause, a.d.owner.ID, at)
		return err
	})
}
