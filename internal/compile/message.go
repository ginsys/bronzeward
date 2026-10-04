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
// one way, when the step's input is unmarked (it holds a boolean reference, whose stand-in is its
// own value, or a mapping reference, whose keys the trace pass keeps, so nothing marks a quote of
// either), when two quotes of different references partly overlap, or when the values did not
// all decode. What is shown then has every exact copy of a resolved string of copyFloor bytes or
// more redacted as a value, overlapping copies as one, and is withheld if a value form still
// remains, as when a token's reference name spells one. The result never holds real's text when
// it is withheld, and real itself is never kept.
func redactMessage(step, real, trace string, ts []traced, unmarked bool, r redactor) (string, messageOutcome) {
	if real == "" && trace == "" {
		return "", messageNone
	}
	withheld := withheldNotice(step)
	if r.opaque || unmarked || real == "" || trace == "" {
		return withheld, messageWithheld
	}
	var all []quote
	for _, t := range ts {
		for _, s := range t.Quotes(trace) {
			all = append(all, quote{s, t})
		}
	}
	qs, ok := orderQuotes(all)
	if !ok {
		return withheld, messageWithheld
	}
	if len(qs) == 0 {
		if real != trace {
			return withheld, messageWithheld
		}
		if out := r.values(real); !r.holds(out) {
			return out, messageVerbatim
		}
		return withheld, messageWithheld
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
		// A token names its reference, and a name can be a value too short to be looked for in
		// the text.
		if r.exact[q.t.Ref()] {
			return withheld, messageWithheld
		}
		out.WriteString(r.values(real[last:lazy[2+2*i]]))
		out.WriteString(tokenOf(q.t))
		last = lazy[3+2*i]
	}
	out.WriteString(r.values(real[last:]))
	// A token names its reference, and a name can spell a value.
	if r.holds(out.String()) {
		return withheld, messageWithheld
	}
	return out.String(), messageRedacted
}

// quote is where a trace message quotes one tracer's stand-in.
type quote struct {
	span [2]int
	t    traced
}

// orderQuotes is all in message order, a quote inside another dropped. Two quotes that only partly
// overlap name two references for one run of text, so no token can stand for it: not ok.
func orderQuotes(all []quote) ([]quote, bool) {
	slices.SortFunc(all, func(a, b quote) int { return cmp.Or(a.span[0]-b.span[0], b.span[1]-a.span[1]) })
	var qs []quote
	for _, q := range all {
		if len(qs) > 0 && q.span[0] < qs[len(qs)-1].span[1] {
			if q.span[1] > qs[len(qs)-1].span[1] {
				return nil, false
			}
			continue
		}
		qs = append(qs, q)
	}
	return qs, true
}

// values is s with every run covered by exact copies of value forms of copyFloor bytes or more
// replaced by one valueToken. Copies that overlap or touch are one run, so no part of any copy is
// left beside a token.
func (r redactor) values(s string) string {
	covered := make([]bool, len(s))
	for _, f := range r.contains {
		for i := 0; ; i++ {
			j := strings.Index(s[i:], f)
			if j < 0 {
				break
			}
			i += j
			for k := i; k < i+len(f); k++ {
				covered[k] = true
			}
		}
	}
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if !covered[i] {
			out.WriteByte(s[i])
			continue
		}
		out.WriteString(valueToken)
		for i+1 < len(s) && covered[i+1] {
			i++
		}
	}
	return out.String()
}
