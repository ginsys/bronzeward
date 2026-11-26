package api

import (
	"database/sql"
	"net/http"
	"slices"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// The publication of a draft's adoption entries, adoption removals and detachments
// (persistence-api.md §3.1.1, §6.2). No route writes one yet, so each case seeds its draft's
// entries.

// adoptionEntry sets the draft's adoption entry of a shared name to revision (nil: a removal of
// the adoption), from the adoption counter base (nil: absent).
func (p *publishEnv) adoptionEntry(kind, name string, revision, base any) {
	p.t.Helper()
	mustExec(p.t, p.db, `DELETE FROM draft_source_entry WHERE draft = $1 AND kind = $2 AND name = $3 AND scope = 'library'`,
		p.draft, kind, name)
	mustExec(p.t, p.db, `INSERT INTO draft_source_entry (draft, cluster, kind, name, scope, `+kind+`_revision, base, adoption)
		VALUES ($1, $2, $3, $4, 'library', $5, $6, true)`, p.draft, p.cluster, kind, name, revision, base)
}

// detachedEntry sets the draft's cluster entry of name to revision, the copy a detachment wrote.
func (p *publishEnv) detachedEntry(kind, name, revision string) {
	p.t.Helper()
	p.entry(kind, name, revision)
	mustExec(p.t, p.db, `UPDATE draft_source_entry SET detached = true WHERE draft = $1 AND kind = $2 AND name = $3
		AND scope = 'cluster'`, p.draft, kind, name)
}

// removedRow is the source of a removed adoption: no revision, previous the one it held.
func removedRow(previous string, counter int64) sourceRow {
	return sharedRow("", previous, counter)
}

// refusedSnapshot wants the snapshot refused 422 with these members.
func (p *publishEnv) refusedSnapshot(want map[string]any) {
	p.t.Helper()
	_, ref := p.snapshot()
	if ref == nil || ref.status != http.StatusUnprocessableEntity {
		p.t.Fatalf("snapshot %v; want 422 %v", ref, want)
	}
	for k, v := range want {
		if ref.extra[k] != v {
			p.t.Fatalf("snapshot %v %v; want %s %v", ref, ref.extra, k, v)
		}
	}
}

// PA §3.1.1 table, the adoption entry row: the cluster's adoption alone is set, to any revision of
// the name, from the entry's base; the shared head does not move.
func TestLibraryPublishAdoption(t *testing.T) {
	for name, c := range map[string]struct {
		// the published adoption: counter 0 for none, held "older", "newer" or "" (removed)
		held    string
		counter int
		adopts  string
	}{
		"a first adoption of an older revision": {counter: 0, adopts: "older"},
		"a newer revision over an adopted one":  {held: "older", counter: 4, adopts: "newer"},
		"back to an older revision":             {held: "newer", counter: 4, adopts: "older"},
		"after a removal":                       {counter: 2, adopts: "newer"},
	} {
		t.Run(name, func(t *testing.T) {
			p := newPublishEnv(t)
			f := p.sharedFragment("ntp")
			revisions := map[string]string{"older": f.older, "newer": f.newer, "": ""}
			var base any
			if c.counter != 0 {
				base = c.counter
				p.adopt("fragment", "ntp", f.head, nullable(revisions[c.held]), c.counter)
			}
			to := revisions[c.adopts]
			p.adoptionEntry("fragment", "ntp", to, base)
			p.selectToo(nil, "cluster/ntp")
			if got := p.compiled().machines[0].fragments; !slices.Contains(got, to) {
				t.Fatalf("fragments %v; want the adopted %s", got, to)
			}
			rel := p.published()
			p.wantDigest(rel)
			p.wantHead("fragment", "ntp", f.newer, 2)
			p.wantAdoption(p.cluster, "fragment", "ntp", to, c.counter+1)
			p.wantSources(rel, "fragment", "ntp", sharedRow(to, revisions[c.held], int64(c.counter+1)))
		})
	}
	t.Run("a name no machine uses is still a source", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.adoptionEntry("fragment", "ntp", f.older, nil)
		rel := p.publishedEntries()
		p.wantSources(rel, "fragment", "ntp", sharedRow(f.older, "", 1))
	})
	t.Run("a shared profile's pin of the adopted revision is one source, the adoption's", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.adoptedSharedProfile("shared-set", f.older)
		p.adoptionEntry("fragment", "ntp", f.older, nil)
		p.selectToo([]string{"shared-set"})
		rel := p.publishedEntries()
		p.wantSources(rel, "fragment", "ntp", sharedRow(f.older, "", 1))
	})
	t.Run("an adopted profile compiles its pins as written", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		prv := p.sharedProfileRevision("shared-set", f.older)
		head := p.sharedProfileHead("shared-set", prv)
		p.adoptionEntry("profile", "shared-set", prv, nil)
		p.selectToo([]string{"shared-set"})
		s := p.compiled()
		if got := s.machines[0].fragments; !slices.Contains(got, f.older) {
			t.Fatalf("fragments %v; want the pinned %s", got, f.older)
		}
		rel := p.published()
		p.wantDigest(rel)
		p.wantAdoption(p.cluster, "profile", "shared-set", prv, 1)
		if got, want := p.sourceRow(rel, "profile", head), sharedRow(prv, "", 1); got != want {
			t.Fatalf("source %+v; want %+v", got, want)
		}
		p.wantSources(rel, "fragment", "ntp", sharedRow(f.older, "", 0))
	})
	for name, c := range map[string]struct {
		base, expected any
		actual         any
		held           bool
	}{
		"the adoption moved":   {base: 3, expected: 3, actual: 4, held: true},
		"an adoption appeared": {base: nil, expected: "absent", actual: 4, held: true},
		"the adoption is none": {base: 4, expected: 4, actual: "absent"},
	} {
		t.Run("stale: "+name, func(t *testing.T) {
			p := newPublishEnv(t)
			f := p.sharedFragment("ntp")
			if c.held {
				p.adopt("fragment", "ntp", f.head, f.older, 4)
			}
			p.adoptionEntry("fragment", "ntp", f.newer, c.base)
			p.compiled()
			wantConflicts(t, p.refused(409, "stale-input"),
				map[string]any{"head": f.head, "scope": "adoption", "expected": c.expected, "actual": c.actual})
		})
	}
	t.Run("a head removed before the commit", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.adoptionEntry("fragment", "ntp", f.older, nil)
		p.compiled()
		p.hold(`UPDATE fragment SET head_revision_id = NULL, head_revision = 3 WHERE id = $1`, f.head)
		ref := p.refused(422, "validation-failed")
		if ref.extra["rule"] != "library-removed" || ref.extra["name"] != "ntp" || ref.extra["kind"] != "fragment" {
			t.Fatalf("refusal %v %v; want library-removed of fragment ntp", ref, ref.extra)
		}
	})
	// PA §3.1.1: an adoption entry proposes no revision, so a shared profile entry that pins the
	// revision it adopts is still held to the shared head at the commit.
	t.Run("an adoption entry proposes nothing to a shared profile's pin", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.adoptionEntry("fragment", "ntp", f.newer, nil)
		p.share("profile", "shared-set", p.sharedProfileRevision("shared-set", f.newer), nil, nil)
		p.compiled()
		p.hold(`UPDATE fragment SET head_revision_id = $2, head_revision = 3 WHERE id = $1`, f.head, f.older)
		if ref := p.refused(422, "validation-failed"); ref.extra["profile"] != "shared-set" || ref.extra["revision"] != f.newer {
			t.Fatalf("refusal %v %v; want the pin %s", ref, ref.extra, f.newer)
		}
	})
	t.Run("beside the cluster's own head", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.fragmentHead("ntp", "cluster", p.fragmentRevision(p.cluster, "ntp", "cluster"), 1)
		p.adoptionEntry("fragment", "ntp", f.older, nil)
		p.compiled()
		if ref := p.refused(422, "validation-failed"); ref.extra["rule"] != "name-in-two-scopes" {
			t.Fatalf("refusal %v %v; want name-in-two-scopes", ref, ref.extra)
		}
	})
	t.Run("a cluster pin of the revision adopted before is refused", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.adopt("fragment", "ntp", f.head, f.older, 1)
		p.adoptionEntry("fragment", "ntp", f.newer, 1)
		p.pinning(f.older)
		p.selectToo([]string{"workers"})
		p.refusedSnapshot(map[string]any{"profile": "workers", "revision": f.older})
	})
	t.Run("beside a shared profile's pin of another revision", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.adoptedSharedProfile("shared-set", f.older)
		p.adoptionEntry("fragment", "ntp", f.newer, nil)
		p.selectToo([]string{"shared-set"}, "cluster/ntp")
		p.refusedSnapshot(map[string]any{"rule": "library-pin-conflict", "fragment": "ntp"})
	})
}

