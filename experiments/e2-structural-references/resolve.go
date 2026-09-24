package main

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// A Resolution is one place a reference was replaced by its value: the sensitive-path provenance
// the resolver records.
type Resolution struct {
	Ref  string
	Doc  string
	Path Path
}

// Resolve replaces a candidate's references in docs by their values and returns where it did.
//
// With fragment set, docs is that one fragment (early materialization, before composition): the
// marked candidate honours the fragment's opt-in and the binding candidate applies that fragment's
// bindings only. With fragment empty, docs is the composed configuration (late resolution): no
// fragment's opt-in survives composition, so every `bwref:` string counts, and every binding is
// applied, in fragment order. Only identified embedded documents are parsed.
func Resolve(c *Case, candidate, fragment string, docs []*yaml.Node, bindings []Binding, values map[string]*yaml.Node) ([]Resolution, error) {
	lookup := func(ref string) (*yaml.Node, error) {
		v, ok := values[ref]
		if !ok {
			return nil, fmt.Errorf("unknown reference %q", ref)
		}
		return v, nil
	}
	switch candidate {
	case "tag", "marked":
		m := matchTag(candidateTag)
		if candidate == "marked" {
			m = matchMarker
			if fragment != "" {
				f, err := c.fragment(fragment)
				if err != nil {
					return nil, err
				}
				if !f.Marked {
					return nil, nil
				}
			}
		}
		w := &walker{c: c, match: m, descend: func(e Embedded) bool { return e.Identified }}
		if err := w.walkDocs(docs); err != nil {
			return nil, err
		}
		var res []Resolution
		done := map[*yaml.Node]bool{}
		for _, o := range w.occ {
			res = append(res, Resolution{Ref: o.ref, Doc: o.doc, Path: o.path})
			if done[o.node] {
				continue
			}
			done[o.node] = true
			v, err := lookup(o.ref)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", o.doc, o.path, err)
			}
			replace(o.node, v)
		}
		return res, w.writeBack()
	case "binding":
		var res []Resolution
		for _, b := range bindings {
			if fragment != "" && b.Fragment != fragment {
				continue
			}
			v, err := lookup(b.Ref)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", b.Doc, b.Path, err)
			}
			d := findDoc(docs, b.Doc)
			if d == nil {
				return nil, fmt.Errorf("binding %s %s: no such document", b.Doc, b.Path)
			}
			if err := setAt(c, b.Doc, d.Content[0], nil, b.Path, v); err != nil {
				return nil, fmt.Errorf("binding %s %s: %w", b.Doc, b.Path, err)
			}
			res = append(res, Resolution{Ref: b.Ref, Doc: b.Doc, Path: b.Path})
		}
		return res, nil
	}
	return nil, fmt.Errorf("unknown candidate %q", candidate)
}

func findDoc(docs []*yaml.Node, id string) *yaml.Node {
	for _, d := range docs {
		if docID(d) == id && len(d.Content) > 0 {
			return d
		}
	}
	return nil
}

