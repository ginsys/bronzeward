package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// fullPipe returns a pipe whose buffer is full: the read end, which nobody reads, and the write end in
// blocking mode, so that a write to it waits as a write to a stopped terminal does.
func fullPipe(t *testing.T) (r, w *os.File) {
	t.Helper()
	var p [2]int
	if err := syscall.Pipe2(p[:], syscall.O_NONBLOCK|syscall.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("x"), 4096)
	for {
		if _, err := syscall.Write(p[1], chunk); err != nil {
			if errors.Is(err, syscall.EAGAIN) {
				break
			}
			t.Fatal(err)
		}
	}
	if err := syscall.SetNonblock(p[1], false); err != nil {
		t.Fatal(err)
	}
	r, w = os.NewFile(uintptr(p[0]), "pipe-read"), os.NewFile(uintptr(p[1]), "pipe-write")
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	return r, w
}

// TestOperationWatchSecondCtrlCEndsBlockedWatch: the stop lines of the first Ctrl-C wait on a standard
// output that does not take them (operator-experience.md section 5.7). The second Ctrl-C ends the command
// at once, with the default action of the signal: the process dies by SIGINT. The command runs in a
// process of its own, because the default action would end the test binary. Bound: 2 s after the first
// signal.
func TestOperationWatchSecondCtrlCEndsBlockedWatch(t *testing.T) {
	f := &opFake{body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]"), hang: make(chan struct{})}
	srv := httptest.NewServer(f)
	defer srv.Close()
	defer close(f.hang)
	automationContext(t, srv.URL)
	_, w := fullPipe(t)
	var stderr bytes.Buffer
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "BW_WATCH_HELPER=1")
	cmd.Stdout, cmd.Stderr = w, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() { ended <- cmd.Wait() }()
	defer func() { _ = cmd.Process.Kill() }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		f.mu.Lock()
		n := f.reads
		f.mu.Unlock()
		// The first read of the operation comes after the signal handler is set. The watch then prints
		// its first line, which waits on the full pipe, so no stream request follows.
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the watch read no operation within 10 s")
		}
	}
	// Repeated Ctrl-C: the first one stops the handler of the watch, so a later one takes the default
	// action. A watch that kept its handler would swallow all of them and hang on the blocked output.
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	timeout := time.After(2 * time.Second) // under the 3 s grace: a watch that kept its handler swallows the second Ctrl-C until the grace ends
	for {
		_ = cmd.Process.Signal(syscall.SIGINT)
		select {
		case err := <-ended:
			var x *exec.ExitError
			if !errors.As(err, &x) {
				t.Fatalf("the process ended with %v, stderr %q; want death by SIGINT", err, stderr.String())
			}
			st, ok := x.Sys().(syscall.WaitStatus)
			if !ok || !st.Signaled() || st.Signal() != syscall.SIGINT {
				t.Errorf("the process ended with %v, stderr %q; want death by SIGINT", err, stderr.String())
			}
			return
		case <-timeout:
			t.Fatalf("the process did not end within 2 s of repeated Ctrl-C, stderr %q: the second Ctrl-C does not end a blocked watch at once", stderr.String())
		case <-tick.C:
		}
	}
}
