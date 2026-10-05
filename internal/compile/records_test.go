package compile

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

func origin(s Source, fragment int) Origin {
	return Origin{Base: fragment < 0, Fragment: fragment, Revision: s.Revision, Digest: s.Digest}
}

// Each occurrence has one record per output path it reached, naming its reference, version,
// encoding, mapping member and source revision, digest and path (compilation.md §8.2). A member is
// named by its position in key order, and a path token holding its key is redacted: the key is a
// value the provider holds (§4.2).
func TestProvenanceRecords(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	frag := source(t, "machine:\n  nodeLabels:\n    s: &a !bwref app/str\n    t: *a\n    e: !bwref app/enc\n"+
		"  nodeAnnotations: !bwref app/map\n  features:\n    rbac: !bwref app/bool\n",
		refs(map[string]ingest.Reference{
			"app/str": ref(provider.KindString), "app/bool": ref(provider.KindBoolean),
			"app/map": {Kind: provider.KindMapping, Version: 3},
			"app/enc": {Kind: provider.KindString, Version: 2, Encoding: "base64"},
		}),
		map[string]provider.Value{
			"app/str":  value(t, provider.KindString, compileSecret),
			"app/bool": value(t, provider.KindBoolean, false),
			"app/map":  value(t, provider.KindMapping, map[string]any{"key-one-7c": "first-" + compileSecret, "key-two-7c": "second-" + compileSecret}),
			"app/enc":  value(t, provider.KindString, compileSecret),
		})
	c := compiled(t, Input{Base: base, Fragments: []Source{frag}, Mode: ModeMetal})
	var got []Record
	for _, r := range c.Provenance() {
		if r.Source.Base {
			if r.Source != origin(base, -1) || r.Output == "" || r.OverriddenBy != nil {
				t.Errorf("base record %+v", r)
			}
			continue
		}
		got = append(got, r)
	}
	fo := origin(frag, 0)
	labels, annotations := "doc[0]/machine/nodeLabels/", "doc[0]/machine/nodeAnnotations"
	want := []Record{
		{Reference: "app/str", Version: 1, Kind: "string", Member: -1, Source: fo, SourcePath: labels + "s", Output: labels + "s"},
		{Reference: "app/str", Version: 1, Kind: "string", Member: -1, Source: fo, SourcePath: labels + "s", Output: labels + "t"},
		{Reference: "app/enc", Version: 2, Kind: "string", Encoding: "base64", Member: -1, Source: fo, SourcePath: labels + "e", Occurrence: 1,
			Output: labels + "e"},
		{Reference: "app/map", Version: 3, Kind: "mapping", Member: 0, Source: fo, SourcePath: annotations, Occurrence: 2, Output: annotations + "/<redacted>"},
		{Reference: "app/map", Version: 3, Kind: "mapping", Member: 1, Source: fo, SourcePath: annotations, Occurrence: 2, Output: annotations + "/<redacted>"},
		{Reference: "app/bool", Version: 1, Kind: "boolean", Member: -1, Source: fo, SourcePath: "doc[0]/machine/features/rbac", Occurrence: 3,
			Output: "doc[0]/machine/features/rbac"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("records\n%+v\nwant\n%+v", got, want)
	}
	if s := fmt.Sprintf("%+v", c.Provenance()); strings.Contains(s, "key-one-7c") || strings.Contains(s, "key-two-7c") {
		t.Errorf("the records hold a mapping key: %s", s)
	}
	maps := 0
	for _, o := range c.Reproduction() {
		if o.Reference == "app/map" {
			maps++
		}
	}
	if maps != 1 {
		t.Errorf("the mapping reference is %d reproduction occurrences, want one", maps)
	}
}

// unlabel deletes machine.nodeLabels, so that a key copying a value never reaches the output.
const unlabel = "machine:\n  nodeLabels:\n    $patch: delete\n"

// A source path token that holds a resolved value, here a key equal to its own reference's value,
// is redacted in the records and the reproduction dependencies alike (compilation.md §8.3). The key
// would be a copy in the output (TestCompileRefusesAKeyCopy), so a later fragment deletes it.
func TestProvenanceRedactsSourcePaths(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	const six = "ab12xy"
	frag := source(t, "machine:\n  nodeLabels:\n    "+six+": !bwref app/six\n", strRef("app/six"),
		map[string]provider.Value{"app/six": value(t, provider.KindString, six)})
	del := source(t, unlabel, ingest.Declarations{}, nil)
	c := compiled(t, Input{Base: base, Fragments: []Source{frag, del}, Mode: ModeMetal})
	const at = "doc[0]/machine/nodeLabels/<redacted>"
	found := 0
	for _, r := range c.Provenance() {
		if r.Reference == "app/six" {
			found++
			if r.SourcePath != at || r.OverriddenBy == nil || r.OverriddenBy.Fragment != 1 {
				t.Errorf("record %+v, want %s overridden by fragment 1", r, at)
			}
		}
	}
	for _, o := range c.Reproduction() {
		if o.Reference == "app/six" {
			found++
			if o.Path != at {
				t.Errorf("reproduction occurrence at %s, want %s", o.Path, at)
			}
		}
	}
	if found != 2 {
		t.Errorf("%d rows for the reference, want one record and one occurrence", found)
	}
	if s := fmt.Sprintf("%+v %+v", c.Provenance(), c.Reproduction()); strings.Contains(s, six) {
		t.Errorf("the records hold the value: %s", s)
	}
	// fmt reaches an unexported field by reflection, past Compiled's own placeholder.
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
		if s := fmt.Sprintf(verb, struct{ c Compiled }{c}); shows(s, six) {
			t.Errorf("%s of a struct holding the compilation renders the value: %s", verb, s)
		}
	}
}

