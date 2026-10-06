package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/staging"
)

// queuePublish inserts a queued publish operation of d's draft at revision rev, as T2 writes it.
func (d *draftEnv) queuePublish(rev int) string {
	d.t.Helper()
	op := id.New(id.Operation)
	mustExec(d.t, d.db, `INSERT INTO operation (id, kind, state, epoch, owner_gen, last_event, draft, draft_revision,
			created_by, created_by_kind, created_role, created_at)
		SELECT $1, 'publish', 'queued', epoch, 0, 1, $2, $3, $4, 'human', 'publisher', now() FROM installation_state`,
		op, d.draft, rev, d.seed)
	mustExec(d.t, d.db, `INSERT INTO operation_event (operation, number, epoch, kind, entry, at)
		SELECT $1, 1, epoch, 'publish', '{"type": "queued"}', now() FROM installation_state`, op)
	return op
}

func (d *draftEnv) claim(a *API) (publishJob, bool) {
	d.t.Helper()
	j, ok, err := a.claimPublish(d.t.Context())
	if err != nil {
		d.t.Fatalf("claim: %v", err)
	}
	return j, ok
}

// claimed reports op's owner, generation and the entry of the event at number n.
func (d *draftEnv) claimed(op string, n int) (string, int64, string) {
	d.t.Helper()
	var owner string
	var gen int64
	var entry string
	if err := d.db.QueryRow(`SELECT o.owner, o.owner_gen, e.entry::text FROM operation o
		JOIN operation_event e ON e.operation = o.id AND e.number = $2
		WHERE o.id = $1 AND o.state = 'running' AND o.lease_until > clock_timestamp() AND o.last_event = $2`, op, n).
		Scan(&owner, &gen, &entry); err != nil {
		d.t.Fatalf("operation %s at event %d: %v", op, n, err)
	}
	return owner, gen, entry
}

// §5.1: a claim takes the oldest eligible job, queued or running with a lapsed lease, under this
// process's owner at the next generation with a lease, and records it; a live lease is not taken.
func TestPublishClaim(t *testing.T) {
	d := newDraftEnv(t)
	a := d.publisherAs("run-1/1/a", options{})
	b := d.publisherAs("run-1/2/b", options{})
	first, second := d.queuePublish(1), d.queuePublish(2)

	j, ok := d.claim(a)
	if !ok || j.op != first || j.gen != 1 || j.draft != d.draft || j.cluster != d.cluster || j.draftRev != 1 {
		t.Fatalf("first claim %+v %v; want %s at generation 1", j, ok, first)
	}
	if owner, gen, entry := d.claimed(first, 2); owner != "run-1/1/a" || gen != 1 || entry != `{"type": "claimed", "generation": 1}` {
		t.Fatalf("claimed by %s at %d: %s", owner, gen, entry)
	}
	if j, ok := d.claim(b); !ok || j.op != second || j.draftRev != 2 {
		t.Fatalf("second claim %+v %v; want %s", j, ok, second)
	}
	if j, ok := d.claim(b); ok {
		t.Fatalf("a live lease was claimed: %+v", j)
	}

	// The first lease lapses: the next claim takes the job over at the next generation, and the
	// first owner can neither extend nor finish it.
	mustExec(t, d.db, `UPDATE operation SET lease_until = clock_timestamp() - interval '1 second' WHERE id = $1`, first)
	taken, ok := d.claim(b)
	if !ok || taken.op != first || taken.gen != 2 {
		t.Fatalf("takeover %+v %v; want %s at generation 2", taken, ok, first)
	}
	if owner, gen, entry := d.claimed(first, 3); owner != "run-1/2/b" || gen != 2 || entry != `{"type": "claimed", "generation": 2}` {
		t.Fatalf("taken over by %s at %d: %s", owner, gen, entry)
	}
	if err := a.extendPublish(t.Context(), j); !errors.Is(err, staging.ErrFenced) {
		t.Fatalf("the superseded owner extended: %v", err)
	}
	if err := a.inTx(t.Context(), func(tx *sql.Tx) error {
		return a.finishPublish(t.Context(), tx, j, "failed", nil, map[string]any{"code": "x"}, map[string]any{"type": "failed"})
	}); !errors.Is(err, staging.ErrFenced) {
		t.Fatalf("the superseded owner finished: %v", err)
	}

	// The owner extends a live lease; a lapsed one it never extends.
	var before, after time.Time
	if err := d.db.QueryRow(`SELECT lease_until FROM operation WHERE id = $1`, first).Scan(&before); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := b.extendPublish(t.Context(), taken); err != nil {
		t.Fatalf("extension: %v", err)
	}
	if err := d.db.QueryRow(`SELECT lease_until FROM operation WHERE id = $1`, first).Scan(&after); err != nil || !after.After(before) {
		t.Fatalf("lease %v → %v, %v", before, after, err)
	}
	mustExec(t, d.db, `UPDATE operation SET lease_until = clock_timestamp() - interval '1 second' WHERE id = $1`, first)
	if err := b.extendPublish(t.Context(), taken); !errors.Is(err, staging.ErrFenced) {
		t.Fatalf("a lapsed lease was extended: %v", err)
	}
}

