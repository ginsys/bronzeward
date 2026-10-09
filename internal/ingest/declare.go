package ingest

import (
	"cmp"
	"maps"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/provider"
)

// Declarations is what a fragment revision carries beside its YAML (compilation.md §5.2): a
// declaration for every name it references, and the embedded documents it identifies.
type Declarations struct {
	References map[string]Reference `json:"references,omitempty" yaml:"references,omitempty"`
	Embedded   []Embedded           `json:"embedded,omitempty" yaml:"embedded,omitempty"`
}

// Reference declares one logical secret name: its stored kind, the exact version it resolves to,
// and an optional placement encoding.
type Reference struct {
	Kind     provider.Kind `json:"kind" yaml:"kind"`
	Version  int64         `json:"version" yaml:"version"`
	Encoding string        `json:"encoding,omitempty" yaml:"encoding,omitempty"`
}

// Embedded identifies an embedded document: the path of the string scalar that holds it and its
// format, yaml or json.
type Embedded struct {
	Path   string `json:"path" yaml:"path"`
	Format string `json:"format" yaml:"format"`
}

func (d Declarations) clone() Declarations {
	return Declarations{References: maps.Clone(d.References), Embedded: slices.Clone(d.Embedded)}
}

const refTag = "!bwref"

// coreTags are the YAML core schema tags; with refTag, the only tags a fragment may carry
// (compilation.md §5.3: no other local tag; its behaviour under typed decoding is unknown).
var coreTags = map[string]bool{
	"!!str": true, "!!int": true, "!!bool": true, "!!float": true, "!!null": true, "!!map": true,
	"!!seq": true, "!!binary": true, "!!timestamp": true, "!!merge": true,
}

var nameSyntax = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(/[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// validName is the name grammar of §5.1.
func validName(s string) bool { return nameSyntax.MatchString(s) }

// ValidReference reports whether s is a logical name by §5.1's grammar.
func ValidReference(s string) bool { return validName(s) }

// checkDeclarations holds each declaration to §5.2 and returns the identified embedded documents
// by the path of the scalar that holds them.
func checkDeclarations(d Declarations) (map[string]string, error) {
	for name, r := range d.References {
		if !validName(name) {
			return nil, refuse(RuleBadName)
		}
		switch r.Kind {
		case provider.KindString, provider.KindInteger, provider.KindBoolean, provider.KindMapping:
		default:
			return nil, refuse(RuleBadDeclaration, name)
		}
		if r.Version < 1 || r.Encoding != "" && (r.Encoding != "base64" || r.Kind != provider.KindString) {
			return nil, refuse(RuleBadDeclaration, name)
		}
	}
	embedded := map[string]string{}
	for _, e := range d.Embedded {
		p, err := ParsePath(e.Path)
		if err != nil {
			return nil, err
		}
		if p.Format != "" || e.Format != "yaml" && e.Format != "json" {
			return nil, refuse(RuleBadDeclaration, e.Path)
		}
		key := p.String()
		if _, dup := embedded[key]; dup {
			return nil, refuse(RuleBadDeclaration, key)
		}
		embedded[key] = e.Format
	}
	return embedded, nil
}

// validate checks a stream and its declarations as authored (compilation.md §5, §13 authoring
// row): declarations well formed; identified embedded documents are strings that parse; every
// tag is !bwref or a core tag; !bwref stands only on a scalar that is a mapping value or a list
// element, names a declared name, and every declared name is used; no other string holds the
// reserved text. Identified embedded documents are checked as parsed documents, not as text.
func validate(docs []*yaml.Node, d Declarations) error { return validateRefs(docs, d, nil) }

// validateRefs is validate. With misplaced set, a reference at a place refused goes on into
// misplaced by name instead of ending the walk, and the first such refusal is returned once
// the walk is done: the refusal of a substituted stream is that of every mark whose reference
// substitution put out of place, which a path cannot tell (an alias key is refused at its
// mapping's path). An identified document left unvalidated is then refused at every such
// holder's path.
func validateRefs(docs []*yaml.Node, d Declarations, misplaced map[string]bool) error {
	embedded, err := checkDeclarations(d)
	if err != nil {
		return err
	}
	used := map[string]bool{}
	var first error
	place := func(name string, p Path) error {
		r := refuse(RuleTagPlacement, p.String())
		if misplaced == nil {
			return r
		}
		misplaced[name] = true
		if first == nil {
			first = r
		}
		return nil
	}
	var check visit
	check = func(n *yaml.Node, p Path, key bool, parent *yaml.Node) error {
		if n.Kind == yaml.AliasNode {
			// The anchored node was checked where it stands; an alias puts it in another
			// position, which must be allowed too.
			if t := deref(n); key && t != nil && t.Tag == refTag {
				return place(t.Value, p)
			}
			return nil
		}
		if n.Tag == refTag {
			// Only a mapping value or a list element: never a key, a collection, or a whole
			// document (which has no parent).
			if key || n.Kind != yaml.ScalarNode || parent == nil {
				return place(n.Value, p)
			}
			if !validName(n.Value) {
				return refuse(RuleBadName, p.String())
			}
			if _, ok := d.References[n.Value]; !ok {
				return refuse(RuleUndeclaredName, p.String())
			}
			used[n.Value] = true
			return nil
		}
		if n.Tag != "" && !coreTags[n.Tag] {
			return refuse(RuleLocalTag, p.String())
		}
		if n.Kind != yaml.ScalarNode {
			return nil
		}
		if format, ok := embedded[p.String()]; ok && !key && p.Format == "" {
			inner, err := embeddedDocument(n)
			if err != nil {
				return refuse(RuleEmbedded, p.String())
			}
			delete(embedded, p.String())
			return walkEmbedded(inner, p, format, check)
		}
		if strings.Contains(n.Value, refTag) {
			return refuse(RuleReservedText, p.String())
		}
		return nil
	}
	// A misplaced reference was met before any refusal that ended the walk.
	if err := walkStream(docs, check); first != nil || err != nil {
		return cmp.Or(first, err)
	}
	if len(embedded) > 0 {
		// Every identified document left unvalidated is named when attributing (a mark on its
		// holder put a reference there), the first in path order otherwise.
		left := slices.Sorted(maps.Keys(embedded))
		if misplaced == nil {
			left = left[:1]
		}
		return refuse(RuleEmbedded, left...)
	}
	for name := range d.References {
		if !used[name] {
			return refuse(RuleUnusedName, name)
		}
	}
	return nil
}

// embeddedDocument parses an identified embedded document's text: one document, in YAML (an
// identified JSON document is authored as YAML, compilation.md §5.4).
func embeddedDocument(n *yaml.Node) (*yaml.Node, error) {
	if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
		return nil, refuse(RuleEmbedded)
	}
	docs, err := parseStream([]byte(n.Value))
	if err != nil || len(docs) != 1 {
		return nil, refuse(RuleEmbedded)
	}
	return docs[0], nil
}
