package api

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/role"

	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/staging"
	"github.com/ginsys/bronzeward/internal/talos"
	"github.com/ginsys/bronzeward/internal/talos/talostest"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
)

// execEnv is a commitEnv whose machine is served by a stand-in node at the plan's route: the node
// holds standInConfig, the plan's expected digest, and takes the release's artifact (standInConfig
// with a hostname) as its configuration when a no-reboot apply reaches it.
type execEnv struct {
	*commitEnv
	node     *talostest.Node
	x        *fakeExecutor
	artifact []byte
	keys     *keyStore             // the signer the loops' own observations sign from
	keyAct   provider.CAKeyVersion // the version the cluster's first-key act names
}

func newExecEnv(t *testing.T) *execEnv {
	t.Helper()
	// The node trusts the signer's CA, so the certificates the loops sign for their own observations
	// are accepted, and so is the stored talosconfig a dispatch still sends with.
	ca := newTalosCA(t)
	pki := talostest.NewPKIFromCA(t, ca)
	node := talostest.Serve(t, pki)
	p := newPlanEnv(t)
	keys := newKeyStore(t, ca)
	p.env.keys, p.d.sg = keys, keys.sg
	keyAct := keys.seed(t, p.db, p.cluster)
	mustExec(t, p.db, `UPDATE machine SET talos_endpoint = $2 WHERE id = $1`, p.machine, node.Endpoint)
	ce := &commitEnv{planEnv: p}
	b := decode[planBody](t, ce.plan(ce.robot, "k-apply-0123456789ab", applyBody(ce.target.rel, ce.machine, "")), http.StatusCreated)
	ce.pid, ce.expires = b.ID, b.ExpiresAt
	ce.a = observer(t, ce.env, &fakeExecutor{}, options{})
	var uuid, nodeID sql.NullString
	var clusterID string
	if err := ce.db.QueryRow(`SELECT m.smbios_uuid::text, m.talos_node_id, c.talos_cluster_id FROM machine m
		JOIN cluster c ON c.id = m.cluster WHERE m.id = $1`, ce.machine).Scan(&uuid, &nodeID, &clusterID); err != nil {
		t.Fatal(err)
	}
	node.SetSMBIOSUUID(t, uuid.String, uuid.Valid)
	node.SetNodeID(t, cmp.Or(nodeID.String, "node-a"))
	node.SetClusterID(t, clusterID)
	node.SetMachineConfig(t, []byte(standInConfig))
	artifact := []byte(strings.Replace(standInConfig, "  type: worker\n", "  type: worker\n  network:\n    hostname: wk-1\n", 1))
	before, after := talos.ConfigurationDigest([]byte(standInConfig)), talos.ConfigurationDigest(artifact)
	// The plan expects the node's configuration, and the release's artifact has its own digest.
	mustExec(t, ce.db, `ALTER TABLE plan DISABLE TRIGGER immutable_rows`)
	mustExec(t, ce.db, `UPDATE plan SET expected_digest = $2 WHERE id = $1`, ce.pid, before[:])
	mustExec(t, ce.db, `ALTER TABLE plan ENABLE TRIGGER immutable_rows`)
	mustExec(t, ce.db, `ALTER TABLE release_machine DISABLE TRIGGER immutable_rows`)
	mustExec(t, ce.db, `UPDATE release_machine SET configuration_digest = $3 WHERE release = $1 AND machine = $2`,
		ce.target.rel, ce.machine, after[:])
	mustExec(t, ce.db, `ALTER TABLE release_machine ENABLE TRIGGER immutable_rows`)
	node.AcceptApply(t)
	// The talosconfig's own endpoints and nodes, unroutable, are ignored: the machine's endpoint is dialled (PA §16).
	return &execEnv{commitEnv: ce, node: node, artifact: artifact, keys: keys, keyAct: keyAct, x: &fakeExecutor{artifact: artifact}}
}

// executor is a controller of the current epoch over xe's database whose executor loop, once
// started, signals idle after each pass and stops when life ends. A failed attempt of work waits a
// millisecond.
func (xe *execEnv) executor(t *testing.T, life context.Context, idle chan<- struct{}, o options) *API {
	t.Helper()
	d := xe.env.d
	d.exe, d.life, d.runs = xe.x, life, &sync.WaitGroup{}
	d.timers = config.Ingestion{Heartbeat: time.Hour, Lease: time.Millisecond}
	d.owner = staging.Owner{ID: "x/1/" + rand.Text(), Epoch: epoch(t, xe.db)}
	o.onExecIdle = func() {
		select {
		case idle <- struct{}{}:
		case <-life.Done():
		}
	}
	return xe.buildWith(d, o)
}

// until passes the loop, one wake at a time, until cond holds.
func until(t *testing.T, a *API, idle <-chan struct{}, what string, cond func() bool) {
	t.Helper()
	for range 200 {
		if cond() {
			return
		}
		a.wakeExecutor()
		select {
		case <-idle:
		case <-time.After(10 * time.Second):
			t.Fatalf("the executor loop did not pass while waiting for %s", what)
		}
	}
	t.Fatalf("never reached: %s", what)
}

// passEnds waits for the end of a pass that started no earlier than now, so the pass in which an
// until condition came to hold has ended too: its idle signal blocks until received.
func passEnds(t *testing.T, a *API, idle <-chan struct{}) {
	t.Helper()
	a.wakeExecutor()
	waitIdle(t, idle, 1)
}

// approveThrough approves the plan through a's T5a route, which wakes a's loops.
func (xe *execEnv) approveThrough(t *testing.T, a *API) {
	t.Helper()
	xe.approval = decode[approvalBody](t, xe.do(a, call{method: "POST", path: prefix + "/plans/" + xe.pid + "/approvals",
		token: xe.human("h-approver"), key: xe.key(), body: `{}`}), http.StatusCreated).ID
}

func (xe *execEnv) opState(t *testing.T) string {
	t.Helper()
	var s sql.NullString
	if err := xe.db.QueryRow(`SELECT (SELECT state FROM operation WHERE plan = $1)`, xe.pid).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s.String
}

