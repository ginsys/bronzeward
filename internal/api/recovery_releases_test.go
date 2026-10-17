package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos"
)

// §7.3 step 5 (ruling G5): restored pending operations are reclassified by the executor of the normal
// restart entry allows (one with no attempt, by the recovery start too: TestRecoveryStartReclassifies),
// never by an act and never by retrying: one with no attempt ends cancelled, its
// approval's epoch no longer current, and one whose attempt its recorded response accounted completes
// on a completion observation. Neither is rejected, and nothing is sent again.
func TestRecoveryRestartReclassifies(t *testing.T) {
	t.Parallel()
	for name, attempted := range map[string]bool{"no attempt": false, "accounted attempt": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			xe := newExecEnv(t)
			xe.ready(t)
			cm, err := xe.a.commitPlan(context.Background(), xe.pid)
			if err != nil {
				t.Fatal(err)
			}
			want := "cancelled"
			if attempted {
				at, err := xe.a.recordAttempt(context.Background(), cm.Operation, 1)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := xe.a.recordResponse(context.Background(), at.Attempt, answer(talos.ClassAccepted)); err != nil {
					t.Fatal(err)
				}
				xe.node.SetMachineConfig(t, xe.artifact)
				want = "completed"
			}
			var entered atomic.Int32
			decode[entryBody](t, xe.enter(recoveryStart(t, xe.env, &entered), "restart", entryBodyText), http.StatusCreated)
			if s := xe.opState(t); s != "unresolved" {
				t.Fatalf("entry left the operation %s", s)
			}
			life, stop := context.WithCancel(t.Context())
			idle := make(chan struct{})
			a := xe.executor(t, life, idle, options{})
			a.startExecutor()
			defer stopping(a, stop)
			until(t, a, idle, "the operation is "+want, func() bool { return xe.opState(t) == want })
			if n := len(xe.node.AppliedRequests()); n != 0 {
				t.Fatalf("%d requests after the restore", n)
			}
		})
	}
}

