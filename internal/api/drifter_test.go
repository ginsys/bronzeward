package api

import (
	"context"
	"crypto/rand"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/staging"
	"github.com/ginsys/bronzeward/internal/talos"
)

// drifter is a controller of the current epoch over le's database whose drift loop, once started,
// passes every interval, signals idle after each pass and stops when life ends.
func (le *loopEnv) drifter(t *testing.T, life context.Context, interval time.Duration, idle chan<- struct{}) *API {
	t.Helper()
	d := le.d
	d.exe, d.life, d.timers, d.runs, d.exec = le.x, life, idleLoop, &sync.WaitGroup{}, testExecution()
	d.exec.DriftInterval = interval
	d.owner = staging.Owner{ID: "a/1/" + rand.Text(), Epoch: epoch(t, le.db)}
	return le.buildWith(d, options{onDriftIdle: func() {
		select {
		case idle <- struct{}{}:
		case <-life.Done():
		}
	}})
}

// appliedIsNode gives the machine an Applied, the adopt plan's release with the stand-in node's
// configuration's digest.
func (le *loopEnv) appliedIsNode(t *testing.T) {
	t.Helper()
	digest := talos.ConfigurationDigest([]byte(standInConfig))
	mustExec(t, le.db, `UPDATE machine_state s SET applied_release = p.release, applied_digest = $3, applied_source = 'adoption',
			baseline_revision = 1
		FROM plan p WHERE s.machine = $1 AND p.id = $2`, le.machine, le.pid, digest[:])
}

// driftStarts counts the machine's drift observations begun, and those begun for no plan.
func (le *loopEnv) driftStarts(t *testing.T) (all, planless int) {
	t.Helper()
	return count(t, le.db, `SELECT count(*) FROM observation_start WHERE machine = $1 AND purpose = 'drift'`, le.machine),
		count(t, le.db, `SELECT count(*) FROM observation_start WHERE machine = $1 AND purpose = 'drift' AND plan IS NULL
			AND operation IS NULL`, le.machine)
}

// passUntil waits for passes of the loop until cond holds.
func passUntil(t *testing.T, idle <-chan struct{}, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("the drift loop did not %s", what)
		}
		waitIdle(t, idle, 1)
	}
}

// Execution and recovery §6.1, §6.5 (choice §10.36), PA §16: the drift loop observes a managed
// machine for no plan, reading its Talos access; a digest equal to Applied's opens nothing, a
// differing one opens a drift record naming that drift observation, Applied's digest and the
// observed one, and the digest returning to Applied's closes the record returned.
func TestDrifterOpensAndReturns(t *testing.T) {
	t.Parallel()
	le := newLoopEnv(t, "")
	le.appliedIsNode(t)
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := le.drifter(t, life, 20*time.Millisecond, idle)
	a.startDrifter()
	defer stopping(a, stop)
	waitIdle(t, idle, 1)
	if all, planless := le.driftStarts(t); all == 0 || all != planless {
		t.Fatalf("%d drift observations, %d of them for no plan", all, planless)
	}
	if n := count(t, le.db, `SELECT count(*) FROM observation o JOIN observation_start s ON s.machine = o.machine
			AND s.revision = o.basis
		WHERE o.machine = $1 AND s.purpose = 'drift' AND o.access_version = 1 AND o.configuration_digest IS NOT NULL`,
		le.machine); n == 0 {
		t.Fatal("no drift observation recorded the access version and digest it read")
	}
	if n := count(t, le.db, `SELECT count(*) FROM drift_record WHERE machine = $1`, le.machine); n != 0 {
		t.Fatalf("%d drift records while the node runs Applied", n)
	}

	drifted := standInConfig + "# changed out of band\n"
	le.node.SetMachineConfig(t, []byte(drifted))
	applied, observed := talos.ConfigurationDigest([]byte(standInConfig)), talos.ConfigurationDigest([]byte(drifted))
	opened := `SELECT count(*) FROM drift_record d JOIN observation_start s ON s.machine = d.machine
			JOIN observation o ON o.machine = s.machine AND o.basis = s.revision AND o.id = d.observation
		WHERE d.machine = $1 AND d.closed IS NULL AND s.purpose = 'drift' AND d.applied_digest = $2 AND d.observed_digest = $3`
	passUntil(t, idle, "open a drift record", func() bool { return count(t, le.db, opened, le.machine, applied[:], observed[:]) == 1 })

	le.node.SetMachineConfig(t, []byte(standInConfig))
	passUntil(t, idle, "close the record returned", func() bool {
		return count(t, le.db, `SELECT count(*) FROM drift_record WHERE machine = $1 AND resolution = 'returned'`, le.machine) == 1
	})
	if n := count(t, le.db, `SELECT count(*) FROM drift_record WHERE machine = $1`, le.machine); n != 1 {
		t.Fatalf("%d drift records", n)
	}
}

