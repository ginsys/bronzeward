package talos

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/talos/talostest"
	"github.com/siderolabs/talos/pkg/machinery/resources/cluster"
	"github.com/siderolabs/talos/pkg/machinery/resources/hardware"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	standInUUID = "3E8D9F4B-5C6A-4B8C-9D2E-3F4A5B6C7D8E"
	standInNode = "7x1SuC8Ege5BGXdAfTEff5iQnlWZLfv9h1LGMxA2pYkC"
	// standInCluster is a cluster ID as Talos v1.14 prints it, in the standard alphabet.
	// standInClusterURLSafe is the same 32 bytes as Talos before v1.14 prints them.
	standInCluster        = "8TMwqXnWOTdw7xFDHSn+f6JMbBQrSWAuyzCfGIRVSL0="
	standInClusterURLSafe = "8TMwqXnWOTdw7xFDHSn-f6JMbBQrSWAuyzCfGIRVSL0="
)

func dialStandIn(t *testing.T) (*talostest.Node, Reader, context.Context) {
	t.Helper()
	p := talostest.NewPKI(t)
	n := talostest.Serve(t, p)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	r, err := Dial(ctx, p.Talosconfig("", "", ""), n.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return n, r, ctx
}

// PA §3.3: Identity answers the node's SMBIOS UUID as Talos prints it, its node ID and its cluster
// ID. A node with no SystemInformation resource (a container node), or one whose UUID is empty,
// reports no UUID.
func TestIdentity(t *testing.T) {
	for name, c := range map[string]struct {
		uuid    string
		present bool
		want    string
	}{
		"UUID reported":       {standInUUID, true, standInUUID},
		"no system resource":  {"", false, ""},
		"empty UUID reported": {"", true, ""},
	} {
		t.Run(name, func(t *testing.T) {
			n, r, ctx := dialStandIn(t)
			n.SetSMBIOSUUID(t, c.uuid, c.present)
			n.SetNodeID(t, standInNode)
			n.SetClusterID(t, standInCluster)
			got, err := r.Identity(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got != (Identity{SMBIOSUUID: c.want, NodeID: standInNode, ClusterID: standInCluster}) {
				t.Fatalf("Identity %+v", got)
			}
		})
	}
}

// PA §7.3: Identity gives the cluster ID in the standard spelling, whichever spelling the node
// prints, so a node of Talos before v1.14 and one of v1.14 or later compare equal to one record. A
// value that is no cluster ID in either spelling is passed on as read, for the caller's own form
// check to refuse, and never repaired here.
func TestIdentityClusterIDSpelling(t *testing.T) {
	for name, c := range map[string]struct{ reported, want string }{
		"standard, as Talos v1.14 prints it":    {standInCluster, standInCluster},
		"URL-safe, as Talos before v1.14 does":  {standInClusterURLSafe, standInCluster},
		"neither spelling is passed on as read": {"c-1", "c-1"},
	} {
		t.Run(name, func(t *testing.T) {
			n, r, ctx := dialStandIn(t)
			n.SetNodeID(t, standInNode)
			n.SetClusterID(t, c.reported)
			got, err := r.Identity(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got.ClusterID != c.want {
				t.Fatalf("cluster ID %q; want %q", got.ClusterID, c.want)
			}
		})
	}
}

// A failing read of any of the three resources fails Identity, keeping the gRPC code and nothing of
// the node's text; a missing node identity or cluster information is a failure too, not an empty
// value, since every Talos node reports both.
func TestIdentityReadFailures(t *testing.T) {
	for name, c := range map[string]struct {
		typ   string
		code  codes.Code
		unset bool
	}{
		"system information denied": {hardware.SystemInformationType, codes.PermissionDenied, false},
		"system information fails":  {hardware.SystemInformationType, codes.Internal, false},
		"node identity fails":       {cluster.IdentityType, codes.Unavailable, false},
		"cluster information fails": {cluster.InfoType, codes.Internal, false},
		"no node identity":          {cluster.IdentityType, codes.NotFound, true},
		"no cluster information":    {cluster.InfoType, codes.NotFound, true},
	} {
		t.Run(name, func(t *testing.T) {
			n, r, ctx := dialStandIn(t)
			n.SetSMBIOSUUID(t, standInUUID, true)
			if c.unset {
				if c.typ != cluster.IdentityType {
					n.SetNodeID(t, standInNode)
				}
				if c.typ != cluster.InfoType {
					n.SetClusterID(t, standInCluster)
				}
			} else {
				n.SetNodeID(t, standInNode)
				n.SetClusterID(t, standInCluster)
				n.Fail(c.typ, c.code)
			}
			got, err := r.Identity(ctx)
			if err == nil {
				t.Fatalf("Identity %+v; want an error", got)
			}
			if status.Code(err) != c.code || strings.Contains(err.Error(), "stand-in") || got != (Identity{}) {
				t.Fatalf("%v (code %s), %+v; want code %s, no node text, no identity", err, status.Code(err), got, c.code)
			}
		})
	}
	// A context error is kept, as the other reads keep it.
	n, r, ctx := dialStandIn(t)
	n.SetNodeID(t, standInNode)
	n.SetClusterID(t, standInCluster)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.Identity(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}

// PA §3.3: the identity reads reach the dialled endpoint without node routing metadata, whatever the
// caller's context holds.
func TestIdentityDropsRouting(t *testing.T) {
	n, r, ctx := dialStandIn(t)
	n.SetNodeID(t, standInNode)
	n.SetClusterID(t, standInCluster)
	routed := metadata.AppendToOutgoingContext(ctx, "node", "192.0.2.2", "nodes", "192.0.2.3")
	if _, err := r.Identity(routed); err != nil {
		t.Fatal(err)
	}
	seen := n.Seen()
	if len(seen) != 3 {
		t.Fatalf("%d requests; want the three identity reads", len(seen))
	}
	for i, md := range seen {
		if len(md.Get("node")) > 0 || len(md.Get("nodes")) > 0 {
			t.Errorf("request %d carries routing metadata: %v", i, md)
		}
	}
}

// MachineConfig reads the active configuration through the stand-in's COSI state, so the api tests
// that ingest from it read what a node serves.
func TestMachineConfigThroughStandIn(t *testing.T) {
	n, r, ctx := dialStandIn(t)
	doc := []byte("version: v1alpha1\nmachine:\n  type: worker\n  token: synthetic.token\ncluster:\n  controlPlane:\n    endpoint: https://cp.example.test:6443\n")
	n.SetMachineConfig(t, doc)
	c, err := r.MachineConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(c.Bytes()), "synthetic.token") || c.ResourceVersion() == "" {
		t.Fatalf("configuration %q at version %q", c.Bytes(), c.ResourceVersion())
	}
}

// A Talos v1.14 node serves document kinds that the machinery of v1.13 does not register. The stand-in
// serves a configuration with two of them, and MachineConfig reads it whole. On v1.13.6 the stand-in
// cannot hold it ("not registered"), so a server on that library cannot import such a node.
func TestMachineConfigWithV114Documents(t *testing.T) {
	n, r, ctx := dialStandIn(t)
	doc := "version: v1alpha1\nmachine:\n  type: worker\n  token: synthetic.token\ncluster:\n  controlPlane:\n    endpoint: https://cp.example.test:6443\n" +
		"---\napiVersion: v1alpha1\nkind: UnattendedInstallConfig\ninstaller: {}\nprovisioning:\n  diskSelector:\n    match: disk.dev_path == \"/dev/vda\"\n  wipe: false\n" +
		"---\napiVersion: v1alpha1\nkind: KubeletConfig\nimage: ghcr.io/siderolabs/kubelet:v1.37.1\nconfig: {}\n"
	n.SetMachineConfig(t, []byte(doc))
	c, err := r.MachineConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"kind: UnattendedInstallConfig", "kind: KubeletConfig"} {
		if !strings.Contains(string(c.Bytes()), kind) {
			t.Errorf("the configuration read lacks %q: %q", kind, c.Bytes())
		}
	}
}
