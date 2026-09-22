package control

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/journal"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/secret"
)

// plaintext stands in for the fixture canary. It is not a real credential and never was: the
// fixtures generate their own synthetic values, and this package's tests must not depend on them.
const plaintext = "BWSYNTH-e1-control-test-value"

func openJournal(t *testing.T) *journal.Journal {
	t.Helper()
	j, err := journal.Open(filepath.Join(t.TempDir(), "journal.jsonl"), "run-test")
	if err != nil {
		t.Fatalf("journal.Open: %v", err)
	}
	t.Cleanup(func() { j.Close() })
	return j
}

// TestEveryFileSurfaceActuallyLeaks is the calibration this whole package exists for. A control
// that quietly writes nothing produces a clean bundle indistinguishable from an honest run, and
// the honest run's clean bundle would then be reported as proof of something the instrument never
// tested. Each surface must contain the value verbatim.
// TestTheZeroValueIsAnHonestRun checks an Options literal that never set LeakAt is the honest path.
// Surface's zero value is "", not SurfaceNone, and comparing against SurfaceNone alone scored such
// a run as a control: Active, captioned "leak-at=" and expected to show a hit.
func TestTheZeroValueIsAnHonestRun(t *testing.T) {
	var o Options
	if o.Active() {
		t.Error("the zero Options is Active")
	}
	if got := o.Describe(); got != "honest path" {
		t.Errorf("the zero Options is captioned %q, want \"honest path\"", got)
	}
	if got := o.LeakAt.Expect(); got != ExpectNone {
		t.Errorf("the zero surface expects %v, want ExpectNone", got)
	}
}

func TestEveryFileSurfaceActuallyLeaks(t *testing.T) {
	for _, surface := range []Surface{SurfaceTempFile, SurfaceStaging, SurfaceAppLog, SurfaceErrorReport} {
		t.Run(string(surface), func(t *testing.T) {
			root := t.TempDir()
			where, err := Leak(context.Background(), surface, root, nil,
				secret.NewUnresolved([]byte(plaintext)), openJournal(t))
			if err != nil {
				t.Fatalf("Leak(%s): %v", surface, err)
			}

			body, err := os.ReadFile(where)
			if err != nil {
				t.Fatalf("reading %s: %v", where, err)
			}
			if !strings.Contains(string(body), plaintext) {
				t.Errorf("the %s control wrote no plaintext, so that surface would scan clean for the "+
					"wrong reason: %q", surface, body)
			}
			if surface.Expect() != ExpectHit {
				t.Errorf("%s is declared as %v but leaks verbatim", surface, surface.Expect())
			}
		})
	}
}

// TestTheAppLogSurfaceAppends checks a second leak does not silently replace the first. A control
// that truncated would still produce one hit and would hide an ordering question the report asks:
// whether a value written before extraction is still there afterwards.
func TestTheAppLogSurfaceAppends(t *testing.T) {
	root := t.TempDir()
	value := secret.NewUnresolved([]byte(plaintext))
	j := openJournal(t)

	for range 2 {
		if _, err := Leak(context.Background(), SurfaceAppLog, root, nil, value, j); err != nil {
			t.Fatalf("Leak: %v", err)
		}
	}

	body, err := os.ReadFile(filepath.Join(root, "e1.log"))
	if err != nil {
		t.Fatalf("reading the app log: %v", err)
	}
	if n := strings.Count(string(body), plaintext); n != 2 {
		t.Errorf("the app log holds %d copies after two leaks, want 2", n)
	}
}

// TestTheTransformedSurfaceIsUnreadableToALiteralScan covers the control that must produce zero
// hits. Its value is present and the scan cannot see it, which is a measurement of the
// instrument's limits rather than a caveat about them.
func TestTheTransformedSurfaceIsUnreadableToALiteralScan(t *testing.T) {
	root := t.TempDir()
	where, err := Leak(context.Background(), SurfaceTransformed, root, nil,
		secret.NewUnresolved([]byte(plaintext)), openJournal(t))
	if err != nil {
		t.Fatalf("Leak: %v", err)
	}

	body, err := os.ReadFile(where)
	if err != nil {
		t.Fatalf("reading %s: %v", where, err)
	}
	if strings.Contains(string(body), plaintext) {
		t.Errorf("the transformed control wrote the value verbatim, so it demonstrates nothing: %q", body)
	}
	// It must really be there, or the zero-hit result would be trivial.
	if !strings.Contains(string(body), base64.StdEncoding.EncodeToString([]byte(plaintext))) {
		t.Errorf("the transformed control does not hold the encoded value at all: %q", body)
	}
	if SurfaceTransformed.Expect() != ExpectMiss {
		t.Error("the transformed surface is not declared as an expected miss")
	}
}

// TestLeakIsJournalledAsAWrite checks a control appears in the ordering journal. A leak that was
// not journalled would leave verify-order unable to tell a leaking run from a clean one, and the
// control runs would pass the ordering check they are supposed to fail.
func TestLeakIsJournalledAsAWrite(t *testing.T) {
	j := openJournal(t)
	value := secret.NewUnresolved([]byte(plaintext))

	if _, err := Leak(context.Background(), SurfaceTempFile, t.TempDir(), nil, value, j); err != nil {
		t.Fatalf("Leak: %v", err)
	}

	records := j.Records()
	if len(records) != 1 {
		t.Fatalf("the leak produced %d journal records, want 1", len(records))
	}
	if records[0].Event != journal.EventWrite {
		t.Errorf("the leak was journalled as %q, want %q", records[0].Event, journal.EventWrite)
	}
	if records[0].PayloadSHA256 != value.Digest() {
		t.Error("the journalled digest is not the digest of what was written")
	}
	// The digest, never the value: the journal is committed evidence and is read by people.
	for _, r := range records {
		if strings.Contains(r.Detail, plaintext) || strings.Contains(r.Surface, plaintext) {
			t.Errorf("the journal record carries the plaintext: %+v", r)
		}
	}
}

