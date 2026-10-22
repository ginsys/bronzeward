package api

import (
	"crypto/rand"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/staging"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

// recoveryStart is a process started with the recovery-start flag over e's database (§12.2): it
// owns under the epoch it read at its start, serves only the entry until its entry commits, and
// counts the entry callbacks it runs.
func recoveryStart(t *testing.T, e *env, entered *atomic.Int32, o ...options) *API {
	t.Helper()
	d := e.d
	ep := epoch(t, e.db)
	d.owner = staging.Owner{ID: "r/1/" + rand.Text(), Epoch: ep}
	d.start, d.recoveryStart = ep, true
	d.preEntry = new(atomic.Bool)
	d.preEntry.Store(true)
	d.onEntry = func() { entered.Add(1) }
	d.entry = new(entryState)
	return e.buildWith(d, append(o, options{})[0])
}

const entryBodyText = `{"restored":[{"family":"database","backup":"pg-2026-09-25T02:00Z","takenAt":"2026-09-25T02:00:00Z"}],` +
	`"stoppedAt":"2026-09-25T03:00:00Z","reason":"database restored after storage failure"}`

// enter is h-recovery's entry request on a under key.
func (e *env) enter(a *API, key, body string) *httptest.ResponseRecorder {
	return e.do(a, call{method: "POST", path: path(entryPattern), token: e.human("h-recovery"), key: "k-entry-" + key + "-0123456789",
		body: body})
}

// Recovery-mode entry (persistence-api.md §12.2 T9; execution-recovery.md §7.2):
// one transaction mints a new current epoch, sets recovery mode, takes every non-terminal operation
// over into it (committed, sending and verifying become unresolved, unresolved stays so at the next
// generation), marks every scope pre-restore unaccounted with a timeline entry, and writes the act and
// the record in the new epoch. The process then owns under that epoch, serves past the entry and runs
// its entry callback once; a retry replays, and an entry under another key is refused naming the epoch.
func TestRecoveryEntry(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"committed", "sending", "verifying", "unresolved"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			ae := newAttemptEnv(t, "")
			ae.reach(t, state)
			_, gen, _ := ae.state(t)
			before := epoch(t, ae.db)
			var entered atomic.Int32
			r := recoveryStart(t, ae.env, &entered)
			rec := ae.enter(r, "1", entryBodyText)
			b := decode[entryBody](t, rec, http.StatusCreated)
			if b.Epoch == before || b.Epoch != epoch(t, ae.db) || rec.Header().Get("Bronzeward-Epoch") != b.Epoch ||
				rec.Header().Get("Bronzeward-Recovery-Mode") != "true" || rec.Header().Get("Location") != prefix+"/recovery" {
				t.Fatalf("answered %+v with headers %v; the epoch before was %s", b, rec.Header(), before)
			}
			human := ae.principalOf("h-recovery")
			if b.PreviousEpoch != before || b.EnteredBy != human || b.Role != "recovery-admin" || len(b.Operations) != 1 ||
				b.Operations[0] != ae.op || !b.StoppedAt.Equal(time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC)) {
				t.Fatalf("answered %+v", b)
			}
			if n := count(t, ae.db, `SELECT count(*) FROM installation_state WHERE recovery_mode`); n != 1 {
				t.Fatal("recovery mode is not in effect")
			}
			if n := count(t, ae.db, `SELECT count(*) FROM recovery_epoch e JOIN recovery_restore r USING (epoch)
					WHERE e.epoch = $1 AND e.entered_by = $2 AND e.reason = 'database restored after storage failure'
					AND e.stopped_at = '2026-09-25T03:00:00Z' AND r.family = 'database' AND r.backup = 'pg-2026-09-25T02:00Z' AND r.taken_at = '2026-09-25T02:00:00Z'`,
				b.Epoch, human); n != 1 {
				t.Fatal("the epoch does not record its entry and the restored backup")
			}
			var st, owner, ownerEpoch string
			var g int64
			if err := ae.db.QueryRow(`SELECT state, owner_gen, owner, owner_epoch FROM operation WHERE id = $1`, ae.op).
				Scan(&st, &g, &owner, &ownerEpoch); err != nil {
				t.Fatal(err)
			}
			if st != "unresolved" || g != gen+1 || owner != r.d.owner.ID || ownerEpoch != b.Epoch {
				t.Fatalf("the operation is %s at generation %d owned by %s in %s", st, g, owner, ownerEpoch)
			}
			if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'ownership-transition'
					AND epoch = $2 AND entry->>'operation' = $3 AND entry->>'epoch' = $2 AND entry->>'fromEpoch' = $4
					AND entry->>'act' = $5 AND entry->>'reason' = 'recovery-entry'`, ae.machine, b.Epoch, ae.op, before, b.Act); n != 1 {
				t.Fatal("no ownership transition into the new epoch")
			}
			moved := map[bool]int{true: 0, false: 1}[state == "unresolved"]
			if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'operation-state' AND epoch = $2
					AND entry->>'from' = $3 AND entry->>'to' = 'unresolved' AND entry->>'cause' = 'recovery-entry'`,
				ae.machine, b.Epoch, state); n != moved {
				t.Fatalf("%d operation state entries from %s; want %d", n, state, moved)
			}
			// Every scope, this one and any other, is pre-restore unaccounted, each with its entry last.
			if n := count(t, ae.db, `SELECT count(*) FROM machine m WHERE m.scope_state <> 'pre-restore-unaccounted'
					OR NOT EXISTS (SELECT FROM machine_event e WHERE e.machine = m.id AND e.revision = m.revision_counter
						AND e.kind = 'scope-state' AND e.epoch = $1 AND e.entry->>'to' = 'pre-restore-unaccounted'
						AND e.entry->>'cause' = 'recovery-entry' AND e.entry->>'act' = $2)`, b.Epoch, b.Act); n != 0 {
				t.Fatalf("%d machines are not marked pre-restore unaccounted by the entry", n)
			}
			if n := count(t, ae.db, `SELECT count(*) FROM act a JOIN idempotency_record r ON r.request_id = a.request_id
					WHERE a.id = $1 AND a.action = 'recovery.enter' AND a.epoch = $2 AND r.epoch = $2`, b.Act, b.Epoch); n != 1 {
				t.Fatal("the act and the record are not in the new epoch")
			}
			if entered.Load() != 1 || r.d.preEntry.Load() || r.d.owner.Epoch != b.Epoch {
				t.Fatalf("after entry: %d callbacks, preEntry %t, owning under %s", entered.Load(), r.d.preEntry.Load(), r.d.owner.Epoch)
			}
			if rec := ae.do(r, call{method: "GET", path: path("/clusters"), token: ae.human("h-viewer")}); rec.Code != http.StatusOK {
				t.Fatalf("a read after entry: %d %s", rec.Code, rec.Body)
			}
			// A retry replays; another key finds the entry committed since the start.
			if rec := ae.enter(r, "1", entryBodyText); rec.Code != http.StatusCreated || rec.Header().Get("Idempotent-Replayed") != "true" {
				t.Fatalf("the retry: %d %v %s", rec.Code, rec.Header(), rec.Body)
			}
			doc := wantProblem(t, ae.enter(r, "2", entryBodyText), http.StatusConflict, "conflict")
			if doc["epoch"] != b.Epoch || entered.Load() != 1 || epoch(t, ae.db) != b.Epoch {
				t.Fatalf("a second entry: %v, %d callbacks, epoch %s", doc, entered.Load(), epoch(t, ae.db))
			}
		})
	}
}

