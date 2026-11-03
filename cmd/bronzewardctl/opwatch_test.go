package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode"
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

// TestOperationWatchInterruptReturns: the first Ctrl-C on a watch whose stream ends within the grace
// time returns the operation the watch last read, so the stop line names its state and exits 130.
func TestOperationWatchInterruptReturns(t *testing.T) {
	f := &opFake{body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]"), hang: make(chan struct{})}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	grace := interruptGrace
	interruptGrace = 5 * time.Second
	defer func() { interruptGrace = grace }()
	go func() {
		for { // the stream request comes after the signal handler is set
			f.mu.Lock()
			n := f.streams
			f.mu.Unlock()
			if n > 0 {
				_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
				time.Sleep(200 * time.Millisecond)
				close(f.hang) // the stream ends: the watch reads the operation and stops
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	code, out, errOut := runCtl(t, "operation", "watch", "op_BWSYNTH")
	follow := "Follow it again with: bronzewardctl operation watch 'op_BWSYNTH'\n"
	if code != 130 || !strings.HasSuffix(out, "Stopped watching at your request. Operation op_BWSYNTH is still running, as the server last read it; it goes on.\n"+follow) ||
		!strings.Contains(errOut, "stopped watching operation op_BWSYNTH, which is still running") {
		t.Errorf("exit %d, stdout\n%s\nstderr %q", code, out, errOut)
	}
}

// TestOperationWatchEscapes: the kind, the state, the event types, their data and the failure cause
// are the server's text; none reaches the terminal with a control character as itself, in an event
// line or the outcome (client-contracts.md §6 item 8).
func TestOperationWatchEscapes(t *testing.T) {
	const bad = "x\x1b[31m\ty\r\u202ez"
	js := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	f := &opFake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	f.body = fmt.Sprintf(`{"id":"op_BWSYNTH","kind":%s,"state":"failed","subject":{"plan":%s},"epoch":"ep_BWSYNTH-2",`+
		`"createdAt":"2026-11-01T06:00:00Z","createdBy":null,"lastEvent":2,"result":null,"error":{"cause":%s,"attempts":1},"attempts":[]}`,
		js(bad), js(bad), js(bad))
	f.stream = []string{sse(1, strings.NewReplacer("\r", "", "\n", "").Replace(bad), `{"note":`+js(bad)+`}`)}
	f.pages = []string{`{"id":"ep_BWSYNTH-2:1","type":` + js(bad) + `,"at":"2026-11-01T06:00:01Z","data":{"note":` + js(bad) + `}}`}
	_, out, errOut := runCtl(t, "operation", "watch", "op_BWSYNTH", "--timeout", "2s")
	for name, text := range map[string]string{"stdout": out, "stderr": errOut} {
		if i := strings.IndexFunc(strings.ReplaceAll(text, "\n", ""), func(r rune) bool { return unicode.IsControl(r) || r == '\u202e' }); i >= 0 {
			t.Errorf("%s holds a control character at %d:\n%q", name, i, text)
		}
	}
	if !strings.Contains(out, `x\x1b[31m\x09y\x0d\u202ez`) || !strings.Contains(out, `received `) || !strings.Contains(out, `x\x1b[31m\x09y\u202ez  {`) {
		t.Errorf("the escaped server text is not shown:\n%q", out)
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
