package main

import (
	"encoding/json"
	"net/url"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// The sentences of client contracts A9 item 8. A test reads the item and checks each against its quotations.
const (
	// hostWaitsUnread stands for the long sentence where the client cannot show it. A null or absent node name cannot
	// go with a holder the server named (the rule needs a recorded name), so only a failed read of the review or a
	// race between the two reads reaches it: it takes no "Not checked" notice.
	hostWaitsUnread      = "This plan waits for another machine's hostname."
	nodeNotCheckedHidden = "Not checked: the review hides an input to this machine's Kubernetes node name, so a clash with another machine's node name cannot be checked."
	nodeNotCheckedOld    = "Not checked: this release was made before hostname clashes were recorded, so a clash with another machine's hostname cannot be checked."
)

// hostWaits is the sentence of a plan that waits for another machine's hostname: holder is the machine whose last
// applied release records the node name, node that name, mine the machine the plan is for. It says "waits", never
// "refused" or "failed": the plan stays approved and nothing was sent.
func hostWaits(holder, node, mine string) string {
	return "This plan waits. The last release applied to machine " + holder + " records the Kubernetes node name " + node +
		", and this release gives the same name to machine " + mine + ". This plan has sent nothing to either machine. Apply the release for machine " +
		holder + " first, or give machine " + mine + " another hostname."
}

// waitsMachine names a machine as A8 rule 1 names it in the CLI: by its name where it has one, else by its whole
// identifier.
func waitsMachine(id string, name *string) string {
	if name != nil && *name != "" {
		return pr(*name)
	}
	return pr(id)
}

// hostnameWaits is the sentence for the approved apply-config plan that pos names, or "" where pos names no holder for
// it. The node name is the one the plan's release gives the machine, read from that release's review; where that read
// gives none, the short sentence stands. The position belongs to the plan it names, so another plan is never worded
// from it.
func hostnameWaits(c *client.Client, pos *apitypes.PositionBody, plan string) string {
	p := pos.Plan
	if p == nil || p.Id != plan || p.State != apitypes.PositionPlanStateApproved || p.Operation != apitypes.PositionPlanOperationApplyConfig || pos.HostnameHeldBy == nil {
		return ""
	}
	var rv apitypes.ReviewBody
	if err := getJSON(c, "/releases/"+url.PathEscape(p.Release)+"/machines/"+url.PathEscape(pos.Machine.Id)+"/review", &rv); err != nil ||
		rv.Hostname.NodeName == nil || *rv.Hostname.NodeName == "" {
		return hostWaitsUnread
	}
	h := pos.HostnameHeldBy
	return hostWaits(waitsMachine(h.Id, h.Name), pr(*rv.Hostname.NodeName), waitsMachine(pos.Machine.Id, pos.Machine.Name))
}

// nodeNameKind is what a review's hostname.nodeName holds: not recorded (absent), not compared (null) or a name.
type nodeNameKind int

const (
	nodeNameUnknown nodeNameKind = iota // the body could not be read as the review it is
	nodeNameAbsent
	nodeNameNull
	nodeNameSet
)

// nodeNameKindOf reads the kind from the review's raw body. The typed body cannot: nodeName is a *string with
// omitempty, so absent and null both read nil there, and the two have different notices.
func nodeNameKindOf(body []byte) nodeNameKind {
	var r struct {
		Hostname map[string]json.RawMessage `json:"hostname"`
	}
	if json.Unmarshal(body, &r) != nil {
		return nodeNameUnknown
	}
	raw, ok := r.Hostname["nodeName"]
	switch {
	case !ok:
		return nodeNameAbsent
	case string(raw) == "null":
		return nodeNameNull
	}
	return nodeNameSet
}

// withNodeNotice adds, last, the one notice of A9 item 8 where no node-name clash was checked: the node name not
// recorded (the release is older), or not compared because the review hides an input to it. A release made after the
// record with a none or legacy-stable source and no node name already says so in its item 7 sentence, a source this
// client does not know gets no notice (the exemption cannot be decided), and a review with no line gets none.
func withNodeNotice(lines []string, h apitypes.HostnameBody, kind nodeNameKind) []string {
	switch h.Source {
	case apitypes.HostnameBodySourceMachineName, apitypes.HostnameBodySourceConfiguration, apitypes.HostnameBodySourceNone,
		apitypes.HostnameBodySourceLegacyStable, apitypes.HostnameBodySourceUnrecorded:
	default:
		return lines
	}
	if len(lines) == 0 {
		return lines
	}
	switch {
	case kind == nodeNameAbsent:
		return append(lines, nodeNotCheckedOld)
	case kind == nodeNameNull && h.Source != apitypes.HostnameBodySourceNone && h.Source != apitypes.HostnameBodySourceLegacyStable:
		return append(lines, nodeNotCheckedHidden)
	}
	return lines
}
