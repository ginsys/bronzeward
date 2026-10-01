package ingest

import (
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// redacted stands for a path token that holds an extracted value.
const redacted = "<redacted>"

// guard is compilation.md §4.2 over the candidate stream, parsed back: value comparison over
// every scalar, keys and references included; substring search over every scalar except this
// run's references. It also searches every comment, which §4.2 does not name: a comment is
// persisted with the stream and no other check reads it. The refusal names the matching paths
// and the rule; any path token holding an extracted value is shown as <redacted>. An identified
// embedded document is checked as its parsed nodes, as validate checks it, so this run's
// references inside it are skipped like those outside.
func guard(docs []*yaml.Node, exs []extraction, embedded map[string]string) error {
	values := extractedScalars(exs)
	texts := searchTexts(values)
	if len(texts) == 0 {
		return nil
	}
	minted := map[string]bool{}
	for _, ex := range exs {
		minted[ex.name] = true
	}
	var equal, within []string
	hit := func(list *[]string, p Path) {
		if s := redactPath(p, texts); !slices.Contains(*list, s) {
			*list = append(*list, s)
		}
	}
	for i, d := range docs {
		if containsAny(d.HeadComment+"\n"+d.FootComment, texts) {
			hit(&within, Path{Doc: i})
		}
	}
	var check visit
	check = func(n *yaml.Node, p Path, key bool, _ *yaml.Node) error {
		if key && n.Kind == yaml.ScalarNode {
			p = p.child(n.Value)
		}
		if containsAny(n.HeadComment+"\n"+n.LineComment+"\n"+n.FootComment, texts) {
			hit(&within, p)
		}
		// Anchor and alias names are persisted text too.
		if containsAny(n.Anchor, texts) || n.Kind == yaml.AliasNode && containsAny(n.Value, texts) {
			hit(&within, p)
		}
		if n.Kind != yaml.ScalarNode {
			return nil
		}
		if format, ok := embedded[p.String()]; ok && !key && p.Format == "" {
			if inner, err := embeddedDocument(n); err == nil {
				// The whole text first, without this run's references: a copy can span
				// nodes, or sit in a comment the parsed document drops.
				if equalsAny(n, values) {
					hit(&equal, p)
				} else if containsAny(withoutMinted(n.Value, minted), texts) {
					hit(&within, p)
				}
				return walkEmbedded(inner, p, format, check)
			}
		}
		switch {
		case equalsAny(n, values):
			hit(&equal, p)
		case n.Tag == refTag && minted[n.Value]:
		case containsAny(n.Value, texts):
			hit(&within, p)
		}
		return nil
	}
	_ = walkStream(docs, check)
	if len(equal) > 0 {
		return refuse(RuleGuardValue, equal...)
	}
	if len(within) > 0 {
		return refuse(RuleGuardSubstring, within...)
	}
	return nil
}

// extractedScalars is every extracted scalar value: each scalar extraction, and each member of
// an extracted mapping (its keys are not values).
func extractedScalars(exs []extraction) []any {
	var out []any
	for _, ex := range exs {
		if m, ok := ex.plain.(map[string]any); ok {
			for _, k := range sortedKeys(m) {
				out = append(out, m[k])
			}
			continue
		}
		out = append(out, ex.plain)
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// scalarText is a scalar value as it reads in YAML.
func scalarText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

// searchTexts is the text of every extracted value; an empty value discloses nothing and would
// match everything, so it is not searched for.
func searchTexts(values []any) []string {
	var out []string
	for _, v := range values {
		if s := scalarText(v); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// equalsAny compares a scalar with the extracted values as parsed values: its text, or for an
// integer or boolean scalar its decoded value.
func equalsAny(n *yaml.Node, values []any) bool {
	for _, v := range values {
		s := scalarText(v)
		if s == "" {
			continue
		}
		if n.Value == s {
			return true
		}
		switch x := v.(type) {
		case int64:
			var i int64
			if n.Tag == "!!int" && n.Decode(&i) == nil && i == x {
				return true
			}
		case uint64:
			var u uint64
			if n.Tag == "!!int" && n.Decode(&u) == nil && u == x {
				return true
			}
		case bool:
			var b bool
			if n.Tag == "!!bool" && n.Decode(&b) == nil && b == x {
				return true
			}
		}
	}
	return false
}

// withoutMinted is an embedded document's text with this run's references removed, so that only
// the text around them is searched.
func withoutMinted(s string, minted map[string]bool) string {
	for _, name := range slices.Sorted(maps.Keys(minted)) {
		s = strings.ReplaceAll(s, refTag+" "+name, "")
	}
	return s
}

func containsAny(s string, texts []string) bool {
	for _, t := range texts {
		if strings.Contains(s, t) {
			return true
		}
	}
	return false
}

// redactRefusal re-renders a refusal's paths through redactPath against texts; other errors pass
// unchanged. A string that is not a path is kept only when it is a reference name holding no
// value: its escaping is unknown, so a value inside it may not read as itself.
func redactRefusal(err error, texts []string) error {
	var r *Refusal
	if !errors.As(err, &r) {
		return err
	}
	out := &Refusal{Rule: r.Rule}
	for _, s := range r.Paths {
		if p, perr := ParsePath(s); perr == nil {
			out.Paths = append(out.Paths, redactPath(p, texts))
		} else if validName(s) && !holdsAny(s, texts) {
			out.Paths = append(out.Paths, s)
		} else {
			out.Paths = append(out.Paths, redacted)
		}
	}
	return out
}

// documentsOnly re-renders a refusal's paths as their documents alone, for when the values to
// redact are not all known; anything that is not a path is shown as <redacted>. Other errors pass
// unchanged.
func documentsOnly(err error) error {
	var r *Refusal
	if !errors.As(err, &r) {
		return err
	}
	out := &Refusal{Rule: r.Rule}
	for _, s := range r.Paths {
		d := redacted
		if p, perr := ParsePath(s); perr == nil {
			d = Path{Doc: p.Doc}.String()
		}
		if !slices.Contains(out.Paths, d) {
			out.Paths = append(out.Paths, d)
		}
	}
	return out
}

// redactPath renders p with every token that holds an extracted value replaced by <redacted>.
func redactPath(p Path, texts []string) string {
	q := Path{Doc: p.Doc, Format: p.Format, Pointer: slices.Clone(p.Pointer), Inner: slices.Clone(p.Inner)}
	for _, tokens := range [][]string{q.Pointer, q.Inner} {
		for i, tok := range tokens {
			if holdsAny(tok, texts) {
				tokens[i] = redacted
			}
		}
	}
	return q.String()
}

// holdsAny reports whether s contains one of texts, or as a plain scalar decodes to the same
// integer or boolean as one of them: the guard's equality (equalsAny) treats 0x4cb2f as 314159
// and TRUE as true, so a key spelled either way discloses the value.
func holdsAny(s string, texts []string) bool {
	if containsAny(s, texts) {
		return true
	}
	for _, t := range texts {
		if sameScalar(s, t) {
			return true
		}
	}
	return false
}

// sameScalar reports whether a and b, read as plain YAML scalars, are the same integer or
// boolean.
func sameScalar(a, b string) bool {
	na := &yaml.Node{Kind: yaml.ScalarNode, Value: a}
	nb := &yaml.Node{Kind: yaml.ScalarNode, Value: b}
	tag := na.ShortTag()
	if tag != nb.ShortTag() || tag != "!!int" && tag != "!!bool" {
		return false
	}
	var va, vb any
	if na.Decode(&va) != nil || nb.Decode(&vb) != nil {
		return false
	}
	return va == vb
}
