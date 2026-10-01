package ingest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"

	"go.yaml.in/yaml/v3"
)

// parse decodes every document of the input's stream, keeping tags (compilation.md §2.3 step 2).
// A failure refuses the input with at most a line number: the decoder's messages can quote the
// offending text.
func parse(u Unresolved) ([]*yaml.Node, error) {
	b := u.bytes()
	if len(b) == 0 {
		return nil, ErrEmptyInput
	}
	return parseStream(b)
}

func parseStream(b []byte) ([]*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var docs []*yaml.Node
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, parseRefusal(err)
		}
		if line := cyclicAlias(&n, map[*yaml.Node]bool{}); line != 0 {
			return nil, refuse(RuleParse, fmt.Sprintf("line %d", line))
		}
		if line := duplicateKey(&n); line != 0 {
			return nil, refuse(RuleParse, fmt.Sprintf("line %d", line))
		}
		docs = append(docs, &n)
	}
	if len(docs) == 0 {
		return nil, refuse(RuleParse, "the stream holds no document")
	}
	return docs, nil
}

// cyclicAlias is the line of an alias that names a node containing it, or 0. The decoder accepts
// one, but the graph is infinite: anything that follows aliases would not end. An alias can name
// only an anchor already seen, so a cycle needs an alias to a node still open above it.
func cyclicAlias(n *yaml.Node, open map[*yaml.Node]bool) int {
	if n.Kind == yaml.AliasNode {
		if open[n.Alias] {
			return n.Line
		}
		return 0
	}
	open[n] = true
	defer delete(open, n)
	for _, c := range n.Content {
		if line := cyclicAlias(c, open); line != 0 {
			return line
		}
	}
	return 0
}

// duplicateKey is the line of a mapping key whose text an earlier key of the same mapping holds,
// or 0. YAML forbids duplicate keys but the decoder accepts them into nodes, and a pointer token
// matches a key by its text: a path through such a mapping would name more than one node.
// Aliases are not followed; the node they name is checked where it is written.
func duplicateKey(n *yaml.Node) int {
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := deref(n.Content[i])
			if k == nil || k.Kind != yaml.ScalarNode {
				continue
			}
			if seen[k.Value] {
				return n.Content[i].Line
			}
			seen[k.Value] = true
		}
	}
	if n.Kind == yaml.AliasNode {
		return 0
	}
	for _, c := range n.Content {
		if line := duplicateKey(c); line != 0 {
			return line
		}
	}
	return 0
}

var yamlLine = regexp.MustCompile(`\bline ([0-9]+)\b`)

func parseRefusal(err error) error {
	if m := yamlLine.FindStringSubmatch(err.Error()); m != nil {
		return refuse(RuleParse, fmt.Sprintf("line %s", m[1]))
	}
	return refuse(RuleParse)
}
