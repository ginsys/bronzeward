package ingest

import (
	"slices"
	"strconv"

	"go.yaml.in/yaml/v3"
)

// visit is called for every node of a stream, keys included. key is true for a mapping key, whose
// path is its mapping's; parent is the node holding it (nil for a document's top node). An alias
// node is visited, not followed: its anchored node is visited where it stands.
type visit func(n *yaml.Node, p Path, key bool, parent *yaml.Node) error

// walkStream visits every node of every document.
func walkStream(docs []*yaml.Node, fn visit) error {
	for i, d := range docs {
		top := root(d)
		if top == nil {
			continue
		}
		if err := walkNode(top, Path{Doc: i}, false, nil, fn); err != nil {
			return err
		}
	}
	return nil
}

// walkEmbedded visits the nodes of an identified embedded document, whose own path is outer.
func walkEmbedded(doc *yaml.Node, outer Path, format string, fn visit) error {
	top := root(doc)
	if top == nil {
		return nil
	}
	return walkNode(top, Path{Doc: outer.Doc, Pointer: outer.Pointer, Format: format}, false, nil, fn)
}

func walkNode(n *yaml.Node, p Path, key bool, parent *yaml.Node, fn visit) error {
	if err := fn(n, p, key, parent); err != nil {
		return err
	}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if err := walkNode(k, p, true, n, fn); err != nil {
				return err
			}
			if err := walkNode(v, p.child(keyToken(k)), false, n, fn); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			if err := walkNode(c, p.child(strconv.Itoa(i)), false, n, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// keyToken is the pointer token of a mapping key; a key that is not a scalar has no pointer
// token, and its value is named by a placeholder no pointer can produce.
func keyToken(k *yaml.Node) string {
	if k = deref(k); k != nil && k.Kind == yaml.ScalarNode {
		return k.Value
	}
	return "<complex key>"
}

// child is p extended by one token, inside the embedded document when p is in one.
func (p Path) child(tok string) Path {
	if p.Format != "" {
		p.Inner = append(slices.Clone(p.Inner), tok)
		return p
	}
	p.Pointer = append(slices.Clone(p.Pointer), tok)
	return p
}
