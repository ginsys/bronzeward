package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// The adoption, adoption removal and detachment routes of a draft (persistence-api.md §3.1.1,
// §9.2, §9.3). The cases speak HTTP and SQL alone.

// adopts is PUT on the draft's adoption of a fragment or profile name.
func (d *draftEnv) adopts(kinds, name, revision, ifMatch, k string) *httptest.ResponseRecorder {
	return d.put("/adoptions/"+kinds+"/"+name, `{"revision":"`+revision+`"}`, ifMatch, k)
}

func (d *draftEnv) unadopts(kinds, name, ifMatch, k string) *httptest.ResponseRecorder {
	return d.del("/adoptions/"+kinds+"/"+name, ifMatch, k)
}

func (d *draftEnv) detaches(kinds, name, ifMatch, k string) *httptest.ResponseRecorder {
	return d.do(d.api, call{method: "POST", path: prefix + "/drafts/" + d.draft + "/adoptions/" + kinds + "/" + name + "/detachments",
		token: d.human("h-author"), key: k, ifMatch: ifMatch})
}

// slot is a draft entry as stored: its revision ("" a removal), base and marks.
type slot struct {
	revision           string
	base, adoptionBase sql.NullInt64
	adoption, detached bool
}

// stored is the draft's entry of a name at scope, and whether it has one.
func (d *draftEnv) stored(kind, name, scope string) (slot, bool) {
	d.t.Helper()
	var s slot
	err := d.db.QueryRow(`SELECT coalesce(fragment_revision, profile_revision, ''), base, adoption_base, adoption, detached
		FROM draft_source_entry WHERE draft = $1 AND kind = $2 AND name = $3 AND scope = $4`, d.draft, kind, name, scope).
		Scan(&s.revision, &s.base, &s.adoptionBase, &s.adoption, &s.detached)
	if err == sql.ErrNoRows {
		return s, false
	}
	if err != nil {
		d.t.Fatal(err)
	}
	return s, true
}

func counter(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: true} }

// shownEntries are the draft read's source entries of name.
func (d *draftEnv) shownEntries(name string) []map[string]any {
	d.t.Helper()
	rec := d.do(d.api, call{method: "GET", path: prefix + "/drafts/" + d.draft, token: d.human("h-viewer")})
	var out []map[string]any
	for _, e := range decode[map[string]any](d.t, rec, http.StatusOK)["entries"].([]any) {
		if m := e.(map[string]any); m["name"] == name {
			out = append(out, m)
		}
	}
	return out
}

// wantAdoptionEntry wants e to be exactly an adoption entry or removal (§9.3).
func wantAdoptionEntry(t *testing.T, e map[string]any, of, name string, head, base, revision any) {
	t.Helper()
	if len(e) != 6 {
		t.Fatalf("entry %v: want kind, of, name, head, base and revision alone", e)
	}
	for member, want := range map[string]any{"kind": "adoption", "of": of, "name": name, "head": head, "base": base,
		"revision": revision} {
		wantMember(t, e, member, want)
	}
}

// wantNothing wants the draft still at its ETag with no entry of name.
func (d *draftEnv) wantNothing(name string) {
	d.t.Helper()
	if got := d.shownEntries(name); len(got) != 0 {
		d.t.Fatalf("entries of %s: %v; want none", name, got)
	}
	var revision int
	var token string
	if err := d.db.QueryRow(`SELECT revision, etag_token FROM draft WHERE id = $1`, d.draft).Scan(&revision, &token); err != nil {
		d.t.Fatal(err)
	}
	if etag(revision, token) != d.etag {
		d.t.Fatal("the refusal moved the draft")
	}
}

