package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/staging"
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

// putFragment PUTs body as h-author and records the response body for assertAbsent.
func (ie *ingestEnv) putFragment(name, body, ifMatch, k string) *httptest.ResponseRecorder {
	rec := ie.do(ie.api, call{method: "PUT", path: prefix + "/drafts/" + ie.draft + "/fragments/" + name,
		token: ie.human("h-author"), key: k, ifMatch: ifMatch, body: body})
	ie.bodies = append(ie.bodies, rec.Body.String())
	return rec
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

// noIngestion fails unless the PUTs so far created no claim, no generation and no record.
func noIngestion(t *testing.T, ie *ingestEnv) {
	t.Helper()
	calls, _ := ie.f.paths()
	if n := count(t, ie.db, `SELECT count(*) FROM staging_claim`); n != 0 || calls != 0 {
		t.Fatalf("%d claims, %d generation creates", n, calls)
	}
	if n := count(t, ie.db, `SELECT count(*) FROM idempotency_record WHERE key LIKE 'k-fragment-%'`); n != 0 {
		t.Fatalf("%d records", n)
	}
}

// PA §3.1, §7.2: the draft check runs before ingestion. No If-Match is 428, a stale one 412, an
// active publication 409 naming it, a closed draft 409; none creates a claim, a generation or a
// record.
func TestFragmentPutPreconditions(t *testing.T) {
	ie := newFragmentEnv(t, options{})
	body := fragmentPut(t, "cluster", labelDoc, []string{labelMark}, nil)
	wantProblem(t, ie.putFragment("registries", body, "", "k-fragment-pre-0001"), http.StatusPreconditionRequired, "precondition-required")
	wantProblem(t, ie.putFragment("registries", body, `"1-aaaaaaaaaaaaaaaaaaaaaaaaaa"`, "k-fragment-pre-0002"),
		http.StatusPreconditionFailed, "precondition-failed")
	wantProblem(t, ie.putFragment("Not_A_Name", body, ie.etag, "k-fragment-pre-0003"), http.StatusBadRequest, "invalid-request")
	op := id.New(id.Operation)
	mustExec(t, ie.db, `INSERT INTO operation (id, kind, state, epoch, draft, draft_revision, owner_gen, created_by, created_by_kind,
		created_role, created_at) SELECT $1, 'publish', 'queued', epoch, $2, 1, 0, p.id, 'human', 'publisher', now()
		FROM installation_state, principal p WHERE p.sub = 'h-author'`, op, ie.draft)
	if doc := wantProblem(t, ie.putFragment("registries", body, ie.etag, "k-fragment-pre-0004"), http.StatusConflict, "conflict"); doc["operation"] != op {
		t.Fatalf("publication refusal %v", doc)
	}
	noIngestion(t, ie)

	closed := newFragmentEnv(t, options{})
	mustExec(t, closed.db, `UPDATE draft SET state = 'discarded' WHERE id = $1`, closed.draft)
	wantProblem(t, closed.putFragment("registries", body, closed.etag, "k-fragment-pre-0005"), http.StatusConflict, "conflict")
	noIngestion(t, closed)
}

// PA §7.2: a refusal after the claim exists is recorded with the claim abandoned. A retry replays
// it and ingests nothing; the key with another document is another request.
func TestFragmentPutRefusalReplayed(t *testing.T) {
	ie := newFragmentEnv(t, options{})
	const k = "k-fragment-replay-0001"
	body := fragmentPut(t, "cluster", labelDoc, []string{"doc[0]/machine/nodeLabels/absent"}, nil)
	first := ie.putFragment("registries", body, ie.etag, k)
	p := wantProblem(t, first, http.StatusUnprocessableEntity, "validation-failed")
	claim, _ := p["ingestion"].(string)
	if p["rule"] != "mark-unaddressed" || claim == "" {
		t.Fatalf("problem %v", p)
	}
	again := ie.putFragment("registries", body, ie.etag, k)
	wantProblem(t, again, http.StatusUnprocessableEntity, "validation-failed")
	if again.Header().Get("Idempotent-Replayed") != "true" || again.Body.String() != first.Body.String() {
		t.Fatalf("replay %v %s", again.Header(), again.Body)
	}
	other := ie.putFragment("registries", fragmentPut(t, "cluster", labelDoc, []string{labelMark}, nil), ie.etag, k)
	wantProblem(t, other, http.StatusUnprocessableEntity, "idempotency-key-reused")
	if n := count(t, ie.db, `SELECT count(*) FROM staging_claim WHERE state = 'abandoned' AND id = $1`, claim); n != 1 {
		t.Fatalf("claim %s not abandoned", claim)
	}
	if n := count(t, ie.db, `SELECT count(*) FROM staging_claim`); n != 1 {
		t.Fatalf("%d claims after the replays", n)
	}
	if ie.currentETag(t) != ie.etag {
		t.Fatal("a refused update moved the draft")
	}
}

// PA §7.2, T1: a draft that moves between ingestion and T1 refuses 412 there; the refusal is
// recorded with the claim abandoned and replayed. Its generation is left for the orphan report.
func TestFragmentPutMovedBeforeT1(t *testing.T) {
	var ie *ingestEnv
	moved := false
	ie = newFragmentEnv(t, options{beforeT1: func() {
		if !moved {
			moved = true
			mustExec(t, ie.db, `UPDATE draft SET revision = revision + 1 WHERE id = $1`, ie.draft)
		}
	}})
	const k = "k-fragment-moved-0001"
	body := fragmentPut(t, "cluster", labelDoc, []string{labelMark}, nil)
	p := wantProblem(t, ie.putFragment("registries", body, ie.etag, k), http.StatusPreconditionFailed, "precondition-failed")
	var state string
	if err := ie.db.QueryRow(`SELECT state FROM staging_claim WHERE id = $1`, p["ingestion"]).Scan(&state); err != nil || state != "abandoned" {
		t.Fatalf("claim %v state %q (%v)", p["ingestion"], state, err)
	}
	again := ie.putFragment("registries", body, ie.etag, k)
	wantProblem(t, again, http.StatusPreconditionFailed, "precondition-failed")
	if again.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatal("the 412 was not replayed")
	}
	if n := count(t, ie.db, `SELECT count(*) FROM fragment_revision`); n != 0 {
		t.Fatalf("%d revisions", n)
	}
	if calls, _ := ie.f.paths(); calls != 1 {
		t.Fatalf("%d generation creates; want the first run's one", calls)
	}
}

