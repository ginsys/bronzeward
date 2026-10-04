package compile

import (
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

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

// A refusal names paths only, and a path token that holds a resolved value, such as a copy written
// as its own key, is redacted (compilation.md §8.3).
func TestCompileRefusalRedactsValueTokens(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	const six = "ab12xy"
	f0 := source(t, "machine:\n  nodeAnnotations:\n    s: !bwref app/six\n", strRef("app/six"),
		map[string]provider.Value{"app/six": value(t, provider.KindString, six)})
	f1 := source(t, "machine:\n  nodeAnnotations:\n    "+six+": "+six+"\n", ingest.Declarations{}, nil)
	e := refusal(t, Input{Base: base, Fragments: []Source{f0, f1}, Mode: ModeMetal}, RuleCopy, six)
	if !slices.Equal(e.Paths, []string{"doc[0]/machine/nodeAnnotations/<redacted>"}) {
		t.Errorf("refused at %v, want the redacted copy path", e.Paths)
	}
}

// A refusal raised inside resolution and wrapped with the input's name is redacted in its message
// too, not only in its paths (compilation.md §8.3).
func TestCompileRefusalRedactsWrappedMessage(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	const six = "ab12xy"
	f := source(t, "machine:\n  nodeAnnotations:\n    s: !bwref app/six\n    "+six+"-k: !bwref app/gone\n",
		refs(map[string]ingest.Reference{"app/six": ref(provider.KindString), "app/gone": ref(provider.KindString)}),
		map[string]provider.Value{"app/six": value(t, provider.KindString, six)})
	_, err := Compile(Input{Base: base, Fragments: []Source{f}, Mode: ModeMetal})
	var r *ingest.Refusal
	if !errors.As(err, &r) || !slices.Equal(r.Paths, []string{"doc[0]/machine/nodeAnnotations/<redacted>"}) {
		t.Fatalf("got %v, want a refusal at the redacted path", err)
	}
	if s := fmt.Sprintf("%s %v %+v", err.Error(), err, err); strings.Contains(s, six) {
		t.Errorf("the refusal message holds the value: %s", s)
	}
	if !strings.HasPrefix(err.Error(), "compile: fragment[0]: ") {
		t.Errorf("the message %q lost the input it names", err.Error())
	}
}

// The redactor replaces a path token equal to a resolved string, a mapping member or key, in its
// stored or base64 form, or containing one of six bytes or more; other tokens, integers and
// booleans stay, and an unparseable path is redacted whole (compilation.md §8.3).
func TestRedactorPaths(t *testing.T) {
	r := newRedactor([]Source{{Values: map[string]provider.Value{
		"s":   value(t, provider.KindString, "abc"),
		"l":   value(t, provider.KindString, "long-value"),
		"m":   value(t, provider.KindMapping, map[string]any{"mkey": "mval", "": 7}),
		"i":   value(t, provider.KindInteger, 6443),
		"b":   value(t, provider.KindBoolean, true),
		"enc": value(t, provider.KindString, "wxyz"),
	}}})
	enc := base64.StdEncoding.EncodeToString([]byte("wxyz"))
	for in, want := range map[string]string{
		"doc[0]/machine/abc":                        "doc[0]/machine/<redacted>",
		"doc[0]/machine/abcd":                       "doc[0]/machine/abcd",
		"doc[0]/x-long-value-y/a":                   "doc[0]/<redacted>/a",
		"doc[0]/mkey/mval":                          "doc[0]/<redacted>/<redacted>",
		"doc[0]/a//b":                               "doc[0]/a/<redacted>/b",
		"doc[0]/6443/true":                          "doc[0]/6443/true",
		"doc[0]/" + enc:                             "doc[0]/<redacted>",
		"doc[1]/m|json/abc":                         "doc[1]/m|json/<redacted>",
		"doc[0]/cluster/inlineManifests/0/contents": "doc[0]/cluster/inlineManifests/0/contents",
		"not a path":                                "<redacted>",
	} {
		if got := r.path(in); got != want {
			t.Errorf("path(%q) = %q, want %q", in, got, want)
		}
	}
	opaque := newRedactor([]Source{{Values: map[string]provider.Value{"bad": {}}}})
	if got := opaque.path("doc[0]/machine"); got != "<redacted>" {
		t.Errorf("with an undecodable value: %q, want the path redacted whole", got)
	}
}

// A stand-in too short to hold an id is found only where a leaf equals it, so an overridden base
// reference of three bytes is named as overridden, not attributed to the longer stand-ins that
// hold its letters; two such references of one shape cannot be told apart and are refused
// (compilation.md §8.1).
func TestCompileShortStandIns(t *testing.T) {
	text := mutate(t, generatedBase(t), func(m *yaml.Node) {
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "nodeAnnotations"},
			&yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{{Kind: yaml.ScalarNode, Value: "a"}, {Kind: yaml.ScalarNode, Value: "abc"}}})
	})
	base := source(t, string(text), ingest.Declarations{}, nil, "doc[0]/machine/nodeAnnotations/a")
	override := source(t, "machine:\n  nodeAnnotations:\n    a: literal-text\n", ingest.Declarations{}, nil)
	e := refusal(t, Input{Base: base, Fragments: []Source{override}, Mode: ModeMetal}, RuleBaseOverride, "abc")
	if e.Input != "fragment[0]" || !slices.Equal(e.Paths, []string{"doc[0]/machine/nodeAnnotations/a"}) {
		t.Errorf("refused %s at %v, want fragment[0] at the annotation", e.Input, e.Paths)
	}
	same := source(t, "machine:\n  nodeLabels:\n    d: !bwref app/def\n", strRef("app/def"),
		map[string]provider.Value{"app/def": value(t, provider.KindString, "def")})
	_, err := Compile(Input{Base: base, Fragments: []Source{same}, Mode: ModeMetal})
	var ref *ingest.Refusal
	if !errors.As(err, &ref) || ref.Rule != ingest.RuleTraceIndistinct {
		t.Errorf("two three-letter stand-ins: %v, want trace-indistinct", err)
	}
}