// §7.3 step 5 in the recovery start (ruling R8b-1): once its entry commits, the recovery start, which
// runs no executor, ends a restored operation with no attempt cancelled itself, so that its scope can
// be marked and released before the normal restart; nothing is sent.
func TestRecoveryStartReclassifies(t *testing.T) {
	t.Parallel()
	xe := newExecEnv(t)
	xe.ready(t)
	if _, err := xe.a.commitPlan(context.Background(), xe.pid); err != nil {
		t.Fatal(err)
	}
	life, stop := context.WithCancel(t.Context())
	xe.env.d.exe, xe.env.d.life, xe.env.d.runs = xe.x, life, &sync.WaitGroup{}
	xe.env.d.exec = testExecution()
	xe.env.d.exec.SettleFloor = 3 * time.Second
	var entered atomic.Int32
	r := recoveryStart(t, xe.env, &entered)
	defer stopping(r, stop)
	decode[entryBody](t, xe.enter(r, "reclassify", entryBodyText), http.StatusCreated)
	// Two recovery reads: the pass of the first ran in full. The scope is still pre-restore
	// unaccounted, so the operation is not reclassified before the decision accounts it (§7.3 step 1).
	// The bounds are generous: on a loaded runner two paced reads took over ten seconds.
	deadline := time.Now().Add(time.Minute)
	for count(t, xe.db, `SELECT count(*) FROM observation o JOIN observation_start s
		ON s.machine = o.machine AND s.revision = o.basis WHERE o.machine = $1 AND s.purpose = 'recovery'`, xe.machine) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the recovery start took fewer than two recovery reads")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if s := xe.opState(t); s != "unresolved" {
		t.Fatalf("before the accounting, the recovery start left the operation %s", s)
	}
	mustExec(t, xe.db, `UPDATE machine SET scope_state = 'unresolved' WHERE id = $1`, xe.machine)
	deadline = time.Now().Add(time.Minute)
	for xe.opState(t) != "cancelled" {
		if time.Now().After(deadline) {
			t.Fatalf("the recovery start left the operation %s", xe.opState(t))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n := len(xe.node.AppliedRequests()); n != 0 {
		t.Fatalf("%d requests after the restore", n)
	}
}

// release is h-recovery's release of machine m's scope on a under key.
func (me *markEnv) release(a *API, m, key string) *httptest.ResponseRecorder {
	return me.do(a, call{method: "POST", path: prefix + "/recovery/scopes/" + m + "/releases", token: me.human("h-recovery"),
		key: "k-release-" + key + "-0123456789", body: `{"facts":{"note":"marked ready after the restore"}}`})
}

// The scope release (execution-recovery.md §7.3 step 7, §7.4; persistence-api.md §9.2): only a ready
// scope is released, and only while its Desired release is the one its mark checked. The release is a
// scope state entry, last on the timeline, naming the mark it rests on and the act, recorded in the
// current epoch; it opens the scope gate for that scope only (comparison 6). It is refused outside
// recovery mode, on a scope in any other state, for an unknown machine, and by a controller whose
// epoch an entry superseded.
func TestRecoveryScopeRelease(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	ae.sent(t)
	ae.second(t)
	me := &markEnv{attemptEnv: ae, x: &fakeExecutor{access: provider.NewTalosAccess(provider.TalosAccessVersion{
		Path: "access/talos/" + ae.cluster, Version: 2, CreatedTime: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)}, []byte("tc"))}}
	m2 := ae.machine2
	me.report(me.recordedIdentity(t, m2))

	wantProblem(t, me.release(me.controller(t), m2, "normal"), http.StatusConflict, "conflict")
	r := ae.enterStopped(t, time.Now().Add(-time.Minute))
	ctl := me.controller(t)
	wantProblem(t, me.release(ctl, m2, "unaccounted"), http.StatusConflict, "recovery-mode-active")
	wantProblem(t, me.release(ctl, "mch_aaaaaaaaaaaaaaaaaaaaaaaaaa", "unknown"), http.StatusNotFound, "not-found")
	start := ae.recoveryRead(t, m2, 5*time.Minute, "rv1")
	end := ae.recoveryRead(t, m2, 6*time.Minute, "rv1")
	decode[scopeAccountingBody](t, ae.account(r, "releases", `{"scopes":[{"machine":"`+m2+`","start":"`+start+`","end":"`+end+`"}]}`),
		http.StatusCreated)
	wantProblem(t, me.release(ctl, m2, "blocked"), http.StatusConflict, "conflict")

	mark := decode[scopeMarkBody](t, me.mark(ctl, m2, "ready"), http.StatusCreated)
	if mark.State != "ready" {
		t.Fatalf("the scope was marked %s missing %v", mark.State, mark.Missing)
	}
	var desired string
	if err := ae.db.QueryRow(`SELECT desired FROM machine_state WHERE machine = $1`, m2).Scan(&desired); err != nil {
		t.Fatal(err)
	}
	// The mark records the Desired release it checked (ruling G4).
	if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND revision = $2 AND entry->>'release' = $3`,
		m2, mark.Revision, desired); n != 1 {
		t.Fatalf("the mark's entry does not name the Desired release %s it checked", desired)
	}
	// Desired moved since the mark: the dependencies it checked are not the ones a plan would need.
	mustExec(t, ae.db, `UPDATE machine_state SET desired = NULL WHERE machine = $1`, m2)
	wantProblem(t, me.release(ctl, m2, "moved"), http.StatusConflict, "conflict")
	mustExec(t, ae.db, `UPDATE machine_state SET desired = $2 WHERE machine = $1`, m2, desired)

	human := ae.principalOf("h-recovery")
	b := decode[scopeReleaseBody](t, me.release(ctl, m2, "released"), http.StatusCreated)
	if b.Machine != m2 || b.From != "ready" || b.State != "released" || b.Mark != mark.Revision || b.ReleasedBy != human ||
		b.Role != "recovery-admin" || b.Epoch != epoch(t, ae.db) || b.Revision <= mark.Revision {
		t.Fatalf("answered %+v; want the release of the ready scope marked at %d", b, mark.Revision)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM machine m JOIN machine_event e ON e.machine = m.id AND e.revision = m.revision_counter
			JOIN scope_release s ON s.machine = m.id AND s.epoch = $3 AND s.revision = e.revision
		WHERE m.id = $1 AND m.scope_state = 'released' AND e.kind = 'scope-state' AND e.revision = $2 AND e.entry->>'from' = 'ready'
			AND e.entry->>'to' = 'released' AND e.entry->>'cause' = 'release' AND e.entry->>'act' = $4
			AND (e.entry->>'mark')::bigint = $5 AND e.entry->'facts'->>'note' IS NOT NULL AND s.act = $4 AND s.mark_revision = $5
			AND s.released_by = $6 AND s.at = e.at`, m2, b.Revision, b.Epoch, b.Act, mark.Revision, human); n != 1 {
		t.Fatal("no scope state entry, last, and release record of the release")
	}
	if n := count(t, ae.db, `SELECT count(*) FROM act WHERE id = $1 AND action = 'recovery.release' AND role = 'recovery-admin'
			AND subjects = ARRAY[$2]`, b.Act, m2); n != 1 {
		t.Fatal("no act of the release")
	}
	// The released scope passes comparison 6 while recovery mode is in effect.
	var scope string
	var recovery bool
	if err := ae.db.QueryRow(`SELECT m.scope_state, i.recovery_mode FROM machine m, installation_state i WHERE m.id = $1`, m2).Scan(&scope,
		&recovery); err != nil {
		t.Fatal(err)
	}
	if cause := scopeGate(false, sql.NullInt64{}, sql.NullInt64{}, scope, recovery); !recovery || cause != "" {
		t.Fatalf("the released scope's gate in recovery mode %v: %q", recovery, cause)
	}
	wantProblem(t, me.release(ctl, m2, "again"), http.StatusConflict, "conflict")

	// A recovery entry commits after this controller's: it releases nothing in the new epoch (§5.1).
	mustExec(t, ae.db, `UPDATE machine SET scope_state = 'ready' WHERE id = $1`, m2)
	before := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE machine = $1`, m2)
	newEpoch(t, ae.db)
	wantProblem(t, me.release(ctl, m2, "superseded"), http.StatusServiceUnavailable, "epoch-superseded")
	if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE machine = $1`, m2); n != before {
		t.Fatal("a controller of a superseded epoch released the scope")
	}
}