// PA §7.2: a live claim for the key with no record refuses 409 naming it; once its lease lapses
// it is abandoned and the PUT ingests afresh under a new claim.
func TestFragmentPutLiveClaimForKey(t *testing.T) {
	ie := newFragmentEnv(t, options{})
	const k = "k-fragment-live-0001"
	var principal string
	if err := ie.db.QueryRow(`SELECT id FROM principal WHERE sub = 'h-author'`).Scan(&principal); err != nil {
		t.Fatal(err)
	}
	c := staging.Claim{ID: id.New(id.Ingestion), Kind: "draft-update", Mode: "transient", Cluster: ie.cluster, Draft: ie.draft, Gen: 1}
	tx, err := ie.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := staging.Create(t.Context(), tx, ie.d.owner, staging.Timers{Lease: time.Minute, AbsoluteExpiry: time.Hour}, c, principal, k); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	body := fragmentPut(t, "cluster", labelDoc, []string{labelMark}, nil)
	if p := wantProblem(t, ie.putFragment("registries", body, ie.etag, k), http.StatusConflict, "conflict"); p["ingestion"] != c.ID {
		t.Fatalf("live claim refusal %v", p)
	}
	if calls, _ := ie.f.paths(); calls != 0 {
		t.Fatalf("%d generation creates under a live claim", calls)
	}
	mustExec(t, ie.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, c.ID)
	b := ie.fragmentOK(t, ie.putFragment("registries", body, ie.etag, k))
	if b.Ingestion == c.ID {
		t.Fatal("the lapsed claim was reused")
	}
	if n := count(t, ie.db, `SELECT count(*) FROM staging_claim WHERE id = $1 AND state = 'abandoned'`, c.ID); n != 1 {
		t.Fatal("the lapsed claim was not abandoned")
	}
}

