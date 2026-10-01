package ingest

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/provider"
)

// declaredRules is every Rule constant in refusal.go, read from its syntax so that a rule added
// there without a sweep case fails TestRefusalSweep.
func declaredRules(t *testing.T) []Rule {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "refusal.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []Rule
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, s := range g.Specs {
			v := s.(*ast.ValueSpec)
			if id, ok := v.Type.(*ast.Ident); !ok || id.Name != "Rule" {
				continue
			}
			for _, val := range v.Values {
				lit := val.(*ast.BasicLit)
				out = append(out, Rule(strings.Trim(lit.Value, `"`)))
			}
		}
	}
	return out
}

// TestRefusalSweep: every refusal rule, reached through Extract with a distinctive value in the
// input, names its rule and quotes no input value under any rendering (compilation.md §13).
func TestRefusalSweep(t *testing.T) {
	const s = secretText
	decl := func(name string, r Reference) Declarations {
		return Declarations{References: map[string]Reference{name: r}}
	}
	cases := []struct {
		rule   Rule
		text   string
		marks  []string
		decl   Declarations
		secret string // what must not appear; secretText when empty
	}{
		{rule: RuleParse, text: "machine:\n  token: *" + s + "\n"},
		{rule: RuleSchemaUnloadable, text: "apiVersion: v1alpha1\nkind: Nope\nx: " + s + "\n"},
		{rule: RuleSchemaIndirect, text: "cluster:\n  secretboxEncryptionSecret: !!binary " + b64Secret + "\n", secret: b64Secret},
		{rule: RuleMarkUnaddressed, text: "machine:\n  token: " + s + "\n", marks: []string{"doc[0]/machine/nope"}},
		{rule: RuleMarkKind, text: "machine:\n  certSANs: [" + s + "]\n", marks: []string{"doc[0]/machine/certSANs"}},
		{rule: RuleBadPath, text: "machine:\n  type: " + s + "\n", marks: []string{"doc[0]/machine/type|yaml/a"}},
		{rule: RuleGuardValue, text: "machine:\n  token: " + s + "\n  nodeLabels:\n    " + s + ": x\n"},
		{rule: RuleGuardSubstring, text: "machine:\n  token: " + s + "\n  nodeLabels:\n    k-" + s + ": x\n"},
		{rule: RuleReservedText, text: "machine:\n  nodeLabels:\n    k: \"!bwref " + s + "\"\n"},
		{rule: RuleLocalTag, text: "machine:\n  token: !secret " + s + "\n"},
		{rule: RuleLocalTag, text: "machine:\n  token: " + s + "\n  nodeLabels:\n    " + s + ": !unknown x\n"},
		{rule: RuleLocalTag, text: "machine:\n  nodeLabels:\n    a: " + s + "\n    " + s + ": !unknown x\n", marks: []string{"doc[0]/machine/nodeLabels/a"}},
		{rule: RuleMarkUnaddressed, text: "machine:\n  token: " + s + "\n", marks: []string{"doc[0]/machine/nodeLabels/" + s}},
		// The machinery cannot load the document, so its secret fields are unknown.
		{rule: RuleLocalTag, text: "machine:\n  token: " + s + "\n  type: []\n  nodeLabels:\n    " + s + ": !unknown x\n"},
		// The machinery's value is the decoded form of the input text.
		{rule: RuleLocalTag, text: "cluster:\n  secretboxEncryptionSecret: !!binary " + b64Secret + "\nmachine:\n  nodeLabels:\n    " + b64Secret + ": !unknown x\n", secret: b64Secret},
		// The key holding the value is escaped in the path, and the path does not parse back.
		{rule: RuleLocalTag, text: "machine:\n  token: " + s + "/x\n  nodeLabels:\n    " + s + "/x|y: !unknown x\n"},
		// A key equal to the value as a parsed integer or boolean, spelled differently.
		{rule: RuleGuardValue, text: manifestStream("secret: 314159\n0x4cb2f: x\n"), secret: "0x4cb2f",
			marks: []string{manifestPath + "|yaml/secret"}, decl: Declarations{Embedded: []Embedded{{Path: manifestPath, Format: "yaml"}}}},
		{rule: RuleGuardValue, text: manifestStream("secret: true\nTRUE: x\n"), secret: "TRUE",
			marks: []string{manifestPath + "|yaml/secret"}, decl: Declarations{Embedded: []Embedded{{Path: manifestPath, Format: "yaml"}}}},
		// The marked value is reached through an alias to a mapping.
		{rule: RuleLocalTag, text: manifestStream("source: &a {password: " + s + "}\ntarget: {nested: *a}\n" + s + ": !unknown x\n"),
			marks: []string{manifestPath + "|yaml/target"}, decl: Declarations{Embedded: []Embedded{{Path: manifestPath, Format: "yaml"}}}},
		{rule: RuleTagPlacement, text: "machine:\n  certSANs: !bwref [" + s + "]\n", decl: decl("s-x", str(provider.KindString))},
		{rule: RuleUndeclaredName, text: "machine:\n  token: !bwref s-x\n  type: " + s + "\n"},
		{rule: RuleUnusedName, text: "machine:\n  token: " + s + "\n", decl: decl("s-x", str(provider.KindString))},
		{rule: RuleBadName, text: "machine:\n  token: !bwref " + s + "_\n"},
		{rule: RuleBadDeclaration, text: "machine:\n  token: !bwref s-x\n  type: " + s + "\n", decl: decl("s-x", Reference{Kind: "list", Version: 1})},
		{rule: RuleEmbedded, text: "machine:\n  nodeLabels:\n    k: " + s + "\n",
			decl: Declarations{Embedded: []Embedded{{Path: "doc[0]/machine/nodeLabels", Format: "yaml"}}}},
	}
	covered := map[Rule]bool{}
	for _, tc := range cases {
		covered[tc.rule] = true
		t.Run(string(tc.rule), func(t *testing.T) {
			secret := tc.secret
			if secret == "" {
				secret = s
			}
			if !strings.Contains(tc.text, secret) {
				t.Fatal("the case does not carry the value it checks for")
			}
			req := request(t, tc.text, tc.marks...)
			req.Declarations = tc.decl
			c, err := Extract(req)
			var r *Refusal
			if !errors.As(err, &r) || r.Rule != tc.rule || c != nil {
				t.Fatalf("got %v, want a %s refusal and no candidate", err, tc.rule)
			}
			for _, text := range []string{err.Error(), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", r)} {
				if strings.Contains(text, secret) {
					t.Errorf("the refusal quotes the input: %s", text)
				}
			}
		})
	}
	t.Run("every rule has a case", func(t *testing.T) {
		rules := declaredRules(t)
		if len(rules) == 0 {
			t.Fatal("control: no Rule constants read from refusal.go")
		}
		for _, r := range rules {
			if !covered[r] {
				t.Errorf("rule %s has no sweep case", r)
			}
		}
	})
}
