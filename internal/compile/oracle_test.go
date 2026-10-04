package compile

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// The sensitivity gate's oracle: SP's (experiments/e2-sensitivity-provenance/oracle.go), ported.
// It is evaluation, never the redaction mechanism.

// secret is one value the oracle looks for: a case reference's value (or one member of a mapping
// reference), class ref, or a base secrets-bundle value, class base. placed is what a modifier
// placed (base64 for bytes); key is the mapping key a number or boolean sits under, the only way to
// recognize one.
type secret struct{ id, class, kind, value, placed, key string }

// finding is one form of one secret found in a text.
type finding struct{ secret, form string }

// oracleForms, in the order a longer or more specific hit wins over one it contains:
//
//	exact         the provider value
//	placed        the value as placed (a modifier's output)
//	yaml-escaped  as a YAML double-quoted scalar writes it
//	json-escaped  as encoding/json writes it
//	base64        its standard base64
//	line          one line of eight bytes or more of a multi-line value
//	prefix        its first seven bytes followed by `...`, as a talosctl decode error quotes it
//	fragment      any twelve consecutive bytes of it the public text does not also hold
//	keyed         `<key>: <value>` for a number or a boolean
var oracleForms = []string{"exact", "placed", "yaml-escaped", "json-escaped", "base64", "line", "prefix", "fragment", "keyed"}

const (
	oracleMinLine  = 8
	oracleFragment = 12
	oraclePrefix   = 7
)

// oracleScan reports every form of every secret in text, once per secret and form, sorted. A hit
// inside a longer or earlier-form hit is not counted. public is text known to hold no secret; a
// fragment it also holds is ignored.
func oracleScan(text string, secrets []secret, public string) []finding {
	type hit struct {
		start, end int
		secret     string
		form       int
	}
	var hits []hit
	for _, s := range secrets {
		for form, ns := range oracleNeedles(s, public) {
			for _, n := range ns {
				bounded := oracleForms[form] == "keyed"
				for i := 0; ; {
					j := strings.Index(text[i:], n)
					if j < 0 {
						break
					}
					at := i + j
					end := at + len(n)
					if !bounded || end == len(text) || !isAlnum(text[end]) {
						hits = append(hits, hit{at, end, s.id, form})
					}
					i = at + 1
				}
			}
		}
	}
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
	seen := map[finding]bool{}
	var out []finding
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
		f := finding{h.secret, oracleForms[h.form]}
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].secret != out[j].secret {
			return out[i].secret < out[j].secret
		}
		return out[i].form < out[j].form
	})
	return out
}

// oracleNeedles lists, per form index, the strings that form of s is.
func oracleNeedles(s secret, public string) map[int][]string {
	out := map[int][]string{}
	add := func(form, v string) {
		if v != "" {
			i := 0
			for oracleForms[i] != form {
				i++
			}
			out[i] = append(out[i], v)
		}
	}
	v := s.value
	if s.kind == "int" || s.kind == "bool" {
		if s.key != "" {
			add("keyed", s.key+": "+v)
		}
		return out
	}
	add("exact", v)
	if s.class == "base" {
		return out
	}
	if s.placed != "" && s.placed != v {
		add("placed", s.placed)
	}
	if e := yamlEscaped(v); e != v {
		add("yaml-escaped", e)
	}
	if e := jsonEscaped(v); e != v {
		add("json-escaped", e)
	}
	if b := base64.StdEncoding.EncodeToString([]byte(v)); b != s.placed {
		add("base64", b)
	}
	if strings.Contains(v, "\n") {
		for _, l := range strings.Split(v, "\n") {
			if len(l) >= oracleMinLine {
				add("line", l)
			}
		}
	}
	if len(v) > 10 {
		add("prefix", v[:oraclePrefix]+"...")
	}
	seen := map[string]bool{}
	for i := 0; i+oracleFragment <= len(v); i++ {
		w := v[i : i+oracleFragment]
		if seen[w] || strings.Contains(w, "\n") || strings.Contains(public, w) {
			continue
		}
		seen[w] = true
		add("fragment", w)
	}
	return out
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