// C §2.3, PA §7.2: a process killed at any step of a draft update's ingestion leaves its claim
// held under the request's key and writes nothing else: no revision, no record. Generations exist
// only once step 6 began. Once the lease lapses the sweep abandons the claim, and a retry with the
// key ingests afresh.
func TestFragmentPutInterrupted(t *testing.T) {
	for _, tc := range []struct {
		step    string
		creates int
	}{{"claim", 0}, {"read", 0}, {"guard", 0}, {"generation", 1}, {"construct", 1}} {
		t.Run(tc.step, func(t *testing.T) {
			stopped := false
			ie := newFragmentEnv(t, options{stopAt: func(s string) bool {
				if s != tc.step || stopped {
					return false
				}
				stopped = true
				return true
			}})
			const k = "k-fragment-killed-0001"
			body := fragmentPut(t, "cluster", labelDoc, []string{labelMark}, nil)
			wantProblem(t, ie.putFragment("registries", body, ie.etag, k), http.StatusInternalServerError, "internal-error")
			var claim, state string
			if err := ie.db.QueryRow(`SELECT id, state FROM staging_claim WHERE idempotency_key = $1`, k).Scan(&claim, &state); err != nil || state != "held" {
				t.Fatalf("claim %q state %q (%v)", claim, state, err)
			}
			if calls, _ := ie.f.paths(); calls != tc.creates {
				t.Fatalf("%d generation creates; want %d", calls, tc.creates)
			}
			if n := count(t, ie.db, `SELECT count(*) FROM fragment_revision`); n != 0 {
				t.Fatalf("%d revisions", n)
			}
			if n := count(t, ie.db, `SELECT count(*) FROM idempotency_record WHERE key = $1`, k); n != 0 {
				t.Fatalf("%d records", n)
			}
			mustExec(t, ie.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, claim)
			if n, err := staging.Sweep(t.Context(), ie.db); err != nil || n != 1 {
				t.Fatalf("sweep abandoned %d (%v)", n, err)
			}
			if b := ie.fragmentOK(t, ie.putFragment("registries", body, ie.etag, k)); b.Ingestion == claim {
				t.Fatal("the retry reused the swept claim")
			}
		})
	}
}

// C §13: no input text reaches a table, the log or a response, after a marked value and a schema
// secret are extracted and after refusals in parsing, the guard and a mark addressing nothing.
func TestFragmentPutNoEcho(t *testing.T) {
	ie := newFragmentEnv(t, options{})
	canary := "bw-canary-" + strings.ToLower(rand.Text())
	for i, c := range []struct {
		doc    string
		marks  []string
		status int
	}{
		{"machine:\n  nodeLabels:\n    tier: " + canary + "\n", []string{labelMark}, http.StatusOK},
		{"machine:\n  token: " + canary + "\n", []string{}, http.StatusOK},
		{"machine: [" + canary + "\n", []string{}, http.StatusUnprocessableEntity},
		{"machine:\n  token: " + canary + "\n  nodeLabels:\n    copy: x" + canary + "\n", []string{}, http.StatusUnprocessableEntity},
		{"machine:\n  token: " + canary + "\n", []string{"doc[0]/machine/nope"}, http.StatusUnprocessableEntity},
	} {
		rec := ie.putFragment(fmt.Sprintf("canary-%d", i), fragmentPut(t, "cluster", c.doc, c.marks, nil), ie.etag,
			fmt.Sprintf("k-fragment-echo-%04d", i))
		if rec.Code != c.status {
			t.Fatalf("case %d: %d %s", i, rec.Code, rec.Body)
		}
		if rec.Code == http.StatusOK {
			ie.etag = rec.Header().Get("ETag")
		}
	}
	assertAbsent(t, ie, canary)
}

