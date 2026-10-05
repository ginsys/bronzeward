package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// releaseSeed is a published release of d's draft, written as T3 writes one: two machines, one
// whose configuration could not be redacted, and a fragment source with a removed one.
type releaseSeed struct {
	rel, op, frg, frv, gone, machine2, ibr string
}

const releaseCipher = "vault:v1:c2VjcmV0LWNpcGhlcnRleHQ="

// releaseProvenance is two provenance records (compilation §8.2) as T3 stores them: the import
// base's occurrence, overridden by the fragment, and the fragment's mapping member, base64-encoded,
// at its output path. {ibr} and {frv} stand for the seed's revisions.
const releaseProvenance = `[{"reference":"registry/example-pass","version":3,` +
	`"source":{"revision":"{ibr}","digest":"` + hexA + `","path":"doc[0]/machine/registries/config/<redacted>/auth/password"},` +
	`"overriddenBy":{"revision":"{frv}","digest":"` + hexB + `"}},` +
	`{"reference":"registry/example-pass","version":3,"encoding":"base64","member":0,` +
	`"source":{"revision":"{frv}","digest":"` + hexB + `","path":"doc[0]/machine/registries"},` +
	`"output":"doc[0]/machine/registries/config/<redacted>/auth/password"}]`

const (
	hexA = "8c6976e5b5410415bde908bd4dee15dfb167a9c873fc4bb8a81f6f2ab448a918"
	hexB = "4fc82b26aecb47d2868c4efbe3581732a3e7cbcc6c2efb32062c08170a05eeb8"
)

