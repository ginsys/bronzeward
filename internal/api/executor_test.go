package api

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
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
	return &execEnv{commitEnv: ce, node: node, artifact: artifact, x: &fakeExecutor{artifact: artifact,
		access: provider.NewTalosAccess(provider.TalosAccessVersion{Path: "access/talos/" + ce.cluster, Version: 1,
			CreatedTime: accessCreated}, pki.Talosconfig("", "", ""))}}
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
	if strings.Contains(entry, "hostname") || strings.Contains(entry, "cluster:") {
		t.Fatalf("the check holds a value: %s", entry)
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
// dependency is back the same plan is dispatched and completes.
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			xe := newExecEnv(t)
			tc.fail(xe.x)
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
			if n := count(t, xe.db, `SELECT count(*) FROM observation_start`); n != 0 || xe.entries(t, "commitment") != 0 {
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

// ER §3.3: an attempt whose transaction failed was never recorded, so nothing is sent for it; the
// operation stays committed under its owner and the next pass records it and sends once.
func TestExecutorAttemptNotRecordedSendsNothing(t *testing.T) {
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
	until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
	if n := len(xe.node.AppliedRequests()); n != 1 || count(t, xe.db, `SELECT count(*) FROM attempt WHERE number = 1`) != 1 {
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
