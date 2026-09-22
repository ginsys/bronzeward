package extract

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/document"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/journal"
)

const sample = `version: v1alpha1
machine:
  type: controlplane
  token: machine-join-token
  ca:
    crt: public-certificate
    key: machine-ca-private-key
cluster:
  id: cluster-id-value
`

// fakeStore is an in-memory provider. It records what it was given so a test can assert the value
// actually reached the store, and it can be made to fail at a chosen call so the partial-failure
// path is exercised rather than assumed.
type fakeStore struct {
	put       map[string]string
	failAfter int // zero means never
	calls     int
}

func newFakeStore() *fakeStore { return &fakeStore{put: map[string]string{}} }

func (s *fakeStore) Name() string { return "fake" }

func (s *fakeStore) Put(_ context.Context, key string, value []byte) (string, error) {
	s.calls++
	if s.failAfter > 0 && s.calls > s.failAfter {
		return "", errors.New("provider unavailable")
	}
	s.put[key] = string(value)
	return "kv://fake/" + key, nil
}

func setup(t *testing.T) (*document.Document, *fakeStore, *journal.Journal) {
	t.Helper()
	d, err := document.Load([]byte(sample))
	if err != nil {
		t.Fatalf("document.Load: %v", err)
	}
	j, err := journal.Open(filepath.Join(t.TempDir(), "journal.jsonl"), "run-test")
	if err != nil {
		t.Fatalf("journal.Open: %v", err)
	}
	t.Cleanup(func() { j.Close() })
	return d, newFakeStore(), j
}

func request(d *document.Document, s Store, j *journal.Journal, paths ...string) Request {
	return Request{Document: d, Paths: paths, Store: s, Journal: j, RunID: "run-test"}
}

