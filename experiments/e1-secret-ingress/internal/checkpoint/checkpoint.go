// Package checkpoint is Phase-0 evidence code for the secret-ingress feasibility experiment
// (ginsys/bronzeward issue 2). It is not the v1 implementation.
//
// It names the boundaries design §7.1 lists as persistence surfaces, in the order ingestion
// crosses them, and lets a run be stopped at any one of them. Two stop modes exist because they
// answer different questions:
//
//   - Hold pauses the process at a boundary so an external observer can capture the disk while
//     ingestion is provably mid-flight. It is how "nothing is on disk yet" becomes measurable.
//   - Crash sends SIGKILL to the process itself, so no deferred cleanup, no buffered write and no
//     rollback handler runs. A design that only avoids leaving plaintext behind because its
//     cleanup path ran has not met §7.1; this is what tells the two apart.
//
// This package requires a Unix platform: Crash has no meaningful equivalent elsewhere, and the
// experiment's fixtures are Linux containers regardless.
package checkpoint

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Point is a boundary in the ingestion flow. The values are ordered: a lower Point is crossed
// before a higher one, and the ordering journal's verification relies on that.
type Point int

// The boundaries, in crossing order. Each corresponds to a row of the experiment's boundary map.
const (
	// None is the zero value: no boundary. A Control with None set stops nowhere.
	None Point = iota
	// AfterRead is immediately after the source bytes are in memory and before anything has been
	// parsed or written. A capture here must find nothing of the input on disk.
	AfterRead
	// AfterParse is once the document is parsed. Any temporary file a parser creates exists by now.
	AfterParse
	// AfterDetect is once secret-bearing locations are identified but nothing has moved.
	AfterDetect
	// AfterExtract is once every marked secret is in the provider and references have replaced it.
	// This is the boundary §7.1 requires every persistence to sit after.
	AfterExtract
	// AfterFirstLog is once the first application log line for this run has been written, since
	// §7.1 names request logs and error reports as persistence surfaces in their own right.
	AfterFirstLog
	// InReview is while the change sits in staging awaiting review, the window the two staging
	// alternatives are compared over.
	InReview
	// InDBTxn is inside the transaction that writes the sanitized draft, before COMMIT. A crash
	// here leaves an aborted transaction whose bytes may still be in the heap and the WAL.
	InDBTxn
	// AfterCommit is once that transaction has committed.
	AfterCommit
	// AfterBaseline is once the encrypted baseline has been written.
	AfterBaseline
)

// names is the external spelling of each Point, which is what --crash-at and --hold-at accept and
// what the journal records. Keeping one table for both directions means a new Point cannot be
// added to one and forgotten in the other.
var names = map[Point]string{
	None:          "none",
	AfterRead:     "after-read",
	AfterParse:    "after-parse",
	AfterDetect:   "after-detect",
	AfterExtract:  "after-extract",
	AfterFirstLog: "after-first-log",
	InReview:      "in-review",
	InDBTxn:       "in-db-txn",
	AfterCommit:   "after-commit",
	AfterBaseline: "after-baseline",
}

// String implements fmt.Stringer. An unknown Point renders with its number rather than falling
// back to an empty string, which would make a journal record silently unreadable.
func (p Point) String() string {
	if n, ok := names[p]; ok {
		return n
	}
	return fmt.Sprintf("checkpoint(%d)", int(p))
}

// Parse maps the external spelling back to a Point. It rejects anything unknown rather than
// defaulting to None, because a typo in --crash-at that silently meant "never crash" would turn a
// control run into an honest-path run and the bundle would be captioned wrongly.
func Parse(s string) (Point, error) {
	for p, n := range names {
		if n == s {
			return p, nil
		}
	}
	return None, fmt.Errorf("unknown checkpoint %q; known: %s", s, strings.Join(Names(), ", "))
}

// Names lists every spelling in crossing order, for flag help and error messages.
func Names() []string {
	points := make([]Point, 0, len(names))
	for p := range names {
		points = append(points, p)
	}
	sort.Slice(points, func(i, j int) bool { return points[i] < points[j] })
	out := make([]string, 0, len(points))
	for _, p := range points {
		out = append(out, names[p])
	}
	return out
}

// Control decides what happens at each boundary. The zero value crosses every boundary without
// stopping, which is the honest path.
type Control struct {
	// CrashAt is the boundary at which the process kills itself, or None.
	CrashAt Point
	// HoldAt is the boundary at which the process pauses, or None.
	HoldAt Point
	// HoldFor is how long to pause. Zero means the default hold.
	HoldFor time.Duration
	// Observe, if set, is called as each boundary is reached and before any stop. It is how the
	// ordering journal records the crossing, including the crossing that is about to be fatal.
	Observe func(Point)
	// Announce, if set, receives a human-readable line for each stop. The harness reads it to know
	// a hold has begun, so that a capture starts while the process is provably inside the window
	// rather than after a guessed sleep.
	Announce func(string)
}

// defaultHold is long enough for the harness to notice the announcement and run bin/evidence, and
// short enough that a forgotten hold does not wedge the matrix.
const defaultHold = 30 * time.Second

// Reach records that the flow has arrived at p, then stops if this is the configured boundary.
//
// The Observe call happens before the stop, and for a crash the journal is fsynced by its own
// Append, so the record of reaching a fatal boundary survives the kill. That record is what makes
// a crashed run's evidence interpretable: without it, a bundle with nothing on disk is
// indistinguishable from a run that never started.
func (c *Control) Reach(p Point) {
	if c.Observe != nil {
		c.Observe(p)
	}
	switch {
	case c.CrashAt != None && c.CrashAt == p:
		c.announce(fmt.Sprintf("checkpoint %s reached: killing this process with SIGKILL", p))
		crash()
	case c.HoldAt != None && c.HoldAt == p:
		d := c.HoldFor
		if d <= 0 {
			d = defaultHold
		}
		c.announce(fmt.Sprintf("checkpoint %s reached: holding for %s", p, d))
		time.Sleep(d)
		c.announce(fmt.Sprintf("checkpoint %s hold over", p))
	}
}

// announce writes to the configured sink, falling back to stderr. Stopping without saying so would
// leave the harness waiting on a process that is already gone.
func (c *Control) announce(msg string) {
	if c.Announce != nil {
		c.Announce(msg)
		return
	}
	fmt.Fprintf(os.Stderr, "e1: %s\n", msg)
}

// crash is a real SIGKILL to this process, not os.Exit. The two are alike from inside the process
// and different from outside it: a signalled exit is what a genuine kill looks like to the
// harness, so a control run cannot be mistaken for a clean one in the evidence.
//
// It is a variable so the package's own tests can exercise Reach without dying.
var crash = func() {
	// A signal to self is delivered before Kill returns, so the lines after it never run. They
	// exist for the case where the process is unkillable (PID 1 in a container without an init),
	// where exiting anyway is better than continuing past a boundary that was meant to be fatal.
	if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
		fmt.Fprintf(os.Stderr, "e1: SIGKILL to self failed (%v); exiting instead\n", err)
	}
	os.Exit(137)
}
