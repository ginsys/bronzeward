package clitest

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	bwid "github.com/ginsys/bronzeward/internal/id"
)

// seedDrift records an open drift record on machine, by SQL, as the observation transaction does
// (execution and recovery §4.1, §6.1): the start of a drift observation, its result reading a
// digest other than Applied's, and the drift entry that opens the record. Applied must exist. It
// answers the observation the record names. The seeding follows internal/api/residual_test.go,
// which is in another package.
func seedDrift(t *testing.T, db *sql.DB, machine string) string {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	run := func(query string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(query, args...); err != nil {
			t.Fatalf("seeding drift: %v\n%s", err, query)
		}
	}
	next := func() int64 {
		t.Helper()
		var rev int64
		if err := tx.QueryRow(`UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1 RETURNING revision_counter`,
			machine).Scan(&rev); err != nil {
			t.Fatal(err)
		}
		return rev
	}
	basis := next()
	run(`INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		SELECT m.id, $2, epoch, 'observation-started', jsonb_build_object('purpose', 'drift', 'endpoint', m.talos_endpoint::text,
			'controller', 'test-controller'), now()
		FROM installation_state, machine m WHERE m.id = $1`, machine, basis)
	run(`INSERT INTO observation_start (machine, revision, purpose, endpoint, controller)
		SELECT id, $2, 'drift', talos_endpoint, 'test-controller' FROM machine WHERE id = $1`, machine, basis)
	observed := next()
	obs := bwid.New(bwid.Observation)
	run(`INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		SELECT $1, $2, epoch, 'observation', jsonb_build_object('observation', $3::text, 'purpose', 'drift', 'basis', $4::bigint), now()
		FROM installation_state`, machine, observed, obs, basis)
	run(`INSERT INTO observation (id, machine, basis, revision, access_path, access_version, access_created,
			configuration_digest, unread, at)
		SELECT $1, id, $3, $4, 'access/talos/' || cluster, 3, '2026-10-01T00:00:00Z', $5, '{}', now()
		FROM machine WHERE id = $2`, obs, machine, basis, observed, bytes.Repeat([]byte{0xee}, 32))
	opened := next()
	run(`INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		SELECT $1, $2, epoch, 'drift', jsonb_build_object('record', $2::bigint, 'change', 'opened', 'observation', $3::text), now()
		FROM installation_state`, machine, opened, obs)
	run(`INSERT INTO drift_record (machine, opened, observation, applied_digest, observed_digest, last_observation)
		SELECT $1, $2, $3, applied_digest, $4, $3 FROM machine_state WHERE machine = $1`, machine, opened, obs, bytes.Repeat([]byte{0xee}, 32))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return obs
}

// observedAt is the time of observation obs as bronzewardctl shows it.
func observedAt(t *testing.T, db *sql.DB, obs string) string {
	t.Helper()
	var at time.Time
	if err := db.QueryRow(`SELECT at FROM observation WHERE id = $1`, obs).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at.UTC().Format("2006-01-02 15:04:05 MST")
}

// freezeWords are the sentences machine freeze, machine unfreeze and drift show say, pinned whole.
const (
	frozenWords   = "Frozen. No new commitment or attempt for this machine until an approver unfreezes it.\n"
	unfrozenWords = "Unfrozen. The freeze no longer stops a commitment or an attempt for this machine.\n"
	stopsWords    = "Frozen: no new commitment or attempt until an approver unfreezes; an attempt already recorded is not stopped, and an adoption can still be recorded.\n"
)

