package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// planFake serves one plan, pln_BWSYNTH, for machine mch_a, whose fields the test sets, and the
// reads planView makes; POST /plans and the cancellation answer with post, recording each body.
type planFake struct {
	mu                        sync.Mutex
	state, op, evidence       string
	approval, approvalEpoch   string
	desired, assignment, open string
	post                      func(w http.ResponseWriter, path string)
	bodies                    []string
}

const planJSON = `{"id":"pln_BWSYNTH","revision":2,"state":%q,"committedOperation":%s,"approval":%s,"cluster":"cl_BWSYNTH",` +
	`"machine":"mch_a","release":"rel_BWSYNTH","operation":%q,"mode":%s,"assignmentRevision":"asr_BWSYNTH-1","desiredRelease":"rel_BWSYNTH",` +
	`"baselineRevision":%s,"driftRecord":null,"route":"10.5.0.5:50000","maxObservationAgeSeconds":300,"checkValiditySeconds":60,` +
	`"transportDeadlineSeconds":%s,"verificationDeadlineSeconds":%s,"maxAttempts":%s,"rolloutLimit":1,"approvalPolicy":"one-approver",` +
	`"createdBy":{"principal":"svc_BWSYNTH","role":"publisher"},"epoch":"ep_BWSYNTH-1","timelineRevision":7,` +
	`"createdAt":"2026-11-01T06:00:00Z","expiresAt":%q,"evidence":%s}`

func (f *planFake) plan() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	null := func(s string) string {
		if s == "" {
			return "null"
		}
		return `"` + s + `"`
	}
	committed := map[bool]string{true: `"op_BWSYNTH-apply"`, false: "null"}[f.state == "committed"]
	mode, rev, deadlines := `"no-reboot"`, "4", []string{"60", "600", "3"}
	if f.op == "adopt" {
		mode, rev, deadlines = "null", "null", []string{"null", "null", "null"}
	}
	expires := time.Now().Add(24 * time.Hour)
	if f.state == "expired" {
		expires = time.Now().Add(-time.Hour)
	}
	return fmt.Sprintf(planJSON, f.state, committed, null(f.approval), f.op, mode, rev, deadlines[0], deadlines[1], deadlines[2],
		expires.UTC().Format(time.RFC3339), f.evidence)
}

