package api

import (
	"cmp"
	"context"
	"crypto/rand"
	"net/http"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/staging"
	"github.com/ginsys/bronzeward/internal/talos"
	"github.com/ginsys/bronzeward/internal/talos/talostest"
)

// loopEnv is an adoptEnv whose machine is served by a stand-in node at the plan's route, reporting
// the machine's identity and a configuration whose digest the release's import base carries, read
// through an executor that answers the node's talosconfig.
type loopEnv struct {
	*adoptEnv
	node *talostest.Node
	x    *fakeExecutor
}

func newLoopEnv(t *testing.T, extra string, before ...func(*planEnv)) *loopEnv {
	t.Helper()
	pki := talostest.NewPKI(t)
	node := talostest.Serve(t, pki)
	ae := newAdoptEnv(t, extra, append([]func(*planEnv){func(p *planEnv) {
		mustExec(t, p.db, `UPDATE machine SET talos_endpoint = $2 WHERE id = $1`, p.machine, node.Endpoint)
	}}, before...)...)
	node.SetSMBIOSUUID(t, ae.uuid.String, ae.uuid.Valid)
	node.SetNodeID(t, cmp.Or(ae.nodeID.String, "node-a"))
	node.SetClusterID(t, ae.clusterID)
	node.SetMachineConfig(t, []byte(standInConfig))
	digest := talos.ConfigurationDigest([]byte(standInConfig))
	// The import base's digest is the stand-in's, as an import of the node's configuration records.
	mustExec(t, ae.db, `ALTER TABLE import_base_revision DISABLE TRIGGER immutable_rows`)
	mustExec(t, ae.db, `UPDATE import_base_revision b SET configuration_digest = $2 FROM plan p
		JOIN release_machine rm ON rm.release = p.release AND rm.machine = p.machine
		WHERE p.id = $1 AND b.id = rm.import_base_revision`, ae.pid, digest[:])
	mustExec(t, ae.db, `ALTER TABLE import_base_revision ENABLE TRIGGER immutable_rows`)
	return &loopEnv{adoptEnv: ae, node: node, x: &fakeExecutor{access: provider.NewTalosAccess(provider.TalosAccessVersion{
		Path: "access/talos/" + ae.cluster, Version: 1, CreatedTime: accessCreated}, pki.Talosconfig("", "", ""))}}
}

// adopter is a controller of the current epoch over le's database under timers, whose adoption
// loop, once started, signals idle after each pass and stops when life ends.
func (le *loopEnv) adopter(t *testing.T, life context.Context, timers config.Ingestion, idle chan<- struct{}) *API {
	t.Helper()
	d := le.d
	d.exe, d.life, d.timers, d.runs = le.x, life, timers, &sync.WaitGroup{}
	d.owner = staging.Owner{ID: "a/1/" + rand.Text(), Epoch: epoch(t, le.db)}
	return le.buildWith(d, options{onAdoptIdle: func() {
		select {
		case idle <- struct{}{}:
		case <-life.Done():
		}
	}})
}

// approveThrough approves the adopt plan through a's T5a route.
func (le *loopEnv) approveThrough(t *testing.T, a *API) {
	t.Helper()
	le.approval = decode[approvalBody](t, le.do(a, call{method: "POST", path: prefix + "/plans/" + le.pid + "/approvals",
		token: le.human("h-approver"), key: le.key(), body: `{}`}), http.StatusCreated).ID
}

// idleLoop is a loop's timers with no poll in a test's time.
var idleLoop = config.Ingestion{Heartbeat: time.Hour, Lease: time.Minute}

// waitIdle waits for n idle signals.
func waitIdle(t *testing.T, idle <-chan struct{}, n int) {
	t.Helper()
	for range n {
		select {
		case <-idle:
		case <-time.After(10 * time.Second):
			t.Fatal("the adoption loop did not pass")
		}
	}
}

