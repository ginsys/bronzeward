package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// applyFake serves a release of two machines: alpha (a control plane with a plan a publisher prepared)
// and beta (a worker with none). Every POST is kept as "path body" in order.
type applyFake struct {
	mu      sync.Mutex
	posts   []string
	refuse  string // a plan whose approval answers 409
	applied bool   // beta is already applied
	// Faults, each off by default: alpha's waiting plan is an adopt plan; the made plan cannot be read;
	// the approval of the plan named answers 500 (an unknown outcome); making a plan answers 500; the
	// the epoch moves after the last plan is read (moves; moved is that state).
	adoptPlan, unreadMade bool
	unknown               string
	createFails           int    // the status making a plan answers with, 0 for none
	moveAt                string // the epoch moves after this path is served
	cancelFails, moved    bool
	cancelRefused         bool // a cancellation answers 409, as for a plan somebody cancelled meanwhile
	unreadPrepared        bool
	withheld              string // the plan whose change the server withholds
}

func (f *applyFake) took() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.posts...)
}

func (f *applyFake) plan(id, machine string) string {
	evidence := diffEvidence
	if id == f.withheld {
		evidence = withheldEvidence
	}
	body := fmt.Sprintf(planJSON, "proposed", "null", "null", "apply-config", `"no-reboot"`, "4", "null", "60", "600", "3",
		map[string]string{"pln_prepared": "2026-11-24T09:15:00Z", "pln_made": "2026-11-24T10:30:00Z"}[id], evidence)
	return strings.NewReplacer("pln_BWSYNTH", id, "mch_a", machine).Replace(body)
}

