package main

import (
	"sort"
	"strings"
)

// minValue is the shortest whole value the value matcher replaces: shorter values (a port, `true`)
// would replace every unrelated occurrence of the same text, so a value matcher cannot use them.
const minValue = 6

const valueToken = "<redacted:value>"

// A valueMatcher is the baseline representation (design 6.9: "value matching alone is
// insufficient"): it replaces the values the resolver returned, whole and line by line, wherever
// they occur as text. It knows nothing of paths, encodings or truncation.
type valueMatcher struct {
	needles []string
}

func newValueMatcher(values []string) *valueMatcher {
	seen := map[string]bool{}
	var n []string
	add := func(s string, min int) {
		if len(s) >= min && !seen[s] {
			seen[s] = true
			n = append(n, s)
		}
	}
	for _, v := range values {
		add(v, minValue)
		if strings.Contains(v, "\n") {
			for _, l := range strings.Split(v, "\n") {
				add(l, minLine)
			}
		}
	}
	sort.Slice(n, func(i, j int) bool { return len(n[i]) > len(n[j]) })
	return &valueMatcher{needles: n}
}

// redact replaces every needle, longest first, and returns the text and the number replaced.
func (m *valueMatcher) redact(text string) (string, int) {
	total := 0
	for _, s := range m.needles {
		c := strings.Count(text, s)
		if c > 0 {
			text = strings.ReplaceAll(text, s, valueToken)
			total += c
		}
	}
	return text, total
}
