package ingest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/provider"
)

func traced(t *testing.T, s Sanitized, values map[string]provider.Value, first, flip int) (string, []Tracer) {
	t.Helper()
	r, ts, err := Trace(s, values, first, flip)
	if err != nil {
		t.Fatal(err)
	}
	return string(r.bytes()), ts
}

// The stand-in is SP's (compilation.md §8.1, choice §16.22): each letter becomes x or X, each
// digit 0, every other byte stays, and each line of five bytes or more starts with zq and the
// three-digit id.
func TestStandIn(t *testing.T) {
	for _, c := range []struct {
		real string
		id   int
		want string
	}{
		{"secret-Value-42\nab", 12, "zq012x-Xxxxx-00\nxx"},
		{"Ab1-c", 3, "zq003"},
		{"Ab1", 3, "Xx0"},
		{"x \"q\" \\ tab\tend # y", 7, "zq007 \\ xxx\txxx # x"},
		{"", 1, ""},
	} {
		if got := standIn(c.real, c.id); got != c.want {
			t.Errorf("standIn(%q, %d) = %q, want %q", c.real, c.id, got, c.want)
		}
	}
}

// Each reference occurrence gets one tracer per scalar leaf, ids continuing from first in walk
// order; a string's stand-in keeps its shape, an integer's is 61000 plus its id, a boolean keeps
// its value, a base64-encoded string is the encoding of its stand-in, and a mapping's members are
// traced in key order (compilation.md §8.1).
func TestTraceKinds(t *testing.T) {
	text := "machine:\n  nodeLabels:\n    s: !bwref app/str\n    i: !bwref app/int\n    b: !bwref app/bool\n" +
		"    e: !bwref app/enc\n  nodeAnnotations: !bwref app/map\n"
	decl := Declarations{References: map[string]Reference{
		"app/str": str(provider.KindString), "app/int": str(provider.KindInteger),
		"app/bool": str(provider.KindBoolean), "app/map": {Kind: provider.KindMapping, Version: 4},
		"app/enc": {Kind: provider.KindString, Version: 2, Encoding: "base64"},
	}}
	out, ts := traced(t, sanitizedOf(t, text, decl), map[string]provider.Value{
		"app/str":  value(t, provider.KindString, resolveSecret),
		"app/int":  value(t, provider.KindInteger, 6443),
		"app/bool": value(t, provider.KindBoolean, true),
		"app/enc":  value(t, provider.KindString, resolveSecret),
		"app/map":  value(t, provider.KindMapping, map[string]any{"z": "member-" + resolveSecret, "a": 7}),
	}, 40, -1)
	if strings.Contains(out, resolveSecret) || strings.Contains(out, base64.StdEncoding.EncodeToString([]byte(resolveSecret))) {
		t.Fatalf("the trace pass holds a real value:\n%s", out)
	}
	strStandIn := standIn(resolveSecret, 40)
	encRaw := standIn(resolveSecret, 43)
	labels := decodeStream(t, out)[0]["machine"].(map[string]any)["nodeLabels"].(map[string]any)
	want := map[string]any{"s": strStandIn, "i": 61041, "b": true, "e": base64.StdEncoding.EncodeToString([]byte(encRaw))}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("nodeLabels = %#v, want %#v", labels, want)
	}
	annotations := decodeStream(t, out)[0]["machine"].(map[string]any)["nodeAnnotations"]
	if want := map[string]any{"a": 61044, "z": standIn("member-"+resolveSecret, 45)}; !reflect.DeepEqual(annotations, want) {
		t.Errorf("nodeAnnotations = %#v, want %#v", annotations, want)
	}
	type row struct {
		id       int
		ref      string
		version  int64
		encoding string
		leaf     string
		kind     TraceKind
		path     string
	}
	var got []row
	for _, x := range ts {
		got = append(got, row{x.ID(), x.Ref(), x.Version(), x.Encoding(), x.Leaf(), x.Kind(), x.Path().String()})
	}
	wantRows := []row{
		{40, "app/str", 1, "", "", TraceString, "doc[0]/machine/nodeLabels/s"},
		{41, "app/int", 1, "", "", TraceInteger, "doc[0]/machine/nodeLabels/i"},
		{42, "app/bool", 1, "", "", TraceBoolean, "doc[0]/machine/nodeLabels/b"},
		{43, "app/enc", 2, "base64", "", TraceBytes, "doc[0]/machine/nodeLabels/e"},
		{44, "app/map", 4, "", "a", TraceInteger, "doc[0]/machine/nodeAnnotations"},
		{45, "app/map", 4, "", "z", TraceString, "doc[0]/machine/nodeAnnotations"},
	}
	if !reflect.DeepEqual(got, wantRows) {
		t.Errorf("tracers\n%v\nwant\n%v", got, wantRows)
	}
}

