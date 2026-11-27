// Package discover reads a cluster's identity from the operator's own talosconfig, for
// `bronzewardctl cluster discover`: the Talos cluster ID and version, the members, and for each
// member its SMBIOS UUID, node ID and platform. Every read is a read: the machinery client can also
// apply, reset, reboot and upgrade a node, and discover_guard_test.go in internal/talos holds this
// package's source to an allowlist of machinery calls so that none can be added unnoticed.
//
// The CLI links this package (the one exception to decision T2: the client never links the
// server's Talos packages). The server links it too, for POST /discoveries, through DiscoverLimited. It therefore has its own dial and does not import internal/talos,
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
	"strings"
	"time"

	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/ginsys/bronzeward/internal/contract"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/resources/cluster"
	"github.com/siderolabs/talos/pkg/machinery/resources/hardware"
	"github.com/siderolabs/talos/pkg/machinery/resources/k8s"
	"github.com/siderolabs/talos/pkg/machinery/resources/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
// a proposal for the operator to replace: the endpoint the control-plane members that answered all
// report as the cluster's own (clusterEndpoint), else the first control-plane member's address.
type Result struct {
	ClusterID, Version, Contract, KubernetesEndpoint string
	// Endpoint is the address Discover dialled, as host:port. ResolvedFrom is the DNS name it was
	// resolved from, by the walk or by the caller that passed it as DiscoverLimited's serverName, or ""
	// when the endpoint was an IP literal given with no name.
	Endpoint, ResolvedFrom string
	Supported              bool
	Members                []Member
	// Cut is true when the caller's context ended before every member was read: a member listed
	// unanswered then may be reachable. A member that did not answer on its own is not a cut.
	Cut bool
}

// TooManyMembers is DiscoverLimited's refusal for a cluster that lists more members than the caller
// allows. No member has been dialled when it is returned.
type TooManyMembers struct{ Limit int }

func (e *TooManyMembers) Error() string {
	return "discover: the cluster lists more than " + strconv.Itoa(e.Limit) + " members"
}

// memberEndpoint is where a member is dialled; a test points it at a stand-in.
var memberEndpoint = func(a netip.Addr) string { return netip.AddrPortFrom(a, 50000).String() }

// linkedMachinery is the version of the linked machinery module; a test replaces it. A cluster is
// supported when its contract is on the list of supported contracts and the machinery is not older
// than it (internal/contract).
var linkedMachinery = func() string {
	bi, _ := debug.ReadBuildInfo()
	return machineryVersion(bi)
}

