package discover

import (
	"context"
	"net"
	"net/netip"
	"testing"

	"github.com/ginsys/bronzeward/internal/talos/talostest"
)

// TestEndpointServerName: the endpoint connection is checked against a name's certificate names while
// the dial stays on the address, whether the walk resolved the name (Discover) or the caller did
// (DiscoverLimited's serverName). The cluster is one node that lists only itself, so the read of the
// endpoint is the whole discovery; its member is dialled by address with no server name and is not the
// subject here.
func TestEndpointServerName(t *testing.T) {
	const name = "k8s.example.net"
	loop := []net.IP{net.ParseIP("127.0.0.1")}
	tests := []struct {
		name string
		dns  []string
		ips  []net.IP
		run  func(c *oneNode) (Result, error) // what the test calls
		ok   bool
		// memberNotAnswered: the one member, dialled at its address with no server name, is refused by
		// a certificate that lists the name only. Were the name passed on to the member dial, it would answer.
		memberNotAnswered bool
	}{
		{name: "the caller resolved, the certificate lists the name only", dns: []string{name}, ok: true, memberNotAnswered: true,
			run: func(c *oneNode) (Result, error) { return c.limited(t, c.addr, name) }},
		{name: "the caller resolved, no name is given", dns: []string{name},
			run: func(c *oneNode) (Result, error) { return c.limited(t, c.addr, "") }},
		{name: "the caller resolved, the certificate lists neither", dns: []string{"other.example.net"}, ips: []net.IP{net.ParseIP("127.0.0.9")},
			run: func(c *oneNode) (Result, error) { return c.limited(t, c.addr, name) }},
		{name: "the caller resolved, the certificate lists the address only", ips: loop,
			run: func(c *oneNode) (Result, error) { return c.limited(t, c.addr, name) }},
		{name: "a typed address, the certificate lists it", ips: loop, ok: true,
			run: func(c *oneNode) (Result, error) { return c.limited(t, c.addr, "") }},
		{name: "a typed address, the certificate lists a name only", dns: []string{name},
			run: func(c *oneNode) (Result, error) { return c.discover(t, c.addr) }},
		{name: "the walk resolved the name, the certificate lists the name only", dns: []string{name}, ok: true,
			run: func(c *oneNode) (Result, error) { return c.discover(t, net.JoinHostPort(name, c.port)) }},
		{name: "the walk resolved the name, the certificate lists the address only", ips: loop,
			run: func(c *oneNode) (Result, error) { return c.discover(t, net.JoinHostPort(name, c.port)) }},
		{name: "the walk resolved the name, the certificate lists neither", dns: []string{"other.example.net"}, ips: []net.IP{net.ParseIP("127.0.0.9")},
			run: func(c *oneNode) (Result, error) { return c.discover(t, net.JoinHostPort(name, c.port)) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newOneNode(t, tc.dns, tc.ips, name)
			got, err := tc.run(c)
			if tc.ok {
				if err != nil || got.ClusterID != "c-1" {
					t.Fatalf("got %+v, %v; want the cluster read", got, err)
				}
				if tc.memberNotAnswered && (len(got.Members) != 1 || got.Members[0].Answered) {
					t.Fatalf("the member was dialled with the name: %+v, want one member that did not answer", got.Members)
				}
				return
			}
			if err == nil {
				t.Fatalf("the certificate was accepted: %+v", got)
			}
		})
	}
}

// oneNode is a cluster of one node whose certificate names dns and ips.
type oneNode struct {
	p          talostest.PKI
	addr, port string
}

func newOneNode(t *testing.T, dns []string, ips []net.IP, name string) *oneNode {
	t.Helper()
	p := talostest.NewPKIFor(t, dns, ips)
	n := talostest.Serve(t, p)
	n.SetClusterID(t, "c-1")
	n.SetNodeID(t, "n1")
	n.SetPlatform(t, "metal")
	n.SetSMBIOSUUID(t, "", false)
	n.SetMembers(t, talostest.Member{NodeID: "n1", Hostname: "cp-1", Addresses: []string{"10.0.0.1"}, ControlPlanePort: 6443})
	_, port, err := net.SplitHostPort(n.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	old, oldLookup := memberEndpoint, lookup
	memberEndpoint = func(netip.Addr) string { return n.Endpoint }
	lookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == name {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return nil, net.UnknownNetworkError("no such name")
	}
	t.Cleanup(func() { memberEndpoint, lookup = old, oldLookup })
	return &oneNode{p: p, addr: n.Endpoint, port: port}
}

func (c *oneNode) limited(t *testing.T, endpoint, serverName string) (Result, error) {
	t.Helper()
	return DiscoverLimited(ctx20(t), c.p.Talosconfig(c.addr, "", ""), "", endpoint, serverName, 0)
}

func (c *oneNode) discover(t *testing.T, endpoint string) (Result, error) {
	t.Helper()
	return Discover(ctx20(t), c.p.Talosconfig(c.addr, "", ""), "", endpoint)
}