// planState reads the plan's stored state.
func (le *loopEnv) planState(t *testing.T) string {
	t.Helper()
	var s string
	if err := le.db.QueryRow(`SELECT state FROM plan_state WHERE plan = $1`, le.pid).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// starts counts the observation starts taken for the plan, and refusals its refusal entries.
func (le *loopEnv) starts(t *testing.T) (starts, refusals int) {
	t.Helper()
	return count(t, le.db, `SELECT count(*) FROM observation_start WHERE plan = $1`, le.pid),
		count(t, le.db, `SELECT count(*) FROM machine_event WHERE kind = 'refusal' AND entry->>'plan' = $1`, le.pid)
}

// ER §6.3 step 4: the controller records an approved adopt plan's adoption itself. A T5a
// approval's COMMIT wakes its adoption loop, which takes an evidence observation for the plan at
// the plan's route and commits the adoption record relying on it.
func TestAdopterAdoptsOnApproval(t *testing.T) {
	t.Parallel()
	le := newLoopEnv(t, "")
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := le.adopter(t, life, idleLoop, idle)
	a.startAdopter()
	defer stopping(a, stop)
	waitIdle(t, idle, 1) // the first pass found no approved plan
	le.approveThrough(t, a)
	waitIdle(t, idle, 1) // no poll in the test's time: only the approval's wake runs this pass
	if s := le.planState(t); s != "committed" {
		t.Fatalf("plan %s", s)
	}
	var purpose, endpoint, relied string
	if err := le.db.QueryRow(`SELECT s.purpose, s.endpoint, r.observation FROM adoption_record r
		JOIN observation o ON o.id = r.observation JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis
		WHERE r.plan = $1 AND s.plan = $1`, le.pid).Scan(&purpose, &endpoint, &relied); err != nil {
		t.Fatal(err)
	}
	if purpose != "evidence" || endpoint != le.node.Endpoint {
		t.Fatalf("relied on a %s observation at %s", purpose, endpoint)
	}
}

// A refused adoption ends the loop's attempts for that plan: one observation and one refusal
// entry, however many passes follow.
func TestAdopterRefusalEndsAttempts(t *testing.T) {
	t.Parallel()
	le := newLoopEnv(t, "")
	le.node.SetSMBIOSUUID(t, "1b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b", true) // another machine at the route
	le.approveIt(t)
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	a := le.adopter(t, life, config.Ingestion{Heartbeat: 20 * time.Millisecond, Lease: 50 * time.Millisecond}, idle)
	a.startAdopter()
	defer stopping(a, stop)
	waitIdle(t, idle, 1)
	time.Sleep(100 * time.Millisecond) // past a lease
	waitIdle(t, idle, 3)
	if s, r := le.starts(t); s != 1 || r != 1 {
		t.Fatalf("%d observations and %d refusal entries for the plan", s, r)
	}
	if s := le.planState(t); s != "approved" {
		t.Fatalf("plan %s", s)
	}
}

// An observation that cannot read the node records no refusal: the loop observes the plan again
// no sooner than a lease later, and adopts once the read succeeds.
func TestAdopterRetriesUnread(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		fail func(le *loopEnv, failing bool) // makes the read fail, or recover
	}{
		{"access", func(le *loopEnv, failing bool) {
			le.x.mu.Lock()
			defer le.x.mu.Unlock()
			le.x.err = nil
			if failing {
				le.x.err = provider.ErrUnavailable
			}
		}},
		{"identity", func(le *loopEnv, failing bool) { // an answered failure: the later reads still run
			le.node.Fail(talostest.IdentityType, map[bool]codes.Code{true: codes.Internal, false: codes.OK}[failing])
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			le := newLoopEnv(t, "")
			c.fail(le, true)
			le.approveIt(t)
			life, stop := context.WithCancel(t.Context())
			idle := make(chan struct{})
			lease := 300 * time.Millisecond
			a := le.adopter(t, life, config.Ingestion{Heartbeat: 20 * time.Millisecond, Lease: lease}, idle)
			start := time.Now()
			a.startAdopter()
			defer stopping(a, stop)
			waitIdle(t, idle, 3)
			if time.Since(start) < lease {
				if s, r := le.starts(t); s != 1 || r != 0 {
					t.Fatalf("within a lease: %d observations and %d refusal entries", s, r)
				}
			}
			c.fail(le, false)
			deadline := time.Now().Add(10 * time.Second)
			for le.planState(t) != "committed" {
				if time.Now().After(deadline) {
					t.Fatal("the plan was not adopted after the read recovered")
				}
				waitIdle(t, idle, 1)
			}
			if s, r := le.starts(t); s < 2 || r != 0 {
				t.Fatalf("%d observations and %d refusal entries", s, r)
			}
		})
	}
}

