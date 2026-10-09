package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/seam"
)

// Request is one ingestion: the input, the paths the operator marks, and the declarations the
// input already carries (a draft update of a sanitized document; empty for a node read).
type Request struct {
	Input        Unresolved
	Marks        []Path
	Declarations Declarations
}

// Candidate is a sanitized stream whose extracted values are not yet written (compilation.md
// §2.3 steps 4-5 done, step 6 pending). It holds the values only as provider values, which
// never render.
type Candidate struct {
	docs      []byte
	decl      Declarations
	values    []namedValue
	committed bool
}

type namedValue struct {
	name  string
	value provider.Value
}

// extraction is one substituted target: its minted name, its kind and its value. plain is the
// value in Go form and lives only inside Extract, for the guard and the provider value.
type extraction struct {
	name  string
	kind  provider.Kind
	plain any
	paths []Path
}

// Extract runs compilation.md §2.3 steps 2-5 on a request: parse, check the input as authored,
// identify, substitute and guard. It writes nothing; Commit performs step 6 and constructs the
// sanitized value (step 7).
func Extract(req Request) (*Candidate, error) { return extract(req, true) }

// extract is Extract; guarded is false only in tests, to build the control candidates that
// show a guard refusal is the guard's.
func extract(req Request, guarded bool) (_ *Candidate, err error) {
	docs, err := parse(req.Input)
	if err != nil {
		return nil, err
	}
	seam.At("parse")
	// A refusal path can run through a key that holds a value being extracted, before or after
	// substitution: every refusal is redacted against those values. When the machinery cannot
	// load a document its secret fields are unknown, and a refusal names documents only.
	known, complete := knownSecrets(docs, req.Marks)
	defer func() {
		if complete {
			err = redactRefusal(err, known)
		} else {
			err = documentsOnly(err)
		}
	}()
	if err := validate(docs, req.Declarations); err != nil {
		return nil, err
	}
	declared, err := checkDeclarations(req.Declarations)
	if err != nil {
		return nil, err
	}
	var outerMarks, innerMarks []Path
	for _, m := range req.Marks {
		if m.Format == "" {
			outerMarks = append(outerMarks, m)
		} else {
			innerMarks = append(innerMarks, m)
		}
	}
	targets, err := identify(docs, outerMarks)
	if err != nil {
		return nil, err
	}
	if err := refuseReferenceHolders(docs, targets, declared); err != nil {
		return nil, err
	}
	inner, embedded, err := identifyEmbedded(docs, innerMarks, declared, targets)
	if err != nil {
		return nil, err
	}
	all, err := coalesce(append(targets, inner...))
	if err != nil {
		return nil, err
	}
	seam.At("identify")
	exs, err := substitute(all, req.Declarations.References)
	if err != nil {
		return nil, err
	}
	for _, e := range embedded {
		plainStyle(e.doc)
		text, err := encodeStream([]*yaml.Node{e.doc})
		if err != nil {
			return nil, err
		}
		*e.outer = yaml.Node{
			Kind: yaml.ScalarNode, Tag: "!!str", Value: string(text), Style: yaml.LiteralStyle, Anchor: e.outer.Anchor,
			HeadComment: e.outer.HeadComment, LineComment: e.outer.LineComment, FootComment: e.outer.FootComment,
		}
	}
	decl := req.Declarations.clone()
	if decl.References == nil {
		decl.References = map[string]Reference{}
	}
	for _, ex := range exs {
		decl.References[ex.name] = Reference{Kind: ex.kind, Version: 1}
	}
	out, err := encodeStream(docs)
	if err != nil {
		return nil, err
	}
	back, err := parseStream(out)
	if err != nil {
		return nil, errors.New("ingest: the sanitized stream does not parse back")
	}
	seam.At("substitute") // step 4 done: the candidate document and its declarations built, not guarded
	if guarded {
		if err := guard(back, exs, declared); err != nil {
			return nil, err
		}
	}
	if err := validate(back, decl); err != nil {
		return nil, err
	}
	c := &Candidate{docs: out, decl: decl}
	for _, ex := range exs {
		v, err := provider.NewValue(ex.kind, ex.plain)
		if err != nil {
			return nil, refuse(RuleMarkKind, ex.paths[0].String())
		}
		c.values = append(c.values, namedValue{ex.name, v})
	}
	return c, nil
}