// PA §3.1.1 table, the adoption entry rows, and its update-time rules.
func TestLibraryAdoptionRoute(t *testing.T) {
	t.Run("records the entry from the adoption counter", func(t *testing.T) {
		d := newDraftEnv(t)
		p := &publishEnv{draftEnv: d}
		f := p.sharedFragment("ntp")
		e := updated(t, &d.etag, d.adopts("fragments", "ntp", f.older, d.etag, d.key()))
		wantAdoptionEntry(t, e, "fragment", "ntp", f.head, nil, f.older)
		if got, _ := d.stored("fragment", "ntp", "library"); got != (slot{revision: f.older, adoption: true}) {
			t.Fatalf("stored %+v", got)
		}
		if got := d.shownEntries("ntp"); len(got) != 1 {
			t.Fatalf("shown %v", got)
		} else {
			wantAdoptionEntry(t, got[0], "fragment", "ntp", f.head, nil, f.older)
		}
		// The counter is read again by the entry that replaces it.
		p.adopt("fragment", "ntp", f.head, f.older, 4)
		e = updated(t, &d.etag, d.adopts("fragments", "ntp", f.newer, d.etag, d.key()))
		wantAdoptionEntry(t, e, "fragment", "ntp", f.head, 4, f.newer)
		if got, _ := d.stored("fragment", "ntp", "library"); got != (slot{revision: f.newer, base: counter(4), adoption: true}) {
			t.Fatalf("stored %+v", got)
		}
		if n := count(t, d.db, `SELECT count(*) FROM act WHERE action = 'draft.adoption.update'`); n != 2 {
			t.Fatalf("%d acts", n)
		}
	})
	t.Run("a profile", func(t *testing.T) {
		d := newDraftEnv(t)
		p := &publishEnv{draftEnv: d}
		f := p.sharedFragment("ntp")
		prv := p.sharedProfileRevision("shared-set", f.older)
		head := p.sharedProfileHead("shared-set", prv)
		e := updated(t, &d.etag, d.adopts("profiles", "shared-set", prv, d.etag, d.key()))
		wantAdoptionEntry(t, e, "profile", "shared-set", head, nil, prv)
	})
	t.Run("refusals", func(t *testing.T) {
		d := newDraftEnv(t)
		p := &publishEnv{draftEnv: d}
		f := p.sharedFragment("ntp")
		other := p.sharedFragment("dns")
		own := d.fragmentRevision(d.cluster, "ntp", "cluster")
		layered := p.layered(true, "ntp", "site")
		// A revision's reference rows are written in its own transaction. No route writes a shared
		// revision with one.
		referencing := id.New(id.FragmentRevision)
		tx, err := d.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		mustExec(t, tx, `INSERT INTO fragment_revision (id, cluster, name, layer, document, author, embedded, created_at)
			VALUES ($1, NULL, 'ntp', 'cluster', 'machine: {}', $2, '[]', now())`, referencing, d.seed)
		mustExec(t, tx, `INSERT INTO fragment_reference (revision, name, kind, version, generation)
			VALUES ($1, 'registry/pass', 'string', 1, $2)`, referencing, "gen/"+d.cluster+"/"+id.New(id.Ingestion)+"/pass")
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		prv := p.sharedProfileRevision("shared-set", f.older)
		p.sharedProfileHead("shared-set", prv)
		for name, c := range map[string]struct {
			kinds, name, revision string
			want                  map[string]any
		}{
			"another name's revision":       {"fragments", "ntp", other.newer, map[string]any{"path": "revision"}},
			"a cluster's revision":          {"fragments", "ntp", own, map[string]any{"path": "revision"}},
			"a profile revision":            {"fragments", "ntp", prv, map[string]any{"path": "revision"}},
			"a fragment revision":           {"profiles", "shared-set", f.older, map[string]any{"path": "revision"}},
			"no identifier":                 {"fragments", "ntp", "latest", map[string]any{"path": "revision"}},
			"another layer than the name's": {"fragments", "ntp", layered, map[string]any{"path": "revision"}},
			"a revision with a reference":   {"fragments", "ntp", referencing, map[string]any{"rule": "library-reference"}},
			"a name the library never had":  {"fragments", "unknown", f.older, map[string]any{"rule": "library-removed", "name": "unknown"}},
		} {
			problem := wantProblem(t, d.adopts(c.kinds, c.name, c.revision, d.etag, d.key()),
				http.StatusUnprocessableEntity, "validation-failed")
			for k, v := range c.want {
				if problem[k] != v {
					t.Fatalf("%s: problem %v; want %s %v", name, problem, k, v)
				}
			}
			d.wantNothing(c.name)
		}
		// A removed head: whatever the revision.
		mustExec(t, d.db, `UPDATE fragment SET head_revision_id = NULL, head_revision = 3 WHERE id = $1`, f.head)
		for _, revision := range []string{f.newer, own} {
			if problem := wantProblem(t, d.adopts("fragments", "ntp", revision, d.etag, d.key()),
				http.StatusUnprocessableEntity, "validation-failed"); problem["rule"] != "library-removed" {
				t.Fatalf("problem %v; want library-removed", problem)
			}
		}
		d.wantNothing("ntp")
		for _, body := range []string{`{}`, `{"revision":""}`, `{"revision":"` + other.newer + `","base":1}`, ``} {
			wantProblem(t, d.put("/adoptions/fragments/dns", body, d.etag, d.key()), http.StatusBadRequest, "invalid-request")
		}
		wantProblem(t, d.adopts("assignments", "dns", other.newer, d.etag, d.key()), http.StatusNotFound, "not-found")
		wantProblem(t, d.adopts("fragments", "Bad_Name", other.newer, d.etag, d.key()), http.StatusBadRequest, "invalid-request")
		wantProblem(t, d.put("/adoptions/fragments/dns?scope=library", `{"revision":"`+other.newer+`"}`, d.etag, d.key()),
			http.StatusBadRequest, "invalid-request")
		wantProblem(t, d.del("/adoptions/fragments/dns?scope=library", d.etag, d.key()), http.StatusBadRequest, "invalid-request")
		d.wantNothing("dns")
		// A refused entry replaces nothing.
		updated(t, &d.etag, d.adopts("fragments", "dns", other.older, d.etag, d.key()))
		wantProblem(t, d.adopts("fragments", "dns", f.older, d.etag, d.key()), http.StatusUnprocessableEntity, "validation-failed")
		if got, _ := d.stored("fragment", "dns", "library"); got.revision != other.older {
			t.Fatalf("stored %+v after a refused entry", got)
		}
	})
}

