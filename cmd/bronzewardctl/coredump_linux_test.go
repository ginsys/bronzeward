package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// TestHarden starts this test binary again under GOTRACEBACK=all: after harden, the process is
// not dumpable, has no core size, and a panic in another goroutine prints no stack.
func TestHarden(t *testing.T) {
	if os.Getenv("BW_TEST_HARDEN") != "" {
		if err := harden(); err != nil {
			t.Fatal(err)
		}
		var lim syscall.Rlimit
		err := syscall.Getrlimit(syscall.RLIMIT_CORE, &lim)
		dumpable, _, _ := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_GET_DUMPABLE, 0, 0)
		fmt.Fprintf(os.Stderr, "core=%d/%d err=%v dumpable=%d\n", lim.Cur, lim.Max, err, dumpable)
		go func() { panic("BWSYNTH-panic") }()
		select {}
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHarden$")
	cmd.Env = append(os.Environ(), "BW_TEST_HARDEN=1", "GOTRACEBACK=all")
	out, _ := cmd.CombinedOutput()
	s := string(out)
	if !strings.Contains(s, "core=0/0 err=<nil> dumpable=0") || !strings.Contains(s, "BWSYNTH-panic") ||
		strings.Contains(s, "coredump_linux_test.go") || strings.Contains(s, "goroutine ") {
		t.Errorf("hardened process printed:\n%s", s)
	}
}