func (xe *execEnv) entries(t *testing.T, kind string) int {
	t.Helper()
	return count(t, xe.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = $2`, xe.machine, kind)
}

// wantNothingSent is the state of a dispatch that stopped before its attempt: no operation, no
// commitment or attempt, and no request at the node.
func (xe *execEnv) wantNothingSent(t *testing.T) {
	t.Helper()
	if n := len(xe.node.AppliedRequests()); n != 0 {
		t.Fatalf("%d requests reached the node", n)
	}
	if n := count(t, xe.db, `SELECT (SELECT count(*) FROM operation WHERE plan = $1) + (SELECT count(*) FROM attempt WHERE plan = $1)`,
		xe.pid); n != 0 {
		t.Fatalf("%d operations and attempts", n)
	}
}

// ER §3 end to end (AP S4 and §7.1): an approved apply-config plan's approval wakes the executor
// loop, which checks its use-time dependencies, observes the machine for the plan, commits,
// records the attempt, sends the artifact once in no-reboot mode, records the response, observes
// for completion and settles: the operation completes, Applied moves and the scope is released.
func TestExecutorCompletes(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := xe.executor(t, life, idle, options{})
	a.startExecutor()
	defer stopping(a, stop)
	waitIdle(t, idle, 1)
	xe.approveThrough(t, a)
	until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })

	got := xe.node.AppliedRequests()
	if len(got) != 1 || got[0].Mode != "NO_REBOOT" || got[0].DryRun || !bytes.Equal(got[0].Data, xe.artifact) {
		t.Fatalf("requests %+v", got)
	}
	after := talos.ConfigurationDigest(xe.artifact)
	if n := count(t, xe.db, `SELECT count(*) FROM machine_state WHERE machine = $1 AND applied_release = $2 AND applied_digest = $3
		AND applied_source = 'operation'`, xe.machine, xe.target.rel, after[:]); n != 1 {
		t.Fatal("Applied did not move to the release and the artifact's digest")
	}
	// The timeline: the use-time check precedes the evidence that precedes the commitment, which
	// precedes the attempt, the response and the completion observation the operation settled on.
	var rev [6]int64
	if err := xe.db.QueryRow(`SELECT
			(SELECT revision FROM machine_event WHERE machine = $1 AND kind = 'use-time-check'),
			(SELECT basis FROM observation o JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis
				WHERE o.machine = $1 AND s.purpose = 'evidence'),
			(SELECT revision FROM machine_event WHERE machine = $1 AND kind = 'commitment'),
			(SELECT revision FROM attempt WHERE plan = $2),
			(SELECT r.revision FROM attempt_response r JOIN attempt a ON a.id = r.attempt WHERE a.plan = $2),
			(SELECT o.basis FROM observation o JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis
				WHERE o.machine = $1 AND s.purpose = 'completion')`, xe.machine, xe.pid).Scan(&rev[0], &rev[1], &rev[2], &rev[3], &rev[4],
		&rev[5]); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(rev); i++ {
		if rev[i-1] >= rev[i] {
			t.Fatalf("timeline order %v", rev)
		}
	}
	// The check names the access version the evidence observation read and holds no value.
	var evidence int64
	if err := xe.db.QueryRow(`SELECT o.access_version FROM observation o JOIN observation_start s ON s.machine = o.machine
		AND s.revision = o.basis WHERE o.machine = $1 AND s.purpose = 'evidence'`, xe.machine).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	var entry string
	if err := xe.db.QueryRow(`SELECT entry::text FROM machine_event WHERE machine = $1 AND kind = 'use-time-check'`,
		xe.machine).Scan(&entry); err != nil {
		t.Fatal(err)
	}
	if n := count(t, xe.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'use-time-check'
		AND entry->>'result' = 'passed' AND (entry->'dependencies'->'access'->>'version')::bigint = $2
		AND entry->'dependencies'->'artifact'->>'key' = 'bw-artifact' AND entry->>'plan' = $3`, xe.machine, evidence, xe.pid); n != 1 {
		t.Fatalf("use-time check %s, evidence read access version %d", entry, evidence)
	}
	// PA §16 Talos access, executor: the read at dispatch is recorded as its path, version and
	// created_time, those the evidence observation recorded.
	if n := count(t, xe.db, `SELECT count(*) FROM machine_event e JOIN observation o ON o.machine = e.machine
			JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis AND s.purpose = 'evidence'
		WHERE e.machine = $1 AND e.kind = 'use-time-check' AND e.entry->'dependencies'->'access'->>'path' = o.access_path
			AND (e.entry->'dependencies'->'access'->>'created')::timestamptz = o.access_created::timestamptz`, xe.machine); n != 1 {
		t.Fatalf("use-time check %s names another access path or created_time than its evidence", entry)
	}
	if strings.Contains(entry, "hostname") || strings.Contains(entry, "cluster:") {
		t.Fatalf("the check holds a value: %s", entry)
	}
	// The check's window starts before its dependency reads, not at the time it is recorded.
	if n := count(t, xe.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'use-time-check'
		AND (entry->>'started')::timestamptz <= at`, xe.machine); n != 1 {
		t.Fatalf("use-time check %s names no start at or before its entry", entry)
	}
	// One decryption for the dispatch. Two certificates are signed, each a read of the act's key
	// version: one for the dispatch (its evidence observation, check and attempt) and one for the
	// completion observation, which records its own.
	xe.x.mu.Lock()
	defer xe.x.mu.Unlock()
	if kvReads := xe.keys.kv.readVersions(); xe.x.decrypts != 1 || !slices.Equal(kvReads, []int{xe.keyAct.Version, xe.keyAct.Version}) {
		t.Fatalf("%d decryptions, CA key reads of versions %v; want 1 and [%d %d]", xe.x.decrypts, kvReads, xe.keyAct.Version, xe.keyAct.Version)
	}
	if n := count(t, xe.db, `SELECT count(*) FROM observation o JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis
		WHERE o.machine = $1 AND s.purpose = 'completion' AND o.access_path = $2 AND o.access_version = $3
			AND o.access_created::timestamptz = $4 AND o.access_serial IS NOT NULL AND o.access_not_after IS NOT NULL`,
		xe.machine, xe.keyAct.Path, xe.keyAct.Version, xe.keyAct.CreatedTime); n != 1 {
		t.Fatalf("the completion observation does not record a certificate from %+v", xe.keyAct)
	}
	if n := count(t, xe.db, `SELECT count(*) FROM operation WHERE state IN ('committed', 'sending', 'verifying', 'unresolved')`); n != 0 {
		t.Fatalf("%d operations hold the scope", n)
	}
}

// ER §3.1 item 2: a dependency the dispatch itself needs that cannot be used stops it before
// anything is committed or sent, and records the check with its failed result and cause; once the
// dependency is back the same plan is dispatched and completes. The artifact key is classified
// `retained` throughout: a retained class admits no dispatch (DM §8, §10.1 item 8).
func TestExecutorUseTimeCheckFailure(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, cause string
		fail        func(*testing.T, *execEnv)
		heal        func(*testing.T, *execEnv)
	}{
		{"sealed provider", "provider-unavailable", func(_ *testing.T, xe *execEnv) { xe.x.decryptErr = fmt.Errorf("sealed: %w", provider.ErrUnavailable) },
			func(_ *testing.T, xe *execEnv) { xe.x.decryptErr = nil }},
		{"another key", "key-mismatch", func(_ *testing.T, xe *execEnv) { xe.x.keyName = "other-key" }, func(_ *testing.T, xe *execEnv) { xe.x.keyName = "" }},
		{"unreadable key", "denied", kvStatus(http.StatusForbidden), kvStatus(0)},
		{"absent key act", "absent", func(t *testing.T, xe *execEnv) { dropFirstKeyActs(t, xe.db, xe.cluster) },
			func(t *testing.T, xe *execEnv) { xe.keys.seed(t, xe.db, xe.cluster) }},
		{"the named version is gone", "version-gone", func(_ *testing.T, xe *execEnv) { xe.keys.kv.destroy(xe.keyAct.Path, xe.keyAct.Version) },
			func(t *testing.T, xe *execEnv) { xe.keys.seed(t, xe.db, xe.cluster) }},
		{"malformed key answer", "malformed", func(_ *testing.T, xe *execEnv) { xe.keys.kv.setGarble(true) },
			func(_ *testing.T, xe *execEnv) { xe.keys.kv.setGarble(false) }},
		{"sealed key", "provider-unavailable", kvStatus(http.StatusServiceUnavailable), kvStatus(0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			xe := newExecEnv(t)
			if n := count(t, xe.db, `SELECT count(*) FROM dependency_status WHERE provider = 'transit' AND object = $1
				AND class = 'retained'`, artifactKeyName); n < 1 {
				t.Fatal("the artifact key is not classified retained")
			}
			tc.fail(t, xe)
			beforeObservations := count(t, xe.db, `SELECT count(*) FROM observation_start`)
			life, stop := context.WithCancel(t.Context())
			idle := make(chan struct{})
			a := xe.executor(t, life, idle, options{})
			a.startExecutor()
			defer stopping(a, stop)
			waitIdle(t, idle, 1)
			xe.approveThrough(t, a)
			until(t, a, idle, "a failed use-time check", func() bool { return xe.entries(t, "use-time-check") > 0 })
			xe.wantNothingSent(t)
			if n := count(t, xe.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'use-time-check'
				AND entry->>'result' = 'failed' AND entry::text LIKE '%' || $2 || '%'`, xe.machine, tc.cause); n < 1 {
				t.Fatalf("no failed check naming %q", tc.cause)
			}
			if n := count(t, xe.db, `SELECT count(*) FROM observation_start`); n != beforeObservations || xe.entries(t, "commitment") != 0 {
				t.Fatalf("%d observations or a commitment after a failed check", n)
			}
			xe.x.mu.Lock()
			tc.heal(t, xe)
			xe.x.mu.Unlock()
			until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
			if n := len(xe.node.AppliedRequests()); n != 1 {
				t.Fatalf("%d requests", n)
			}
		})
	}
}

// kvStatus forces the status of every read of the CA key the dispatch's certificate needs, 0 for none.
func kvStatus(code int) func(*testing.T, *execEnv) {
	return func(_ *testing.T, xe *execEnv) {
		xe.keys.kv.mu.Lock()
		defer xe.keys.kv.mu.Unlock()
		xe.keys.kv.status["GET"] = code
	}
}

