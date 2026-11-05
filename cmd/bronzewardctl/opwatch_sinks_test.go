package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestWatchOutAfterFinish: once the stop line is printed, a line the watch still shows prints
// nothing, so nothing follows the stop line.
func TestWatchOutAfterFinish(t *testing.T) {
	var b strings.Builder
	out := &watchOut{main: &sink{w: &b}}
	out.show("one")
	out.finish("stopped", "follow")
	out.show("late")
	if got, want := b.String(), "one\nstopped\nfollow\n"; got != want {
		t.Errorf("printed %q, want %q", got, want)
	}
}

// TestWatchOutGate: what the watch's client writes (notices, -v output) goes through the same stop
// barrier, so it prints nothing after the stop line; a nil sink gives a nil writer.
func TestWatchOutGate(t *testing.T) {
	var b, side strings.Builder
	out := &watchOut{main: &sink{w: &b}}
	g := out.gate(&sink{w: &side})
	fmt.Fprint(g, "notice before\n")
	out.finish("stopped")
	fmt.Fprint(g, "notice after\n")
	if got, want := side.String(), "notice before\n"; got != want {
		t.Errorf("client wrote %q, want %q", got, want)
	}
	if out.gate(nil) != nil {
		t.Error("gate(nil) is not nil")
	}
}

// within runs f and fails the test if it does not return in 5 s. A call that waits so long is stuck
// behind a lock that another goroutine holds across a blocked write.
func within(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return within 5 s", what)
	}
}

// TestWatchOutSinks: a write blocked on one sink holds only that sink.
//   - The stop lines go to the main sink while a gated write waits on another (standard error blocked,
//     the lines on standard output): finish does not wait for it, and the watch's failure is readable.
//   - A gated write on the main sink that starts after finish prints nothing, so no line follows the
//     stop lines on their own stream.
//   - stop sends its lines to the alternative sink when the main one failed, and waits on that one only.
func TestWatchOutSinks(t *testing.T) {
	var stdout, stderr strings.Builder
	blocked := newBlockedWriter()
	defer close(blocked.release)
	errSink := &sink{w: blocked}
	out := &watchOut{main: &sink{w: &stdout}}
	notice := out.gate(errSink)
	go fmt.Fprint(notice, "notice\n") // waits inside the blocked standard error
	select {
	case <-blocked.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the gated write did not reach the blocked writer within 5 s")
	}
	within(t, "failure()", func() {
		if err := out.failure(); err != nil {
			t.Errorf("failure() = %v before any failed write", err)
		}
	})
	within(t, "show on the main sink", func() { out.show("line") })
	within(t, "finish on the main sink", func() { out.finish("stopped", "follow") })
	// Started after finish, on the main sink itself: nothing is printed.
	same := out.gate(out.main)
	within(t, "a gated write on the main sink after finish", func() { fmt.Fprint(same, "late\n") })
	if got, want := stdout.String(), "line\nstopped\nfollow\n"; got != want {
		t.Errorf("standard output %q, want %q", got, want)
	}

	// The main sink itself is blocked (-o json: standard error carries the lines and the notices): the
	// check of the watch's failure, which Until makes, still returns.
	stuckW := newBlockedWriter()
	defer close(stuckW.release)
	stuck := &watchOut{main: &sink{w: stuckW}}
	go stuck.show("line") // waits inside the blocked writer, holding the main sink
	select {
	case <-stuckW.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the line did not reach the blocked writer within 5 s")
	}
	within(t, "failure() while the main sink is blocked", func() { _ = stuck.failure() })

	// The main sink failed: stop sends its lines to the alternative sink, which is free here.
	failing := &watchOut{main: &sink{w: failsWith{errors.New("write refused")}}}
	failing.show("lost")
	if failing.failure() == nil {
		t.Fatal("a refused line left no failure")
	}
	within(t, "stop with a failed main sink", func() { failing.stop(&sink{w: &stderr}, "stopped") })
	if got, want := stderr.String(), "stopped\n"; got != want {
		t.Errorf("alternative sink %q, want %q", got, want)
	}
}