// A plan waiting for its retry keeps its time while its scope is closed for a pass: reopening the
// scope does not observe it again before a lease has passed.
func TestAdopterRetryOutlivesClosedScope(t *testing.T) {
	t.Parallel()
	le := newLoopEnv(t, "")
	le.node.Fail(talostest.IdentityType, codes.Internal)
	le.approveIt(t)
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	lease := 2 * time.Second
	a := le.adopter(t, life, config.Ingestion{Heartbeat: 20 * time.Millisecond, Lease: lease}, idle)
	start := time.Now()
	a.startAdopter()
	defer stopping(a, stop)
	waitIdle(t, idle, 2)
	mustExec(t, le.db, `UPDATE installation_state SET recovery_mode = true`)
	waitIdle(t, idle, 3)
	mustExec(t, le.db, `UPDATE installation_state SET recovery_mode = false`)
	waitIdle(t, idle, 3)
	if time.Since(start) >= lease {
		t.Skip("the passes took longer than a lease")
	}
	if s, r := le.starts(t); s != 1 || r != 0 {
		t.Fatalf("within a lease: %d observations and %d refusal entries", s, r)
	}
}

// A refusal whose refusal entry could not be recorded is retried no sooner than a lease later,
// and recorded once the entry can be.
func TestAdopterUnrecordedRefusal(t *testing.T) {
	t.Parallel()
	le := newLoopEnv(t, "")
	le.node.SetSMBIOSUUID(t, "1b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b", true) // another machine at the route
	mustExec(t, le.db, `CREATE FUNCTION test_fail_refusal() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN RAISE EXCEPTION 'refusal entries fail in this test'; END $$`)
	mustExec(t, le.db, `CREATE TRIGGER test_fail_refusal BEFORE INSERT ON machine_event FOR EACH ROW
		WHEN (NEW.kind = 'refusal') EXECUTE FUNCTION test_fail_refusal()`)
	le.approveIt(t)
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{})
	lease := 2 * time.Second
	a := le.adopter(t, life, config.Ingestion{Heartbeat: 20 * time.Millisecond, Lease: lease}, idle)
	start := time.Now()
	a.startAdopter()
	defer stopping(a, stop)
	waitIdle(t, idle, 5)
	if time.Since(start) < lease {
		if s, r := le.starts(t); s != 1 || r != 0 {
			t.Fatalf("within a lease: %d observations and %d refusal entries", s, r)
		}
	}
	mustExec(t, le.db, `DROP TRIGGER test_fail_refusal ON machine_event`)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, r := le.starts(t); r == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the refusal was not recorded after its entry could be")
		}
		waitIdle(t, idle, 1)
	}
	if s, r := le.starts(t); s != 2 || r != 1 {
		t.Fatalf("%d observations and %d refusal entries", s, r)
	}
}

