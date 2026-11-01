package main

import (
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestWatchOutAfterFinish: once the stop line is printed, a line the watch still shows prints
// nothing, so nothing follows the stop line.
func TestWatchOutAfterFinish(t *testing.T) {
	var b strings.Builder
	out := &watchOut{w: &b}
	out.show("one")
	out.finish("stopped", "follow")
	out.show("late")
	if got, want := b.String(), "one\nstopped\nfollow\n"; got != want {
		t.Errorf("printed %q, want %q", got, want)
	}
}

// TestOperationWatchInterrupt: the first Ctrl-C on a watch whose stream stays silent waits for the
// grace time, then prints the stop line and exits 130; the watch still running prints nothing more.
func TestOperationWatchInterrupt(t *testing.T) {
	f := &opFake{body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]"), hang: make(chan struct{})}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	grace := interruptGrace
	interruptGrace = 100 * time.Millisecond
	defer func() { interruptGrace = grace }()
	go func() {
		for { // the stream request comes after the signal handler is set
			f.mu.Lock()
			n := f.streams
			f.mu.Unlock()
			if n > 0 {
				_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	code, out, errOut := runCtl(t, "operation", "watch", "op_BWSYNTH")
	close(f.hang)
	follow := "Follow it again with: bronzewardctl operation watch 'op_BWSYNTH'\n"
	if code != 130 || !strings.HasSuffix(out, "Stopped watching at your request. Operation op_BWSYNTH goes on; the state it has now was not read.\n"+follow) ||
		!strings.Contains(errOut, "stopped watching operation op_BWSYNTH") {
		t.Errorf("exit %d, stdout\n%s\nstderr %q", code, out, errOut)
	}
}

// TestOperationWatchCompletionTime: the completion line shows the server's time only when the last
// page item is of the epoch of the operation read that follows the watch; after a restore it says
// the time was not read.
func TestOperationWatchCompletionTime(t *testing.T) {
	f := &opFake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	f.body = fmt.Sprintf(opJSON, "apply-config", "completed", `{"appliedRelease":"rel_BWSYNTH","observation":"obs_c"}`, "null", "[]")
	f.pages = []string{evPage(1, "commitment", commitment), evPage(2, "operation-state", toState("verifying", "completed", ""))}
	for _, c := range []struct{ late, want, not string }{
		{"", "(server): applied rel_BWSYNTH", "was not read"},
		{"ep_BWSYNTH-3", "The server's time of this end was not read.", "(server)"},
	} {
		f.mu.Lock()
		f.late, f.reads = c.late, 0
		f.mu.Unlock()
		code, out, _ := runCtl(t, "operation", "watch", "op_BWSYNTH", "--timeout", "2s")
		if code != 0 || !strings.Contains(out, c.want) || strings.Contains(out, c.not) {
			t.Errorf("late epoch %q: exit %d, want %q and not %q in\n%s", c.late, code, c.want, c.not, out)
		}
	}
}