// setGarble makes every read answer 200 with a body that is no KV answer.
func (k *kvStandIn) setGarble(on bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.garble = on
}

// ER §9.2 and PA §3.3, against a stubbed Talos node: a commitment is refused by comparison 3, nothing
// sent, when the evidence shows an SMBIOS UUID for a machine recorded by node ID or another node ID
// for one recorded by it; a failed systeminformation read leaves the identity unread, which is no
// evidence (choice §10.28): nothing is committed or sent. The controls admit a machine recorded by
// node ID whose systeminformation read returns no resource, and one recorded by SMBIOS UUID showing
// another node ID with its UUID and cluster ID.
func TestExecutorCommitmentIdentityKeys(t *testing.T) {
	t.Parallel()
	byNodeID := func(t *testing.T, xe *execEnv) {
		mustExec(t, xe.db, `ALTER TABLE machine DISABLE TRIGGER identity`)
		mustExec(t, xe.db, `UPDATE machine SET smbios_uuid = NULL, talos_node_id = 'node-a' WHERE id = $1`, xe.machine)
		mustExec(t, xe.db, `ALTER TABLE machine ENABLE TRIGGER identity`)
		xe.node.SetSMBIOSUUID(t, "", false)
		xe.node.SetNodeID(t, "node-a")
	}
	for name, c := range map[string]struct {
		set      func(t *testing.T, xe *execEnv)
		admitted bool
	}{
		"recorded by node ID, no systeminformation resource (control)": {byNodeID, true},
		"recorded by SMBIOS UUID, another node ID (control)": {func(t *testing.T, xe *execEnv) {
			xe.node.SetNodeID(t, "node-other")
		}, true},
		"recorded by SMBIOS UUID, another SMBIOS UUID shown": {func(t *testing.T, xe *execEnv) {
			xe.node.SetSMBIOSUUID(t, "1b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b", true)
		}, false},
		"recorded by SMBIOS UUID, no systeminformation resource": {func(t *testing.T, xe *execEnv) {
			xe.node.SetSMBIOSUUID(t, "", false)
		}, false},
		"recorded by node ID, an SMBIOS UUID shown": {func(t *testing.T, xe *execEnv) {
			byNodeID(t, xe)
			xe.node.SetSMBIOSUUID(t, "1b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b", true)
		}, false},
		"recorded by node ID, another node ID": {func(t *testing.T, xe *execEnv) {
			byNodeID(t, xe)
			xe.node.SetNodeID(t, "node-z")
		}, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			xe := newExecEnv(t)
			c.set(t, xe)
			life, stop := context.WithCancel(t.Context())
			idle := make(chan struct{})
			a := xe.executor(t, life, idle, options{nodeTimeout: 500 * time.Millisecond})
			a.startExecutor()
			defer stopping(a, stop)
			waitIdle(t, idle, 1)
			xe.approveThrough(t, a)
			if c.admitted {
				until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
				return
			}
			until(t, a, idle, "a refused commitment", func() bool {
				return count(t, xe.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'refusal'
					AND entry->>'comparison' = '3'`, xe.machine) > 0
			})
			xe.wantNothingSent(t)
		})
	}
	t.Run("a failed systeminformation read", func(t *testing.T) {
		t.Parallel()
		xe := newExecEnv(t)
		xe.node.Fail(talostest.SystemInformationType, codes.Internal)
		life, stop := context.WithCancel(t.Context())
		idle := make(chan struct{})
		a := xe.executor(t, life, idle, options{nodeTimeout: 500 * time.Millisecond})
		a.startExecutor()
		defer stopping(a, stop)
		waitIdle(t, idle, 1)
		xe.approveThrough(t, a)
		until(t, a, idle, "evidence with the identity unread", func() bool {
			return count(t, xe.db, `SELECT count(*) FROM observation o JOIN observation_start s ON s.machine = o.machine
				AND s.revision = o.basis WHERE o.machine = $1 AND s.purpose = 'evidence' AND o.unread ? 'identity'`, xe.machine) > 0
		})
		passEnds(t, a, idle)
		xe.wantNothingSent(t)
		if xe.entries(t, "commitment") != 0 {
			t.Fatal("a commitment on evidence with the identity unread")
		}
	})
}

// PA §3.3 and §16 Talos access, executor: a node-side cause (a certificate Talos refuses, signed from
// a CA the node does not trust) fails the dispatch's fresh observation, which records its cause; no
// operation is committed and nothing is sent, and once the key is repaired the plan completes.
func TestExecutorNodeSideAccessFailure(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	good := xe.keys.ca
	xe.keys.ca = newTalosCA(t) // the act names a key the node does not trust
	xe.keys.seed(t, xe.db, xe.cluster)
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := xe.executor(t, life, idle, options{nodeTimeout: 500 * time.Millisecond})
	a.startExecutor()
	defer stopping(a, stop)
	waitIdle(t, idle, 1)
	xe.approveThrough(t, a)
	until(t, a, idle, "a failed evidence observation", func() bool {
		return count(t, xe.db, `SELECT count(*) FROM observation o JOIN observation_start s ON s.machine = o.machine
			AND s.revision = o.basis WHERE o.machine = $1 AND s.purpose = 'evidence' AND o.unread::text LIKE '%node-unreachable%'`,
			xe.machine) > 0
	})
	passEnds(t, a, idle)
	xe.wantNothingSent(t)
	if xe.entries(t, "commitment") != 0 {
		t.Fatal("a commitment after a failed observation")
	}
	xe.keys.ca = good
	xe.keys.seed(t, xe.db, xe.cluster)
	until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
}

// refusingSigner is a Signer whose named-version read fails with err, as a provider answer that carried
// text of its own would.
type refusingSigner struct {
	Signer
	err error
}

func (s refusingSigner) ReadCAKeyVersion(context.Context, string, int) (provider.CAKey, error) {
	return provider.CAKey{}, s.err
}

// PA §16 Talos access, executor: a failed access read at dispatch records its cause, never the
// provider's error text, in the timeline or the log; the control logs that text.
func TestExecutorAccessErrorTextControl(t *testing.T) {
	t.Parallel()
	const canary = "bw-access-error-canary-7q4z"
	for _, quote := range []bool{false, true} {
		t.Run(fmt.Sprint("quoteErrors=", quote), func(t *testing.T) {
			t.Parallel()
			xe := newExecEnv(t)
			xe.env.d.sg = refusingSigner{Signer: xe.keys.sg, err: fmt.Errorf("%s: %w", canary, provider.ErrDenied)}
			var mu sync.Mutex
			var logged strings.Builder
			logf := func(format string, args ...any) {
				mu.Lock()
				defer mu.Unlock()
				fmt.Fprintf(&logged, format+"\n", args...)
			}
			life, stop := context.WithCancel(t.Context())
			idle := make(chan struct{})
			a := xe.executor(t, life, idle, options{logf: logf, quoteErrors: quote})
			a.startExecutor()
			defer stopping(a, stop)
			waitIdle(t, idle, 1)
			xe.approveThrough(t, a)
			until(t, a, idle, "a failed use-time check", func() bool { return xe.entries(t, "use-time-check") > 0 })
			// The entry commits before the pass logs the failure; the log is read once that pass ends.
			passEnds(t, a, idle)
			xe.wantNothingSent(t)
			if n := count(t, xe.db, `SELECT count(*) FROM machine_event WHERE entry::text LIKE '%' || $1 || '%'`, canary); n != 0 {
				t.Fatalf("%d timeline entries hold the error text", n)
			}
			mu.Lock()
			defer mu.Unlock()
			if got := strings.Contains(logged.String(), canary); got != quote {
				t.Fatalf("logged the error text %v with quoteErrors %v", got, quote)
			}
		})
	}
}

// ER §3.3: an attempt whose transaction failed was never recorded, so nothing is sent for it; the
// operation stays committed under its owner and the next pass records it and sends once. A wake
// left pending by the approval or by until starts that next pass at once, so the commit holding an
// attempt waits, before it commits, until the state between the two passes has been checked.
func TestExecutorAttemptNotRecordedSendsNothing(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	var armed atomic.Bool
	armed.Store(true)
	checked := make(chan struct{})
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := xe.executor(t, life, idle, options{commit: func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM attempt`).Scan(&n); err == nil && n > 0 {
			if armed.CompareAndSwap(true, false) {
				_ = tx.Rollback()
				return errors.New("the attempt's commit failed")
			}
			select {
			case <-checked:
			case <-life.Done():
			}
		}
		return tx.Commit()
	}})
	a.startExecutor()
	defer stopping(a, stop)
	waitIdle(t, idle, 1)
	xe.approveThrough(t, a)
	until(t, a, idle, "the attempt's commit to fail", func() bool { return !armed.Load() })
	if s := xe.opState(t); s != "committed" {
		t.Fatalf("operation %s", s)
	}
	if n := len(xe.node.AppliedRequests()); n != 0 || count(t, xe.db, `SELECT count(*) FROM attempt`) != 0 {
		t.Fatalf("%d requests after an attempt that was not recorded", n)
	}
	close(checked)
	until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
	if n := len(xe.node.AppliedRequests()); n != 1 || count(t, xe.db, `SELECT count(*) FROM attempt WHERE number = 1`) != 1 {
		t.Fatalf("%d requests", n)
	}
}

// ER §3.2 and §3.3 comparison 3, PA §3.3: an attempt made again after its transaction failed signs
// a new certificate, which names another serial than the commitment's evidence read through, whether
// or not the CA key moved (a replaced key is another version of it). Comparison 3 refuses the
// attempt, the operation is observed and classified again, and the one request is sent on new
// evidence recorded for the operation, signed with the certificate it is sent with.
func TestExecutorAttemptAfterCertificateRenewal(t *testing.T) {
	t.Parallel()
	for name, replaceKey := range map[string]bool{"the key unchanged": false, "the key replaced": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			xe := newExecEnv(t)
			var armed atomic.Bool
			armed.Store(true)
			life, stop := context.WithCancel(t.Context())
			idle := make(chan struct{})
			a := xe.executor(t, life, idle, options{commit: func(tx *sql.Tx) error {
				var n int
				if err := tx.QueryRow(`SELECT count(*) FROM attempt`).Scan(&n); err == nil && n > 0 && armed.CompareAndSwap(true, false) {
					_ = tx.Rollback()
					if replaceKey {
						xe.keys.seed(t, xe.db, xe.cluster)
					}
					return errors.New("the attempt's commit failed")
				}
				return tx.Commit()
			}})
			a.startExecutor()
			defer stopping(a, stop)
			waitIdle(t, idle, 1)
			xe.approveThrough(t, a)
			until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
			if n := len(xe.node.AppliedRequests()); n != 1 {
				t.Fatalf("%d requests", n)
			}
			if n := count(t, xe.db, `SELECT count(*) FROM machine_event WHERE kind = 'refusal' AND entry->>'comparison' = '3'
				AND entry->>'cause' = 'the use-time check used another certificate than the evidence'`); n != 1 {
				t.Fatalf("%d comparison 3 refusals for the renewed certificate", n)
			}
			// The retry's evidence: an evidence observation recorded for the operation, signed from the key
			// the act names now.
			latest := latestActVersion(t, xe.db, xe.cluster)
			if n := count(t, xe.db, `SELECT count(*) FROM observation o JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis
				WHERE s.purpose = 'evidence' AND s.operation IS NOT NULL AND o.access_path = $1 AND o.access_version = $2`,
				latest.Path, latest.Version); n != 1 {
				t.Fatalf("%d evidence observations recorded for the operation read through the latest key version", n)
			}
		})
	}
}