// TestSurfaceNoneLeaksNothing checks the honest path's value writes no file at all, rather than an
// empty one that a later reader would have to interpret.
func TestSurfaceNoneLeaksNothing(t *testing.T) {
	root := t.TempDir()
	where, err := Leak(context.Background(), SurfaceNone, root, nil,
		secret.NewUnresolved([]byte(plaintext)), openJournal(t))
	if err != nil {
		t.Fatalf("Leak(none): %v", err)
	}
	if where != "" {
		t.Errorf("the honest path reported a leak location %q", where)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("reading the run root: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the honest path left %d entries in the run root", len(entries))
	}
}

// TestParseSurfaceRejectsAMisspelling checks a wrong flag value stops the run. Falling back to the
// honest path would produce a clean bundle filed as a control that never ran, which is the single
// most dangerous failure this experiment can have.
func TestParseSurfaceRejectsAMisspelling(t *testing.T) {
	if _, err := ParseSurface("app_log"); err == nil {
		t.Error("ParseSurface accepted a misspelled surface")
	} else if !strings.Contains(err.Error(), "app-log") {
		t.Errorf("the error does not list the accepted values: %v", err)
	}

	for _, name := range Surfaces() {
		if _, err := ParseSurface(name); err != nil {
			t.Errorf("ParseSurface rejected its own advertised value %q: %v", name, err)
		}
	}
}

// TestOptionsValidateRefusesBundlesThatWouldBeMislabelled covers the combinations that would run,
// succeed, and be read as a result they are not.
func TestOptionsValidateRefusesBundlesThatWouldBeMislabelled(t *testing.T) {
	cases := map[string]Options{
		"redact without persist":   {RedactAfter: true},
		"rollback without persist": {Rollback: true},
		"redact and rollback":      {PersistFirst: true, RedactAfter: true, Rollback: true},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			if err := opts.Validate(); err == nil {
				t.Fatal("Validate accepted a combination that produces a mislabelled bundle")
			}
		})
	}

	for name, opts := range map[string]Options{
		"honest":                {},
		"persist first":         {PersistFirst: true},
		"persist then redact":   {PersistFirst: true, RedactAfter: true},
		"persist then rollback": {PersistFirst: true, Rollback: true},
		"leak only":             {LeakAt: SurfaceAppLog},
	} {
		t.Run(name, func(t *testing.T) {
			if err := opts.Validate(); err != nil {
				t.Fatalf("Validate rejected a legitimate control: %v", err)
			}
		})
	}
}

// TestActiveAndDescribeSeparateControlsFromHonestRuns checks the caption a bundle carries comes
// from the flags rather than from whoever ran it.
func TestActiveAndDescribeSeparateControlsFromHonestRuns(t *testing.T) {
	honest := Options{LeakAt: SurfaceNone}
	if honest.Active() {
		t.Error("an unflagged run reported itself as a control")
	}
	if honest.Describe() != "honest path" {
		t.Errorf("an unflagged run is captioned %q", honest.Describe())
	}

	control := Options{PersistFirst: true, RedactAfter: true, LeakAt: SurfaceDBLog}
	if !control.Active() {
		t.Error("a control reported itself as an honest run")
	}
	for _, want := range []string{"persist-first", "redact-after", "leak-at=db-log"} {
		if !strings.Contains(control.Describe(), want) {
			t.Errorf("the caption %q does not mention %q", control.Describe(), want)
		}
	}
}

// TestPersistFirstAndRedactAfterRefuseBeforeAnyStatement covers the guards that run before a
// connection is used, so a misconfigured control fails as a wrong invocation rather than as a
// database error an operator would read as an environment problem.
func TestPersistFirstAndRedactAfterRefuseBeforeAnyStatement(t *testing.T) {
	ctx := context.Background()
	body := []byte("machine: {}\n")

	if err := PersistFirst(ctx, nil, "run-1", "import", body, nil, openJournal(t), Options{}, nil); err == nil {
		t.Error("PersistFirst ran with no database")
	}
	if err := RedactAfter(ctx, nil, "run-1", secret.NewSanitized(body, nil), openJournal(t)); err == nil {
		t.Error("RedactAfter ran with no database")
	}
	// The zero Sanitized never went through extraction, so there is nothing to redact to.
	if err := RedactAfter(ctx, nil, "run-1", secret.Sanitized{}, openJournal(t)); err == nil {
		t.Error("RedactAfter accepted a document that did not come from extraction")
	}
}

// TestSortedKeysIsStable checks two runs of the same control issue the same sequence of statements,
// so a diff between their bundles is about the data rather than about Go's map iteration.
func TestSortedKeysIsStable(t *testing.T) {
	index := map[string]string{"cluster.token": "b", "machine.token": "a", "cluster.ca.key": "c"}
	want := []string{"cluster.ca.key", "cluster.token", "machine.token"}

	for range 5 {
		got := sortedKeys(index)
		if len(got) != len(want) {
			t.Fatalf("sortedKeys gave %d keys, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("sortedKeys gave %v, want %v", got, want)
			}
		}
	}
}
