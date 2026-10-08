package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

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

// check validates what the request alone fixes. The defaults and the deployment's maximum are
// applied by resolve, after the key is looked up (§7.2), so a committed request replays whatever
// the deployment says now.
func (in *planInput) check(*API) error {
	if err := id.MustHave(in.ReleaseID, id.Release); err != nil {
		return errors.New("releaseId must be a release id")
	}
	if err := id.MustHave(in.Machine, id.Machine); err != nil {
		return errors.New("machine must be a machine id")
	}
	switch in.Operation {
	case "apply-config":
		if in.Mode == nil || *in.Mode != "no-reboot" {
			return errors.New(`an apply-config plan takes mode "no-reboot"`)
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
		v    *int64
	}{
		{"expiresInSeconds", in.ExpiresInSeconds},
		{"maxObservationAgeSeconds", in.MaxObservationAgeSeconds},
		{"checkValiditySeconds", in.CheckValiditySeconds},
		{"transportDeadlineSeconds", in.TransportDeadlineSeconds},
		{"verificationDeadlineSeconds", in.VerificationDeadlineSeconds},
	} {
		if d.v != nil && (*d.v < 1 || *d.v > int64(config.MaxPlanDuration/time.Second)) {
			return fmt.Errorf("%s must be from 1 to %d", d.name, int64(config.MaxPlanDuration/time.Second))
		}
	}
	if in.MaxAttempts != nil && (*in.MaxAttempts < 1 || *in.MaxAttempts > config.MaxPlanAttempts) {
		return fmt.Errorf("maxAttempts must be from 1 to %d", config.MaxPlanAttempts)
	}
	if t, v := in.TransportDeadlineSeconds, in.VerificationDeadlineSeconds; t != nil && v != nil && *t > *v {
		return errors.New("transportDeadlineSeconds must be at most verificationDeadlineSeconds")
	}
	return nil
}

// resolve fills what the request leaves out from the deployment's defaults and holds the transport
// deadline to the deployment's maximum and the verification deadline (choice §17.37). It runs in
// T4, so only a request with no record meets it.
func (in *planInput) resolve(exec config.Execution) error {
	def := exec.PlanDefaults
	fill := func(v **int64, d time.Duration) {
		if *v == nil {
			s := int64(d / time.Second)
			*v = &s
		}
	}
	fill(&in.ExpiresInSeconds, def.Expiry)
	fill(&in.MaxObservationAgeSeconds, def.MaxObservationAge)
	fill(&in.CheckValiditySeconds, def.CheckValidity)
	if in.Operation != "apply-config" {
		return nil
	}
	fill(&in.TransportDeadlineSeconds, def.TransportDeadline)
	fill(&in.VerificationDeadlineSeconds, def.VerificationDeadline)
	if in.MaxAttempts == nil {
		n := int64(def.MaxAttempts)
		in.MaxAttempts = &n
	}
	if limit := int64(exec.MaxTransportDeadline / time.Second); *in.TransportDeadlineSeconds > limit {
		return fmt.Errorf("transportDeadlineSeconds must be at most the deployment's maximum, %d", limit)
	}
	if *in.TransportDeadlineSeconds > *in.VerificationDeadlineSeconds {
		return errors.New("transportDeadlineSeconds must be at most verificationDeadlineSeconds")
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
	Approval                    *string      `json:"approval"`
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
	if err := in.resolve(a.d.exec); err != nil {
		return result{}, refuse(http.StatusBadRequest, "invalid-request", err.Error())
	}
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
		d, err := planDiffFrom(ctx, tx, in.Machine, appliedRelease.String, in.ReleaseID, appliedDigest, redacted)
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
	// PA §1.2 rule 4: the plan's creation time, and so its expiry, follow the lock waits above;
	// now() is fixed when the transaction began.
	var at time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&at); err != nil {
		return result{}, err
	}
	if err := tx.QueryRowContext(ctx, `UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1
		RETURNING revision_counter`, in.Machine).Scan(&b.TimelineRevision); err != nil {
		return result{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		VALUES ($1, $2, $3, 'plan', jsonb_build_object('plan', $4::text, 'operation', $5::text, 'release', $6::text,
			'principal', $7::text, 'role', $8::text), $9)`,
		in.Machine, b.TimelineRevision, q.epoch, b.ID, in.Operation, in.ReleaseID, b.CreatedBy.Principal, b.CreatedBy.Role, at); err != nil {
		return result{}, err
	}
	if err := tx.QueryRowContext(ctx, `INSERT INTO plan (id, cluster, machine, kind, mode, release, assignment_revision,
			desired_release, baseline_revision, expected_digest, route, max_observation_age, check_validity, transport_deadline,
			verification_deadline, max_attempts, expires_at, idempotency_key, approval_policy, created_by, created_by_kind,
			created_role, epoch, created_at, evidence, revision)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, make_interval(secs => $12), make_interval(secs => $13),
			make_interval(secs => $14), make_interval(secs => $15), $16, $24::timestamptz + make_interval(secs => $17), $18,
			'one-approver', $19, $20, 'publisher', $21, $24, $22, $23)
		RETURNING created_at, expires_at`,
		b.ID, b.Cluster, in.Machine, in.Operation, in.Mode, in.ReleaseID, b.AssignmentRevision, b.DesiredRelease,
		b.BaselineRevision, appliedDigest, b.Route, b.MaxObservationAgeSeconds, b.CheckValiditySeconds,
		b.TransportDeadlineSeconds, b.VerificationDeadlineSeconds, b.MaxAttempts, *in.ExpiresInSeconds, q.key,
		b.CreatedBy.Principal, string(q.principal.Kind), q.epoch, evidence, b.TimelineRevision, at).Scan(&b.CreatedAt, &b.ExpiresAt); err != nil {
		return result{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO plan_state (plan, state, updated_at) VALUES ($1, 'proposed', $2)`, b.ID, at); err != nil {
		return result{}, err
	}
	b.CreatedAt, b.ExpiresAt = b.CreatedAt.UTC(), b.ExpiresAt.UTC()
	return result{status: http.StatusCreated, location: prefix + "/plans/" + b.ID, body: b, subjects: []string{b.ID, in.Machine}}, nil
}

// selectPlan reads a plan with its state. A proposed or approved plan past its expiry reads
// expired, and an approved plan whose approver is revoked reads revoked, before any transaction
// has written either (§8.1): whichever came first, a revoked flag with no recorded time counting
// as before. The clock is read when the plan is, not at the transaction's start, which can
// precede a wait on the installation state.
const selectPlan = `SELECT p.id, s.revision,
		CASE WHEN s.state = 'approved' AND ap.revoked AND (r.at IS NULL OR r.at < p.expires_at) THEN 'revoked'
			WHEN s.state IN ('proposed', 'approved') AND p.expires_at <= c.t THEN 'expired' ELSE s.state END,
		s.operation, s.approval, p.cluster, p.machine, p.release, p.kind, p.mode,
		p.assignment_revision, p.desired_release, p.baseline_revision, p.route, extract(epoch FROM p.max_observation_age)::bigint,
		extract(epoch FROM p.check_validity)::bigint, extract(epoch FROM p.transport_deadline)::bigint,
		extract(epoch FROM p.verification_deadline)::bigint, p.max_attempts, p.rollout_limit, p.approval_policy, p.created_by,
		p.created_role, p.epoch, p.revision, p.created_at, p.expires_at, p.evidence
	FROM plan p JOIN plan_state s ON s.plan = p.id LEFT JOIN approval a ON a.id = s.approval
		LEFT JOIN principal ap ON ap.id = a.approver LEFT JOIN identity_revocation r ON r.identity = ap.id
		CROSS JOIN (SELECT clock_timestamp() AS t) c`

func scanPlan(r interface{ Scan(...any) error }) (*planBody, error) {
	b := &planBody{}
	var evidence []byte
	if err := r.Scan(&b.ID, &b.Revision, &b.State, &b.CommittedOperation, &b.Approval, &b.Cluster, &b.Machine, &b.Release,
		&b.Operation, &b.Mode, &b.AssignmentRevision, &b.DesiredRelease, &b.BaselineRevision, &b.Route,
		&b.MaxObservationAgeSeconds, &b.CheckValiditySeconds, &b.TransportDeadlineSeconds, &b.VerificationDeadlineSeconds,
		&b.MaxAttempts, &b.RolloutLimit, &b.ApprovalPolicy, &b.CreatedBy.Principal, &b.CreatedBy.Role, &b.Epoch,
		&b.TimelineRevision, &b.CreatedAt, &b.ExpiresAt, &evidence); err != nil {
		return nil, err
	}
	b.CreatedAt, b.ExpiresAt = b.CreatedAt.UTC(), b.ExpiresAt.UTC()
	return b, json.Unmarshal(evidence, &b.Evidence)
}

var listPlans = listed(id.Plan, selectPlan+` WHERE p.id > $1 ORDER BY p.id LIMIT $2`, func(r *sql.Rows) (*planBody, string, error) {
	b, err := scanPlan(r)
	if err != nil {
		return nil, "", err
	}
	return b, b.ID, nil
})

var getPlan = item(id.Plan, func(ctx context.Context, tx *sql.Tx, v string) (string, any, error) {
	b, err := scanPlan(tx.QueryRowContext(ctx, selectPlan+` WHERE p.id = $1`, v))
	return "", b, err
})

// planDiffFrom is the unified diff from Applied's redacted configuration to the target's, or a
// withheld diff when either has none, or when the machine holds a configuration other than its
// Applied release's artifact (pending convergence, execution-recovery.md §6.3), which has no
// redacted form.
func planDiffFrom(ctx context.Context, tx *sql.Tx, machine, from, to string, appliedDigest []byte, target sql.NullString) (*planDiff, error) {
	d := &planDiff{From: from, To: to}
	var applied sql.NullString
	var artifact []byte
	if err := tx.QueryRowContext(ctx, `SELECT redacted, configuration_digest FROM release_machine WHERE release = $1 AND machine = $2`,
		from, machine).Scan(&applied, &artifact); err != nil {
		return nil, err
	}
	if !applied.Valid || !target.Valid || !bytes.Equal(artifact, appliedDigest) {
		d.Withheld = true
		return d, nil
	}
	base, shown, err := pairRedacted(applied.String, target.String)
	if err != nil {
		d.Withheld = true
		return d, nil
	}
	d.Unified = textdiff.Unified(base, shown)
	return d, nil
}

// pairedToken stands for a base leaf shown beside a redacted target leaf (compilation §8.3).
const pairedToken = "<redacted:paired>"

// pairRedacted applies paired redaction to a diff's two sides (compilation §8.3, §12.2). A redacted
// target value may have come from any base value, not only the one at its path: an index shifts, a
// key is renamed, a value moves to another mapping. So once the target holds a redaction token
// anywhere, every base scalar becomes <redacted:paired>, whatever its kind, so that a boolean or a
// short value cannot be read by elimination, unless it is one whole token or the unchanged one leaf
// of its path on both sides; and every base key becomes <redacted:paired> unless it is one whole
// token or the target shows it in the same mapping. A path is its keys' text and its indexes, so
// redaction that gives two keys of one mapping the same token gives their leaves one path; every
// plaintext base leaf there is paired. An embedded document is one scalar here, so a changed one
// is paired whole. A target with no token pairs nothing. Both sides are encoded again by the same
// encoder so that only what differs shows; a side that does not parse, or holds an alias anywhere,
// mapping keys included, withholds the diff.
func pairRedacted(base, target string) (string, string, error) {
	tdocs, err := yamlDocs(target)
	if err != nil {
		return "", "", err
	}
	values := map[string][]string{}
	keys := map[string]map[string]bool{} // the plaintext keys of each target mapping
	secret := false                      // the target holds a redaction token
	walkScalars(tdocs, func(p string, n *yaml.Node) {
		values[p] = append(values[p], n.Value)
		secret = secret || isRedacted(n.Value)
	}, func(p string, k *yaml.Node) {
		if isRedacted(k.Value) {
			secret = true
		} else if keys[p] == nil {
			keys[p] = map[string]bool{k.Value: true}
		} else {
			keys[p][k.Value] = true
		}
	})
	bdocs, err := yamlDocs(base)
	if err != nil {
		return "", "", err
	}
	count := map[string]int{}
	walkScalars(bdocs, func(p string, _ *yaml.Node) { count[p]++ }, nil)
	walkScalars(bdocs, func(p string, n *yaml.Node) {
		// A base leaf that is one whole token shows nothing, nor does the one leaf of a path on both
		// sides when unchanged. Where a path holds several leaves, which one a token replaced is
		// unknown, so an equal target leaf exempts none; an embedded document holding a token is
		// not a token.
		unchanged := count[p] == 1 && len(values[p]) == 1 && values[p][0] == n.Value
		if secret && !isToken(n.Value) && !unchanged {
			*n = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: pairedToken}
		}
	}, func(p string, k *yaml.Node) {
		// A redacted target leaf or key may hold a base key's text, so a base key the target does not
		// show in the same mapping is paired too.
		if secret && !isToken(k.Value) && !keys[p][k.Value] {
			*k = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: pairedToken}
		}
	})
	paired, err := encodeDocs(bdocs)
	if err != nil {
		return "", "", err
	}
	shown, err := encodeDocs(tdocs)
	if err != nil {
		return "", "", err
	}
	return paired, shown, nil
}

