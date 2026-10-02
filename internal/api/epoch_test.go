package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
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
