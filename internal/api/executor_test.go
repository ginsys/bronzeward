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
			'dependencies', '{}'::jsonb), now() - interval '1 hour'
		FROM installation_state`, ae.machine, rev, ae.pid, ae.a.d.owner.ID)
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
			'started', to_char(now() - interval '1 hour', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"'), 'dependencies', '{}'::jsonb), now()
		FROM installation_state`, ae.machine, rev, ae.pid, ae.a.d.owner.ID)
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
// the loop carries the operation on. The control is the same plan with no loss.
func TestExecutorRetriesALostResponseRecord(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		losses    int32
		committed bool // the lost COMMIT did commit
	}{
		"lost before the commit": {losses: 2},
		"lost after the commit":  {losses: 1, committed: true},
		"control":                {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			xe := newExecEnv(t)
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
				return fmt.Errorf("the connection closed: %w", driver.ErrBadConn)
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

// ER §3.1 and §3.3: a retry is made with the access its evidence read. When the access moved to
// another version while the retry was held back, the operation is observed and classified again
// with the new one, and the attempt rests on that observation.
func TestExecutorRetryFollowsAccessRotation(t *testing.T) {
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
	xe.x.mu.Lock()
	v := xe.x.access.Version()
	v.Version++
	xe.x.access = provider.NewTalosAccess(v, xe.x.access.Talosconfig())
	xe.x.decryptErr = nil
	xe.x.mu.Unlock()
	until(t, a, idle, "the operation completes", func() bool { return xe.opState(t) == "completed" })
	if n := count(t, xe.db, `SELECT count(*) FROM attempt a JOIN observation o ON o.id = a.observation WHERE o.access_version = $1`,
		v.Version); n != 1 {
		t.Fatalf("the attempt does not rest on an observation read with access version %d", v.Version)
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

// ER §3.4, §5.2 and row 020: an operation unresolved with an attempt nobody accounted for waits for
// its accounting decision, which refuses a completion observation until then, so the loop takes
// none on any pass.
func TestExecutorTakesNoObservationWhileAnAttemptIsUnaccounted(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	xe.ready(t)
	cm, err := xe.a.commitPlan(context.Background(), xe.pid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := xe.a.recordAttempt(context.Background(), cm.Operation, 1); err != nil {
		t.Fatal(err)
	}
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := xe.executor(t, life, idle, options{})
	a.startExecutor()
	defer stopping(a, stop)
	until(t, a, idle, "the taken-over operation is unresolved", func() bool { return xe.opState(t) == "unresolved" })
	before := xe.entries(t, "observation-started")
	for range 3 {
		a.wakeExecutor()
		waitIdle(t, idle, 1)
	}
	if after := xe.entries(t, "observation-started"); after != before {
		t.Fatalf("%d observations started while the attempt was unaccounted", after-before)
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
