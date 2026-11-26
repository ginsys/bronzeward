package api

import (
	"net/http"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// A published fragment head keeps its layer, a removed head included (0001_schema.sql: a head's
// revision keeps its layer), at cluster and at shared scope.

// layered inserts a fragment revision of name in layer, of the cluster or shared.
func (p *publishEnv) layered(shared bool, name, layer string) string {
	p.t.Helper()
	frv := id.New(id.FragmentRevision)
	mustExec(p.t, p.db, `INSERT INTO fragment_revision (id, cluster, name, layer, document, author, embedded, created_at)
		VALUES ($1, $2, $3, $4, 'machine: {}', $5, '[]', now())`, frv, ownerArg(shared, p.cluster), name, layer, p.seed)
	return frv
}

// ownerArg is the cluster column of a row: null for a shared one.
func ownerArg(shared bool, cluster string) any {
	if shared {
		return nil
	}
	return cluster
}

func layerCases(t *testing.T, run func(t *testing.T, shared, removed bool)) {
	for name, c := range map[string]struct{ shared, removed bool }{
		"a cluster head": {false, false}, "a removed cluster head": {false, true},
		"a shared head": {true, false}, "a removed shared head": {true, true},
	} {
		t.Run(name, func(t *testing.T) { run(t, c.shared, c.removed) })
	}
}

// seedLayerHead publishes a head of "layered" in the site layer at counter 2, removed or at a
// revision.
func (p *publishEnv) seedLayerHead(shared, removed bool) {
	p.t.Helper()
	var rev any
	if !removed {
		rev = p.layered(shared, "layered", "site")
	}
	scope := "cluster"
	if shared {
		scope = "library"
	}
	mustExec(p.t, p.db, `INSERT INTO fragment (id, cluster, scope, name, layer, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, $2, $3, 'layered', 'site', $4, 2, 'm3oxmlfh6phr7aigshdydcb4ji', now())`,
		id.New(id.Fragment), ownerArg(shared, p.cluster), scope, rev)
}

// An update at another layer than the published head's is refused when written, naming the body
// path; one at the head's layer is accepted.
func TestLibraryLayerKeptAtUpdate(t *testing.T) {
	layerCases(t, func(t *testing.T, shared, removed bool) {
		ie := newFragmentEnv(t, options{})
		d := &draftEnv{env: ie.env, cluster: ie.cluster, machine: ie.machine, draft: ie.draft, seed: id.New(id.Principal)}
		mustExec(t, ie.db, `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', 'https://idp.test', 'seed', now())`, d.seed)
		(&publishEnv{draftEnv: d}).seedLayerHead(shared, removed)
		body := func(layer string) string {
			scope := "cluster"
			if shared {
				scope = "library"
			}
			return `{"scope":"` + scope + `","layer":"` + layer + `","document":"machine:\n  env:\n    BW_LAYER: edge\n","marks":[]}`
		}
		p := wantProblem(t, ie.putFragment("layered", body("role"), ie.etag, "k-fragment-layer-0001"),
			http.StatusUnprocessableEntity, "validation-failed")
		if p["path"] != "layer" {
			t.Fatalf("problem %v; want the path layer", p)
		}
		if n := count(t, ie.db, `SELECT count(*) FROM draft_source_entry WHERE draft = $1`, ie.draft); n != 0 {
			t.Fatalf("%d entries after the refusal", n)
		}
		updated(t, &ie.etag, ie.putFragment("layered", body("site"), ie.etag, "k-fragment-layer-0002"))
	})
}

// A draft that holds a revision at another layer than its published head's is refused at
// publication, never as a broken constraint.
func TestLibraryLayerKeptAtPublication(t *testing.T) {
	layerCases(t, func(t *testing.T, shared, removed bool) {
		p := newPublishEnv(t)
		p.seedLayerHead(shared, removed)
		frv := p.layered(shared, "layered", "role")
		if shared {
			p.share("fragment", "layered", frv, 2, nil)
		} else {
			p.entry("fragment", "layered", frv)
			mustExec(t, p.db, `UPDATE draft_source_entry SET base = 2 WHERE draft = $1 AND name = 'layered'`, p.draft)
		}
		p.compiled()
		ref := p.refused(http.StatusUnprocessableEntity, "validation-failed")
		if ref.extra["path"] != "layer" || ref.extra["name"] != "layered" {
			t.Fatalf("refusal %v %v; want the layer of layered", ref, ref.extra)
		}
	})
}
