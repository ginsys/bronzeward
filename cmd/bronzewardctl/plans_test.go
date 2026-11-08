package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
)

// planFake serves one plan, pln_BWSYNTH, for machine mch_a, whose fields the test sets, and the
// reads planView makes; POST /plans and the cancellation answer with post, recording each body.
// A path in raw is answered with its body, one in fail with a 404 problem whose detail it holds;
// epochFor, when set, gives each answer's epoch, ep_BWSYNTH-2 otherwise; a path busy accepts,
// asked after epochFor, is answered with a retryable 503.
type planFake struct {
	mu                        sync.Mutex
	state, op, evidence       string
	approval, approvalEpoch   string
	desired, assignment, open string
	drift                     string // the plan's drift record, "" for none
	noApplied                 bool   // the machine has no Applied configuration
	expiresIn                 time.Duration
	now                       time.Time // the expiry's clock when set, so two renders are equal
	raw, fail                 map[string]string
	epochFor                  func(path string) string
	busy                      func(path string) bool
	post                      func(w http.ResponseWriter, path string)
	bodies                    []string
}

const planJSON = `{"id":"pln_BWSYNTH","revision":2,"state":%q,"committedOperation":%s,"approval":%s,"cluster":"cl_BWSYNTH",` +
	`"machine":"mch_a","release":"rel_BWSYNTH","operation":%q,"mode":%s,"assignmentRevision":"asr_BWSYNTH-1","desiredRelease":"rel_BWSYNTH",` +
	`"baselineRevision":%s,"driftRecord":%s,"route":"10.5.0.5:50000","maxObservationAgeSeconds":300,"checkValiditySeconds":60,` +
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
	now := f.now
	if now.IsZero() {
		now = time.Now()
	}
	expires := now.Add(24 * time.Hour)
	if f.state == "expired" {
		expires = now.Add(-time.Hour)
	}
	if f.expiresIn != 0 {
		expires = now.Add(f.expiresIn)
	}
	drift := "null"
	if f.drift != "" {
		drift = f.drift
	}
	return fmt.Sprintf(planJSON, f.state, committed, null(f.approval), f.op, mode, rev, drift, deadlines[0], deadlines[1], deadlines[2],
		expires.UTC().Format(time.RFC3339), f.evidence)
}

