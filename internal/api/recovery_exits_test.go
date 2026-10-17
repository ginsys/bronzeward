package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/auth"
)

// exit is h-recovery's request to leave recovery mode on a under key.
func (e *env) exit(a *API, key string) *httptest.ResponseRecorder {
	return e.do(a, call{method: "POST", path: prefix + "/recovery/exits", token: e.human("h-recovery"),
		key: "k-exit-" + key + "-0123456789", body: `{"facts":{"note":"every scope released"}}`})
}

// Leaving recovery mode (execution-recovery.md §7.6; persistence-api.md §9.2, §12.2): refused while
// any machine scope is not released, naming one, and outside recovery mode. It ends recovery mode,
// returns every released scope to normal with a scope state entry (ruling X1) and records the exit
// with its act in the current epoch. A controller whose epoch an entry superseded leaves nothing.
func TestRecoveryExit(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	ae.second(t)
	wantProblem(t, ae.exit(ae.a, "normal"), http.StatusConflict, "conflict")
	r := ae.enterStopped(t, time.Now().Add(-time.Minute))
	mustExec(t, ae.db, `UPDATE machine SET scope_state = 'released' WHERE id <> $1`, ae.machine2)
	if doc := wantProblem(t, ae.exit(r, "unreleased"), http.StatusConflict, "conflict"); doc["machine"] != ae.machine2 {
		t.Fatalf("the refusal names %v; want the unreleased scope %s", doc["machine"], ae.machine2)
	}
	mustExec(t, ae.db, `UPDATE machine SET scope_state = 'released'`)

	human := ae.principalOf("h-recovery")
	rec := ae.exit(r, "left")
	b := decode[exitBody](t, rec, http.StatusCreated)
	if b.Epoch != epoch(t, ae.db) || b.ExitedBy != human || b.Role != "recovery-admin" ||
		len(b.Scopes) != count(t, ae.db, `SELECT count(*) FROM machine`) ||
		rec.Header().Get("Bronzeward-Recovery-Mode") != "" {
		t.Fatalf("answered %+v with headers %v", b, rec.Header())
	}
	if n := count(t, ae.db, `SELECT count(*) FROM installation_state WHERE recovery_mode`); n != 0 {
		t.Fatal("recovery mode is still in effect")
	}
	if n := count(t, ae.db, `SELECT count(*) FROM machine m WHERE m.scope_state <> 'normal'
			OR NOT EXISTS (SELECT FROM machine_event e WHERE e.machine = m.id AND e.revision = m.revision_counter
				AND e.kind = 'scope-state' AND e.epoch = $1 AND e.entry->>'from' = 'released' AND e.entry->>'to' = 'normal'
				AND e.entry->>'cause' = 'exit' AND e.entry->>'act' = $2)`, b.Epoch, b.Act); n != 0 {
		t.Fatalf("%d scopes are not returned to normal by the exit", n)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM recovery_exit x JOIN act a ON a.id = x.act WHERE x.epoch = $1 AND x.act = $2
			AND x.exited_by = $3 AND x.facts->>'note' IS NOT NULL AND a.action = 'recovery.exit' AND a.role = 'recovery-admin'
			AND a.epoch = $1`, b.Epoch, b.Act, human); n != 1 {
		t.Fatal("no exit record and act in the epoch")
	}
	wantProblem(t, ae.exit(r, "again"), http.StatusConflict, "conflict")

	// A recovery entry commits after this controller's: it leaves nothing in the new epoch (§5.1).
	mustExec(t, ae.db, `UPDATE installation_state SET recovery_mode = true`)
	mustExec(t, ae.db, `UPDATE machine SET scope_state = 'released'`)
	newEpoch(t, ae.db)
	wantProblem(t, ae.exit(r, "superseded"), http.StatusServiceUnavailable, "epoch-superseded")
	if n := count(t, ae.db, `SELECT count(*) FROM installation_state WHERE recovery_mode`); n != 1 {
		t.Fatal("a controller of a superseded epoch left recovery mode")
	}
}

// PA §16 (T11): leaving recovery mode takes the installation state FOR UPDATE before it checks that
// every scope is released, so it waits for an inventory request holding that row FOR SHARE and then
// sees the machine that request inserted, whose scope is pre-restore unaccounted: the exit is
// refused. With the exit reading the installation state FOR SHARE (the control) it does not wait,
// misses the machine and leaves recovery mode over an unaccounted scope.
func TestRecoveryExitRacesInventory(t *testing.T) {
	t.Parallel()
	for name, control := range map[string]bool{"for update": false, "for share": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ae := newAttemptEnv(t, "")
			var entered atomic.Int32
			r := recoveryStart(t, ae.env, &entered, options{shareExit: control})
			decode[entryBody](t, ae.enter(r, "race", entryBodyText), http.StatusCreated)
			mustExec(t, ae.db, `UPDATE machine SET scope_state = 'released'`)

			// The act-order lock is every transaction's last: holding it stops the inventory after its
			// insert, with the installation state held FOR SHARE.
			hold, err := ae.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = hold.Rollback() }()
			if err := auth.LockActOrder(context.Background(), hold); err != nil {
				t.Fatal(err)
			}
			inventoried := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				inventoried <- ae.do(r, machineCall(ae.human("h-author"), "k-machine-exit-race-01", ae.cluster,
					"0b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5e"))
			}()
			waitForLockWaits(t, ae.db, 1)
			left := make(chan *httptest.ResponseRecorder, 1)
			go func() { left <- ae.exit(r, "race") }()
			waitForLockWaits(t, ae.db, 2)
			if err := hold.Rollback(); err != nil {
				t.Fatal(err)
			}
			m := decode[machineBody](t, <-inventoried, http.StatusCreated)
			if m.ScopeState != "pre-restore-unaccounted" {
				t.Fatalf("the machine inventoried in recovery mode is %s", m.ScopeState)
			}
			rec := <-left
			if control {
				decode[exitBody](t, rec, http.StatusCreated)
				return
			}
			if doc := wantProblem(t, rec, http.StatusConflict, "conflict"); doc["machine"] != m.ID {
				t.Fatalf("the refusal names %v; want the machine inventoried meanwhile, %s", doc["machine"], m.ID)
			}
		})
	}
}

// GET /recovery (persistence-api.md §9.2; ruling X2): the current epoch, whether recovery mode is in
// effect, the entry that minted the epoch with its restored backups (none for the installation's),
// the exit that ended it if any, and every machine's scope state.
func TestRecoveryRead(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	viewer := ae.human("h-viewer")
	machines := count(t, ae.db, `SELECT count(*) FROM machine`)
	// read answers GET /recovery on a, and this machine's scope state in it.
	read := func(a *API) (recoveryBody, string) {
		t.Helper()
		b := decode[recoveryBody](t, ae.do(a, call{method: "GET", path: prefix + "/recovery", token: viewer}), http.StatusOK)
		if len(b.Scopes) != machines {
			t.Fatalf("%d scopes for %d machines: %+v", len(b.Scopes), machines, b)
		}
		for _, s := range b.Scopes {
			if s.Machine == ae.machine {
				return b, s.State
			}
		}
		t.Fatalf("no scope of %s: %+v", ae.machine, b)
		return b, ""
	}
	if b, s := read(ae.a); b.Epoch != epoch(t, ae.db) || b.RecoveryMode || b.Entry != nil || b.Exit != nil || s != "normal" {
		t.Fatalf("before any entry: %+v", b)
	}
	r := ae.enterStopped(t, time.Now().Add(-time.Minute))
	b, s := read(r)
	if !b.RecoveryMode || b.Entry == nil || b.Entry.EnteredBy != ae.principalOf("h-recovery") ||
		b.Entry.Reason != "database restored after storage failure" || len(b.Entry.Restored) != 1 ||
		b.Entry.Restored[0].Family != "database" || b.Entry.Restored[0].Backup != "pg-2026-09-25T02:00Z" ||
		b.Exit != nil || s != "pre-restore-unaccounted" {
		t.Fatalf("in recovery mode: %+v", b)
	}
	mustExec(t, ae.db, `UPDATE machine SET scope_state = 'released'`)
	x := decode[exitBody](t, ae.exit(r, "read"), http.StatusCreated)
	if b, s := read(r); b.RecoveryMode || b.Entry == nil || b.Exit == nil || b.Exit.Act != x.Act || s != "normal" {
		t.Fatalf("after the exit: %+v", b)
	}
}

// After its entry a recovery start serves observation, the recovery routes, the acts that only remove
// authority and the installation-wide acts (execution-recovery.md §7.2, persistence-api.md §12.2); plan
// creation, approval and unfreezing wait for the normal restart (ruling X3). A normal process answers
// the same requests by their own checks.
func TestRecoveryStartAfterEntryRestricts(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	r := ae.enterStopped(t, time.Now().Add(-time.Minute))
	mustExec(t, ae.db, `UPDATE machine SET scope_state = 'released'`)
	refused := []call{
		{method: "POST", path: prefix + "/plans", token: ae.human("h-publisher"), key: "k-plan-after-entry-012", body: `{}`},
		{method: "POST", path: prefix + "/plans/" + ae.pid + "/approvals", token: ae.human("h-approver"), key: "k-approve-after-entry", body: `{}`},
		{method: "POST", path: prefix + "/machines/" + ae.machine + "/unfreezes", token: ae.human("h-approver"),
			key: "k-unfreeze-after-entry", body: `{}`},
	}
	for _, c := range refused {
		wantProblem(t, ae.do(r, c), http.StatusConflict, "recovery-mode-active")
		if rec := ae.do(ae.a, c); rec.Code == http.StatusConflict {
			if got, _ := problem(t, rec); got == "recovery-mode-active" {
				t.Fatalf("a normal process refused %s %s as the recovery start does", c.method, c.path)
			}
		}
	}
	if rec := ae.do(r, call{method: "POST", path: prefix + "/drafts", token: ae.human("h-author"), key: "k-draft-after-entry-01",
		body: `{}`}); rec.Code == http.StatusConflict {
		t.Fatalf("the recovery start refused a draft after its entry: %s", rec.Body)
	}
}
