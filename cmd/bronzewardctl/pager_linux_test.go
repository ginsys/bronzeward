package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/term"
)

// takesSignal skips a test of the pager's reaction to sig when this process inherited sig as
// ignored (a suite run under nohup): the pager then leaves it ignored, as it should
// (TestPagerKeepsIgnoredSignal), so there is no reaction to test.
func takesSignal(t *testing.T, sig syscall.Signal) {
	t.Helper()
	if signal.Ignored(sig) {
		t.Skipf("%v is ignored in this process", sig)
	}
}

// screen collects what the pager writes to a pseudo-terminal, as a person's terminal would show it.
type screen struct {
	mu sync.Mutex
	b  []byte
}

func watchScreen(ctl *os.File) *screen {
	s := &screen{}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ctl.Read(buf)
			s.mu.Lock()
			s.b = append(s.b, buf[:n]...)
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return s
}

func (s *screen) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.b)
}

// count waits up to 10 s for sub to show n times or more and returns how many times it shows.
func (s *screen) count(sub string, n int) int {
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if c := strings.Count(s.text(), sub); c >= n {
			return c
		}
	}
	return strings.Count(s.text(), sub)
}

// lineMode reports whether the terminal of ctl is back in canonical mode with echo, as a shell
// leaves it, waiting up to 10 s.
func lineMode(t *testing.T, ctl *os.File) bool {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		var tio syscall.Termios
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ctl.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&tio))); errno != 0 {
			t.Fatalf("reading the terminal modes: %v", errno)
		} else if tio.Lflag&syscall.ICANON != 0 {
			return true
		}
	}
	return false
}

// reviewLines is a review long enough for two pages on a terminal of 10 rows.
func reviewLines() []string {
	var l []string
	for i := 1; i <= 30; i++ {
		l = append(l, fmt.Sprintf("BWSYNTH review line %d", i))
	}
	return l
}

// checkLeft checks what a signal during the pager must leave: the terminal in line mode, the
// alternate screen left, and no review text written after that.
func checkLeft(t *testing.T, ctl *os.File, s *screen, what string) {
	t.Helper()
	if !lineMode(t, ctl) {
		t.Errorf("%s: the terminal is still raw", what)
	}
	// The collector reads the terminal on its own schedule: wait for the mark that the alternate
	// screen was left, then for a late write.
	if s.count(altScreenOff, 1) == 0 {
		t.Fatalf("%s: the alternate screen was not left: %q", what, s.text())
	}
	time.Sleep(50 * time.Millisecond)
	text := s.text()
	off := strings.LastIndex(text, altScreenOff)
	if tail := text[off+len(altScreenOff):]; strings.Contains(tail, "BWSYNTH") || strings.Contains(tail, clearScreen) {
		t.Errorf("%s: the normal screen got text after the alternate screen was left: %q", what, tail)
	}
}

// TestPagerSignals sends a real SIGINT, SIGTERM and SIGHUP to the process while the pager shows the
// review: it leaves line mode and the alternate screen, writes nothing after, and reports the
// signal as the one the process is to end by (#546 part 1).
func TestPagerSignals(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			takesSignal(t, sig)
			ctl, out := onPty(t, 24, 80)
			s := watchScreen(ctl)
			errc := make(chan error, 1)
			go func() { errc <- (&env{}).pager(out, reviewLines(), 24, 80) }()
			if s.count("space or f forward", 1) == 0 {
				t.Fatalf("the pager showed no key line: %q", s.text())
			}
			if err := syscall.Kill(os.Getpid(), sig); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-errc:
				if e, ok := err.(endedBySignal); !ok || e.sig != sig {
					t.Errorf("the pager returned %v; want it ended by %v", err, sig)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the pager did not end on the signal")
			}
			checkLeft(t, ctl, s, sig.String())
		})
	}
}

// TestPagerSignalBeforeRaw: a signal that comes after the handler is registered and before the
// terminal is raw ends the pager too, with the terminal restored. Registering after MakeRaw leaves
// that window to the default action, which ends the process with the terminal raw.
func TestPagerSignalBeforeRaw(t *testing.T) {
	takesSignal(t, syscall.SIGTERM)
	ctl, out := onPty(t, 24, 80)
	s := watchScreen(ctl)
	old := makeRaw
	t.Cleanup(func() { makeRaw = old })
	makeRaw = func(fd int) (*term.State, error) {
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		time.Sleep(100 * time.Millisecond) // the runtime hands the signal to the handler
		return old(fd)
	}
	err := (&env{}).pager(out, reviewLines(), 24, 80)
	if e, ok := err.(endedBySignal); !ok || e.sig != syscall.SIGTERM {
		t.Errorf("the pager returned %v; want it ended by SIGTERM", err)
	}
	checkLeft(t, ctl, s, "before raw mode")
}

