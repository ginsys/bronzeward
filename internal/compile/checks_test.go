package compile

import (
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// refusal is Compile's refusal of in, which must be rule; it never quotes any of values.
func refusal(t *testing.T, in Input, rule Rule, values ...string) *Error {
	t.Helper()
	_, err := Compile(in)
	var e *Error
	if !errors.As(err, &e) || e.Rule != rule {
		t.Fatalf("got %v, want a %s refusal", err, rule)
	}
	for _, v := range values {
		if strings.Contains(err.Error(), v) || slices.ContainsFunc(e.Paths, func(p string) bool { return strings.Contains(p, v) }) {
			t.Fatalf("the refusal quotes a value: %v", err)
		}
	}
	return e
}

// A resolved string of six bytes or more, in its stored or placed form, at an output leaf
// provenance does not attribute to its reference is refused by path only; five bytes are below
// the floor, and the leaves attributed to the reference are not copies (compilation.md §6 step 7,
// choice §16.21).
func TestCompileRefusesAnExactCopy(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	const six, five, placed = "ab12xy", "ab12x", "placed-" + compileSecret
	decl := refs(map[string]ingest.Reference{
		"app/six": ref(provider.KindString), "app/five": ref(provider.KindString),
		"app/enc": {Kind: provider.KindString, Version: 1, Encoding: "base64"},
	})
	values := map[string]provider.Value{
		"app/six": value(t, provider.KindString, six), "app/five": value(t, provider.KindString, five),
		"app/enc": value(t, provider.KindString, placed),
	}
	f0 := source(t, "machine:\n  nodeAnnotations:\n    six: &s !bwref app/six\n    alias: *s\n    five: !bwref app/five\n    enc: !bwref app/enc\n", decl, values)
	encoded := base64.StdEncoding.EncodeToString([]byte(placed))
	annotations := "doc[0]/machine/nodeAnnotations/"
	for _, c := range []struct {
		literal string
		refused bool
	}{
		{six, true},
		{"prefix " + six + " suffix", true},
		{five, false},
		{encoded, true},
		{placed, true},
	} {
		f1 := source(t, "machine:\n  nodeAnnotations:\n    copy: \""+c.literal+"\"\n", ingest.Declarations{}, nil)
		in := Input{Base: base, Fragments: []Source{f0, f1}, Mode: ModeMetal}
		if !c.refused {
			if _, err := Compile(in); err != nil {
				t.Errorf("a %d-byte copy: %v, want no refusal", len(c.literal), err)
			}
			continue
		}
		if e := refusal(t, in, RuleCopy, six, placed, encoded); !slices.Equal(e.Paths, []string{annotations + "copy"}) || e.Input != "" {
			t.Errorf("refused at %v, input %q, want the copy's path only", e.Paths, e.Input)
		}
	}
}

// A fragment that overrides a reference of the import base, by a literal, by a delete directive
// or by its own reference, is refused, naming the fragment and the base path (compilation.md §6
// step 7, choice §16.20).
func TestCompileRefusesABaseOverride(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	other := source(t, "machine:\n  nodeLabels:\n    unrelated: plain\n", ingest.Declarations{}, nil)
	tok := map[string]provider.Value{"app/tok": value(t, provider.KindString, "zyxwvu.9876543210zyxwvu")}
	for _, c := range []struct {
		name, text, path string
		decl             ingest.Declarations
		values           map[string]provider.Value
	}{
		{"literal", "machine:\n  token: abcdef.0123456789abcdef\n", "doc[0]/machine/token", ingest.Declarations{}, nil},
		{"delete", "cluster:\n  secretboxEncryptionSecret:\n    $patch: delete\n", "doc[0]/cluster/secretboxEncryptionSecret", ingest.Declarations{}, nil},
		// the check precedes validation: this fragment also makes the configuration invalid
		{"literal in an invalid configuration", "machine:\n  token: abcdef.0123456789abcdef\ncluster:\n  network:\n    dnsDomain: not a domain\n",
			"doc[0]/machine/token", ingest.Declarations{}, nil},
		{"reference", "cluster:\n  token: !bwref app/tok\n", "doc[0]/cluster/token", strRef("app/tok"), tok},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := source(t, c.text, c.decl, c.values)
			e := refusal(t, Input{Base: base, Fragments: []Source{other, f}, Mode: ModeMetal}, RuleBaseOverride, "abcdef.0123456789abcdef", "zyxwvu")
			if e.Input != "fragment[1]" || !slices.Equal(e.Paths, []string{c.path}) {
				t.Errorf("refused %s at %v, want fragment[1] at %s", e.Input, e.Paths, c.path)
			}
		})
	}
}
