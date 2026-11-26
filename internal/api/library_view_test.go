package api

import (
	"database/sql"
	"net/http"
	"slices"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// sharedFragment is a shared fragment name in the cluster layer: its head at newer, and an older
// revision a cluster may still hold. No route writes shared data yet, so it is seeded.
type sharedFragment struct{ name, head, older, newer string }

func (p *publishEnv) sharedRevision(name string) string {
	p.t.Helper()
	frv := id.New(id.FragmentRevision)
	mustExec(p.t, p.db, `INSERT INTO fragment_revision (id, cluster, name, layer, document, author, embedded, created_at)
		VALUES ($1, NULL, $2, 'cluster', 'machine: {}', $3, '[]', now())`, frv, name, p.seed)
	return frv
}

func (p *publishEnv) sharedFragment(name string) sharedFragment {
	p.t.Helper()
	f := sharedFragment{name: name, head: id.New(id.Fragment), older: p.sharedRevision(name), newer: p.sharedRevision(name)}
	mustExec(p.t, p.db, `INSERT INTO fragment (id, cluster, scope, name, layer, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, NULL, 'library', $2, 'cluster', $3, 2, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, f.head, name, f.newer)
	return f
}

// adopt seeds the cluster's adoption of the shared head, at revision (nil: removed).
func (p *publishEnv) adopt(kind, name, head string, revision any, counter int) {
	p.t.Helper()
	mustExec(p.t, p.db, `INSERT INTO library_adoption (cluster, kind, name, `+kind+`, `+kind+`_revision, adoption_revision, etag_token)
		VALUES ($1, $2, $3, $4, $5, $6, 'm3oxmlfh6phr7aigshdydcb4ji')`, p.cluster, kind, name, head, revision, counter)
}

// selectToo makes the draft's assignment select, beside what the fixture's selects, the profiles
// and the "layer/name" fragments given, and binds it to the unit.
func (p *publishEnv) selectToo(profiles []string, fragments ...string) {
	p.t.Helper()
	asr := p.assignmentRevision(append([]string{"standard"}, profiles...),
		append([]string{"site/network", "role/storage"}, fragments...)...)
	p.entry("assignment", "", asr)
	p.unit.machines[0].assignment = asr
}

// pinning is a cluster profile "workers" of the draft pinning the shared revision frv.
func (p *publishEnv) pinning(frv string) {
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
	p.entry("profile", "workers", prv)
}

// compiled takes the snapshot, which must not be refused, and hands the unit its unchanged heads.
func (p *publishEnv) compiled() snapshot {
	p.t.Helper()
	s, ref := p.snapshot()
	if ref != nil {
		p.t.Fatalf("snapshot refused: %v %v", ref, ref.extra)
	}
	p.unit.unchanged = s.unchanged
	return s
}

func (p *publishEnv) published() string {
	p.t.Helper()
	rel, ref := p.commit()
	if ref != nil {
		p.t.Fatalf("refused: %v %v", ref, ref.extra)
	}
	return rel
}

// sourceRow is a release's source row of one head.
type sourceRow struct {
	scope                  string
	revision, previous     sql.NullString
	headRevision, adoption sql.NullInt64
}

func (p *publishEnv) sourceRow(rel, kind, head string) sourceRow {
	p.t.Helper()
	var r sourceRow
	if err := p.db.QueryRow(`SELECT scope, `+kind+`_revision, previous_`+kind+`_revision, head_revision, adoption_revision
		FROM release_source WHERE release = $1 AND `+kind+` = $2`, rel, head).
		Scan(&r.scope, &r.revision, &r.previous, &r.headRevision, &r.adoption); err != nil {
		p.t.Fatalf("source %s of %s: %v", head, rel, err)
	}
	return r
}

func adoptedRow(revision string, counter int64) sourceRow {
	return sourceRow{scope: "library", revision: sql.NullString{String: revision, Valid: true},
		previous: sql.NullString{String: revision, Valid: true}, adoption: sql.NullInt64{Int64: counter, Valid: true}}
}

func hasUsed(s snapshot, want usedHead) bool { return slices.Contains(s.unchanged, want) }

// PA §3.1.1 criterion 1, §6.1, §6.2: a cluster compiles the shared revision it adopted while the
// shared head is newer, a shared head that moves moves nothing of it, and the release records the
// adoption: its counter and the revision adopted before, with no head revision.
func TestLibraryViewCompilesTheAdoptedRevision(t *testing.T) {
	p := newPublishEnv(t)
	f := p.sharedFragment("ntp")
	p.adopt("fragment", "ntp", f.head, f.older, 4)
	p.selectToo(nil, "cluster/ntp")
	s := p.compiled()
	if got := s.machines[0].fragments; !slices.Contains(got, f.older) || slices.Contains(got, f.newer) {
		t.Fatalf("fragments %v; want the adopted %s and not the head's %s", got, f.older, f.newer)
	}
	if want := (usedHead{kind: "fragment", head: f.head, revision: f.older, headRevision: 4, adopted: true}); !hasUsed(s, want) {
		t.Fatalf("unchanged %+v; want %+v", s.unchanged, want)
	}
	mustExec(t, p.db, `UPDATE fragment SET head_revision = 3 WHERE id = $1`, f.head)
	rel := p.published()
	if got, want := p.sourceRow(rel, "fragment", f.head), adoptedRow(f.older, 4); got != want {
		t.Fatalf("source %+v; want %+v", got, want)
	}
	stored, err := releaseDigest(t.Context(), p.db, rel)
	if err != nil {
		t.Fatal(err)
	}
	if n := p.count(`SELECT count(*) FROM release WHERE id = $1 AND digest = $2`, rel, stored[:]); n != 1 {
		t.Fatalf("the stored rows give another digest than the release's")
	}
	if n := p.count(`SELECT count(*) FROM library_adoption WHERE adoption_revision = 4 AND fragment_revision = $1`, f.older); n != 1 {
		t.Fatal("the publication moved the adoption")
	}
}

// PA §4.2, §6.2: an adoption advanced between the snapshot and the commit is stale input, named by
// the shared head with scope adoption. The commit waits for the writer's row, so it holds the
// adoption and not only reads it.
func TestLibraryViewAdoptionMovedIsStale(t *testing.T) {
	p := newPublishEnv(t)
	f := p.sharedFragment("ntp")
	p.adopt("fragment", "ntp", f.head, f.older, 1)
	p.selectToo(nil, "cluster/ntp")
	p.compiled()
	p.hold(`UPDATE library_adoption SET fragment_revision = $1, adoption_revision = 2 WHERE cluster = $2`, f.newer, p.cluster)
	wantConflicts(t, p.refused(409, "stale-input"),
		map[string]any{"head": f.head, "scope": "adoption", "expected": 1, "actual": 2})
}

// PA §3.1.1: the view of a name is the cluster's own head, else its adoption, else none.
func TestLibraryViewPrecedence(t *testing.T) {
	t.Run("a cluster head with a revision wins", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		own := p.fragmentRevision(p.cluster, "ntp", "cluster")
		head := p.fragmentHead("ntp", "cluster", own, 1)
		p.adopt("fragment", "ntp", f.head, f.older, 1)
		p.selectToo(nil, "cluster/ntp")
		s := p.compiled()
		if got := s.machines[0].fragments; !slices.Contains(got, own) || slices.Contains(got, f.older) {
			t.Fatalf("fragments %v; want the cluster's own %s", got, own)
		}
		if want := (usedHead{kind: "fragment", head: head, revision: own, headRevision: 1}); !hasUsed(s, want) {
			t.Fatalf("unchanged %+v; want %+v", s.unchanged, want)
		}
	})
	t.Run("a removed cluster head leaves the adoption", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.fragmentHead("ntp", "cluster", nil, 2)
		p.adopt("fragment", "ntp", f.head, f.older, 1)
		p.selectToo(nil, "cluster/ntp")
		if got := p.compiled().machines[0].fragments; !slices.Contains(got, f.older) {
			t.Fatalf("fragments %v; want the adopted %s", got, f.older)
		}
	})
	for name, adopt := range map[string]func(*publishEnv, sharedFragment){
		"a removed adoption is none": func(p *publishEnv, f sharedFragment) { p.adopt("fragment", "ntp", f.head, nil, 2) },
		"a shared head is none":      func(*publishEnv, sharedFragment) {},
	} {
		t.Run(name, func(t *testing.T) {
			p := newPublishEnv(t)
			adopt(p, p.sharedFragment("ntp"))
			p.selectToo(nil, "cluster/ntp")
			_, ref := p.snapshot()
			if ref == nil || ref.status != http.StatusUnprocessableEntity || ref.extra["path"] != "fragments.cluster[0]" {
				t.Fatalf("snapshot %v; want 422 at fragments.cluster[0]", ref)
			}
		})
	}
}

// PA §3.1.1: a cluster profile pins a shared fragment at the revision the cluster adopted.
func TestLibraryViewClusterPin(t *testing.T) {
	t.Run("the adopted revision publishes", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.adopt("fragment", "ntp", f.head, f.older, 1)
		p.pinning(f.older)
		p.selectToo([]string{"workers"})
		if got := p.compiled().machines[0].fragments; !slices.Contains(got, f.older) {
			t.Fatalf("fragments %v; want the pinned %s", got, f.older)
		}
		if got, want := p.sourceRow(p.published(), "fragment", f.head), adoptedRow(f.older, 1); got != want {
			t.Fatalf("source %+v; want %+v", got, want)
		}
	})
	t.Run("the shared head's revision, not adopted, is refused", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.adopt("fragment", "ntp", f.head, f.older, 1)
		p.pinning(f.newer)
		p.selectToo([]string{"workers"})
		_, ref := p.snapshot()
		if ref == nil || ref.status != http.StatusUnprocessableEntity || ref.extra["revision"] != f.newer {
			t.Fatalf("snapshot %v; want 422 naming %s", ref, f.newer)
		}
	})
}

// An adopted shared profile is held and recorded as an adoption too. Its pins are still held to
// the cluster's view, so one the cluster has not adopted refuses the publication.
func TestLibraryViewAdoptedProfile(t *testing.T) {
	for name, adopted := range map[string]bool{"with its fragment adopted": true, "with its fragment not adopted": false} {
		t.Run(name, func(t *testing.T) {
			p := newPublishEnv(t)
			f := p.sharedFragment("ntp")
			prv, prf := id.New(id.ProfileRevision), id.New(id.Profile)
			tx, err := p.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			mustExec(t, tx, `INSERT INTO profile_revision (id, cluster, name, author, created_at) VALUES ($1, NULL, 'shared-set', $2, now())`,
				prv, p.seed)
			mustExec(t, tx, `INSERT INTO profile_revision_fragment (revision, cluster, position, fragment_revision) VALUES ($1, NULL, 0, $2)`,
				prv, f.older)
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			mustExec(t, p.db, `INSERT INTO profile (id, cluster, scope, name, head_revision_id, head_revision, etag_token, created_at)
				VALUES ($1, NULL, 'library', 'shared-set', $2, 1, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, prf, prv)
			p.adopt("profile", "shared-set", prf, prv, 3)
			if adopted {
				p.adopt("fragment", "ntp", f.head, f.older, 1)
			}
			p.selectToo([]string{"shared-set"})
			if !adopted {
				if _, ref := p.snapshot(); ref == nil || ref.status != http.StatusUnprocessableEntity {
					t.Fatalf("snapshot %v; want 422", ref)
				}
				return
			}
			p.compiled()
			if got, want := p.sourceRow(p.published(), "profile", prf), adoptedRow(prv, 3); got != want {
				t.Fatalf("source %+v; want %+v", got, want)
			}
		})
	}
}

// Contract 9: a detachment's two sources share a kind and a name, and give one digest in either
// order.
func TestLibraryDigestOrdersADetachment(t *testing.T) {
	removal, own := contentSource{kind: "fragment", key: "ntp"}, contentSource{kind: "fragment", key: "ntp", revision: "frv_x"}
	a, b := releaseContent{sources: []contentSource{removal, own}}, releaseContent{sources: []contentSource{own, removal}}
	if a.digest() != b.digest() {
		t.Fatal("the digest depends on the order the two sources were read in")
	}
}