// An identified embedded document is walked in the output whether or not a reference stands in
// it, so a copy written there with JSON escapes is refused at its decoded leaf; a mapping member
// under an empty key is still a member, and a copy of it is refused (compilation.md §6 step 7).
func TestCompileCopyChecksEveryLeaf(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	const quoted, member = "secret\"word", "member-secret"
	f0 := source(t, "machine:\n  nodeLabels:\n    q: !bwref app/q\n  nodeAnnotations: !bwref app/m\n",
		refs(map[string]ingest.Reference{"app/q": ref(provider.KindString), "app/m": ref(provider.KindMapping)}),
		map[string]provider.Value{
			"app/q": value(t, provider.KindString, quoted),
			"app/m": value(t, provider.KindMapping, map[string]any{"": member}),
		})
	manifest := ingest.Declarations{Embedded: []ingest.Embedded{{Path: "doc[0]/cluster/inlineManifests/0/contents", Format: "json"}}}
	for _, c := range []struct {
		name, text string
		decl       ingest.Declarations
		path       string
	}{
		{"escaped in embedded JSON", "cluster:\n  inlineManifests:\n    - name: j\n      contents: |\n        {\"copy\": \"secret\\\"word\"}\n",
			manifest, "doc[0]/cluster/inlineManifests/0/contents|json/copy"},
		{"member under an empty key", "machine:\n  nodeLabels:\n    copy: " + member + "\n", ingest.Declarations{}, "doc[0]/machine/nodeLabels/copy"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f1 := source(t, c.text, c.decl, nil)
			e := refusal(t, Input{Base: base, Fragments: []Source{f0, f1}, Mode: ModeMetal}, RuleCopy, quoted, member)
			if !slices.Equal(e.Paths, []string{c.path}) {
				t.Errorf("refused at %v, want %s", e.Paths, c.path)
			}
		})
	}
}