// TestDriftFreeze runs drift show, machine freeze and machine unfreeze (#360) through the CLI
// against a server with a provider and a Talos stand-in: the machine is adopted, so it has an
// Applied release, and an open drift record is seeded. It covers the sentences of an open drift
// and a frozen one, the freeze and unfreeze answers and their refusals, and the roles of each.
func TestDriftFreeze(t *testing.T) {
	e := newPlanEnv(t)
	// drift show prints a time in the zone of the client, and the fixture runs the client with no TZ,
	// so it takes the zone of the host. This test pins UTC, as the operation cases do, so that the
	// expected text of observedAt does not depend on the host. The other choice, to build the text in
	// the zone of the client, would have to know that zone, and the test cannot read it from the
	// child. Printing local time stays the client's choice (operator experience specification).
	e.env = []string{"TZ=UTC"}
	f, in := e.f, e.in
	h := []string{"--context", "human"}
	as := func(who string) []string {
		t.Helper()
		file := filepath.Join(f.home, who+".json")
		f.store(file, f.mint("h-"+who, ""), "")
		f.want(f.ctl(nil, "", "context", "add", who, "--server", in.URL, "--certificate-authority", in.ca, "--issuer", f.iss.URL,
			"--client-id", "BWSYNTH-cli", "--audience", "bronzeward", "--oidc-token-file", file), 0)
		return []string{"--context", who}
	}
	roles := map[string][]string{"viewer": as("viewer"), "publisher": as("publisher"), "approver": as("approver")}

	// A machine with no Applied has no drift to show.
	in.set(nil, nil)
	f.want(e.ctl(append(h, "drift", "show", e.s.machine)...), 0, "No open drift on machine "+e.s.machine+".",
		"Why: only a configuration read that differs from Applied opens a drift record")

	// The adoption gives the machine its Applied release (the T21 adopt plan of TestApprovals).
	p := planID.FindString(e.ctl(e.create("adopt")...).out)
	f.want(e.approveTyped("human", p), 0, "Applied becomes "+e.rel+" (adoption) once it does.")
	e.until(t, "committed", `SELECT state FROM plan_state WHERE plan = $1`, p)
	var op string
	if err := f.db.QueryRow(`SELECT operation FROM plan_state WHERE plan = $1`, p).Scan(&op); err != nil || f.ended(op) != "completed" {
		t.Fatalf("the adoption %s ended %s, %v: %s", op, f.ended(op), err, f.events(op))
	}
	f.want(e.ctl(append(h, "drift", "show", e.s.machine)...), 0, "No open drift on machine "+e.s.machine+".")

	obs := seedDrift(t, f.db, e.s.machine)
	at := observedAt(t, f.db, obs)
	open := "Drift opened by observation " + obs + " at " + at + ", last seen in observation " + obs + " at " + at + ". Late landing: no.\n" +
		"A configuration read differed from what Bronzeward last verified: Applied " + e.rel + ".\n" +
		"Bronzeward cannot show what changed until it is imported for adoption.\n" +
		"Choose: freeze while you decide; adopt it into a release; or revert to Desired (" + e.rel + ").\n"
	before := f.actCount()
	in.set(nil, nil)
	r := e.ctl(append(h, "drift", "show", e.s.machine)...)
	if r.code != 0 || r.out != open {
		t.Errorf("drift show: exit %d, stdout %q, stderr %q; want %q", r.code, r.out, r.err, open)
	}
	f.want(e.ctl(append(h, "-o", "json", "drift", "show", e.s.machine)...), 0, `"openDrift"`, obs)
	f.want(e.ctl(append(h, "drift", "ignore", e.s.machine)...), 2, "drift needs show")
	if n := f.actCount(); n != before {
		t.Errorf("%d acts recorded by drift show", n-before)
	}

	// The roles. A viewer may do neither; a publisher may freeze and not unfreeze.
	freeze := []string{"machine", "freeze", e.s.machine, "--reason", "BWSYNTH incident"}
	unfreeze := []string{"machine", "unfreeze", e.s.machine, "--reason", "BWSYNTH decided"}
	f.want(e.ctl(append(roles["viewer"], freeze...)...), 1, "403", "Needs one of the roles [", "You are signed in as h-viewer.")
	f.want(e.ctl(append(roles["viewer"], unfreeze...)...), 1, "403", "Needs one of the roles [approver].", "You are signed in as h-viewer.")
	f.want(e.ctl(append(roles["publisher"], unfreeze...)...), 1, "403", "Needs one of the roles [approver].", "You are signed in as h-publisher.")
	if n := f.actCount(); n != before {
		t.Errorf("%d acts recorded by refused roles", n-before)
	}
	f.want(e.ctl(append(roles["approver"], unfreeze...)...), 1, "409", "the machine scope is not frozen")

	// A freeze by a publisher, then the sentences of a frozen machine, then an unfreeze by an approver.
	recorded := regexp.MustCompile(`Recorded by \S+ \(publisher\): machine ` + e.s.machine + `, timeline revision [0-9]+, act act_[a-z2-7]+\.\n`)
	r = e.ctl(append(roles["publisher"], freeze...)...)
	if want := frozenWords + "An attempt already recorded is not stopped, and an adoption can still be recorded.\n"; r.code != 0 ||
		!strings.HasPrefix(r.out, want) || !recorded.MatchString(r.out) {
		t.Errorf("freeze: exit %d, stdout %q, stderr %q; want %q and a record line", r.code, r.out, r.err, want)
	}
	f.want(e.ctl(append(roles["approver"], freeze...)...), 1, "409", "the machine scope is already frozen")
	r = e.ctl(append(h, "drift", "show", e.s.machine)...)
	if r.code != 0 || r.out != open+stopsWords {
		t.Errorf("drift show, frozen: exit %d, stdout %q, stderr %q; want %q", r.code, r.out, r.err, open+stopsWords)
	}
	f.want(e.ctl(append(h, "machine", "show", e.s.machine)...), 0, stopsWords)
	r = e.ctl(append(roles["approver"], unfreeze...)...)
	if r.code != 0 || !strings.HasPrefix(r.out, unfrozenWords+"Unfreezing approves and commits nothing. An attempt already recorded was not stopped by the freeze, and an adoption could still be recorded.\n") ||
		!strings.Contains(r.out, "(approver): machine "+e.s.machine+", timeline revision ") {
		t.Errorf("unfreeze: exit %d, stdout %q, stderr %q", r.code, r.out, r.err)
	}
	r = e.ctl(append(h, "drift", "show", e.s.machine)...)
	if r.code != 0 || r.out != open {
		t.Errorf("drift show, unfrozen: exit %d, stdout %q, stderr %q; want %q", r.code, r.out, r.err, open)
	}
	if n := f.actCount(); n != before+2 {
		t.Errorf("%d acts recorded by a freeze and an unfreeze, want 2", n-before)
	}
	f.scanFiles()
}