// sharedProfileHead publishes a shared profile head of name at prv.
func (p *publishEnv) sharedProfileHead(name, prv string) string {
	p.t.Helper()
	head := id.New(id.Profile)
	mustExec(p.t, p.db, `INSERT INTO profile (id, cluster, scope, name, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, NULL, 'library', $2, $3, 1, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, head, name, prv)
	return head
}

// clusterPinningHead publishes the cluster's own profile "workers" pinning the shared revision frv,
// which no machine selects.
func (p *publishEnv) clusterPinningHead(frv string) {
	p.t.Helper()
	prv := id.New(id.ProfileRevision)
	tx, err := p.db.Begin()
	if err != nil {
		p.t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(p.t, tx, `INSERT INTO profile_revision (id, cluster, name, author, created_at) VALUES ($1, $2, 'workers', $3, now())`,
		prv, p.cluster, p.seed)
	mustExec(p.t, tx, `INSERT INTO profile_revision_fragment (revision, cluster, position, fragment_revision, library)
		VALUES ($1, $2, 0, $3, true)`, prv, p.cluster, frv)
	if err := tx.Commit(); err != nil {
		p.t.Fatal(err)
	}
	mustExec(p.t, p.db, `INSERT INTO profile (id, cluster, scope, name, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, $2, 'cluster', 'workers', $3, 1, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, id.New(id.Profile), p.cluster, prv)
}

// PA §3.1.1 table, the adoption removal row: the row stays with no revision and the next counter,
// the shared head is not touched, and the release records the removal.
func TestLibraryPublishAdoptionRemoval(t *testing.T) {
	t.Run("a name no machine uses", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.adopt("fragment", "ntp", f.head, f.older, 4)
		p.adoptionEntry("fragment", "ntp", nil, 4)
		rel := p.publishedEntries()
		p.wantHead("fragment", "ntp", f.newer, 2)
		p.wantAdoption(p.cluster, "fragment", "ntp", "", 5)
		p.wantSources(rel, "fragment", "ntp", removedRow(f.older, 5))
	})
	t.Run("a removed shared head is not read", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		mustExec(t, p.db, `UPDATE fragment SET head_revision_id = NULL, head_revision = 3 WHERE id = $1`, f.head)
		p.adopt("fragment", "ntp", f.head, f.older, 4)
		p.adoptionEntry("fragment", "ntp", nil, 4)
		p.publishedEntries()
		p.wantAdoption(p.cluster, "fragment", "ntp", "", 5)
	})
	t.Run("stale", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.adopt("fragment", "ntp", f.head, f.older, 4)
		p.adoptionEntry("fragment", "ntp", nil, 3)
		p.compiled()
		wantConflicts(t, p.refused(409, "stale-input"),
			map[string]any{"head": f.head, "scope": "adoption", "expected": 3, "actual": 4})
	})
	t.Run("a selected name is needed", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.adopt("fragment", "ntp", f.head, f.older, 4)
		p.adoptionEntry("fragment", "ntp", nil, 4)
		p.selectToo(nil, "cluster/ntp")
		p.refusedSnapshot(map[string]any{"machine": p.machine, "path": "fragments.cluster[0]"})
	})
	t.Run("a selected profile's pin is needed", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.adopt("fragment", "ntp", f.head, f.older, 4)
		p.adoptionEntry("fragment", "ntp", nil, 4)
		p.pinning(f.older)
		p.selectToo([]string{"workers"})
		p.refusedSnapshot(map[string]any{"profile": "workers", "revision": f.older})
	})
	// Only a profile the release names needs the name: an unselected one composes nothing, and a
	// later selection of it is refused.
	t.Run("an unselected profile's pin is not needed until it is selected", func(t *testing.T) {
		d := newDraftEnv(t)
		removing := bare(d, d.cluster, d.machine, "k-draft-remove-012345", "bw-artifact")
		f := removing.sharedFragment("ntp")
		removing.adopt("fragment", "ntp", f.head, f.older, 4)
		removing.clusterPinningHead(f.older)
		removing.adoptionEntry("fragment", "ntp", nil, 4)
		removing.publishedEntries()
		removing.wantAdoption(d.cluster, "fragment", "ntp", "", 5)

		selecting := bare(d, d.cluster, d.machine, "k-draft-select-012345", "bw-artifact-select")
		asr := selecting.assignmentRevision([]string{"workers"})
		selecting.entry("assignment", "", asr)
		selecting.unit.machines[0].assignment = asr
		selecting.refusedSnapshot(map[string]any{"profile": "workers", "revision": f.older})
	})
}

