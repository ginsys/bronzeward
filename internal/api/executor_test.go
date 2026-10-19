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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
}

func newExecEnv(t *testing.T) *execEnv {
	t.Helper()
	pki := talostest.NewPKI(t)
	node := talostest.Serve(t, pki)
	p := newPlanEnv(t)
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
	return &execEnv{commitEnv: ce, node: node, artifact: artifact, x: &fakeExecutor{artifact: artifact,
		access: provider.NewTalosAccess(provider.TalosAccessVersion{Path: "access/talos/" + ce.cluster, Version: 1,
			CreatedTime: accessCreated}, pki.Talosconfig("192.0.2.1", "192.0.2.2", ""))}}
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
	// One decryption, one access read for the dispatch and one for the completion observation.
	xe.x.mu.Lock()
	defer xe.x.mu.Unlock()
	if xe.x.decrypts != 1 || len(xe.x.reads) != 2 {
		t.Fatalf("%d decryptions, %d access reads", xe.x.decrypts, len(xe.x.reads))
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
		fail        func(*fakeExecutor)
		heal        func(*fakeExecutor)
		decrypts    int
	}{
		{"sealed provider", "provider-unavailable", func(x *fakeExecutor) { x.decryptErr = fmt.Errorf("sealed: %w", provider.ErrUnavailable) },
			func(x *fakeExecutor) { x.decryptErr = nil }, 1},
		{"another key", "key-mismatch", func(x *fakeExecutor) { x.keyName = "other-key" }, func(x *fakeExecutor) { x.keyName = "" }, 0},
		{"unreadable access", "denied", func(x *fakeExecutor) { x.err = fmt.Errorf("no: %w", provider.ErrDenied) },
			func(x *fakeExecutor) { x.err = nil }, 1},
		{"absent access", "absent", func(x *fakeExecutor) { x.err = fmt.Errorf("no: %w", provider.ErrAbsent) },
			func(x *fakeExecutor) { x.err = nil }, 1},
		{"malformed access", "malformed", func(x *fakeExecutor) { x.err = fmt.Errorf("no: %w", provider.ErrProtocol) },
			func(x *fakeExecutor) { x.err = nil }, 1},
		{"sealed access", "provider-unavailable", func(x *fakeExecutor) { x.err = fmt.Errorf("sealed: %w", provider.ErrUnavailable) },
			func(x *fakeExecutor) { x.err = nil }, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			xe := newExecEnv(t)
			if n := count(t, xe.db, `SELECT count(*) FROM dependency_status WHERE provider = 'transit' AND object = $1
				AND class = 'retained'`, artifactKeyName); n < 1 {
				t.Fatal("the artifact key is not classified retained")
			}
			tc.fail(xe.x)
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
			tc.heal(xe.x)
			xe.x.mu.Unlock()
			until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
			if n := len(xe.node.AppliedRequests()); n != 1 {
				t.Fatalf("%d requests", n)
			}
		})
	}
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

// PA §3.3 and §16 Talos access, executor: a node-side cause (a talosconfig the client cannot use, a
// credential Talos refuses) fails the dispatch's fresh observation, which records its cause; no
// operation is committed and nothing is sent, and once the access is repaired the plan completes.
func TestExecutorNodeSideAccessFailure(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		access func(t *testing.T, old provider.TalosAccess) []byte
		cause  string
	}{
		"malformed talosconfig": {func(_ *testing.T, old provider.TalosAccess) []byte {
			return []byte(strings.Replace(string(old.Talosconfig()), "ca: ", "ca: not-base64-", 1))
		}, "talosconfig"},
		"a credential Talos refuses": {func(t *testing.T, _ provider.TalosAccess) []byte {
			return talostest.NewPKI(t).Talosconfig("", "", "")
		}, "node-unreachable"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			xe := newExecEnv(t)
			good := xe.x.access
			xe.x.access = provider.NewTalosAccess(good.Version(), c.access(t, good))
			life, stop := context.WithCancel(t.Context())
			idle := make(chan struct{})
			a := xe.executor(t, life, idle, options{nodeTimeout: 500 * time.Millisecond})
			a.startExecutor()
			defer stopping(a, stop)
			waitIdle(t, idle, 1)
			xe.approveThrough(t, a)
			until(t, a, idle, "a failed evidence observation", func() bool {
				return count(t, xe.db, `SELECT count(*) FROM observation o JOIN observation_start s ON s.machine = o.machine
					AND s.revision = o.basis WHERE o.machine = $1 AND s.purpose = 'evidence' AND o.unread::text LIKE '%' || $2 || '%'`,
					xe.machine, c.cause) > 0
			})
			passEnds(t, a, idle)
			xe.wantNothingSent(t)
			if xe.entries(t, "commitment") != 0 {
				t.Fatal("a commitment after a failed observation")
			}
			xe.x.mu.Lock()
			xe.x.access = good
			xe.x.mu.Unlock()
			until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
		})
	}
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
			xe.x.err = fmt.Errorf("%s: %w", canary, provider.ErrDenied)
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

