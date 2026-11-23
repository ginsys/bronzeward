package main

import (
	"flag"
	"fmt"
	"net/url"
	"strings"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// A machine given by name or by identifier (ginsys/bronzeward#1055, part 9b). `machine show`, `rename`, `freeze`,
// `unfreeze` and `plan create --machine` take either. An identifier is sent as it is and never lists. A name is
// matched in the machine list, which is read with its largest page (500 machines, so one request at the design's
// 100 machines), across every cluster the credential reads: a name is unique in a cluster only, and the command line
// holds no cluster. `--cluster` (a cluster's name or its identifier) narrows the match. A name that no machine holds,
// or that machines of two clusters hold, refuses before anything is sent.

// machineByNameNote is the one sentence of every verb's help that says a machine may be given by name.
const machineByNameNote = "<machine> is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID."

// isMachineID reports whether ref is an identifier. A name that begins with mch_ is read as one.
func isMachineID(ref string) bool { return strings.HasPrefix(ref, "mch_") }

// givenLabel is how a line names the machine an act landed on, in the client's one form for a machine (machineLabel):
// the identifier with the name beside it for a machine the person gave by name, so they read which machine it was, and
// the identifier alone for one given by identifier.
func givenLabel(ref, id string) string {
	if ref == id {
		return id
	}
	return machineLabel(id, &ref)
}

// machinePath is "/machines/<identifier><suffix>" of the machine ref names, resolved on the session's own client.
func machinePath(ref, cluster, suffix string) func(*client.Client) (string, error) {
	return func(c *client.Client) (string, error) {
		id, _, err := machineByName(c, ref, cluster)
		return "/machines/" + url.PathEscape(id) + suffix, err
	}
}

// withCluster adds the --cluster flag, which narrows a machine given by name and is no filter of the list, to flags.
func withCluster(cluster *string, flags func(*flag.FlagSet)) func(*flag.FlagSet) {
	return func(fs *flag.FlagSet) {
		flags(fs)
		fs.StringVar(cluster, "cluster", "", "")
	}
}

// machineFilter is a list's path and query with the machine filter ref (when given) resolved to its identifier.
func machineFilter(ref, cluster, path string, q url.Values) func(*client.Client) (string, url.Values, error) {
	return func(c *client.Client) (string, url.Values, error) {
		if ref != "" {
			id, _, err := machineByName(c, ref, cluster)
			if err != nil {
				return "", nil, err
			}
			q.Set("machine", id)
		}
		return path, q, nil
	}
}

// writeNamed resolves ref and sends the write build makes of its identifier, both on one client (a token read from
// standard input is read once). A write built from a name is bound to the epoch of the lookup, and its repeat
// instruction names the identifier, since the name may no longer find the machine (a rename that committed) or may
// find another.
func (e *env) writeNamed(ref, cluster string, prep func(*client.Client) error, build func(id string) *client.Write,
	say func(*client.Client, *client.Response) ([]string, error)) (err error) {
	c, err := e.open()
	if err != nil {
		return err
	}
	defer func() { err = joinStoredSave(err, c.Save()) }()
	id, at, err := machineByName(c, ref, cluster)
	if err != nil {
		return err
	}
	w := build(id)
	if id != ref {
		w.ReadsAt(at.epoch, at.moves)
		w.Input = "machine " + client.ShellWord(id) + " in place of " + client.ShellWord(ref)
	}
	return e.writeWith(c, prep, w, say)
}

// machineByName is the identifier of the machine ref names, on a client already open, which the caller saves, with the
// epoch and move count the machine list was read at: a write built from a name binds to that read, so a later read of
// the lookup (the cluster names) that shows another epoch refuses the write.
func machineByName(c *client.Client, ref, cluster string) (string, epochBind, error) {
	if isMachineID(ref) {
		if cluster != "" {
			return "", epochBind{}, usageError{fmt.Errorf("--cluster narrows a name; an identifier needs none")}
		}
		return ref, epochBind{}, nil
	}
	items, epoch, moves, err := c.ListBound("/machines?limit=500")
	if err != nil {
		return "", epochBind{}, err
	}
	if epoch == "" {
		epoch = c.Context.LastEpoch
	}
	at := epochBind{epoch: epoch, moves: moves}
	all, err := decode[apitypes.MachineBody](c, items...)
	if err != nil {
		return "", at, err
	}
	var hits []apitypes.MachineBody
	for _, m := range all {
		if m.Name != nil && *m.Name == ref {
			hits = append(hits, m)
		}
	}
	var names map[string]string
	if cluster != "" || len(hits) > 1 {
		if names, err = clusterNames(c); err != nil {
			return "", at, err
		}
	}
	if cluster != "" {
		var in []apitypes.MachineBody
		for _, m := range hits {
			if m.Cluster == cluster || names[m.Cluster] == cluster {
				in = append(in, m)
			}
		}
		hits = in
	}
	switch len(hits) {
	case 0:
		in := ""
		if cluster != "" {
			in = " in cluster " + client.Printable(cluster)
		}
		return "", at, usageError{fmt.Errorf("no machine is named %s%s; nothing was sent", client.Printable(ref), in)}
	case 1:
		return hits[0].Id, at, nil
	}
	held := make([]string, len(hits))
	for i, m := range hits {
		name := names[m.Cluster]
		if name == "" {
			name = m.Cluster
		}
		held[i] = m.Id + " in cluster " + client.Printable(name)
	}
	return "", at, usageError{fmt.Errorf("%s names %d machines: %s; give the identifier or --cluster; nothing was sent", client.Printable(ref), len(hits), strings.Join(held, ", "))}
}

// clusterNames is the name of every cluster the credential reads, by identifier.
func clusterNames(c *client.Client) (map[string]string, error) {
	items, err := c.List("/clusters?limit=500")
	if err != nil {
		return nil, err
	}
	cs, err := decode[apitypes.ClusterBody](c, items...)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, x := range cs {
		names[x.Id] = x.Name
	}
	return names, nil
}