// A dispatch that commits the plan and then fails short of a refusal at its attempt waits a lease
// before it is tried again, and the committed operation waits with it: the delay is stored under
// the plan, and the loop's read of the operation honours it. Within a lease of one hour the loop
// passes ten more times on a 20 ms heartbeat without the operation being advanced, so no new
// certificate is signed for it and comparison 3 refuses nothing; with a lease of 50 ms the
// operation is advanced after it, comparison 3 refuses the certificate signed again once, and the
// operation completes.
func TestExecutorRetryDelayOfCommittedOperation(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		lease time.Duration
		waits bool // the operation is not advanced within the passes
	}{"a lease of one hour": {time.Hour, true}, "a lease of 50 ms": {50 * time.Millisecond, false}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			xe := newExecEnv(t)
			var armed atomic.Bool
			armed.Store(true)
			life, stop := context.WithCancel(t.Context())
			idle := make(chan struct{})
			a := xe.executor(t, life, idle, options{commit: func(tx *sql.Tx) error {
				var n int
				if err := tx.QueryRow(`SELECT count(*) FROM attempt`).Scan(&n); err == nil && n > 0 && armed.CompareAndSwap(true, false) {
					_ = tx.Rollback()
					return errors.New("the attempt's commit failed")
				}
				return tx.Commit()
			}})
			a.d.timers.Heartbeat, a.d.timers.Lease = 20*time.Millisecond, c.lease
			a.startExecutor()
			defer stopping(a, stop)
			waitIdle(t, idle, 1)
			xe.approveThrough(t, a)
			until(t, a, idle, "the plan is committed", func() bool { return xe.entries(t, "commitment") == 1 })
			if !c.waits {
				// The heartbeat, not a wake, carries the loop past the lease: wait by the clock.
				for deadline := time.Now().Add(30 * time.Second); xe.opState(t) != "completed"; waitIdle(t, idle, 1) {
					if time.Now().After(deadline) {
						t.Fatalf("the operation is %s 30 s after the lease; want completed", xe.opState(t))
					}
				}
				if n := count(t, xe.db, `SELECT count(*) FROM machine_event WHERE kind = 'refusal' AND entry->>'comparison' = '3'
					AND entry->>'cause' = 'the use-time check used another certificate than the evidence'`); n != 1 {
					t.Fatalf("%d comparison 3 refusals for the renewed certificate; want 1", n)
				}
				return
			}
			waitIdle(t, idle, 10) // ten more passes of the loop, a heartbeat apart, with the lease still running
			if s := xe.opState(t); s != "committed" {
				t.Errorf("the operation is %s; want committed, not advanced within the lease", s)
			}
			if n := count(t, xe.db, `SELECT count(*) FROM machine_event WHERE kind = 'use-time-check' AND entry->>'operation' IS NOT NULL`); n != 0 {
				t.Errorf("%d use-time checks recorded for the operation within the lease; want none", n)
			}
			if n := count(t, xe.db, `SELECT count(*) FROM machine_event WHERE kind = 'refusal'`); n != 0 {
				t.Errorf("%d refusals recorded within the lease; want none", n)
			}
		})
	}
}

// Where revokeAtCommitment places the approval's revocation.
const (
	// revokeAfter revokes once the commitment has committed (DS row 004).
	revokeAfter = iota
	// revokeHeld starts the revocation while the commitment holds its locks and commits once it waits
	// on the machine row (DS row 003). Which of the revocation and the attempt then takes the row first
	// is PostgreSQL's choice: the commitment updated the row, so the waiting revocation locks its new
	// version afresh, as the attempt does (choice §10.31).
	revokeHeld
	// revokeAttemptFirst starts the revocation while the commitment holds its locks, but holds it at
	// its key's lock, its first (§7.2), until the attempt holds the machine row: row 003's other order,
	// every time.
	revokeAttemptFirst
)

