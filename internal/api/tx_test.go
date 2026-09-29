package api

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/dbtest"
)

type testInput struct {
	Note string `json:"note"`
}

func (*testInput) check(*API) error { return nil }

// testRoute is a mutating route whose effect is only what effect does; the act and the record are
// the runner's.
func testRoute(effect effectFunc) *route {
	if effect == nil {
		effect = func(_ context.Context, _ *API, _ *sql.Tx, q *request) (result, error) {
			return result{status: http.StatusCreated, body: map[string]string{"request": q.id}, subjects: []string{q.principal.ID}}, nil
		}
	}
	return &route{method: http.MethodPost, pattern: "/test-effects", roles: []auth.Role{auth.Author, auth.Publisher},
		action: "test.effect", input: func() input { return &testInput{} }, effect: effect}
}

const key = "k0123456789abcdef"

func post(tok, k, body string) call {
	return call{method: "POST", path: prefix + "/test-effects", token: tok, key: k, body: body}
}

func rows(t *testing.T, e *env) (acts, records int) {
	return count(t, e.db, "SELECT count(*) FROM act WHERE via = 'api'"), count(t, e.db, "SELECT count(*) FROM idempotency_record")
}

// recordHuman gives a fixture human its principal row, so a racing request does not create it.
func (e *env) recordHuman(sub string) {
	e.t.Helper()
	if _, err := auth.RecordHuman(e.t.Context(), e.db, e.iss.URL, sub); err != nil {
		e.t.Fatal(err)
	}
}

func TestReplay(t *testing.T) {
	e := newEnv(t, options{extra: []*route{testRoute(nil)}})
	for _, tok := range []string{e.human("h-author"), e.robot} {
		first := e.do(e.api, post(tok, key, `{"note":"a"}`))
		second := e.do(e.api, post(tok, key, `{ "note" : "a" }`)) // same canonical body
		if first.Code != http.StatusCreated || first.Header().Get("Idempotent-Replayed") != "" {
			t.Fatalf("first: %d %v %s", first.Code, first.Header(), first.Body)
		}
		if second.Code != http.StatusCreated || second.Header().Get("Idempotent-Replayed") != "true" || !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
			t.Fatalf("second: %d %v %s; first %s", second.Code, second.Header(), second.Body, first.Body)
		}
	}
	// One act per principal, recorded under its first qualifying role (choice §17.20).
	if acts, records := rows(t, e); acts != 2 || records != 2 {
		t.Fatalf("acts %d, records %d; want 2 and 2", acts, records)
	}
	if n := count(t, e.db, "SELECT count(*) FROM act WHERE via = 'api' AND role = 'author' AND idempotency_key = $1 AND request_id LIKE 'req\\_%'", key); n != 2 {
		t.Fatalf("%d acts with role author, the key and a request id", n)
	}
}

// §7.2: a stored refusal (a draft entry refused after ingestion) is a problem document, and its
// replay is one too.
func TestStoredRefusalReplaysAsProblem(t *testing.T) {
	refused := func(_ context.Context, _ *API, _ *sql.Tx, q *request) (result, error) {
		return result{status: http.StatusConflict, subjects: []string{q.principal.ID}, body: map[string]any{
			"type": "urn:bronzeward:problem:conflict", "title": titles["conflict"], "status": http.StatusConflict, "instance": q.id}}, nil
	}
	e := newEnv(t, options{extra: []*route{testRoute(refused)}})
	tok := e.human("h-author")
	first := e.do(e.api, post(tok, key, `{"note":"a"}`))
	wantProblem(t, first, http.StatusConflict, "conflict")
	second := e.do(e.api, post(tok, key, `{"note":"a"}`))
	wantProblem(t, second, http.StatusConflict, "conflict")
	if second.Header().Get("Idempotent-Replayed") != "true" || !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Fatalf("replay: %v %s; first %s", second.Header(), second.Body, first.Body)
	}
	// §9.4: the log carries the problem's instance, for the first answer and the replay alike.
	_, doc := problem(t, first)
	instance, _ := doc["instance"].(string)
	e.mu.Lock()
	n := 0
	for _, l := range e.logs {
		if strings.Contains(l, instance) && strings.Contains(l, "409") {
			n++
		}
	}
	e.mu.Unlock()
	if n != 2 {
		t.Fatalf("%d log lines name the stored problem's instance %s; want 2", n, instance)
	}
}

