package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/ginsys/bronzeward/internal/id"
)

// The reads of what a shared fragment or profile name reaches (persistence-api.md §3.1.1
// "Reach", §9.2). They read the source graph in one snapshot and render nothing; what composes
// where is the publication snapshot's own reading of the published state.

// clusterAdoptionBody is one cluster's use of a shared name: the revision it compiles, with its
// adoption counter and the release that set it, or via, the adopted shared profile whose pin
// brings a fragment at a revision the cluster has no adoption of. Machines are those whose
// composition holds that revision, through that profile for via.
type clusterAdoptionBody struct {
	Cluster          string   `json:"cluster"`
	Revision         string   `json:"revision"`
	AdoptionRevision *int     `json:"adoptionRevision"`
	Behind           bool     `json:"behind"`
	Release          *string  `json:"release"`
	Via              *string  `json:"via"`
	Machines         []string `json:"machines"`
}

// selectClusterAdoptions pages the uses of the shared head $3, in the order of cluster, via and
// revision ($1 the last one listed); $4 keeps only those behind the head. A use is a cluster's
// adoption that holds a revision, and, for a fragment, each pin of it in a shared profile revision
// a cluster adopts, unless that cluster adopts the pinned revision itself. The release that set an
// adoption is the one at its counter whose draft held the entry that set it: a release that only
// holds the adoption has none.
const selectClusterAdoptions = `WITH direct AS (
		SELECT cluster, coalesce(fragment_revision, profile_revision) AS revision, adoption_revision
		FROM library_adoption
		WHERE coalesce(fragment, profile) = $3 AND coalesce(fragment_revision, profile_revision) IS NOT NULL),
	uses AS (
		SELECT cluster, '' AS via, revision, adoption_revision FROM direct
		UNION ALL
		SELECT DISTINCT a.cluster, a.name::text, p.fragment_revision, NULL::integer
		FROM library_adoption a
		JOIN profile_revision_fragment p ON p.revision = a.profile_revision
		JOIN fragment_revision r ON r.id = p.fragment_revision
		JOIN fragment h ON h.owner = 'library' AND h.name = r.name AND h.id = $3
		WHERE NOT EXISTS (SELECT FROM direct d WHERE d.cluster = a.cluster AND d.revision = p.fragment_revision)),
	listed AS (
		SELECT u.*, (u.cluster || ' ' || u.via || ' ' || u.revision) COLLATE "C" AS key,
			u.revision IS DISTINCT FROM (SELECT head_revision_id FROM fragment WHERE id = $3
				UNION ALL SELECT head_revision_id FROM profile WHERE id = $3) AS behind
		FROM uses u)
	SELECT l.key, l.cluster, l.via, l.revision, l.adoption_revision, l.behind,
		(SELECT s.release FROM release_source s
			JOIN release rel ON rel.id = s.release
			JOIN draft_source_entry e ON e.draft = rel.draft AND e.kind = s.kind AND e.name = s.name AND e.scope = 'library'
				AND (e.adoption OR coalesce(e.fragment_revision, e.profile_revision) IS NOT NULL)
			WHERE l.via = '' AND s.cluster = l.cluster AND coalesce(s.fragment, s.profile) = $3
				AND s.adoption_revision = l.adoption_revision)
	FROM listed l WHERE l.key > $1 AND (l.behind OR NOT $4) ORDER BY l.key LIMIT $2`

