package ingest

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/provider"
)

// The typed reference to the cluster's CA key (compilation.md §5.1, §5.2, choice §16.40).

const caKeyDoc = "version: v1alpha1\nmachine:\n  type: controlplane\n  ca:\n    key: !bwref talos/ca-key\n"

// A synthetic stand-in for a PEM key: no real key material.
const syntheticCAKey = "-----BEGIN SYNTHETIC KEY-----\nc3ludGhldGljLWNhLWtleS1mb3ItdGVzdHM=\n-----END SYNTHETIC KEY-----\n"

func caKeyDecl(name string, r Reference) Declarations {
	return Declarations{References: map[string]Reference{name: r}}
}

func ruleOf(t *testing.T, err error) Rule {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("not a refusal: %v", err)
	}
	return r.Rule
}

// A stored import base holds the typed reference, and resolution places the key base64 encoded.
func TestCAKeyReferenceStoredAndResolved(t *testing.T) {
	decl := caKeyDecl(CAKeyName, Reference{Kind: provider.KindCAKey})
	s, err := Stored(caKeyDoc, decl)
	if err != nil {
		t.Fatalf("stored: %v", err)
	}
	v, err := provider.NewValue(provider.KindString, syntheticCAKey)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Resolve(s, map[string]provider.Value{CAKeyName: v})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := "key: " + base64.StdEncoding.EncodeToString([]byte(syntheticCAKey)); !strings.Contains(string(r.bytes()), want) {
		t.Fatal("the resolved document does not hold the key's base64 form")
	}
}

// The kind stands only under its fixed name, with no version or encoding, at machine.ca.key, once.
func TestCAKeyReferenceShape(t *testing.T) {
	ok := Reference{Kind: provider.KindCAKey}
	for name, c := range map[string]struct {
		doc  string
		decl Declarations
		rule Rule
	}{
		"another name": {strings.Replace(caKeyDoc, CAKeyName, "talos/other", 1), caKeyDecl("talos/other", ok), RuleBadDeclaration},
		"a version":    {caKeyDoc, caKeyDecl(CAKeyName, Reference{Kind: provider.KindCAKey, Version: 1}), RuleBadDeclaration},
		"an encoding":  {caKeyDoc, caKeyDecl(CAKeyName, Reference{Kind: provider.KindCAKey, Encoding: "base64"}), RuleBadDeclaration},
		"another path": {"machine:\n  token: !bwref talos/ca-key\n", caKeyDecl(CAKeyName, ok), RuleTagPlacement},
		"aliased":      {"machine:\n  ca:\n    key: &k !bwref talos/ca-key\n  token: *k\n", caKeyDecl(CAKeyName, ok), RuleTagPlacement},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Stored(c.doc, c.decl); err == nil {
				t.Fatal("stored")
			}
			docs, err := parseStream([]byte(c.doc))
			if err != nil {
				t.Fatal(err)
			}
			if got := ruleOf(t, validate(docs, c.decl)); got != c.rule {
				t.Fatalf("rule %s, want %s", got, c.rule)
			}
		})
	}
}

// No request creates or places the reference: extraction is the one entry of every request
// (import, drift adoption, draft update, mark). Red against the widened declarations without the
// guard.
func TestCAKeyReferenceRefusedFromRequests(t *testing.T) {
	req := request(t, caKeyDoc)
	req.Declarations = caKeyDecl(CAKeyName, Reference{Kind: provider.KindCAKey})
	_, err := Extract(req)
	if got := ruleOf(t, err); got != RuleBadDeclaration {
		t.Fatalf("rule %s, want %s", got, RuleBadDeclaration)
	}
}

// Guard cases, green before and after the kind exists: a request document that carries the tag
// with no declaration, or the reserved text, is refused.
func TestCAKeyTagInRequestDocument(t *testing.T) {
	for name, c := range map[string]struct {
		doc  string
		rule Rule
	}{
		"the tag":           {caKeyDoc, RuleUndeclaredName},
		"the reserved text": {"version: v1alpha1\nmachine:\n  type: controlplane\n  token: \"!bwref talos/ca-key\"\n", RuleReservedText},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Extract(request(t, c.doc))
			if got := ruleOf(t, err); got != c.rule {
				t.Fatalf("rule %s, want %s", got, c.rule)
			}
		})
	}
}
