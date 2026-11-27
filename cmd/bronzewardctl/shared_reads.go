package main

import (
	"flag"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// The reads of the shared verbs (operator-experience.md §5.4 and §10): adoptions of a shared name and
// draft impact. Both are reads with any role, and neither renders a configuration.

// sharedAdoptions lists the clusters that use a shared name (GET /{kind}/{id}/adoptions): the client finds
// the shared head's id by the name in the list of heads, since the route is keyed by the id.
func (e *env) sharedAdoptions(k sharedKind, args []string) error {
	var l listing
	var behind bool
	pos, err := e.paged(k.name+" adoptions", args, 1, &l, func(fs *flag.FlagSet) { fs.BoolVar(&behind, "behind", false, "") })
	if err != nil {
		return err
	}
	q := url.Values{}
	if behind {
		q.Set("behind", "true")
	}
	var head *sourceHead
	var names map[string]string
	return listResolved(e, l, func(c *client.Client) (string, url.Values, error) {
		heads, _, err := readHeads(c, k)
		if err != nil {
			return "", nil, err
		}
		if head = libraryHead(heads, pos[0]); head == nil {
			return "", nil, ef("there is no shared %s %s; %s set --library makes one", k.name, client.ShellWord(pos[0]), k.name)
		}
		return "/" + k.plural + "/" + url.PathEscape(head.Id) + "/adoptions", q, nil
	}, nil, nil, func(c *client.Client, _ []apitypes.ClusterAdoptionBody) {
		names, _ = clusterNames(c)
	}, "CLUSTER\tADOPTED\tHEAD\tRELEASE\tMACHINES\tVIA", "", func(w *tabwriter.Writer, a *apitypes.ClusterAdoptionBody) {
		tf(w, "%s\t%s\t%s\t%s\t%d\t%s\n", named(names, a.Cluster), a.Revision, or(head.Revision, "removed"), or(a.Release, "-"), len(a.Machines), or(a.Via, "-"))
	})
}

// draftImpact lists, per fragment or profile entry of a draft, what it changes for this cluster's machines
// and which other clusters it leaves as they are (GET /drafts/{id}/impact). It renders nothing.
func (e *env) draftImpact(args []string) error {
	pos, err := e.parse("draft impact", args, 1, nil)
	if err != nil {
		return err
	}
	var own string
	var names map[string]string
	return showThen(e, "/drafts/"+url.PathEscape(pos[0])+"/impact", func(c *client.Client, _ *client.Response, _ *apitypes.ImpactBody) error {
		d, _, _, err := readDraft(c, pos[0])
		if err != nil {
			return err
		}
		names, _ = clusterNames(c)
		own = named(names, d.Cluster)
		return nil
	}, func(w *tabwriter.Writer, b *apitypes.ImpactBody) {
		if len(b.Items) == 0 {
			fmt.Fprintln(w, "The draft holds no fragment or profile entry.")
		}
		for _, it := range b.Items {
			var lines []string
			for _, change := range impactChanges(it.Machines) {
				if line := impactMachines(own, change, it.Machines); line != "" {
					lines = append(lines, line)
				}
			}
			var same, moved []string
			for _, o := range it.OtherClusters {
				text := named(names, o.Cluster) + " on " + o.Revision
				if o.Via != nil {
					text += " via profile " + *o.Via
				}
				if o.Unchanged {
					same = append(same, text)
				} else {
					moved = append(moved, text)
				}
			}
			if len(same) > 0 {
				lines = append(lines, "stays unchanged: "+strings.Join(same, ", "))
			}
			if len(moved) > 0 {
				lines = append(lines, "also changes: "+strings.Join(moved, ", "))
			}
			if len(lines) == 0 {
				lines = []string{"reaches no machine of " + own}
			}
			scope := string(it.Scope)
			if it.Scope == apitypes.ImpactItemScopeLibrary {
				scope = "shared"
			}
			for i, line := range lines {
				if i == 0 {
					tf(w, "%s %s\t%s\t%s\n", it.Kind, it.Name, scope, line)
				} else {
					tf(w, "\t\t%s\n", line)
				}
			}
		}
	})
}

// named is a cluster by its name when the names read hold one, else by its identifier.
func named(names map[string]string, id string) string {
	if names[id] != "" {
		return names[id]
	}
	return id
}

// impactChanges are the changes the machines of one entry hold: the three the API names, in the order a
// person reads them, then any other value as the server sent it. A change this client does not know is
// shown, never left out, so that an entry that reaches machines never reads as one that reaches none.
func impactChanges(ms []apitypes.ImpactMachine) []apitypes.ImpactMachineChange {
	changes := []apitypes.ImpactMachineChange{apitypes.ImpactMachineChangeGains, apitypes.ImpactMachineChangeLoses, apitypes.ImpactMachineChangeKeeps}
	for _, m := range ms {
		if !slices.Contains(changes, m.Change) {
			changes = append(changes, m.Change)
		}
	}
	return changes
}

// impactMachines is the line for the machines of one change: how many, the first, and the reasons; "" for none.
func impactMachines(cluster string, change apitypes.ImpactMachineChange, ms []apitypes.ImpactMachine) string {
	var ids, reasons []string
	for _, m := range ms {
		if m.Change == change {
			ids = append(ids, m.Machine)
			if r := string(m.Reason); !slices.Contains(reasons, r) {
				reasons = append(reasons, r)
			}
		}
	}
	if len(ids) == 0 {
		return ""
	}
	more := ""
	if len(ids) > 1 {
		more = sf(" and %d more", len(ids)-1)
	}
	return sf("%s in %s: %s (%s%s; %s)", change, cluster, machineCount(len(ids)), ids[0], more, strings.Join(reasons, ", "))
}
