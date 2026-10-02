// Package talostest is a stand-in for a Talos node's apid, for tests: a certificate authority that
// signs both the server's certificate and a talosconfig's, and a gRPC server behind mutual TLS that
// answers the machine service's Version and serves COSI state reads from memory, as apid does for
// itself. Tests set the resources a node reports, make a read of one resource type fail with a
// gRPC code, and see the metadata of every request that reached the node.
//
// It lives beside package talos so that tests elsewhere go through the real talos.Dial and Reader,
// not a fake of them. It is test-only and synthetic: every key is generated per call, and nothing
// it holds is a real credential.
package talostest

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
	"sync"
	"testing"
	"time"

	"github.com/cosi-project/runtime/api/v1alpha1"
	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/cosi-project/runtime/pkg/state/impl/inmem"
	"github.com/cosi-project/runtime/pkg/state/impl/namespaced"
	"github.com/cosi-project/runtime/pkg/state/protobuf/server"
	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/resources/cluster"
	cfgres "github.com/siderolabs/talos/pkg/machinery/resources/config"
	"github.com/siderolabs/talos/pkg/machinery/resources/hardware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// The resource types a node serves, for Fail, so that a test outside package talos names them
// without importing the machinery.
const (
	SystemInformationType = hardware.SystemInformationType
	IdentityType          = cluster.IdentityType
	InfoType              = cluster.InfoType
	MachineConfigType     = cfgres.MachineConfigType
)

// PKI is a certificate authority with a server certificate for 127.0.0.1 and a client certificate
// in the os:admin role, as a Talos cluster's CA signs both apid's certificate and a talosconfig's.
type PKI struct {
	CAPEM, CrtPEM, KeyPEM []byte
	Server                tls.Certificate
	Pool                  *x509.CertPool
}

// NewPKI generates a PKI.
func NewPKI(t testing.TB) PKI {
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
	srv, err := tls.X509KeyPair(srvCrt, srvKey)
	if err != nil {
		t.Fatal(err)
	}
	crt, key := leaf(3, x509.ExtKeyUsageClientAuth, nil)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return PKI{CAPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), CrtPEM: crt, KeyPEM: key, Server: srv, Pool: pool}
}

// Talosconfig is a talosconfig document for p whose context names endpoints and nodes (which
// talos.Dial ignores); extra is appended to the context (an auth block, say).
func (p PKI) Talosconfig(endpoints, nodes, extra string) []byte {
	b64 := base64.StdEncoding.EncodeToString
	return []byte("context: a\ncontexts:\n  a:\n" +
		"    endpoints: [" + endpoints + "]\n" +
		"    nodes: [" + nodes + "]\n" +
		"    ca: " + b64(p.CAPEM) + "\n" +
		"    crt: " + b64(p.CrtPEM) + "\n" +
		"    key: " + b64(p.KeyPEM) + "\n" + extra)
}

// ClientCertificate is p's client certificate and key.
func (p PKI) ClientCertificate(t testing.TB) tls.Certificate {
	t.Helper()
	c, err := tls.X509KeyPair(p.CrtPEM, p.KeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Node is a running stand-in.
type Node struct {
	// Endpoint is the node's host:port on 127.0.0.1.
	Endpoint string

	st       state.CoreState
	mu       sync.Mutex
	metadata []metadata.MD
	failures map[resource.Type]codes.Code
}

// Serve runs a node behind mutual TLS with p's CA until the test ends. It reports no resource
// until the test sets one.
func Serve(t testing.TB, p PKI) *Node {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	n := &Node{Endpoint: l.Addr().String(), st: namespaced.NewState(inmem.Build), failures: map[resource.Type]codes.Code{}}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{p.Server}, ClientCAs: p.Pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12,
	})),
		grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
			n.record(ctx)
			return h(ctx, req)
		}),
		grpc.StreamInterceptor(func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, h grpc.StreamHandler) error {
			n.record(ss.Context())
			return h(srv, ss)
		}),
		grpc.UnknownServiceHandler(func(any, grpc.ServerStream) error { return status.Error(codes.Unimplemented, "stand-in") }),
	)
	machine.RegisterMachineServiceServer(srv, versionServer{})
	v1alpha1.RegisterStateServer(srv, server.NewState(failing{n}))
	go srv.Serve(l) //nolint:errcheck // ends with Stop
	t.Cleanup(srv.Stop)
	return n
}

// versionServer answers Version for the node itself, as apid does without routing metadata.
type versionServer struct {
	machine.UnimplementedMachineServiceServer
}