// machineryVersion is the version of the machinery module bi records, "" when there is none or a
// replacement stands in for it: the linked version is then in Replace, and the server's renderer
// refuses a replacement the same way.
func machineryVersion(bi *debug.BuildInfo) string {
	if bi == nil {
		return ""
	}
	for _, d := range bi.Deps {
		if d.Path == "github.com/siderolabs/talos/pkg/machinery" {
			if d.Replace != nil {
				return ""
			}
			return d.Version
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

// Discover dials endpoint (an IP literal or a DNS name, with an optional port; the first endpoint of
// the context when empty) with the talosconfig's context called contextName (its current context when
// "") and reads the cluster. No other context is used. A name that the file does not hold is
// ErrUnknownContext, before any dial. A DNS name is resolved once and the address it gave is dialled
// (resolveEndpoint). A member that does not answer is listed as such, not an error.
//
// A name endpoint's certificate check is against the name, and the dial is on the address
// (persistence-api §3.3): the name is the one resolveEndpoint resolved.
func Discover(ctx context.Context, talosconfig []byte, contextName, endpoint string) (Result, error) {
	return walk(ctx, talosconfig, contextName, endpoint, "", 0, false, "")
}

// DiscoverListed is Discover for an endpoint the caller took from the talosconfig's own list (Endpoints), one of
// several it tries in order: a refusal of its name says it came from a talosconfig endpoint and not from
// --endpoint, which the person did not give.
func DiscoverListed(ctx context.Context, talosconfig []byte, contextName, endpoint string) (Result, error) {
	return walk(ctx, talosconfig, contextName, endpoint, "", 0, false, sourceListed)
}

// DiscoverLimited is Discover for a caller that bounds its work, the server, with its ctx alone: the
// reads from the endpoint have no deadline of their own beyond it. A cluster that lists more than
// maxMembers members (0 is no bound) is the refusal *TooManyMembers, raised after the member list is
// read and before any member is dialled. A caller whose ctx ends once the cluster's identity is read
// gets that identity, the members read so far, the rest listed unanswered, and Result.Cut set: only an
// identity that was not read is an error.
//
// serverName is for a caller that resolved a name itself and passes the address as endpoint: the
// bare name the endpoint's certificate is checked against, in place of that address. "" adds no
// option, and an endpoint that is itself a name is checked against its own name, as in Discover. The
// members are dialled at the addresses the member list gives, with no server name.
func DiscoverLimited(ctx context.Context, talosconfig []byte, contextName, endpoint, serverName string, maxMembers int) (Result, error) {
	return walk(ctx, talosconfig, contextName, endpoint, serverName, maxMembers, true, "")
}

// walk is all three: limited says the caller's ctx is the only bound on the reads from the endpoint, and
// that a ctx that ends after the identity is read is a Cut result, not an error. listedFrom is the source a
// refusal of the endpoint names when the caller took it from the talosconfig's list ("" for the usual two:
// the flag, or the file's first endpoint when none is given).
func walk(ctx context.Context, talosconfig []byte, contextName, endpoint, serverName string, maxMembers int, limited bool, listedFrom string) (Result, error) {
	creds, first, err := credentials(talosconfig, contextName)
	if err != nil {
		return Result{}, err
	}
	source := sourceFlag
	if listedFrom != "" {
		source = listedFrom
	}
	if endpoint == "" {
		endpoint, source = first, sourceFile
	}
	if endpoint == "" {
		return Result{}, ErrNoEndpoint
	}
	// The name lookup, the dial and the reads from the endpoint share one deadline; the members are
	// read under the caller's context, each address under its own.
	startCtx, stop := ctx, func() {}
	if !limited {
		var cancel context.CancelFunc
		startCtx, cancel = context.WithTimeout(ctx, startTimeout)
		stop = cancel
	}
	defer stop()
	ep, name, err := resolveEndpoint(startCtx, endpoint, source)
	if err != nil {
		return Result{}, err
	}
	if name == "" {
		name = serverName
	}
	n, err := dial(startCtx, creds, ep, name)
	if err != nil {
		return Result{}, err
	}
	defer n.close()
	res := Result{Endpoint: ep, ResolvedFrom: name}
	if res.Version, err = version(startCtx, n); err != nil {
		return Result{}, err
	}
	info, err := safe.StateGetByID[*cluster.Info](startCtx, n.api.COSI, cluster.InfoID)
	if err != nil {
		return Result{}, failed("reading the cluster information", err)
	}
	res.ClusterID, res.Contract = info.TypedSpec().ClusterID, contractOf(res.Version)
	res.Supported = contract.Supported.Listed(res.Contract, linkedMachinery())
	list, err := safe.StateListAll[*cluster.Member](startCtx, n.api.COSI)
	if err != nil {
		if limited && cutBy(ctx, err) {
			res.Cut = true // the identity was read; the caller's time ran out before the members were listed
			return res, nil
		}
		return Result{}, failed("listing the cluster members", err)
	}
	stop()
	if maxMembers > 0 && list.Len() > maxMembers {
		return Result{}, &TooManyMembers{Limit: maxMembers}
	}
	// The cluster's own endpoint, as the control-plane members that answered report it: set once one
	// reports a value, and dropped for good when a member reports none or another one.
	var own string
	var cpAnswered int
	agree := true
	for ms := range list.All() {
		s := ms.TypedSpec()
		m := Member{NodeID: s.NodeID, Hostname: s.Hostname, Role: "worker"}
		if s.MachineType.IsControlPlane() {
			m.Role = "controlplane"
		}
		reported := readMemberAt(ctx, creds, s.Addresses, &m)
		if !m.Answered && ended(ctx) {
			res.Cut = true
		}
		if m.Role == "controlplane" && s.ControlPlane != nil && res.KubernetesEndpoint == "" && m.Address != "" {
			res.KubernetesEndpoint = "https://" + net.JoinHostPort(m.Address, strconv.Itoa(s.ControlPlane.APIServerPort))
		}
		if m.Role == "controlplane" && m.Answered {
			cpAnswered++
			if reported == "" || own != "" && reported != own {
				agree = false
			}
			own = reported
		}
		res.Members = append(res.Members, m)
	}
	if cpAnswered > 0 && agree {
		res.KubernetesEndpoint = own
	}
	return res, nil
}

// deadlineTolerance is how near the caller's deadline has to be for a read that failed to count as cut by it.
// A node's deadline is derived from the caller's by the transport, and its DeadlineExceeded can arrive a few
// microseconds before the caller's own timer has set ctx.Err().
const deadlineTolerance = 100 * time.Millisecond

// ended reports whether the caller's time is up: its context has ended, or its deadline is within
// deadlineTolerance. It is only for a member that did not answer, where listing the walk as Cut errs on
// the side of "that member may be reachable"; a failed read goes through cutBy.
func ended(ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	d, ok := ctx.Deadline()
	return ok && time.Until(d) < deadlineTolerance
}

// cutBy reports whether err, a failed read, is the caller's cut: its context has ended, or the read failed
// with a deadline or cancellation code while the caller's deadline is within deadlineTolerance. Any other
// error is the node's own, however near the deadline is.
func cutBy(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return true
	}
	code := status.Code(err)
	timedOut := code == codes.DeadlineExceeded || code == codes.Canceled || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
	return timedOut && ended(ctx)
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
// a member is therefore the number of its listed addresses times memberTimeout, or less when the caller's
// ctx ends first: no address is dialled after that. Address is the one
// that answered, or the first listed when none did, and a member that did not answer keeps the
// NodeID of its list entry and nothing else from any address. It returns the cluster endpoint a
// control-plane member that answered reports (readMember), "" for any other member.
func readMemberAt(ctx context.Context, creds *clientconfig.Context, addrs []netip.Addr, m *Member) (reported string) {
	for _, a := range addrs {
		if m.Address == "" {
			m.Address = a.String()
		}
		if ctx.Err() != nil { // strict: an address is still tried while the deadline is near and has not passed
			return ""
		}
		if read, own, ok := readMember(ctx, creds, a, m.NodeID, m.Role == "controlplane"); ok {
			m.Address, m.SMBIOSUUID, m.Platform, m.Answered = a.String(), read.SMBIOSUUID, read.Platform, true
			return own
		}
	}
	return ""
}

// readMember reads the member at address a under memberTimeout and reports what it said, and whether
// it answered as nodeID, the member of the list. These are no answer: a member with no ID, an address
// that answers as another node or with no ID, and an address whose identity, SystemInformation or
// PlatformMetadata read fails. Only the
// SystemInformation and PlatformMetadata resources may be missing: a container node has no SMBIOS
// information, and an old node may report no platform. Nothing is returned for an address that did
// not answer in full. For a control-plane member (controlPlane) it also reads the cluster endpoint the
// node reports, "" when it reports none a caller can use (clusterEndpoint): that read is no part of the
// answer, so a node that refuses it, or does not have the resource, is still an answering member.
func readMember(ctx context.Context, creds *clientconfig.Context, a netip.Addr, nodeID string, controlPlane bool) (Member, string, bool) {
	ctx, cancel := context.WithTimeout(ctx, memberTimeout)
	defer cancel()
	n, err := dial(ctx, creds, memberEndpoint(a), "")
	if err != nil {
		return Member{}, "", false
	}
	defer n.close()
	id, err := safe.StateGetByID[*cluster.Identity](ctx, n.api.COSI, cluster.LocalIdentity)
	if err != nil || nodeID == "" || id.TypedSpec().NodeID != nodeID { // an empty ID names no node, on either side
		return Member{}, "", false
	}
	var read Member
	if si, err := safe.StateGetByID[*hardware.SystemInformation](ctx, n.api.COSI, hardware.SystemInformationID); err == nil {
		read.SMBIOSUUID = si.TypedSpec().UUID
	} else if !state.IsNotFoundError(err) {
		return Member{}, "", false
	}
	if pm, err := safe.StateGetByID[*runtime.PlatformMetadata](ctx, n.api.COSI, runtime.PlatformMetadataID); err == nil {
		read.Platform = platform(pm.TypedSpec().Platform)
	} else if !state.IsNotFoundError(err) {
		return Member{}, "", false
	}
	var own string
	if controlPlane {
		// KubePrismEndpoints is not sensitive, unlike the API server's own configuration, which a reader
		// certificate cannot read: its first entry is the cluster's endpoint where the machine
		// configuration names one, and localhost where it names none.
		if kp, err := safe.StateGetByID[*k8s.KubePrismEndpoints](ctx, n.api.COSI, k8s.KubePrismEndpointsID); err == nil {
			own = clusterEndpoint(kp.TypedSpec().Endpoints)
		}
	}
	return read, own, true
}

// clusterEndpoint is the Kubernetes endpoint (https, host and port) that es, a node's KubePrism
// endpoints, give as the cluster's own, or "" for no value: the first entry counts, and only when its
// host is an IP address with no zone that is not unspecified, or a DNS name other than localhost, in
// the grammar the cluster record takes (validName, with no trailing dot and no underscore, as
// internal/api hostShape), and its port is 1 to 65535. The list holds localhost on a control-plane node with no
// cluster endpoint, and the other members' addresses after the first entry, so no other entry says
// anything about the cluster's endpoint.
func clusterEndpoint(es []k8s.KubePrismEndpoint) string {
	if len(es) == 0 || es[0].Port < 1 || es[0].Port > 65535 {
		return ""
	}
	host := es[0].Host
	if a, err := netip.ParseAddr(host); err == nil {
		if a.Zone() != "" || a.IsUnspecified() {
			return ""
		}
	} else if !validName(host) || strings.HasSuffix(host, ".") || strings.Contains(host, "_") || strings.EqualFold(host, "localhost") {
		return ""
	}
	return "https://" + net.JoinHostPort(host, strconv.FormatUint(uint64(es[0].Port), 10))
}
