package discover

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/netip"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/talos/talostest"
	"google.golang.org/grpc/codes"
)

// stand is a stand-in cluster of two answering members and the addresses their Members entries
// give. memberEndpoint points each address at its stand-in, which listens on 127.0.0.1.
type stand struct {
	p        talostest.PKI
	a, b     *talostest.Node
	closed   string // an endpoint with no listener
	endpoint func(netip.Addr) string
}

func newCluster(t *testing.T) *stand {
	t.Helper()
	p := talostest.NewPKI(t)
	c := &stand{p: p, a: talostest.Serve(t, p), b: talostest.Serve(t, p)}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c.closed = l.Addr().String()
	l.Close()
	c.a.SetClusterID(t, "c-1")
	c.a.SetNodeID(t, "n1")
	c.a.SetSMBIOSUUID(t, "11111111-2222-3333-4444-555555555555", true)
	c.a.SetPlatform(t, "metal")
	c.b.SetNodeID(t, "n2")
	c.b.SetSMBIOSUUID(t, "", false)
	c.b.SetPlatform(t, "container")
	c.a.SetMembers(t,
		talostest.Member{NodeID: "n1", Hostname: "cp-1", Addresses: []string{"10.0.0.1"}, ControlPlanePort: 6443},
		talostest.Member{NodeID: "n2", Hostname: "w-1", Addresses: []string{"10.0.0.2"}},
	)
	by := map[string]string{"10.0.0.1": c.a.Endpoint, "10.0.0.2": c.b.Endpoint, "10.0.0.3": c.closed}
	old := memberEndpoint
	memberEndpoint = func(a netip.Addr) string { return by[a.String()] }
	t.Cleanup(func() { memberEndpoint = old })
	return c
}

func (c *stand) discover(t *testing.T) (Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	return Discover(ctx, c.p.Talosconfig(c.a.Endpoint, "", ""), c.a.Endpoint)
}