func (f *applyFake) position(machine string) string {
	role, name, step, plan, applied := "worker", "beta", "needs-plan", "null", "null"
	if machine == "mch_alpha" {
		role, name, step = "controlplane", "alpha", "awaiting-approval"
		op := "apply-config"
		if f.adoptPlan {
			op = "adopt"
		}
		plan = `{"id":"pln_prepared","state":"proposed","operation":"` + op + `","release":"rel_BWSYNTH","approvalCurrent":null,"createdAt":"2026-11-22T08:00:00Z","expiresAt":"2026-11-23T08:00:00Z"}`
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
	f.mu.Lock()
	epoch := "ep_BWSYNTH-2"
	if f.moved {
		epoch = "ep_BWSYNTH-3"
	}
	if f.moveAt != "" && path == f.moveAt {
		f.moved = true // the epoch moves after this path, so every later read sees the new one
	}
	f.mu.Unlock()
	w.Header().Set("Bronzeward-Epoch", epoch)
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.posts = append(f.posts, path+" "+string(b))
		f.mu.Unlock()
		switch {
		case path == "/plans" && f.createFails == http.StatusConflict, strings.HasSuffix(path, "/cancellations") && f.cancelRefused:
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"type":"urn:bronzeward:problem:conflict","title":"Conflict","status":409,"detail":"BWSYNTH the machine changed"}`)
		case path == "/plans" && f.createFails != 0, strings.HasSuffix(path, "/approvals") && f.unknown != "" && strings.Contains(path, f.unknown),
			strings.HasSuffix(path, "/cancellations") && f.cancelFails:
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"type":"urn:bronzeward:problem:internal-error","title":"Internal Server Error","status":500,"detail":"BWSYNTH"}`)
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
		if f.unreadPrepared {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"type":"urn:bronzeward:problem:internal-error","title":"Internal Server Error","status":500,"detail":"BWSYNTH"}`)
			return
		}
		fmt.Fprint(w, f.plan("pln_prepared", "mch_alpha"))
	case "/plans/pln_made":
		if f.unreadMade {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"type":"urn:bronzeward:problem:internal-error","title":"Internal Server Error","status":500,"detail":"BWSYNTH"}`)
			return
		}
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
		"one machine of a cluster at a time, in an order the controller does not promise. A plan that is not applied expires at the time shown for it.",
		"plan pln_prepared (prepared by somebody else), expires 2026-11-24 09:15:00 UTC",
		"plan pln_made (made by this command), expires 2026-11-24 10:30:00 UTC",
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
			!strings.Contains(errOut, "Cancellation recorded for the plans this command made: pln_made. "+cancelLimits+" Plans prepared by somebody else were left as they were.") {
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
	if code, _, errOut := runCtl(t, "apply", "rel_BWSYNTH", "--machine", "alpha"); code != 2 || strings.Contains(errOut, "Cancellation recorded") {
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

// TestApplyFaults checks what #1096's command does not approve or does not claim: another operation's plan
// is not reused, a plan whose change cannot be read is left out (and cancelled when the command made it),
// an approval of unknown outcome is not said to be unapproved, a failed plan fails the command although
// another machine was approved, and an epoch that moved after the list was shown refuses the later approvals.
func TestApplyFaults(t *testing.T) {
	run := func(set func(*applyFake)) (*applyFake, int, string, string) {
		f := applyServer(t)
		set(f)
		setStdin(t, "y\n")
		code, out, errOut := runCtl(t, "apply", "rel_BWSYNTH", "--all")
		return f, code, out, errOut
	}
	sent := func(f *applyFake) string { return strings.Join(f.took(), "\n") }

	f, code, out, errOut := run(func(f *applyFake) { f.adoptPlan = true })
	if code != 0 || !strings.Contains(errOut, "alpha  controlplane  cp-base  stopped: the plan waiting for this machine is not an apply plan; open it with plan show 'pln_prepared'") ||
		!strings.Contains(out, "beta: approved") || strings.Contains(sent(f), "pln_prepared") {
		t.Errorf("adopt plan: exit %d, stdout %q, stderr %q, sent %q", code, out, errOut, f.took())
	}

	f, code, out, errOut = run(func(f *applyFake) { f.unreadMade = true })
	if code != 0 || !strings.Contains(errOut, "the change of its plan could not be read") ||
		!strings.Contains(errOut, "Cancellation recorded for the plans this command made: pln_made. "+cancelLimits) || !strings.Contains(out, "alpha: approved") || strings.Contains(out, "beta: approved") ||
		strings.Contains(sent(f), "pln_made/approvals") || !strings.Contains(sent(f), "pln_made/cancellations") {
		t.Errorf("unread plan: exit %d, stdout %q, stderr %q, sent %q", code, out, errOut, f.took())
	}

	// A plan whose change the server withholds is never approved in a batch, as the apply page keeps Apply off
	// for it: a plan this command made is cancelled, one somebody else prepared is left as it was.
	f, code, out, errOut = run(func(f *applyFake) { f.withheld = "pln_made" })
	if code != 0 || !strings.Contains(errOut, "beta  worker  worker-base,storage  stopped: the change of its plan cannot be shown") ||
		!strings.Contains(errOut, "make a plan with plan create") || strings.Contains(errOut, "approve 'pln_made'") || !strings.Contains(errOut, "Cancellation recorded for the plans this command made: pln_made. "+cancelLimits) ||
		!strings.Contains(out, "alpha: approved") || strings.Contains(sent(f), "pln_made/approvals") || !strings.Contains(sent(f), "pln_made/cancellations") {
		t.Errorf("a withheld change, made plan: exit %d, stdout %q, stderr %q, sent %q", code, out, errOut, f.took())
	}

	f, code, out, errOut = run(func(f *applyFake) { f.withheld = "pln_prepared" })
	if code != 0 || !strings.Contains(errOut, "alpha  controlplane  cp-base  stopped: the change of its plan cannot be shown") || strings.Contains(errOut, "Cancellation recorded") ||
		!strings.Contains(errOut, "approve 'pln_prepared'") || !strings.Contains(out, "beta: approved") || strings.Contains(sent(f), "pln_prepared") {
		t.Errorf("a withheld change, prepared plan: exit %d, stdout %q, stderr %q, sent %q", code, out, errOut, f.took())
	}

	f, code, out, errOut = run(func(f *applyFake) { f.unknown = "pln_prepared" })
	if code != 3 || !strings.Contains(errOut, "outcome unknown") || strings.Contains(errOut, "nothing was approved for this machine") || !strings.Contains(out, "beta: approved") {
		t.Errorf("unknown approval: exit %d, stdout %q, stderr %q", code, out, errOut)
	}

	f, code, out, errOut = run(func(f *applyFake) { f.createFails = http.StatusInternalServerError })
	if code != 3 || !strings.Contains(errOut, "whether its plan was made is unknown") || !strings.Contains(out, "alpha: approved") || strings.Contains(sent(f), "pln_made") {
		t.Errorf("a plan of unknown outcome: exit %d, stdout %q, stderr %q, sent %q", code, out, errOut, f.took())
	}

	f, code, out, errOut = run(func(f *applyFake) { f.createFails = http.StatusConflict })
	if code == 0 || !strings.Contains(errOut, "not planned: ") || !strings.Contains(errOut, "BWSYNTH the machine changed") || !strings.Contains(out, "alpha: approved") || strings.Contains(sent(f), "pln_made") {
		t.Errorf("a plan refused: exit %d, stdout %q, stderr %q, sent %q", code, out, errOut, f.took())
	}

	// An epoch that moves after the plans were read refuses every approval; one that moves while they are read
	// stops the command before the list; both with an unreadable plan, whose cleanup reads also see the new epoch.
	for _, c := range []struct {
		name, at string
		unread   bool
		want     string
	}{
		{"after the reads", "/plans/pln_made", false, "nothing was approved for this machine"},
		{"after the reads, with an unread plan", "/plans/pln_made", true, "nothing was approved for this machine"},
		{"during the reads", "/machines/mch_beta/position", false, "the epoch changed while the plans were read"},
	} {
		f, code, out, errOut = run(func(f *applyFake) { f.moveAt, f.unreadMade = c.at, c.unread })
		if code == 0 || strings.Contains(out, "approved (") || strings.Contains(sent(f), "/approvals") || !strings.Contains(errOut, c.want) {
			t.Errorf("epoch moved %s: exit %d, stdout %q, stderr %q, sent %q", c.name, code, out, errOut, f.took())
		}
	}

	f, code, out, errOut = run(func(f *applyFake) { f.unreadMade, f.cancelRefused = true, true })
	if !strings.Contains(errOut, "the request to cancel plan pln_made was refused (") || !strings.Contains(errOut, "plan show 'pln_made' reads its state") ||
		strings.Contains(errOut, "stays until it expires") || strings.Contains(errOut, "was not cancelled") || strings.Contains(errOut, "Cancellation recorded") || !strings.Contains(out, "alpha: approved") {
		t.Errorf("a cancellation refused: exit %d, stdout %q, stderr %q", code, out, errOut)
	}

	f, code, out, errOut = run(func(f *applyFake) { f.unreadMade, f.cancelFails = true, true })
	if code != 3 || !strings.Contains(errOut, "whether plan pln_made was cancelled is unknown") || strings.Contains(errOut, "was not cancelled") || !strings.Contains(out, "alpha: approved") {
		t.Errorf("a cancellation of unknown outcome: exit %d, stdout %q, stderr %q, sent %q", code, out, errOut, f.took())
	}
}

// TestApplySentences pins every sentence the command prints about a plan, against the state it describes: a
// plan it made (beta's pln_made) or one somebody prepared (alpha's pln_prepared), crossed with each outcome the
// command can meet. A sentence claims only what a request returned or a read showed, so each row names what
// must be said and what must not, and every row asserts that a plan somebody prepared is never cancelled.
func TestApplySentences(t *testing.T) {
	const cancelled = "Cancellation recorded for the plans this command made: pln_made. "
	for _, c := range []struct {
		name   string
		set    func(*applyFake)
		stdin  string
		code   int
		must   []string // each in the output of the run (standard output and error together)
		mustNo []string
		sent   []string // each in the requests sent
		sentNo []string
	}{
		{"made, approved", func(f *applyFake) {}, "y\n", 0,
			[]string{"beta: approved", "plan pln_made (made by this command)"},
			[]string{"Cancellation recorded", "nothing was approved", "outcome unknown"}, []string{"pln_made/approvals"}, []string{"pln_made/cancellations"}},
		{"made, refused", func(f *applyFake) { f.refuse = "pln_made" }, "y\n", 1,
			[]string{"beta: ", "BWSYNTH the plan changed", "nothing was approved for this machine"},
			[]string{"beta: approved", "outcome unknown", "Cancellation recorded"}, []string{"pln_made/approvals"}, []string{"pln_made/cancellations"}},
		{"made, approval unknown", func(f *applyFake) { f.unknown = "pln_made" }, "y\n", 3,
			[]string{"outcome unknown"},
			[]string{"beta: approved", "nothing was approved for this machine", "Cancellation recorded", "were still sent"}, []string{"pln_made/approvals"}, []string{"pln_made/cancellations"}},
		{"made, declined and cancelled", func(f *applyFake) {}, "n\n", 2,
			[]string{cancelled + cancelLimits, "Plans prepared by somebody else were left as they were."},
			[]string{"was not cancelled", "stays until it expires", "approved ("}, []string{"pln_made/cancellations"}, []string{"/approvals"}},
		{"made, cancellation unknown", func(f *applyFake) { f.cancelFails = true }, "n\n", 3,
			[]string{"whether plan pln_made was cancelled is unknown"},
			[]string{"Cancellation recorded", "was not cancelled", "stays until it expires"}, []string{"pln_made/cancellations"}, []string{"/approvals"}},
		{"made, cancellation refused", func(f *applyFake) { f.cancelRefused = true }, "n\n", 2,
			[]string{"the request to cancel plan pln_made was refused (", "plan show 'pln_made' reads its state"},
			[]string{"Cancellation recorded", "was not cancelled", "stays until it expires"}, []string{"pln_made/cancellations"}, []string{"/approvals"}},
		{"made, withheld", func(f *applyFake) { f.withheld = "pln_made" }, "y\n", 0,
			[]string{"beta  worker  worker-base,storage  stopped: the change of its plan cannot be shown", "make a plan with plan create", cancelled + cancelLimits, "alpha: approved"},
			[]string{"approve 'pln_made'", "beta: approved"}, []string{"pln_made/cancellations"}, []string{"pln_made/approvals"}},
		{"made, unread", func(f *applyFake) { f.unreadMade = true }, "y\n", 0,
			[]string{"the change of its plan could not be read", cancelled + cancelLimits, "alpha: approved"},
			[]string{"beta: approved"}, []string{"pln_made/cancellations"}, []string{"pln_made/approvals"}},
		{"prepared, approved", func(f *applyFake) {}, "y\n", 0,
			[]string{"alpha: approved", "plan pln_prepared (prepared by somebody else)"},
			[]string{"nothing was approved", "outcome unknown"}, []string{"pln_prepared/approvals"}, nil},
		{"prepared, refused", func(f *applyFake) { f.refuse = "pln_prepared" }, "y\n", 1,
			[]string{"alpha: ", "BWSYNTH the plan changed", "nothing was approved for this machine", "beta: approved"},
			[]string{"alpha: approved", "outcome unknown"}, []string{"pln_prepared/approvals"}, nil},
		{"prepared, approval unknown", func(f *applyFake) { f.unknown = "pln_prepared" }, "y\n", 3,
			[]string{"outcome unknown", "of the plans after it: 1 approved", "beta: approved"},
			[]string{"alpha: approved", "nothing was approved for this machine", "still sent", "not sent because"}, []string{"pln_prepared/approvals", "pln_made/approvals"}, nil},
		{"prepared, approval unknown, then the epoch moves", func(f *applyFake) { f.unknown, f.moveAt = "pln_prepared", "/plans/pln_prepared/approvals" }, "y\n", 3,
			[]string{"Its outcome is unknown", "of the plans after it: 1 not sent because the epoch moved", "beta: ", "nothing was approved for this machine"},
			[]string{"alpha: approved", "beta: approved", "still sent", "1 approved"}, []string{"pln_prepared/approvals"}, []string{"pln_made/approvals"}},
		{"prepared, declined", func(f *applyFake) {}, "n\n", 2,
			[]string{cancelled + cancelLimits},
			[]string{"alpha: approved", "pln_prepared was cancelled", "plan pln_prepared was not"}, []string{"pln_made/cancellations"}, []string{"/approvals"}},
		{"prepared, withheld", func(f *applyFake) { f.withheld = "pln_prepared" }, "y\n", 0,
			[]string{"alpha  controlplane  cp-base  stopped: the change of its plan cannot be shown", "approve 'pln_prepared'", "beta: approved"},
			[]string{"alpha: approved", "Cancellation recorded", "make a plan with plan create"}, nil, []string{"pln_prepared/approvals"}},
		{"prepared, unread", func(f *applyFake) { f.unreadPrepared = true }, "y\n", 0,
			[]string{"alpha  controlplane  cp-base  stopped: the change of its plan could not be read", "beta: approved"},
			[]string{"alpha: approved", "Cancellation recorded"}, nil, []string{"pln_prepared/approvals"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := applyServer(t)
			c.set(f)
			setStdin(t, c.stdin)
			code, out, errOut := runCtl(t, "apply", "rel_BWSYNTH", "--all")
			all := out + "\n" + errOut
			sent := strings.Join(f.took(), "\n")
			if code != c.code {
				t.Errorf("exit %d, want %d; stdout %q, stderr %q", code, c.code, out, errOut)
			}
			for _, s := range c.must {
				if !strings.Contains(all, s) {
					t.Errorf("missing %q; stdout %q, stderr %q", s, out, errOut)
				}
			}
			for _, s := range c.mustNo {
				if strings.Contains(all, s) {
					t.Errorf("must not say %q; stdout %q, stderr %q", s, out, errOut)
				}
			}
			for _, s := range c.sent {
				if !strings.Contains(sent, s) {
					t.Errorf("did not send %q; sent %q", s, sent)
				}
			}
			for _, s := range append(c.sentNo, "pln_prepared/cancellations") {
				if strings.Contains(sent, s) {
					t.Errorf("must not send %q; sent %q", s, sent)
				}
			}
		})
	}
}

// TestApplyLeavesOutWhatIsDone checks that a machine already applied is listed with its reason and gets no plan.
func TestApplyLeavesOutWhatIsDone(t *testing.T) {
	f := applyServer(t)
	f.applied = true
	setStdin(t, "y\n")
	code, out, errOut := runCtl(t, "apply", "rel_BWSYNTH", "--all")
	if code != 0 || !strings.Contains(errOut, "beta  worker  worker-base,storage  applied: this release is what the machine runs") ||
		!strings.Contains(out, "beta: applied: this release is what the machine runs") || !strings.Contains(errOut, "Approve them? [y/N]") ||
		!strings.Contains(errOut, "  the plan's change is 3 lines; --diff shows it\n") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if b := f.took(); len(b) != 1 || b[0] != "/plans/pln_prepared/approvals {}" {
		t.Errorf("sent %q; want only alpha's approval", b)
	}
}
