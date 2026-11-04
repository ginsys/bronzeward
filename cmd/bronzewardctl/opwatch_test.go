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
	"sync"
	"sync/atomic"
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

// TestWatchOutGate: what the watch's client writes (notices, -v output) goes through the same stop
// barrier, so it prints nothing after the stop line; a nil writer stays nil.
func TestWatchOutGate(t *testing.T) {
	var b, side strings.Builder
	out := &watchOut{w: &b}
	g := out.gate(&side)
	fmt.Fprint(g, "notice before\n")
	out.finish("stopped")
	fmt.Fprint(g, "notice after\n")
	if got, want := side.String(), "notice before\n"; got != want {
		t.Errorf("client wrote %q, want %q", got, want)
	}
	if out.gate(nil) != nil {
		t.Error("gate(nil) is not nil")
	}
}

// TestOperationWatchInterrupt: the first Ctrl-C on a watch whose stream stays silent waits for the
// grace time, then prints the stop line and exits 130; the watch still running prints nothing more,
// and nothing follows the follow line, on the stream the stop lines go to (standard output, or with
// -o json standard error).
func TestOperationWatchInterrupt(t *testing.T) {
	for _, extra := range [][]string{nil, {"-o", "json"}} {
		f := &opFake{body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]"), hang: make(chan struct{})}
		srv := httptest.NewServer(f)
		automationContext(t, srv.URL)
		grace := interruptGrace
		interruptGrace = 100 * time.Millisecond
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
		code, out, errOut := runCtl(t, append([]string{"operation", "watch", "op_BWSYNTH"}, extra...)...)
		close(f.hang)
		srv.Close()
		interruptGrace = grace
		stop := "Stopped watching at your request. Operation op_BWSYNTH goes on; the state it has now was not read.\n"
		follow := "Follow it again with: bronzewardctl operation watch 'op_BWSYNTH'\n"
		stopLines, other := out, errOut // the stream the stop lines go to, and the other
		if extra != nil {
			stopLines, other = errOut, out
		}
		if code != 130 || !strings.HasSuffix(stopLines, stop+follow) || strings.Contains(other, "stopped watching") {
			t.Errorf("%v: exit %d, stdout\n%s\nstderr %q; want 130 and the stop lines last", extra, code, out, errOut)
		}
	}
}

// TestOperationWatchInterruptReturns: the first Ctrl-C on a watch whose stream ends within the grace
// time returns the operation the watch last read, so the stop line names its state and exits 130.
//
// With -o json or -o yaml the operation it returned is also printed on standard output, as the
// resource the stop line names (operator-experience.md section 5.7), and the stop lines stay last on
// standard error.
func TestOperationWatchInterruptReturns(t *testing.T) {
	for _, extra := range [][]string{nil, {"-o", "json"}, {"-o", "yaml"}} {
		f := &opFake{body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]"), hang: make(chan struct{})}
		srv := httptest.NewServer(f)
		automationContext(t, srv.URL)
		grace := interruptGrace
		interruptGrace = 5 * time.Second
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
		code, out, errOut := runCtl(t, append([]string{"operation", "watch", "op_BWSYNTH"}, extra...)...)
		srv.Close()
		interruptGrace = grace
		follow := "Follow it again with: bronzewardctl operation watch 'op_BWSYNTH'\n"
		stop := "Stopped watching at your request. Operation op_BWSYNTH is still running, as the server last read it; it goes on.\n" + follow
		stopLines, other := out, errOut // the stream the stop lines go to, and the other
		if extra != nil {
			stopLines, other = errOut, out
		}
		if code != 130 || !strings.HasSuffix(stopLines, stop) || strings.Contains(errOut, "stopped watching") {
			t.Errorf("%v: exit %d, stdout\n%s\nstderr %q; want 130, the stop lines last and no diagnostic after them", extra, code, out, errOut)
		}
		if extra != nil && (!strings.Contains(other, "op_BWSYNTH") || !strings.Contains(other, "running")) {
			t.Errorf("%v: standard output %q; want the operation resource, state running", extra, other)
		}
	}
}

