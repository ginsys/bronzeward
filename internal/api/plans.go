package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/textdiff"
)

func planCreation() effectRoute {
	return effectRoute{action: "plan.create", input: func() input { return &planInput{} }, effect: createPlan}
}

// planInput is T4's request (persistence-api.md §9.2): the release and machine, the operation,
// an apply-config plan's mode, and the durations and attempt limit, each in whole seconds and
// each the deployment's default when left out (choice §17.37). An adopt plan takes no mode,
// deadlines or attempt limit (execution-recovery.md §6.3).
type planInput struct {
	ReleaseID                   string  `json:"releaseId"`
	Machine                     string  `json:"machine"`
	Operation                   string  `json:"operation"`
	Mode                        *string `json:"mode"`
	ExpiresInSeconds            *int64  `json:"expiresInSeconds"`
	MaxObservationAgeSeconds    *int64  `json:"maxObservationAgeSeconds"`
	CheckValiditySeconds        *int64  `json:"checkValiditySeconds"`
	TransportDeadlineSeconds    *int64  `json:"transportDeadlineSeconds"`
	VerificationDeadlineSeconds *int64  `json:"verificationDeadlineSeconds"`
	MaxAttempts                 *int64  `json:"maxAttempts"`
}

func (in *planInput) check(a *API) error {
	if err := id.MustHave(in.ReleaseID, id.Release); err != nil {
		return errors.New("releaseId must be a release id")
	}
	if err := id.MustHave(in.Machine, id.Machine); err != nil {
		return errors.New("machine must be a machine id")
	}
	def := a.d.exec.PlanDefaults
	switch in.Operation {
	case "apply-config":
		if in.Mode == nil || *in.Mode != "no-reboot" {
			return errors.New(`an apply-config plan takes mode "no-reboot"`)
		}
		for _, d := range []struct {
			v   **int64
			def time.Duration
		}{{&in.TransportDeadlineSeconds, def.TransportDeadline}, {&in.VerificationDeadlineSeconds, def.VerificationDeadline}} {
			if *d.v == nil {
				s := int64(d.def / time.Second)
				*d.v = &s
			}
		}
		if in.MaxAttempts == nil {
			n := int64(def.MaxAttempts)
			in.MaxAttempts = &n
		}
	case "adopt":
		if in.Mode != nil || in.TransportDeadlineSeconds != nil || in.VerificationDeadlineSeconds != nil || in.MaxAttempts != nil {
			return errors.New("an adopt plan takes no mode, deadlines or attempt limit")
		}
	default:
		return errors.New(`operation must be "apply-config" or "adopt"`)
	}
	for _, d := range []struct {
		name string
		v    **int64
		def  time.Duration
	}{
		{"expiresInSeconds", &in.ExpiresInSeconds, def.Expiry},
		{"maxObservationAgeSeconds", &in.MaxObservationAgeSeconds, def.MaxObservationAge},
		{"checkValiditySeconds", &in.CheckValiditySeconds, def.CheckValidity},
		{"transportDeadlineSeconds", &in.TransportDeadlineSeconds, 0},
		{"verificationDeadlineSeconds", &in.VerificationDeadlineSeconds, 0},
	} {
		if *d.v == nil {
			if d.def == 0 {
				continue // an adopt plan's deadline
			}
			s := int64(d.def / time.Second)
			*d.v = &s
		}
		if s := **d.v; s < 1 || s > int64(config.MaxPlanDuration/time.Second) {
			return fmt.Errorf("%s must be from 1 to %d", d.name, int64(config.MaxPlanDuration/time.Second))
		}
	}
	if in.MaxAttempts != nil && (*in.MaxAttempts < 1 || *in.MaxAttempts > config.MaxPlanAttempts) {
		return fmt.Errorf("maxAttempts must be from 1 to %d", config.MaxPlanAttempts)
	}
	if in.TransportDeadlineSeconds != nil {
		if limit := int64(a.d.exec.MaxTransportDeadline / time.Second); *in.TransportDeadlineSeconds > limit {
			return fmt.Errorf("transportDeadlineSeconds must be at most the deployment's maximum, %d", limit)
		}
		if *in.TransportDeadlineSeconds > *in.VerificationDeadlineSeconds {
			return errors.New("transportDeadlineSeconds must be at most verificationDeadlineSeconds")
		}
	}
	return nil
}