// adoptionsRead answers GET /fragments/{id}/adoptions, or /profiles/{id}/adoptions for kind
// "profile": the uses of the shared name whose head {id} names. A cluster-scope head is 404.
func adoptionsRead(kind string) readFunc {
	head, revision := headKinds[kind], id.FragmentRevision
	prefix := id.Fragment
	if kind == "profile" {
		prefix, revision = id.Profile, id.ProfileRevision
	}
	cursor := func(last string) bool {
		k := strings.Split(last, " ")
		return len(k) == 3 && id.MustHave(k[0], id.Cluster) == nil && id.MustHave(k[2], revision) == nil
	}
	return func(a *API, w http.ResponseWriter, q *request) {
		v := q.r.PathValue("id")
		readInOn(readSnapshot, a, w, q, func(ctx context.Context, tx *sql.Tx) (string, any, error) {
			limit, after, ref := pageBy(q, cursor)
			if ref != nil {
				return "", nil, ref
			}
			query := q.r.URL.Query()
			if query.Has("behind") && query.Get("behind") != "true" {
				return "", nil, refuse(http.StatusBadRequest, "invalid-request", "behind is true or absent")
			}
			notFound := refuse(http.StatusNotFound, "not-found", "no such shared resource")
			if id.MustHave(v, prefix) != nil {
				return "", nil, notFound
			}
			var name string
			err := tx.QueryRowContext(ctx, `SELECT name FROM `+head.table+` WHERE id = $1 AND scope = 'library'`, v).Scan(&name)
			if errors.Is(err, sql.ErrNoRows) {
				return "", nil, notFound
			}
			if err != nil {
				return "", nil, err
			}
			out, err := pageRows(ctx, tx, q, limit, after, selectClusterAdoptions,
				func(r *sql.Rows) (*clusterAdoptionBody, string, error) {
					b := &clusterAdoptionBody{Machines: []string{}}
					var key, via string
					err := r.Scan(&key, &b.Cluster, &via, &b.Revision, &b.AdoptionRevision, &b.Behind, &b.Release)
					if via != "" {
						b.Via = &via
					}
					return b, key, err
				}, v, query.Has("behind"))
			if err != nil {
				return "", nil, err
			}
			published := map[string]composition{}
			for _, b := range out.Items {
				c, ok := published[b.Cluster]
				if !ok {
					if c, err = readComposition(ctx, tx, publishJob{cluster: b.Cluster}); err != nil {
						return "", nil, err
					}
					published[b.Cluster] = c
				}
				for _, m := range c.machines {
					if c.holds(m, kind, name, b.Revision, b.Via) {
						b.Machines = append(b.Machines, m.machine)
					}
				}
			}
			return "", out, nil
		})
	}
}

// composition is what a cluster's sources compose, as the publication snapshot reads them after
// the draft j names, or as published where it names none: the machines a publication would cover,
// those with an import base (§3.2, choice §17.33), each with its assignment, and their selections.
type composition struct {
	reader   *snapshotReader
	machines []snapshotMachine
	sel      selections
}

func readComposition(ctx context.Context, tx *sql.Tx, j publishJob) (composition, error) {
	c := composition{reader: &snapshotReader{ctx: ctx, tx: tx, j: j}}
	if err := c.reader.readStates(); err != nil {
		return c, err
	}
	all, err := c.reader.clusterMachines()
	if err != nil {
		return c, err
	}
	covered := slices.DeleteFunc(all, func(m snapshotMachine) bool { return m.importBase == "" })
	return c, c.load(covered)
}

// publishedComposition is the cluster's published composition of the machines given, whether or
// not their published state covers them.
func publishedComposition(ctx context.Context, tx *sql.Tx, cluster string, machines []snapshotMachine) (composition, error) {
	c := composition{reader: &snapshotReader{ctx: ctx, tx: tx, j: publishJob{cluster: cluster}}}
	if err := c.reader.readStates(); err != nil {
		return c, err
	}
	return c, c.load(slices.Clone(machines))
}

// load makes machines the composition's, each with the assignment its view gives it, and reads
// their selections.
func (c *composition) load(machines []snapshotMachine) error {
	for i := range machines {
		machines[i].assignment = c.reader.states["assignment"][machines[i].machine].revision
	}
	c.machines = machines
	var err error
	c.sel, _, err = c.reader.loadSelections(machines)
	return err
}

// holds reports whether m composes revision of the kind's name: for a profile, whether its
// assignment selects the name and the view gives it that revision; for a fragment, whether any
// route composes the revision, or, with via, the pins of the profile via names.
func (c composition) holds(m snapshotMachine, kind, name, revision string, via *string) bool {
	if kind == "profile" {
		return slices.Contains(c.sel.profiles[m.assignment], name) && c.reader.states["profile"][name].revision == revision
	}
	var held bool
	c.sel.walk(c.reader, m.assignment, func(frv, profile, _ string) {
		held = held || frv == revision && (via == nil || profile == *via)
	})
	return held
}
