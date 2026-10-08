package api

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/id"
)

// testExecution is a deployment's execution block at its defaults, with a five-minute transport
// maximum.
func testExecution() config.Execution {
	return config.Execution{SettleFloor: 30 * time.Second, MaxTransportDeadline: 5 * time.Minute,
		PlanDefaults: config.PlanDefaults{Expiry: 24 * time.Hour, MaxObservationAge: 5 * time.Minute, CheckValidity: 5 * time.Minute,
			TransportDeadline: time.Minute, VerificationDeadline: 10 * time.Minute, MaxAttempts: 3}}
}

// assignment returns machine's assignment and its head revision, creating both on first use.
func (d *draftEnv) assignment(machine string) [2]string {
	d.t.Helper()
	if h, ok := d.heads[machine]; ok {
		return h
	}
	h := [2]string{id.New(id.Assignment), id.New(id.AssignmentRevision)}
	mustExec(d.t, d.db, `INSERT INTO assignment_revision (id, cluster, machine, author, created_at) VALUES ($1, $2, $3, $4, now())`,
		h[1], d.cluster, machine, d.seed)
	mustExec(d.t, d.db, `INSERT INTO assignment (id, cluster, machine, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, $2, $3, $4, 1, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, h[0], d.cluster, machine, h[1])
	if d.heads == nil {
		d.heads = map[string][2]string{}
	}
	d.heads[machine] = h
	return h
}

// planEnv holds two releases of d.machine under one assignment revision: applied, its Applied,
// and target, its Desired, whose redacted configurations differ in one line.
type planEnv struct {
	*draftEnv
	applied, target releaseSeed
	appliedDigest   []byte
}

func newPlanEnv(t *testing.T) *planEnv {
	t.Helper()
	p := &planEnv{draftEnv: newDraftEnv(t), appliedDigest: bytes.Repeat([]byte{7}, 32)}
	p.assigned = true
	p.redacted = "machine:\n  type: worker\n  token: <redacted:schema>\n"
	p.applied = p.release(p.draft, 1, releaseProvenance)
	p.redacted = "machine:\n  type: controlplane\n  token: <redacted:schema>\n"
	p.target = p.release(p.secondDraft("k-draft2-0123456789"), 1, releaseProvenance)
	mustExec(t, p.db, `UPDATE machine_state SET desired = $2, applied_release = $3, applied_digest = $4, applied_source = 'adoption',
		baseline_revision = 4 WHERE machine = $1`, p.machine, p.target.rel, p.applied.rel, p.appliedDigest)
	return p
}

func (d *draftEnv) secondDraft(k string) string {
	d.t.Helper()
	rec := d.do(d.api, call{method: "POST", path: prefix + "/drafts", token: d.human("h-author"), key: k,
		body: `{"cluster":"` + d.cluster + `","title":"another"}`})
	return decode[draftBody](d.t, rec, http.StatusCreated).ID
}

func (d *draftEnv) plan(token, k, body string) *httptest.ResponseRecorder {
	return d.do(d.api, call{method: "POST", path: prefix + "/plans", token: token, key: k, body: body})
}

func applyBody(rel, machine, extra string) string {
	return fmt.Sprintf(`{"releaseId":%q,"machine":%q,"operation":"apply-config","mode":"no-reboot"%s}`, rel, machine, extra)
}

func adoptBody(rel, machine, extra string) string {
	return fmt.Sprintf(`{"releaseId":%q,"machine":%q,"operation":"adopt"%s}`, rel, machine, extra)
}

// T4 (persistence-api.md §5, §9.2; execution-recovery.md §2): a publisher's apply-config plan for
// the machine's Desired binds every value the plan table holds, at the deployment's defaults when
// the request leaves them out, with the redacted whole-configuration diff from Applied as its
// evidence; it is proposed, one plan entry on the machine's timeline, and its act names it. A
// replay answers the same plan.
func TestPlanCreation(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	body := applyBody(p.target.rel, p.machine, "")
	rec := p.plan(p.robot, "k-plan-0123456789ab", body)
	b := decode[planBody](t, rec, http.StatusCreated)
	if loc := rec.Header().Get("Location"); loc != prefix+"/plans/"+b.ID || id.MustHave(b.ID, id.Plan) != nil {
		t.Fatalf("Location %q, id %q", loc, b.ID)
	}
	var route, epoch string
	var counter int64
	if err := p.db.QueryRow(`SELECT m.talos_endpoint, m.revision_counter, i.epoch FROM machine m, installation_state i WHERE m.id = $1`,
		p.machine).Scan(&route, &counter, &epoch); err != nil {
		t.Fatal(err)
	}
	asr := p.heads[p.machine][1]
	if b.Revision != 1 || b.State != "proposed" || b.CommittedOperation != nil || b.Approval != nil || b.Release != p.target.rel ||
		b.Machine != p.machine || b.Cluster != p.cluster || b.Operation != "apply-config" || b.Mode == nil || *b.Mode != "no-reboot" ||
		b.CreatedBy != (planCreator{Principal: p.robotID, Role: "publisher"}) || b.AssignmentRevision != asr ||
		b.DesiredRelease == nil || *b.DesiredRelease != p.target.rel || b.BaselineRevision == nil || *b.BaselineRevision != 4 ||
		b.Route != route || b.MaxObservationAgeSeconds != 300 || b.CheckValiditySeconds != 300 ||
		b.TransportDeadlineSeconds == nil || *b.TransportDeadlineSeconds != 60 ||
		b.VerificationDeadlineSeconds == nil || *b.VerificationDeadlineSeconds != 600 || b.MaxAttempts == nil || *b.MaxAttempts != 3 ||
		b.RolloutLimit != 1 || b.ApprovalPolicy != "one-approver" || b.Epoch != epoch || b.TimelineRevision != counter ||
		b.ExpiresAt.Sub(b.CreatedAt) != 24*time.Hour {
		t.Fatalf("plan %+v", b)
	}
	wantDiff := "@@ -1,3 +1,3 @@\n machine:\n-  type: worker\n+  type: controlplane\n   token: <redacted:schema>\n"
	if d := b.Evidence.Diff; d == nil || d.From != p.applied.rel || d.To != p.target.rel || d.Withheld || d.Unified != wantDiff {
		t.Fatalf("diff %+v", b.Evidence.Diff)
	}
	if v := b.Evidence.Validation; v != (planValidation{Result: "passed", At: "publication", Contract: "v1.13",
		KubernetesVersion: "v1.36.0", MachineryVersion: "v1.13.6", PlatformMode: "container"}) || b.Evidence.Baseline != nil ||
		b.Evidence.DryRun.Available || b.Evidence.DryRun.Reason == "" {
		t.Fatalf("evidence %+v", b.Evidence)
	}
	// The stored plan: the expected digest is Applied's (choice §10.23), never answered.
	if n := count(t, p.db, `SELECT count(*) FROM plan p JOIN plan_state s ON s.plan = p.id
		WHERE p.id = $1 AND p.expected_digest = $2 AND p.idempotency_key = $4 AND p.created_by_kind = 'service'
			AND p.transport_deadline = interval '60 seconds' AND p.verification_deadline = interval '600 seconds'
			AND p.expires_at = p.created_at + interval '24 hours' AND s.state = 'proposed' AND s.revision = 1
			AND p.evidence -> 'diff' ->> 'unified' = $3`, b.ID, p.appliedDigest, wantDiff, "k-plan-0123456789ab"); n != 1 {
		t.Fatalf("%d stored plans", n)
	}
	if strings.Contains(rec.Body.String(), "digest") {
		t.Fatalf("an answer naming a digest: %s", rec.Body)
	}
	if n := count(t, p.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND revision = $2 AND kind = 'plan'
		AND entry = jsonb_build_object('plan', $3::text, 'operation', 'apply-config', 'release', $4::text, 'principal', $5::text,
			'role', 'publisher')`, p.machine, counter, b.ID, p.target.rel, p.robotID); n != 1 {
		t.Fatalf("%d plan entries", n)
	}
	if n := count(t, p.db, `SELECT count(*) FROM act WHERE action = 'plan.create' AND subjects = ARRAY[$1, $2]
		AND idempotency_key = $3 AND role = 'publisher'`, b.ID, p.machine, "k-plan-0123456789ab"); n != 1 {
		t.Fatalf("%d acts", n)
	}
	// A replay answers the same plan and writes nothing.
	again := p.plan(p.robot, "k-plan-0123456789ab", body)
	if again.Code != http.StatusCreated || again.Body.String() != rec.Body.String() || count(t, p.db, `SELECT count(*) FROM plan`) != 1 {
		t.Fatalf("replay %d %s", again.Code, again.Body)
	}
}