// An import base reference aliased to two fields is overridden when a fragment replaces one alias,
// though the other still holds its value: the fragment after which fewer leaves carry it is named
// (compilation.md §8.1). A fragment that leaves both aliases is not an override.
func TestCompileRefusesAnAliasOverride(t *testing.T) {
	const tok = "base-" + compileSecret
	text := mutate(t, generatedBase(t), func(m *yaml.Node) {
		ref := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!bwref", Value: "app/ann", Anchor: "s"}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "nodeAnnotations"},
			&yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "a"}, ref,
				{Kind: yaml.ScalarNode, Value: "b"}, {Kind: yaml.AliasNode, Value: "s", Alias: ref},
			}})
	})
	base := source(t, string(text), strRef("app/ann"), map[string]provider.Value{"app/ann": value(t, provider.KindString, tok)})
	other := source(t, "machine:\n  nodeLabels:\n    unrelated: plain\n", ingest.Declarations{}, nil)
	if _, err := Compile(Input{Base: base, Fragments: []Source{other}, Mode: ModeMetal}); err != nil {
		t.Fatalf("no override: %v", err)
	}
	override := source(t, "machine:\n  nodeAnnotations:\n    b: literal-text\n", ingest.Declarations{}, nil)
	e := refusal(t, Input{Base: base, Fragments: []Source{other, override}, Mode: ModeMetal}, RuleBaseOverride, tok)
	if e.Input != "fragment[1]" || !slices.Equal(e.Paths, []string{"doc[0]/machine/nodeAnnotations/a"}) {
		t.Errorf("refused %s at %v, want fragment[1] at the reference", e.Input, e.Paths)
	}
}

// A base literal that equals a short reference's stand-in is not that reference: a fragment that
// replaces the literal overrides nothing (compilation.md §8.1).
func TestCompileStandInLiteralIsNotAnOverride(t *testing.T) {
	text := mutate(t, generatedBase(t), func(m *yaml.Node) {
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "nodeAnnotations"},
			&yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "a"}, {Kind: yaml.ScalarNode, Value: "abcd"},
				{Kind: yaml.ScalarNode, Value: "b"}, {Kind: yaml.ScalarNode, Value: "xxxx"},
			}})
	})
	base := source(t, string(text), ingest.Declarations{}, nil, "doc[0]/machine/nodeAnnotations/a")
	f := source(t, "machine:\n  nodeAnnotations:\n    b: plain\n", ingest.Declarations{}, nil)
	if _, err := Compile(Input{Base: base, Fragments: []Source{f}, Mode: ModeMetal}); err != nil {
		t.Errorf("replacing a literal shaped like a stand-in: %v", err)
	}
}

// A mapping reference's keys are values the provider holds (compilation.md §4.2), so a key of six
// bytes or more written as a literal elsewhere is a copy, whatever its member's kind.
func TestCompileRefusesAMappingKeyCopy(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	const key = "key-" + compileSecret
	f0 := source(t, "machine:\n  nodeAnnotations: !bwref app/m\n", refs(map[string]ingest.Reference{"app/m": ref(provider.KindMapping)}),
		map[string]provider.Value{"app/m": value(t, provider.KindMapping, map[string]any{key: "v"})})
	f1 := source(t, "machine:\n  nodeLabels:\n    copy: "+key+"\n", ingest.Declarations{}, nil)
	if e := refusal(t, Input{Base: base, Fragments: []Source{f0, f1}, Mode: ModeMetal}, RuleCopy, key); !slices.Equal(e.Paths, []string{"doc[0]/machine/nodeLabels/copy"}) {
		t.Errorf("refused at %v, want the copy's path", e.Paths)
	}
}