// A shared profile revision no publication wrote was never held to its pins, so it can pin a
// fragment name the library has no head for. Publication records each pin as a source of its
// fragment's head, so such a revision is refused when adopted. A head that was removed is still
// one: its pin publishes as written.
func TestLibraryAdoptionOfAProfilePin(t *testing.T) {
	published := func(d *draftEnv) *publishEnv {
		t.Helper()
		var rev int
		if err := d.db.QueryRow(`SELECT revision FROM draft WHERE id = $1`, d.draft).Scan(&rev); err != nil {
			t.Fatal(err)
		}
		return publicationOf(d, d.cluster, d.machine, d.draft, rev, "bw-artifact")
	}
	t.Run("a fragment name with no library head", func(t *testing.T) {
		d := newDraftEnv(t)
		p := &publishEnv{draftEnv: d}
		f := p.sharedFragment("ntp")
		p.sharedProfileHead("shared-set", p.sharedProfileRevision("shared-set", f.newer))
		unpublished := p.sharedProfileRevision("shared-set", f.older, p.sharedRevision("never"))
		rec := d.adopts("profiles", "shared-set", unpublished, d.etag, d.key())
		if rec.Code == http.StatusOK {
			// Where it is accepted, say what publication makes of it.
			next := published(d)
			next.compiled()
			rel, ref := next.commit()
			t.Fatalf("the adoption was accepted; its publication: release %q, refusal %v", rel, ref)
		}
		if problem := wantProblem(t, rec, http.StatusUnprocessableEntity, "validation-failed"); problem["path"] != "revision" ||
			problem["revision"] != unpublished || problem["fragment"] != "never" {
			t.Fatalf("problem %v; want the revision and the fragment never", problem)
		}
		d.wantNothing("shared-set")
	})
	t.Run("a removed fragment head", func(t *testing.T) {
		d := newDraftEnv(t)
		p := &publishEnv{draftEnv: d}
		f := p.sharedFragment("ntp")
		mustExec(t, d.db, `UPDATE fragment SET head_revision_id = NULL, head_revision = 3 WHERE id = $1`, f.head)
		prv := p.sharedProfileRevision("shared-set", f.older)
		p.sharedProfileHead("shared-set", prv)
		updated(t, &d.etag, d.adopts("profiles", "shared-set", prv, d.etag, d.key()))
		next := published(d)
		rel := next.publishedEntries()
		next.wantAdoption(d.cluster, "profile", "shared-set", prv, 1)
		next.wantSources(rel, "fragment", "ntp", sharedRow(f.older, "", 0))
	})
}

