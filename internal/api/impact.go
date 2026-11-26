package api

import (
	"context"
	"database/sql"
	"math"
	"net/http"
	"slices"

	"github.com/ginsys/bronzeward/internal/id"
)

// GET /drafts/{id}/impact (persistence-api.md §3.1.1 "Reach, before publishing", §9.2): what each
// fragment and profile entry of an open draft changes in the draft's cluster, and the other
// clusters that use a shared name, which the draft leaves unchanged. It compares the published
// composition with the one a publication of the draft would read, and renders nothing.

type impactBody struct {
	Items []impactItem `json:"items"`
}

type impactItem struct {
	Kind          string          `json:"kind"`
	Name          string          `json:"name"`
	Scope         string          `json:"scope"`
	Machines      []impactMachine `json:"machines"`
	OtherClusters []impactCluster `json:"otherClusters"`
}

// impactMachine is a machine whose composition holds the name at the item's scope before or after
// the draft.
type impactMachine struct {
	Machine string `json:"machine"`
	Reason  string `json:"reason"`
	Change  string `json:"change"`
}

type impactCluster struct {
	Cluster   string  `json:"cluster"`
	Revision  string  `json:"revision"`
	Via       *string `json:"via"`
	Unchanged bool    `json:"unchanged"`
}

var getImpact = itemOn(readSnapshot, id.Draft, func(ctx context.Context, tx *sql.Tx, draft string) (string, any, error) {
	var state, cluster string
	if err := tx.QueryRowContext(ctx, `SELECT state, cluster FROM draft WHERE id = $1`, draft).Scan(&state, &cluster); err != nil {
		return "", nil, err
	}
	if state != "open" {
		return "", nil, refuse(http.StatusConflict, "conflict", "the draft is no longer open").with("draft", draft)
	}
	after, err := readComposition(ctx, tx, publishJob{draft: draft, cluster: cluster})
	if err != nil {
		return "", nil, err
	}
	// The published composition of the same machines, those the draft's publication would cover.
	before, err := publishedComposition(ctx, tx, cluster, after.machines)
	if err != nil {
		return "", nil, err
	}

	out := impactBody{Items: []impactItem{}}
	err = after.reader.query(`SELECT kind, name, scope FROM draft_source_entry WHERE draft = $1 AND kind <> 'assignment'
		ORDER BY kind, name, scope`, func(row *sql.Rows) error {
		it := impactItem{Machines: []impactMachine{}, OtherClusters: []impactCluster{}}
		err := row.Scan(&it.Kind, &it.Name, &it.Scope)
		out.Items = append(out.Items, it)
		return err
	}, draft)
	if err != nil {
		return "", nil, err
	}
	for i := range out.Items {
		it := &out.Items[i]
		for j, m := range after.machines {
			was, wasBy := before.uses(before.machines[j], it.Kind, it.Name, it.Scope)
			is, isBy := after.uses(m, it.Kind, it.Name, it.Scope)
			x := impactMachine{Machine: m.machine, Reason: isBy, Change: "keeps"}
			switch {
			case len(was) == 0 && len(is) == 0:
				continue
			case len(is) == 0:
				x.Reason, x.Change = wasBy, "loses"
			case slices.ContainsFunc(is, func(r string) bool { return !slices.Contains(was, r) }):
				x.Change = "gains"
			}
			it.Machines = append(it.Machines, x)
		}
		if it.Scope == scopeLibrary {
			if it.OtherClusters, err = otherClusters(ctx, tx, it.Kind, it.Name, cluster); err != nil {
				return "", nil, err
			}
		}
	}
	return "", out, nil
})

// reasons are why a machine's composition holds a name, the first that applies first: its
// assignment selects the name, a cluster profile it selects pins it, or only a shared profile's
// pin brings it.
var reasons = []string{"assignment", "profile", "adoption"}

// uses is the revisions of the kind's name at scope that m composes, and why, "" for none.
func (c composition) uses(m snapshotMachine, kind, name, scope string) ([]string, string) {
	var out []string
	by := len(reasons)
	add := func(revision string, reason int) {
		if revision != "" && c.sel.shared[revision] == (scope == scopeLibrary) {
			out, by = append(out, revision), min(by, reason)
		}
	}
	if kind == "profile" {
		if slices.Contains(c.sel.profiles[m.assignment], name) {
			add(c.reader.states["profile"][name].revision, 0)
		}
	} else {
		c.sel.walk(c.reader, m.assignment, func(frv, profile, selected string) {
			switch {
			case c.sel.revisions[frv][0] != name:
			case selected != "":
				add(frv, 0)
			case c.sel.shared[c.reader.states["profile"][profile].revision]:
				add(frv, 2)
			default:
				add(frv, 1)
			}
		})
	}
	if len(out) == 0 {
		return nil, ""
	}
	return out, reasons[by]
}

// otherClusters is each cluster but own that uses the shared name of the kind, as the adoptions
// read lists it; none where the library has no head of the name yet.
func otherClusters(ctx context.Context, tx *sql.Tx, kind, name, own string) ([]impactCluster, error) {
	out := []impactCluster{}
	var head string
	err := tx.QueryRowContext(ctx, `SELECT coalesce((SELECT id FROM `+headKinds[kind].table+`
		WHERE scope = 'library' AND name = $1), '')`, name).Scan(&head)
	if err != nil || head == "" {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, selectClusterAdoptions, "", math.MaxInt32, head, false)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var b clusterAdoptionBody
		var key, via string
		if err := rows.Scan(&key, &b.Cluster, &via, &b.Revision, &b.AdoptionRevision, &b.Behind, &b.Release); err != nil {
			return out, err
		}
		if b.Cluster == own {
			continue
		}
		x := impactCluster{Cluster: b.Cluster, Revision: b.Revision, Unchanged: true}
		if via != "" {
			x.Via = &via
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