// §5.1: a process whose epoch is no longer the current one claims nothing.
func TestPublishClaimEpoch(t *testing.T) {
	d := newDraftEnv(t)
	a := d.publisher(options{})
	op := d.queuePublish(1)
	newEpoch(t, d.db)
	if _, _, err := a.claimPublish(t.Context()); !errors.Is(err, staging.ErrEpochSuperseded) {
		t.Fatalf("claim in a superseded epoch: %v", err)
	}
	var state string
	if err := d.db.QueryRow(`SELECT state FROM operation WHERE id = $1`, op).Scan(&state); err != nil || state != "queued" {
		t.Fatalf("operation %s, %v", state, err)
	}
}

// worker builds an API over b's database for the process owner, publishing with b's stand-in
// provider under timers; its publish worker, once started, stops when life ends.
func (b *buildEnv) worker(owner string, life context.Context, timers config.Ingestion) *API {
	b.t.Helper()
	var epoch string
	if err := b.db.QueryRow(`SELECT epoch FROM installation_state`).Scan(&epoch); err != nil {
		b.t.Fatal(err)
	}
	return b.buildWith(deps{owner: staging.Owner{ID: owner, Epoch: epoch}, life: life, timers: timers,
		pub: &publishClients{meta: b.held, reader: b.held, encrypter: b.held}}, options{})
}

// ended waits, at most 10 s, for op to end, and returns its state, its last owner generation and
// its event types in number order.
func (d *draftEnv) ended(op string) (string, int64, []string) {
	d.t.Helper()
	var state string
	var gen int64
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if err := d.db.QueryRow(`SELECT state, owner_gen FROM operation WHERE id = $1`, op).Scan(&state, &gen); err != nil {
			d.t.Fatal(err)
		}
		if state == "succeeded" || state == "failed" {
			break
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("operation %s still %s after 10 s", op, state)
		}
	}
	var types []string
	rows, err := d.db.Query(`SELECT entry->>'type' FROM operation_event WHERE operation = $1 AND kind = 'publish' ORDER BY number`, op)
	if err != nil {
		d.t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			d.t.Fatal(err)
		}
		types = append(types, s)
	}
	return state, gen, types
}

// stopping stops a's worker by ending life, and waits for it.
func stopping(a *API, stop context.CancelFunc) {
	stop()
	a.d.runs.Wait()
}

var idle = config.Ingestion{Heartbeat: time.Hour, Lease: time.Minute} // no poll and no heartbeat in a test's time

// §5.1, §8.3: T2's COMMIT wakes this process's worker, which claims the job, compiles and commits
// the release, and ends the operation succeeded naming it: events queued, claimed, succeeded.
func TestPublishWorker(t *testing.T) {
	b := newBuildEnv(t)
	mustExec(t, b.db, `DELETE FROM operation WHERE id = $1`, b.job.op)
	life, stop := context.WithCancel(t.Context())
	a := b.worker("run-1/1/worker", life, idle)
	// The worker's first claim finds nothing before T2 runs, so only the wake can start the job.
	waiting := make(chan struct{}, 1)
	a.o.onIdle = func() {
		select {
		case waiting <- struct{}{}:
		default:
		}
	}
	a.startPublisher()
	defer stopping(a, stop)
	<-waiting

	op := decode[operationBody](t, b.publish(a, b.etag, b.key(), `{}`), http.StatusAccepted).ID
	state, gen, types := b.ended(op)
	if state != "succeeded" || gen != 1 || !slices.Equal(types, []string{"queued", "claimed", "succeeded"}) {
		t.Fatalf("operation %s at generation %d, events %v", state, gen, types)
	}
	if n := count(t, b.db, `SELECT count(*) FROM release r JOIN operation o ON o.result->>'release' = r.id
		WHERE o.id = $1 AND r.operation = $1`, op); n != 1 {
		t.Fatalf("%d releases name the operation", n)
	}
	if n := count(t, b.db, `SELECT count(*) FROM draft WHERE id = $1 AND state = 'published'`, b.draft); n != 1 {
		t.Fatal("the draft is not published")
	}
}

