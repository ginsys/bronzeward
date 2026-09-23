// Package document is Phase-0 evidence code for the secret-ingress feasibility experiment
// (ginsys/bronzeward issue 2). It is not the v1 implementation.
//
// It is the smallest YAML layer the experiment needs: address every scalar in a machine
// configuration by a stable path, read one, and replace one in place. It deliberately does not
// decode into Talos types. Typed decoding would make the experiment depend on a machinery version
// and on which fields that version knows about, and the question under test — whether extraction
// can be made to happen before persistence — does not turn on either.
//
// Paths are dotted, with bracketed indices for sequence elements, and every path names the YAML
// document it is in:
//
//	doc[0].machine.ca.crt
//	doc[0].machine.files[0].content
//	doc[1].cluster.apiServer.extraArgs.audit-log-path
//
// The document index is always present, including for a single-document file. It was added after
// the prototype was found to be reading only the first document of the fixtures' own
// controlplane.yaml: Talos 1.13 emits the machine configuration and several sibling documents in
// one file, and yaml.Unmarshal into a node decodes the first and discards the rest without an
// error. A secret in a later document would have been unreachable by every mark and every rule,
// and the run would have scanned clean. Indexing all of them, under a prefix that cannot be
// omitted, is what stops that from coming back.
//
// A key containing a dot or a bracket would make two different locations share one path, and a
// mark or a detection rule pointing at such a path could extract the wrong value — a mistake that
// is invisible in every piece of evidence this experiment collects. Rather than resolve the
// collision silently, Load leaves the subtree under such a key unaddressable: it is excluded from
// Paths, Get and Replace, and listed by Unaddressable.
//
// Load originally refused the whole document instead. That turned out to reject every real Talos
// configuration: machine.nodeLabels carries Kubernetes label keys such as
// "node.kubernetes.io/exclude-from-external-load-balancers", and dots in label and annotation keys
// are ordinary. Refusing was the wrong response to a hazard that only exists where someone tries
// to address the path, and it would have left the experiment able to run on nothing but fixtures
// it wrote itself. The coverage gap that remains is real and is reported rather than hidden: a
// secret under an unaddressable key cannot be marked, and the report records that as a limit of
// this prototype's addressing, not of the design.
package document

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Document is a parsed YAML stream with every addressable scalar indexed by path. It is one file,
// which may hold several YAML documents; the path's doc[n] prefix says which.
type Document struct {
	roots []*yaml.Node
	index map[string]*yaml.Node
	paths []string
	// unaddressable holds one entry per key whose name would make its path ambiguous, as
	// "<parent path>: <key>". Everything below such a key is excluded from the index.
	unaddressable []string
}

// Load parses every YAML document in the input and indexes their scalars.
//
// A decoder loop, not yaml.Unmarshal: Unmarshal into a node decodes the first document and
// discards the rest with no error at all, which would leave a secret in a later document outside
// every path this program can address, while the run scanned clean.
func Load(data []byte) (*Document, error) {
	d := &Document{index: map[string]*yaml.Node{}}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for n := 0; ; n++ {
		var root yaml.Node
		err := dec.Decode(&root)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("document: parsing document %d: %w", n, err)
		}
		if root.Kind == 0 || len(root.Content) == 0 || root.Content[0].ShortTag() == "!!null" {
			// An empty document between separators. It carries nothing and indexes nothing, but the
			// numbering must still advance so a path keeps naming the document it came from.
			//
			// yaml.v3 decodes one as a document holding a null scalar, not as a document with no
			// content, so the null case is the one that actually occurs. Without it, a legal stream
			// with a trailing separator was refused below as "a bare scalar", and this branch — and
			// the round trip in Bytes that depends on it — was never reached at all.
			d.roots = append(d.roots, nil)
			continue
		}
		if root.Content[0].Kind == yaml.ScalarNode {
			// A bare scalar document. There is nothing to address inside it, and a machine
			// configuration is never one.
			return nil, fmt.Errorf("document: document %d is a bare scalar, not a mapping", n)
		}
		d.roots = append(d.roots, &root)
		if err := d.walk(fmt.Sprintf("doc[%d]", n), root.Content[0]); err != nil {
			return nil, err
		}
	}
	if len(d.roots) == 0 {
		return nil, fmt.Errorf("document: the input holds no YAML document")
	}
	sort.Strings(d.paths)
	sort.Strings(d.unaddressable)
	if len(d.paths) == 0 {
		// Extraction would find nothing and the run would look clean for a reason that has nothing
		// to do with the design under test. The two causes are reported apart: blaming ambiguous
		// keys for an input that simply holds no value would send the reader to the wrong fix.
		if len(d.unaddressable) == 0 {
			return nil, errors.New("document: the input holds no scalar value; there is nothing to extract or persist")
		}
		return nil, fmt.Errorf("document: no scalar in this document can be addressed; %d key(s) "+
			"carry a dot or a bracket and everything is below one of them", len(d.unaddressable))
	}
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
				// Ambiguous: "a.b: x" and "a: {b: x}" would both be "a.b". Nothing below this key
				// is indexed, so no mark and no rule can reach it, and the gap is reported rather
				// than resolved by guessing which location a path meant.
				d.unaddressable = append(d.unaddressable, prefix+": "+key.Value)
				continue
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

// Unaddressable lists the keys whose names would make a path ambiguous, as "<parent>: <key>".
// Everything below each of them is outside the index, so it cannot be marked, detected, extracted
// or substituted. A caller that reports a clean run has to report this alongside it: the two
// together are the coverage claim, and the clean result on its own overstates it.
func (d *Document) Unaddressable() []string {
	out := make([]string, len(d.unaddressable))
	copy(out, d.unaddressable)
	return out
}

// Scalars returns the value of every scalar node in every document, keys included and subtrees
// under unaddressable keys included. It is for checking what a document holds, not for addressing
// it: Paths omits exactly the subtrees a leftover secret could hide in.
func (d *Document) Scalars() []string {
	var out []string
	var visit func(*yaml.Node)
	visit = func(n *yaml.Node) {
		if n == nil {
			return
		}
		if n.Kind == yaml.ScalarNode {
			out = append(out, n.Value)
		}
		for _, c := range n.Content {
			visit(c)
		}
	}
	for _, root := range d.roots {
		visit(root)
	}
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
// Every document is re-encoded, in order, separated as they arrived. Dropping the ones that carry
// no addressable scalar would silently rewrite the operator's file.
//
// That includes the empty ones. An earlier version skipped them, which shifted the doc[n] prefix of
// every later path on a round trip — a reference recorded against doc[2] would then name a
// different document once the sanitized bytes were read back. Each document is encoded on its own
// and the separators are written here, so an empty one can be emitted as nothing between two of
// them; a stream with no empty document comes out byte-for-byte as the single encoder wrote it.
func (d *Document) Bytes() ([]byte, error) {
	var buf bytes.Buffer
	for n, root := range d.roots {
		switch {
		case n > 0:
			buf.WriteString("---\n")
		case root == nil:
			// An empty first document needs its own explicit start, or the separator before the
			// second would be read as the start of the first.
			buf.WriteString("---\n")
		}
		if root == nil {
			continue
		}
		var one bytes.Buffer
		enc := yaml.NewEncoder(&one)
		enc.SetIndent(2)
		if err := enc.Encode(root); err != nil {
			return nil, fmt.Errorf("document: encoding document %d: %w", n, err)
		}
		if err := enc.Close(); err != nil {
			return nil, fmt.Errorf("document: closing the encoder for document %d: %w", n, err)
		}
		buf.Write(one.Bytes())
	}
	return buf.Bytes(), nil
}