// as is a context named who, signed in as h-who, on the injector's address.
func (e *planEnv) as(t *testing.T, who string) []string {
	t.Helper()
	file := filepath.Join(e.f.home, who+".json")
	e.f.store(file, e.f.mint("h-"+who, ""), "")
	e.f.want(e.f.ctl(nil, "", "context", "add", who, "--server", e.in.URL, "--certificate-authority", e.in.ca, "--issuer", e.f.iss.URL,
		"--client-id", "BWSYNTH-cli", "--audience", "bronzeward", "--oidc-token-file", file), 0)
	return []string{"--context", who}
}

// openDrift waits up to a minute for the controller's drift observation to open a record on the
// machine, and answers the record's number, the revision of its opening entry.
func (e *planEnv) openDrift(t *testing.T) int64 {
	t.Helper()
	var opened int64
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if err := e.f.db.QueryRow(`SELECT opened FROM drift_record WHERE machine = $1 AND closed IS NULL`, e.s.machine).Scan(&opened); err == nil {
			return opened
		}
	}
	t.Fatalf("no drift record opened on %s within a minute", e.s.machine)
	return 0
}

// closedAs waits up to two minutes for the drift record opened at revision to close, and answers how.
func (e *planEnv) closedAs(t *testing.T, opened int64) string {
	t.Helper()
	var resolution string
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if err := e.f.db.QueryRow(`SELECT resolution FROM drift_record WHERE machine = $1 AND opened = $2 AND closed IS NOT NULL`,
			e.s.machine, opened).Scan(&resolution); err == nil {
			return resolution
		}
	}
	t.Fatalf("drift record %d of %s did not close within two minutes", opened, e.s.machine)
	return ""
}