// Entry ends what the earlier epoch left running (§12.2 T9 steps 3 and 4, §8.2): every live staging
// claim is abandoned with its payload cleared and its running ingest operation failed
// ingestion-abandoned, and every queued or running publish job is failed recovery-mode-entered, each
// with its terminal event in the new epoch, dated no earlier than the epoch (§5 rule 4). A released
// claim and a finished job stay as they are.
func TestRecoveryEntryEndsJobsAndClaims(t *testing.T) {
	t.Parallel()
	d := newDraftEnv(t)
	claim := func(mode, state string) string {
		c := id.New(id.Ingestion)
		payload := map[bool]string{true: `'\x01'::bytea`, false: `NULL`}[state == "paused"]
		mustExec(t, d.db, `INSERT INTO staging_claim (id, mode, state, owner, owner_gen, owner_epoch, lease_until, expires_at,
			payload, payload_digest, review, cluster, machine, kind, created_at)
			SELECT $1, $2, $3, 'w/1/start', 1, epoch, now() + interval '1 minute', now() + interval '1 hour',
			`+payload+`, sha256(`+payload+`), $4, $5, $6, 'import', now() FROM installation_state`,
			c, mode, state, map[bool]any{true: "pending", false: nil}[state == "paused"], d.cluster, d.machine)
		return c
	}
	op := func(kind, state string, revision int, ingestion any) string {
		o := id.New(id.Operation)
		mustExec(t, d.db, `INSERT INTO operation (id, kind, state, epoch, owner, owner_gen, owner_epoch, lease_until, last_event,
			draft, draft_revision, ingestion, created_by, created_by_kind, created_role, created_at, error)
			SELECT $1, $2, $3, epoch, CASE WHEN $3 = 'running' THEN 'w/1/start' END, CASE WHEN $3 = 'running' THEN 1 ELSE 0 END,
			CASE WHEN $3 = 'running' THEN epoch END, CASE WHEN $3 = 'running' THEN now() + interval '1 minute' END, 2,
			$4, $5, $6, $7, 'human', 'author', now(), CASE WHEN $3 = 'failed' THEN '{}'::jsonb END FROM installation_state`,
			o, kind, state, d.draft, revision, ingestion, d.seed)
		return o
	}
	held, paused, released := claim("transient", "held"), claim("encrypted", "paused"), claim("transient", "released")
	ingests := []string{op("ingest", "running", 1, held), op("ingest", "running", 2, paused)}
	publishes := []string{op("publish", "queued", 1, nil), op("publish", "running", 2, nil)}
	finished := op("publish", "failed", 3, nil)
	var entered atomic.Int32
	b := decode[entryBody](t, d.enter(recoveryStart(t, d.env, &entered), "ends", entryBodyText), http.StatusCreated)
	if n := count(t, d.db, `SELECT count(*) FROM staging_claim WHERE id IN ($1, $2) AND state = 'abandoned' AND payload IS NULL
			AND payload_digest IS NULL`, held, paused); n != 2 {
		t.Fatalf("%d of the live claims are abandoned with their payload cleared", n)
	}
	if n := count(t, d.db, `SELECT count(*) FROM staging_claim WHERE id = $1 AND state = 'released'`, released); n != 1 {
		t.Fatal("entry changed a released claim")
	}
	for code, ops := range map[string][]string{"ingestion-abandoned": ingests, "recovery-mode-entered": publishes} {
		for _, o := range ops {
			if n := count(t, d.db, `SELECT count(*) FROM operation o JOIN operation_event e ON e.operation = o.id AND e.number = o.last_event
					WHERE o.id = $1 AND o.state = 'failed' AND o.owner IS NULL AND o.lease_until IS NULL AND o.last_event = 3
					AND o.error->>'type' = 'urn:bronzeward:problem:' || $2 AND o.error->>'instance' = o.id
					AND e.epoch = $3 AND e.entry->>'type' = 'failed' AND e.entry->>'code' = $2
					AND e.at >= (SELECT entered_at FROM recovery_epoch WHERE epoch = $3)`, o, code, b.Epoch); n != 1 {
				t.Errorf("operation %s is not failed %s with its terminal event in the new epoch", o, code)
			}
		}
	}
	if n := count(t, d.db, `SELECT count(*) FROM operation WHERE id = $1 AND last_event = 2`, finished); n != 1 {
		t.Fatal("entry changed a finished job")
	}
}

