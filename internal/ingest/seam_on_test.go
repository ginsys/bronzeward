//go:build fixtureinterrupt

package ingest

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/seam"
)

// The generation seam closes pipeline step 6: Commit stalls there with every generation created
// and the sanitized form not yet constructed.
func TestGenerationSeamFollowsEveryCreate(t *testing.T) {
	stallCommit(t, "generation", 4)
}

// The generation-first seam falls inside step 6: Commit stalls there with its first generation
// created and the rest not, the state a kill leaves when only part of step 6 ran.
func TestGenerationFirstSeamFollowsTheFirstCreate(t *testing.T) {
	stallCommit(t, "generation-first", 1)
}

// stallCommit commits a candidate of four values with the control file naming step, and checks
// Commit stalls there after want creates and returns once the file is removed.
func stallCommit(t *testing.T, step string, want int32) {
	t.Helper()
	control := filepath.Join(t.TempDir(), "interrupt")
	if err := os.WriteFile(control, []byte(step+" stall\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := seam.Control
	seam.Control = control
	t.Cleanup(func() { seam.Control = old })

	c, err := Extract(request(t, multiDoc, "doc[0]/machine/nodeLabels/a~1b~0c.d"))
	if err != nil {
		t.Fatal(err)
	}
	var creates atomic.Int32
	done := make(chan error, 1)
	go func() {
		_, err := c.Commit(context.Background(), func(context.Context, string, provider.Value) error {
			creates.Add(1)
			return nil
		})
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("Commit returned (%v) without stalling at the %s seam", err, step)
	case <-time.After(500 * time.Millisecond):
	}
	if n := creates.Load(); n != want {
		t.Errorf("stalled at %s after %d of 4 creates, want %d", step, n, want)
	}

	if err := os.Remove(control); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Commit did not return once released")
	}
}
