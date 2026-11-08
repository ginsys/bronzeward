package clitest

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"regexp"
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