// revokeAtCommitment runs the executor on the plan and revokes its approval at the commitment, at
// the place at names. With control, the attempt transaction's comparison 1 is skipped. Checked, a
// revocation that takes the machine row before the attempt refuses it: the operation is cancelled
// and nothing reaches the node. An attempt that takes the row first is recorded before the
// revocation, as one recorded before a revocation is (DS row 005), and the revocation waits for it.
func revokeAtCommitment(t *testing.T, at int, control bool) {
	xe := newExecEnv(t)
	token, k := xe.human("h-recovery"), xe.key()
	var gate *sql.Tx
	if at == revokeAttemptFirst {
		var err error
		if gate, err = xe.db.BeginTx(t.Context(), nil); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = gate.Rollback() }()
		mustExec(t, gate, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, xe.principalOf("h-recovery")+"/"+k)
	}
	var armed, attempting atomic.Bool
	armed.Store(true)
	attempting.Store(at == revokeAttemptFirst)
	revoked := make(chan int, 1)
	waited := make(chan error, 2)
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := xe.executor(t, life, idle, options{noPlanCheck: control, commit: func(tx *sql.Tx) error {
		var ops, attempts int
		var approval sql.NullString
		if err := tx.QueryRow(`SELECT (SELECT count(*) FROM operation WHERE plan = $1), (SELECT count(*) FROM attempt WHERE plan = $1),
			(SELECT approval FROM plan_state WHERE plan = $1)`, xe.pid).Scan(&ops, &attempts, &approval); err != nil || ops == 0 {
			return tx.Commit()
		}
		if attempts == 1 && attempting.CompareAndSwap(true, false) {
			// The attempt holds the machine row: the revocation takes its key and waits on the row.
			if err := gate.Commit(); err != nil {
				waited <- err
			} else {
				waited <- revocationWaits(xe.db, machineLock)
			}
			return tx.Commit()
		}
		if attempts != 0 || !armed.CompareAndSwap(true, false) {
			return tx.Commit()
		}
		revoke := func() { revoked <- xe.revokeApproval(token, k, approval.String, `{"reason":"wrong"}`).Code }
		switch at {
		case revokeHeld:
			go revoke()
			waited <- revocationWaits(xe.db, machineLock)
			return tx.Commit()
		case revokeAttemptFirst:
			go revoke()
			waited <- revocationWaits(xe.db, keyLock)
			return tx.Commit()
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		revoke()
		return nil
	}})
	a.startExecutor()
	defer stopping(a, stop)
	waitIdle(t, idle, 1)
	xe.approveThrough(t, a)
	switch {
	case control || at == revokeAttemptFirst:
		until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
	case at == revokeHeld:
		until(t, a, idle, "the operation is cancelled or completed", func() bool {
			s := xe.opState(t)
			return s == "cancelled" || s == "completed"
		})
	default:
		until(t, a, idle, "the operation is cancelled", func() bool { return xe.opState(t) == "cancelled" })
	}
	waits := map[int]int{revokeAfter: 0, revokeHeld: 1, revokeAttemptFirst: 2} // the commit hook's reports
	for range waits[at] {
		if err := <-waited; err != nil {
			t.Fatal(err)
		}
	}
	if code := <-revoked; code != http.StatusCreated {
		t.Fatalf("revocation %d", code)
	}
	if n := count(t, xe.db, `SELECT count(*) FROM machine_event c JOIN machine_event v ON v.machine = c.machine
		AND v.kind = 'approval-revocation' AND v.revision > c.revision WHERE c.machine = $1 AND c.kind = 'commitment'`, xe.machine); n != 1 {
		t.Fatal("the revocation is not recorded after the commitment")
	}
	if !control && at != revokeAfter && xe.opState(t) == "completed" {
		xe.wantAttemptBeforeRevocation(t)
		return
	}
	if control {
		got := xe.node.AppliedRequests()
		if len(got) != 1 {
			t.Fatalf("control: %d requests", len(got))
		}
		if got[0].Mode != "NO_REBOOT" || got[0].DryRun || !bytes.Equal(got[0].Data, xe.artifact) {
			t.Fatalf("control: request %+v", got[0])
		}
		if n := count(t, xe.db, `SELECT count(*) FROM attempt a JOIN machine_event v ON v.machine = a.machine
			AND v.kind = 'approval-revocation' AND v.revision < a.revision WHERE a.plan = $1`, xe.pid); n != 1 {
			t.Fatal("control: the attempt is not recorded after the revocation")
		}
		return
	}
	if n := len(xe.node.AppliedRequests()); n != 0 || count(t, xe.db, `SELECT count(*) FROM attempt WHERE plan = $1`, xe.pid) != 0 {
		t.Fatalf("%d requests after the revocation", n)
	}
	// Comparison 1 refused it, and that refusal moved it committed, unresolved, cancelled.
	var states sql.NullString
	if err := xe.db.QueryRow(`SELECT string_agg(e.entry->>'to', ',' ORDER BY e.revision) FROM machine_event r
		JOIN machine_event e ON e.machine = r.machine AND e.revision > r.revision AND e.kind = 'operation-state'
			AND e.entry->>'comparison' = '1' AND e.entry->>'cause' = 'the approval is revoked'
			AND e.entry->>'operation' = r.entry->>'operation'
		WHERE r.machine = $1 AND r.kind = 'refusal' AND r.entry->>'transaction' = 'T6' AND r.entry->>'comparison' = '1'
			AND r.entry->>'cause' = 'the approval is revoked' AND r.entry->>'plan' = $2
			AND r.entry->>'operation' = (SELECT id FROM operation WHERE plan = $2)`, xe.machine, xe.pid).Scan(&states); err != nil ||
		states.String != "unresolved,cancelled" {
		t.Fatalf("states after comparison 1's refusal %q: %v", states.String, err)
	}
}

// The approval revocation's statements that revocationWaits recognises: its key's lock (§7.2) and
// the machine row, T5b's first lock.
const (
	keyLock     = `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`
	machineLock = `SELECT 1 FROM machine WHERE id = $1 FOR UPDATE`
)

// revocationWaits is nil once the approval revocation waits on the lock that stmt takes, in this
// test's own database. It reports, not fails: it runs on the executor's goroutine.
func revocationWaits(db *sql.DB, stmt string) error {
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()
			AND wait_event_type = 'Lock' AND query = $1`, stmt).Scan(&n); err != nil {
			return err
		}
		if n == 1 {
			return nil
		}
	}
	return fmt.Errorf("the revocation did not wait on %q within 10s", stmt)
}

// wantAttemptBeforeRevocation checks an attempt that took the machine row before the revocation:
// it was recorded before the revocation, which waited for it and was recorded too, and its one
// request reached the node. No attempt is recorded after the revocation, so nothing is sent on one.
func (xe *execEnv) wantAttemptBeforeRevocation(t *testing.T) {
	t.Helper()
	if n := count(t, xe.db, `SELECT count(*) FROM attempt a JOIN machine_event v ON v.machine = a.machine
		AND v.kind = 'approval-revocation' AND v.revision > a.revision WHERE a.plan = $1`, xe.pid); n != 1 ||
		count(t, xe.db, `SELECT count(*) FROM attempt WHERE plan = $1`, xe.pid) != 1 {
		t.Fatal("the attempt is not the plan's one attempt, recorded before the revocation")
	}
	if n := count(t, xe.db, `SELECT count(*) FROM approval_revocation WHERE approval = $1`, xe.approval); n != 1 {
		t.Fatalf("%d revocations of the approval", n)
	}
	if n := count(t, xe.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'refusal'`, xe.machine); n != 0 {
		t.Fatalf("%d refusals of an attempt recorded before the revocation", n)
	}
	got := xe.node.AppliedRequests()
	if len(got) != 1 || got[0].Mode != "NO_REBOOT" || got[0].DryRun || !bytes.Equal(got[0].Data, xe.artifact) {
		t.Fatalf("requests %+v", got)
	}
}

// DS row 003 through the executor and a Talos node: a revocation started while the commitment holds
// its locks waits for it. Either the attempt transaction's comparison 1 then refuses the attempt, or
// the attempt takes the machine row first and the revocation waits for it too (choice §10.31).
// attempt-first forces the second order; held leaves the order to PostgreSQL.
func TestExecutorRevocationHeld(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		at   int
	}{{"held", revokeHeld}, {"attempt-first", revokeAttemptFirst}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			revokeAtCommitment(t, c.at, false)
		})
	}
}

// DS rows 004 and 006: the approval revoked after the commitment, before the attempt, refused by
// the attempt's comparison 1. The control skips it, as a controller relying on its early check alone
// would: the artifact is sent after the revocation. Row 006's revocation before the commitment
// cannot reach a send here: the plan state refuses committing a revoked plan (choice §10.31).
func TestExecutorRevokedControl(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		control bool
	}{{"checked", false}, {"control", true}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			revokeAtCommitment(t, revokeAfter, c.control)
		})
	}
}

