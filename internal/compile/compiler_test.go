package compile

import (
	"bytes"
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

func refs(kinds map[string]ingest.Reference) ingest.Declarations {
	return ingest.Declarations{References: kinds}
}

func ref(k provider.Kind) ingest.Reference { return ingest.Reference{Kind: k, Version: 1} }

func compiled(t *testing.T, in Input) Compiled {
	t.Helper()
	c, err := Compile(in)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// pathsOf is every output path attributed to the tracers of reference name, in tracer order.
func pathsOf(c Compiled, name string) []string {
	var out []string
	for _, o := range c.outcomes {
		if o.tracer.Ref() == name {
			out = append(out, o.paths...)
		}
	}
	return out
}

// Every reference of a generated base, the schema secrets ingestion extracts from it, is
// attributed to its output leaf. Three of them are byte fields the machinery decodes and encodes
// again canonically, which only the canonical base64 match attributes (the probe case).
func TestCompileAttributesTheGeneratedBase(t *testing.T) {
	text := string(generatedBase(t))
	c := compiled(t, Input{Base: source(t, text, ingest.Declarations{}, nil), Mode: ModeMetal})
	if len(c.outcomes) < 9 {
		t.Fatalf("%d occurrences, want the generated base's schema secrets", len(c.outcomes))
	}
	var all []string
	for _, o := range c.outcomes {
		if len(o.paths) == 0 {
			t.Errorf("occurrence at %s is not attributed", o.tracer.Path())
		}
		all = append(all, o.paths...)
	}
	for _, p := range []string{"doc[0]/cluster/ca/key", "doc[0]/cluster/aggregatorCA/key", "doc[0]/cluster/etcd/ca/key"} {
		if !slices.Contains(all, p) {
			t.Errorf("%s is not attributed; attributed: %v", p, all)
		}
	}
	m, err := Compose(resolved(t, text, ingest.Declarations{}, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(c.Materialized().bytes(), m.bytes()) {
		t.Fatal("the compiled configuration is not the real composition")
	}
}

// Each kind of reference is attributed to the output leaves holding it: a string, an integer, a
// boolean (by its flip pass), a base64-encoded string, each member of a mapping, both places an
// alias puts one value, and a value inside identified embedded JSON and YAML (compilation.md §8.1).
func TestCompileAttributesEachKind(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	labels := source(t, "machine:\n  nodeLabels:\n    s: &a !bwref app/str\n    t: *a\n    e: !bwref app/enc\n"+
		"  nodeAnnotations: !bwref app/map\n  features:\n    rbac: !bwref app/bool\n    kubePrism:\n      port: !bwref app/int\n",
		refs(map[string]ingest.Reference{
			"app/str": ref(provider.KindString), "app/int": ref(provider.KindInteger), "app/bool": ref(provider.KindBoolean),
			"app/map": ref(provider.KindMapping), "app/enc": {Kind: provider.KindString, Version: 1, Encoding: "base64"},
		}),
		map[string]provider.Value{
			"app/str":  value(t, provider.KindString, compileSecret),
			"app/int":  value(t, provider.KindInteger, 7445),
			"app/bool": value(t, provider.KindBoolean, false),
			"app/map":  value(t, provider.KindMapping, map[string]any{"one": "first-" + compileSecret, "two": "second-" + compileSecret}),
			"app/enc":  value(t, provider.KindString, compileSecret),
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
			"app/json": value(t, provider.KindString, "json-"+compileSecret+"&<>\""),
			"app/yaml": value(t, provider.KindString, "yaml-"+compileSecret),
		})
	c := compiled(t, Input{Base: base, Fragments: []Source{labels, manifests}, Mode: ModeMetal})
	for name, want := range map[string][]string{
		"app/str":  {"doc[0]/machine/nodeLabels/s", "doc[0]/machine/nodeLabels/t"},
		"app/enc":  {"doc[0]/machine/nodeLabels/e"},
		"app/map":  {"doc[0]/machine/nodeAnnotations/one", "doc[0]/machine/nodeAnnotations/two"},
		"app/bool": {"doc[0]/machine/features/rbac"},
		"app/int":  {"doc[0]/machine/features/kubePrism/port"},
		"app/json": {"doc[0]/cluster/inlineManifests/0/contents|json/password"},
		"app/yaml": {"doc[0]/cluster/inlineManifests/1/contents|yaml/password"},
	} {
		if got := pathsOf(c, name); !slices.Equal(got, want) {
			t.Errorf("%s attributed to %v, want %v", name, got, want)
		}
	}
	for _, o := range c.outcomes {
		if o.tracer.Ref() == "app/str" && len(o.paths) != 2 {
			t.Errorf("the aliased occurrence has %d paths, want one tracer at both", len(o.paths))
		}
	}
}

// A real rejection or an invalid real configuration is the machinery's verdict on the real
// input, returned as Compose and Validate return it; the trace pass does not replace it.
func TestCompileReturnsTheRealVerdict(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	port := source(t, "machine:\n  features:\n    kubePrism:\n      port: !bwref port\n", strRef("port"),
		map[string]provider.Value{"port": value(t, provider.KindString, compileSecret)})
	_, err := Compile(Input{Base: base, Fragments: []Source{port}, Mode: ModeMetal})
	var e *Error
	if !errors.As(err, &e) || e.Rule != RuleRejected || e.Input != "fragment[0]" {
		t.Fatalf("got %v, want a rejection of fragment[0]", err)
	}
	domain := source(t, "cluster:\n  network:\n    dnsDomain: !bwref domain\n", strRef("domain"),
		map[string]provider.Value{"domain": value(t, provider.KindString, "compile not a domain 7a3c")})
	if _, err := Compile(Input{Base: base, Fragments: []Source{domain}, Mode: ModeMetal}); !errors.As(err, &e) || e.Rule != RuleInvalid {
		t.Fatalf("got %v, want an invalid verdict", err)
	}
	unresolved := source(t, "machine:\n  nodeLabels:\n    a: !bwref gone\n", strRef("gone"), nil)
	unresolved.Values = nil
	var r *ingest.Refusal
	if _, err := Compile(Input{Base: base, Fragments: []Source{unresolved}, Mode: ModeMetal}); !errors.As(err, &r) || r.Rule != ingest.RuleUnresolved {
		t.Fatalf("got %v, want the unresolved refusal", err)
	}
	if _, err := Compile(Input{Base: base, Mode: Mode("docker")}); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
}

// One pass validating while another does not refuses with fidelity: here the real address is
// valid and its stand-in is not (compilation.md §8.1).
func TestCompileTraceInvalidIsFidelity(t *testing.T) {
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	addr := source(t, "machine:\n  network:\n    interfaces:\n      - interface: eth0\n        addresses:\n          - !bwref addr\n",
		strRef("addr"), map[string]provider.Value{"addr": value(t, provider.KindString, "192.0.2.5/24")})
	real, err := Compose(resolved(t, string(generatedBase(t)), ingest.Declarations{}, nil), []ingest.Resolved{mustResolve(t, addr)})
	if err != nil {
		t.Fatal(err)
	}
	if err := real.Validate(ModeMetal); err != nil {
		t.Fatalf("control: the real configuration is invalid: %v", err)
	}
	_, err = Compile(Input{Base: base, Fragments: []Source{addr}, Mode: ModeMetal})
	var e *Error
	if !errors.As(err, &e) || e.Rule != RuleFidelity {
		t.Fatalf("got %v, want a fidelity refusal", err)
	}
}

func mustResolve(t *testing.T, s Source) ingest.Resolved {
	t.Helper()
	r, err := ingest.Resolve(s.Text, s.Values)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// passes composes one fragment's real and trace passes onto the generated base.
func passes(t *testing.T, frag Source) (real, trace []byte, ts []traced) {
	t.Helper()
	base := source(t, string(generatedBase(t)), ingest.Declarations{}, nil)
	var rs, trs []ingest.Resolved
	next := 0
	for i, s := range []Source{base, frag} {
		rs = append(rs, mustResolve(t, s))
		r, xs, err := ingest.Trace(s.Text, s.Values, next, -1)
		if err != nil {
			t.Fatal(err)
		}
		trs = append(trs, r)
		for _, x := range xs {
			ts = append(ts, traced{x, i})
		}
		next += len(xs)
	}
	rm, err := Compose(rs[0], rs[1:])
	if err != nil {
		t.Fatal(err)
	}
	tm, err := Compose(trs[0], trs[1:])
	if err != nil {
		t.Fatal(err)
	}
	return rm.bytes(), tm.bytes(), ts
}

// mutate parses a composed stream, changes it and writes it back.
func mutate(t *testing.T, b []byte, change func(machine *yaml.Node)) []byte {
	t.Helper()
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var docs []*yaml.Node
	for {
		var n yaml.Node
		if err := dec.Decode(&n); err != nil {
			break
		}
		docs = append(docs, &n)
	}
	change(child(docs[0].Content[0], "machine"))
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	for _, d := range docs {
		if err := enc.Encode(d); err != nil {
			t.Fatal(err)
		}
	}
	return out.Bytes()
}

func child(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	panic("no key " + key)
}

// The fidelity check fires on an injected structural change in the trace output, and on a leaf
// that differs from the real one without carrying a tracer (compilation.md §8.1, §15: a check
// that fires on an injected structural change).
func TestFidelityFailsOnAnInjectedChange(t *testing.T) {
	frag := source(t, "machine:\n  nodeLabels:\n    team: !bwref team\n  features:\n    rbac: !bwref rbac\n",
		refs(map[string]ingest.Reference{"team": ref(provider.KindString), "rbac": ref(provider.KindBoolean)}),
		map[string]provider.Value{"team": value(t, provider.KindString, compileSecret), "rbac": value(t, provider.KindBoolean, true)})
	real, trace, ts := passes(t, frag)
	if _, _, err := attribute(real, trace, ts); err != nil {
		t.Fatalf("control: the unchanged passes fail: %v", err)
	}
	for name, change := range map[string]func(*yaml.Node){
		"a key added": func(m *yaml.Node) {
			m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "injected"}, &yaml.Node{Kind: yaml.ScalarNode, Value: "x"})
		},
		"a key renamed": func(m *yaml.Node) { child(m, "nodeLabels").Content[0].Value = "teams" },
		"a scalar made a sequence": func(m *yaml.Node) {
			l := child(m, "nodeLabels")
			l.Content[1] = &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{l.Content[1]}}
		},
		"a tag changed": func(m *yaml.Node) {
			p := child(child(child(m, "features"), "kubePrism"), "port")
			p.Tag, p.Style = "!!str", yaml.DoubleQuotedStyle
		},
		"an untraced leaf changed": func(m *yaml.Node) { child(m, "install").Content[1].Value = "/dev/injected" },
	} {
		_, _, err := attribute(real, mutate(t, trace, change), ts)
		var e *Error
		if !errors.As(err, &e) || e.Rule != RuleFidelity {
			t.Errorf("%s: got %v, want a fidelity refusal", name, err)
		}
	}
	// A leaf equal in both passes that carries a tracer fails too: the stand-in is then not the
	// value's, and attributing it would be a guess (SP's rule).
	team := ts[len(ts)-2]
	if team.Ref() != "team" {
		t.Fatalf("control: tracer %d is %s", team.ID(), team.Ref())
	}
	standIn := fmt.Sprintf("zq%03dxx-xxxx-xxxxxx-0x0x", team.ID())
	if !team.Carried(standIn) {
		t.Fatal("control: the computed stand-in is not the tracer's")
	}
	planted := func(m *yaml.Node) { child(m, "install").Content[1].Value = standIn }
	var fe *Error
	if _, _, err := attribute(mutate(t, real, planted), mutate(t, trace, planted), ts); !errors.As(err, &fe) || fe.Rule != RuleFidelity {
		t.Errorf("a tracer in an unchanged leaf: got %v, want a fidelity refusal", err)
	}
	// A flip pass is held to the trace pass's shape the same way.
	_, hosts, _ := attribute(real, trace, ts)
	traceLeaves, err := leaves(trace, hosts)
	if err != nil {
		t.Fatal(err)
	}
	flip := mutate(t, trace, func(m *yaml.Node) {
		f := child(m, "features")
		f.Content = append(f.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "injected"}, &yaml.Node{Kind: yaml.ScalarNode, Value: "x"})
	})
	var e *Error
	if _, err := flipped(traceLeaves, flip, hosts); !errors.As(err, &e) || e.Rule != RuleFidelity {
		t.Errorf("a flip pass with a key added: got %v, want a fidelity refusal", err)
	}
}

// The compiled result and its errors never render a value or a stand-in.
func TestCompiledNeverRendersValues(t *testing.T) {
	frag := source(t, "machine:\n  nodeLabels:\n    team: !bwref team\n", strRef("team"),
		map[string]provider.Value{"team": value(t, provider.KindString, compileSecret)})
	c := compiled(t, Input{Base: source(t, string(generatedBase(t)), ingest.Declarations{}, nil), Fragments: []Source{frag}, Mode: ModeMetal})
	encoded := base64.StdEncoding.EncodeToString([]byte(compileSecret))
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		for _, x := range []any{c, &c} {
			s := fmt.Sprintf(verb, x)
			if strings.Contains(s, compileSecret) || strings.Contains(s, encoded) || strings.Contains(s, "zq0") {
				t.Fatalf("%s renders a value or stand-in: %s", verb, s)
			}
		}
	}
}