// A path is checked as rendered too: a value equal to the escaped spelling of a key that holds
// none, or spanning two tokens, is not shown.
func TestProvenanceRedactsRenderedPaths(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	for _, c := range []struct{ name, text, value string }{
		{"escaped", "machine:\n  nodeAnnotations:\n    abc/de: !bwref app/s\n", "abc~1de"},
		{"spanning", "machine:\n  nodeAnnotations:\n    abcdef: !bwref app/s\n", "nodeAnnotations/abcd"},
		{"spanning unescaped", "machine:\n  nodeAnnotations:\n    abc/de: !bwref app/s\n", "nodeAnnotations/abc/d"},
		{"short escaped", "machine:\n  nodeAnnotations:\n    a/b: !bwref app/s\n", "a~1b"},
	} {
		frag := source(t, c.text, strRef("app/s"),
			map[string]provider.Value{"app/s": value(t, provider.KindString, c.value)})
		out := compiled(t, Input{Base: base, Fragments: []Source{frag}, Mode: ModeMetal})
		s := fmt.Sprintf("%+v %+v", out.Provenance(), out.Reproduction())
		if plain := strings.NewReplacer("~1", "/", "~0", "~").Replace(s); strings.Contains(s, c.value) || strings.Contains(plain, c.value) {
			t.Errorf("%s: the records hold the value", c.name)
		}
	}
}

// A value shorter than six bytes is looked for as whole tokens, so one spanning a run of tokens is
// not shown either.
func TestProvenanceRedactsShortSpanningValues(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	frag := source(t, "cluster:\n  inlineManifests:\n    - name: y\n      contents: |\n        a:\n          b: !bwref app/s\n",
		ingest.Declarations{
			References: map[string]ingest.Reference{"app/s": ref(provider.KindString)},
			Embedded:   []ingest.Embedded{{Path: "doc[0]/cluster/inlineManifests/0/contents", Format: "yaml"}},
		},
		map[string]provider.Value{"app/s": value(t, provider.KindString, "a/b")})
	out := compiled(t, Input{Base: base, Fragments: []Source{frag}, Mode: ModeMetal})
	if s := fmt.Sprintf("%+v %+v", out.Provenance(), out.Reproduction()); strings.Contains(s, "a/b") {
		t.Error("the records hold the value")
	}
}

// Two occurrences whose source paths redact alike are still two reproduction dependencies.
func TestReproductionKeepsRedactedOccurrences(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	frag := source(t, "machine:\n  nodeLabels:\n    sensitive-a: !bwref app/s\n    sensitive-b: !bwref app/s\n",
		strRef("app/s"), map[string]provider.Value{"app/s": value(t, provider.KindString, "sensitive")})
	del := source(t, unlabel, ingest.Declarations{}, nil)
	c := compiled(t, Input{Base: base, Fragments: []Source{frag, del}, Mode: ModeMetal})
	var got []string
	var numbered []int
	for _, o := range c.Reproduction() {
		if o.Reference == "app/s" {
			got = append(got, o.Path)
			numbered = append(numbered, o.Occurrence)
		}
	}
	at := "doc[0]/machine/nodeLabels/<redacted>"
	if !slices.Equal(got, []string{at, at}) {
		t.Errorf("reproduction paths %v, want two occurrences at %s", got, at)
	}
	// Each is told apart by its position among its source's occurrences, and its provenance
	// names the same position.
	if !slices.Equal(numbered, []int{0, 1}) {
		t.Errorf("reproduction occurrences %v, want [0 1]", numbered)
	}
	var recorded []int
	for _, r := range c.Provenance() {
		if r.Reference == "app/s" {
			recorded = append(recorded, r.Occurrence)
		}
	}
	if slices.Sort(recorded); !slices.Equal(recorded, []int{0, 1}) {
		t.Errorf("provenance occurrences %v, want [0 1]", recorded)
	}
}

