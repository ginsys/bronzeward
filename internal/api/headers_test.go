package api

import (
	"encoding/json"
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
// refusal, an unavailable database, a read and a write.
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
	// A database that cannot be read answers 503 after authentication, with the same headers.
	mustExec(t, e.db, "ALTER TABLE installation_state RENAME TO installation_state_away")
	t.Cleanup(func() { mustExec(t, e.db, "ALTER TABLE installation_state_away RENAME TO installation_state") })
	wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/acts", token: e.robot}), http.StatusServiceUnavailable, "dependency-unavailable")
}
