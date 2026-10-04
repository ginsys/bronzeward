package compile

import (
	"encoding/base64"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/ingest"
)

const (
	// RuleCopy refuses a resolved string of copyFloor bytes or more, in its stored or placed
	// form, contained in an output leaf provenance does not attribute to its reference: a
	// source-side exposure, reported by path only (compilation.md §6 step 7, choice §16.21).
	RuleCopy Rule = "exact-copy"
	// RuleBaseOverride refuses a fragment that overrode or deleted a reference of the import
	// base (compilation.md §6 step 7, choice §16.20).
	RuleBaseOverride Rule = "base-override"
)

// copyFloor is SP's value matcher's floor: a shorter value is not looked for.
const copyFloor = 6

// checkOutput is compilation.md §6 step 7 over the real composition's leaves and the occurrences'
// outcomes. A copy is refused at its output paths; a base override names the first overriding
// fragment and the base paths it overrode. The value is never in the refusal.
func checkOutput(real []leaf, sources []Source, outcomes []outcome) error {
	var overridden []string
	by := -1
	for _, o := range outcomes {
		if o.source == 0 && o.by >= 0 {
			overridden = append(overridden, o.tracer.Path().String())
			if by < 0 {
				by = o.by
			}
		}
	}
	if by >= 0 {
		slices.Sort(overridden)
		return &Error{Rule: RuleBaseOverride, Input: inputName(by + 1), Paths: slices.Compact(overridden)}
	}
	// A reference replaces a whole node, so a leaf provenance attributes holds a reference's
	// placed value and nothing else: another reference whose value it contains is an overlap of
	// two values, not a literal written into a source, and the leaf is never a copy.
	var forms []string
	attributed := map[string]bool{}
	for _, o := range outcomes {
		t := o.tracer
		for _, p := range o.paths {
			attributed[p] = true
		}
		if t.Kind() != ingest.TraceString && t.Kind() != ingest.TraceBytes {
			continue
		}
		x, err := sources[o.source].Values[t.Ref()].Decode()
		if err != nil {
			return fidelity()
		}
		if t.Leaf() != "" {
			m, _ := x.(map[string]any)
			x = m[t.Leaf()]
		}
		s, ok := x.(string)
		if !ok || len(s) < copyFloor {
			continue
		}
		forms = append(forms, s)
		if t.Kind() == ingest.TraceBytes {
			forms = append(forms, base64.StdEncoding.EncodeToString([]byte(s)))
		}
	}
	var copies []string
	for _, l := range real {
		if l.kind == yaml.ScalarNode && !attributed[l.path] && slices.ContainsFunc(forms, func(f string) bool { return strings.Contains(l.value, f) }) {
			copies = append(copies, l.path)
		}
	}
	if len(copies) > 0 {
		return &Error{Rule: RuleCopy, Paths: copies}
	}
	return nil
}
