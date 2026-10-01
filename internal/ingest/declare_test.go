package ingest

import (
	"errors"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/provider"
)

func TestNameGrammar(t *testing.T) {
	for _, ok := range []string{"a", "s-abc", "registry/example-pass", "a1/b-2/c", "0"} {
		if !validName(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "A", "-a", "a-", "a//b", "/a", "a/", "a_b", "a.b", "a b", "é", "a/-b"} {
		if validName(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func str(k provider.Kind) Reference { return Reference{Kind: k, Version: 1} }

// TestValidateAuthoring: compilation.md §5 and the §13 authoring row. Every case carries the
// test secret in the input, and the refusal must name the rule and not quote it.
func TestValidateAuthoring(t *testing.T) {
	refs := func(names ...string) Declarations {
		d := Declarations{References: map[string]Reference{}}
		for _, n := range names {
			d.References[n] = str(provider.KindString)
		}
		return d
	}
	for _, tc := range []struct {
		name string
		yaml string
		decl Declarations
		rule Rule // "" accepts
	}{
		{"declared reference", "a: !bwref s-one\nb: " + secretText + "\n", refs("s-one"), ""},
		{"reference in a list and document 1", "a: x\n---\nl: [!bwref s-one]\n", refs("s-one"), ""},
		{"alias of a reference", "a: &x !bwref s-one\nb: *x\n", refs("s-one"), ""},
		{"undeclared name", "a: !bwref s-two\nb: " + secretText + "\n", refs("s-one"), RuleUndeclaredName},
		{"unused declaration", "a: " + secretText + "\n", refs("s-one"), RuleUnusedName},
		{"bad name", "a: !bwref S_x\n", Declarations{References: map[string]Reference{"S_x": str(provider.KindString)}}, RuleBadName},
		{"other local tag", "a: !secret " + secretText + "\n", Declarations{}, RuleLocalTag},
		{"python tag", "a: !!python/str " + secretText + "\n", Declarations{}, RuleLocalTag},
		{"tag on a key", "? !bwref s-one\n: " + secretText + "\n", refs("s-one"), RuleTagPlacement},
		{"tag on a sequence", "a: !bwref [" + secretText + "]\n", refs("s-one"), RuleTagPlacement},
		{"tag on a mapping", "a: !bwref {k: " + secretText + "}\n", refs("s-one"), RuleTagPlacement},
		{"alias of a reference as a key", "a: &t !bwref s-one\n? *t\n: " + secretText + "\n", refs("s-one"), RuleTagPlacement},
		{"reference as the document", "!bwref s-one\n", refs("s-one"), RuleTagPlacement},
		{"reference as an embedded document", "a: \"!bwref s-one\"\n",
			Declarations{References: refs("s-one").References, Embedded: []Embedded{{Path: "doc[0]/a", Format: "yaml"}}}, RuleTagPlacement},
		{"reserved text in a value", "a: \"!bwref " + secretText + "\"\n", Declarations{}, RuleReservedText},
		{"reserved text in a key", "\"x !bwref\": " + secretText + "\n", Declarations{}, RuleReservedText},
		{"reserved text in unidentified embedded text", "a: |\n  b: !bwref s-one\n  c: " + secretText + "\n", refs("s-one"), RuleReservedText},
		{"identified embedded yaml", "a: |\n  b: !bwref s-one\n  c: " + secretText + "\n",
			Declarations{References: refs("s-one").References, Embedded: []Embedded{{Path: "doc[0]/a", Format: "yaml"}}}, ""},
		{"identified embedded json authored as yaml", "a: '{\"b\": !bwref s-one}'\n",
			Declarations{References: refs("s-one").References, Embedded: []Embedded{{Path: "doc[0]/a", Format: "json"}}}, ""},
		{"embedded reference undeclared", "a: |\n  b: !bwref s-two\n",
			Declarations{Embedded: []Embedded{{Path: "doc[0]/a", Format: "yaml"}}}, RuleUndeclaredName},
		{"embedded path holds no string", "a: {b: " + secretText + "}\n",
			Declarations{Embedded: []Embedded{{Path: "doc[0]/a", Format: "yaml"}}}, RuleEmbedded},
		{"embedded path addresses nothing", "a: x\n",
			Declarations{Embedded: []Embedded{{Path: "doc[0]/b", Format: "yaml"}}}, RuleEmbedded},
		{"embedded document does not parse", "a: \"b: [" + secretText + "\"\n",
			Declarations{Embedded: []Embedded{{Path: "doc[0]/a", Format: "yaml"}}}, RuleEmbedded},
		{"embedded bad format", "a: x\n", Declarations{Embedded: []Embedded{{Path: "doc[0]/a", Format: "toml"}}}, RuleBadDeclaration},
		{"embedded bad path", "a: x\n", Declarations{Embedded: []Embedded{{Path: "a", Format: "yaml"}}}, RuleBadPath},
		{"embedded twice", "a: x\n", Declarations{Embedded: []Embedded{{Path: "doc[0]/a", Format: "yaml"}, {Path: "doc[0]/a", Format: "yaml"}}}, RuleBadDeclaration},
		{"bad kind", "a: !bwref s-one\n", Declarations{References: map[string]Reference{"s-one": {Kind: "list", Version: 1}}}, RuleBadDeclaration},
		{"version 0", "a: !bwref s-one\n", Declarations{References: map[string]Reference{"s-one": {Kind: provider.KindString}}}, RuleBadDeclaration},
		{"base64 on an integer", "a: !bwref s-one\n", Declarations{References: map[string]Reference{"s-one": {Kind: provider.KindInteger, Version: 1, Encoding: "base64"}}}, RuleBadDeclaration},
		{"unknown encoding", "a: !bwref s-one\n", Declarations{References: map[string]Reference{"s-one": {Kind: provider.KindString, Version: 1, Encoding: "hex"}}}, RuleBadDeclaration},
		{"base64 on a string", "a: !bwref s-one\n", Declarations{References: map[string]Reference{"s-one": {Kind: provider.KindString, Version: 1, Encoding: "base64"}}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs, err := parseStream([]byte(tc.yaml))
			if err != nil {
				t.Fatal(err)
			}
			err = validate(docs, tc.decl)
			if tc.rule == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			var r *Refusal
			if !errors.As(err, &r) || r.Rule != tc.rule {
				t.Fatalf("got %v, want a %s refusal", err, tc.rule)
			}
			if strings.Contains(err.Error(), secretText) {
				t.Fatalf("the refusal quotes the input: %v", err)
			}
		})
	}
}
