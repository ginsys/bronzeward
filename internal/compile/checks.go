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

// canonical is s decoded as standard base64 and encoded again, when s decodes and that differs:
// the spelling the machinery writes for a byte field it decodes (§8.1), holding the same bytes.
// Decoding skips line breaks, so a string of them alone has no canonical form.
func canonical(s string) (string, bool) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", false
	}
	c := base64.StdEncoding.EncodeToString(b)
	return c, c != "" && c != s
}

// checkOutput is compilation.md §6 step 7 over the real composition's leaves and mapping keys and
// the occurrences' outcomes. A copy is refused at its output paths, a key at the path of the value
// it names; a base override names the first fragment in composition order that overrode the base
// and only the base paths it overrode. The value is never in the refusal.
func checkOutput(real, keys []leaf, sources []Source, outcomes []outcome) error {
	by := -1
	for _, o := range outcomes {
		if o.source == 0 && o.by >= 0 && (by < 0 || o.by < by) {
			by = o.by
		}
	}
	if by >= 0 {
		var overridden []string
		for _, o := range outcomes {
			if o.source == 0 && o.by == by {
				overridden = append(overridden, o.tracer.Path().String())
			}
		}
		slices.Sort(overridden)
		return &Error{Rule: RuleBaseOverride, Input: inputName(by + 1), Paths: slices.Compact(overridden)}
	}
	// A reference replaces a whole node, so a leaf provenance attributes holds a reference's
	// placed value and nothing else: another reference whose value it contains is an overlap of
	// two values, not a literal written into a source, and the leaf is never a copy.
	// The floor applies to each form's own length, as the redactor's does, so a canonical
	// re-encoding shorter than the value it re-spells is not looked for.
	var forms []string
	add := func(fs ...string) {
		for _, f := range fs {
			if len(f) >= copyFloor {
				forms = append(forms, f)
			}
		}
	}
	attributed := map[string]bool{}
	placed := map[[2]string]bool{} // the keys mapping references placed, by the path they name
	for _, o := range outcomes {
		t := o.tracer
		for _, p := range o.paths {
			attributed[p] = true
			if t.Member() >= 0 {
				placed[[2]string{p, t.Leaf()}] = true
			}
		}
		// A mapping's key is a value the provider holds (§4.2), whatever its member's kind; the
		// output leaves are value nodes, so the key where its reference placed it is never one.
		if t.Member() >= 0 {
			add(t.Leaf())
			if c, ok := canonical(t.Leaf()); ok {
				add(c)
			}
		}
		if t.Kind() != ingest.TraceString && t.Kind() != ingest.TraceBytes {
			continue
		}
		x, err := sources[o.source].Values[t.Ref()].Decode()
		if err != nil {
			return fidelity()
		}
		if t.Member() >= 0 {
			m, _ := x.(map[string]any)
			x = m[t.Leaf()]
		}
		s, ok := x.(string)
		if !ok || len(s) < copyFloor {
			continue
		}
		add(s)
		switch t.Kind() {
		case ingest.TraceBytes:
			add(base64.StdEncoding.EncodeToString([]byte(s)))
		case ingest.TraceString:
			if c, ok := canonical(s); ok {
				add(c)
			}
		}
	}
	copied := func(v string) bool {
		return slices.ContainsFunc(forms, func(f string) bool { return strings.Contains(v, f) })
	}
	var copies []string
	for _, l := range real {
		if l.kind == yaml.ScalarNode && !attributed[l.path] && copied(l.value) {
			copies = append(copies, l.path)
		}
	}
	for _, k := range keys {
		if !placed[[2]string{k.path, k.value}] && copied(k.value) {
			copies = append(copies, k.path)
		}
	}
	if len(copies) > 0 {
		slices.Sort(copies)
		return &Error{Rule: RuleCopy, Paths: slices.Compact(copies)}
	}
	return nil
}
