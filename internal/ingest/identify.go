package ingest

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/container"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/provider"
)

// target is one input node to extract and every path that names it: an anchored node reached
// through aliases is one target (extracted once, referenced wherever it appears).
type target struct {
	node  *yaml.Node
	paths []Path
}

// identify lists the nodes to extract (compilation.md §2.3 step 3): every field the pinned
// machinery's RedactSecrets changes, document by document, and every mark. Nodes already tagged
// !bwref are excluded. A mark addressing no node refuses the input, as does a document the
// machinery cannot load or a secret field it finds that is not one plain input node.
func identify(docs []*yaml.Node, marks []Path) ([]*target, error) {
	var out []*target
	byNode := map[*yaml.Node]*target{}
	add := func(n *yaml.Node, p Path) {
		if n.Tag == refTag {
			return
		}
		if t, ok := byNode[n]; ok {
			t.paths = append(t.paths, p)
			return
		}
		t := &target{node: n, paths: []Path{p}}
		byNode[n] = t
		out = append(out, t)
	}
	for i := range docs {
		pointers, err := schemaPointers(docs[i], i, nil)
		if err != nil {
			return nil, err
		}
		for _, sp := range pointers {
			n, ok := resolve(docs, i, sp.pointer)
			// The input node must be the leaf the machinery encoded, as written: a merge key,
			// a multi-line or !!binary base64 or any other indirection would leave the secret
			// where substitution cannot reach it.
			if !ok || n.Kind != yaml.ScalarNode || n.Value != sp.value {
				return nil, refuse(RuleSchemaIndirect, fmt.Sprintf("doc[%d]", i))
			}
			add(n, Path{Doc: i, Pointer: sp.pointer})
		}
	}
	for _, m := range marks {
		if m.Format != "" {
			return nil, refuseAt(RuleBadPath, []Path{m}, m.String())
		}
		n, ok := resolve(docs, m.Doc, m.Pointer)
		if !ok {
			return nil, refuseAt(RuleMarkUnaddressed, []Path{m}, m.String())
		}
		add(n, m)
	}
	for _, t := range out {
		if _, _, err := valueOf(t.node); err != nil {
			return nil, refuseAt(RuleMarkKind, t.paths, t.paths[0].String())
		}
	}
	// Each target is stored as a reference, which a later ingestion loads as a null: a document
	// that does not load that way (a mark on its kind) could never be ingested again.
	stored := map[*yaml.Node]bool{}
	var withTargets []int
	for _, t := range out {
		stored[t.node] = true
		for _, p := range t.paths {
			if !slices.Contains(withTargets, p.Doc) {
				withTargets = append(withTargets, p.Doc)
			}
		}
	}
	slices.Sort(withTargets)
	// The refusal is the first document's, and arises from every document that does not load:
	// it names the first mark in the request among them, not the one in the first document.
	var unloadable error
	var sources []Path
	for _, i := range withTargets {
		if _, err := schemaPointers(docs[i], i, stored); err != nil {
			if unloadable == nil {
				unloadable = err
			}
			sources = append(sources, unloadableSources(docs[i], i, out)...)
		}
	}
	if unloadable != nil {
		return nil, from(unloadable, sources)
	}
	return out, nil
}

// unloadableSources is what a document i that does not load with its targets stored arises from:
// the paths in it of the target whose storing first stops it loading, its targets stored one by
// one in identification order. The document loads with none stored and not with all, so such a
// target exists, and bisection finds one in log2 of the targets' number of loads, however many
// marks the request carries. When only a combination stops it loading, that is the combination's
// last target.
func unloadableSources(doc *yaml.Node, i int, out []*target) []Path {
	// A node under another target's is stored with it: nulled or not, it is not there.
	under := map[*yaml.Node]bool{}
	for _, t := range out {
		var walk func(n *yaml.Node)
		walk = func(n *yaml.Node) {
			for _, c := range n.Content {
				if !under[c] {
					under[c] = true
					walk(c)
				}
			}
		}
		walk(t.node)
	}
	var in [][]Path
	var nodes []*yaml.Node
	for _, t := range out {
		if under[t.node] {
			continue
		}
		var ps []Path
		for _, p := range t.paths {
			if p.Doc == i {
				ps = append(ps, p)
			}
		}
		if ps != nil {
			in = append(in, ps)
			nodes = append(nodes, t.node)
		}
	}
	// The first lo targets stored, the document loads; the first hi, it does not.
	lo, hi := 0, len(nodes)
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		stored := map[*yaml.Node]bool{}
		for _, n := range nodes[:mid] {
			stored[n] = true
		}
		if _, err := schemaPointers(doc, i, stored); err != nil {
			hi = mid
		} else {
			lo = mid
		}
	}
	if hi == 0 {
		return nil
	}
	return in[hi-1]
}

