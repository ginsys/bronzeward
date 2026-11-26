package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// The writes of a draft's shared (library) entries (persistence-api.md §3.1.1, §9.2). The cases
// speak HTTP and SQL alone.

// entryJSON is a draft update's answered entry with every member as sent.
func entryJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	b := decode[map[string]any](t, rec, http.StatusOK)
	e, ok := b["entry"].(map[string]any)
	if !ok {
		t.Fatalf("answer %s", rec.Body)
	}
	return e
}

// wantMember wants e's member name to be present with the JSON value want (nil: null).
func wantMember(t *testing.T, e map[string]any, name string, want any) {
	t.Helper()
	got, present := e[name]
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if !present || string(g) != string(w) {
		t.Fatalf("entry %v: member %s is %s (present %t); want %s", e, name, g, present, w)
	}
}

// updated is a 200 draft update: etag moves to the answer's.
func updated(t *testing.T, etag *string, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	e := entryJSON(t, rec)
	*etag = rec.Header().Get("ETag")
	return e
}

// sharedRows seeds a shared fragment revision of name, and with head a shared head at it with head
// revision counter (head false: no head).
func (d *draftEnv) sharedRows(name string, head bool, counter int) (frv, frg string) {
	d.t.Helper()
	p := &publishEnv{draftEnv: d}
	frv = p.sharedRevision(name)
	if !head {
		return frv, ""
	}
	frg = id.New(id.Fragment)
	mustExec(d.t, d.db, `INSERT INTO fragment (id, cluster, scope, name, layer, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, NULL, 'library', $2, 'cluster', $3, $4, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, frg, name, frv, counter)
	return frv, frg
}

// PA §3.1.1, library-reference: a shared fragment update that would mint or carry a reference is
// refused before any generation is created, and writes no revision or entry. The same update at
// cluster scope creates its generation: the control.
func TestLibraryWriteRefusesAReference(t *testing.T) {
	ie := newFragmentEnv(t, options{})
	shared := func(layer, doc string, marks []string, decl map[string]any) string {
		var m map[string]any
		if err := json.Unmarshal([]byte(fragmentPut(t, layer, doc, marks, decl)), &m); err != nil {
			t.Fatal(err)
		}
		m["scope"] = "library"
		b, _ := json.Marshal(m)
		return string(b)
	}
	refused := func(key, body string) {
		t.Helper()
		before, _ := ie.f.paths()
		p := wantProblem(t, ie.putFragment("registries", body, ie.etag, key), http.StatusUnprocessableEntity, "validation-failed")
		if p["rule"] != "library-reference" {
			t.Fatalf("problem %v; want rule library-reference", p)
		}
		if after, _ := ie.f.paths(); after != before {
			t.Fatalf("the refused update created %d generations", after-before)
		}
		if n := count(t, ie.db, `SELECT (SELECT count(*) FROM fragment_revision WHERE name = 'registries' AND cluster IS NULL)
			+ (SELECT count(*) FROM draft_source_entry WHERE draft = $1 AND name = 'registries')`, ie.draft); n != 0 {
			t.Fatalf("the refused update wrote %d rows", n)
		}
		if ie.currentETag(t) != ie.etag {
			t.Fatal("the refused update moved the draft")
		}
	}
	refused("k-fragment-shared-mark", shared("cluster", labelDoc, []string{labelMark}, nil))
	if calls, _ := ie.f.paths(); calls != 0 {
		t.Fatalf("%d generations before the control", calls)
	}
	own := ie.fragmentOK(t, ie.putFragment("tokens", fragmentPut(t, "cluster", labelDoc, []string{labelMark}, nil), ie.etag,
		"k-fragment-own-012345"))
	if calls, _ := ie.f.paths(); calls != 1 {
		t.Fatalf("%d generations for the cluster update; want 1", calls)
	}
	minted := fragmentRefs(t, ie, *own.Entry.Revision)[0]
	refused("k-fragment-shared-carry", shared("cluster", "machine:\n  nodeLabels:\n    tier: !bwref "+minted.name+"\n", []string{},
		map[string]any{"references": map[string]any{minted.name: map[string]any{"kind": "string", "version": 1}}}))

	e := updated(t, &ie.etag, ie.putFragment("plain", shared("cluster", "machine:\n  nodeLabels:\n    tier: edge\n", []string{}, nil),
		ie.etag, "k-fragment-shared-plain"))
	wantMember(t, e, "scope", "library")
	wantMember(t, e, "base", nil)
	wantMember(t, e, "adoptionBase", nil)
	if n := count(t, ie.db, `SELECT count(*) FROM fragment_revision WHERE id = $1 AND cluster IS NULL`, e["revision"]); n != 1 {
		t.Fatal("the shared update's revision belongs to a cluster")
	}
}

// PA §3.1.1: a shared entry records fresh bases each time it is written: the shared head's counter
// and the cluster's adoption counter. A cluster entry of the same name has its own slot.
func TestLibraryWriteBases(t *testing.T) {
	d := newDraftEnv(t)
	frv, frg := d.sharedRows("ntp", true, 3)
	body := `{"scope":"library","fragments":["` + frv + `"]}`
	e := updated(t, &d.etag, d.put("/profiles/shared-set", body, d.etag, d.key()))
	wantMember(t, e, "head", nil)
	wantMember(t, e, "base", nil)
	wantMember(t, e, "adoptionBase", nil)

	// A shared fragment removal: the head's counter, no adoption base.
	e = updated(t, &d.etag, d.del("/fragments/ntp?scope=library", d.etag, d.key()))
	wantMember(t, e, "scope", "library")
	wantMember(t, e, "head", frg)
	wantMember(t, e, "base", 3)
	wantMember(t, e, "revision", nil)
	if _, present := e["adoptionBase"]; present {
		t.Fatalf("entry %v: a removal has no adoption base", e)
	}
	mustExec(t, d.db, `UPDATE fragment SET head_revision = 4 WHERE id = $1`, frg)
	e = updated(t, &d.etag, d.del("/fragments/ntp?scope=library", d.etag, d.key()))
	wantMember(t, e, "base", 4)
	if n := count(t, d.db, `SELECT count(*) FROM draft_source_entry WHERE draft = $1 AND name = 'ntp' AND scope = 'library'`, d.draft); n != 1 {
		t.Fatalf("%d shared entries of ntp", n)
	}

	own := d.fragmentRevision(d.cluster, "ntp", "cluster")
	d.fragmentHead("ntp", "cluster", own, 1)
	updated(t, &d.etag, d.del("/fragments/ntp", d.etag, d.key()))
	rec := d.do(d.api, call{method: "GET", path: prefix + "/drafts/" + d.draft, token: d.human("h-viewer")})
	entries := decode[map[string]any](t, rec, http.StatusOK)["entries"].([]any)
	var scopes []any
	for _, en := range entries {
		if m := en.(map[string]any); m["name"] == "ntp" {
			scopes = append(scopes, m["scope"])
		}
	}
	if len(scopes) != 2 || scopes[0] != nil || scopes[1] != "library" {
		t.Fatalf("scopes of the ntp entries %v; want the cluster entry, then the shared one", scopes)
	}
}

// Contract 5: the DELETE of a fragment or profile entry takes the one query scope, once, and its
// idempotency fingerprint covers it. Every other mutation refuses any query.
func TestLibraryWriteScopeQuery(t *testing.T) {
	d := newDraftEnv(t)
	d.sharedRows("ntp", true, 1)
	own := d.fragmentRevision(d.cluster, "ntp", "cluster")
	d.fragmentHead("ntp", "cluster", own, 1)
	for _, q := range []string{"?scope=tenant", "?scope=", "?scope=library&scope=library", "?scope=library&x=1", "?x=1", "?scope=%zz"} {
		wantProblem(t, d.del("/fragments/ntp"+q, d.etag, d.key()), http.StatusBadRequest, "invalid-request")
	}
	wantProblem(t, d.put("/profiles/workers?scope=library", `{"fragments":["`+own+`"]}`, d.etag, d.key()),
		http.StatusBadRequest, "invalid-request")
	wantProblem(t, d.del("/assignments/"+d.machine+"?scope=library", d.etag, d.key()), http.StatusBadRequest, "invalid-request")

	before, key := d.etag, d.key()
	first := d.del("/fragments/ntp?scope=library", before, key)
	updated(t, &d.etag, first)
	again := d.del("/fragments/ntp?scope=library", before, key)
	if again.Code != http.StatusOK || again.Header().Get("Idempotent-Replayed") != "true" || again.Body.String() != first.Body.String() {
		t.Fatalf("replay %d %s", again.Code, again.Body)
	}
	for _, q := range []string{"", "?scope=cluster"} {
		wantProblem(t, d.del("/fragments/ntp"+q, before, key), http.StatusUnprocessableEntity, "idempotency-key-reused")
	}
	// Absent and cluster are one request.
	before, key = d.etag, d.key()
	updated(t, &d.etag, d.del("/fragments/ntp", before, key))
	if rec := d.del("/fragments/ntp?scope=cluster", before, key); rec.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("scope=cluster after no query: %d %s", rec.Code, rec.Body)
	}
}

// PA §3.1.1: a shared removal is 404 where the shared scope holds no such name: no head and no
// entry of this draft, or a head an earlier publication removed, whatever the draft holds.
func TestLibraryWriteRemovalNotFound(t *testing.T) {
	d := newDraftEnv(t)
	own := d.fragmentRevision(d.cluster, "own", "cluster")
	d.fragmentHead("own", "cluster", own, 1)
	for _, name := range []string{"own", "nothing"} {
		wantProblem(t, d.del("/fragments/"+name+"?scope=library", d.etag, d.key()), http.StatusNotFound, "not-found")
	}
	frv, frg := d.sharedRows("gone", true, 2)
	updated(t, &d.etag, d.put("/profiles/shared-set", `{"scope":"library","fragments":["`+frv+`"]}`, d.etag, d.key()))
	// A name the draft introduces: accepted, with no base.
	e := updated(t, &d.etag, d.del("/profiles/shared-set?scope=library", d.etag, d.key()))
	wantMember(t, e, "base", nil)
	wantMember(t, e, "head", nil)
	mustExec(t, d.db, `UPDATE fragment SET head_revision_id = NULL, head_revision = 3 WHERE id = $1`, frg)
	wantProblem(t, d.del("/fragments/gone?scope=library", d.etag, d.key()), http.StatusNotFound, "not-found")
}

// PA §3.1.1 "update-time": a shared profile pins existing shared revisions. A cluster profile may
// pin a shared revision and an assignment may select a shared name, both judged at publication.
func TestLibraryWritePinsAndSelections(t *testing.T) {
	d := newDraftEnv(t)
	shared, _ := d.sharedRows("ntp", true, 1)
	introduced, _ := d.sharedRows("not-yet", false, 0)
	own := d.fragmentRevision(d.cluster, "own", "cluster")
	for i, pin := range []string{own, "frv_aaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		p := wantProblem(t, d.put("/profiles/bad", `{"scope":"library","fragments":["`+shared+`","`+pin+`"]}`, d.etag, d.key()),
			http.StatusUnprocessableEntity, "validation-failed")
		if p["path"] != "fragments[1]" || p["revision"] != pin {
			t.Fatalf("pin %d: problem %v", i, p)
		}
	}
	wantProblem(t, d.put("/profiles/bad", `{"scope":"tenant","fragments":["`+shared+`"]}`, d.etag, d.key()),
		http.StatusBadRequest, "invalid-request")
	updated(t, &d.etag, d.put("/profiles/shared-set", `{"scope":"library","fragments":["`+shared+`","`+introduced+`"]}`, d.etag, d.key()))
	updated(t, &d.etag, d.put("/profiles/workers", `{"fragments":["`+introduced+`"]}`, d.etag, d.key()))
	if n := count(t, d.db, `SELECT count(*) FROM profile_revision_fragment WHERE fragment_revision = $1 AND library`, introduced); n != 1 {
		t.Fatalf("%d marked pins of the shared revision", n)
	}
	// A shared name: one with a head, one this draft introduces, of either kind.
	updated(t, &d.etag, d.put("/assignments/"+d.machine, `{"profiles":["shared-set"],"fragments":{"cluster":["ntp"]}}`, d.etag, d.key()))
	p := wantProblem(t, d.put("/assignments/"+d.machine, `{"profiles":[],"fragments":{"cluster":["unknown"]}}`, d.etag, d.key()),
		http.StatusUnprocessableEntity, "validation-failed")
	if p["path"] != "fragments.cluster[0]" {
		t.Fatalf("problem %v", p)
	}
}

// Contract 8: a shared head and its revisions answer cluster null, and list their revisions.
func TestLibraryReads(t *testing.T) {
	d := newDraftEnv(t)
	frv, frg := d.sharedRows("ntp", true, 1)
	get := func(path string) map[string]any {
		t.Helper()
		return decode[map[string]any](t, d.do(d.api, call{method: "GET", path: prefix + path, token: d.human("h-viewer")}),
			http.StatusOK)
	}
	for _, b := range []map[string]any{get("/fragments/" + frg), get("/fragment-revisions/" + frv)} {
		wantMember(t, b, "cluster", nil)
	}
	for _, list := range []string{"/fragments", "/fragments/" + frg + "/revisions"} {
		items := get(list)["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("%s: %d items", list, len(items))
		}
		wantMember(t, items[0].(map[string]any), "cluster", nil)
	}

	prf, prv := (&publishEnv{draftEnv: d}).adoptedSharedProfile("shared-set", frv)
	wantMember(t, get("/profiles/"+prf), "cluster", nil)
	wantMember(t, get("/profile-revisions/"+prv), "cluster", nil)
	if items := get("/profiles/" + prf + "/revisions")["items"].([]any); len(items) != 1 {
		t.Fatalf("%d profile revisions", len(items))
	}
}

// Contract 8: a release's shared source answers headRevision null.
func TestLibraryReleaseRead(t *testing.T) {
	p := newPublishEnv(t)
	f := p.sharedFragment("ntp")
	p.adopt("fragment", "ntp", f.head, f.older, 1)
	p.selectToo(nil, "cluster/ntp")
	rel := p.publishedEntries()
	b := decode[map[string]any](t, p.do(p.api, call{method: "GET", path: prefix + "/releases/" + rel, token: p.human("h-viewer")}),
		http.StatusOK)
	var shared int
	for _, s := range b["sources"].([]any) {
		if m := s.(map[string]any); m["head"] == f.head {
			wantMember(t, m, "headRevision", nil)
			shared++
		}
	}
	if shared != 1 {
		t.Fatalf("%d sources of the shared head", shared)
	}
}

// A library write meets the draft checks of any draft update (§3.1, §7.2): a stale If-Match is
// 412, an active publication of the draft 409, and a lost answer is replayed under its key.
func TestLibraryWriteDraftChecks(t *testing.T) {
	d := newDraftEnv(t)
	frv, _ := d.sharedRows("ntp", true, 1)
	body, stale, key := `{"scope":"library","fragments":["`+frv+`"]}`, d.etag, d.key()
	first := d.put("/profiles/shared-set", body, stale, key)
	updated(t, &d.etag, first)
	again := d.put("/profiles/shared-set", body, stale, key)
	if again.Code != http.StatusOK || again.Header().Get("Idempotent-Replayed") != "true" || again.Body.String() != first.Body.String() {
		t.Fatalf("replay %d %s", again.Code, again.Body)
	}
	for _, rec := range []*httptest.ResponseRecorder{d.put("/profiles/shared-set", body, stale, d.key()),
		d.del("/fragments/ntp?scope=library", stale, d.key())} {
		wantProblem(t, rec, http.StatusPreconditionFailed, "precondition-failed")
	}
	var epoch string
	if err := d.db.QueryRow(`SELECT epoch FROM installation_state`).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	op := id.New(id.Operation)
	mustExec(t, d.db, `INSERT INTO operation (id, kind, state, draft, draft_revision, created_by, created_by_kind, created_role,
			epoch, created_at)
		VALUES ($1, 'publish', 'queued', $2, 2, $3, 'human', 'publisher', $4, now())`, op, d.draft, d.seed, epoch)
	for _, rec := range []*httptest.ResponseRecorder{d.put("/profiles/shared-set", body, d.etag, d.key()),
		d.del("/fragments/ntp?scope=library", d.etag, d.key())} {
		if p := wantProblem(t, rec, http.StatusConflict, "conflict"); p["operation"] != op {
			t.Fatalf("problem %v", p)
		}
	}
}

// The whole of part 2 by API: a person writes a shared fragment and selects it, the publication
// adopts it for that cluster, which compiles it, and another cluster that selects it is refused
// at publication.
func TestLibraryWritePublishAndUse(t *testing.T) {
	ie := newFragmentEnv(t, options{})
	d := &draftEnv{env: ie.env, cluster: ie.cluster, machine: ie.machine, draft: ie.draft, seed: id.New(id.Principal)}
	mustExec(t, ie.db, `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', 'https://idp.test', 'seed', now())`, d.seed)
	put := `{"scope":"library","layer":"cluster","document":"machine:\n  nodeLabels:\n    tier: edge\n","marks":[]}`
	entry := updated(t, &ie.etag, ie.putFragment("ntp", put, ie.etag, "k-fragment-shared-use1"))
	frv, _ := entry["revision"].(string)
	selects := func(draft, etag, machine, key string) string {
		t.Helper()
		rec := ie.do(ie.api, call{method: "PUT", path: prefix + "/drafts/" + draft + "/assignments/" + machine,
			token: ie.human("h-author"), key: key, ifMatch: etag, body: `{"profiles":[],"fragments":{"cluster":["ntp"]}}`})
		updated(t, &etag, rec)
		return etag
	}
	selects(ie.draft, ie.etag, ie.machine, "k-assignment-shared-1")

	publication := func(cluster, machine, draft, key string) (*publishEnv, *refusal) {
		t.Helper()
		var rev int
		if err := ie.db.QueryRow(`SELECT revision FROM draft WHERE id = $1`, draft).Scan(&rev); err != nil {
			t.Fatal(err)
		}
		p := publicationOf(d, cluster, machine, draft, rev, key)
		s, ref := p.snapshot()
		if ref == nil {
			p.unit.unchanged, p.unit.machines[0].assignment = s.unchanged, s.machines[0].assignment
			if got := s.machines[0].fragments; len(got) != 1 || got[0] != frv {
				t.Fatalf("fragments %v; want the written %s", got, frv)
			}
		}
		return p, ref
	}
	p, ref := publication(ie.cluster, ie.machine, ie.draft, "bw-artifact")
	if ref != nil {
		t.Fatalf("snapshot refused: %v %v", ref, ref.extra)
	}
	rel := p.published()
	p.wantAdoption(ie.cluster, "fragment", "ntp", frv, 1)
	p.wantSources(rel, "fragment", "ntp", sharedRow(frv, "", 1))

	var other string
	if err := ie.db.QueryRow(`SELECT cluster FROM machine WHERE id = $1`, ie.otherMachine).Scan(&other); err != nil {
		t.Fatal(err)
	}
	rec := ie.do(ie.api, call{method: "POST", path: prefix + "/drafts", token: ie.human("h-author"), key: "k-draft-other-0123456",
		body: `{"cluster":"` + other + `","title":"lab"}`})
	draft := decode[draftBody](t, rec, http.StatusCreated).ID
	selects(draft, rec.Header().Get("ETag"), ie.otherMachine, "k-assignment-shared-2")
	if _, ref := publication(other, ie.otherMachine, draft, "bw-artifact-b"); ref == nil ||
		ref.status != http.StatusUnprocessableEntity || ref.extra["path"] != "fragments.cluster[0]" {
		t.Fatalf("snapshot of the other cluster %v; want 422 at fragments.cluster[0]", ref)
	}
}