// TestOperationWatchReadUnknown: when the stream ends and the operation read that follows is an
// answer the client cannot read, the state of the operation is unknown, not refused: exit 3, and the
// message names the command that reads it. A refusal the server named keeps exit 1.
func TestOperationWatchReadUnknown(t *testing.T) {
	for _, extra := range [][]string{nil, {"-o", "json"}} {
		f := &opFake{stream: []string{sse(1, "commitment", commitment)}, badReadFrom: 2,
			body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]")}
		srv := httptest.NewServer(f)
		automationContext(t, srv.URL)
		code, out, errOut := runCtl(t, append([]string{"operation", "watch", "op_BWSYNTH", "--timeout", "5s"}, extra...)...)
		srv.Close()
		if code != 3 || !strings.Contains(errOut, "the state of operation op_BWSYNTH was not read") ||
			!strings.Contains(errOut, "bronzewardctl operation show 'op_BWSYNTH'") || (extra != nil && out != "") {
			t.Errorf("%v: exit %d, stdout\n%s\nstderr %q; want 3, the operation to read named and no resource", extra, code, out, errOut)
		}
	}
	f := &opFake{stream: []string{sse(1, "commitment", commitment)}, forbidFrom: 2,
		body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]")}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	code, _, errOut := runCtl(t, "operation", "watch", "op_BWSYNTH", "--timeout", "5s")
	if code != 1 || strings.Contains(errOut, "was not read") {
		t.Errorf("a 403 on the read: exit %d, stderr %q; want 1 and no unknown-outcome text", code, errOut)
	}
}

// TestOperationWatchInterruptTerminal: the first Ctrl-C on a watch whose operation ends within the grace
// time gets the operation's own answer, not exit 130: what the watch read is the outcome, so it exits 0
// for a completed operation and prints no stop line.
func TestOperationWatchInterruptTerminal(t *testing.T) {
	for _, c := range []struct {
		name, state, result, failure, want string
		code                               int
	}{
		{"completed", "completed", `{"appliedRelease":"rel_BWSYNTH","observation":"obs_c"}`, "null", "applied rel_BWSYNTH", 0},
		{"failed", "failed", "null", `{"cause":"verification-mismatch","attempts":2}`, "verification-mismatch", 4},
	} {
		t.Run(c.name, func(t *testing.T) { interruptTerminal(t, c.state, c.result, c.failure, c.want, c.code) })
	}
}