// knownSecrets is the text of every value the request would extract that can be found before
// extraction: each value the machinery redacts, and every scalar under each mark (keys included),
// outer or inside an embedded document that parses, aliases followed. complete is false when the
// machinery could not load a document, or a value it redacts is not the text of the input scalar
// at its path (schema-indirect): the input's own spelling of the secret is then unknown. Other
// failures are skipped here; identification reports them.
func knownSecrets(docs []*yaml.Node, marks []Path) (texts []string, complete bool) {
	var values []any
	complete = true
	for i, d := range docs {
		pointers, err := schemaPointers(d, i, nil)
		if err != nil {
			complete = false
		}
		for _, sp := range pointers {
			values = append(values, sp.value)
			n, ok := resolve(docs, i, sp.pointer)
			if !ok || n.Kind != yaml.ScalarNode || n.Value != scalarText(sp.value) {
				complete = false
			}
		}
	}
	seen := map[*yaml.Node]bool{}
	var collect func(n *yaml.Node)
	collect = func(n *yaml.Node) {
		if n = deref(n); n == nil || seen[n] {
			return
		}
		seen[n] = true
		switch n.Kind {
		case yaml.ScalarNode:
			if n.Tag != refTag {
				values = append(values, n.Value)
			}
		case yaml.MappingNode, yaml.SequenceNode:
			// A marked mapping's keys are extracted with its members.
			for _, c := range n.Content {
				collect(c)
			}
		}
	}
	for _, m := range marks {
		n, ok := resolve(docs, m.Doc, m.Pointer)
		if ok && m.Format != "" {
			inner, err := embeddedDocument(n)
			if err != nil {
				continue
			}
			n, ok = resolveIn(root(inner), m.Inner)
		}
		if !ok {
			continue
		}
		collect(n)
	}
	return searchTexts(values), complete
}

// refuseReferenceHolders refuses mark-kind a target that is, or holds as a member, an identified
// embedded document with a reference inside it (compilation.md §3.6 item 3): its value would
// carry the reference's name, never the value the name stands for.
func refuseReferenceHolders(docs []*yaml.Node, targets []*target, declared map[string]string) error {
	holders := map[*yaml.Node]bool{}
	for key := range declared {
		p, err := ParsePath(key)
		if err != nil {
			continue
		}
		n, ok := resolve(docs, p.Doc, p.Pointer)
		if !ok {
			continue
		}
		if inner, err := embeddedDocument(n); err == nil && holdsReference(inner) {
			holders[n] = true
		}
	}
	for _, t := range targets {
		n := deref(t.node)
		held := holders[n]
		for i := 1; n.Kind == yaml.MappingNode && i < len(n.Content); i += 2 {
			held = held || holders[deref(n.Content[i])]
		}
		if held {
			return refuse(RuleMarkKind, t.paths[0].String())
		}
	}
	return nil
}

// holdsReference reports whether n or any node under it is a !bwref. An alias needs no visit: its
// anchored node is visited where it stands.
func holdsReference(n *yaml.Node) bool {
	if n.Tag == refTag {
		return true
	}
	for _, c := range n.Content {
		if holdsReference(c) {
			return true
		}
	}
	return false
}

// embeddedDoc is an identified embedded document with marks inside it: the outer string scalar
// that holds it and its parsed document, which substitution changes and extract writes back.
type embeddedDoc struct {
	outer, doc *yaml.Node
}

