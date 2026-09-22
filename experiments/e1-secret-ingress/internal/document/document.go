// Package document is Phase-0 evidence code for the secret-ingress feasibility experiment
// (ginsys/bronzeward issue 2). It is not the v1 implementation.
//
// It is the smallest YAML layer the experiment needs: address every scalar in a machine
// configuration by a stable path, read one, and replace one in place. It deliberately does not
// decode into Talos types. Typed decoding would make the experiment depend on a machinery version
// and on which fields that version knows about, and the question under test — whether extraction
// can be made to happen before persistence — does not turn on either.
//
// Paths are dotted, with bracketed indices for sequence elements:
//
//	machine.ca.crt
//	machine.files[0].content
//	cluster.apiServer.extraArgs.audit-log-path
//
// A key containing a dot or a bracket would make two different locations share one path. Rather
// than silently resolving such a collision, Load refuses the document: a mark or a detection rule
// pointing at an ambiguous path could extract the wrong value, and a wrong extraction is not
// visible in any of this experiment's evidence.
package document

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Document is a parsed YAML document with every scalar indexed by path.
type Document struct {
	root  *yaml.Node
	index map[string]*yaml.Node
	paths []string
}

// Load parses YAML and indexes its scalars.
func Load(data []byte) (*Document, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("document: parsing: %w", err)
	}
	if root.Kind == 0 || len(root.Content) == 0 {
		return nil, fmt.Errorf("document: the input holds no YAML document")
	}

	d := &Document{root: &root, index: map[string]*yaml.Node{}}
	if err := d.walk("", root.Content[0]); err != nil {
		return nil, err
	}
	sort.Strings(d.paths)
	return d, nil
}

// walk indexes every scalar reachable from node, building its path as it descends.
func (d *Document) walk(prefix string, node *yaml.Node) error {
	switch node.Kind {
	case yaml.MappingNode:
		// Content alternates key, value.
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if strings.ContainsAny(key.Value, ".[]") {
				return fmt.Errorf("document: key %q at line %d contains a dot or a bracket, so its path "+
					"would be ambiguous; this experiment refuses such a document rather than risk "+
					"extracting the wrong value", key.Value, key.Line)
			}
			child := key.Value
			if prefix != "" {
				child = prefix + "." + key.Value
			}
			if err := d.walk(child, value); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for i, item := range node.Content {
			if err := d.walk(fmt.Sprintf("%s[%d]", prefix, i), item); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		if prefix == "" {
			// A bare scalar document. There is nothing to address it by, and a machine
			// configuration is never one.
			return fmt.Errorf("document: the input is a bare scalar, not a mapping")
		}
		if _, clash := d.index[prefix]; clash {
			return fmt.Errorf("document: two locations share the path %q", prefix)
		}
		d.index[prefix] = node
		d.paths = append(d.paths, prefix)
	case yaml.AliasNode:
		// An alias points at a node indexed elsewhere. Following it would index the same scalar
		// under two paths and make a single extraction look like two.
		return fmt.Errorf("document: YAML aliases are not supported (line %d); one scalar would "+
			"appear under two paths and an extraction count would be wrong", node.Line)
	}
	return nil
}

// Paths lists every scalar path, sorted. Sorting matters: extraction order feeds the journal, and
// a map iteration would make two runs over the same input produce different journals.
func (d *Document) Paths() []string {
	out := make([]string, len(d.paths))
	copy(out, d.paths)
	return out
}

// Get returns the scalar at path.
func (d *Document) Get(path string) (string, bool) {
	node, ok := d.index[path]
	if !ok {
		return "", false
	}
	return node.Value, true
}

// Replace overwrites the scalar at path. It is how a reference takes a secret's place.
//
// The replacement is forced to a plain, unstyled string. A certificate's value arrives as a
// literal block or a long quoted line; leaving that style on a short reference would produce a
// diff that looks like a formatting change rather than a substitution.
func (d *Document) Replace(path, value string) error {
	node, ok := d.index[path]
	if !ok {
		return fmt.Errorf("document: no scalar at %q", path)
	}
	node.SetString(value)
	node.Style = 0
	return nil
}

// Bytes re-encodes the document. Two-space indentation matches the fixtures' own configurations,
// so a diff against the input shows the substitutions and nothing else.
func (d *Document) Bytes() ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d.root); err != nil {
		return nil, fmt.Errorf("document: encoding: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("document: closing the encoder: %w", err)
	}
	return buf.Bytes(), nil
}
