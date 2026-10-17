package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/staging"
)

// §7.2: a recovery-mode entry that commits while a replay is being decided makes the record an
// earlier epoch's, so the request is 409, never the stored success. The decision waits on the
// entry's lock of the installation state.
func TestReplayWaitsForEpochChange(t *testing.T) {
	e := newEnv(t, options{extra: []*route{testRoute(nil)}})
	tok := e.human("h-author")
	if rec := e.do(e.api, post(tok, key, `{"note":"a"}`)); rec.Code != http.StatusCreated {
		t.Fatalf("first: %d %s", rec.Code, rec.Body)
	}
	tx, err := e.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	ep := id.New(id.Epoch)
	if _, err := tx.Exec("INSERT INTO recovery_epoch (epoch, entered_at) VALUES ($1, now())", ep); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE installation_state SET epoch = $1", ep); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- e.do(e.api, post(tok, key, `{"note":"a"}`)) }()
	dbtest.WaitForLockWait(t, e.db)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rec := <-done
	wantProblem(t, rec, http.StatusConflict, "conflict")
	if rec.Header().Get("Bronzeward-Epoch") != ep {
		t.Fatalf("epoch header %q; want the epoch the decision was made in, %s", rec.Header().Get("Bronzeward-Epoch"), ep)
	}
}

// §10.2: a token from an earlier epoch is refused. A request whose token was verified in the
// epoch before an entry that committed before routing is 401, not served in the new epoch.
func TestServiceTokenEpochRecheckedAtRouting(t *testing.T) {
	e := newEnv(t, options{})
	old := epoch(t, e.db)
	newEpoch(t, e.db)
	a := newAPI(e.db, stubAuthn{p: auth.Principal{Kind: auth.Service, ID: e.robotID, Epoch: old,
		Roles: []auth.Role{auth.Author, auth.Publisher}}}, e.cfg, deps{}, options{logf: e.logf})
	rec := e.do(a, call{method: "GET", path: prefix + "/acts", token: "x"})
	wantProblem(t, rec, http.StatusUnauthorized, "unauthenticated")
	if rec.Header().Get("Bronzeward-Epoch") != "" {
		t.Fatal("an earlier epoch's token was told the epoch")
	}
	// Control: the same principal in the current epoch is served.
	a = newAPI(e.db, stubAuthn{p: auth.Principal{Kind: auth.Service, ID: e.robotID, Epoch: epoch(t, e.db),
		Roles: []auth.Role{auth.Author, auth.Publisher}}}, e.cfg, deps{}, options{logf: e.logf})
	if rec := e.do(a, call{method: "GET", path: prefix + "/acts", token: "x"}); rec.Code != http.StatusOK {
		t.Fatalf("current epoch: %d %s", rec.Code, rec.Body)
	}
}

// ... and an entry that commits after routing, before the transaction, refuses the mutation
// under the transaction's lock of the installation state: nothing commits.
func TestServiceTokenEpochRecheckedInTransaction(t *testing.T) {
	rt := testRoute(nil)
	var e *env
	rt.prepare = func(context.Context, *API, *request) error { newEpoch(t, e.db); return nil }
	e = newEnv(t, options{extra: []*route{rt}})
	wantProblem(t, e.do(e.api, post(e.robot, key, `{}`)), http.StatusUnauthorized, "unauthenticated")
	if acts, records := rows(t, e); acts != 0 || records != 0 {
		t.Fatalf("acts %d, records %d", acts, records)
	}
}