// PA §3.1.1 table, the detachment rows: the cluster's copy and the adoption's removal publish
// together, and a detached profile's pins must be in the cluster's view.
func TestLibraryPublishDetachment(t *testing.T) {
	t.Run("a fragment", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.adopt("fragment", "ntp", f.head, f.older, 4)
		own := p.fragmentRevision(p.cluster, "ntp", "cluster")
		p.detachedEntry("fragment", "ntp", own)
		p.adoptionEntry("fragment", "ntp", nil, 4)
		p.selectToo(nil, "cluster/ntp")
		if got := p.compiled().machines[0].fragments; !slices.Contains(got, own) || slices.Contains(got, f.older) {
			t.Fatalf("fragments %v; want the copy %s", got, own)
		}
		rel := p.published()
		p.wantDigest(rel)
		p.wantAdoption(p.cluster, "fragment", "ntp", "", 5)
		p.wantSources(rel, "fragment", "ntp", removedRow(f.older, 5),
			sourceRow{scope: "cluster", revision: sql.NullString{String: own, Valid: true},
				headRevision: sql.NullInt64{Int64: 1, Valid: true}})
	})
	// The copy of a profile, with the adoption's removal (nil base: the detachment dropped an
	// adoption its draft introduced, so the copy stands alone).
	detached := func(p *publishEnv, f sharedFragment, removes bool) {
		prv := p.sharedProfileRevision("shared-set", f.older)
		// The copy first: seeding a cluster entry clears the name's entries.
		p.detachedEntry("profile", "shared-set", p.clusterCopy("shared-set", f.older))
		if removes {
			head := p.sharedProfileHead("shared-set", prv)
			p.adopt("profile", "shared-set", head, prv, 3)
			p.adoptionEntry("profile", "shared-set", nil, 3)
		}
		p.selectToo([]string{"shared-set"})
	}
	for name, removes := range map[string]bool{"with the adoption's removal": true, "the copy alone": false} {
		t.Run("a profile "+name+", pins in the view", func(t *testing.T) {
			p := newPublishEnv(t)
			f := p.sharedFragment("ntp")
			p.adopt("fragment", "ntp", f.head, f.older, 1)
			detached(p, f, removes)
			rel := p.publishedEntries()
			if n := p.count(`SELECT count(*) FROM release_source WHERE release = $1 AND name = 'shared-set'`, rel); removes && n != 2 ||
				!removes && n != 1 {
				t.Fatalf("%d sources of the detached name", n)
			}
		})
		for view, revision := range map[string]string{"another revision": "newer", "none": ""} {
			t.Run("a profile "+name+", the view holds "+view, func(t *testing.T) {
				p := newPublishEnv(t)
				f := p.sharedFragment("ntp")
				if revision == "newer" {
					p.adopt("fragment", "ntp", f.head, f.newer, 1)
				}
				detached(p, f, removes)
				p.refusedSnapshot(map[string]any{"rule": "detach-pin-not-in-view", "fragment": "ntp",
					"profile": "shared-set", "revision": f.older})
			})
		}
	}
}

