package client

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestNoteRefusalRule checks the one rule that decides which refusal a watch ends with: a refused certificate wins
// over a recovery start's refusal in either order, and a later refusal of the same kind replaces the earlier one.
func TestNoteRefusalRule(t *testing.T) {
	untrusted := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(untrusted.Close)
	recovering := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH1")
		problemAnswer(w, http.StatusConflict, "recovery-mode-active", "")
	}))
	t.Cleanup(recovering.Close)
	f := &fake{epoch: "ep_BWSYNTH1"}
	c, _, _ := writeClient(t, f, "ep_BWSYNTH1")
	good := c.Context.Server
	read := func(server string) {
		t.Helper()
		c.Context.Server = server
		_, _ = c.Get("/clusters")
	}
	kind := func(n *refusalNote) string {
		switch {
		case n.err == nil:
			return "none"
		case errors.As(n.err, new(*trust)):
			return "certificate"
		case processRefusal(n.err):
			return "recovery"
		}
		return "other"
	}
	for _, order := range [][]string{{"recovery", "certificate"}, {"certificate", "recovery"}, {"recovery", "recovery"}, {"certificate", "certificate"}} {
		n := &refusalNote{}
		c.watchRefusal = n
		for _, kindOf := range order {
			if kindOf == "recovery" {
				read(recovering.URL)
			} else {
				read(untrusted.URL)
			}
		}
		want := "certificate"
		if order[0] == "recovery" && order[1] == "recovery" {
			want = "recovery"
		}
		if got := kind(n); got != want {
			t.Errorf("refusals met in the order %v: kept %s; want %s", order, got, want)
		}
	}
	c.watchRefusal = nil
	read(untrusted.URL)
	n := &refusalNote{}
	c.watchRefusal = n
	read(good)
	c.watchRefusal = nil
	if kind(n) != "none" {
		t.Errorf("a refusal kept by a read outside a watch, or by a read that succeeded: %s", kind(n))
	}
}