// ER §3.2 and §3.3 comparison 3, PA §3.3: an attempt made again after its transaction failed reads
// the access again. When it moved since the commitment's evidence was observed (another version
// number, or the same number with another created_time after the metadata was deleted and written
// again), comparison 3 refuses the attempt, the operation is observed and classified again with the
// new access, and the one request rests on that observation, not on the earlier evidence.
func TestExecutorAttemptAfterAccessRotation(t *testing.T) {
	t.Parallel()
	for name, move := range map[string]func(*provider.TalosAccessVersion){
		"another version":                        func(v *provider.TalosAccessVersion) { v.Version++ },
		"the same version, another created time": func(v *provider.TalosAccessVersion) { v.CreatedTime = v.CreatedTime.Add(time.Hour) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			attemptAfterAccessRotation(t, move)
		})
	}
}

func attemptAfterAccessRotation(t *testing.T, move func(*provider.TalosAccessVersion)) {
	xe := newExecEnv(t)
	var armed atomic.Bool
	armed.Store(true)
	var moved provider.TalosAccessVersion // set under xe.x.mu
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := xe.executor(t, life, idle, options{commit: func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM attempt`).Scan(&n); err == nil && n > 0 && armed.CompareAndSwap(true, false) {
			_ = tx.Rollback()
			xe.x.mu.Lock()
			moved = xe.x.access.Version()
			move(&moved)
			xe.x.access = provider.NewTalosAccess(moved, xe.x.access.Talosconfig())
			xe.x.mu.Unlock()
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
	xe.x.mu.Lock()
	v := moved
	xe.x.mu.Unlock()
	if n := count(t, xe.db, `SELECT count(*) FROM attempt a JOIN observation o ON o.id = a.observation
		WHERE o.access_path = $1 AND o.access_version = $2 AND o.access_created = $3`, v.Path, v.Version,
		v.CreatedTime.UTC().Format(time.RFC3339Nano)); n != 1 {
		t.Fatalf("the attempt does not rest on an observation read with access %+v", v)
	}
	if n := count(t, xe.db, `SELECT count(*) FROM machine_event WHERE kind = 'refusal' AND entry->>'comparison' = '3'
		AND entry->>'cause' = 'the use-time check read another Talos access than the evidence'`); n != 1 {
		t.Fatalf("%d comparison 3 refusals for the moved access", n)
	}
}

// revokeAtCommitment runs the executor on the plan and revokes its approval at the commitment: while
// the commitment transaction holds its locks (held, DS row 003), the revocation waiting for it, or
// once it has committed (row 004). With control, the attempt transaction's comparison 1 is skipped.
// Checked, comparison 1 refuses the attempt: the operation is cancelled and nothing reaches the node.
func revokeAtCommitment(t *testing.T, held, control bool) {
	xe := newExecEnv(t)
	token, k := xe.human("h-recovery"), xe.key()
	var armed atomic.Bool
	armed.Store(true)
	revoked := make(chan int, 1)
	waited := make(chan error, 1)
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := xe.executor(t, life, idle, options{noPlanCheck: control, commit: func(tx *sql.Tx) error {
		var ops, attempts int
		var approval sql.NullString
		if err := tx.QueryRow(`SELECT (SELECT count(*) FROM operation WHERE plan = $1), (SELECT count(*) FROM attempt WHERE plan = $1),
			(SELECT approval FROM plan_state WHERE plan = $1)`, xe.pid).Scan(&ops, &attempts, &approval); err != nil || ops == 0 ||
			attempts != 0 || !armed.CompareAndSwap(true, false) {
			return tx.Commit()
		}
		revoke := func() { revoked <- xe.revokeApproval(token, k, approval.String, `{"reason":"wrong"}`).Code }
		if held {
			go revoke()
			waited <- revocationWaits(xe.db)
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
	if control {
		until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
	} else {
		until(t, a, idle, "the operation is cancelled", func() bool { return xe.opState(t) == "cancelled" })
	}
	if held {
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

// revocationWaits is nil once the approval revocation waits on the machine row the commitment holds
// (T5b's first lock), in this test's own database. It reports, not fails: it runs on the executor's
// goroutine.
func revocationWaits(db *sql.DB) error {
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()
			AND wait_event_type = 'Lock' AND query LIKE 'SELECT 1 FROM machine WHERE id = $1 FOR UPDATE'`).Scan(&n); err != nil {
			return err
		}
		if n == 1 {
			return nil
		}
	}
	return errors.New("the revocation did not wait on the machine row within 10s")
}

// DS row 003 through the executor and a Talos node: a revocation started while the commitment holds
// its locks waits for it, and the attempt transaction's comparison 1 then refuses the attempt.
func TestExecutorRevocationHeld(t *testing.T) {
	t.Parallel()
	revokeAtCommitment(t, true, false)
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
			revokeAtCommitment(t, false, c.control)
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
	setCheckValidity("1 hour")
	until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
}