// A flip pass negates exactly the one boolean tracer it names; every other leaf is as in the
// trace pass (compilation.md §8.1).
func TestTraceFlip(t *testing.T) {
	text := "machine:\n  nodeLabels:\n    a: !bwref app/a\n    b: !bwref app/b\n    s: !bwref app/str\n"
	decl := Declarations{References: map[string]Reference{
		"app/a": str(provider.KindBoolean), "app/b": str(provider.KindBoolean), "app/str": str(provider.KindString),
	}}
	values := map[string]provider.Value{
		"app/a": value(t, provider.KindBoolean, true), "app/b": value(t, provider.KindBoolean, false),
		"app/str": value(t, provider.KindString, resolveSecret),
	}
	s := sanitizedOf(t, text, decl)
	trace, _ := traced(t, s, values, 0, -1)
	flip, _ := traced(t, s, values, 0, 1)
	tl := decodeStream(t, trace)[0]["machine"].(map[string]any)["nodeLabels"].(map[string]any)
	fl := decodeStream(t, flip)[0]["machine"].(map[string]any)["nodeLabels"].(map[string]any)
	if tl["a"] != true || tl["b"] != false {
		t.Fatalf("the trace pass changed a boolean: %#v", tl)
	}
	if fl["a"] != true || fl["b"] != true || fl["s"] != tl["s"] {
		t.Errorf("flip of tracer 1 gives %#v from %#v", fl, tl)
	}
}

// An alias of a traced reference carries the same stand-in, and the occurrence is one tracer.
func TestTraceAlias(t *testing.T) {
	text := "machine:\n  nodeLabels:\n    a: &x !bwref app/str\n    b: *x\n"
	decl := Declarations{References: map[string]Reference{"app/str": str(provider.KindString)}}
	out, ts := traced(t, sanitizedOf(t, text, decl), map[string]provider.Value{
		"app/str": value(t, provider.KindString, resolveSecret),
	}, 0, -1)
	labels := decodeStream(t, out)[0]["machine"].(map[string]any)["nodeLabels"].(map[string]any)
	if len(ts) != 1 || labels["a"] != standIn(resolveSecret, 0) || labels["b"] != labels["a"] {
		t.Errorf("%d tracers, nodeLabels %#v", len(ts), labels)
	}
}

// A reference inside an identified embedded document is traced there, with its embedded path,
// and the document is written back as Resolve writes it.
func TestTraceEmbedded(t *testing.T) {
	values := map[string]provider.Value{"app/pass": value(t, provider.KindString, resolveSecret)}
	decl := Declarations{
		References: map[string]Reference{"app/pass": str(provider.KindString)},
		Embedded:   []Embedded{{Path: manifestPath, Format: "json"}},
	}
	manifest := "{\"user\": \"admin\", \"password\": !bwref app/pass}\n"
	out, ts := traced(t, sanitizedOf(t, manifestStream(manifest), decl), values, 0, -1)
	want := "{\"password\":\"" + standIn(resolveSecret, 0) + "\",\"user\":\"admin\"}\n"
	if got := innerText(t, []byte(out)); got != want {
		t.Errorf("embedded document %q, want %q", got, want)
	}
	if len(ts) != 1 || ts[0].Path().String() != manifestPath+"|json/password" {
		t.Errorf("tracers %v at %v", len(ts), ts)
	}
}

