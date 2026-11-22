package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// applyFake serves a release of two machines: alpha (a control plane with a plan a publisher prepared)
// and beta (a worker with none). Every POST is kept as "path body" in order.
type applyFake struct {
	mu      sync.Mutex
	posts   []string
	refuse  string // a plan whose approval answers 409
	applied bool   // beta is already applied
}

func (f *applyFake) took() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.posts...)
}

func (f *applyFake) plan(id, machine string) string {
	body := fmt.Sprintf(planJSON, "proposed", "null", "null", "apply-config", `"no-reboot"`, "4", "null", "60", "600", "3",
		time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339), diffEvidence)
	return strings.NewReplacer("pln_BWSYNTH", id, "mch_a", machine).Replace(body)
}

func (f *applyFake) position(machine string) string {
	role, name, step, plan, applied := "worker", "beta", "needs-plan", "null", "null"
	if machine == "mch_alpha" {
		role, name, step = "controlplane", "alpha", "awaiting-approval"
		plan = `{"id":"pln_prepared","state":"proposed","operation":"apply-config","release":"rel_BWSYNTH","approvalCurrent":null,"createdAt":"2026-11-22T08:00:00Z","expiresAt":"2026-11-23T08:00:00Z"}`
	}
	pending := "true"
	if machine == "mch_beta" && f.applied {
		step, applied, pending = "no-step", `{"release":"rel_BWSYNTH","source":"operation"}`, "false"
	}
	return fmt.Sprintf(`{"machine":{"id":%q,"name":%q,"role":%q,"cluster":"cl_BWSYNTH","desired":"rel_BWSYNTH","applied":%s,"pendingConvergence":%s,"formOnly":false,"frozen":false,"openDrift":null},`+
		`"ingestion":null,"draft":null,"operation":null,"rolloutHeldBy":null,"step":%q,"plan":%s}`, machine, name, role, applied, pending, step, plan)
}

func (f *applyFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-2")
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.posts = append(f.posts, path+" "+string(b))
		f.mu.Unlock()
		switch {
		case path == "/plans":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, f.plan("pln_made", "mch_beta"))
		case strings.HasSuffix(path, "/approvals") && strings.Contains(path, f.refuse) && f.refuse != "":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"type":"urn:bronzeward:problem:conflict","title":"Conflict","status":409,"detail":"BWSYNTH the plan changed"}`)
		case strings.HasSuffix(path, "/approvals"):
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, approvalJSON, false)
		default:
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, f.plan("pln_made", "mch_beta"))
		}
		return
	}
	switch path {
	case "/server":
		fmt.Fprint(w, `{"version":"BWSYNTH","apiRevision":14,"startMode":"normal"}`)
	case "/identity":
		fmt.Fprint(w, `{"kind":"service","principal":"svc_BWSYNTH"}`)
	case "/recovery":
		fmt.Fprint(w, `{"epoch":"ep_BWSYNTH-2","recoveryMode":false,"scopes":[]}`)
	case "/releases/rel_BWSYNTH":
		fmt.Fprint(w, `{"id":"rel_BWSYNTH","cluster":"cl_BWSYNTH","draft":"drf_BWSYNTH","draftRevision":1,"operation":"op_BWSYNTH","publishedAt":"2026-11-22T08:00:00Z",`+
			`"publishedBy":{"principal":"svc_BWSYNTH","role":"publisher"},"renderer":{"contract":"v1.14","machineryVersion":"v1.14.5","machineryChecksum":"x","kubernetesVersion":"v1.34.1"},`+
			`"sources":[{"kind":"assignment","head":"asg_a","revision":"asr_a","headRevision":1},{"kind":"assignment","head":"asg_b","revision":"asr_b","headRevision":1}],`+
			`"machines":[{"machine":"mch_alpha","mode":"no-reboot","review":"rev_a","publicationObservation":"obs_a"},{"machine":"mch_beta","mode":"no-reboot","review":"rev_b","publicationObservation":"obs_b"}]}`)
	case "/assignment-revisions/asr_a":
		fmt.Fprint(w, `{"id":"asr_a","cluster":"cl_BWSYNTH","machine":"mch_alpha","profiles":["cp-base"],"fragments":{},"author":"svc_BWSYNTH","createdAt":"2026-11-22T08:00:00Z"}`)
	case "/assignment-revisions/asr_b":
		fmt.Fprint(w, `{"id":"asr_b","cluster":"cl_BWSYNTH","machine":"mch_beta","profiles":["worker-base","storage"],"fragments":{},"author":"svc_BWSYNTH","createdAt":"2026-11-22T08:00:00Z"}`)
	case "/machines/mch_alpha/position":
		fmt.Fprint(w, f.position("mch_alpha"))
	case "/machines/mch_beta/position":
		fmt.Fprint(w, f.position("mch_beta"))
	case "/plans/pln_prepared":
		fmt.Fprint(w, f.plan("pln_prepared", "mch_alpha"))
	case "/plans/pln_made":
		fmt.Fprint(w, f.plan("pln_made", "mch_beta"))
	default:
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"type":"urn:bronzeward:problem:not-found","title":"Not Found","status":404,"detail":"BWSYNTH"}`)
	}
}