// PA §3.1.1 table, the adoption removal rows.
func TestLibraryAdoptionRemovalRoute(t *testing.T) {
	notFound := func(t *testing.T, d *draftEnv) {
		t.Helper()
		before, had := d.stored("fragment", "ntp", "library")
		wantProblem(t, d.unadopts("fragments", "ntp", d.etag, d.key()), http.StatusNotFound, "not-found")
		wantProblem(t, d.detaches("fragments", "ntp", d.etag, d.key()), http.StatusNotFound, "not-found")
		if after, has := d.stored("fragment", "ntp", "library"); after != before || has != had {
			t.Fatalf("slot %+v %t after the refusals; was %+v %t", after, has, before, had)
		}
		if _, own := d.stored("fragment", "ntp", "cluster"); own {
			t.Fatal("a refused detachment wrote a copy")
		}
	}
	t.Run("an adoption with a revision, the head removed", func(t *testing.T) {
		d := newDraftEnv(t)
		p := &publishEnv{draftEnv: d}
		f := p.sharedFragment("ntp")
		p.adopt("fragment", "ntp", f.head, f.older, 4)
		mustExec(t, d.db, `UPDATE fragment SET head_revision_id = NULL, head_revision = 3 WHERE id = $1`, f.head)
		e := updated(t, &d.etag, d.unadopts("fragments", "ntp", d.etag, d.key()))
		wantAdoptionEntry(t, e, "fragment", "ntp", f.head, 4, nil)
		if got, _ := d.stored("fragment", "ntp", "library"); got != (slot{base: counter(4), adoption: true}) {
			t.Fatalf("stored %+v", got)
		}
		if got := d.shownEntries("ntp"); len(got) != 1 {
			t.Fatalf("shown %v", got)
		} else {
			wantAdoptionEntry(t, got[0], "fragment", "ntp", f.head, 4, nil)
		}
		if n := count(t, d.db, `SELECT count(*) FROM act WHERE action = 'draft.adoption.remove'`); n != 1 {
			t.Fatalf("%d acts", n)
		}
	})
	t.Run("no adoption", func(t *testing.T) {
		d := newDraftEnv(t)
		(&publishEnv{draftEnv: d}).sharedFragment("ntp")
		notFound(t, d)
		// A shared removal in the slot introduces no adoption.
		updated(t, &d.etag, d.del("/fragments/ntp?scope=library", d.etag, d.key()))
		notFound(t, d)
	})
	// A published row with no revision: the staged re-adoption is not dropped (table row "adoption
	// removal", 404 on a row with no revision).
	t.Run("a removed adoption, a re-adoption staged", func(t *testing.T) {
		d := newDraftEnv(t)
		p := &publishEnv{draftEnv: d}
		f := p.sharedFragment("ntp")
		p.adopt("fragment", "ntp", f.head, nil, 2)
		notFound(t, d)
		updated(t, &d.etag, d.adopts("fragments", "ntp", f.older, d.etag, d.key()))
		notFound(t, d)
	})
	for name, introduce := range map[string]func(*testing.T, *draftEnv, sharedFragment){
		"an adoption the draft introduced": func(t *testing.T, d *draftEnv, f sharedFragment) {
			updated(t, &d.etag, d.adopts("fragments", "ntp", f.older, d.etag, d.key()))
		},
		// Reading (a): a shared edit of an existing head also introduces the cluster's adoption.
		"a shared edit by a cluster with no adoption": func(t *testing.T, d *draftEnv, f sharedFragment) {
			p := &publishEnv{draftEnv: d}
			p.share("fragment", "ntp", p.sharedRevision("ntp"), 2, nil)
		},
	} {
		t.Run(name+" is dropped", func(t *testing.T) {
			d := newDraftEnv(t)
			f := (&publishEnv{draftEnv: d}).sharedFragment("ntp")
			introduce(t, d, f)
			e := updated(t, &d.etag, d.unadopts("fragments", "ntp", d.etag, d.key()))
			wantAdoptionEntry(t, e, "fragment", "ntp", f.head, nil, nil)
			if got := d.shownEntries("ntp"); len(got) != 0 {
				t.Fatalf("entries %v; want the introducing entry dropped", got)
			}
			notFound(t, d)
		})
	}
	t.Run("a name the draft introduces is dropped", func(t *testing.T) {
		d := newDraftEnv(t)
		p := &publishEnv{draftEnv: d}
		p.share("fragment", "ntp", p.sharedRevision("ntp"), nil, nil)
		e := updated(t, &d.etag, d.unadopts("fragments", "ntp", d.etag, d.key()))
		wantAdoptionEntry(t, e, "fragment", "ntp", nil, nil, nil)
		wantProblem(t, d.del("/fragments/ntp?scope=library", d.etag, d.key()), http.StatusNotFound, "not-found")
		notFound(t, d)
	})
}