// planCreator is who created a plan and in which role.
type planCreator struct {
	Principal string `json:"principal"`
	Role      string `json:"role"`
}

// planDiff is an apply-config plan's diff of whole redacted configurations, from Applied's release
// to the plan's (execution-recovery.md §2). It is withheld when either side has no redacted form
// (compilation §8.3).
type planDiff struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Withheld bool   `json:"withheld"`
	Unified  string `json:"unified"`
}

// planBaseline is an adopt plan's evidence (execution-recovery.md §6.3): the import base revision
// the adopted release was compiled from, and whether its artifact differs from that baseline, so
// that the machine stays pending convergence after the adoption. It names no digest (§9.1).
type planBaseline struct {
	ImportBaseRevision string `json:"importBaseRevision"`
	PendingConvergence bool   `json:"pendingConvergence"`
}

// planValidation is the release's validation the plan relies on: it passed at publication.
type planValidation struct {
	Result            string `json:"result"`
	At                string `json:"at"`
	Contract          string `json:"contract"`
	KubernetesVersion string `json:"kubernetesVersion"`
	MachineryVersion  string `json:"machineryVersion"`
	PlatformMode      string `json:"platformMode"`
}

// planDryRun says whether a dry run backs the plan. The PoC has none.
type planDryRun struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

// planEvidence is what the plan was created on, stored with it and answered as stored.
type planEvidence struct {
	Diff       *planDiff      `json:"diff,omitempty"`
	Baseline   *planBaseline  `json:"baseline,omitempty"`
	Validation planValidation `json:"validation"`
	DryRun     planDryRun     `json:"dryRun"`
}

// planBody is §9.3's plan resource. Its state is the plan's current state; approval stays null
// until approvals exist. The expected digest it binds is never answered (§9.1).
type planBody struct {
	ID                          string       `json:"id"`
	Revision                    int64        `json:"revision"`
	State                       string       `json:"state"`
	CommittedOperation          *string      `json:"committedOperation"`
	Approval                    *struct{}    `json:"approval"`
	Cluster                     string       `json:"cluster"`
	Machine                     string       `json:"machine"`
	Release                     string       `json:"release"`
	Operation                   string       `json:"operation"`
	Mode                        *string      `json:"mode"`
	AssignmentRevision          string       `json:"assignmentRevision"`
	DesiredRelease              *string      `json:"desiredRelease"`
	BaselineRevision            *int64       `json:"baselineRevision"`
	Route                       string       `json:"route"`
	MaxObservationAgeSeconds    int64        `json:"maxObservationAgeSeconds"`
	CheckValiditySeconds        int64        `json:"checkValiditySeconds"`
	TransportDeadlineSeconds    *int64       `json:"transportDeadlineSeconds"`
	VerificationDeadlineSeconds *int64       `json:"verificationDeadlineSeconds"`
	MaxAttempts                 *int64       `json:"maxAttempts"`
	RolloutLimit                int          `json:"rolloutLimit"`
	ApprovalPolicy              string       `json:"approvalPolicy"`
	CreatedBy                   planCreator  `json:"createdBy"`
	Epoch                       string       `json:"epoch"`
	TimelineRevision            int64        `json:"timelineRevision"`
	CreatedAt                   time.Time    `json:"createdAt"`
	ExpiresAt                   time.Time    `json:"expiresAt"`
	Evidence                    planEvidence `json:"evidence"`
}

