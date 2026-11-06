package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestReadSecondCtrlCEndsBlockedStopLine: the stop line of the first Ctrl-C waits on a standard error
// that does not take it. The second Ctrl-C ends the command at once, with the default action of the
// signal: the process dies by SIGINT, as operation watch does (#643). The command runs in a process of
// its own, because the default action would end the test binary. Bound: 2 s after the second signal.
func TestReadSecondCtrlCEndsBlockedStopLine(t *testing.T) {
	srv, arrived := stalledServer(t)
	automationContext(t, srv.URL)
	_, w := fullPipe(t)
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "BW_PIPE_HELPER=cluster list")
	cmd.Stderr = w
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() { ended <- cmd.Wait() }()
	defer func() { _ = cmd.Process.Kill() }()
	select {
	case <-arrived: // the handler is armed before the request is sent
	case err := <-ended:
		t.Fatalf("the process ended with %v before it sent the read", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the process sent no read within 10 s")
	}
	// The first Ctrl-C: the read takes it and ends. Its stop line then waits on the full pipe, so the
	// process stays alive.
	_ = cmd.Process.Signal(syscall.SIGINT)
	select {
	case err := <-ended:
		t.Fatalf("the process ended with %v on the first Ctrl-C; want it to wait on the blocked stop line", err)
	case <-time.After(500 * time.Millisecond):
	}
	// The second Ctrl-C finds no handler: the process dies by SIGINT.
	_ = cmd.Process.Signal(syscall.SIGINT)
	select {
	case err := <-ended:
		var x *exec.ExitError
		if !errors.As(err, &x) {
			t.Fatalf("the process ended with %v; want death by SIGINT", err)
		}
		if st, ok := x.Sys().(syscall.WaitStatus); !ok || !st.Signaled() || st.Signal() != syscall.SIGINT {
			t.Errorf("the process ended with %v; want death by SIGINT", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the process did not end within 2 s of the second Ctrl-C: the handler is still armed")
	}
}
