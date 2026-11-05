package main

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// heldWriter is a terminal that keeps what it is given and does not take the first write until release
// is closed.
type heldWriter struct {
	entered, release chan struct{}
	once             sync.Once
	mu               sync.Mutex
	got              bytes.Buffer
}

func newHeldWriter() *heldWriter {
	return &heldWriter{entered: make(chan struct{}), release: make(chan struct{})}
}

func (w *heldWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.got.Write(p)
}

func (w *heldWriter) text() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.got.String()
}

// TestWatchOutStopWaitsOnBlockedSink: the stop lines of the first Ctrl-C are the output of the command.
// When the main stream is blocked, stop waits there and prints every line, in order, once the stream
// takes them; no timeout drops a line. (A second Ctrl-C ends the command at once: see
// TestOperationWatchSecondCtrlCEndsBlockedWatch.)
func TestWatchOutStopWaitsOnBlockedSink(t *testing.T) {
	w := newHeldWriter()
	out := &watchOut{main: &sink{w: w}}
	done := make(chan struct{})
	go func() {
		out.stop(&sink{w: io.Discard}, "Stopped watching.", "Follow it again.")
		close(done)
	}()
	select {
	case <-w.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("stop wrote nothing to the main stream within 5 s")
	}
	select {
	case <-done:
		t.Fatal("stop returned while the main stream was blocked: the lines are dropped or moved")
	case <-time.After(300 * time.Millisecond):
	}
	close(w.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return within 5 s of the stream taking its lines")
	}
	if got, want := w.text(), "Stopped watching.\nFollow it again.\n"; got != want {
		t.Errorf("main stream got %q; want %q", got, want)
	}
}

// TestOperationWatchInterruptSaveNoteStderrBlocked: in the table view the stop lines go to standard
// output. A failure to keep the client's state is a note on standard error. When standard error does
// not take that note (a terminal stopped by flow control), the note waits no longer than noteWait and
// the stop lines still print on standard output, with exit 130.
func TestOperationWatchInterruptSaveNoteStderrBlocked(t *testing.T) {
	afterInterruptContext = breakConfigDir
	defer func() { afterInterruptContext = nil }()
	wait := noteWait
	noteWait = 50 * time.Millisecond
	defer func() { noteWait = wait }()
	errW := newBlockedWriter()
	var released sync.Once
	release := func() { released.Do(func() { close(errW.release) }) }
	defer release() // the abandoned note ends with the test, whatever the outcome
	var late atomic.Bool
	watchdog := time.AfterFunc(15*time.Second, func() { late.Store(true); release() })
	defer watchdog.Stop()
	var stdout bytes.Buffer
	code := interruptedWatchTo(t, &stdout, errW)
	if late.Load() {
		t.Fatal("the command did not return within 15 s: the stop lines wait on the blocked standard error")
	}
	select {
	case <-errW.entered:
	default:
		t.Error("nothing was written to standard error: the save failure was not noted, so the test proves nothing")
	}
	stop := "Stopped watching at your request. Operation op_BWSYNTH is still running, as the server last read it; it goes on.\n" +
		"Follow it again with: bronzewardctl operation watch 'op_BWSYNTH'\n"
	if code != 130 || !strings.HasSuffix(stdout.String(), stop) {
		t.Errorf("exit %d, stdout %q; want 130 and the stop lines last on standard output", code, stdout.String())
	}
}
