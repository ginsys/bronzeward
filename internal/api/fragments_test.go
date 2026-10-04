package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The fragment PUT (persistence-api.md §9.3, compilation.md §2.3): its document ingested under a
// draft-update claim, then T1 writes the revision, its reference rows and the draft entry.

// newFragmentEnv is an ingestEnv whose API is built with o and serves the fragment PUT in the
// request; it hands no job to a runner.
func newFragmentEnv(t *testing.T, o options) *ingestEnv {
	t.Helper()
	ie := setupIngestEnv(t)
	ie.captured = o
	ie.api = ie.build(o)
	return ie
}

func (ie *ingestEnv) putFragment(name, body, ifMatch, k string) *httptest.ResponseRecorder {
	return ie.do(ie.api, call{method: "PUT", path: prefix + "/drafts/" + ie.draft + "/fragments/" + name,
		token: ie.human("h-author"), key: k, ifMatch: ifMatch, body: body})
}

// fragmentPut is a fragment PUT body; decl, when not nil, is its declarations member.
func fragmentPut(t *testing.T, layer, doc string, marks []string, decl map[string]any) string {
	t.Helper()
	m := map[string]any{"layer": layer, "document": doc, "marks": marks}
	if decl != nil {
		m["declarations"] = decl
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// labelDoc holds one marked label: one generation.
const labelDoc = "machine:\n  nodeLabels:\n    tier: " + runLabel + "\n"

// fragmentOK checks a 200 fragment update, moves ie's ETag to the answer's and returns the answer.
func (ie *ingestEnv) fragmentOK(t *testing.T, rec *httptest.ResponseRecorder) sourceUpdate {
	t.Helper()
	b := decode[sourceUpdate](t, rec, http.StatusOK)
	if b.Draft != ie.draft || rec.Header().Get("ETag") == "" || rec.Header().Get("ETag") == ie.etag {
		t.Fatalf("fragment answer %+v, ETag %q after %q", b, rec.Header().Get("ETag"), ie.etag)
	}
	ie.etag = rec.Header().Get("ETag")
	return b
}

type fragmentRef struct{ name, kind, generation string }

func fragmentRefs(t *testing.T, ie *ingestEnv, frv string) []fragmentRef {
	t.Helper()
	rows, err := ie.db.Query(`SELECT name, kind, generation FROM fragment_reference WHERE revision = $1 ORDER BY name`, frv)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []fragmentRef
	for rows.Next() {
		var r fragmentRef
		if err := rows.Scan(&r.name, &r.kind, &r.generation); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// A marked value is extracted to a generation under the draft-update claim; the answer holds the
// sanitized document, the claim is released and no operation exists for it.
func TestFragmentPutMintsReference(t *testing.T) {
	ie := newFragmentEnv(t, options{})
	b := ie.fragmentOK(t, ie.putFragment("registries", fragmentPut(t, "cluster", labelDoc, []string{labelMark}, nil), ie.etag,
		"k-fragment-0123456789"))
	e := b.Entry
	if e.Kind != "fragment" || e.Name != "registries" || e.Head != nil || e.Base != nil || e.Revision == nil || e.Document == nil {
		t.Fatalf("entry %+v", e)
	}
	if !strings.Contains(*e.Document, "!bwref s-") || strings.Contains(*e.Document, runLabel) {
		t.Fatalf("answered document %q", *e.Document)
	}
	if !strings.HasPrefix(b.Ingestion, "ing_") {
		t.Fatalf("ingestion %q", b.Ingestion)
	}
	var kind, state, draft, embedded string
	var machine *string
	if err := ie.db.QueryRow(`SELECT kind, state, draft, machine FROM staging_claim WHERE id = $1`, b.Ingestion).
		Scan(&kind, &state, &draft, &machine); err != nil {
		t.Fatal(err)
	}
	if kind != "draft-update" || state != "released" || draft != ie.draft || machine != nil {
		t.Fatalf("claim kind %s state %s draft %s machine %v", kind, state, draft, machine)
	}
	if n := count(t, ie.db, `SELECT count(*) FROM operation WHERE ingestion = $1`, b.Ingestion); n != 0 {
		t.Fatalf("%d operations for a draft-update claim", n)
	}
	var layer, document string
	if err := ie.db.QueryRow(`SELECT layer, document, embedded::text FROM fragment_revision WHERE id = $1`, *e.Revision).
		Scan(&layer, &document, &embedded); err != nil {
		t.Fatal(err)
	}
	if layer != "cluster" || document != *e.Document || embedded != "[]" {
		t.Fatalf("revision layer %s embedded %s document %q", layer, embedded, document)
	}
	refs := fragmentRefs(t, ie, *e.Revision)
	_, created := ie.f.paths()
	if len(refs) != 1 || refs[0].kind != "string" || !generationPath.MatchString(refs[0].generation) ||
		!strings.Contains(refs[0].generation, "/"+b.Ingestion+"/") || len(created) != 1 || created[0] != refs[0].generation {
		t.Fatalf("references %+v, created %v", refs, created)
	}
	if !strings.Contains(*e.Document, "!bwref "+refs[0].name) {
		t.Fatalf("document %q does not name %s", *e.Document, refs[0].name)
	}
	if n := count(t, ie.db, `SELECT count(*) FROM draft_source_entry WHERE draft = $1 AND kind = 'fragment'
		AND name = 'registries' AND fragment_revision = $2`, ie.draft, *e.Revision); n != 1 {
		t.Fatalf("%d draft entries", n)
	}
}

// A !bwref the document already carries, declared in the body, resolves to the generation of a
// reference row of the draft's cluster with the same name, kind and version: a fragment's, then an
// import base's. No generation is created for it.
func TestFragmentPutCarriesReference(t *testing.T) {
	ie := newIngestEnv(t, options{})
	_, j := ie.startJob(t, nil)
	ie.runWith(t, ie.captured, j)
	var imported, importedGen string
	if err := ie.db.QueryRow(`SELECT name, generation FROM import_base_reference`).Scan(&imported, &importedGen); err != nil {
		t.Fatal(err)
	}
	ie.etag = ie.currentETag(t)
	first := ie.fragmentOK(t, ie.putFragment("registries", fragmentPut(t, "cluster", labelDoc, []string{labelMark}, nil), ie.etag,
		"k-fragment-0123456789"))
	minted := fragmentRefs(t, ie, *first.Entry.Revision)[0]
	calls, _ := ie.f.paths()

	doc := "machine:\n  token: !bwref " + imported + "\n  nodeLabels:\n    tier: !bwref " + minted.name + "\n"
	decl := map[string]any{"references": map[string]any{
		imported:    map[string]any{"kind": "string", "version": 1},
		minted.name: map[string]any{"kind": "string", "version": 1},
	}}
	b := ie.fragmentOK(t, ie.putFragment("workers", fragmentPut(t, "role", doc, []string{}, decl), ie.etag, "k-fragment-carry-0123"))
	refs := fragmentRefs(t, ie, *b.Entry.Revision)
	want := map[string]string{imported: importedGen, minted.name: minted.generation}
	if len(refs) != 2 {
		t.Fatalf("references %+v", refs)
	}
	for _, r := range refs {
		if want[r.name] != r.generation || r.kind != "string" {
			t.Fatalf("reference %+v; want generation %s", r, want[r.name])
		}
	}
	if after, _ := ie.f.paths(); after != calls {
		t.Fatalf("a carried reference created %d generations", after-calls)
	}
}

// A carried reference whose declaration differs from every row of its name, or names none, is
// refused 422 unknown-reference, naming the reference; the claim is abandoned and nothing written.
func TestFragmentPutCarriedReferenceRefused(t *testing.T) {
	ie := newFragmentEnv(t, options{})
	first := ie.fragmentOK(t, ie.putFragment("registries", fragmentPut(t, "cluster", labelDoc, []string{labelMark}, nil), ie.etag,
		"k-fragment-0123456789"))
	minted := fragmentRefs(t, ie, *first.Entry.Revision)[0]
	for i, c := range []struct{ name, kind string }{{minted.name, "integer"}, {"s-nosuchname", "string"}} {
		doc := "machine:\n  nodeLabels:\n    tier: !bwref " + c.name + "\n"
		decl := map[string]any{"references": map[string]any{c.name: map[string]any{"kind": c.kind, "version": 1}}}
		rec := ie.putFragment("workers", fragmentPut(t, "role", doc, []string{}, decl), ie.etag, "k-fragment-refused-"+string(rune('a'+i)))
		p := wantProblem(t, rec, http.StatusUnprocessableEntity, "validation-failed")
		if p["rule"] != "unknown-reference" || p["reference"] != c.name {
			t.Fatalf("%s: problem %v", c.name, p)
		}
		claim, _ := p["ingestion"].(string)
		var state string
		if err := ie.db.QueryRow(`SELECT state FROM staging_claim WHERE id = $1`, claim).Scan(&state); err != nil || state != "abandoned" {
			t.Fatalf("%s: claim %q state %q (%v)", c.name, claim, state, err)
		}
	}
	if n := count(t, ie.db, `SELECT count(*) FROM fragment_revision WHERE name = 'workers'`); n != 0 {
		t.Fatalf("%d refused revisions written", n)
	}
	if ie.currentETag(t) != ie.etag {
		t.Fatal("a refused update moved the draft")
	}
}
