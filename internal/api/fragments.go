package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/seam"
	"github.com/ginsys/bronzeward/internal/staging"
)

// PUT /drafts/{id}/fragments/{name} proposes a fragment revision (persistence-api.md §9.3). It is
// a draft-entry route (§7.2): its document is ingested before T1, outside the key's lock, under a
// transient draft-update claim the request's principal and key name (compilation.md §2.3 steps
// 0-7; step 8, the baseline, is an import's). T1 then holds the claim, takes the draft check,
// resolves every carried reference, writes the revision with its reference rows and the draft
// entry, and releases the claim. Its fingerprint is keyed (§7.1): the document is HMACed, never
// stored. A refusal after the claim exists is recorded with the claim abandoned, so a retry
// replays it; a failure the server or a dependency caused records nothing.
func fragmentUpdate() effectRoute {
	return effectRoute{action: "draft.fragment.update", input: func() input { return &fragmentInput{} },
		prepare: prepareFragment, effect: updateFragment, keyed: "document"}
}

type fragmentInput struct {
	Layer        string              `json:"layer"`
	Document     ingest.Unresolved   `json:"document"`
	Marks        []string            `json:"marks"`
	Declarations ingest.Declarations `json:"declarations"`
	marks        []ingest.Path
}

func (in *fragmentInput) check(*API) error {
	switch {
	case !slices.Contains(fragmentLayers, in.Layer):
		return errors.New("layer must be one of global, site, cluster, role, workload, override")
	case in.Document.Size() == 0:
		return errors.New("document is required")
	case len(in.Marks) > maxMarks:
		return fmt.Errorf("at most %d marks", maxMarks)
	}
	in.marks = make([]ingest.Path, len(in.Marks))
	for i, s := range in.Marks {
		p, err := ingest.ParsePath(s)
		if err != nil {
			return fmt.Errorf("marks[%d] is not a compilation §2.2 path", i)
		}
		in.marks[i] = p
	}
	return nil
}

func (in *fragmentInput) document() ingest.Unresolved { return in.Document }

// draftIngest is a draft update's ingestion as T1 takes it: the claim, the sanitized stream and
// the generation of each name it minted, or the refusal T1 records. It holds no input.
type draftIngest struct {
	claim     staging.Claim
	sanitized ingest.Sanitized
	gens      map[string]string
	ref       *refusal
	stop      func() // ends the claim's heartbeat and waits for it
}

// endIngest stops q's claim heartbeat, if a draft update started one.
func (q *request) endIngest() {
	if q.ingest != nil {
		q.ingest.stop()
	}
}

