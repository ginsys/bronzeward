package compile

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// messageOutcome is how a renderer message is shown (compilation.md §8.3).
type messageOutcome string

const (
	messageNone     messageOutcome = "none"     // both passes printed nothing
	messageVerbatim messageOutcome = "verbatim" // no stand-in quoted and the texts are equal
	messageRedacted messageOutcome = "redacted" // the template explained every value quote
	messageWithheld messageOutcome = "withheld" // anything else: the notice replaces the text
)

// valueToken stands for an exact copy of a resolved value that no path covers (compilation.md
// §8.3, the value means).
const valueToken = "<redacted:value>"

// withheldNotice is the fixed text a withheld message of step is replaced by.
func withheldNotice(step string) string {
	return "<withheld: the " + step + " message may quote a sensitive value>"
}

// tokenOf is a tracer's reference token: <redacted:REF@VERSION>, with #N for the member at
// position N of a mapping reference.
func tokenOf(t traced) string {
	if t.Member() >= 0 {
		return fmt.Sprintf("<redacted:%s@%d#%d>", t.Ref(), t.Version(), t.Member())
	}
	return fmt.Sprintf("<redacted:%s@%d>", t.Ref(), t.Version())
}

// redactMessage redacts the message real that step gave on the real composition by trace, the
// message the same step gave on the trace pass (compilation.md §8.3; SP §6.2). The trace message's
// stand-in quotes mark where real quotes values, and the text between them must match exactly
// and in one way only; each value quote is then replaced by its reference's token. A message is
// withheld when the passes disagree, when the template does not match or matches in more than
// one way, when the step's input holds a boolean reference (whose stand-in is its own value, so
// nothing marks a quote of it), or when the values did not all decode. What is shown then has
// every exact copy of a resolved string of copyFloor bytes or more redacted as a value, longest
// first. The result never holds real's text when it is withheld, and real itself is never kept.
func redactMessage(step, real, trace string, ts []traced, boolInput bool, r redactor) (string, messageOutcome) {
	if real == "" && trace == "" {
		return "", messageNone
	}
	withheld := withheldNotice(step)
	if r.opaque || boolInput || real == "" || trace == "" {
		return withheld, messageWithheld
	}
	type quote struct {
		span [2]int
		t    traced
	}
	var all []quote
	for _, t := range ts {
		for _, s := range t.Quotes(trace) {
			all = append(all, quote{s, t})
		}
	}
	slices.SortFunc(all, func(a, b quote) int { return cmp.Or(a.span[0]-b.span[0], b.span[1]-a.span[1]) })
	var qs []quote
	for _, q := range all {
		if len(qs) > 0 && q.span[0] < qs[len(qs)-1].span[1] {
			continue
		}
		qs = append(qs, q)
	}
	if len(qs) == 0 {
		if real != trace {
			return withheld, messageWithheld
		}
		return r.values(real), messageVerbatim
	}
	// The lazy and the greedy match give the lexicographically first and last split of real
	// among the quotes; when they agree the split is the only one.
	pattern := func(capture string) *regexp.Regexp {
		var p strings.Builder
		p.WriteString("(?s)^")
		last := 0
		for _, q := range qs {
			p.WriteString(regexp.QuoteMeta(trace[last:q.span[0]]))
			p.WriteString(capture)
			last = q.span[1]
		}
		p.WriteString(regexp.QuoteMeta(trace[last:]))
		p.WriteString("$")
		return regexp.MustCompile(p.String())
	}
	lazy := pattern("(.+?)").FindStringSubmatchIndex(real)
	greedy := pattern("(.+)").FindStringSubmatchIndex(real)
	if lazy == nil || !slices.Equal(lazy, greedy) {
		return withheld, messageWithheld
	}
	var out strings.Builder
	last := 0
	for i, q := range qs {
		out.WriteString(r.values(real[last:lazy[2+2*i]]))
		out.WriteString(tokenOf(q.t))
		last = lazy[3+2*i]
	}
	out.WriteString(r.values(real[last:]))
	return out.String(), messageRedacted
}

// values is s with every exact copy of a value form of copyFloor bytes or more replaced by
// valueToken, longest first, so a value holding another is replaced whole.
func (r redactor) values(s string) string {
	fs := slices.Clone(r.contains)
	slices.SortFunc(fs, func(a, b string) int { return cmp.Or(len(b)-len(a), strings.Compare(a, b)) })
	var pairs []string
	for _, f := range fs {
		pairs = append(pairs, f, valueToken)
	}
	return strings.NewReplacer(pairs...).Replace(s)
}
