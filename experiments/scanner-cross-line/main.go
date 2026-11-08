// Command oracle reads a set of YAML documents and writes, for each, what go-yaml v3.0.5 (the
// server's pin, go.mod) lists as its !bwref references. It is the reference the web scanner
// (web/src/position.ts) is compared with.
//
// Usage: go run . <set.json> <result.json>
//
// <set.json> is a JSON array of strings, one document (a stream of one or more YAML documents)
// each; an element may also be an object whose "doc" holds the stream (sets/cases.json). <result.json> is a JSON array of {"err": "", "tags": []}, one per input, in order. "err"
// is the decoder's error for a stream go-yaml refuses; "tags" are then empty, because the server
// reads no reference from a stream it refuses. Otherwise "tags" holds the value of every scalar
// tagged !bwref in the stream whose value is a name by the server's grammar, in document order,
// each once.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

const refTag = "!bwref"

// Result is the oracle's reading of one stream.
type Result struct {
	Err  string   `json:"err"`
	Tags []string `json:"tags"`
	// Unfit is true when the server would refuse the stream for what it reads about !bwref
	// (internal/ingest/declare.go): a tagged value that is no name (bad name), a scalar whose
	// text holds the tag (reserved text), or a tag on a mapping key (tag-placement). The web
	// scanner can show a reference there that the server never accepts.
	Unfit bool `json:"unfit"`
	// Bad holds the tagged values that are no name. With Tags it is every value go-yaml reads
	// under the tag (the raw references); a scanner that lists names only never lists these.
	Bad []string `json:"bad"`
}

// References lists the !bwref values of the stream, or the error go-yaml refuses it with.
func References(doc string) Result {
	dec := yaml.NewDecoder(bytes.NewReader([]byte(doc)))
	var found []string
	unfit := false
	bad := []string{}
	seen := map[string]bool{}
	aliased := map[*yaml.Node]bool{}
	var listed []*yaml.Node
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Result{Err: err.Error(), Tags: []string{}}
		}
		walk(&n, &unfit, &bad, aliased, &listed)
	}
	// An alias key that repeats the anchor of a tagged node makes the server refuse the stream
	// (tag-placement), so the editor lists no name for that node. The alias may stand in a later
	// document than the node, so the filter runs once the whole stream is read
	// (ginsys/bronzeward#672).
	for _, n := range listed {
		if !aliased[n] && !seen[n.Value] {
			seen[n.Value] = true
			found = append(found, n.Value)
		}
	}
	if found == nil {
		found = []string{}
	}
	return Result{Tags: found, Unfit: unfit, Bad: bad}
}

// nameSyntax is the server's name grammar (internal/ingest/declare.go nameSyntax, §5.1). A tagged
// value outside it is no reference: the server refuses it as a bad name, and the web scanner
// lists names only.
var nameSyntax = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(/[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// walk visits every node once. An alias is not followed: the anchored node was visited where it
// stands.
//
// A scalar that carries the tag in key position, explicit or implicit, lists no name and marks the
// document unfit. The server refuses it (tag-placement, internal/ingest/declare.go: the test
// `key || n.Kind != yaml.ScalarNode || parent == nil`), and the decision of
// ginsys/bronzeward#629 (option A) is that the editor lists no name there. An alias key whose anchor
// is such a key is refused too (the alias branch of the same function). The oracle reads it the
// same way, so a scanner that lists nothing for these keys is equal to it
// (ginsys/bronzeward#637).
//
// The alias branch refuses any alias key whose anchored node carries the tag, whether that node is a
// key or a value (`a: &k !bwref x/foo`, then `*k : v`). The oracle marks the stream unfit and puts
// the anchored node in aliased, so the caller lists no name for it (ginsys/bronzeward#672). The
// nodes that carry a listable name go in listed, in stream order, for that filter.
func walk(n *yaml.Node, unfit *bool, bad *[]string, aliased map[*yaml.Node]bool, listed *[]*yaml.Node) {
	walkNode(n, false, unfit, bad, aliased, listed)
}

// walkNode is walk for one node. key is true for a mapping key.
func walkNode(n *yaml.Node, key bool, unfit *bool, bad *[]string, aliased map[*yaml.Node]bool, listed *[]*yaml.Node) {
	if n.Kind == yaml.AliasNode {
		if key && n.Alias != nil && n.Alias.Tag == refTag {
			aliased[n.Alias] = true
			*unfit = true
		}
		return
	}
	switch {
	case key && n.Kind == yaml.ScalarNode && n.Tag == refTag:
		*unfit = true
	case n.Tag == refTag && nameSyntax.MatchString(n.Value):
		*listed = append(*listed, n)
	case n.Tag == refTag:
		*unfit = true
		*bad = append(*bad, n.Value)
	case n.Kind == yaml.ScalarNode && strings.Contains(n.Value, refTag):
		*unfit = true
	}
	for i, c := range n.Content {
		walkNode(c, n.Kind == yaml.MappingNode && i%2 == 0, unfit, bad, aliased, listed)
	}
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: go run . <set.json> <result.json>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// An element is a string, or an object with the document in "doc" (sets/cases.json).
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		fmt.Fprintln(os.Stderr, "set:", err)
		os.Exit(1)
	}
	docs := make([]string, len(elems))
	for i, e := range elems {
		var obj struct {
			Doc string `json:"doc"`
		}
		if json.Unmarshal(e, &docs[i]) != nil {
			if err := json.Unmarshal(e, &obj); err != nil {
				fmt.Fprintln(os.Stderr, "set element", i, ":", err)
				os.Exit(1)
			}
			docs[i] = obj.Doc
		}
	}
	out := make([]Result, len(docs))
	for i, d := range docs {
		out[i] = References(d)
	}
	b, err := json.Marshal(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[2], append(b, '\n'), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