// PA §5.1's process-epoch comparison and its control (AP S7 "Negative controls"): a process started
// before a recovery-mode entry claims no publish job, takes over no operation, starts no ingestion
// and commits no plan after it; the control that drops `$my_epoch = $current_epoch` lets each pass.
// The commitment's plan is approved in the new epoch, so the comparison is the only one refusing it.
func TestProcessEpochControl(t *testing.T) {
	t.Parallel()
	for _, control := range []bool{false, true} {
		name := "as shipped"
		if control {
			name = "without the process-epoch comparison"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			o := options{noProcessEpoch: control}
			t.Run("job-claim", func(t *testing.T) {
				t.Parallel()
				d := newDraftEnv(t)
				a := d.publisher(o)
				op := d.queuePublish(1)
				newEpoch(t, d.db)
				_, ok, err := a.claimPublish(t.Context())
				want := "queued"
				switch {
				case control && (err != nil || !ok):
					t.Fatalf("the claim with the control: %v, %v", ok, err)
				case control:
					want = "running"
				case !errors.Is(err, staging.ErrEpochSuperseded):
					t.Fatalf("the claim in a superseded epoch: %v", err)
				}
				var state string
				if err := d.db.QueryRow(`SELECT state FROM operation WHERE id = $1`, op).Scan(&state); err != nil || state != want {
					t.Fatalf("operation %s, %v; want %s", state, err, want)
				}
			})
			t.Run("takeover", func(t *testing.T) {
				t.Parallel()
				ae := newAttemptEnv(t, "")
				pre := observer(t, ae.env, &fakeExecutor{}, o)
				newEpoch(t, ae.db)
				rec := ae.post(pre, "take-1", "/takeovers", `{"generation":1,"reason":"the controller was lost"}`)
				state, gen, owner := ae.state(t)
				if !control {
					wantProblem(t, rec, http.StatusServiceUnavailable, "epoch-superseded")
					if state == "unresolved" || gen != 1 {
						t.Fatalf("a superseded process moved the operation to %s at generation %d", state, gen)
					}
					return
				}
				decode[takeoverBody](t, rec, http.StatusCreated)
				if state != "unresolved" || gen != 2 || owner != pre.d.owner.ID {
					t.Fatalf("the operation is %s at generation %d owned by %s", state, gen, owner)
				}
				// The control is the takeover's alone: every other act on the operation still compares.
				tx, err := ae.db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback() }()
				if _, err := pre.lockOperation(t.Context(), tx, ae.op); !errors.Is(err, errNotController) {
					t.Fatalf("another act's lock by a superseded process: %v", err)
				}
			})
			t.Run("ingestion-start", func(t *testing.T) {
				t.Parallel()
				ie := newIngestEnv(t, o)
				old := epoch(t, ie.db)
				newEpoch(t, ie.db)
				rec := ie.do(ie.api, ingestCall(ie.human("h-author"), key, ie.etag, ie.body(nil)))
				if !control {
					wantProblem(t, rec, http.StatusServiceUnavailable, "epoch-superseded")
					return
				}
				if rec.Code != http.StatusAccepted {
					t.Fatalf("the ingestion start with the control: %d %s", rec.Code, rec.Body)
				}
				// The superseded process's write: the claim is owned under its own, earlier epoch.
				if n := count(t, ie.db, `SELECT count(*) FROM staging_claim WHERE owner_epoch = $1`, old); n != 1 {
					t.Fatalf("%d claims under the superseded epoch", n)
				}
			})
			t.Run("commitment", func(t *testing.T) {
				t.Parallel()
				ce := newCommitEnv(t, "")
				pre := observer(t, ce.env, &fakeExecutor{}, o)
				newEpoch(t, ce.db)
				ce.a = observer(t, ce.env, &fakeExecutor{}, options{})
				ce.approveIt(t)
				ce.evidence(t, ce.pid, func(s *evSeed) { s.controller = pre.d.owner.ID })
				_, err := pre.commitPlan(context.Background(), ce.pid)
				switch {
				case control && err != nil:
					t.Fatalf("the commitment with the control: %v", err)
				case !control && !errors.Is(err, errNotController):
					t.Fatalf("the commitment by a superseded process: %v", err)
				}
				want := 0
				if control {
					want = 1
				}
				if n := count(t, ce.db, `SELECT count(*) FROM plan_state WHERE plan = $1 AND state = 'committed'`, ce.pid); n != want {
					t.Fatalf("%d committed plans, want %d", n, want)
				}
				if !control {
					if n := count(t, ce.db, `SELECT count(*) FROM machine_event WHERE kind IN ('refusal', 'commitment')`); n != 0 {
						t.Fatalf("%d refusal or commitment entries by a superseded process", n)
					}
				}
			})
		})
	}
}

// Comparison 6's scope gate and its control (execution-recovery.md §7.5, AP S7 "Negative controls"):
// in recovery mode a plan approved in the new epoch on a `ready` scope is refused until the scope's
// release, and committed with the gate removed. The executor's dispatch filter waits for the release
// rather than have the commitment refused, so the gate is reached by committing the plan directly.
func TestCommitmentScopeGateControl(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T, o options) *commitEnv {
		ce := newCommitEnv(t, "")
		newEpoch(t, ce.db)
		ce.a = observer(t, ce.env, &fakeExecutor{}, o)
		ce.ready(t)
		mustExec(t, ce.db, `UPDATE installation_state SET recovery_mode = true`)
		mustExec(t, ce.db, `UPDATE machine SET scope_state = 'ready' WHERE id = $1`, ce.machine)
		return ce
	}
	committed := func(t *testing.T, ce *commitEnv, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if n := count(t, ce.db, `SELECT count(*) FROM plan_state WHERE plan = $1 AND state = 'committed'`, ce.pid); n != 1 {
			t.Fatal("the plan was not committed")
		}
	}
	t.Run("refused until the release", func(t *testing.T) {
		t.Parallel()
		ce := setup(t, options{})
		_, err := ce.a.commitPlan(context.Background(), ce.pid)
		ce.wantRefused(t, err, "6", "the machine scope is ready", "")
		mustExec(t, ce.db, `UPDATE machine SET scope_state = 'released' WHERE id = $1`, ce.machine)
		_, err = ce.a.commitPlan(context.Background(), ce.pid)
		committed(t, ce, err)
	})
	t.Run("without the scope gate", func(t *testing.T) {
		t.Parallel()
		ce := setup(t, options{noScopeGate: true})
		_, err := ce.a.commitPlan(context.Background(), ce.pid)
		committed(t, ce, err)
	})
	t.Run("the dispatch filter waits for the release", func(t *testing.T) {
		t.Parallel()
		ce := setup(t, options{})
		if cs, err := ce.a.dispatchCandidates(context.Background(), ce.pid); err != nil || len(cs) != 0 {
			t.Fatalf("candidates on a ready scope: %v, %v", cs, err)
		}
		mustExec(t, ce.db, `UPDATE machine SET scope_state = 'released' WHERE id = $1`, ce.machine)
		if cs, err := ce.a.dispatchCandidates(context.Background(), ce.pid); err != nil || len(cs) != 1 {
			t.Fatalf("candidates on the released scope: %v, %v", cs, err)
		}
	})
}
