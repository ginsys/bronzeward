package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/database"
	"github.com/ginsys/bronzeward/internal/staging"
)

// seeded names the records the e2e harness's browser cases read (web/e2e/auth.conf.ts serves this
// as JSON): one cluster of two machines and its release, and one plan of the target release in each
// state a plan page shows. Committed is the plan of Operation, whose first attempt is recorded, or
// with no attempt where the seed is asked for none (BW_E2E_SEED_ATTEMPT=none): a resolution to
// cancelled is refused once an attempt exists (recovery_acts.go), so the browser cases that cancel
// the operation read the seed without one.
type seeded struct {
	Cluster   string `json:"cluster"`
	Release   string `json:"release"`
	Machine   string `json:"machine"`  // the committed plan's
	Machine2  string `json:"machine2"` // the others'
	Operation string `json:"operation"`
	Plans     struct {
		Proposed  string `json:"proposed"`
		Approved  string `json:"approved"`
		Committed string `json:"committed"`
		Expired   string `json:"expired"`
		Cancelled string `json:"cancelled"`
		Revoked   string `json:"revoked"`
	} `json:"plans"`
	// Observations are the recovery observations of the accounting cases (BW_E2E_SEED_OBSERVATIONS=1), absent otherwise.
	Observations *seededObservations `json:"observations,omitempty"`
}

// seededObservations names the recovery observations written for the accounting of a scope and of an attempt, with the shifts
// TestRecoveryAccounting uses. The seeded attempt's transport deadline is one minute ahead, so the committed plan's machine has
// First as the first read after it (the one start the server accepts), Later after that (a start it refuses) and End after the
// settle floor; the other machine has no attempt, and has Start, Soon (within the settle floor of it, reading another resource
// version) and End (a version that moved).
type seededObservations struct {
	Machine struct {
		First string `json:"first"`
		Later string `json:"later"`
		End   string `json:"end"`
	} `json:"machine"`
	Machine2 struct {
		Start string `json:"start"`
		Soon  string `json:"soon"`
		End   string `json:"end"`
	} `json:"machine2"`
}

// seedObservations writes the recovery observations of seededObservations. The accounting decision reads them after a recovery
// entry whose stop is older than they are, so a browser case enters recovery with a stop an hour back.
func seedObservations(t *testing.T, ae *attemptEnv) *seededObservations {
	t.Helper()
	o := &seededObservations{}
	o.Machine.First = ae.recoveryRead(t, ae.machine, 3*time.Minute, "rv1")
	o.Machine.Later = ae.recoveryRead(t, ae.machine, 5*time.Minute, "rv1")
	o.Machine.End = ae.recoveryRead(t, ae.machine, 6*time.Minute, "rv1")
	o.Machine2.Start = ae.recoveryRead(t, ae.machine2, 5*time.Minute, "rv1")
	o.Machine2.Soon = ae.recoveryRead(t, ae.machine2, 5*time.Minute+10*time.Second, "rv2")
	o.Machine2.End = ae.recoveryRead(t, ae.machine2, 6*time.Minute, "rv2")
	return o
}