func applyServer(t *testing.T) *applyFake {
	f := &applyFake{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	automationContext(t, srv.URL)
	return f
}

// TestApply checks #1096's command: the selection, the list with role and profiles, the one question, an
// approval for each plan listed on y and none otherwise, a refusal for one machine that leaves the other
// approved, and the cancel of the plans the command made, never of one somebody else prepared.
func TestApply(t *testing.T) {
	f := applyServer(t)
	apply := []string{"apply", "rel_BWSYNTH"}
	for _, c := range []struct{ name, flags, want string }{
		{"neither --machine nor --all", "", "apply needs --machine <a,b> (names or IDs) or --all, one of them; nothing was sent"},
		{"both", "--all --machine alpha", "apply needs --machine <a,b> (names or IDs) or --all, one of them; nothing was sent"},
		{"a machine that is not the release's", "--machine gamma", "not a machine of release 'rel_BWSYNTH': 'gamma'; nothing was sent"},
		{"structured output", "--all -o json", "apply prints a table and asks a question"},
		{"--no-input", "--all --no-input", "apply asks for your y, which --no-input forbids; nothing was sent"},
	} {
		setStdin(t, "y\n")
		code, out, errOut := runCtl(t, append(append([]string{}, apply...), strings.Fields(c.flags)...)...)
		if code == 0 || out != "" || !strings.Contains(errOut, c.want) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want %q", c.name, code, out, errOut, c.want)
		}
		if b := f.took(); len(b) != 0 {
			t.Fatalf("%s: sent %v", c.name, b)
		}
	}

	// y on both machines: beta's plan is made, alpha's prepared plan is reused, each is approved once.
	setStdin(t, "y\n")
	code, out, errOut := runCtl(t, "apply", "rel_BWSYNTH", "--all", "--diff")
	for _, w := range []string{
		"alpha  controlplane  cp-base  plan pln_prepared (prepared by somebody else)",
		"beta  worker  worker-base,storage  plan pln_made (made by this command)",
		"    @@ -1 +1 @@\n    -team: a\n    +team: b\n",
		"A control plane machine is chosen. Applying to a control plane machine was not covered by this version's acceptance tests.",
		"one machine of a cluster at a time, in the order the plans were approved",
		"expires 24 hours after it was made",
		"Answering y records your approval of the 2 plans listed as you. Approve them? [y/N]: ",
	} {
		if !strings.Contains(errOut, w) {
			t.Errorf("stderr has no %q:\n%s", w, errOut)
		}
	}
	if code != 0 || strings.Count(out, "approved (apr_BWSYNTH)") != 2 || !strings.Contains(out, "alpha: approved") || !strings.Contains(out, "beta: approved") {
		t.Errorf("y: exit %d, stdout %q", code, out)
	}
	got := f.took()
	want := []string{`/plans {"machine":"mch_beta","mode":"no-reboot","operation":"apply-config","releaseId":"rel_BWSYNTH"}`,
		"/plans/pln_prepared/approvals {}", "/plans/pln_made/approvals {}"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("sent %q; want %q", got, want)
	}
}

// TestApplyStops checks that an answer other than y approves nothing and cancels only the plan the command
// made, that a machine chosen by name is the only one planned, and that one refused approval leaves the other.
func TestApplyStops(t *testing.T) {
	f := applyServer(t)
	for _, c := range []struct{ name, stdin string }{{"n", "n\n"}, {"Enter", "\n"}, {"end of input", ""}} {
		f.mu.Lock()
		f.posts = nil
		f.mu.Unlock()
		setStdin(t, c.stdin)
		code, out, errOut := runCtl(t, "apply", "rel_BWSYNTH", "--machine", "alpha,mch_beta")
		got := f.took()
		if code != 2 || out != "" || !strings.Contains(errOut, "nothing was sent") ||
			!strings.Contains(errOut, "Cancelled the plans this command made: pln_made. Plans prepared by somebody else were left as they were.") {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", c.name, code, out, errOut)
		}
		if len(got) != 2 || !strings.HasPrefix(got[0], "/plans {") || !strings.HasPrefix(got[1], "/plans/pln_made/cancellations {") {
			t.Errorf("%s: sent %q; want the plan made, then only its cancellation", c.name, got)
		}
	}

	// A plan somebody prepared is the only one listed, so nothing is made and nothing is cancelled on n.
	f.mu.Lock()
	f.posts = nil
	f.mu.Unlock()
	setStdin(t, "n\n")
	if code, _, errOut := runCtl(t, "apply", "rel_BWSYNTH", "--machine", "alpha"); code != 2 || strings.Contains(errOut, "Cancelled") {
		t.Errorf("prepared plan, n: exit %d, stderr %q", code, errOut)
	}
	if b := f.took(); len(b) != 0 {
		t.Errorf("prepared plan, n: sent %q", b)
	}

	// One refused approval is that machine's line, and the other machine is approved.
	f.mu.Lock()
	f.refuse = "pln_prepared"
	f.mu.Unlock()
	setStdin(t, "y\n")
	code, out, errOut := runCtl(t, "apply", "rel_BWSYNTH", "--all")
	if code == 0 || !strings.Contains(out, "beta: approved") || strings.Contains(out, "alpha: approved") ||
		!strings.Contains(errOut, "alpha: ") || !strings.Contains(errOut, "nothing was approved for this machine") {
		t.Errorf("one refusal: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

// TestApplyLeavesOutWhatIsDone checks that a machine already applied is listed with its reason and gets no plan.
func TestApplyLeavesOutWhatIsDone(t *testing.T) {
	f := applyServer(t)
	f.applied = true
	setStdin(t, "y\n")
	code, out, errOut := runCtl(t, "apply", "rel_BWSYNTH", "--all")
	if code != 0 || !strings.Contains(errOut, "beta  worker  worker-base,storage  applied: this release is what the machine runs") ||
		!strings.Contains(out, "beta: applied: this release is what the machine runs") || !strings.Contains(errOut, "Approve them? [y/N]") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if b := f.took(); len(b) != 1 || b[0] != "/plans/pln_prepared/approvals {}" {
		t.Errorf("sent %q; want only alpha's approval", b)
	}
}
