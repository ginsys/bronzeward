package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

func planApproval() effectRoute {
	return effectRoute{action: "plan.approve", input: func() input { return &approvalInput{} }, effect: approvePlan}
}

// approvalInput is T5a's request, {} (§9.2): the plan is the path's and the approver the caller.
type approvalInput struct{}

func (*approvalInput) check(*API) error { return nil }

// approvalBy is who approved and in which role.
type approvalBy struct {
	Principal string `json:"principal"`
	Role      string `json:"role"`
}

// selfApproval is an approval's self-approval mark (§10.5): marked when any reason holds, with
// every reason that holds in §10.5's table order.
type selfApproval struct {
	Marked  bool     `json:"marked"`
	Reasons []string `json:"reasons"`
}

// approvalBody is §9.3's approval resource.
type approvalBody struct {
	ID           string       `json:"id"`
	Plan         string       `json:"plan"`
	Approver     approvalBy   `json:"approver"`
	Epoch        string       `json:"epoch"`
	SelfApproval selfApproval `json:"selfApproval"`
}

// approvePlan is T5a (persistence-api.md §5): the plan's machine FOR UPDATE, its scope not
// pre-restore unaccounted; the approver's principal FOR SHARE, not revoked (rule 2); the plan's
// state FOR UPDATE, proposed, or approved by an approval of an earlier epoch, and unexpired once
// the act-order lock is held (rule 4). The approval is one approval entry on the machine's
// timeline, and the plan becomes approved under it.
func approvePlan(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
	plan := q.r.PathValue("id")
	if id.MustHave(plan, id.Plan) != nil {
		return result{}, refuse(http.StatusNotFound, "not-found", "no such plan")
	}
	b := approvalBody{ID: id.New(id.Approval), Plan: plan, Approver: approvalBy{Principal: q.principal.ID, Role: string(q.role)},
		Epoch: q.epoch, SelfApproval: selfApproval{Reasons: []string{}}}
	// A plan row is immutable, so its machine is read before the machine's lock.
	var machine, scope string
	err := tx.QueryRowContext(ctx, `SELECT machine FROM plan WHERE id = $1`, plan).Scan(&machine)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return result{}, refuse(http.StatusNotFound, "not-found", "no such plan").with("plan", plan)
	case err != nil:
		return result{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT scope_state FROM machine WHERE id = $1 FOR UPDATE`, machine).Scan(&scope); err != nil {
		return result{}, err
	}
	if scope == "pre-restore-unaccounted" {
		return result{}, refuse(http.StatusConflict, "recovery-mode-active",
			"the machine's scope is still pre-restore unaccounted").with("machine", machine).with("scope", scope)
	}
	// The approver of the plan's current approval, if any. Every writer of a plan's state holds its
	// machine first, so the approval this reads is the one the state lock below reads; its
	// principal is held with the approver's, so a revocation of it either commits before the
	// projection below reads it or waits for this approval.
	var held sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT a.approver FROM plan_state s LEFT JOIN approval a ON a.id = s.approval
		WHERE s.plan = $1`, plan).Scan(&held); err != nil {
		return result{}, err
	}
	ids := []string{q.principal.ID}
	if held.Valid {
		ids = append(ids, held.String)
	}
	slices.Sort(ids)
	for _, p := range slices.Compact(ids) {
		var revoked bool
		if err := tx.QueryRowContext(ctx, `SELECT revoked FROM principal WHERE id = $1 FOR SHARE`, p).Scan(&revoked); err != nil {
			return result{}, err
		}
		if revoked && p == q.principal.ID {
			return result{}, refuse(http.StatusForbidden, "identity-revoked", "")
		}
	}
	// The state as selectPlan reads it, but for the expiry, which is judged after the act-order
	// wait; an approval of an earlier epoch is void (execution-recovery.md §2).
	var state string
	var earlier bool
	var approver sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT CASE WHEN s.state = 'approved' AND ap.revoked AND (r.at IS NULL OR r.at < p.expires_at)
				THEN 'revoked' ELSE s.state END, a.epoch IS NOT NULL AND a.epoch <> $2, a.approver
		FROM plan_state s JOIN plan p ON p.id = s.plan LEFT JOIN approval a ON a.id = s.approval
			LEFT JOIN principal ap ON ap.id = a.approver LEFT JOIN identity_revocation r ON r.identity = ap.id
		WHERE s.plan = $1 FOR UPDATE OF s`, plan, q.epoch).Scan(&state, &earlier, &approver); err != nil {
		return result{}, err
	}
	if approver != held {
		return result{}, errors.New("approve: the plan's approval changed under its machine's lock")
	}
	if state != "proposed" && (state != "approved" || !earlier) {
		return result{}, refuse(http.StatusConflict, "conflict",
			"the plan is neither proposed nor awaiting approval in the current epoch").with("plan", plan).with("state", state)
	}
	if b.SelfApproval.Reasons, err = selfApprovalReasons(ctx, tx, plan, q.principal.ID); err != nil {
		return result{}, err
	}
	b.SelfApproval.Marked = len(b.SelfApproval.Reasons) > 0
	mark, err := json.Marshal(b.SelfApproval)
	if err != nil {
		return result{}, err
	}
	// PA §5 rule 4: the approval's time, and the expiry it is judged against, follow every lock
	// wait, the act-order lock's included.
	write := func(ctx context.Context, tx *sql.Tx) error {
		var at time.Time
		var expired bool
		if err := tx.QueryRowContext(ctx, `SELECT c.t, p.expires_at <= c.t FROM plan p, (SELECT clock_timestamp() AS t) c
			WHERE p.id = $1`, plan).Scan(&at, &expired); err != nil {
			return err
		}
		if expired {
			return refuse(http.StatusConflict, "conflict", "the plan has expired").with("plan", plan).with("state", "expired")
		}
		var rev int64
		if err := tx.QueryRowContext(ctx, `UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1
			RETURNING revision_counter`, machine).Scan(&rev); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
			VALUES ($1, $2, $3, 'approval', jsonb_build_object('plan', $4::text, 'approval', $5::text, 'principal', $6::text,
				'role', $7::text, 'selfApproval', $8::jsonb), $9)`,
			machine, rev, q.epoch, plan, b.ID, b.Approver.Principal, b.Approver.Role, mark, at); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO approval (id, plan, machine, approver, role, epoch, self_approval, act, at, revision)
			VALUES ($1, $2, $3, $4, $5, $6, string_to_array($7, ','), $8, $9, $10)`,
			b.ID, plan, machine, b.Approver.Principal, b.Approver.Role, q.epoch, strings.Join(b.SelfApproval.Reasons, ","),
			q.actID, at, rev); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE plan_state SET state = 'approved', approval = $2, revision = revision + 1,
			updated_at = $3 WHERE plan = $1`, plan, b.ID, at)
		return err
	}
	return result{status: http.StatusCreated, location: prefix + "/approvals/" + b.ID, body: &b,
		subjects: []string{b.ID, plan, machine}, atActOrder: write}, nil
}

