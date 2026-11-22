package ingest

import (
	"bytes"
	"errors"
	"io"

	"go.yaml.in/yaml/v3"
)

// MachineRole is the machine's role as its Talos configuration says it: "controlplane" or "worker",
// read from the `machine.type` of the documents (ginsys/bronzeward#1096). It is the one thing the
// console shows beside a machine's name. Anything it cannot read is "", a machine with no role: text
// that does not parse, no `machine` mapping, no `type`, a type that is not a string, any other value
// (`init`), or documents that name different types. It never refuses an import, because the role is a
// label and no machine is made unusable by lacking one.
func MachineRole(docs []byte) string {
	dec := yaml.NewDecoder(bytes.NewReader(docs))
	role := ""
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return role
			}
			return ""
		}
		got, ok := documentType(&doc)
		switch {
		case !ok:
		case got != "controlplane" && got != "worker":
			return ""
		case role != "" && role != got:
			return ""
		default:
			role = got
		}
	}
}

// documentType is the `machine.type` of one document, and whether it has one: a `type` key that is
// present counts, whatever it holds, so a document that names a type this package does not know ends
// the answer instead of being skipped.
func documentType(doc *yaml.Node) (string, bool) {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return "", false
	}
	machine := deref(inMapping(doc.Content[0], "machine"))
	if machine == nil {
		return "", false
	}
	t := deref(inMapping(machine, "type"))
	if t == nil {
		return "", false
	}
	if t.Kind != yaml.ScalarNode || t.Tag != "!!str" {
		return "", true
	}
	return t.Value, true
}

// inMapping is the value under key when n is a mapping, and nil for any other node: a sequence's
// items would otherwise read as keys and values.
func inMapping(n *yaml.Node, key string) *yaml.Node {
	if n = deref(n); n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	return mappingValue(n, key)
}
