package talos

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// pki is a certificate authority with a server certificate for 127.0.0.1 and a client certificate,
// as a Talos cluster's CA signs both apid's certificate and a talosconfig's.
type pki struct {
	caPEM, crtPEM, keyPEM []byte
	server                tls.Certificate
	pool                  *x509.CertPool
}

func newPKI(t *testing.T) pki {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "talos"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leaf := func(serial int64, usage x509.ExtKeyUsage, ips []net.IP) ([]byte, []byte) {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "leaf", Organization: []string{"os:admin"}},
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}, IPAddresses: ips,
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &k.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		kder, err := x509.MarshalECPrivateKey(k)
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})
	}
	srvCrt, srvKey := leaf(2, x509.ExtKeyUsageServerAuth, []net.IP{net.ParseIP("127.0.0.1")})
	server, err := tls.X509KeyPair(srvCrt, srvKey)
	if err != nil {
		t.Fatal(err)
	}
	crt, key := leaf(3, x509.ExtKeyUsageClientAuth, nil)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return pki{caPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), crtPEM: crt, keyPEM: key, server: server, pool: pool}
}

// talosconfig is a talosconfig document for p whose context names endpoints and nodes Dial must
// ignore; extra is appended to the context (an auth block, say).
func (p pki) talosconfig(endpoints, nodes, extra string) []byte {
	b64 := base64.StdEncoding.EncodeToString
	return []byte("context: a\ncontexts:\n  a:\n" +
		"    endpoints: [" + endpoints + "]\n" +
		"    nodes: [" + nodes + "]\n" +
		"    ca: " + b64(p.caPEM) + "\n" +
		"    crt: " + b64(p.crtPEM) + "\n" +
		"    key: " + b64(p.keyPEM) + "\n" + extra)
}

// standIn is apid's machine service answering Version for itself, recording each request's
// metadata.
type standIn struct {
	machine.UnimplementedMachineServiceServer
	mu       sync.Mutex
	metadata []metadata.MD
}

func (s *standIn) Version(ctx context.Context, _ *emptypb.Empty) (*machine.VersionResponse, error) {
	s.record(ctx)
	return &machine.VersionResponse{Messages: []*machine.Version{{Version: &machine.VersionInfo{Tag: "v1.13.6"}}}}, nil
}

// unknown records a request for any other service (the COSI state MachineConfig reads) and
// answers Unimplemented.
func (s *standIn) unknown(_ any, stream grpc.ServerStream) error {
	s.record(stream.Context())
	return status.Error(codes.Unimplemented, "stand-in")
}

func (s *standIn) record(ctx context.Context) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.mu.Lock()
	s.metadata = append(s.metadata, md)
	s.mu.Unlock()
}

func (s *standIn) seen() []metadata.MD {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]metadata.MD(nil), s.metadata...)
}

// serve runs the stand-in on 127.0.0.1 behind mutual TLS with p's CA, returning its endpoint.
func serve(t *testing.T, p pki) (string, *standIn) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &standIn{}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{p.server}, ClientCAs: p.pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12,
	})), grpc.UnknownServiceHandler(s.unknown))
	machine.RegisterMachineServiceServer(srv, s)
	go srv.Serve(l) //nolint:errcheck // ends with Stop
	t.Cleanup(srv.Stop)
	return l.Addr().String(), s
}

func hasRouting(md metadata.MD) bool {
	return len(md.Get("node")) > 0 || len(md.Get("nodes")) > 0
}

