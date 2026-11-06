package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unicode"

	"github.com/ginsys/bronzeward/internal/client"
)

// blockedWriter is a terminal that does not take a write: the first write signals entered and waits
// until release is closed.
type blockedWriter struct {
	entered, release chan struct{}
	once             sync.Once
}

func newBlockedWriter() *blockedWriter {
	return &blockedWriter{entered: make(chan struct{}), release: make(chan struct{})}
}

func (w *blockedWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return len(p), nil
}

// TestOperationWatchInterruptStderrBlocked: standard error does not take a write (a terminal stopped
// by flow control, a pipe nobody reads). The watch's client wrote a -v line to it and waits there.
// The first Ctrl-C still prints the stop lines on standard output and exits 130 after the grace: a
// blocked write on one stream does not hold the stop lines of the other. The server name is not
// loopback, so the client names its proxy with -v in its first request (see TestVerboseProxyIsEscaped).
func TestOperationWatchInterruptStderrBlocked(t *testing.T) {
	automationContext(t, "https://bw.example.invalid")
	guard := make(chan os.Signal, 1)
	signal.Notify(guard, os.Interrupt) // a Ctrl-C sent too early cannot end the test binary
	defer signal.Stop(guard)
	grace := interruptGrace
	interruptGrace = 100 * time.Millisecond
	defer func() { interruptGrace = grace }()
	errW := newBlockedWriter()
	defer close(errW.release) // the abandoned watch ends with the test, whatever the outcome
	var stdout bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- run([]string{"-v", "operation", "watch", "op_BWSYNTH"}, &stdout, errW) }()
	select {
	case <-errW.entered: // the signal handler is set before the client opens
	case <-time.After(5 * time.Second):
		t.Fatal("no write to standard error within 5 s: the client wrote no -v line in its first request (assumption A1)")
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	select {
	case code := <-done:
		stop := "Stopped watching at your request. Operation op_BWSYNTH goes on; the state it has now was not read.\n"
		follow := "Follow it again with: bronzewardctl operation watch 'op_BWSYNTH'\n"
		if code != 130 || !strings.HasSuffix(stdout.String(), stop+follow) {
			t.Errorf("exit %d, stdout %q; want 130 and the stop lines last on standard output", code, stdout.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the command did not return within 5 s of the first Ctrl-C: the stop lines wait on the blocked standard error")
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
		var o bytes.Buffer
		code, errOut := interruptedWatch(t, &o, extra...)
		out := o.String()
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

// failingWriter is a standard output that refuses every write, as a closed pipe or /dev/full does.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write refused") }

// TestOperationWatchInterruptUnwritable: when the resource cannot be written after the first Ctrl-C,
// one note goes to standard error before the stop lines, the follow line stays last, and the exit is
// still 130: the user ended the watch, and the operation goes on.
func TestOperationWatchInterruptUnwritable(t *testing.T) {
	code, errOut := interruptedWatch(t, failingWriter{}, "-o", "json")
	follow := "Follow it again with: bronzewardctl operation watch 'op_BWSYNTH'\n"
	note := "bronzewardctl: write refused; the operation was not printed\n"
	if code != 130 || !strings.HasSuffix(errOut, follow) || strings.Count(errOut, note) != 1 ||
		strings.Index(errOut, note) > strings.Index(errOut, "Stopped watching") {
		t.Errorf("exit %d, stderr %q; want 130, one note before the stop lines and the follow line last", code, errOut)
	}
}

// afterInterruptContext, when set, runs after interruptedWatch made its context: a test breaks the
// configuration directory there.
var afterInterruptContext func(*testing.T)

// interruptedWatch runs operation watch against a stream that stays open, sends the first Ctrl-C, and
// ends the stream within the grace time: the watch reads the still running operation and stops.
func interruptedWatch(t *testing.T, stdout io.Writer, extra ...string) (int, string) {
	t.Helper()
	var e bytes.Buffer
	code := interruptedWatchTo(t, stdout, &e, extra...)
	return code, e.String()
}

// interruptedWatchTo is interruptedWatch with the standard error of the caller.
func interruptedWatchTo(t *testing.T, stdout, stderr io.Writer, extra ...string) int {
	t.Helper()
	f := &opFake{body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]"), hang: make(chan struct{})}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	if afterInterruptContext != nil {
		afterInterruptContext(t)
	}
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
	return run(append([]string{"operation", "watch", "op_BWSYNTH"}, extra...), stdout, stderr)
}

// TestOperationWatchReadUnknown: when the stream ends and the operation read that follows is an
// answer the client cannot read, the state of the operation is unknown, not refused: exit 3, and the
// message names the command that reads it. Only a problem document the server named keeps exit 1.
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
	// A 403 whose body stops short names no problem: the status alone does not say what the server
	// decided, so the state is unknown.
	cut := &opFake{stream: []string{sse(1, "commitment", commitment)}, cutFrom: 2,
		body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]")}
	cutSrv := httptest.NewServer(cut)
	defer cutSrv.Close()
	automationContext(t, cutSrv.URL)
	code, _, errOut = runCtl(t, "operation", "watch", "op_BWSYNTH", "--timeout", "5s")
	if code != 3 || !strings.Contains(errOut, "answered 403") || !strings.Contains(errOut, "reading the answer failed") ||
		!strings.Contains(errOut, "bronzewardctl operation show 'op_BWSYNTH'") {
		t.Errorf("a cut-off 403 on the read: exit %d, stderr %q; want 3 and the operation to read named", code, errOut)
	}
}

// TestOperationWatchReadUnknownAfterEventsRefusal: when the events of a finished operation are
// refused and the operation read that confirms it is then an answer the client cannot read, the
// operation read decides: exit 3 with the command that reads the state, not exit 1 for the events.
func TestOperationWatchReadUnknownAfterEventsRefusal(t *testing.T) {
	f := &opFake{stream: []string{sse(1, "commitment", commitment)}, pagesFail: true, badReadFrom: 3,
		body: fmt.Sprintf(opJSON, "apply-config", "completed", `{"appliedRelease":"rel_BWSYNTH","observation":"obs_c"}`, "null", "[]")}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	code, _, errOut := runCtl(t, "operation", "watch", "op_BWSYNTH", "--timeout", "5s")
	if code != 3 || !strings.Contains(errOut, "the state of operation op_BWSYNTH was not read") ||
		!strings.Contains(errOut, "bronzewardctl operation show 'op_BWSYNTH'") {
		t.Errorf("exit %d, stderr %q; want 3 and the operation to read named", code, errOut)
	}
}

// TestUnreadState: the error of a watch that read no operation. Only a problem document the server
// named keeps its own exit code; the operation read, the last part of a joined error, decides which.
func TestUnreadState(t *testing.T) {
	// The suite's proxy tunnels every https request to a server whose certificate nothing trusts.
	refused := client.Live(&client.Context{Name: "lab", Server: "https://bronzeward.example.invalid", Credential: "automation"}, nil)
	if refused == nil || !strings.Contains(refused.Error(), "is not trusted by") {
		t.Fatalf("%v: not a refused certificate", refused)
	}
	problem := &client.Problem{Status: 403}
	garbled := errors.New("GET /api/v1/operations/op_x: the answer is not JSON")
	for _, c := range []struct {
		name    string
		err     error
		unknown bool // the exit is 3 (outcome unknown); otherwise the error stands as it is
	}{
		{"a refusal the server named", problem, false},
		{"a refused certificate", refused, true},
		{"a refused certificate in a wrapped error", fmt.Errorf("GET /x: %w", refused), true},
		{"an unreadable answer", garbled, true},
		{"events refused, then an unreadable operation read", errors.Join(problem, garbled), true},
		{"events unreadable, then a refused operation read", errors.Join(garbled, problem), false},
		{"events refused, then a refused certificate", errors.Join(problem, refused), true},
	} {
		err := unreadState("op_x", c.err)
		var u outcomeUnknown
		if got := errors.As(err, &u); got != c.unknown {
			t.Errorf("%s: outcome unknown is %v for %v; want %v", c.name, got, err, c.unknown)
		}
		if !c.unknown && err != c.err {
			t.Errorf("%s: the error was changed to %v", c.name, err)
		}
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

// TestOperationWatchInterruptDuringStartReads: a Ctrl-C while the server read or the first operation read
// stalls is the first Ctrl-C of the watch: after the 3 s grace it prints the stop line and the follow line
// last and exits 130, as for a stalled stream (docs/spec/operator-experience.md section 5.7). The command
// runs in a process of its own, because without a handler the default action of SIGINT ends it.
func TestOperationWatchInterruptDuringStartReads(t *testing.T) {
	for _, c := range []struct {
		name    string
		stalled func(f *opFake) int // the requests that have begun to wait
	}{
		{"server read", func(f *opFake) int { return f.serverReqs }},
		{"first operation read", func(f *opFake) int { return f.readStalls }},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &opFake{body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]")}
			hang := make(chan struct{})
			if c.name == "server read" {
				f.serverHang = hang
			} else {
				f.readHang = hang
			}
			srv := httptest.NewServer(f)
			defer srv.Close()
			defer close(hang)
			automationContext(t, srv.URL)
			var stdout, stderr bytes.Buffer
			cmd := exec.Command(os.Args[0])
			cmd.Env = append(os.Environ(), "BW_WATCH_HELPER=1")
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			ended := make(chan error, 1)
			go func() { ended <- cmd.Wait() }()
			defer func() { _ = cmd.Process.Kill() }()
			for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
				f.mu.Lock()
				n := c.stalled(f)
				f.mu.Unlock()
				if n > 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the process did not reach the stalled read")
				}
			}
			_ = cmd.Process.Signal(syscall.SIGINT)
			select {
			case err := <-ended:
				stop := "Stopped watching at your request. Operation op_BWSYNTH goes on; the state it has now was not read.\n"
				follow := "Follow it again with: bronzewardctl operation watch 'op_BWSYNTH'\n"
				var x *exec.ExitError
				if !errors.As(err, &x) || x.ExitCode() != 130 || !strings.HasSuffix(stdout.String(), stop+follow) {
					t.Errorf("the process ended with %v, stdout\n%s\nstderr %q; want exit 130 and the stop lines last", err, stdout.String(), stderr.String())
				}
			case <-time.After(10 * time.Second):
				t.Error("the first Ctrl-C during the stalled read did not end the watch within the grace")
			}
		})
	}
}