// clusterCopy is a cluster profile revision of name pinning the shared revision frv, as a
// detachment writes one.
func (p *publishEnv) clusterCopy(name, frv string) string {
	p.t.Helper()
	prv := id.New(id.ProfileRevision)
	tx, err := p.db.Begin()
	if err != nil {
		p.t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(p.t, tx, `INSERT INTO profile_revision (id, cluster, name, author, created_at) VALUES ($1, $2, $3, $4, now())`,
		prv, p.cluster, name, p.seed)
	mustExec(p.t, tx, `INSERT INTO profile_revision_fragment (revision, cluster, position, fragment_revision, library)
		VALUES ($1, $2, 0, $3, true)`, prv, p.cluster, frv)
	if err := tx.Commit(); err != nil {
		p.t.Fatal(err)
	}
	return prv
}

// Contract 16: an adoption entry and a removal of the shared head share no cluster lock. The head's
// own lock orders them: removal first, the adoption is refused; adoption first, both commit.
func TestLibraryConcurrentAdoptionAndRemoval(t *testing.T) {
	for name, removalFirst := range map[string]bool{"the removal first": true, "the adoption first": false} {
		t.Run(name, func(t *testing.T) {
			d := newDraftEnv(t)
			other := d.createCluster(d.api, d.human("h-author"), "k-cluster-other-012345")
			rec := d.do(d.api, machineCall(d.human("h-author"), "k-machine-other-012345", other, "1b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b"))
			otherMachine := decode[machineBody](t, rec, http.StatusCreated).ID
			removing := bare(d, d.cluster, d.machine, "k-draft-remove-012345", "bw-artifact")
			f := removing.sharedFragment("ntp")
			removing.share("fragment", "ntp", nil, 2, nil)
			adopting := bare(d, other, otherMachine, "k-draft-adopt-0123456", "bw-artifact-adopt")
			adopting.adoptionEntry("fragment", "ntp", f.older, nil)
			removing.compiled()
			adopting.compiled()
			if removalFirst {
				a, b := race(t, removing, adopting)
				if ref := wantRace(t, a, b, http.StatusUnprocessableEntity, "validation-failed"); ref.extra["rule"] != "library-removed" {
					t.Fatalf("refusal %v; want library-removed", ref.extra)
				}
				if n := adopting.count(`SELECT count(*) FROM library_adoption`); n != 0 {
					t.Fatalf("%d adoptions of the removed name", n)
				}
				return
			}
			a, b := race(t, adopting, removing)
			for _, c := range []raced{a, b} {
				if c.err != nil || c.ref != nil {
					t.Fatalf("publication: %v %v; want both committed", c.ref, c.err)
				}
			}
			adopting.wantAdoption(other, "fragment", "ntp", f.older, 1)
			adopting.wantHead("fragment", "ntp", "", 3)
		})
	}
}

// Contract 16: an adoption entry and a publication that introduces or restores the cluster's own
// head of the name cannot both commit, whichever is first.
func TestLibraryConcurrentAdoptionAndOwnHead(t *testing.T) {
	for name, c := range map[string]struct{ restored, ownFirst bool }{
		"a new head, committed first":      {false, true},
		"a new head, committed second":     {false, false},
		"a restored head, committed first": {true, true},
		"a restored head, committed after": {true, false},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDraftEnv(t)
			own := bare(d, d.cluster, d.machine, "k-draft-own-012345678", "bw-artifact")
			rev := own.fragmentRevision(d.cluster, "ntp", "cluster")
			if c.restored {
				own.fragmentHead("ntp", "cluster", nil, 2)
			}
			own.entry("fragment", "ntp", rev)
			if c.restored {
				mustExec(t, d.db, `UPDATE draft_source_entry SET base = 2 WHERE draft = $1 AND name = 'ntp'`, own.draft)
			}
			adopting := bare(d, d.cluster, d.machine, "k-draft-adopt-0123456", "bw-artifact-adopt")
			f := adopting.sharedFragment("ntp")
			adopting.adoptionEntry("fragment", "ntp", f.older, nil)
			own.compiled()
			adopting.compiled()
			first, second := own, adopting
			if !c.ownFirst {
				first, second = adopting, own
			}
			a, b := race(t, first, second)
			if ref := wantRace(t, a, b, http.StatusUnprocessableEntity, "validation-failed"); ref.extra["rule"] != "name-in-two-scopes" {
				t.Fatalf("refusal %v; want name-in-two-scopes", ref.extra)
			}
			if n := count(t, d.db, `SELECT (SELECT count(*) FROM fragment WHERE cluster = $1 AND name = 'ntp'
				AND head_revision_id IS NOT NULL) + (SELECT count(*) FROM library_adoption WHERE cluster = $1 AND name = 'ntp')`,
				d.cluster); n != 1 {
				t.Fatalf("the cluster holds the name %d times", n)
			}
		})
	}
}

