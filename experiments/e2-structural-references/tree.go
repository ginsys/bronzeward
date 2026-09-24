package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// readDocs reads every document of a multi-document YAML file, as nodes, so that tags, anchors and
// aliases survive.
func readDocs(path string) ([]*yaml.Node, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseDocs(b)
}

func parseDocs(b []byte) ([]*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var docs []*yaml.Node
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			return docs, nil
		}
		if err != nil {
			return nil, err
		}
		docs = append(docs, &n)
	}
}

func encodeDocs(docs []*yaml.Node) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(4)
	for _, d := range docs {
		if err := enc.Encode(d); err != nil {
			return nil, err
		}
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeDocs(path string, docs []*yaml.Node) error {
	b, err := encodeDocs(docs)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// clone deep-copies a node. Aliases keep pointing at their original anchors, which is only used on
// value nodes from a case's values, which hold none.
func clone(n *yaml.Node) *yaml.Node {
	c := *n
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, k := range n.Content {
		c.Content[i] = clone(k)
	}
	return &c
}

// replace puts a copy of v where n is, keeping n's anchor so that aliases to n see the new value.
func replace(n, v *yaml.Node) {
	anchor := n.Anchor
	*n = *clone(v)
	n.Anchor = anchor
	n.Line, n.Column = 0, 0
}

// An occurrence is one place a reference (or a sentinel) is found: the node, the document it is
// in, its full path (through embedded documents) and, when it is a mapping value, the mapping
// holding it and the node actually in that mapping (the alias node, for an occurrence reached
// through an alias).
type occurrence struct {
	ref    string
	doc    string
	path   Path
	node   *yaml.Node
	parent *yaml.Node
	holder *yaml.Node
	alias  bool
}

// match reports whether a node is a reference, and to what.
type match func(n *yaml.Node) (string, bool)

// walker finds occurrences in a tree. Embedded string values declared for the case are parsed and
// walked too, when descend allows it; hosts records them so that the caller can write them back.
type walker struct {
	c       *Case
	match   match
	descend func(e Embedded) bool
	occ     []occurrence
	hosts   []host
}

// A host is an embedded document found in a string value: the string node, its parsed tree, and
// its declaration.
type host struct {
	node *yaml.Node
	tree *yaml.Node
	decl Embedded
	doc  string
	path Path
}

func (w *walker) walk(n *yaml.Node, doc string, p Path, parent, holder *yaml.Node, alias bool) error {
	if n.Kind == yaml.AliasNode {
		return w.walk(n.Alias, doc, p, parent, n, true)
	}
	if ref, ok := w.match(n); ok {
		w.occ = append(w.occ, occurrence{ref: ref, doc: doc, path: p, node: n, parent: parent, holder: holder, alias: alias})
		return nil
	}
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			if err := w.walk(c, doc, p, nil, c, false); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			v := n.Content[i+1]
			if err := w.walk(v, doc, p.with(Step{Key: n.Content[i].Value}), n, v, false); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			if err := w.walk(item, doc, p.with(elementStep(item, i)), n, item, false); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		if alias {
			return nil // the anchor's own visit handles an embedded document once
		}
		decl, ok := w.c.embeddedAt(doc, p)
		if !ok || !w.descend(decl) {
			return nil
		}
		tree, err := parseEmbedded(n.Value)
		if err != nil {
			return fmt.Errorf("%s %s: embedded %s document does not parse: %w", doc, p, decl.Format, err)
		}
		w.hosts = append(w.hosts, host{node: n, tree: tree, decl: decl, doc: doc, path: p})
		return w.walk(tree, doc, p.with(Step{Embedded: decl.Format}), nil, tree, false)
	}
	return nil
}

// walkDocs walks every document, naming each by docID.
func (w *walker) walkDocs(docs []*yaml.Node) error {
	for _, d := range docs {
		if err := w.walk(d, docID(d), nil, nil, d, false); err != nil {
			return err
		}
	}
	return nil
}

// writeBack re-serializes every embedded document, innermost last found first, into its host
// string.
func (w *walker) writeBack() error {
	for i := len(w.hosts) - 1; i >= 0; i-- {
		h := w.hosts[i]
		s, err := encodeEmbedded(h.tree, h.decl.Format)
		if err != nil {
			return fmt.Errorf("%s %s: %w", h.doc, h.path, err)
		}
		h.node.Value = s
		h.node.Style = yaml.LiteralStyle
		h.node.Tag = "!!str"
	}
	return nil
}

func parseEmbedded(s string) (*yaml.Node, error) {
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(s), &n); err != nil {
		return nil, err
	}
	return &n, nil
}

// encodeEmbedded writes an embedded document back: YAML with two-space indentation, or JSON with
// sorted keys. A JSON document still carrying a YAML tag (the tag candidate, unresolved) cannot be
// JSON; it is written as YAML flow text, which is what an author using tags there would store.
func encodeEmbedded(tree *yaml.Node, format string) (string, error) {
	if format == "json" && !hasCustomTag(tree) {
		var v any
		if err := tree.Decode(&v); err != nil {
			return "", err
		}
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(b) + "\n", nil
	}
	if format == "json" {
		setFlow(tree)
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(tree); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return b.String(), nil
}

func hasCustomTag(n *yaml.Node) bool {
	if n.Tag != "" && !strings.HasPrefix(n.Tag, "!!") {
		return true
	}
	for _, c := range n.Content {
		if hasCustomTag(c) {
			return true
		}
	}
	return false
}

func setFlow(n *yaml.Node) {
	if n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode {
		n.Style = yaml.FlowStyle
	}
	for _, c := range n.Content {
		setFlow(c)
	}
}
