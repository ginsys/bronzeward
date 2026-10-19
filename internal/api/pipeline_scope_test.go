package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// C §5.1/§13: validate all source scopes before the first provider access, even
// when the foreign reference follows a valid one. This deliberately supplies an
// inconsistent snapshot; supported authoring already prevents its construction.
func TestReadPinnedClusterScope(t *testing.T) {
	for _, origin := range []string{"import-base", "fragment"} {
		for _, foreign := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/foreign=%t", origin, foreign), func(t *testing.T) {
				p := newPublishEnv(t)
				generation := p.kvPath
				if foreign {
					generation = strings.Replace(generation, "/"+p.cluster+"/", "/"+id.New(id.Cluster)+"/", 1)
				}
				revision, earlier := p.ibr, p.networkNew
				if origin == "fragment" {
					revisions := []string{p.networkNew, id.New(id.FragmentRevision)}
					slices.Sort(revisions)
					earlier, revision = revisions[0], revisions[1]
				}
				if earlier >= revision {
					t.Fatal("fixture must put the valid source before the tested source")
				}
				s := snapshot{sources: map[string]storedSource{revision: {
					declarations: ingest.Declarations{References: map[string]ingest.Reference{
						"a-current": {Kind: provider.KindString, Version: 1},
						"z-target":  {Kind: provider.KindString, Version: 1},
					}},
					generations: map[string]string{"a-current": p.kvPath, "z-target": generation},
				}}}
				s.sources[earlier] = storedSource{
					declarations: ingest.Declarations{References: map[string]ingest.Reference{
						"earlier-current": {Kind: provider.KindString, Version: 1},
					}},
					generations: map[string]string{"earlier-current": p.kvPath},
				}
				var classifications, reads int
				got, ref, err := p.a.readPinned(t.Context(), p.job, s,
					standInMeta{kv: map[string]classify.Answer{p.kvPath: kvAnswer(nil, kvCreated), generation: kvAnswer(nil, kvCreated)}, calls: &classifications},
					standInReader{created: map[string]time.Time{p.kvPath: kvCreated, generation: kvCreated}, calls: &reads})
				if foreign {
					if err != nil || ref == nil || ref.status != http.StatusUnprocessableEntity || ref.code != "validation-failed" ||
						classifications != 0 || reads != 0 || len(got.values) != 0 || len(got.statuses) != 0 {
						t.Fatalf("foreign reference: ref=%v err=%v classifications=%d reads=%d; want 422 before provider access", ref, err, classifications, reads)
					}
					if ref.extra["reference"] != "z-target" || ref.extra["source"] != revision ||
						strings.Contains(fmt.Sprint(ref.extra)+ref.detail, pinHeld) || p.logged(pinHeld) {
						t.Fatal("scope refusal lacks its source/reference or exposes the synthetic value")
					}
					return
				}
				if err != nil || ref != nil || classifications != 1 || reads != 1 ||
					len(got.values[revision]) != 2 || len(got.values[earlier]) != 1 {
					t.Fatalf("same-cluster control: ref=%v err=%v classifications=%d reads=%d", ref, err, classifications, reads)
				}
			})
		}
	}
}

type scopeCountingProvider struct {
	heldValues
	calls atomic.Int32
}

func (p *scopeCountingProvider) KV(ctx context.Context, path provider.GenerationPath) (classify.Answer, error) {
	p.calls.Add(1)
	return p.heldValues.KV(ctx, path)
}

func (p *scopeCountingProvider) Transit(ctx context.Context, key string) (classify.Answer, error) {
	p.calls.Add(1)
	return p.heldValues.Transit(ctx, key)
}

func (p *scopeCountingProvider) ReadGeneration(ctx context.Context, path provider.GenerationPath, version int64) (provider.Value, time.Time, error) {
	p.calls.Add(1)
	return p.heldValues.ReadGeneration(ctx, path, version)
}

func (p *scopeCountingProvider) EncryptArtifact(ctx context.Context, plaintext []byte) (provider.Ciphertext, error) {
	p.calls.Add(1)
	return p.heldValues.EncryptArtifact(ctx, plaintext)
}

// A stored cross-cluster reference is refused by the actual publication worker:
// safe failed operation/event, original draft still open, no release or provider
// access. The seed is intentionally inconsistent, not a supported authoring act.
func TestPublishWorkerRefusesForeignGeneration(t *testing.T) {
	b := newBuildEnv(t)
	src := b.ingested(buildFragment(""))
	if len(src.gens) != 1 {
		t.Fatal("fixture must mint exactly one fragment reference")
	}
	foreign := id.New(id.Cluster)
	for name, generation := range src.gens {
		next := strings.Replace(generation, "/"+b.cluster+"/", "/"+foreign+"/", 1)
		if next == generation {
			t.Fatal("fixture did not change the reference's cluster")
		}
		src.gens[name], b.held.values[next] = next, b.held.values[generation]
	}
	frv := id.New(id.FragmentRevision)
	b.store(`INSERT INTO fragment_revision (id, cluster, name, layer, document, author, embedded, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())`, "fragment_reference", src,
		frv, b.cluster, "network", "site", src.document, b.seed, src.embedded)
	b.entry("fragment", "network", frv)
	mustExec(t, b.db, `UPDATE draft_source_entry SET base = (SELECT head_revision FROM fragment WHERE cluster = $2 AND name = 'network')
		WHERE draft = $1 AND kind = 'fragment' AND name = 'network'`, b.draft, b.cluster)
	mustExec(t, b.db, `DELETE FROM operation WHERE id = $1`, b.job.op)
	life, stop := context.WithCancel(t.Context())
	a := b.worker("run-1/1/cluster-scope", life, idle)
	provider := &scopeCountingProvider{heldValues: b.held}
	a.d.pub = &publishClients{meta: provider, reader: provider, encrypter: provider}
	a.startPublisher()
	defer stopping(a, stop)
	op := decode[operationBody](t, b.publish(a, b.etag, b.key(), `{}`), http.StatusAccepted).ID
	state, _, events := b.ended(op)
	if state != "failed" || !slices.Equal(events, []string{"queued", "claimed", "failed"}) || provider.calls.Load() != 0 {
		t.Fatalf("state=%s events=%v provider calls=%d", state, events, provider.calls.Load())
	}
	var problem string
	if err := b.db.QueryRow(`SELECT error::text FROM operation WHERE id = $1 AND error->>'type' = 'urn:bronzeward:problem:validation-failed'
		AND error->>'source' = $2`, op, frv).Scan(&problem); err != nil {
		t.Fatal(err)
	}
	if count(t, b.db, `SELECT count(*) FROM release`) != 0 ||
		count(t, b.db, `SELECT count(*) FROM draft WHERE id = $1 AND state = 'open'`, b.draft) != 1 ||
		count(t, b.db, `SELECT count(*) FROM act WHERE action = 'draft.publish'`) != 1 {
		t.Fatal("refusal changed the draft, committed a release or lost the publication act")
	}
	b.noSecretIn(problem)
}
