package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"
)

// The log escapes the request path: a decoded newline or control character cannot forge a line.
func TestLogEscapesPath(t *testing.T) {
	e := newEnv(t, options{})
	e.do(e.api, call{method: "GET", path: "/%0Aforged-entry%1B[2J"})
	e.do(e.api, call{method: "GET", path: prefix + "/%0Aforged-entry", token: e.human("h-viewer")})
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.logs) < 2 {
		t.Fatalf("%d log lines; want one per request", len(e.logs))
	}
	for _, l := range e.logs {
		if strings.ContainsAny(l, "\n\r\x1b") {
			t.Errorf("log line carries a control character: %q", l)
		}
	}
}

// A path that is not clean is 404 not-found after authentication, never the ServeMux's redirect,
// which would skip the problem document, its log line and the route's role check.
func TestUncleanPathIs404(t *testing.T) {
	e := newEnv(t, options{})
	v := e.human("h-viewer")
	for _, p := range []string{prefix + "//acts", "/api//v1/acts", prefix + "/../v1/acts", prefix + "/./acts", prefix + "/acts/."} {
		wantProblem(t, e.do(e.api, call{method: "GET", path: p, token: v}), http.StatusNotFound, "not-found")
		wantProblem(t, e.do(e.api, call{method: "GET", path: p}), http.StatusUnauthorized, "unauthenticated")
	}
	if rec := e.do(e.api, call{method: "GET", path: prefix + "/acts", token: v}); rec.Code != http.StatusOK { // control
		t.Fatalf("clean path: %d %s", rec.Code, rec.Body)
	}
}

// PostgreSQL text cannot hold U+0000: a body string carrying one is refused before any
// transaction, not failed inside it.
func TestBodyStringWithNUL(t *testing.T) {
	e := newEnv(t, options{extra: []*route{testRoute(nil)}})
	wantProblem(t, e.do(e.api, post(e.human("h-author"), key, `{"note":"a\u0000b"}`)), http.StatusBadRequest, "invalid-request")
	if acts, records := rows(t, e); acts != 0 || records != 0 {
		t.Fatalf("acts %d, records %d", acts, records)
	}
	if rec := e.do(e.api, post(e.human("h-author"), key, `{"note":"a\u0001b"}`)); rec.Code != http.StatusCreated { // control
		t.Fatalf("U+0001: %d %s", rec.Code, rec.Body)
	}
}

func TestRevocationReasonWithNUL(t *testing.T) {
	e := newEnv(t, options{})
	wantProblem(t, revoke(e, e.human("h-recovery"), key, `{"identity":"`+e.robotID+`","reason":"left\u0000"}`),
		http.StatusBadRequest, "invalid-request")
}

// §9.4: the database connection lost inside the transaction is 503 dependency-unavailable, and
// nothing is committed.
func TestConnectionLostInTransaction(t *testing.T) {
	// The effect's statement ends its own backend: the statement, not COMMIT, meets the loss.
	lose := func(ctx context.Context, _ *API, tx *sql.Tx, _ *request) (result, error) {
		_, err := tx.ExecContext(ctx, `SELECT pg_terminate_backend(pg_backend_pid())`)
		return result{}, err
	}
	e := newEnv(t, options{extra: []*route{testRoute(lose)}})
	wantProblem(t, e.do(e.api, post(e.human("h-author"), key, `{}`)), http.StatusServiceUnavailable, "dependency-unavailable")
	if !e.logged("57P01") {
		t.Fatal("the log does not show the lost connection")
	}
	if acts, records := rows(t, e); acts != 0 || records != 0 {
		t.Fatalf("acts %d, records %d", acts, records)
	}
}
