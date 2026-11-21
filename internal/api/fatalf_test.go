package api

import (
	"fmt"
	"sync"
	"testing"
)

// recorder is a fataler that notes what a helper reported instead of ending a test.
type recorder struct {
	mu            sync.Mutex
	fatal, errors []string
}

func (r *recorder) Helper() {}

func (r *recorder) Fatalf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fatal = append(r.fatal, fmt.Sprintf(format, args...))
}

func (r *recorder) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

// A test's goroutine, and the goroutine of a subtest, may end the test; a goroutine the test starts may not.
func TestOnTestGoroutine(t *testing.T) {
	if !onTestGoroutine() {
		t.Error("the test's own goroutine was taken for another")
	}
	t.Run("subtest", func(t *testing.T) {
		if !onTestGoroutine() {
			t.Error("a subtest's goroutine was taken for another")
		}
	})
	started := make(chan bool)
	go func() { started <- onTestGoroutine() }()
	if <-started {
		t.Error("a goroutine the test started was taken for the test's own")
	}
}

// runtime.Stack cuts the bottom of a stack first, and testing.tRunner is the bottom frame: a call nested
// deeper than the first buffer must still find it.
func TestOnTestGoroutineDeepStack(t *testing.T) {
	var nest func(n int) bool
	nest = func(n int) bool {
		if n == 0 {
			return onTestGoroutine()
		}
		return nest(n - 1)
	}
	if !nest(500) {
		t.Error("a deep call on the test's own goroutine was taken for another")
	}
	started := make(chan bool)
	go func() { started <- nest(500) }()
	if <-started {
		t.Error("a deep call on a goroutine the test started was taken for the test's own")
	}
}

// On the test's goroutine fatalf ends the test with Fatalf; anywhere else it reports once with Errorf and
// returns, so the goroutine that called it goes on and the test is not marked finished from the wrong one.
func TestFatalf(t *testing.T) {
	on := &recorder{}
	fatalf(on, "seed %s: %v", "k", "boom")
	if len(on.fatal) != 1 || on.fatal[0] != "seed k: boom" || len(on.errors) != 0 {
		t.Errorf("on the test's goroutine: Fatalf %q, Errorf %q; want one Fatalf and no Errorf", on.fatal, on.errors)
	}
	off := &recorder{}
	reached := make(chan bool)
	go func() {
		fatalf(off, "seed %s: %v", "k", "boom")
		reached <- true // the goroutine was not ended
	}()
	if !<-reached {
		t.Error("the goroutine did not go on after fatalf")
	}
	if len(off.errors) != 1 || off.errors[0] != "seed k: boom" || len(off.fatal) != 0 {
		t.Errorf("off the test's goroutine: Fatalf %q, Errorf %q; want one Errorf and no Fatalf", off.fatal, off.errors)
	}
}
