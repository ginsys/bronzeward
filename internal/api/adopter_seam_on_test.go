//go:build fixtureinterrupt

package api

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/seam"
)

// The adoption seam falls between the evidence observation and the adoption record: with the
// control file naming it the loop stalls there, the observation taken and nothing recorded, and
// records the adoption once the file is removed.
func TestAdoptionSeamFollowsTheEvidenceObservation(t *testing.T) {
	control := filepath.Join(t.TempDir(), "interrupt")
	if err := os.WriteFile(control, []byte("adoption stall\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := seam.Control
	seam.Control = control
	t.Cleanup(func() { seam.Control = old })

	// The seam says on stderr that it stalled. The test reads that line, so the stall is known to
	// have been entered: neither the observation's start entry nor its record is the seam, and
	// closing the observation's connection follows the record.
	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = pipeW
	t.Cleanup(func() {
		os.Stderr = stderr
		_ = pipeW.Close()
	})
	stalled := make(chan struct{})
	go func() {
		defer func() { _ = pipeR.Close() }()
		seen := false
		for sc := bufio.NewScanner(pipeR); sc.Scan(); {
			if !seen && strings.Contains(sc.Text(), "stall at adoption") {
				seen = true
				close(stalled)
			}
		}
	}()

	le := newLoopEnv(t, "")
	le.approveIt(t)
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{}, 16)
	a := le.adopter(t, life, config.Ingestion{Heartbeat: 20 * time.Millisecond, Lease: 50 * time.Millisecond}, idle)
	a.startAdopter()
	defer stopping(a, stop)
	// Registered after stopping, so it runs first: a failure while the seam stalls releases it, or
	// stopping would wait on a loop that ignores cancellation, and the stderr swap would never be
	// undone.
	defer func() { _ = os.Remove(control) }()

	select {
	case <-stalled:
	case <-time.After(10 * time.Second):
		t.Fatal("the loop did not stall at the adoption seam")
	}
	time.Sleep(300 * time.Millisecond) // well past a heartbeat: a loop not stalled would have adopted
	if n := count(t, le.db, `SELECT count(*) FROM adoption_record WHERE plan = $1`, le.pid); n != 0 {
		t.Fatalf("%d adoption records while the seam stalls", n)
	}
	if s := le.planState(t); s != "approved" {
		t.Fatalf("plan %s while the seam stalls", s)
	}

	if err := os.Remove(control); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for le.planState(t) != "committed" {
		if time.Now().After(deadline) {
			t.Fatal("the plan was not adopted once the seam was released")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
