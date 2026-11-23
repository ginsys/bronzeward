package compile

import (
	"strings"
	"testing"

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