func TestKeyReusedForAnotherRequest(t *testing.T) {
	e := newEnv(t, options{extra: []*route{testRoute(nil)}})
	tok := e.human("h-author")
	e.do(e.api, post(tok, key, `{"note":"a"}`))
	wantProblem(t, e.do(e.api, post(tok, key, `{"note":"b"}`)), http.StatusUnprocessableEntity, "idempotency-key-reused")
	// Another principal may use the same key (§7.1).
	if rec := e.do(e.api, post(e.robot, key, `{"note":"b"}`)); rec.Code != http.StatusCreated {
		t.Fatalf("another principal's key: %d %s", rec.Code, rec.Body)
	}
}

// §7.2: a record from an earlier epoch is not replayed; nothing executes.
func TestEarlierEpochRecord(t *testing.T) {
	e := newEnv(t, options{extra: []*route{testRoute(nil)}})
	tok := e.human("h-author")
	first := e.do(e.api, post(tok, key, `{"note":"a"}`))
	old := first.Header().Get("Bronzeward-Epoch")
	newEpoch(t, e.db)
	doc := wantProblem(t, e.do(e.api, post(tok, key, `{"note":"a"}`)), http.StatusConflict, "conflict")
	if req, _ := doc["request"].(string); doc["epoch"] != old || !strings.HasPrefix(req, "req_") {
		t.Fatalf("409 names %v and %v; want the stored request and epoch %s", doc["request"], doc["epoch"], old)
	}
	if acts, _ := rows(t, e); acts != 1 {
		t.Fatalf("%d acts; the refused retry executed", acts)
	}
}

// §9.1, §9.4: the body checks refuse before any row is written, the principal row included.
func TestBodyRefusals(t *testing.T) {
	e := newEnv(t, options{extra: []*route{testRoute(nil)}})
	tok := e.human("h-author")
	for name, body := range map[string]string{
		"unknown member": `{"note":"a","extra":1}`, "other case": `{"Note":"a"}`, "repeated member": `{"note":"a","note":"b"}`,
		"two values": `{"note":"a"} {}`, "array": `[]`, "not JSON": `note=a`, "too large": `{"note":"` + strings.Repeat("x", 1<<20) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			wantProblem(t, e.do(e.api, post(tok, key, body)), http.StatusBadRequest, "invalid-request")
		})
	}
	if n := count(t, e.db, "SELECT count(*) FROM principal WHERE sub = 'h-author'"); n != 0 {
		t.Fatal("a refused body created a principal row")
	}
	// The control: the same request, well formed, succeeds.
	if r := e.do(e.api, post(tok, key, `{"note":"a"}`)); r.Code != http.StatusCreated {
		t.Fatalf("well-formed: %d %s", r.Code, r.Body)
	}
	if n := count(t, e.db, "SELECT count(*) FROM act WHERE via = 'api'"); n != 1 {
		t.Fatalf("%d acts", n)
	}
}

// httptestRequest sends a well-formed test-effect request whose Content-Type is ct.
func httptestRequest(t *testing.T, e *env, ct string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", prefix+"/test-effects", strings.NewReader(`{"note":"a"}`))
	r.Header.Set("Authorization", "Bearer "+e.human("h-author"))
	r.Header.Set("Idempotency-Key", key)
	r.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	e.api.ServeHTTP(rec, r)
	return rec
}

func TestWrongContentType(t *testing.T) {
	e := newEnv(t, options{extra: []*route{testRoute(nil)}})
	wantProblem(t, httptestRequest(t, e, "text/plain"), http.StatusBadRequest, "invalid-request")
	if n := count(t, e.db, "SELECT count(*) FROM principal WHERE sub = 'h-author'"); n != 0 {
		t.Fatal("a refused request created a principal row")
	}
	if rec := httptestRequest(t, e, "application/json; charset=utf-8"); rec.Code != http.StatusCreated {
		t.Fatalf("JSON with a charset parameter: %d %s", rec.Code, rec.Body)
	}
}

