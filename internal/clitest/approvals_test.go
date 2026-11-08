package clitest

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// aprID is an approval ID in the CLI's output.
var aprID = regexp.MustCompile(`\bapr_[a-z2-7]{26}\b`)

// approveTyped is approve of plan on context ctx, the plan ID typed as standard input's one line.
func (e *planEnv) approveTyped(ctx, plan string) result {
	return e.ctlIn(plan+"\n", "--context", ctx, "approve", plan)
}

// planState is plan's state and approval, read in the database.
func (e *planEnv) planState(t *testing.T, plan string) (state, approval string) {
	t.Helper()
	var a *string
	if err := e.f.db.QueryRow(`SELECT state, approval FROM plan_state WHERE plan = $1`, plan).Scan(&state, &a); err != nil {
		t.Fatal(err)
	}
	if a != nil {
		approval = *a
	}
	return state, approval
}

// supersede commits a new epoch, as a recovery entry does, so that serve's controller commits no
// plan an approval of the case authorizes: the machine keeps no Applied, and the next case can
// create an adopt plan again.
func (e *planEnv) supersede() { e.f.newEpoch() }

// approvalCases are the client-contracts.md §12 cases of approval (#353);
// testdata/approval-cases.yaml holds their records, and TestApprovals runs exactly those. AUTH-04
// runs first, while the automation token's epoch is current.
var approvalCases = map[string]func(*testing.T, *planEnv){
	"AUTH-04": func(t *testing.T, e *planEnv) {
		e.in.set(nil, nil)
		p := planID.FindString(e.ctl(e.create("adopt")...).out)
		before := e.f.actCount()
		r := e.approveTyped("auto", p)
		e.f.want(r, 1, "Type the plan ID to send it", "POST /api/v1/plans/"+p+"/approvals is for a person signed in, not an automation token: the server refuses it.",
			"answered 403", "(forbidden): this route is for humans only", "This needs a person signed in, not an automation token.")
		if warned, sent := strings.Index(r.err, "is for a person signed in"), strings.Index(r.err, "answered 403"); warned > sent || r.out != "" {
			t.Errorf("the warning is not before the 403, or standard output is %q:\n%s", r.out, r.err)
		}
		posts := 0
		for _, q := range e.in.requests() {
			if q == "POST /api/v1/plans/"+p+"/approvals" {
				posts++
			}
		}
		if state, _ := e.planState(t, p); posts != 1 || state != "proposed" || e.f.actCount() != before {
			t.Errorf("%d approvals sent, plan %s, %d acts; want one sent, refused, and the plan proposed", posts, state, e.f.actCount()-before)
		}
	},
	"AMB-05": func(t *testing.T, e *planEnv) {
		e.supersede()
		e.in.set(nil, nil)
		p := planID.FindString(e.ctl(e.create("adopt")...).out)
		var s sent
		var epoch atomic.Value // written by the injector's goroutine
		e.in.set(s.posting(func(_ int, w http.ResponseWriter, r *http.Request) {
			e.in.commit(w, r)
			epoch.Store(e.f.newEpoch())
		}), nil)
		before := e.f.actCount()
		r := e.approveTyped("human", p)
		e.f.want(r, 3, "set aside, not repeated: the epoch is now "+fmt.Sprint(epoch.Load())+", not the reference epoch",
			"GET /api/v1/plans/"+p+" shows state approved, approval apr_", "read it again with: bronzewardctl --context 'human' plan show '"+p+"'", "--idempotency-key ")
		s.one(t, 1)
		if n := e.f.actCount(); r.out != "" || n != before+1 {
			t.Errorf("standard output %q, %d acts recorded; want none and 1", r.out, n-before)
		}
	},
	"AMB-07": func(t *testing.T, e *planEnv) {
		e.supersede()
		e.in.set(nil, nil)
		p := planID.FindString(e.ctl(e.create("adopt")...).out)
		var s sent
		e.in.set(s.posting(func(n int, w http.ResponseWriter, r *http.Request) {
			if n == 0 {
				e.in.commit(w, r)
				return
			}
			e.f.newEpoch()
			e.in.proxy.ServeHTTP(w, r)
		}), nil)
		before := e.f.actCount()
		r := e.approveTyped("human", p)
		e.f.want(r, 3, "repeat 1 of 3", "set aside, not repeated: the key's record is from an earlier epoch", "GET /api/v1/plans/"+p+" shows state approved, approval apr_")
		s.one(t, 2)
		if n := e.f.actCount(); r.out != "" || n != before+1 {
			t.Errorf("standard output %q, %d acts recorded; want none and 1", r.out, n-before)
		}
	},
	"REC-03": func(t *testing.T, e *planEnv) {
		f := e.f
		e.in.set(nil, nil)
		var last string
		if err := f.db.QueryRow(`SELECT id FROM plan ORDER BY created_at DESC LIMIT 1`).Scan(&last); err != nil {
			t.Fatal(err)
		}
		f.restart("-recovery-start")
		at := func(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339) }
		q, _ := http.NewRequest(http.MethodPost, f.base+"/api/v1/recovery/entries", strings.NewReader(`{"restored":[{"family":"database","backup":"BWSYNTH-pg",`+
			`"takenAt":"`+at(time.Hour)+`"}],"stoppedAt":"`+at(time.Minute)+`","reason":"BWSYNTH restore drill"}`))
		q.Header.Set("Authorization", "Bearer "+f.mint("h-recovery", ""))
		q.Header.Set("Content-Type", "application/json")
		q.Header.Set("Idempotency-Key", "k-rec03-recovery-entry")
		f.do(q, http.StatusCreated)
		before := f.actCount()
		config := filepath.Join(f.home, "bronzeward", "config.yaml")
		process := "Recovery mode: the server was started for recovery: it refuses plan creation and approval"
		f.want(e.ctl(e.create("apply-config")...), 1, "409", "(recovery-mode-active)", process)
		f.want(e.approveTyped("human", last), 1, "409", "(recovery-mode-active)", process)
		if cfg, _ := os.ReadFile(config); !strings.Contains(string(cfg), "startMode: recovery") || f.actCount() != before {
			t.Errorf("after the refusals on a recovery start, %d acts and the contexts file:\n%s", f.actCount()-before, cfg)
		}
		// The entry marked the machine's scope; a normal start refuses plan creation for that scope.
		f.restart()
		r := e.ctl(e.create("apply-config")...)
		f.want(r, 1, "409", "Recovery mode: the scope of machine "+e.s.machine+" is pre-restore-unaccounted.")
		if cfg, _ := os.ReadFile(config); strings.Contains(r.err, "started for recovery") || strings.Contains(string(cfg), "startMode: recovery") {
			t.Errorf("a scope refusal was read as a recovery start:\n%s\n%s", r.err, cfg)
		}
	},
}

