package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// stalledServer takes every request and never answers. arrived gets one value for each request.
func stalledServer(t *testing.T) (srv *httptest.Server, arrived chan struct{}) {
	t.Helper()
	arrived, hang := make(chan struct{}, 16), make(chan struct{})
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		select {
		case <-hang:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(hang); srv.Close() })
	return srv, arrived
}

// TestReadCtrlCExits130WithOneStopLine: a read command whose server never answers. The first Ctrl-C
// ends it within a few seconds, not after the retries and the request timeout of one minute each
// (#643). It exits 130 and prints one stop line, naming the request it was reading, and nothing else.
func TestReadCtrlCExits130WithOneStopLine(t *testing.T) {
	srv, arrived := stalledServer(t)
	automationContext(t, srv.URL)
	go func() {
		<-arrived // the handler is armed before the request is sent
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	}()
	start := time.Now()
	code, out, errOut := runCtl(t, "cluster", "list")
	const stop = "Stopped at your request while reading GET /api/v1/"
	if code != 130 || out != "" || !strings.HasPrefix(errOut, stop) || strings.Count(errOut, "\n") != 1 {
		t.Errorf("exit %d, stdout %q, stderr %q; want 130, no output and the one stop line %q...", code, out, errOut, stop)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the command took %s after the Ctrl-C; want a few seconds at most", d)
	}
}

// TestCtrlCIsNotHeldBetweenReads: after a read ended with its Ctrl-C taken, nothing holds the signal
// any more. A new read arms again and takes its own, so a command that reads twice stops on either.
func TestCtrlCIsNotHeldBetweenReads(t *testing.T) {
	e := &env{}
	for i := 0; i < 2; i++ {
		ctx, disarm := e.arm()
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT) // the handler is set by arm
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
			t.Fatalf("read %d: the Ctrl-C did not reach the context within 5 s", i+1)
		}
		if !disarm() {
			t.Errorf("read %d: disarm says no Ctrl-C was taken", i+1)
		}
	}
}