// PA §9.2: the ingestion resource of a draft-update claim names its draft, with machine and
// operation null; an operator abandons a live one as any other, and the answer is the same shape.
func TestIngestionReadDraftUpdate(t *testing.T) {
	ie := newFragmentEnv(t, options{})
	b := ie.fragmentOK(t, ie.putFragment("registries", fragmentPut(t, "cluster", labelDoc, []string{labelMark}, nil), ie.etag,
		"k-fragment-read-0001"))
	got := decode[map[string]any](t, ie.do(ie.api, getIngestionCall(ie.human("h-author"), b.Ingestion)), http.StatusOK)
	if got["kind"] != "draft-update" || got["mode"] != "transient" || got["state"] != "released" || got["draft"] != ie.draft ||
		got["machine"] != nil || got["operation"] != nil {
		t.Fatalf("ingestion %v", got)
	}
	for _, m := range ingestionMembers {
		if _, ok := got[m]; !ok {
			t.Fatalf("member %s missing from %v", m, got)
		}
	}

	var principal string
	if err := ie.db.QueryRow(`SELECT id FROM principal WHERE sub = 'h-author'`).Scan(&principal); err != nil {
		t.Fatal(err)
	}
	c := staging.Claim{ID: id.New(id.Ingestion), Kind: "draft-update", Mode: "transient", Cluster: ie.cluster, Draft: ie.draft, Gen: 1}
	tx, err := ie.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := staging.Create(t.Context(), tx, ie.d.owner, staging.Timers{Lease: time.Minute, AbsoluteExpiry: time.Hour}, c, principal,
		"k-fragment-read-0002"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ab := decode[map[string]any](t, ie.do(ie.api, abandonCall(ie.human("h-author"), "k-abandon-draft-0001", c.ID)), http.StatusOK)
	if ab["state"] != "abandoned" || ab["draft"] != ie.draft || ab["machine"] != nil || ab["operation"] != nil {
		t.Fatalf("abandonment answer %v", ab)
	}
}

// PA §9.2: a fragment revision answers its declarations: each reference row's name with its kind,
// version and encoding, and the embedded documents it identifies, in the item and the list.
func TestFragmentRevisionDeclarations(t *testing.T) {
	ie := newFragmentEnv(t, options{})
	first := ie.fragmentOK(t, ie.putFragment("registries", fragmentPut(t, "cluster", labelDoc, []string{labelMark}, nil), ie.etag,
		"k-fragment-decl-0001"))
	minted := fragmentRefs(t, ie, *first.Entry.Revision)[0]
	doc := "machine:\n  nodeLabels:\n    tier: !bwref " + minted.name + "\n" +
		"cluster:\n  inlineManifests:\n    - name: m\n      contents: |\n        apiVersion: v1\n        kind: ConfigMap\n"
	decl := map[string]any{
		"references": map[string]any{minted.name: map[string]any{"kind": "string", "version": 1}},
		"embedded":   []any{map[string]any{"path": "doc[0]/cluster/inlineManifests/0/contents", "format": "yaml"}},
	}
	b := ie.fragmentOK(t, ie.putFragment("workers", fragmentPut(t, "role", doc, []string{}, decl), ie.etag, "k-fragment-decl-0002"))

	type revision struct {
		ID           string `json:"id"`
		Declarations struct {
			References map[string]map[string]any `json:"references"`
			Embedded   []map[string]any          `json:"embedded"`
		} `json:"declarations"`
	}
	check := func(r revision, embedded bool) {
		t.Helper()
		ref, ok := r.Declarations.References[minted.name]
		if len(r.Declarations.References) != 1 || !ok || ref["kind"] != "string" || ref["version"] != float64(1) || ref["encoding"] != nil {
			t.Fatalf("revision %s references %v", r.ID, r.Declarations.References)
		}
		if _, leaked := ref["generation"]; leaked {
			t.Fatalf("revision %s answers a generation path", r.ID)
		}
		switch {
		case r.Declarations.Embedded == nil:
			t.Fatalf("revision %s has no embedded member", r.ID)
		case embedded && (len(r.Declarations.Embedded) != 1 || r.Declarations.Embedded[0]["format"] != "yaml" ||
			r.Declarations.Embedded[0]["path"] != "doc[0]/cluster/inlineManifests/0/contents"):
			t.Fatalf("revision %s embedded %v", r.ID, r.Declarations.Embedded)
		case !embedded && len(r.Declarations.Embedded) != 0:
			t.Fatalf("revision %s embedded %v", r.ID, r.Declarations.Embedded)
		}
	}
	tok := ie.human("h-author")
	check(decode[revision](t, ie.do(ie.api, call{method: "GET", path: prefix + "/fragment-revisions/" + *first.Entry.Revision, token: tok}),
		http.StatusOK), false)
	check(decode[revision](t, ie.do(ie.api, call{method: "GET", path: prefix + "/fragment-revisions/" + *b.Entry.Revision, token: tok}),
		http.StatusOK), true)

	head := id.New(id.Fragment) // publication writes heads; the list needs one
	mustExec(t, ie.db, `INSERT INTO fragment (id, cluster, scope, name, layer, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, $2, 'cluster', 'workers', 'role', $3, 1, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, head, ie.cluster, *b.Entry.Revision)
	list := decode[struct{ Items []revision }](t, ie.do(ie.api, call{method: "GET", path: prefix + "/fragments/" + head + "/revisions", token: tok}),
		http.StatusOK)
	if len(list.Items) != 1 {
		t.Fatalf("listed %d revisions", len(list.Items))
	}
	check(list.Items[0], true)
}

// Review Focus 1, PA §7.2: a duplicate that arrives while the first PUT's T1 is uncommitted waits
// for the key's lock and then replays the first answer; it neither ingests nor answers the draft's
// new revision as a stale If-Match.
func TestFragmentPutConcurrentDuplicate(t *testing.T) {
	var ie *ingestEnv
	const k = "k-fragment-dup-0001"
	body := fragmentPut(t, "cluster", labelDoc, []string{labelMark}, nil)
	var put call
	done := make(chan *httptest.ResponseRecorder, 1)
	inT1, held := false, false
	ie = newFragmentEnv(t, options{
		afterEffect: func() { inT1 = true },
		commit: func(tx *sql.Tx) error {
			if inT1 && !held {
				held = true
				var t1 int
				if err := tx.QueryRow(`SELECT pg_backend_pid()`).Scan(&t1); err != nil {
					return err
				}
				go func() { done <- ie.do(ie.api, put) }()
				waitBlockedBy(t, ie.db, t1)
			}
			return tx.Commit()
		},
	})
	put = call{method: "PUT", path: prefix + "/drafts/" + ie.draft + "/fragments/registries", token: ie.human("h-author"), key: k,
		ifMatch: ie.etag, body: body}
	first := ie.do(ie.api, put)
	if !held {
		t.Fatal("the first PUT never reached T1's COMMIT")
	}
	var second *httptest.ResponseRecorder
	select {
	case second = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the duplicate never answered")
	}
	b := ie.fragmentOK(t, first)
	if second.Code != http.StatusOK || second.Header().Get("Idempotent-Replayed") != "true" || second.Body.String() != first.Body.String() {
		t.Fatalf("duplicate %d %v %s", second.Code, second.Header(), second.Body)
	}
	if n := count(t, ie.db, `SELECT count(*) FROM staging_claim`); n != 1 {
		t.Fatalf("%d claims for one key", n)
	}
	if n := count(t, ie.db, `SELECT count(*) FROM fragment_revision`); n != 1 {
		t.Fatalf("%d revisions for one key", n)
	}
	if b.Ingestion == "" {
		t.Fatal("no ingestion named")
	}
}

// PA §9.3: a draft's entries are as the update route answered them, so a fragment entry carries
// its sanitized document in the draft read too.
func TestFragmentPutDraftReadMatches(t *testing.T) {
	ie := newFragmentEnv(t, options{})
	b := ie.fragmentOK(t, ie.putFragment("registries", fragmentPut(t, "cluster", labelDoc, []string{labelMark}, nil), ie.etag,
		"k-fragment-match-0001"))
	d := decode[struct{ Entries []sourceEntry }](t, ie.do(ie.api, call{method: "GET", path: prefix + "/drafts/" + ie.draft,
		token: ie.human("h-author")}), http.StatusOK)
	if len(d.Entries) != 1 || d.Entries[0].Document == nil || *d.Entries[0].Document != *b.Entry.Document {
		t.Fatalf("draft entries %+v; want the answered entry %+v", d.Entries, b.Entry)
	}
}

// PA §5.1: a process whose epoch a recovery-mode entry superseded creates no draft-update claim;
// the PUT is refused before any ingestion starts.
func TestFragmentPutEpochTerm(t *testing.T) {
	ie := newFragmentEnv(t, options{})
	newEpoch(t, ie.db)
	rec := ie.putFragment("registries", fragmentPut(t, "cluster", labelDoc, []string{labelMark}, nil), ie.etag,
		"k-fragment-epoch-0001")
	wantProblem(t, rec, http.StatusServiceUnavailable, "epoch-superseded")
	var n int
	if err := ie.db.QueryRow(`SELECT count(*) FROM staging_claim WHERE kind = 'draft-update'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d draft-update claims, %v; want none", n, err)
	}
}
