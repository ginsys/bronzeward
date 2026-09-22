package mark

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/document"
)

const sample = `version: v1alpha1
machine:
  type: controlplane
  token: abcdef.0123456789abcdef
  ca:
    crt: cert-value
    key: key-value
  files:
    - path: /var/etc/thing.conf
      content: inline-content
`

func load(t *testing.T) *document.Document {
	t.Helper()
	d, err := document.Load([]byte(sample))
	if err != nil {
		t.Fatalf("document.Load: %v", err)
	}
	return d
}

func writeMarks(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "marks.txt")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("writing the mark file: %v", err)
	}
	return path
}

// TestPathListReadsAndSorts covers the file format, including the comments that let a mark file
// explain itself to whoever finds it in an evidence bundle.
func TestPathListReadsAndSorts(t *testing.T) {
	path := writeMarks(t,
		"# the operator's marks for this run",
		"machine.ca.key",
		"",
		"  machine.token  ",
		"machine.ca.crt",
	)

	p, err := LoadPathList(path)
	if err != nil {
		t.Fatalf("LoadPathList: %v", err)
	}

	got, err := p.Marks(load(t))
	if err != nil {
		t.Fatalf("Marks: %v", err)
	}
	want := "machine.ca.crt,machine.ca.key,machine.token"
	if strings.Join(got, ",") != want {
		t.Errorf("Marks() = %v, want %s", got, want)
	}
	if !strings.HasPrefix(p.Name(), "path-list:") {
		t.Errorf("Name() = %q, want it to identify the mechanism", p.Name())
	}
}

// TestPathListRejectsAMarkThatMatchesNothing is the one that keeps a typo from producing a leak
// the journal would record as an honest run: an unmatched mark extracts nothing, the secret stays
// in the document, and every check downstream passes.
func TestPathListRejectsAMarkThatMatchesNothing(t *testing.T) {
	p := NewPathList("test", "machine.ca.key", "machine.ca.keyy")

	_, err := p.Marks(load(t))
	if err == nil {
		t.Fatal("Marks accepted a mark that matches nothing in the document")
	}
	if !strings.Contains(err.Error(), "machine.ca.keyy") {
		t.Errorf("the error does not name the unmatched mark: %v", err)
	}
	// It must also say why this is fatal rather than cosmetic.
	if !strings.Contains(err.Error(), "look clean") {
		t.Errorf("the error does not explain the consequence: %v", err)
	}
}

// TestPathListRejectsDuplicates checks a path listed twice is refused. Extracting one secret twice
// would put two EventExtracted records in the journal for one value and make the count wrong.
func TestPathListRejectsDuplicates(t *testing.T) {
	path := writeMarks(t, "machine.ca.key", "machine.token", "machine.ca.key")

	_, err := LoadPathList(path)
	if err == nil {
		t.Fatal("LoadPathList accepted a duplicated path")
	}
	if !strings.Contains(err.Error(), "lines 1 and 3") {
		t.Errorf("the error does not name both lines: %v", err)
	}
}

// TestPathListRejectsAnEmptyFile checks a mark file with nothing in it fails the run rather than
// producing a clean bundle that demonstrates nothing.
func TestPathListRejectsAnEmptyFile(t *testing.T) {
	for name, lines := range map[string][]string{
		"empty":         {""},
		"only comments": {"# nothing here", "#"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadPathList(writeMarks(t, lines...)); err == nil {
				t.Fatal("LoadPathList accepted a file that marks nothing")
			}
		})
	}
}

// TestPathListReportsAMissingFile checks the run fails loudly rather than proceeding with no marks.
func TestPathListReportsAMissingFile(t *testing.T) {
	if _, err := LoadPathList(filepath.Join(t.TempDir(), "absent.txt")); err == nil {
		t.Fatal("LoadPathList accepted a path that does not exist")
	}
}

// TestKeySuffixMarksByMechanismNotByList covers the second marking mechanism, which shares no code
// path with the first. Its job in the experiment is to show that the ordering results survive a
// change of marking syntax.
func TestKeySuffixMarksByMechanismNotByList(t *testing.T) {
	k, err := NewKeySuffix("key", "token")
	if err != nil {
		t.Fatalf("NewKeySuffix: %v", err)
	}

	got, err := k.Marks(load(t))
	if err != nil {
		t.Fatalf("Marks: %v", err)
	}
	want := "machine.ca.key,machine.token"
	if strings.Join(got, ",") != want {
		t.Errorf("Marks() = %v, want %s", got, want)
	}
	if !strings.HasPrefix(k.Name(), "key-suffix:") {
		t.Errorf("Name() = %q, want it to identify the mechanism", k.Name())
	}
}

// TestKeySuffixStripsSequenceIndices checks a path inside a sequence is matched on its key rather
// than on the bracketed index, so machine.files[0].content is matched by "content".
func TestKeySuffixStripsSequenceIndices(t *testing.T) {
	k, err := NewKeySuffix("content")
	if err != nil {
		t.Fatalf("NewKeySuffix: %v", err)
	}

	got, err := k.Marks(load(t))
	if err != nil {
		t.Fatalf("Marks: %v", err)
	}
	if strings.Join(got, ",") != "machine.files[0].content" {
		t.Errorf("Marks() = %v, want machine.files[0].content", got)
	}
}

// TestKeySuffixRejectsAnEmptySuffix checks the degenerate case that would mark every scalar in the
// document and make every run look like a total extraction.
func TestKeySuffixRejectsAnEmptySuffix(t *testing.T) {
	if _, err := NewKeySuffix(); err == nil {
		t.Error("NewKeySuffix accepted no suffixes at all")
	}
	if _, err := NewKeySuffix("key", ""); err == nil {
		t.Error("NewKeySuffix accepted an empty suffix")
	}
}

// TestKeySuffixRejectsMatchingNothing checks the same failure PathList guards against, reached by
// the other mechanism.
func TestKeySuffixRejectsMatchingNothing(t *testing.T) {
	k, err := NewKeySuffix("passphrase")
	if err != nil {
		t.Fatalf("NewKeySuffix: %v", err)
	}
	if _, err := k.Marks(load(t)); err == nil {
		t.Fatal("Marks accepted a suffix that matches nothing")
	}
}

// TestBothSourcesSatisfyTheInterface pins that the two mechanisms are interchangeable at the call
// site, which is what lets the screen be re-run against each without touching the flow under test.
func TestBothSourcesSatisfyTheInterface(t *testing.T) {
	k, err := NewKeySuffix("key")
	if err != nil {
		t.Fatalf("NewKeySuffix: %v", err)
	}
	sources := []Source{NewPathList("test", "machine.ca.key"), k}

	for _, s := range sources {
		marks, err := s.Marks(load(t))
		if err != nil {
			t.Errorf("%s: %v", s.Name(), err)
			continue
		}
		if strings.Join(marks, ",") != "machine.ca.key" {
			t.Errorf("%s marked %v, want machine.ca.key", s.Name(), marks)
		}
	}
}