// A compilation refusal fails the operation with its problem and a failed event naming the code,
// in the failure transaction (§6.2, ruling R33); nothing is released.
func TestPublishWorkerRefused(t *testing.T) {
	b := newBuildEnv(t)
	mustExec(t, b.db, `DELETE FROM operation WHERE id = $1`, b.job.op)
	mustExec(t, b.db, `UPDATE cluster SET contract = 'v1.12' WHERE id = $1`, b.cluster)
	life, stop := context.WithCancel(t.Context())
	a := b.worker("run-1/1/worker", life, idle)
	a.startPublisher()
	defer stopping(a, stop)

	op := decode[operationBody](t, b.publish(a, b.etag, b.key(), `{}`), http.StatusAccepted).ID
	state, _, types := b.ended(op)
	if state != "failed" || !slices.Equal(types, []string{"queued", "claimed", "failed"}) {
		t.Fatalf("operation %s, events %v", state, types)
	}
	var problem, code string
	if err := b.db.QueryRow(`SELECT o.error->>'type', e.entry->>'code' FROM operation o
		JOIN operation_event e ON e.operation = o.id AND e.number = o.last_event WHERE o.id = $1`, op).Scan(&problem, &code); err != nil {
		t.Fatal(err)
	}
	if problem != "urn:bronzeward:problem:validation-failed" || code != "validation-failed" {
		t.Fatalf("problem %s, failed event code %s", problem, code)
	}
	if n := count(t, b.db, `SELECT count(*) FROM release`); n != 0 {
		t.Fatalf("%d releases", n)
	}
}

// A run whose claim another worker took over writes nothing: no release, no failure, and the
// operation stays the new owner's (§5.1).
func TestPublishRunSuperseded(t *testing.T) {
	b := newBuildEnv(t)
	mustExec(t, b.db, `DELETE FROM operation WHERE id = $1`, b.job.op)
	a := b.worker("run-1/1/a", t.Context(), idle)
	other := b.worker("run-1/2/b", t.Context(), idle)
	op := b.queuePublish(1)
	j, ok := b.claim(a)
	if !ok {
		t.Fatal("nothing claimed")
	}
	mustExec(t, b.db, `UPDATE operation SET lease_until = clock_timestamp() - interval '1 second' WHERE id = $1`, op)
	if _, ok := b.claim(other); !ok {
		t.Fatal("no takeover")
	}
	a.runPublish(t.Context(), j)
	if owner, gen, _ := b.claimed(op, 3); owner != "run-1/2/b" || gen != 2 {
		t.Fatalf("operation owned by %s at %d", owner, gen)
	}
	if n := count(t, b.db, `SELECT count(*) FROM release`) + count(t, b.db, `SELECT count(*) FROM operation_event WHERE operation = $1`, op); n != 3 {
		t.Fatalf("%d releases and events; want the 3 events", n)
	}
}

// A run whose lease lapsed, with no other claim yet, writes nothing either: neither the release
// and its success nor a refusal's failure (§5.1, as compilation §3.5 fences a claim).
func TestPublishRunLapsed(t *testing.T) {
	for _, refused := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "refusal"}[refused], func(t *testing.T) {
			b := newBuildEnv(t)
			mustExec(t, b.db, `DELETE FROM operation WHERE id = $1`, b.job.op)
			if refused {
				mustExec(t, b.db, `UPDATE cluster SET contract = 'v1.12' WHERE id = $1`, b.cluster)
			}
			a := b.worker("run-1/1/a", t.Context(), idle)
			op := b.queuePublish(1)
			j, ok := b.claim(a)
			if !ok {
				t.Fatal("nothing claimed")
			}
			mustExec(t, b.db, `UPDATE operation SET lease_until = clock_timestamp() - interval '1 second' WHERE id = $1`, op)
			a.runPublish(t.Context(), j)
			if n := count(t, b.db, `SELECT count(*) FROM operation WHERE id = $1 AND state = 'running' AND owner_gen = 1 AND last_event = 2`, op); n != 1 {
				t.Fatal("the lapsed run ended the operation")
			}
			if n := count(t, b.db, `SELECT count(*) FROM release`) + count(t, b.db, `SELECT count(*) FROM operation_event WHERE operation = $1`, op); n != 2 {
				t.Fatalf("%d releases and events; want the 2 events", n)
			}
		})
	}
}