// A request's durations and attempt limit replace the defaults, each bound as given.
func TestPlanCreationDurations(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	rec := p.plan(p.human("h-publisher"), "k-plan-dur-0123456789", applyBody(p.target.rel, p.machine,
		`,"expiresInSeconds":7200,"maxObservationAgeSeconds":45,"checkValiditySeconds":90,"transportDeadlineSeconds":300,`+
			`"verificationDeadlineSeconds":300,"maxAttempts":10`))
	b := decode[planBody](t, rec, http.StatusCreated)
	if b.ExpiresAt.Sub(b.CreatedAt) != 2*time.Hour || b.MaxObservationAgeSeconds != 45 || b.CheckValiditySeconds != 90 ||
		*b.TransportDeadlineSeconds != 300 || *b.VerificationDeadlineSeconds != 300 || *b.MaxAttempts != 10 {
		t.Fatalf("plan %+v", b)
	}
	if n := count(t, p.db, `SELECT count(*) FROM plan WHERE id = $1 AND created_by_kind = 'human' AND max_observation_age = interval '45 s'
		AND check_validity = interval '90 s' AND transport_deadline = interval '5 min' AND max_attempts = 10`, b.ID); n != 1 {
		t.Fatalf("%d stored", n)
	}
}

// An apply-config plan whose Applied or target configuration could not be redacted shows no diff
// and says so (compilation §8.3).
func TestPlanCreationDiffWithheld(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	// machine2 of each release has no redacted configuration.
	m2 := p.target.machine2
	mustExec(t, p.db, `UPDATE machine_state SET desired = $2, applied_release = $2, applied_digest = $3, applied_source = 'adoption',
		baseline_revision = 1 WHERE machine = $1`, m2, p.target.rel, p.appliedDigest)
	b := decode[planBody](t, p.plan(p.robot, "k-plan-withheld-0123", applyBody(p.target.rel, m2, "")), http.StatusCreated)
	if d := b.Evidence.Diff; d == nil || !d.Withheld || d.Unified != "" || d.From != p.target.rel || d.To != p.target.rel {
		t.Fatalf("diff %+v", b.Evidence.Diff)
	}
}

