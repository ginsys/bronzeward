package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/seam"
	"github.com/ginsys/bronzeward/internal/staging"
)

// The ingest runner takes a job after T11's COMMIT and runs compilation.md §2.3 steps 2-9 outside
// any request: extract, create the generations, compute the baseline, seal and store an
// encrypted claim's envelope, then T1 (persistence-api.md §5) or the owner's abandonment. No
// transaction spans provider I/O. Every write is fenced on the claim's owner generation, so a
// run that lost its claim writes nothing; a run that stops (ownership lost, the server stopping,
// a database failure) leaves the claim to its lease and the sweep.

// startRunner hands j to the runner, after T11's COMMIT and outside any transaction.
func (a *API) startRunner(j job) {
	if a.o.onRunner != nil {
		a.o.onRunner(j)
		return
	}
	a.d.runs.Add(1)
	go func() {
		defer a.d.runs.Done()
		a.runIngest(a.d.life, j)
	}()
}

// imported is what T1 persists: the sanitized stream, the generation of each name and the
// baseline. It holds no input.
type imported struct {
	sanitized ingest.Sanitized
	gens      map[string]string
	baseline  ingest.Baseline
}

// errStop ends a run without an outcome: the claim is no longer this run's, or the outcome of
// the run's last write is unknown. The lease decides what happens next.
var errStop = errors.New("the run stops")

// errPaused ends a run whose claim paused for the operator's review (compilation §3.6): the
// envelope is stored and the operation stays running until a mark or a continuation takes it.
var errPaused = errors.New("the run paused for the operator's review")

// runIngest runs j to its end, heartbeating the claim every timers.Heartbeat until it returns.
func (a *API) runIngest(ctx context.Context, j job) {
	ctx, cancel := context.WithCancel(ctx)
	var beat sync.WaitGroup
	beat.Add(1)
	go func() {
		defer beat.Done()
		a.heartbeat(ctx, cancel, j)
	}()
	defer func() {
		cancel()
		beat.Wait()
	}()
	var imp imported
	var ref *refusal
	var err error
	if j.resume != nil {
		imp, ref, err = a.resume(ctx, j)
	} else {
		// The fixture's interruption points (internal/seam), named for compilation §2.3's steps;
		// empty in every build but the fixture's instance C.
		seam.At("claim")
		if j.node != nil {
			j.input, ref, err = a.readNode(ctx, j)
		}
		if ref == nil && err == nil {
			seam.At("read")
			imp, ref, err = a.stage(ctx, j)
		}
	}
	j.input = ingest.Unresolved{} // step 8 was the last to read it
	switch {
	case errors.Is(err, errPaused):
		a.o.logf("ingestion %s: paused for the operator's review", j.claim.ID)
		return
	case ctx.Err() != nil:
		a.o.logf("ingestion %s: stopped before the draft transaction", j.claim.ID)
		return
	case err != nil:
		a.o.logf("ingestion %s: %v", j.claim.ID, err)
		return
	case ref != nil:
		a.abandon(ctx, j, ref)
		return
	}
	if a.o.afterStage != nil {
		a.o.afterStage()
	}
	if a.o.beforeT1 != nil {
		a.o.beforeT1()
	}
	switch ref, err := a.commitImport(ctx, j, imp); {
	case err != nil:
		a.o.logf("ingestion %s: the draft transaction: %v", j.claim.ID, err)
	case ref != nil:
		a.abandon(ctx, j, ref)
	}
}

// heartbeat extends the claim's lease until ctx ends; a heartbeat the fence refuses stops the run.
func (a *API) heartbeat(ctx context.Context, stop context.CancelFunc, j job) {
	t := time.NewTicker(a.d.timers.Heartbeat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		err := staging.Heartbeat(ctx, a.db, a.d.owner, j.claim, a.d.timers.Lease)
		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, staging.ErrFenced), errors.Is(err, staging.ErrEpochSuperseded):
			a.o.logf("ingestion %s: heartbeat refused (%v); the run stops", j.claim.ID, err)
			stop()
			return
		case err != nil: // the lease may still hold; the next heartbeat tries again
			a.o.logf("ingestion %s: %v", j.claim.ID, err)
		}
	}
}