// Concurrent publications of one cluster, one of them giving a machine its first assignment.

// assigning is a publication of a new draft that gives d's machine its first assignment, selecting
// the cluster-layer fragment name.
func assigning(d *draftEnv, name string) *publishEnv {
	d.t.Helper()
	p := bare(d, d.cluster, d.machine, "k-draft-assign-012345", "bw-artifact-assign")
	asr := p.assignmentRevision(nil, "cluster/"+name)
	p.entry("assignment", "", asr)
	p.unit.machines[0].assignment = asr
	return p
}

// wantAssignmentAppeared wants removing, compiled before assigning gave the machine its first
// assignment and committed after it, refused as stale input naming that assignment head, and the
// machine left on assigning's release.
func wantAssignmentAppeared(t *testing.T, assigning, removing *publishEnv) {
	t.Helper()
	assigning.compiled()
	removing.compiled()
	a, b := race(t, assigning, removing)
	ref := wantRace(t, a, b, http.StatusConflict, "stale-input")
	var head string
	if err := assigning.db.QueryRow(`SELECT id FROM assignment WHERE machine = $1`, assigning.machine).Scan(&head); err != nil {
		t.Fatal(err)
	}
	wantConflicts(t, ref, map[string]any{"head": head, "expected": "absent", "actual": 1})
	if n := assigning.count(`SELECT count(*) FROM machine_state WHERE machine = $1 AND desired = $2`,
		assigning.machine, a.release); n != 1 {
		t.Fatal("the machine's desired release is not the one with its assignment")
	}
}