func TestDiscoverTwoMembers(t *testing.T) {
	c := newCluster(t)
	got, err := c.discover(t)
	if err != nil {
		t.Fatal(err)
	}
	want := Result{
		ClusterID: "c-1", Version: "v1.13.6", Contract: "v1.13", Supported: true,
		KubernetesEndpoint: "https://10.0.0.1:6443",
		Members: []Member{
			{NodeID: "n1", Hostname: "cp-1", Address: "10.0.0.1", Role: "controlplane", SMBIOSUUID: "11111111-2222-3333-4444-555555555555", Platform: "metal", Answered: true},
			{NodeID: "n2", Hostname: "w-1", Address: "10.0.0.2", Role: "worker", Platform: "container", Answered: true},
		},
	}
	if !equalResult(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func equalResult(a, b Result) bool {
	if len(a.Members) != len(b.Members) {
		return false
	}
	for i := range a.Members {
		if a.Members[i] != b.Members[i] {
			return false
		}
	}
	a.Members, b.Members = nil, nil
	return a.ClusterID == b.ClusterID && a.Version == b.Version && a.Contract == b.Contract &&
		a.Supported == b.Supported && a.KubernetesEndpoint == b.KubernetesEndpoint
}

// TestDiscoverEndpointFromTheFile: with no endpoint given, the talosconfig's first endpoint is
// dialled; a file that names none is ErrNoEndpoint, before any dial.
func TestDiscoverEndpointFromTheFile(t *testing.T) {
	c := newCluster(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	got, err := Discover(ctx, c.p.Talosconfig(c.a.Endpoint+", "+c.closed, "", ""), "")
	if err != nil || got.ClusterID != "c-1" {
		t.Fatalf("the file's first endpoint: %+v, %v", got, err)
	}
	seen := len(c.a.Seen())
	if _, err := Discover(ctx, c.p.Talosconfig("", "", ""), ""); !errors.Is(err, ErrNoEndpoint) {
		t.Fatalf("a file with no endpoint: %v, want ErrNoEndpoint", err)
	}
	if len(c.a.Seen()) != seen {
		t.Fatal("a request reached the node although the file names no endpoint")
	}
}

func TestDiscoverMemberDoesNotAnswer(t *testing.T) {
	c := newCluster(t)
	c.a.SetMembers(t,
		talostest.Member{NodeID: "n1", Hostname: "cp-1", Addresses: []string{"10.0.0.1"}, ControlPlanePort: 6443},
		talostest.Member{NodeID: "n3", Hostname: "w-2", Addresses: []string{"10.0.0.3"}},
	)
	got, err := c.discover(t)
	if err != nil {
		t.Fatal(err)
	}
	m := got.Members[1]
	if m.NodeID != "n3" || m.Answered || m.SMBIOSUUID != "" || m.Platform != "" {
		t.Fatalf("the member that does not answer: %+v", m)
	}
	if !got.Members[0].Answered {
		t.Fatalf("the answering member is not marked answered: %+v", got.Members[0])
	}
}

func TestDiscoverUnsupportedContract(t *testing.T) {
	c := newCluster(t)
	old := supportedMinor
	supportedMinor = func() string { return "v1.14" }
	t.Cleanup(func() { supportedMinor = old })
	got, err := c.discover(t)
	if err != nil {
		t.Fatal(err)
	}
	if got.Contract != "v1.13" || got.Supported {
		t.Fatalf("contract %q supported %v against a server on v1.14", got.Contract, got.Supported)
	}
}

func TestPlatformMapping(t *testing.T) {
	for in, want := range map[string]string{
		"metal": "metal", "container": "container", "aws": "cloud", "gcp": "cloud", "hcloud": "cloud",
		"openstack": "cloud", "": "",
	} {
		if got := platform(in); got != want {
			t.Errorf("platform(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestContractOf(t *testing.T) {
	for in, want := range map[string]string{"v1.14.5": "v1.14", "v1.13.0-alpha.2": "v1.13", "v2.0.1": "v2.0", "": "", "1.14.5": "", "v1": ""} {
		if got := contractOf(in); got != want {
			t.Errorf("contractOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDiscoverReadsOnly: the stand-in refuses writes and records no apply, and no request carries
// node routing metadata.
func TestDiscoverReadsOnly(t *testing.T) {
	c := newCluster(t)
	if _, err := c.discover(t); err != nil {
		t.Fatal(err)
	}
	for _, n := range []*talostest.Node{c.a, c.b} {
		if got := n.AppliedRequests(); len(got) != 0 {
			t.Errorf("%d apply requests reached a node", len(got))
		}
		for _, md := range n.Seen() {
			if len(md.Get("node")) > 0 || len(md.Get("nodes")) > 0 {
				t.Errorf("a request carries routing metadata: %v", md)
			}
		}
	}
	if len(c.a.Seen()) == 0 || len(c.b.Seen()) == 0 {
		t.Fatalf("control: requests reached a %d, b %d; both must be read", len(c.a.Seen()), len(c.b.Seen()))
	}
}

// TestDiscoverErrorsDoNotQuoteUpstream: a failed read, a bad endpoint and a bad talosconfig leave
// the talosconfig's key and the node's own error text out of the error.
func TestDiscoverErrorsDoNotQuoteUpstream(t *testing.T) {
	c := newCluster(t)
	c.a.Fail(talostest.MemberType, codes.PermissionDenied)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	tc := c.p.Talosconfig(c.a.Endpoint, "", "")
	for name, call := range map[string]func() error{
		"members refused": func() error { _, err := Discover(ctx, tc, c.a.Endpoint); return err },
		"bad endpoint":    func() error { _, err := Discover(ctx, tc, "not an endpoint"); return err },
		"bad talosconfig": func() error {
			_, err := Discover(ctx, bytes.ReplaceAll(tc, []byte("key:"), []byte("kex:")), c.a.Endpoint)
			return err
		},
	} {
		err := call()
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		for _, secret := range leaked(err.Error(), c.p) {
			t.Errorf("%s: the error quotes %.20q: %v", name, secret, err)
		}
	}
	if !strings.Contains(callErr(ctx, tc, c).Error(), "PermissionDenied") {
		t.Errorf("the refused read is not named by its code: %v", callErr(ctx, tc, c))
	}
}

func callErr(ctx context.Context, tc []byte, c *stand) error {
	_, err := Discover(ctx, tc, c.a.Endpoint)
	return err
}

// TestDiscoverBoundsAMemberNotEachAddress: a member that lists several addresses that never answer
// holds the command for one member timeout, not one per address.
func TestDiscoverBoundsAMemberNotEachAddress(t *testing.T) {
	c := newCluster(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() { // accepts and says nothing: the TLS handshake waits for the deadline
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { conn.Close() })
		}
	}()
	old, oldTimeout := memberEndpoint, memberTimeout
	memberEndpoint = func(a netip.Addr) string {
		if a.String() == "10.0.0.1" {
			return c.a.Endpoint
		}
		return l.Addr().String()
	}
	memberTimeout = 400 * time.Millisecond
	t.Cleanup(func() { memberEndpoint, memberTimeout = old, oldTimeout })
	c.a.SetMembers(t,
		talostest.Member{NodeID: "n1", Hostname: "cp-1", Addresses: []string{"10.0.0.1"}, ControlPlanePort: 6443},
		talostest.Member{NodeID: "n3", Hostname: "w-2", Addresses: []string{"10.0.0.3", "10.0.0.4", "10.0.0.5"}},
	)
	start := time.Now()
	got, err := c.discover(t)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 2*memberTimeout {
		t.Errorf("discovery took %v for members with a %v timeout and one member with three silent addresses", took, memberTimeout)
	}
	if m := got.Members[1]; m.Answered || m.Address != "10.0.0.3" {
		t.Errorf("the silent member: %+v", m)
	}
}

// leaked is the secrets and upstream words that msg quotes. The key is looked for as the PEM and
// as the base64 text that a talosconfig holds, because a talosconfig quoted whole carries only the
// second.
func leaked(msg string, p talostest.PKI) []string {
	var out []string
	for _, secret := range []string{string(p.KeyPEM), base64.StdEncoding.EncodeToString(p.KeyPEM), "stand-in", "not an endpoint"} {
		if strings.Contains(msg, secret) {
			out = append(out, secret)
		}
	}
	return out
}

// TestLeakCheckSeesTheEncodedKey is the control for TestDiscoverErrorsDoNotQuoteUpstream: an error
// that quotes the whole talosconfig, or only its encoded key, must be reported.
func TestLeakCheckSeesTheEncodedKey(t *testing.T) {
	p := talostest.NewPKI(t)
	whole := "not a talosconfig: " + string(p.Talosconfig("10.0.0.1:50000", "", ""))
	if len(leaked(whole, p)) == 0 {
		t.Error("an error that quotes the whole talosconfig is not reported")
	}
	if len(leaked("key "+base64.StdEncoding.EncodeToString(p.KeyPEM), p)) == 0 {
		t.Error("an error that quotes the encoded key is not reported")
	}
	if got := leaked("discover: version: Unavailable", p); len(got) != 0 {
		t.Errorf("a clean error is reported: %q", got)
	}
}

// TestMachineryContract: the supported contract is the linked machinery module's minor. A module
// replacement hides the linked version in Replace, so nothing is supported then (the server's
// renderer refuses a replacement the same way).
func TestMachineryContract(t *testing.T) {
	const path = "github.com/siderolabs/talos/pkg/machinery"
	for name, tc := range map[string]struct {
		deps []*debug.Module
		want string
	}{
		"plain":       {[]*debug.Module{{Path: path, Version: "v1.13.6"}}, "v1.13"},
		"replaced":    {[]*debug.Module{{Path: path, Version: "v1.13.6", Replace: &debug.Module{Path: "../machinery", Version: "v1.14.2"}}}, ""},
		"absent":      {[]*debug.Module{{Path: "example.org/other", Version: "v1.13.6"}}, ""},
		"no build id": {nil, ""},
	} {
		if got := machineryContract(&debug.BuildInfo{Deps: tc.deps}); got != tc.want {
			t.Errorf("%s: machineryContract = %q, want %q", name, got, tc.want)
		}
	}
	if got := machineryContract(nil); got != "" {
		t.Errorf("no build information: %q", got)
	}
}