// stage runs steps 2-8 and, for an encrypted claim, stores the sealed envelope with the staged
// event. A claim whose review is pending pauses in the same transaction (compilation §3.6) and the
// run ends with errPaused. A refusal is the operation's outcome; an error stops the run.
func (a *API) stage(ctx context.Context, j job) (imported, *refusal, error) {
	c, err := ingest.Extract(ingest.Request{Input: j.input, Marks: j.marks, Declarations: j.decl})
	if err != nil {
		return imported{}, a.failure(j, "extraction", err), nil
	}
	seam.At("guard")
	gens := map[string]string{}
	s, err := c.Commit(ctx, a.createGeneration(j.claim, gens))
	if err != nil {
		return imported{}, a.failure(j, "generation create", err), nil
	}
	seam.At("construct")
	b, err := ingest.ComputeBaseline(ctx, j.input, a.d.ing.EncryptBaseline, a.d.ing.Digest)
	if err != nil {
		return imported{}, a.failure(j, "baseline", err), nil
	}
	seam.At("baseline")
	imp := imported{sanitized: s, gens: gens, baseline: b}
	if j.claim.Mode != "encrypted" {
		return imp, nil, nil
	}
	plain, sum, err := ingest.Staged{Sanitized: s, Generations: gens, Baseline: b}.Seal()
	if err != nil {
		return imported{}, a.failure(j, "envelope", err), nil
	}
	ct, err := a.d.ing.EncryptStaging(ctx, plain)
	if err != nil {
		return imported{}, a.failure(j, "envelope encryption", err), nil
	}
	review := j.claim.Review == "pending"
	if err := a.inTx(ctx, func(tx *sql.Tx) error {
		store := staging.StorePayload
		if review {
			store = staging.Pause
		}
		if err := store(ctx, tx, a.d.owner, j.claim, []byte(ct), sum); err != nil {
			return err
		}
		if err := a.event(ctx, tx, j, map[string]any{"type": "staged"}); err != nil || !review {
			return err
		}
		return a.event(ctx, tx, j, map[string]any{"type": "paused", "generation": j.claim.Gen})
	}); err != nil {
		return imported{}, nil, fmt.Errorf("storing the envelope: %w", err)
	}
	seam.At("staged")
	if review {
		return imported{}, nil, errPaused
	}
	return imp, nil, nil
}

// createGeneration is step 6's create for claim: each value at a fresh path under the claim's
// cluster and id, its path recorded in gens by name.
func (a *API) createGeneration(claim staging.Claim, gens map[string]string) func(context.Context, string, provider.Value) error {
	return func(ctx context.Context, name string, v provider.Value) error {
		p, err := provider.NewGenerationPath(claim.Cluster, claim.ID, provider.NewValueID())
		if err != nil {
			return err
		}
		g, err := a.d.ing.CreateGeneration(ctx, p, v)
		if err != nil {
			return err
		}
		gens[name] = g.Path.String()
		return nil
	}
}

// resume is a taken-over run's steps 2-8 (compilation §3.4): the staged envelope decrypted and
// opened in place of extraction, generation creates and the baseline, which ran before the
// takeover. A decryption failure leaves the claim resumed under this owner with a resume-failed
// event, and the run stops: another takeover may retry once the lease lapses, until the absolute
// expiry. An envelope that does not match its digest, or does not decode, is an integrity
// failure and the refusal returned. A claim whose review is still pending pauses again with its
// stored envelope (compilation §3.4, §3.6), and the run ends with errPaused.
func (a *API) resume(ctx context.Context, j job) (imported, *refusal, error) {
	plain, err := a.d.ing.DecryptStaging(ctx, j.resume.ct)
	if err != nil {
		a.o.logf("ingestion %s: decrypting the staged envelope: %v", j.claim.ID, err)
		if err := a.inTx(ctx, func(tx *sql.Tx) error {
			if err := staging.Hold(ctx, tx, a.d.owner, j.claim); err != nil {
				return err
			}
			return a.event(ctx, tx, j, map[string]any{"type": "resume-failed", "code": "dependency-unavailable"})
		}); err != nil {
			return imported{}, nil, fmt.Errorf("recording the failed resume: %w", err)
		}
		return imported{}, nil, errStop
	}
	st, err := ingest.Open(plain, j.resume.sum)
	if err != nil {
		a.o.logf("ingestion %s: opening the staged envelope: %v", j.claim.ID, err)
		return imported{}, refuse(http.StatusInternalServerError, "internal-error",
			"the staged envelope is incomplete or does not match its digest; the claim is abandoned"), nil
	}
	if j.marks != nil {
		ref, err := a.remark(ctx, j, st)
		return imported{}, ref, err
	}
	if j.claim.Review == "pending" {
		if err := a.inTx(ctx, func(tx *sql.Tx) error {
			if err := staging.Pause(ctx, tx, a.d.owner, j.claim, nil, [32]byte{}); err != nil {
				return err
			}
			return a.event(ctx, tx, j, map[string]any{"type": "paused", "generation": j.claim.Gen})
		}); err != nil {
			return imported{}, nil, fmt.Errorf("pausing for the review: %w", err)
		}
		return imported{}, nil, errPaused
	}
	return imported{sanitized: st.Sanitized, gens: st.Generations, baseline: st.Baseline}, nil, nil
}