// checkedAccess is a seeded use-time check's dependencies: the access the evidence observation o
// read, so that comparison 3's access term passes and the check's time alone decides.
const checkedAccess = `jsonb_build_object('access', jsonb_build_object('path', o.access_path,
	'version', o.access_version, 'created', o.access_created))`

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

// ER §3.1 item 2: a caller that read the access itself passes the time its reads began, and the
// check's window runs from that time, not from the check's own start.
func TestUseTimeCheckWindowStartsWhereTheCallersReadsBegan(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	began := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	acc := xe.x.access
	if _, err := xe.a.useTimeCheck(context.Background(), xe.machine, xe.cluster, xe.pid, "", &acc, began); err != nil {
		t.Fatal(err)
	}
	if n := count(t, xe.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'use-time-check'
		AND (entry->>'started')::timestamptz = $2`, xe.machine, began); n != 1 {
		t.Fatalf("%d use-time checks start at %s", n, began.Format(time.RFC3339Nano))
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

// ER §3.1 and §3.3, PA §16: a retry is made with the access its evidence read. When the access
// moved to another version identity while the retry was held back (another version number, or the
// same number with another created_time after the metadata was deleted and written again), the
// operation is observed and classified again with the new one, and the attempt rests on that
// observation.
func TestExecutorRetryFollowsAccessRotation(t *testing.T) {
	t.Parallel()
	for name, move := range map[string]func(*provider.TalosAccessVersion){
		"another version":                        func(v *provider.TalosAccessVersion) { v.Version++ },
		"the same version, another created time": func(v *provider.TalosAccessVersion) { v.CreatedTime = v.CreatedTime.Add(time.Hour) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			retryFollowsAccessRotation(t, move)
		})
	}
}

func retryFollowsAccessRotation(t *testing.T, move func(*provider.TalosAccessVersion)) {
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
	// The access moves while the provider is still sealed, and a pass ends before it unseals: a
	// pass that read the earlier access, in flight when until returned, is held back too.
	xe.x.mu.Lock()
	v := xe.x.access.Version()
	move(&v)
	xe.x.access = provider.NewTalosAccess(v, xe.x.access.Talosconfig())
	xe.x.mu.Unlock()
	passEnds(t, a, idle)
	xe.x.mu.Lock()
	xe.x.decryptErr = nil
	xe.x.mu.Unlock()
	until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
	if n := count(t, xe.db, `SELECT count(*) FROM attempt a JOIN observation o ON o.id = a.observation
		WHERE o.access_path = $1 AND o.access_version = $2 AND o.access_created = $3`, v.Path, v.Version, v.CreatedTime.UTC().Format(time.RFC3339Nano)); n != 1 {
		t.Fatalf("the attempt does not rest on an observation read with access %+v", v)
	}
}

// PA §16 Talos access, executor: a rotation between the evidence observation and the attempt
// leaves the attempt sending with the version the observation read; the control reads the latest
// version at send.
func TestExecutorSendsWithTheObservedAccess(t *testing.T) {
	t.Parallel()
	for _, latest := range []bool{false, true} {
		t.Run(fmt.Sprint("latestAccess=", latest), func(t *testing.T) {
			t.Parallel()
			xe := newExecEnv(t)
			old := xe.x.access
			v := old.Version()
			v.Version++
			rotated := provider.NewTalosAccess(v, append(old.Talosconfig(), "\n# rotated\n"...))
			var once sync.Once
			xe.x.onRead = func() { // the first read returns old; every later one rotated
				once.Do(func() {
					xe.x.mu.Lock()
					xe.x.access = rotated
					xe.x.mu.Unlock()
				})
			}
			// The first attempt's dial records the talosconfig it was given and fails as a transport
			// failure: what is sent is the configuration, not the apply.
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
			a := xe.executor(t, life, idle, options{dialApply: dial, latestAccess: latest})
			a.startExecutor()
			defer stopping(a, stop)
			waitIdle(t, idle, 1)
			xe.approveThrough(t, a)
			until(t, a, idle, "the first attempt dials", func() bool {
				mu.Lock()
				defer mu.Unlock()
				return sent != nil
			})
			if n := count(t, xe.db, `SELECT count(*) FROM attempt a JOIN observation o ON o.id = a.observation
				WHERE a.plan = $1 AND a.number = 1 AND o.access_version = $2`, xe.pid, old.Version().Version); n != 1 {
				t.Fatalf("the first attempt does not rest on an observation read with access version %d", old.Version().Version)
			}
			mu.Lock()
			defer mu.Unlock()
			want := old.Talosconfig()
			if latest {
				want = rotated.Talosconfig()
			}
			if !bytes.Equal(sent, want) {
				t.Fatalf("sent with %q, want %q", sent, want)
			}
		})
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
	if len(xe.x.reads) != 0 || xe.x.decrypts != 0 {
		t.Fatalf("%d access reads and %d decryptions for a cancelled plan", len(xe.x.reads), xe.x.decrypts)
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
