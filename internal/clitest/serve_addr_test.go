package clitest

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestServeAddressTakenBeforeItBinds is the control for ginsys/bronzeward#940: the first address
// the fixture picks is held by a server that answers /livez, as a server of another test does, so
// serve exits on the bind. The fixture must not take that answer for its own serve's: it must run
// serve on another address. No test here runs in parallel, so the swap of pickAddr is not seen by
// another test.
func TestServeAddressTakenBeforeItBinds(t *testing.T) {
	// It answers after 2 s, which serve's exit on the bind precedes even on a loaded host: the control
	// is the answer of another server that arrives after serve ended, and it must not depend on how
	// fast serve fails (a faster answer is the accepted limit, in the retry's own comment).
	held := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer held.Close()
	real := pickAddr
	first := true
	pickAddr = func(t *testing.T) string {
		if first {
			first = false
			return held.Listener.Addr().String()
		}
		return real(t)
	}
	defer func() { pickAddr = real }()

	f := start(t, "viewer")
	if f.base == "http://"+held.Listener.Addr().String() {
		t.Fatalf("the fixture took the answer of the server that held %s for its own serve's", f.base)
	}
	select {
	case <-f.srv.done:
		t.Fatalf("serve is not running on %s", f.base)
	default:
	}
}