// The trace pass refuses as resolution does, by path; and a stand-in that would equal the real
// value, or a tracer id past three digits, refuses with trace-indistinct: nothing could tell the
// stand-in from the value (compilation.md §8.1, fidelity fails closed).
func TestTraceRefusals(t *testing.T) {
	text := "machine:\n  nodeLabels:\n    s: !bwref app/str\n    i: !bwref app/int\n"
	decl := Declarations{References: map[string]Reference{"app/str": str(provider.KindString), "app/int": str(provider.KindInteger)}}
	s := sanitizedOf(t, text, decl)
	good := map[string]provider.Value{"app/str": value(t, provider.KindString, resolveSecret), "app/int": value(t, provider.KindInteger, 1)}
	for name, c := range map[string]struct {
		values      map[string]provider.Value
		first, flip int
		rule        Rule
		path        string
	}{
		"kind mismatch": {map[string]provider.Value{"app/str": value(t, provider.KindInteger, 3), "app/int": good["app/int"]}, 0, -1, RuleKindMismatch, "doc[0]/machine/nodeLabels/s"},
		"no value":      {map[string]provider.Value{"app/int": good["app/int"]}, 0, -1, RuleUnresolved, "doc[0]/machine/nodeLabels/s"},
		"string shape":  {map[string]provider.Value{"app/str": value(t, provider.KindString, "x-0"), "app/int": good["app/int"]}, 0, -1, RuleTraceIndistinct, "doc[0]/machine/nodeLabels/s"},
		"integer":       {map[string]provider.Value{"app/str": good["app/str"], "app/int": value(t, provider.KindInteger, 61001)}, 0, -1, RuleTraceIndistinct, "doc[0]/machine/nodeLabels/i"},
		"id past 999":   {good, 999, -1, RuleTraceIndistinct, "doc[0]/machine/nodeLabels/i"},
	} {
		r, ts, err := Trace(s, c.values, c.first, c.flip)
		var ref *Refusal
		if !errors.As(err, &ref) || ref.Rule != c.rule || r.b != nil || ts != nil {
			t.Errorf("%s: got %v, want a %s refusal and nothing traced", name, err, c.rule)
			continue
		}
		if !reflect.DeepEqual(ref.Paths, []string{c.path}) {
			t.Errorf("%s: paths %v", name, ref.Paths)
		}
		if strings.Contains(fmt.Sprintf("%v %+v", err, ref), resolveSecret) {
			t.Errorf("%s: the refusal quotes the value", name)
		}
	}
	if _, _, err := Trace(Sanitized{}, nil, 0, -1); !errors.Is(err, ErrZeroSanitized) {
		t.Errorf("the zero Sanitized: %v", err)
	}
	// A flip pass must name a boolean tracer of this stream.
	for _, flip := range []int{0, 1, 2} {
		if r, ts, err := Trace(s, good, 0, flip); err == nil || r.b != nil || ts != nil {
			t.Errorf("flip %d of a stream without booleans: %v", flip, err)
		}
	}
}

