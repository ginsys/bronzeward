package compile

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/configloader"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// The Kubernetes node name Talos derives from a hostname (compilation.md §6.1 item 6). The names and
// hostnames here are synthetic.

const (
	nodeFQDNOn  = "apiVersion: v1alpha1\nkind: KubeNodeConfig\nregisterWithFQDN: true\n"
	nodeFQDNOff = "apiVersion: v1alpha1\nkind: KubeNodeConfig\nregisterWithFQDN: false\n"
)

func hostDoc(hostname string) string {
	return "apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: " + hostname + "\n"
}

// TestNodeNameRules pins the character rules, the specification's sentence copied from Talos
// (nodename.go FromHostname, and the first label unless registerWithFQDN is true).
func TestNodeNameRules(t *testing.T) {
	for _, tc := range []struct {
		host string
		fqdn bool
		want string
	}{
		{"node-a", false, "node-a"},
		{"node-a.example.org", false, "node-a"},
		{"NODE_A.example.org", false, "node-a"},
		{"_node-a_", false, "node-a"},
		{"node-a.example.org", true, "node-a.example.org"},
		{"a.x.org", false, "a"},
		{"a.y.org", false, "a"},
		{"a.x.org", true, "a.x.org"},
		{"nöde-a", false, "nde-a"},
		{"node!-a", false, "node-a"},
		{".node-a.", true, "node-a"},
		{"-.-", true, ""},
		{"___", false, ""},
		{"", false, ""},
		{"", true, ""},
	} {
		if got := nodeName(tc.host, tc.fqdn); got != tc.want {
			t.Errorf("nodeName(%q, %v) = %q, want %q", tc.host, tc.fqdn, got, tc.want)
		}
	}
}

// TestNodeName takes the node name from real compositions: a filled name, an explicit hostname, none,
// and the effective registerWithFQDN of either document.
func TestNodeName(t *testing.T) {
	for _, tc := range []struct {
		name    string
		machine string
		frags   []string
		node    string
		known   bool
	}{
		{"filled, one label", hostMachine, nil, hostMachine, true},
		{"filled, a dotted name: the first label", "a.x.org", nil, "a", true},
		{"filled, a dotted name, registerWithFQDN true: the whole name", "a.x.org", []string{nodeFQDNOn}, "a.x.org", true},
		{"filled, a dotted name, registerWithFQDN false: the first label", "a.x.org", []string{nodeFQDNOff}, "a", true},
		{"explicit and plain", hostMachine, []string{hostDoc("Node_A.Example.org")}, "node-a", true},
		{"explicit, registerWithFQDN true", "", []string{hostDoc("Node_A.Example.org"), nodeFQDNOn}, "node-a.example.org", true},
		{"no hostname at all: never compared", hostMachine, []string{hostDocOff}, "", false},
		{"unnamed, no hostname: never compared", "", nil, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var frags []Source
			for _, f := range tc.frags {
				frags = append(frags, frag(t, f))
			}
			c := compiled(t, hostIn(generated(t), tc.machine, ModeMetal, frags...))
			node, known, err := c.NodeName()
			if err != nil || node != tc.node || known != tc.known {
				t.Errorf("NodeName() = %q, %v, %v; want %q, %v, nil", node, known, err, tc.node, tc.known)
			}
		})
	}
}

// TestNodeNameHeldByAReference: a hostname, or a registerWithFQDN value, that depends on a secret
// reference is skipped and never refused (compilation.md §6.1 item 6): comparing it would make the
// refusal an equality test on a secret. The controls: the same hostname written out is compared, and
// no error comes back for the skipped one.
func TestNodeNameHeldByAReference(t *testing.T) {
	heldHost := source(t, "apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: !bwref app/host\n", strRef("app/host"),
		map[string]provider.Value{"app/host": value(t, provider.KindString, "node-a.example.org")})
	c := compiled(t, hostIn(generated(t), hostMachine, ModeMetal, heldHost))
	if node, known, err := c.NodeName(); err != nil || known || node != "" {
		t.Errorf("held hostname: NodeName() = %q, %v, %v; want it skipped, with no error", node, known, err)
	}
	c = compiled(t, hostIn(generated(t), hostMachine, ModeMetal, frag(t, hostDoc("node-a.example.org"))))
	if node, known, err := c.NodeName(); err != nil || !known || node != "node-a" {
		t.Errorf("control, the same hostname written out: NodeName() = %q, %v, %v; want node-a, true", node, known, err)
	}

	heldFQDN := source(t, "apiVersion: v1alpha1\nkind: KubeNodeConfig\nregisterWithFQDN: !bwref app/fqdn\n",
		refs(map[string]ingest.Reference{"app/fqdn": ref(provider.KindBoolean)}),
		map[string]provider.Value{"app/fqdn": value(t, provider.KindBoolean, true)})
	c = compiled(t, hostIn(generated(t), "a.x.org", ModeMetal, heldFQDN))
	if node, known, err := c.NodeName(); err != nil || known || node != "" {
		t.Errorf("held registerWithFQDN: NodeName() = %q, %v, %v; want it skipped, with no error", node, known, err)
	}
	c = compiled(t, hostIn(generated(t), "a.x.org", ModeMetal, frag(t, nodeFQDNOn)))
	if _, known, err := c.NodeName(); err != nil || !known {
		t.Errorf("control, registerWithFQDN written out: known %v, err %v; want true, nil", known, err)
	}
}