func (f *planFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-2")
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	if r.Method == http.MethodPost {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.bodies = append(f.bodies, path+" "+string(b))
		f.post(w, path)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch path {
	case "/server":
		fmt.Fprint(w, `{"version":"BWSYNTH","apiRevision":2,"startMode":"normal"}`)
	case "/identity":
		fmt.Fprint(w, `{"kind":"service","principal":"svc_BWSYNTH"}`)
	case "/recovery":
		fmt.Fprint(w, `{"epoch":"ep_BWSYNTH-2","recoveryMode":false,"scopes":[]}`)
	case "/plans/pln_BWSYNTH":
		f.mu.Unlock()
		fmt.Fprint(w, f.plan())
		f.mu.Lock()
	case "/machines/mch_a":
		drift := "null"
		if f.open != "" {
			drift = `{"revision":` + f.open + `,"observation":"obs_BWSYNTH","lastObservation":"obs_BWSYNTH"}`
		}
		fmt.Fprintf(w, `{"id":"mch_a","cluster":"cl_BWSYNTH","talosEndpoint":"10.5.0.5:50000","desired":%q,"openDrift":%s}`, f.desired, drift)
	case "/clusters/cl_BWSYNTH":
		fmt.Fprint(w, `{"id":"cl_BWSYNTH","name":"lab-a"}`)
	case "/approvals/apr_BWSYNTH":
		fmt.Fprintf(w, `{"id":"apr_BWSYNTH","plan":"pln_BWSYNTH","epoch":%q,"approver":{"principal":"h-approver","role":"approver"},`+
			`"selfApproval":{"marked":false,"reasons":[]},"unseenOverwrite":false}`, f.approvalEpoch)
	case "/assignments":
		fmt.Fprintf(w, `{"items":[{"id":"asg_BWSYNTH-b","machine":"mch_b","revision":"asr_BWSYNTH-9"},{"id":"asg_BWSYNTH","machine":"mch_a","revision":%q}]}`, f.assignment)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *planFake) set(do func(*planFake)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	do(f)
}

func (f *planFake) took() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.bodies
	f.bodies = nil
	return b
}

const (
	validation = `"validation":{"result":"passed","at":"publication","contract":"v1.13","machineryVersion":"v1.13.6","kubernetesVersion":"v1.34.1","platformMode":"metal"},` +
		`"dryRun":{"available":false,"reason":"no dry run is available for this operation"}`
	diffEvidence     = `{"diff":{"from":"rel_BWSYNTH-old","to":"rel_BWSYNTH","withheld":false,"unified":"@@ -1 +1 @@\n-team: a\n+team: b\n"},` + validation + `}`
	withheldEvidence = `{"diff":{"from":"rel_BWSYNTH-old","to":"rel_BWSYNTH","withheld":true,"unified":""},` + validation + `}`
	sameEvidence     = `{"diff":{"from":"rel_BWSYNTH-old","to":"rel_BWSYNTH","withheld":false,"unified":""},` + validation + `}`
	adoptEvidence    = `{"baseline":{"importBaseRevision":"ibr_BWSYNTH","pendingConvergence":true},` + validation + `}`
)

func planServer(t *testing.T) *planFake {
	f := &planFake{state: "proposed", op: "apply-config", evidence: diffEvidence, desired: "rel_BWSYNTH", assignment: "asr_BWSYNTH-1"}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	automationContext(t, srv.URL)
	return f
}

func runCtl(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var o, e bytes.Buffer
	code := run(args, &o, &e)
	return code, o.String(), e.String()
}

// TestPlanView checks T20's display through plan show: every evidence kind in its own words, never
// "unchanged" from release IDs, and each state's line with what can be done next.
func TestPlanView(t *testing.T) {
	f := planServer(t)
	show := []string{"plan", "show", "pln_BWSYNTH"}
	again := "Create a new plan: bronzewardctl plan create --release 'rel_BWSYNTH' --machine 'mch_a' --operation apply-config"
	for _, c := range []struct {
		name string
		set  func(*planFake)
		want []string
	}{
		{"a diff", func(*planFake) {}, []string{
			"Plan pln_BWSYNTH  proposed", "(in 24h0m0s by this client's clock)",
			"Machine         mch_a  10.5.0.5:50000  cluster cl_BWSYNTH (lab-a), route 10.5.0.5:50000",
			"Operation       apply-config, mode no-reboot",
			"machine Desired: rel_BWSYNTH at plan creation, rel_BWSYNTH now",
			"asr_BWSYNTH-1", "baseline revision 4, drift record none",
			"transport 60s, verification 600s, observation age 300s, check validity 60s, attempts 3, rollout 1",
			"svc_BWSYNTH (publisher)", "epoch ep_BWSYNTH-1", "plan revision 2, timeline revision 7",
			"policy one-approver, committed operation none",
			"passed at publication (contract v1.13, machinery v1.13.6, Kubernetes v1.34.1, metal)",
			"Dry run     not available: no dry run is available for this operation",
			"Changes shown below: the whole redacted configuration, rel_BWSYNTH-old -> rel_BWSYNTH\n    @@ -1 +1 @@\n    -team: a\n    +team: b\n",
			"It is not approved and nothing runs until an approver approves: bronzewardctl approve 'pln_BWSYNTH'",
		}},
		{"a withheld diff", func(f *planFake) { f.evidence = withheldEvidence }, []string{"Diff withheld: the change cannot be shown (rel_BWSYNTH-old -> rel_BWSYNTH)"}},
		{"no differences", func(f *planFake) { f.evidence = sameEvidence }, []string{"No differences in the redacted configurations (rel_BWSYNTH-old -> rel_BWSYNTH)"}},
		{"an adopt plan's baseline", func(f *planFake) { f.op, f.evidence = "adopt", adoptEvidence }, []string{
			"adopt: records Applied from the node's own bytes and sends nothing to the machine",
			"transport -, verification -", "attempts -",
			"Baseline             import base ibr_BWSYNTH, the node's own bytes",
			"Pending convergence  yes: the release's compiled configuration differs from the node's",
		}},
		{"committed", func(f *planFake) { f.op, f.evidence, f.state = "apply-config", diffEvidence, "committed" },
			[]string{"Committed: operation op_BWSYNTH-apply. Follow it with: bronzewardctl operation watch 'op_BWSYNTH-apply'"}},
		{"expired", func(f *planFake) { f.state = "expired" }, []string{"Expired: it can no longer be approved or committed. " + again, "ago by this client's clock)"}},
		{"revoked", func(f *planFake) { f.state = "revoked" }, []string{"Revoked: its approval was revoked, so it cannot be committed. " + again}},
		{"cancelled", func(f *planFake) { f.state = "cancelled" }, []string{"Cancelled: it cannot be committed. " + again}},
		{"approved in an earlier epoch", func(f *planFake) { f.state, f.approval, f.approvalEpoch = "approved", "apr_BWSYNTH", "ep_BWSYNTH-1" }, []string{
			"Approved by h-approver (approver) in epoch ep_BWSYNTH-1. This approval is from before the last recovery entry and authorizes nothing; " +
				"an approver may approve again: bronzewardctl approve 'pln_BWSYNTH'"}},
		{"approved, nothing moved", func(f *planFake) { f.approvalEpoch = "ep_BWSYNTH-2" }, []string{
			"Approved by h-approver (approver) as apr_BWSYNTH. Not committed",
			"A change of the machine's baseline revision is served by no read: the controller's refusal shows it on the timeline: bronzewardctl machine timeline 'mch_a'"}},
		{"approved, Desired moved", func(f *planFake) { f.desired = "rel_BWSYNTH-new" }, []string{
			"Predicted stale, by this client's reads: the machine's Desired is now rel_BWSYNTH-new. The controller will refuse its commitment, so it cannot run. " + again}},
		{"approved, assignment moved and a drift opened", func(f *planFake) { f.desired, f.assignment, f.open = "rel_BWSYNTH", "asr_BWSYNTH-2", "12" }, []string{
			"Predicted stale, by this client's reads: the machine's open drift record is 12, not the plan's; the machine's assignment is now at asr_BWSYNTH-2."}},
	} {
		f.set(c.set)
		code, out, errOut := runCtl(t, show...)
		if code != 0 {
			t.Errorf("%s: exit %d, %s", c.name, code, errOut)
		}
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: no %q in\n%s", c.name, w, out)
			}
		}
		if strings.Contains(strings.ToLower(out), "unchanged") {
			t.Errorf("%s: the display says unchanged:\n%s", c.name, out)
		}
	}
	f.set(func(f *planFake) { f.state = "proposed" })
	if code, out, _ := runCtl(t, append(show, "-o", "json")...); code != 0 || strings.TrimSuffix(out, "\n") != f.plan() {
		t.Errorf("-o json: exit %d, printed %q; want the plan unchanged", code, out)
	}
}