// PA §3.1.1 "Stopping" and the detachment rows.
func TestLibraryDetachmentRoute(t *testing.T) {
	copyOf := func(t *testing.T, d *draftEnv, rec *httptest.ResponseRecorder) (removal, own map[string]any) {
		t.Helper()
		b := decode[map[string]any](t, rec, http.StatusOK)
		d.etag = rec.Header().Get("ETag")
		return b["entry"].(map[string]any), b["copy"].(map[string]any)
	}
	t.Run("a fragment", func(t *testing.T) {
		d := newDraftEnv(t)
		p := &publishEnv{draftEnv: d}
		f := p.sharedFragment("ntp")
		p.adopt("fragment", "ntp", f.head, f.older, 4)
		// A re-adoption of another text is staged: the copy is of the published adoption's.
		staged := id.New(id.FragmentRevision)
		mustExec(t, d.db, `INSERT INTO fragment_revision (id, cluster, name, layer, document, author, embedded, created_at)
			VALUES ($1, NULL, 'ntp', 'cluster', 'machine: {env: {BW_STAGED: "1"}}', $2, '[]', now())`, staged, d.seed)
		updated(t, &d.etag, d.adopts("fragments", "ntp", staged, d.etag, d.key()))
		removal, own := copyOf(t, d, d.detaches("fragments", "ntp", d.etag, d.key()))
		wantAdoptionEntry(t, removal, "fragment", "ntp", f.head, 4, nil)
		frv, _ := own["revision"].(string)
		for member, want := range map[string]any{"kind": "fragment", "name": "ntp", "head": nil, "base": nil,
			"document": "machine: {}"} {
			wantMember(t, own, member, want)
		}
		if _, scoped := own["scope"]; scoped || frv == f.older || frv == staged {
			t.Fatalf("copy %v; want a new cluster revision", own)
		}
		if n := count(t, d.db, `SELECT count(*) FROM fragment_revision r, fragment_revision s, principal a WHERE r.id = $1
			AND s.id = $2 AND r.cluster = $3 AND r.name = 'ntp' AND r.layer = s.layer AND r.document = s.document
			AND r.embedded = s.embedded AND a.id = r.author AND a.sub = 'h-author'
			AND NOT EXISTS (SELECT FROM fragment_reference f WHERE f.revision = r.id)`, frv, f.older, d.cluster); n != 1 {
			t.Fatal("the copy is not the adopted revision's text, the caller's, with no reference")
		}
		if got, _ := d.stored("fragment", "ntp", "cluster"); got != (slot{revision: frv, detached: true}) {
			t.Fatalf("cluster entry %+v", got)
		}
		if got, _ := d.stored("fragment", "ntp", "library"); got != (slot{base: counter(4), adoption: true}) {
			t.Fatalf("shared slot %+v", got)
		}
		shown := d.shownEntries("ntp")
		if len(shown) != 2 {
			t.Fatalf("shown %v; want the cluster entry and the removal", shown)
		}
		wantMember(t, shown[0], "revision", frv)
		wantAdoptionEntry(t, shown[1], "fragment", "ntp", f.head, 4, nil)
		if n := count(t, d.db, `SELECT count(*) FROM act WHERE action = 'draft.adoption.detach'`); n != 1 {
			t.Fatalf("%d acts", n)
		}
		// A later write of the cluster's entry is the author's own, no detachment's copy.
		updated(t, &d.etag, d.del("/fragments/ntp", d.etag, d.key()))
		if got, _ := d.stored("fragment", "ntp", "cluster"); got != (slot{}) {
			t.Fatalf("cluster entry %+v after its removal", got)
		}
	})
	t.Run("a profile keeps its pins", func(t *testing.T) {
		d := newDraftEnv(t)
		p := &publishEnv{draftEnv: d}
		f := p.sharedFragment("ntp")
		g := p.sharedFragment("dns")
		prv := p.sharedProfileRevision("shared-set", f.older, g.newer)
		head := p.sharedProfileHead("shared-set", prv)
		p.adopt("profile", "shared-set", head, prv, 2)
		_, own := copyOf(t, d, d.detaches("profiles", "shared-set", d.etag, d.key()))
		wantMember(t, own, "kind", "profile")
		if _, has := own["document"]; has {
			t.Fatalf("copy %v: a profile has no document", own)
		}
		if n := count(t, d.db, `SELECT count(*) FROM profile_revision_fragment a JOIN profile_revision_fragment b
			ON b.revision = $2 AND b.position = a.position AND b.fragment_revision = a.fragment_revision
			WHERE a.revision = $1 AND a.library AND a.cluster = $3`, own["revision"], prv, d.cluster); n != 2 {
			t.Fatalf("%d pins copied", n)
		}
		// A later PUT of the cluster's profile clears the mark.
		if got, _ := d.stored("profile", "shared-set", "cluster"); !got.detached {
			t.Fatalf("cluster entry %+v", got)
		}
		updated(t, &d.etag, d.put("/profiles/shared-set", `{"fragments":["`+f.older+`"]}`, d.etag, d.key()))
		if got, _ := d.stored("profile", "shared-set", "cluster"); got.detached {
			t.Fatalf("cluster entry %+v; a later write keeps no detached mark", got)
		}
	})
	t.Run("an adoption the draft introduced: the copy alone", func(t *testing.T) {
		d := newDraftEnv(t)
		f := (&publishEnv{draftEnv: d}).sharedFragment("ntp")
		updated(t, &d.etag, d.adopts("fragments", "ntp", f.older, d.etag, d.key()))
		removal, own := copyOf(t, d, d.detaches("fragments", "ntp", d.etag, d.key()))
		wantAdoptionEntry(t, removal, "fragment", "ntp", f.head, nil, nil)
		if _, shared := d.stored("fragment", "ntp", "library"); shared {
			t.Fatal("the dropped adoption left an entry")
		}
		if got, _ := d.stored("fragment", "ntp", "cluster"); got != (slot{revision: own["revision"].(string), detached: true}) {
			t.Fatalf("cluster entry %+v", got)
		}
		// The adoption is gone: another removal finds none, and the copy stays.
		wantProblem(t, d.unadopts("fragments", "ntp", d.etag, d.key()), http.StatusNotFound, "not-found")
		if _, ok := d.stored("fragment", "ntp", "cluster"); !ok {
			t.Fatal("the copy was dropped")
		}
	})
	t.Run("the cluster's own head keeps its layer", func(t *testing.T) {
		d := newDraftEnv(t)
		p := &publishEnv{draftEnv: d}
		f := p.sharedFragment("ntp")
		p.adopt("fragment", "ntp", f.head, f.older, 1)
		d.fragmentHead("ntp", "site", nil, 2)
		if problem := wantProblem(t, d.detaches("fragments", "ntp", d.etag, d.key()),
			http.StatusUnprocessableEntity, "validation-failed"); problem["path"] != "layer" || problem["name"] != "ntp" {
			t.Fatalf("problem %v; want the layer of ntp", problem)
		}
		d.wantNothing("ntp")
	})
}

