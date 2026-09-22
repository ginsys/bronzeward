package checkpoint

import (
	"strings"
	"testing"
	"time"
)

// TestPointsAreOrderedAndNamed pins the property the ordering journal depends on: the constants
// are in crossing order, and every one of them has an external spelling that round-trips.
func TestPointsAreOrderedAndNamed(t *testing.T) {
	order := []Point{
		None, AfterRead, AfterParse, AfterDetect, AfterExtract,
		AfterFirstLog, InReview, InDBTxn, AfterCommit, AfterBaseline,
	}

	if len(order) != len(names) {
		t.Fatalf("this test lists %d points but the package defines %d; a new boundary was added "+
			"without extending the ordering assertion", len(order), len(names))
	}

	for i := 1; i < len(order); i++ {
		if order[i-1] >= order[i] {
			t.Errorf("%s (%d) does not precede %s (%d)", order[i-1], int(order[i-1]), order[i], int(order[i]))
		}
	}

	// AfterExtract is the boundary §7.1 requires persistence to sit after, so its position
	// relative to every persistence boundary is the property under test, not an incidental one.
	for _, later := range []Point{AfterFirstLog, InReview, InDBTxn, AfterCommit, AfterBaseline} {
		if AfterExtract >= later {
			t.Errorf("%s is not ordered after extraction; §7.1's requirement cannot be checked", later)
		}
	}

	for _, p := range order {
		spelling := p.String()
		got, err := Parse(spelling)
		if err != nil {
			t.Errorf("Parse(%q): %v", spelling, err)
			continue
		}
		if got != p {
			t.Errorf("Parse(%q) = %s, want %s", spelling, got, p)
		}
	}
}

// TestNamesIsInCrossingOrder checks the flag help lists boundaries in the order they happen, since
// that listing is how a reader of the report learns the flow.
func TestNamesIsInCrossingOrder(t *testing.T) {
	got := Names()
	want := []string{
		"none", "after-read", "after-parse", "after-detect", "after-extract",
		"after-first-log", "in-review", "in-db-txn", "after-commit", "after-baseline",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Names() = %v, want %v", got, want)
	}
}

// TestParseRejectsUnknown is the one that matters operationally: a typo in --crash-at must fail
// the run, not quietly produce an honest-path run captioned as a control.
func TestParseRejectsUnknown(t *testing.T) {
	for _, s := range []string{"", "after_read", "AfterRead", "after-reads", "in-db-tx"} {
		if p, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) returned %s with no error", s, p)
		}
	}

	// The error must name the alternatives, or a typo at 2am costs a re-run of the matrix.
	_, err := Parse("after_read")
	if err == nil || !strings.Contains(err.Error(), "after-read") {
		t.Errorf("the error does not list the known spellings: %v", err)
	}
}

// TestUnknownPointStringIsReadable checks a Point outside the table still renders something a
// journal reader can act on, rather than an empty string.
func TestUnknownPointStringIsReadable(t *testing.T) {
	got := Point(99).String()
	if got == "" || !strings.Contains(got, "99") {
		t.Errorf("Point(99).String() = %q, want something naming 99", got)
	}
}

// TestReachObservesBeforeCrashing pins the ordering the evidence depends on: the journal record of
// arriving at a fatal boundary must be written before the process dies, or a crashed bundle cannot
// be told apart from a run that never reached that point.
func TestReachObservesBeforeCrashing(t *testing.T) {
	original := crash
	t.Cleanup(func() { crash = original })

	var events []string
	crash = func() { events = append(events, "crash") }

	c := &Control{
		CrashAt:  InDBTxn,
		Observe:  func(p Point) { events = append(events, "observe:"+p.String()) },
		Announce: func(string) {},
	}

	c.Reach(AfterExtract)
	c.Reach(InDBTxn)

	want := "observe:after-extract,observe:in-db-txn,crash"
	if got := strings.Join(events, ","); got != want {
		t.Errorf("events = %q, want %q", got, want)
	}
}

// TestReachIsInertWithoutAControl checks the honest path: the zero Control crosses every boundary
// and stops nowhere.
func TestReachIsInertWithoutAControl(t *testing.T) {
	original := crash
	t.Cleanup(func() { crash = original })
	crash = func() { t.Error("the zero Control crashed") }

	var seen []Point
	c := &Control{Observe: func(p Point) { seen = append(seen, p) }}

	start := time.Now()
	for _, p := range []Point{AfterRead, AfterParse, AfterExtract, InDBTxn, AfterCommit} {
		c.Reach(p)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("the zero Control paused for %s", elapsed)
	}
	if len(seen) != 5 {
		t.Errorf("observed %d boundaries, want 5", len(seen))
	}
}

// TestHoldPausesAtItsBoundaryOnly checks a hold fires once, at the named boundary, and that the
// announcement is emitted — the harness starts its capture on that line, so a silent hold would
// have the capture race the window it is meant to be inside.
func TestHoldPausesAtItsBoundaryOnly(t *testing.T) {
	original := crash
	t.Cleanup(func() { crash = original })
	crash = func() { t.Error("a hold-only Control crashed") }

	var announcements []string
	c := &Control{
		HoldAt:   InReview,
		HoldFor:  20 * time.Millisecond,
		Announce: func(s string) { announcements = append(announcements, s) },
	}

	beforeHold := time.Now()
	c.Reach(AfterExtract)
	if elapsed := time.Since(beforeHold); elapsed > 10*time.Millisecond {
		t.Errorf("a boundary that is not the hold point paused for %s", elapsed)
	}
	if len(announcements) != 0 {
		t.Errorf("a boundary that is not the hold point announced %v", announcements)
	}

	atHold := time.Now()
	c.Reach(InReview)
	if elapsed := time.Since(atHold); elapsed < 20*time.Millisecond {
		t.Errorf("the hold lasted %s, want at least 20ms", elapsed)
	}
	if len(announcements) != 2 {
		t.Fatalf("the hold announced %d lines, want 2 (start and end): %v", len(announcements), announcements)
	}
	if !strings.Contains(announcements[0], "in-review") {
		t.Errorf("the opening announcement does not name the boundary: %q", announcements[0])
	}
}