// TestRunExtractsSubstitutesAndRecords is the honest path end to end.
func TestRunExtractsSubstitutesAndRecords(t *testing.T) {
	d, store, j := setup(t)

	res, err := Run(t.Context(), request(d, store, j, "doc[0].machine.ca.key", "doc[0].machine.token"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The values reached the provider, under run-namespaced keys.
	if got := store.put["run-test/doc[0].machine.ca.key"]; got != "machine-ca-private-key" {
		t.Errorf("the provider holds %q for doc[0].machine.ca.key", got)
	}
	if got := store.put["run-test/doc[0].machine.token"]; got != "machine-join-token" {
		t.Errorf("the provider holds %q for doc[0].machine.token", got)
	}

	// The sanitized document holds references and neither secret.
	if !res.Sanitized.Valid() {
		t.Fatal("Run returned an invalid Sanitized")
	}
	body := string(res.Sanitized.Document())
	for _, gone := range []string{"machine-ca-private-key", "machine-join-token"} {
		if strings.Contains(body, gone) {
			t.Errorf("the sanitized document still holds %q:\n%s", gone, body)
		}
	}
	for _, present := range []string{
		"bw:ref:kv://fake/run-test/doc[0].machine.ca.key",
		"bw:ref:kv://fake/run-test/doc[0].machine.token",
	} {
		if !strings.Contains(body, present) {
			t.Errorf("the sanitized document does not hold %q:\n%s", present, body)
		}
	}

	// Values that were not marked must survive, including the public certificate sitting beside
	// the key that was extracted.
	for _, kept := range []string{"public-certificate", "cluster-id-value", "controlplane"} {
		if !strings.Contains(body, kept) {
			t.Errorf("the sanitized document lost the unmarked value %q:\n%s", kept, body)
		}
	}

	if len(res.Sanitized.References()) != 2 || len(res.Digests) != 2 {
		t.Errorf("got %d references and %d digests, want 2 of each",
			len(res.Sanitized.References()), len(res.Digests))
	}
}

// TestJournalRecordsExtractionBeforeAnyWrite is the ordering assertion itself, run against the
// real verifier rather than against a reading of the code: the caller writes after Run returns,
// and journal.Verify must accept the result.
func TestJournalRecordsExtractionBeforeAnyWrite(t *testing.T) {
	d, store, j := setup(t)

	res, err := Run(t.Context(), request(d, store, j, "doc[0].machine.ca.key", "doc[0].machine.token"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// What an ingesting caller does next: persist the sanitized draft, naming every secret that
	// was in the source document.
	if _, err := j.Append(journal.Record{
		Event:   journal.EventWrite,
		Surface: "machine_draft.document",
		Digests: res.Digests,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if v := journal.Verify(j.Records()); v != nil {
		t.Errorf("an honest extraction produced ordering violations: %v", v)
	}

	// And the control: the same records with the write moved to the front must be rejected, or
	// this test would pass on a verifier that accepts anything.
	records := j.Records()
	moved := append([]journal.Record{records[len(records)-1]}, records[:len(records)-1]...)
	for i := range moved {
		moved[i].Seq = i + 1
	}
	if v := journal.Verify(moved); v == nil {
		t.Error("the verifier accepted a write placed before the extractions")
	}
}

// TestRunFailsClosedOnAProviderError covers the partial-failure path. One secret is already in the
// provider and substituted in the document when the second write fails; Run must return nothing
// persistable rather than a document that is half sanitized.
func TestRunFailsClosedOnAProviderError(t *testing.T) {
	d, store, j := setup(t)
	store.failAfter = 1

	res, err := Run(t.Context(), request(d, store, j, "doc[0].machine.ca.key", "doc[0].machine.token"))
	if err == nil {
		t.Fatal("Run succeeded despite a provider failure")
	}
	if !strings.Contains(err.Error(), "doc[0].machine.token") {
		t.Errorf("the error does not name the path that failed: %v", err)
	}
	if res.Sanitized.Valid() {
		t.Error("Run returned a usable Sanitized after failing")
	}
	// The zero Sanitized is what a caller would receive, and every persistence function rejects
	// it. That is the property being relied on, so assert it here too.
	if len(res.Digests) != 0 {
		t.Errorf("Run returned %d digests after failing", len(res.Digests))
	}
}

// TestRunRefusesADocumentAlreadyExtracted checks a second pass is caught. Without it the reference
// would be stored as if it were a secret, the document would be unchanged, and the journal would
// record a substitution that did not happen.
func TestRunRefusesADocumentAlreadyExtracted(t *testing.T) {
	d, store, j := setup(t)

	if _, err := Run(t.Context(), request(d, store, j, "doc[0].machine.ca.key")); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	_, err := Run(t.Context(), request(d, store, j, "doc[0].machine.ca.key"))
	if err == nil {
		t.Fatal("Run extracted the same path twice")
	}
	if !strings.Contains(err.Error(), "already holds a reference") {
		t.Errorf("the error does not explain the double extraction: %v", err)
	}
}

// TestRunExtractsAPathNamedTwiceOnce covers the union Request.Paths documents: schema detection and
// an operator mark naming the same location. That is one secret, stored once, with one reference —
// not a re-ingestion.
func TestRunExtractsAPathNamedTwiceOnce(t *testing.T) {
	d, store, j := setup(t)

	res, err := Run(t.Context(), request(d, store, j, "doc[0].machine.token", "doc[0].machine.ca.key", "doc[0].machine.token"))
	if err != nil {
		t.Fatalf("Run refused a path named twice: %v", err)
	}
	if store.calls != 2 {
		t.Errorf("the provider was written %d time(s), want 2", store.calls)
	}
	if got := len(res.Sanitized.References()); got != 2 {
		t.Errorf("got %d reference(s), want 2", got)
	}
	if got := len(res.Digests); got != 2 {
		t.Errorf("got %d digest(s), want 2", got)
	}
}

// TestRunFailsWhenTheSameValueSitsAtAnUnmarkedPath is a real finding, asserted rather than
// described. Extraction removes a value from the path it was marked at; an identical copy
// elsewhere is untouched, and persisting the result would leak it. Run refuses instead.
//
// This is the case no leak scan on the honest path would surface, because the honest path never
// reaches persistence: the run fails first.
func TestRunFailsWhenTheSameValueSitsAtAnUnmarkedPath(t *testing.T) {
	body := "machine:\n  token: shared-value\n  backup:\n    token: shared-value\n"
	d, err := document.Load([]byte(body))
	if err != nil {
		t.Fatalf("document.Load: %v", err)
	}
	j, err := journal.Open(filepath.Join(t.TempDir(), "journal.jsonl"), "run-test")
	if err != nil {
		t.Fatalf("journal.Open: %v", err)
	}
	t.Cleanup(func() { j.Close() })

	res, err := Run(t.Context(), request(d, newFakeStore(), j, "doc[0].machine.token"))
	if err == nil {
		t.Fatal("Run returned a document still holding the extracted value")
	}
	if !strings.Contains(err.Error(), "doc[0].machine.token") {
		t.Errorf("the error does not name the path: %v", err)
	}
	if res.Sanitized.Valid() {
		t.Error("Run returned a usable Sanitized despite the value remaining")
	}

	// Marking both copies is the working alternative, and it must succeed.
	d2, err := document.Load([]byte(body))
	if err != nil {
		t.Fatalf("document.Load: %v", err)
	}
	res2, err := Run(t.Context(), request(d2, newFakeStore(), j, "doc[0].machine.backup.token", "doc[0].machine.token"))
	if err != nil {
		t.Fatalf("Run with both copies marked: %v", err)
	}
	if strings.Contains(string(res2.Sanitized.Document()), "shared-value") {
		t.Errorf("the sanitized document still holds the value:\n%s", res2.Sanitized.Document())
	}
}

// TestRunFailsWhenAMultiLineValueRemains covers the value the text search cannot see. A multi-line
// secret is re-encoded as an indented block, so the value as extracted — lines joined by bare
// newlines — never appears in the output text, and the first version of the guard passed with the
// secret still in the document. The body here is built so that the second copy stays behind.
func TestRunFailsWhenAMultiLineValueRemains(t *testing.T) {
	const value = "-----BEGIN KEY-----\nline-one-of-the-key\nline-two-of-the-key\n"
	body := "machine:\n  key: |\n    -----BEGIN KEY-----\n    line-one-of-the-key\n    line-two-of-the-key\n" +
		"  backup:\n    key: |\n      -----BEGIN KEY-----\n      line-one-of-the-key\n      line-two-of-the-key\n"
	d, err := document.Load([]byte(body))
	if err != nil {
		t.Fatalf("document.Load: %v", err)
	}
	if got, _ := d.Get("doc[0].machine.backup.key"); got != value {
		t.Fatalf("the fixture does not hold the value it means to: %q", got)
	}
	j, err := journal.Open(filepath.Join(t.TempDir(), "journal.jsonl"), "run-test")
	if err != nil {
		t.Fatalf("journal.Open: %v", err)
	}
	t.Cleanup(func() { j.Close() })

	res, err := Run(t.Context(), request(d, newFakeStore(), j, "doc[0].machine.key"))
	if err == nil {
		t.Fatalf("Run returned a document still holding a multi-line secret:\n%s", res.Sanitized.Document())
	}
	if !strings.Contains(err.Error(), "doc[0].machine.backup.key") {
		t.Errorf("the error does not name where the value remains: %v", err)
	}
}

// TestRunRejectsIncompleteRequests covers the inputs that would each produce a run which looks
// clean while demonstrating nothing.
func TestRunRejectsIncompleteRequests(t *testing.T) {
	d, store, j := setup(t)

	cases := map[string]Request{
		"no document": {Paths: []string{"doc[0].machine.token"}, Store: store, Journal: j, RunID: "r"},
		"no store":    {Document: d, Paths: []string{"doc[0].machine.token"}, Journal: j, RunID: "r"},
		"no journal":  {Document: d, Paths: []string{"doc[0].machine.token"}, Store: store, RunID: "r"},
		"no run id":   {Document: d, Paths: []string{"doc[0].machine.token"}, Store: store, Journal: j},
		"no paths":    {Document: d, Store: store, Journal: j, RunID: "r"},
	}

	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Run(t.Context(), req); err == nil {
				t.Fatal("Run accepted an incomplete request")
			}
		})
	}
}

// TestRunRejectsAnUnknownPath checks a mark pointing at nothing fails here too, not only in the
// mark package, since detection results reach Run by a different route.
func TestRunRejectsAnUnknownPath(t *testing.T) {
	d, store, j := setup(t)
	if _, err := Run(t.Context(), request(d, store, j, "doc[0].machine.absent")); err == nil {
		t.Fatal("Run accepted a path that does not exist")
	}
}

// TestRunHandlesAnEmptyValue checks a marked field that is present but empty is extracted and
// substituted rather than tripping the remains check, which cannot search for an empty string.
func TestRunHandlesAnEmptyValue(t *testing.T) {
	d, err := document.Load([]byte("machine:\n  token: \"\"\n"))
	if err != nil {
		t.Fatalf("document.Load: %v", err)
	}
	j, err := journal.Open(filepath.Join(t.TempDir(), "journal.jsonl"), "run-test")
	if err != nil {
		t.Fatalf("journal.Open: %v", err)
	}
	t.Cleanup(func() { j.Close() })

	res, err := Run(t.Context(), request(d, newFakeStore(), j, "doc[0].machine.token"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(string(res.Sanitized.Document()), "bw:ref:") {
		t.Errorf("an empty marked value was not substituted:\n%s", res.Sanitized.Document())
	}
}

// TestJournalHoldsNoExtractedValue checks the record extract writes carries the digest and the
// redacted rendering, never the value.
func TestJournalHoldsNoExtractedValue(t *testing.T) {
	d, store, j := setup(t)

	if _, err := Run(t.Context(), request(d, store, j, "doc[0].machine.ca.key")); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var detail string
	for _, r := range j.Records() {
		if r.Event == journal.EventExtracted {
			detail = r.Detail
		}
	}
	if detail == "" {
		t.Fatal("no extraction was recorded")
	}
	if strings.Contains(detail, "machine-ca-private-key") {
		t.Errorf("the journal record holds the extracted value: %q", detail)
	}
	if !strings.Contains(detail, "bw:redacted:sha256:") {
		t.Errorf("the journal record does not carry the redacted rendering: %q", detail)
	}
}

// TestRunStopsWhenTheExtractionCannotBeRecorded checks the case where the provider write succeeded
// but the journal did not. Continuing would produce a run whose journal cannot establish that this
// secret was extracted before the draft was written, so stopping is the only honest outcome.
func TestRunStopsWhenTheExtractionCannotBeRecorded(t *testing.T) {
	d, store, j := setup(t)
	// Close the journal's file so Append fails on write, which is the realistic shape of this
	// failure and exercises the production code path rather than a substituted type.
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	res, err := Run(t.Context(), request(d, store, j, "doc[0].machine.ca.key"))
	if err == nil {
		t.Fatal("Run succeeded despite being unable to record the extraction")
	}
	if res.Sanitized.Valid() {
		t.Error("Run returned a usable Sanitized after failing to record")
	}
	// The value did reach the provider, which is exactly why the run must not continue: there is
	// now a secret in the store that no journal entry accounts for.
	if len(store.put) != 1 {
		t.Errorf("the provider holds %d values, want 1", len(store.put))
	}
}
