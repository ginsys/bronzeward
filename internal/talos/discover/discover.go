// Package discover reads a cluster's identity from the operator's own talosconfig, for
// `bronzewardctl cluster discover`: the Talos cluster ID and version, the members, and for each
// member its SMBIOS UUID, node ID and platform. Every read is a read: the machinery client can also
// apply, reset, reboot and upgrade a node, and discover_guard_test.go in internal/talos holds this
// package's source to an allowlist of machinery calls so that none can be added unnoticed.
//
// The CLI links this package (the one exception to decision T2: the client never links the
// server's Talos packages). It therefore has its own dial and does not import internal/talos,
// whose reader returns whole machine configurations with their secrets. Nothing here writes a
// file, sends a node routing header or quotes the talosconfig or a node's error text.
package discover

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/resources/cluster"
	"github.com/siderolabs/talos/pkg/machinery/resources/hardware"
	"github.com/siderolabs/talos/pkg/machinery/resources/runtime"
)

// Member is one cluster member. SMBIOSUUID and Platform are "" when the member did not answer, or
// reported none (a container node has no SMBIOS UUID). Address is the one that answered, or the
// first listed when none did.
type Member struct {
	NodeID, Hostname, Address, Role string // Role is "controlplane" or "worker"
	SMBIOSUUID, Platform            string // Platform is metal, container or cloud
	Answered                        bool
}

// Result is what a cluster reports. Contract is the minor of Version; Supported is whether it is
// the minor of the machinery this binary links, the one the server renders. KubernetesEndpoint is
// a proposal from the first control-plane member, for the operator to replace.
type Result struct {
	ClusterID, Version, Contract, KubernetesEndpoint string
	Supported                                        bool
	Members                                          []Member
}

// memberEndpoint is where a member is dialled; a test points it at a stand-in.
var memberEndpoint = func(a netip.Addr) string { return netip.AddrPortFrom(a, 50000).String() }

// supportedMinor is the contract minor of the linked machinery; a test replaces it.
var supportedMinor = func() string {
	bi, _ := debug.ReadBuildInfo()
	return machineryContract(bi)
}

// machineryContract is the minor of the machinery module bi records, "" when there is none or a
// replacement stands in for it: the linked version is then in Replace, and the server's renderer
// refuses a replacement the same way.
func machineryContract(bi *debug.BuildInfo) string {
	if bi == nil {
		return ""
	}
	for _, d := range bi.Deps {
		if d.Path == "github.com/siderolabs/talos/pkg/machinery" {
			if d.Replace != nil {
				return ""
			}
			return contractOf(d.Version)
		}
	}
	return ""
}

// memberTimeout bounds the dial and the reads at one address of a member (readMemberAt gives the total
// bound for a member), so that an address that does not answer does not hold the command.
var memberTimeout = 10 * time.Second

// startTimeout bounds the dial and the version, cluster-information and member-list reads of the
// endpoint that Discover starts from, so that an endpoint that accepts a connection and never
// answers does not hold the command until the caller cancels it.
var startTimeout = 15 * time.Second

// ErrNoEndpoint is Discover's refusal when it is given no endpoint and the talosconfig names none.
var ErrNoEndpoint = errors.New("discover: the talosconfig names no endpoint")

