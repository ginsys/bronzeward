package api

import (
	"net/http"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

func scopeAct(tok, k, act, machine, body string) call {
	return call{method: "POST", path: prefix + "/machines/" + machine + "/" + act, token: tok, key: k, body: body}
}

// freezeRows counts what freezes and unfreezes wrote for machine: timeline entries, freeze facts,
// acts, and the machine's revision counter.
func freezeRows(t *testing.T, e *env, machine string) (events, facts, acts, counter int) {
	t.Helper()
	return count(t, e.db, "SELECT count(*) FROM machine_event WHERE machine = $1 AND kind IN ('freeze', 'unfreeze')", machine),
		count(t, e.db, "SELECT count(*) FROM machine_freeze WHERE machine = $1", machine),
		count(t, e.db, "SELECT count(*) FROM act WHERE action IN ('machine.freeze', 'machine.unfreeze')"),
		count(t, e.db, "SELECT revision_counter FROM machine WHERE id = $1", machine)
}

func frozen(t *testing.T, e *env, machine string) bool {
	t.Helper()
	var f bool
	if err := e.db.QueryRow("SELECT frozen FROM machine WHERE id = $1", machine).Scan(&f); err != nil {
		t.Fatal(err)
	}
	return f
}

// ER §6.2, PA §3: a freeze is a durable fact on the machine scope, with who, role, reason, epoch
// and act, one freeze entry at the next machine revision (T7), and the machine reads frozen until
// an approver unfreezes it the same way. A replay answers the same fact and writes nothing more.
func TestFreezeAndUnfreeze(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	m := e.endpointMachine(author)
	rec := e.do(e.api, scopeAct(author, "k-freeze-1-0123456789", "freezes", m, `{"reason":"investigating drift"}`))
	b := decode[freezeBody](t, rec, http.StatusCreated)
	if b.Machine != m || b.Revision != 1 || !b.Frozen || b.Role != "author" || b.Reason != "investigating drift" ||
		id.MustHave(b.Principal, id.Principal) != nil || id.MustHave(b.Act, id.Act) != nil || b.Epoch != epoch(t, e.db) ||
		b.At.IsZero() || rec.Header().Get("Location") != prefix+"/machines/"+m {
		t.Fatalf("freeze %+v, Location %q", b, rec.Header().Get("Location"))
	}
	if !frozen(t, e, m) {
		t.Fatal("the machine does not read frozen")
	}
	if got := decode[machineBody](t, e.do(e.api, call{method: "GET", path: prefix + "/machines/" + m, token: e.human("h-viewer")}),
		http.StatusOK); !got.Frozen {
		t.Fatal("GET after the freeze: not frozen")
	}
	if n := count(t, e.db, `SELECT count(*) FROM machine_event v JOIN machine_freeze f USING (machine, revision) JOIN act a ON a.id = f.act
		WHERE v.machine = $1 AND v.revision = 1 AND v.kind = 'freeze' AND v.epoch = $2
		AND v.entry = jsonb_build_object('principal', $3::text, 'role', 'author', 'reason', 'investigating drift')
		AND f.frozen AND f.principal = $3 AND f.role = 'author' AND f.reason = 'investigating drift' AND f.epoch = $2
		AND a.action = 'machine.freeze' AND a.principal = $3 AND a.role = 'author' AND a.subjects = ARRAY[$1]`,
		m, b.Epoch, b.Principal); n != 1 {
		t.Fatalf("%d matching entry, fact and act", n)
	}
	if r := decode[freezeBody](t, e.do(e.api, scopeAct(author, "k-freeze-1-0123456789", "freezes", m, `{"reason":"investigating drift"}`)),
		http.StatusCreated); r != b {
		t.Fatalf("replay %+v; want %+v", r, b)
	}
	if ev, f, acts, ctr := freezeRows(t, e, m); ev != 1 || f != 1 || acts != 1 || ctr != 1 {
		t.Fatalf("after the replay: events %d, facts %d, acts %d, counter %d", ev, f, acts, ctr)
	}

	u := decode[freezeBody](t, e.do(e.api, scopeAct(e.human("h-approver"), "k-unfreeze-1-0123456789", "unfreezes", m,
		`{"reason":"drift resolved"}`)), http.StatusCreated)
	if u.Revision != 2 || u.Frozen || u.Role != "approver" || frozen(t, e, m) {
		t.Fatalf("unfreeze %+v, frozen %v", u, frozen(t, e, m))
	}
	if n := count(t, e.db, `SELECT count(*) FROM machine_event v JOIN machine_freeze f USING (machine, revision)
		WHERE v.machine = $1 AND v.revision = 2 AND v.kind = 'unfreeze' AND NOT f.frozen AND f.role = 'approver'`, m); n != 1 {
		t.Fatalf("%d matching unfreeze entry and fact", n)
	}
}

// ER §6.2, §13.7 item 6: any author, publisher, approver or recovery admin freezes, automation
// holding one of those roles included; only an approver unfreezes.
func TestFreezeRoles(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	m := e.endpointMachine(author)
	approver := e.human("h-approver")
	for i, tok := range []string{author, e.human("h-publisher"), approver, e.human("h-recovery"), e.robot} {
		k := "k-freeze-role-" + string(rune('a'+i)) + "-0123456789"
		if rec := e.do(e.api, scopeAct(tok, k, "freezes", m, `{"reason":"hold"}`)); rec.Code != http.StatusCreated {
			t.Fatalf("freeze %d: %d %s", i, rec.Code, rec.Body)
		}
		if rec := e.do(e.api, scopeAct(approver, k+"-u", "unfreezes", m, `{"reason":"release"}`)); rec.Code != http.StatusCreated {
			t.Fatalf("unfreeze after freeze %d: %d %s", i, rec.Code, rec.Body)
		}
	}
}

// §9.1, ER §6.2, §7.5: a blank or missing reason is 400, an unknown machine 404, freezing a frozen
// scope or unfreezing one that is not frozen 409 conflict, and an unfreeze on a scope still
// pre-restore unaccounted 409 recovery-mode-active, while a freeze there is accepted. A refusal
// writes nothing.
func TestFreezeRefusals(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	approver := e.human("h-approver")
	m := e.endpointMachine(author)
	for _, c := range map[string]struct {
		tok, act, machine, body string
		status                  int
		code                    string
	}{
		"no reason":       {author, "freezes", m, `{}`, 400, "invalid-request"},
		"blank reason":    {author, "freezes", m, `{"reason":"  "}`, 400, "invalid-request"},
		"unknown member":  {author, "freezes", m, `{"reason":"x","until":1}`, 400, "invalid-request"},
		"unknown machine": {author, "freezes", id.New(id.Machine), `{"reason":"x"}`, 404, "not-found"},
		"not frozen":      {approver, "unfreezes", m, `{"reason":"x"}`, 409, "conflict"},
	} {
		wantProblem(t, e.do(e.api, scopeAct(c.tok, "k-refused-0123456789", c.act, c.machine, c.body)), c.status, c.code)
	}
	if ev, f, acts, ctr := freezeRows(t, e, m); ev != 0 || f != 0 || acts != 0 || ctr != 0 {
		t.Fatalf("after refusals: events %d, facts %d, acts %d, counter %d", ev, f, acts, ctr)
	}
	mustExec(t, e.db, `UPDATE machine SET scope_state = 'pre-restore-unaccounted' WHERE id = $1`, m)
	if rec := e.do(e.api, scopeAct(author, "k-freeze-0123456789", "freezes", m, `{"reason":"x"}`)); rec.Code != http.StatusCreated {
		t.Fatalf("freeze on a pre-restore scope: %d %s", rec.Code, rec.Body)
	}
	wantProblem(t, e.do(e.api, scopeAct(author, "k-freeze-again-0123456789", "freezes", m, `{"reason":"x"}`)), 409, "conflict")
	wantProblem(t, e.do(e.api, scopeAct(approver, "k-unfreeze-pre-0123456789", "unfreezes", m, `{"reason":"x"}`)), 409,
		"recovery-mode-active")
	if !frozen(t, e, m) {
		t.Fatal("a refused unfreeze lifted the freeze")
	}
	if ev, f, _, ctr := freezeRows(t, e, m); ev != 1 || f != 1 || ctr != 1 {
		t.Fatalf("after the refused unfreeze: events %d, facts %d, counter %d", ev, f, ctr)
	}
}