// createPlan is T4 (persistence-api.md §5): the machine locked, not pre-restore unaccounted; the
// release published and covering it; for apply-config the machine's Desired with an Applied to
// diff from, for adopt a machine with no Applied; the machine's assignment head still the revision
// the release was compiled from. The plan is one plan entry on the machine's timeline, proposed.
func createPlan(ctx context.Context, a *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*planInput)
	b := planBody{ID: id.New(id.Plan), Revision: 1, State: "proposed", Machine: in.Machine, Release: in.ReleaseID,
		Operation: in.Operation, Mode: in.Mode, MaxObservationAgeSeconds: *in.MaxObservationAgeSeconds,
		CheckValiditySeconds: *in.CheckValiditySeconds, TransportDeadlineSeconds: in.TransportDeadlineSeconds,
		VerificationDeadlineSeconds: in.VerificationDeadlineSeconds, MaxAttempts: in.MaxAttempts, RolloutLimit: 1,
		ApprovalPolicy: "one-approver", CreatedBy: planCreator{Principal: q.principal.ID, Role: string(q.role)}, Epoch: q.epoch}
	var scope string
	err := tx.QueryRowContext(ctx, `SELECT cluster, scope_state, talos_endpoint FROM machine WHERE id = $1 FOR UPDATE`,
		in.Machine).Scan(&b.Cluster, &scope, &b.Route)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return result{}, refuse(http.StatusNotFound, "not-found", "no such machine").with("machine", in.Machine)
	case err != nil:
		return result{}, err
	case scope == "pre-restore-unaccounted":
		return result{}, refuse(http.StatusConflict, "recovery-mode-active",
			"the machine's scope is still pre-restore unaccounted").with("machine", in.Machine).with("scope", scope)
	}

	// The release and, when it covers the machine, the machine's artifact in it.
	var (
		covered                  bool
		ibr, asr, mode, redacted sql.NullString
		artifact, baseline       []byte
	)
	err = tx.QueryRowContext(ctx, `SELECT r.contract, r.kubernetes_version, r.machinery_version, m.machine IS NOT NULL,
			m.import_base_revision, m.assignment_revision, m.mode, m.redacted, m.configuration_digest, i.configuration_digest
		FROM release r
		LEFT JOIN release_machine m ON m.release = r.id AND m.machine = $2
		LEFT JOIN import_base_revision i ON i.id = m.import_base_revision
		WHERE r.id = $1 AND r.cluster = $3`, in.ReleaseID, in.Machine, b.Cluster).Scan(
		&b.Evidence.Validation.Contract, &b.Evidence.Validation.KubernetesVersion, &b.Evidence.Validation.MachineryVersion,
		&covered, &ibr, &asr, &mode, &redacted, &artifact, &baseline)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return result{}, refuse(http.StatusNotFound, "not-found", "no such release in the machine's cluster").with("release", in.ReleaseID)
	case err != nil:
		return result{}, err
	case !covered:
		return result{}, refuse(http.StatusUnprocessableEntity, "validation-failed",
			"the release does not cover the machine").with("release", in.ReleaseID).with("machine", in.Machine)
	}
	b.Evidence.Validation.Result, b.Evidence.Validation.At, b.Evidence.Validation.PlatformMode = "passed", "publication", mode.String
	b.Evidence.DryRun.Reason = "no dry run is available for this operation"

	var desired, appliedRelease sql.NullString
	var appliedDigest []byte
	var baselineRev sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT desired, applied_release, applied_digest, baseline_revision
		FROM machine_state WHERE machine = $1 FOR SHARE`, in.Machine).Scan(&desired, &appliedRelease, &appliedDigest, &baselineRev); err != nil {
		return result{}, err
	}
	if desired.Valid {
		b.DesiredRelease = &desired.String
	}
	switch in.Operation {
	case "apply-config":
		if desired.String != in.ReleaseID {
			return result{}, refuse(http.StatusConflict, "conflict",
				"an apply-config plan is for the machine's Desired release").with("desired", b.DesiredRelease)
		}
		if !appliedRelease.Valid {
			return result{}, refuse(http.StatusConflict, "conflict",
				"the machine has no Applied configuration to plan an apply-config from; adopt it first").with("machine", in.Machine)
		}
		b.BaselineRevision = &baselineRev.Int64
		d, err := planDiffFrom(ctx, tx, in.Machine, appliedRelease.String, in.ReleaseID, redacted)
		if err != nil {
			return result{}, err
		}
		b.Evidence.Diff = d
	case "adopt":
		if appliedRelease.Valid {
			return result{}, refuse(http.StatusConflict, "conflict",
				"the machine already has an Applied configuration; adopt only a machine with none").with("machine", in.Machine)
		}
		appliedDigest = nil
		b.Evidence.Baseline = &planBaseline{ImportBaseRevision: ibr.String, PendingConvergence: !bytes.Equal(artifact, baseline)}
	}

	// The release was compiled from the machine's assignment at one revision; a plan on a moved
	// head would bind a configuration its assignment no longer selects.
	var head sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT head_revision_id FROM assignment WHERE machine = $1 FOR SHARE`, in.Machine).Scan(&head)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result{}, err
	}
	if !asr.Valid || !head.Valid || head.String != asr.String {
		return result{}, refuse(http.StatusConflict, "conflict",
			"the machine's assignment has moved since the release was compiled").with("release", in.ReleaseID).with("machine", in.Machine)
	}
	b.AssignmentRevision = asr.String

	evidence, err := json.Marshal(b.Evidence)
	if err != nil {
		return result{}, err
	}
	if err := tx.QueryRowContext(ctx, `UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1
		RETURNING revision_counter`, in.Machine).Scan(&b.TimelineRevision); err != nil {
		return result{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		VALUES ($1, $2, $3, 'plan', jsonb_build_object('plan', $4::text, 'operation', $5::text, 'release', $6::text,
			'principal', $7::text, 'role', $8::text), now())`,
		in.Machine, b.TimelineRevision, q.epoch, b.ID, in.Operation, in.ReleaseID, b.CreatedBy.Principal, b.CreatedBy.Role); err != nil {
		return result{}, err
	}
	if err := tx.QueryRowContext(ctx, `INSERT INTO plan (id, cluster, machine, kind, mode, release, assignment_revision,
			desired_release, baseline_revision, expected_digest, route, max_observation_age, check_validity, transport_deadline,
			verification_deadline, max_attempts, expires_at, idempotency_key, approval_policy, created_by, created_by_kind,
			created_role, epoch, created_at, evidence, revision)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, make_interval(secs => $12), make_interval(secs => $13),
			make_interval(secs => $14), make_interval(secs => $15), $16, now() + make_interval(secs => $17), $18, 'one-approver',
			$19, $20, 'publisher', $21, now(), $22, $23)
		RETURNING created_at, expires_at`,
		b.ID, b.Cluster, in.Machine, in.Operation, in.Mode, in.ReleaseID, b.AssignmentRevision, b.DesiredRelease,
		b.BaselineRevision, appliedDigest, b.Route, b.MaxObservationAgeSeconds, b.CheckValiditySeconds,
		b.TransportDeadlineSeconds, b.VerificationDeadlineSeconds, b.MaxAttempts, *in.ExpiresInSeconds, q.key,
		b.CreatedBy.Principal, string(q.principal.Kind), q.epoch, evidence, b.TimelineRevision).Scan(&b.CreatedAt, &b.ExpiresAt); err != nil {
		return result{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO plan_state (plan, state, updated_at) VALUES ($1, 'proposed', now())`, b.ID); err != nil {
		return result{}, err
	}
	b.CreatedAt, b.ExpiresAt = b.CreatedAt.UTC(), b.ExpiresAt.UTC()
	return result{status: http.StatusCreated, location: prefix + "/plans/" + b.ID, body: b, subjects: []string{b.ID, in.Machine}}, nil
}

// planDiffFrom is the unified diff from Applied's redacted configuration to the target's, or a
// withheld diff when either has none.
func planDiffFrom(ctx context.Context, tx *sql.Tx, machine, from, to string, target sql.NullString) (*planDiff, error) {
	d := &planDiff{From: from, To: to}
	var applied sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT redacted FROM release_machine WHERE release = $1 AND machine = $2`,
		from, machine).Scan(&applied); err != nil {
		return nil, err
	}
	if !applied.Valid || !target.Valid {
		d.Withheld = true
		return d, nil
	}
	d.Unified = textdiff.Unified(applied.String, target.String)
	return d, nil
}