// plainDeletes reports whether every delete directive under n holds nothing but itself and stands
// outside a list. The machinery drops a directive's mapping whole, and a list entry's directive
// with the member that selects the entry, so anything beside it would be stored without ever being
// loaded; a list-entry delete is refused, since its selector could be such a value.
func plainDeletes(n *yaml.Node) bool {
	switch n.Kind {
	case yaml.MappingNode:
		if directive(n) {
			return len(n.Content) == 2
		}
		for i := 1; i < len(n.Content); i += 2 {
			if !plainDeletes(n.Content[i]) {
				return false
			}
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if directive(c) || !plainDeletes(c) {
				return false
			}
		}
	}
	return true
}

// directive reports whether n is a mapping holding a delete directive.
func directive(n *yaml.Node) bool {
	if n.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k, v := n.Content[i], n.Content[i+1]; k.Value == "$patch" && v.Kind == yaml.ScalarNode && v.Value == "delete" {
			return true
		}
	}
	return false
}

// schemaPointer is a leaf the machinery redacts and its unredacted encoded value.
type schemaPointer struct {
	pointer []string
	value   string
}

// machineryLoaded, when set (only by tests), is called once per machinery load: what a request
// can make the server spend.
var machineryLoaded func()

// schemaPointers loads one document on its own with the pinned machinery and returns the leaves
// whose encoding RedactSecrets changes, in pointer order. !bwref nodes, and the nodes in nulled,
// are loaded as nulls (the machinery redacts only non-empty fields). Machinery errors are
// dropped: they can quote input.
func schemaPointers(doc *yaml.Node, i int, nulled map[*yaml.Node]bool) ([]schemaPointer, error) {
	unloadable := refuse(RuleSchemaUnloadable, fmt.Sprintf("doc[%d]", i))
	top := root(doc)
	if top == nil || top.Kind == yaml.ScalarNode && top.Tag == "!!null" {
		return nil, nil
	}
	if !plainDeletes(top) {
		return nil, unloadable
	}
	if machineryLoaded != nil {
		machineryLoaded()
	}
	text, err := yaml.Marshal(copyWithoutReferences(top, map[*yaml.Node]*yaml.Node{}, nulled))
	if err != nil {
		return nil, unloadable
	}
	// A fragment may carry delete directives (compilation.md §6 step 5): it loads as composition
	// loads a patch, each directive becoming a selector document of its own, which holds no value.
	// A document of directives only holds no secret.
	loaded, err := configloader.NewFromBytes(text, configloader.WithAllowPatchDelete())
	if errors.Is(err, configloader.ErrNoConfig) {
		return nil, nil
	}
	if err != nil {
		return nil, unloadable
	}
	var docs []config.Document
	for _, d := range loaded.Documents() {
		if _, directive := d.(interface{ ApplyTo(config.Document) error }); !directive {
			docs = append(docs, d)
		}
	}
	if len(docs) == 0 {
		return nil, nil
	}
	p, err := container.New(docs...)
	if err != nil || len(docs) != 1 {
		return nil, unloadable
	}
	opt := encoder.WithComments(encoder.CommentsDisabled)
	raw, err := p.EncodeBytes(opt)
	if err != nil {
		return nil, unloadable
	}
	red, err := p.RedactSecrets("bronzeward-redacted-" + provider.NewValueID()).EncodeBytes(opt)
	if err != nil {
		return nil, unloadable
	}
	a, errA := parseStream(raw)
	b, errB := parseStream(red)
	if errA != nil || errB != nil || len(a) != 1 || len(b) != 1 {
		return nil, unloadable
	}
	ra, rb := scalarLeaves(a[0]), scalarLeaves(b[0])
	var out []schemaPointer
	for k, v := range ra {
		if w, ok := rb[k]; !ok || w != v {
			out = append(out, schemaPointer{pointer: splitKey(k), value: v})
		}
	}
	slices.SortFunc(out, func(x, y schemaPointer) int { return slices.Compare(x.pointer, y.pointer) })
	return out, nil
}

