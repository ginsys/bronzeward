package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"unicode"
	"unicode/utf8"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/talos/discover"
)

// discoverCluster reads the cluster; tests replace it, because the CLI's tests may not link the
// stand-in node (internal/client's closure test refuses it).
var discoverCluster = discover.Discover

// maxTalosconfig bounds the file read: a talosconfig is a few KiB.
const maxTalosconfig = 1 << 20

// clusterDiscover reads a cluster's identity with the operator's own talosconfig and prints the
// commands that record it (operator-experience.md §5.2). It calls no Bronzeward route and writes
// nothing: the talosconfig never reaches the server (persistence-api.md choice 29). The file is
// named with --talosconfig and nowhere else is looked at, not $TALOSCONFIG, not a default file.
func (e *env) clusterDiscover(args []string) error {
	var file, endpoint, talosContext string
	if _, err := e.parse("cluster discover", args, 0, func(fs *flag.FlagSet) {
		fs.StringVar(&file, "talosconfig", "", "")
		fs.StringVar(&endpoint, "endpoint", "", "")
		fs.StringVar(&talosContext, "talos-context", "", "")
	}); err != nil {
		return err
	}
	if file == "" {
		return usageError{errors.New("cluster discover needs --talosconfig <file>: it looks in no default place")}
	}
	if e.output != "table" {
		return usageError{errors.New("cluster discover prints commands as text: use -o table")}
	}
	if unprintable(file) {
		return usageError{errors.New("--talosconfig names a file whose name holds a byte or character that cannot be printed back in the commands it prints: rename the file")}
	}
	if unprintable(talosContext) {
		return usageError{errors.New("--talos-context names a context whose name holds a byte or character that cannot be printed back in the onboarding command: rename the context")}
	}
	b, err := readTalosconfig(file)
	if err != nil {
		return err
	}
	var res discover.Result
	if err := e.cancellable("the cluster's Talos nodes", func(ctx context.Context) (err error) {
		res, err = discoverCluster(ctx, b, talosContext, endpoint)
		return err
	}); errors.Is(err, discover.ErrUnknownContext) {
		return usageError{errors.New("--talos-context names a context that the talosconfig does not hold")}
	} else if errors.Is(err, discover.ErrNoEndpoint) {
		return usageError{errors.New("the talosconfig names no endpoint: give --endpoint <host|ip[:port]>")}
	} else if err != nil {
		return err
	}
	return e.say(discoverLines(res, file, talosContext, endpoint)...)
}

// unprintable says that a file name holds a byte or character that a command printed back cannot show.
func unprintable(file string) bool {
	return !utf8.ValidString(file) || strings.IndexFunc(file, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0
}

// readTalosconfig reads the file the operator named, at most maxTalosconfig bytes of it.
func readTalosconfig(file string) ([]byte, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("reading the talosconfig: %w", unwrapPath(err))
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxTalosconfig+1))
	if err != nil || len(b) > maxTalosconfig {
		return nil, errors.New("reading the talosconfig: the file cannot be read or is not a talosconfig")
	}
	return b, nil
}

// firstControlPlane is the address the first control-plane member that answered was read at: the
// endpoint a first key is tried at (operator-experience.md §5.2). None answered, none is given.
func firstControlPlane(res discover.Result) (string, bool) {
	for _, m := range res.Members {
		if m.Role == "controlplane" && m.Answered && m.Address != "" {
			return m.Address, true
		}
	}
	return "", false
}

// endpointArg is address as the server's endpoint grammar takes it: an IPv6 literal in brackets.
func endpointArg(address string) string {
	if strings.Contains(address, ":") {
		return "[" + address + "]"
	}
	return address
}