// identifyEmbedded resolves marks inside identified embedded documents (compilation.md §2.2,
// §5.4). The outer path must be declared in embedded with the mark's format; an embedded
// document is parsed once however many marks it holds. Its outer scalar must not itself be
// extracted: it cannot be both a reference and the document holding one.
func identifyEmbedded(docs []*yaml.Node, marks []Path, declared map[string]string, outer []*target) ([]*target, []embeddedDoc, error) {
	extracted := map[*yaml.Node]bool{}
	for _, t := range outer {
		extracted[t.node] = true
	}
	parsed := map[string]*embeddedDoc{}
	var order []string
	var out []*target
	byNode := map[*yaml.Node]*target{}
	for _, m := range marks {
		holder := Path{Doc: m.Doc, Pointer: m.Pointer}
		key := holder.String()
		if format, ok := declared[key]; !ok || format != m.Format {
			return nil, nil, refuse(RuleBadPath, m.String())
		}
		e, ok := parsed[key]
		if !ok {
			n, found := resolve(docs, m.Doc, m.Pointer)
			if !found || extracted[n] {
				return nil, nil, refuse(RuleEmbedded, key)
			}
			doc, err := embeddedDocument(n)
			if err != nil {
				return nil, nil, refuse(RuleEmbedded, key)
			}
			e = &embeddedDoc{outer: n, doc: doc}
			parsed[key] = e
			order = append(order, key)
		}
		n, found := resolveIn(root(e.doc), m.Inner)
		if !found {
			return nil, nil, refuse(RuleMarkUnaddressed, m.String())
		}
		if n.Tag == refTag {
			continue
		}
		if t, dup := byNode[n]; dup {
			t.paths = append(t.paths, m)
			continue
		}
		if _, _, err := valueOf(n); err != nil {
			return nil, nil, refuse(RuleMarkKind, m.String())
		}
		t := &target{node: n, paths: []Path{m}}
		byNode[n] = t
		out = append(out, t)
	}
	var docsOut []embeddedDoc
	for _, k := range order {
		docsOut = append(docsOut, *parsed[k])
	}
	return out, docsOut, nil
}

// plainStyle drops the author's styles and comments from an embedded document before it is
// re-serialized (compilation.md §5.4: formatting, comments and quoting are not preserved; an
// identified JSON document is written back as block YAML).
func plainStyle(n *yaml.Node) {
	n.Style = 0
	n.HeadComment, n.LineComment, n.FootComment = "", "", ""
	for _, c := range n.Content {
		plainStyle(c)
	}
}

// coalesce drops each target that is a member value of a mapping target and is reached only
// through it: the mapping's extraction carries its value, which substituting it first would
// turn into a reference the mapping cannot hold. A member that aliases a target, or a target
// that is also reached outside the mapping, cannot be carried and refuses the mapping. An alias
// that reaches a member from outside needs an anchor on it, which identification has already
// refused (valueOf).
func coalesce(ts []*target) ([]*target, error) {
	byNode := map[*yaml.Node]*target{}
	for _, t := range ts {
		byNode[t.node] = t
	}
	inside := map[*target]bool{}
	for _, p := range ts {
		if p.node.Kind != yaml.MappingNode {
			continue
		}
		for i := 1; i < len(p.node.Content); i += 2 {
			m := p.node.Content[i]
			c := byNode[deref(m)]
			if c == nil {
				continue
			}
			if m.Kind == yaml.AliasNode || !reachedOnlyThrough(c.paths, p.paths) {
				return nil, refuse(RuleMarkKind, p.paths[0].String())
			}
			inside[c] = true
		}
	}
	return slices.DeleteFunc(slices.Clone(ts), func(t *target) bool { return inside[t] }), nil
}

// reachedOnlyThrough reports whether every path in cs lies strictly below a path in ps.
func reachedOnlyThrough(cs, ps []Path) bool {
	for _, c := range cs {
		if !slices.ContainsFunc(ps, func(p Path) bool { return below(c, p) }) {
			return false
		}
	}
	return true
}