var errPairAlias = errors.New("api: a redacted configuration holds an alias")

// yamlDocs parses every document of a YAML stream into nodes, duplicate keys included.
func yamlDocs(s string) ([]*yaml.Node, error) {
	dec := yaml.NewDecoder(strings.NewReader(s))
	var docs []*yaml.Node
	for {
		var n yaml.Node
		if err := dec.Decode(&n); errors.Is(err, io.EOF) {
			return docs, nil
		} else if err != nil {
			return nil, err
		}
		if hasAlias(&n) {
			return nil, errPairAlias
		}
		docs = append(docs, &n)
	}
}

// hasAlias reports whether n or any node under it, mapping keys included, is an alias.
func hasAlias(n *yaml.Node) bool {
	if n.Kind == yaml.AliasNode {
		return true
	}
	return slices.ContainsFunc(n.Content, hasAlias)
}

// isToken reports whether a scalar is one whole redaction token, `<redacted>` or `<redacted:…>`
// (compilation §8.3).
func isToken(v string) bool {
	return (v == "<redacted>" || strings.HasPrefix(v, "<redacted:")) && strings.HasSuffix(v, ">") &&
		strings.Count(v, "<") == 1 && strings.Count(v, ">") == 1
}

// isRedacted reports whether a scalar holds a redaction token, in an embedded JSON document with
// its angle bracket escaped, matched without the backslash.
func isRedacted(v string) bool {
	return strings.Contains(v, "<redacted") || strings.Contains(v, "u003credacted")
}

