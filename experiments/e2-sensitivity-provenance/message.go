package main

import (
	"regexp"
	"sort"
	"strings"
)

// minQuote is the shortest tracer quote a message is searched for: the tracer head alone.
const minQuote = 5

// A span is where a message quotes a tracer: whole, or a prefix of it (talosctl quotes the first
// seven bytes of a longer value in a decode error).
type span struct {
	start, end int
	id         int
}

// tracerSpans finds every tracer quote in a message of the trace run.
func tracerSpans(msg string, tracers []Tracer) []span {
	var all []span
	for _, t := range tracers {
		switch t.Kind {
		case "int":
			for i := 0; ; {
				j := strings.Index(msg[i:], t.Value)
				if j < 0 {
					break
				}
				s, e := i+j, i+j+len(t.Value)
				if (s == 0 || !isDigit(msg[s-1])) && (e == len(msg) || !isDigit(msg[e])) {
					all = append(all, span{s, e, t.ID})
				}
				i = e
			}
		case "str", "bytes":
			v := t.Value
			if t.Kind == "bytes" {
				all = append(all, exact(msg, t.Value, t.ID)...)
				v = t.Raw
			}
			all = append(all, quotes(msg, v, t.ID)...)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].start != all[j].start {
			return all[i].start < all[j].start
		}
		return all[i].end > all[j].end
	})
	var out []span
	for _, s := range all {
		if len(out) > 0 && s.start < out[len(out)-1].end {
			continue
		}
		out = append(out, s)
	}
	return out
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func exact(msg, v string, id int) []span {
	var out []span
	for i := 0; v != ""; {
		j := strings.Index(msg[i:], v)
		if j < 0 {
			break
		}
		out = append(out, span{i + j, i + j + len(v), id})
		i += j + len(v)
	}
	return out
}

// quotes finds each place a message quotes the tracer or one of its lines from the start, for at
// least minQuote bytes: every line of a string tracer that long begins with the tracer head.
func quotes(msg, v string, id int) []span {
	var out []span
	lines := strings.Split(v, "\n")
	candidates := append([]string{v}, lines...)
	head := v
	if len(head) > minQuote {
		head = head[:minQuote]
	}
	for i := 0; len(head) >= minQuote; {
		j := strings.Index(msg[i:], head)
		if j < 0 {
			break
		}
		s := i + j
		best := 0
		for _, c := range candidates {
			n := commonPrefix(msg[s:], c)
			if n > best {
				best = n
			}
		}
		out = append(out, span{s, s + best, id})
		i = s + best
	}
	return out
}

func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// redactMessage redacts a real step's message by the template the same step gave on the trace
// input, run from the same relative paths: the trace message's tracer quotes are where the real
// message quotes values, and everything between them must match exactly. Outcomes:
//
//	none      both runs printed nothing
//	verbatim  no tracer quoted and the messages are identical: the message depends on no value
//	redacted  the template matched; each quote is replaced by its reference's token
//	withheld  anything else (the runs disagree on success, the text differs in a way no tracer
//	          explains): the message is not shown
func redactMessage(real, trace string, realRC, traceRC int, tracers []Tracer, token func(id int) string) (string, string) {
	const withheld = "<withheld: the message depends on a sensitive value in a way the tracer run does not explain>\n"
	if realRC != traceRC {
		return withheld, "withheld"
	}
	spans := tracerSpans(trace, tracers)
	if len(spans) == 0 {
		switch {
		case real == trace && real == "":
			return "", "none"
		case real == trace:
			return real, "verbatim"
		}
		return withheld, "withheld"
	}
	var pat strings.Builder
	pat.WriteString("(?s)^")
	last := 0
	for _, s := range spans {
		pat.WriteString(regexp.QuoteMeta(trace[last:s.start]))
		pat.WriteString("(.+?)")
		last = s.end
	}
	pat.WriteString(regexp.QuoteMeta(trace[last:]))
	pat.WriteString("$")
	re, err := regexp.Compile(pat.String())
	if err != nil {
		return withheld, "withheld"
	}
	m := re.FindStringSubmatchIndex(real)
	if m == nil {
		return withheld, "withheld"
	}
	var out strings.Builder
	last = 0
	for i, s := range spans {
		gs, ge := m[2+2*i], m[3+2*i]
		out.WriteString(real[last:gs])
		out.WriteString(token(s.id))
		last = ge
	}
	out.WriteString(real[last:])
	return out.String(), "redacted"
}
