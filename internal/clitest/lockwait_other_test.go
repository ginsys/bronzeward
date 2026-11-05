//go:build !linux

package clitest

import "testing"

// waiting needs the Linux lock table, so a test that waits on a lock skips elsewhere.
func waiting(t *testing.T, _ string, _ int) {
	t.Helper()
	t.Skip("waiting for a flock needs the Linux lock table")
}
