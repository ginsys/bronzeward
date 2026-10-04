package ingest

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
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

// WalkKeys visits every mapping key, at the path of the value it names, through aliases and
// inside an identified embedded document, and no value.
func TestWalkKeys(t *testing.T) {
	text := "a: &x\n  k: v\nb: *x\nc: [{n: 1}]\nd: '{\"p\": \"q\"}'\n---\nf: x\n"
	var got []string
	err := WalkKeys([]byte(text), map[string]string{"doc[0]/d": "json"}, func(p Path, k *yaml.Node) error {
		got = append(got, p.String()+"="+k.Value)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"doc[0]/a=a", "doc[0]/a/k=k", "doc[0]/b=b", "doc[0]/b/k=k", "doc[0]/c=c", "doc[0]/c/0/n=n",
		"doc[0]/d=d", "doc[0]/d|json/p=p", "doc[1]/f=f"}
	if !slices.Equal(got, want) {
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

// The trace pass gives one host per identified embedded document, with or without a reference in
// it, naming its format when given exactly the text it wrote, so the composed output's copies can
// be found and descended into. A host can hold plaintext, so it never renders.
func TestTraceHosts(t *testing.T) {
	values := map[string]provider.Value{
		"app/pass": value(t, provider.KindString, resolveSecret),
		"app/str":  value(t, provider.KindString, resolveSecret),
	}
	decl := Declarations{
		References: map[string]Reference{"app/pass": str(provider.KindString), "app/str": str(provider.KindString)},
		Embedded:   []Embedded{{Path: manifestPath, Format: "json"}},
	}
	for name, text := range map[string]string{
		"with a reference": manifestStream("{\"user\": \"admin\", \"password\": !bwref app/pass}\n") +
			"machine:\n  nodeLabels:\n    s: !bwref app/str\n",
		"without a reference": manifestStream("{\"user\": \"admin\", \"copy\": \""+resolveSecret+"\\\"\"}\n") +
			"machine:\n  nodeLabels:\n    s: !bwref app/str\n    p: !bwref app/pass\n",
	} {
		r, _, hosts, err := Trace(sanitizedOf(t, text, decl), values, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		host := innerText(t, r.bytes())
		if len(hosts) != 1 {
			t.Fatalf("%s: %d hosts", name, len(hosts))
		}
		if got := hosts[0].HostFormat(host); got != "json" {
			t.Errorf("%s: host format %q, want json", name, got)
		}
		if got := hosts[0].HostFormat(host + " "); got != "" {
			t.Errorf("%s: another text has host format %q", name, got)
		}
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			if got := fmt.Sprintf(verb, hosts); strings.Contains(got, "admin") || strings.Contains(got, fmt.Sprintf("%x", "admin")) {
				t.Errorf("%s: %s shows the host text: %s", name, verb, got)
			}
		}
		if b, err := json.Marshal(hosts[0]); err == nil || b != nil {
			t.Errorf("%s: json.Marshal: %s, %v", name, b, err)
		}
	}
	if got := (Host{}).HostFormat(""); got != "" {
		t.Errorf("the zero host has format %q", got)
	}
}

// RewriteLeaves walks as WalkLeaves and WalkKeys do, the callbacks changing nodes in place, and
// writes the stream back: an embedded JSON document compact with its angle brackets escaped, an
// embedded YAML document and the stream with two-space indentation; a host that is not met fails.
func TestRewriteLeaves(t *testing.T) {
	text := "a: v\nd: '{\"p\": \"q\", \"r\": 1}'\ny: |\n  k: w\n---\nf: x\n"
	host := map[string]string{"doc[0]/d": "json", "doc[0]/y": "yaml"}
	b, err := RewriteLeaves([]byte(text), host, func(p Path, n *yaml.Node) error {
		if n.Kind == yaml.ScalarNode && p.String() != "doc[0]/d|json/r" {
			n.Tag, n.Value = "!!str", "<"+p.String()+">"
		}
		return nil
	}, func(p Path, k *yaml.Node) error {
		if p.String() == "doc[0]/y|yaml/k" {
			k.Value = "<key>"
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "a: <doc[0]/a>\nd: |\n  {\"p\":\"\\u003cdoc[0]/d|json/p\\u003e\",\"r\":1}\ny: |\n  <key>: <doc[0]/y|yaml/k>\n---\nf: <doc[1]/f>\n"
	if string(b) != want {
		t.Errorf("RewriteLeaves =\n%s\nwant\n%s", b, want)
	}
	if _, err := RewriteLeaves([]byte(text), map[string]string{"doc[0]/z": "json"}, nil, nil); err == nil {
		t.Error("an unmet host was accepted")
	}
}

// An alias is written as a copy of its anchored node, so rewriting one occurrence neither changes
// another nor the path of a key that aliases it, and no alias is left without its anchor; inside
// an embedded document too.
func TestRewriteLeavesAliases(t *testing.T) {
	text := "s: &a abc\nt: *a\n? *a\n: 7\ny: |\n  u: &b xyz\n  ? *b\n  : 8\n"
	var seen []string
	b, err := RewriteLeaves([]byte(text), map[string]string{"doc[0]/y": "yaml"}, func(p Path, n *yaml.Node) error {
		if n.Kind == yaml.ScalarNode {
			seen = append(seen, p.String())
			n.Tag, n.Value = "!!str", "<"+p.String()+">"
		}
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"doc[0]/s", "doc[0]/t", "doc[0]/abc", "doc[0]/y|yaml/u", "doc[0]/y|yaml/xyz"}
	if !slices.Equal(seen, want) {
		t.Errorf("visited %v, want %v", seen, want)
	}
	wantText := "s: <doc[0]/s>\nt: <doc[0]/t>\nabc: <doc[0]/abc>\ny: |\n  u: <doc[0]/y|yaml/u>\n  xyz: <doc[0]/y|yaml/xyz>\n"
	if string(b) != wantText {
		t.Errorf("RewriteLeaves =\n%s\nwant\n%s", b, wantText)
	}

	// Nested aliases that would expand to 10^9 nodes are refused, not expanded.
	var bomb strings.Builder
	bomb.WriteString("l0: &l0 [x]\n")
	for i := 1; i <= 9; i++ {
		fmt.Fprintf(&bomb, "l%d: &l%d [", i, i)
		for j := range 10 {
			if j > 0 {
				bomb.WriteString(", ")
			}
			fmt.Fprintf(&bomb, "*l%d", i-1)
		}
		bomb.WriteString("]\n")
	}
	if _, err := RewriteLeaves([]byte(bomb.String()), nil, nil, nil); err != errLeavesAlias {
		t.Error("an alias expansion beyond the limit was accepted")
	}
	// The limit counts the nodes expansion creates, not the document's own.
	if _, err := RewriteLeaves([]byte(strings.Repeat("- x\n", expandLimit)), nil, nil, nil); err != nil {
		t.Errorf("a document without aliases beyond the limit's size was refused: %v", err)
	}
}
