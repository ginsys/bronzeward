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
	// check returns no error today; a walk that stopped early would leave nodes unchecked.
	if err := walkStream(docs, check); err != nil {
		return err
	}
	if len(equal) > 0 {
		return refuse(RuleGuardValue, equal...)
	}
	if len(within) > 0 {
		return refuse(RuleGuardSubstring, within...)
	}
	return nil
}

// extractedScalars is every extracted scalar value: each scalar extraction, and each key and
// member of an extracted mapping. The keys are stored in the provider with the members, so a
// copy of one elsewhere persists part of the extracted value.
func extractedScalars(exs []extraction) []any {
	var out []any
	for _, ex := range exs {
		if m, ok := ex.plain.(map[string]any); ok {
			for _, k := range sortedKeys(m) {
				out = append(out, k, m[k])
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

// equalsAny compares a scalar with the extracted values as parsed values: its text, or for a
// number or boolean scalar its decoded value (sameValue). A string value is compared as the
// plain scalar its text reads as, so "314159" and 314159 are one secret, as redaction treats them.
func equalsAny(n *yaml.Node, values []any) bool {
	nv, typed := decodedScalar(n)
	for _, v := range values {
		s := scalarText(v)
		if s == "" {
			continue
		}
		if n.Value == s {
			return true
		}
		if str, ok := v.(string); ok {
			v, _ = decodedScalar(&yaml.Node{Kind: yaml.ScalarNode, Value: str})
		}
		if typed && sameValue(nv, v) {
			return true
		}
	}
	return false
}

// decodedScalar is a scalar's decoded value when it is an integer (int64 or uint64), a float
// (float64) or a boolean.
func decodedScalar(n *yaml.Node) (any, bool) {
	if n.Kind != yaml.ScalarNode {
		return nil, false
	}
	switch n.ShortTag() {
	case "!!int":
		var i int64
		if n.Decode(&i) == nil {
			return i, true
		}
		var u uint64
		if n.Decode(&u) == nil {
			return u, true
		}
	case "!!float":
		var f float64
		if n.Decode(&f) == nil {
			return f, true
		}
	case "!!bool":
		var b bool
		if n.Decode(&b) == nil {
			return b, true
		}
	}
	return nil, false
}

// sameValue reports whether two decoded scalars are the same boolean or the same number.
// Integers compare exactly; a float equals an integer it rounds to, which over-matches beyond
// 2^53 rather than missing a spelling.
func sameValue(a, b any) bool {
	if x, ok := a.(bool); ok {
		y, ok := b.(bool)
		return ok && x == y
	}
	fa, ia, ua, oka := number(a)
	fb, ib, ub, okb := number(b)
	if !oka || !okb {
		return false
	}
	if fa != nil || fb != nil {
		return toFloat(fa, ia, ua) == toFloat(fb, ib, ub)
	}
	switch {
	case ia != nil && ib != nil:
		return *ia == *ib
	case ua != nil && ub != nil:
		return *ua == *ub
	case ia != nil:
		return *ia >= 0 && uint64(*ia) == *ub
	default:
		return *ib >= 0 && uint64(*ib) == *ua
	}
}

// number splits a decoded numeric scalar by kind; ok is false for anything else.
func number(v any) (f *float64, i *int64, u *uint64, ok bool) {
	switch x := v.(type) {
	case float64:
		return &x, nil, nil, true
	case int64:
		return nil, &x, nil, true
	case uint64:
		return nil, nil, &x, true
	}
	return nil, nil, nil, false
}

func toFloat(f *float64, i *int64, u *uint64) float64 {
	switch {
	case f != nil:
		return *f
	case i != nil:
		return float64(*i)
	}
	return float64(*u)
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
// A path string can parse with its tokens split differently from the keys that produced it (a
// key ending in |yaml), so the rendering is checked whole as well: if it, unescaped, still holds
// a value, only the document is named.
func redactPath(p Path, texts []string) string {
	q := Path{Doc: p.Doc, Format: p.Format, Pointer: slices.Clone(p.Pointer), Inner: slices.Clone(p.Inner)}
	for _, tokens := range [][]string{q.Pointer, q.Inner} {
		for i, tok := range tokens {
			if holdsAny(tok, texts) {
				tokens[i] = redacted
			}
		}
	}
	if s := q.String(); !renderingHolds(s, texts) {
		return s
	}
	if d := (Path{Doc: p.Doc}).String(); !renderingHolds(d, texts) {
		return d
	}
	return redacted
}

// renderingHolds reports whether a rendered path holds a value: as written, unescaped, or in any
// piece between separators.
func renderingHolds(s string, texts []string) bool {
	plain := strings.NewReplacer("~1", "/", "~0", "~").Replace(s)
	if containsAny(s, texts) || containsAny(plain, texts) {
		return true
	}
	for _, piece := range strings.FieldsFunc(plain, func(r rune) bool { return r == '/' || r == '|' }) {
		if holdsAny(piece, texts) {
			return true
		}
	}
	return false
}

// holdsAny reports whether s contains one of texts, or as a plain scalar decodes to the same
// number or boolean as one of them: the guard's equality (equalsAny) treats 0x4cb2f and
// 3.14159e5 as 314159 and TRUE as true, so a key spelled any of those ways discloses the value.
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

// sameScalar reports whether a and b can be the same number or boolean, as the guard compares
// them. A path token has lost the tag its key carried, so each text is read plain and under
// every explicit tag it decodes with (!!float 0x20000000000001 rounds to 2^53, which its plain
// reading does not): redaction then matches at least whatever the guard matched.
func sameScalar(a, b string) bool {
	for _, va := range readings(a) {
		for _, vb := range readings(b) {
			if sameValue(va, vb) {
				return true
			}
		}
	}
	return false
}

// readings is every decoded value s has as a plain scalar or under an explicit !!int, !!float
// or !!bool tag.
func readings(s string) []any {
	var out []any
	for _, tag := range []string{"", "!!int", "!!float", "!!bool"} {
		if v, ok := decodedScalar(&yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: s}); ok {
			out = append(out, v)
		}
	}
	return out
}