// afterQuit is a keySource that runs fn once the key that quits has been taken.
type afterQuit struct {
	keySource
	fn func()
}

func (k afterQuit) next() (byte, event, error) {
	key, ev, err := k.keySource.next()
	if key == 'q' && ev == evKey {
		k.fn()
	}
	return key, ev, err
}

// TestPagerSignalAtReturn: a signal that came while the pager was taking its quit key, and was not
// yet seen, is handled at its return: the pager reports it and the marks prompt does not start.
func TestPagerSignalAtReturn(t *testing.T) {
	takesSignal(t, syscall.SIGHUP)
	ctl, out := onPty(t, 24, 80)
	s := watchScreen(ctl)
	old := pageKeys
	t.Cleanup(func() { pageKeys = old })
	pageKeys = func(k keySource) keySource {
		return afterQuit{k, func() {
			_ = syscall.Kill(os.Getpid(), syscall.SIGHUP)
			time.Sleep(100 * time.Millisecond)
		}}
	}
	errc := make(chan error, 1)
	go func() { errc <- (&env{}).pager(out, reviewLines(), 24, 80) }()
	s.count("space or f forward", 1)
	ctl.WriteString("q")
	select {
	case err := <-errc:
		if e, ok := err.(endedBySignal); !ok || e.sig != syscall.SIGHUP {
			t.Errorf("the pager returned %v; want it ended by SIGHUP", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the pager did not return")
	}
	checkLeft(t, ctl, s, "at return")
}

// TestPagerSetupErrorKeepsSignal: a termination signal that came before the terminal could not be
// made raw is still the outcome, not the setup error.
func TestPagerSetupErrorKeepsSignal(t *testing.T) {
	takesSignal(t, syscall.SIGTERM)
	_, out := onPty(t, 24, 80)
	old := makeRaw
	t.Cleanup(func() { makeRaw = old })
	makeRaw = func(int) (*term.State, error) {
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		time.Sleep(100 * time.Millisecond)
		return nil, errors.New("no raw mode")
	}
	err := (&env{}).pager(out, reviewLines(), 24, 80)
	if e, ok := err.(endedBySignal); !ok || e.sig != syscall.SIGTERM {
		t.Errorf("the pager returned %v; want it ended by SIGTERM", err)
	}
}

// TestPagerResizeBurstKeepsSignal: resizes that pile up while the watcher cannot run do not
// displace a termination signal that comes after them.
func TestPagerResizeBurstKeepsSignal(t *testing.T) {
	takesSignal(t, syscall.SIGTERM)
	ctl, out := onPty(t, 24, 80)
	s := watchScreen(ctl)
	old := pageKeys
	t.Cleanup(func() { pageKeys = old })
	pageKeys = func(k keySource) keySource {
		lk := k.(*liveKeys)
		lk.mu.Lock() // the watcher stops at its first signal
		go func() {
			for range 40 {
				_ = syscall.Kill(os.Getpid(), syscall.SIGWINCH)
			}
			time.Sleep(50 * time.Millisecond)
			_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
			time.Sleep(100 * time.Millisecond) // the runtime hands them to their channels
			lk.mu.Unlock()
		}()
		return k
	}
	errc := make(chan error, 1)
	go func() { errc <- (&env{}).pager(out, reviewLines(), 24, 80) }()
	select {
	case err := <-errc:
		if e, ok := err.(endedBySignal); !ok || e.sig != syscall.SIGTERM {
			t.Errorf("the pager returned %v; want it ended by SIGTERM", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the pager did not end on SIGTERM after a burst of resizes")
	}
	checkLeft(t, ctl, s, "after a burst of resizes")
}

// TestPagerResize: a SIGWINCH after the terminal changed size shows the page again, to the new
// size (#546 part 2): 10 rows by 40 columns have 9 body rows, so the key line says lines 1-9.
func TestPagerResize(t *testing.T) {
	ctl, out := onPty(t, 24, 80)
	s := watchScreen(ctl)
	errc := make(chan error, 1)
	go func() { errc <- (&env{}).pager(out, reviewLines(), 24, 80) }()
	if s.count("lines 1-23 of 30", 1) == 0 {
		t.Fatalf("no first page at 24 rows: %q", s.text())
	}
	ws := struct{ rows, cols, x, y uint16 }{10, 40, 0, 0}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ctl.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws))); errno != 0 {
		t.Fatal(errno)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	if s.count("lines 1-9 of 30", 1) == 0 {
		t.Fatalf("no page of 9 rows after the resize: %q", s.text())
	}
	ctl.WriteString("q")
	select {
	case err := <-errc:
		if err != nil {
			t.Errorf("the pager returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the pager did not quit")
	}
}

func init() { helperMain = pagerHelper }

// pagerHelper is the process TestPagerEndsBySignal starts: the pager on its standard input and
// output, then what main does with the result.
func pagerHelper() {
	err := (&env{}).pager(os.Stdout, reviewLines(), 24, 80)
	code := report(err, io.Discard, os.Stderr)
	endByCause()
	os.Exit(code)
}

// pagerProcess starts the helper on a new pseudo-terminal of 24 by 80 and waits for its first page.
// The process is killed and reaped when the test ends, whatever way it ends.
func pagerProcess(t *testing.T, ignore ...syscall.Signal) (ctl *os.File, s *screen, cmd *exec.Cmd, exited <-chan struct{}) {
	t.Helper()
	ctl, path := pty(t)
	ws := struct{ rows, cols, x, y uint16 }{24, 80, 0, 0}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ctl.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws))); errno != 0 {
		t.Fatal(errno)
	}
	tty, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer tty.Close()
	s = watchScreen(ctl)
	cmd = exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "BW_PAGER_HELPER=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, nil
	// An ignored signal is inherited through exec; the test process takes its own disposition back
	// once the child has started.
	for _, sig := range ignore {
		signal.Ignore(sig)
	}
	err = cmd.Start()
	for _, sig := range ignore {
		signal.Reset(sig)
	}
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		cmd.Process.Kill()
		<-done
	})
	if s.count("space or f forward", 1) == 0 {
		t.Fatalf("the process showed no key line: %q", s.text())
	}
	return ctl, s, cmd, done
}

