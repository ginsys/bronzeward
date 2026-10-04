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
	docs, err := parseStream(b)
	if err != nil {
		return errLeavesParse
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
				return walk(root(inner), Path{Doc: p.Doc, Pointer: p.Pointer, Format: format})
			}
		}
		if err := fn(p, n); err != nil {
			return err
		}
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				if err := walk(n.Content[i+1], p.child(keyToken(n.Content[i]))); err != nil {
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
				return err
			}
		}
	}
	if len(met) != len(embedded) {
		return errLeavesHost
	}
	return nil
}