// Contract 16, PA §4.2: a publication compiled while a machine had no assignment holds no head of
// one, so it compares that none appeared: else it would remove a name the new assignment selects.
func TestLibraryConcurrentAssignmentAppeared(t *testing.T) {
	t.Run("a cluster fragment removal", func(t *testing.T) {
		d := newDraftEnv(t)
		removing := bare(d, d.cluster, d.machine, "k-draft-remove-012345", "bw-artifact")
		removing.fragmentHead("ntp", "cluster", removing.fragmentRevision(d.cluster, "ntp", "cluster"), 1)
		removing.entry("fragment", "ntp", nil)
		mustExec(t, d.db, `UPDATE draft_source_entry SET base = 1 WHERE draft = $1 AND name = 'ntp'`, removing.draft)
		wantAssignmentAppeared(t, assigning(d, "ntp"), removing)
	})
	t.Run("an adoption removal", func(t *testing.T) {
		d := newDraftEnv(t)
		removing := bare(d, d.cluster, d.machine, "k-draft-remove-012345", "bw-artifact")
		f := removing.sharedFragment("ntp")
		removing.adopt("fragment", "ntp", f.head, f.older, 1)
		removing.adoptionEntry("fragment", "ntp", nil, 1)
		wantAssignmentAppeared(t, assigning(d, "ntp"), removing)
	})
}