func below(c, p Path) bool {
	if c.Doc != p.Doc || c.Format != p.Format {
		return false
	}
	if c.Format == "" {
		return len(c.Pointer) > len(p.Pointer) && slices.Equal(c.Pointer[:len(p.Pointer)], p.Pointer)
	}
	return slices.Equal(c.Pointer, p.Pointer) && len(c.Inner) > len(p.Inner) && slices.Equal(c.Inner[:len(p.Inner)], p.Inner)
}

// substitute replaces each target node in place by a reference under a newly minted name
// (compilation.md §5.1: "s-" and a random identifier, never derived from the value). The node
// keeps its anchor and comments, so every alias of it yields the reference.
func substitute(targets []*target, taken map[string]Reference) ([]extraction, error) {
	minted := map[string]bool{}
	var out []extraction
	for _, t := range targets {
		kind, plain, err := valueOf(t.node)
		if err != nil {
			return nil, refuse(RuleMarkKind, t.paths[0].String())
		}
		name := "s-" + provider.NewValueID()
		if _, dup := taken[name]; dup || minted[name] {
			return nil, errors.New("ingest: a minted name collided; retry")
		}
		minted[name] = true
		*t.node = yaml.Node{
			Kind: yaml.ScalarNode, Tag: refTag, Value: name, Anchor: t.node.Anchor,
			HeadComment: t.node.HeadComment, LineComment: t.node.LineComment, FootComment: t.node.FootComment,
		}
		out = append(out, extraction{name: name, kind: kind, plain: plain, paths: t.paths})
	}
	return out, nil
}

// encodeStream writes the documents through the pinned encoder with two-space indentation.
func encodeStream(docs []*yaml.Node) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	for _, d := range docs {
		if err := enc.Encode(d); err != nil {
			return nil, errors.New("ingest: the sanitized stream could not be encoded")
		}
	}
	if err := enc.Close(); err != nil {
		return nil, errors.New("ingest: the sanitized stream could not be encoded")
	}
	return b.Bytes(), nil
}

// CreateError is a create callback's failure for one reference. It names the reference and
// answers errors.Is for the cause, but never renders or returns it: the callback's message is
// the caller's text and can hold the value it was writing, and a reporter that walks a chain
// through Unwrap renders every error it reaches. errors.As cannot reach the cause for the same
// reason: it would hand the caller the error itself.
// The cause sits behind a function, not in an error field, so that reflection over a CreateError
// (any fmt verb without a method, or an enclosing error's unexported field) shows an address.
type CreateError struct {
	Name  string
	cause func() error
}

func newCreateError(name string, err error) *CreateError {
	return &CreateError{Name: name, cause: func() error { return err }}
}

func (e *CreateError) Error() string {
	return fmt.Sprintf("ingest: creating the generation of %s failed", e.Name)
}

// Format prints the message for every verb.
func (e *CreateError) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, e.Error()) }

// Is reports whether the cause's chain holds target.
func (e *CreateError) Is(target error) bool {
	return e.cause != nil && errors.Is(e.cause(), target)
}

// Commit performs compilation.md §2.3 step 6 through create, once per extracted value in order,
// then constructs the sanitized value (step 7). A failed create returns a CreateError and no
// sanitized value; a candidate commits at most once, failed or not.
func (c *Candidate) Commit(ctx context.Context, create func(ctx context.Context, name string, v provider.Value) error) (Sanitized, error) {
	if c == nil || len(c.docs) == 0 || create == nil {
		return Sanitized{}, errors.New("ingest: no candidate to commit")
	}
	if c.committed {
		return Sanitized{}, errors.New("ingest: the candidate was already committed")
	}
	c.committed = true
	for _, v := range c.values {
		if err := create(ctx, v.name, v.value); err != nil {
			return Sanitized{}, newCreateError(v.name, err)
		}
	}
	seam.At("generation") // step 6 done: every generation created, nothing constructed
	return newSanitized(c.docs, c.decl), nil
}