// ER §3.2 comparison 3: a use-time check older than the plan's validity window at the commitment is
// no check: nothing is committed or sent, and a new check, once the window holds, is dispatched.
func TestExecutorCheckValidityBoundsDispatch(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	setCheckValidity := func(d string) {
		mustExec(t, xe.db, `ALTER TABLE plan DISABLE TRIGGER immutable_rows`)
		mustExec(t, xe.db, `UPDATE plan SET check_validity = $2::interval WHERE id = $1`, xe.pid, d)
		mustExec(t, xe.db, `ALTER TABLE plan ENABLE TRIGGER immutable_rows`)
	}
	setCheckValidity("1 millisecond")
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := xe.executor(t, life, idle, options{})
	a.startExecutor()
	defer stopping(a, stop)
	waitIdle(t, idle, 1)
	xe.approveThrough(t, a)
	until(t, a, idle, "three checks that expired before the commitment", func() bool { return xe.entries(t, "use-time-check") >= 3 })
	xe.wantNothingSent(t)
	// The window that holds stays within what a certificate can be signed for (a plan with a longer one is
	// refused at creation): the dispatch certificate's life includes it, and the deployment's maximum is 15 minutes.
	setCheckValidity("5 minutes")
	until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
}

// checkedAccess is a seeded use-time check's dependencies: the certificate the evidence observation o
// read through, so that comparison 3's certificate terms pass and the check's time alone decides.
const checkedAccess = `jsonb_build_object('access', jsonb_build_object('path', o.access_path,
	'version', o.access_version, 'created', o.access_created, 'serial', o.access_serial,
	'notAfter', o.access_not_after))`

// ER §3.3: an attempt whose use-time check is older than the plan's validity window at its time is
// refused by comparison 3 and sends nothing.
func TestAttemptRefusesAnExpiredUseTimeCheck(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	var rev int64
	if err := ae.db.QueryRow(`UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1 RETURNING revision_counter`,
		ae.machine).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	mustExec(t, ae.db, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		SELECT $1, $2, epoch, 'use-time-check', jsonb_build_object('plan', $3::text, 'controller', $4::text, 'result', 'passed',
			'dependencies', `+checkedAccess+`), now() - interval '1 hour'
		FROM installation_state, observation o WHERE o.id = $5`, ae.machine, rev, ae.pid, ae.a.d.owner.ID, ae.obs)
	_, err := ae.a.recordAttempt(context.Background(), ae.op, 1)
	ae.wantAttemptRefused(t, err, "3", "the use-time check is older than the plan allows", ae.obs, "committed",
		settled{"unresolved", "3", "the use-time check is older than the plan allows"})
}

// access is a seeded use-time check's access dependency: the evidence observation o's certificate, with
// the serial and the NotAfter replaced by the SQL expressions given (empty keeps the observation's).
func access(serial, notAfter string) string {
	if serial == "" {
		serial = "o.access_serial"
	}
	if notAfter == "" {
		notAfter = "o.access_not_after"
	}
	return `jsonb_build_object('access', jsonb_build_object('path', o.access_path, 'version', o.access_version,
		'created', o.access_created, 'serial', ` + serial + `, 'notAfter', ` + notAfter + `))`
}

// ER §3.2 comparison 3, #899: the attempt is sent with the check's certificate, so the check must name
// the certificate the evidence read through (its serial, beside the key's path, version and
// created_time), and a certificate past its NotAfter is not sent with. Each control differs from its
// case in the one term alone: the same check with the observation's own serial and NotAfter is accepted,
// so the refusal is that term's and no other's.
func TestAttemptComparesTheCertificate(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		deps  string
		cause string // empty: the attempt is accepted
	}{
		"the evidence's own certificate":     {access("", ""), ""},
		"another serial, the same key":       {access("'ffff'", ""), "the use-time check used another certificate than the evidence"},
		"a NotAfter in the future":           {access("", "now() + interval '1 hour'"), ""},
		"a certificate past its NotAfter":    {access("", "now() - interval '1 second'"), "the certificate passed its NotAfter"},
		"the NotAfter of another serial too": {access("'ffff'", "now() - interval '1 second'"), "the use-time check used another certificate than the evidence"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ae := newAttemptEnv(t, "")
			var rev int64
			if err := ae.db.QueryRow(`UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1 RETURNING revision_counter`,
				ae.machine).Scan(&rev); err != nil {
				t.Fatal(err)
			}
			mustExec(t, ae.db, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
				SELECT $1, $2, epoch, 'use-time-check', jsonb_build_object('plan', $3::text, 'controller', $4::text, 'result', 'passed',
					'dependencies', `+c.deps+`), now()
				FROM installation_state, observation o WHERE o.id = $5`, ae.machine, rev, ae.pid, ae.a.d.owner.ID, ae.obs)
			_, err := ae.a.recordAttempt(context.Background(), ae.op, 1)
			if c.cause == "" {
				if err != nil {
					t.Fatalf("the control was refused: %v", err)
				}
				return
			}
			ae.wantAttemptRefused(t, err, "3", c.cause, ae.obs, "committed", settled{"unresolved", "3", c.cause})
		})
	}
}

// ER §3.1 item 2: the check's validity runs from before its dependency reads, so a check recorded
// late, after slow reads, does not make the earlier values fresh.
func TestAttemptMeasuresCheckValidityFromBeforeItsReads(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	var rev int64
	if err := ae.db.QueryRow(`UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1 RETURNING revision_counter`,
		ae.machine).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	mustExec(t, ae.db, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		SELECT $1, $2, epoch, 'use-time-check', jsonb_build_object('plan', $3::text, 'controller', $4::text, 'result', 'passed',
			'started', to_char(now() - interval '1 hour', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'), 'dependencies', `+checkedAccess+`), now()
		FROM installation_state, observation o WHERE o.id = $5`, ae.machine, rev, ae.pid, ae.a.d.owner.ID, ae.obs)
	_, err := ae.a.recordAttempt(context.Background(), ae.op, 1)
	ae.wantAttemptRefused(t, err, "3", "the use-time check is older than the plan allows", ae.obs, "committed",
		settled{"unresolved", "3", "the use-time check is older than the plan allows"})
}

// ER §3.4 and §4: an operation whose attempt another controller recorded and whose response it
// recorded as accepted is taken over at start, unresolved, and a completion observation of the
// artifact completes it: nothing is sent again.
func TestExecutorCompletesATakenOverAcceptedAttempt(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	xe.ready(t)
	cm, err := xe.a.commitPlan(context.Background(), xe.pid)
	if err != nil {
		t.Fatal(err)
	}
	at, err := xe.a.recordAttempt(context.Background(), cm.Operation, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := xe.a.recordResponse(context.Background(), at.Attempt, answer(talos.ClassAccepted)); err != nil {
		t.Fatal(err)
	}
	xe.node.SetMachineConfig(t, xe.artifact)
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := xe.executor(t, life, idle, options{})
	a.startExecutor()
	defer stopping(a, stop)
	until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
	if n := len(xe.node.AppliedRequests()); n != 0 {
		t.Fatalf("%d requests after an attempt this process did not make", n)
	}
}

// DS row 016: the database lost while the node's response is recorded. The response is held in
// process and its record retried after a lease until it commits, so the one request completes the
// operation; a COMMIT that succeeded after its reply was lost is found recorded on the retry, and
// the loop carries the operation on. A statement on a connection pgx already closed fails with its
// own error, not a network one: the live run met that one. The control is the same plan with no loss.
func TestExecutorRetriesALostResponseRecord(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		losses    int32
		committed bool // the lost COMMIT did commit
		closed    bool // the loss is pgx's error for a closed connection
	}{
		"lost before the commit":      {losses: 2},
		"lost after the commit":       {losses: 1, committed: true},
		"lost on a closed connection": {losses: 1, closed: true},
		"control":                     {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			xe := newExecEnv(t)
			lost := fmt.Errorf("the connection closed: %w", driver.ErrBadConn)
			if c.closed {
				lost = closedConnErr(t)
			}
			var left atomic.Int32
			left.Store(c.losses)
			life, stop := context.WithCancel(t.Context())
			idle := make(chan struct{})
			a := xe.executor(t, life, idle, options{commit: func(tx *sql.Tx) error {
				var response bool
				if err := tx.QueryRow(`SELECT EXISTS (SELECT FROM attempt_response)`).Scan(&response); err != nil || !response ||
					left.Add(-1) < 0 {
					return tx.Commit()
				}
				if c.committed {
					if err := tx.Commit(); err != nil {
						return err
					}
				} else {
					_ = tx.Rollback()
				}
				return lost
			}})
			a.startExecutor()
			defer stopping(a, stop)
			waitIdle(t, idle, 1)
			xe.approveThrough(t, a)
			until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
			if n := len(xe.node.AppliedRequests()); n != 1 {
				t.Fatalf("%d requests reached the node", n)
			}
			if n := count(t, xe.db, `SELECT count(*) FROM attempt_response r JOIN attempt a ON a.id = r.attempt WHERE a.plan = $1`,
				xe.pid); n != 1 {
				t.Fatalf("%d responses recorded", n)
			}
			if retried := xe.logged("not recorded"); retried != (c.losses > 0) || left.Load() > 0 {
				t.Fatalf("retried %v, %d losses left", retried, left.Load())
			}
		})
	}
}

// closedConnErr is the error pgx returns for a statement on a connection it already closed.
func closedConnErr(t *testing.T) error {
	t.Helper()
	c, err := pgconn.Connect(t.Context(), os.Getenv("BW_TEST_PG_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err = c.Exec(t.Context(), "SELECT 1").ReadAll()
	if err == nil {
		t.Fatal("a closed connection ran a statement")
	}
	return err
}

// ER §3.4: a takeover that failed at start is retried after the lease, from the generation the
// loop read, and the operation is then carried to its outcome.
func TestExecutorRetriesAFailedStartupTakeover(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	xe.ready(t)
	if _, err := xe.a.commitPlan(context.Background(), xe.pid); err != nil {
		t.Fatal(err)
	}
	var armed atomic.Bool
	armed.Store(true)
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := xe.executor(t, life, idle, options{commit: func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM machine_event WHERE kind = 'ownership-transition'`).Scan(&n); err == nil && n > 0 &&
			armed.CompareAndSwap(true, false) {
			_ = tx.Rollback()
			return errors.New("the takeover's commit failed")
		}
		return tx.Commit()
	}})
	a.startExecutor()
	defer stopping(a, stop)
	until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
	if armed.Load() {
		t.Fatal("no takeover failed")
	}
}

