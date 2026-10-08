package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
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
	removedOnly                         bool                 // d.release names only a removed fragment, no fragment revision
	reproductions                       bool                 // d.release also records two occurrences of reproducedPass in d.machine's import base
	assigned                            bool                 // d.release binds each machine's assignment head, d.assignment creating it
	redacted                            string               // d.release's redacted configuration of d.machine, when set
	artifact                            []byte               // d.release's artifact configuration digest, when set
	heads                               map[string][2]string // a machine's assignment and its head revision, by d.assignment
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
	mustExec(d.t, d.db, `INSERT INTO fragment_revision (id, cluster, name, layer, document, author, embedded, created_at)
		VALUES ($1, $2, $3, $4, 'machine: {}', $5, '[]', now())`, frv, cluster, name, layer, d.seed)
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
		// A running job has an owner and a lease; a queued one has neither.
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

// PA §9.4: a path identifier that is not one of its entity is 404 before any query, and the
// refusal repeats none of its text.
func TestDraftUpdateIdentifiers(t *testing.T) {
	d := newDraftEnv(t)
	assignment := `{"profiles":["workers"]}`
	for _, c := range []struct{ method, path, body string }{
		{"PUT", "/drafts/synthetic-secret-text/assignments/" + d.machine, assignment},
		{"PUT", "/drafts/drf_synthetic%00secret/profiles/workers", `{"fragments":["` + id.New(id.FragmentRevision) + `"]}`},
		{"PUT", "/drafts/" + d.draft + "/assignments/synthetic-secret-text", assignment},
		{"PUT", "/drafts/" + d.draft + "/assignments/mch_synthetic%00secret", assignment},
		{"DELETE", "/drafts/" + d.draft + "/assignments/synthetic-secret-text", ""},
		{"DELETE", "/drafts/synthetic-secret-text/fragments/registries", ""},
		{"POST", "/drafts/synthetic-secret-text/discard", "{}"},
	} {
		rec := d.do(d.api, call{method: c.method, path: prefix + c.path, token: d.human("h-author"), key: d.key(), ifMatch: d.etag, body: c.body})
		wantProblem(t, rec, http.StatusNotFound, "not-found")
		if strings.Contains(rec.Body.String(), "synthetic") {
			t.Errorf("%s %s: the refusal repeats the path: %s", c.method, c.path, rec.Body.String())
		}
	}
}