// §10: an admitted mutating request creates the human's row; a read does not.
func TestFirstUseCreatesPrincipal(t *testing.T) {
	e := newEnv(t, options{extra: []*route{testRoute(nil)}})
	e.do(e.api, call{method: "GET", path: prefix + "/clusters", token: e.human("h-author")})
	if n := count(t, e.db, "SELECT count(*) FROM principal WHERE sub = 'h-author'"); n != 0 {
		t.Fatal("a read created a principal row")
	}
	e.do(e.api, post(e.human("h-author"), key, `{"note":"a"}`))
	if n := count(t, e.db, "SELECT count(*) FROM principal WHERE sub = 'h-author' AND kind = 'human' AND NOT revoked"); n != 1 {
		t.Fatalf("%d rows after the first admitted mutating request", n)
	}
}

// holdFirst returns an afterEffect hook that holds the first transaction to reach it until
// release is called, and a channel that receives once it is held.
func holdFirst() (hook func(), held <-chan struct{}, release func()) {
	var once atomic.Bool
	h, r := make(chan struct{}, 1), make(chan struct{})
	var closeOnce sync.Once
	return func() {
			if once.CompareAndSwap(false, true) {
				h <- struct{}{}
				<-r
			}
		}, h, func() {
			closeOnce.Do(func() { close(r) })
		}
}

// §7.2, §16: the second request with a key in flight waits on the key lock, then replays.
func TestSameKeyInFlight(t *testing.T) {
	hook, held, release := holdFirst()
	e := newEnv(t, options{extra: []*route{testRoute(nil)}, afterEffect: hook})
	t.Cleanup(release)
	e.recordHuman("h-author") // the principal row exists before the race
	tok := e.human("h-author")
	a, b := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
	go func() { a <- e.do(e.api, post(tok, key, `{"note":"a"}`)) }()
	<-held
	go func() { b <- e.do(e.api, post(tok, key, `{"note":"a"}`)) }()
	dbtest.WaitForLockWait(t, e.db)
	release()
	ra, rb := <-a, <-b
	if ra.Code != http.StatusCreated || rb.Code != http.StatusCreated || rb.Header().Get("Idempotent-Replayed") != "true" ||
		!bytes.Equal(ra.Body.Bytes(), rb.Body.Bytes()) {
		t.Fatalf("first %d %s; second %d %v %s", ra.Code, ra.Body, rb.Code, rb.Header(), rb.Body)
	}
	if acts, records := rows(t, e); acts != 1 || records != 1 {
		t.Fatalf("acts %d, records %d", acts, records)
	}
}

// The key-lock control: without the advisory lock the second request runs its effect and fails
// on the (principal, key) primary key instead of replaying.
func TestSameKeyInFlightNoKeyLockControl(t *testing.T) {
	hook, held, release := holdFirst()
	e := newEnv(t, options{extra: []*route{testRoute(nil)}, afterEffect: hook, noKeyLock: true})
	t.Cleanup(release)
	e.recordHuman("h-author")
	tok := e.human("h-author")
	a, b := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
	go func() { a <- e.do(e.api, post(tok, key, `{"note":"a"}`)) }()
	<-held
	go func() { b <- e.do(e.api, post(tok, key, `{"note":"a"}`)) }()
	dbtest.WaitForLockWait(t, e.db) // on the first transaction's uncommitted record row
	release()
	ra, rb := <-a, <-b
	if ra.Code != http.StatusCreated {
		t.Fatalf("first: %d %s", ra.Code, ra.Body)
	}
	wantProblem(t, rb, http.StatusInternalServerError, "internal-error")
	if rb.Header().Get("Idempotent-Replayed") != "" || !e.logged("23505") {
		t.Fatalf("second: %v %s; want a unique-violation failure, not a replay", rb.Header(), rb.Body)
	}
}

// §7.2: when the first transaction rolls back, the waiting duplicate executes.
func TestSameKeyAfterRollbackExecutes(t *testing.T) {
	hook, held, release := holdFirst()
	var calls atomic.Int32
	refuseFirst := func(_ context.Context, _ *API, _ *sql.Tx, q *request) (result, error) {
		if calls.Add(1) == 1 {
			hook()
			return result{}, refuse(http.StatusConflict, "conflict", "test refusal")
		}
		return result{status: http.StatusCreated, body: map[string]string{"request": q.id}}, nil
	}
	e := newEnv(t, options{extra: []*route{testRoute(refuseFirst)}})
	t.Cleanup(release)
	e.recordHuman("h-author")
	tok := e.human("h-author")
	a, b := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
	go func() { a <- e.do(e.api, post(tok, key, `{"note":"a"}`)) }()
	<-held
	go func() { b <- e.do(e.api, post(tok, key, `{"note":"a"}`)) }()
	dbtest.WaitForLockWait(t, e.db)
	release()
	wantProblem(t, <-a, http.StatusConflict, "conflict")
	if rb := <-b; rb.Code != http.StatusCreated || rb.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("second: %d %v %s; want a fresh execution", rb.Code, rb.Header(), rb.Body)
	}
}