// A composition names each source revision once (compilation.md §6 step 1): a revision composed
// twice would be recorded as overridden by itself, and its occurrences numbered twice.
func TestCompileRefusesRevisionComposedTwice(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	frag := source(t, "machine:\n  nodeLabels:\n    a: !bwref app/s\n",
		strRef("app/s"), map[string]provider.Value{"app/s": value(t, provider.KindString, "sensitive")})
	other := source(t, "machine:\n  nodeLabels:\n    b: literal\n", ingest.Declarations{}, nil)
	sameAsBase := frag
	sameAsBase.Revision = base.Revision
	for name, fragments := range map[string][]Source{
		"fragment twice":     {frag, other, frag},
		"fragment then base": {sameAsBase},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Compile(Input{Base: base, Fragments: fragments, Mode: ModeMetal})
			if err == nil || !strings.Contains(err.Error(), "composed twice") {
				t.Fatalf("Compile = %v, want a revision composed twice refused", err)
			}
		})
	}
}

// A reference overwritten by a literal, by another reference or by a delete directive, and a
// boolean overwritten by a literal, is recorded as overridden by the fragment after which it
// disappears, not by the last fragment; an overridden reference is a reproduction dependency
// only (compilation.md §8.1, §8.2, §9).
func TestProvenanceOverrides(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	f0 := source(t, "machine:\n  nodeLabels:\n    a: !bwref app/a\n    b: !bwref app/b\n    c: !bwref app/c\n    keep: !bwref app/keep\n"+
		"  features:\n    rbac: !bwref app/flag\n",
		refs(map[string]ingest.Reference{
			"app/a": ref(provider.KindString), "app/b": ref(provider.KindString), "app/c": ref(provider.KindString),
			"app/keep": ref(provider.KindString), "app/flag": ref(provider.KindBoolean),
		}),
		map[string]provider.Value{
			"app/a": value(t, provider.KindString, "a-"+compileSecret), "app/b": value(t, provider.KindString, "b-"+compileSecret),
			"app/c": value(t, provider.KindString, "c-"+compileSecret), "app/keep": value(t, provider.KindString, "keep-"+compileSecret),
			"app/flag": value(t, provider.KindBoolean, true),
		})
	f1 := source(t, "machine:\n  nodeLabels:\n    a: literal-value\n    b: !bwref app/other\n    c:\n      $patch: delete\n"+
		"  features:\n    rbac: false\n", strRef("app/other"),
		map[string]provider.Value{"app/other": value(t, provider.KindString, "other-"+compileSecret)})
	f2 := source(t, "machine:\n  nodeLabels:\n    unrelated: plain\n", ingest.Declarations{}, nil)
	c := compiled(t, Input{Base: base, Fragments: []Source{f0, f1, f2}, Mode: ModeMetal})
	by := origin(f1, 1)
	var got []Record
	for _, r := range c.Provenance() {
		if !r.Source.Base {
			got = append(got, r)
		}
	}
	labels := "doc[0]/machine/nodeLabels/"
	rbac := "doc[0]/machine/features/rbac"
	want := []Record{
		{Reference: "app/a", Version: 1, Kind: "string", Member: -1, Source: origin(f0, 0), SourcePath: labels + "a", OverriddenBy: &by},
		{Reference: "app/b", Version: 1, Kind: "string", Member: -1, Source: origin(f0, 0), SourcePath: labels + "b", Occurrence: 1, OverriddenBy: &by},
		{Reference: "app/c", Version: 1, Kind: "string", Member: -1, Source: origin(f0, 0), SourcePath: labels + "c", Occurrence: 2, OverriddenBy: &by},
		{Reference: "app/keep", Version: 1, Kind: "string", Member: -1, Source: origin(f0, 0), SourcePath: labels + "keep", Occurrence: 3,
			Output: labels + "keep"},
		{Reference: "app/flag", Version: 1, Kind: "boolean", Member: -1, Source: origin(f0, 0), SourcePath: rbac, Occurrence: 4, OverriddenBy: &by},
		{Reference: "app/other", Version: 1, Kind: "string", Member: -1, Source: origin(f1, 1), SourcePath: labels + "b", Output: labels + "b"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("records\n%+v\nwant\n%+v", got, want)
	}
	effective := map[string]bool{}
	for _, d := range c.Effective() {
		effective[d.Reference] = true
	}
	for name, want := range map[string]bool{"app/a": false, "app/b": false, "app/c": false, "app/flag": false, "app/keep": true, "app/other": true} {
		if effective[name] != want {
			t.Errorf("%s effective: %v, want %v", name, effective[name], want)
		}
	}
	var reproduction []Occurrence
	for _, o := range c.Reproduction() {
		if !o.Source.Base {
			reproduction = append(reproduction, o)
		}
	}
	wantOcc := []Occurrence{
		{Reference: "app/a", Version: 1, Source: origin(f0, 0), Path: labels + "a"},
		{Reference: "app/b", Version: 1, Source: origin(f0, 0), Path: labels + "b", Occurrence: 1},
		{Reference: "app/c", Version: 1, Source: origin(f0, 0), Path: labels + "c", Occurrence: 2},
		{Reference: "app/keep", Version: 1, Source: origin(f0, 0), Path: labels + "keep", Occurrence: 3},
		{Reference: "app/flag", Version: 1, Source: origin(f0, 0), Path: "doc[0]/machine/features/rbac", Occurrence: 4},
		{Reference: "app/other", Version: 1, Source: origin(f1, 1), Path: labels + "b"},
	}
	if !reflect.DeepEqual(reproduction, wantOcc) {
		t.Errorf("reproduction\n%+v\nwant\n%+v", reproduction, wantOcc)
	}
}

// A list entry appended by a fragment is attributed at its output index, and the same reference
// appended twice is two occurrences at two indexes (SP's list append and duplicate cases).
func TestProvenanceListAppendAndDuplicate(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	values := map[string]provider.Value{"san": value(t, provider.KindString, "san.compile.test")}
	f0 := source(t, "machine:\n  certSANs:\n    - !bwref san\n", strRef("san"), values)
	f1 := source(t, "machine:\n  certSANs:\n    - !bwref san\n  nodeLabels:\n    second: x\n", strRef("san"), values)
	c := compiled(t, Input{Base: base, Fragments: []Source{f0, f1}, Mode: ModeMetal})
	var outputs []string
	for _, r := range c.Provenance() {
		if r.Reference == "san" {
			if !strings.HasPrefix(r.Output, "doc[0]/machine/certSANs/") {
				t.Errorf("san from fragment %d at %q", r.Source.Fragment, r.Output)
			}
			outputs = append(outputs, r.Output)
		}
	}
	if len(outputs) != 2 || outputs[0] == outputs[1] {
		t.Errorf("san outputs %v, want two distinct list entries", outputs)
	}
	if d := c.Effective(); !slices.Contains(d, Dependency{Reference: "san", Version: 1}) {
		t.Errorf("effective %v lacks san@1", d)
	}
}

// overrider names the fragment after which an occurrence disappears, from its presence in each
// prefix composition, and fails closed when the occurrence is absent from its own source's
// prefix (compilation.md §8.1).
func TestOverrider(t *testing.T) {
	for _, c := range []struct {
		own     int
		present []bool
		want    int
		fails   bool
	}{
		{0, []bool{true, true, false}, 1, false},
		{0, []bool{true, false, false}, 0, false},
		{1, []bool{false, true, true, false}, 2, false},
		{2, []bool{false, false, true, false}, 2, false},
		{1, []bool{true, false, false}, 0, true},
		{0, []bool{true, true}, 0, true},
	} {
		got, err := overrider(c.own, c.present)
		var e *Error
		switch {
		case c.fails && (!errors.As(err, &e) || e.Rule != RuleFidelity):
			t.Errorf("own %d present %v: got %d, %v, want a fidelity refusal", c.own, c.present, got, err)
		case !c.fails && (err != nil || got != c.want):
			t.Errorf("own %d present %v: got %d, %v, want fragment %d", c.own, c.present, got, err, c.want)
		}
	}
}
