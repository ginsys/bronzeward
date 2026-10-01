package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/provider"
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
func Extract(req Request) (*Candidate, error) {
	docs, err := parse(req.Input)
	if err != nil {
		return nil, err
	}
	if err := validate(docs, req.Declarations); err != nil {
		return nil, err
	}
	targets, err := identify(docs, req.Marks)
	if err != nil {
		return nil, err
	}
	exs, err := substitute(targets, req.Declarations.References)
	if err != nil {
		return nil, err
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

// Commit performs compilation.md §2.3 step 6 through create, once per extracted value in order,
// then constructs the sanitized value (step 7). A failed create returns its error and no
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
			return Sanitized{}, fmt.Errorf("ingest: creating the generation of %s: %w", v.name, err)
		}
	}
	return newSanitized(c.docs, c.decl), nil
}