// remark is a mark's run once its envelope is opened (compilation §3.6 items 3-4): the staged
// document re-extracted with the marks, their values created under the claim, and the new
// envelope, every earlier generation and the baseline carried over, stored as the claim pauses
// again. A mark refused before any provider write, or whose first generation the provider
// refuses, pauses the claim with its earlier envelope and the mark-refused event; a failure after
// a generation was created is the refusal returned, which abandons the claim. Either pause ends
// the run with errPaused.
func (a *API) remark(ctx context.Context, j job, st ingest.Staged) (*refusal, error) {
	const goesOn, abandoned = "the review goes on", "the claim is abandoned"
	c, err := ingest.Remark(st, j.marks)
	if err != nil {
		return nil, a.markRefused(ctx, j, a.markProblem(j, "extraction", err, goesOn))
	}
	created := map[string]string{}
	s, err := c.Commit(ctx, a.createGeneration(j.claim, created))
	switch {
	case err != nil && ctx.Err() != nil:
		return nil, ctx.Err()
	case err != nil && len(created) == 0:
		// A generation the failed request may have created is referenced by no row: an orphan.
		return nil, a.markRefused(ctx, j, a.markProblem(j, "generation create", err, goesOn))
	case err != nil:
		return a.markProblem(j, "generation create", err, abandoned), nil
	}
	plain, sum, err := st.Remarked(s, created).Seal()
	if err != nil {
		return a.markProblem(j, "envelope", err, abandoned), nil
	}
	ct, err := a.d.ing.EncryptStaging(ctx, plain)
	if err != nil {
		return a.markProblem(j, "envelope encryption", err, abandoned), nil
	}
	if err := a.inTx(ctx, func(tx *sql.Tx) error {
		if err := staging.Pause(ctx, tx, a.d.owner, j.claim, []byte(ct), sum); err != nil {
			return err
		}
		return a.event(ctx, tx, j, map[string]any{"type": "paused", "generation": j.claim.Gen})
	}); err != nil {
		return nil, fmt.Errorf("pausing after the mark: %w", err)
	}
	return nil, errPaused
}

// markRefused returns a mark's claim to paused with its stored envelope unchanged and records
// ref on the operation as the mark-refused event (compilation §3.6 item 4).
func (a *API) markRefused(ctx context.Context, j job, ref *refusal) error {
	if err := a.inTx(ctx, func(tx *sql.Tx) error {
		if err := staging.Pause(ctx, tx, a.d.owner, j.claim, nil, [32]byte{}); err != nil {
			return err
		}
		if err := a.event(ctx, tx, j, map[string]any{"type": "mark-refused", "generation": j.claim.Gen,
			"problem": problemDoc(j.op, ref)}); err != nil {
			return err
		}
		return a.event(ctx, tx, j, map[string]any{"type": "paused", "generation": j.claim.Gen})
	}); err != nil {
		return fmt.Errorf("recording the refused mark: %w", err)
	}
	return errPaused
}

// markProblem is the problem a mark's failed step leaves, ending with outcome. A compilation
// refusal names its rule and the mark by its position, never a path: an operator's path, or a
// staged path, can spell an earlier extracted value the run no longer holds (compilation §3.6
// item 4). Every other cause is logged by its step alone.
func (a *API) markProblem(j job, step string, err error, outcome string) *refusal {
	var r *ingest.Refusal
	switch {
	case errors.As(err, &r):
		return refuse(http.StatusUnprocessableEntity, "validation-failed", "compilation refused the mark; "+outcome).
			with("rule", string(r.Rule)).with("position", r.Position)
	case errors.Is(err, provider.ErrUnavailable):
		return refuse(http.StatusServiceUnavailable, "dependency-unavailable", "the provider is unavailable; "+outcome)
	}
	a.o.logf("ingestion %s: %s: %v", j.claim.ID, step, err)
	return refuse(http.StatusInternalServerError, "internal-error", "the mark failed at its "+step+"; "+outcome)
}