// An adopt plan (execution-recovery.md §6.3) for a machine with no Applied binds its Desired at
// creation, or none, and no mode, baseline, expected digest, deadlines or attempts; its evidence
// names the baseline and whether the release's artifact differs from it (pending convergence).
func TestPlanCreationAdopt(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	mustExec(t, p.db, `UPDATE machine_state SET applied_release = NULL, applied_digest = NULL, applied_source = NULL,
		baseline_revision = NULL, desired = NULL WHERE machine = $1`, p.machine)
	rec := p.plan(p.robot, "k-adopt-0123456789", adoptBody(p.applied.rel, p.machine, `,"maxObservationAgeSeconds":60`))
	b := decode[planBody](t, rec, http.StatusCreated)
	if b.Operation != "adopt" || b.Mode != nil || b.DesiredRelease != nil || b.BaselineRevision != nil || b.TransportDeadlineSeconds != nil ||
		b.VerificationDeadlineSeconds != nil || b.MaxAttempts != nil || b.MaxObservationAgeSeconds != 60 || b.Evidence.Diff != nil ||
		b.Evidence.Baseline == nil || b.Evidence.Baseline.ImportBaseRevision != p.applied.ibr || b.Evidence.Baseline.PendingConvergence {
		t.Fatalf("adopt %+v %+v", b, b.Evidence.Baseline)
	}
	if n := count(t, p.db, `SELECT count(*) FROM plan WHERE id = $1 AND kind = 'adopt' AND expected_digest IS NULL
		AND desired_release IS NULL AND max_attempts IS NULL`, b.ID); n != 1 {
		t.Fatalf("%d stored", n)
	}
	// With a Desired, the plan binds it; an artifact whose digest is not the baseline's is pending
	// convergence.
	p.artifact = bytes.Repeat([]byte{9}, 32)
	other := p.release(p.secondDraft("k-draft3-0123456789"), 1, releaseProvenance)
	mustExec(t, p.db, `UPDATE machine_state SET desired = $2 WHERE machine = $1`, p.machine, p.target.rel)
	b = decode[planBody](t, p.plan(p.robot, "k-adopt2-0123456789", adoptBody(other.rel, p.machine, "")), http.StatusCreated)
	if b.DesiredRelease == nil || *b.DesiredRelease != p.target.rel || b.Evidence.Baseline == nil ||
		b.Evidence.Baseline.ImportBaseRevision != other.ibr || !b.Evidence.Baseline.PendingConvergence {
		t.Fatalf("adopt %+v %+v", b, b.Evidence.Baseline)
	}
}

