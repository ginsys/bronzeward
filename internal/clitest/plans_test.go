package clitest

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// planID is a plan ID in the CLI's output.
var planID = regexp.MustCompile(`\bpln_[a-z2-7]{26}\b`)

// planEnv is TestPlans' server: the seeded machine with its stand-in, rel0 published without an
// assignment and rel published with one, which is the machine's Desired.
type planEnv struct {
	f         *fixture
	in        *injector
	s         seeded
	st        *standIn
	rel0, rel string
	ctl       func(...string) result
	ctlIn     func(stdin string, args ...string) result // ctl with stdin as standard input
}

// create is plan create for the machine and rel with operation.
func (e *planEnv) create(operation string, extra ...string) []string {
	return append([]string{"--context", "human", "plan", "create", "--release", e.rel, "--machine", e.s.machine, "--operation", operation}, extra...)
}

// proposed are the machine's proposed plans, read in the database.
func (e *planEnv) proposed(t *testing.T) []string {
	t.Helper()
	rows, err := e.f.db.Query(`SELECT p.id FROM plan p JOIN plan_state s ON s.plan = p.id WHERE p.machine = $1 AND s.state = 'proposed'
		AND p.expires_at > now() ORDER BY p.id`, e.s.machine)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

// cancelAll cancels every proposed plan of the machine through the CLI.
func (e *planEnv) cancelAll(t *testing.T) {
	t.Helper()
	e.in.set(nil, nil)
	for _, p := range e.proposed(t) {
		e.f.want(e.ctl("--context", "human", "plan", "cancel", p, "--reason", "BWSYNTH: the next case starts with no proposed plan"), 0, "The plan is cancelled")
	}
}

// approve approves plan as h-approver, who did not create it (POST /plans/{id}/approvals, human
// approver); approve is #353's command, so the test sends the route itself.
func (e *planEnv) approve(t *testing.T, plan string) {
	t.Helper()
	q, _ := http.NewRequest(http.MethodPost, e.f.base+"/api/v1/plans/"+plan+"/approvals", strings.NewReader("{}"))
	q.Header.Set("Authorization", "Bearer "+e.f.mint("h-approver", ""))
	q.Header.Set("Content-Type", "application/json")
	q.Header.Set("Idempotency-Key", "k-approve-"+plan)
	e.f.do(q, http.StatusCreated)
}

// until waits up to two minutes for query, given args, to answer want.
func (e *planEnv) until(t *testing.T, want, query string, args ...any) {
	t.Helper()
	var got string
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if err := e.f.db.QueryRow(query, args...).Scan(&got); err == nil && got == want {
			return
		}
	}
	t.Fatalf("%s %v answered %q, not %q, within two minutes", query, args, got, want)
}