// Discover dials endpoint (an IP literal with an optional port; the talosconfig's first endpoint
// when empty) with the talosconfig's current context and reads the cluster. A member that does not
// answer is listed as such, not an error.
func Discover(ctx context.Context, talosconfig []byte, endpoint string) (Result, error) {
	creds, first, err := credentials(talosconfig)
	if err != nil {
		return Result{}, err
	}
	if endpoint == "" {
		endpoint = first
	}
	if endpoint == "" {
		return Result{}, ErrNoEndpoint
	}
	ep, err := parseEndpoint(endpoint)
	if err != nil {
		return Result{}, err
	}
	// The reads from the endpoint have their own deadline; the members are read under the caller's
	// context, each address under its own.
	startCtx, stop := context.WithTimeout(ctx, startTimeout)
	defer stop()
	n, err := dial(startCtx, creds, ep)
	if err != nil {
		return Result{}, err
	}
	defer n.close()
	var res Result
	if res.Version, err = version(startCtx, n); err != nil {
		return Result{}, err
	}
	info, err := safe.StateGetByID[*cluster.Info](startCtx, n.api.COSI, cluster.InfoID)
	if err != nil {
		return Result{}, failed("reading the cluster information", err)
	}
	res.ClusterID, res.Contract = info.TypedSpec().ClusterID, contractOf(res.Version)
	res.Supported = res.Contract != "" && res.Contract == supportedMinor()
	list, err := safe.StateListAll[*cluster.Member](startCtx, n.api.COSI)
	if err != nil {
		return Result{}, failed("listing the cluster members", err)
	}
	stop()
	for ms := range list.All() {
		s := ms.TypedSpec()
		m := Member{NodeID: s.NodeID, Hostname: s.Hostname, Role: "worker"}
		if s.MachineType.IsControlPlane() {
			m.Role = "controlplane"
		}
		readMemberAt(ctx, creds, s.Addresses, &m)
		if m.Role == "controlplane" && s.ControlPlane != nil && res.KubernetesEndpoint == "" && m.Address != "" {
			res.KubernetesEndpoint = "https://" + net.JoinHostPort(m.Address, strconv.Itoa(s.ControlPlane.APIServerPort))
		}
		res.Members = append(res.Members, m)
	}
	return res, nil
}

// version is the Talos tag the dialled node answers.
func version(ctx context.Context, n *node) (string, error) {
	resp, err := n.api.Version(ctx)
	if err != nil {
		return "", failed("version", err)
	}
	msgs := resp.GetMessages()
	if len(msgs) != 1 || msgs[0].GetMetadata().GetError() != "" || msgs[0].GetVersion().GetTag() == "" {
		return "", errors.New("discover: version: the node gave no usable answer")
	}
	return msgs[0].GetVersion().GetTag(), nil
}

// readMemberAt fills m from the first of addrs that answers as m. Every address has its own deadline
// of memberTimeout, so a silent address does not use up the time of the next one. The total bound for
// a member is therefore the number of its listed addresses times memberTimeout. Address is the one
// that answered, or the first listed when none did, and a member that did not answer keeps the
// NodeID of its list entry and nothing else from any address.
func readMemberAt(ctx context.Context, creds *clientconfig.Context, addrs []netip.Addr, m *Member) {
	for _, a := range addrs {
		if m.Address == "" {
			m.Address = a.String()
		}
		if read, ok := readMember(ctx, creds, a, m.NodeID); ok {
			m.Address, m.SMBIOSUUID, m.Platform, m.Answered = a.String(), read.SMBIOSUUID, read.Platform, true
			return
		}
	}
}

// readMember reads the member at address a under memberTimeout and reports what it said, and whether
// it answered as nodeID, the member of the list. An address that answers as another node, or whose
// identity, SystemInformation or PlatformMetadata read fails, is no answer. Only the
// SystemInformation and PlatformMetadata resources may be missing: a container node has no SMBIOS
// information, and an old node may report no platform. Nothing is returned for an address that did
// not answer in full.
func readMember(ctx context.Context, creds *clientconfig.Context, a netip.Addr, nodeID string) (Member, bool) {
	ctx, cancel := context.WithTimeout(ctx, memberTimeout)
	defer cancel()
	n, err := dial(ctx, creds, memberEndpoint(a))
	if err != nil {
		return Member{}, false
	}
	defer n.close()
	id, err := safe.StateGetByID[*cluster.Identity](ctx, n.api.COSI, cluster.LocalIdentity)
	if err != nil || id.TypedSpec().NodeID != nodeID {
		return Member{}, false
	}
	var read Member
	if si, err := safe.StateGetByID[*hardware.SystemInformation](ctx, n.api.COSI, hardware.SystemInformationID); err == nil {
		read.SMBIOSUUID = si.TypedSpec().UUID
	} else if !state.IsNotFoundError(err) {
		return Member{}, false
	}
	if pm, err := safe.StateGetByID[*runtime.PlatformMetadata](ctx, n.api.COSI, runtime.PlatformMetadataID); err == nil {
		read.Platform = platform(pm.TypedSpec().Platform)
	} else if !state.IsNotFoundError(err) {
		return Member{}, false
	}
	return read, true
}
