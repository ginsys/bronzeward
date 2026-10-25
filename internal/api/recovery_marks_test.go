package api

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos"
)

// nodeReader is a node that reports identity and a version and refuses its configuration read.
type nodeReader struct{ identity talos.Identity }

func (r nodeReader) Identity(context.Context) (talos.Identity, error) { return r.identity, nil }
func (r nodeReader) Version(context.Context) (string, error)          { return "v1.13.6", nil }
func (r nodeReader) MachineConfig(context.Context) (talos.Config, error) {
	return talos.Config{}, errors.New("no configuration in this test")
}
func (r nodeReader) Close() error { return nil }

// markEnv is an attemptEnv in recovery mode with a controller of the current epoch whose executor
// is x and whose node reports the identity reports holds.
type markEnv struct {
	*attemptEnv
	x       *fakeExecutor
	mu      sync.Mutex
	reports talos.Identity
	ctl     *API
}

// recordedIdentity is the identity the machine record and its cluster bind.
func (me *markEnv) recordedIdentity(t *testing.T, m string) talos.Identity {
	t.Helper()
	var smbios, node, cluster sql.NullString
	if err := me.db.QueryRow(`SELECT m.smbios_uuid::text, m.talos_node_id, c.talos_cluster_id FROM machine m
		JOIN cluster c ON c.id = m.cluster WHERE m.id = $1`, m).Scan(&smbios, &node, &cluster); err != nil {
		t.Fatal(err)
	}
	// A machine keyed by its SMBIOS UUID records no node ID; the node still reports one.
	got := talos.Identity{SMBIOSUUID: smbios.String, NodeID: cmp.Or(node.String, "node-of-"+m), ClusterID: cluster.String}
	if !validIdentity(got) {
		t.Fatalf("the fixture's machine %s binds no readable identity: %+v", m, got)
	}
	return got
}

func (me *markEnv) report(id talos.Identity) {
	me.mu.Lock()
	defer me.mu.Unlock()
	me.reports = id
}

// controller is a controller of the current epoch, the one a start in recovery mode is.
func (me *markEnv) controller(t *testing.T) *API {
	t.Helper()
	dial := func(context.Context, []byte, string) (talos.Reader, error) {
		me.mu.Lock()
		defer me.mu.Unlock()
		return nodeReader{identity: me.reports}, nil
	}
	return observer(t, me.env, me.x, options{dial: dial})
}

// mark is h-recovery's mark of machine m's scope on a under key.
func (me *markEnv) mark(a *API, m, key string) *httptest.ResponseRecorder {
	return me.do(a, call{method: "POST", path: prefix + "/recovery/scopes/" + m + "/marks", token: me.human("h-recovery"),
		key: "k-mark-" + key + "-0123456789", body: `{"facts":{"note":"after the restore"}}`})
}

func (me *markEnv) restorations(t *testing.T, m string) int {
	t.Helper()
	return count(t, me.db, `SELECT count(*) FROM observation_start WHERE machine = $1 AND purpose = 'restoration'`, m)
}

