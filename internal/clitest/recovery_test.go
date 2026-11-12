package clitest

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// syntheticProvider is a provider and ingestion block whose address answers nothing and whose
// token files hold synthetic values. Serve reads the token files at start and contacts the
// provider only from its workers, so the recovery entry, which needs ingestion's owner identity,
// is served without a provider running.
func syntheticProvider(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	block := "provider:\n  address: http://127.0.0.1:1\n  keys: {baseline: bw-baseline, staging: bw-staging, digest: bw-digest, artifact: bw-artifact}\n"
	for _, p := range []string{"ingestion", "compiler", "metadata", "executor"} {
		path := filepath.Join(dir, p+".token")
		if err := os.WriteFile(path, []byte("BWSYNTH-"+p+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		block += "  " + p + "TokenFile: " + path + "\n"
	}
	return block + "ingestion: {instance: a, heartbeat: 5s, lease: 15s, absoluteExpiry: 10m, sweep: 15s}\n"
}

// enter is recovery enter with one synthetic restored backup.
var enter = []string{"recovery", "enter", "--restored", "family=database,backup=BWSYNTH-pg,taken-at=2026-11-01T08:30:00Z",
	"--stopped-at", "2026-11-01T08:31:00Z", "--reason", "BWSYNTH drill"}

// humanContext adds the oidc context "human" of f's fixture issuer behind a TLS injector that lets
// writes through, and returns the injector and a runner of bronzewardctl commands in that context.
func humanContext(f *fixture) (*injector, func(stdin string, args ...string) result) {
	f.t.Helper()
	in := newInjector(f.t, f.base)
	in.writing = true
	f.want(f.ctl(nil, "", "context", "add", "human", "--server", in.URL, "--certificate-authority", in.ca,
		"--issuer", f.iss.URL, "--client-id", "BWSYNTH-cli", "--audience", "bronzeward", "--oidc-token-file", f.humanFile()), 0, "credential oidc")
	return in, func(stdin string, args ...string) result {
		return f.scan(f.exec(nil, stdin, append([]string{"--context", "human"}, args...)...), args)
	}
}

// recoveryCases are the client-contracts.md §12 recovery cases bronzewardctl's recovery enter runs;
// testdata/recovery-cases.yaml holds their records, and TestRecovery runs exactly those, in order.
var recoveryCases = []string{"REC-02", "REC-04"}

// TestRecovery runs recovery enter, status and exit and identity revoke through the CLI against a
// served bronzeward: on a normal start, then on a recovery start of the same database.
func TestRecovery(t *testing.T) {
	b, err := os.ReadFile("testdata/recovery-cases.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var records []struct {
		ID string `yaml:"id"`
	}
	if err := yaml.Unmarshal(b, &records); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range records {
		ids = append(ids, r.ID)
	}
	if !slices.Equal(ids, recoveryCases) {
		t.Fatalf("records %v, cases %v: want one record per case, in order", ids, recoveryCases)
	}

	f := startConfig(t, "viewer", "", "bronzeward", syntheticProvider(t))
	s := f.seed()
	in, ctl := humanContext(f)
	contexts := func() string { return ctl("", "context", "list", "-o", "json").out }
	epoch := func() string {
		var e string
		if err := f.db.QueryRow(`SELECT epoch FROM installation_state`).Scan(&e); err != nil {
			t.Fatal(err)
		}
		return e
	}

	// identity revoke, on the normal start: h-viewer signs in once, so its identity exists.
	f.store(f.humanFile(), f.mint("h-viewer", ""), "")
	f.want(ctl("", "whoami"), 0, "h-viewer")
	f.store(f.humanFile(), f.mint("h-recovery", ""), "")
	revoke := []string{"identity", "revoke", "--iss", f.iss.URL, "--sub", "h-viewer", "--reason", "BWSYNTH left"}
	in.set(nil, nil)
	f.want(ctl("BWSYNTH-wrong\n", revoke...), 2, "A revocation is permanent", "nothing was sent")
	if slices.Contains(in.requests(), "POST /api/v1/identity-revocations") {
		t.Errorf("a wrong confirmation sent the revocation: %v", in.requests())
	}
	f.want(ctl("h-viewer\n", revoke...), 0, "Revoked idn_", "auth.deniedSubjects lists it: no: add it now")

	// REC-02, first: the entry sent to a normal start.
	before := f.actCount()
	in.set(nil, nil)
	f.want(ctl("enter\n", enter...), 1, "409", "(conflict)", "This server was started normally; only a server started for recovery serves the entry.")
	if c := contexts(); !strings.Contains(c, `"startMode":"normal"`) {
		t.Errorf("REC-02 normal start: the context does not read a normal start:\n%s", c)
	}

	// The typed confirmation on the recovery start: nothing is sent unless enter is typed back.
	f.restart("-recovery-start")
	for _, typed := range []string{"", "\n", "BWSYNTH-wrong\n", "enterx\n"} {
		in.set(nil, nil)
		f.want(ctl(typed, enter...), 2, "It marks every machine's scope pre-restore-unaccounted.", "nothing was sent")
		if seen := in.requests(); len(seen) != 0 {
			t.Errorf("typed %q: sent %v", typed, seen)
		}
	}
	in.set(nil, nil)
	f.want(ctl("", append(slices.Clone(enter), "--no-input")...), 2, "--no-input forbids", "nothing was sent")
	if seen := in.requests(); len(seen) != 0 {
		t.Errorf("--no-input: sent %v", seen)
	}

	// REC-01's recovery acts, before the entry (#362): a recovery start serves only the entry, so each
	// act shows the process's restriction, names no scope and sends no write.
	// The acts are counted in the database: the acts list is a read, which this start refuses too.
	actRows := func() (n int) {
		if err := f.db.QueryRow(`SELECT count(*) FROM act`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	beforeActs := actRows()
	for _, args := range [][]string{
		{"recovery", "takeover", "op_1", "--reason", "BWSYNTH drill"},
		{"recovery", "resolve", "op_1", "--reason", "BWSYNTH drill"},
		{"recovery", "scope-account", s.machine, "--start", "obs_s", "--end", "obs_e"},
		{"recovery", "account", "op_1", "att_1", "--start", "obs_s", "--end", "obs_e"},
		{"recovery", "mark", s.machine},
		{"recovery", "release", s.machine},
	} {
		in.set(nil, nil)
		r := ctl("", args...)
		f.want(r, 1, "409", "Recovery mode: the server was started for recovery:", "A restriction of the process, not of one scope")
		if strings.Contains(r.err, "the scope of machine") {
			t.Errorf("%s: the process's refusal names a scope:\n%s", args[1], r.err)
		}
		for _, req := range in.requests() {
			if strings.HasPrefix(req, "POST ") {
				t.Errorf("%s: sent %s", args[1], req)
			}
		}
	}
	if n := actRows(); n != beforeActs {
		t.Errorf("the refused recovery acts recorded %d acts, want none", n-beforeActs)
	}

	// REC-04 (a): the entry commits and the injector drops its 201.
	var posts sent
	in.set(posts.posting(func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 0 {
			in.commit(w, r)
			return
		}
		in.proxy.ServeHTTP(w, r)
	}), nil)
	previous := epoch()
	r := ctl("enter\n", enter...)
	minted := epoch()
	f.want(r, 0, "repeat 1 of 3 with the same key", "already done earlier; showing that result",
		"Recovery mode entered (act act_", "Epoch "+minted+", was "+previous+".", "Scopes marked pre-restore-unaccounted: 1 machines.")
	posts.one(t, 2)
	if posts.expected[0] != "" {
		t.Errorf("REC-04: the entry was sent with Bronzeward-Expected-Epoch %q", posts.expected[0])
	}
	if want := []string{"GET /api/v1/server", "POST /api/v1/recovery/entries", "GET /api/v1/recovery", "POST /api/v1/recovery/entries", "GET /api/v1/server"}; !slices.Equal(in.requests(), want) {
		t.Errorf("REC-04: the injector saw %v, want %v", in.requests(), want)
	}
	if c := contexts(); minted == previous || !strings.Contains(c, `"lastEpoch":"`+minted+`"`) || !strings.Contains(c, `"startMode":"recovery"`) {
		t.Errorf("REC-04: epoch %s, was %s; the context does not keep the minted epoch and a recovery start:\n%s", minted, previous, c)
	}

	// REC-02, second: another entry to the recovery start whose entry committed under REC-04's key.
	in.set(nil, nil)
	f.want(ctl("enter\n", enter...), 1, "409", "(conflict)", "Another recovery entry committed first (epoch "+minted+"); this one recorded nothing.")
	if c := contexts(); strings.Contains(c, `"startMode":"normal"`) || !strings.Contains(c, `"startMode":"recovery"`) {
		t.Errorf("REC-02 recovery start: the context reads a normal start:\n%s", c)
	}
	if n := f.actCount(); n != before+1 {
		t.Errorf("%d acts recorded by the entries, want 1", n-before)
	}

	// recovery status and exit: the seeded machine's scope is not released.
	f.want(ctl("", "recovery", "status"), 0, "Recovery mode", "on", "BWSYNTH drill",
		s.machine, "pre-restore-unaccounted", "recovery scope-account '"+s.machine+"'")
	f.want(ctl("", "recovery", "exit"), 1, "409", "The first scope not released: machine '"+s.machine+"', 'pre-restore-unaccounted'.")
	// REC-01's recovery acts, after the entry (#362): a mark and a release of the pre-restore-unaccounted
	// scope are refused for that scope, with the accounting as the next step and no process restriction.
	beforeScopeActs := actRows()
	for _, verb := range []string{"mark", "release"} {
		r := ctl("", "recovery", verb, s.machine)
		f.want(r, 1, "409", "Recovery mode: the scope of machine "+s.machine+" is pre-restore-unaccounted.",
			"Next: account the scope first: recovery scope-account '"+s.machine+"'.")
		if strings.Contains(r.err, "started for recovery") {
			t.Errorf("recovery %s: the scope's refusal holds the process's restriction:\n%s", verb, r.err)
		}
	}
	if n := actRows(); n != beforeScopeActs {
		t.Errorf("the refused mark and release recorded %d acts, want none", n-beforeScopeActs)
	}
	// A token without recovery-admin: the three commands that send a recovery write show the 403
	// and the role they need (client-contracts.md §9.6). Typed confirmations are given, so the
	// refusal is the server's. The accountings read observations first; a seeded machine has none,
	// so they stop at the client and their 403 is not run here. h-viewer is revoked above, and a
	// revoked subject answers 403 identity-revoked (§9.6), so another viewer signs in.
	f.store(f.humanFile(), f.viewerToken("h-viewer-2", 0, 5*time.Minute), "")
	in.set(nil, nil)
	for _, c := range []struct {
		typed string
		args  []string
	}{
		{"enter\n", enter},
		{"", []string{"recovery", "exit"}},
		{"h-recovery\n", []string{"identity", "revoke", "--iss", f.iss.URL, "--sub", "h-recovery", "--reason", "BWSYNTH left"}},
	} {
		f.want(ctl(c.typed, c.args...), 1, "403", "Needs one of the roles", "recovery-admin")
	}

	// REC-01's unfreeze variants (#360): a freeze is accepted on any scope, and an unfreeze is
	// refused twice over. A server started for recovery refuses it with no scope member, even after
	// the entry; a normal start of the same database refuses it for the machine's scope.
	f.store(f.humanFile(), f.mint("h-all", ""), "")
	freeze := []string{"machine", "freeze", s.machine, "--reason", "BWSYNTH drill freeze"}
	unfreeze := []string{"machine", "unfreeze", s.machine, "--reason", "BWSYNTH drill unfreeze"}
	frozen := func() bool {
		var v bool
		if err := f.db.QueryRow(`SELECT frozen FROM machine WHERE id = $1`, s.machine).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	in.set(nil, nil)
	f.want(ctl("", freeze...), 0, "Frozen. No new commitment or attempt for this machine until an approver unfreezes it.")
	acts := f.actCount()
	process := []string{"Recovery mode: the server was started for recovery: it refuses plan creation, approval and unfreeze",
		"Why: a server started for recovery refuses every request but the recovery entry until the entry is recorded. After the entry it still refuses unfreezing, plan creation and approval.",
		"Next: if the recovery entry is not recorded yet, record it with recovery enter. Then restart the server without the recovery flag and repeat this command."}
	f.want(ctl("", unfreeze...), 1, append([]string{"409"}, process...)...)
	f.restart()
	scope := []string{"Recovery mode: the scope of machine " + s.machine + " is pre-restore-unaccounted.",
		"Why: a restore can erase the record of a request sent to this machine, and a request sent before the restore may still land. " +
			"Until a recovery administrator accounts for the scope, unfreezing is refused.",
		"Next: recovery status names the next step of each scope."}
	r = ctl("", unfreeze...)
	f.want(r, 1, append([]string{"409"}, scope...)...)
	for _, absent := range []string{"started for recovery", "restart the server"} {
		if strings.Contains(r.err, absent) {
			t.Errorf("the scope's refusal holds %q:\n%s", absent, r.err)
		}
	}
	if n := f.actCount(); n != acts || !frozen() {
		t.Errorf("refused unfreezes recorded %d acts, frozen %v; want none and still frozen", n-acts, frozen())
	}
	f.scanFiles()
}

// REC-04 (b): the server commits the entry and loses its own reply, which it answers 500 with the
// outcome unknown, still gated, so the confirming read is refused. The client repeats under the
// same key with no epoch check and shows the replayed result as its own, as in (a). The server is
// the interrupt build, steered by a control file of this test.
func TestRecoveryLostReply(t *testing.T) {
	f := startConfig(t, "viewer", "", "bronzeward", syntheticProvider(t))
	f.seed()
	f.interrupt("recovery-entry-commit lose-reply", "-recovery-start")
	in, ctl := humanContext(f)
	f.store(f.humanFile(), f.mint("h-recovery", ""), "")
	epoch := func() string {
		var e string
		if err := f.db.QueryRow(`SELECT epoch FROM installation_state`).Scan(&e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	// The acts are counted in the database: the gated server refuses every read but the entry.
	acts := func() (n int) {
		if err := f.db.QueryRow(`SELECT count(*) FROM act`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before, previous := acts(), epoch()
	in.set(nil, nil)
	r := ctl("enter\n", enter...)
	minted := epoch()
	f.want(r, 0, "repeat 1 of 3 with the same key", "already done earlier; showing that result",
		"Recovery mode entered (act act_", "Epoch "+minted+", was "+previous+".")
	if want := []string{"GET /api/v1/server", "POST /api/v1/recovery/entries", "GET /api/v1/recovery", "POST /api/v1/recovery/entries", "GET /api/v1/server"}; !slices.Equal(in.requests(), want) {
		t.Errorf("REC-04 (b): the injector saw %v, want %v", in.requests(), want)
	}
	if after := acts(); minted == previous || after != before+1 {
		t.Errorf("REC-04 (b): epoch %s, was %s; %d acts recorded, want 1", minted, previous, after-before)
	}
	f.srv.stop() // the log is read only after the process ended
	if n := strings.Count(f.srv.log.String(), "fixture interrupt: lose-reply at recovery-entry-commit"); n != 1 {
		t.Errorf("REC-04 (b): the server lost %d replies, want 1:\n%s", n, f.srv.log.String())
	}
	f.scanFiles()
}
