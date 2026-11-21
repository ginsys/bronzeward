package discover

import (
	"net"
	"net/netip"
	"testing"

	"github.com/ginsys/bronzeward/internal/talos/talostest"
	"google.golang.org/grpc/codes"
)

// ep is one KubePrismEndpoints entry.
func ep(host string, port uint32) talostest.Endpoint {
	return talostest.Endpoint{Host: host, Port: port}
}

// cpStand is a stand-in cluster of two control-plane members, cp1 (10.0.0.1, node n1, the one the
// walk dials) and cp2 (10.0.0.2, node n2), and one worker (10.0.0.3, node n3). Every member answers;
// the test sets what each reports as its KubePrism endpoints, and may close a member's listener.
type cpStand struct {
	p            talostest.PKI
	cp1, cp2, wk *talostest.Node
	by           map[string]string
}

func newCPStand(t *testing.T) *cpStand {
	t.Helper()
	p := talostest.NewPKI(t)
	c := &cpStand{p: p, cp1: talostest.Serve(t, p), cp2: talostest.Serve(t, p), wk: talostest.Serve(t, p)}
	for i, n := range []*talostest.Node{c.cp1, c.cp2, c.wk} {
		n.SetNodeID(t, []string{"n1", "n2", "n3"}[i])
		n.SetPlatform(t, "metal")
		n.SetSMBIOSUUID(t, "", false)
	}
	c.cp1.SetClusterID(t, "c-1")
	c.cp1.SetMembers(t,
		talostest.Member{NodeID: "n1", Hostname: "cp-1", Addresses: []string{"10.0.0.1"}, ControlPlanePort: 6443},
		talostest.Member{NodeID: "n2", Hostname: "cp-2", Addresses: []string{"10.0.0.2"}, ControlPlanePort: 6443},
		talostest.Member{NodeID: "n3", Hostname: "w-1", Addresses: []string{"10.0.0.3"}},
	)
	c.by = map[string]string{"10.0.0.1": c.cp1.Endpoint, "10.0.0.2": c.cp2.Endpoint, "10.0.0.3": c.wk.Endpoint}
	old := memberEndpoint
	memberEndpoint = func(a netip.Addr) string { return c.by[a.String()] }
	t.Cleanup(func() { memberEndpoint = old })
	return c
}

// down makes the member at address unreachable.
func (c *cpStand) down(t *testing.T, address string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c.by[address] = l.Addr().String()
	l.Close()
}