// PA §5 rules 2 and 5: an update locks the heads its pins and selections are checked against,
// FOR SHARE, before the draft, so a publication advancing or removing one (T3, which holds it FOR
// UPDATE) is waited for and its result checked, never committed past. A head created after that
// lock pass is not one the update checked: it is refused as the name was when the heads were taken.
func TestDraftUpdateLocksCheckedHeads(t *testing.T) {
	d := newDraftEnv(t)
	f1 := d.fragmentRevision(d.cluster, "registries", "override")
	f2 := d.fragmentRevision(d.cluster, "registries", "override")
	frg := d.fragmentHead("registries", "override", f1, 1)
	prf := d.profileHead("workers", 1)
	token := d.human("h-author")
	update := func(q string, args ...any) func(*sql.Tx) error {
		return func(tx *sql.Tx) error { _, err := tx.Exec(q, args...); return err }
	}
	for _, c := range []struct {
		name, table, row, part, body, path string
		move                               func(*sql.Tx) error
	}{
		{"pinned fragment advanced", "fragment", frg, "/profiles/storage", `{"fragments":["` + f1 + `"]}`, "fragments[0]",
			update(`UPDATE fragment SET head_revision_id = $2, head_revision = 2 WHERE id = $1`, frg, f2)},
		{"selected profile removed", "profile", prf, "/assignments/" + d.machine, `{"profiles":["workers"]}`, "profiles[0]",
			update(`UPDATE profile SET head_revision_id = NULL, head_revision = 2 WHERE id = $1`, prf)},
		{"selected fragment removed", "fragment", frg, "/assignments/" + d.machine, `{"fragments":{"override":["registries"]}}`,
			"fragments.override[0]", update(`UPDATE fragment SET head_revision_id = NULL, head_revision = 3 WHERE id = $1`, frg)},
		// The holder takes the draft, so the update passes its lock pass with no head for the name
		// and waits on the draft while a publication creates one.
		{"selected profile created after the lock pass", "draft", d.draft, "/assignments/" + d.machine, `{"profiles":["fresh"]}`,
			"profiles[0]", func(*sql.Tx) error { d.profileHead("fresh", 1); return nil }},
	} {
		tx, err := d.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		var holder int
		if err := tx.QueryRow(`SELECT pg_backend_pid()`).Scan(&holder); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`SELECT 1 FROM `+c.table+` WHERE id = $1 FOR UPDATE`, c.row); err != nil {
			t.Fatal(err)
		}
		done := make(chan *httptest.ResponseRecorder, 1)
		k := d.key()
		go func() {
			done <- d.do(d.api, call{method: "PUT", path: prefix + "/drafts/" + d.draft + c.part, token: token, key: k, ifMatch: d.etag, body: c.body})
		}()
		waitBlockedBy(t, d.db, holder)
		if c.table != "draft" {
			// Waiting on the head, the update holds no draft lock yet (rule 5's order).
			if _, err := d.db.Exec(`SELECT 1 FROM draft WHERE id = $1 FOR UPDATE NOWAIT`, d.draft); err != nil {
				t.Errorf("%s: the draft is locked while the update waits for a head: %v", c.name, err)
			}
		}
		if err := c.move(tx); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		doc := wantProblem(t, <-done, http.StatusUnprocessableEntity, "validation-failed")
		if doc["path"] != c.path {
			t.Errorf("%s: %v; want path %s", c.name, doc, c.path)
		}
	}
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