// T4's refusals (persistence-api.md §5, §14; execution-recovery.md §2): each writes nothing.
func TestPlanCreationRefusals(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	pub := p.robot
	other := p.target.machine2 // covered by the target release only, no Applied, Desired NULL
	cases := []struct {
		name, token, body, setup string
		status                   int
		code                     string
		member                   [2]string // a problem member and its value, when the refusal names one
	}{
		{name: "author role", token: p.human("h-author"), body: applyBody(p.target.rel, p.machine, ""), status: 403, code: "forbidden"},
		{name: "no such machine", body: applyBody(p.target.rel, id.New(id.Machine), ""), status: 404, code: "not-found"},
		{name: "no such release", body: applyBody(id.New(id.Release), p.machine, ""), status: 404, code: "not-found"},
		{name: "malformed machine", body: applyBody(p.target.rel, "mch_x", ""), status: 400, code: "invalid-request"},
		{name: "unknown operation", body: `{"releaseId":"` + p.target.rel + `","machine":"` + p.machine + `","operation":"revert"}`,
			status: 400, code: "invalid-request"},
		{name: "apply-config without mode", body: `{"releaseId":"` + p.target.rel + `","machine":"` + p.machine + `","operation":"apply-config"}`,
			status: 400, code: "invalid-request"},
		{name: "adopt with mode", body: adoptBody(p.target.rel, other, `,"mode":"no-reboot"`), status: 400, code: "invalid-request"},
		{name: "adopt with deadline", body: adoptBody(p.target.rel, other, `,"transportDeadlineSeconds":30`), status: 400, code: "invalid-request"},
		{name: "adopt with attempts", body: adoptBody(p.target.rel, other, `,"maxAttempts":2`), status: 400, code: "invalid-request"},
		{name: "zero seconds", body: applyBody(p.target.rel, p.machine, `,"checkValiditySeconds":0`), status: 400, code: "invalid-request"},
		{name: "over a week", body: applyBody(p.target.rel, p.machine, `,"expiresInSeconds":604801`), status: 400, code: "invalid-request"},
		{name: "fraction", body: applyBody(p.target.rel, p.machine, `,"expiresInSeconds":1.5`), status: 400, code: "invalid-request"},
		{name: "attempts over ten", body: applyBody(p.target.rel, p.machine, `,"maxAttempts":11`), status: 400, code: "invalid-request"},
		{name: "attempts zero", body: applyBody(p.target.rel, p.machine, `,"maxAttempts":0`), status: 400, code: "invalid-request"},
		{name: "transport above maximum", body: applyBody(p.target.rel, p.machine, `,"transportDeadlineSeconds":301,"verificationDeadlineSeconds":900`),
			status: 400, code: "invalid-request"},
		{name: "transport above verification", body: applyBody(p.target.rel, p.machine, `,"transportDeadlineSeconds":120,"verificationDeadlineSeconds":90`),
			status: 400, code: "invalid-request"},
		{name: "default transport above verification", body: applyBody(p.target.rel, p.machine, `,"verificationDeadlineSeconds":30`),
			status: 400, code: "invalid-request"},
		{name: "release not covering the machine", setup: "uncovered", status: 422, code: "validation-failed"},
		{name: "not Desired", body: applyBody(p.applied.rel, p.machine, ""), status: 409, code: "conflict", member: [2]string{"desired", p.target.rel}},
		{name: "no Applied", body: applyBody(p.target.rel, other, ""), status: 409, code: "conflict", setup: "desire-other"},
		{name: "adopt with Applied", body: adoptBody(p.target.rel, p.machine, ""), status: 409, code: "conflict"},
		{name: "assignment moved", body: applyBody(p.target.rel, p.machine, ""), setup: "move-head", status: 409, code: "conflict"},
		{name: "pre-restore unaccounted", body: applyBody(p.target.rel, p.machine, ""), setup: "unaccounted", status: 409,
			code: "recovery-mode-active"},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := c.body
			switch c.setup {
			case "uncovered":
				rec := p.do(p.api, machineCall(p.human("h-author"), "k-machine-unc-012345", p.cluster, "0b5a6c1e-2f3d-4e5f-8a9b-00000000fe01"))
				body = applyBody(p.target.rel, decode[machineBody](t, rec, http.StatusCreated).ID, "")
			case "desire-other":
				mustExec(t, p.db, `UPDATE machine_state SET desired = $2 WHERE machine = $1`, other, p.target.rel)
				defer mustExec(t, p.db, `UPDATE machine_state SET desired = NULL WHERE machine = $1`, other)
			case "move-head":
				asr := id.New(id.AssignmentRevision)
				mustExec(t, p.db, `INSERT INTO assignment_revision (id, cluster, machine, author, created_at) VALUES ($1, $2, $3, $4, now())`,
					asr, p.cluster, p.machine, p.seed)
				mustExec(t, p.db, `UPDATE assignment SET head_revision_id = $2, head_revision = 2 WHERE machine = $1`, p.machine, asr)
				defer mustExec(t, p.db, `UPDATE assignment SET head_revision_id = $2, head_revision = 1 WHERE machine = $1`,
					p.machine, p.heads[p.machine][1])
			case "unaccounted":
				mustExec(t, p.db, `UPDATE machine SET scope_state = 'pre-restore-unaccounted' WHERE id = $1`, p.machine)
				defer mustExec(t, p.db, `UPDATE machine SET scope_state = 'normal' WHERE id = $1`, p.machine)
			}
			token := c.token
			if token == "" {
				token = pub
			}
			var before int64
			if err := p.db.QueryRow(`SELECT revision_counter FROM machine WHERE id = $1`, p.machine).Scan(&before); err != nil {
				t.Fatal(err)
			}
			rec := p.plan(token, fmt.Sprintf("k-refuse-%02d-0123456789", i), body)
			doc := wantProblem(t, rec, c.status, c.code)
			if c.member[0] != "" && doc[c.member[0]] != c.member[1] {
				t.Fatalf("problem %v, want %s %s", doc, c.member[0], c.member[1])
			}
			var after int64
			if err := p.db.QueryRow(`SELECT revision_counter FROM machine WHERE id = $1`, p.machine).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if n := count(t, p.db, `SELECT count(*) FROM plan`); n != 0 || after != before {
				t.Fatalf("%d plans, counter %d -> %d", n, before, after)
			}
		})
	}
	// The cases ran in order; the last left the machine as it was, so a valid request is accepted.
	decode[planBody](t, p.plan(pub, "k-plan-after-0123456", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
}