func (c *cpStand) read(t *testing.T) Result {
	t.Helper()
	got, err := Discover(ctx20(t), c.p.Talosconfig(c.cp1.Endpoint, "", ""), "", c.cp1.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestKubernetesEndpoint: the proposal is the cluster's own endpoint when the control-plane members
// that answered all report the same one, and the first control-plane member's address in every other
// case, and a member that answered stays answered whatever it reported.
func TestKubernetesEndpoint(t *testing.T) {
	const first = "https://10.0.0.1:6443" // the first control-plane member's address and port
	vip := []talostest.Endpoint{ep("vip.example.net", 6443), ep("10.0.0.1", 6443)}
	tests := []struct {
		name       string
		cp1, cp2   []talostest.Endpoint
		wk         []talostest.Endpoint
		setup      func(t *testing.T, c *cpStand)
		want       string
		unanswered int // the members the setup takes down
	}{
		{name: "equal on both members", cp1: vip, cp2: vip, want: "https://vip.example.net:6443"},
		{name: "an IPv4 literal", cp1: []talostest.Endpoint{ep("10.0.0.9", 443)}, cp2: []talostest.Endpoint{ep("10.0.0.9", 443)}, want: "https://10.0.0.9:443"},
		{name: "an IPv6 literal", cp1: []talostest.Endpoint{ep("2001:db8::1", 6443)}, cp2: []talostest.Endpoint{ep("2001:db8::1", 6443)}, want: "https://[2001:db8::1]:6443"},
		{name: "a name in upper case is a name", cp1: []talostest.Endpoint{ep("K8s.Example.NET", 6443)}, cp2: []talostest.Endpoint{ep("K8s.Example.NET", 6443)}, want: "https://K8s.Example.NET:6443"},
		{name: "members disagree", cp1: vip, cp2: []talostest.Endpoint{ep("other.example.net", 6443)}, want: first},
		{name: "same host, another port", cp1: vip, cp2: []talostest.Endpoint{ep("vip.example.net", 443)}, want: first},
		{name: "a member that did not answer does not count", cp1: vip, cp2: []talostest.Endpoint{ep("other.example.net", 6443)},
			setup: func(t *testing.T, c *cpStand) { c.down(t, "10.0.0.2") }, want: "https://vip.example.net:6443", unanswered: 1},
		{name: "no control-plane member answered", cp1: vip, cp2: vip,
			setup: func(t *testing.T, c *cpStand) { c.down(t, "10.0.0.1"); c.down(t, "10.0.0.2") }, want: first, unanswered: 2},
		{name: "the dialled member reports localhost: no value", cp1: []talostest.Endpoint{ep("localhost", 7445), ep("10.0.0.1", 6443)}, cp2: vip, want: first},
		{name: "a member reports no resource: no value", cp1: vip, want: first},
		{name: "a member reports no entry: no value", cp1: vip, cp2: []talostest.Endpoint{}, want: first},
		{name: "a refused read is no value, and the member stays answered", cp1: vip, cp2: vip,
			setup: func(t *testing.T, c *cpStand) { c.cp2.Fail(talostest.KubePrismType, codes.PermissionDenied) }, want: first},
		{name: "a worker's value is ignored", cp1: vip, cp2: vip, wk: []talostest.Endpoint{ep("worker.example.net", 6443)}, want: "https://vip.example.net:6443"},
		{name: "a worker alone gives no value", wk: vip, want: first},
		{name: "a host holding a slash", cp1: []talostest.Endpoint{ep("a/b.example.net", 6443)}, cp2: []talostest.Endpoint{ep("a/b.example.net", 6443)}, want: first},
		{name: "a host holding an at sign", cp1: []talostest.Endpoint{ep("user@vip.example.net", 6443)}, cp2: []talostest.Endpoint{ep("user@vip.example.net", 6443)}, want: first},
		{name: "a host with a scheme", cp1: []talostest.Endpoint{ep("https://vip.example.net", 6443)}, cp2: []talostest.Endpoint{ep("https://vip.example.net", 6443)}, want: first},
		{name: "an empty host", cp1: []talostest.Endpoint{ep("", 6443)}, cp2: []talostest.Endpoint{ep("", 6443)}, want: first},
		{name: "a name with a trailing dot", cp1: []talostest.Endpoint{ep("vip.example.net.", 6443)}, cp2: []talostest.Endpoint{ep("vip.example.net.", 6443)}, want: first},
		{name: "a name with an underscore", cp1: []talostest.Endpoint{ep("vip_1.example.net", 6443)}, cp2: []talostest.Endpoint{ep("vip_1.example.net", 6443)}, want: first},
		{name: "localhost in upper case", cp1: []talostest.Endpoint{ep("LocalHost", 6443)}, cp2: []talostest.Endpoint{ep("LocalHost", 6443)}, want: first},
		{name: "an unspecified address", cp1: []talostest.Endpoint{ep("0.0.0.0", 6443)}, cp2: []talostest.Endpoint{ep("0.0.0.0", 6443)}, want: first},
		{name: "an address with a zone", cp1: []talostest.Endpoint{ep("fe80::1%eth0", 6443)}, cp2: []talostest.Endpoint{ep("fe80::1%eth0", 6443)}, want: first},
		{name: "a port above 65535", cp1: []talostest.Endpoint{ep("vip.example.net", 65536)}, cp2: []talostest.Endpoint{ep("vip.example.net", 65536)}, want: first},
		{name: "port 0", cp1: []talostest.Endpoint{ep("vip.example.net", 0)}, cp2: []talostest.Endpoint{ep("vip.example.net", 0)}, want: first},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newCPStand(t)
			for n, es := range map[*talostest.Node][]talostest.Endpoint{c.cp1: tc.cp1, c.cp2: tc.cp2, c.wk: tc.wk} {
				if es != nil {
					n.SetKubePrismEndpoints(t, es...)
				}
			}
			if tc.setup != nil {
				tc.setup(t, c)
			}
			got := c.read(t)
			if got.KubernetesEndpoint != tc.want {
				t.Errorf("KubernetesEndpoint %q, want %q", got.KubernetesEndpoint, tc.want)
			}
			// A read of the endpoint never decides whether a member answered: only the members the
			// setup took down are unanswered.
			var gone int
			for _, m := range got.Members {
				if !m.Answered {
					gone++
				}
			}
			if gone != tc.unanswered {
				t.Errorf("%d members unanswered, want %d: %+v", gone, tc.unanswered, got.Members)
			}
		})
	}
}

// TestKubernetesEndpointNoControlPlane: a cluster that lists no control-plane member has no proposal.
func TestKubernetesEndpointNoControlPlane(t *testing.T) {
	c := newCPStand(t)
	c.cp1.SetMembers(t, talostest.Member{NodeID: "n3", Hostname: "w-1", Addresses: []string{"10.0.0.3"}})
	c.wk.SetKubePrismEndpoints(t, ep("vip.example.net", 6443))
	if got := c.read(t); got.KubernetesEndpoint != "" {
		t.Errorf("KubernetesEndpoint %q, want none", got.KubernetesEndpoint)
	}
}
