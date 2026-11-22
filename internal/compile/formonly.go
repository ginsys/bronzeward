package compile

import (
	"bytes"
	"errors"
	"io"
	"reflect"

	"go.yaml.in/yaml/v3"
)

// formOnly reports whether two plaintext configurations hold the same documents, keys and values and
// differ only in form: comments, key order inside a map, indentation, quoting and flow style
// (operator-experience.md §2.4). It is the conservative subset of the typed comparison the research
// calls E3: a true answer implies equal content, and anything it cannot decide is false. Secrets are
// part of the content, so the texts are the resolved, plaintext ones and the answer is never shown
// with a value.
//
// Both texts must parse as multi-document YAML without an alias, an anchor, a merge key, a tag outside
// YAML's core schema or a duplicate key. They must hold the same documents in the same order, with
// sequences equal element by element and scalars equal by their resolved tag and value (so "1" and 1,
// and true and "true", differ; the spellings of one null are equal).
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

// sameMapping compares two mappings as sets of keys. A key that is not a scalar, a merge key and a
// repeated key make the answer false.
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
		if k.Kind != yaml.ScalarNode || k.Anchor != "" || k.ShortTag() == "!!merge" || !coreTags[k.ShortTag()] {
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

// sameScalar compares the resolved value of two scalars: a string by its text, every other core type by
// the Go value YAML resolves it to, a null by its tag alone.
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
	var va, vb any
	if a.Decode(&va) != nil || b.Decode(&vb) != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
}
