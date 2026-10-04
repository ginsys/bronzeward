package ingest

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"
	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/provider"
)

// The rules of resolution and its hand-off to composition (compilation.md §6 steps 4-5). They are
// not reached through Extract, so they live here, outside refusal.go's sweep.
const (
	RuleKindMismatch Rule = "kind-mismatch" // a value's stored kind differs from its declaration
	RuleUnresolved   Rule = "unresolved"    // a declared name has no value to resolve to
	RuleJSONPatch    Rule = "json-patch"    // a fragment loads as a JSON6902 patch
)

// ErrPatchLoad is the machinery's rejection of a resolved fragment, such as a string in an
// integer field (compilation.md §7: a native rejection, not a compiler error). The machinery's
// message quotes the value, so it is never wrapped.
var ErrPatchLoad = errors.New("ingest: the machinery cannot load the resolved fragment")

// Resolved is a sanitized stream with every reference replaced by its value: plaintext. Like
// Unresolved, every fmt verb prints a placeholder, the marshallers fail and the text sits behind
// a pointer to a string. It leaves this package only as the machinery's own composition input or
// patch.
type Resolved struct{ s *string }

// Input is the resolved stream as the first composition input, the import base.
func (r Resolved) Input() configpatcher.Input {
	return configpatcher.WithBytes(slices.Clone(r.bytes()))
}

// Patch is the resolved stream loaded as talosctl loads a patch file. A JSON6902 patch is
// refused; only strategic merge patches compose (compilation.md §6 step 5).
func (r Resolved) Patch() (configpatcher.Patch, error) {
	p, err := configpatcher.LoadPatch(slices.Clone(r.bytes()))
	if err != nil {
		return nil, ErrPatchLoad
	}
	if _, ok := p.(jsonpatch.Patch); ok {
		return nil, refuse(RuleJSONPatch)
	}
	return p, nil
}

// Resolve replaces each reference of s by its value from values, keyed by name (compilation.md
// §6 step 4): a string (with its declared encoding), an integer, a boolean, or a mapping whose
// members are placed in key order. A tagged node is replaced in place, keeping its anchor, so
// every alias of it yields the same value. Each identified embedded document is resolved and
// written back whole (§5.4). Every reference resolves or nothing does; a refusal names paths
// only. The caller supplies exactly the pinned versions; Resolve selects none.
func Resolve(s Sanitized, values map[string]provider.Value) (Resolved, error) {
	return resolveWith(s, func(name string, r Reference, p Path) (*yaml.Node, error) {
		v, rule := placed(name, r, values)
		if rule != "" {
			return nil, refuse(rule, p.String())
		}
		return v, nil
	}, nil)
}

// resolveWith replaces each reference of s, in walk order, by the node value gives for its name,
// declaration and path, keeping its anchor, and writes each identified embedded document back,
// telling hosted (if set) the document's format and written text.
func resolveWith(s Sanitized, value func(name string, r Reference, p Path) (*yaml.Node, error), hosted func(format, text string)) (Resolved, error) {
	if err := s.Check(); err != nil {
		return Resolved{}, err
	}
	docs, err := parseStream(s.docs)
	if err != nil {
		return Resolved{}, errors.New("ingest: a sanitized stream does not parse")
	}
	embedded, err := checkDeclarations(s.decl)
	if err != nil {
		return Resolved{}, err
	}
	var place visit
	place = func(n *yaml.Node, p Path, key bool, _ *yaml.Node) error {
		if n.Kind == yaml.AliasNode {
			return nil
		}
		if n.Tag == refTag {
			v, err := value(n.Value, s.decl.References[n.Value], p)
			if err != nil {
				return err
			}
			// The first "|" of a path ends its outer pointer (§2.2): a member key holding one
			// in the outer stream would have no path.
			if p.Format == "" && v.Kind == yaml.MappingNode {
				for i := 0; i < len(v.Content); i += 2 {
					if strings.Contains(v.Content[i].Value, "|") {
						return refuse(RuleBadPath, p.String())
					}
				}
			}
			*n = yaml.Node{Kind: v.Kind, Tag: v.Tag, Value: v.Value, Content: v.Content, Anchor: n.Anchor}
			return nil
		}
		format, ok := embedded[p.String()]
		if !ok || key || p.Format != "" || n.Kind != yaml.ScalarNode {
			return nil
		}
		inner, err := embeddedDocument(n)
		if err != nil {
			return err
		}
		if err := walkEmbedded(inner, p, format, place); err != nil {
			return err
		}
		text, err := encodeEmbedded(inner, format)
		if err != nil {
			return refuse(RuleEmbedded, p.String())
		}
		n.Tag, n.Value, n.Style = "!!str", text, yaml.LiteralStyle
		if hosted != nil {
			hosted(format, text)
		}
		return nil
	}
	if err := walkStream(docs, place); err != nil {
		return Resolved{}, err
	}
	out, err := encodeStream(docs)
	if err != nil {
		return Resolved{}, err
	}
	text := string(out)
	return Resolved{s: &text}, nil
}

// placed is the node a declared name resolves to, or the rule that refuses it.
func placed(name string, r Reference, values map[string]provider.Value) (*yaml.Node, Rule) {
	v, ok := values[name]
	if !ok || v.Kind() == "" {
		return nil, RuleUnresolved
	}
	if v.Kind() != r.Kind {
		return nil, RuleKindMismatch
	}
	x, err := v.Decode()
	if err != nil {
		return nil, RuleUnresolved
	}
	if r.Kind != provider.KindMapping {
		n := scalarNode(x)
		if r.Encoding == "base64" {
			n.Value = base64.StdEncoding.EncodeToString([]byte(n.Value))
		}
		return n, ""
	}
	m := x.(map[string]any)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, k := range keys {
		out.Content = append(out.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, scalarNode(m[k]))
	}
	return out, ""
}

// scalarNode is a decoded string, json.Number or bool as a typed scalar.
func scalarNode(x any) *yaml.Node {
	switch v := x.(type) {
	case json.Number:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: v.String()}
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(v)}
	default:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v.(string)}
	}
}

// encodeEmbedded writes a resolved embedded document back (compilation.md §5.4, SR's prototype
// rules): JSON compact with sorted keys and a final newline, or YAML with two-space indentation.
func encodeEmbedded(doc *yaml.Node, format string) (string, error) {
	if format == "json" {
		var v any
		if err := doc.Decode(&v); err != nil {
			return "", err
		}
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(b) + "\n", nil
	}
	b, err := encodeStream([]*yaml.Node{doc})
	return string(b), err
}

// bytes is a copy of the resolved stream, for this package only.
func (r Resolved) bytes() []byte {
	if r.s == nil {
		return nil
	}
	return []byte(*r.s)
}

const resolvedPlaceholder = "[resolved configuration]"

var errResolvedRender = errors.New("ingest: a resolved configuration is not marshalled")

func (Resolved) String() string               { return resolvedPlaceholder }
func (Resolved) GoString() string             { return resolvedPlaceholder }
func (Resolved) Format(f fmt.State, _ rune)   { io.WriteString(f, resolvedPlaceholder) }
func (Resolved) MarshalJSON() ([]byte, error) { return nil, errResolvedRender }
func (Resolved) MarshalText() ([]byte, error) { return nil, errResolvedRender }
func (Resolved) MarshalYAML() (any, error)    { return nil, errResolvedRender }
