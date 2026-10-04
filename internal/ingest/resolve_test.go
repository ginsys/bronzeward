package ingest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"
	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/provider"
)

const resolveSecret = "resolve-test-secret-5d1e"

// sanitizedOf is text, holding references declared by decl, as ingestion stores it.
func sanitizedOf(t *testing.T, text string, decl Declarations) Sanitized {
	t.Helper()
	req := request(t, text)
	req.Declarations = decl
	c, err := Extract(req)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := commitAll(t, c)
	return s
}

func value(t *testing.T, k provider.Kind, v any) provider.Value {
	t.Helper()
	val, err := provider.NewValue(k, v)
	if err != nil {
		t.Fatal(err)
	}
	return val
}

func resolvedText(t *testing.T, s Sanitized, values map[string]provider.Value) string {
	t.Helper()
	r, err := Resolve(s, values)
	if err != nil {
		t.Fatal(err)
	}
	return string(r.bytes())
}

// decodeStream is every document of text as plain Go values.
func decodeStream(t *testing.T, text string) []map[string]any {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(text))
	var out []map[string]any
	for {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				return out
			}
			t.Fatalf("the resolved stream does not decode: %v", err)
		}
		out = append(out, m)
	}
}

// Each kind is placed as its typed YAML value, with its encoding, in any document
// (compilation.md §5.2, §6 step 4); a mapping's members are placed in key order.
func TestResolveKinds(t *testing.T) {
	text := "machine:\n  nodeLabels:\n    s: !bwref app/str\n    n: !bwref app/numeric-str\n    i: !bwref app/int\n" +
		"    b: !bwref app/bool\n    e: !bwref app/enc\n  nodeAnnotations: !bwref app/map\n" +
		"---\napiVersion: v1alpha1\nkind: TrustedRootsConfig\nname: r\ncertificates: !bwref app/roots\n"
	decl := Declarations{References: map[string]Reference{
		"app/str": str(provider.KindString), "app/numeric-str": str(provider.KindString),
		"app/int": str(provider.KindInteger), "app/bool": str(provider.KindBoolean),
		"app/enc":   {Kind: provider.KindString, Version: 1, Encoding: "base64"},
		"app/map":   str(provider.KindMapping),
		"app/roots": str(provider.KindString),
	}}
	out := resolvedText(t, sanitizedOf(t, text, decl), map[string]provider.Value{
		"app/str":         value(t, provider.KindString, resolveSecret),
		"app/numeric-str": value(t, provider.KindString, "6443"),
		"app/int":         value(t, provider.KindInteger, 6443),
		"app/bool":        value(t, provider.KindBoolean, true),
		"app/enc":         value(t, provider.KindString, "line one\n"+resolveSecret),
		"app/map":         value(t, provider.KindMapping, map[string]any{"t": false, "a": "x", "n": 7, "g": "g", "c": "c", "q": "q", "e": "e"}),
		"app/roots":       value(t, provider.KindString, "roots-"+resolveSecret),
	})
	if strings.Contains(out, refTag) {
		t.Fatalf("a reference is left:\n%s", out)
	}
	docs := decodeStream(t, out)
	if len(docs) != 2 {
		t.Fatalf("%d documents:\n%s", len(docs), out)
	}
	machine := docs[0]["machine"].(map[string]any)
	labels := machine["nodeLabels"].(map[string]any)
	want := map[string]any{
		"s": resolveSecret, "n": "6443", "i": 6443, "b": true,
		"e": base64.StdEncoding.EncodeToString([]byte("line one\n" + resolveSecret)),
	}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("nodeLabels = %#v, want %#v", labels, want)
	}
	if got := machine["nodeAnnotations"]; !reflect.DeepEqual(got, map[string]any{"a": "x", "c": "c", "e": "e", "g": "g", "n": 7, "q": "q", "t": false}) {
		t.Errorf("nodeAnnotations = %#v", got)
	}
	if !strings.Contains(out, "nodeAnnotations:\n    a: x\n    c: c\n    e: e\n    g: g\n    n: 7\n    q: q\n    t: false\n") {
		t.Errorf("the mapping's members are not in key order:\n%s", out)
	}
	if got := docs[1]["certificates"]; got != "roots-"+resolveSecret {
		t.Errorf("doc[1] certificates = %#v", got)
	}
}