// walkScalars calls fn on every scalar leaf of docs with its path, and keyFn, when set, on every
// scalar mapping key with its mapping's path, after the key's value has its path, so keyFn may
// rewrite the key; yamlDocs has refused aliases.
func walkScalars(docs []*yaml.Node, fn, keyFn func(p string, n *yaml.Node)) {
	esc := strings.NewReplacer("~", "~0", "/", "~1")
	var walk func(n *yaml.Node, p string)
	walk = func(n *yaml.Node, p string) {
		switch n.Kind {
		case yaml.DocumentNode:
			for _, c := range n.Content {
				walk(c, p)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k := "?" // a key that is not a scalar names no path of its own
				if n.Content[i].Kind == yaml.ScalarNode {
					k = esc.Replace(n.Content[i].Value)
					if keyFn != nil {
						keyFn(p, n.Content[i])
					}
				}
				walk(n.Content[i+1], p+"/"+k)
			}
		case yaml.SequenceNode:
			for i, c := range n.Content {
				walk(c, p+"/"+strconv.Itoa(i))
			}
		case yaml.ScalarNode:
			fn(p, n)
		}
	}
	for i, d := range docs {
		walk(d, "doc"+strconv.Itoa(i))
	}
}

// encodeDocs writes docs as the redacted configuration is written: two-space indentation.
func encodeDocs(docs []*yaml.Node) (string, error) {
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	for _, d := range docs {
		if err := enc.Encode(d); err != nil {
			return "", err
		}
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return b.String(), nil
}
