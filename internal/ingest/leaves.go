package ingest

import (
	"errors"
	"strconv"

	"go.yaml.in/yaml/v3"
)

var (
	errLeavesParse = errors.New("ingest: a composed stream does not parse")
	errLeavesHost  = errors.New("ingest: an identified embedded document is not met as a parseable string")
)

// WalkLeaves visits every value node of every document of a composed stream b, in document order,
// containers included so that two streams' shapes can be compared. An alias is followed and
// visited at its own path. A string at a path named in embedded (keyed by Path.String, valued by
// its format) is an identified embedded document: it is parsed and walked at "|<format>" paths
// instead of being visited. Every named path must be met as such a string, or the walk fails.
// Neither error quotes the stream.
func WalkLeaves(b []byte, embedded map[string]string, fn func(p Path, n *yaml.Node) error) error {
	return walkComposed(b, embedded, fn, nil)
}

// WalkKeys visits every mapping key of a composed stream b as WalkLeaves walks it, embedded
// documents included, at the path of the value it names. A key is a node of its own, not a value:
// WalkLeaves does not visit it.
func WalkKeys(b []byte, embedded map[string]string, fn func(p Path, k *yaml.Node) error) error {
	return walkComposed(b, embedded, nil, fn)
}

// RewriteLeaves walks a composed stream b as WalkLeaves and WalkKeys walk it, fn and keyFn (either
// may be nil) changing the nodes they are given in place, and writes the stream back: each
// identified embedded document as Resolve writes one (JSON compact with sorted keys, so a "<" or
// ">" is written as a JSON Unicode escape; YAML with two-space indentation), then the stream with
// two-space indentation. No error quotes the stream.
func RewriteLeaves(b []byte, embedded map[string]string, fn, keyFn func(p Path, n *yaml.Node) error) ([]byte, error) {
	docs, err := composedDocs(b, embedded, fn, keyFn, true)
	if err != nil {
		return nil, err
	}
	return encodeStream(docs)
}

func walkComposed(b []byte, embedded map[string]string, fn, keyFn func(p Path, n *yaml.Node) error) error {
	_, err := composedDocs(b, embedded, fn, keyFn, false)
	return err
}

// composedDocs walks a composed stream's documents, writing each embedded document back into
// its host when write is set.
func composedDocs(b []byte, embedded map[string]string, fn, keyFn func(p Path, n *yaml.Node) error, write bool) ([]*yaml.Node, error) {
	docs, err := parseStream(b)
	if err != nil {
		return nil, errLeavesParse
	}
	met := map[string]bool{}
	var walk func(n *yaml.Node, p Path) error
	walk = func(n *yaml.Node, p Path) error {
		n = deref(n)
		if p.Format == "" {
			if format, ok := embedded[p.String()]; ok {
				inner, err := embeddedDocument(n)
				if err != nil || root(inner) == nil {
					return errLeavesHost
				}
				met[p.String()] = true
				if err := walk(root(inner), Path{Doc: p.Doc, Pointer: p.Pointer, Format: format}); err != nil || !write {
					return err
				}
				text, err := encodeEmbedded(inner, format)
				if err != nil {
					return errLeavesHost
				}
				n.Tag, n.Value, n.Style = "!!str", text, yaml.LiteralStyle
				return nil
			}
		}
		if fn != nil {
			if err := fn(p, n); err != nil {
				return err
			}
		}
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				c := p.child(keyToken(n.Content[i]))
				if keyFn != nil {
					if err := keyFn(c, deref(n.Content[i])); err != nil {
						return err
					}
				}
				if err := walk(n.Content[i+1], c); err != nil {
					return err
				}
			}
		case yaml.SequenceNode:
			for i, c := range n.Content {
				if err := walk(c, p.child(strconv.Itoa(i))); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for i, d := range docs {
		if top := root(d); top != nil {
			if err := walk(top, Path{Doc: i}); err != nil {
				return nil, err
			}
		}
	}
	if len(met) != len(embedded) {
		return nil, errLeavesHost
	}
	return docs, nil
}