// The epoch's entry time is read after the entry's lock waits (§5 rule 4): an entry that waited for
// the installation state is dated after the transaction it waited for, not at its own start.
func TestRecoveryEntryTimeFollowsItsWaits(t *testing.T) {
	t.Parallel()
	e := newEnv(t, options{})
	var entered atomic.Int32
	r := recoveryStart(t, e, &entered)
	tx, err := e.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var pid int
	if err := tx.QueryRow(`SELECT pg_backend_pid() FROM installation_state FOR SHARE`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- e.enter(r, "waits", entryBodyText) }()
	waitBlockedBy(t, e.db, pid)
	var released time.Time
	if err := tx.QueryRow(`SELECT clock_timestamp()`).Scan(&released); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	b := decode[entryBody](t, <-done, http.StatusCreated)
	var at time.Time
	if err := e.db.QueryRow(`SELECT entered_at FROM recovery_epoch WHERE epoch = $1`, b.Epoch).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if at.Before(released) || !b.At.Equal(at) {
		t.Fatalf("entered at %s (answered %s); the wait ended at %s", at, b.At, released)
	}
}

// gateWriter records whether the pre-entry gate was still closed when the reply's status was written.
type gateWriter struct {
	*httptest.ResponseRecorder
	gate   *atomic.Bool
	closed bool
}