// seedPlans makes ae's records into one plan per state. The committed plan goes first: its
// operation holds the cluster's rollout scope, so a running controller dispatches none of the
// others. With no attempt (attempt false) the operation is itself one a controller might dispatch:
// TestE2ESeedWithoutAttemptSurvivesARunningController proves it does not. Only the proposed plan is left for a browser sign-in to approve, and it is made by the
// automation identity, so any approver other than that identity may.
func seedPlans(t *testing.T, ae *attemptEnv, attempt bool) seeded {
	t.Helper()
	if attempt {
		ae.seedAttempt(t)
	}
	ae.second(t)
	s := seeded{Cluster: ae.cluster, Release: ae.target.rel, Machine: ae.machine, Machine2: ae.machine2, Operation: ae.op}
	s.Plans.Committed, s.Plans.Approved = ae.pid, ae.plan2
	fresh := func(k string) string {
		return decode[planBody](t, ae.plan(ae.robot, "k-seed-"+k+"-0123456789", applyBody(ae.target.rel, ae.machine2, "")), http.StatusCreated).ID
	}
	s.Plans.Proposed = fresh("proposed")
	s.Plans.Expired = fresh("expired")
	ae.expirePlan(t, s.Plans.Expired, 0)
	s.Plans.Cancelled = fresh("cancelled")
	if rec := ae.cancel(ae.robot, "k-seed-cancel-0123456789", s.Plans.Cancelled, `{"reason":"seeded"}`); rec.Code != http.StatusOK { // a cancellation answers 200, a revocation 201
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	s.Plans.Revoked = fresh("revoked")
	apr := decode[approvalBody](t, ae.approve(ae.human("h-approver"), "k-seed-approve-0123456789", s.Plans.Revoked), http.StatusCreated).ID
	if rec := ae.revokeApproval(ae.human("h-recovery"), "k-seed-revoke-0123456789", apr, `{"reason":"seeded"}`); rec.Code != http.StatusCreated {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	return s
}

// useDatabase points newEnvWith at db until the test ends.
func useDatabase(t *testing.T, db *sql.DB) {
	t.Helper()
	was := testDB
	testDB = func(*testing.T) *sql.DB { return db }
	t.Cleanup(func() { testDB = was })
}

// TestE2ESeed is the e2e harness's seeding step (ginsys/bronzeward#822): with BW_E2E_SEED_DSN naming
// a migrated database, it writes the seeded records there and their identifiers to BW_E2E_SEED_OUT.
// It skips otherwise.
func TestE2ESeed(t *testing.T) {
	dsn, out := os.Getenv("BW_E2E_SEED_DSN"), os.Getenv("BW_E2E_SEED_OUT")
	if dsn == "" || out == "" {
		t.Skip("BW_E2E_SEED_DSN and BW_E2E_SEED_OUT unset: this is the browser harness's step")
	}
	db, err := database.Open(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	useDatabase(t, db)
	ae := newAttemptEnv(t, "")
	s := seedPlans(t, ae, os.Getenv("BW_E2E_SEED_ATTEMPT") != "none")
	if os.Getenv("BW_E2E_SEED_OBSERVATIONS") == "1" {
		s.Observations = seedObservations(t, ae)
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// shown is what a plan page and an operation page read: the plans' states and approvals, the
// operations and their attempts.
func shown(t *testing.T, db *sql.DB) string {
	t.Helper()
	var s string
	if err := db.QueryRow(`SELECT coalesce(string_agg(x, E'\n' ORDER BY x), '') FROM (
			SELECT row_to_json(s)::text x FROM plan_state s UNION ALL SELECT row_to_json(a)::text FROM approval a
			UNION ALL SELECT row_to_json(o)::text FROM operation o UNION ALL SELECT row_to_json(t)::text FROM attempt t) q`).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// The seed is written after the disposable deployment's controller has started, which takes over
// every non-terminal operation another process owned at its first pass (execution-recovery.md §3.4)
// and dispatches any approved plan whose scope is open. This runs that controller's executor and
// drift loops over the database, lets the first pass end, seeds, and then runs many passes more:
// every plan keeps its state, and the operation keeps its owner, its state and its one attempt. A
// controller that moved a seeded record would fail here, on every CI run.
func TestE2ESeedSurvivesARunningController(t *testing.T) { seedSurvives(t, true) }

// The seed with no attempt (BW_E2E_SEED_ATTEMPT=none) has the same proof: its committed operation
// is still its seeded owner's and not unresolved, and no controller pass wrote an attempt for it.
func TestE2ESeedWithoutAttemptSurvivesARunningController(t *testing.T) { seedSurvives(t, false) }

func seedSurvives(t *testing.T, attempt bool) {
	db := migrated(t)
	useDatabase(t, db)
	idle := make(chan struct{}, 1000)
	var execPasses, driftPasses atomic.Int64
	// The controller has no environment of its own: a second one on this database would issue the
	// automation token the seed's environment issues, and the principal's name is unique.
	cfg := testAuth("http://127.0.0.1:1")
	var d deps
	d.exe = &fakeExecutor{}
	life, stop := context.WithCancel(t.Context())
	d.life, d.runs = life, &sync.WaitGroup{}
	d.timers = config.Ingestion{Heartbeat: 10 * time.Millisecond, Lease: 10 * time.Millisecond}
	d.exec = testExecution()
	d.exec.DriftInterval = 10 * time.Millisecond
	d.owner = staging.Owner{ID: "live/1/" + rand.Text(), Epoch: epoch(t, db)}
	a := newAPI(db, auth.NewVerifier(cfg, db, auth.Discover(cfg.OIDC)), cfg, d, options{logf: t.Logf, onDriftIdle: func() { driftPasses.Add(1) }, onExecIdle: func() {
		execPasses.Add(1)
		select {
		case idle <- struct{}{}:
		default:
		}
	}})
	a.startExecutor()
	a.startDrifter()
	t.Cleanup(func() { stop(); d.runs.Wait() })
	waitIdle(t, idle, 1) // the first pass, whose takeover reads the operations that exist then

	ae := newAttemptEnv(t, "")
	s := seedPlans(t, ae, attempt)
	execFrom, driftFrom := execPasses.Load(), driftPasses.Load() // passes before this point may have read nothing of the seed
	before := shown(t, db)
	if before == "" {
		t.Fatal("nothing seeded")
	}
	for len(idle) > 0 { // passes that ended before the seed was written
		<-idle
	}
	waitIdle(t, idle, 2) // the first signal may be of a pass begun before the drain, the second is not
	for range 30 {
		a.wakeExecutor()
		waitIdle(t, idle, 1)
	}
	// The drift loop's passes: two, because one begun before driftFrom was read ends after it and may have
	// read nothing of the seed (as the executor's first signal above). A wait for the counter, bounded, not
	// a sleep: a loaded runner can starve the loop's ticker for longer than any fixed time.
	for deadline := time.Now().Add(10 * time.Second); driftPasses.Load()-driftFrom < 2 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	execRan, driftRan := execPasses.Load()-execFrom, driftPasses.Load()-driftFrom
	t.Logf("passes after the seed: executor %d, drift %d", execRan, driftRan)
	if execRan < 30 || driftRan < 2 {
		t.Fatalf("only %d executor and %d drift passes ran after the seed: the proof below would say nothing", execRan, driftRan)
	}
	if after := shown(t, db); after != before {
		t.Fatalf("a running controller changed the seeded records:\nbefore\n%s\nafter\n%s", before, after)
	}
	for state, plan := range map[string]string{"proposed": s.Plans.Proposed, "approved": s.Plans.Approved, "committed": s.Plans.Committed,
		"expired": s.Plans.Expired, "cancelled": s.Plans.Cancelled, "revoked": s.Plans.Revoked} {
		got := decode[planBody](t, ae.do(ae.a, call{method: "GET", path: prefix + "/plans/" + plan, token: ae.human("h-viewer")}), http.StatusOK)
		if got.State != state {
			t.Errorf("plan %s reads %s, want %s", plan, got.State, state)
		}
	}
	// The snapshot above was taken after the seed, so a takeover between the two would sit in both:
	// the operation must still be its seeded owner's and not unresolved.
	var owner, state string
	if err := db.QueryRow(`SELECT coalesce(owner, ''), state FROM operation WHERE id = $1`, s.Operation).Scan(&owner, &state); err != nil {
		t.Fatal(err)
	}
	if owner == d.owner.ID || state == "unresolved" {
		t.Errorf("the controller took over the seeded operation: owner %s, state %s", owner, state)
	}
	want := 0
	if attempt {
		want = 1
	}
	if n := count(t, db, `SELECT count(*) FROM attempt WHERE operation = $1`, s.Operation); n != want {
		t.Errorf("the operation has %d attempts, want %d", n, want)
	}
}

// TestE2EDrift is the e2e harness's drift step (ginsys/bronzeward#363): with BW_E2E_DRIFT_DSN naming the
// disposable deployment's database and BW_E2E_DRIFT_MACHINE a machine in it, it opens a drift record on that
// machine as a drift observation's recording transaction does (openDriftOn). The deployment's detector runs
// every 5 minutes and the stand-in nodes' configuration is fixed, so no read of one ever differs from Applied:
// a case that needs a record the server itself binds to a plan opens it here. It skips otherwise.
func TestE2EDrift(t *testing.T) {
	dsn, machine := os.Getenv("BW_E2E_DRIFT_DSN"), os.Getenv("BW_E2E_DRIFT_MACHINE")
	if dsn == "" || machine == "" {
		t.Skip("BW_E2E_DRIFT_DSN and BW_E2E_DRIFT_MACHINE unset: this is the browser harness's step")
	}
	db, err := database.Open(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	openDriftOn(t, db, machine)
}
