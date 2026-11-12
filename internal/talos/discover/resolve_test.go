package discover

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// names replaces the resolver for the test with a table of answers and reports the names it was asked
// for, in order. A name that is not in the table does not resolve.
func names(t *testing.T, table map[string][]string) func() []string {
	t.Helper()
	old := lookup
	var mu sync.Mutex
	var asked []string
	lookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		mu.Lock()
		asked = append(asked, host)
		mu.Unlock()
		ips, ok := table[host]
		if !ok {
			return nil, errors.New("lookup " + host + ": no such host")
		}
		var out []netip.Addr
		for _, s := range ips {
			out = append(out, netip.MustParseAddr(s))
		}
		return out, nil
	}
	t.Cleanup(func() { lookup = old })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), asked...)
	}
}

func portOf(t *testing.T, endpoint string) string {
	t.Helper()
	_, p, err := net.SplitHostPort(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func ctx20(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// TestDiscoverResolvesTheEndpointFlag: a DNS name from --endpoint is resolved once, the address it
// gave is dialled, and the result names both.
func TestDiscoverResolvesTheEndpointFlag(t *testing.T) {
	c := newCluster(t)
	asked := names(t, map[string][]string{"node.test": {"127.0.0.1"}})
	got, err := Discover(ctx20(t), c.p.Talosconfig(c.a.Endpoint, "", ""), "node.test:"+portOf(t, c.a.Endpoint))
	if err != nil || got.ClusterID != "c-1" {
		t.Fatalf("a name from --endpoint: %+v, %v", got, err)
	}
	if got.Endpoint != c.a.Endpoint || got.ResolvedFrom != "node.test" {
		t.Errorf("the result names endpoint %q resolved from %q, want %q from %q", got.Endpoint, got.ResolvedFrom, c.a.Endpoint, "node.test")
	}
	if want := []string{"node.test"}; len(asked()) != 1 || asked()[0] != want[0] {
		t.Errorf("the resolver was asked for %q, want %q once", asked(), want)
	}
}

// TestDiscoverResolvesTheFilesFirstEndpoint: with no --endpoint, a DNS name in the talosconfig is
// resolved the same way.
func TestDiscoverResolvesTheFilesFirstEndpoint(t *testing.T) {
	c := newCluster(t)
	asked := names(t, map[string][]string{"node.test": {"127.0.0.1"}})
	got, err := Discover(ctx20(t), c.p.Talosconfig("node.test:"+portOf(t, c.a.Endpoint)+", "+c.closed, "", ""), "")
	if err != nil || got.ClusterID != "c-1" {
		t.Fatalf("a name from the file: %+v, %v", got, err)
	}
	if got.Endpoint != c.a.Endpoint || got.ResolvedFrom != "node.test" {
		t.Errorf("the result names endpoint %q resolved from %q", got.Endpoint, got.ResolvedFrom)
	}
	if len(asked()) != 1 {
		t.Errorf("the resolver was asked %q, want one name once", asked())
	}
}

// TestDiscoverIPLiteralIsNotResolved: an IP literal goes to no resolver and the result names no
// source name.
func TestDiscoverIPLiteralIsNotResolved(t *testing.T) {
	c := newCluster(t)
	asked := names(t, nil)
	got, err := c.discover(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(asked()) != 0 || got.ResolvedFrom != "" || got.Endpoint != c.a.Endpoint {
		t.Errorf("asked %q, resolved from %q, endpoint %q", asked(), got.ResolvedFrom, got.Endpoint)
	}
}

// TestDiscoverNameThatDoesNotResolve: the refusal names where the value came from, quotes neither
// the name nor the talosconfig, and sends nothing to any node.
func TestDiscoverNameThatDoesNotResolve(t *testing.T) {
	c := newCluster(t)
	names(t, nil)
	seen := len(c.a.Seen())
	for _, tc := range []struct {
		name, endpoint, file, source string
	}{
		{"flag", "gone.test:" + portOf(t, c.a.Endpoint), c.a.Endpoint, "--endpoint"},
		{"file", "", "gone.test:" + portOf(t, c.a.Endpoint), "the talosconfig's first endpoint"},
	} {
		_, err := Discover(ctx20(t), c.p.Talosconfig(tc.file, "", ""), tc.endpoint)
		if err == nil || !strings.Contains(err.Error(), tc.source) {
			t.Errorf("%s: %v, want a refusal that names %q", tc.name, err, tc.source)
			continue
		}
		if strings.Contains(err.Error(), "gone.test") {
			t.Errorf("%s: the refusal quotes the name: %v", tc.name, err)
		}
		for _, secret := range leaked(err.Error(), c.p) {
			t.Errorf("%s: the refusal quotes %.20q", tc.name, secret)
		}
	}
	if len(c.a.Seen()) != seen {
		t.Error("a request reached the node although the name did not resolve")
	}
}

// TestDiscoverRefusesAnUnusableAnswer: a name that resolves to no address, or only to one that
// cannot be dialled as a node (unspecified, multicast, a zone), is a name that does not resolve.
func TestDiscoverRefusesAnUnusableAnswer(t *testing.T) {
	c := newCluster(t)
	names(t, map[string][]string{
		"empty.test": {}, "any.test": {"0.0.0.0", "::"}, "multi.test": {"224.0.0.1"}, "zone.test": {"fe80::1%eth0"},
	})
	for _, name := range []string{"empty.test", "any.test", "multi.test", "zone.test"} {
		if _, err := Discover(ctx20(t), c.p.Talosconfig(c.a.Endpoint, "", ""), name+":50000"); err == nil || !strings.Contains(err.Error(), "--endpoint") {
			t.Errorf("%s: %v, want a refusal that names --endpoint", name, err)
		}
	}
}

// TestDiscoverUsesTheFirstUsableAddress: an answer that starts with an address that cannot be dialled
// goes on to the next, and an IPv4-mapped IPv6 answer is the IPv4 address.
func TestDiscoverUsesTheFirstUsableAddress(t *testing.T) {
	c := newCluster(t)
	names(t, map[string][]string{"node.test": {"0.0.0.0", "::ffff:127.0.0.1", "10.9.9.9"}})
	got, err := Discover(ctx20(t), c.p.Talosconfig(c.a.Endpoint, "", ""), "node.test:"+portOf(t, c.a.Endpoint))
	if err != nil || got.Endpoint != c.a.Endpoint {
		t.Fatalf("%+v, %v", got, err)
	}
}

// TestResolveEndpointGrammar: the default port, and the names and ports that are refused before any
// lookup.
func TestResolveEndpointGrammar(t *testing.T) {
	asked := names(t, map[string][]string{"node.test": {"10.1.2.3"}, "node.test.": {"10.1.2.3"}, "a_b.test": {"10.1.2.3"}, "localhost": {"127.0.0.1"}})
	ctx := ctx20(t)
	for in, want := range map[string]string{
		"node.test": "10.1.2.3:50000", "node.test:6443": "10.1.2.3:6443", "node.test.": "10.1.2.3:50000",
		"a_b.test": "10.1.2.3:50000", "localhost": "127.0.0.1:50000", "10.5.0.5": "10.5.0.5:50000", "[::1]:6443": "[::1]:6443",
	} {
		got, _, err := resolveEndpoint(ctx, in, "--endpoint")
		if err != nil || got != want {
			t.Errorf("%q: %q, %v; want %q", in, got, err, want)
		}
	}
	before := len(asked())
	for _, in := range []string{
		"", " node.test", "node.test ", "node.test:0", "node.test:65536", "node.test:080", "node.test:+80", "node.test:", "node.test:80:90",
		":80", "https://node.test", "user@node.test", "node.test/path", "-node.test", "node-.test", "a..test", ".test", "node.test..",
		"256.1.1.1", "1.2.3", "node.123", "not an endpoint", strings.Repeat("a", 64) + ".test", strings.Repeat("a.", 130) + "test",
		"node.test:99999999999999999999", "[node.test]:80", "nöde.test",
	} {
		if got, _, err := resolveEndpoint(ctx, in, "--endpoint"); err == nil {
			t.Errorf("%q is accepted as %q", in, got)
		}
	}
	if len(asked()) != before {
		t.Errorf("the resolver was asked %q for an input the grammar refuses", asked()[before:])
	}
}