func (d *draftEnv) release(draft string, revision int, provenance string) releaseSeed {
	d.t.Helper()
	t := d.t
	s := releaseSeed{rel: id.New(id.Release), op: id.New(id.Operation)}
	d.keys++
	rec := d.do(d.api, machineCall(d.human("h-author"), "k-machine2-"+draft[4:14], d.cluster, fmt.Sprintf("0b5a6c1e-2f3d-4e5f-8a9b-%012x", d.keys)))
	s.machine2 = decode[machineBody](t, rec, http.StatusCreated).ID
	ibr1, ibr2 := id.New(id.ImportBase), id.New(id.ImportBase)
	s.ibr = ibr1
	for _, ib := range [][2]string{{ibr1, d.machine}, {ibr2, s.machine2}} {
		mustExec(t, d.db, `INSERT INTO import_base_revision (id, machine, document, baseline_ciphertext, baseline_digest,
			baseline_digest_key, configuration_digest, created_at) VALUES ($1, $2, 'machine: {}', '\x01', $3, 'transit/baseline-digest:1', $3, now())`,
			ib[0], ib[1], make([]byte, 32))
	}
	s.frv = d.fragmentRevision(d.cluster, "registries-"+draft[4:8], "override")
	s.frg = d.fragmentHead("registries-"+draft[4:8], "override", s.frv, 1)
	s.gone = d.fragmentHead("gone-"+draft[4:8], "site", nil, 2)
	mustExec(t, d.db, `INSERT INTO operation (id, kind, state, owner, owner_gen, owner_epoch, lease_until, draft, draft_revision,
			created_by, created_by_kind, created_role, epoch, created_at)
		SELECT $1, 'publish', 'running', 'run-1/4242/publish', 1, epoch, now() + interval '1 minute', $2, $3, $4, 'human', 'publisher', epoch, now()
		FROM installation_state`, s.op, draft, revision, d.seed)
	tx, err := d.db.Begin() // a release's rows are written with it (PA §3)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(t, tx, `INSERT INTO release (id, cluster, draft, draft_revision, digest, contract, machinery_version, machinery_checksum,
			kubernetes_version, operation, published_by, published_role, epoch, published_at)
		SELECT $1, $2, $3, $4, $5, 'v1.13', 'v1.13.6', 'h1:2rBcdYQ4m1u3oPmvbMQw3F9dZb8i0EwQnJ6y5Kx8sJ0=', 'v1.36.0', $6, $7,
			'publisher', epoch, '2026-09-26T09:14:05Z' FROM installation_state`, s.rel, d.cluster, draft, revision, make([]byte, 32), s.op, d.seed)
	for _, m := range []struct {
		machine, ibr string
		redacted     any
	}{{d.machine, ibr1, "machine:\n  type: worker\n  token: <redacted:schema>\n"}, {s.machine2, ibr2, nil}} {
		mustExec(t, tx, `INSERT INTO release_machine (release, cluster, machine, import_base_revision, mode, ciphertext,
				ciphertext_digest, configuration_digest, redacted, provenance)
			VALUES ($1, $2, $3, $4, 'container', $5, $6, $6, $7, $8::jsonb)`,
			s.rel, d.cluster, m.machine, m.ibr, releaseCipher, make([]byte, 32), m.redacted,
			strings.NewReplacer("{ibr}", m.ibr, "{frv}", s.frv).Replace(provenance))
	}
	mustExec(t, tx, `INSERT INTO release_source (release, cluster, kind, fragment, name, fragment_revision, head_revision)
		VALUES ($1, $2, 'fragment', $3, $4, $5, 1), ($1, $2, 'fragment', $6, $7, NULL, 2)`,
		s.rel, d.cluster, s.frg, "registries-"+draft[4:8], s.frv, s.gone, "gone-"+draft[4:8])
	mustExec(t, tx, `UPDATE draft SET state = 'published', release = $2 WHERE id = $1`, draft, s.rel)
	mustExec(t, tx, `UPDATE operation SET state = 'succeeded', owner = NULL, owner_epoch = NULL, lease_until = NULL,
		result = jsonb_build_object('release', $2::text) WHERE id = $1`, s.op, s.rel)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return s
}

// PA §9.2 and its read example: any role reads a release, its sources and machines, and each
// machine's redacted review data (compilation §8.3, §11); a configuration that could not be
// redacted shows nothing and says so. No answer carries ciphertext or a digest (PA §9.1).
func TestReleaseReads(t *testing.T) {
	d := newDraftEnv(t)
	s := d.release(d.draft, 1, releaseProvenance)

	rec := d.get("/releases/" + s.rel)
	r := decode[releaseBody](t, rec, http.StatusOK)
	if r.ID != s.rel || r.Cluster != d.cluster || r.Draft != d.draft || r.DraftRevision != 1 || r.Operation != s.op ||
		r.PublishedBy != (createdBy{Principal: d.seed, Role: "publisher"}) ||
		r.PublishedAt.Format("2006-01-02T15:04:05Z07:00") != "2026-09-26T09:14:05Z" ||
		r.Renderer != (rendererBody{Contract: "v1.13", MachineryVersion: "v1.13.6",
			MachineryChecksum: "h1:2rBcdYQ4m1u3oPmvbMQw3F9dZb8i0EwQnJ6y5Kx8sJ0=", KubernetesVersion: "v1.36.0"}) {
		t.Fatalf("release %+v", r)
	}
	wantSources := []releaseSourceBody{{Kind: "fragment", Head: s.frg, Revision: &s.frv, HeadRevision: 1},
		{Kind: "fragment", Head: s.gone, HeadRevision: 2}}
	slices.SortFunc(wantSources, func(a, b releaseSourceBody) int { return strings.Compare(a.Head, b.Head) })
	if len(r.Sources) != 2 {
		t.Fatalf("sources %+v", r.Sources)
	}
	for i, w := range wantSources {
		g := r.Sources[i]
		if g.Kind != w.Kind || g.Head != w.Head || g.HeadRevision != w.HeadRevision || (g.Revision == nil) != (w.Revision == nil) ||
			(g.Revision != nil && *g.Revision != *w.Revision) {
			t.Fatalf("source %d %+v; want %+v", i, g, w)
		}
	}
	machines := []string{d.machine, s.machine2}
	slices.Sort(machines)
	if len(r.Machines) != 2 {
		t.Fatalf("machines %+v", r.Machines)
	}
	for i, m := range machines {
		want := releaseMachineBody{Machine: m, Mode: "container", Review: prefix + "/releases/" + s.rel + "/machines/" + m + "/review"}
		if r.Machines[i] != want {
			t.Fatalf("machine %d %+v; want %+v", i, r.Machines[i], want)
		}
	}
	for _, leak := range []string{releaseCipher, "digest", "ciphertext", "vault:"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("release answer holds %q: %s", leak, rec.Body)
		}
	}

	rec = d.get("/releases/" + s.rel + "/machines/" + d.machine + "/review")
	v := decode[reviewBody](t, rec, http.StatusOK)
	if v.Release != s.rel || v.Machine != d.machine || v.Mode != "container" || v.Configuration == nil ||
		*v.Configuration != "machine:\n  type: worker\n  token: <redacted:schema>\n" || v.Notice != "" {
		t.Fatalf("review %+v", v)
	}
	// The records answered are the records stored, field for field.
	var answered struct {
		Provenance json.RawMessage `json:"provenance"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answered); err != nil {
		t.Fatal(err)
	}
	var gotProv, wantProv any
	if err := json.Unmarshal(answered.Provenance, &gotProv); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(strings.NewReplacer("{ibr}", s.ibr, "{frv}", s.frv).Replace(releaseProvenance)), &wantProv); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotProv, wantProv) {
		t.Fatalf("provenance %s; want %s", answered.Provenance, releaseProvenance)
	}
	rec = d.get("/releases/" + s.rel + "/machines/" + s.machine2 + "/review")
	if strings.Contains(rec.Body.String(), "vault:") {
		t.Fatalf("review answer holds ciphertext: %s", rec.Body)
	}
	if v := decode[reviewBody](t, rec, http.StatusOK); v.Configuration != nil || v.Notice == "" {
		t.Fatalf("withheld review %+v; want no configuration and a notice", v)
	}

	// A second release on another draft: the list pages in identifier order, each item with its
	// own sources and machines.
	rec = d.do(d.api, call{method: "POST", path: prefix + "/drafts", token: d.human("h-author"), key: "k-draft2-0123456789",
		body: `{"cluster":"` + d.cluster + `","title":"second"}`})
	draft2 := decode[draftBody](t, rec, http.StatusCreated).ID
	s2 := d.release(draft2, 1, releaseProvenance)
	list := decode[listPage[releaseBody]](t, d.get("/releases"), http.StatusOK)
	want := []string{s.rel, s2.rel}
	slices.Sort(want)
	if len(list.Items) != 2 || list.Items[0].ID != want[0] || list.Items[1].ID != want[1] || list.Next != "" {
		t.Fatalf("releases %+v; want %v", list, want)
	}
	for _, it := range list.Items {
		one := decode[releaseBody](t, d.get("/releases/"+it.ID), http.StatusOK)
		if len(it.Sources) != 2 || len(it.Machines) != 2 || !sameSources(it.Sources, one.Sources) ||
			!slices.Equal(it.Machines, one.Machines) {
			t.Fatalf("listed %+v; read %+v", it, one)
		}
	}
	page1 := decode[listPage[releaseBody]](t, d.get("/releases?limit=1"), http.StatusOK)
	if len(page1.Items) != 1 || page1.Items[0].ID != want[0] || page1.Next == "" {
		t.Fatalf("first page %+v", page1)
	}
	page2 := decode[listPage[releaseBody]](t, d.get("/releases?limit=1&cursor="+page1.Next), http.StatusOK)
	if len(page2.Items) != 1 || page2.Items[0].ID != want[1] || page2.Next != "" {
		t.Fatalf("second page %+v", page2)
	}

	// Refusals: an unknown release, an identifier of another entity, a machine the release does
	// not cover, and a query on an item read.
	for _, c := range []struct {
		path   string
		status int
		code   string
	}{
		{"/releases/" + id.New(id.Release), http.StatusNotFound, "not-found"},
		{"/releases/" + d.draft, http.StatusNotFound, "not-found"},
		{"/releases/" + id.New(id.Release) + "/machines/" + d.machine + "/review", http.StatusNotFound, "not-found"},
		{"/releases/" + s.rel + "/machines/" + id.New(id.Machine) + "/review", http.StatusNotFound, "not-found"},
		{"/releases/" + s.rel + "/machines/" + d.draft + "/review", http.StatusNotFound, "not-found"},
		{"/releases/" + d.draft + "/machines/" + d.machine + "/review", http.StatusNotFound, "not-found"},
		{"/releases/" + s2.rel + "/machines/" + "mch_" + strings.Repeat("a", 26) + "/review", http.StatusNotFound, "not-found"},
		{"/releases/" + s.rel + "?x=1", http.StatusBadRequest, "invalid-request"},
		{"/releases/" + s.rel + "/machines/" + d.machine + "/review?x=1", http.StatusBadRequest, "invalid-request"},
	} {
		wantProblem(t, d.get(c.path), c.status, c.code)
	}
}

