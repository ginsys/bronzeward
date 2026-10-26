package client

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestKeyringWritesUnbounded checks that a credential-store write slower than within's bound is
// waited for: one given up on would land after its caller released the renewal lock.
func TestKeyringWritesUnbounded(t *testing.T) {
	bound, set, del := storeBound, keyringSetCall, keyringDeleteCall
	t.Cleanup(func() { storeBound, keyringSetCall, keyringDeleteCall = bound, set, del })
	storeBound = 10 * time.Millisecond
	var wrote atomic.Int32
	slow := func() { time.Sleep(100 * time.Millisecond); wrote.Add(1) }
	keyringSetCall = func(string, string, string) error { slow(); return nil }
	keyringDeleteCall = func(string, string) error { slow(); return nil }
	if err := keyringSet("BWSYNTH-context", "{}"); err != nil || wrote.Load() != 1 {
		t.Errorf("keyringSet returned %v before its write finished (%d writes)", err, wrote.Load())
	}
	if err := keyringDelete("BWSYNTH-context"); err != nil || wrote.Load() != 2 {
		t.Errorf("keyringDelete returned %v before its deletion finished (%d writes)", err, wrote.Load())
	}
}