// ER §3.3: a retry's evidence is the classification's observation, bound by the plan's age. One
// that expired while the retry was held back is not relied on: the operation is observed again,
// reclassified and then attempted.
func TestExecutorReclassifiesExpiredRetryEvidence(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	xe.ready(t)
	if _, err := xe.a.commitPlan(context.Background(), xe.pid); err != nil {
		t.Fatal(err)
	}
	mustExec(t, xe.db, `ALTER TABLE plan DISABLE TRIGGER immutable_rows`)
	mustExec(t, xe.db, `UPDATE plan SET max_observation_age = interval '2 seconds' WHERE id = $1`, xe.pid)
	mustExec(t, xe.db, `ALTER TABLE plan ENABLE TRIGGER immutable_rows`)
	xe.x.mu.Lock()
	xe.x.decryptErr = fmt.Errorf("sealed: %w", provider.ErrUnavailable)
	xe.x.mu.Unlock()
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := xe.executor(t, life, idle, options{})
	a.startExecutor()
	defer stopping(a, stop)
	until(t, a, idle, "a retry held back by a sealed provider", func() bool {
		return xe.entries(t, "use-time-check") > 0 && count(t, xe.db, `SELECT count(*) FROM operation_classification
			WHERE outcome = 'safe-to-retry'`) > 0
	})
	time.Sleep(2200 * time.Millisecond)
	xe.x.mu.Lock()
	xe.x.decryptErr = nil
	xe.x.mu.Unlock()
	until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
	if n := count(t, xe.db, `SELECT count(*) FROM operation_classification WHERE outcome = 'safe-to-retry'`); n < 2 {
		t.Fatalf("%d safe-to-retry classifications: the expired one was relied on", n)
	}
}

// ER §3.3, D6 of #899: a classified retry takes a new certificate, one new evidence observation
// recorded for the operation and taken with it, and its attempt, and no new classification: the
// certificate of the classification's observation lived for that use alone. A key replaced while the
// retry was held back is signed from, and the evidence names it. A loop that classified again
// instead of observing for evidence would show a second safe-to-retry classification and no
// evidence recorded for the operation.
func TestExecutorRetryTakesNewEvidence(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	xe.ready(t)
	if _, err := xe.a.commitPlan(context.Background(), xe.pid); err != nil {
		t.Fatal(err)
	}
	xe.x.mu.Lock()
	xe.x.decryptErr = fmt.Errorf("sealed: %w", provider.ErrUnavailable)
	xe.x.mu.Unlock()
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := xe.executor(t, life, idle, options{})
	a.startExecutor()
	defer stopping(a, stop)
	until(t, a, idle, "a retry held back by a sealed provider", func() bool {
		return xe.entries(t, "use-time-check") > 0 && count(t, xe.db, `SELECT count(*) FROM operation_classification
			WHERE outcome = 'safe-to-retry'`) > 0
	})
	// The key is replaced while the provider is still sealed, and a pass ends before it unseals: a
	// pass in flight when until returned is held back too.
	latest := xe.keys.seed(t, xe.db, xe.cluster)
	passEnds(t, a, idle)
	xe.x.mu.Lock()
	xe.x.decryptErr = nil
	xe.x.mu.Unlock()
	until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
	if n := count(t, xe.db, `SELECT count(*) FROM operation_classification WHERE outcome = 'safe-to-retry'`); n != 1 {
		t.Fatalf("%d safe-to-retry classifications: the retry classified again instead of taking new evidence", n)
	}
	if n := count(t, xe.db, `SELECT count(*) FROM observation o JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis
		WHERE s.purpose = 'evidence' AND s.operation IS NOT NULL AND o.access_path = $1 AND o.access_version = $2`,
		latest.Path, latest.Version); n != 1 {
		t.Fatalf("%d evidence observations recorded for the operation read through key version %d, want 1", n, latest.Version)
	}
	if n := count(t, xe.db, `SELECT count(*) FROM attempt`); n != 1 || len(xe.node.AppliedRequests()) != 1 {
		t.Fatalf("%d attempts and %d requests; want one of each", n, len(xe.node.AppliedRequests()))
	}
}

// PA §3.3, §16, ER §3.1 item 2: one certificate covers a dispatch's evidence observation and its
// attempt. A key replaced while the evidence observation holds its certificate leaves the attempt
// sending with that certificate: its serial is the one the observation recorded. The control signs
// a certificate again at the time of the send and shows another serial, so the comparison can fail.
func TestExecutorSendsWithTheObservedCertificate(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	var once sync.Once
	observe := func(ctx context.Context, tc []byte, endpoint string) (talos.Reader, error) {
		once.Do(func() { xe.keys.seed(t, xe.db, xe.cluster) }) // the key is replaced after the certificate was signed
		return talos.Dial(ctx, tc, endpoint)
	}
	// The first attempt's dial records the talosconfig it was given and fails as a transport failure:
	// what is sent is the configuration, not the apply.
	var mu sync.Mutex
	var sent []byte
	dial := func(_ context.Context, tc []byte, _ string) (talos.Applier, error) {
		mu.Lock()
		defer mu.Unlock()
		if sent == nil {
			sent = bytes.Clone(tc)
		}
		return nil, errors.New("not dialled")
	}
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := xe.executor(t, life, idle, options{dial: observe, dialApply: dial})
	a.startExecutor()
	defer stopping(a, stop)
	waitIdle(t, idle, 1)
	xe.approveThrough(t, a)
	until(t, a, idle, "the first attempt dials", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return sent != nil
	})
	var serial string
	if err := xe.db.QueryRow(`SELECT o.access_serial FROM attempt a JOIN observation o ON o.id = a.observation
		WHERE a.plan = $1 AND a.number = 1`, xe.pid).Scan(&serial); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := certSerial(t, sent); got != serial {
		t.Fatalf("sent with certificate %s, want the observation's %s", got, serial)
	}
	// No row of any table holds the client key the certificate was signed for or the CA's: the
	// scan's control finds the serial, which the observation records.
	scan := func(needle string) (n int) {
		rows, err := xe.db.Query(`SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema()
			AND table_type = 'BASE TABLE'`)
		if err != nil {
			t.Fatal(err)
		}
		var tables []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			tables = append(tables, s)
		}
		_ = rows.Close()
		for _, tbl := range tables {
			n += count(t, xe.db, `SELECT count(*) FROM "`+tbl+`" r WHERE r::text LIKE '%' || $1 || '%'`, needle)
		}
		return n
	}
	caKeyLine := strings.Split(string(xe.keys.ca.Key), "\n")[1] // a line of the PEM body, which a JSON or text rendering keeps whole
	if key := clientKey(sent); key == "" || scan(key) != 0 || len(caKeyLine) < 20 || scan(caKeyLine) != 0 {
		t.Fatalf("a row holds a client key or the CA key (client key present in the talosconfig: %v)", clientKey(sent) != "")
	}
	if scan(serial) == 0 {
		t.Fatal("the control: the scan did not find the recorded serial")
	}
	again, _, err := xe.a.certFor(t.Context(), xe.cluster, role.Admin, nodeUseLife*nodeTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if again.Serial() == serial {
		t.Fatal("the control: a certificate signed again names the same serial, so the comparison cannot tell them apart")
	}
}