// copyWithoutReferences is a deep copy of n with every !bwref node, and every node in nulled,
// replaced by a null that keeps its anchor, so its aliases still resolve. Each node is copied
// once and an alias points at its target's copy, as in the input.
func copyWithoutReferences(n *yaml.Node, copies map[*yaml.Node]*yaml.Node, nulled map[*yaml.Node]bool) *yaml.Node {
	if n == nil {
		return nil
	}
	if c, ok := copies[n]; ok {
		return c
	}
	if n.Tag == refTag || nulled[n] {
		c := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null", Anchor: n.Anchor}
		copies[n] = c
		return c
	}
	c := *n
	copies[n] = &c
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, ch := range n.Content {
		c.Content[i] = copyWithoutReferences(ch, copies, nulled)
	}
	if n.Alias != nil {
		c.Alias = copyWithoutReferences(n.Alias, copies, nulled)
	}
	return &c
}

// scalarLeaves maps every scalar value's pointer (tokens joined by NUL, which no YAML key the
// machinery writes holds) to its value.
func scalarLeaves(doc *yaml.Node) map[string]string {
	out := map[string]string{}
	_ = walkStream([]*yaml.Node{doc}, func(n *yaml.Node, p Path, key bool, _ *yaml.Node) error {
		if !key && n.Kind == yaml.ScalarNode {
			out[strings.Join(p.Pointer, "\x00")] = n.Value
		}
		return nil
	})
	return out
}

func splitKey(k string) []string {
	if k == "" {
		return nil
	}
	return strings.Split(k, "\x00")
}

// valueOf is a node's value and kind (rulings in the package doc): a !!str, !!int or !!bool
// scalar, or a mapping of string keys to those. Anything else (null, float, sequence, nested
// mapping, a reference, another tag, a repeated key, an anchored key or member) has no kind.
// Errors never quote the node.
func valueOf(n *yaml.Node) (provider.Kind, any, error) {
	n = deref(n)
	if n == nil {
		return "", nil, refuse(RuleMarkKind)
	}
	if n.Kind == yaml.MappingNode {
		m := map[string]any{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			// Substitution drops a member's anchor, so an alias of it elsewhere would name an
			// earlier anchor of the same name, or none.
			if n.Content[i].Anchor != "" || n.Content[i+1].Anchor != "" {
				return "", nil, refuse(RuleMarkKind)
			}
			k, v := deref(n.Content[i]), n.Content[i+1]
			if k == nil || k.Kind != yaml.ScalarNode || k.Tag != "!!str" {
				return "", nil, refuse(RuleMarkKind)
			}
			if _, dup := m[k.Value]; dup {
				return "", nil, refuse(RuleMarkKind)
			}
			_, member, err := scalarValue(deref(v))
			if err != nil {
				return "", nil, err
			}
			m[k.Value] = member
		}
		return provider.KindMapping, m, nil
	}
	return scalarValue(n)
}

func scalarValue(n *yaml.Node) (provider.Kind, any, error) {
	bad := refuse(RuleMarkKind)
	if n == nil || n.Kind != yaml.ScalarNode {
		return "", nil, bad
	}
	switch n.Tag {
	case "!!str":
		return provider.KindString, n.Value, nil
	case "!!bool":
		// Only the canonical spellings: a reference renders true or false, so True or TRUE
		// would not come back as written.
		switch n.Value {
		case "true":
			return provider.KindBoolean, true, nil
		case "false":
			return provider.KindBoolean, false, nil
		}
	case "!!int":
		// Only canonical decimal: 0x1F or 0o17 would render back as 31 or 15.
		if !canonicalInteger.MatchString(n.Value) {
			return "", nil, bad
		}
		if i, err := strconv.ParseInt(n.Value, 10, 64); err == nil {
			return provider.KindInteger, i, nil
		}
		if u, err := strconv.ParseUint(n.Value, 10, 64); err == nil {
			return provider.KindInteger, u, nil
		}
	}
	return "", nil, bad
}

var canonicalInteger = regexp.MustCompile(`^(0|-?[1-9][0-9]*)$`)
