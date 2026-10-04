package compile

import (
	"encoding/base64"
	"maps"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// valueForms is every string form of every value of the sources that the redaction must hide:
// a string, its base64 encoding and canonical re-encoding, and a mapping's keys and string
// members.
func valueForms(t *testing.T, sources ...Source) []string {
	t.Helper()
	var out []string
	add := func(s string) {
		out = append(out, s, base64.StdEncoding.EncodeToString([]byte(s)))
		if c, ok := canonical(s); ok {
			out = append(out, c)
		}
	}
	for _, s := range sources {
		for _, v := range s.Values {
			x, err := v.Decode()
			if err != nil {
				t.Fatal(err)
			}
			switch x := x.(type) {
			case string:
				add(x)
			case map[string]any:
				for k, m := range x {
					add(k)
					if m, ok := m.(string); ok {
						add(m)
					}
				}
			}
		}
	}
	return out
}

// hidesValues fails when the rendering holds a value form of copyFloor bytes or more anywhere, or
// any value form as a whole scalar or key of the stream.
func hidesValues(t *testing.T, out string, forms []string) {
	t.Helper()
	whole := map[string]bool{}
	if err := ingest.WalkLeaves([]byte(out), nil, func(_ ingest.Path, n *yaml.Node) error {
		whole[n.Value] = n.Kind == yaml.ScalarNode
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := ingest.WalkKeys([]byte(out), nil, func(_ ingest.Path, k *yaml.Node) error {
		whole[k.Value] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, f := range forms {
		if len(f) >= copyFloor && strings.Contains(out, f) || whole[f] {
			t.Errorf("the redacted configuration holds a value form of %d bytes", len(f))
		}
	}
}

// redactedFixture compiles the generated base with a fragment of every reference kind, an alias,
// a short string also written as a literal, and a value inside embedded JSON and YAML; version is
// app/str's.
func redactedFixture(t *testing.T, base Source, version int64, secret string) (Compiled, []Source) {
	t.Helper()
	labels := source(t, "machine:\n  nodeLabels:\n    s: &a !bwref app/str\n    t: *a\n    h: !bwref app/short\n    lit: abc\n"+
		"  nodeAnnotations: !bwref app/map\n  features:\n    rbac: !bwref app/bool\n    kubePrism:\n      port: !bwref app/int\n",
		refs(map[string]ingest.Reference{
			"app/str": {Kind: provider.KindString, Version: version}, "app/short": ref(provider.KindString),
			"app/int": ref(provider.KindInteger), "app/bool": ref(provider.KindBoolean), "app/map": ref(provider.KindMapping),
		}),
		map[string]provider.Value{
			"app/str":   value(t, provider.KindString, secret),
			"app/short": value(t, provider.KindString, "abc"),
			"app/int":   value(t, provider.KindInteger, 7445),
			"app/bool":  value(t, provider.KindBoolean, false),
			"app/map":   value(t, provider.KindMapping, map[string]any{"one-key-7c": "first-" + compileSecret, "two-key-7c": "second-" + compileSecret}),
		})
	manifests := source(t, "cluster:\n  inlineManifests:\n    - name: j\n      contents: |\n        {\"user\": \"admin\", \"password\": !bwref app/json}\n"+
		"    - name: y\n      contents: |\n        user: admin\n        password: !bwref app/yaml\n",
		ingest.Declarations{
			References: map[string]ingest.Reference{"app/json": ref(provider.KindString), "app/yaml": ref(provider.KindString)},
			Embedded: []ingest.Embedded{
				{Path: "doc[0]/cluster/inlineManifests/0/contents", Format: "json"},
				{Path: "doc[0]/cluster/inlineManifests/1/contents", Format: "yaml"},
			},
		},
		map[string]provider.Value{
			"app/json": value(t, provider.KindString, "json-"+compileSecret),
			"app/yaml": value(t, provider.KindString, "yaml-"+compileSecret),
		})
	frags := []Source{labels, manifests}
	return compiled(t, Input{Base: base, Fragments: frags, Mode: ModeMetal}), append([]Source{base}, frags...)
}

// The redacted configuration is the real composition with compilation.md §8.3's three means
// applied: every attributed leaf, and a mapping member's key, its reference token; every schema
// secret <redacted:schema>; every remaining copy <redacted:value>, a whole scalar of any length or
// a contained copy of six bytes or more. A token inside embedded JSON has its angle brackets
// escaped. No value form and no secret of the base bundle survives, and a rotation shows only as
// the token's version.
func TestCompiledRedacted(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	c, sources := redactedFixture(t, base, 1, compileSecret)
	out, err := c.Redacted()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"s: <redacted:app/str@1>", "t: <redacted:app/str@1>", "h: <redacted:app/short@1>", "lit: <redacted:value>",
		"<redacted:app/map@1#0>: <redacted:app/map@1#0>", "<redacted:app/map@1#1>: <redacted:app/map@1#1>",
		"rbac: <redacted:app/bool@1>", "port: <redacted:app/int@1>",
		`"password":"\u003credacted:app/json@1\u003e"`, "password: <redacted:app/yaml@1>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the redacted configuration lacks %q", want)
		}
	}
	if strings.Contains(out, "<redacted:app/json") {
		t.Error("a token inside embedded JSON is not escaped")
	}
	hidesValues(t, out, valueForms(t, sources...))

	rotated, _ := redactedFixture(t, base, 2, "rotated-"+compileSecret)
	got, err := rotated.Redacted()
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.ReplaceAll(out, "<redacted:app/str@1>", "<redacted:app/str@2>"); got != want {
		t.Error("a rotation changes more than the token's version")
	}
	if _, err := (Compiled{}).Redacted(); err == nil {
		t.Error("an empty compilation rendered")
	}
}

// An alias in an embedded document is redacted as a copy of its anchor: rewriting the anchor's
// short copy of a value does not move the key that aliases it, so the integer under that key is
// still its token.
func TestRedactedAliases(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	frag := source(t, "cluster:\n  inlineManifests:\n    - name: y\n      contents: |\n        anchor: &k abc\n        copy: !bwref app/short\n        ? *k\n        : !bwref app/pin\n",
		ingest.Declarations{
			References: map[string]ingest.Reference{"app/short": ref(provider.KindString), "app/pin": ref(provider.KindInteger)},
			Embedded:   []ingest.Embedded{{Path: "doc[0]/cluster/inlineManifests/0/contents", Format: "yaml"}},
		},
		map[string]provider.Value{"app/short": value(t, provider.KindString, "abc"), "app/pin": value(t, provider.KindInteger, 39157)})
	out, err := compiled(t, Input{Base: base, Fragments: []Source{frag}, Mode: ModeMetal}).Redacted()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"anchor: <redacted:value>", "copy: <redacted:app/short@1>", "<redacted:value>: <redacted:app/pin@1>"} {
		if !strings.Contains(out, want) {
			t.Errorf("the redacted configuration lacks %q", want)
		}
	}
	if strings.Contains(out, ": 39157\n") || strings.Contains(out, "*k") || strings.Contains(out, "&k") {
		t.Error("the redacted configuration shows the integer, an anchor or a dangling alias")
	}
}

// The rendering fails closed: an attributed leaf it did not meet, or a token whose reference name
// is a value of any length, shows nothing.
func TestRedactedTextGuards(t *testing.T) {
	cfg, err := configloader.NewFromBytes(generatedBase(t))
	if err != nil {
		t.Fatal(err)
	}
	b, err := cfg.EncodeBytes(encoder.WithComments(encoder.CommentsDisabled))
	if err != nil {
		t.Fatal(err)
	}
	ts, r, _ := messageFixture(t)
	at := func(p string) []outcome { return []outcome{{tracer: ts[0].Tracer, source: 1, paths: []string{p}}} }
	if _, err := redactedText(b, nil, at("doc[0]/version"), r); err != nil {
		t.Fatalf("control: %v", err)
	}
	if _, err := redactedText(b, nil, at("doc[0]/no-such-leaf"), r); err == nil {
		t.Error("an attributed leaf that was not met rendered")
	}
	o := r
	o.exact = maps.Clone(r.exact)
	o.exact[ts[0].Ref()] = true
	if _, err := redactedText(b, nil, at("doc[0]/version"), o); err == nil {
		t.Error("a token naming a value rendered")
	}
}

// The schema means covers every field the pinned machinery marks secret, as ingestion identifies
// them: a base whose secrets were not extracted shows none of them.
func TestRedactedSchemaMeans(t *testing.T) {
	text := generatedBase(t)
	secrets := source(t, string(text), ingest.Declarations{}, nil)
	if len(secrets.Values) == 0 {
		t.Fatal("control: ingestion identified no schema secret")
	}
	cfg, err := configloader.NewFromBytes(text)
	if err != nil {
		t.Fatal(err)
	}
	b, err := cfg.EncodeBytes(encoder.WithComments(encoder.CommentsDisabled))
	if err != nil {
		t.Fatal(err)
	}
	out, err := redactedText(b, nil, nil, newRedactor(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, schemaToken) {
		t.Error("no schema token")
	}
	hidesValues(t, out, valueForms(t, secrets))
}