// interruptTerminal is TestOperationWatchInterruptTerminal's run: the operation ends with state, result
// and failure, and the command prints want and exits with code.
func interruptTerminal(t *testing.T, state, result, failure, want string, code int) {
	// The stream stays open and sends a keep-alive every 10 ms; the watch asks whether to stop at each
	// one. Nothing but the Ctrl-C ends the stream, so the operation read that follows is made only
	// after the Ctrl-C was taken, and it finds the operation ended.
	f := &opFake{body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]"), hang: make(chan struct{}), beat: 10 * time.Millisecond}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	grace := interruptGrace
	interruptGrace = 5 * time.Second
	defer func() { interruptGrace = grace }()
	var stuck atomic.Bool
	var release sync.Once
	defer release.Do(func() { close(f.hang) })          // the server closes only once its streams end
	watchdog := time.AfterFunc(10*time.Second, func() { // the Ctrl-C was not taken: release the stream and fail
		stuck.Store(true)
		release.Do(func() { close(f.hang) })
	})
	defer watchdog.Stop()
	go func() {
		for { // the stream request comes after the signal handler is set
			f.mu.Lock()
			n := f.streams
			if n > 0 {
				f.body = fmt.Sprintf(opJSON, "apply-config", state, result, failure, "[]")
			}
			f.mu.Unlock()
			if n > 0 {
				_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	got, out, errOut := runCtl(t, "operation", "watch", "op_BWSYNTH")
	if stuck.Load() {
		t.Fatal("the stream was still open after 10 s: the Ctrl-C did not end the watch's wait")
	}
	if got != code || !strings.Contains(out, want) || strings.Contains(out, "Stopped watching") || strings.Contains(errOut, "stopped watching") {
		t.Errorf("exit %d, stdout\n%s\nstderr %q; want %d, the outcome and no stop line", got, out, errOut, code)
	}
}

// TestOperationWatchDrainFails: an operation read twice as ended, with the reading of its events
// failing, is still the outcome: the exit code, the outcome line and with -o json the resource come
// from the operation, and the failed read of the events is reported beside, as publish and import do.
func TestOperationWatchDrainFails(t *testing.T) {
	f := &opFake{pagesFail: true, stream: []string{sse(1, "commitment", commitment)},
		body: fmt.Sprintf(opJSON, "apply-config", "failed", "null", `{"cause":"verification-mismatch","attempts":2}`, "[]")}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	code, out, errOut := runCtl(t, "operation", "watch", "op_BWSYNTH", "--timeout", "2s")
	if code != 4 || !strings.Contains(out, "Failed: verification-mismatch, after 2 attempts.") || !strings.Contains(out, "The server's time of this end was not read.") ||
		!strings.Contains(errOut, "the operation's last events were not shown") || !strings.Contains(errOut, "operation op_BWSYNTH ended failed") {
		t.Errorf("exit %d, stdout\n%s\nstderr %q; want 4, the outcome line and the failed read reported", code, out, errOut)
	}
	code, out, errOut = runCtl(t, "operation", "watch", "op_BWSYNTH", "--timeout", "2s", "-o", "json")
	if code != 4 || !strings.HasPrefix(out, `{"id":"op_BWSYNTH"`) || !strings.Contains(out, `"state":"failed"`) || !strings.Contains(errOut, "the operation's last events were not shown") {
		t.Errorf("-o json: exit %d, stdout\n%s\nstderr %q; want 4 and the resource", code, out, errOut)
	}
}

// TestOperationWatchOneSnapshot: with -o json the resource printed, the outcome line and the exit code
// all come from the one operation read the watch returned, with the members of the server's answer as
// they came, also when the operation changes after that read.
func TestOperationWatchOneSnapshot(t *testing.T) {
	f := &opFake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	f.body = strings.Replace(fmt.Sprintf(opJSON, "apply-config", "completed", `{"appliedRelease":"rel_BWSYNTH","observation":"obs_c"}`, "null", "[]"),
		`"lastEvent":3,`, `"lastEvent":3,"BWSYNTH-member":{"n":9007199254740993},`, 1)
	f.pages = []string{evPage(1, "operation-state", toState("verifying", "completed", ""))}
	// The reads are the command's own first, the watch's, the one for the event's time, and a fourth
	// that no part of the command should make.
	f.swapFrom = 4
	f.swapBody = fmt.Sprintf(opJSON, "apply-config", "failed", "null", `{"cause":"verification-mismatch","attempts":2}`, "[]")
	code, out, errOut := runCtl(t, "operation", "watch", "op_BWSYNTH", "-o", "json", "--timeout", "2s")
	if code != 0 || !strings.Contains(out, `"state":"completed"`) || !strings.Contains(out, `"BWSYNTH-member":{"n":9007199254740993}`) ||
		!strings.Contains(errOut, "Completed at ") || strings.Contains(out, "failed") {
		t.Errorf("exit %d, want 0 and the completed resource with the server's member, stdout\n%s\nstderr %q", code, out, errOut)
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

// TestOperationWatchEscapesCompleted: the applied release and the observation of a completed
// operation are the server's text too, and reach the terminal escaped.
func TestOperationWatchEscapesCompleted(t *testing.T) {
	const bad = "x\x1b[31m\ty\r‮z\nFAKE LINE"
	js := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	f := &opFake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	f.body = fmt.Sprintf(opJSON, "apply-config", "completed", `{"appliedRelease":`+js(bad)+`,"observation":`+js(bad)+`}`, "null", "[]")
	f.pages = []string{evPage(1, "operation-state", toState("verifying", "completed", ""))}
	code, out, errOut := runCtl(t, "operation", "watch", "op_BWSYNTH", "--timeout", "2s")
	if i := strings.IndexFunc(strings.ReplaceAll(out+errOut, "\n", ""), func(r rune) bool { return unicode.IsControl(r) || r == '‮' }); i >= 0 {
		t.Errorf("a control character at %d in\n%q\n%q", i, out, errOut)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "FAKE LINE") {
			t.Errorf("a line of the server's making in\n%q", out)
		}
	}
	if want := `x\x1b[31m\x09y\x0d\u202ez\x0aFAKE LINE`; code != 0 || !strings.Contains(out, "applied "+want+", confirmed by observation "+want+".") {
		t.Errorf("exit %d; the escaped release is not shown in\n%q", code, out)
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
	if ev := readLast(c, "op_BWSYNTH", "cancelled", "ep_BWSYNTH-2"); ev == nil || ev.At == nil {
		t.Errorf("the state and epoch the watch returned: readLast gave %v; want the last event with its time", ev)
	}
	if ev := readLast(c, "op_BWSYNTH", "completed", "ep_BWSYNTH-2"); ev != nil {
		t.Errorf("another state than the watch returned: readLast gave %v; want nil", ev)
	}
	if ev := readLast(c, "op_BWSYNTH", "cancelled", "ep_BWSYNTH-1"); ev != nil {
		t.Errorf("another epoch than the watch returned: readLast gave %v; want nil", ev)
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
	for _, c := range []struct {
		name, late, want, not string
		created               string // the epoch member of the operation, which is the epoch it was created in
		restored              bool   // the pages hold the new epoch's events too, as after a restore before the page read
	}{
		{"no restore", "", "(server): applied rel_BWSYNTH", "was not read", "", false},
		{"created before a recovery, read in the new epoch", "", "(server): applied rel_BWSYNTH", "was not read", "ep_BWSYNTH-1", false},
		{"restored after the pages", "ep_BWSYNTH-3", "The server's time of this end was not read.", "(server)", "", false},
		{"restored before the pages", "ep_BWSYNTH-3", "The server's time of this end was not read.", "(server)", "", true},
	} {
		f.mu.Lock()
		f.late, f.reads = c.late, 0
		f.body = fmt.Sprintf(opJSON, "apply-config", "completed", `{"appliedRelease":"rel_BWSYNTH","observation":"obs_c"}`, "null", "[]")
		if c.created != "" {
			f.body = strings.Replace(f.body, `"epoch":"ep_BWSYNTH-2"`, `"epoch":"`+c.created+`"`, 1)
		}
		f.pages = []string{evPage(1, "commitment", commitment), evPage(2, "operation-state", toState("verifying", "completed", ""))}
		f.pageReqs, f.lateFrom, f.latePages = 0, 0, nil
		if c.restored { // the watch's page read finds the old epoch's events, the read after it the new epoch's
			f.lateFrom = 2
			for _, p := range f.pages {
				f.latePages = append(f.latePages, strings.ReplaceAll(p, "ep_BWSYNTH-2", "ep_BWSYNTH-3"))
			}
		}
		f.mu.Unlock()
		code, out, _ := runCtl(t, "operation", "watch", "op_BWSYNTH", "--timeout", "2s")
		if code != 0 || !strings.Contains(out, c.want) || strings.Contains(out, c.not) {
			t.Errorf("%s: exit %d, want %q and not %q in\n%s", c.name, code, c.want, c.not, out)
		}
	}
}
