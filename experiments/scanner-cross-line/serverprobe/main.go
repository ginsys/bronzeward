// Command serverprobe asks the server's own authoring check about each document of a set. It is the
// "server" column of the tag handle report (ginsys/bronzeward#603).
//
// Usage: go run . <cases.json> <result.json>
//
// <cases.json> is a JSON array of {"doc": "<stream>", "names": ["x/foo"]}. "names" are the names that
// go-yaml reads as !bwref references in the stream (the oracle's "tags"), declared as the server
// requires: it refuses a declared name that no reference uses and a reference with no declaration.
// <result.json> is a JSON array of {"accepted": true|false}, one per input, in order. A document is
// accepted when internal/ingest.Stored takes it as an authored source: it parses, every tag is !bwref
// or a core tag, every reference stands where one may and names a declared name, and no other string
// holds the text !bwref.
//
// This module is separate from the oracle's on purpose. It runs the yaml package that the server
// itself pins, so a later bump of the root module moves this probe and not the oracle.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

type input struct {
	Doc   string   `json:"doc"`
	Names []string `json:"names"`
}

type output struct {
	Accepted bool `json:"accepted"`
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: go run . <cases.json> <result.json>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var in []input
	if err := json.Unmarshal(raw, &in); err != nil {
		fmt.Fprintln(os.Stderr, "cases:", err)
		os.Exit(1)
	}
	out := make([]output, len(in))
	for i, c := range in {
		out[i] = output{Accepted: accepted(c)}
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

func accepted(c input) bool {
	decl := ingest.Declarations{References: map[string]ingest.Reference{}}
	for _, n := range c.Names {
		decl.References[n] = ingest.Reference{Kind: provider.KindString, Version: 1}
	}
	_, err := ingest.Stored(c.Doc, decl)
	return err == nil
}
