package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// draftEnv is a cluster with one machine, an open draft at revision 1 and a seeded author for the
// revisions a test inserts directly: no route writes a fragment revision before ginsys/bronzeward#23
// PR 1b, and no route writes a head before publication.
type draftEnv struct {
	*env
	cluster, machine, draft, etag, seed string
	keys                                int
}

func newDraftEnv(t *testing.T) *draftEnv {
	t.Helper()
	e := newEnv(t, options{})
	d := &draftEnv{env: e, seed: id.New(id.Principal)}
	d.cluster = e.createCluster(e.api, e.human("h-author"), "k-cluster-0123456789")
	rec := e.do(e.api, machineCall(e.human("h-author"), "k-machine-0123456789", d.cluster, "0b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b"))
	d.machine = decode[machineBody](t, rec, http.StatusCreated).ID
	rec = e.do(e.api, call{method: "POST", path: prefix + "/drafts", token: e.human("h-author"), key: "k-draft-0123456789",
		body: `{"cluster":"` + d.cluster + `","title":"workers"}`})
	d.draft, d.etag = decode[draftBody](t, rec, http.StatusCreated).ID, rec.Header().Get("ETag")
	mustExec(t, e.db, `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', 'https://idp.test', 'seed', now())`, d.seed)
	return d
}

// key returns a fresh Idempotency-Key.
func (d *draftEnv) key() string {
	d.keys++
	return "k-draft-entry-" + string(rune('a'+d.keys/26)) + string(rune('a'+d.keys%26)) + "-0123456789"
}

// fragmentRevision inserts a fragment revision of cluster, as a draft update would.
func (d *draftEnv) fragmentRevision(cluster, name, layer string) string {
	d.t.Helper()
	frv := id.New(id.FragmentRevision)
	mustExec(d.t, d.db, `INSERT INTO fragment_revision (id, cluster, name, layer, document, author, created_at)
		VALUES ($1, $2, $3, $4, 'machine: {}', $5, now())`, frv, cluster, name, layer, d.seed)
	return frv
}

