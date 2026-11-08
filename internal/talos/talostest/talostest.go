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
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/cosi-project/runtime/api/v1alpha1"
	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/resource/protobuf"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/cosi-project/runtime/pkg/state/impl/inmem"
	"github.com/cosi-project/runtime/pkg/state/impl/namespaced"
	"github.com/cosi-project/runtime/pkg/state/protobuf/server"
	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	configpb "github.com/siderolabs/talos/pkg/machinery/api/resource/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	machinecfg "github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/talos/pkg/machinery/resources/cluster"
	cfgres "github.com/siderolabs/talos/pkg/machinery/resources/config"
	"github.com/siderolabs/talos/pkg/machinery/resources/hardware"
	"github.com/siderolabs/talos/pkg/machinery/resources/runtime"
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
	MemberType            = cluster.MemberType
	PlatformMetadataType  = runtime.PlatformMetadataType
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
	stalls   map[resource.Type]bool
	applied  []Applied
	onApply  func(context.Context, *machine.ApplyConfigurationRequest) (*machine.ApplyConfigurationResponse, error)
}

// Serve runs a node behind mutual TLS with p's CA until the test ends. It reports no resource
// until the test sets one.
func Serve(t testing.TB, p PKI) *Node {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	n := &Node{Endpoint: l.Addr().String(), st: namespaced.NewState(inmem.Build), failures: map[resource.Type]codes.Code{}, stalls: map[resource.Type]bool{}}
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
	machine.RegisterMachineServiceServer(srv, machineServer{n: n})
	v1alpha1.RegisterStateServer(srv, server.NewState(failing{n}))
	go srv.Serve(l) //nolint:errcheck // ends with Stop
	t.Cleanup(srv.Stop)
	return n
}

// machineServer answers Version for the node itself, as apid does without routing metadata, and
// ApplyConfiguration as the test sets (OnApply).
type machineServer struct {
	machine.UnimplementedMachineServiceServer
	n *Node
}

func (machineServer) Version(context.Context, *emptypb.Empty) (*machine.VersionResponse, error) {
	return &machine.VersionResponse{Messages: []*machine.Version{{Version: &machine.VersionInfo{Tag: "v1.14.5"}}}}, nil
}

func (s machineServer) ApplyConfiguration(ctx context.Context, req *machine.ApplyConfigurationRequest) (*machine.ApplyConfigurationResponse, error) {
	s.n.mu.Lock()
	s.n.applied = append(s.n.applied, Applied{Data: append([]byte(nil), req.GetData()...), Mode: req.GetMode().String(),
		DryRun: req.GetDryRun(), TryModeTimeout: req.GetTryModeTimeout() != nil})
	h := s.n.onApply
	s.n.mu.Unlock()
	if h == nil {
		return nil, status.Error(codes.Unimplemented, "stand-in: no apply behaviour set")
	}
	return h(ctx, req)
}

// Applied is one ApplyConfiguration request that reached the node.
type Applied struct {
	Data           []byte
	Mode           string // the machinery's name of the mode, "NO_REBOOT" for a no-reboot apply
	DryRun         bool
	TryModeTimeout bool // the request carried a try-mode timeout
}

// OnApply sets how the node answers ApplyConfiguration; until it is set the node answers
// Unimplemented. The handler's context ends with the request.
func (n *Node) OnApply(h func(context.Context, *machine.ApplyConfigurationRequest) (*machine.ApplyConfigurationResponse, error)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.onApply = h
}

// AcceptApply makes the node accept every apply as the mode it asked for and report the
// configuration it carried as its own from then on, as a node that applied it does.
func (n *Node) AcceptApply(t testing.TB) {
	n.OnApply(func(_ context.Context, req *machine.ApplyConfigurationRequest) (*machine.ApplyConfigurationResponse, error) {
		n.SetMachineConfig(t, req.GetData())
		return &machine.ApplyConfigurationResponse{Messages: []*machine.ApplyConfiguration{{Mode: req.GetMode()}}}, nil
	})
}

// HoldApply makes the node hold each apply until release runs, then accept it as AcceptApply
// does. arrived is closed when the first apply reaches the node. A held apply whose request ends
// first answers the request's error.
func (n *Node) HoldApply(t testing.TB) (arrived <-chan struct{}, release func()) {
	in, out := make(chan struct{}), make(chan struct{})
	first := sync.OnceFunc(func() { close(in) })
	n.OnApply(func(ctx context.Context, req *machine.ApplyConfigurationRequest) (*machine.ApplyConfigurationResponse, error) {
		first()
		select {
		case <-out:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		n.SetMachineConfig(t, req.GetData())
		return &machine.ApplyConfigurationResponse{Messages: []*machine.ApplyConfiguration{{Mode: req.GetMode()}}}, nil
	})
	return in, sync.OnceFunc(func() { close(out) })
}

// AppliedRequests are the ApplyConfiguration requests that reached the node, in order.
func (n *Node) AppliedRequests() []Applied {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Applied(nil), n.applied...)
}

