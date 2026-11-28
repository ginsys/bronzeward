package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/id"
)

// A publish job that meets an error which is a pure function of stored data would meet it on every
// claim: it ends failed at once (persistence-api.md §6.1, §6.2, failure table), where an error of
// the database, the clock, the context or a provider leaves the job to its lease.

// faultTimers lapse a lease in 150 ms, so the worker claims a stuck job again within a few
// hundred milliseconds.
var faultTimers = config.Ingestion{Heartbeat: 20 * time.Millisecond, Lease: 150 * time.Millisecond}

const faultDetail = "the release could not be built from stored data; no release or Desired change was committed"

// opState is op's state, its last owner generation and its event types in number order.
func (b *buildEnv) opState(op string) (string, int64, []string) {
	b.t.Helper()
	var state string
	var gen int64
	if err := b.db.QueryRow(`SELECT state, owner_gen FROM operation WHERE id = $1`, op).Scan(&state, &gen); err != nil {
		b.t.Fatal(err)
	}
	rows, err := b.db.Query(`SELECT entry->>'type' FROM operation_event WHERE operation = $1 AND kind = 'publish' ORDER BY number`, op)
	if err != nil {
		b.t.Fatal(err)
	}
	defer rows.Close()
	var types []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			b.t.Fatal(err)
		}
		types = append(types, s)
	}
	if err := rows.Err(); err != nil {
		b.t.Fatal(err)
	}
	return state, gen, types
}

// runFaulted queues a publish job of the draft's revision 1 and lets a worker with short timers
// and options o run until reached reports true (at most 20 s: a case that never reaches it fails
// on its own assertions), then four leases more, which is longer than a stuck job needs to be
// claimed again. It stops the worker and returns the operation. A run's length depends on the
// node, so no case waits for a fixed time.
func (b *buildEnv) runFaulted(o options, reached func(op string) bool) string {
	b.t.Helper()
	mustExec(b.t, b.db, `DELETE FROM operation WHERE id = $1`, b.job.op)
	life, stop := context.WithCancel(b.t.Context())
	a := b.workerWith("run-1/1/a", life, faultTimers, o)
	op := b.queuePublish(1)
	a.startPublisher()
	for deadline := time.Now().Add(20 * time.Second); !reached(op) && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(4 * faultTimers.Lease)
	stopping(a, stop)
	return op
}

// finished is reached once op is failed or succeeded.
func (b *buildEnv) finished(op string) bool {
	state, _, _ := b.opState(op)
	return state == "failed" || state == "succeeded"
}

// wantBuildFault checks that op ended failed on its first claim, with the fixed 500 problem that
// names no value, no release, and a draft that takes edits again; and that the cause is logged.
func (b *buildEnv) wantBuildFault(op, cause string) {
	b.t.Helper()
	state, gen, types := b.opState(op)
	if state != "failed" || gen != 1 || !slices.Equal(types, []string{"queued", "claimed", "failed"}) {
		b.t.Fatalf("operation %s at generation %d, events %v; want failed on its first claim", state, gen, types)
	}
	var typ, detail string
	var status float64
	if err := b.db.QueryRow(`SELECT error->>'type', error->>'detail', (error->>'status')::float8 FROM operation WHERE id = $1`, op).
		Scan(&typ, &detail, &status); err != nil {
		b.t.Fatal(err)
	}
	if typ != "urn:bronzeward:problem:internal-error" || status != http.StatusInternalServerError || detail != faultDetail {
		b.t.Fatalf("problem %s %v %q", typ, status, detail)
	}
	if n := count(b.t, b.db, `SELECT count(*) FROM release`); n != 0 {
		b.t.Fatalf("%d releases", n)
	}
	if !b.logged(cause) {
		b.t.Fatalf("the log does not name the cause %q", cause)
	}
	frv := b.fragmentRevision(b.cluster, "faultcheck", "override")
	b.fragmentHead("faultcheck", "override", frv, 1)
	b.ok(b.put("/profiles/faultcheck", `{"fragments":["`+frv+`"]}`, b.etag, b.key()))
}

// A stored source that ingestion's well-formedness checks no longer accept ends the job failed on
// its first claim, with no seam: the stored text is emptied in the database.
func TestPublishBuildFaultStoredSource(t *testing.T) {
	b := newBuildEnv(t)
	// A revision is immutable, so the machine's import base is replaced by one stored with text that is no YAML stream.
	ibr := id.New(id.ImportBase)
	b.store(`INSERT INTO import_base_revision (id, machine, document, embedded, baseline_ciphertext, baseline_digest,
		baseline_digest_key, configuration_digest, created_at, author) VALUES ($1, $2, $3, $4, '\x01', $5, 'transit/baseline-digest:1', $5, now(),
		(SELECT min(id) FROM principal))`,
		"import_base_reference", sanitizedSource{embedded: []byte("[]")}, ibr, b.machine, "key: [unclosed\n", []byte("[]"), make([]byte, 32))
	mustExec(t, b.db, `UPDATE draft_entry SET import_base_revision = $2 WHERE draft = $1 AND machine = $3`, b.draft, ibr, b.machine)
	op := b.runFaulted(options{}, b.finished)
	b.wantBuildFault(op, "not a well-formed sanitized source")
}

// Each other typed site of the build ends the job failed on its first claim. The mode text, the
// build record of the machinery module and the node-name claim are not rows a test can edit, so
// options.fault stands in for the site's error.
func TestPublishBuildFaultSeamSites(t *testing.T) {
	for _, site := range []string{"machinery", "mode", "claim", "records"} {
		t.Run(site, func(t *testing.T) {
			b := newBuildEnv(t)
			cause := "synthetic fault at " + site
			op := b.runFaulted(options{fault: func(s string) error {
				if s == site {
					return errors.New(cause)
				}
				return nil
			}}, b.finished)
			b.wantBuildFault(op, cause)
		})
	}
}

// The control: an error of the database at the publication commit leaves the job running, and its
// lapsed lease is claimed again at the next generation, as before.
func TestPublishBuildFaultDatabaseControl(t *testing.T) {
	b := newBuildEnv(t)
	var atCommit bool
	var resets atomic.Int32
	op := b.runFaulted(options{
		stopAt: func(step string) bool {
			atCommit = step == "publish-commit"
			return false
		},
		commit: func(tx *sql.Tx) error {
			if !atCommit {
				return tx.Commit()
			}
			atCommit = false
			resets.Add(1)
			return errors.New("connection reset (test)")
		},
	}, func(string) bool { return resets.Load() >= 2 })
	state, gen, types := b.opState(op)
	if state != "running" || gen < 2 || types[len(types)-1] != "claimed" || slices.Contains(types, "failed") {
		t.Fatalf("operation %s at generation %d, events %v; want running and claimed again", state, gen, types)
	}
	if n := count(t, b.db, `SELECT count(*) FROM operation WHERE id = $1 AND error IS NOT NULL`, op); n != 0 {
		t.Fatal("the database error was recorded as the operation's failure")
	}
}