func (versionServer) Version(context.Context, *emptypb.Empty) (*machine.VersionResponse, error) {
	return &machine.VersionResponse{Messages: []*machine.Version{{Version: &machine.VersionInfo{Tag: "v1.13.6"}}}}, nil
}

// failing is the node's state, a Get of a type set to fail answering that code.
type failing struct{ n *Node }

func (f failing) Get(ctx context.Context, p resource.Pointer, opts ...state.GetOption) (resource.Resource, error) {
	f.n.mu.Lock()
	c, ok := f.n.failures[p.Type()]
	f.n.mu.Unlock()
	if ok {
		return nil, status.Error(c, "stand-in")
	}
	return f.n.st.Get(ctx, p, opts...)
}

func (f failing) List(ctx context.Context, k resource.Kind, opts ...state.ListOption) (resource.List, error) {
	return f.n.st.List(ctx, k, opts...)
}

func (failing) Create(context.Context, resource.Resource, ...state.CreateOption) error {
	return status.Error(codes.PermissionDenied, "stand-in: read only")
}

func (failing) Update(context.Context, resource.Resource, ...state.UpdateOption) error {
	return status.Error(codes.PermissionDenied, "stand-in: read only")
}

func (failing) Destroy(context.Context, resource.Pointer, ...state.DestroyOption) error {
	return status.Error(codes.PermissionDenied, "stand-in: read only")
}

func (failing) Watch(context.Context, resource.Pointer, chan<- state.Event, ...state.WatchOption) error {
	return status.Error(codes.Unimplemented, "stand-in")
}

func (failing) WatchKind(context.Context, resource.Kind, chan<- state.Event, ...state.WatchKindOption) error {
	return status.Error(codes.Unimplemented, "stand-in")
}

func (failing) WatchKindAggregated(context.Context, resource.Kind, chan<- []state.Event, ...state.WatchKindOption) error {
	return status.Error(codes.Unimplemented, "stand-in")
}

func (n *Node) record(ctx context.Context) {
	md, _ := metadata.FromIncomingContext(ctx)
	n.mu.Lock()
	n.metadata = append(n.metadata, md)
	n.mu.Unlock()
}

// Seen is the metadata of every request that reached the node, in order.
func (n *Node) Seen() []metadata.MD {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]metadata.MD(nil), n.metadata...)
}

// Fail makes every read of resources of type typ answer code; codes.OK clears it.
func (n *Node) Fail(typ resource.Type, code codes.Code) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if code == codes.OK {
		delete(n.failures, typ)
		return
	}
	n.failures[typ] = code
}

// set replaces the resource at r's pointer with r.
func (n *Node) set(t testing.TB, r resource.Resource) {
	t.Helper()
	ctx := context.Background()
	if err := n.st.Destroy(ctx, r.Metadata()); err != nil && !state.IsNotFoundError(err) {
		t.Fatal(err)
	}
	if err := n.st.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
}

// SetSMBIOSUUID makes the node report uuid in its SystemInformation resource; with present false
// it reports no such resource, as a container node does.
func (n *Node) SetSMBIOSUUID(t testing.TB, uuid string, present bool) {
	t.Helper()
	si := hardware.NewSystemInformation(hardware.SystemInformationID)
	if !present {
		if err := n.st.Destroy(context.Background(), si.Metadata()); err != nil && !state.IsNotFoundError(err) {
			t.Fatal(err)
		}
		return
	}
	si.TypedSpec().UUID = uuid
	n.set(t, si)
}

// SetNodeID makes the node report id as its Talos node ID.
func (n *Node) SetNodeID(t testing.TB, id string) {
	t.Helper()
	r := cluster.NewIdentity(cluster.NamespaceName, cluster.LocalIdentity)
	r.TypedSpec().NodeID = id
	n.set(t, r)
}

// SetClusterID makes the node report id as its Talos cluster ID.
func (n *Node) SetClusterID(t testing.TB, id string) {
	t.Helper()
	r := cluster.NewInfo()
	r.TypedSpec().ClusterID = id
	r.TypedSpec().ClusterName = "stand-in"
	n.set(t, r)
}

// SetMachineConfig makes the node's active machine configuration the document doc.
func (n *Node) SetMachineConfig(t testing.TB, doc []byte) {
	t.Helper()
	p, err := configloader.NewFromBytes(doc)
	if err != nil {
		t.Fatal("talostest: the machine configuration does not load")
	}
	n.set(t, cfgres.NewMachineConfig(p))
}
