// Package mark is Phase-0 evidence code for the secret-ingress feasibility experiment
// (ginsys/bronzeward issue 2). It is not the v1 implementation.
//
// Design §6.9 leaves the marked-secret syntax open, and this experiment must not close it. What it
// needs is somewhere for an operator to say "this value is a secret" — the mechanism by which they
// say it is a separate decision, made in its own work item.
//
// So marking sits behind an interface with two implementations that share no mechanism:
//
//   - PathList is external: a file of dotted paths, alongside the configuration and never inside
//     it. It is chosen as the experiment's default because it is inert with respect to upstream
//     typed decoding — nothing is added to the document, so no Talos schema version can reject it,
//     and it therefore prejudges the §6.9 decision least.
//   - KeySuffix is inline in nature: every scalar whose final key ends in a configured suffix is
//     marked, with no external file at all.
//
// The experiment runs its screen twice, once with each, to show that the ordering results do not
// depend on which one is used. That is the whole reason two exist: not to compare them, but to
// demonstrate that the conclusion survives the choice.
package mark

import (
	"bufio"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/document"
)

// Source decides which scalars an operator has marked as secret.
type Source interface {
	// Name identifies the mechanism in the journal and the report.
	Name() string
	// Marks returns the paths to extract, sorted, with no duplicates. An unmatched or unknown mark
	// is an error rather than a silent omission: a typo that quietly extracted nothing would
	// produce a leak the journal would record as an honest run.
	Marks(d *document.Document) ([]string, error)
}

// PathList marks the paths listed in an external file.
type PathList struct {
	source string
	paths  []string
}

// LoadPathList reads a mark file. Blank lines and lines beginning with # are ignored, so the file
// can explain itself to whoever reads the evidence bundle it ends up in.
func LoadPathList(path string) (*PathList, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("mark: opening the mark file %s: %w", path, err)
	}
	defer f.Close()

	seen := map[string]int{}
	var paths []string
	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		if first, dup := seen[text]; dup {
			return nil, fmt.Errorf("mark: %s lists %q twice (lines %d and %d); one secret would be "+
				"extracted twice and the journal's count would be wrong", path, text, first, line)
		}
		seen[text] = line
		paths = append(paths, text)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("mark: reading %s: %w", path, err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("mark: %s marks nothing; a run with no marks cannot demonstrate "+
			"extraction and would be recorded as clean", path)
	}

	sort.Strings(paths)
	return &PathList{source: path, paths: paths}, nil
}

// NewPathList builds a mark list directly, for tests and for the deliberate-failure controls. A
// path given twice is kept once, so the list meets Source.Marks' no-duplicates contract; it has no
// line numbers to report, unlike LoadPathList, which refuses the duplicate instead.
func NewPathList(source string, paths ...string) *PathList {
	out := append([]string(nil), paths...)
	sort.Strings(out)
	return &PathList{source: source, paths: slices.Compact(out)}
}

// Name implements Source.
func (p *PathList) Name() string { return "path-list:" + p.source }

// Marks implements Source. Every listed path must exist in the document.
func (p *PathList) Marks(d *document.Document) ([]string, error) {
	var missing []string
	for _, path := range p.paths {
		if _, ok := d.Get(path); !ok {
			missing = append(missing, path)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("mark: %s marks %d path(s) this document does not have: %s; "+
			"a mark that matches nothing extracts nothing, and the run would look clean",
			p.source, len(missing), strings.Join(missing, ", "))
	}
	return append([]string(nil), p.paths...), nil
}

// KeySuffix marks every scalar whose final key component ends in one of the given suffixes. It is
// the experiment's second marking mechanism, present so that the ordering results can be shown not
// to depend on how a secret is marked.
type KeySuffix struct {
	suffixes []string
}

// NewKeySuffix builds a suffix matcher. It rejects an empty suffix, which would match every scalar
// in the document and turn the whole configuration into secrets.
func NewKeySuffix(suffixes ...string) (*KeySuffix, error) {
	if len(suffixes) == 0 {
		return nil, fmt.Errorf("mark: no suffixes given")
	}
	for _, s := range suffixes {
		if s == "" {
			return nil, fmt.Errorf("mark: an empty suffix matches every scalar in the document")
		}
	}
	out := append([]string(nil), suffixes...)
	sort.Strings(out)
	return &KeySuffix{suffixes: out}, nil
}

// Name implements Source.
func (k *KeySuffix) Name() string { return "key-suffix:" + strings.Join(k.suffixes, "|") }

// Marks implements Source.
func (k *KeySuffix) Marks(d *document.Document) ([]string, error) {
	var out []string
	for _, path := range d.Paths() {
		if k.matches(lastKey(path)) {
			out = append(out, path)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("mark: no key in this document ends in any of %s; a run with no "+
			"marks cannot demonstrate extraction and would be recorded as clean",
			strings.Join(k.suffixes, ", "))
	}
	return out, nil
}

func (k *KeySuffix) matches(key string) bool {
	for _, s := range k.suffixes {
		if strings.HasSuffix(key, s) {
			return true
		}
	}
	return false
}

// lastKey is the final key component of a dotted path, with any sequence index stripped, so that
// machine.files[0].content yields "content".
func lastKey(path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 {
		path = path[i+1:]
	}
	if i := strings.Index(path, "["); i >= 0 {
		path = path[:i]
	}
	return path
}