// An output mapping key holding a resolved value is a copy, by its path with the key redacted,
// whether its value is a literal or another reference; the keys a mapping reference places are
// not (TestCompileRefusesAMappingKeyCopy).
func TestCompileRefusesAKeyCopy(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	f0 := source(t, "machine:\n  nodeAnnotations:\n    m: !bwref app/s\n", strRef("app/s"),
		map[string]provider.Value{"app/s": value(t, provider.KindString, compileSecret)})
	v := map[string]provider.Value{"app/v": value(t, provider.KindString, "other-value-1")}
	for name, f1 := range map[string]Source{
		"literal value":   source(t, "machine:\n  nodeLabels:\n    "+compileSecret+": v\n", ingest.Declarations{}, nil),
		"reference value": source(t, "machine:\n  nodeLabels:\n    "+compileSecret+": !bwref app/v\n", strRef("app/v"), v),
		"contained":       source(t, "machine:\n  nodeLabels:\n    x-"+compileSecret+": v\n", ingest.Declarations{}, nil),
	} {
		e := refusal(t, Input{Base: base, Fragments: []Source{f0, f1}, Mode: ModeMetal}, RuleCopy, compileSecret)
		if !slices.Equal(e.Paths, []string{"doc[0]/machine/nodeLabels/<redacted>"}) {
			t.Errorf("%s: refused at %v, want the key's path", name, e.Paths)
		}
	}
}

// A string placed in a byte field the machinery re-encodes canonically is looked for in that
// re-encoding too: a literal holding the canonical spelling of a non-canonical value is a copy
// (compilation.md §6 step 7, §8.1 Canonical base64).
func TestCompileRefusesACanonicalCopy(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	const stored, canonical = "c2VjcmV0LWtleR==", "c2VjcmV0LWtleQ=="
	f0 := source(t, "machine:\n  acceptedCAs:\n    - crt: !bwref app/c\n", strRef("app/c"),
		map[string]provider.Value{"app/c": value(t, provider.KindString, stored)})
	f1 := source(t, "machine:\n  nodeAnnotations:\n    copy: "+canonical+"\n", ingest.Declarations{}, nil)
	e := refusal(t, Input{Base: base, Fragments: []Source{f0, f1}, Mode: ModeMetal}, RuleCopy, stored, canonical)
	if !slices.Equal(e.Paths, []string{"doc[0]/machine/nodeAnnotations/copy"}) {
		t.Errorf("refused at %v, want the copy's path", e.Paths)
	}
}

// A non-ASCII string compiles: its stand-in never splits a character, so the trace pass composes
// as the real one does (compilation.md §8.1).
func TestCompileMultibyteValue(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	for _, v := range []string{"秘密", "abcd秘密", "é-secret"} {
		f := source(t, "machine:\n  nodeAnnotations:\n    m: !bwref app/m\n", strRef("app/m"),
			map[string]provider.Value{"app/m": value(t, provider.KindString, v)})
		if _, err := Compile(Input{Base: base, Fragments: []Source{f}, Mode: ModeMetal}); err != nil {
			t.Errorf("a %d-byte multibyte value: %v", len(v), err)
		}
	}
}

// Two fragments overriding two base references: the refusal names the first and only its paths.
func TestCompileBaseOverrideNamesOneFragment(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	f0 := source(t, "cluster:\n  token: abcdef.0123456789abcdef\n", ingest.Declarations{}, nil)
	f1 := source(t, "machine:\n  token: fedcba.9876543210fedcba\n", ingest.Declarations{}, nil)
	e := refusal(t, Input{Base: base, Fragments: []Source{f0, f1}, Mode: ModeMetal}, RuleBaseOverride)
	if e.Input != "fragment[0]" || !slices.Equal(e.Paths, []string{"doc[0]/cluster/token"}) {
		t.Errorf("refused %s at %v, want fragment[0] at doc[0]/cluster/token only", e.Input, e.Paths)
	}
}
