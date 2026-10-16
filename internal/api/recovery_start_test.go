package api

import (
	"net/http"
	"testing"
)

// §12.2: before its entry commits, a recovery start serves the entry only. A read and a mutating
// act are refused 409 recovery-mode-active after their role check and before any transaction, so
// no principal row is created; the entry passes the gate (it answers 501 until it is built). The
// role check still comes first: a role the route does not admit is refused 403.
func TestRecoveryStartServesOnlyTheEntry(t *testing.T) {
	e := newEnv(t, options{})
	e.api.d.preEntry.Store(true)
	viewer, author, rec := e.human("h-viewer"), e.human("h-author"), e.human("h-recovery")
	wantProblem(t, e.do(e.api, call{method: "GET", path: path("/clusters"), token: viewer}),
		http.StatusConflict, "recovery-mode-active")
	wantProblem(t, e.do(e.api, call{method: "POST", path: path("/drafts"), token: author, key: key, body: `{}`}),
		http.StatusConflict, "recovery-mode-active")
	wantProblem(t, e.do(e.api, call{method: "POST", path: path("/recovery/exits"), token: rec, key: key, body: `{}`}),
		http.StatusConflict, "recovery-mode-active")
	wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/recovery", token: viewer}),
		http.StatusConflict, "recovery-mode-active")
	// A request no route matches, by path, by method or by an unclean path, is refused too.
	for _, c := range []call{
		{method: "GET", path: path("/no-such-route"), token: viewer},
		{method: "GET", path: path(entryPattern), token: rec},
		{method: "GET", path: prefix + "//clusters", token: viewer},
	} {
		wantProblem(t, e.do(e.api, c), http.StatusConflict, "recovery-mode-active")
	}
	wantProblem(t, e.do(e.api, call{method: "POST", path: path("/drafts"), token: viewer, key: key, body: `{}`}),
		http.StatusForbidden, "forbidden")
	wantProblem(t, e.do(e.api, call{method: "POST", path: path(entryPattern), token: viewer, key: key, body: `{}`}),
		http.StatusForbidden, "forbidden")
	wantProblem(t, e.do(e.api, call{method: "POST", path: path(entryPattern), token: rec, key: key, body: `{}`}),
		http.StatusNotImplemented, "not-implemented")
	if n := count(t, e.db, "SELECT count(*) FROM principal WHERE sub IN ('h-viewer', 'h-author', 'h-recovery')"); n != 0 {
		t.Fatalf("a recovery start before entry created %d principal rows", n)
	}
	// Without the flag the same read is served, and an unmatched request is not found.
	e.api.d.preEntry.Store(false)
	if r := e.do(e.api, call{method: "GET", path: path("/clusters"), token: viewer}); r.Code != http.StatusOK {
		t.Fatalf("GET /clusters without the flag: %d %s", r.Code, r.Body)
	}
	wantProblem(t, e.do(e.api, call{method: "GET", path: path("/no-such-route"), token: viewer}),
		http.StatusNotFound, "not-found")
}
