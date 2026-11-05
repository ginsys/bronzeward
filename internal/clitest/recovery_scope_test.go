package clitest

import (
	"fmt"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// recoveryScopeCases are the client-contracts.md §12 cases bronzewardctl's recovery scope-account runs
// against an ambiguous write (#362); testdata/recovery-scope-cases.yaml holds their records, and
// TestRecoveryScopeAmbiguity runs exactly those, each on a server of its own.
var recoveryScopeCases = map[string]func(*testing.T, *recoveryEnv){
	"AMB-05": func(t *testing.T, e *recoveryEnv) {
		var s sent
		var epoch atomic.Value // written by the injector's goroutine
		e.in.set(s.posting(func(_ int, w http.ResponseWriter, r *http.Request) {
			e.in.commit(w, r)
			epoch.Store(e.f.newEpoch())
		}), nil)
		before := e.f.actCount()
		r := e.ctl(e.s.machine+"\n", e.scopeAccount()...)
		e.f.want(r, 3, "set aside, not repeated: the epoch is now "+fmt.Sprint(epoch.Load())+", not the reference epoch",
			"the newest timeline entry of machine "+e.s.machine, "--idempotency-key ")
		s.one(t, 1)
		if n := e.f.actCount(); r.out != "" || n != before+1 {
			t.Errorf("standard output %q, %d acts recorded; want none and 1", r.out, n-before)
		}
	},
	"AMB-07": func(t *testing.T, e *recoveryEnv) {
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
		r := e.ctl(e.s.machine+"\n", e.scopeAccount()...)
		e.f.want(r, 3, "repeat 1 of 3", "set aside, not repeated: the key's record is from an earlier epoch", "the newest timeline entry of machine "+e.s.machine)
		s.one(t, 2)
		if n := e.f.actCount(); r.out != "" || n != before+1 {
			t.Errorf("standard output %q, %d acts recorded; want none and 1", r.out, n-before)
		}
	},
	"AMB-12": func(t *testing.T, e *recoveryEnv) {
		var s sent
		var epoch atomic.Value // written by the injector's goroutine
		e.in.set(s.posting(func(n int, w http.ResponseWriter, r *http.Request) {
			if n == 0 {
				drop(w, r)
				return
			}
			epoch.Store(e.f.newEpoch())
			e.in.proxy.ServeHTTP(w, r)
		}), nil)
		before := e.f.actCount()
		r := e.ctl(e.s.machine+"\n", e.scopeAccount()...)
		e.f.want(r, 3, "set aside, not repeated: the epoch is now "+fmt.Sprint(epoch.Load())+", and this send committed nothing",
			"the newest timeline entry of machine "+e.s.machine)
		s.one(t, 2)
		if n := e.f.actCount(); r.out != "" || n != before {
			t.Errorf("standard output %q, %d acts recorded; want none", r.out, n-before)
		}
	},
}

// recoveryEnv is a served bronzeward on a recovery start whose entry the human h-recovery committed,
// behind the injector, with the seeded machine's scope pre-restore unaccounted and two observations of
// it: the start, a routine read, and the end, a recovery observation, both at resource version 7 and
// both long enough ago for the settle.
type recoveryEnv struct {
	f          *fixture
	in         *injector
	s          seeded
	ctl        func(typed string, args ...string) result
	start, end string
}

// newRecoveryEnv starts the server, enters recovery and seeds the observations.
func newRecoveryEnv(t *testing.T) *recoveryEnv {
	t.Helper()
	f := startConfig(t, "viewer", "", "bronzeward", syntheticProvider(t))
	e := &recoveryEnv{f: f, s: f.seed(), in: newInjector(t, f.base)}
	e.in.writing = true
	f.want(f.ctl(nil, "", "context", "add", "human", "--server", e.in.URL, "--certificate-authority", e.in.ca,
		"--issuer", f.iss.URL, "--client-id", "BWSYNTH-cli", "--audience", "bronzeward", "--oidc-token-file", f.humanFile()), 0, "credential oidc")
	f.restart("-recovery-start")
	e.ctl = func(typed string, args ...string) result {
		return f.scan(f.exec(nil, typed, append([]string{"--context", "human"}, args...)...), args)
	}
	f.store(f.humanFile(), f.mint("h-recovery", ""), "")
	e.in.set(nil, nil)
	f.want(e.ctl("enter\n", "recovery", "enter", "--restored", "family=database,backup=BWSYNTH-pg,taken-at=2026-11-01T08:30:00Z",
		"--stopped-at", "2026-11-01T08:31:00Z", "--reason", "BWSYNTH drill"), 0, "Recovery mode entered")
	e.start = f.seedObservation(e.s.machine, "drift", "7", 20*time.Minute)
	e.end = f.seedObservation(e.s.machine, "recovery", "7", 10*time.Minute)
	return e
}

// scopeAccount is the scope accounting of the seeded machine over the two observations.
func (e *recoveryEnv) scopeAccount() []string {
	return []string{"recovery", "scope-account", e.s.machine, "--start", e.start, "--end", e.end}
}

// seedObservation records an observation of machine as the server's own would be: its start entry
// and its result entry on the machine's timeline, ago before now, reading resource version version.
// The synthetic server has no Talos stand-in, so this is the only way to give the settle two
// observations to account between.
func (f *fixture) seedObservation(machine, purpose, version string, ago time.Duration) string {
	f.t.Helper()
	obs := id.New(id.Observation)
	var rev int64
	if err := f.db.QueryRow(`UPDATE machine SET revision_counter = revision_counter + 2 WHERE id = $1 RETURNING revision_counter`,
		machine).Scan(&rev); err != nil {
		f.t.Fatal(err)
	}
	secs := -ago.Seconds()
	for _, s := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
			SELECT $1, $2, epoch, 'observation-started', jsonb_build_object('purpose', $3::text), now() + make_interval(secs => $4)
			FROM installation_state`, []any{machine, rev - 1, purpose, secs - 2}},
		{`INSERT INTO observation_start (machine, revision, purpose, endpoint, controller)
			SELECT id, $2, $3, talos_endpoint, 'BWSYNTH-controller' FROM machine WHERE id = $1`, []any{machine, rev - 1, purpose}},
		{`INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
			SELECT $1, $2, epoch, 'observation', jsonb_build_object('observation', $3::text, 'purpose', $4::text), now() + make_interval(secs => $5)
			FROM installation_state`, []any{machine, rev, obs, purpose, secs}},
		{`INSERT INTO observation (id, machine, basis, revision, access_path, access_version, access_created, smbios_uuid, talos_node_id,
				talos_cluster_id, running_version, configuration_digest, resource_version, unread, at)
			SELECT $1, m.id, $3 - 1, $3, 'access/talos/' || m.cluster, 1, '2026-10-19T00:00:00Z', m.smbios_uuid, coalesce(m.talos_node_id, 'fixture-node'),
				c.talos_cluster_id, c.contract || '.0', decode(repeat('ab', 32), 'hex'), $4, '{"health":{"cause":"none-bound"}}',
				now() + make_interval(secs => $5)
			FROM machine m JOIN cluster c ON c.id = m.cluster WHERE m.id = $2`, []any{obs, machine, rev, version, secs}},
	} {
		if _, err := f.db.Exec(s.q, s.args...); err != nil {
			f.t.Fatalf("%.60s: %v", s.q, err)
		}
	}
	return obs
}

// TestRecoveryScopeAmbiguity runs the ambiguous-write cases with recovery scope-account as the
// dropped write: an epoch change between the write and its repeat is read, never repeated through.
func TestRecoveryScopeAmbiguity(t *testing.T) {
	for _, name := range cases(t, "testdata/recovery-scope-cases.yaml", recoveryScopeCases) {
		t.Run(name, func(t *testing.T) {
			e := newRecoveryEnv(t)
			recoveryScopeCases[name](t, e)
			e.f.scanFiles()
		})
	}
}

// recoveryWrites are the nine recovery write commands, each with what its typed answer needs.
func (e *recoveryEnv) recoveryWrites(withAttempt, withoutAttempt string) []struct {
	typed string
	args  []string
} {
	m := e.s.machine
	return []struct {
		typed string
		args  []string
	}{
		{"enter\n", []string{"recovery", "enter", "--restored", "family=database,backup=BWSYNTH-pg,taken-at=2026-11-01T08:30:00Z", "--stopped-at", "2026-11-01T08:31:00Z", "--reason", "BWSYNTH drill"}},
		{"", []string{"recovery", "exit"}},
		{m + "\n", e.scopeAccount()},
		{m + "\n", []string{"recovery", "account", withAttempt, "att_BWSYNTH", "--start", e.start, "--end", e.end}},
		{"", []string{"recovery", "takeover", withAttempt, "--reason", "BWSYNTH drill"}},
		{"", []string{"recovery", "resolve", withoutAttempt, "--reason", "BWSYNTH drill"}},
		{"", []string{"recovery", "mark", m}},
		{"", []string{"recovery", "release", m}},
		{"h-recovery\n", []string{"identity", "revoke", "--iss", e.f.iss.URL, "--sub", "h-recovery", "--reason", "BWSYNTH left"}},
	}
}

// TestRecoveryWritesRefuseWithoutTheRole: a person signed in without recovery-admin is shown the 403
// and the role on each of the nine recovery write commands, and no act is recorded (#361 criterion 3,
// gap G14). The typed answers are given, so the refusal is the server's. The reads that the commands
// make first are the server's, but for the two operations they read, which the injector answers: the
// synthetic server records no operation.
func TestRecoveryWritesRefuseWithoutTheRole(t *testing.T) {
	e := newRecoveryEnv(t)
	const withAttempt, withoutAttempt = "op_BWSYNTHwithattempt", "op_BWSYNTHnoattempt"
	op := func(name, attempts string) string {
		return `{"id":"` + name + `","kind":"apply-config","state":"unresolved","epoch":"","subject":{"machine":"` + e.s.machine + `"}` + attempts + `}`
	}
	canned := map[string]string{}
	canned["/api/v1/operations/"+withAttempt] = op(withAttempt, `,"attempts":[{"id":"att_BWSYNTH","number":1,"generation":1,"transportDeadline":"2026-11-01T08:35:00Z"}]`)
	canned["/api/v1/operations/"+withoutAttempt] = op(withoutAttempt, "")
	canned["/api/v1/operations/"+withAttempt+"/events"] = `{"items":[]}`
	canned["/api/v1/operations/"+withoutAttempt+"/events"] = `{"items":[]}`
	e.in.set(func(w http.ResponseWriter, r *http.Request) bool {
		body, ok := canned[r.URL.Path]
		if r.Method != http.MethodGet || !ok {
			return false
		}
		var epoch string
		if err := e.f.db.QueryRow(`SELECT epoch FROM installation_state`).Scan(&epoch); err != nil {
			e.f.t.Errorf("epoch: %v", err)
		}
		w.Header().Set("Bronzeward-Epoch", epoch)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
		return true
	}, nil)
	writes := e.recoveryWrites(withAttempt, withoutAttempt)
	// The list is the recovery write routes: a new verb that sends one needs a case here.
	if verbs := []string{"enter", "exit", "scope-account", "account", "takeover", "resolve", "mark", "release"}; len(writes) != len(verbs)+1 {
		t.Fatalf("%d commands, want %d recovery verbs and identity revoke", len(writes), len(verbs))
	}
	e.f.store(e.f.humanFile(), e.f.mint("h-viewer", ""), "")
	before := e.f.actCount()
	for _, c := range writes {
		e.f.want(e.ctl(c.typed, c.args...), 1, "403", "Needs one of the roles", "recovery-admin", "You are signed in as h-viewer.")
	}
	if n := e.f.actCount(); n != before {
		t.Errorf("%d acts recorded by refused writes", n-before)
	}
	if !slices.Contains(e.in.requests(), "POST /api/v1/recovery/exits") {
		t.Errorf("recovery exit was not sent: %v", e.in.requests())
	}
	e.f.scanFiles()
}
