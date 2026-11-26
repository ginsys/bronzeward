package api

import (
	"maps"
	"net/http"
	"slices"
	"strconv"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// The reads of what a shared change reaches (persistence-api.md §3.1.1, §9.2). Where a case says a
// read equals what a publication compiles, it compares the read with the snapshot of a
// publication of each cluster, never with the read's own query.

// clusterEnv is a cluster and one machine of it.
type clusterEnv struct{ cluster, machine string }

// fleet is a draft env with two clusters of one machine each.
type fleet struct {
	*draftEnv
	a, b   clusterEnv
	drafts int
}

func newFleet(t *testing.T) *fleet {
	d := newDraftEnv(t)
	other := d.createCluster(d.api, d.human("h-author"), "k-cluster-other-012345")
	rec := d.do(d.api, machineCall(d.human("h-author"), "k-machine-other-012345", other, "1b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b"))
	return &fleet{draftEnv: d, a: clusterEnv{d.cluster, d.machine},
		b: clusterEnv{other, decode[machineBody](t, rec, http.StatusCreated).ID}}
}

// next is a publication of a new draft of c, covering its machine on a base of its own. Its
// assignment is the cluster's published one until selects replaces it.
func (f *fleet) next(c clusterEnv) *publishEnv {
	f.t.Helper()
	f.drafts++
	n := strconv.Itoa(f.drafts)
	p := bare(f.draftEnv, c.cluster, c.machine, "k-draft-fleet-01234567-"+n, "bw-artifact-"+n)
	var asr string
	if err := f.db.QueryRow(`SELECT coalesce((SELECT head_revision_id FROM assignment WHERE machine = $1), '')`,
		c.machine).Scan(&asr); err != nil {
		f.t.Fatal(err)
	}
	p.unit.machines[0].assignment = asr
	return p
}

// selects makes p's draft set its machine's first assignment: the profiles and "layer/name"
// fragments given.
func (p *publishEnv) selects(profiles []string, fragments ...string) {
	p.t.Helper()
	asr := p.assignmentRevision(profiles, fragments...)
	p.entry("assignment", "", asr)
	p.unit.machines[0].assignment = asr
}

// applied records rel as the Applied release of c's machine, so the published state covers it.
func (f *fleet) applied(rel string, c clusterEnv) {
	f.t.Helper()
	mustExec(f.t, f.db, `UPDATE machine_state SET applied_release = $2, applied_digest = $3, applied_source = 'operation',
		baseline_revision = 1 WHERE machine = $1`, c.machine, rel, make([]byte, 32))
}

// compiledBy is the fragment revisions a publication of c, of a draft that changes nothing, would
// compose into its machine.
func (f *fleet) compiledBy(c clusterEnv) []string {
	f.t.Helper()
	s := f.next(c).compiled()
	if len(s.machines) != 1 || s.machines[0].machine != c.machine {
		f.t.Fatalf("a publication of %s covers %+v; want its machine %s", c.cluster, s.machines, c.machine)
	}
	return s.machines[0].fragments
}

// adoptionsOf reads every item of GET /{kinds}/{head}/adoptions with query, page by page.
func (d *draftEnv) adoptionsOf(kinds, head, query string) []map[string]any {
	d.t.Helper()
	var out []map[string]any
	cursor := ""
	for range 20 {
		q := "?" + query
		if cursor != "" {
			q += "&cursor=" + cursor
		}
		b := decode[map[string]any](d.t, d.do(d.api, call{method: "GET", path: prefix + "/" + kinds + "/" + head + "/adoptions" + q,
			token: d.human("h-viewer")}), http.StatusOK)
		for _, it := range b["items"].([]any) {
			out = append(out, it.(map[string]any))
		}
		next, ok := b["next"].(string)
		if !ok {
			return out
		}
		cursor = next
	}
	d.t.Fatal("the pages do not end")
	return nil
}

// reach is one adoptions item as a case wants it: "" is null, counter 0 is null.
type reach struct {
	cluster, revision, via, release string
	counter                         int
	behind                          bool
	machines                        []string
}

func (d *draftEnv) wantReach(got []map[string]any, want ...reach) {
	d.t.Helper()
	slices.SortStableFunc(want, func(x, y reach) int {
		return slices.Compare([]string{x.cluster, x.via, x.revision}, []string{y.cluster, y.via, y.revision})
	})
	if len(got) != len(want) {
		d.t.Fatalf("%d items %v; want %d %+v", len(got), got, len(want), want)
	}
	orNull := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	for i, w := range want {
		var counter any
		if w.counter != 0 {
			counter = float64(w.counter)
		}
		for name, v := range map[string]any{"cluster": w.cluster, "revision": w.revision, "via": orNull(w.via),
			"release": orNull(w.release), "adoptionRevision": counter, "behind": w.behind} {
			wantMember(d.t, got[i], name, v)
		}
		machines := []string{}
		for _, m := range got[i]["machines"].([]any) {
			machines = append(machines, m.(string))
		}
		slices.Sort(machines)
		if !slices.Equal(machines, append([]string{}, w.machines...)) {
			d.t.Fatalf("item %d %v: machines %v; want %v", i, got[i], machines, w.machines)
		}
	}
}

// wantCompiled holds items to what a publication of each cluster compiles: of the shared
// revisions of the name, a machine composes exactly those whose item names it.
func (f *fleet) wantCompiled(items []map[string]any, revisions []string, clusters ...clusterEnv) {
	f.t.Helper()
	for _, c := range clusters {
		listed := map[string]bool{}
		for _, it := range items {
			if it["cluster"] == c.cluster && slices.Contains(it["machines"].([]any), any(c.machine)) {
				listed[it["revision"].(string)] = true
			}
		}
		composed := map[string]bool{}
		for _, frv := range f.compiledBy(c) {
			if slices.Contains(revisions, frv) {
				composed[frv] = true
			}
		}
		if !maps.Equal(listed, composed) {
			f.t.Fatalf("cluster %s: the read lists %v; a publication compiles %v", c.cluster, listed, composed)
		}
	}
}

// PA §9.2: a release's source carries scope, previous and adoptionRevision; the last two are null
// on a cluster source and on a fragment only a shared profile revision pins.
func TestReleaseSourceAdoptionMembers(t *testing.T) {
	type members struct {
		revision, previous string
		counter            int
	}
	for name, c := range map[string]struct {
		counter int    // the published adoption's, 0 for none
		stage   string // "", "move", "remove" or "pin"
		want    members
		pinned  bool // a second source of the head: the pin-only one
	}{
		"a first adoption":           {stage: "move", want: members{revision: "newer", counter: 1}},
		"an adoption held unchanged": {counter: 4, want: members{revision: "older", previous: "older", counter: 4}},
		"a moved adoption":           {counter: 4, stage: "move", want: members{revision: "newer", previous: "older", counter: 5}},
		"a removed adoption":         {counter: 4, stage: "remove", want: members{previous: "older", counter: 5}},
		"beside a pin-only source":   {counter: 4, stage: "pin", want: members{revision: "older", previous: "older", counter: 4}, pinned: true},
	} {
		t.Run(name, func(t *testing.T) {
			p := newPublishEnv(t)
			f := p.sharedFragment("ntp")
			revisions := map[string]any{"older": f.older, "newer": f.newer, "": nil}
			var base any
			if c.counter != 0 {
				base = c.counter
				p.adopt("fragment", "ntp", f.head, f.older, c.counter)
			}
			switch c.stage {
			case "move":
				p.adoptionEntry("fragment", "ntp", f.newer, base)
				p.selectToo(nil, "cluster/ntp")
			case "remove":
				p.adoptionEntry("fragment", "ntp", nil, base)
			case "pin":
				p.share("profile", "shared-set", p.sharedProfileRevision("shared-set", f.newer), nil, nil)
				p.selectToo(nil, "cluster/ntp")
			default:
				p.selectToo(nil, "cluster/ntp")
			}
			rel := p.publishedEntries()
			b := decode[map[string]any](t, p.do(p.api, call{method: "GET", path: prefix + "/releases/" + rel,
				token: p.human("h-viewer")}), http.StatusOK)
			var shared, own int
			for _, s := range b["sources"].([]any) {
				m := s.(map[string]any)
				if m["head"] != f.head {
					if m["kind"] == "profile" && m["scope"] == "library" {
						continue
					}
					wantMember(t, m, "scope", "cluster")
					wantMember(t, m, "previous", nil)
					wantMember(t, m, "adoptionRevision", nil)
					own++
					continue
				}
				shared++
				wantMember(t, m, "scope", "library")
				wantMember(t, m, "headRevision", nil)
				if c.pinned && m["revision"] == f.newer {
					wantMember(t, m, "previous", nil)
					wantMember(t, m, "adoptionRevision", nil)
					continue
				}
				wantMember(t, m, "revision", revisions[c.want.revision])
				wantMember(t, m, "previous", revisions[c.want.previous])
				wantMember(t, m, "adoptionRevision", float64(c.want.counter))
			}
			if want := 1 + map[bool]int{true: 1}[c.pinned]; shared != want || own == 0 {
				t.Fatalf("%d sources of the shared head and %d of the cluster; want %d and some", shared, own, want)
			}
		})
	}
}

// PA §3.1.1 acceptance check 2: after publication, adoptions lists every cluster on the name with
// its revision and which are behind, and equals what a publication of each would compile; the
// release is the one that set the adoption.
func TestLibraryAdoptionsRead(t *testing.T) {
	fl := newFleet(t)
	seed := &publishEnv{draftEnv: fl.draftEnv}
	f := seed.sharedFragment("ntp")
	publish := func(c clusterEnv, revision string, base any) (*publishEnv, string) {
		p := fl.next(c)
		p.adoptionEntry("fragment", "ntp", revision, base)
		if base == nil {
			p.selects(nil, "cluster/ntp")
		}
		rel := p.publishedEntries()
		fl.applied(rel, c)
		return p, rel
	}
	_, relA := publish(fl.a, f.older, nil)
	_, relB := publish(fl.b, f.newer, nil)
	a := reach{cluster: fl.a.cluster, revision: f.older, release: relA, counter: 1, behind: true, machines: []string{fl.a.machine}}
	b := reach{cluster: fl.b.cluster, revision: f.newer, release: relB, counter: 1, machines: []string{fl.b.machine}}

	items := fl.adoptionsOf("fragments", f.head, "")
	fl.wantReach(items, a, b)
	fl.wantReach(fl.adoptionsOf("fragments", f.head, "limit=1"), a, b)
	fl.wantReach(fl.adoptionsOf("fragments", f.head, "behind=true"), a)
	fl.wantCompiled(items, []string{f.older, f.newer}, fl.a, fl.b)

	// A release that holds the adoption unchanged set nothing.
	held := fl.next(fl.a)
	fl.applied(held.publishedEntries(), fl.a)
	fl.wantReach(fl.adoptionsOf("fragments", f.head, ""), a, b)

	// Adopting the same revision again sets the adoption anew.
	_, again := publish(fl.a, f.older, 1)
	a.release, a.counter = again, 2
	fl.wantReach(fl.adoptionsOf("fragments", f.head, ""), a, b)

	// A machine whose assignment selects the name but has no import base is covered by no
	// publication, so it is listed nowhere.
	rec := fl.do(fl.api, machineCall(fl.human("h-author"), "k-machine-nobase-01234", fl.a.cluster,
		"2c6b7d2f-3e4a-4f60-9b0c-1d2e3f4a5b6c"))
	nobase := decode[machineBody](t, rec, http.StatusCreated).ID
	other := &publishEnv{draftEnv: &draftEnv{env: fl.env, cluster: fl.a.cluster, machine: nobase, seed: fl.seed}}
	mustExec(t, fl.db, `INSERT INTO assignment (id, cluster, machine, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, $2, $3, $4, 1, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, id.New(id.Assignment), fl.a.cluster, nobase,
		other.assignmentRevision(nil, "cluster/ntp"))
	fl.wantReach(fl.adoptionsOf("fragments", f.head, ""), a, b)

	// A removed adoption is not listed, and a shared head removed leaves every adoption behind.
	mustExec(t, fl.db, `UPDATE library_adoption SET fragment_revision = NULL, adoption_revision = 2 WHERE cluster = $1`,
		fl.b.cluster)
	fl.wantReach(fl.adoptionsOf("fragments", f.head, ""), a)
	mustExec(t, fl.db, `UPDATE fragment SET head_revision_id = NULL, head_revision = 3 WHERE id = $1`, f.head)
	fl.wantReach(fl.adoptionsOf("fragments", f.head, "behind=true"), a)

	// An adoption no publication wrote names no release.
	g := seed.sharedFragment("dns")
	seed.adopt("fragment", "dns", g.head, g.newer, 1)
	fl.wantReach(fl.adoptionsOf("fragments", g.head, ""),
		reach{cluster: fl.a.cluster, revision: g.newer, counter: 1, machines: []string{}})
}

// PA §3.1.1: a cluster that adopted a shared profile revision pinning a fragment is listed for the
// fragment, via that profile; beside an adoption of another revision both are listed, each with
// the machines that compose its revision.
func TestLibraryAdoptionsVia(t *testing.T) {
	fl := newFleet(t)
	seed := &publishEnv{draftEnv: fl.draftEnv}
	f := seed.sharedFragment("ntp")
	prv := seed.sharedProfileRevision("shared-set", f.older)
	prf := id.New(id.Profile)
	mustExec(t, fl.db, `INSERT INTO profile (id, cluster, scope, name, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, NULL, 'library', 'shared-set', $2, 1, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, prf, prv)

	// A adopts the profile alone and selects it.
	pa := fl.next(fl.a)
	pa.adoptionEntry("profile", "shared-set", prv, nil)
	pa.selects([]string{"shared-set"})
	relA := pa.publishedEntries()
	fl.applied(relA, fl.a)
	// B adopts the fragment's newer revision and the profile, and selects the profile alone.
	pb := fl.next(fl.b)
	pb.adoptionEntry("fragment", "ntp", f.newer, nil)
	pb.adoptionEntry("profile", "shared-set", prv, nil)
	pb.selects([]string{"shared-set"})
	relB := pb.publishedEntries()
	fl.applied(relB, fl.b)

	viaA := reach{cluster: fl.a.cluster, revision: f.older, via: "shared-set", behind: true, machines: []string{fl.a.machine}}
	viaB := reach{cluster: fl.b.cluster, revision: f.older, via: "shared-set", behind: true, machines: []string{fl.b.machine}}
	directB := reach{cluster: fl.b.cluster, revision: f.newer, release: relB, counter: 1, machines: []string{}}
	items := fl.adoptionsOf("fragments", f.head, "")
	fl.wantReach(items, viaA, viaB, directB)
	fl.wantReach(fl.adoptionsOf("fragments", f.head, "limit=1"), viaA, viaB, directB)
	fl.wantReach(fl.adoptionsOf("fragments", f.head, "behind=true&limit=1"), viaA, viaB)
	fl.wantCompiled(items, []string{f.older, f.newer}, fl.a, fl.b)

	// The profile's own list: both adopt it, no via.
	fl.wantReach(fl.adoptionsOf("profiles", prf, ""),
		reach{cluster: fl.a.cluster, revision: prv, release: relA, counter: 1, machines: []string{fl.a.machine}},
		reach{cluster: fl.b.cluster, revision: prv, release: relB, counter: 1, machines: []string{fl.b.machine}})

	// An adoption of the pinned revision itself is the one item (one revision composes once).
	mustExec(t, fl.db, `UPDATE library_adoption SET fragment_revision = $2 WHERE cluster = $1 AND kind = 'fragment'`,
		fl.b.cluster, f.older)
	directB.revision, directB.behind, directB.machines = f.older, true, []string{fl.b.machine}
	// The release that set counter 1 is still the one recorded for it.
	fl.wantReach(fl.adoptionsOf("fragments", f.head, ""), viaA, directB)
}

// PA §9.2: adoptions is of a shared name: 404 for a cluster-scope source or another kind's id;
// behind takes only true.
func TestLibraryAdoptionsRefusals(t *testing.T) {
	p := newPublishEnv(t)
	f := p.sharedFragment("ntp")
	prf, _ := p.adoptedSharedProfile("shared-set", f.newer)
	read := func(path string) {
		t.Helper()
		wantProblem(t, p.do(p.api, call{method: "GET", path: prefix + path, token: p.human("h-viewer")}),
			http.StatusNotFound, "not-found")
	}
	for _, path := range []string{"/fragments/" + p.network + "/adoptions", "/profiles/" + p.prf + "/adoptions",
		"/fragments/" + id.New(id.Fragment) + "/adoptions", "/fragments/" + prf + "/adoptions",
		"/profiles/" + f.head + "/adoptions", "/fragments/ntp/adoptions"} {
		read(path)
	}
	for _, q := range []string{"behind=false", "behind=", "behind=true&behind=true", "cluster=" + p.cluster, "limit=0"} {
		wantProblem(t, p.do(p.api, call{method: "GET", path: prefix + "/fragments/" + f.head + "/adoptions?" + q,
			token: p.human("h-viewer")}), http.StatusBadRequest, "invalid-request")
	}
	wantProblem(t, p.do(p.api, call{method: "GET", path: prefix + "/fragments/" + f.head + "/adoptions?cursor=bm90",
		token: p.human("h-viewer")}), http.StatusBadRequest, "cursor-invalid")
}

// reached is one machine of an impact item as a case wants it.
type reached struct{ machine, reason, change string }

// reachedItem is one impact item as a case wants it.
type reachedItem struct {
	kind, name, scope string
	machines          []reached
	others            []elsewhere
}

// elsewhere is one other cluster of an impact item: via "" is null.
type elsewhere struct{ cluster, revision, via string }

// impactOf reads GET /drafts/{id}/impact.
func (d *draftEnv) impactOf(draft string) []reachedItem {
	d.t.Helper()
	b := decode[map[string]any](d.t, d.do(d.api, call{method: "GET", path: prefix + "/drafts/" + draft + "/impact",
		token: d.human("h-viewer")}), http.StatusOK)
	var out []reachedItem
	for _, v := range b["items"].([]any) {
		m := v.(map[string]any)
		it := reachedItem{kind: m["kind"].(string), name: m["name"].(string), scope: m["scope"].(string)}
		for _, x := range m["machines"].([]any) {
			r := x.(map[string]any)
			it.machines = append(it.machines, reached{r["machine"].(string), r["reason"].(string), r["change"].(string)})
		}
		for _, x := range m["otherClusters"].([]any) {
			r := x.(map[string]any)
			wantMember(d.t, r, "unchanged", true)
			via, _ := r["via"].(string)
			it.others = append(it.others, elsewhere{r["cluster"].(string), r["revision"].(string), via})
		}
		out = append(out, it)
	}
	return out
}

func (d *draftEnv) wantImpact(draft string, want ...reachedItem) {
	d.t.Helper()
	got := d.impactOf(draft)
	for _, items := range [][]reachedItem{got, want} {
		for _, it := range items {
			slices.SortFunc(it.others, func(x, y elsewhere) int {
				return slices.Compare([]string{x.cluster, x.via, x.revision}, []string{y.cluster, y.via, y.revision})
			})
		}
	}
	if !slices.EqualFunc(got, want, func(x, y reachedItem) bool {
		return x.kind == y.kind && x.name == y.name && x.scope == y.scope && slices.Equal(x.machines, y.machines) &&
			slices.Equal(x.others, y.others)
	}) {
		d.t.Fatalf("impact %+v; want %+v", got, want)
	}
}

// impactFleet is two clusters whose published state is applied: A adopts the older revision of the
// shared fragment ntp and selects it, B adopts the newer and selects it.
type impactFleet struct {
	*fleet
	ntp sharedFragment
}

func newImpactFleet(t *testing.T) *impactFleet {
	fl := newFleet(t)
	f := (&publishEnv{draftEnv: fl.draftEnv}).sharedFragment("ntp")
	for _, c := range []struct {
		c        clusterEnv
		revision string
	}{{fl.a, f.older}, {fl.b, f.newer}} {
		p := fl.next(c.c)
		p.adoptionEntry("fragment", "ntp", c.revision, nil)
		p.selects(nil, "cluster/ntp")
		fl.applied(p.publishedEntries(), c.c)
	}
	return &impactFleet{fleet: fl, ntp: f}
}

func (f *impactFleet) onB() []elsewhere { return []elsewhere{{f.b.cluster, f.ntp.newer, ""}} }

// compiledNames holds p's draft's impact to what its publication would compile: a machine gains or
// keeps a fragment item exactly where the snapshot composes a revision of the name at that scope.
func (f *impactFleet) compiledNames(p *publishEnv) {
	f.t.Helper()
	s := p.compiled()
	for _, it := range f.impactOf(p.draft) {
		if it.kind != "fragment" {
			continue
		}
		listed := false
		for _, m := range it.machines {
			listed = listed || m.change != "loses"
		}
		var composed int
		for _, frv := range s.machines[0].fragments {
			composed += count(f.t, f.db, `SELECT count(*) FROM fragment_revision WHERE id = $1 AND name = $2
				AND (cluster IS NULL) = $3`, frv, it.name, it.scope == "library")
		}
		if listed != (composed > 0) {
			f.t.Fatalf("%+v: the publication composes %d revisions of the name at that scope", it, composed)
		}
	}
}

// PA §3.1.1 "Reach, before publishing", acceptance check 2: impact lists this cluster's machines
// that gain or lose each changed source, and the other clusters as unchanged on their revision.
func TestLibraryImpact(t *testing.T) {
	library := func(f *impactFleet, machines ...reached) reachedItem {
		return reachedItem{"fragment", "ntp", "library", machines, f.onB()}
	}
	for name, c := range map[string]struct {
		stage func(f *impactFleet, p *publishEnv)
		want  func(f *impactFleet) []reachedItem
	}{
		"an edit": {
			func(f *impactFleet, p *publishEnv) { p.share("fragment", "ntp", p.sharedRevision("ntp"), 2, 1) },
			func(f *impactFleet) []reachedItem {
				return []reachedItem{library(f, reached{f.a.machine, "assignment", "gains"})}
			},
		},
		"an adoption": {
			func(f *impactFleet, p *publishEnv) { p.adoptionEntry("fragment", "ntp", f.ntp.newer, 1) },
			func(f *impactFleet) []reachedItem {
				return []reachedItem{library(f, reached{f.a.machine, "assignment", "gains"})}
			},
		},
		"an adoption removal": {
			func(f *impactFleet, p *publishEnv) {
				p.adoptionEntry("fragment", "ntp", nil, 1)
				p.selects(nil)
			},
			func(f *impactFleet) []reachedItem {
				return []reachedItem{library(f, reached{f.a.machine, "assignment", "loses"})}
			},
		},
		"a shared head removal moves no adoption": {
			func(f *impactFleet, p *publishEnv) { p.share("fragment", "ntp", nil, 2, nil) },
			func(f *impactFleet) []reachedItem {
				return []reachedItem{library(f, reached{f.a.machine, "assignment", "keeps"})}
			},
		},
		"a detachment": {
			func(f *impactFleet, p *publishEnv) {
				// detachedEntry clears every entry of the name, so the removal is staged after it.
				p.detachedEntry("fragment", "ntp", p.fragmentRevision(p.cluster, "ntp", "cluster"))
				p.adoptionEntry("fragment", "ntp", nil, 1)
			},
			func(f *impactFleet) []reachedItem {
				return []reachedItem{
					{"fragment", "ntp", "cluster", []reached{{f.a.machine, "assignment", "gains"}}, nil},
					library(f, reached{f.a.machine, "assignment", "loses"}),
				}
			},
		},
		"a source no machine composes": {
			func(f *impactFleet, p *publishEnv) {
				p.entry("fragment", "dns", p.fragmentRevision(p.cluster, "dns", "cluster"))
			},
			func(f *impactFleet) []reachedItem {
				return []reachedItem{{"fragment", "dns", "cluster", nil, nil}}
			},
		},
		"an assignment alone": {
			func(f *impactFleet, p *publishEnv) { p.selects(nil) },
			func(f *impactFleet) []reachedItem { return nil },
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newImpactFleet(t)
			p := f.next(f.a)
			c.stage(f, p)
			f.wantImpact(p.draft, c.want(f)...)
			f.compiledNames(p)
		})
	}
}

// PA §9.2: the reason is the machine's assignment selecting the name, else a cluster profile
// pinning it, else only a shared profile's pin; another cluster that pins the fragment through an
// adopted shared profile is listed via it.
func TestLibraryImpactThroughProfiles(t *testing.T) {
	f := newImpactFleet(t)
	seed := &publishEnv{draftEnv: f.draftEnv}
	prv := seed.sharedProfileRevision("shared-set", f.ntp.older)
	prf := id.New(id.Profile)
	mustExec(t, f.db, `INSERT INTO profile (id, cluster, scope, name, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, NULL, 'library', 'shared-set', $2, 1, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, prf, prv)
	b := &publishEnv{draftEnv: &draftEnv{env: f.env, cluster: f.b.cluster, machine: f.b.machine, seed: f.seed}}
	b.adopt("profile", "shared-set", prf, prv, 1)
	others := []elsewhere{{f.b.cluster, f.ntp.newer, ""}, {f.b.cluster, f.ntp.older, "shared-set"}}

	// A adopts the shared profile and selects it alone, in place of the fragment: the fragment
	// reaches its machine through the shared profile's pin only, at the revision it composed.
	p := f.next(f.a)
	p.adoptionEntry("profile", "shared-set", prv, nil)
	p.selects([]string{"shared-set"})
	p.share("fragment", "ntp", p.sharedRevision("ntp"), 2, 1)
	f.wantImpact(p.draft,
		reachedItem{"fragment", "ntp", "library", []reached{{f.a.machine, "adoption", "keeps"}}, others},
		reachedItem{"profile", "shared-set", "library", []reached{{f.a.machine, "assignment", "gains"}},
			[]elsewhere{{f.b.cluster, prv, ""}}})
	f.compiledNames(p)

	// A cluster profile pinning the adopted revision is the reason before any shared pin.
	q := f.next(f.a)
	q.pinning(f.ntp.older)
	q.selects([]string{"workers"})
	q.adoptionEntry("fragment", "ntp", f.ntp.older, 1)
	f.wantImpact(q.draft,
		reachedItem{"fragment", "ntp", "library", []reached{{f.a.machine, "profile", "keeps"}}, others},
		reachedItem{"profile", "workers", "cluster", []reached{{f.a.machine, "assignment", "gains"}}, nil})
}

// PA §9.2: impact is of an open draft, takes no query and counts only machines with an import base.
func TestLibraryImpactDraft(t *testing.T) {
	f := newImpactFleet(t)
	p := f.next(f.a)
	p.adoptionEntry("fragment", "ntp", f.ntp.newer, 1)
	rec := f.do(f.api, machineCall(f.human("h-author"), "k-machine-nobase-01234", f.a.cluster,
		"2c6b7d2f-3e4a-4f60-9b0c-1d2e3f4a5b6c"))
	nobase := decode[machineBody](t, rec, http.StatusCreated).ID
	other := &publishEnv{draftEnv: &draftEnv{env: f.env, cluster: f.a.cluster, machine: nobase, seed: f.seed}}
	mustExec(t, f.db, `INSERT INTO assignment (id, cluster, machine, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, $2, $3, $4, 1, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, id.New(id.Assignment), f.a.cluster, nobase,
		other.assignmentRevision(nil, "cluster/ntp"))
	f.wantImpact(p.draft, reachedItem{"fragment", "ntp", "library",
		[]reached{{f.a.machine, "assignment", "gains"}}, f.onB()})

	read := func(path string, status int, code string) {
		t.Helper()
		wantProblem(t, f.do(f.api, call{method: "GET", path: prefix + path, token: f.human("h-viewer")}), status, code)
	}
	read("/drafts/"+id.New(id.Draft)+"/impact", http.StatusNotFound, "not-found")
	read("/drafts/workers/impact", http.StatusNotFound, "not-found")
	read("/drafts/"+p.draft+"/impact?limit=1", http.StatusBadRequest, "invalid-request")
	p.publishedEntries()
	read("/drafts/"+p.draft+"/impact", http.StatusConflict, "conflict")
}
