package compile

import (
	"bytes"
	"errors"
	"io"
	"math/big"
	"regexp"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/ingest"
)

// formOnly reports whether two plaintext configurations hold the same documents, keys and values and
// differ only in form: comments, key order inside a map, indentation, quoting and flow style
// (operator-experience.md §2.4). It is the conservative subset of the typed comparison the research
// calls E3: a true answer implies equal content, and anything it cannot decide is false. Secrets are
// part of the content, so the texts are the resolved, plaintext ones and the answer is never shown
// with a value.
//
// Both texts must parse as multi-document YAML without an alias, an anchor, a key that is not a string, a repeated key or a tag
// outside YAML's core schema. They must hold the same documents in the same order, with
// sequences equal element by element and scalars of one tag and equal text, or numbers of equal decimal value (so "1" and 1, and
// true and "true", differ; the spellings of one null are equal; a bool or a timestamp spelled two ways differs).
func formOnly(a, b []byte) bool {
	da, err := parseDocuments(a)
	if err != nil {
		return false
	}
	db, err := parseDocuments(b)
	if err != nil || len(da) != len(db) {
		return false
	}
	for i := range da {
		if !sameNode(da[i], db[i]) {
			return false
		}
	}
	return true
}

// sameInFormBut is formOnly of the base's text and the composition without the one HostnameConfig the
// compiler filled for name, which an import removed from the base (compilation.md §2.3 step 2a). The
// document is set aside only when it is exactly what the fill writes: apiVersion v1alpha1, kind, auto off
// and hostname equal to name, as plain strings; a composition with another HostnameConfig, or with this
// one changed in any member, is not form-only. Every other document is compared as formOnly does.
func sameInFormBut(base ingest.Resolved, m Materialized, name string) bool {
	if base.Embedded() {
		return false
	}
	text, err := base.Input().Bytes()
	if err != nil {
		return false
	}
	da, err := parseDocuments(text)
	if err != nil {
		return false
	}
	db, err := parseDocuments(m.bytes())
	if err != nil {
		return false
	}
	at := -1
	for i, d := range db {
		if kindOf(d) != "HostnameConfig" {
			continue
		}
		if at >= 0 || !isFill(d, name) {
			return false
		}
		at = i
	}
	if at < 0 || len(da) != len(db)-1 {
		return false
	}
	db = slices.Delete(db, at, at+1)
	for i := range da {
		if !sameNode(da[i], db[i]) {
			return false
		}
	}
	return true
}

// kindOf is a document's kind when it is a mapping with a plain string kind, else empty.
func kindOf(d *yaml.Node) string {
	n := d
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == "kind" && n.Content[i+1].Kind == yaml.ScalarNode {
			return n.Content[i+1].Value
		}
	}
	return ""
}

// isFill reports whether d is exactly {apiVersion v1alpha1, kind HostnameConfig, auto off, hostname name},
// every value a plain string; comments and key order do not count.
func isFill(d *yaml.Node, name string) bool {
	n := d
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode || len(n.Content) != 8 {
		return false
	}
	want := map[string]string{"apiVersion": "v1alpha1", "kind": "HostnameConfig", "auto": "off", "hostname": name}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		w, ok := want[k.Value]
		if k.Kind != yaml.ScalarNode || v.Kind != yaml.ScalarNode || v.Tag != "!!str" || !ok || v.Value != w {
			return false
		}
		delete(want, k.Value)
	}
	return len(want) == 0
}

func parseDocuments(text []byte) ([]*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(text))
	var docs []*yaml.Node
	for {
		var n yaml.Node
		if err := dec.Decode(&n); err != nil {
			if errors.Is(err, io.EOF) {
				return docs, nil
			}
			return nil, err
		}
		docs = append(docs, &n)
	}
}

var coreTags = map[string]bool{
	"!!str": true, "!!int": true, "!!float": true, "!!bool": true, "!!null": true,
	"!!timestamp": true, "!!binary": true, "!!map": true, "!!seq": true,
}

// sameNode compares two nodes by content. A node the comparison does not handle is unequal to every
// node, itself included.
func sameNode(a, b *yaml.Node) bool {
	if a.Kind != b.Kind || a.Anchor != "" || b.Anchor != "" {
		return false
	}
	switch a.Kind {
	case yaml.DocumentNode:
		return len(a.Content) == len(b.Content) && allSame(a.Content, b.Content)
	case yaml.SequenceNode:
		return a.ShortTag() == b.ShortTag() && coreTags[a.ShortTag()] && len(a.Content) == len(b.Content) &&
			allSame(a.Content, b.Content)
	case yaml.MappingNode:
		return sameMapping(a, b)
	case yaml.ScalarNode:
		return sameScalar(a, b)
	}
	return false // an alias, or a kind this does not know
}

func allSame(a, b []*yaml.Node) bool {
	for i := range a {
		if !sameNode(a[i], b[i]) {
			return false
		}
	}
	return true
}

// sameMapping compares two mappings as sets of keys. A key that is not a string and a repeated key
// make the answer false.
func sameMapping(a, b *yaml.Node) bool {
	if a.ShortTag() != "!!map" || b.ShortTag() != "!!map" || len(a.Content) != len(b.Content) {
		return false
	}
	byKey, ok := keyed(a)
	if !ok {
		return false
	}
	other, ok := keyed(b)
	if !ok || len(byKey) != len(other) {
		return false
	}
	for k, v := range byKey {
		w, found := other[k]
		if !found || !sameNode(v, w) {
			return false
		}
	}
	return true
}

type mapKey struct{ tag, value string }

func keyed(m *yaml.Node) (map[mapKey]*yaml.Node, bool) {
	out := make(map[mapKey]*yaml.Node, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i]
		// Only a string key: two spellings of one number (16 and 0x10) would otherwise be two keys.
		if k.Kind != yaml.ScalarNode || k.Anchor != "" || k.ShortTag() != "!!str" {
			return nil, false
		}
		mk := mapKey{k.ShortTag(), k.Value}
		if _, dup := out[mk]; dup {
			return nil, false
		}
		out[mk] = m.Content[i+1]
	}
	return out, true
}

// plainDecimal is a decimal number with no leading zero before another digit.
var plainDecimal = regexp.MustCompile(`^[+-]?(0|[1-9][0-9]*)?(\.[0-9]*)?([eE][+-]?[0-9]+)?$`)

// sameScalar compares two scalars: a string by its text, a null by its tag alone, a number by its exact
// decimal value, any other type by its text.
func sameScalar(a, b *yaml.Node) bool {
	ta, tb := a.ShortTag(), b.ShortTag()
	if ta != tb || !coreTags[ta] {
		return false
	}
	switch ta {
	case "!!null":
		return true
	case "!!str":
		return a.Value == b.Value
	}
	if a.Value == b.Value {
		return true
	}
	// Different text is proved equal for numbers only, as exact decimals of plain decimal syntax: a float64 would
	// read two long spellings of different numbers as equal, and a leading zero, a hex or an underscore reads
	// differently in different decoders. A bool, a timestamp or a binary with different text reads different.
	if ta != "!!int" && ta != "!!float" {
		return false
	}
	if !plainDecimal.MatchString(a.Value) || !plainDecimal.MatchString(b.Value) {
		return false
	}
	x, okx := new(big.Rat).SetString(a.Value)
	y, oky := new(big.Rat).SetString(b.Value)
	return okx && oky && x.Cmp(y) == 0
}
