package api

import (
	"database/sql"
	"errors"
	"testing"
	"time"

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