// TestPlanCreate checks T19: the refusals before sending, the body with only the values given, the
// display of the answer, and each conflict's next step, which is named and not taken.
func TestPlanCreate(t *testing.T) {
	f := planServer(t)
	var answer struct {
		status int
		body   string
	}
	f.set(func(f *planFake) {
		f.post = func(w http.ResponseWriter, _ string) {
			if answer.status >= 400 {
				w.Header().Set("Content-Type", "application/problem+json")
			}
			w.WriteHeader(answer.status)
			fmt.Fprint(w, answer.body)
		}
	})
	create := []string{"plan", "create", "--release", "rel_BWSYNTH", "--machine", "mch_a"}
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"no operation", nil, "--operation: give apply-config or adopt"},
		{"an adopt plan with a mode", []string{"--operation", "adopt", "--mode", "no-reboot"}, "an adopt plan takes no --mode"},
		{"an adopt plan with a deadline", []string{"--operation", "adopt", "--transport-deadline", "1m"}, "nothing was sent"},
		{"an adopt plan with an attempt limit", []string{"--operation", "adopt", "--max-attempts", "2"}, "nothing was sent"},
		{"another mode", []string{"--operation", "apply-config", "--mode", "reboot"}, "no-reboot, its only mode"},
		{"part of a second", []string{"--operation", "apply-config", "--expires-in", "1500ms"}, "whole seconds"},
		{"no attempts", []string{"--operation", "apply-config", "--max-attempts", "0"}, "at least 1"},
	} {
		if code, out, errOut := runCtl(t, append(create, c.args...)...); code != 2 || out != "" || !strings.Contains(errOut, c.want) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want 2 and %q", c.name, code, out, errOut, c.want)
		}
	}
	if b := f.took(); len(b) != 0 {
		t.Errorf("refused before sending, yet sent %v", b)
	}

	answer.status, answer.body = http.StatusCreated, f.plan()
	code, out, errOut := runCtl(t, append(create, "--operation", "apply-config", "--expires-in", "2h", "--transport-deadline", "90s")...)
	if code != 0 || !strings.Contains(out, "Plan pln_BWSYNTH  proposed") || !strings.HasSuffix(out, "nothing runs until an approver approves: bronzewardctl approve 'pln_BWSYNTH'\n") {
		t.Errorf("apply-config: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	f.set(func(f *planFake) { f.op = "adopt" })
	answer.body = f.plan()
	if code, _, errOut := runCtl(t, append(create, "--operation", "adopt")...); code != 0 {
		t.Errorf("adopt: exit %d, %s", code, errOut)
	}
	sent := f.took()
	var bodies []map[string]any
	for _, s := range sent {
		var b map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(s, "/plans ")), &b); err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		bodies = append(bodies, b)
	}
	want := []string{`map[expiresInSeconds:7200 machine:mch_a mode:no-reboot operation:apply-config releaseId:rel_BWSYNTH transportDeadlineSeconds:90]`,
		`map[machine:mch_a operation:adopt releaseId:rel_BWSYNTH]`}
	if len(bodies) != 2 || fmt.Sprint(bodies[0]) != want[0] || fmt.Sprint(bodies[1]) != want[1] {
		t.Errorf("sent %v; want %v", bodies, want)
	}

	for _, c := range []struct{ name, problem, want string }{
		{"not the machine's Desired", `"detail":"an apply-config plan is for the machine's Desired release","desired":"rel_BWSYNTH-new"`,
			"Plan the machine's Desired release instead: bronzewardctl plan create --release 'rel_BWSYNTH-new' --machine 'mch_a' --operation apply-config"},
		{"no Applied", `"detail":"the machine has no Applied configuration to plan an apply-config from; adopt it first","machine":"mch_a"`,
			"Hand the machine over with an adopt plan: bronzewardctl plan create --release 'rel_BWSYNTH' --machine 'mch_a' --operation adopt"},
		{"assignment moved", `"detail":"the machine's assignment has moved since the release was compiled","release":"rel_BWSYNTH","machine":"mch_a"`,
			"Publish a release from the machine's current assignment, then plan that release. The assignment's history: bronzewardctl machine timeline 'mch_a'"},
	} {
		answer.status = http.StatusConflict
		answer.body = `{"type":"urn:bronzeward:problem:conflict","title":"Conflict","status":409,"instance":"urn:bronzeward:act:BWSYNTH",` + c.problem + `}`
		code, out, errOut := runCtl(t, append(create, "--operation", "apply-config")...)
		if code != 1 || out != "" || !strings.Contains(errOut, "409 Conflict (conflict)") || !strings.HasSuffix(errOut, c.want+"\n") {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want 1 and %q", c.name, code, out, errOut, c.want)
		}
		if b := f.took(); len(b) != 1 {
			t.Errorf("%s: sent %d plan creations; the next step is named, not taken", c.name, len(b))
		}
	}
}

