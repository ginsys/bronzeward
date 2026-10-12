//go:build fixtureinterrupt

package api

import (
	"context"
	"os"
	"path/filepath"
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

	le := newLoopEnv(t, "")
	le.approveIt(t)
	life, stop := context.WithCancel(t.Context())
	idle := make(chan struct{}, 16)
	a := le.adopter(t, life, config.Ingestion{Heartbeat: 20 * time.Millisecond, Lease: 50 * time.Millisecond}, idle)
	a.startAdopter()
	defer stopping(a, stop)

	deadline := time.Now().Add(10 * time.Second)
	for {
		if s, _ := le.starts(t); s >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the evidence observation was not taken")
		}
		time.Sleep(10 * time.Millisecond)
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
	deadline = time.Now().Add(10 * time.Second)
	for le.planState(t) != "committed" {
		if time.Now().After(deadline) {
			t.Fatal("the plan was not adopted once the seam was released")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