// An alias of a reference yields the same value: the tagged node is replaced in place, its anchor
// kept (compilation.md §5.4).
func TestResolveAlias(t *testing.T) {
	text := "machine:\n  nodeLabels:\n    a: &x !bwref app/str\n    b: *x\n"
	decl := Declarations{References: map[string]Reference{"app/str": str(provider.KindString)}}
	out := resolvedText(t, sanitizedOf(t, text, decl), map[string]provider.Value{
		"app/str": value(t, provider.KindString, resolveSecret),
	})
	labels := decodeStream(t, out)[0]["machine"].(map[string]any)["nodeLabels"].(map[string]any)
	if labels["a"] != resolveSecret || labels["b"] != resolveSecret {
		t.Errorf("nodeLabels = %#v", labels)
	}
	if !strings.Contains(out, "&x") || !strings.Contains(out, "*x") {
		t.Errorf("the anchor or alias was dropped:\n%s", out)
	}
}

// An identified embedded document is resolved and written back whole: YAML with two-space
// indentation, JSON compact with sorted keys and a final newline (compilation.md §5.4, SR's
// prototype rules).
func TestResolveEmbedded(t *testing.T) {
	values := map[string]provider.Value{"app/pass": value(t, provider.KindString, resolveSecret)}
	decl := func(format string) Declarations {
		return Declarations{
			References: map[string]Reference{"app/pass": str(provider.KindString)},
			Embedded:   []Embedded{{Path: manifestPath, Format: format}},
		}
	}
	for _, c := range []struct {
		format, manifest, want string
	}{
		{"yaml", "kind: Secret\nstringData:\n    password: !bwref app/pass\n    user: admin\n",
			"kind: Secret\nstringData:\n  password: " + resolveSecret + "\n  user: admin\n"},
		{"json", "{\"user\": \"admin\", \"password\": !bwref app/pass, \"port\": 5432, \"tls\": true}\n",
			"{\"password\":\"" + resolveSecret + "\",\"port\":5432,\"tls\":true,\"user\":\"admin\"}\n"},
	} {
		out := resolvedText(t, sanitizedOf(t, manifestStream(c.manifest), decl(c.format)), values)
		if got := innerText(t, []byte(out)); got != c.want {
			t.Errorf("%s: the embedded document is\n%q\nwant\n%q", c.format, got, c.want)
		}
		if strings.Contains(out, refTag) {
			t.Errorf("%s: a reference is left:\n%s", c.format, out)
		}
	}
}

// A value whose stored kind differs from its declaration, or a declared name with no value,
// refuses resolution by path; nothing is coerced and no value is quoted.
func TestResolveRefusals(t *testing.T) {
	text := "machine:\n  nodeLabels:\n    s: !bwref app/str\n"
	decl := Declarations{References: map[string]Reference{"app/str": str(provider.KindString)}}
	s := sanitizedOf(t, text, decl)
	for name, c := range map[string]struct {
		values map[string]provider.Value
		rule   Rule
	}{
		"kind mismatch": {map[string]provider.Value{"app/str": value(t, provider.KindMapping, map[string]any{"k": resolveSecret})}, RuleKindMismatch},
		"no value":      {map[string]provider.Value{"app/other": value(t, provider.KindString, resolveSecret)}, RuleUnresolved},
		"zero value":    {map[string]provider.Value{"app/str": {}}, RuleUnresolved},
	} {
		r, err := Resolve(s, c.values)
		var ref *Refusal
		if !errors.As(err, &ref) || ref.Rule != c.rule || r.s != nil {
			t.Errorf("%s: got %v, want a %s refusal and nothing resolved", name, err, c.rule)
			continue
		}
		if !reflect.DeepEqual(ref.Paths, []string{"doc[0]/machine/nodeLabels/s"}) {
			t.Errorf("%s: paths %v", name, ref.Paths)
		}
		if strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, ref), resolveSecret) {
			t.Errorf("%s: the refusal quotes the value", name)
		}
	}
	if _, err := Resolve(Sanitized{}, nil); !errors.Is(err, ErrZeroSanitized) {
		t.Errorf("the zero Sanitized: %v", err)
	}
}