// setAt puts v at the rest of the path under n (at is the path walked so far, for embedded
// declarations). Missing mapping keys are created; a sequence element must exist; an embedded step
// needs an identified declaration and re-serializes the string it parsed.
func setAt(c *Case, doc string, n *yaml.Node, at, rest Path, v *yaml.Node) error {
	s := rest[0]
	last := len(rest) == 1
	switch {
	case s.Embedded != "":
		decl, ok := c.embeddedAt(doc, at)
		if !ok || !decl.Identified {
			return fmt.Errorf("%s is not an identified embedded document", at)
		}
		if n.Kind != yaml.ScalarNode {
			return fmt.Errorf("%s holds no string to parse", at)
		}
		tree, err := parseEmbedded(n.Value)
		if err != nil {
			return fmt.Errorf("%s: %w", at, err)
		}
		if last {
			return fmt.Errorf("%s: a binding must name a path inside the embedded document", at)
		}
		if err := setAt(c, doc, tree.Content[0], at.with(s), rest[1:], v); err != nil {
			return err
		}
		out, err := encodeEmbedded(tree, decl.Format)
		if err != nil {
			return err
		}
		n.Value, n.Style, n.Tag = out, yaml.LiteralStyle, "!!str"
		return nil
	case s.SelKey != "" || s.IsIndex:
		if n.Kind != yaml.SequenceNode {
			return fmt.Errorf("%s is not a sequence", at)
		}
		for i, item := range n.Content {
			if e := elementStep(item, i); e == s || (s.IsIndex && i == s.Index) {
				if last {
					replace(item, v)
					return nil
				}
				return setAt(c, doc, item, at.with(s), rest[1:], v)
			}
		}
		return fmt.Errorf("%s has no element %s", at, Path{s})
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("%s is not a mapping", at)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == s.Key {
			if last {
				replace(n.Content[i+1], v)
				return nil
			}
			return setAt(c, doc, n.Content[i+1], at.with(s), rest[1:], v)
		}
	}
	// A mapping that was written empty (`{}`) is flow style; once it holds keys it is written as
	// the literal form would be.
	n.Style &^= yaml.FlowStyle
	child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if last {
		child = clone(v)
	}
	n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s.Key}, child)
	if last {
		return nil
	}
	return setAt(c, doc, child, at.with(s), rest[1:], v)
}

// Sentinels gives every reference a value of the same kind that can be recognised after
// composition: strings become `E2SENTINEL` plus six digits (sixteen characters, so also valid
// base64 for a bytes field), integers 61000 plus the reference's index, mappings the same per leaf.
// A boolean cannot be told apart from a literal and is reported instead.
func Sentinels(c *Case) (map[string]*yaml.Node, map[string]string) {
	refs := make([]string, 0, len(c.Values))
	for r := range c.Values {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	out := map[string]*yaml.Node{}
	unidentifiable := map[string]string{}
	for i, r := range refs {
		v := c.Values[r]
		n := clone(&v)
		leaf := 0
		ok := sentinelize(n, i, &leaf)
		if !ok {
			unidentifiable[r] = "a boolean value cannot be told apart from a literal"
		}
		out[r] = n
	}
	return out, unidentifiable
}

func sentinelize(n *yaml.Node, i int, leaf *int) bool {
	switch n.Kind {
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!str":
			n.Value = fmt.Sprintf("E2SENTINEL%03d%03d", i, *leaf)
		case "!!int":
			n.Value = fmt.Sprintf("%d", 61000+i*10+*leaf)
		default:
			return false
		}
		*leaf++
		return true
	}
	ok := true
	for j, k := range n.Content {
		if n.Kind == yaml.MappingNode && j%2 == 0 {
			continue // keys stay
		}
		ok = sentinelize(k, i, leaf) && ok
	}
	return ok
}

// FindSentinels lists where each reference's sentinel ended up in a composed configuration.
func FindSentinels(c *Case, docs []*yaml.Node, sentinels map[string]*yaml.Node) ([]Resolution, error) {
	byValue := map[string]string{}
	for r, n := range sentinels {
		collectLeaves(n, func(s *yaml.Node) {
			if strings.HasPrefix(s.Value, "E2SENTINEL") || s.ShortTag() == "!!int" {
				byValue[s.ShortTag()+" "+s.Value] = r
			}
		})
	}
	m := func(n *yaml.Node) (string, bool) {
		if n.Kind != yaml.ScalarNode {
			return "", false
		}
		r, ok := byValue[n.ShortTag()+" "+n.Value]
		return r, ok
	}
	w := &walker{c: c, match: m, descend: func(e Embedded) bool { return e.Identified }}
	if err := w.walkDocs(docs); err != nil {
		return nil, err
	}
	var res []Resolution
	for _, o := range w.occ {
		res = append(res, Resolution{Ref: o.ref, Doc: o.doc, Path: o.path})
	}
	return res, nil
}

func collectLeaves(n *yaml.Node, f func(*yaml.Node)) {
	if n.Kind == yaml.ScalarNode {
		f(n)
		return
	}
	for j, k := range n.Content {
		if n.Kind == yaml.MappingNode && j%2 == 0 {
			continue
		}
		collectLeaves(k, f)
	}
}