// ER §4 (unresolved to cancelled): an operation taken over with no attempt, whose approval can no
// longer pass comparison 1, is cancelled and releases its scope, whatever the observation shows.
func TestExecutorCancelsATakenOverOperationWhosePlanIsDead(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	xe.ready(t)
	if _, err := xe.a.commitPlan(context.Background(), xe.pid); err != nil {
		t.Fatal(err)
	}
	decode[cancellationBody](t, xe.cancel(xe.human("h-approver"), xe.key(), xe.pid, `{"reason":"not now"}`), http.StatusOK)
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := xe.executor(t, life, idle, options{})
	a.startExecutor()
	defer stopping(a, stop)
	until(t, a, idle, "the operation is cancelled", func() bool { return xe.opState(t) == "cancelled" })
	if n := len(xe.node.AppliedRequests()); n != 0 {
		t.Fatalf("%d requests", n)
	}
}

// ER §3.1: a queued plan is checked as approved, unexpired and with its scope open immediately
// before its dependencies are read, so a plan cancelled while it waited for an earlier dispatch
// gets neither the Talos access nor the decrypted artifact.
func TestDispatchOfAPlanCancelledWhileQueuedReadsNoDependency(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	xe.ready(t)
	decode[cancellationBody](t, xe.cancel(xe.human("h-approver"), xe.key(), xe.pid, `{"reason":"not now"}`), http.StatusOK)
	life, stop := context.WithCancel(t.Context())
	defer stop()
	a := xe.executor(t, life, make(chan struct{}), options{})
	if err := a.dispatchPlan(context.Background(), dispatchCandidate{plan: xe.pid, machine: xe.machine, cluster: xe.cluster}); err != nil {
		t.Fatal(err)
	}
	xe.x.mu.Lock()
	defer xe.x.mu.Unlock()
	if reads := xe.keys.kv.readVersions(); len(reads) != 0 || xe.x.decrypts != 0 {
		t.Fatalf("%d CA key reads and %d decryptions for a cancelled plan", len(reads), xe.x.decrypts)
	}
	xe.wantNothingSent(t)
}

// ER §3.4, §5.2 and rows 012-015, 020: an operation unresolved with an attempt nobody accounted for
// is settled by nothing until its accounting decision, which names a recovery observation as the
// start of its settle and one begun after the floor as the end. The loop takes them: none before the
// attempt's transport deadline, then one, then one more once a settle floor has passed since it; it
// sends nothing and leaves the operation unresolved.
func TestExecutorPacesRecoveryReadsWhileAnAttemptIsUnaccounted(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	xe.ready(t)
	cm, err := xe.a.commitPlan(context.Background(), xe.pid)
	if err != nil {
		t.Fatal(err)
	}
	at, err := xe.a.recordAttempt(context.Background(), cm.Operation, 1)
	if err != nil {
		t.Fatal(err)
	}
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := xe.executor(t, life, idle, options{})
	const floor = 2 * time.Second
	a.d.exec.SettleFloor = floor
	a.startExecutor()
	defer stopping(a, stop)
	until(t, a, idle, "the taken-over operation is unresolved", func() bool { return xe.opState(t) == "unresolved" })
	reads := func() int {
		return count(t, xe.db, `SELECT count(*) FROM observation_start WHERE machine = $1 AND operation = $2 AND purpose = 'recovery'`,
			xe.machine, cm.Operation)
	}
	passes := func() {
		for range 3 {
			a.wakeExecutor()
			waitIdle(t, idle, 1)
		}
	}
	passes()
	if n := reads(); n != 0 {
		t.Fatalf("%d recovery observations before the transport deadline", n)
	}
	mustExec(t, xe.db, `ALTER TABLE attempt DISABLE TRIGGER immutable_rows`)
	mustExec(t, xe.db, `UPDATE attempt SET at = clock_timestamp() - interval '11 seconds',
		transport_deadline = clock_timestamp() - interval '10 seconds' WHERE id = $1`, at.Attempt)
	mustExec(t, xe.db, `ALTER TABLE attempt ENABLE TRIGGER immutable_rows`)
	passes()
	if n := reads(); n != 1 {
		t.Fatalf("%d recovery observations after the transport deadline, within one settle floor; want 1", n)
	}
	// A read that took longer than the floor: begun before it, recorded within it. The floor runs
	// from the recorded read, as the decision measures it.
	mustExec(t, xe.db, `ALTER TABLE machine_event DISABLE TRIGGER immutable_rows`)
	mustExec(t, xe.db, `UPDATE machine_event e SET at = clock_timestamp() - interval '5 seconds' FROM observation_start s
		WHERE e.machine = s.machine AND e.revision = s.revision AND s.operation = $1 AND s.purpose = 'recovery'`, cm.Operation)
	mustExec(t, xe.db, `ALTER TABLE machine_event ENABLE TRIGGER immutable_rows`)
	passes()
	if n := reads(); n != 1 {
		t.Fatalf("%d recovery observations within a settle floor of the last recorded read; want 1", n)
	}
	time.Sleep(floor)
	passes()
	if n := reads(); n != 2 {
		t.Fatalf("%d recovery observations a settle floor later; want 2", n)
	}
	if s := xe.opState(t); s != "unresolved" {
		t.Fatalf("the operation is %s", s)
	}
	if n := len(xe.node.AppliedRequests()); n != 0 || count(t, xe.db, `SELECT count(*) FROM attempt WHERE operation = $1`, cm.Operation) != 1 {
		t.Fatalf("%d requests", n)
	}
}

// ER §3.4: a starting controller takes over an operation another controller committed and did
// not send, which is unresolved; its recovery observation shows the pre-dispatch configuration, so
// the operation is classified safe to retry, attempted once and completed.
func TestExecutorTakesOverAtStart(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	xe.ready(t)
	cm, err := xe.a.commitPlan(context.Background(), xe.pid)
	if err != nil {
		t.Fatal(err)
	}
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := xe.executor(t, life, idle, options{})
	a.startExecutor()
	defer stopping(a, stop)
	until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
	if n := count(t, xe.db, `SELECT count(*) FROM operation o WHERE o.id = $1 AND o.owner = $2 AND o.owner_gen = 2`, cm.Operation,
		a.d.owner.ID); n != 1 {
		t.Fatal("the starting controller does not hold the operation at generation 2")
	}
	if n := count(t, xe.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'ownership-transition'
		AND entry->>'reason' = 'controller-start' AND entry->>'operation' = $2`, xe.machine, cm.Operation); n != 1 {
		t.Fatalf("%d takeover entries", n)
	}
	if n := count(t, xe.db, `SELECT count(*) FROM operation_classification WHERE operation = $1 AND outcome = 'safe-to-retry'`,
		cm.Operation); n != 1 {
		t.Fatalf("%d safe-to-retry classifications", n)
	}
	if n := len(xe.node.AppliedRequests()); n != 1 || count(t, xe.db, `SELECT count(*) FROM attempt WHERE operation = $1`, cm.Operation) != 1 {
		t.Fatalf("%d requests", n)
	}
}
