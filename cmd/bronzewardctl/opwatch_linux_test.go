package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// blockedOn returns once a process is blocked on the flock of path, as /proc/locks lists it, within 10 s.
// A process blocked there has opened the client, so it is past its signal handler: no sleep decides it.
func blockedOn(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	ino := ":" + strconv.FormatUint(fi.Sys().(*syscall.Stat_t).Ino, 10) + " "
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		b, err := os.ReadFile("/proc/locks")
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range strings.Split(string(b), "\n") {
			if strings.Contains(l, "-> FLOCK") && strings.Contains(l, ino) {
				return
			}
		}
	}
	t.Fatalf("no process blocked on %s within 10 s", path)
}

// TestOperationWatchInterruptDuringOpen: the first Ctrl-C while opening the client waits for the renewal
// lock of the token file gets the grace, the stop line and the follow line last, and exit 130, as for any
// stalled read of the watch. No operation was read, so the stop line says so. The command runs in a
// process of its own, because without a handler the default action of SIGINT ends it. Bound: the process
// ends within 10 s of the signal (the grace is 3 s).
func TestOperationWatchInterruptDuringOpen(t *testing.T) {
	f := &opFake{body: fmt.Sprintf(opJSON, "apply-config", "running", "null", "null", "[]")}
	srv := httptest.NewServer(f)
	defer srv.Close()
	lock, _ := oidcRenewalContext(t, srv.URL)
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
	blockedOn(t, lock)
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
		t.Error("the first Ctrl-C during the opening of the client did not end the watch within the grace")
	}
}