// The loop observes no plan it cannot adopt now, and records no refusal for it: an expired plan,
// one whose approver's identity is revoked, one approved in an earlier epoch, a plan of another
// kind, or one whose machine's scope is closed by an operation holding it or by recovery mode
// (requirement 4.5 is waited for, not refused).
func TestAdopterSkips(t *testing.T) {
	t.Parallel()
	applyPlan := func(other *string) func(*planEnv) {
		return func(p *planEnv) {
			*other = decode[planBody](t, p.plan(p.robot, "k-apply-0123456789", applyBody(p.target.rel, p.machine, "")),
				http.StatusCreated).ID
			decode[approvalBody](t, p.approve(p.human("h-approver"), "k-approve-apply-0123", *other), http.StatusCreated)
		}
	}
	for _, c := range []struct {
		name, extra string
		apply       bool                                          // an approved apply-config plan of the machine
		setup       func(t *testing.T, le *loopEnv, other string) // after the plans, before the loop starts
	}{
		{"expired", `,"expiresInSeconds":1`, false, func(t *testing.T, le *loopEnv, _ string) {
			le.approveIt(t)
			time.Sleep(time.Until(le.expires.Add(100 * time.Millisecond)))
		}},
		{"approver revoked", "", false, func(t *testing.T, le *loopEnv, _ string) {
			le.approveIt(t)
			revocationOf(t, revoke(le.env, le.human("h-recovery"), le.key(),
				`{"identity":"`+le.principalOf("h-approver")+`","reason":"left"}`))
		}},
		{"earlier epoch", "", false, func(t *testing.T, le *loopEnv, _ string) {
			le.approveIt(t)
			newEpoch(t, le.db)
		}},
		{"another kind", "", true, func(*testing.T, *loopEnv, string) {}},
		{"scope held", "", true, func(t *testing.T, le *loopEnv, other string) {
			le.approveIt(t)
			le.commitAs(t, other)
		}},
		{"recovery mode", "", false, func(t *testing.T, le *loopEnv, _ string) {
			le.approveIt(t)
			mustExec(t, le.db, `UPDATE installation_state SET recovery_mode = true`)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var other string
			var before []func(*planEnv)
			if c.apply {
				before = append(before, applyPlan(&other))
			}
			le := newLoopEnv(t, c.extra, before...)
			c.setup(t, le, other)
			life, stop := context.WithCancel(t.Context())
			idle := make(chan struct{})
			a := le.adopter(t, life, config.Ingestion{Heartbeat: 20 * time.Millisecond, Lease: 50 * time.Millisecond}, idle)
			a.startAdopter()
			defer stopping(a, stop)
			waitIdle(t, idle, 3)
			if s, r := count(t, le.db, `SELECT count(*) FROM observation_start WHERE machine = $1`, le.machine),
				count(t, le.db, `SELECT count(*) FROM machine_event WHERE kind = 'refusal'`); s != 0 || r != 0 {
				t.Fatalf("%d observations and %d refusal entries", s, r)
			}
		})
	}
}

// A loop whose epoch is superseded ends (persistence-api.md §5.1).
func TestAdopterEpoch(t *testing.T) {
	t.Parallel()
	le := newLoopEnv(t, "")
	life, stop := context.WithCancel(t.Context())
	defer stop()
	a := le.adopter(t, life, config.Ingestion{Heartbeat: 20 * time.Millisecond, Lease: time.Minute}, make(chan struct{}, 100))
	newEpoch(t, le.db)
	a.startAdopter()
	done := make(chan struct{})
	go func() {
		a.d.runs.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the adoption loop still runs in a superseded epoch")
	}
	if !le.logged("superseded") {
		t.Fatal("no log line names the superseded epoch")
	}
}

// New starts the adoption loop of a controller with an executor, and none without one.
func TestNewStartsAdopter(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		exe  bool
		want string
	}{{"executor", true, "committed"}, {"none", false, "approved"}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			le := newLoopEnv(t, "")
			var x Executor
			if c.exe {
				x = le.x
			}
			life, stop := context.WithCancel(t.Context())
			h := New(life, le.db, auth.NewVerifier(le.cfg, le.db, auth.Discover(le.cfg.OIDC)), le.cfg, testExecution(), nil, nil, x,
				&config.Ingestion{Instance: "a", Heartbeat: 20 * time.Millisecond, Lease: time.Minute}, epoch(t, le.db))
			defer stopping(h.(*API), stop)
			le.approveThrough(t, h.(*API))
			deadline := time.Now().Add(2 * time.Second)
			for le.planState(t) != c.want && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			time.Sleep(200 * time.Millisecond) // a loop that should not run has had its passes
			if s := le.planState(t); s != c.want {
				t.Fatalf("plan %s", s)
			}
		})
	}
}