// TestPagerEndsBySignal checks the process, not the function: a real signal sent to a process that
// shows the pager ends it by that signal, as its parent sees it, with the terminal restored.
func TestPagerEndsBySignal(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			takesSignal(t, sig)
			ctl, s, cmd, exited := pagerProcess(t)
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			select {
			case <-exited:
			case <-time.After(10 * time.Second):
				t.Fatal("the process did not end on the signal")
			}
			st, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
			if !ok || !st.Signaled() || st.Signal() != sig {
				t.Errorf("the process ended with %v; want it ended by %v", cmd.ProcessState, sig)
			}
			checkLeft(t, ctl, s, sig.String())
		})
	}
}

// TestPagerKeepsIgnoredSignal: a signal the process inherited as ignored (nohup) stays ignored, so
// the pager does not take it and the process is not left unable to end by it: SIGHUP changes
// nothing, and the quit key still ends the pager the ordinary way.
func TestPagerKeepsIgnoredSignal(t *testing.T) {
	ctl, s, cmd, exited := pagerProcess(t, syscall.SIGHUP)
	if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
		t.Fatalf("the process ended on an ignored SIGHUP: %v", cmd.ProcessState)
	case <-time.After(500 * time.Millisecond):
	}
	ctl.WriteString("q")
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		t.Fatal("the process did not quit on the key")
	}
	if !cmd.ProcessState.Success() {
		t.Errorf("the process ended with %v; want a normal exit", cmd.ProcessState)
	}
	checkLeft(t, ctl, s, "ignored SIGHUP")
}

// TestImportReviewTypedAhead runs import review on a pseudo-terminal with the quit key, a mark and
// two Enter keys written in one piece, as a fast typist or a paste does (#546 part 5). The pager
// reads the terminal raw, so the Enters wait in the queue as carriage returns; the marks prompt
// takes them as line ends, the mark is sent once and without a carriage return, and the command
// does not wait for another key.
func TestImportReviewTypedAhead(t *testing.T) {
	f, _ := importSteps(t)
	f.set("held", 2, -time.Minute, "running", "", "", ev("paused", `{"type":"paused","generation":1}`), ev("marked", `{"type":"marked","generation":2,"paths":1}`))
	ctl, term := onPty(t, 24, 80)
	s := watchScreen(ctl)
	go func() {
		if s.count("space or f forward", 1) > 0 {
			ctl.WriteString("qdoc[0]/BWSYNTH\r\r")
		}
	}()
	var e bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- run([]string{"import", "review", "ing_BWSYNTH"}, term, &e) }()
	select {
	case got := <-done:
		if got != 3 {
			t.Errorf("exit %d, stderr %q; want 3 and the lapsed lease", got, e.String())
		}
	case <-time.After(5 * time.Second):
		ctl.WriteString("\n\n") // a prompt that still waits is ended here: the line, then an empty one
		<-done
		t.Error("the marks prompt waited for another key after the typed-ahead Enters")
	}
	if took := f.took(); strings.Join(took, "\n") != `POST /ingestions/ing_BWSYNTH/marks if-match= body={"marks":["doc[0]/BWSYNTH"]}` {
		t.Errorf("sent %q; want the mark once, without a carriage return", took)
	}
}