// unwrapPath is err without the file path's directory noise: the operating system's own text.
func unwrapPath(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// discoverLines is the listing and the commands that record it. A member that did not answer, or
// reported no platform, has no command: its identity or platform would be a guess. An unsupported
// contract has none for the cluster either, because the server would refuse it.
func discoverLines(res discover.Result, file, talosContext, override string) []string {
	out := []string{sf("Talos cluster ID %s, Talos %s, contract %s.", res.ClusterID, res.Version, res.Contract)}
	if res.ResolvedFrom != "" {
		out = append(out, sf("Dialled %s: the address that %s resolved to, once. The first reads used that address. Each member was read at its own listed address.", res.Endpoint, res.ResolvedFrom))
	}
	if res.KubernetesEndpoint != "" {
		out = append(out, sf("Kubernetes endpoint %s, the cluster's own where its control-plane members agree on one, else the first control-plane member's address: replace it if a VIP or a DNS name fronts the API server.", res.KubernetesEndpoint))
	} else {
		out = append(out, "No control-plane member gave an API server port: give the Kubernetes endpoint yourself.")
	}
	var table strings.Builder
	tw := tabwriter.NewWriter(&table, 0, 4, 2, ' ', 0)
	for _, m := range res.Members {
		what := "did not answer"
		switch {
		case m.Answered && m.Platform == "":
			what = "answered, reported no platform"
		case m.Answered && m.SMBIOSUUID != "":
			what = sf("%s, SMBIOS UUID %s", m.Platform, m.SMBIOSUUID)
		case m.Answered:
			what = sf("%s, node ID %s", m.Platform, m.NodeID)
		}
		_ = tf(tw, "  %s\t%s\t%s\t%s\t%s\n", m.NodeID, m.Hostname, m.Role, m.Address, what)
	}
	tw.Flush()
	out = append(out, "", "Members:")
	out = append(out, strings.Split(strings.TrimRight(table.String(), "\n"), "\n")...)
	if !res.Supported {
		return append(out, "", sf("Not recorded: contract %s is not the contract of the machinery in this client, so the server would refuse the cluster.", res.Contract))
	}
	endpoint := "<https url>"
	if res.KubernetesEndpoint != "" {
		endpoint = client.ShellWord(res.KubernetesEndpoint)
	}
	out = append(out, "", "Record it with these commands, in this order. The cluster ID is the one cluster add prints:", "",
		"  bronzewardctl cluster add --name <name> --endpoint "+endpoint+" --contract "+client.ShellWord(res.Contract)+" --talos-cluster-id "+client.ShellWord(res.ClusterID))
	var notRecorded []string
	for _, m := range res.Members {
		key := "--talos-node-id " + client.ShellWord(m.NodeID)
		if m.SMBIOSUUID != "" {
			key = "--smbios-uuid " + client.ShellWord(m.SMBIOSUUID)
		}
		switch {
		case !m.Answered:
			notRecorded = append(notRecorded, sf("%s (%s) did not answer.", m.NodeID, m.Hostname))
		case m.Platform == "":
			notRecorded = append(notRecorded, sf("%s (%s) answered but reported no platform.", m.NodeID, m.Hostname))
		default:
			out = append(out, "  bronzewardctl machine add --cluster <cluster id> "+key+" --platform "+client.ShellWord(m.Platform)+" --talos-endpoint "+client.ShellWord(endpointArg(m.Address)))
		}
	}
	selection := ""
	if talosContext != "" {
		selection += " --talos-context " + client.ShellWord(talosContext)
	}
	if override != "" {
		selection += " --endpoint " + client.ShellWord(override)
	}
	if address, ok := firstControlPlane(res); ok {
		first := "  bronzewardctl cluster first-key --cluster <cluster id> --kind talosconfig --file " + client.ShellWord(file) + " --endpoint " + client.ShellWord(endpointArg(address))
		if talosContext != "" {
			first += " --talos-context " + client.ShellWord(talosContext)
		}
		out = append(out, first)
	} else {
		out = append(out, "", "Not recorded: the first key. No control-plane member answered, so no address can carry it; run cluster discover again once one does.")
	}
	for _, n := range notRecorded {
		out = append(out, "", "Not recorded: "+n+" Add it with machine add once it answers.")
	}
	return append(out, "", "Nothing was sent to the server. Until the first-key command has run, the first source: machine import fails with talos-access-unavailable.",
		"cluster onboard does all of this in one run, and imports each machine into a draft: bronzewardctl cluster onboard --talosconfig "+client.ShellWord(file)+selection+
			" --name <name> --kubernetes-endpoint proposed")
}
