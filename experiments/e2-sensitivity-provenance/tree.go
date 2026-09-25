package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Embedded declares a string value that holds a YAML or JSON document, as issue 3's case schema
// does. Only an identified one is parsed; an unidentified one is one opaque leaf.
type Embedded struct {
	Doc        string `yaml:"doc"`
	Path       string `yaml:"path"`
	Format     string `yaml:"format"`
	Identified bool   `yaml:"identified"`
}

// A Leaf is one scalar of a machine configuration, with two names:
//
//	Path  the display path, in issue 3's path syntax (`key/key`, `[field=value]`, `[i]`, `|yaml`),
//	      where a selector two elements of one sequence share gets the element's index (`#i`);
//	Pos   the positional key, document plus a path that names every sequence element by index,
//	      which is what the tracer pass attributes and redaction targets.
type Leaf struct {
	Doc  string
	Path string
	Pos  string
	Node *yaml.Node
}

// A Tree is a parsed multi-document configuration and its leaves in document order.
type Tree struct {
	Docs   []*yaml.Node
	Leaves []Leaf
	hosts  []host
}

type host struct {
	node   *yaml.Node
	tree   *yaml.Node
	format string
}

// identifying are the fields that name a sequence element, as in issue 3.
var identifying = []string{"name", "interface", "path"}

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

// encodeDocs writes documents as talosctl does: yaml.v3 with four-space indentation.
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

// parseTree parses text and names every leaf, descending into identified embedded documents.
func parseTree(text []byte, emb []Embedded) (*Tree, error) {
	docs, err := parseDocs(text)
	if err != nil {
		return nil, err
	}
	t := &Tree{Docs: docs}
	for _, d := range docs {
		id := docID(d)
		if err := t.walk(d, id, emb, "", ""); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func (t *Tree) walk(n *yaml.Node, doc string, emb []Embedded, path, pos string) error {
	if n.Kind == yaml.AliasNode {
		return t.walk(n.Alias, doc, emb, path, pos)
	}
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			if err := t.walk(c, doc, emb, path, pos); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i].Value
			if err := t.walk(n.Content[i+1], doc, emb, join(path, k), join(pos, k)); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		steps := make([]string, len(n.Content))
		count := map[string]int{}
		for i, item := range n.Content {
			steps[i] = elementStep(item, i)
			count[steps[i]]++
		}
		for i, item := range n.Content {
			s := steps[i]
			if count[s] > 1 {
				s = strings.TrimSuffix(s, "]") + "#" + strconv.Itoa(i) + "]"
			}
			if err := t.walk(item, doc, emb, path+s, pos+"["+strconv.Itoa(i)+"]"); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		if e, ok := embeddedAt(emb, doc, path); ok && e.Identified {
			var tree yaml.Node
			if err := yaml.Unmarshal([]byte(n.Value), &tree); err != nil {
				return fmt.Errorf("%s %s: embedded %s document does not parse: %w", doc, path, e.Format, err)
			}
			t.hosts = append(t.hosts, host{node: n, tree: &tree, format: e.Format})
			return t.walk(&tree, doc, emb, path+"|"+e.Format, pos+"|"+e.Format)
		}
		t.Leaves = append(t.Leaves, Leaf{Doc: doc, Path: path, Pos: doc + ":" + pos, Node: n})
	}
	return nil
}

func join(p, k string) string {
	if p == "" {
		return k
	}
	return p + "/" + k
}

// stripIndex removes the `#i` a shared selector carries, giving issue 3's path.
func stripIndex(p string) string {
	var b strings.Builder
	in := false
	skip := false
	for _, r := range p {
		switch {
		case r == '[':
			in, skip = true, false
		case r == ']':
			in, skip = false, false
		case in && r == '#':
			skip = true
			continue
		}
		if !skip {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func embeddedAt(emb []Embedded, doc, path string) (Embedded, bool) {
	p := stripIndex(path)
	for _, e := range emb {
		if e.Doc == doc && e.Path == p {
			return e, true
		}
	}
	return Embedded{}, false
}

// elementStep names a sequence element by its first identifying field, else by position.
func elementStep(item *yaml.Node, index int) string {
	if item.Kind == yaml.MappingNode {
		for _, f := range identifying {
			for i := 0; i+1 < len(item.Content); i += 2 {
				if item.Content[i].Value == f && item.Content[i+1].Kind == yaml.ScalarNode {
					return "[" + f + "=" + item.Content[i+1].Value + "]"
				}
			}
		}
	}
	return "[" + strconv.Itoa(index) + "]"
}

// docID names a document: `v1alpha1` for the legacy document, `<kind>/<name>` (or `<kind>`).
func docID(doc *yaml.Node) string {
	root := doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	var kind, name string
	if root.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(root.Content); i += 2 {
			switch root.Content[i].Value {
			case "kind":
				kind = root.Content[i+1].Value
			case "name":
				name = root.Content[i+1].Value
			}
		}
	}
	switch {
	case kind == "":
		return "v1alpha1"
	case name == "":
		return kind
	}
	return kind + "/" + name
}

// render parses text and writes it back with the leaf at each positional key in tokens replaced
// by that token. An embedded document is re-serialized only when a leaf inside it was replaced, so
// the text is otherwise what it was.
func render(text []byte, emb []Embedded, tokens map[string]string) ([]byte, error) {
	t, err := parseTree(text, emb)
	if err != nil {
		return nil, err
	}
	touched := map[*yaml.Node]bool{}
	for _, l := range t.Leaves {
		tok, ok := tokens[l.Pos]
		if !ok {
			continue
		}
		l.Node.Kind, l.Node.Tag, l.Node.Value, l.Node.Style = yaml.ScalarNode, "!!str", tok, 0
		touched[l.Node] = true
	}
	// Innermost hosts were appended after their parents; write them back first.
	for i := len(t.hosts) - 1; i >= 0; i-- {
		h := t.hosts[i]
		if !containsTouched(h.tree, touched) {
			continue
		}
		s, err := encodeEmbedded(h.tree, h.format)
		if err != nil {
			return nil, err
		}
		h.node.Value, h.node.Style, h.node.Tag = s, yaml.LiteralStyle, "!!str"
		touched[h.node] = true
	}
	return encodeDocs(t.Docs)
}

func containsTouched(n *yaml.Node, touched map[*yaml.Node]bool) bool {
	if touched[n] {
		return true
	}
	for _, c := range n.Content {
		if containsTouched(c, touched) {
			return true
		}
	}
	return false
}

// encodeEmbedded writes an embedded document back as issue 3's resolver does: YAML with two-space
// indentation, or JSON with sorted keys.
func encodeEmbedded(tree *yaml.Node, format string) (string, error) {
	if format == "json" {
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
