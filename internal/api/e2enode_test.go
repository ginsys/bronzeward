package api

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/talos/talostest"
)

// TestE2ENode is the Talos target of the browser cases that onboard a cluster (web/e2e/auth.conf.ts runs
// the compiled test as bin/e2eseed.test, as it runs TestE2ESeed): a two-member cluster of stand-ins on
// loopback that the disposable deployment can dial. A discovery dials each member at its own address and
// port 50000, so the control plane listens on 127.0.0.2:50000 and the worker on 127.0.0.3:50000 (macOS
// carries only 127.0.0.1 on lo0: the Safari job adds the two aliases first); the
// talosconfig names the control plane as its endpoint and a second context that no case uses.
//
// It writes the endpoint and the talosconfig as JSON to BW_E2E_NODE_OUT and then serves until its standard
// input closes, which the harness closes when it stops the deployment. Without BW_E2E_NODE_OUT it does
// nothing, so a plain `go test` skips it. The talosconfig carries BW_WEB_CANARY in a comment, which the
// artifact scan then finds in any file that kept a copy of it.
func TestE2ENode(t *testing.T) {
	out := os.Getenv("BW_E2E_NODE_OUT")
	if out == "" {
		t.Skip("BW_E2E_NODE_OUT names no file: this test is the browser cases' Talos target")
	}
	ca := newTalosCA(t)
	pki := talostest.NewPKIFromCA(t, ca)
	// 32 bytes in padded base64, as `talosctl get info` prints one and the cluster route requires.
	clusterID := base64.StdEncoding.EncodeToString([]byte("bronzeward-e2e-onboard-cluster-0"))
	members := []talostest.Member{
		{NodeID: "e2e-n1", Hostname: "cp-1", Addresses: []string{"127.0.0.2"}, ControlPlanePort: 6443},
		{NodeID: "e2e-n2", Hostname: "w-1", Addresses: []string{"127.0.0.3"}},
	}
	// Both configurations validate in the node's mode (a cluster section on each, the machine CA on the worker), so a
	// draft that imports both publishes: the handover journey (ONBOARD-03) runs the whole press (ginsys/bronzeward#1068).
	// Each carries comments as a talosctl-written configuration does, so the journey shows an adoption whose release
	// differs from the adopted configuration in form only (ginsys/bronzeward#1098). They also state the empty defaults
	// talosctl writes (machine.token on a control plane, machine.ca.key and machine.certSANs on a worker): the
	// composition adds any that is missing, and a release with an added key differs in content.
	cluster := "# Provides cluster specific configuration options.\ncluster:\n  controlPlane:\n    endpoint: https://cp.example.test:6443 # the control plane\n"
	for i, m := range members {
		n := talostest.ServeOn(t, pki, m.Addresses[0]+":50000")
		n.SetNodeID(t, m.NodeID)
		n.SetClusterID(t, clusterID)
		n.SetMembers(t, members...)
		n.SetSMBIOSUUID(t, []string{"11111111-2222-3333-4444-555555555555", "66666666-7777-8888-9999-aaaaaaaaaaaa"}[i], true)
		n.SetPlatform(t, "metal")
		if i == 0 {
			n.SetMachineConfig(t, []byte("# Stand-in control plane.\n"+controlPlaneConfig(ca)+"  token: \"\"\n  certSANs: []\n"+cluster))
		} else {
			n.SetMachineConfig(t, []byte("# Stand-in worker.\nversion: v1alpha1\nmachine:\n  type: worker\n  token: "+runToken+"\n  ca:\n    crt: "+b64(ca.Crt)+"\n    key: \"\"\n  certSANs: []\n"+cluster))
		}
	}
	other := "    endpoints: [192.0.2.9]\n    ca: " + strings.Repeat("A", 8) + "\n"
	doc := "# " + os.Getenv("BW_WEB_CANARY") + "\n" + string(pki.Talosconfig("127.0.0.2:50000", "", "")) + "  b:\n" + other
	b, err := json.Marshal(map[string]any{"endpoint": "127.0.0.2:50000", "clusterId": clusterID, "talosconfig": doc})
	if err != nil {
		t.Fatal(err)
	}
	// Renamed into place, so the harness never reads half of it.
	if err := os.WriteFile(out+".tmp", b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(out+".tmp", out); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}