// fragmentHead inserts a head at revision rev (NULL for a removed one), as publication would.
func (d *draftEnv) fragmentHead(name, layer string, rev any, headRevision int) string {
	d.t.Helper()
	frg := id.New(id.Fragment)
	mustExec(d.t, d.db, `INSERT INTO fragment (id, cluster, scope, name, layer, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, $2, 'cluster', $3, $4, $5, $6, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, frg, d.cluster, name, layer, rev, headRevision)
	return frg
}

func (d *draftEnv) put(part, body, ifMatch, k string) *httptest.ResponseRecorder {
	return d.do(d.api, call{method: "PUT", path: prefix + "/drafts/" + d.draft + part, token: d.human("h-author"), key: k,
		ifMatch: ifMatch, body: body})
}

func (d *draftEnv) del(part, ifMatch, k string) *httptest.ResponseRecorder {
	return d.do(d.api, call{method: "DELETE", path: prefix + "/drafts/" + d.draft + part, token: d.human("h-author"), key: k,
		ifMatch: ifMatch})
}

// ok checks a 200 draft update, moves the env's ETag to the answer's and returns the entry.
func (d *draftEnv) ok(rec *httptest.ResponseRecorder) sourceEntry {
	d.t.Helper()
	b := decode[sourceUpdate](d.t, rec, http.StatusOK)
	if b.Draft != d.draft || rec.Header().Get("ETag") == "" || rec.Header().Get("ETag") == d.etag {
		d.t.Fatalf("update answer %+v, ETag %q after %q", b, rec.Header().Get("ETag"), d.etag)
	}
	d.etag = rec.Header().Get("ETag")
	return b.Entry
}

// PA §3.1, T1: a draft update needs If-Match (428) equal to the draft's ETag (412), on an open draft
// (409) with no publication queued or running (409 naming it); once that operation fails, the
// draft takes edits again. Nothing is written by a refusal.
func TestDraftUpdatePreconditions(t *testing.T) {
	d := newDraftEnv(t)
	frv := d.fragmentRevision(d.cluster, "registries", "override")
	body := `{"fragments":["` + frv + `"]}`
	d.fragmentHead("registries", "override", frv, 1)
	wantProblem(t, d.put("/profiles/workers", body, "", d.key()), http.StatusPreconditionRequired, "precondition-required")
	wantProblem(t, d.put("/profiles/workers", body, `"1-aaaaaaaaaaaaaaaaaaaaaaaaaa"`, d.key()), http.StatusPreconditionFailed, "precondition-failed")
	for _, state := range []string{"queued", "running"} {
		op := id.New(id.Operation)
		// A running job has an owner and a lease (0004); a queued one has neither.
		mustExec(t, d.db, `INSERT INTO operation (id, kind, state, epoch, owner, owner_gen, owner_epoch, lease_until, draft,
			draft_revision, created_by, created_by_kind, created_role, created_at)
			SELECT $1, 'publish', $2, epoch, CASE WHEN $2 = 'running' THEN 'run-1/4242/pub' END, CASE WHEN $2 = 'running' THEN 1 ELSE 0 END,
			CASE WHEN $2 = 'running' THEN epoch END, CASE WHEN $2 = 'running' THEN now() + interval '1 minute' END,
			$3, 1, $4, 'human', 'publisher', now() FROM installation_state`, op, state, d.draft, d.seed)
		doc := wantProblem(t, d.put("/profiles/workers", body, d.etag, d.key()), http.StatusConflict, "conflict")
		if doc["operation"] != op {
			t.Errorf("%s publication: %v; want it named", state, doc)
		}
		wantProblem(t, d.del("/profiles/workers", d.etag, d.key()), http.StatusConflict, "conflict")
		mustExec(t, d.db, `UPDATE operation SET state = 'failed', error = '{}'::jsonb WHERE id = $1`, op)
	}
	if n := count(t, d.db, `SELECT count(*) FROM profile_revision`) + count(t, d.db, `SELECT count(*) FROM draft_source_entry`); n != 0 {
		t.Fatalf("refusals wrote %d rows", n)
	}
	d.ok(d.put("/profiles/workers", body, d.etag, d.key()))
	mustExec(t, d.db, `UPDATE draft SET state = 'discarded' WHERE id = $1`, d.draft)
	wantProblem(t, d.put("/profiles/workers", body, d.etag, d.key()), http.StatusConflict, "conflict")
	wantProblem(t, d.do(d.api, call{method: "PUT", path: prefix + "/drafts/" + id.New(id.Draft) + "/profiles/workers",
		token: d.human("h-author"), key: d.key(), ifMatch: d.etag, body: body}), http.StatusNotFound, "not-found")
}

// PA §9.2: DELETE takes no body; any body is refused before anything runs.
func TestDraftRemovalTakesNoBody(t *testing.T) {
	d := newDraftEnv(t)
	rec := d.do(d.api, call{method: "DELETE", path: prefix + "/drafts/" + d.draft + "/profiles/workers", token: d.human("h-author"),
		key: d.key(), ifMatch: d.etag, body: `{}`})
	wantProblem(t, rec, http.StatusBadRequest, "invalid-request")
}

// PA §3.1, §9.3, choice §17.31: a profile pins fragment revisions, each its fragment's head
// revision or the one this draft proposes; anything else is 422 naming the body path. An update
// stores a revision and the draft's entry, advances the draft and answers its ETag; a replay
// answers the same; the same key with another body is 422.
func TestProfileUpdate(t *testing.T) {
	d := newDraftEnv(t)
	old := d.fragmentRevision(d.cluster, "registries", "override")
	head := d.fragmentRevision(d.cluster, "registries", "override")
	d.fragmentHead("registries", "override", head, 2)
	proposed := d.fragmentRevision(d.cluster, "site-dns", "site")
	elsewhere := d.fragmentRevision(d.cluster, "unproposed", "site")

	for name, pin := range map[string]string{"superseded revision": old, "revision no draft entry proposes": elsewhere,
		"no such revision": id.New(id.FragmentRevision)} {
		doc := wantProblem(t, d.put("/profiles/workers", `{"fragments":["`+head+`","`+pin+`"]}`, d.etag, d.key()),
			http.StatusUnprocessableEntity, "validation-failed")
		if doc["path"] != "fragments[1]" {
			t.Errorf("%s: %v; want fragments[1] named", name, doc)
		}
	}
	mustExec(t, d.db, `INSERT INTO draft_source_entry (draft, cluster, kind, name, fragment_revision) VALUES ($1, $2, 'fragment', 'site-dns', $3)`,
		d.draft, d.cluster, proposed)
	// With an entry for its fragment, the entry decides: another revision of the name, or the head
	// of a fragment this draft removes, is refused like the cases above.
	other := d.fragmentRevision(d.cluster, "site-dns", "site")
	removed := d.fragmentRevision(d.cluster, "ntp", "site")
	d.fragmentHead("ntp", "site", removed, 1)
	mustExec(t, d.db, `INSERT INTO draft_source_entry (draft, cluster, kind, name, base) VALUES ($1, $2, 'fragment', 'ntp', 1)`, d.draft, d.cluster)
	for name, pin := range map[string]string{"revision other than the proposed one": other, "head the draft removes": removed} {
		doc := wantProblem(t, d.put("/profiles/workers", `{"fragments":["`+head+`","`+pin+`"]}`, d.etag, d.key()),
			http.StatusUnprocessableEntity, "validation-failed")
		if doc["path"] != "fragments[1]" {
			t.Errorf("%s: %v; want fragments[1] named", name, doc)
		}
	}
	k := d.key()
	body := `{"fragments":["` + head + `","` + proposed + `"]}`
	rec := d.put("/profiles/workers", body, d.etag, k)
	before := d.etag
	e := d.ok(rec)
	if e.Kind != "profile" || e.Name != "workers" || e.Head != nil || e.Base != nil || e.Revision == nil {
		t.Fatalf("entry %+v", e)
	}
	var pins []string
	rows, err := d.db.Query(`SELECT fragment_revision FROM profile_revision_fragment WHERE revision = $1 ORDER BY position`, *e.Revision)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		pins = append(pins, p)
	}
	if len(pins) != 2 || pins[0] != head || pins[1] != proposed {
		t.Fatalf("pins %v", pins)
	}
	replay := d.put("/profiles/workers", body, before, k)
	if replay.Code != http.StatusOK || replay.Body.String() != rec.Body.String() || replay.Header().Get("ETag") != d.etag {
		t.Fatalf("replay %d %s", replay.Code, replay.Body)
	}
	wantProblem(t, d.put("/profiles/workers", `{"fragments":["`+head+`"]}`, before, k), http.StatusUnprocessableEntity, "idempotency-key-reused")
	if n := count(t, d.db, `SELECT count(*) FROM profile_revision`); n != 1 {
		t.Fatalf("%d profile revisions", n)
	}
	// A name with a head takes the head's revision as its base. A second update replaces the entry
	// and keeps the base it was edited from, even after a head appears for a name the draft
	// introduced: publication then refuses it as stale (§4.2) instead of overwriting that head.
	prf := id.New(id.Profile)
	mustExec(t, d.db, `INSERT INTO profile (id, cluster, scope, name, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, $2, 'cluster', $3, NULL, 3, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, prf, d.cluster, "storage")
	e = d.ok(d.put("/profiles/storage", `{"fragments":["`+head+`"]}`, d.etag, d.key()))
	if e.Head == nil || *e.Head != prf || e.Base == nil || *e.Base != 3 {
		t.Fatalf("entry over a head %+v", e)
	}
	late := id.New(id.Profile)
	mustExec(t, d.db, `INSERT INTO profile (id, cluster, scope, name, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, $2, 'cluster', 'workers', NULL, 5, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, late, d.cluster)
	e = d.ok(d.put("/profiles/workers", `{"fragments":["`+head+`"]}`, d.etag, d.key()))
	if e.Head == nil || *e.Head != late || e.Base != nil {
		t.Fatalf("second update after a head appeared %+v; want the base kept absent", e)
	}
	if n := count(t, d.db, `SELECT count(*) FROM draft_source_entry WHERE kind = 'profile'`); n != 2 {
		t.Fatalf("%d profile entries", n)
	}
	if n := count(t, d.db, `SELECT count(*) FROM act WHERE action = 'draft.profile.update'`); n != 3 {
		t.Fatalf("%d acts", n)
	}
	wantProblem(t, d.do(d.api, call{method: "PUT", path: prefix + "/drafts/" + d.draft + "/profiles/workers", token: d.human("h-viewer"),
		key: d.key(), ifMatch: d.etag, body: body}), http.StatusForbidden, "forbidden")
}

// The profile body: 1 to 256 fragment revision identifiers, none repeated; a name per choice §17.31.
func TestProfileUpdateRefusals(t *testing.T) {
	d := newDraftEnv(t)
	frv := d.fragmentRevision(d.cluster, "registries", "override")
	for name, c := range map[string]struct{ part, body string }{
		"no fragments":        {"/profiles/workers", `{"fragments":[]}`},
		"repeated fragment":   {"/profiles/workers", `{"fragments":["` + frv + `","` + frv + `"]}`},
		"not a revision id":   {"/profiles/workers", `{"fragments":["frg_rgkebwvneg6mxhid62gec5difi"]}`},
		"unknown member":      {"/profiles/workers", `{"fragments":["` + frv + `"],"layer":"site"}`},
		"name with a capital": {"/profiles/Workers", `{"fragments":["` + frv + `"]}`},
	} {
		if rec := d.put(c.part, c.body, d.etag, d.key()); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s; want 400", name, rec.Code, rec.Body)
		}
	}
}

// PA §3.1 rule 8: a removal is an entry with no revision and the head's revision as base; of a
// name only this draft introduces it leaves base absent; of a name with neither, 404.
func TestProfileRemoval(t *testing.T) {
	d := newDraftEnv(t)
	frv := d.fragmentRevision(d.cluster, "registries", "override")
	d.fragmentHead("registries", "override", frv, 1)
	wantProblem(t, d.del("/profiles/workers", d.etag, d.key()), http.StatusNotFound, "not-found")
	d.ok(d.put("/profiles/workers", `{"fragments":["`+frv+`"]}`, d.etag, d.key()))
	e := d.ok(d.del("/profiles/workers", d.etag, d.key()))
	if e.Kind != "profile" || e.Name != "workers" || e.Revision != nil || e.Base != nil || e.Head != nil {
		t.Fatalf("removal of an introduced name %+v", e)
	}
	prv := id.New(id.ProfileRevision)
	mustExec(t, d.db, `INSERT INTO profile_revision (id, cluster, name, author, created_at) VALUES ($1, $2, 'base', $3, now())`, prv, d.cluster, d.seed)
	prf := id.New(id.Profile)
	mustExec(t, d.db, `INSERT INTO profile (id, cluster, scope, name, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, $2, 'cluster', 'base', $3, 4, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, prf, d.cluster, prv)
	e = d.ok(d.del("/profiles/base", d.etag, d.key()))
	if e.Revision != nil || e.Head == nil || *e.Head != prf || e.Base == nil || *e.Base != 4 {
		t.Fatalf("removal of a head %+v", e)
	}
	if n := count(t, d.db, `SELECT count(*) FROM act WHERE action = 'draft.profile.remove'`); n != 2 {
		t.Fatalf("%d acts", n)
	}
}