func (w *gateWriter) WriteHeader(code int) {
	w.closed = w.gate.Load()
	w.ResponseRecorder.WriteHeader(code)
}

// The gate opens before the entry answers (§12.2): a request sent once the 201 arrives must find the
// process past entry, not refused recovery-mode-active.
func TestRecoveryEntryOpensTheGateBeforeItAnswers(t *testing.T) {
	t.Parallel()
	e := newEnv(t, options{})
	var entered atomic.Int32
	r := recoveryStart(t, e, &entered)
	req := httptest.NewRequest("POST", path(entryPattern), strings.NewReader(entryBodyText))
	req.Header.Set("Authorization", "Bearer "+e.human("h-recovery"))
	req.Header.Set("Idempotency-Key", "k-entry-gate-0123456789")
	req.Header.Set("Content-Type", "application/json")
	w := &gateWriter{ResponseRecorder: httptest.NewRecorder(), gate: r.d.preEntry}
	r.ServeHTTP(w, req)
	checkAnswerHeaders(t, w.ResponseRecorder)
	checkResponse(t, req, entryBodyText, w.ResponseRecorder)
	if w.Code != http.StatusCreated || w.closed {
		t.Fatalf("answered %d %s with the gate closed: %t", w.Code, w.Body, w.closed)
	}
}

// An entry whose COMMIT succeeded but whose reply was lost, and whose read-back failed too, answers
// its outcome unknown and leaves the process gated. Its retry under the same key finds the record
// of the epoch this process minted, completes the entry once, and replays it (§12.2, §12.4).
func TestRecoveryEntryLostCommitReply(t *testing.T) {
	t.Parallel()
	e := newEnv(t, options{})
	cfg, err := pgx.ParseConfig("")
	if err != nil {
		t.Fatal(err)
	}
	dead := stdlib.OpenDB(*cfg)
	if err := dead.Close(); err != nil {
		t.Fatal(err)
	}
	var entered atomic.Int32
	var r *API
	r = recoveryStart(t, e, &entered, options{commit: func(tx *sql.Tx) error {
		if err := tx.Commit(); err != nil {
			return err
		}
		r.db = dead // the read-back cannot reach the database
		return &pgconn.PgError{Code: "08006", Message: "connection lost at COMMIT (test)"}
	}})
	wantProblem(t, e.enter(r, "lost", entryBodyText), http.StatusInternalServerError, "internal-error")
	r.db, r.o.commit = e.db, nil
	minted := epoch(t, e.db)
	if minted == r.d.start || entered.Load() != 0 || !r.d.preEntry.Load() {
		t.Fatalf("after a lost reply: epoch %s, %d callbacks, preEntry %t", minted, entered.Load(), r.d.preEntry.Load())
	}
	for range 2 {
		rec := e.enter(r, "lost", entryBodyText)
		if b := decode[entryBody](t, rec, http.StatusCreated); b.Epoch != minted || rec.Header().Get("Idempotent-Replayed") != "true" {
			t.Fatalf("the retry: %+v %v", b, rec.Header())
		}
	}
	if entered.Load() != 1 || r.d.preEntry.Load() || r.d.owner.Epoch != minted {
		t.Fatalf("after the retries: %d callbacks, preEntry %t, owning under %s", entered.Load(), r.d.preEntry.Load(), r.d.owner.Epoch)
	}
	if rec := e.do(r, call{method: "GET", path: path("/clusters"), token: e.human("h-viewer")}); rec.Code != http.StatusOK {
		t.Fatalf("a read after the retry: %d %s", rec.Code, rec.Body)
	}
}

