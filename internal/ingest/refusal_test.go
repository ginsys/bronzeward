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
		remark string // "author" or "widened": the text is staged unmarked (re-indented when widened) and the marks re-mark it
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
		// An outer key holding "|" has no path, so it is refused before any path names it.
		{rule: RuleParse, text: "machine:\n  token: " + s + "/x\n  nodeLabels:\n    " + s + "/x|y: !unknown x\n"},
		{rule: RuleParse, text: "machine:\n  token: " + s + "|yaml\n  nodeLabels:\n    " + s + "|yaml: !unknown x\n"},
		{rule: RuleParse, text: "machine:\n  token: " + s + "|json\n  nodeLabels:\n    " + s + "|json: !unknown x\n"},
		// A key equal to the value as a parsed number, spelled as a float.
		{rule: RuleGuardValue, text: manifestStream("secret: 314159\n3.14159e5: x\n"), secret: "3.14159e5",
			marks: []string{manifestPath + "|yaml/secret"}, decl: Declarations{Embedded: []Embedded{{Path: manifestPath, Format: "yaml"}}}},
		{rule: RuleGuardValue, text: manifestStream("secret: 314159\n!!float 0x4cb2f: x\n"), secret: "0x4cb2f",
			marks: []string{manifestPath + "|yaml/secret"}, decl: Declarations{Embedded: []Embedded{{Path: manifestPath, Format: "yaml"}}}},
		// The key's explicit tag makes it equal to the value; its text alone reads as another number.
		{rule: RuleGuardValue, text: manifestStream("secret: 9007199254740992\n!!float 0x20000000000001: x\n"), secret: "0x20000000000001",
			marks: []string{manifestPath + "|yaml/secret"}, decl: Declarations{Embedded: []Embedded{{Path: manifestPath, Format: "yaml"}}}},
		{rule: RuleLocalTag, text: manifestStream("secret: 9007199254740992\n!!float 0x20000000000001: !unknown x\n"), secret: "0x20000000000001",
			marks: []string{manifestPath + "|yaml/secret"}, decl: Declarations{Embedded: []Embedded{{Path: manifestPath, Format: "yaml"}}}},
		{rule: RuleGuardValue, text: manifestStream("secret: \"314159\"\n0x4cb2f: x\n"), secret: "0x4cb2f",
			marks: []string{manifestPath + "|yaml/secret"}, decl: Declarations{Embedded: []Embedded{{Path: manifestPath, Format: "yaml"}}}},
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
		{rule: RuleMarkRewritesText, remark: "author", text: manifestStream(secretManifest), marks: []string{manifestPath + "|yaml/stringData/password"},
			decl: Declarations{Embedded: []Embedded{{Path: manifestPath, Format: "yaml"}}}},
		// The refusal names the first mark, whose key is a value the second extracts.
		{rule: RuleMarkRewritesText, remark: "widened", text: "machine:\n  nodeLabels:\n    a: " + s + "\n    " + s + ": x\n",
			marks: []string{"doc[0]/machine/nodeLabels/" + s, "doc[0]/machine/nodeLabels/a"}},
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
			var c *Candidate
			var err error
			if tc.remark == "" {
				c, err = Extract(req)
			} else {
				st := stagedFrom(t, Request{Input: req.Input, Declarations: tc.decl})
				if tc.remark == "widened" {
					wide := strings.ReplaceAll(string(st.Sanitized.Documents()), "\n  ", "\n    ")
					st.Sanitized = newSanitized([]byte(wide), st.Sanitized.Declarations())
				}
				c, err = Remark(st, req.Marks)
			}
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