// A resolved stream leaves this package only as the machinery's own input or patch, loaded as
// talosctl loads a patch file (compilation.md §6 step 5).
func TestResolvedPatchAndInput(t *testing.T) {
	text := "machine:\n  nodeLabels:\n    s: !bwref app/str\n"
	decl := Declarations{References: map[string]Reference{"app/str": str(provider.KindString)}}
	r, err := Resolve(sanitizedOf(t, text, decl), map[string]provider.Value{"app/str": value(t, provider.KindString, resolveSecret)})
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.Patch()
	if err != nil {
		t.Fatal(err)
	}
	smp, ok := p.(configpatcher.StrategicMergePatch)
	if !ok {
		t.Fatalf("Patch is a %T, want a strategic merge patch", p)
	}
	if got := smp.Provider().Machine().NodeLabels()["s"]; got != resolveSecret {
		t.Errorf("the patch's label is %q", got)
	}
	cfg, err := r.Input().Config()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Machine().NodeLabels()["s"]; got != resolveSecret {
		t.Errorf("the input's label is %q", got)
	}
}

// A JSON6902 patch is refused (compilation.md §6 step 5); a fragment the machinery cannot load,
// such as a string in an integer field, is the machinery's rejection, and its message, which
// quotes the value, is not rendered.
func TestResolvedPatchRefusals(t *testing.T) {
	jsonPatch := newSanitized([]byte("- op: add\n  path: /machine/nodeLabels/x\n  value: "+resolveSecret+"\n"), Declarations{})
	r, err := Resolve(jsonPatch, nil)
	if err != nil {
		t.Fatal(err)
	}
	var ref *Refusal
	if _, err := r.Patch(); !errors.As(err, &ref) || ref.Rule != RuleJSONPatch {
		t.Errorf("a JSON6902 patch: %v, want a %s refusal", err, RuleJSONPatch)
	}
	text := "machine:\n  features:\n    kubePrism:\n      port: !bwref app/port\n"
	decl := Declarations{References: map[string]Reference{"app/port": str(provider.KindString)}}
	r, err = Resolve(sanitizedOf(t, text, decl), map[string]provider.Value{"app/port": value(t, provider.KindString, resolveSecret)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Patch()
	if !errors.Is(err, ErrPatchLoad) {
		t.Fatalf("a string in an integer field: %v, want ErrPatchLoad", err)
	}
	if strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, err), resolveSecret[:8]) {
		t.Errorf("the rejection quotes the value: %v", err)
	}
}

// TestResolvedNeverRenders: a resolved stream is plaintext, and no fmt verb or marshaller shows it.
func TestResolvedNeverRenders(t *testing.T) {
	text := "machine:\n  nodeLabels:\n    s: !bwref app/str\n"
	decl := Declarations{References: map[string]Reference{"app/str": str(provider.KindString)}}
	r, err := Resolve(sanitizedOf(t, text, decl), map[string]provider.Value{"app/str": value(t, provider.KindString, resolveSecret)})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(r.bytes(), []byte(resolveSecret)) {
		t.Fatal("control: the resolved stream does not hold the value")
	}
	holder := struct{ r Resolved }{r}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"} {
		for _, x := range []any{r, &r, holder} {
			if got := fmt.Sprintf(verb, x); shows(got, resolveSecret) {
				t.Errorf("%s of %T shows the value: %s", verb, x, got)
			}
		}
	}
	if b, err := json.Marshal(r); err == nil || bytes.Contains(b, []byte(resolveSecret)) {
		t.Errorf("json.Marshal: %s, %v", b, err)
	}
	if b, err := yaml.Marshal(r); err == nil || bytes.Contains(b, []byte(resolveSecret)) {
		t.Errorf("yaml.Marshal: %s, %v", b, err)
	}
	if b, err := r.MarshalText(); err == nil || b != nil {
		t.Errorf("MarshalText: %s, %v", b, err)
	}
}