// Carried finds a tracer at a composed leaf: a string's stand-in anywhere in the leaf, or its
// canonical base64 re-encoding, which the machinery writes for a byte field it decodes (probe on
// a generated base, compilation.md §8.1); a base64-encoded string's encoding, or a leaf decoding
// to its stand-in; an integer exactly; a boolean never (it is attributed by flipping).
func TestTracerCarried(t *testing.T) {
	text := "machine:\n  nodeLabels:\n    s: !bwref app/str\n    k: !bwref app/key\n    e: !bwref app/enc\n    i: !bwref app/int\n    b: !bwref app/bool\n"
	decl := Declarations{References: map[string]Reference{
		"app/str": str(provider.KindString), "app/key": str(provider.KindString),
		"app/enc": {Kind: provider.KindString, Version: 1, Encoding: "base64"},
		"app/int": str(provider.KindInteger), "app/bool": str(provider.KindBoolean),
	}}
	// A base64 text whose last character before the padding has non-zero trailing bits once
	// traced: its stand-in ends "x=", the canonical encoding of the same bytes "w=".
	key := base64.StdEncoding.EncodeToString([]byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ"))
	_, ts := traced(t, sanitizedOf(t, text, decl), map[string]provider.Value{
		"app/str": value(t, provider.KindString, resolveSecret), "app/key": value(t, provider.KindString, key),
		"app/enc": value(t, provider.KindString, resolveSecret), "app/int": value(t, provider.KindInteger, 5),
		"app/bool": value(t, provider.KindBoolean, true),
	}, 0, -1)
	if len(ts) != 5 {
		t.Fatalf("%d tracers", len(ts))
	}
	keyStand := standIn(key, 1)
	decoded, err := base64.StdEncoding.DecodeString(keyStand)
	if err != nil {
		t.Fatal(err)
	}
	canonical := base64.StdEncoding.EncodeToString(decoded)
	if canonical == keyStand {
		t.Fatal("control: the key's stand-in is already canonical")
	}
	encRaw := standIn(resolveSecret, 2)
	for _, c := range []struct {
		tracer int
		leaf   string
		want   bool
	}{
		{0, standIn(resolveSecret, 0), true},
		{0, "prefix " + standIn(resolveSecret, 0) + " suffix", true},
		{0, standIn(resolveSecret, 3), false},
		{0, resolveSecret, false},
		{1, keyStand, true},
		{1, canonical, true},
		{2, base64.StdEncoding.EncodeToString([]byte(encRaw)), true},
		{2, encRaw, true},
		{2, base64.StdEncoding.EncodeToString([]byte("a " + encRaw + " b")), true},
		{2, base64.StdEncoding.EncodeToString([]byte(resolveSecret)), false},
		{3, "61003", true},
		{3, "610030", false},
		{4, "true", false},
	} {
		if got := ts[c.tracer].Carried(c.leaf); got != c.want {
			t.Errorf("tracer %d Carried(leaf %d bytes) = %v, want %v", c.tracer, len(c.leaf), got, c.want)
		}
	}
}

// A tracer holds a value's length and character classes, so it is handled like the value: every
// fmt verb prints a placeholder and the marshallers fail (compilation.md §8.1).
func TestTracerNeverRenders(t *testing.T) {
	text := "machine:\n  nodeLabels:\n    s: !bwref app/str\n"
	decl := Declarations{References: map[string]Reference{"app/str": str(provider.KindString)}}
	_, ts := traced(t, sanitizedOf(t, text, decl), map[string]provider.Value{"app/str": value(t, provider.KindString, resolveSecret)}, 0, -1)
	stand := standIn(resolveSecret, 0)
	holder := struct{ t Tracer }{ts[0]}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"} {
		for _, x := range []any{ts[0], &ts[0], ts, holder} {
			if got := fmt.Sprintf(verb, x); strings.Contains(got, stand) || strings.Contains(got, fmt.Sprintf("%x", stand)) {
				t.Errorf("%s of %T shows the stand-in: %s", verb, x, got)
			}
		}
	}
	if b, err := json.Marshal(ts[0]); err == nil || bytes.Contains(b, []byte(stand)) {
		t.Errorf("json.Marshal: %s, %v", b, err)
	}
	if b, err := yaml.Marshal(ts[0]); err == nil || bytes.Contains(b, []byte(stand)) {
		t.Errorf("yaml.Marshal: %s, %v", b, err)
	}
	if b, err := ts[0].MarshalText(); err == nil || b != nil {
		t.Errorf("MarshalText: %s, %v", b, err)
	}
}
