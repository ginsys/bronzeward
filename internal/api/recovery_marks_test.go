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
	r := ae.enterStopped(t, time.Now().Add(-time.Minute))
	ctl := me.controller(t)
	wantProblem(t, me.mark(ctl, m2, "unaccounted"), http.StatusConflict, "recovery-mode-active")
	wantProblem(t, me.mark(ctl, "mch_aaaaaaaaaaaaaaaaaaaaaaaaaa", "unknown"), http.StatusNotFound, "not-found")
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
	mustExec(t, ae.db, `UPDATE machine SET scope_state = 'released' WHERE id = $1`, m2)
	before := me.restorations(t, m2)
	wantProblem(t, me.mark(ctl, m2, "released"), http.StatusConflict, "conflict")
	if me.restorations(t, m2) != before {
		t.Fatal("a refused mark of a released scope observed the machine")
	}
}
