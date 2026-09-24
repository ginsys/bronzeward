package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// A Case is one directory under cases/: case.yaml plus the fragments it names. Fragments are
// written once, in a canonical form where every reference is a `!ref <name>` tag; Generate derives
// each candidate's form (and the reference-free form) from it, so that every candidate is tried on
// exactly the same intent.
type Case struct {
	Description string                       `yaml:"description"`
	Fragments   []Fragment                   `yaml:"fragments"`
	Embedded    []Embedded                   `yaml:"embedded"`
	Values      map[string]yaml.Node         `yaml:"values"`
	Native      string                       `yaml:"native"`
	Expect      map[string]map[string]string `yaml:"expect"`
	dir         string
}

// A Fragment is one ordered patch. Marked opts it in to the marked-string candidate: only an
// opted-in fragment's `bwref:` strings are references when it is resolved on its own.
type Fragment struct {
	File   string `yaml:"file"`
	Marked bool   `yaml:"marked"`
}

// Embedded declares a string value that holds a YAML or JSON document. Identified says whether the
// resolver may parse it; an unidentified one is opaque text to every candidate (design §6.9), and
// only the reference-free form resolves inside it, as the author's intent.
type Embedded struct {
	Doc        string `yaml:"doc"`
	Path       string `yaml:"path"`
	Format     string `yaml:"format"`
	Identified bool   `yaml:"identified"`
}

var candidates = []string{"tag", "marked", "binding"}

const (
	canonicalTag = "!ref"
	candidateTag = "!bwref"
	marker       = "bwref:"
)

func LoadCase(dir string) (*Case, error) {
	b, err := os.ReadFile(filepath.Join(dir, "case.yaml"))
	if err != nil {
		return nil, err
	}
	var c Case
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s/case.yaml: %w", dir, err)
	}
	c.dir = dir
	if len(c.Fragments) == 0 {
		return nil, fmt.Errorf("%s/case.yaml: no fragments", dir)
	}
	for _, e := range c.Embedded {
		if e.Format != "yaml" && e.Format != "json" {
			return nil, fmt.Errorf("%s/case.yaml: embedded %s: format %q", dir, e.Path, e.Format)
		}
		if _, err := ParsePath(e.Path); err != nil {
			return nil, fmt.Errorf("%s/case.yaml: %w", dir, err)
		}
	}
	return &c, nil
}

func (c *Case) embeddedAt(doc string, p Path) (Embedded, bool) {
	s := p.String()
	for _, e := range c.Embedded {
		if e.Doc == doc && e.Path == s {
			return e, true
		}
	}
	return Embedded{}, false
}

func (c *Case) fragment(file string) (Fragment, error) {
	for _, f := range c.Fragments {
		if f.File == file {
			return f, nil
		}
	}
	return Fragment{}, fmt.Errorf("case has no fragment %q", file)
}

func (c *Case) value(ref string) (*yaml.Node, error) {
	v, ok := c.Values[ref]
	if !ok {
		return nil, fmt.Errorf("unknown reference %q", ref)
	}
	return &v, nil
}

func matchTag(tag string) match {
	return func(n *yaml.Node) (string, bool) {
		if n.Kind == yaml.ScalarNode && n.Tag == tag {
			return n.Value, true
		}
		return "", false
	}
}

func matchMarker(n *yaml.Node) (string, bool) {
	if n.Kind == yaml.ScalarNode && n.ShortTag() == "!!str" && strings.HasPrefix(n.Value, marker) {
		return strings.TrimPrefix(n.Value, marker), true
	}
	return "", false
}

// A Binding is the external-path-binding candidate's record: which fragment, document and path a
// reference belongs at. The fragment itself carries nothing there.
type Binding struct {
	Fragment string
	Doc      string
	Path     Path
	Ref      string
}

// An Occurrence is one reference in the canonical fragments: the provenance superset.
type Occurrence struct {
	Fragment string
	Ref      string
	Doc      string
	Path     Path
}

// Generate writes one fragment set: `literal` (every reference replaced by its value; the native,
// reference-free input), `tag`, `marked` or `binding`. It returns the fragments in order, the
// bindings (binding only) and every canonical reference occurrence.
func Generate(c *Case, form string) ([][]*yaml.Node, []Binding, []Occurrence, error) {
	var out [][]*yaml.Node
	var bindings []Binding
	var occs []Occurrence
	for _, f := range c.Fragments {
		docs, err := readDocs(filepath.Join(c.dir, f.File))
		if err != nil {
			return nil, nil, nil, err
		}
		// Every form descends into every declared embedded document: an author writes the
		// candidate's syntax there too. Whether a resolver may look inside is decided at resolution.
		w := &walker{c: c, match: matchTag(canonicalTag), descend: func(Embedded) bool { return true }}
		if err := w.walkDocs(docs); err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", f.File, err)
		}
		seen := map[*yaml.Node]bool{}
		var remove []occurrence
		for _, o := range w.occ {
			occs = append(occs, Occurrence{Fragment: f.File, Ref: o.ref, Doc: o.doc, Path: o.path})
			if form == "binding" {
				if o.parent == nil || o.parent.Kind != yaml.MappingNode {
					return nil, nil, nil, fmt.Errorf("%s %s: a binding needs a mapping key; this reference is held by a sequence", f.File, o.path)
				}
				bindings = append(bindings, Binding{Fragment: f.File, Doc: o.doc, Path: o.path, Ref: o.ref})
				remove = append(remove, o)
				continue
			}
			if seen[o.node] {
				continue
			}
			seen[o.node] = true
			switch form {
			case "literal":
				v, err := c.value(o.ref)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("%s %s: %w", f.File, o.path, err)
				}
				replace(o.node, v)
			case "tag":
				o.node.Tag = candidateTag
			case "marked":
				o.node.Tag = "!!str"
				o.node.Value = marker + o.ref
				o.node.Style = yaml.DoubleQuotedStyle
			default:
				return nil, nil, nil, fmt.Errorf("unknown form %q", form)
			}
		}
		emptied := map[*yaml.Node]bool{}
		for _, o := range remove {
			deleteValue(o.parent, o.holder)
			if len(o.parent.Content) == 0 {
				emptied[o.parent] = true
			}
		}
		for _, h := range w.hosts {
			pruneEmptied(h.tree, emptied)
		}
		for _, d := range docs {
			pruneEmptied(d, emptied)
		}
		if err := w.writeBack(); err != nil {
			return nil, nil, nil, err
		}
		out = append(out, docs)
	}
	return out, bindings, occs, nil
}

// deleteValue removes the key whose value node is v from mapping m.
func deleteValue(m, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i+1] == v {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// pruneEmptied removes mapping keys whose value was emptied by deleting bound keys, bottom-up, so
// the binding form holds no mapping that only its references filled. A mapping that was empty in the
// canonical fragment stays. It reports whether n itself is now such an emptied mapping.
func pruneEmptied(n *yaml.Node, emptied map[*yaml.Node]bool) bool {
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			pruneEmptied(c, emptied)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); {
			if pruneEmptied(n.Content[i+1], emptied) {
				n.Content = append(n.Content[:i], n.Content[i+2:]...)
				if len(n.Content) == 0 {
					emptied[n] = true
				}
				continue
			}
			i += 2
		}
		return len(n.Content) == 0 && emptied[n]
	}
	return false
}
