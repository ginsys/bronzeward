package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

func (d *draftEnv) get(path string) *httptest.ResponseRecorder {
	return d.do(d.api, call{method: "GET", path: prefix + path, token: d.human("h-viewer")})
}

// PA §9.2: any role reads heads and revisions. A head answers its head revision and token as its
// ETag; its revisions are every revision of its name (or machine), published or not, in
// identifier order; a revision answers its content. An unknown identifier, or one of another
// entity, is 404.
func TestSourceReads(t *testing.T) {
	d := newDraftEnv(t)
	old := d.fragmentRevision(d.cluster, "registries", "override")
	frv := d.fragmentRevision(d.cluster, "registries", "override")
	siteDNS := d.fragmentRevision(d.cluster, "site-dns", "site")
	frg := d.fragmentHead("registries", "override", frv, 2)
	removed := d.fragmentHead("gone", "site", nil, 3)
	prf := d.profileHead("workers", 1)
	var prv string
	if err := d.db.QueryRow(`SELECT head_revision_id FROM profile WHERE id = $1`, prf).Scan(&prv); err != nil {
		t.Fatal(err)
	}
	mustExec(t, d.db, `INSERT INTO profile_revision_fragment (revision, cluster, position, fragment_revision) VALUES ($1, $2, 0, $3), ($1, $2, 1, $4)`,
		prv, d.cluster, frv, siteDNS)
	asr, asg := id.New(id.AssignmentRevision), id.New(id.Assignment)
	mustExec(t, d.db, `INSERT INTO assignment_revision (id, cluster, machine, author, created_at) VALUES ($1, $2, $3, $4, now())`, asr, d.cluster, d.machine, d.seed)
	mustExec(t, d.db, `INSERT INTO assignment_revision_profile (revision, position, profile) VALUES ($1, 0, 'workers')`, asr)
	mustExec(t, d.db, `INSERT INTO assignment_revision_fragment (revision, layer, position, fragment) VALUES ($1, 'override', 0, 'registries')`, asr)
	mustExec(t, d.db, `INSERT INTO assignment (id, cluster, machine, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, $2, $3, $4, 1, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, asg, d.cluster, d.machine, asr)

	rec := d.get("/fragments/" + frg)
	h := decode[fragmentBody](t, rec, http.StatusOK)
	if h.ID != frg || h.Cluster != d.cluster || h.Scope != "cluster" || h.Name != "registries" || h.Layer != "override" ||
		h.Revision == nil || *h.Revision != frv || h.HeadRevision != 2 || h.CreatedAt.IsZero() ||
		rec.Header().Get("ETag") != `"2-m3oxmlfh6phr7aigshdydcb4ji"` {
		t.Fatalf("fragment %+v, ETag %q", h, rec.Header().Get("ETag"))
	}
	if g := decode[fragmentBody](t, d.get("/fragments/"+removed), http.StatusOK); g.Revision != nil || g.HeadRevision != 3 {
		t.Fatalf("removed fragment %+v", g)
	}
	list := decode[listPage[fragmentBody]](t, d.get("/fragments"), http.StatusOK)
	if len(list.Items) != 2 {
		t.Fatalf("fragments %+v", list)
	}
	revs := decode[listPage[fragmentRevisionBody]](t, d.get("/fragments/"+frg+"/revisions"), http.StatusOK)
	want := []string{old, frv}
	slices.Sort(want)
	if len(revs.Items) != 2 || revs.Items[0].ID != want[0] || revs.Items[1].ID != want[1] {
		t.Fatalf("revisions %+v; want %v", revs, want)
	}
	page1 := decode[listPage[fragmentRevisionBody]](t, d.get("/fragments/"+frg+"/revisions?limit=1"), http.StatusOK)
	if len(page1.Items) != 1 || page1.Next == "" {
		t.Fatalf("first page %+v", page1)
	}
	r := decode[fragmentRevisionBody](t, d.get("/fragment-revisions/"+frv), http.StatusOK)
	if r.ID != frv || r.Cluster != d.cluster || r.Name != "registries" || r.Layer != "override" || r.Document != "machine: {}" ||
		r.Author != d.seed || r.CreatedAt.IsZero() {
		t.Fatalf("fragment revision %+v", r)
	}

	p := decode[profileBody](t, d.get("/profiles/"+prf), http.StatusOK)
	if p.ID != prf || p.Name != "workers" || p.Revision == nil || *p.Revision != prv || p.HeadRevision != 1 {
		t.Fatalf("profile %+v", p)
	}
	pr := decode[profileRevisionBody](t, d.get("/profile-revisions/"+prv), http.StatusOK)
	if pr.ID != prv || pr.Name != "workers" || !slices.Equal(pr.Fragments, []string{frv, siteDNS}) {
		t.Fatalf("profile revision %+v", pr)
	}
	if l := decode[listPage[profileRevisionBody]](t, d.get("/profiles/"+prf+"/revisions"), http.StatusOK); len(l.Items) != 1 {
		t.Fatalf("profile revisions %+v", l)
	}

	a := decode[assignmentBody](t, d.get("/assignments/"+asg), http.StatusOK)
	if a.ID != asg || a.Machine != d.machine || a.Revision == nil || *a.Revision != asr || a.HeadRevision != 1 {
		t.Fatalf("assignment %+v", a)
	}
	ar := decode[assignmentRevisionBody](t, d.get("/assignment-revisions/"+asr), http.StatusOK)
	if ar.Machine != d.machine || !slices.Equal(ar.Profiles, []string{"workers"}) || len(ar.Fragments) != 1 ||
		!slices.Equal(ar.Fragments["override"], []string{"registries"}) {
		t.Fatalf("assignment revision %+v", ar)
	}
	if l := decode[listPage[assignmentBody]](t, d.get("/assignments"), http.StatusOK); len(l.Items) != 1 {
		t.Fatalf("assignments %+v", l)
	}
	if l := decode[listPage[assignmentRevisionBody]](t, d.get("/assignments/"+asg+"/revisions"), http.StatusOK); len(l.Items) != 1 {
		t.Fatalf("assignment revisions %+v", l)
	}

	for _, path := range []string{"/fragments/" + id.New(id.Fragment), "/fragments/" + prf, "/fragment-revisions/" + prv,
		"/fragments/" + id.New(id.Fragment) + "/revisions", "/profiles/" + frg + "/revisions", "/profile-revisions/" + id.New(id.ProfileRevision),
		"/assignments/" + id.New(id.Assignment), "/assignment-revisions/" + frv} {
		wantProblem(t, d.get(path), http.StatusNotFound, "not-found")
	}
}

// PA §3.1, §9.3: a draft read lists its source entries after its import base entries, fragments,
// then profiles, then assignments, each by name or machine, in the update routes' entry shape.
func TestDraftReadShowsSourceEntries(t *testing.T) {
	d := newDraftEnv(t)
	frv := d.fragmentRevision(d.cluster, "registries", "override")
	frg := d.fragmentHead("registries", "override", frv, 2)
	d.profileHead("workers", 1)
	prv := d.ok(d.put("/profiles/storage", `{"fragments":["`+frv+`"]}`, d.etag, d.key())).Revision
	asr := d.ok(d.put("/assignments/"+d.machine, `{"profiles":["workers"]}`, d.etag, d.key())).Revision
	d.ok(d.del("/fragments/registries", d.etag, d.key()))
	d.ok(d.del("/profiles/workers", d.etag, d.key()))

	rec := d.get("/drafts/" + d.draft)
	if rec.Code != http.StatusOK || rec.Header().Get("ETag") != d.etag {
		t.Fatalf("draft read %d, ETag %q; want %q", rec.Code, rec.Header().Get("ETag"), d.etag)
	}
	var b struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if len(b.Entries) != 4 {
		t.Fatalf("entries %v", b.Entries)
	}
	want := []map[string]any{
		{"kind": "fragment", "name": "registries", "head": frg, "base": float64(2), "revision": nil},
		{"kind": "profile", "name": "storage", "head": nil, "base": nil, "revision": *prv},
		{"kind": "profile", "name": "workers", "head": b.Entries[2]["head"], "base": float64(1), "revision": nil},
		{"kind": "assignment", "machine": d.machine, "head": nil, "base": nil, "revision": *asr},
	}
	for i, w := range want {
		if len(b.Entries[i]) != len(w) {
			t.Errorf("entry %d: %v; want %v", i, b.Entries[i], w)
			continue
		}
		for k, v := range w {
			if b.Entries[i][k] != v {
				t.Errorf("entry %d: %v; want %v", i, b.Entries[i], w)
			}
		}
	}
	if h, _ := b.Entries[2]["head"].(string); id.MustHave(h, id.Profile) != nil {
		t.Errorf("removal of a head names no head: %v", b.Entries[2])
	}
	list := decode[listPage[map[string]any]](t, d.get("/drafts"), http.StatusOK)
	if len(list.Items) != 1 || len(list.Items[0]["entries"].([]any)) != 4 {
		t.Fatalf("draft list %v", list)
	}
}
