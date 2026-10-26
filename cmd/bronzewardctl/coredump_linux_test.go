package main

import (
	"syscall"
	"testing"
)

func TestNoCoreDumps(t *testing.T) {
	if err := noCoreDumps(); err != nil {
		t.Fatal(err)
	}
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_CORE, &lim); err != nil || lim.Cur != 0 || lim.Max != 0 {
		t.Errorf("RLIMIT_CORE %+v, %v; want 0", lim, err)
	}
	if v, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_GET_DUMPABLE, 0, 0); errno != 0 || v != 0 {
		t.Errorf("dumpable %d, %v; want 0", v, errno)
	}
}