// PA §3.1.1 "One slot per name": whichever entry is written second stands alone in the shared
// slot, and a detachment's copy keeps its own.
func TestLibraryOneSlot(t *testing.T) {
	type write struct {
		do func(d *draftEnv, f sharedFragment) *httptest.ResponseRecorder
		is func(d *draftEnv, f sharedFragment, s slot) bool
	}
	// A shared fragment edit is only ever the first write here, seeded: its route ingests.
	// TestLibraryProfileSlot writes a shared edit, a profile's, through its route over the others.
	const edit = "a shared edit"
	writes := map[string]write{
		"a shared removal": {
			func(d *draftEnv, f sharedFragment) *httptest.ResponseRecorder {
				return d.del("/fragments/ntp?scope=library", d.etag, d.key())
			},
			func(_ *draftEnv, _ sharedFragment, s slot) bool { return s == slot{base: counter(2)} },
		},
		"an adoption entry": {
			func(d *draftEnv, f sharedFragment) *httptest.ResponseRecorder {
				return d.adopts("fragments", "ntp", f.newer, d.etag, d.key())
			},
			func(_ *draftEnv, f sharedFragment, s slot) bool {
				return s == slot{revision: f.newer, base: counter(4), adoption: true}
			},
		},
		"an adoption removal": {
			func(d *draftEnv, _ sharedFragment) *httptest.ResponseRecorder {
				return d.unadopts("fragments", "ntp", d.etag, d.key())
			},
			func(_ *draftEnv, _ sharedFragment, s slot) bool { return s == slot{base: counter(4), adoption: true} },
		},
		"a detachment": {
			func(d *draftEnv, _ sharedFragment) *httptest.ResponseRecorder {
				return d.detaches("fragments", "ntp", d.etag, d.key())
			},
			func(d *draftEnv, _ sharedFragment, s slot) bool {
				own, ok := d.stored("fragment", "ntp", "cluster")
				return s == slot{base: counter(4), adoption: true} && ok && own.detached
			},
		},
	}
	firsts := []string{edit}
	for name := range writes {
		firsts = append(firsts, name)
	}
	for _, first := range firsts {
		for second := range writes {
			t.Run(first+", then "+second, func(t *testing.T) {
				d := newDraftEnv(t)
				p := &publishEnv{draftEnv: d}
				f := p.sharedFragment("ntp")
				p.adopt("fragment", "ntp", f.head, f.older, 4)
				if first == edit {
					p.share("fragment", "ntp", p.sharedRevision("ntp"), 2, 4)
				} else {
					updated(t, &d.etag, writes[first].do(d, f))
				}
				updated(t, &d.etag, writes[second].do(d, f))
				got, ok := d.stored("fragment", "ntp", "library")
				if !ok || !writes[second].is(d, f, got) || got.adoptionBase.Valid {
					t.Fatalf("shared slot %+v %t", got, ok)
				}
				if n := count(t, d.db, `SELECT count(*) FROM draft_source_entry WHERE draft = $1 AND name = 'ntp' AND scope = 'library'`,
					d.draft); n != 1 {
					t.Fatalf("%d shared entries", n)
				}
				_, copied := d.stored("fragment", "ntp", "cluster")
				if want := first == "a detachment" || second == "a detachment"; copied != want {
					t.Fatalf("cluster entry %t; want %t", copied, want)
				}
			})
		}
	}
}