// The review answers each provenance record by compilation §8.2's fields alone: a stored record
// with another field, or one that breaks its shape, is answered as nothing and logged without its
// content, never forwarded.
func TestReleaseReviewProvenanceProjected(t *testing.T) {
	d := newDraftEnv(t)
	const standIn = "bw-stand-in-7f3c" // a field's content that must reach neither answer nor log
	good := `{"reference":"registry/example-pass","version":3,"source":{"revision":"{frv}","digest":"` + hexB +
		`","path":"doc[0]/machine/registries"},"output":"doc[0]/machine/registries"}`
	for i, c := range []struct{ name, stored string }{
		{"another field", `[{"reference":"registry/example-pass","version":3,"value":"` + standIn + `","source":{"revision":"{frv}",` +
			`"digest":"` + hexB + `","path":"doc[0]/machine/registries"},"output":"doc[0]/machine/registries"}]`},
		{"another source field", strings.Replace("["+good+"]", `"path"`, `"text":"`+standIn+`","path"`, 1)},
		{"both outcomes", `[` + strings.Replace(good, `"output"`, `"overriddenBy":{"revision":"{frv}","digest":"`+hexB+`"},"output"`, 1) + `]`},
		{"no outcome", `[` + strings.Replace(good, `,"output":"doc[0]/machine/registries"`, ``, 1) + `]`},
		{"a digest that is not SHA-256 hex", `[` + strings.Replace(good, hexB, standIn, 1) + `]`},
		{"a source revision of another kind", `[` + strings.Replace(good, `{frv}`, d.draft, 1) + `]`},
		{"no reference", `[` + strings.Replace(good, `"registry/example-pass"`, `""`, 1) + `]`},
		{"version 0", `[` + strings.Replace(good, `"version":3`, `"version":0`, 1) + `]`},
		{"a member below 0", `[` + strings.Replace(good, `"version":3`, `"version":3,"member":-1`, 1) + `]`},
		{"a record that is not an object", `["` + standIn + `"]`},
		{"an encoding outside the enum", `[` + strings.Replace(good, `"version":3`, `"version":3,"encoding":"`+standIn+`"`, 1) + `]`},
		{"a reference outside the grammar", `[` + strings.Replace(good, `"registry/example-pass"`, `"`+standIn+` x"`, 1) + `]`},
		{"a field name in another case", `[` + strings.Replace(good, `"reference"`, `"REFERENCE"`, 1) + `]`},
		{"a null member", `[` + strings.Replace(good, `"version":3`, `"version":3,"member":null`, 1) + `]`},
		{"a null second outcome", `[` + strings.Replace(good, `"output"`, `"overriddenBy":null,"output"`, 1) + `]`},
		{"an empty source path", `[` + strings.Replace(good, `"path":"doc[0]/machine/registries"`, `"path":""`, 1) + `]`},
		{"an override by the import base", `[` + strings.Replace(good, `"output":"doc[0]/machine/registries"`,
			`"overriddenBy":{"revision":"`+id.New(id.ImportBase)+`","digest":"`+hexB+`"}`, 1) + `]`},
		{"an override digest that is not SHA-256 hex", `[` + strings.Replace(good, `"output":"doc[0]/machine/registries"`,
			`"overriddenBy":{"revision":"{frv}","digest":"`+standIn+`"}`, 1) + `]`},
		{"a digest in capitals", `[` + strings.Replace(good, hexB, strings.ToUpper(hexB), 1) + `]`},
		{"a field whose name carries the content", `[` + strings.Replace(good, `"version":3`, `"version":3,"`+standIn+`":1`, 1) + `]`},
		{"a reference of 257 bytes", `[` + strings.Replace(good, `"registry/example-pass"`, `"`+strings.Repeat("a", 257)+`"`, 1) + `]`},
		{"control: a reference of 256 bytes", `[` + strings.Replace(good, `"registry/example-pass"`, `"`+strings.Repeat("a", 256)+`"`, 1) + `]`},
		// compilation §8.3: a stored path is a path, or <redacted> whole.
		{"a source path outside the path grammar", `[` + strings.Replace(good, `"path":"doc[0]/machine/registries"`,
			`"path":"`+standIn+`"`, 1) + `]`},
		{"an output path outside the path grammar", `[` + strings.Replace(good, `"output":"doc[0]/machine/registries"`,
			`"output":"`+standIn+`"`, 1) + `]`},
		{"control: paths redacted whole", `[` + strings.ReplaceAll(good, `"doc[0]/machine/registries"`, `"<redacted>"`) + `]`},
		{"control", `[` + good + `]`}, // the record each case above breaks one way
	} {
		draft := d.draft
		if i > 0 {
			rec := d.do(d.api, call{method: "POST", path: prefix + "/drafts", token: d.human("h-author"),
				key: fmt.Sprintf("k-draft-prov-%010d", i), body: `{"cluster":"` + d.cluster + `","title":"provenance"}`})
			draft = decode[draftBody](t, rec, http.StatusCreated).ID
		}
		s := d.release(draft, 1, c.stored)
		rec := d.get("/releases/" + s.rel + "/machines/" + d.machine + "/review")
		if strings.HasPrefix(c.name, "control") {
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: %d %s", c.name, rec.Code, rec.Body)
			}
			continue
		}
		wantProblem(t, rec, http.StatusInternalServerError, "internal-error")
		if strings.Contains(rec.Body.String(), standIn) || d.logged(standIn) {
			t.Fatalf("%s: the stored content reached the answer or the log: %s", c.name, rec.Body)
		}
	}
}

// sameSources compares two source lists member by member, since Revision is a pointer.
func sameSources(a, b []releaseSourceBody) bool {
	return slices.EqualFunc(a, b, func(x, y releaseSourceBody) bool {
		return x.Kind == y.Kind && x.Head == y.Head && x.HeadRevision == y.HeadRevision && (x.Revision == nil) == (y.Revision == nil) &&
			(x.Revision == nil || *x.Revision == *y.Revision)
	})
}
