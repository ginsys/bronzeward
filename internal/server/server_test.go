package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Every request but liveness reaches the API, which authenticates first (persistence-api.md §10):
// nothing in front of it answers an unclean path with a redirect or `OPTIONS *` with its own 400.
func TestEveryOtherRequestReachesTheAPI(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	h := New(api)
	for _, c := range []struct{ method, target string }{
		{"GET", "/api//v1/acts"}, {"GET", "/api/v1/../v1/acts"}, {"GET", "/api/./v1/acts"},
		{"GET", "//livez"}, {"OPTIONS", "*"}, {"POST", "/livez"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.target, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d (%v), want the API's 401", c.method, c.target, rec.Code, rec.Header())
		}
	}
}

func TestLiveness(t *testing.T) {
	h := New(nil)
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/livez", http.StatusNoContent},
		{"HEAD", "/livez", http.StatusNoContent},
		{"POST", "/livez", http.StatusNotFound}, // reaches the API handler, nil here
		{"GET", "/api/v1/dispatch", http.StatusNotFound},
		{"GET", "/", http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rec.Code, c.want)
		}
		if c.want == http.StatusNoContent && rec.Body.Len() != 0 { // 404 carries http.Error's text
			t.Errorf("%s /livez returned data", c.method)
		}
	}
}
