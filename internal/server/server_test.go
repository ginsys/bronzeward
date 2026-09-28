package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLiveness(t *testing.T) {
	h := New()
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/livez", http.StatusNoContent},
		{"HEAD", "/livez", http.StatusNoContent},
		{"POST", "/livez", http.StatusMethodNotAllowed},
		{"GET", "/api/v1/dispatch", http.StatusNotFound},
		{"GET", "/", http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rec.Code, c.want)
		}
		if c.want == http.StatusNoContent && rec.Body.Len() != 0 { // 405 carries http.Error's text
			t.Errorf("%s /livez returned data", c.method)
		}
	}
}
