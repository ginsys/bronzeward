package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
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

// TestOperationWatchInterruptTerminal: the first Ctrl-C on a watch whose operation ends within the grace
// time gets the operation's own answer, not exit 130: what the watch read is the outcome, so it exits 0
// for a completed operation and prints no stop line.
func TestOperationWatchInterruptTerminal(t *testing.T) {
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
				f.mu.Lock()
				f.body = fmt.Sprintf(opJSON, "apply-config", "completed", `{"appliedRelease":"rel_BWSYNTH","observation":"obs_c"}`, "null", "[]")
				f.mu.Unlock()
				close(f.hang) // the stream ends: the watch reads the operation, which has ended
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	code, out, errOut := runCtl(t, "operation", "watch", "op_BWSYNTH")
	if code != 0 || !strings.Contains(out, "applied rel_BWSYNTH") || strings.Contains(out, "Stopped watching") || strings.Contains(errOut, "stopped watching") {
		t.Errorf("exit %d, stdout\n%s\nstderr %q; want 0, the outcome and no stop line", code, out, errOut)
	}
}

// TestOperationWatchCtrlCAfterTheWatch: a Ctrl-C while the watch reads the operation's last event, after
// the watch returned, ends the process by the signal at once; no handler swallows it. The command runs
// in a process of its own, because the default action of SIGINT ends it.
func TestOperationWatchCtrlCAfterTheWatch(t *testing.T) {
	f := &opFake{body: fmt.Sprintf(opJSON, "apply-config", "completed", `{"appliedRelease":"rel_BWSYNTH","observation":"obs_c"}`, "null", "[]")}
	f.pages = []string{evPage(1, "operation-state", toState("verifying", "completed", ""))}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	if code, _, errOut := runCtl(t, "operation", "watch", "op_BWSYNTH", "--timeout", "2s"); code != 0 {
		t.Fatalf("the watch alone: exit %d, stderr %q", code, errOut)
	}
	f.mu.Lock()
	total := f.pageReqs // the page reads of the watch, then the last one is the read after it
	f.pageHang, f.pageHangFrom = make(chan struct{}), total
	f.pageReqs = 0
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		close(f.pageHang)
		f.mu.Unlock()
	}()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "BW_WATCH_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() { ended <- cmd.Wait() }()
	defer func() { _ = cmd.Process.Kill() }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		f.mu.Lock()
		n := f.pageReqs
		f.mu.Unlock()
		if n >= total {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the process did not reach the read after the watch (%d of %d page reads)", n, total)
		}
	}
	_ = cmd.Process.Signal(syscall.SIGINT)
	select {
	case err := <-ended:
		var x *exec.ExitError
		if !errors.As(err, &x) || x.Sys().(syscall.WaitStatus).Signal() != syscall.SIGINT {
			t.Errorf("the process ended with %v; want it killed by SIGINT", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Ctrl-C during the read after the watch did not end the process")
	}
}

// TestOperationWatchEscapes: the kind, the state, the event types, their data and the failure cause
// are the server's text; none reaches the terminal with a control character as itself, in an event
// line or the outcome (client-contracts.md §6 item 8). The JSON fields also carry a line feed, which
// must not start a line of its own, in a terminal state's outcome and in a state that is not terminal.
func TestOperationWatchEscapes(t *testing.T) {
	const bad = "x\x1b[31m\ty\r\u202ez"
	const badLF = bad + "\nFAKE LINE"
	js := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	for _, c := range []struct {
		name, state, want string
		code              int
	}{
		{"a terminal state", "failed", `x\x1b[31m\x09y\x0d\u202ez\x0aFAKE LINE`, 4},
		{"a state that is not terminal", badLF, `Still x\x1b[31m\x09y\x0d\u202ez\x0aFAKE LINE after --timeout`, 5},
	} {
		f := &opFake{}
		srv := httptest.NewServer(f)
		automationContext(t, srv.URL)
		state := js(c.state)
		f.body = fmt.Sprintf(`{"id":"op_BWSYNTH","kind":%s,"state":%s,"subject":{"plan":%s},"epoch":"ep_BWSYNTH-2",`+
			`"createdAt":"2026-11-01T06:00:00Z","createdBy":null,"lastEvent":2,"result":null,"error":{"cause":%s,"attempts":1},"attempts":[]}`,
			js(badLF), state, js(badLF), js(badLF))
		f.stream = []string{sse(1, strings.NewReplacer("\r", "", "\n", "").Replace(bad), `{"note":`+js(bad)+`}`)}
		f.pages = []string{`{"id":"ep_BWSYNTH-2:1","type":` + js(bad) + `,"at":"2026-11-01T06:00:01Z","data":{"note":` + js(bad) + `}}`}
		code, out, errOut := runCtl(t, "operation", "watch", "op_BWSYNTH", "--timeout", "2s")
		srv.Close()
		for name, text := range map[string]string{"stdout": out, "stderr": errOut} {
			if i := strings.IndexFunc(strings.ReplaceAll(text, "\n", ""), func(r rune) bool { return unicode.IsControl(r) || r == '\u202e' }); i >= 0 {
				t.Errorf("%s: %s holds a control character at %d:\n%q", c.name, name, i, text)
			}
			for _, l := range strings.Split(text, "\n") {
				if strings.HasPrefix(l, "FAKE LINE") {
					t.Errorf("%s: %s has a line of the server's making:\n%q", c.name, name, text)
				}
			}
		}
		if code != c.code || !strings.Contains(out, c.want) || !strings.Contains(out, `x\x1b[31m\x09y\x0d\u202ez`) || !strings.Contains(out, `received `) ||
			!strings.Contains(out, `x\x1b[31m\x09y\u202ez  {`) {
			t.Errorf("%s: exit %d (want %d); the escaped server text is not shown, want %q in\n%q", c.name, code, c.code, c.want, out)
		}
	}
}

// TestReadLastKeepsOneOperation: the event time is read only when the operation read that follows the
// watch is the state the watch returned, so a time never goes with the outcome of another operation.
func TestReadLastKeepsOneOperation(t *testing.T) {
	f := &opFake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	f.body = fmt.Sprintf(opJSON, "apply-config", "cancelled", "null", "null", "[]")
	f.pages = []string{evPage(1, "operation-state", toState("verifying", "cancelled", ""))}
	e := &env{output: "table", out: io.Discard, err: io.Discard}
	c, err := e.open()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Server(); err != nil {
		t.Fatal(err)
	}
	if ev := readLast(c, "op_BWSYNTH", "cancelled"); ev == nil || ev.At == nil {
		t.Errorf("the state the watch returned: readLast gave %v; want the last event with its time", ev)
	}
	if ev := readLast(c, "op_BWSYNTH", "completed"); ev != nil {
		t.Errorf("another state than the watch returned: readLast gave %v; want nil", ev)
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
