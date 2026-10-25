package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
)

// wantEpochMismatch checks §9.4's 412 epoch-mismatch: it names the current epoch, in the body and
// in Bronzeward-Epoch.
func wantEpochMismatch(t *testing.T, rec *httptest.ResponseRecorder, current string) {
	t.Helper()
	doc := wantProblem(t, rec, http.StatusPreconditionFailed, "epoch-mismatch")
	if doc["epoch"] != current || rec.Header().Get("Bronzeward-Epoch") != current {
		t.Fatalf("epoch %v, header %q; want %s", doc["epoch"], rec.Header().Get("Bronzeward-Epoch"), current)
	}
}

// §7.2: a mutation naming the current epoch runs as one without the header; one naming another is
// refused 412 and commits nothing; one without the header is unchanged. Shown on a §9.2 route.
func TestExpectedEpoch(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	cur := epoch(t, e.db)
	stale := clusterCall(author, "k-expected-stale-0123")
	stale.expected = id.New(id.Epoch)
	wantEpochMismatch(t, e.do(e.api, stale), cur)
	if acts, records := rows(t, e); acts != 0 || records != 0 || count(t, e.db, "SELECT count(*) FROM cluster") != 0 {
		t.Fatalf("a refused mutation committed: acts %d, records %d", acts, records)
	}
	match := clusterCall(author, "k-expected-match-0123")
	match.expected = cur
	if rec := e.do(e.api, match); rec.Code != http.StatusCreated {
		t.Fatalf("matching: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(e.api, match); rec.Code != http.StatusCreated || rec.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("matching replay: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	if rec := e.do(e.api, clusterCall(author, "k-expected-none-01234")); rec.Code != http.StatusCreated {
		t.Fatalf("no header: %d %s", rec.Code, rec.Body)
	}
	// A present header must name an epoch: an empty one is not taken as absent, which would skip
	// the check the client asked for.
	for _, vs := range [][]string{{""}, {cur, cur}, {"", cur}} {
		c := clusterCall(author, "k-expected-bad-"+id.New(id.Request))
		c.expectedValues = vs
		wantProblem(t, e.do(e.api, c), http.StatusBadRequest, "invalid-request")
	}
	if acts, records := rows(t, e); acts != 2 || records != 2 {
		t.Fatalf("a malformed header committed: acts %d, records %d", acts, records)
	}
}

// AMB-07 and AMB-12: after a recovery entry, a repeat whose record survived still answers the
// earlier epoch's 409; one whose record a restore removed is refused 412 instead of executing
// afresh. Without the header it executes afresh, the residual the header closes (§12.4).
func TestExpectedEpochAfterRestore(t *testing.T) {
	e := newEnv(t, options{extra: []*route{testRoute(nil)}})
	tok := e.human("h-author")
	before := epoch(t, e.db)
	first := post(tok, key, `{"note":"a"}`)
	first.expected = before
	if rec := e.do(e.api, first); rec.Code != http.StatusCreated {
		t.Fatalf("first: %d %s", rec.Code, rec.Body)
	}
	after := newEpoch(t, e.db)
	wantProblem(t, e.do(e.api, first), http.StatusConflict, "conflict")
	// A restore to a backup taken before the first attempt removed its record.
	mustExec(t, e.db, "ALTER TABLE idempotency_record DISABLE TRIGGER immutable_rows")
	mustExec(t, e.db, "DELETE FROM idempotency_record")
	wantEpochMismatch(t, e.do(e.api, first), after)
	if acts, records := rows(t, e); acts != 1 || records != 0 {
		t.Fatalf("acts %d, records %d; the refused repeat committed", acts, records)
	}
	if rec := e.do(e.api, post(tok, key, `{"note":"a"}`)); rec.Code != http.StatusCreated {
		t.Fatalf("no header: %d %s", rec.Code, rec.Body)
	}
}

// An entry that commits between the request's arrival and its transaction is caught by the
// transaction's own lookup; one that commits while a lookup, the first or the transaction's, waits
// on the installation state is caught when the wait ends. Nothing commits in any case.
func TestExpectedEpochConcurrentEntry(t *testing.T) {
	rt := testRoute(nil)
	var e *env
	rt.prepare = func(context.Context, *API, *request) error { newEpoch(t, e.db); return nil }
	e = newEnv(t, options{extra: []*route{rt}})
	tok := e.human("h-author")
	c := post(tok, key, `{"note":"a"}`)
	c.expected = epoch(t, e.db)
	rec := e.do(e.api, c)
	wantEpochMismatch(t, rec, epoch(t, e.db))
	if acts, records := rows(t, e); acts != 0 || records != 0 {
		t.Fatalf("acts %d, records %d", acts, records)
	}

	e = newEnv(t, options{extra: []*route{testRoute(nil)}})
	tok = e.human("h-author")
	e.recordHuman("h-author")
	c = post(tok, key, `{"note":"a"}`)
	c.expected = epoch(t, e.db)
	tx, err := e.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	ep := id.New(id.Epoch)
	mustExec(t, tx, "INSERT INTO recovery_epoch (epoch, entered_at) VALUES ($1, now())", ep)
	mustExec(t, tx, "UPDATE installation_state SET epoch = $1", ep)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- e.do(e.api, c) }()
	dbtest.WaitForLockWait(t, e.db)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	wantEpochMismatch(t, <-done, ep)
	if acts, records := rows(t, e); acts != 0 || records != 0 {
		t.Fatalf("acts %d, records %d", acts, records)
	}

	// The entry starts after the first lookup passed, and commits while the transaction's own lookup
	// waits on it.
	rt = testRoute(nil)
	held := make(chan *sql.Tx, 1)
	ep = id.New(id.Epoch)
	rt.prepare = func(context.Context, *API, *request) error {
		tx, err := e.db.BeginTx(t.Context(), nil)
		if err != nil {
			return err
		}
		held <- tx // the request's goroutine: report a failure, never t.Fatal
		if _, err := tx.Exec("INSERT INTO recovery_epoch (epoch, entered_at) VALUES ($1, now())", ep); err != nil {
			return err
		}
		_, err = tx.Exec("UPDATE installation_state SET epoch = $1", ep)
		return err
	}
	e = newEnv(t, options{extra: []*route{rt}})
	tok = e.human("h-author")
	e.recordHuman("h-author")
	c = post(tok, key, `{"note":"a"}`)
	c.expected = epoch(t, e.db)
	go func() { done <- e.do(e.api, c) }()
	tx = <-held
	defer func() { _ = tx.Rollback() }()
	dbtest.WaitForLockWait(t, e.db)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	wantEpochMismatch(t, <-done, ep)
	if acts, records := rows(t, e); acts != 0 || records != 0 {
		t.Fatalf("acts %d, records %d", acts, records)
	}

	// Only a lookup under the key's lock refuses. A request without the header passes its first
	// lookup; the entry starts and holds the installation state while that request's transaction
	// waits on it under the key's lock, and a second request with the same key and the earlier epoch
	// waits in its first lookup. When the entry commits, the first commits its record, which the
	// second's first lookup, begun before, cannot see: the read under the key's lock finds it, and
	// the second answers with the first's replay, not 412.
	rt = testRoute(nil)
	ep = id.New(id.Epoch)
	var once sync.Once
	rt.prepare = func(context.Context, *API, *request) error {
		var err error
		once.Do(func() {
			var tx *sql.Tx
			if tx, err = e.db.BeginTx(t.Context(), nil); err != nil {
				return
			}
			held <- tx // the request's goroutine: report a failure, never t.Fatal
			if _, err = tx.Exec("INSERT INTO recovery_epoch (epoch, entered_at) VALUES ($1, now())", ep); err != nil {
				return
			}
			_, err = tx.Exec("UPDATE installation_state SET epoch = $1", ep)
		})
		return err
	}
	e = newEnv(t, options{extra: []*route{rt}})
	tok = e.human("h-author")
	e.recordHuman("h-author")
	first := post(tok, key, `{"note":"a"}`)
	second := first
	second.expected = epoch(t, e.db)
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstDone <- e.do(e.api, first) }()
	tx = <-held
	defer func() { _ = tx.Rollback() }()
	dbtest.WaitForLockWait(t, e.db)
	go func() { done <- e.do(e.api, second) }()
	waitForLockWaits(t, e.db, 2)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if rec := <-firstDone; rec.Code != http.StatusCreated {
		t.Fatalf("first: %d %s", rec.Code, rec.Body)
	}
	if rec := <-done; rec.Code != http.StatusCreated || rec.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("second: %d %v %s; want the first's replay", rec.Code, rec.Header(), rec.Body)
	}
	if acts, records := rows(t, e); acts != 1 || records != 1 {
		t.Fatalf("acts %d, records %d", acts, records)
	}
}

