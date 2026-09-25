package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// A Secret is one value the oracle looks for: a reference's value (or one leaf of a mapping
// reference), class ref, or a base secrets-bundle value, class base. Value is what the provider
// returned; Placed, when set, is what was placed in the configuration (after a modifier). Key is
// the mapping key a non-string value sits under, the only way to recognize a number or a boolean.
type Secret struct {
	ID     string `json:"id"`
	Class  string `json:"class"`
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	Placed string `json:"placed,omitempty"`
	Key    string `json:"key,omitempty"`
}

// A Finding is how often one form of one secret occurs in a text.
type Finding struct {
	Secret string
	Class  string
	Form   string
	Count  int
}

// The oracle is evaluation, never the redaction mechanism. Its forms, in the order a longer or more
// specific hit wins over one it contains:
//
//	exact         the provider value
//	placed        the value as placed (a modifier's output, such as base64)
//	yaml-escaped  as a YAML double-quoted scalar writes it
//	json-escaped  as encoding/json writes it
//	base64        its standard base64
//	line          one line of eight bytes or more of a multi-line value
//	prefix        its first seven bytes followed by `...`, as a talosctl decode error quotes it
//	fragment      any twelve consecutive bytes of it the public text does not also hold
//	keyed         `<key>: <value>` for a number or a boolean
var formOrder = []string{"exact", "placed", "yaml-escaped", "json-escaped", "base64", "line", "prefix", "fragment", "keyed"}

const (
	minLine     = 8
	fragmentLen = 12
	prefixLen   = 7
)

type hit struct {
	start, end int
	secret     *Secret
	form       int
}

// scan reports every form of every secret in text. A hit inside a longer or earlier-form hit is not
// counted again. public is text known to hold no secret; a fragment it also holds is ignored.
func scan(text string, secrets []Secret, public string) []Finding {
	var hits []hit
	for i := range secrets {
		s := &secrets[i]
		for f, needles := range needles(s, public) {
			for _, n := range needles {
				for _, at := range occurrences(text, n, s.Kind != "str" && s.Kind != "bytes" && f == len(formOrder)-1) {
					hits = append(hits, hit{at, at + len(n), s, f})
				}
			}
		}
	}
	hits = mergeFragments(hits)
	sort.Slice(hits, func(i, j int) bool {
		li, lj := hits[i].end-hits[i].start, hits[j].end-hits[j].start
		if li != lj {
			return li > lj
		}
		if hits[i].form != hits[j].form {
			return hits[i].form < hits[j].form
		}
		return hits[i].start < hits[j].start
	})
	var kept []hit
	counts := map[[2]string]int{}
	class := map[string]string{}
	for _, h := range hits {
		inside := false
		for _, k := range kept {
			if h.start >= k.start && h.end <= k.end {
				inside = true
				break
			}
		}
		if inside {
			continue
		}
		kept = append(kept, h)
		counts[[2]string{h.secret.ID, formOrder[h.form]}]++
		class[h.secret.ID] = h.secret.Class
	}
	var out []Finding
	for k, n := range counts {
		out = append(out, Finding{Secret: k[0], Class: class[k[0]], Form: k[1], Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Secret != out[j].Secret {
			return out[i].Secret < out[j].Secret
		}
		return out[i].Form < out[j].Form
	})
	return out
}

// mergeFragments joins overlapping fragment hits of one secret into one: a longer run of a value
// is one occurrence, not one per twelve-byte window.
func mergeFragments(hits []hit) []hit {
	frag := len(formOrder) - 2
	var rest, frags []hit
	for _, h := range hits {
		if h.form == frag {
			frags = append(frags, h)
		} else {
			rest = append(rest, h)
		}
	}
	sort.Slice(frags, func(i, j int) bool {
		if frags[i].secret.ID != frags[j].secret.ID {
			return frags[i].secret.ID < frags[j].secret.ID
		}
		return frags[i].start < frags[j].start
	})
	for i := 0; i < len(frags); {
		cur := frags[i]
		j := i + 1
		for j < len(frags) && frags[j].secret.ID == cur.secret.ID && frags[j].start < cur.end {
			if frags[j].end > cur.end {
				cur.end = frags[j].end
			}
			j++
		}
		rest = append(rest, cur)
		i = j
	}
	return rest
}

// needles lists, per form index, the strings that form of s is.
func needles(s *Secret, public string) map[int][]string {
	out := map[int][]string{}
	add := func(form string, v string) {
		if v == "" {
			return
		}
		for i, f := range formOrder {
			if f == form {
				out[i] = append(out[i], v)
			}
		}
	}
	v := s.Value
	if s.Kind == "int" || s.Kind == "bool" {
		if s.Key != "" {
			add("keyed", s.Key+": "+v)
		}
		return out
	}
	add("exact", v)
	if s.Class == "base" {
		return out
	}
	if s.Placed != "" && s.Placed != v {
		add("placed", s.Placed)
	}
	if e := yamlEscaped(v); e != v {
		add("yaml-escaped", e)
	}
	if e := jsonEscaped(v); e != v {
		add("json-escaped", e)
	}
	if b := base64.StdEncoding.EncodeToString([]byte(v)); b != s.Placed {
		add("base64", b)
	}
	if strings.Contains(v, "\n") {
		for _, l := range strings.Split(v, "\n") {
			if len(l) >= minLine {
				add("line", l)
			}
		}
	}
	if len(v) > 10 {
		add("prefix", v[:prefixLen]+"...")
	}
	seen := map[string]bool{}
	for i := 0; i+fragmentLen <= len(v); i++ {
		w := v[i : i+fragmentLen]
		if seen[w] || strings.Contains(w, "\n") || strings.Contains(public, w) {
			continue
		}
		seen[w] = true
		add("fragment", w)
	}
	return out
}

// occurrences lists where n starts in text; bounded, it must not run on into a letter or digit
// (a keyed number is not a prefix of a longer one).
func occurrences(text, n string, bounded bool) []int {
	var out []int
	for i := 0; ; {
		j := strings.Index(text[i:], n)
		if j < 0 {
			return out
		}
		at := i + j
		end := at + len(n)
		if !bounded || end == len(text) || !isAlnum(text[end]) {
			out = append(out, at)
		}
		i = at + 1
	}
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func yamlEscaped(v string) string {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	if err := enc.Encode(&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v, Style: yaml.DoubleQuotedStyle}); err != nil {
		return v
	}
	_ = enc.Close()
	s := strings.TrimSuffix(b.String(), "\n")
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return v
}

func jsonEscaped(v string) string {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	return string(b[1 : len(b)-1])
}
