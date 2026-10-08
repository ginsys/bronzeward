package api

import (
	"bytes"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/dbtest"
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
	return newPlanEnvWith(t, "machine:\n  type: worker\n  token: <redacted:schema>\n",
		"machine:\n  type: controlplane\n  token: <redacted:schema>\n")
}

// newPlanEnvWith is newPlanEnv with the Applied and target releases' redacted configurations.
func newPlanEnvWith(t *testing.T, applied, target string) *planEnv {
	t.Helper()
	p := &planEnv{draftEnv: newDraftEnv(t), appliedDigest: bytes.Repeat([]byte{7}, 32)}
	p.assigned = true
	p.redacted = applied
	p.artifact = p.appliedDigest // the node holds the Applied release's artifact
	p.applied = p.release(p.draft, 1, releaseProvenance)
	p.artifact = nil
	p.redacted = target
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
	// The target holds a token, so the changed base leaf is paired (compilation §8.3).
	wantDiff := "@@ -1,3 +1,3 @@\n machine:\n-  type: <redacted:paired>\n+  type: controlplane\n   token: <redacted:schema>\n"
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

// A committed creation replays whatever the deployment's defaults and maximum are now (§7.2): the
// checks that depend on them run after the key lookup, so only a new request meets them.
func TestPlanCreationReplayAfterConfigChange(t *testing.T) {
	p := newPlanEnv(t)
	for _, c := range []struct {
		name, key, extra string
		change           func(*config.Execution)
	}{
		{"transport default above the request's verification", "k-plan-cfg-default-012", `,"verificationDeadlineSeconds":90`,
			func(e *config.Execution) { e.PlanDefaults.TransportDeadline = 120 * time.Second }},
		{"maximum below the request's transport", "k-plan-cfg-maximum-012", `,"transportDeadlineSeconds":240`,
			func(e *config.Execution) {
				e.MaxTransportDeadline = time.Minute
				e.PlanDefaults.TransportDeadline = time.Minute
			}},
	} {
		p.api.d.exec = testExecution()
		body := applyBody(p.target.rel, p.machine, c.extra)
		first := p.plan(p.robot, c.key, body)
		if first.Code != http.StatusCreated {
			t.Fatalf("%s: first %d %s", c.name, first.Code, first.Body)
		}
		c.change(&p.api.d.exec)
		replay := p.plan(p.robot, c.key, body)
		if replay.Code != http.StatusCreated || replay.Header().Get("Idempotent-Replayed") != "true" || replay.Body.String() != first.Body.String() {
			t.Errorf("%s: replay %d %q %s", c.name, replay.Code, replay.Header().Get("Idempotent-Replayed"), replay.Body)
		}
		wantProblem(t, p.plan(p.robot, c.key+"-new", body), http.StatusBadRequest, "invalid-request")
	}
}

// An apply-config plan whose Applied or target configuration could not be redacted shows no diff
// and says so (compilation §8.3).
func TestPlanCreationDiffWithheld(t *testing.T) {
	t.Parallel()
	// The node holds the Applied release's artifact, so only the missing redacted form withholds.
	ok := "machine:\n  type: worker\n"
	for side, forms := range map[string][2]string{"applied": {noRedacted, ok}, "target": {ok, noRedacted}} {
		p := newPlanEnvWith(t, forms[0], forms[1])
		b := decode[planBody](t, p.plan(p.robot, "k-plan-withheld-0123", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
		if d := b.Evidence.Diff; d == nil || !d.Withheld || d.Unified != "" || d.From != p.applied.rel || d.To != p.target.rel {
			t.Fatalf("%s without a redacted form: diff %+v", side, b.Evidence.Diff)
		}
	}
}

// Once the target holds a redaction, a changed base leaf is shown <redacted:paired>, whatever its
// kind, so that a boolean or a short value cannot be read by elimination (compilation §8.3, §12.2);
// a base leaf beside a literal is too, since a redacted value may have come from it. A base leaf
// equal to its target, or one whole token, is shown as it is.
func TestPlanCreationDiffPaired(t *testing.T) {
	esc := string(rune(92)) + "u003c" // an embedded JSON document's escaped angle bracket
	p := newPlanEnvWith(t,
		"machine:\n  install:\n    wipe: false\n    disk: /dev/sda\n  certSANs:\n    - 10.0.0.1\n    - 10.0.0.2\n  network:\n    hostname: a\n  token: <redacted:schema>\n"+
			"  config: |\n    {\"k\": \"plain\"}\n",
		"machine:\n  install:\n    wipe: <redacted:install/wipe@1>\n    disk: /dev/sdb\n  certSANs:\n    - <redacted:san@1#0>\n    - 10.0.0.2\n  network:\n    hostname: <redacted:value>\n  token: <redacted:schema>\n"+
			"  config: |\n    {\"k\": \""+esc+"redacted:k@1>\"}\n")
	t.Parallel()
	b := decode[planBody](t, p.plan(p.robot, "k-plan-paired-0123456", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
	d := b.Evidence.Diff
	if d == nil || d.Withheld {
		t.Fatalf("diff %+v", d)
	}
	removed := map[string]bool{}
	for _, l := range strings.Split(d.Unified, "\n") {
		if strings.HasPrefix(l, "-") {
			removed[strings.TrimSpace(l[1:])] = true
		}
	}
	want := map[string]bool{"wipe: <redacted:paired>": true, "disk: <redacted:paired>": true, "- <redacted:paired>": true,
		"hostname: <redacted:paired>": true, "config: <redacted:paired>": true}
	if !reflect.DeepEqual(removed, want) {
		t.Fatalf("removed lines %v\ndiff:\n%s", removed, d.Unified)
	}
	for _, leak := range []string{"false", "10.0.0.1", "hostname: a", "plain", "/dev/sda"} {
		if strings.Contains(d.Unified, leak) {
			t.Fatalf("the diff shows %q beside a redacted leaf:\n%s", leak, d.Unified)
		}
	}
}

// Redaction can give two keys of one mapping the same token, as two label keys that are copies of
// values (compilation §8.3). The diff is still shown, and a base leaf under such a key is paired when
// the target has a redacted leaf there. Which base leaf a redacted one replaced cannot be told
// apart, so every plaintext base leaf at such a path is paired, even one equal to a target leaf.
func TestPlanCreationDiffDuplicateKeys(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ base, leak string }{
		{"machine:\n  nodeLabels:\n    <redacted:value>: east\n    <redacted:value>: west\n  token: <redacted:t@1>\n  type: worker\n", "east"},
		{"machine:\n  nodeLabels:\n    <redacted:value>: west\n    <redacted:value>: west\n  token: <redacted:t@1>\n  type: worker\n", "west"},
	} {
		// token is redacted and unchanged on both sides, so it shows no change.
		p := newPlanEnvWith(t, tc.base,
			"machine:\n  nodeLabels:\n    <redacted:value>: <redacted:zone@1>\n    <redacted:value>: west\n  token: <redacted:t@1>\n  type: controlplane\n")
		b := decode[planBody](t, p.plan(p.robot, "k-plan-dupkeys-012345", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
		d := b.Evidence.Diff
		if d == nil || d.Withheld {
			t.Fatalf("base %q: diff %+v", tc.base, d)
		}
		removed := map[string]bool{}
		for _, l := range strings.Split(d.Unified, "\n") {
			if strings.HasPrefix(l, "-") {
				removed[strings.TrimSpace(l[1:])] = true
				if strings.Contains(l, tc.leak) {
					t.Fatalf("base %q: removed line %q shows the replaced value\n%s", tc.base, l, d.Unified)
				}
			}
		}
		want := map[string]bool{"<redacted:value>: <redacted:paired>": true, "type: <redacted:paired>": true}
		if !reflect.DeepEqual(removed, want) {
			t.Fatalf("base %q: removed lines %v\ndiff:\n%s", tc.base, removed, d.Unified)
		}
	}
}

// A target mapping with a redacted key gives its leaves paths no base leaf shares, so every
// plaintext base leaf and key under it is paired, except a key the target shows in the same mapping.
func TestPlanCreationDiffRedactedKey(t *testing.T) {
	p := newPlanEnvWith(t,
		"machine:\n  nodeLabels:\n    enabled: \"false\"\n    zone: east\n    region: west\n  nodeTaints:\n    dedicated: infra\n",
		"machine:\n  nodeLabels:\n    region: west\n    <redacted:labels@1#0>: <redacted:labels@1#0>\n  nodeTaints:\n    dedicated: edge\n")
	t.Parallel()
	b := decode[planBody](t, p.plan(p.robot, "k-plan-redkey-0123456", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
	d := b.Evidence.Diff
	if d == nil || d.Withheld {
		t.Fatalf("diff %+v", d)
	}
	var removed []string
	for _, l := range strings.Split(d.Unified, "\n") {
		if strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---") {
			removed = append(removed, strings.TrimSpace(l[1:]))
		}
	}
	// The base keys are paired too: the redacted key may resolve to one of them. A key the target
	// shows itself, in the same mapping, is shown.
	paired := pairedToken + ": " + pairedToken
	if want := []string{paired, paired, "dedicated: " + pairedToken}; !reflect.DeepEqual(removed, want) {
		t.Fatalf("removed lines %q\ndiff:\n%s", removed, d.Unified)
	}
	if !strings.Contains(d.Unified, "\n     region: west\n") {
		t.Fatalf("the key both sides show is not shown unchanged:\n%s", d.Unified)
	}
}

// Pairing by path misses a base value that moved: a shifted sequence index, a renamed key, a value
// moved to another mapping. So once the target holds any redaction, every base leaf and key the
// target does not show unchanged at the same path is paired (compilation §8.3).
func TestPlanCreationDiffMoved(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, base, target, leak string }{
		{"shifted index", "machine:\n  certSANs:\n    - s3cret-san\n    - safe\n",
			"machine:\n  certSANs:\n    - safe\n    - <redacted:san@1#1>\n", "s3cret-san"},
		{"renamed key", "machine:\n  env:\n    OLD_PASSWORD: s3cret-env\n",
			"machine:\n  env:\n    NEW_PASSWORD: <redacted:env@1>\n", "s3cret-env"},
		{"key name", "machine:\n  env:\n    S3CRET_KEY_NAME: x\n  token: <redacted:t@1>\n",
			"machine:\n  env:\n    OTHER: x\n  token: <redacted:t@1>\n", "S3CRET_KEY_NAME"},
		{"other mapping", "machine:\n  a:\n    v: s3cret-moved\n  b: {}\n",
			"machine:\n  a: {}\n  b:\n    w: <redacted:w@1>\n", "s3cret-moved"},
		{"redacted key only", "machine:\n  env:\n    OLD: s3cret-k\n",
			"machine:\n  env:\n    <redacted:k@1>: x\n", "s3cret-k"},
		{"escaped token only", "machine:\n  old: s3cret-json\n  config: |\n    {\"k\": \"v\"}\n",
			"machine:\n  config: |\n    {\"k\": \"" + string(rune(92)) + "u003credacted:k@1>\"}\n", "s3cret-json"},
	} {
		p := newPlanEnvWith(t, tc.base, tc.target)
		b := decode[planBody](t, p.plan(p.robot, "k-plan-moved-01234567", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
		d := b.Evidence.Diff
		if d == nil || d.Withheld {
			t.Fatalf("%s: diff %+v", tc.name, d)
		}
		if strings.Contains(d.Unified, tc.leak) {
			t.Errorf("%s: the diff shows %q:\n%s", tc.name, tc.leak, d.Unified)
		}
	}
	// A target with no redaction pairs nothing.
	p := newPlanEnvWith(t, "machine:\n  certSANs:\n    - old-san\n    - safe\n", "machine:\n  certSANs:\n    - safe\n    - new-san\n")
	b := decode[planBody](t, p.plan(p.robot, "k-plan-moved-plain-012", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
	if d := b.Evidence.Diff; d == nil || d.Withheld || !strings.Contains(d.Unified, "-    - old-san") || strings.Contains(d.Unified, pairedToken) {
		t.Fatalf("plain diff %+v", d)
	}
}

// An embedded document holding a token is not itself a token: a changed one is paired whole even
// when its base already held another token, and an unchanged one shows no change. A whole token
// replaced by another, as a new secret version, is shown as itself.
func TestPlanCreationDiffEmbeddedMixed(t *testing.T) {
	p := newPlanEnvWith(t,
		"machine:\n  files:\n    - contents: |\n        existing: <redacted:existing@1>\n        enabled: false\n"+
			"    - contents: |\n        keep: same\n        other: <redacted:other@1>\n  rotated: <redacted:rotated@1>\n",
		"machine:\n  files:\n    - contents: |\n        existing: <redacted:existing@1>\n        enabled: <redacted:enabled@1>\n"+
			"    - contents: |\n        keep: same\n        other: <redacted:other@1>\n  rotated: <redacted:rotated@2>\n")
	t.Parallel()
	b := decode[planBody](t, p.plan(p.robot, "k-plan-embedded-01234", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
	d := b.Evidence.Diff
	if d == nil || d.Withheld {
		t.Fatalf("diff %+v", d)
	}
	var removed []string
	for _, l := range strings.Split(d.Unified, "\n") {
		if strings.HasPrefix(l, "-") {
			removed = append(removed, strings.TrimSpace(l[1:]))
		}
	}
	if want := []string{"- contents: " + pairedToken, "rotated: <redacted:rotated@1>"}; !reflect.DeepEqual(removed, want) {
		t.Fatalf("removed lines %q, want %q\ndiff:\n%s", removed, want, d.Unified)
	}
}

// A side that paired redaction cannot walk withholds the diff rather than show it unpaired.
func TestPlanCreationDiffUnpairable(t *testing.T) {
	t.Parallel()
	for _, base := range []string{
		"machine:\n  wipe: false\n  bad: [\n",              // does not parse
		"machine:\n  wipe: &w false\n  keep: *w\n  x: 1\n", // an alias, which one path cannot pair
		"machine:\n  wipe: &w false\n  *w : old\n  x: 1\n", // an alias as a mapping key
	} {
		p := newPlanEnvWith(t, base, "machine:\n  wipe: <redacted:install/wipe@1>\n  keep: <redacted:install/wipe@1>\n  x: 2\n")
		b := decode[planBody](t, p.plan(p.robot, "k-plan-unpaired-01234", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
		if d := b.Evidence.Diff; d == nil || !d.Withheld || d.Unified != "" {
			t.Fatalf("base %q: diff %+v", base, b.Evidence.Diff)
		}
	}
}

// A machine pending convergence (execution-recovery.md §6.3) holds a configuration that is not its
// Applied release's artifact, so the Applied release's redacted form is not what the plan changes:
// the diff is withheld even though both releases have one.
func TestPlanCreationDiffPendingConvergence(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	mustExec(t, p.db, `UPDATE machine_state SET applied_digest = $2 WHERE machine = $1`, p.machine, bytes.Repeat([]byte{5}, 32))
	b := decode[planBody](t, p.plan(p.robot, "k-plan-pending-012345", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
	if d := b.Evidence.Diff; d == nil || !d.Withheld || d.Unified != "" || d.From != p.applied.rel || d.To != p.target.rel {
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
	// The target release's artifact is its import base's configuration.
	rec := p.plan(p.robot, "k-adopt-0123456789", adoptBody(p.target.rel, p.machine, `,"maxObservationAgeSeconds":60`))
	b := decode[planBody](t, rec, http.StatusCreated)
	if b.Operation != "adopt" || b.Mode != nil || b.DesiredRelease != nil || b.BaselineRevision != nil || b.TransportDeadlineSeconds != nil ||
		b.VerificationDeadlineSeconds != nil || b.MaxAttempts != nil || b.MaxObservationAgeSeconds != 60 || b.Evidence.Diff != nil ||
		b.Evidence.Baseline == nil || b.Evidence.Baseline.ImportBaseRevision != p.target.ibr || b.Evidence.Baseline.PendingConvergence {
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

// GET /plans/{id} and GET /plans (persistence-api.md §9.2, §9.3) answer a plan as its creation
// did, with its current state and revision; any role reads them.
func TestPlanReads(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	created := p.plan(p.robot, "k-plan-read-0123456789", applyBody(p.target.rel, p.machine, ""))
	b := decode[planBody](t, created, http.StatusCreated)
	viewer := p.human("h-viewer")
	got := p.do(p.api, call{method: "GET", path: prefix + "/plans/" + b.ID, token: viewer})
	if got.Code != http.StatusOK || got.Body.String() != created.Body.String() {
		t.Fatalf("read %d %s\nwant %s", got.Code, got.Body, created.Body)
	}
	adopt := decode[planBody](t, p.plan(p.robot, "k-plan-read-adopt-0123", adoptBody(p.target.rel, p.target.machine2, "")),
		http.StatusCreated)
	mustExec(t, p.db, `UPDATE plan_state SET state = 'cancelled', reason = 'cancelled', revision = 2, updated_at = now() WHERE plan = $1`,
		adopt.ID)
	adopt.State, adopt.Revision = "cancelled", 2
	list := decode[listPage[planBody]](t, p.do(p.api, call{method: "GET", path: prefix + "/plans?limit=1", token: viewer}), http.StatusOK)
	if len(list.Items) != 1 || list.Next == "" {
		t.Fatalf("first page %+v", list)
	}
	rest := decode[listPage[planBody]](t, p.do(p.api, call{method: "GET",
		path: prefix + "/plans?cursor=" + url.QueryEscape(list.Next), token: viewer}), http.StatusOK)
	all := append(list.Items, rest.Items...)
	if len(all) != 2 || rest.Next != "" || all[0].ID >= all[1].ID {
		t.Fatalf("pages %+v %+v", list, rest)
	}
	for _, it := range all {
		want := b
		if it.ID == adopt.ID {
			want = adopt
		}
		if !reflect.DeepEqual(it, want) {
			t.Fatalf("listed %+v\nwant %+v", it, want)
		}
	}
	// §8.1: expiry is evaluated by the server clock whenever the plan is read; a terminal state stays.
	short := decode[planBody](t, p.plan(p.robot, "k-plan-read-short-0123", applyBody(p.target.rel, p.machine,
		`,"expiresInSeconds":1`)), http.StatusCreated)
	gone := decode[planBody](t, p.plan(p.robot, "k-plan-read-gone-01234", applyBody(p.target.rel, p.machine,
		`,"expiresInSeconds":1`)), http.StatusCreated)
	mustExec(t, p.db, `UPDATE plan_state SET state = 'cancelled', reason = 'cancelled', revision = 2, updated_at = now() WHERE plan = $1`,
		gone.ID)
	time.Sleep(time.Until(short.ExpiresAt.Add(100 * time.Millisecond)))
	for _, c := range []struct{ id, state string }{{short.ID, "expired"}, {gone.ID, "cancelled"}, {b.ID, "proposed"}} {
		got := decode[planBody](t, p.do(p.api, call{method: "GET", path: prefix + "/plans/" + c.id, token: viewer}), http.StatusOK)
		if got.State != c.state {
			t.Errorf("%s read after its expiry: state %q, want %q", c.id, got.State, c.state)
		}
	}
	wantProblem(t, p.do(p.api, call{method: "GET", path: prefix + "/plans/" + id.New(id.Plan), token: viewer}), http.StatusNotFound, "not-found")
	wantProblem(t, p.do(p.api, call{method: "GET", path: prefix + "/plans/" + b.ID + "?x=1", token: viewer}), http.StatusBadRequest,
		"invalid-request")
}

// A read whose transaction began before a plan's expiry but waited on the installation state past
// it reads the plan expired: expiry is judged when the plan is read, not when the read began (§8.1).
func TestPlanReadAfterLockWait(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	short := decode[planBody](t, p.plan(p.robot, "k-plan-wait-short-0123", applyBody(p.target.rel, p.machine,
		`,"expiresInSeconds":2`)), http.StatusCreated)
	lock, err := p.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback() }()
	if _, err := lock.Exec(`SELECT 1 FROM installation_state FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	viewer := p.human("h-viewer")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- p.do(p.api, call{method: "GET", path: prefix + "/plans/" + short.ID, token: viewer}) }()
	// The read's transaction has begun and waits on the installation state before the expiry.
	dbtest.WaitForLockWait(t, p.db)
	if !time.Now().Before(short.ExpiresAt) {
		t.Fatal("the read began waiting only after the plan expired; the setup is too slow to show anything")
	}
	time.Sleep(time.Until(short.ExpiresAt.Add(200 * time.Millisecond)))
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := decode[planBody](t, <-done, http.StatusOK); got.State != "expired" {
		t.Fatalf("read after waiting past expiry: state %q, want expired", got.State)
	}
}

// A creation whose transaction waited on the machine's row longer than its expiry still creates a
// plan that reads proposed: its creation time and expiry are read after the locks (PA §1.2 rule 4).
func TestPlanCreationAfterLockWait(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	lock, err := p.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback() }()
	if _, err := lock.Exec(`SELECT 1 FROM machine WHERE id = $1 FOR UPDATE`, p.machine); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- p.plan(p.robot, "k-plan-wait-create-01", applyBody(p.target.rel, p.machine, `,"expiresInSeconds":1`))
	}()
	dbtest.WaitForLockWait(t, p.db)
	time.Sleep(1200 * time.Millisecond)
	released := time.Now()
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	b := decode[planBody](t, <-done, http.StatusCreated)
	if !b.ExpiresAt.After(released) || b.ExpiresAt.Sub(b.CreatedAt) != time.Second {
		t.Fatalf("created %s, expires %s, lock released %s", b.CreatedAt, b.ExpiresAt, released)
	}
	var at time.Time
	if err := p.db.QueryRow(`SELECT e.at FROM machine_event e JOIN plan p ON p.machine = e.machine
		WHERE p.id = $1 AND e.kind = 'plan' AND e.entry->>'plan' = p.id`, b.ID).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if !at.Equal(b.CreatedAt) {
		t.Fatalf("timeline entry at %s, plan created %s", at, b.CreatedAt)
	}
	got := decode[planBody](t, p.do(p.api, call{method: "GET", path: prefix + "/plans/" + b.ID, token: p.human("h-viewer")}),
		http.StatusOK)
	if got.State != "proposed" {
		t.Fatalf("read at once: state %q, want proposed", got.State)
	}
}

// holdActOrder takes the act-order lock (auth.actOrderKey) in a transaction of its own, so a
// request's transaction waits on it last, after its effect (PA §1.2 rule 5).
func holdActOrder(t *testing.T, db *sql.DB) *sql.Tx {
	t.Helper()
	lock, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Rollback() })
	if _, err := lock.Exec(`SELECT pg_advisory_xact_lock($1, 0)`, 0x62776163); err != nil {
		t.Fatal(err)
	}
	return lock
}

// waitForLockWaits polls until n sessions of db's database wait on a lock.
func waitForLockWaits(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var got int
		if err := db.QueryRow(`SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got >= n {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("fewer than %d sessions waiting on a lock within 10s", n)
}

// A creation queued on the act-order lock already holds its creator's principal against a
// revocation of that creator, so the plan's reference to it after the lock waits on nothing: the
// two do not deadlock (PA §1.2 rule 5).
func TestPlanCreationRevocationOfCreatorNoDeadlock(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	lock := holdActOrder(t, p.db)
	created := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		created <- p.plan(p.robot, "k-plan-creator-0123456", applyBody(p.target.rel, p.machine, ""))
	}()
	waitForLockWaits(t, p.db, 1)
	recovery := p.human("h-recovery")
	revoked := make(chan *httptest.ResponseRecorder, 1)
	go func() { revoked <- revoke(p.env, recovery, key, `{"identity":"`+p.robotID+`","reason":"left"}`) }()
	waitForLockWaits(t, p.db, 2)
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	decode[planBody](t, <-created, http.StatusCreated)
	revocationOf(t, <-revoked)
	if p.logged("deadlocked") {
		t.Fatal("the creation and the revocation of its creator deadlocked")
	}
}

// The act-order wait is a lock wait too: a creation that waited on it past its expiry still reads
// proposed, its times read after that lock (PA §1.2 rule 4).
func TestPlanCreationAfterActOrderWait(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	lock := holdActOrder(t, p.db)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- p.plan(p.robot, "k-plan-wait-order-01", applyBody(p.target.rel, p.machine, `,"expiresInSeconds":1`))
	}()
	dbtest.WaitForLockWait(t, p.db)
	time.Sleep(1200 * time.Millisecond)
	released := time.Now()
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	b := decode[planBody](t, <-done, http.StatusCreated)
	if !b.ExpiresAt.After(released) || b.ExpiresAt.Sub(b.CreatedAt) != time.Second {
		t.Fatalf("created %s, expires %s, lock released %s", b.CreatedAt, b.ExpiresAt, released)
	}
	var at, updated time.Time
	if err := p.db.QueryRow(`SELECT e.at, s.updated_at FROM machine_event e JOIN plan p ON p.machine = e.machine
		JOIN plan_state s ON s.plan = p.id WHERE p.id = $1 AND e.kind = 'plan' AND e.entry->>'plan' = p.id`, b.ID).Scan(&at,
		&updated); err != nil {
		t.Fatal(err)
	}
	if !at.Equal(b.CreatedAt) || !updated.Equal(b.CreatedAt) {
		t.Fatalf("timeline entry at %s, state at %s, plan created %s", at, updated, b.CreatedAt)
	}
	got := decode[planBody](t, p.do(p.api, call{method: "GET", path: prefix + "/plans/" + b.ID, token: p.human("h-viewer")}),
		http.StatusOK)
	if got.State != "proposed" {
		t.Fatalf("read at once: state %q, want proposed", got.State)
	}
}

// approveAs records the approval of plan by approver as T5a does, which no route makes yet, and
// moves the plan to approved.
func (p *planEnv) approveAs(t *testing.T, plan, approver string) {
	t.Helper()
	tx, err := p.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var rev int64
	if err := tx.QueryRow(`UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1 RETURNING revision_counter`,
		p.machine).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	apr, act := id.New(id.Approval), id.New(id.Act)
	for _, s := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
			SELECT $1, $2, epoch, 'approval', '{}', now() FROM installation_state`, []any{p.machine, rev}},
		{`INSERT INTO act (id, principal, principal_kind, via, role, action, subjects, request_id, epoch, at)
			SELECT $1, $2, 'human', 'api', 'approver', 'plan', ARRAY[$3], $4, epoch, now() FROM installation_state`,
			[]any{act, approver, plan, id.New(id.Request)}},
		{`INSERT INTO approval (id, plan, machine, approver, role, epoch, self_approval, act, at, revision)
			SELECT $1, $2, $3, $4, 'approver', epoch, '{}', $5, now(), $6 FROM installation_state`,
			[]any{apr, plan, p.machine, approver, act, rev}},
		{`UPDATE plan_state SET state = 'approved', approval = $2, revision = revision + 1 WHERE plan = $1`, []any{plan, apr}},
	} {
		if _, err := tx.Exec(s.q, s.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// PA §8.1: a revocation of the approving identity is evaluated whenever the plan is read. An
// approved plan whose approver is revoked reads revoked, by id and in the list, unless it expired
// before the revocation; a plan whose approver is not revoked reads approved.
func TestPlanReadRevokedApprover(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	human := func(sub string) string {
		v := id.New(id.Principal)
		mustExec(t, p.db, `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', $2, $3, now())`,
			v, p.iss.URL, sub)
		return v
	}
	plan := func(k, extra string) planBody {
		return decode[planBody](t, p.plan(p.robot, k, applyBody(p.target.rel, p.machine, extra)), http.StatusCreated)
	}
	revoked, kept, late := plan("k-plan-revoked-012345", ""), plan("k-plan-kept-012345678", ""),
		plan("k-plan-late-012345678", `,"expiresInSeconds":1`)
	gone, stays := human("h-approver-gone"), human("h-approver-stays")
	p.approveAs(t, revoked.ID, gone)
	p.approveAs(t, late.ID, gone)
	p.approveAs(t, kept.ID, stays)
	time.Sleep(time.Until(late.ExpiresAt.Add(100 * time.Millisecond)))
	revocationOf(t, revoke(p.env, p.human("h-recovery"), key, `{"identity":"`+gone+`","reason":"left"}`))
	want := map[string]string{revoked.ID: "revoked", kept.ID: "approved", late.ID: "expired"}
	viewer := p.human("h-viewer")
	for plan, state := range want {
		if got := decode[planBody](t, p.do(p.api, call{method: "GET", path: prefix + "/plans/" + plan, token: viewer}),
			http.StatusOK); got.State != state {
			t.Errorf("%s read by id: state %q, want %q", plan, got.State, state)
		}
	}
	list := decode[listPage[planBody]](t, p.do(p.api, call{method: "GET", path: prefix + "/plans", token: viewer}), http.StatusOK)
	listed := map[string]string{}
	for _, it := range list.Items {
		if _, ok := want[it.ID]; ok {
			listed[it.ID] = it.State
		}
	}
	if !reflect.DeepEqual(listed, want) {
		t.Errorf("listed %v, want %v", listed, want)
	}
}

// A revocation that waited on the approver's principal row until after the plan's expiry is
// recorded after it: the plan reads expired, not revoked (PA §1.2 rule 4).
func TestPlanReadRevocationAfterLockWait(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	approver := id.New(id.Principal)
	mustExec(t, p.db, `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', $2, 'h-approver-held', now())`,
		approver, p.iss.URL)
	b := decode[planBody](t, p.plan(p.robot, "k-plan-held-012345678", applyBody(p.target.rel, p.machine, `,"expiresInSeconds":2`)),
		http.StatusCreated)
	p.approveAs(t, b.ID, approver)
	lock, err := p.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback() }()
	if _, err := lock.Exec(`SELECT 1 FROM principal WHERE id = $1 FOR SHARE`, approver); err != nil {
		t.Fatal(err)
	}
	recovery := p.human("h-recovery")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- revoke(p.env, recovery, key, `{"identity":"`+approver+`","reason":"left"}`) }()
	dbtest.WaitForLockWait(t, p.db)
	if !time.Now().Before(b.ExpiresAt) {
		t.Fatal("the revocation began waiting only after the plan expired; the setup is too slow to show anything")
	}
	time.Sleep(time.Until(b.ExpiresAt.Add(200 * time.Millisecond)))
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	revocationOf(t, <-done)
	if got := decode[planBody](t, p.do(p.api, call{method: "GET", path: prefix + "/plans/" + b.ID, token: p.human("h-viewer")}),
		http.StatusOK); got.State != "expired" {
		t.Fatalf("revoked after the expiry: state %q, want expired", got.State)
	}
}

// A revocation that waited on the act-order lock until after the plan's expiry is recorded after
// it: the plan reads expired, not revoked (PA §1.2 rule 4).
func TestPlanReadRevocationAfterActOrderWait(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	approver := id.New(id.Principal)
	mustExec(t, p.db, `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', $2, 'h-approver-order', now())`,
		approver, p.iss.URL)
	b := decode[planBody](t, p.plan(p.robot, "k-plan-order-012345678", applyBody(p.target.rel, p.machine, `,"expiresInSeconds":2`)),
		http.StatusCreated)
	p.approveAs(t, b.ID, approver)
	lock := holdActOrder(t, p.db)
	recovery := p.human("h-recovery")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- revoke(p.env, recovery, key, `{"identity":"`+approver+`","reason":"left"}`) }()
	dbtest.WaitForLockWait(t, p.db)
	if !time.Now().Before(b.ExpiresAt) {
		t.Fatal("the revocation began waiting only after the plan expired; the setup is too slow to show anything")
	}
	time.Sleep(time.Until(b.ExpiresAt.Add(200 * time.Millisecond)))
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	r := revocationOf(t, <-done)
	if !r.At.After(b.ExpiresAt) {
		t.Fatalf("revocation answered at %s, plan expires %s", r.At, b.ExpiresAt)
	}
	if got := decode[planBody](t, p.do(p.api, call{method: "GET", path: prefix + "/plans/" + b.ID, token: p.human("h-viewer")}),
		http.StatusOK); got.State != "expired" {
		t.Fatalf("revoked after the expiry: state %q, want expired", got.State)
	}
}