// TestApprovals runs approve, approval show and approval revoke (#353) through the CLI against a
// server with a provider and a Talos stand-in: the cases of testdata/approval-cases.yaml, T21's
// confirmation, the prediction beside the recorded reasons, T22's revocation before and after
// commitment, and journey task T8, whose output testdata/journeys/t8.txt keeps. REC-03 runs last:
// its recovery entry leaves the machine's scope unaccounted.
func TestApprovals(t *testing.T) {
	ids := cases(t, "testdata/approval-cases.yaml", approvalCases)
	e := newPlanEnv(t)
	f, in := e.f, e.in
	h := []string{"--context", "human"}
	f.want(f.ctl(nil, "", "context", "add", "auto", "--server", in.URL, "--certificate-authority", in.ca, "--automation-token-file", f.tokenFile), 0)
	for _, who := range []string{"publisher", "approver"} {
		file := filepath.Join(f.home, who+".json")
		f.store(file, f.mint("h-"+who, ""), "")
		f.want(f.ctl(nil, "", "context", "add", who, "--server", in.URL, "--certificate-authority", in.ca, "--issuer", f.iss.URL,
			"--client-id", "BWSYNTH-cli", "--audience", "bronzeward", "--oidc-token-file", file), 0)
	}

	t.Run("T21 confirmation", func(t *testing.T) {
		p := planID.FindString(e.ctl(e.create("adopt")...).out)
		in.set(nil, nil) // clears the request log, so it holds only the unconfirmed approvals' requests
		for _, c := range []struct{ stdin, want string }{
			{"pln_aaaaaaaaaaaaaaaaaaaaaaaaaa\n", "the typed text is not the plan ID; nothing was sent"},
			{"\n", "stopped: nothing was typed; nothing was sent"},
			{"", "stopped: nothing was typed; nothing was sent"},
		} {
			f.want(e.ctlIn(c.stdin, append(h, "approve", p)...), 2, "Plan "+p, "Self-approval  predicted: ", "Type the plan ID to send it", c.want)
		}
		f.want(e.ctlIn(p+"\n", append(h, "approve", p, "--no-input")...), 2, "which --no-input forbids; nothing was sent")
		for _, q := range in.requests() {
			if !strings.HasPrefix(q, "GET ") {
				t.Errorf("an unconfirmed approval sent %s", q)
			}
		}
		if state, _ := e.planState(t, p); state != "proposed" {
			t.Errorf("the plan is %s", state)
		}
	})

	for _, id := range ids {
		if id == "REC-03" {
			continue // last, below
		}
		e.cancelAll(t)
		t.Run(id, func(t *testing.T) { approvalCases[id](t, e) })
	}

	// The controller is superseded since AMB-05, so this approval stays uncommitted.
	t.Run("T22 revocation before commitment", func(t *testing.T) {
		e.cancelAll(t)
		p := planID.FindString(e.ctl(e.create("adopt")...).out)
		r := e.approveTyped("human", p)
		reasons := "created-plan, published, authored-change"
		f.want(r, 0, "Self-approval  predicted: you created this plan and published this release, "+
			"and authored a fragment, profile or assignment revision its draft introduced.", "Import-base authorship and automation you own are not predicted.",
			"Recorded self-approval reasons: "+reasons+".",
			"The controller records the adoption without sending anything; Applied becomes "+e.rel+" (adoption) once it does.")
		a := aprID.FindString(r.out)
		f.want(e.ctl(append(h, "approval", "show", a)...), 0, "Approval "+a+" of plan "+p, "recorded reasons: "+reasons, "Overwrites     nothing unseen")
		f.want(e.ctl(append(h, "approval", "revoke", a)...), 2, "--reason: give a reason of 1 to 1024 bytes; nothing was sent")
		f.want(e.ctl(append(h, "approval", "revoke", a, "--reason", "BWSYNTH withdrawn")...), 0, "Revocation of approval "+a+" recorded by ",
			"The plan "+p+" is revoked: it cannot be committed. A revocation ends an uncommitted approval and undoes no commitment; it does not stop an attempt already recorded.")
		f.want(e.ctl(append(h, "approval", "revoke", a, "--reason", "BWSYNTH again")...), 1, "409", "the approval is already revoked; a revocation is permanent")
		f.want(e.ctl(append(h, "plan", "show", p)...), 0, "Revoked: its approval or its approver's identity was revoked, so it cannot be committed.")
	})
	e.cancelAll(t)
	// The cases' epochs superseded serve's controller; the restarted one commits only approvals of
	// the current epoch.
	f.restart()

	var op string
	t.Run("T21 adopt plan", func(t *testing.T) {
		in.set(nil, nil)
		p := planID.FindString(e.ctl(e.create("adopt")...).out)
		f.want(e.approveTyped("human", p), 0, "Applied becomes "+e.rel+" (adoption) once it does.")
		e.until(t, "committed", `SELECT state FROM plan_state WHERE plan = $1`, p)
		if err := f.db.QueryRow(`SELECT operation FROM plan_state WHERE plan = $1`, p).Scan(&op); err != nil || f.ended(op) != "completed" {
			t.Fatalf("the adoption %s ended %s, %v: %s", op, f.ended(op), err, f.events(op))
		}
	})
	if op == "" || t.Failed() {
		t.FailNow()
	}

	t.Run("T22 revocation after commitment", func(t *testing.T) {
		in.set(nil, nil)
		arrived, release := e.st.node.HoldApply(t)
		defer release()
		p := planID.FindString(e.ctl(e.create("apply-config", "--transport-deadline", "1m", "--verification-deadline", "1m", "--max-attempts", "1")...).out)
		r := e.approveTyped("human", p)
		f.want(r, 0, "The controller re-checks the plan at use time; it applies it only if every check passes.")
		a := aprID.FindString(r.out)
		select {
		case <-arrived:
		case <-time.After(2 * time.Minute):
			t.Fatal("the apply did not reach the stand-in within two minutes")
		}
		var op string
		if err := f.db.QueryRow(`SELECT operation FROM plan_state WHERE plan = $1`, p).Scan(&op); err != nil {
			t.Fatal(err)
		}
		r = e.ctl(append(h, "approval", "revoke", a, "--reason", "BWSYNTH revoked after commitment")...)
		release()
		f.want(r, 0, "The plan "+p+" stays committed to operation "+op+". A revocation ends an uncommitted approval and undoes no commitment; "+
			"it does not stop an attempt already recorded.\nFollow it with: bronzewardctl operation watch '"+op+"'\n")
		if state := f.ended(op); state != "completed" {
			t.Errorf("the operation with a recorded attempt ended %s: %s", state, f.events(op))
		}
		if n := len(e.st.node.AppliedRequests()); n != 1 {
			t.Errorf("the stand-in received %d applies; want the recorded attempt's one", n)
		}
	})

	t.Run("T8 journey", func(t *testing.T) { e.t8(t) })

	t.Run("REC-03", func(t *testing.T) { approvalCases["REC-03"](t, e) })
	f.scanFiles()
}