// TestNodeNameUnrelatedSecret: a secret reference that sits elsewhere and happens to resolve to the
// filled name does not hide the node name. The redacted text also redacts that literal at the hostname
// leaf, so a comparison of the two texts would skip the machine and a clash would pass. The control is
// the same reference resolving to another value, and the contrast with the held hostname above, which
// is skipped.
func TestNodeNameUnrelatedSecret(t *testing.T) {
	label := func(v string) Source {
		return source(t, "machine:\n  nodeLabels:\n    s: !bwref app/short\n", strRef("app/short"),
			map[string]provider.Value{"app/short": value(t, provider.KindString, v)})
	}
	for _, v := range []string{"abc", "another-value"} {
		c := compiled(t, hostIn(generated(t), "abc", ModeMetal, label(v)))
		if node, known, err := c.NodeName(); err != nil || !known || node != "abc" {
			t.Errorf("a label resolving to %q: NodeName() = %q, %v, %v; want abc, true, nil", v, node, known, err)
		}
	}
}

// TestNodeNameShowsNoValue: the answer of a skipped hostname holds nothing of its value, and the
// compared one only the derived name.
func TestNodeNameShowsNoValue(t *testing.T) {
	held := source(t, "apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: !bwref app/host\n", strRef("app/host"),
		map[string]provider.Value{"app/host": value(t, provider.KindString, "secret-host")})
	c := compiled(t, hostIn(generated(t), hostMachine, ModeMetal, held))
	node, _, err := c.NodeName()
	if err != nil || strings.Contains(node, "secret-host") {
		t.Errorf("NodeName() = %q, %v: it shows the held value", node, err)
	}
}

// TestRecordedNodeName: the node name a release records is NodeName's, kept only where the review shows
// every input to it as the literal it is (persistence-api.md §9.3). A secret reference that happens to
// resolve to the filled name elsewhere in the composition is compared by NodeName but redacted by value
// at the hostname leaf in the review, so the record is "not compared"; a withheld review (nil) is too.
func TestRecordedNodeName(t *testing.T) {
	label := func(v string) Source {
		return source(t, "machine:\n  nodeLabels:\n    s: !bwref app/short\n", strRef("app/short"),
			map[string]provider.Value{"app/short": value(t, provider.KindString, v)})
	}
	review := func(c Compiled) *string {
		r, err := c.Redacted()
		if err != nil {
			t.Fatal(err)
		}
		return &r
	}
	for _, tc := range []struct {
		name  string
		c     Compiled
		nilR  bool // the review is withheld
		node  string
		known bool
	}{
		{"a filled name, the review shows it", compiled(t, hostIn(generated(t), "abc", ModeMetal, label("another-value"))), false, "abc", true},
		{"an explicit hostname, the review shows it", compiled(t, hostIn(generated(t), hostMachine, ModeMetal, frag(t, hostDoc("node-a.example.org")))), false, "node-a", true},
		{"a secret elsewhere resolves to the filled name: the review redacts it by value", compiled(t, hostIn(generated(t), "abc", ModeMetal, label("abc"))), false, "", false},
		{"the review is withheld in whole", compiled(t, hostIn(generated(t), "abc", ModeMetal, label("another-value"))), true, "", false},
		{"no hostname is set", compiled(t, hostIn(generated(t), "", ModeMetal)), false, "", false},
	} {
		var r *string
		if !tc.nilR {
			r = review(tc.c)
		}
		node, known, err := tc.c.RecordedNodeName(r)
		if err != nil || known != tc.known || node != tc.node {
			t.Errorf("%s: RecordedNodeName = %q, %v, %v; want %q, %v, nil", tc.name, node, known, err, tc.node, tc.known)
		}
	}
}