// failing is the node's state, a Get or List of a type set to fail answering that code.
type failing struct{ n *Node }

func (f failing) failure(typ resource.Type) error {
	f.n.mu.Lock()
	defer f.n.mu.Unlock()
	if c, ok := f.n.failures[typ]; ok {
		return status.Error(c, "stand-in")
	}
	return nil
}

// stall waits for the request to end when reads of typ are stalled, and answers its error.
func (f failing) stall(ctx context.Context, typ resource.Type) error {
	f.n.mu.Lock()
	stalled := f.n.stalls[typ]
	f.n.mu.Unlock()
	if !stalled {
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
}

func (f failing) Get(ctx context.Context, p resource.Pointer, opts ...state.GetOption) (resource.Resource, error) {
	if err := f.stall(ctx, p.Type()); err != nil {
		return nil, err
	}
	if err := f.failure(p.Type()); err != nil {
		return nil, err
	}
	return f.n.st.Get(ctx, p, opts...)
}

func (f failing) List(ctx context.Context, k resource.Kind, opts ...state.ListOption) (resource.List, error) {
	if err := f.stall(ctx, k.Type()); err != nil {
		return resource.List{}, err
	}
	if err := f.failure(k.Type()); err != nil {
		return resource.List{}, err
	}
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

// Stall makes every read of resources of type typ wait until the request ends, then answer the
// request's error; on false clears it. The TLS handshake and the other types are not affected.
func (n *Node) Stall(typ resource.Type, on bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if on {
		n.stalls[typ] = true
		return
	}
	delete(n.stalls, typ)
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

// Member is one cluster member as a node lists it. ControlPlanePort is the Kubernetes API server
// port of a control-plane member; a worker has 0.
type Member struct {
	NodeID, Hostname string
	Addresses        []string
	ControlPlanePort int
}

// SetMembers makes the node list exactly these cluster members.
func (n *Node) SetMembers(t testing.TB, ms ...Member) {
	t.Helper()
	ctx := context.Background()
	old, err := n.st.List(ctx, resource.NewMetadata(cluster.NamespaceName, cluster.MemberType, "", resource.VersionUndefined))
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range old.Items {
		if err := n.st.Destroy(ctx, o.Metadata()); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range ms {
		r := cluster.NewMember(cluster.NamespaceName, m.NodeID)
		s := r.TypedSpec()
		s.NodeID, s.Hostname, s.MachineType = m.NodeID, m.Hostname, machinecfg.TypeWorker
		for _, a := range m.Addresses {
			s.Addresses = append(s.Addresses, netip.MustParseAddr(a))
		}
		if m.ControlPlanePort != 0 {
			s.MachineType, s.ControlPlane = machinecfg.TypeControlPlane, &cluster.ControlPlane{APIServerPort: m.ControlPlanePort}
		}
		n.set(t, r)
	}
}

// SetPlatform makes the node report name as its Talos platform ("metal", "container", "aws" ...).
func (n *Node) SetPlatform(t testing.TB, name string) {
	t.Helper()
	r := runtime.NewPlatformMetadataSpec(runtime.NamespaceName, runtime.PlatformMetadataID)
	r.TypedSpec().Platform = name
	n.set(t, r)
}

// SetUndecodableMachineConfig makes the node's active machine configuration a resource whose
// spec carries yaml as it is, for a document the client's machinery cannot decode (a kind it does
// not register, for one). SetMachineConfig cannot do this: it loads the document and fails the
// test when that load fails.
func (n *Node) SetUndecodableMachineConfig(t testing.TB, yaml []byte) {
	t.Helper()
	n.set(t, &undecodableConfig{
		md:   resource.NewMetadata(cfgres.NamespaceName, cfgres.MachineConfigType, cfgres.ActiveID, resource.VersionUndefined),
		yaml: yaml,
	})
}

// undecodableConfig is a MachineConfig resource that marshals any bytes as the configuration.
type undecodableConfig struct {
	md   resource.Metadata
	yaml []byte
}

func (u *undecodableConfig) Metadata() *resource.Metadata { return &u.md }
func (u *undecodableConfig) Spec() any                    { return rawConfigSpec(u.yaml) }
func (u *undecodableConfig) DeepCopy() resource.Resource {
	return &undecodableConfig{md: u.md, yaml: append([]byte(nil), u.yaml...)}
}

type rawConfigSpec []byte

// MarshalProto implements protobuf.ProtoMarshaler, as the machinery's own spec does.
func (s rawConfigSpec) MarshalProto() ([]byte, error) {
	return protobuf.ProtoMarshal(&configpb.MachineConfigSpec{YamlMarshalled: s})
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