func (f *planFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	f.mu.Lock()
	epoch := "ep_BWSYNTH-2"
	if f.epochFor != nil {
		epoch = f.epochFor(path)
	}
	raw, isRaw := f.raw[path]
	detail, fails := f.fail[path]
	busy := f.busy != nil && f.busy(path)
	f.mu.Unlock()
	w.Header().Set("Bronzeward-Epoch", epoch)
	w.Header().Set("Content-Type", "application/json")
	switch {
	case busy:
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"type":"urn:bronzeward:problem:dependency-unavailable","title":"Service Unavailable","status":503,"detail":"BWSYNTH busy"}`)
		return
	case fails && r.Method == http.MethodGet:
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		b, _ := json.Marshal(map[string]any{"type": "urn:bronzeward:problem:not-found", "title": "Not Found", "status": 404, "detail": detail})
		w.Write(b)
		return
	case isRaw && r.Method == http.MethodGet:
		fmt.Fprint(w, raw)
		return
	}
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
		applied := `{"release":"rel_BWSYNTH-old","source":"apply"}`
		if f.noApplied {
			applied = "null"
		}
		fmt.Fprintf(w, `{"id":"mch_a","cluster":"cl_BWSYNTH","talosEndpoint":"10.5.0.5:50000","desired":%q,"applied":%s,"openDrift":%s}`, f.desired, applied, drift)
	case "/clusters/cl_BWSYNTH":
		fmt.Fprint(w, `{"id":"cl_BWSYNTH","name":"lab-a"}`)
	case "/approvals/apr_BWSYNTH":
		fmt.Fprintf(w, `{"id":"apr_BWSYNTH","plan":"pln_BWSYNTH","epoch":%q,"approver":{"principal":"h-approver","role":"approver"},`+
			`"selfApproval":{"marked":false,"reasons":[]},"unseenOverwrite":false}`, f.approvalEpoch)
	case "/assignments":
		fmt.Fprintf(w, `{"items":[{"id":"asg_BWSYNTH-b","machine":"mch_b","revision":"asr_BWSYNTH-9"},{"id":"asg_BWSYNTH","machine":"mch_a","revision":%q}]}`, f.assignment)
	// approve's prediction reads: the release, its draft and its source revisions' authors. The
	// caller authored frv_a, which the draft introduced, and asr_BWSYNTH-1, which it reuses.
	case "/releases/rel_BWSYNTH":
		fmt.Fprint(w, `{"id":"rel_BWSYNTH","draft":"drf_BWSYNTH","publishedBy":{"principal":"svc_BWSYNTH","role":"publisher"},"sources":[`+
			`{"kind":"fragment","head":"frg_a","headRevision":1,"revision":"frv_a"},{"kind":"profile","head":"prf_b","headRevision":1,"revision":"prv_b"},`+
			`{"kind":"assignment","head":"asg_BWSYNTH","headRevision":1,"revision":"asr_BWSYNTH-1"},{"kind":"fragment","head":"frg_gone","headRevision":2,"revision":null}]}`)
	case "/drafts/drf_BWSYNTH":
		fmt.Fprint(w, `{"id":"drf_BWSYNTH","entries":[{"kind":"fragment","name":"a","head":"frg_a","base":null,"revision":"frv_a"},{"kind":"import-base","machine":"mch_a","revision":"ibr_BWSYNTH"}]}`)
	case "/fragment-revisions/frv_a", "/assignment-revisions/asr_BWSYNTH-1":
		fmt.Fprint(w, `{"author":"svc_BWSYNTH"}`)
	case "/profile-revisions/prv_b":
		fmt.Fprint(w, `{"author":"h-other"}`)
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
	validation = `"validation":{"result":"passed","at":"publication","contract":"v1.14","machineryVersion":"v1.14.5","kubernetesVersion":"v1.34.1","platformMode":"metal"},` +
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
	again := "Create a new plan: bronzewardctl plan create --release 'rel_BWSYNTH' --machine 'mch_a' --operation 'apply-config'"
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
			"passed at publication (contract v1.14, machinery v1.14.5, Kubernetes v1.34.1, metal)",
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
		{"revoked", func(f *planFake) { f.state = "revoked" }, []string{"Revoked: its approval or its approver's identity was revoked, so it cannot be committed. " + again}},
		{"cancelled", func(f *planFake) { f.state = "cancelled" }, []string{"Cancelled: it cannot be committed. " + again}},
		{"approved in an earlier epoch", func(f *planFake) { f.state, f.approval, f.approvalEpoch = "approved", "apr_BWSYNTH", "ep_BWSYNTH-1" }, []string{
			"Approved by h-approver (approver) in epoch ep_BWSYNTH-1. This approval is from before the last recovery entry and authorizes nothing; " +
				"an approver may approve again: bronzewardctl approve 'pln_BWSYNTH'\n" + again}},
		{"approved, nothing moved", func(f *planFake) { f.approvalEpoch = "ep_BWSYNTH-2" }, []string{
			"Approved by h-approver (approver) as apr_BWSYNTH. Not committed",
			"A change of the machine's baseline revision is served by no read: the controller's refusal shows it on the timeline: bronzewardctl machine timeline 'mch_a'"}},
		{"approved, Desired moved", func(f *planFake) { f.desired = "rel_BWSYNTH-new" }, []string{
			"Predicted stale, by this client's reads: the machine's Desired is now rel_BWSYNTH-new. The controller will refuse its commitment, so it cannot run. " + again}},
		{"approved, assignment moved and a drift opened", func(f *planFake) { f.desired, f.assignment, f.open = "rel_BWSYNTH", "asr_BWSYNTH-2", "12" }, []string{
			"Predicted stale, by this client's reads: the machine's open drift record is 12, not the plan's; the machine's assignment is now at asr_BWSYNTH-2."}},
		{"approved, Desired moved, assignment unread", func(f *planFake) {
			f.desired, f.assignment, f.open, f.fail = "rel_BWSYNTH-new", "asr_BWSYNTH-1", "", map[string]string{"/assignments": "BWSYNTH no list"}
		}, []string{
			"Predicted stale, by this client's reads: the machine's Desired is now rel_BWSYNTH-new. The controller will refuse its commitment, so it cannot run. " + again + "\n" +
				"Whether it is stale is not fully predicted: the assignment cannot be read: ", "BWSYNTH no list"}},
		{"cluster unread", func(f *planFake) { f.fail = map[string]string{"/clusters/cl_BWSYNTH": "BWSYNTH no cluster"} }, []string{
			"cluster cl_BWSYNTH (unread: ", "BWSYNTH no cluster"}},
		{"approved, approval unread, Desired moved", func(f *planFake) { f.fail = map[string]string{"/approvals/apr_BWSYNTH": "BWSYNTH no approval"} }, []string{
			"Approved by apr_BWSYNTH, which cannot be read: ", "BWSYNTH no approval",
			"Predicted stale, by this client's reads: the machine's Desired is now rel_BWSYNTH-new. The controller will refuse its commitment, so it cannot run. " + again}},
		{"approved, machine unread, assignment moved", func(f *planFake) {
			f.assignment, f.fail = "asr_BWSYNTH-2", map[string]string{"/machines/mch_a": "BWSYNTH no machine"}
		}, []string{
			"Approved by h-approver (approver) as apr_BWSYNTH.",
			"Predicted stale, by this client's reads: the machine's assignment is now at asr_BWSYNTH-2. The controller will refuse its commitment, so it cannot run. " + again + "\n" +
				"Whether it is stale is not fully predicted: the machine cannot be read: ", "BWSYNTH no machine"}},
		{"less than a minute left", func(f *planFake) { f.state, f.fail, f.expiresIn = "proposed", nil, 20*time.Second }, []string{
			"proposed               expires ", "(in 0s by this client's clock)"}},
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
	// One clock for the served answer and the expected one: a second boundary between them would
	// otherwise change expiresAt (ginsys/bronzeward#576).
	f.set(func(f *planFake) { f.state, f.expiresIn, f.now = "proposed", 0, time.Now() })
	if code, out, _ := runCtl(t, append(show, "-o", "json")...); code != 0 || strings.TrimSuffix(out, "\n") != f.plan() {
		t.Errorf("-o json: exit %d, printed %q; want the plan unchanged", code, out)
	}
}

// TestPlanSnapshot checks client-contracts.md section 8.5 on the plan view: an epoch change
// between the plan's answer and the view's last read, or a change and a change back, shows no
// view. plan show then fails; plan create still reports the plan it created.
func TestPlanSnapshot(t *testing.T) {
	// moved answers ep_BWSYNTH-3 from the machine read on; back only to the machine read, so the
	// GET /server that a new epoch calls for brings ep_BWSYNTH-2 back.
	moved := func(back bool) func(*planFake) {
		return func(f *planFake) {
			seen := false
			f.epochFor = func(path string) string {
				if path == "/machines/mch_a" || seen && !back {
					seen = true
					return "ep_BWSYNTH-3"
				}
				return "ep_BWSYNTH-2"
			}
		}
	}
	for _, c := range []struct {
		name string
		back bool
		want string
	}{
		{"moved", false, "the epoch changed from ep_BWSYNTH-2 to ep_BWSYNTH-3"},
		{"moved and back", true, "the epoch changed from ep_BWSYNTH-2 and changed back"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := planServer(t)
			f.set(moved(c.back))
			code, out, errOut := runCtl(t, "plan", "show", "pln_BWSYNTH")
			if code == 0 || out != "" || !strings.Contains(errOut, c.want+" while the plan was read; nothing is shown. Read it again with: plan show 'pln_BWSYNTH'") {
				t.Errorf("plan show: exit %d, stdout %q, stderr %q; want a failure naming %q", code, out, errOut, c.want)
			}
		})
	}
	// A move at the plan answer itself is before the snapshot: the plan answer shows ep_BWSYNTH-3,
	// and every read from it on stays there, so the whole view shows, for show and for create.
	t.Run("moved at the plan answer", func(t *testing.T) {
		f := planServer(t)
		f.set(func(f *planFake) {
			seen := false
			f.epochFor = func(path string) string {
				seen = seen || strings.HasPrefix(path, "/plans")
				return map[bool]string{false: "ep_BWSYNTH-2", true: "ep_BWSYNTH-3"}[seen]
			}
			f.post = func(w http.ResponseWriter, _ string) {
				w.WriteHeader(http.StatusCreated)
				f.mu.Unlock()
				fmt.Fprint(w, f.plan())
				f.mu.Lock()
			}
		})
		code, out, errOut := runCtl(t, "plan", "show", "pln_BWSYNTH")
		if code != 0 || !strings.Contains(out, "Plan pln_BWSYNTH  proposed") || !strings.Contains(out, "Evidence") {
			t.Errorf("plan show: exit %d, stdout %q, stderr %q; want the whole view", code, out, errOut)
		}
		f.set(func(f *planFake) {
			seen := false
			f.epochFor = func(path string) string {
				seen = seen || strings.HasPrefix(path, "/plans")
				return map[bool]string{false: "ep_BWSYNTH-3", true: "ep_BWSYNTH-4"}[seen]
			}
		})
		code, out, errOut = runCtl(t, "plan", "create", "--release", "rel_BWSYNTH", "--machine", "mch_a", "--operation", "apply-config")
		if code != 0 || !strings.Contains(out, "Plan pln_BWSYNTH  proposed") || !strings.Contains(out, "Evidence") {
			t.Errorf("plan create: exit %d, stdout %q, stderr %q; want the whole view", code, out, errOut)
		}
	})
	// The plan answer shows ep_BWSYNTH-3, so the client reads GET /server again: that answers 503 in
	// ep_BWSYNTH-4, then ep_BWSYNTH-3 on the retry, and every later read stays in ep_BWSYNTH-3. The
	// change and change back fall inside the plan's own read, after its answer.
	t.Run("moved and back in the refresh after the plan answer", func(t *testing.T) {
		f := planServer(t)
		f.set(func(f *planFake) {
			plan, servers := false, 0
			f.epochFor = func(path string) string {
				plan = plan || path == "/plans/pln_BWSYNTH"
				if !plan {
					return "ep_BWSYNTH-2"
				}
				if path == "/server" {
					if servers++; servers == 1 {
						return "ep_BWSYNTH-4"
					}
				}
				return "ep_BWSYNTH-3"
			}
			f.busy = func(path string) bool { return path == "/server" && servers == 1 && plan }
		})
		code, out, errOut := runCtl(t, "plan", "show", "pln_BWSYNTH")
		if code == 0 || out != "" || !strings.Contains(errOut, "the epoch changed from ep_BWSYNTH-3 and changed back while the plan was read") {
			t.Errorf("plan show: exit %d, stdout %q, stderr %q; want the change and change back named", code, out, errOut)
		}
	})
	t.Run("plan create", func(t *testing.T) {
		f := planServer(t)
		f.set(func(f *planFake) {
			f.post = func(w http.ResponseWriter, _ string) {
				w.WriteHeader(http.StatusCreated)
				f.mu.Unlock()
				fmt.Fprint(w, f.plan())
				f.mu.Lock()
			}
		})
		f.set(moved(false))
		code, out, errOut := runCtl(t, "plan", "create", "--release", "rel_BWSYNTH", "--machine", "mch_a", "--operation", "apply-config")
		want := "Plan pln_BWSYNTH was created for machine mch_a, operation apply-config.\n" +
			"  the epoch changed from ep_BWSYNTH-2 to ep_BWSYNTH-3 while the plan was read, so it is not shown. Read it again with: plan show 'pln_BWSYNTH'\n"
		if code != 0 || !strings.Contains(out, want) || strings.Contains(out, "Evidence") {
			t.Errorf("plan create: exit %d, stdout %q, stderr %q; want %q and no view", code, out, errOut, want)
		}
	})
}

// TestPlanViewEscapes sends hostile text in each field that a valid state prints, and in the
// details of the reads that fail: no control character but the column tab and the line feed
// reaches the terminal, no value starts a line of its own, and a printed command quotes it.
func TestPlanViewEscapes(t *testing.T) {
	const bad = "x\x1b[31m\nForged: line 'q"
	const word = `'x\x1b[31m\x0aForged: line '\''q'`
	js := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	f := planServer(t)
	plan := func(state string, set map[string]any) string {
		f.set(func(f *planFake) { f.state = state })
		var m map[string]any
		if err := json.Unmarshal([]byte(f.plan()), &m); err != nil {
			t.Fatal(err)
		}
		for k, v := range set {
			m[k] = v
		}
		return js(m)
	}
	machine := js(map[string]any{"id": "mch_a", "cluster": "cl_BWSYNTH", "talosEndpoint": bad, "desired": bad, "openDrift": nil})
	assignments := js(map[string]any{"items": []any{map[string]any{"id": "asg_BWSYNTH", "machine": "mch_a", "revision": bad}}})
	approvalIn := func(epoch string) string {
		return js(map[string]any{"id": bad, "plan": "pln_BWSYNTH", "epoch": epoch, "approver": map[string]any{"principal": bad, "role": bad},
			"selfApproval": map[string]any{"marked": false, "reasons": []any{}}, "unseenOverwrite": false})
	}
	approval := approvalIn("ep_BWSYNTH-2")
	// clean fails the test if out carries a control character but the column tab and the line
	// feed, or a line that a value starts.
	clean := func(name, out string) {
		t.Helper()
		if strings.ContainsFunc(out, func(r rune) bool { return unicode.IsControl(r) && r != '\t' && r != '\n' }) {
			t.Errorf("%s: a control character reaches the terminal:\n%q", name, out)
		}
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(strings.TrimLeft(l, " \t"), "Forged") {
				t.Errorf("%s: a value starts a line of its own: %q", name, l)
			}
		}
	}
	for _, c := range []struct {
		name      string
		raw, fail map[string]string
		want      []string
	}{
		{"committed", map[string]string{"/plans/pln_BWSYNTH": plan("committed", map[string]any{"committedOperation": bad})}, nil,
			[]string{"bronzewardctl operation watch " + word}},
		{"expired", map[string]string{"/plans/pln_BWSYNTH": plan("expired", map[string]any{"operation": bad, "release": bad})}, nil,
			[]string{"bronzewardctl plan create --release " + word + " --machine 'mch_a' --operation " + word}},
		{"approved, every read hostile", map[string]string{
			"/plans/pln_BWSYNTH": plan("approved", map[string]any{"approval": bad, "assignmentRevision": bad, "desiredRelease": "rel_BWSYNTH"}),
			"/machines/mch_a":    machine, "/assignments": assignments, "/approvals/" + bad: approval,
		}, map[string]string{"/clusters/cl_BWSYNTH": bad}, []string{"Predicted stale", "unread: "}},
		{"approval unread", map[string]string{"/plans/pln_BWSYNTH": plan("approved", map[string]any{"approval": "apr_BWSYNTH"})},
			map[string]string{"/approvals/apr_BWSYNTH": bad}, []string{"Approved by apr_BWSYNTH, which cannot be read: "}},
		{"assignment unread", map[string]string{"/plans/pln_BWSYNTH": plan("approved", map[string]any{"approval": bad}),
			"/machines/mch_a": machine, "/approvals/" + bad: approval},
			map[string]string{"/assignments": bad}, []string{"Predicted stale", "the assignment cannot be read"}},
		{"approved in an earlier epoch", map[string]string{"/plans/pln_BWSYNTH": plan("approved", map[string]any{"approval": bad, "release": bad}),
			"/approvals/" + bad: approvalIn(bad)}, nil,
			[]string{"This approval is from before the last recovery entry", "bronzewardctl plan create --release " + word}},
	} {
		f.set(func(f *planFake) { f.raw, f.fail = c.raw, c.fail })
		code, out, errOut := runCtl(t, "plan", "show", "pln_BWSYNTH")
		if code != 0 {
			t.Errorf("%s: exit %d, %s", c.name, code, errOut)
		}
		clean(c.name, out)
		for _, w := range append(c.want, `x\x1b[31m\x0aForged: line`) {
			if !strings.Contains(out, w) {
				t.Errorf("%s: no %q in\n%s", c.name, w, out)
			}
		}
	}

	// An epoch change while the view is read: plan show's error and plan create's report name the
	// plan's hostile ID, machine and operation.
	hostile := plan("proposed", map[string]any{"id": bad, "machine": bad, "operation": bad})
	f.set(func(f *planFake) {
		f.raw, f.fail = map[string]string{"/plans/pln_BWSYNTH": hostile}, nil
		seen := false
		f.epochFor = func(path string) string {
			seen = seen || strings.HasPrefix(path, "/machines/")
			return map[bool]string{false: "ep_BWSYNTH-2", true: "ep_BWSYNTH-3"}[seen]
		}
		f.post = func(w http.ResponseWriter, _ string) {
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, hostile)
		}
	})
	code, out, errOut := runCtl(t, "plan", "show", "pln_BWSYNTH")
	clean("plan show", errOut)
	if code == 0 || out != "" || !strings.Contains(errOut, "Read it again with: plan show "+word) {
		t.Errorf("plan show: exit %d, stdout %q, stderr %q; want the quoted ID", code, out, errOut)
	}
	f.set(func(f *planFake) {
		seen := false
		f.epochFor = func(path string) string {
			seen = seen || strings.HasPrefix(path, "/machines/")
			return map[bool]string{false: "ep_BWSYNTH-2", true: "ep_BWSYNTH-3"}[seen]
		}
	})
	code, out, errOut = runCtl(t, "plan", "create", "--release", "rel_BWSYNTH", "--machine", "mch_a", "--operation", "apply-config")
	clean("plan create", out)
	if want := `Plan x\x1b[31m\x0aForged: line 'q was created for machine x\x1b[31m\x0aForged: line 'q, operation x\x1b[31m\x0aForged: line 'q.`; code != 0 ||
		!strings.Contains(out, want) || !strings.Contains(out, "Read it again with: plan show "+word) {
		t.Errorf("plan create: exit %d, stdout %q, stderr %q; want %q and the quoted ID", code, out, errOut, want)
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
		{"an adopt plan with an empty mode", []string{"--operation", "adopt", "--mode", ""}, "an adopt plan takes no --mode"},
		{"another mode", []string{"--operation", "apply-config", "--mode", "reboot"}, "no-reboot, its only mode"},
		{"an empty mode", []string{"--operation", "apply-config", "--mode", ""}, "no-reboot, its only mode"},
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
			"Plan the machine's Desired release instead: bronzewardctl plan create --release 'rel_BWSYNTH-new' --machine 'mch_a' --operation 'apply-config'"},
		{"no Applied", `"detail":"the machine has no Applied configuration to plan an apply-config from; adopt it first","machine":"mch_a"`,
			"Hand the machine over with an adopt plan: bronzewardctl plan create --release 'rel_BWSYNTH' --machine 'mch_a' --operation 'adopt'"},
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

	// A restore after the answer can undo the cancellation: across an epoch change, or one changed
	// and back, the read-back says nothing about what the cancellation stopped.
	for _, c := range []struct{ name, server, want string }{
		{"moved after the answer", "ep_BWSYNTH-3", "the epoch changed from ep_BWSYNTH-2 to ep_BWSYNTH-3"},
		{"moved and back after the answer", "ep_BWSYNTH-2", "the epoch changed from ep_BWSYNTH-2 and changed back"},
	} {
		posted := false
		f.set(func(f *planFake) {
			f.state = "proposed"
			f.epochFor = func(path string) string {
				if posted && (path != "/server" || c.server == "ep_BWSYNTH-3") {
					return "ep_BWSYNTH-3"
				}
				return "ep_BWSYNTH-2"
			}
			post := f.post
			f.post = func(w http.ResponseWriter, path string) { post(w, path); posted = true; f.post = post }
		})
		code, out, errOut := runCtl(t, append(cancel, "--reason", "BWSYNTH")...)
		if code != 0 || strings.Contains(out, "cannot be committed") || !strings.HasSuffix(out, c.want+
			" after the cancellation was recorded, so whether it still stands is not known. Read the plan with: plan show 'pln_BWSYNTH'\n"+cancelLimits+"\n") {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", c.name, code, out, errOut)
		}
		f.set(func(f *planFake) { f.epochFor = nil })
	}
}

// TestPlanCreateSaveFails checks that a created plan is shown even when keeping what the answers
// taught the context fails afterwards: the plan stands, so its ID must not be lost. The
// configuration file is made unreadable as the plan is created.
func TestPlanCreateSaveFails(t *testing.T) {
	f := planServer(t)
	f.set(func(f *planFake) {
		f.post = func(w http.ResponseWriter, _ string) {
			if err := os.WriteFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "bronzeward", "config.yaml"), []byte("{"), 0o600); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusCreated)
			f.mu.Unlock()
			fmt.Fprint(w, f.plan())
			f.mu.Lock()
		}
	})
	code, out, errOut := runCtl(t, "plan", "create", "--release", "rel_BWSYNTH", "--machine", "mch_a", "--operation", "apply-config")
	if code != 1 || !strings.Contains(out, "Plan pln_BWSYNTH  proposed") || !strings.Contains(errOut, "keeping the epoch") {
		t.Errorf("exit %d, stdout %q, stderr %q; want the plan shown and the save failure reported", code, out, errOut)
	}
}