// kubeletFQDN sets the v1alpha1 machine.kubelet.registerWithFQDN of a generated stream, and docFQDN the
// field of its KubeNodeConfig document ("" leaves the document's own, unset). Each insertion must land.
func kubeletFQDN(t *testing.T, stream, v string) string {
	t.Helper()
	// The generated stream holds the kubelet's image in a KubeletConfig document, which the loader
	// refuses beside a v1alpha1 machine.kubelet: that document is left out, and the field added.
	var docs []string
	for _, d := range strings.Split(stream, "\n---\n") {
		if !strings.Contains(d, "kind: KubeletConfig") {
			docs = append(docs, d)
		}
	}
	stream = strings.Join(docs, "\n---\n")
	re := regexp.MustCompile(`(?m)^machine:[^\n]*\n`)
	if !re.MatchString(stream) {
		t.Fatal("the generated stream holds no machine section")
	}
	return re.ReplaceAllString(stream, "machine:\n    kubelet:\n        registerWithFQDN: "+v+"\n") // the encoder indents by four
}

func docFQDN(t *testing.T, stream, v string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^kind: KubeNodeConfig[^\n]*\n`)
	if !re.MatchString(stream) {
		t.Fatal("the generated stream holds no KubeNodeConfig document")
	}
	return re.ReplaceAllString(stream, "kind: KubeNodeConfig\nregisterWithFQDN: "+v+"\n")
}

// TestNodeInputsKubeNodeConfigWins pins where the precedence is decided (compilation.md §6.1 item 6): the
// effective registerWithFQDN is the KubeNodeConfig document's field, and the v1alpha1 machine.kubelet one
// only where no such document is present. A document that leaves the field unset therefore means false,
// whatever the legacy field holds. The controls show each side is read.
func TestNodeInputsKubeNodeConfigWins(t *testing.T) {
	full := string(generatedStream(t))  // holds a KubeNodeConfig document, field unset
	trimmed := string(generatedBase(t)) // no KubeNodeConfig document
	for _, tc := range []struct {
		name   string
		stream string
		want   bool
	}{
		{"document unset, legacy true: the document wins", kubeletFQDN(t, full, "true"), false},
		{"document false, legacy true: the document wins", kubeletFQDN(t, docFQDN(t, full, "false"), "true"), false},
		{"document true, legacy false: the document wins", kubeletFQDN(t, docFQDN(t, full, "true"), "false"), true},
		{"control, no document, legacy true: the legacy field is read", kubeletFQDN(t, trimmed, "true"), true},
		{"control, no document, legacy false", kubeletFQDN(t, trimmed, "false"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := configloader.NewFromBytes([]byte(tc.stream)); err != nil {
				t.Fatalf("the loader refuses the stream: %v", err)
			}
			_, fqdn, ok := nodeInputs([]byte(tc.stream))
			if !ok || fqdn != tc.want {
				t.Errorf("nodeInputs: fqdn %v, ok %v; want %v, true", fqdn, ok, tc.want)
			}
		})
	}
}

// TestCompileKubeletFQDNBesideKubeNodeConfig states what a composition holding both does, as a v1alpha1
// nodeLabels fragment over a base with a KubeNodeConfig document does (TestCompileNodeLabelsGoThroughKubeNodeConfig):
// the v1alpha1 machine.kubelet.registerWithFQDN fragment is refused as invalid, and a KubeNodeConfig
// fragment with the same value composes. The refusal is why a stored stream holding both is a case of
// nodeInputs only.
func TestCompileKubeletFQDNBesideKubeNodeConfig(t *testing.T) {
	base := source(t, string(generatedStream(t)), ingest.Declarations{}, nil)
	in := func(frag string) Input {
		return Input{Base: base, Fragments: []Source{source(t, frag, ingest.Declarations{}, nil)}, Mode: ModeMetal, Name: "a.x.org"}
	}
	_, err := Compile(in("machine:\n  kubelet:\n    registerWithFQDN: true\n"))
	var e *Error
	if !errors.As(err, &e) || e.Rule != RuleInvalid {
		t.Fatalf("a v1alpha1 registerWithFQDN fragment beside a KubeNodeConfig document: got %v, want an invalid verdict", err)
	}
	if _, err := Compile(in(nodeFQDNOn)); err != nil {
		t.Fatalf("a KubeNodeConfig fragment with the same value: %v", err)
	}
}