// TestDialUsesOnlyTheChosenEndpoint: the talosconfig's context names an endpoint and a node that
// do not answer (TEST-NET-1, RFC 5737) and an auth block that would read keys from disk; Dial still
// reaches the endpoint it was given, authenticated by the CA, certificate and key alone, and its
// requests carry no node routing metadata.
func TestDialUsesOnlyTheChosenEndpoint(t *testing.T) {
	p := newPKI(t)
	ep, s := serve(t, p)
	tc := p.talosconfig("192.0.2.1", "192.0.2.2", "    auth:\n      siderov1:\n        identity: someone@example.org\n")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	r, err := Dial(ctx, tc, ep)
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
	seen := s.seen()
	if len(seen) != 1 {
		t.Fatalf("%d requests reached the stand-in", len(seen))
	}
	if hasRouting(seen[0]) {
		t.Fatalf("the request carries routing metadata: %v", seen[0])
	}

	// Control: a request with node metadata on the same server is seen as such, so the check above
	// can fail.
	conn, err := grpc.NewClient(ep, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		RootCAs: p.pool, Certificates: []tls.Certificate{clientCert(t, p)}, MinVersion: tls.VersionTLS12,
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := machine.NewMachineServiceClient(conn).Version(metadata.AppendToOutgoingContext(ctx, "node", "192.0.2.2"), &emptypb.Empty{}); err != nil {
		t.Fatal(err)
	}
	if seen := s.seen(); len(seen) != 2 || !hasRouting(seen[1]) {
		t.Fatalf("control: node metadata not seen: %v", seen)
	}
}

// TestRequestsDropRouting (PA §3.3): a caller's context carrying node or nodes metadata does not
// make a request routable; both of Reader's requests reach the endpoint without it.
func TestRequestsDropRouting(t *testing.T) {
	p := newPKI(t)
	ep, s := serve(t, p)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	r, err := Dial(ctx, p.talosconfig("", "", ""), ep)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	routed := metadata.AppendToOutgoingContext(ctx, "node", "192.0.2.2", "nodes", "192.0.2.3", "x-kept", "1")
	if _, err := r.Version(routed); err != nil {
		t.Fatal(err)
	}
	if _, err := r.MachineConfig(routed); status.Code(err) != codes.Unimplemented {
		t.Fatalf("MachineConfig: %v; want the stand-in's Unimplemented", err)
	}
	seen := s.seen()
	if len(seen) != 2 {
		t.Fatalf("%d requests reached the stand-in; want 2", len(seen))
	}
	for i, md := range seen {
		if hasRouting(md) || len(md.Get("x-kept")) != 1 {
			t.Errorf("request %d: metadata %v; want no node or nodes, other keys kept", i, md)
		}
	}
}

func clientCert(t *testing.T, p pki) tls.Certificate {
	t.Helper()
	c, err := tls.X509KeyPair(p.crtPEM, p.keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestDialWritesNoFile: a dial and a request leave the temporary and home directories empty; the
// credential is never put on disk, and no client reads it from a path.
func TestDialWritesNoFile(t *testing.T) {
	p := newPKI(t)
	ep, _ := serve(t, p)
	tmp, home := t.TempDir(), t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("HOME", home)
	t.Setenv("TALOSCONFIG", "")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	r, err := Dial(ctx, p.talosconfig("192.0.2.1", "192.0.2.2", ""), ep)
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
	ep, s := serve(t, newPKI(t))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	r, err := Dial(ctx, newPKI(t).talosconfig("", "", ""), ep)
	if err == nil {
		defer r.Close()
		_, err = r.Version(ctx)
	}
	if err == nil {
		t.Fatal("another authority's talosconfig was accepted")
	}
	if n := len(s.seen()); n != 0 {
		t.Fatalf("%d requests reached the node", n)
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
	p := newPKI(t)
	good := p.talosconfig("", "", "")
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
		"mismatched key":       {[]byte(strings.Replace(string(good), base64.StdEncoding.EncodeToString(p.keyPEM), base64.StdEncoding.EncodeToString(newPKI(t).keyPEM), 1)), "10.55.0.3:50000"},
		"undecodable ca":       {[]byte(strings.Replace(string(good), "ca: ", "ca: "+mark+"-", 1)), "10.55.0.3:50000"},
		"ca without a cert":    {[]byte(strings.Replace(string(good), base64.StdEncoding.EncodeToString(p.caPEM), base64.StdEncoding.EncodeToString([]byte(mark)), 1)), "10.55.0.3:50000"},
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