// localTimes are the times and the time left a plan display prints by this client's clock.
var localTimes = regexp.MustCompile(`\d{4}-\d\d-\d\d \d\d:\d\d:\d\d [A-Z]+|\(in \S+ by this client's clock\)`)

// t8 runs journey task T8 (operator-experience.md §13) as a script: a release changing the workers'
// labels is planned by h-publisher and approved by h-approver, and the change is read from the
// plan's diff. Standard error, which holds approve's display and prompt, is kept before standard
// output.
func (e *planEnv) t8(t *testing.T) {
	f := e.f
	e.in.set(nil, nil)
	dir := t.TempDir()
	labels := filepath.Join(dir, "worker-labels.yaml")
	if err := os.WriteFile(labels, []byte("apiVersion: v1alpha1\nkind: KubeNodeConfig\nlabels:\n  node.example.test/pool: workers\n  node.example.test/tier: b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := []string{"--context", "human"}
	draft := regexp.MustCompile(`drf_[a-z2-7]{26}`).FindString(e.ctl(append(h, "draft", "create", "--cluster", e.s.cluster, "--title", "worker tier")...).out)
	f.importMachine(t, e.s.machine, draft)
	for _, step := range [][]string{{"draft", "show", draft}, {"fragment", "set", "labels", "--draft", draft, "--layer", "role", "--file", labels},
		{"profile", "set", "workers", "--draft", draft, "--fragments", "labels"}, {"assignment", "set", e.s.machine, "--draft", draft, "--profiles", "workers"}} {
		f.want(e.ctl(append(h, step...)...), 0)
	}
	rel := regexp.MustCompile(`rel_[a-z2-7]{26}`).FindString(e.ctl(append(h, "publish", draft)...).out)
	var out strings.Builder
	step := func(stdin string, args ...string) result {
		r := e.ctlIn(stdin, args...)
		fmt.Fprintf(&out, "$ bronzewardctl %s\n%s%s[exit %d]\n\n", strings.Join(args, " "), r.err, r.out, r.code)
		return r
	}
	r := step("", "--context", "publisher", "plan", "create", "--release", rel, "--machine", e.s.machine, "--operation", "apply-config")
	p := planID.FindString(r.out)
	f.want(r, 0, "Changes shown below", "node.example.test/tier: b")
	r = step(p+"\n", "--context", "approver", "approve", p)
	f.want(r, 0, "Self-approval  predicted: you did not create this plan or publish this release, "+
		"and authored no fragment, profile or assignment revision in it.", "Recorded self-approval reasons: none.")
	// The prompt ends in a space, which the repository's whitespace check refuses in a kept file.
	kept := regexp.MustCompile(`(?m)[ \t]+$`).ReplaceAllString(localTimes.ReplaceAllString(out.String(), "<local time>"), "")
	keepJourney(t, "t8", kept, dir)
	e.until(t, "committed", `SELECT state FROM plan_state WHERE plan = $1`, p)
	var op string
	if err := f.db.QueryRow(`SELECT operation FROM plan_state WHERE plan = $1`, p).Scan(&op); err != nil || f.ended(op) != "completed" {
		t.Errorf("T8's operation %s ended %s, %v: %s", op, f.ended(op), err, f.events(op))
	}
}