func deadlock(upTo int) func(int) error {
	return func(attempt int) error {
		if attempt <= upTo {
			return &pgconn.PgError{Code: "40P01", Message: "deadlock detected (test)"}
		}
		return nil
	}
}

// §5 rule 5: a deadlock is retried whole, at most three times, then 503 transient-conflict.
func TestDeadlockRetried(t *testing.T) {
	e := newEnv(t, options{extra: []*route{testRoute(nil)}, beforeCommit: deadlock(maxAttempts - 1)})
	if rec := e.do(e.api, post(e.human("h-author"), key, `{}`)); rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if acts, records := rows(t, e); acts != 1 || records != 1 {
		t.Fatalf("acts %d, records %d", acts, records)
	}
}

func TestDeadlockRetriesExhausted(t *testing.T) {
	e := newEnv(t, options{extra: []*route{testRoute(nil)}, beforeCommit: deadlock(maxAttempts)})
	wantProblem(t, e.do(e.api, post(e.human("h-author"), key, `{}`)), http.StatusServiceUnavailable, "transient-conflict")
	if acts, records := rows(t, e); acts != 0 || records != 0 {
		t.Fatalf("acts %d, records %d", acts, records)
	}
}

// §5 rule 6: a lost COMMIT reply is resolved by reading the record back.
func TestCommitUnknownCommitted(t *testing.T) {
	lost := func(tx *sql.Tx) error {
		if err := tx.Commit(); err != nil {
			return err
		}
		return errors.New("connection reset after COMMIT (test)")
	}
	e := newEnv(t, options{extra: []*route{testRoute(nil)}, commit: lost})
	rec := e.do(e.api, post(e.human("h-author"), key, `{}`))
	if rec.Code != http.StatusCreated || rec.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body)
	}
}

// §5 rule 6: when the client loses the COMMIT's reply, the server may still be committing. The
// read-back waits on the key lock, which that transaction holds until it ends, and so answers
// the request's own response once it commits.
func TestCommitUnknownStillCommitting(t *testing.T) { commitStillInFlight(t, false) }

// Control: without the key lock the read-back answers "nothing was committed" while the
// transaction is still open and can still commit.
func TestCommitUnknownStillCommittingNoKeyLockControl(t *testing.T) { commitStillInFlight(t, true) }

func commitStillInFlight(t *testing.T, control bool) {
	handoff := make(chan *sql.Tx, 1)
	inFlight := func(tx *sql.Tx) error { // the test ends the transaction, not the request
		handoff <- tx
		return errors.New("connection reset while COMMIT was in flight (test)")
	}
	e := newEnv(t, options{extra: []*route{testRoute(nil)}, commit: inFlight, noKeyLock: control})
	tok := e.human("h-author")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- e.do(e.api, post(tok, key, `{}`)) }()
	tx := <-handoff
	t.Cleanup(func() { _ = tx.Rollback() }) // every path ends it, a t.Fatal included
	if control {
		wantProblem(t, <-done, http.StatusServiceUnavailable, "dependency-unavailable")
		return
	}
	dbtest.WaitForLockWait(t, e.db)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if rec := <-done; rec.Code != http.StatusCreated || rec.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("%d %v %s; want the request's own 201", rec.Code, rec.Header(), rec.Body)
	}
}

func TestCommitUnknownNotCommitted(t *testing.T) {
	lost := func(tx *sql.Tx) error {
		_ = tx.Rollback()
		return errors.New("connection reset before COMMIT (test)")
	}
	e := newEnv(t, options{extra: []*route{testRoute(nil)}, commit: lost})
	wantProblem(t, e.do(e.api, post(e.human("h-author"), key, `{}`)), http.StatusServiceUnavailable, "dependency-unavailable")
	if acts, records := rows(t, e); acts != 0 || records != 0 {
		t.Fatalf("acts %d, records %d", acts, records)
	}
}
