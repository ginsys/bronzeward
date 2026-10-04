package ingest

import (
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/provider"
)

type visited struct {
	path  string
	kind  yaml.Kind
	tag   string
	value string
}

func walked(t *testing.T, text string, embedded map[string]string) []visited {
	t.Helper()
	var got []visited
	err := WalkLeaves([]byte(text), embedded, func(p Path, n *yaml.Node) error {
		v := visited{path: p.String(), kind: n.Kind}
		if n.Kind == yaml.ScalarNode {
			v.tag, v.value = n.Tag, n.Value
		}
		got = append(got, v)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// WalkLeaves visits every value node of every document in order, containers included so that
// shape can be compared: an alias is followed and visited at its own path, an identified embedded
// document is descended into at "|<format>" paths, and any other string is one leaf.
func TestWalkLeaves(t *testing.T) {
	text := "a: &x\n  k: v\nb: *x\nc: [1, true]\nd: '{\"p\": \"q\"}'\ne: '{\"p\": \"q\"}'\n---\nf: x\n"
	got := walked(t, text, map[string]string{"doc[0]/d": "json"})
	m, s, q := yaml.MappingNode, yaml.SequenceNode, yaml.ScalarNode
	want := []visited{
		{"doc[0]", m, "", ""},
		{"doc[0]/a", m, "", ""},
		{"doc[0]/a/k", q, "!!str", "v"},
		{"doc[0]/b", m, "", ""},
		{"doc[0]/b/k", q, "!!str", "v"},
		{"doc[0]/c", s, "", ""},
		{"doc[0]/c/0", q, "!!int", "1"},
		{"doc[0]/c/1", q, "!!bool", "true"},
		{"doc[0]/d|json", m, "", ""},
		{"doc[0]/d|json/p", q, "!!str", "q"},
		{"doc[0]/e", q, "!!str", "{\"p\": \"q\"}"},
		{"doc[1]", m, "", ""},
		{"doc[1]/f", q, "!!str", "x"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("visited\n%v\nwant\n%v", got, want)
	}
}

// An identified document that is not met as a parseable string, or not met at all, fails the
// walk: the real and trace outputs must descend at the same places (compilation.md §8.1, fidelity
// fails closed).
func TestWalkLeavesRefusesMissingHost(t *testing.T) {
	for name, embedded := range map[string]map[string]string{
		"absent":       {"doc[0]/z": "json"},
		"not a string": {"doc[0]/a": "yaml"},
		"unparseable":  {"doc[0]/u": "yaml"},
	} {
		err := WalkLeaves([]byte("a: {k: v}\nu: '[unclosed'\n"), embedded, func(Path, *yaml.Node) error { return nil })
		if err == nil {
			t.Errorf("%s: walked without error", name)
		}
	}
}

// HostFormat names the embedded document a tracer stands in when given exactly that document's
// trace text, so the composed output's copy of it can be found and descended into.
func TestTracerHostFormat(t *testing.T) {
	values := map[string]provider.Value{
		"app/pass": value(t, provider.KindString, resolveSecret),
		"app/str":  value(t, provider.KindString, resolveSecret),
	}
	decl := Declarations{
		References: map[string]Reference{"app/pass": str(provider.KindString), "app/str": str(provider.KindString)},
		Embedded:   []Embedded{{Path: manifestPath, Format: "json"}},
	}
	text := manifestStream("{\"user\": \"admin\", \"password\": !bwref app/pass}\n") + "machine:\n  nodeLabels:\n    s: !bwref app/str\n"
	out, ts := traced(t, sanitizedOf(t, text, decl), values, 0, -1)
	host := innerText(t, []byte(out))
	if len(ts) != 2 {
		t.Fatalf("%d tracers", len(ts))
	}
	if got := ts[0].HostFormat(host); got != "json" {
		t.Errorf("embedded tracer's host format %q, want json", got)
	}
	if got := ts[0].HostFormat(host + " "); got != "" {
		t.Errorf("another text has host format %q", got)
	}
	if got := ts[1].HostFormat(host); got != "" {
		t.Errorf("a tracer outside any embedded document has host format %q", got)
	}
	if got := (Tracer{}).HostFormat(""); got != "" {
		t.Errorf("the zero tracer has host format %q", got)
	}
}