// A lease that lapses while the extension waits for the operation's row is not extended, though
// the holder ends without changing the row (§5.1): the predicate is read after the lock.
func TestPublishExtendAfterLockWait(t *testing.T) {
	d := newDraftEnv(t)
	a := d.publisherAs("run-1/1/a", options{})
	op := d.queuePublish(1)
	j, ok := d.claim(a)
	if !ok {
		t.Fatal("nothing claimed")
	}
	mustExec(t, d.db, `UPDATE operation SET lease_until = clock_timestamp() + interval '1 second' WHERE id = $1`, op)
	holder, err := d.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	var pid int
	if err := holder.QueryRow(`SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(`SELECT 1 FROM operation WHERE id = $1 FOR UPDATE`, op); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.extendPublish(context.Background(), j) }()
	waitBlockedBy(t, d.db, pid)
	for count(t, d.db, `SELECT count(*) FROM operation WHERE id = $1 AND lease_until < clock_timestamp()`, op) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, staging.ErrFenced) {
		t.Fatalf("a lease that lapsed in the wait was extended: %v", err)
	}
}

// The run's heartbeat keeps its lease while a compilation outlasts it, so a polling worker in
// another process never takes the job over: one claim, at generation 1.
func TestPublishWorkerHeartbeat(t *testing.T) {
	b := newBuildEnv(t)
	mustExec(t, b.db, `DELETE FROM operation WHERE id = $1`, b.job.op)
	b.held.slow = 400 * time.Millisecond
	timers := config.Ingestion{Heartbeat: 20 * time.Millisecond, Lease: 150 * time.Millisecond}
	life, stop := context.WithCancel(t.Context())
	a := b.worker("run-1/1/a", life, timers)
	other := b.worker("run-1/2/b", life, timers)
	op := b.queuePublish(1)
	j, ok := b.claim(a)
	if !ok {
		t.Fatal("nothing claimed")
	}
	other.startPublisher()
	defer stopping(other, stop)
	a.runPublish(life, j)
	state, gen, types := b.ended(op)
	if state != "succeeded" || gen != 1 || !slices.Equal(types, []string{"queued", "claimed", "succeeded"}) {
		t.Fatalf("operation %s at generation %d, events %v", state, gen, types)
	}
}

// New starts the publish worker of a process with publishers: a queued job ends without a request.
func TestNewStartsPublisher(t *testing.T) {
	b := newBuildEnv(t)
	mustExec(t, b.db, `DELETE FROM operation WHERE id = $1`, b.job.op)
	var epoch string
	if err := b.db.QueryRow(`SELECT epoch FROM installation_state`).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	life, stop := context.WithCancel(t.Context())
	h := New(life, b.db, auth.NewVerifier(b.cfg, b.db, auth.Discover(b.cfg.OIDC)), b.cfg, nil,
		&Publishers{Meta: b.held, Compiler: b.held},
		&config.Ingestion{Instance: "a", Heartbeat: 20 * time.Millisecond, Lease: time.Minute}, epoch)
	defer stopping(h.(*API), stop)
	op := b.queuePublish(1)
	if state, _, _ := b.ended(op); state != "succeeded" {
		t.Fatalf("operation %s", state)
	}
}

// A worker whose epoch is superseded stops claiming and ends (§5.1).
func TestPublishWorkerEpoch(t *testing.T) {
	d := newDraftEnv(t)
	a := d.publisher(options{})
	newEpoch(t, d.db)
	a.startPublisher()
	done := make(chan struct{})
	go func() {
		a.d.runs.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker still runs in a superseded epoch")
	}
	if !d.logged("superseded") {
		t.Fatal("no log line names the superseded epoch")
	}
}