// planCases are the client-contracts.md §12 cases of plan creation (#352);
// testdata/plan-cases.yaml holds their records, and TestPlans runs exactly those.
var planCases = map[string]func(*testing.T, *planEnv){
	"AMB-03": func(t *testing.T, e *planEnv) {
		var s sent
		e.in.set(s.posting(func(n int, w http.ResponseWriter, r *http.Request) {
			if n > 0 {
				e.in.proxy.ServeHTTP(w, r)
				return
			}
			e.in.proxy.ServeHTTP(httptest.NewRecorder(), r)
			problem(w, http.StatusInternalServerError, "internal-error", "")
		}), nil)
		before := e.f.actCount()
		r := e.ctl(e.create("adopt")...)
		e.f.want(r, 0, "500", "repeat 1 of 3", "POST /api/v1/plans: already done earlier; showing that result.", "Plan pln_",
			"nothing runs until an approver approves")
		s.one(t, 2)
		if n, p := e.f.actCount(), e.proposed(t); n != before+1 || len(p) != 1 || !strings.Contains(r.out, p[0]) {
			t.Errorf("%d acts, proposed plans %v; want 1 act and the plan shown", n-before, p)
		}
	},
	"AMB-10": func(t *testing.T, e *planEnv) {
		var s sent
		e.in.set(s.posting(func(_ int, w http.ResponseWriter, r *http.Request) { drop(w, r) }), nil)
		before := e.f.actCount()
		r := e.ctl(e.create("adopt")...)
		e.f.want(r, 3, "outcome unknown: an attempt may have committed", "shows no proposed plan for this release and operation")
		s.one(t, 4)
		e.in.set(s.posting(func(_ int, w http.ResponseWriter, r *http.Request) { e.in.proxy.ServeHTTP(w, r) }), nil)
		r = e.ctl(e.create("adopt", append(repeatFlags(t, r.err), "--expires-in", "2h")...)...)
		e.f.want(r, 0, "Plan pln_", "(in 2h0m0s by this client's clock)")
		if strings.Contains(r.err, "already done earlier") {
			t.Error("the changed input's own answer was reported as a replay")
		}
		s.one(t, 5)
		if n, p := e.f.actCount(), e.proposed(t); n != before+1 || len(p) != 1 {
			t.Errorf("%d acts, proposed plans %v; want the changed input's", n-before, p)
		}
	},
	"AMB-11": func(t *testing.T, e *planEnv) {
		var s sent
		e.in.set(s.posting(func(_ int, w http.ResponseWriter, r *http.Request) {
			e.in.commit(w, r)
			e.f.newEpoch()
		}), nil)
		r := e.ctl(e.create("adopt")...)
		e.f.want(r, 3, "set aside")
		flags := repeatFlags(t, r.err)
		before := e.f.actCount()
		e.f.store(e.f.humanFile(), e.f.mint("h-author", ""), "")
		e.in.set(s.posting(func(_ int, w http.ResponseWriter, r *http.Request) { e.in.proxy.ServeHTTP(w, r) }), nil)
		e.f.want(e.ctl(e.create("adopt", flags...)...), 2, "the write is not sent: the credential names "+e.f.iss.URL+"|h-author, not the given --expect-principal "+e.f.iss.URL+"|h-all")
		if seen := e.in.requests(); len(seen) != 0 {
			t.Errorf("under another principal the injector saw %v", seen)
		}
		e.f.store(e.f.humanFile(), e.f.mint("h-all", ""), "")
		e.in.set(s.posting(func(_ int, w http.ResponseWriter, r *http.Request) { e.in.proxy.ServeHTTP(w, r) }), nil)
		e.f.want(e.ctl(e.create("adopt", flags...)...), 2, "POST /api/v1/plans not sent: the epoch is now", "not the given --expect-epoch "+flags[3])
		if seen := e.in.requests(); !slices.Equal(seen, []string{"GET /api/v1/recovery"}) {
			t.Errorf("in another epoch the injector saw %v, want the confirming read only", seen)
		}
		s.one(t, 1)
		if n, p := e.f.actCount(), e.proposed(t); n != before || len(p) != 1 {
			t.Errorf("%d acts by the refused repeats, proposed plans %v; want none and the first send's", n-before, p)
		}
	},
	"AMB-12": func(t *testing.T, e *planEnv) {
		var s sent
		epoch := make(chan string, 1)
		e.in.set(s.posting(func(n int, w http.ResponseWriter, r *http.Request) {
			if n == 0 {
				drop(w, r)
				return
			}
			epoch <- e.f.newEpoch()
			e.in.proxy.ServeHTTP(w, r)
		}), nil)
		before := e.f.actCount()
		r := e.ctl(e.create("adopt")...)
		// The second send moves the epoch before the CLI's answer, so a missing one fails here at
		// once instead of blocking the receive until the test timeout.
		var now string
		select {
		case now = <-epoch:
		default:
			t.Fatalf("the CLI ended without its second send; exit %d, standard error %q", r.code, r.err)
		}
		e.f.want(r, 3, "set aside, not repeated: the epoch is now "+now+", and this send committed nothing",
			"shows no proposed plan for this release and operation")
		s.one(t, 2)
		if n, p := e.f.actCount(), e.proposed(t); r.out != "" || n != before || len(p) != 0 {
			t.Errorf("standard output %q, %d acts, proposed plans %v; want none", r.out, n-before, p)
		}
	},
}