// driftRecordNumber and driftRevision are the numbers a journey's kept output replaces: they count
// the machine's timeline, which the controller's drift passes advance at a pace of their own.
var (
	driftRecordNumber = regexp.MustCompile(`drift record \d+`)
	driftRevision     = regexp.MustCompile(`timeline revision \d+`)
)

// TestDrift runs drift revert and drift adopt (#360) through the CLI against a server with a
// provider and a Talos stand-in whose drift loop passes every second, and journey task T10
// (operator-experience.md §13): a machine that is adopted, drifts because its node's
// configuration is changed by hand, is frozen, and is reverted; then drifts again and is adopted.
// The output is kept in testdata/journeys/t10.txt.
func TestDrift(t *testing.T) {
	e := newPlanEnvExec(t, ", driftInterval: 1s")
	f, in := e.f, e.in
	h := []string{"--context", "human"}
	publisher, approver := e.as(t, "publisher"), e.as(t, "approver")
	m := e.s.machine
	in.set(nil, nil)
	e.st.node.AcceptApply(t)

	// The adoption that gives the machine its Applied release (the T21 adopt plan of TestApprovals).
	p := planID.FindString(e.ctl(e.create("adopt")...).out)
	f.want(e.approveTyped("human", p), 0, "Applied becomes "+e.rel+" (adoption) once it does.")
	e.until(t, "committed", `SELECT state FROM plan_state WHERE plan = $1`, p)

	// With no open drift record there is nothing to revert: nothing is sent, and the refusal names plan create.
	before := f.actCount()
	f.want(e.ctl(append(publisher, "drift", "revert", m)...), 2, "machine "+m+" has no open drift record; nothing was sent.",
		"bronzewardctl plan create --release '"+e.rel+"' --machine '"+m+"' --operation 'apply-config'")
	if n := f.actCount(); n != before {
		t.Errorf("%d acts recorded by a refused revert", n-before)
	}

	var out strings.Builder
	step := func(stdin string, args ...string) result {
		r := e.ctlIn(stdin, args...)
		fmt.Fprintf(&out, "$ bronzewardctl %s\n%s%s[exit %d]\n\n", strings.Join(args, " "), r.err, r.out, r.code)
		return r
	}
	with := func(ctx []string, args ...string) []string { return append(slices.Clone(ctx), args...) }

	// T10, the revert: the node's configuration changes, the drift loop opens a record, the machine
	// is frozen while the operator decides, the revert plan is made and approved after an approver
	// lifts the freeze.
	e.st.drift(t, 1)
	opened := e.openDrift(t)
	f.want(step("", with(h, "drift", "show", m)...), 0, "Drift opened by observation", "Choose: freeze while you decide; adopt it into a release; or revert to Desired ("+e.rel+").")
	f.want(step("", with(publisher, "machine", "freeze", m, "--reason", "BWSYNTH: decide first")...), 0, "Frozen.")
	f.want(step("", with(h, "drift", "show", m)...), 0, "Frozen: no new commitment or attempt")
	r := step("", with(publisher, "drift", "revert", m)...)
	revert := planID.FindString(r.out)
	f.want(r, 0, "Plan "+revert, fmt.Sprintf("Revert of drift record %d to Desired %s.", opened, e.rel),
		"Approving it approves an unseen overwrite", "The machine is frozen: the plan cannot commit until an approver approves it and, separately, unfreezes the machine: bronzewardctl machine unfreeze '"+m+"' --reason <text>")
	var bound int64
	var operation string
	if err := f.db.QueryRow(`SELECT drift_record, kind FROM plan WHERE id = $1`, revert).Scan(&bound, &operation); err != nil || bound != opened || operation != "apply-config" {
		t.Errorf("the revert plan binds drift record %d with operation %q, %v; want %d and apply-config", bound, operation, err, opened)
	}
	f.want(step("", with(approver, "machine", "unfreeze", m, "--reason", "BWSYNTH: decided to revert")...), 0, "Unfrozen.")
	f.want(step(revert+"\n", with(approver, "approve", revert)...), 0, "which no reviewer has seen")
	e.until(t, "committed", `SELECT state FROM plan_state WHERE plan = $1`, revert)
	if got := e.closedAs(t, opened); got != "reverted" {
		t.Errorf("the revert closed drift record %d as %q, want reverted", opened, got)
	}

	// T10, the adoption: the node drifts again, and the drift is adopted into a release. The draft
	// carries the assignment that makes the release the machine's Desired; the composite imports
	// the node over the draft's base, publishes the draft and creates the adopt plan, and an approver approves it.
	e.st.drift(t, 2)
	opened = e.openDrift(t)
	labels := filepath.Join(t.TempDir(), "labels.yaml")
	if err := os.WriteFile(labels, []byte("machine:\n  nodeLabels:\n    node.example.test/pool: workers\n    node.example.test/tier: adopted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	draft := regexp.MustCompile(`drf_[a-z2-7]{26}`).FindString(e.ctl(append(h, "draft", "create", "--cluster", e.s.cluster, "--title", "adopt drift")...).out)
	f.importMachine(t, m, draft)
	for _, s := range [][]string{{"draft", "show", draft}, {"fragment", "set", "labels", "--draft", draft, "--layer", "role", "--file", labels},
		{"profile", "set", "workers", "--draft", draft, "--fragments", "labels"}, {"assignment", "set", m, "--draft", draft, "--profiles", "workers"}} {
		f.want(e.ctl(append(h, s...)...), 0)
	}
	f.want(step("", with(h, "drift", "show", m)...), 0, "Drift opened by observation")
	before = f.actCount()
	r = step("", with(h, "drift", "adopt", m, "--draft", draft)...)
	adopt := planID.FindString(r.out)
	f.want(r, 0, "Step 1/3  import (drift-adoption) of machine "+m+" into draft "+draft, "Step 2/3  publish draft "+draft,
		"Step 3/3  plan an adoption of release rel_", fmt.Sprintf("This plan binds drift record %d.", opened),
		"drift adopt never approves: an approver runs bronzewardctl approve '"+adopt+"'")
	if state, approval := e.planState(t, adopt); state != "proposed" || approval != "" {
		t.Errorf("the adopt plan is %s with approval %q after drift adopt, want proposed with none", state, approval)
	}
	f.want(step(adopt+"\n", with(approver, "approve", adopt)...), 0)
	e.until(t, "committed", `SELECT state FROM plan_state WHERE plan = $1`, adopt)
	if got := e.closedAs(t, opened); got != "adopted" {
		t.Errorf("the adoption closed drift record %d as %q, want adopted", opened, got)
	}

	if n := f.actCount(); n <= before {
		t.Errorf("%d acts recorded by the adoption, want some", n-before)
	}

	// The composite also states, without running a step, what a caller without the roles still has to do.
	// The count is read right before it, so the check is on its own acts and not on the adoption's.
	viewer := e.as(t, "viewer")
	beforeViewer := f.actCount()
	f.want(e.ctl(with(viewer, "drift", "adopt", m, "--draft", draft)...), 0, "Not run: step 1 needs an author signed in as a person. The steps:")
	if n := f.actCount(); n != beforeViewer {
		t.Errorf("%d acts recorded by a viewer's drift adopt, want none", n-beforeViewer)
	}

	// The prompt ends in a space, which the repository's whitespace check refuses in a kept file.
	kept := localTimes.ReplaceAllString(out.String(), "<local time>")
	kept = driftRevision.ReplaceAllString(driftRecordNumber.ReplaceAllString(kept, "drift record <n>"), "timeline revision <n>")
	kept = regexp.MustCompile(`(?m)[ \t]+$`).ReplaceAllString(kept, "")
	keepJourney(t, "t10", kept, f.home)
	f.scanFiles()
}
