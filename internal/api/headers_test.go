package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// checkAnswerHeaders checks the headers §9.1 puts on every answer: Cache-Control no-store and a
// request identifier, the instance of a problem the request answers itself. A replayed stored
// problem keeps the instance of the request that stored it. Nothing sets a cookie or a CORS
// header. It reports with Errorf: env.do runs in goroutines too.
func checkAnswerHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	h := rec.Header()
	if cc := h.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("status %d: Cache-Control %q, want no-store", rec.Code, cc)
	}
	rid := h.Get(requestIDHeader)
	if err := id.MustHave(rid, id.Request); err != nil {
		t.Errorf("status %d: %s %q: %v", rec.Code, requestIDHeader, rid, err)
	}
	for k := range h {
		if k == "Set-Cookie" || strings.HasPrefix(k, "Access-Control-") {
			t.Errorf("status %d: header %s %v", rec.Code, k, h[k])
		}
	}
	if h.Get("Content-Type") != "application/problem+json" || h.Get("Idempotent-Replayed") == "true" {
		return
	}
	var doc struct{ Instance string }
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || doc.Instance != rid {
		t.Errorf("status %d: problem instance %q (%v), want the %s %q", rec.Code, doc.Instance, err, requestIDHeader, rid)
	}
}

// Every answer carries the headers, whatever answers it: an authentication refusal, a role
// refusal, an unavailable database, a read, a write, its replay, every route and the event
// stream's refusals.
func TestAnswerHeadersOnEveryAnswer(t *testing.T) {
	e := newEnv(t, options{})
	viewer, author := e.human("h-viewer"), e.human("h-author")
	for _, c := range []struct {
		name string
		c    call
		want int
	}{
		{"no token", call{method: "GET", path: prefix + "/acts"}, http.StatusUnauthorized},
		{"role refusal", clusterCall(viewer, "k-headers-0123456789"), http.StatusForbidden},
		{"read", call{method: "GET", path: prefix + "/acts", token: viewer}, http.StatusOK},
		{"write", clusterCall(author, "k-headers-0123456789"), http.StatusCreated},
		{"replay", clusterCall(author, "k-headers-0123456789"), http.StatusCreated},
		{"unrouted", call{method: "GET", path: prefix + "/nowhere", token: viewer}, http.StatusNotFound},
	} {
		if rec := e.do(e.api, c.c); rec.Code != c.want {
			t.Errorf("%s: %d %s, want %d", c.name, rec.Code, rec.Body, c.want)
		}
	}
	// The route table, with no token and with the role tokens that together hold every role: e.do
	// checks each answer's headers, and each route is answered past its role check at least once.
	tokens := []string{e.human("h-all"), e.human("h-recovery")}
	for i, rt := range routes() {
		c := call{method: rt.method, path: path(rt.pattern), key: fmt.Sprintf("k-walk-%012d", i), ifMatch: `"1-x"`, body: `{}`}
		if rec := e.do(e.api, c); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with no token: %d", rt.method, rt.pattern, rec.Code)
		}
		reached := false
		for _, tok := range tokens {
			c.token = tok
			rec := e.do(e.api, c)
			reached = reached || rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "urn:bronzeward:problem:forbidden")
		}
		if !reached {
			t.Errorf("%s %s: refused its role check to every token", rt.method, rt.pattern)
		}
	}
	// The event stream's own refusals answer before any frame, with the same headers.
	for _, h := range []struct{ query, lastID string }{{"?limit=5", ""}, {"", "not-an-event-id"}} {
		r := httptest.NewRequest(http.MethodGet, path("/operations/{id}/events")+h.query, nil)
		r.Header.Set("Authorization", "Bearer "+viewer)
		r.Header.Set("Accept", "text/event-stream")
		if h.lastID != "" {
			r.Header.Set("Last-Event-ID", h.lastID)
		}
		rec := httptest.NewRecorder()
		e.api.ServeHTTP(rec, r)
		checkAnswerHeaders(t, rec)
		wantProblem(t, rec, http.StatusBadRequest, "invalid-request")
	}
	// A database that cannot be read answers 503: from authentication for an automation token,
	// whose check reads the installation state, and from the installation-state read after a
	// human's authentication.
	mustExec(t, e.db, "ALTER TABLE installation_state RENAME TO installation_state_away")
	t.Cleanup(func() { mustExec(t, e.db, "ALTER TABLE installation_state_away RENAME TO installation_state") })
	for _, tok := range []string{e.robot, viewer} {
		wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/acts", token: tok}), http.StatusServiceUnavailable, "dependency-unavailable")
	}
}