// profileHead inserts a profile revision of name and a head at it, as publication would.
func (d *draftEnv) profileHead(name string, headRevision int, pins ...string) string {
	d.t.Helper()
	prv, prf := id.New(id.ProfileRevision), id.New(id.Profile)
	tx, err := d.db.Begin()
	if err != nil {
		d.t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(d.t, tx, `INSERT INTO profile_revision (id, cluster, name, author, created_at) VALUES ($1, $2, $3, $4, now())`,
		prv, d.cluster, name, d.seed)
	for i, frv := range pins { // in the revision's own transaction (§3)
		mustExec(d.t, tx, `INSERT INTO profile_revision_fragment (revision, cluster, position, fragment_revision) VALUES ($1, $2, $3, $4)`,
			prv, d.cluster, i, frv)
	}
	if err := tx.Commit(); err != nil {
		d.t.Fatal(err)
	}
	mustExec(d.t, d.db, `INSERT INTO profile (id, cluster, scope, name, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, $2, 'cluster', $3, $4, $5, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, prf, d.cluster, name, prv, headRevision)
	return prf
}

// PA §3.1, §9.3, choice §17.31: an assignment selects profiles, then fragments per layer, by name.
// Each name has a head with a revision or is proposed in this draft, and is not removed by it; a
// fragment carries the layer it is listed under. Anything else is 422 naming the body path.
func TestAssignmentUpdate(t *testing.T) {
	d := newDraftEnv(t)
	d.profileHead("workers", 1)
	d.fragmentHead("registries", "override", d.fragmentRevision(d.cluster, "registries", "override"), 2)
	mustExec(t, d.db, `INSERT INTO draft_source_entry (draft, cluster, kind, name, fragment_revision) VALUES ($1, $2, 'fragment', 'site-dns', $3)`,
		d.draft, d.cluster, d.fragmentRevision(d.cluster, "site-dns", "site"))
	d.fragmentHead("ntp", "site", d.fragmentRevision(d.cluster, "ntp", "site"), 1)
	d.fragmentHead("gone", "site", nil, 3)
	mustExec(t, d.db, `INSERT INTO draft_source_entry (draft, cluster, kind, name, base) VALUES ($1, $2, 'fragment', 'ntp', 1)`, d.draft, d.cluster)
	d.profileHead("storage", 1)
	mustExec(t, d.db, `INSERT INTO draft_source_entry (draft, cluster, kind, name, base) VALUES ($1, $2, 'profile', 'storage', 1)`, d.draft, d.cluster)
	part := "/assignments/" + d.machine

	for name, c := range map[string]struct{ body, path string }{
		"unknown profile":              {`{"profiles":["workers","absent"]}`, "profiles[1]"},
		"profile the draft removes":    {`{"profiles":["storage"]}`, "profiles[0]"},
		"fragment under another layer": {`{"profiles":["workers"],"fragments":{"site":["registries"]}}`, "fragments.site[0]"},
		"proposed fragment's layer":    {`{"fragments":{"override":["registries","site-dns"]}}`, "fragments.override[1]"},
		"fragment the draft removes":   {`{"fragments":{"site":["site-dns","ntp"]}}`, "fragments.site[1]"},
		"removed head":                 {`{"fragments":{"site":["gone"]}}`, "fragments.site[0]"},
		"unknown fragment":             {`{"fragments":{"override":["absent"]}}`, "fragments.override[0]"},
	} {
		doc := wantProblem(t, d.put(part, c.body, d.etag, d.key()), http.StatusUnprocessableEntity, "validation-failed")
		if doc["path"] != c.path {
			t.Errorf("%s: %v; want %s named", name, doc, c.path)
		}
	}
	if n := count(t, d.db, `SELECT count(*) FROM assignment_revision`); n != 0 {
		t.Fatalf("refusals wrote %d revisions", n)
	}

	e := d.ok(d.put(part, `{"profiles":["workers"],"fragments":{"override":["registries"],"site":["site-dns"]}}`, d.etag, d.key()))
	if e.Kind != "assignment" || e.Machine != d.machine || e.Name != "" || e.Head != nil || e.Base != nil || e.Revision == nil {
		t.Fatalf("entry %+v", e)
	}
	if n := count(t, d.db, `SELECT count(*) FROM assignment_revision_profile WHERE revision = '`+*e.Revision+`' AND position = 0 AND profile = 'workers'`); n != 1 {
		t.Fatalf("%d profile rows", n)
	}
	if n := count(t, d.db, `SELECT count(*) FROM assignment_revision_fragment WHERE revision = '`+*e.Revision+`'
		AND (layer, position, fragment) IN (('override', 0, 'registries'), ('site', 0, 'site-dns'))`); n != 2 {
		t.Fatalf("%d fragment rows", n)
	}
	// The head of the machine's assignment is a new entry's base; a removal, then an update, keeps
	// one entry and its base.
	asr := id.New(id.AssignmentRevision)
	mustExec(t, d.db, `INSERT INTO assignment_revision (id, cluster, machine, author, created_at) VALUES ($1, $2, $3, $4, now())`,
		asr, d.cluster, d.machine, d.seed)
	asg := id.New(id.Assignment)
	mustExec(t, d.db, `INSERT INTO assignment (id, cluster, machine, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, $2, $3, $4, 2, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, asg, d.cluster, d.machine, asr)
	mustExec(t, d.db, `DELETE FROM draft_source_entry WHERE kind = 'assignment'`)
	e = d.ok(d.del(part, d.etag, d.key()))
	if e.Revision != nil || e.Head == nil || *e.Head != asg || e.Base == nil || *e.Base != 2 {
		t.Fatalf("removal %+v", e)
	}
	e = d.ok(d.put(part, `{"profiles":["workers"]}`, d.etag, d.key()))
	if e.Revision == nil || e.Base == nil || *e.Base != 2 {
		t.Fatalf("update after the removal %+v", e)
	}
	if n := count(t, d.db, `SELECT count(*) FROM draft_source_entry WHERE kind = 'assignment'`); n != 1 {
		t.Fatalf("%d assignment entries", n)
	}
	if n := count(t, d.db, `SELECT count(*) FROM act WHERE action = 'draft.assignment.update'`); n != 2 {
		t.Fatalf("%d update acts", n)
	}

	// A machine of another cluster, or none, is not found in the draft's cluster.
	other := d.createCluster(d.api, d.human("h-author"), "k-cluster-other-0123456789")
	rec := d.do(d.api, machineCall(d.human("h-author"), "k-machine-other-0123456789", other, "1c6b7d2f-3a4e-4f60-9bac-1d2e3f4a5b6c"))
	foreign := decode[machineBody](t, rec, http.StatusCreated).ID
	for _, m := range []string{foreign, id.New(id.Machine)} {
		wantProblem(t, d.put("/assignments/"+m, `{"profiles":["workers"]}`, d.etag, d.key()), http.StatusNotFound, "not-found")
		wantProblem(t, d.del("/assignments/"+m, d.etag, d.key()), http.StatusNotFound, "not-found")
	}
}

// The assignment body: at least one selection; known layers; valid names, none repeated.
func TestAssignmentUpdateRefusals(t *testing.T) {
	d := newDraftEnv(t)
	part := "/assignments/" + d.machine
	for name, body := range map[string]string{
		"nothing selected":            `{}`,
		"empty lists":                 `{"profiles":[],"fragments":{}}`,
		"empty layer":                 `{"profiles":["workers"],"fragments":{"site":[]}}`,
		"unknown layer":               `{"fragments":{"machine-intrinsic":["registries"]}}`,
		"repeated profile":            `{"profiles":["workers","workers"]}`,
		"fragment under two layers":   `{"fragments":{"site":["registries"],"override":["registries"]}}`,
		"profile name with a capital": `{"profiles":["Workers"]}`,
		"fragment name too long":      `{"fragments":{"site":["` + strings.Repeat("a", 64) + `"]}}`,
		"unknown member":              `{"profiles":["workers"],"machine":"x"}`,
	} {
		if rec := d.put(part, body, d.etag, d.key()); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s; want 400", name, rec.Code, rec.Body)
		}
	}
}

// PA §3.1 rule 8 for a fragment: a removal of a head, removed already or not, takes its head
// revision as base; of a fragment only this draft proposes, base stays absent; of a name with
// neither, 404.
func TestFragmentRemoval(t *testing.T) {
	d := newDraftEnv(t)
	wantProblem(t, d.del("/fragments/registries", d.etag, d.key()), http.StatusNotFound, "not-found")
	frg := d.fragmentHead("registries", "override", d.fragmentRevision(d.cluster, "registries", "override"), 2)
	e := d.ok(d.del("/fragments/registries", d.etag, d.key()))
	if e.Kind != "fragment" || e.Name != "registries" || e.Revision != nil || e.Head == nil || *e.Head != frg || e.Base == nil || *e.Base != 2 {
		t.Fatalf("removal of a head %+v", e)
	}
	mustExec(t, d.db, `INSERT INTO draft_source_entry (draft, cluster, kind, name, fragment_revision) VALUES ($1, $2, 'fragment', 'site-dns', $3)`,
		d.draft, d.cluster, d.fragmentRevision(d.cluster, "site-dns", "site"))
	e = d.ok(d.del("/fragments/site-dns", d.etag, d.key()))
	if e.Revision != nil || e.Head != nil || e.Base != nil {
		t.Fatalf("removal of a proposed fragment %+v", e)
	}
	// A head a publication removed is still a head: the removal records its head revision as base.
	gone := d.fragmentHead("gone", "site", nil, 3)
	e = d.ok(d.del("/fragments/gone", d.etag, d.key()))
	if e.Revision != nil || e.Head == nil || *e.Head != gone || e.Base == nil || *e.Base != 3 {
		t.Fatalf("removal of a removed head %+v", e)
	}
	if n := count(t, d.db, `SELECT count(*) FROM draft_source_entry WHERE kind = 'fragment' AND fragment_revision IS NULL`); n != 3 {
		t.Fatalf("%d removal entries", n)
	}
	if n := count(t, d.db, `SELECT count(*) FROM act WHERE action = 'draft.fragment.remove'`); n != 3 {
		t.Fatalf("%d acts", n)
	}
}

// PA §3.1, §9.3, T11: a discard takes {} and the draft's If-Match, refuses a draft with an active
// publication, and answers the draft discarded at its next revision; the draft then takes neither
// an update nor another discard, and a replay answers the same.
func TestDraftDiscard(t *testing.T) {
	d := newDraftEnv(t)
	discard := func(body, ifMatch, k string) *httptest.ResponseRecorder {
		return d.do(d.api, call{method: "POST", path: prefix + "/drafts/" + d.draft + "/discard", token: d.human("h-author"),
			key: k, ifMatch: ifMatch, body: body})
	}
	d.profileHead("workers", 1)
	frv := d.fragmentRevision(d.cluster, "registries", "override")
	d.fragmentHead("registries", "override", frv, 1)
	d.ok(d.put("/profiles/workers", `{"fragments":["`+frv+`"]}`, d.etag, d.key()))

	wantProblem(t, discard(`{"reason":"x"}`, d.etag, d.key()), http.StatusBadRequest, "invalid-request")
	wantProblem(t, discard(`{}`, `"2-aaaaaaaaaaaaaaaaaaaaaaaaaa"`, d.key()), http.StatusPreconditionFailed, "precondition-failed")
	op := id.New(id.Operation)
	mustExec(t, d.db, `INSERT INTO operation (id, kind, state, epoch, owner_gen, draft, draft_revision, created_by, created_by_kind, created_role, created_at)
		SELECT $1, 'publish', 'queued', epoch, 0, $2, 2, $3, 'human', 'publisher', now() FROM installation_state`, op, d.draft, d.seed)
	if doc := wantProblem(t, discard(`{}`, d.etag, d.key()), http.StatusConflict, "conflict"); doc["operation"] != op {
		t.Fatalf("discard with a queued publication: %v; want it named", doc)
	}
	mustExec(t, d.db, `UPDATE operation SET state = 'failed', error = '{}'::jsonb WHERE id = $1`, op)

	k := d.key()
	rec := discard(`{}`, d.etag, k)
	b := decode[draftBody](t, rec, http.StatusOK)
	if b.ID != d.draft || b.State != "discarded" || b.Revision != 3 {
		t.Fatalf("discard answered %+v", b)
	}
	if replay := discard(`{}`, d.etag, k); replay.Code != http.StatusOK || replay.Body.String() != rec.Body.String() {
		t.Fatalf("replay %d %s", replay.Code, replay.Body)
	}
	var state string
	var revision int
	if err := d.db.QueryRow(`SELECT state, revision FROM draft WHERE id = $1`, d.draft).Scan(&state, &revision); err != nil || state != "discarded" || revision != 3 {
		t.Fatalf("draft %s at %d, %v", state, revision, err)
	}
	wantProblem(t, d.put("/profiles/workers", `{"fragments":["`+frv+`"]}`, d.etag, d.key()), http.StatusConflict, "conflict")
	wantProblem(t, discard(`{}`, d.etag, d.key()), http.StatusConflict, "conflict")
	if n := count(t, d.db, `SELECT count(*) FROM act WHERE action = 'draft.discard'`); n != 1 {
		t.Fatalf("%d acts", n)
	}
}