// cases reads the ids a testdata file of case records holds, in its order, and fails unless they
// are exactly run's.
func cases[T any](t *testing.T, file string, run map[string]T) []string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var records []struct {
		ID string `yaml:"id"`
	}
	if err := yaml.Unmarshal(b, &records); err != nil || len(records) != len(run) {
		t.Fatalf("%s holds %d records, %v; the test runs %d", file, len(records), err, len(run))
	}
	var ids []string
	for _, r := range records {
		if _, ok := run[r.ID]; !ok {
			t.Fatalf("%s records %s, which the test does not run", file, r.ID)
		}
		ids = append(ids, r.ID)
	}
	return ids
}

// newPlanEnv starts a server with a provider and a Talos stand-in behind the injector, the oidc
// context "human" signed in as h-all, and publishes the seeded machine twice: rel0 from the seeded
// draft with its import base only, so without an assignment, then rel with one, which is the
// machine's Desired.
func newPlanEnv(t *testing.T) *planEnv {
	f, bao := startProvider(t, "viewer")
	in := newInjector(t, f.base)
	in.writing = true
	f.want(f.ctl(nil, "", "context", "add", "human", "--server", in.URL, "--certificate-authority", in.ca,
		"--issuer", f.iss.URL, "--client-id", "BWSYNTH-cli", "--audience", "bronzeward", "--oidc-token-file", f.humanFile()), 0, "credential oidc")
	f.store(f.humanFile(), f.mint("h-all", ""), "")
	e := &planEnv{f: f, in: in, s: f.seed()}
	e.ctlIn = func(stdin string, args ...string) result {
		r := f.scan(f.exec(nil, stdin, args...), args)
		if e.st == nil {
			return r
		}
		for _, where := range []string{strings.Join(args, " "), r.out, r.err} {
			if l := e.st.leaks(where); len(l) > 0 {
				t.Errorf("bronzewardctl %v: output or argv holds the stand-in's %v", args, l)
			}
		}
		return r
	}
	e.ctl = func(args ...string) result { return e.ctlIn("", args...) }
	e.st = f.newStandIn(t, bao.b, e.s, e.ctl)
	h := []string{"--context", "human"}
	release := regexp.MustCompile(`rel_[a-z2-7]{26}`)

	// rel0: the seeded draft with the machine's import base only, so it carries no assignment.
	f.importMachine(t, e.s.machine, e.s.draft)
	f.want(e.ctl(append(h, "draft", "show", e.s.draft)...), 0)
	e.rel0 = release.FindString(e.ctl(append(h, "publish", e.s.draft)...).out)
	// rel: a draft with an assignment, published after rel0, so it is the machine's Desired.
	labels := filepath.Join(t.TempDir(), "labels.yaml")
	if err := os.WriteFile(labels, []byte("machine:\n  nodeLabels:\n    node.example.test/pool: workers\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	draft := regexp.MustCompile(`drf_[a-z2-7]{26}`).FindString(e.ctl(append(h, "draft", "create", "--cluster", e.s.cluster, "--title", "plans")...).out)
	f.importMachine(t, e.s.machine, draft)
	for _, step := range [][]string{{"draft", "show", draft}, {"fragment", "set", "labels", "--draft", draft, "--layer", "role", "--file", labels},
		{"profile", "set", "workers", "--draft", draft, "--fragments", "labels"}, {"assignment", "set", e.s.machine, "--draft", draft, "--profiles", "workers"}} {
		f.want(e.ctl(append(h, step...)...), 0)
	}
	r := e.ctl(append(h, "publish", draft)...)
	f.want(r, 0, e.s.machine+" has no Applied release: hand it over with an adopt plan:\n    "+
		"bronzewardctl plan create --release '")
	if e.rel = release.FindString(r.out); e.rel0 == "" || e.rel == "" {
		t.Fatalf("the publications named releases %q and %q", e.rel0, e.rel)
	}
	return e
}

// TestPlans runs plan create, show and cancel (#352) through the CLI against a server with a
// provider and a Talos stand-in: T19's refusals and their next steps, the cases of
// testdata/plan-cases.yaml, T20's display of a proposed, cancelled, expired and committed plan,
// and T22's cancellation of a committed plan whose attempt is recorded, which goes on.
func TestPlans(t *testing.T) {
	ids := cases(t, "testdata/plan-cases.yaml", planCases)
	e := newPlanEnv(t)
	f, in := e.f, e.in
	h := []string{"--context", "human"}
	m := "'" + e.s.machine + "'"

	t.Run("T19 refusals", func(t *testing.T) {
		in.set(nil, nil)
		before := f.actCount()
		f.want(e.ctl(append(h, "plan", "create", "--release", e.rel0, "--machine", e.s.machine, "--operation", "adopt")...), 1,
			"409", "the machine's assignment has moved since the release was compiled",
			"Publish a release from the machine's current assignment, then plan that release. The assignment's history: bronzewardctl machine timeline "+m)
		f.want(e.ctl(append(h, "plan", "create", "--release", e.rel0, "--machine", e.s.machine, "--operation", "apply-config")...), 1,
			"an apply-config plan is for the machine's Desired release",
			"Plan the machine's Desired release instead: bronzewardctl plan create --release '"+e.rel+"' --machine "+m+" --operation 'apply-config'")
		f.want(e.ctl(e.create("apply-config")...), 1, "has no Applied configuration",
			"Hand the machine over with an adopt plan: bronzewardctl plan create --release '"+e.rel+"' --machine "+m+" --operation 'adopt'")
		in.set(nil, nil)
		f.want(e.ctl(e.create("adopt", "--mode", "no-reboot")...), 2, "an adopt plan takes no --mode", "nothing was sent")
		f.want(e.ctl(e.create("adopt", "--expires-in", "1500ms")...), 2, "whole seconds")
		for _, q := range in.requests() {
			if !strings.HasPrefix(q, "GET ") {
				t.Errorf("a refusal before sending sent %s", q)
			}
		}
		if n, p := f.actCount(), e.proposed(t); n != before || len(p) != 0 {
			t.Errorf("%d acts, proposed plans %v; want none", n-before, p)
		}
	})

	for _, id := range ids {
		e.cancelAll(t)
		t.Run(id, func(t *testing.T) { planCases[id](t, e) })
	}
	e.cancelAll(t)
	// The cases' epochs, committed in the database, superseded serve's controller, which adopts and
	// dispatches nothing until it is restarted.
	f.restart()

	t.Run("T20 display and T22 cancellation", func(t *testing.T) {
		in.set(nil, nil)
		r := e.ctl(e.create("adopt")...)
		p := planID.FindString(r.out)
		f.want(r, 0, "Plan "+p, "proposed", "adopt: records Applied from the node's own bytes and sends nothing to the machine",
			"machine Desired: "+e.rel+" at plan creation, "+e.rel+" now", "Baseline", "import base ibr_", "Pending convergence",
			"It is not approved and nothing runs until an approver approves: bronzewardctl approve '"+p+"'")
		f.want(e.ctl(append(h, "plan", "cancel", p)...), 2, "--reason: give a reason of 1 to 1024 bytes; nothing was sent")
		f.want(e.ctl(append(h, "plan", "cancel", p, "--reason", "BWSYNTH superseded")...), 0,
			"Cancellation of plan "+p+" recorded by ", "(publisher), act act_", "The plan is cancelled: it cannot be committed.")
		f.want(e.ctl(append(h, "plan", "show", p)...), 0, "Cancelled: it cannot be committed. Create a new plan: bronzewardctl plan create --release '"+
			e.rel+"' --machine "+m+" --operation 'adopt'")

		p = planID.FindString(e.ctl(e.create("adopt", "--expires-in", "1s")...).out)
		time.Sleep(1500 * time.Millisecond)
		f.want(e.ctl(append(h, "plan", "show", p)...), 0, "Expired: it can no longer be approved or committed. Create a new plan:", "ago by this client's clock)")
	})

	// The handover: an approved adopt plan is committed and adopted by the server's controller.
	var adopted string
	t.Run("committed adopt plan", func(t *testing.T) {
		in.set(nil, nil)
		adopted = planID.FindString(e.ctl(e.create("adopt")...).out)
		e.approve(t, adopted)
		e.until(t, "committed", `SELECT state FROM plan_state WHERE plan = $1`, adopted)
		r := e.ctl(append(h, "plan", "show", adopted)...)
		op := opID.FindString(r.out)
		f.want(r, 0, "Committed: operation "+op+". Follow it with: bronzewardctl operation watch '"+op+"'")
		if state := f.ended(op); state != "completed" {
			t.Fatalf("the adoption ended %s: %s", state, f.events(op))
		}
	})
	if adopted == "" || t.Failed() {
		t.FailNow()
	}

	// T22: a cancellation of a committed plan whose attempt is recorded does not stop it.
	t.Run("cancellation after commitment", func(t *testing.T) {
		in.set(nil, nil)
		arrived, release := e.st.node.HoldApply(t)
		defer release()
		r := e.ctl(e.create("apply-config", "--transport-deadline", "1m", "--verification-deadline", "1m", "--max-attempts", "1")...)
		p := planID.FindString(r.out)
		f.want(r, 0, "apply-config, mode no-reboot", "transport 60s, verification 60s")
		if !strings.Contains(r.out, "Changes shown below") && !strings.Contains(r.out, "Diff withheld") && !strings.Contains(r.out, "No differences") {
			t.Errorf("the display names no diff:\n%s", r.out)
		}
		e.approve(t, p)
		select {
		case <-arrived:
		case <-time.After(2 * time.Minute):
			t.Fatal("the apply did not reach the stand-in within two minutes")
		}
		var op string
		var attempts int
		if err := f.db.QueryRow(`SELECT s.operation, (SELECT count(*) FROM attempt a WHERE a.plan = s.plan) FROM plan_state s WHERE s.plan = $1`,
			p).Scan(&op, &attempts); err != nil || attempts != 1 {
			t.Fatalf("plan %s: operation %q, %d attempts, %v; want one recorded attempt", p, op, attempts, err)
		}
		r = e.ctl(append(h, "plan", "cancel", p, "--reason", "BWSYNTH cancelled after commitment")...)
		release()
		f.want(r, 0, "Cancellation of plan "+p+" recorded by ", "The plan stays committed to operation "+op+
			". A cancellation ends a committed plan's operation only while no attempt is recorded; it does not stop an attempt already recorded.\n"+
			"Follow it with: bronzewardctl operation watch '"+op+"'\n")
		// The released stand-in applies the configuration, so the attempt verifies: only
		// "completed" shows that the cancellation did not stop it, and not a failure for another cause.
		if state := f.ended(op); state != "completed" {
			t.Errorf("the operation with a recorded attempt ended %s, not completed: %s", state, f.events(op))
		}
		if a := e.st.node.AppliedRequests(); len(a) != 1 {
			t.Errorf("the stand-in received %d applies; want the recorded attempt's one", len(a))
		}
		f.want(e.ctl(append(h, "plan", "show", p)...), 0, "Committed: operation "+op)
	})
	f.scanFiles()
}