// prepareFragment is §2.3 steps 0-7. The claim transaction takes the key's lock first, so a
// duplicate of a request still in T1 waits for it and then finds its record: a record is replayed.
// A live claim for the key refuses 409, a due one is abandoned. Then the draft check: a missing,
// closed or moved draft refuses, and the transaction creates no claim and records nothing.
// Otherwise the claim is created there, and heartbeats until the request ends.
func prepareFragment(ctx context.Context, a *API, q *request) error {
	in := q.input.(*fragmentInput)
	if a.d.owner.ID == "" || a.d.ing == nil {
		return refuse(http.StatusServiceUnavailable, "dependency-unavailable", "ingestion is not configured; nothing was committed")
	}
	var c staging.Claim
	replay := false
	if err := a.inTx(ctx, func(tx *sql.Tx) error {
		if err := a.lockKey(ctx, tx, q); err != nil {
			return err
		}
		rec, err := lookup(ctx, tx, q)
		if err != nil || rec != nil {
			replay = rec != nil // the transaction answers it
			return err
		}
		var live string
		switch err := tx.QueryRowContext(ctx, `SELECT id FROM staging_claim WHERE principal = $1 AND idempotency_key = $2
			AND state IN ('held', 'resumed')`, q.principal.ID, q.key).Scan(&live); {
		case err == nil:
			abandoned, err := staging.AbandonDue(ctx, tx, live, q.epoch)
			if err != nil {
				return err
			}
			if !abandoned {
				return refuse(http.StatusConflict, "conflict", "a request with this key is still ingesting; retry once it has answered").
					with("ingestion", live)
			}
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		d, err := lockDraft(ctx, tx, q)
		if err != nil {
			return err
		}
		if _, err := keyOf(ctx, tx, q, d, "fragment"); err != nil {
			return err
		}
		c = staging.Claim{ID: id.New(id.Ingestion), Kind: "draft-update", Mode: "transient", Cluster: d.cluster, Draft: d.id, Gen: 1}
		return staging.Create(ctx, tx, a.d.owner, staging.Timers{Lease: a.d.timers.Lease, AbsoluteExpiry: a.d.timers.AbsoluteExpiry},
			c, q.principal.ID, q.key)
	}); err != nil || replay {
		return err
	}
	j := job{claim: c}
	pctx, cancel := context.WithCancel(ctx)
	var beat sync.WaitGroup
	beat.Add(1)
	go func() {
		defer beat.Done()
		a.heartbeat(pctx, cancel, j)
	}()
	g := &draftIngest{claim: c, stop: func() { cancel(); beat.Wait() }}
	q.ingest = g
	if err := a.step("claim"); err != nil {
		return err
	}
	if err := a.step("read"); err != nil {
		return err
	}
	var err error
	g.ref, err = a.extractDraft(pctx, j, in, g)
	in.Document = ingest.Unresolved{} // the last step to read it has run
	if err != nil {
		return err
	}
	if g.ref != nil {
		g.ref = g.ref.with("ingestion", c.ID)
	}
	if g.ref == nil || g.ref.status < http.StatusInternalServerError {
		if a.o.beforeT1 != nil {
			a.o.beforeT1()
		}
		return nil // T1 writes the update, or records its refusal
	}
	// A failure the server or a dependency caused records nothing; the claim is abandoned now.
	if err := a.inTx(ctx, func(tx *sql.Tx) error { return staging.Abandon(ctx, tx, a.d.owner, c) }); err != nil {
		a.o.logf("ingestion %s: the abandonment was not recorded: %v", c.ID, err)
	}
	return g.ref
}

// extractDraft is §2.3 steps 2-7 on the request's document: extract, create the generations, and
// the sanitized value set on g. A failed step is the refusal returned; an interruption the error.
func (a *API) extractDraft(ctx context.Context, j job, in *fragmentInput, g *draftIngest) (*refusal, error) {
	cand, err := ingest.Extract(ingest.Request{Input: in.Document, Marks: in.marks, Declarations: in.Declarations})
	if err != nil {
		return a.failure(j, "extraction", err), nil
	}
	if err := a.step("guard"); err != nil {
		return nil, err
	}
	gens := map[string]string{}
	create := a.createGeneration(j.claim, gens)
	s, err := cand.Commit(ctx, func(ctx context.Context, name string, v provider.Value) error {
		if err := create(ctx, name, v); err != nil {
			return err
		}
		return a.stop("generation") // Commit's own seam marks the fixture's point, after the last create
	})
	switch {
	case errors.Is(err, errKilled):
		return nil, err
	case err != nil:
		return a.failure(j, "generation create", err), nil
	}
	if err := a.step("construct"); err != nil {
		return nil, err
	}
	g.sanitized, g.gens = s, gens
	return nil, nil
}

// errKilled ends a draft update's ingestion or a publication at a test's interruption point
// (options.stopAt) as a killed process would end it: nothing more is written and nothing abandoned
// or failed; the claim or the publish job's lease lapses.
var errKilled = errors.New("stopped at an interruption point")

// step is an interruption point of a draft update's ingestion, named for compilation §2.3's step,
// or of a publication (compilation §15): the fixture's seam (internal/seam), then a test's stop.
func (a *API) step(name string) error {
	seam.At(name)
	return a.stop(name)
}

func (a *API) stop(name string) error {
	if a.o.stopAt != nil && a.o.stopAt(name) {
		return errKilled
	}
	return nil
}

// updateFragment is T1 (§5) for a fragment update: the claim held first, as every claim
// transaction takes it before the draft, then the draft check, the carried references resolved,
// the revision, its reference rows and the entry written, the claim released and the draft moved.
func updateFragment(ctx context.Context, a *API, tx *sql.Tx, q *request) (result, error) {
	in, g := q.input.(*fragmentInput), q.ingest
	if g == nil {
		return result{}, errors.New("a fragment update reached its transaction without a claim")
	}
	if err := staging.Hold(ctx, tx, a.d.owner, g.claim); err != nil {
		return result{}, err
	}
	if g.ref != nil {
		return g.refused(ctx, a, tx, q, g.ref)
	}
	d, err := lockDraft(ctx, tx, q)
	var k sourceKey
	if err == nil {
		k, err = keyOf(ctx, tx, q, d, "fragment")
	}
	if ref := (*refusal)(nil); errors.As(err, &ref) {
		return g.refused(ctx, a, tx, q, ref)
	} else if err != nil {
		return result{}, err
	}
	decl := g.sanitized.Declarations()
	gens, ref, err := resolveReferences(ctx, tx, d, decl, g.gens)
	switch {
	case err != nil:
		return result{}, err
	case ref != nil:
		return g.refused(ctx, a, tx, q, ref)
	}
	embedded := decl.Embedded
	if embedded == nil {
		embedded = []ingest.Embedded{}
	}
	emb, err := json.Marshal(embedded)
	if err != nil {
		return result{}, err
	}
	doc := string(g.sanitized.Documents())
	frv := id.New(id.FragmentRevision)
	if _, err := tx.ExecContext(ctx, `INSERT INTO fragment_revision (id, cluster, name, layer, document, author, created_at, embedded)
		VALUES ($1, $2, $3, $4, $5, $6, now(), $7::jsonb)`, frv, d.cluster, k.name, in.Layer, doc, q.principal.ID, string(emb)); err != nil {
		return result{}, err
	}
	for _, name := range slices.Sorted(maps.Keys(decl.References)) {
		r := decl.References[name]
		if _, err := tx.ExecContext(ctx, `INSERT INTO fragment_reference (revision, name, kind, version, encoding, generation)
			VALUES ($1, $2, $3, $4, $5, $6)`, frv, name, string(r.Kind), r.Version, nullable(r.Encoding), gens[name]); err != nil {
			return result{}, err
		}
	}
	e, err := setEntry(ctx, tx, d, k, frv)
	if err != nil {
		return result{}, err
	}
	e.Document = &doc
	if err := staging.Release(ctx, tx, a.d.owner, g.claim); err != nil {
		return result{}, err
	}
	res, err := d.answer(ctx, tx, e, frv, g.claim.ID)
	if err != nil {
		return result{}, err
	}
	res.body = sourceUpdate{Draft: d.id, Entry: e, Ingestion: g.claim.ID}
	return res, nil
}

// refused is a refusal after the claim exists (§7.2): the claim abandoned, and the refusal the
// response the record stores.
func (g *draftIngest) refused(ctx context.Context, a *API, tx *sql.Tx, q *request, ref *refusal) (result, error) {
	if err := staging.Abandon(ctx, tx, a.d.owner, g.claim); err != nil {
		return result{}, err
	}
	ref = ref.with("ingestion", g.claim.ID)
	return result{status: ref.status, body: problemDoc(q.id, ref), subjects: []string{g.claim.Draft, g.claim.ID}}, nil
}

// carriedReference is the generation of every reference row of a cluster with a name, kind and
// version: an import base's and a fragment's.
const carriedReference = `SELECT DISTINCT generation FROM (
	SELECT r.generation FROM import_base_reference r JOIN import_base_revision b ON b.id = r.revision
		JOIN machine m ON m.id = b.machine
		WHERE m.cluster = $1 AND r.name = $2 AND r.kind = $3 AND r.version = $4
	UNION ALL
	SELECT r.generation FROM fragment_reference r JOIN fragment_revision f ON f.id = r.revision
		WHERE f.cluster = $1 AND r.name = $2 AND r.kind = $3 AND r.version = $4) g
	ORDER BY generation LIMIT 2`

// resolveReferences maps each declared name to its generation: a name this ingestion minted to
// the generation it created, a carried name to the one generation the draft's cluster's reference
// rows with its name, kind and version hold. A carried name with no such row, or rows that differ,
// is refused unknown-reference, by its name; no value is involved.
func resolveReferences(ctx context.Context, tx *sql.Tx, d lockedDraft, decl ingest.Declarations, minted map[string]string) (map[string]string, *refusal, error) {
	gens := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(decl.References)) {
		if g, ok := minted[name]; ok {
			gens[name] = g
			continue
		}
		r := decl.References[name]
		var found []string
		rows, err := tx.QueryContext(ctx, carriedReference, d.cluster, name, string(r.Kind), r.Version)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var g string
			if err := rows.Scan(&g); err != nil {
				_ = rows.Close()
				return nil, nil, err
			}
			found = append(found, g)
		}
		if err := rows.Close(); err != nil {
			return nil, nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
		if len(found) != 1 {
			return nil, refuse(http.StatusUnprocessableEntity, "validation-failed",
				"a carried reference must name one generation of the draft's cluster with its declared kind and version").
				with("rule", "unknown-reference").with("reference", name), nil
		}
		gens[name] = found[0]
	}
	return gens, nil, nil
}