// The loop observes no machine that has no Applied, one whose scope an operation holds, or one
// recovery mode closes (§6.1: such a read could open nothing).
func TestDrifterSkips(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		apply bool // an approved apply-config plan of the machine, committed
		setup func(t *testing.T, le *loopEnv)
	}{
		{"no Applied", false, func(t *testing.T, le *loopEnv) {
			mustExec(t, le.db, `UPDATE machine_state SET applied_release = NULL, applied_digest = NULL, applied_source = NULL,
				baseline_revision = NULL WHERE machine = $1`, le.machine)
		}},
		{"scope held", true, func(*testing.T, *loopEnv) {}},
		{"recovery mode", false, func(t *testing.T, le *loopEnv) {
			mustExec(t, le.db, `UPDATE installation_state SET recovery_mode = true`)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var other string
			var before []func(*planEnv)
			if c.apply {
				before = append(before, func(p *planEnv) {
					other = decode[planBody](t, p.plan(p.robot, "k-apply-0123456789", applyBody(p.target.rel, p.machine, "")),
						http.StatusCreated).ID
					decode[approvalBody](t, p.approve(p.human("h-approver"), "k-approve-apply-0123", other), http.StatusCreated)
				})
			}
			le := newLoopEnv(t, "", before...)
			le.appliedIsNode(t)
			if c.apply {
				le.commitAs(t, other)
			}
			c.setup(t, le)
			life, stop := context.WithCancel(t.Context())
			idle := make(chan struct{})
			a := le.drifter(t, life, 20*time.Millisecond, idle)
			a.startDrifter()
			defer stopping(a, stop)
			waitIdle(t, idle, 3)
			if all, _ := le.driftStarts(t); all != 0 {
				t.Fatalf("%d drift observations", all)
			}
		})
	}
}

// A drift observation that reads no configuration is logged with its machine, observation and
// cause, with the error text quoted nowhere (choice §10.36), and the next pass observes again.
func TestDrifterLogsUnread(t *testing.T) {
	t.Parallel()
	le := newLoopEnv(t, "")
	le.appliedIsNode(t)
	le.x.err = provider.ErrUnavailable
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := le.drifter(t, life, 20*time.Millisecond, idle)
	a.startDrifter()
	defer stopping(a, stop)
	waitIdle(t, idle, 2)
	if all, _ := le.driftStarts(t); all < 2 {
		t.Fatalf("%d drift observations after two passes", all)
	}
	var obs string
	if err := le.db.QueryRow(`SELECT id FROM observation WHERE machine = $1 ORDER BY revision LIMIT 1`, le.machine).Scan(&obs); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{le.machine, obs, "provider-unavailable"} {
		if !le.logged(want) {
			t.Fatalf("no log line names %q", want)
		}
	}
}

// A drift loop whose epoch is superseded ends (persistence-api.md §5.1).
func TestDrifterEpoch(t *testing.T) {
	t.Parallel()
	le := newLoopEnv(t, "")
	life, stop := context.WithCancel(t.Context())
	defer stop()
	a := le.drifter(t, life, 20*time.Millisecond, make(chan struct{}, 100))
	newEpoch(t, le.db)
	a.startDrifter()
	done := make(chan struct{})
	go func() {
		a.d.runs.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the drift loop still runs in a superseded epoch")
	}
	if !le.logged("superseded") {
		t.Fatal("no log line names the superseded epoch")
	}
}

// New starts the drift loop of a controller with an executor, at the configured interval.
func TestNewStartsDrifter(t *testing.T) {
	t.Parallel()
	le := newLoopEnv(t, "")
	le.appliedIsNode(t)
	ex := testExecution()
	ex.DriftInterval = 20 * time.Millisecond
	life, stop := context.WithCancel(t.Context())
	h := New(life, le.db, auth.NewVerifier(le.cfg, le.db, auth.Discover(le.cfg.OIDC)), le.cfg, ex, nil, nil, le.x,
		&config.Ingestion{Instance: "a", Heartbeat: time.Hour, Lease: 2 * time.Hour}, epoch(t, le.db), false, nil)
	defer stopping(h.(*API), stop)
	deadline := time.Now().Add(5 * time.Second)
	for all, _ := le.driftStarts(t); all == 0; all, _ = le.driftStarts(t) {
		if time.Now().After(deadline) {
			t.Fatal("no drift observation")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