// TestPlanCancel checks T22's limits as cancel prints them: a proposed plan cancelled, and a
// committed plan kept committed, whose operation ends only while no attempt is recorded.
func TestPlanCancel(t *testing.T) {
	f := planServer(t)
	f.set(func(f *planFake) {
		f.post = func(w http.ResponseWriter, _ string) {
			if f.state != "committed" {
				f.state = "cancelled"
			}
			fmt.Fprint(w, `{"plan":"pln_BWSYNTH","machine":"mch_a","cancelledBy":"svc_BWSYNTH","role":"publisher","reason":"BWSYNTH",`+
				`"act":"act_BWSYNTH","epoch":"ep_BWSYNTH-2","at":"2026-11-01T06:10:00Z"}`)
		}
	})
	cancel := []string{"plan", "cancel", "pln_BWSYNTH"}
	for _, reason := range [][]string{nil, {"--reason", ""}, {"--reason", strings.Repeat("x", 1025)}} {
		if code, _, errOut := runCtl(t, append(cancel, reason...)...); code != 2 || !strings.Contains(errOut, "1 to 1024 bytes; nothing was sent") {
			t.Errorf("reason %d bytes: exit %d, %s", len(reason), code, errOut)
		}
	}
	if b := f.took(); len(b) != 0 {
		t.Errorf("sent %v", b)
	}
	code, out, errOut := runCtl(t, append(cancel, "--reason", "superseded by BWSYNTH")...)
	if code != 0 || out != "Cancellation of plan pln_BWSYNTH recorded by svc_BWSYNTH (publisher), act act_BWSYNTH.\nThe plan is cancelled: it cannot be committed.\n" {
		t.Errorf("a proposed plan: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if b := f.took(); len(b) != 1 || b[0] != `/plans/pln_BWSYNTH/cancellations {"reason":"superseded by BWSYNTH"}` {
		t.Errorf("sent %v", b)
	}
	f.set(func(f *planFake) { f.state = "committed" })
	code, out, _ = runCtl(t, append(cancel, "--reason", "BWSYNTH")...)
	if code != 0 || !strings.Contains(out, "The plan stays committed to operation op_BWSYNTH-apply. A cancellation ends a committed plan's operation "+
		"only while no attempt is recorded; it does not stop an attempt already recorded.\nFollow it with: bronzewardctl operation watch 'op_BWSYNTH-apply'\n") {
		t.Errorf("a committed plan: exit %d, stdout %q", code, out)
	}
}