// getApproval reads approval v as T5a answered it (§9.3). An approval is immutable, so it carries
// no ETag.
var getApproval = item(id.Approval, func(ctx context.Context, tx *sql.Tx, v string) (string, any, error) {
	b := approvalBody{SelfApproval: selfApproval{Reasons: []string{}}}
	var reasons string
	if err := tx.QueryRowContext(ctx, `SELECT id, plan, approver, role, epoch, array_to_string(self_approval, ',')
		FROM approval WHERE id = $1`, v).Scan(&b.ID, &b.Plan, &b.Approver.Principal, &b.Approver.Role, &b.Epoch,
		&reasons); err != nil {
		return "", nil, err
	}
	if reasons != "" {
		b.SelfApproval.Reasons = strings.Split(reasons, ",")
	}
	b.SelfApproval.Marked = len(b.SelfApproval.Reasons) > 0
	return "", b, nil
})

// selfApprovalReasons are the §10.5 reasons that hold for approver on plan, in the table's order
// (choice §17.39). The plan's release contains its sources' revisions and its machines' import
// base revisions; its draft introduced the revisions of the draft's own entries. An authored
// revision the draft introduced is a change, any other one is reused, however long ago it was
// written (execution-recovery.md choice §10.4). The rows read take no lock: each is immutable, a
// published draft's entries are no longer written, and a service identity's responsible human is
// set when the token tool creates it.
func selfApprovalReasons(ctx context.Context, tx *sql.Tx, plan, approver string) ([]string, error) {
	var holds [5]bool
	err := tx.QueryRowContext(ctx, `WITH pr AS (SELECT p.created_by, r.id AS release, r.draft, r.published_by
			FROM plan p JOIN release r ON r.id = p.release WHERE p.id = $1),
		contained (revision, author) AS (
			SELECT f.id, f.author FROM pr JOIN release_source s ON s.release = pr.release JOIN fragment_revision f ON f.id = s.fragment_revision
			UNION ALL SELECT v.id, v.author FROM pr JOIN release_source s ON s.release = pr.release
				JOIN profile_revision v ON v.id = s.profile_revision
			UNION ALL SELECT a.id, a.author FROM pr JOIN release_source s ON s.release = pr.release
				JOIN assignment_revision a ON a.id = s.assignment_revision
			UNION ALL SELECT i.id, i.author FROM pr JOIN release_machine m ON m.release = pr.release
				JOIN import_base_revision i ON i.id = m.import_base_revision),
		introduced (revision) AS (
			SELECT coalesce(e.fragment_revision, e.profile_revision, e.assignment_revision) FROM pr
				JOIN draft_source_entry e ON e.draft = pr.draft
			UNION ALL SELECT e.import_base_revision FROM pr JOIN draft_entry e ON e.draft = pr.draft)
		SELECT pr.created_by = $2, pr.published_by = $2,
			EXISTS (SELECT FROM contained c WHERE c.author = $2 AND c.revision IN (SELECT revision FROM introduced)),
			EXISTS (SELECT FROM contained c WHERE c.author = $2
				AND NOT EXISTS (SELECT FROM introduced i WHERE i.revision = c.revision)),
			EXISTS (SELECT FROM principal s WHERE s.kind = 'service' AND s.responsible = $2
				AND (s.id IN (pr.created_by, pr.published_by) OR s.id IN (SELECT author FROM contained)))
		FROM pr`, plan, approver).Scan(&holds[0], &holds[1], &holds[2], &holds[3], &holds[4])
	if err != nil {
		return nil, err
	}
	reasons := []string{}
	for i, r := range []string{"created-plan", "published", "authored-change", "authored-reused", "owned-automation"} {
		if holds[i] {
			reasons = append(reasons, r)
		}
	}
	return reasons, nil
}