// Every mutating route of §9.2 refuses a mismatching header before its effect runs, and runs it
// with a matching one. Each route is walked as itself, with its method, its flags and the path
// handling of its pattern, but with an effect that only records that it ran; that every mutating
// route has an effect, so that it reaches the lookup, is checked against the table itself.
func TestExpectedEpochEveryMutation(t *testing.T) {
	var ran bool
	effect := func(_ context.Context, _ *API, _ *sql.Tx, q *request) (result, error) {
		ran = true
		return result{status: http.StatusCreated, body: map[string]string{"request": q.id}, subjects: []string{q.principal.ID}}, nil
	}
	param := regexp.MustCompile(`\{[^}]+\}`)
	var walk []*route
	for _, rt := range routes() {
		if !rt.mutating() {
			continue
		}
		if rt.effect == nil {
			t.Errorf("%s %s has no effect, so no lookup", rt.method, rt.pattern)
		}
		w := *rt
		w.pattern = "/walk" + rt.pattern
		w.roles = []auth.Role{auth.Author}
		w.keyed, w.prepare, w.effect = "", nil, effect
		w.input = func() input { return &testInput{} }
		walk = append(walk, &w)
	}
	e := newEnv(t, options{extra: walk})
	tok := e.human("h-author")
	cur := epoch(t, e.db)
	for _, rt := range walk {
		c := call{method: rt.method, path: prefix + param.ReplaceAllString(rt.pattern, "x"), token: tok,
			body: `{"note":"w"}`, ifMatch: "\"1\"", expected: id.New(id.Epoch)}
		c.key = "k-walk-stale-" + id.New(id.Request)
		ran = false
		wantEpochMismatch(t, e.do(e.api, c), cur)
		if ran {
			t.Errorf("%s %s: the effect ran under a mismatching header", rt.method, rt.pattern)
		}
		for _, vs := range [][]string{{""}, {cur, cur}} {
			bad := c
			bad.key, bad.expected, bad.expectedValues = "k-walk-bad-"+id.New(id.Request), "", vs
			ran = false
			wantProblem(t, e.do(e.api, bad), http.StatusBadRequest, "invalid-request")
			if ran {
				t.Errorf("%s %s: the effect ran under the headers %q", rt.method, rt.pattern, vs)
			}
		}
		c.key, c.expected = "k-walk-match-"+id.New(id.Request), cur
		if rec := e.do(e.api, c); rec.Code != http.StatusCreated || !ran {
			t.Errorf("%s %s: matching header: %d %s (effect ran %v)", rt.method, rt.pattern, rec.Code, rec.Body, ran)
		}
	}
	mutations := 0
	for _, s := range specRoutes {
		if !strings.HasPrefix(s, "GET ") {
			mutations++
		}
	}
	if len(walk) != mutations {
		t.Fatalf("walked %d mutating routes; §9.2 has %d", len(walk), mutations)
	}
}