// failure is the problem a failed step leaves on the operation. A compilation refusal carries its
// rule and paths, which name no value (compilation §13); every other cause is logged by its step
// alone, since an ingest or provider error names no value either.
func (a *API) failure(j job, step string, err error) *refusal {
	var r *ingest.Refusal
	switch {
	case errors.As(err, &r):
		paths := r.Paths
		if paths == nil {
			paths = []string{}
		}
		return refuse(http.StatusUnprocessableEntity, "validation-failed", "compilation refused the input; the claim is abandoned").
			with("rule", string(r.Rule)).with("paths", paths)
	case errors.Is(err, provider.ErrUnavailable):
		return refuse(http.StatusServiceUnavailable, "dependency-unavailable", "the provider is unavailable; the claim is abandoned")
	}
	a.o.logf("ingestion %s: %s: %v", j.claim.ID, step, err)
	return refuse(http.StatusInternalServerError, "internal-error", "the ingestion failed at its "+step+"; the claim is abandoned")
}

// errRefused rolls back a transaction whose outcome is a refusal.
var errRefused = errors.New("refused")

// commitImport is T1: the claim released, then the draft FOR UPDATE at the revision T11 bound,
// the import base revision with its references, the draft entry, the draft's next revision and
// token, and the operation succeeded with its terminal event (T7), in one transaction. A draft
// that moved or closed is the refusal returned; a claim no longer this owner's, or a failed
// COMMIT, is an error and nothing is written.
func (a *API) commitImport(ctx context.Context, j job, imp imported) (*refusal, error) {
	var ref *refusal
	err := a.inTx(ctx, func(tx *sql.Tx) error {
		if err := staging.Release(ctx, tx, a.d.owner, j.claim); err != nil {
			return err
		}
		var rev int
		var state string
		if err := tx.QueryRowContext(ctx, `SELECT revision, state FROM draft WHERE id = $1 FOR UPDATE`, j.draft).Scan(&rev, &state); err != nil {
			return err
		}
		switch {
		case state != "open":
			ref = refuse(http.StatusConflict, "conflict", "the draft closed while the import ran; the claim is abandoned").with("draft", j.draft)
			return errRefused
		case rev != j.draftRev:
			ref = refuse(http.StatusPreconditionFailed, "precondition-failed", "the draft moved while the import ran; the claim is abandoned").
				with("draft", j.draft)
			return errRefused
		}
		// The draft's lock holds off a new publication (T2); one already active refuses the import.
		var pub string
		switch err := tx.QueryRowContext(ctx, `SELECT id FROM operation WHERE draft = $1 AND kind = 'publish'
			AND state IN ('queued', 'running') ORDER BY id LIMIT 1`, j.draft).Scan(&pub); {
		case err == nil:
			ref = refuse(http.StatusConflict, "conflict", "a publication of the draft is active; the claim is abandoned").
				with("draft", j.draft).with("operation", pub)
			return errRefused
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		ibr := id.New(id.ImportBase)
		b := imp.baseline
		embedded := imp.sanitized.Declarations().Embedded
		if embedded == nil {
			embedded = []ingest.Embedded{}
		}
		emb, err := json.Marshal(embedded)
		if err != nil {
			return err
		}
		// The ingestion's creator authors the revision (execution and recovery §2's self-approval).
		res, err := tx.ExecContext(ctx, `INSERT INTO import_base_revision (id, machine, document, embedded, baseline_ciphertext,
			baseline_digest, baseline_digest_key, configuration_digest, created_at, author)
			SELECT $1, $2, $3, $4, $5, $6, $7, $8, now(), created_by FROM operation WHERE id = $9`,
			ibr, j.claim.Machine, string(imp.sanitized.Documents()), emb, []byte(b.Ciphertext), b.Digest[:], b.DigestKey,
			b.Configuration[:], j.op)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return fmt.Errorf("import base revision of operation %s: %d rows written: %v", j.op, n, err)
		}
		for name, r := range imp.sanitized.Declarations().References {
			gen, ok := imp.gens[name]
			if !ok { // T11 refuses declared references; every name was minted by this run
				return fmt.Errorf("reference %s has no generation of this ingestion", name)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO import_base_reference (revision, name, kind, version, encoding, generation)
				VALUES ($1, $2, $3, $4, $5, $6)`, ibr, name, string(r.Kind), r.Version,
				sql.NullString{String: r.Encoding, Valid: r.Encoding != ""}, gen); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO draft_entry (draft, cluster, kind, machine, import_base_revision)
			VALUES ($1, $2, 'import-base', $3, $4)
			ON CONFLICT (draft, machine) DO UPDATE SET import_base_revision = EXCLUDED.import_base_revision`,
			j.draft, j.claim.Cluster, j.claim.Machine, ibr); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE draft SET revision = revision + 1, etag_token = $2 WHERE id = $1`,
			j.draft, etagToken()); err != nil {
			return err
		}
		return a.finish(ctx, tx, j, "succeeded",
			map[string]any{"draft": j.draft, "draftRevision": rev + 1, "importBaseRevision": ibr}, nil,
			map[string]any{"type": "succeeded", "importBaseRevision": ibr})
	})
	if errors.Is(err, errRefused) {
		return ref, nil
	}
	return nil, err
}

// abandon is the owner's own refusal (spec gap 4, a T8 owner transition): the claim abandoned
// and the operation failed with ref and its terminal event, fenced. A claim no longer this
// owner's is someone else's to end; nothing is written.
func (a *API) abandon(ctx context.Context, j job, ref *refusal) {
	entry := map[string]any{"type": "failed", "code": ref.code}
	if ref.cause != "" {
		a.o.logf("ingestion %s: operation %s fails %d %s (%s)", j.claim.ID, j.op, ref.status, ref.code, ref.cause)
		entry["cause"] = ref.cause
	} else {
		a.o.logf("ingestion %s: operation %s fails %d %s", j.claim.ID, j.op, ref.status, ref.code)
	}
	if err := a.inTx(ctx, func(tx *sql.Tx) error {
		if err := staging.Abandon(ctx, tx, a.d.owner, j.claim); err != nil {
			return err
		}
		return a.finish(ctx, tx, j, "failed", nil, problemDoc(j.op, ref), entry)
	}); err != nil {
		a.o.logf("ingestion %s: the abandonment was not recorded: %v", j.claim.ID, err)
	}
}

// inTx runs fn in a transaction and commits it, retrying a deadlock as a request does (§5).
func (a *API) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	for n := 1; ; n++ {
		err := a.attemptTx(ctx, fn)
		if !isDeadlock(err) || n == maxAttempts {
			return err
		}
	}
}

func (a *API) attemptTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // a no-op once committed
	if err := fn(tx); err != nil {
		return err
	}
	commit := (*sql.Tx).Commit
	if a.o.commit != nil {
		commit = a.o.commit
	}
	if err := commit(tx); err != nil {
		return fmt.Errorf("%w: COMMIT: %w", errStop, err)
	}
	return nil
}

// event appends entry to j's running operation, numbered after its last event (T7), under the
// claim's owner generation.
func (a *API) event(ctx context.Context, tx *sql.Tx, j job, entry map[string]any) error {
	var n int
	err := tx.QueryRowContext(ctx, `UPDATE operation SET last_event = last_event + 1
		WHERE id = $1 AND owner = $2 AND owner_gen = $3 AND state = 'running' RETURNING last_event`,
		j.op, a.d.owner.ID, j.claim.Gen).Scan(&n)
	if err != nil {
		return a.fenced(err)
	}
	return a.insertEvent(ctx, tx, j, n, entry)
}

// finish moves j's running operation to state with its result or problem, clears its owner and
// lease, and appends the terminal event (§8.2), under the claim's owner generation.
func (a *API) finish(ctx context.Context, tx *sql.Tx, j job, state string, result, problem map[string]any, entry map[string]any) error {
	var n int
	err := tx.QueryRowContext(ctx, `UPDATE operation SET state = $4, result = $5::jsonb, error = $6::jsonb,
		owner = NULL, owner_epoch = NULL, lease_until = NULL, last_event = last_event + 1
		WHERE id = $1 AND owner = $2 AND owner_gen = $3 AND state = 'running' RETURNING last_event`,
		j.op, a.d.owner.ID, j.claim.Gen, state, jsonOrNull(result), jsonOrNull(problem)).Scan(&n)
	if err != nil {
		return a.fenced(err)
	}
	return a.insertEvent(ctx, tx, j, n, entry)
}

func (a *API) fenced(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return staging.ErrFenced
	}
	return err
}

func (a *API) insertEvent(ctx context.Context, tx *sql.Tx, j job, n int, entry map[string]any) error {
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO operation_event (operation, number, epoch, kind, entry, at)
		VALUES ($1, $2, $3, 'ingest', $4, now())`, j.op, n, a.d.owner.Epoch, string(b))
	return err
}

// jsonOrNull is m as JSON text, or SQL NULL for nil: never JSON null.
func jsonOrNull(m map[string]any) sql.NullString {
	if m == nil {
		return sql.NullString{}
	}
	b, err := json.Marshal(m)
	if err != nil { // every value the runner records marshals
		panic(err)
	}
	return sql.NullString{String: string(b), Valid: true}
}
