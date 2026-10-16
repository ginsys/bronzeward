package api

import (
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/staging"
)

// recoveryStart is a process started with the recovery-start flag over e's database (§12.2): it
// owns under the epoch it read at its start, serves only the entry until its entry commits, and
// counts the entry callbacks it runs.
func recoveryStart(t *testing.T, e *env, entered *atomic.Int32) *API {
	t.Helper()
	d := e.d
	ep := epoch(t, e.db)
	d.owner = staging.Owner{ID: "r/1/" + rand.Text(), Epoch: ep}
	d.start, d.recoveryStart = ep, true
	d.preEntry = new(atomic.Bool)
	d.preEntry.Store(true)
	d.onEntry = func() { entered.Add(1) }
	return e.buildWith(d, options{})
}

const entryBodyText = `{"restored":[{"family":"database","backup":"pg-2026-09-25T02:00Z","takenAt":"2026-09-25T02:00:00Z"}],` +
	`"reason":"database restored after storage failure"}`

// enter is h-recovery's entry request on a under key.
func (e *env) enter(a *API, key, body string) *httptest.ResponseRecorder {
	return e.do(a, call{method: "POST", path: path(entryPattern), token: e.human("h-recovery"), key: "k-entry-" + key + "-0123456789",
		body: body})
}

// Recovery-mode entry (persistence-api.md §12.2 T9 steps 1, 2, 5 and 6; execution-recovery.md §7.2):
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
				b.Operations[0] != ae.op {
				t.Fatalf("answered %+v", b)
			}
			if n := count(t, ae.db, `SELECT count(*) FROM installation_state WHERE recovery_mode`); n != 1 {
				t.Fatal("recovery mode is not in effect")
			}
			if n := count(t, ae.db, `SELECT count(*) FROM recovery_epoch e JOIN recovery_restore r USING (epoch)
					WHERE e.epoch = $1 AND e.entered_by = $2 AND e.reason = 'database restored after storage failure'
					AND r.family = 'database' AND r.backup = 'pg-2026-09-25T02:00Z' AND r.taken_at = '2026-09-25T02:00:00Z'`,
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
	for i, body := range []string{
		`{"restored":[],"reason":"x"}`,
		`{"restored":[{"family":"etcd","backup":"b","takenAt":"2026-09-25T02:00:00Z"}],"reason":"x"}`,
		`{"restored":[{"family":"provider","backup":"b","takenAt":"2026-09-25T02:00:00Z"},` +
			`{"family":"provider","backup":"c","takenAt":"2026-09-25T02:00:00Z"}],"reason":"x"}`,
		`{"restored":[{"family":"database","backup":"","takenAt":"2026-09-25T02:00:00Z"}],"reason":"x"}`,
		`{"restored":[{"family":"database","backup":"b"}],"reason":"x"}`,
		`{"restored":[{"family":"database","backup":"b","takenAt":"` + future + `"}],"reason":"x"}`,
		`{"restored":[{"family":"database","backup":"b","takenAt":"2026-09-25T02:00:00Z"}],"reason":" "}`,
	} {
		wantProblem(t, e.enter(r, "bad-"+string(rune('a'+i)), body), http.StatusBadRequest, "invalid-request")
	}
	wantProblem(t, e.enter(e.api, "normal", entryBodyText), http.StatusConflict, "conflict")
	d := r.d
	d.owner.ID = ""
	wantProblem(t, e.enter(e.buildWith(d, options{}), "no-owner", entryBodyText), http.StatusServiceUnavailable, "dependency-unavailable")
	if epoch(t, e.db) != start || entered.Load() != 0 || !r.d.preEntry.Load() {
		t.Fatal("a refused entry changed the epoch or ended the pre-entry gate")
	}
	// A first entry commits; then the database is as a restore of a backup taken after it leaves it:
	// its record is in the current epoch. A process started on it refuses that key, and enters under
	// a new one.
	first := decode[entryBody](t, e.enter(r, "cycle", entryBodyText), http.StatusCreated)
	var again atomic.Int32
	next := recoveryStart(t, e, &again)
	doc := wantProblem(t, e.enter(next, "cycle", entryBodyText), http.StatusConflict, "conflict")
	if doc["epoch"] != first.Epoch || doc["request"] == nil || again.Load() != 0 || epoch(t, e.db) != first.Epoch {
		t.Fatalf("a restored entry's record: %v, %d callbacks, epoch %s", doc, again.Load(), epoch(t, e.db))
	}
	b := decode[entryBody](t, e.enter(next, "cycle-2", entryBodyText), http.StatusCreated)
	if b.PreviousEpoch != first.Epoch || again.Load() != 1 {
		t.Fatalf("the new entry: %+v, %d callbacks", b, again.Load())
	}
}