// The shared edit through its route over each other entry: a profile's, which needs no ingestion.
func TestLibraryProfileSlot(t *testing.T) {
	for name, first := range map[string]func(*draftEnv, string) *httptest.ResponseRecorder{
		"an adoption entry": func(d *draftEnv, prv string) *httptest.ResponseRecorder {
			return d.adopts("profiles", "shared-set", prv, d.etag, d.key())
		},
		"an adoption removal": func(d *draftEnv, _ string) *httptest.ResponseRecorder {
			return d.unadopts("profiles", "shared-set", d.etag, d.key())
		},
		"a detachment": func(d *draftEnv, _ string) *httptest.ResponseRecorder {
			return d.detaches("profiles", "shared-set", d.etag, d.key())
		},
	} {
		t.Run(name+", then a shared edit", func(t *testing.T) {
			d := newDraftEnv(t)
			p := &publishEnv{draftEnv: d}
			f := p.sharedFragment("ntp")
			prv := p.sharedProfileRevision("shared-set", f.older)
			head := p.sharedProfileHead("shared-set", prv)
			p.adopt("profile", "shared-set", head, prv, 4)
			rec := first(d, prv)
			d.etag = rec.Header().Get("ETag")
			e := updated(t, &d.etag, d.put("/profiles/shared-set", `{"scope":"library","fragments":["`+f.newer+`"]}`, d.etag, d.key()))
			want := slot{revision: e["revision"].(string), base: counter(1), adoptionBase: counter(4)}
			if got, _ := d.stored("profile", "shared-set", "library"); got != want {
				t.Fatalf("shared slot %+v; want %+v", got, want)
			}
		})
	}
}

// The three routes meet the draft checks of any draft update, and a lost answer is replayed under
// its key (§3.1, §7.2).
func TestLibraryAdoptionDraftChecks(t *testing.T) {
	d := newDraftEnv(t)
	p := &publishEnv{draftEnv: d}
	f := p.sharedFragment("ntp")
	p.adopt("fragment", "ntp", f.head, f.older, 4)
	writes := map[string]func(ifMatch, k string) *httptest.ResponseRecorder{
		"adopt":  func(m, k string) *httptest.ResponseRecorder { return d.adopts("fragments", "ntp", f.newer, m, k) },
		"remove": func(m, k string) *httptest.ResponseRecorder { return d.unadopts("fragments", "ntp", m, k) },
		"detach": func(m, k string) *httptest.ResponseRecorder { return d.detaches("fragments", "ntp", m, k) },
	}
	for _, name := range []string{"adopt", "remove", "detach"} {
		stale, key := d.etag, d.key()
		first := writes[name](stale, key)
		if first.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", name, first.Code, first.Body)
		}
		d.etag = first.Header().Get("ETag")
		copies := count(t, d.db, `SELECT count(*) FROM fragment_revision WHERE cluster = $1`, d.cluster)
		again := writes[name](stale, key)
		if again.Code != http.StatusOK || again.Header().Get("Idempotent-Replayed") != "true" || again.Body.String() != first.Body.String() {
			t.Fatalf("%s: replay %d %s", name, again.Code, again.Body)
		}
		if n := count(t, d.db, `SELECT count(*) FROM fragment_revision WHERE cluster = $1`, d.cluster); n != copies {
			t.Fatalf("%s: the replay wrote another revision", name)
		}
		// Another request under the key: another If-Match.
		wantProblem(t, writes[name](d.etag, key), http.StatusUnprocessableEntity, "idempotency-key-reused")
		wantProblem(t, writes[name](stale, d.key()), http.StatusPreconditionFailed, "precondition-failed")
	}
	var epoch string
	if err := d.db.QueryRow(`SELECT epoch FROM installation_state`).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	var rev int
	if err := d.db.QueryRow(`SELECT revision FROM draft WHERE id = $1`, d.draft).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	op := id.New(id.Operation)
	mustExec(t, d.db, `INSERT INTO operation (id, kind, state, draft, draft_revision, created_by, created_by_kind, created_role,
			epoch, created_at)
		VALUES ($1, 'publish', 'queued', $2, $5, $3, 'human', 'publisher', $4, now())`, op, d.draft, d.seed, epoch, rev)
	for name, write := range writes {
		if problem := wantProblem(t, write(d.etag, d.key()), http.StatusConflict, "conflict"); problem["operation"] != op {
			t.Fatalf("%s: problem %v", name, problem)
		}
	}
}

