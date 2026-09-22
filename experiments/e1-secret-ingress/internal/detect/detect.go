// Package detect is Phase-0 evidence code for the secret-ingress feasibility experiment
// (ginsys/bronzeward issue 2). It is not the v1 implementation.
//
// It is the "known secrets" half of design §7.1: the locations a Talos machine configuration is
// expected to carry a secret in, recognised from the schema rather than from an operator's mark.
//
// §6.9 forbids claiming completeness for this, and the rule table below is why that prohibition is
// right rather than merely cautious. It is a hand-written list. It recognises what it was told to
// recognise, and a configuration that carries a secret anywhere else — in a file's inline content,
// in an extra argument, in a manifest embedded in the cluster section — is not covered. The
// experiment demonstrates those misses rather than caveating them.
//
// The table also carries the precision problem, deliberately. A certificate authority's key is a
// secret; the certificate beside it is not, and both sit under a `ca` mapping with names that
// differ by three characters. A detector that flags `machine.ca.crt` is not being safe, it is
// extracting a public value into a secret store and teaching an operator to ignore its output.
package detect

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/document"
)

// Rule is one recognised secret-bearing location.
type Rule struct {
	// Pattern is a dotted path. A `*` stands for exactly one key, and `[*]` for any sequence
	// index. It is not a general glob: `**` is not supported, because a pattern that can cross an
	// arbitrary number of levels is exactly how a detector starts matching things nobody checked.
	Pattern string
	// Why states what the value is, in the words the report will use.
	Why string
}

// TalosRules is the table. It covers the v1alpha1 locations the fixtures' own secrets bundle
// populates, and nothing beyond them.
//
// Each entry was added because a value at that path appears in a generated secrets bundle. None
// was added by guessing from a field's name, which is the failure mode that produces a detector
// with good recall on paper and no precision in practice.
func TalosRules() []Rule {
	return []Rule{
		{"doc[*].machine.token", "the machine's join token"},
		{"doc[*].machine.ca.key", "the machine certificate authority's private key"},
		{"doc[*].cluster.id", "the cluster identifier, which is a generated secret in v1alpha1"},
		{"doc[*].cluster.secret", "the cluster's bootstrap secret"},
		{"doc[*].cluster.token", "the cluster's join token"},
		{"doc[*].cluster.secretboxEncryptionSecret", "the secretbox encryption key for etcd at rest"},
		{"doc[*].cluster.aescbcEncryptionSecret", "the AES-CBC encryption key for etcd at rest"},
		{"doc[*].cluster.ca.key", "the cluster certificate authority's private key"},
		{"doc[*].cluster.aggregatorCA.key", "the aggregator certificate authority's private key"},
		{"doc[*].cluster.serviceAccount.key", "the service account signing key"},
		{"doc[*].cluster.etcd.ca.key", "etcd's certificate authority private key"},
	}
}

// Finding is one location the schema rules recognised.
type Finding struct {
	// Path is where the value is.
	Path string
	// Pattern is the rule that matched, so a finding can be traced to the line that produced it.
	Pattern string
	// Why is that rule's explanation.
	Why string
}

// Schema returns every location in d that the rules recognise, sorted by path.
//
// A location whose value is empty is still reported. An empty field is not evidence that no secret
// belongs there, and silently dropping it would make recall depend on whether this particular
// environment happened to populate the field — which is the denominator problem this experiment
// has to state plainly rather than hide inside a detector.
func Schema(d *document.Document, rules []Rule) []Finding {
	var out []Finding
	for _, path := range d.Paths() {
		for _, rule := range rules {
			if matches(rule.Pattern, path) {
				out = append(out, Finding{Path: path, Pattern: rule.Pattern, Why: rule.Why})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Paths is the findings' paths, which is what extraction consumes.
func Paths(findings []Finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.Path)
	}
	return out
}

// matches compares a pattern to a path, segment by segment.
func matches(pattern, path string) bool {
	p, q := strings.Split(pattern, "."), strings.Split(path, ".")
	if len(p) != len(q) {
		return false
	}
	for i := range p {
		if p[i] == "*" {
			continue
		}
		if p[i] == q[i] {
			continue
		}
		// `name[*]` matches `name[<any digits>]`.
		if base, ok := strings.CutSuffix(p[i], "[*]"); ok {
			if rest, ok := strings.CutPrefix(q[i], base+"["); ok && strings.HasSuffix(rest, "]") {
				continue
			}
		}
		return false
	}
	return true
}

// Assessment is how a detector's output compares to a known ground truth.
//
// It reports sets rather than a score. §6.9 forbids a completeness claim, and a single number is
// how a completeness claim gets made by accident: 0.94 reads as "nearly all of them" whether the
// denominator was 17 fields or 3. The report prints both denominators and the members of every
// set, and never an F-score.
type Assessment struct {
	// Correct is what the detector found that is genuinely a secret.
	Correct []string
	// Spurious is what it flagged that is not. A public certificate here is not a harmless
	// over-reach: it puts a public value in a secret store and trains an operator to skim.
	Spurious []string
	// Missed is what is genuinely a secret and was not found. These are what an operator must
	// mark by hand, which §6.9 makes their explicit responsibility.
	Missed []string
}

// Assess compares found against truth. Both are treated as sets; duplicates are ignored.
func Assess(found, truth []string) Assessment {
	inTruth := map[string]bool{}
	for _, t := range truth {
		inTruth[t] = true
	}
	inFound := map[string]bool{}
	for _, f := range found {
		inFound[f] = true
	}

	var a Assessment
	for f := range inFound {
		if inTruth[f] {
			a.Correct = append(a.Correct, f)
		} else {
			a.Spurious = append(a.Spurious, f)
		}
	}
	for t := range inTruth {
		if !inFound[t] {
			a.Missed = append(a.Missed, t)
		}
	}
	sort.Strings(a.Correct)
	sort.Strings(a.Spurious)
	sort.Strings(a.Missed)
	return a
}

// Recall is the count found over the count that exist, as integers. It returns 0/0 for an empty
// ground truth rather than a ratio, because a detector cannot be said to recall anything when
// there was nothing to recall.
func (a Assessment) Recall() (found, exist int) {
	return len(a.Correct), len(a.Correct) + len(a.Missed)
}

// Precision is the count correct over the count flagged, as integers.
func (a Assessment) Precision() (correct, flagged int) {
	return len(a.Correct), len(a.Correct) + len(a.Spurious)
}

// String renders the assessment the way the report prints it: integers with both denominators, the
// members of each set, and no derived score.
func (a Assessment) String() string {
	rn, rd := a.Recall()
	pn, pd := a.Precision()
	var b strings.Builder
	fmt.Fprintf(&b, "recall %d/%d, precision %d/%d\n", rn, rd, pn, pd)
	fmt.Fprintf(&b, "  correct  (%d): %s\n", len(a.Correct), join(a.Correct))
	fmt.Fprintf(&b, "  spurious (%d): %s\n", len(a.Spurious), join(a.Spurious))
	fmt.Fprintf(&b, "  missed   (%d): %s\n", len(a.Missed), join(a.Missed))
	return b.String()
}

func join(paths []string) string {
	if len(paths) == 0 {
		return "none"
	}
	return strings.Join(paths, ", ")
}