// The scope mark (execution-recovery.md §7.3 steps 3, 4 and 6, §7.4; persistence-api.md §9.2): the
// request takes a restoration observation of the machine, then checks the dependencies that apply
// its Desired release under the executor identity, then marks the scope from that evidence: ready
// when the observation read the identity the machine record binds and every dependency is present,
// blocked naming what is missing otherwise, unresolved while a restored operation holds the scope.
// Each mark is a scope state entry, last on the timeline, naming the observation, the missing items
// and the act. It is refused outside recovery mode, on a scope still pre-restore unaccounted (which
// leaves only by step 1), on a released scope and for an unknown machine, before anything is read.
func TestRecoveryScopeMark(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	ae.sent(t)
	ae.second(t)
	me := &markEnv{attemptEnv: ae, x: &fakeExecutor{access: provider.NewTalosAccess(provider.TalosAccessVersion{
		Path: "access/talos/" + ae.cluster, Version: 2, CreatedTime: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)}, []byte("tc"))}}
	m2 := ae.machine2
	me.report(me.recordedIdentity(t, m2))

	wantProblem(t, me.mark(me.controller(t), m2, "normal"), http.StatusConflict, "conflict")
	wantProblem(t, me.mark(observer(t, me.env, nil, options{}), m2, "bare-normal"), http.StatusConflict, "conflict")
	r := ae.enterStopped(t, time.Now().Add(-time.Minute))
	ctl := me.controller(t)
	// A controller with no executor refuses as one with an executor does, until a mark needs it.
	bare := observer(t, me.env, nil, options{})
	for _, a := range []*API{ctl, bare} {
		wantProblem(t, me.mark(a, m2, "unaccounted"), http.StatusConflict, "recovery-mode-active")
		wantProblem(t, me.mark(a, "mch_aaaaaaaaaaaaaaaaaaaaaaaaaa", "unknown"), http.StatusNotFound, "not-found")
	}
	if n := me.restorations(t, m2); n != 0 {
		t.Fatalf("a refused mark observed the machine %d times", n)
	}

	start := ae.recoveryRead(t, ae.machine, 5*time.Minute, "rv1")
	end := ae.recoveryRead(t, ae.machine, 6*time.Minute, "rv1")
	start2 := ae.recoveryRead(t, m2, 5*time.Minute, "rv1")
	end2 := ae.recoveryRead(t, m2, 6*time.Minute, "rv1")
	acct := decode[scopeAccountingBody](t, ae.account(r, "marks", `{"scopes":[{"machine":"`+ae.machine+`","start":"`+start+
		`","end":"`+end+`"},{"machine":"`+m2+`","start":"`+start2+`","end":"`+end2+`"}]}`), http.StatusCreated)
	var accounted int64
	for _, s := range acct.Scopes {
		if s.Machine == m2 {
			accounted = s.Revision
		}
	}
	if accounted == 0 {
		t.Fatalf("the accounting answered no scope of %s: %+v", m2, acct.Scopes)
	}
	wantProblem(t, me.mark(bare, m2, "bare"), http.StatusServiceUnavailable, "dependency-unavailable")
	if n := me.restorations(t, m2); n != 0 {
		t.Fatalf("a mark with no executor observed the machine %d times", n)
	}

	human := ae.principalOf("h-recovery")
	for _, c := range []struct {
		name, state string
		missing     []string
		arrange     func()
	}{
		{"ready", "ready", []string{}, func() {}},
		{"artifact", "blocked", []string{"artifact"}, func() { me.x.decryptErr = errors.New("the version was refused") }},
		{"identity", "blocked", []string{"identity"}, func() {
			other := me.recordedIdentity(t, m2)
			other.NodeID, other.SMBIOSUUID = "another-node", ""
			me.report(other)
		}},
		{"access", "blocked", []string{"observation", "access"}, func() { me.x.err = provider.ErrDenied }},
		{"again", "ready", []string{}, func() {}},
	} {
		me.x.decryptErr, me.x.err = nil, nil
		me.report(me.recordedIdentity(t, m2))
		c.arrange()
		from := ""
		if err := ae.db.QueryRow(`SELECT scope_state FROM machine WHERE id = $1`, m2).Scan(&from); err != nil {
			t.Fatal(err)
		}
		b := decode[scopeMarkBody](t, me.mark(ctl, m2, c.name), http.StatusCreated)
		if b.Machine != m2 || b.From != from || b.State != c.state || b.MarkedBy != human || b.Role != "recovery-admin" ||
			b.Epoch != epoch(t, ae.db) || len(b.Missing) != len(c.missing) {
			t.Fatalf("%s: answered %+v; want %s missing %v", c.name, b, c.state, c.missing)
		}
		for i := range c.missing {
			if b.Missing[i] != c.missing[i] {
				t.Fatalf("%s: missing %v; want %v", c.name, b.Missing, c.missing)
			}
		}
		if n := count(t, ae.db, `SELECT count(*) FROM machine m JOIN machine_event e ON e.machine = m.id AND e.revision = m.revision_counter
				JOIN observation o ON o.id = $4 AND o.machine = m.id JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis
			WHERE m.id = $1 AND m.scope_state = $2 AND e.kind = 'scope-state' AND e.revision = $3 AND e.entry->>'from' = $5
				AND e.entry->>'to' = $2 AND e.entry->>'cause' = 'restoration-mark' AND e.entry->>'act' = $6
				AND e.entry->>'observation' = $4 AND jsonb_array_length(e.entry->'missing') = $7 AND e.entry->'facts'->>'note' IS NOT NULL
				AND s.purpose = 'restoration' AND s.plan IS NULL AND s.operation IS NULL AND o.basis > $8`,
			m2, c.state, b.Revision, b.Observation, from, b.Act, len(c.missing), accounted); n != 1 {
			t.Fatalf("%s: no scope state entry, last, on the mark's restoration observation", c.name)
		}
		if n := count(t, ae.db, `SELECT count(*) FROM act WHERE id = $1 AND action = 'recovery.mark' AND role = 'recovery-admin'
				AND subjects = ARRAY[$2]`, b.Act, m2); n != 1 {
			t.Fatalf("%s: no act of the mark", c.name)
		}
	}

	// A publication moves Desired while the mark waits on the machine's lock: the mark reads the
	// Desired its lock admits, not its statement's snapshot, and refuses (ruling M3).
	var desired string
	if err := ae.db.QueryRow(`SELECT desired FROM machine_state WHERE machine = $1`, m2).Scan(&desired); err != nil {
		t.Fatal(err)
	}
	moved, err := ae.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = moved.Rollback() }()
	committed := make(chan error, 1)
	var once sync.Once
	me.x.mu.Lock()
	me.x.onDecrypt = func() {
		once.Do(func() {
			mustExec(t, moved, `SELECT FROM machine WHERE id = $1 FOR SHARE`, m2)
			mustExec(t, moved, `UPDATE machine_state SET desired = NULL WHERE machine = $1`, m2)
			go func() {
				for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
					var waiting int
					if err := ae.db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()
						AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil || waiting > 0 || time.Now().After(deadline) {
						if err == nil && waiting == 0 {
							err = errors.New("the mark never waited on the machine's lock")
						}
						if err != nil {
							_ = moved.Rollback()
							committed <- err
							return
						}
						committed <- moved.Commit()
						return
					}
				}
			}()
		})
	}
	me.x.mu.Unlock()
	wantProblem(t, me.mark(ctl, m2, "moved"), http.StatusConflict, "conflict")
	if err := <-committed; err != nil {
		t.Fatal(err)
	}
	me.x.mu.Lock()
	me.x.onDecrypt = nil
	me.x.mu.Unlock()
	mustExec(t, ae.db, `UPDATE machine_state SET desired = $2 WHERE machine = $1`, m2, desired)

	// The Talos access rotates between the observation's read and the dependency check's: the check
	// passed a credential the observation was not made with, so the observation is missing.
	v2 := me.x.access
	me.x.onRead = func() {
		me.x.mu.Lock()
		defer me.x.mu.Unlock()
		me.x.onRead = nil
		me.x.access = provider.NewTalosAccess(provider.TalosAccessVersion{Path: "access/talos/" + ae.cluster, Version: 3,
			CreatedTime: time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)}, []byte("tc3"))
	}
	if b := decode[scopeMarkBody](t, me.mark(ctl, m2, "rotated"), http.StatusCreated); b.State != "blocked" ||
		len(b.Missing) != 1 || b.Missing[0] != "observation" {
		t.Fatalf("a mark checking access the observation was not made with was marked %s missing %v", b.State, b.Missing)
	}
	me.x.mu.Lock()
	me.x.access = v2
	me.x.mu.Unlock()
	// An observation that is not evidence still names the other identity it read: each item is missing.
	other := me.recordedIdentity(t, m2)
	other.NodeID, other.SMBIOSUUID = "another-node", ""
	me.report(other)
	me.x.onRead = func() {
		me.x.mu.Lock()
		defer me.x.mu.Unlock()
		me.x.onRead = nil
		me.x.access = provider.NewTalosAccess(provider.TalosAccessVersion{Path: "access/talos/" + ae.cluster, Version: 3,
			CreatedTime: time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)}, []byte("tc3"))
	}
	if b := decode[scopeMarkBody](t, me.mark(ctl, m2, "rotated-identity"), http.StatusCreated); b.State != "blocked" ||
		len(b.Missing) != 2 || b.Missing[0] != "observation" || b.Missing[1] != "identity" {
		t.Fatalf("a mark on an observation that is not evidence and read another identity was marked %s missing %v", b.State, b.Missing)
	}
	me.x.mu.Lock()
	me.x.access = v2
	me.x.mu.Unlock()
	me.report(me.recordedIdentity(t, m2))

	// An operation changes state after the observation's basis (here a resolution recorded while the
	// dependencies are checked): §6.1 excludes the observation from drift, so it is not the mark's
	// evidence either, and the scope stays blocked on it.
	var changed sync.Once
	me.x.mu.Lock()
	me.x.onDecrypt = func() {
		changed.Do(func() {
			mustExec(t, ae.db, `WITH r AS (UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1
				RETURNING revision_counter)
				INSERT INTO machine_event (machine, revision, epoch, kind, entry, at) SELECT $1, r.revision_counter, $2, 'operation-state',
				'{"operation": "op_aaaaaaaaaaaaaaaaaaaaaaaaaa", "from": "unresolved", "to": "cancelled"}', clock_timestamp() FROM r`,
				m2, epoch(t, ae.db))
		})
	}
	me.x.mu.Unlock()
	if b := decode[scopeMarkBody](t, me.mark(ctl, m2, "changed"), http.StatusCreated); b.State != "blocked" ||
		len(b.Missing) != 1 || b.Missing[0] != "observation" {
		t.Fatalf("a mark on an observation an operation change follows was marked %s missing %v", b.State, b.Missing)
	}
	me.x.mu.Lock()
	me.x.onDecrypt = nil
	me.x.mu.Unlock()
	// Two marks overlap: while the first checks its dependencies, the second observes another identity
	// and marks the scope blocked. The first's own observation matched, but a newer one contradicts its
	// identity, so it does not mark the scope ready over the second's evidence.
	var overlapped *scopeMarkBody
	me.x.mu.Lock()
	me.x.onDecrypt = func() {
		me.x.mu.Lock()
		me.x.onDecrypt = nil
		me.x.mu.Unlock()
		me.report(other)
		nb := decode[scopeMarkBody](t, me.mark(ctl, m2, "overlapping"), http.StatusCreated)
		overlapped = &nb
		me.report(me.recordedIdentity(t, m2))
	}
	me.x.mu.Unlock()
	if b := decode[scopeMarkBody](t, me.mark(ctl, m2, "overlapped"), http.StatusCreated); overlapped == nil ||
		overlapped.State != "blocked" || b.State != "blocked" || len(b.Missing) != 1 || b.Missing[0] != "identity" {
		t.Fatalf("a mark a newer observation of another identity contradicts was marked %s missing %v (the newer: %+v)",
			b.State, b.Missing, overlapped)
	}

	// A restored operation holds the attempt's scope: it is marked unresolved whatever the evidence.
	me.report(me.recordedIdentity(t, ae.machine))
	if b := decode[scopeMarkBody](t, me.mark(ctl, ae.machine, "held"), http.StatusCreated); b.State != "unresolved" {
		t.Fatalf("the held scope was marked %s", b.State)
	}
	// §7.3 step 6: a restoration observation's digest that the restored Applied does not explain is
	// drift, recorded by the observation's own transaction.
	f := observeFor{purpose: "restoration"}
	acc := me.x.access.Version()
	o := observed{ID: id.New(id.Observation), Access: &acc, Digest: &[32]byte{9}, ResourceVersion: "rv9",
		Unread: map[string]unreadValue{"health": {Cause: "none-bound"}, "identity": {Cause: "identity-read"},
			"runningVersion": {Cause: "version-read"}, "assignmentEvidence": {Cause: "unassigned"}}}
	if err := ctl.inTx(t.Context(), func(tx *sql.Tx) error { _, _, err := ctl.startObservation(t.Context(), tx, m2, f, &o); return err }); err != nil {
		t.Fatal(err)
	}
	if err := ctl.recordObservation(t.Context(), m2, f, &o); err != nil {
		t.Fatal(err)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM drift_record WHERE machine = $1 AND closed IS NULL AND observation = $2`, m2, o.ID); n != 1 {
		t.Fatal("a restoration observation of a digest Applied does not explain opened no drift record")
	}
	// A scope the current epoch has no accounting of is blocked on its observation, the rest of the
	// evidence being present.
	me.report(me.recordedIdentity(t, m2))
	mustExec(t, ae.db, `ALTER TABLE scope_accounting DISABLE TRIGGER immutable_rows`)
	mustExec(t, ae.db, `DELETE FROM scope_accounting WHERE machine = $1`, m2)
	mustExec(t, ae.db, `ALTER TABLE scope_accounting ENABLE TRIGGER immutable_rows`)
	if b := decode[scopeMarkBody](t, me.mark(ctl, m2, "no-accounting"), http.StatusCreated); b.State != "blocked" ||
		len(b.Missing) != 1 || b.Missing[0] != "observation" {
		t.Fatalf("a scope with no accounting in the epoch was marked %s missing %v", b.State, b.Missing)
	}
	mustExec(t, ae.db, `UPDATE machine SET scope_state = 'released' WHERE id = $1`, m2)
	before := me.restorations(t, m2)
	wantProblem(t, me.mark(ctl, m2, "released"), http.StatusConflict, "conflict")
	if me.restorations(t, m2) != before {
		t.Fatal("a refused mark of a released scope observed the machine")
	}
	// A recovery entry commits after the mark's observation: the controller of the earlier epoch
	// marks nothing in the new one (persistence-api.md §5.1).
	mustExec(t, ae.db, `UPDATE machine SET scope_state = 'blocked' WHERE id = $1`, m2)
	var rev int64
	if err := ae.db.QueryRow(`SELECT revision_counter FROM machine WHERE id = $1`, m2).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	var entered sync.Once
	me.x.mu.Lock()
	me.x.onDecrypt = func() { entered.Do(func() { newEpoch(t, ae.db) }) }
	me.x.mu.Unlock()
	wantProblem(t, me.mark(ctl, m2, "superseded"), http.StatusServiceUnavailable, "epoch-superseded")
	me.x.mu.Lock()
	me.x.onDecrypt = nil
	me.x.mu.Unlock()
	if n := count(t, ae.db, `SELECT count(*) FROM machine m WHERE m.id = $1 AND m.scope_state = 'blocked' AND NOT EXISTS (
			SELECT FROM machine_event e WHERE e.machine = m.id AND e.revision > $2 AND e.kind = 'scope-state')`, m2, rev); n != 1 {
		t.Fatal("a controller of a superseded epoch marked the scope")
	}
	// The same entry under a mark expecting the epoch it replaced: the transaction's lookup refuses
	// the mark 412 ahead of the controller check, and the observation taken before it stays
	// (persistence-api.md §7.2, §14).
	ctl = me.controller(t)
	expected := epoch(t, ae.db)
	before = me.restorations(t, m2)
	var ep string
	var again sync.Once
	me.x.mu.Lock()
	me.x.onDecrypt = func() { again.Do(func() { ep = newEpoch(t, ae.db) }) }
	me.x.mu.Unlock()
	rec := me.do(ctl, call{method: "POST", path: prefix + "/recovery/scopes/" + m2 + "/marks", token: me.human("h-recovery"),
		key: "k-mark-expected-" + id.New(id.Request), body: `{"facts":{"note":"after the restore"}}`, expected: expected})
	me.x.mu.Lock()
	me.x.onDecrypt = nil
	me.x.mu.Unlock()
	wantEpochMismatch(t, rec, ep)
	if n := me.restorations(t, m2); n != before+1 {
		t.Fatalf("%d restoration observations after the refused mark; want %d, its own kept", n, before+1)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM machine m WHERE m.id = $1 AND m.scope_state = 'blocked' AND NOT EXISTS (
			SELECT FROM machine_event e WHERE e.machine = m.id AND e.revision > $2 AND e.kind = 'scope-state')`, m2, rev); n != 1 {
		t.Fatal("a mark refused 412 marked the scope")
	}
}