// Criterion 1, by the route: a shared change published by one cluster is in no release of another
// until that cluster's own draft adopts it.
func TestLibraryAdoptionReachesNoClusterUnseen(t *testing.T) {
	d := newDraftEnv(t)
	other := d.createCluster(d.api, d.human("h-author"), "k-cluster-other-012345")
	rec := d.do(d.api, machineCall(d.human("h-author"), "k-machine-other-012345", other, "1b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b"))
	otherMachine := decode[machineBody](t, rec, http.StatusCreated).ID
	author := bare(d, d.cluster, d.machine, "k-draft-author-012345", "bw-artifact")
	f := author.sharedFragment("ntp")
	next := author.sharedRevision("ntp")
	author.share("fragment", "ntp", next, 2, nil)

	// B follows the older revision, its machine selecting the name.
	b := *d
	b.cluster, b.machine = other, otherMachine
	bp := &publishEnv{draftEnv: &b}
	bp.adopt("fragment", "ntp", f.head, f.older, 1)
	asr := bp.assignmentRevision(nil, "cluster/ntp")
	mustExec(t, d.db, `INSERT INTO assignment (id, cluster, machine, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, $2, $3, $4, 1, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, id.New(id.Assignment), other, otherMachine, asr)
	author.publishedEntries()

	// drafted is a new draft of B with the entries write puts in it, covered by a base of its own.
	drafted := func(k string, write func(e *draftEnv)) *publishEnv {
		rec := d.do(d.api, call{method: "POST", path: prefix + "/drafts", token: d.human("h-author"), key: "k-draft-b-" + k,
			body: `{"cluster":"` + other + `","title":"b"}`})
		e := b
		e.draft, e.etag = decode[draftBody](t, rec, http.StatusCreated).ID, rec.Header().Get("ETag")
		write(&e)
		var rev int
		if err := d.db.QueryRow(`SELECT revision FROM draft WHERE id = $1`, e.draft).Scan(&rev); err != nil {
			t.Fatal(err)
		}
		p := publicationOf(&e, other, otherMachine, e.draft, rev, "bw-artifact-"+k)
		p.unit.machines[0].assignment = asr
		return p
	}
	unseen := drafted("unseen-0123456", func(*draftEnv) {})
	if got := unseen.compiled().machines[0].fragments; !slices.Contains(got, f.older) || slices.Contains(got, next) {
		t.Fatalf("fragments %v; want the adopted %s alone", got, f.older)
	}
	seen := drafted("adopts-0123456", func(e *draftEnv) {
		updated(t, &e.etag, e.adopts("fragments", "ntp", next, e.etag, e.key()))
	})
	if got := seen.compiled().machines[0].fragments; !slices.Contains(got, next) || slices.Contains(got, f.older) {
		t.Fatalf("fragments %v; want %s after the adoption", got, next)
	}
	rel := seen.published()
	seen.wantDigest(rel)
	seen.wantSources(rel, "fragment", "ntp", sharedRow(next, f.older, 2))
}

// Contract 10: a revision an adoption entry of the release's draft names counts as introduced by
// it, so its author approving the plan authored a change.
func TestLibraryAdoptionSelfApproval(t *testing.T) {
	p := newPublishEnv(t)
	seed, approver := p.seed, p.principalOf("h-approver")
	p.seed = approver
	f := p.sharedFragment("ntp")
	p.seed = seed
	p.adoptionEntry("fragment", "ntp", f.older, nil)
	p.selectToo(nil, "cluster/ntp")
	rel := p.publishedEntries()
	mustExec(t, p.db, `UPDATE machine_state s SET applied_release = $2, applied_digest = m.configuration_digest, applied_source = 'adoption',
		baseline_revision = 4 FROM release_machine m WHERE s.machine = $1 AND m.release = $2 AND m.machine = $1`, p.machine, rel)
	plan := decode[planBody](t, p.plan(p.human("h-publisher"), p.key(), applyBody(rel, p.machine, "")), http.StatusCreated)
	got := decode[approvalBody](t, p.do(p.api, call{method: "POST", path: prefix + "/plans/" + plan.ID + "/approvals",
		token: p.human("h-approver"), key: p.key(), body: `{}`}), http.StatusCreated)
	if !slices.Equal(got.SelfApproval.Reasons, []string{"authored-change"}) {
		t.Fatalf("self-approval %+v; want authored-change alone", got.SelfApproval)
	}
}
