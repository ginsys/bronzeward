package talos

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/talos/talostest"
	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func hasRouting(md metadata.MD) bool {
	return len(md.Get("node")) > 0 || len(md.Get("nodes")) > 0
}

// TestDialUsesOnlyTheChosenEndpoint: the talosconfig's context names an endpoint and a node that
// do not answer (TEST-NET-1, RFC 5737) and an auth block that would read keys from disk; Dial still
// reaches the endpoint it was given, authenticated by the CA, certificate and key alone, and its
// requests carry no node routing metadata.
func TestDialUsesOnlyTheChosenEndpoint(t *testing.T) {
	p := talostest.NewPKI(t)
	n := talostest.Serve(t, p)
	tc := p.Talosconfig("192.0.2.1", "192.0.2.2", "    auth:\n      siderov1:\n        identity: someone@example.org\n")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	r, err := Dial(ctx, tc, n.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	v, err := r.Version(ctx)
	if err != nil {
		t.Fatalf("Version through the chosen endpoint: %v", err)
	}
	if v != "v1.13.6" {
		t.Fatalf("Version %q", v)
	}
	seen := n.Seen()
	if len(seen) != 1 {
		t.Fatalf("%d requests reached the stand-in", len(seen))
	}
	if hasRouting(seen[0]) {
		t.Fatalf("the request carries routing metadata: %v", seen[0])
	}

	// Control: a request with node metadata on the same server is seen as such, so the check above
	// can fail.
	conn, err := grpc.NewClient(n.Endpoint, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		RootCAs: p.Pool, Certificates: []tls.Certificate{p.ClientCertificate(t)}, MinVersion: tls.VersionTLS12,
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := machine.NewMachineServiceClient(conn).Version(metadata.AppendToOutgoingContext(ctx, "node", "192.0.2.2"), &emptypb.Empty{}); err != nil {
		t.Fatal(err)
	}
	if seen := n.Seen(); len(seen) != 2 || !hasRouting(seen[1]) {
		t.Fatalf("control: node metadata not seen: %v", seen)
	}
}

// TestRequestsDropRouting (PA §3.3): a caller's context carrying node or nodes metadata does not
// make a request routable; both of Reader's requests reach the endpoint without it.
func TestRequestsDropRouting(t *testing.T) {
	p := talostest.NewPKI(t)
	n := talostest.Serve(t, p)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	r, err := Dial(ctx, p.Talosconfig("", "", ""), n.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	routed := metadata.AppendToOutgoingContext(ctx, "node", "192.0.2.2", "nodes", "192.0.2.3", "x-kept", "1")
	if _, err := r.Version(routed); err != nil {
		t.Fatal(err)
	}
	if _, err := r.MachineConfig(routed); status.Code(err) != codes.NotFound {
		t.Fatalf("MachineConfig: %v; want NotFound: the stand-in holds no configuration", err)
	}
	seen := n.Seen()
	if len(seen) != 2 {
		t.Fatalf("%d requests reached the stand-in; want 2", len(seen))
	}
	for i, md := range seen {
		if hasRouting(md) || len(md.Get("x-kept")) != 1 {
			t.Errorf("request %d: metadata %v; want no node or nodes, other keys kept", i, md)
		}
	}
}

// TestDialWritesNoFile: a dial and a request leave the temporary and home directories empty; the
// credential is never put on disk, and no client reads it from a path.
func TestDialWritesNoFile(t *testing.T) {
	p := talostest.NewPKI(t)
	n := talostest.Serve(t, p)
	tmp, home := t.TempDir(), t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("HOME", home)
	t.Setenv("TALOSCONFIG", "")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	r, err := Dial(ctx, p.Talosconfig("192.0.2.1", "192.0.2.2", ""), n.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Version(ctx); err != nil {
		t.Fatal(err)
	}
	r.Close()
	for _, d := range []string{tmp, home} {
		entries, err := os.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("%s holds %d entries after a dial", d, len(entries))
		}
	}
}

// TestDialRefusesAnotherAuthority: a talosconfig signed by another CA fails the handshake.
func TestDialRefusesAnotherAuthority(t *testing.T) {
	n := talostest.Serve(t, talostest.NewPKI(t))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	r, err := Dial(ctx, talostest.NewPKI(t).Talosconfig("", "", ""), n.Endpoint)
	if err == nil {
		defer r.Close()
		_, err = r.Version(ctx)
	}
	if err == nil {
		t.Fatal("another authority's talosconfig was accepted")
	}
	if got := len(n.Seen()); got != 0 {
		t.Fatalf("%d requests reached the node", got)
	}
}

// cutLine is tc without its lines whose trimmed text starts with one of prefixes.
func cutLine(tc []byte, prefixes ...string) string {
	var out []string
	for _, l := range strings.Split(string(tc), "\n") {
		if !slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(strings.TrimSpace(l), p) }) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func TestDialRefuses(t *testing.T) {
	p := talostest.NewPKI(t)
	good := p.Talosconfig("", "", "")
	const mark = "UPSTRM"
	for name, c := range map[string]struct {
		tc       []byte
		endpoint string
	}{
		"no talosconfig":       {nil, "10.55.0.3:50000"},
		"no endpoint":          {good, ""},
		"dns endpoint":         {good, "node.example.org:50000"},
		"unparseable":          {[]byte("context: [" + mark), "10.55.0.3:50000"},
		"no current context":   {[]byte("context: b\ncontexts:\n  a:\n    endpoints: [" + mark + "]\n"), "10.55.0.3:50000"},
		"no certificate":       {[]byte(cutLine(good, "crt: ", "key: ")), "10.55.0.3:50000"},
		"no ca":                {[]byte(cutLine(good, "ca: ")), "10.55.0.3:50000"},
		"undecodable key":      {[]byte(strings.Replace(string(good), "key: ", "key: "+mark+"-", 1)), "10.55.0.3:50000"},
		"mismatched key":       {[]byte(strings.Replace(string(good), base64.StdEncoding.EncodeToString(p.KeyPEM), base64.StdEncoding.EncodeToString(talostest.NewPKI(t).KeyPEM), 1)), "10.55.0.3:50000"},
		"undecodable ca":       {[]byte(strings.Replace(string(good), "ca: ", "ca: "+mark+"-", 1)), "10.55.0.3:50000"},
		"ca without a cert":    {[]byte(strings.Replace(string(good), base64.StdEncoding.EncodeToString(p.CAPEM), base64.StdEncoding.EncodeToString([]byte(mark)), 1)), "10.55.0.3:50000"},
		"secret in the config": {[]byte("context: a\ncontexts:\n  a:\n    crt: " + mark + "\n    key: " + mark + "\n    ca: x\n"), "10.55.0.3:50000"},
		// The machinery's parser dereferences a null context while upgrading the document.
		"null current context": {[]byte("context: a\ncontexts:\n  a: null\n"), "10.55.0.3:50000"},
		"null other context":   {[]byte(string(good) + "  b: null\n"), "10.55.0.3:50000"},
	} {
		r, err := Dial(t.Context(), c.tc, c.endpoint)
		if err == nil {
			r.Close()
			t.Errorf("%s: dialled", name)
			continue
		}
		if strings.Contains(err.Error(), mark) {
			t.Errorf("%s: the error quotes the talosconfig: %v", name, err)
		}
	}
	// Control: the good talosconfig dials (it makes no request).
	r, err := Dial(t.Context(), good, "10.55.0.3:50000")
	if err != nil {
		t.Fatalf("control: %v", err)
	}
	r.Close()
}