// What entry refuses, each committing nothing (§12.2, §12.4): a body that names no valid restored
// backups or no reason; a process not started with the flag; a process with no owner identity; and a
// key whose record a restore brought back from an earlier entry, which a restored database holds in
// its current epoch and a replay would answer 201 with no epoch minted.
func TestRecoveryEntryRefusals(t *testing.T) {
	t.Parallel()
	e := newEnv(t, options{})
	start := epoch(t, e.db)
	var entered atomic.Int32
	r := recoveryStart(t, e, &entered)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	const db, stop = `{"family":"database","backup":"b","takenAt":"2026-09-25T02:00:00Z"}`, `"stoppedAt":"2026-09-25T03:00:00Z"`
	for i, body := range []string{
		`{"restored":[],` + stop + `,"reason":"x"}`,
		`{"restored":[{"family":"etcd","backup":"b","takenAt":"2026-09-25T02:00:00Z"}],` + stop + `,"reason":"x"}`,
		`{"restored":[{"family":"provider","backup":"b","takenAt":"2026-09-25T02:00:00Z"},` +
			`{"family":"provider","backup":"c","takenAt":"2026-09-25T02:00:00Z"}],` + stop + `,"reason":"x"}`,
		`{"restored":[{"family":"database","backup":"","takenAt":"2026-09-25T02:00:00Z"}],` + stop + `,"reason":"x"}`,
		`{"restored":[{"family":"database","backup":"b"}],` + stop + `,"reason":"x"}`,
		`{"restored":[{"family":"database","backup":"b","takenAt":"` + future + `"}],` + stop + `,"reason":"x"}`,
		`{"restored":[` + db + `],` + stop + `,"reason":" "}`,
		`{"restored":[` + db + `],"reason":"x"}`,
		`{"restored":[` + db + `],"stoppedAt":"` + future + `","reason":"x"}`,
	} {
		wantProblem(t, e.enter(r, "bad-"+string(rune('a'+i)), body), http.StatusBadRequest, "invalid-request")
	}
	// A process started without the flag refuses before it records anything, the principal included.
	if n := count(t, e.db, `SELECT count(*) FROM principal WHERE sub = 'h-recovery'`); n != 0 {
		t.Fatalf("a refused body recorded the principal: %d rows", n)
	}
	wantProblem(t, e.enter(e.api, "normal", entryBodyText), http.StatusConflict, "conflict")
	if n := count(t, e.db, `SELECT count(*) FROM principal WHERE sub = 'h-recovery'`); n != 0 {
		t.Fatalf("a process started without the flag recorded the principal: %d rows", n)
	}
	d := r.d
	d.owner.ID = ""
	wantProblem(t, e.enter(e.buildWith(d, options{}), "no-owner", entryBodyText), http.StatusServiceUnavailable, "dependency-unavailable")
	if epoch(t, e.db) != start || entered.Load() != 0 || !r.d.preEntry.Load() {
		t.Fatal("a refused entry changed the epoch or ended the pre-entry gate")
	}
	// A first entry commits. A second recovery start of the same restore, which has no entry of its
	// own, refuses that entry's key and stays gated: the record is current, but not its entry.
	var twinEntered atomic.Int32
	twin := recoveryStart(t, e, &twinEntered)
	first := decode[entryBody](t, e.enter(r, "cycle", entryBodyText), http.StatusCreated)
	doc := wantProblem(t, e.enter(twin, "cycle", entryBodyText), http.StatusConflict, "conflict")
	if doc["epoch"] != first.Epoch || doc["request"] == nil || twinEntered.Load() != 0 || !twin.d.preEntry.Load() {
		t.Fatalf("another start's entry record: %v, %d callbacks, preEntry %t", doc, twinEntered.Load(), twin.d.preEntry.Load())
	}
	// Then the database is as a restore of a backup taken after the entry leaves it: its record is
	// in the current epoch. A process started on it refuses that key, and enters under a new one.
	var again atomic.Int32
	next := recoveryStart(t, e, &again)
	doc = wantProblem(t, e.enter(next, "cycle", entryBodyText), http.StatusConflict, "conflict")
	if doc["epoch"] != first.Epoch || doc["request"] == nil || again.Load() != 0 || epoch(t, e.db) != first.Epoch {
		t.Fatalf("a restored entry's record: %v, %d callbacks, epoch %s", doc, again.Load(), epoch(t, e.db))
	}
	b := decode[entryBody](t, e.enter(next, "cycle-2", entryBodyText), http.StatusCreated)
	if b.PreviousEpoch != first.Epoch || again.Load() != 1 {
		t.Fatalf("the new entry: %+v, %d callbacks", b, again.Load())
	}
}