// TestOperationWatchSlowStartTimeout: a server read that answers only after --timeout has run out ends the
// watch as not terminal at the timeout (exit 5), and no event stream is opened for it: the stop is asked
// before the first stream, not only after it.
func TestOperationWatchSlowStartTimeout(t *testing.T) {
	f := &opFake{body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]"), serverHang: make(chan struct{})}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	release := time.AfterFunc(1500*time.Millisecond, func() { close(f.serverHang) })
	defer release.Stop()
	code, _, errOut := runCtl(t, "operation", "watch", "op_BWSYNTH", "--timeout", "1s")
	f.mu.Lock()
	streams := f.streams
	f.mu.Unlock()
	if code != 5 || !strings.Contains(errOut, "after --timeout") || streams != 0 {
		t.Errorf("exit %d, stderr %q, %d streams opened; want 5, the timeout named and no stream", code, errOut, streams)
	}
}

// TestOperationWatchOutage: when the operation read after a cut stream meets a server that stays out of
// reach, the watch repeats it until its own end instead of after five tries (client-contracts.md
// section 8.6 item 2; #354's T9). The timeout ends it with exit 5 and the operation as the first read
// showed it, and the first Ctrl-C ends it at once with exit 130, not after the delay of the retry.
func TestOperationWatchOutage(t *testing.T) {
	newOutage := func(t *testing.T) *opFake {
		t.Helper()
		f := &opFake{stream: []string{sse(1, "commitment", commitment)}, outageFrom: 2, outageReads: 1000,
			body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]")}
		srv := httptest.NewServer(f)
		t.Cleanup(srv.Close)
		automationContext(t, srv.URL)
		return f
	}
	t.Run("the timeout during the outage: exit 5, the operation as last read", func(t *testing.T) {
		f := newOutage(t)
		began := time.Now()
		code, _, errOut := runCtl(t, "operation", "watch", "op_BWSYNTH", "--timeout", "1500ms")
		f.mu.Lock()
		reads := f.reads
		f.mu.Unlock()
		// The waits of 1 s and 2 s mean two failed tries by 1.5 s, and the third read is the stop's.
		if code != 5 || !strings.Contains(errOut, "after --timeout") || strings.Contains(errOut, "was not read") || reads < 3 {
			t.Errorf("exit %d after %v with %d reads, stderr %q; want 5, the timeout named and no unread state", code, time.Since(began), reads, errOut)
		}
	})
	t.Run("Ctrl-C during a wait: exit 130 at once", func(t *testing.T) {
		f := newOutage(t)
		grace := interruptGrace
		interruptGrace = 5 * time.Second
		defer func() { interruptGrace = grace }()
		sentAt := make(chan time.Time, 1)
		go func() {
			for { // the second failed read is the one whose retry waits 2 s
				f.mu.Lock()
				n := f.reads
				f.mu.Unlock()
				if n >= 3 {
					sentAt <- time.Now()
					_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()
		code, out, errOut := runCtl(t, "operation", "watch", "op_BWSYNTH", "--timeout", "30s")
		var sent time.Time
		select {
		case sent = <-sentAt:
		default:
			t.Fatal("no interrupt was sent")
		}
		if took := time.Since(sent); code != 130 || took > time.Second || !strings.Contains(out, "Stopped watching at your request") {
			t.Errorf("exit %d %v after the interrupt, stdout %q, stderr %q; want 130 within a second (the retry waits 2 s) and the stop line", code, took, out, errOut)
		}
	})
}

// oidcRenewalContext records the oidc context "lab" on srv with a token file whose access token is about
// to expire, so opening the client renews it under the token file's lock, and takes that lock. The lock
// is held until the returned release runs (or the test ends). The renewal itself fails fast at the
// identity provider's .invalid name and only shows a notice.
func oidcRenewalContext(t *testing.T, srv string) (lock string, release func()) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_STATE_HOME", home)
	t.Setenv("BRONZEWARD_TOKEN_FILE", "")
	tok := filepath.Join(home, "tokens")
	exp := strconv.FormatInt(time.Now().Add(10*time.Second).Unix(), 10)
	jwt := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://idp.example.invalid","sub":"h-viewer","exp":`+exp+`}`)) + ".BWSYNTH-sig"
	if err := client.WriteTokens(tok, &client.Tokens{AccessToken: jwt, RefreshToken: "BWSYNTH-rt"}); err != nil {
		t.Fatal(err)
	}
	if err := client.UpdateConfig(func(c *client.Config) error {
		c.Current = "lab"
		c.Contexts = []client.Context{{Name: "lab", Server: srv, Credential: "oidc",
			OIDC: &client.OIDC{Issuer: "https://idp.example.invalid", ClientID: "BWSYNTH-cli", TokenFile: tok}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	lock = tok + ".lock"
	held, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release = func() { once.Do(func() { _ = held.Close() }) }
	t.Cleanup(release) // before the Flock: a fatal exit of it must not leave the file open
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	return lock, release
}

// TestOperationWatchTimeoutDuringOpen: --timeout counts the opening of the client. The renewal lock is held
// for 3 s with --timeout 1s, so the timeout runs out 2 s before the opening returns (a margin over 1.5 s
// for a loaded host). The watch then ends as not terminal at the timeout (exit 5) and opens no event stream.
func TestOperationWatchTimeoutDuringOpen(t *testing.T) {
	f := &opFake{body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]")}
	srv := httptest.NewServer(f)
	defer srv.Close()
	_, release := oidcRenewalContext(t, srv.URL)
	freed := time.AfterFunc(3*time.Second, release)
	defer freed.Stop()
	code, _, errOut := runCtl(t, "operation", "watch", "op_BWSYNTH", "--timeout", "1s")
	f.mu.Lock()
	streams := f.streams
	f.mu.Unlock()
	if code != 5 || !strings.Contains(errOut, "after --timeout") || streams != 0 {
		t.Errorf("exit %d, stderr %q, %d streams opened; want 5, the timeout named and no stream", code, errOut, streams)
	}
}

// TestOperationWatchOpenError: a client that cannot be opened returns its own error with its own exit code,
// as operation show does for the same context, and neither panics on the missing client nor reads the
// state of the operation as unknown (exit 3): no request was sent.
func TestOperationWatchOpenError(t *testing.T) {
	f := &opFake{body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]")}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	if err := os.Remove(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "token")); err != nil {
		t.Fatal(err)
	}
	showCode, _, _ := runCtl(t, "operation", "show", "op_BWSYNTH")
	code, _, errOut := runCtl(t, "operation", "watch", "op_BWSYNTH", "--timeout", "1m")
	if showCode == 0 || code != showCode || code == 3 || strings.Contains(errOut, "panic") {
		t.Errorf("watch exit %d, stderr %q; operation show exit %d; want the same non-zero code, not 3", code, errOut, showCode)
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
		t.Cleanup(srv.Close) // at once, so that a fatal exit of automationContext leaves no listener
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

// TestOperationWatchEscapesProblemFields: the failure of a publish or ingest operation is a problem
// document, and its cause, title, detail and instance are each the server's text. A hostile string with a
// line feed in only one of them reaches the terminal escaped and starts no line of its own. In -o table the
// outcome is on standard output; in -o json it is on standard error (standard output holds the resource,
// which is JSON and escapes by its own rules), so the case runs in both and reads the stream that holds the
// outcome. One case per field and mode names the one that fails.
func TestOperationWatchEscapesProblemFields(t *testing.T) {
	const bad = "x\x1b[31m\ty\r\u202ez\nFAKE LINE"
	const shown = `x\x1b[31m\x09y\x0d\u202ez\x0aFAKE LINE`
	js := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	f := &opFake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	for _, c := range []struct{ field, errBody string }{
		{"cause", `{"cause":` + js(bad) + `}`},
		{"title", `{"title":` + js(bad) + `,"status":409}`},
		{"detail", `{"title":"The job ended","detail":` + js(bad) + `,"status":409}`},
		{"instance", `{"title":"The job ended","instance":` + js(bad) + `,"status":409}`},
	} {
		for _, mode := range []struct {
			name  string
			extra []string
		}{{"table", nil}, {"json", []string{"-o", "json"}}} {
			f.mu.Lock()
			f.body = fmt.Sprintf(opJSONNoPlan, "publish", "failed", publishSubject, "null", c.errBody)
			f.stream, f.pages = nil, []string{evPage(1, "queued", "{}"), evPage(2, "failed", `{"code":"recovery"}`)}
			f.mu.Unlock()
			code, out, errOut := runCtl(t, append([]string{"operation", "watch", "op_BWSYNTH", "--timeout", "2s"}, mode.extra...)...)
			// The streams that carry the client's own text: both in table mode, standard error alone in json.
			streams := map[string]string{"stdout": out, "stderr": errOut}
			outcomeText := out
			if mode.name == "json" {
				streams, outcomeText = map[string]string{"stderr": errOut}, errOut
			}
			for name, text := range streams {
				if i := strings.IndexFunc(strings.ReplaceAll(text, "\n", ""), func(r rune) bool { return unicode.IsControl(r) || r == '\u202e' }); i >= 0 {
					t.Errorf("%s -o %s: %s holds a control character at %d:\n%q", c.field, mode.name, name, i, text)
				}
				for _, l := range strings.Split(text, "\n") {
					if strings.HasPrefix(l, "FAKE LINE") {
						t.Errorf("%s -o %s: %s has a line of the server's making:\n%q", c.field, mode.name, name, text)
					}
				}
			}
			if code != 4 || !strings.Contains(outcomeText, shown) {
				t.Errorf("%s -o %s: exit %d (want 4); the escaped server text %q is not in\n%q", c.field, mode.name, code, shown, outcomeText)
			}
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
	// The pages hold an event that records a cancellation and one that records a completion; the operation
	// read stays cancelled, so only the state check of readLast can refuse the completion event.
	f.pages = []string{evPage(1, "operation-state", toState("verifying", "cancelled", "")), evPage(2, "operation-state", toState("verifying", "completed", ""))}
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

// TestOperationWatchOutputFails: a table-mode watch whose output cannot be written says so and does
// not exit 0. A completed operation exits 6 with the write error, a failed one keeps exit 4 and adds
// it, and a running one exits 6 and stops watching at once instead of waiting for --timeout.
func TestOperationWatchOutputFails(t *testing.T) {
	f := &opFake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	done := []string{evPage(1, "commitment", commitment), evPage(2, "operation-state", toState("verifying", "completed", ""))}
	for _, c := range []struct {
		name, state, result, errBody string
		stream                       []string
		code                         int
		not                          string
	}{
		{"completed", "completed", `{"appliedRelease":"rel_BWSYNTH","observation":"obs_c"}`, "null", []string{sse(1, "commitment", commitment)}, 6, ""},
		{"failed", "failed", "null", `{"cause":"verification-mismatch","attempts":2}`, []string{sse(1, "commitment", commitment)}, 4, ""},
		{"running", "running", "null", "null", []string{sse(1, "commitment", commitment)}, 6, "Still running"},
	} {
		f.mu.Lock()
		f.body = fmt.Sprintf(opJSON, "apply-config", c.state, c.result, c.errBody, "[]")
		f.stream, f.pages = c.stream, done
		f.mu.Unlock()
		var e bytes.Buffer
		code := run([]string{"operation", "watch", "op_BWSYNTH", "--timeout", "2s"}, failingWriter{}, &e)
		if code != c.code || !strings.Contains(e.String(), "write refused") || (c.not != "" && strings.Contains(e.String(), c.not)) {
			t.Errorf("%s: exit %d, stderr %q; want exit %d, the write error named and not %q", c.name, code, e.String(), c.code, c.not)
		}
	}
}

// TestOperationWatchDevFull: with standard output on /dev/full, where every write fails with ENOSPC,
// a completed watch exits 6, not 0, and names the OS error on standard error (operator-experience.md §4).
func TestOperationWatchDevFull(t *testing.T) {
	f := &opFake{stream: []string{sse(1, "commitment", commitment)},
		pages: []string{evPage(1, "commitment", commitment), evPage(2, "operation-state", toState("verifying", "completed", ""))},
		body:  fmt.Sprintf(opJSON, "apply-config", "completed", `{"appliedRelease":"rel_BWSYNTH","observation":"obs_c"}`, "null", "[]")}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("this system has no /dev/full (macOS, for one)")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer full.Close()
	var e bytes.Buffer
	if code := run([]string{"operation", "watch", "op_BWSYNTH", "--timeout", "2s"}, full, &e); code != 6 || !strings.Contains(e.String(), "no space left on device") {
		t.Errorf("exit %d, stderr %q; want 6 and the write error named", code, e.String())
	}
}

// TestOperationWatchEventLinesUnwritableJSON: with -o json the event lines go to standard error. They
// are still the output of the command, so a standard error that refuses them gives exit 6 too, not 0,
// while the resource reaches standard output.
func TestOperationWatchEventLinesUnwritableJSON(t *testing.T) {
	f := &opFake{stream: []string{sse(1, "commitment", commitment)},
		pages: []string{evPage(1, "commitment", commitment), evPage(2, "operation-state", toState("verifying", "completed", ""))},
		body:  fmt.Sprintf(opJSON, "apply-config", "completed", `{"appliedRelease":"rel_BWSYNTH","observation":"obs_c"}`, "null", "[]")}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	var o bytes.Buffer
	if code := run([]string{"operation", "watch", "op_BWSYNTH", "-o", "json", "--timeout", "2s"}, &o, failingWriter{}); code != 6 || !strings.Contains(o.String(), "op_BWSYNTH") {
		t.Errorf("exit %d, stdout %q; want 6 and the operation resource on standard output", code, o.String())
	}
}

// interruptThenFail is a standard output that takes the first two lines of a watch, then sends the
// first Ctrl-C on the third and refuses it and every line after: the output fails while the watch is
// ending by Ctrl-C.
type interruptThenFail struct {
	n       int
	taken   chan struct{} // closed by onInterrupt when the watch has taken the first Ctrl-C
	release chan struct{} // when set, the third write stays pending after the Ctrl-C until this is closed
}

func (w *interruptThenFail) Write(p []byte) (int, error) {
	w.n++
	if w.n <= 2 {
		return len(p), nil
	}
	if w.n == 3 {
		// The write fails only after the watch took the interrupt, so that the end of the output cannot
		// reach the watch before the Ctrl-C does. No sleep: onInterrupt tells it.
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		select {
		case <-w.taken:
		case <-time.After(5 * time.Second):
		}
		if w.release != nil {
			select {
			case <-w.release:
			case <-time.After(5 * time.Second):
			}
		}
	}
	return 0, errors.New("write refused")
}

// TestOperationWatchInterruptOutputFails: when the output of a watch failed before its first Ctrl-C was
// handled, the stop line and the follow line go to standard error, last, and the exit is 130: the user
// ended the watch, so the lost output does not change the exit.
func TestOperationWatchInterruptOutputFails(t *testing.T) {
	f := &opFake{stream: []string{sse(1, "commitment", commitment)},
		body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]")}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	var e bytes.Buffer
	out := &interruptThenFail{taken: make(chan struct{})}
	var taken sync.Once
	onInterrupt = func() { taken.Do(func() { close(out.taken) }) }
	defer func() { onInterrupt = nil }()
	code := run([]string{"operation", "watch", "op_BWSYNTH", "--timeout", "30s"}, out, &e)
	stop := "Stopped watching at your request. Operation op_BWSYNTH is still running, as the server last read it; it goes on.\n"
	follow := "Follow it again with: bronzewardctl operation watch 'op_BWSYNTH'\n"
	if code != 130 || !strings.HasSuffix(e.String(), stop+follow) || out.n < 3 {
		t.Errorf("exit %d after %d writes, stderr %q; want 130 and the stop lines last on standard error", code, out.n, e.String())
	}
}

// TestOperationWatchInterruptPendingWriteFails: a write to standard output is still pending when the grace
// of the first Ctrl-C ends, and it fails after the stop lines started to wait for that sink. The output
// had not failed when the stop lines picked their sink, so they would go to the failed standard output
// and be lost. They go to standard error instead, last, and the exit is 130 (ginsys/bronzeward#648).
// The write stays pending until the test sees the stop lines in the stack of the process, so no elapsed
// time decides the order. A short pause after that lets them reach the lock of the sink. A machine that
// is too slow for the pause can only make the test pass without the fix, never fail with it.
func TestOperationWatchInterruptPendingWriteFails(t *testing.T) {
	f := &opFake{stream: []string{sse(1, "commitment", commitment)},
		body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]")}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	grace := interruptGrace
	interruptGrace = 100 * time.Millisecond
	defer func() { interruptGrace = grace }()
	var e bytes.Buffer
	out := &interruptThenFail{taken: make(chan struct{}), release: make(chan struct{})}
	var taken sync.Once
	onInterrupt = func() { taken.Do(func() { close(out.taken) }) }
	defer func() { onInterrupt = nil }()
	seen := make(chan bool, 1) // whether the stop lines showed in the stack before the budget ended
	go func() {
		defer close(out.release)
		// The budget starts when the watch took the Ctrl-C, not when the command started.
		select {
		case <-out.taken:
		case <-time.After(5 * time.Second):
			seen <- false
			return
		}
		buf := make([]byte, 1<<20)
		for deadline := time.Now().Add(4 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if bytes.Contains(buf[:runtime.Stack(buf, true)], []byte("(*watchOut).stop")) {
				time.Sleep(50 * time.Millisecond)
				seen <- true
				return
			}
		}
		seen <- false
	}()
	code := run([]string{"operation", "watch", "op_BWSYNTH", "--timeout", "30s"}, out, &e)
	if !<-seen {
		t.Fatal("the stop lines never showed in the stack after the Ctrl-C: the test did not reach its interleaving")
	}
	stop := "Stopped watching at your request. Operation op_BWSYNTH goes on; the state it has now was not read.\n"
	follow := "Follow it again with: bronzewardctl operation watch 'op_BWSYNTH'\n"
	if code != 130 || !strings.HasSuffix(e.String(), stop+follow) || out.n < 3 {
		t.Errorf("exit %d after %d writes, stderr %q; want 130 and the stop lines last on standard error", code, out.n, e.String())
	}
}

// TestOperationWatchCompletionEvent: the completion time is the time of the event that records the
// operation's end, not of the last event: a response recorded after the end (the delayed answer of an
// attempt a takeover left behind) comes later and does not move it. A publish or ingest operation
// records its end as an event of the state's own name.
func TestOperationWatchCompletionEvent(t *testing.T) {
	f := &opFake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	for _, c := range []struct {
		name, kind, state, result, errBody string
		pages                              []string
		want                               string
	}{
		// apply-config: the state entry operation-state moves it to the state (internal/api/outcome.go:114,
		// attempt.go:441, recovery_acts.go:300); the response of an attempt can be recorded after it.
		{"apply-config completed, a late response", "apply-config", "completed", `{"appliedRelease":"rel_BWSYNTH","observation":"obs_c"}`, "null",
			[]string{evPage(1, "commitment", commitment), evPage(2, "operation-state", toState("verifying", "completed", "")), evPage(3, "response", `{"attempt":"att_a"}`)},
			"Completed at 2026-11-01 06:00:02 UTC (server)"},
		{"apply-config failed, a late response", "apply-config", "failed", "null", `{"cause":"verification-mismatch","attempts":2}`,
			[]string{evPage(1, "commitment", commitment), evPage(2, "operation-state", toState("verifying", "failed", "")), evPage(3, "response", `{"attempt":"att_a"}`)},
			"Failed at 2026-11-01 06:00:02 UTC (server)"},
		{"apply-config rejected", "apply-config", "rejected", "null", `{"cause":"invalid-configuration","attempts":1}`,
			[]string{evPage(1, "commitment", commitment), evPage(2, "operation-state", toState("unresolved", "rejected", "")), evPage(3, "response", `{"attempt":"att_a"}`)},
			"Rejected at 2026-11-01 06:00:02 UTC (server)"},
		{"apply-config cancelled", "apply-config", "cancelled", "null", "null",
			[]string{evPage(1, "commitment", commitment), evPage(2, "operation-state", toState("unresolved", "cancelled", "resolution")), evPage(3, "response", `{"attempt":"att_a"}`)},
			"Cancelled at 2026-11-01 06:00:02 UTC (server)"},
		// adopt: the operation is inserted completed with the adoption entry (internal/api/adoption.go:293),
		// and the applied-change entry follows it (adoption.go:319).
		{"adopt completed", "adopt", "completed", `{"adoptionRecord":"pln_BWSYNTH"}`, "null",
			[]string{evPage(1, "adoption", `{"plan":"pln_BWSYNTH","operation":"op_BWSYNTH"}`), evPage(2, "applied-change", `{"source":"adoption"}`)},
			"Completed at 2026-11-01 06:00:01 UTC (server): adopted"},
		// publish and ingest: the event is named after the state (internal/api/publish.go:158, 328, 415,
		// runner.go:445, 457, ingestions.go:428, recovery_entries.go:276).
		{"publish succeeded, a later event", "publish", "succeeded", `{"release":"rel_BWSYNTH"}`, "null",
			[]string{evPage(1, "queued", "{}"), evPage(2, "succeeded", `{"release":"rel_BWSYNTH"}`), evPage(3, "taken-over", `{"generation":2}`)},
			"Succeeded at 2026-11-01 06:00:02 UTC (server)"},
		{"publish failed", "publish", "failed", "null", `{"title":"Recovery-mode entry ended the job","status":409}`,
			[]string{evPage(1, "queued", "{}"), evPage(2, "failed", `{"code":"recovery"}`), evPage(3, "taken-over", `{"generation":2}`)},
			"Failed at 2026-11-01 06:00:02 UTC (server)"},
		{"ingest succeeded", "ingest", "succeeded", `{"importBaseRevision":3}`, "null",
			[]string{evPage(1, "started", "{}"), evPage(2, "succeeded", `{"importBaseRevision":3}`), evPage(3, "taken-over", `{"generation":2}`)},
			"Succeeded at 2026-11-01 06:00:02 UTC (server)"},
		{"ingest failed", "ingest", "failed", "null", `{"title":"The ingestion was abandoned","status":409}`,
			[]string{evPage(1, "started", "{}"), evPage(2, "failed", `{"code":"ingestion-abandoned"}`), evPage(3, "taken-over", `{"generation":2}`)},
			"Failed at 2026-11-01 06:00:02 UTC (server)"},
		{"no event records the end", "apply-config", "completed", `{"appliedRelease":"rel_BWSYNTH","observation":"obs_c"}`, "null",
			[]string{evPage(1, "commitment", commitment), evPage(2, "response", `{"attempt":"att_a"}`)},
			"The server's time of this end was not read."},
	} {
		f.mu.Lock()
		f.body = fmt.Sprintf(opJSON, c.kind, c.state, c.result, c.errBody, "[]")
		// A publish or ingest operation is the server's own shape: a draft for its subject, no plan.
		switch c.kind {
		case "publish":
			f.body = fmt.Sprintf(opJSONNoPlan, c.kind, c.state, publishSubject, c.result, c.errBody)
		case "ingest":
			f.body = fmt.Sprintf(opJSONNoPlan, c.kind, c.state, ingestSubject, c.result, c.errBody)
		}
		f.stream, f.pages = nil, c.pages
		f.mu.Unlock()
		code, out, _ := runCtl(t, "operation", "watch", "op_BWSYNTH", "--timeout", "2s")
		wantCode := map[bool]int{true: 0, false: 4}[c.state == "completed" || c.state == "succeeded"]
		if code != wantCode || !strings.Contains(out, c.want) {
			t.Errorf("%s: exit %d, want %d and %q in\n%s", c.name, code, wantCode, c.want, out)
		}
		if noPlan := c.kind == "publish" || c.kind == "ingest"; noPlan && c.state == "failed" && !strings.Contains(out, "Read it: bronzewardctl operation show 'op_BWSYNTH'\n") {
			t.Errorf("%s: no plan, so the next step is to read the operation; want it in\n%s", c.name, out)
		}
	}
}

// breakConfigDir makes the configuration directory read-only, so that the client cannot keep its state
// (the temporary file of the config is made there).
func breakConfigDir(t *testing.T) {
	t.Helper()
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "bronzeward")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
}

// TestOperationWatchInterruptSaveFails: the first Ctrl-C ends the watch with exit 130, and a failure
// to keep the client's state on the way out is still printed, once, before that exit.
func TestOperationWatchInterruptSaveFails(t *testing.T) {
	afterInterruptContext = breakConfigDir
	defer func() { afterInterruptContext = nil }()
	// With -o json the stop lines go to standard error as well: the order there is the contract.
	code, errOut := interruptedWatch(t, io.Discard, "-o", "json")
	follow := "Follow it again with: bronzewardctl operation watch 'op_BWSYNTH'\n"
	save, stop := strings.Index(errOut, "keeping the epoch"), strings.Index(errOut, "Stopped watching")
	if code != 130 || strings.Count(errOut, "keeping the epoch") != 1 || save < 0 || stop < 0 || save > stop || !strings.HasSuffix(errOut, follow) {
		t.Errorf("exit %d, stderr %q; want 130, the save failure once and before the stop lines, the follow line last", code, errOut)
	}
}

// TestOperationWatchResourceUnwritable: with -o json the resource goes to standard output after the
// outcome lines. When it cannot be written, the write error is printed beside the outcome and the
// exit code of the outcome stays: 4 for a failed operation, 5 for one still unresolved at --timeout.
func TestOperationWatchResourceUnwritable(t *testing.T) {
	f := &opFake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	for _, c := range []struct {
		state, errBody string
		code           int
	}{
		{"failed", `{"cause":"verification-mismatch","attempts":2}`, 4},
		{"unresolved", "null", 5},
	} {
		f.mu.Lock()
		f.body = fmt.Sprintf(opJSON, "apply-config", c.state, "null", c.errBody, "[]")
		f.stream, f.pages = []string{sse(1, "commitment", commitment)}, []string{evPage(1, "commitment", commitment), evPage(2, "operation-state", toState("verifying", c.state, ""))}
		f.mu.Unlock()
		var e bytes.Buffer
		code := run([]string{"operation", "watch", "op_BWSYNTH", "-o", "json", "--timeout", "2s"}, failingWriter{}, &e)
		if code != c.code || !strings.Contains(e.String(), "write refused") {
			t.Errorf("%s: exit %d, stderr %q; want exit %d and the write error named", c.state, code, e.String(), c.code)
		}
	}
}

// TestOperationWatchExitMatrix: the exit code and the message of a table-mode watch for every pair of
// an output that works or refuses its writes and an operation read that follows the stream and is
// terminal, not terminal, not JSON, a refusal the server named or a status with no problem document.
// The operation read decides the exit; a write error is named beside it and never replaces it, and an
// operation read that gave nothing leaves the state unknown (exit 3) even when the output failed.
func TestOperationWatchExitMatrix(t *testing.T) {
	done := []string{evPage(1, "commitment", commitment), evPage(2, "operation-state", toState("verifying", "completed", ""))}
	failedPages := []string{evPage(1, "commitment", commitment), evPage(2, "operation-state", toState("verifying", "failed", ""))}
	read := "bronzewardctl operation show 'op_BWSYNTH'"
	for _, c := range []struct {
		name  string
		fake  func() *opFake
		write []struct { // by output: whether its writes fail, the exit, and the parts of standard output and error
			fails bool
			code  int
			has   []string
			not   []string
		}
	}{
		{"terminal, completed", func() *opFake {
			return &opFake{stream: []string{sse(1, "commitment", commitment)}, pages: done,
				body: fmt.Sprintf(opJSON, "apply-config", "completed", `{"appliedRelease":"rel_BWSYNTH","observation":"obs_c"}`, "null", "[]")}
		}, []struct {
			fails bool
			code  int
			has   []string
			not   []string
		}{{false, 0, []string{"Completed at"}, []string{"write refused"}}, {true, 6, []string{"write refused"}, nil}}},
		{"terminal, failed", func() *opFake {
			return &opFake{stream: []string{sse(1, "commitment", commitment)}, pages: failedPages,
				body: fmt.Sprintf(opJSON, "apply-config", "failed", "null", `{"cause":"verification-mismatch","attempts":2}`, "[]")}
		}, []struct {
			fails bool
			code  int
			has   []string
			not   []string
		}{{false, 4, []string{"Failed at", "operation op_BWSYNTH ended failed"}, []string{"write refused"}},
			{true, 4, []string{"operation op_BWSYNTH ended failed", "write refused"}, nil}}},
		{"not terminal", func() *opFake {
			return &opFake{stream: []string{sse(1, "commitment", commitment)}, pages: done,
				body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]")}
		}, []struct {
			fails bool
			code  int
			has   []string
			not   []string
		}{{false, 5, []string{"Still running after --timeout", "Follow it again with: bronzewardctl operation watch 'op_BWSYNTH'"}, []string{"write refused"}},
			{true, 6, []string{"write refused", "operation op_BWSYNTH goes on", "Follow it again with: bronzewardctl operation watch 'op_BWSYNTH'"}, []string{"was not read"}}}},
		{"read is not JSON", func() *opFake {
			return &opFake{stream: []string{sse(1, "commitment", commitment)}, badReadFrom: 2,
				body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]")}
		}, []struct {
			fails bool
			code  int
			has   []string
			not   []string
		}{{false, 3, []string{"the state of operation op_BWSYNTH was not read", read}, []string{"write refused"}},
			{true, 3, []string{"the state of operation op_BWSYNTH was not read", read, "write refused"}, []string{"goes on"}}}},
		{"read refused with a problem", func() *opFake {
			return &opFake{stream: []string{sse(1, "commitment", commitment)}, forbidFrom: 2,
				body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]")}
		}, []struct {
			fails bool
			code  int
			has   []string
			not   []string
		}{{false, 1, []string{"403"}, []string{"was not read", "write refused"}},
			// The server's own refusal keeps exit 1 over a failed write (precedence: report in main.go).
			{true, 1, []string{"403", "write refused"}, []string{"was not read", "goes on"}}}},
		{"read cut off", func() *opFake {
			return &opFake{stream: []string{sse(1, "commitment", commitment)}, cutFrom: 2,
				body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]")}
		}, []struct {
			fails bool
			code  int
			has   []string
			not   []string
		}{{false, 3, []string{"reading the answer failed", read}, []string{"write refused"}},
			{true, 3, []string{"reading the answer failed", read, "write refused"}, []string{"goes on"}}}},
	} {
		for _, w := range c.write {
			srv := httptest.NewServer(c.fake())
			t.Cleanup(srv.Close) // at once, so that a fatal exit of automationContext leaves no listener
			automationContext(t, srv.URL)
			var stdout io.Writer = &bytes.Buffer{}
			if w.fails {
				stdout = failingWriter{}
			}
			var e bytes.Buffer
			code := run([]string{"operation", "watch", "op_BWSYNTH", "--timeout", "2s"}, stdout, &e)
			srv.Close()
			text := e.String()
			if b, ok := stdout.(*bytes.Buffer); ok {
				text = b.String() + text
			}
			if code != w.code {
				t.Errorf("%s, output fails %v: exit %d, want %d:\n%s", c.name, w.fails, code, w.code, text)
			}
			for _, h := range w.has {
				if !strings.Contains(text, h) {
					t.Errorf("%s, output fails %v: %q missing in\n%s", c.name, w.fails, h, text)
				}
			}
			for _, n := range w.not {
				if strings.Contains(text, n) {
					t.Errorf("%s, output fails %v: %q present in\n%s", c.name, w.fails, n, text)
				}
			}
		}
	}
}

// TestOperationWatchExitMatrixByRead: the read-index column of the exit matrix. Every operation read of
// the watch is the first, the one after the stream, or the one that confirms an end for its time, and
// each can fail three ways: an answer that is not JSON, a 403 whose body stops short, a 403 problem
// the server named. A read that gave nothing leaves the state unknown (exit 3, with the command that
// reads it) unless the server named a problem (exit 1); the read that only confirms an end for its time
// keeps the outcome and says the time was not read. The same holds when the output refuses its
// writes, which adds the write error beside the exit.
func TestOperationWatchExitMatrixByRead(t *testing.T) {
	done := []string{evPage(1, "commitment", commitment), evPage(2, "operation-state", toState("verifying", "completed", ""))}
	completed := fmt.Sprintf(opJSON, "apply-config", "completed", `{"appliedRelease":"rel_BWSYNTH","observation":"obs_c"}`, "null", "[]")
	running := fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]")
	read := "bronzewardctl operation show 'op_BWSYNTH'"
	// The server read that comes before the first operation read answers with no JSON: nothing was read
	// of the operation, so its state is unknown as well.
	bad := &opFake{badServer: true, body: running}
	badSrv := httptest.NewServer(bad)
	t.Cleanup(badSrv.Close) // at once, so that a fatal exit of automationContext leaves no listener
	automationContext(t, badSrv.URL)
	if code, _, errOut := runCtl(t, "operation", "watch", "op_BWSYNTH", "--timeout", "2s"); code != 3 ||
		!strings.Contains(errOut, "the state of operation op_BWSYNTH was not read") || !strings.Contains(errOut, read) {
		t.Errorf("the server read is not JSON: exit %d, stderr %q; want 3 and the operation to read named", code, errOut)
	}
	badSrv.Close()
	for _, c := range []struct {
		body  string // the state the operation reads as
		index int    // the operation read, counted from 1, from which the fault applies
	}{{running, 1}, {completed, 1}, {running, 2}, {completed, 2}, {completed, 3}} {
		for _, fault := range []string{"not JSON", "cut off", "problem"} {
			for _, fails := range []bool{false, true} {
				f := &opFake{stream: []string{sse(1, "commitment", commitment)}, pages: done, body: c.body}
				switch fault {
				case "not JSON":
					f.badReadFrom = c.index
				case "cut off":
					f.cutFrom = c.index
				default:
					f.forbidFrom = c.index
				}
				code, has, not := 3, []string{"the state of operation op_BWSYNTH was not read", read}, []string{"goes on"}
				switch {
				case c.index == 3: // the read that confirms the end: the outcome stays, only its time is missing
					code, has, not = 0, []string{"Completed", "The server's time of this end was not read."}, []string{"the state of operation op_BWSYNTH was not read"}
					if fails { // the outcome's exit 0 becomes 6 with the write error beside it
						code, has = 6, []string{"write refused"}
					}
				case fault == "cut off":
					has = []string{"reading the answer failed", read}
				case fault == "problem":
					code, has, not = 1, []string{"403"}, []string{"was not read", "goes on"}
				}
				if fails && c.index == 2 {
					has = append(has, "write refused")
				}
				srv := httptest.NewServer(f)
				t.Cleanup(srv.Close) // at once, so that a fatal exit of automationContext leaves no listener
				automationContext(t, srv.URL)
				var stdout io.Writer = &bytes.Buffer{}
				if fails {
					stdout = failingWriter{}
				}
				var e bytes.Buffer
				got := run([]string{"operation", "watch", "op_BWSYNTH", "--timeout", "2s"}, stdout, &e)
				srv.Close()
				text := e.String()
				if b, ok := stdout.(*bytes.Buffer); ok {
					text = b.String() + text
				}
				name := fmt.Sprintf("read %d %s, output fails %v", c.index, fault, fails)
				if got != code {
					t.Errorf("%s: exit %d, want %d:\n%s", name, got, code, text)
				}
				for _, h := range has {
					if !strings.Contains(text, h) {
						t.Errorf("%s: %q missing in\n%s", name, h, text)
					}
				}
				for _, n := range not {
					if strings.Contains(text, n) {
						t.Errorf("%s: %q present in\n%s", name, n, text)
					}
				}
			}
		}
	}
}
