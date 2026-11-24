package main

import (
	"encoding/json"
	"net/url"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// The sentences of client contracts A9 item 7: where a release's hostname for a machine came from. A test reads
// the item and checks each of these against its quotations, so a change here without the document fails.
const (
	hostConfiguration = "Hostname: set in the configuration. The machine's name does not change it."
	hostNone          = "Hostname: not set in the configuration; the node chooses its own."
	hostLegacyStable  = "Hostname: the configuration holds the legacy stable-hostname setting, so the machine's name is not used. Remove that setting to use the name."
	hostUnrecorded    = "Hostname: not recorded for this release."
	hostNodeName      = "The machine's Kubernetes node name normally follows the hostname, so it may change too."
	hostUnread        = "The comparison with the release last applied to this machine could not be read."
)

// hostnameApplied is what the release last applied to the machine recorded for it; unread is true when that
// record could not be read, and none when the machine has no applied release.
type hostnameApplied struct {
	h      apitypes.HostnameBody
	none   bool
	unread bool
}

// hostnameLines are the lines a release review prints for a machine's hostname (A9 item 7, table output only):
// the sentence of its source, then, for a name taken from the machine's name, what differs from the release last
// applied and from the machine's current name. A source this client does not know shows its raw value and no
// sentence; a review that carries no source shows nothing. No line holds a hostname a release took from its
// configuration: the release records none.
func hostnameLines(h apitypes.HostnameBody, applied hostnameApplied, current *string) []string {
	switch h.Source {
	case "":
		return nil
	case apitypes.HostnameBodySourceConfiguration:
		return []string{hostConfiguration}
	case apitypes.HostnameBodySourceNone:
		return []string{hostNone}
	case apitypes.HostnameBodySourceLegacyStable:
		return []string{hostLegacyStable}
	case apitypes.HostnameBodySourceUnrecorded:
		return []string{hostUnrecorded}
	case apitypes.HostnameBodySourceMachineName:
	default:
		return []string{"Hostname source: " + pr(string(h.Source))}
	}
	if h.Name == nil {
		return nil
	}
	name := pr(*h.Name)
	lines := []string{"Hostname: " + name + ", taken from the machine's name."}
	restart := renameReaches[1]
	switch {
	case applied.unread:
		lines = append(lines, hostUnread)
	case applied.none:
	case applied.h.Source == apitypes.HostnameBodySourceNone || applied.h.Source == apitypes.HostnameBodySourceLegacyStable:
		lines = append(lines, "This release sets the machine's hostname to "+name+", taken from its name. The last release applied to it left the hostname to the node. "+hostNodeName, restart)
	case applied.h.Source == apitypes.HostnameBodySourceUnrecorded:
		lines = append(lines, "This release sets the machine's hostname to "+name+", taken from its name.", restart)
	case applied.h.Source == apitypes.HostnameBodySourceConfiguration:
		lines = append(lines, "This release now takes the machine's hostname from its name, "+name+", in place of the setting it had before.", hostNodeName, restart)
	case applied.h.Source == apitypes.HostnameBodySourceMachineName && applied.h.Name != nil && *applied.h.Name != *h.Name:
		lines = append(lines, "This release sets the machine's hostname to "+name+", taken from its name. The last release applied to it set "+pr(*applied.h.Name)+".", hostNodeName, restart)
	}
	if current != nil && *current != *h.Name {
		lines = append(lines, "This release used the earlier name "+name+". While its hostname comes from the machine's name, the next release will use "+pr(*current)+".")
	}
	return lines
}

// reviewHostname reads what hostnameLines compares a release's hostname with, once: the machine (its current name
// and the release last applied to it), then that release's record for the machine. Only a review whose hostname
// comes from the machine's name compares anything, so any other source reads nothing. A read that fails is not
// guessed: the lines say so.
func reviewHostname(c *client.Client, r *apitypes.ReviewBody) []string {
	if r.Hostname.Source != apitypes.HostnameBodySourceMachineName || r.Hostname.Name == nil {
		return hostnameLines(r.Hostname, hostnameApplied{none: true}, nil)
	}
	m, err := c.Get("/machines/" + url.PathEscape(r.Machine))
	if err != nil {
		return hostnameLines(r.Hostname, hostnameApplied{unread: true}, nil)
	}
	var mb apitypes.MachineBody
	if json.Unmarshal(m.Body, &mb) != nil {
		return hostnameLines(r.Hostname, hostnameApplied{unread: true}, nil)
	}
	if mb.Applied == nil {
		return hostnameLines(r.Hostname, hostnameApplied{none: true}, mb.Name)
	}
	rel, err := c.Get("/releases/" + url.PathEscape(mb.Applied.Release))
	var rb apitypes.ReleaseBody
	if err != nil || json.Unmarshal(rel.Body, &rb) != nil {
		return hostnameLines(r.Hostname, hostnameApplied{unread: true}, mb.Name)
	}
	for _, x := range rb.Machines {
		if x.Machine == r.Machine {
			return hostnameLines(r.Hostname, hostnameApplied{h: x.Hostname}, mb.Name)
		}
	}
	return hostnameLines(r.Hostname, hostnameApplied{unread: true}, mb.Name)
}
