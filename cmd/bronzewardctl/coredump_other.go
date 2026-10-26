//go:build !linux

package main

import "syscall"

// noCoreDumps sets RLIMIT_CORE to 0; only Linux has the not-dumpable flag.
func noCoreDumps() error {
	return syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{})
}
