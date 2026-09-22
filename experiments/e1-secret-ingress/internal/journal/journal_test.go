package journal

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/secret"
)

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// open returns a journal in a temporary directory, closed on cleanup.
func open(t *testing.T) (*Journal, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	j, err := Open(path, "run-test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { j.Close() })
	return j, path
}

// TestAppendNumbersAndPersists checks the three properties a crashed run depends on: the sequence
// starts at 1 and increments, the run id is stamped on every record, and the bytes are on disk by
// the time Append returns rather than sitting in a buffer a SIGKILL would discard.
func TestAppendNumbersAndPersists(t *testing.T) {
	j, path := open(t)

	for i, event := range []string{EventCheckpoint, EventExtracted, EventWrite} {
		got, err := j.Append(Record{Event: event})
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		if want := i + 1; got.Seq != want {
			t.Errorf("record %d got sequence %d, want %d", i, got.Seq, want)
		}
		if got.RunID != "run-test" {
			t.Errorf("record %d carries run id %q", i, got.RunID)
		}
		if got.Wall.IsZero() {
			t.Errorf("record %d has no wall clock", i)
		}

		// Read the file without closing the journal: this is what the harness does while the
		// process is held at a checkpoint, and what a bundle captures after a kill.
		loaded, err := Load(path)
		if err != nil {
			t.Fatalf("Load after %d appends: %v", i+1, err)
		}
		if len(loaded) != i+1 {
			t.Fatalf("after %d appends the file holds %d records; Append did not flush", i+1, len(loaded))
		}
	}
}

// TestLoadRoundTripsEveryField guards against a field that is written but not read back, which
// would make a crashed run's journal quietly less informative than a live one's.
func TestLoadRoundTripsEveryField(t *testing.T) {
	j, path := open(t)

	in := Record{
		Event:         EventWrite,
		Checkpoint:    "in-db-txn",
		Surface:       "machine_draft.document",
		PayloadSHA256: digestOf("sanitized document"),
		Digests:       []string{digestOf("a"), digestOf("b")},
		Detail:        "bw:redacted:sha256:0123456789ab",
	}
	if _, err := j.Append(in); err != nil {
		t.Fatalf("Append: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("Load returned %d records, want 1", len(loaded))
	}
	got := loaded[0]

	for _, c := range []struct{ name, got, want string }{
		{"Event", got.Event, in.Event},
		{"Checkpoint", got.Checkpoint, in.Checkpoint},
		{"Surface", got.Surface, in.Surface},
		{"PayloadSHA256", got.PayloadSHA256, in.PayloadSHA256},
		{"Detail", got.Detail, in.Detail},
		{"RunID", got.RunID, "run-test"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if strings.Join(got.Digests, ",") != strings.Join(in.Digests, ",") {
		t.Errorf("Digests = %v, want %v", got.Digests, in.Digests)
	}
	if got.Seq != 1 {
		t.Errorf("Seq = %d, want 1", got.Seq)
	}
}

// TestJournalHoldsNoPlaintext is the journal's own version of the rule it exists to police. A
// record built the way the prototype builds one — from a secret.Unresolved's digest and its
// redacted rendering — must leave no trace of the value in the file.
func TestJournalHoldsNoPlaintext(t *testing.T) {
	const plaintext = "E1-JOURNAL-PLAINTEXT-MUST-NOT-APPEAR"
	u := secret.NewUnresolved([]byte(plaintext))

	j, path := open(t)
	if _, err := j.Append(Record{
		Event:   EventExtracted,
		Surface: "provider",
		Digests: []string{u.Digest()},
		Detail:  "extracted " + u.String(),
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the journal: %v", err)
	}
	if strings.Contains(string(body), plaintext) {
		t.Errorf("the journal holds the plaintext:\n%s", body)
	}
	// And the control: the digest must be there, or the record is useless for verification and
	// this test would pass on an empty file.
	if !strings.Contains(string(body), u.Digest()) {
		t.Errorf("the journal does not hold the digest, so it cannot order anything:\n%s", body)
	}
}

// honest builds the record sequence an honest run produces: extraction, then the writes.
func honest() []Record {
	a, b := digestOf("talos-ca-key"), digestOf("bootstrap-token")
	return []Record{
		{Seq: 1, Event: EventCheckpoint, Checkpoint: "after-read", MonoNanos: 10},
		{Seq: 2, Event: EventExtracted, Digests: []string{a}, MonoNanos: 20},
		{Seq: 3, Event: EventExtracted, Digests: []string{b}, MonoNanos: 30},
		{Seq: 4, Event: EventWrite, Surface: "machine_draft.document", Digests: []string{a, b}, MonoNanos: 40},
		{Seq: 5, Event: EventWrite, Surface: "parsed_index.value", Digests: []string{a, b}, MonoNanos: 50},
	}
}

// TestVerifyAcceptsAnHonestRun checks the assertion does not fire on the design under test.
func TestVerifyAcceptsAnHonestRun(t *testing.T) {
	if v := Verify(honest()); v != nil {
		t.Errorf("an honest run reported violations: %v", v)
	}
}

// TestVerifyCatchesPersistFirst is the positive control for Verify. It is the exact shape the
// --persist-first flag produces, and if Verify cannot see it then a clean result on an honest run
// means nothing.
func TestVerifyCatchesPersistFirst(t *testing.T) {
	a := digestOf("talos-ca-key")
	records := []Record{
		{Seq: 1, Event: EventCheckpoint, Checkpoint: "after-parse", MonoNanos: 10},
		{Seq: 2, Event: EventWrite, Surface: "machine_draft.document", Digests: []string{a}, MonoNanos: 20},
		{Seq: 3, Event: EventExtracted, Digests: []string{a}, MonoNanos: 30},
	}

	violations := Verify(records)
	if len(violations) == 0 {
		t.Fatal("Verify accepted a run that persisted before extracting")
	}
	if violations[0].Seq != 2 {
		t.Errorf("the violation points at sequence %d, want 2", violations[0].Seq)
	}
	// The message goes into the report verbatim, so it must name both the surface and the secret.
	if r := violations[0].Reason; !strings.Contains(r, "machine_draft.document") || !strings.Contains(r, a[:12]) {
		t.Errorf("the violation does not identify what was written or which secret: %q", r)
	}
}

// TestVerifyCatchesAWriteThatNamesNoSecret is the case the two tests above could not express, and
// the reason all three --persist-first controls passed on the real fixture while these passed here.
//
// Both write records above carry a Digests list, because the test author wrote down what the
// forbidden design ought to record. The prototype's own control does not: it writes the whole
// observed document before anything has been extracted, so there is no secret to name yet and the
// list is empty. A rule quantified over that list is then vacuously satisfied, and the verifier
// reported "ok" for exactly the design the experiment exists to detect.
//
// The fix asserts the phase order itself. This test holds it: a write that names nothing, before
// any extraction, is a violation.
func TestVerifyCatchesAWriteThatNamesNoSecret(t *testing.T) {
	a := digestOf("talos-ca-key")
	records := []Record{
		{Seq: 1, Event: EventCheckpoint, Checkpoint: "after-parse", MonoNanos: 10},
		{Seq: 2, Event: EventWrite, Surface: "machine_draft (plaintext, before extraction)", MonoNanos: 20},
		{Seq: 3, Event: EventExtracted, Digests: []string{a}, MonoNanos: 30},
	}

	violations := Verify(records)
	if len(violations) != 1 {
		t.Fatalf("Verify reported %d violations, want exactly 1: %v", len(violations), violations)
	}
	if violations[0].Seq != 2 {
		t.Errorf("the violation points at sequence %d, want 2", violations[0].Seq)
	}
	if r := violations[0].Reason; !strings.Contains(r, "before the first extraction") {
		t.Errorf("the violation does not say the write came before extraction: %q", r)
	}
}

// TestVerifyAllowsADeclaredRecovery covers the one write with no extraction in its own journal that
// is not a violation: a second principal resuming an interrupted run out of encrypted staging. The
// extraction happened under the crashed run's identity and is recorded there, which the write says.
// Without the carve-out the recovery comparison the two staging alternatives exist for would report
// a §7.1 violation for behaving correctly.
func TestVerifyAllowsADeclaredRecovery(t *testing.T) {
	records := []Record{
		{Seq: 1, Event: EventNote, Detail: "resumed run crashed-in-review-encrypted from encrypted staging", MonoNanos: 10},
		{Seq: 2, Event: EventWrite, Surface: "machine_draft.document", Detail: "recover:crashed-in-review-encrypted", MonoNanos: 20},
	}

	if v := Verify(records); v != nil {
		t.Errorf("a declared recovery reported violations: %v", v)
	}
}

// TestVerifyRejectsAnUndeclaredWriteWithoutExtraction is that carve-out's own control. The marker
// on the write record is what distinguishes a recovery from a run that simply persisted and
// extracted nothing, so a journal without it must still fail.
func TestVerifyRejectsAnUndeclaredWriteWithoutExtraction(t *testing.T) {
	records := []Record{
		{Seq: 1, Event: EventWrite, Surface: "machine_draft.document", MonoNanos: 10},
	}

	violations := Verify(records)
	if len(violations) != 1 {
		t.Fatalf("Verify reported %d violations, want exactly 1: %v", len(violations), violations)
	}
	if r := violations[0].Reason; !strings.Contains(r, "extracted no secret at all") {
		t.Errorf("the violation does not say why it fired: %q", r)
	}
}

// TestVerifyCatchesRedactAfter covers the variant §7.1 singles out: the plaintext is written, then
// replaced by references. The later extraction does not repair the earlier write, and Verify must
// not be fooled by the run ending in a tidy state.
func TestVerifyCatchesRedactAfter(t *testing.T) {
	a := digestOf("talos-ca-key")
	records := []Record{
		{Seq: 1, Event: EventWrite, Surface: "machine_draft.document", Digests: []string{a}, MonoNanos: 10},
		{Seq: 2, Event: EventExtracted, Digests: []string{a}, MonoNanos: 20},
		{Seq: 3, Event: EventWrite, Surface: "machine_draft.document", Digests: []string{a}, MonoNanos: 30},
	}

	violations := Verify(records)
	if len(violations) != 1 {
		t.Fatalf("Verify reported %d violations, want exactly 1 (the first write): %v", len(violations), violations)
	}
	if violations[0].Seq != 1 {
		t.Errorf("the violation points at sequence %d, want 1", violations[0].Seq)
	}
}

// TestVerifyRejectsAnIncompleteJournal checks that a journal which cannot establish an order is
// reported as such rather than as clean. A lost record is the one failure mode that would
// otherwise turn missing evidence into a passing result.
func TestVerifyRejectsAnIncompleteJournal(t *testing.T) {
	gapped := honest()
	gapped = append(gapped[:2], gapped[3:]...) // drop sequence 3

	violations := Verify(gapped)
	if len(violations) == 0 {
		t.Fatal("Verify accepted a journal with a missing record")
	}
	if !strings.Contains(violations[0].Reason, "missing records") {
		t.Errorf("the violation does not explain the gap: %q", violations[0].Reason)
	}

	duplicated := append(honest(), Record{Seq: 5, Event: EventNote, MonoNanos: 60})
	if v := Verify(duplicated); len(v) == 0 {
		t.Error("Verify accepted a journal with a repeated sequence number")
	}
}

// TestVerifyRejectsABackwardsClock checks the other integrity property. Wall time can step under
// NTP; the monotonic reading cannot, so a backwards one means the records are not from one run.
func TestVerifyRejectsABackwardsClock(t *testing.T) {
	records := honest()
	records[3].MonoNanos = 5

	violations := Verify(records)
	if len(violations) == 0 {
		t.Fatal("Verify accepted a journal whose monotonic clock ran backwards")
	}
	if !strings.Contains(violations[0].Reason, "backwards") {
		t.Errorf("the violation does not explain the clock: %q", violations[0].Reason)
	}
}

// TestVerifySortsBySequence checks records are ordered by seq rather than by their position in the
// slice, since Load returns file order and a concurrent writer could interleave lines.
func TestVerifySortsBySequence(t *testing.T) {
	records := honest()
	records[0], records[4] = records[4], records[0]

	if v := Verify(records); v != nil {
		t.Errorf("an honest run in file-shuffled order reported violations: %v", v)
	}
}

// TestLoadRejectsAMalformedLine checks a corrupt journal fails loudly. Skipping the bad line would
// silently drop the record a crash run exists to capture.
func TestLoadRejectsAMalformedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	body := `{"seq":1,"event":"note"}` + "\n" + `{"seq":2,"event":` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load accepted a malformed journal")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("the error does not name the line: %v", err)
	}
}

// TestOpenRequiresARunID checks an unidentified journal is refused. A journal that cannot be
// matched to a bundle is not evidence of anything.
func TestOpenRequiresARunID(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "journal.jsonl"), ""); err == nil {
		t.Error("Open accepted an empty run id")
	}
}
