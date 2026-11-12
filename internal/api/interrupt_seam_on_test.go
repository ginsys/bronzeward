//go:build fixtureinterrupt

package api

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/seam"
)

// interruptControl points seam.Control at a file holding line for the test's length.
func interruptControl(t *testing.T, line string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "interrupt")
	if err := os.WriteFile(f, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := seam.Control
	seam.Control = f
	t.Cleanup(func() { seam.Control = old })
	return f
}

// REC-04 (b): with the control file naming recovery-entry-commit lose-reply, the entry commits and
// answers its outcome unknown, the process still gated. Once the file is removed the retry under
// the same key replays the entry committed the first time, and the process is out of the gate.
func TestRecoveryEntryLoseReplySeam(t *testing.T) {
	e := newEnv(t, options{})
	var entered atomic.Int32
	r := recoveryStart(t, e, &entered)
	f := interruptControl(t, "recovery-entry-commit lose-reply")
	wantProblem(t, e.enter(r, "seam", entryBodyText), http.StatusInternalServerError, "internal-error")
	minted := epoch(t, e.db)
	if minted == r.d.start || entered.Load() != 0 || !r.d.preEntry.Load() {
		t.Fatalf("after the lost reply: epoch %s, %d callbacks, preEntry %t", minted, entered.Load(), r.d.preEntry.Load())
	}
	if err := os.Remove(f); err != nil {
		t.Fatal(err)
	}
	rec := e.enter(r, "seam", entryBodyText)
	if b := decode[entryBody](t, rec, http.StatusCreated); b.Epoch != minted || rec.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("the retry: %+v %v", b, rec.Header())
	}
	if entered.Load() != 1 || r.d.preEntry.Load() {
		t.Fatalf("after the retry: %d callbacks, preEntry %t", entered.Load(), r.d.preEntry.Load())
	}
}

// PROT-06 case 4: a continuation stalled at draft-commit has released its claim and written the
// draft, the operation and its terminal event, and commits nothing. Its heartbeat waits on the
// claim's lock, so the lease lapses and a read of the ingestion answers held, past leaseUntil. The
// operation's own read waits for the commit (ginsys/bronzeward#799), so it is read after the
// release: succeeded, with the claim released.
func TestDraftCommitStallSeam(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j, _ := ie.pausedForContinue(t)
	cj := ie.continueReview(t, "k-seam-0123456789", j.claim.ID, op, 2)
	f := interruptControl(t, "draft-commit stall")
	mustExec(t, ie.db, `UPDATE staging_claim SET lease_until = now() + interval '1500 milliseconds' WHERE id = $1`, j.claim.ID)
	d := ie.d
	d.timers.Heartbeat, d.timers.Lease = 200*time.Millisecond, 1500*time.Millisecond
	api := ie.buildWith(d, options{})
	var run sync.WaitGroup
	run.Go(func() { api.runIngest(t.Context(), cj) })
	t.Cleanup(func() { _ = os.Remove(f); run.Wait() })

	viewer := ie.human("h-viewer")
	type ingestion struct {
		State      string    `json:"state"`
		LeaseUntil time.Time `json:"leaseUntil"`
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		b := decode[ingestion](t, ie.do(ie.api, getIngestionCall(viewer, j.claim.ID)), http.StatusOK)
		if b.State == "held" && time.Now().After(b.LeaseUntil) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the ingestion never read held past its lease: %+v", b)
		}
	}
	if err := os.Remove(f); err != nil {
		t.Fatal(err)
	}
	run.Wait()
	if b := decode[ingestion](t, ie.do(ie.api, getIngestionCall(viewer, j.claim.ID)), http.StatusOK); b.State != "released" {
		t.Fatalf("after the release the ingestion reads %s", b.State)
	}
	if r := readOp(t, ie.db, op); r.state != "succeeded" || r.error != nil {
		t.Fatalf("operation %+v", r)
	}
}
