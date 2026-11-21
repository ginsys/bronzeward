package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
)

const uuidName2 = "1c6b7d2f-3a4e-4f60-9bac-1d2e3f4a5b6c"

// namedMachineCall inventories a machine in cluster with the SMBIOS UUID uuid and the given name
// member, which is the JSON text of the member's value (`"web-1"`, `null`).
func namedMachineCall(tok, k, cluster, uuid, name string) call {
	return call{method: "POST", path: prefix + "/machines", token: tok, key: k,
		body: `{"cluster":"` + cluster + `","smbiosUuid":"` + uuid + `","serial":"SN-1","platform":"container","talosEndpoint":"10.55.0.3","name":` + name + `}`}
}

func renameCall(tok, k, machine, body string) call {
	return call{method: "POST", path: prefix + "/machines/" + machine + "/names", token: tok, key: k, body: body}
}

// nameMachine inventories a machine in cluster under uuid, named name ("" for none), and returns its id.
func (e *env) nameMachine(author, k, cluster, uuid, name string) string {
	e.t.Helper()
	c := machineCall(author, k, cluster, uuid)
	if name != "" {
		c = namedMachineCall(author, k, cluster, uuid, `"`+name+`"`)
	}
	return decode[machineBody](e.t, e.do(e.api, c), http.StatusCreated).ID
}

// nameRows counts what a rename writes for machine: name-change entries, the acts, the counter.
func nameRows(t *testing.T, e *env, machine string) (events, acts, counter int) {
	t.Helper()
	return count(t, e.db, "SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'name-change'", machine),
		count(t, e.db, "SELECT count(*) FROM act WHERE action = 'machine.name' AND subjects = ARRAY[$1]", machine),
		count(t, e.db, "SELECT revision_counter FROM machine WHERE id = $1", machine)
}

func storedName(t *testing.T, e *env, machine string) *string {
	t.Helper()
	var n *string
	if err := e.db.QueryRow("SELECT name FROM machine WHERE id = $1", machine).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// PA §3.5: a machine is inventoried with or without a name, and every read serves it, null when
// there is none.
func TestMachineNameAtInventory(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	named := decode[machineBody](t, e.do(e.api, namedMachineCall(author, "k-named-0123456789", cl, uuidA, `"web-1"`)), http.StatusCreated)
	if named.Name == nil || *named.Name != "web-1" {
		t.Fatalf("inventory answered name %v; want web-1", named.Name)
	}
	bare := decode[machineBody](t, e.do(e.api, machineCall(author, "k-bare-01234567890", cl, uuidName2)), http.StatusCreated)
	if bare.Name != nil {
		t.Fatalf("a machine inventoried with no name serves %q", *bare.Name)
	}
	viewer := e.human("h-viewer")
	got := decode[machineBody](t, e.do(e.api, call{method: "GET", path: prefix + "/machines/" + named.ID, token: viewer}), http.StatusOK)
	if got.Name == nil || *got.Name != "web-1" {
		t.Fatalf("GET /machines/{id}: name %v", got.Name)
	}
	list := decode[listPage[machineBody]](t, e.do(e.api, call{method: "GET", path: prefix + "/machines?cluster=" + cl, token: viewer}), http.StatusOK)
	names := map[string]*string{}
	for _, m := range list.Items {
		names[m.ID] = m.Name
	}
	if n := names[named.ID]; n == nil || *n != "web-1" || names[bare.ID] != nil || len(names) != 2 {
		t.Fatalf("GET /machines names %v", names)
	}
	// Inventory records no name-change entry and no machine.name act: the name is set with the machine.
	if ev, acts, ctr := nameRows(t, e, named.ID); ev != 0 || acts != 0 || ctr != 0 {
		t.Fatalf("after inventory: entries %d, acts %d, counter %d", ev, acts, ctr)
	}
}

// PA §3.5: a hostname of dot-separated labels (shapes in nameCases). Anything else is 400
// invalid-request without the value, on inventory and on the route, and nothing is committed.
// Uppercase is refused, never folded.
func TestMachineNameGrammar(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	m := e.nameMachine(author, "k-machine-0123456789", cl, uuidA, "")
	const marker = "zq-marker-7f3a"
	bad := map[string]string{
		"uppercase":          `"Web-1"`,
		"only uppercase":     `"WEB"`,
		"underscore":         `"web_1"`,
		"leading hyphen":     `"-web"`,
		"trailing hyphen":    `"web-"`,
		"a trailing dot":     `"web.example."`,
		"an address":         `"10.0.0.1"`,
		"254 characters":     `"` + strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 62) + `"`,
		"a space":            `"web 1"`,
		"empty":              `""`,
		"64 characters":      `"` + strings.Repeat("a", 64) + `"`,
		"a control":          `"web\u0007"`,
		"a non-ASCII letter": `"wéb"`,
		"an identifier":      `"mch_aaaaaaaaaaaaaaaaaaaaaaaaaa"`,
		"a number":           `7`,
		"a marker value":     `"` + marker + `_"`,
	}
	for name, v := range bad {
		for route, c := range map[string]call{
			"inventory": namedMachineCall(author, "k-bad-"+strings.ReplaceAll(name, " ", "-")+"-i-0123", cl, uuidName2, v),
			"rename":    renameCall(author, "k-bad-"+strings.ReplaceAll(name, " ", "-")+"-r-0123", m, `{"name":`+v+`}`),
		} {
			rec := e.do(e.api, c)
			wantProblem(t, rec, http.StatusBadRequest, "invalid-request")
			if strings.Contains(rec.Body.String(), marker) {
				t.Errorf("%s on %s: the problem quotes the value: %s", name, route, rec.Body)
			}
		}
	}
	// No name, a null name and an unknown member of the route are refused too: it cannot clear a name.
	for name, body := range map[string]string{"no member": `{}`, "null": `{"name":null}`, "unknown member": `{"name":"web-1","x":1}`} {
		wantProblem(t, e.do(e.api, renameCall(author, "k-body-"+strings.ReplaceAll(name, " ", "-")+"-0123", m, body)), http.StatusBadRequest, "invalid-request")
	}
	if n := count(t, e.db, "SELECT count(*) FROM machine"); n != 1 {
		t.Fatalf("%d machines after refused inventories; want 1", n)
	}
	if ev, acts, ctr := nameRows(t, e, m); ev != 0 || acts != 0 || ctr != 0 || storedName(t, e, m) != nil {
		t.Fatalf("after refusals: entries %d, acts %d, counter %d, name %v", ev, acts, ctr, storedName(t, e, m))
	}
	// The longest and the shortest legal names pass the same checks.
	long := strings.Repeat("a", 63)
	longest := strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 61)
	for i, n := range []string{"a", "0", "42", "a-b", long, "alcorak.office.ginsys.net", longest} {
		rec := e.do(e.api, renameCall(author, "k-good-"+string(rune('a'+i))+"-0123456789", m, `{"name":"`+n+`"}`))
		if b := decode[nameChangeBody](t, rec, http.StatusCreated); b.Name != n {
			t.Fatalf("name %q answered %+v", n, b)
		}
	}
}

// PA §3.5: the CHECK on machine.name refuses and accepts exactly what the API check does, over every
// shape of nameCases, so no route can store a name the API would refuse.
func TestMachineNameCheckMatchesAPI(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	m := e.nameMachine(author, "k-machine-0123456789", cl, uuidA, "")
	for _, c := range nameCases {
		t.Run(c.name, func(t *testing.T) {
			_, err := e.db.Exec("UPDATE machine SET name = $2 WHERE id = $1", m, c.value)
			switch {
			case c.ok && err != nil:
				t.Errorf("the CHECK refuses %q: %v", c.value, err)
			case !c.ok && err == nil:
				t.Errorf("the CHECK accepts %q", c.value)
			}
		})
	}
}

// PA §3.5: a name another machine of the cluster holds is 409 name-in-use naming that machine, on
// inventory and on the route; the same name in another cluster is fine; and the identity key's
// conflict is answered first.
func TestMachineNameInUse(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	other := e.createCluster(e.api, author, "k-cluster-second-0123")
	holder := e.nameMachine(author, "k-holder-0123456789", cl, uuidA, "web-1")
	second := e.nameMachine(author, "k-second-012345678", cl, uuidName2, "")

	rec := e.do(e.api, namedMachineCall(author, "k-clash-inventory-0123", cl, "2d7c8e30-4b5f-4071-8cbd-2e3f4a5b6c7d", `"web-1"`))
	if doc := wantProblem(t, rec, http.StatusConflict, "name-in-use"); doc["machine"] != holder {
		t.Fatalf("inventory clash: %v; want machine %s", doc, holder)
	}
	if n := count(t, e.db, "SELECT count(*) FROM machine"); n != 2 {
		t.Fatalf("%d machines after the refused inventory; want 2", n)
	}

	rec = e.do(e.api, renameCall(author, "k-clash-rename-0123456", second, `{"name":"web-1"}`))
	if doc := wantProblem(t, rec, http.StatusConflict, "name-in-use"); doc["machine"] != holder {
		t.Fatalf("rename clash: %v; want machine %s", doc, holder)
	}
	if ev, acts, ctr := nameRows(t, e, second); ev != 0 || acts != 0 || ctr != 0 || storedName(t, e, second) != nil {
		t.Fatalf("after the refused rename: entries %d, acts %d, counter %d", ev, acts, ctr)
	}

	// A machine whose identity key is recorded and whose name is taken: conflict, naming the machine
	// that holds the identity key, not the name's holder.
	rec = e.do(e.api, namedMachineCall(author, "k-both-0123456789012", cl, uuidName2, `"web-1"`))
	if doc := wantProblem(t, rec, http.StatusConflict, "conflict"); doc["machine"] != second {
		t.Fatalf("identity key and name both clash: %v; want conflict naming %s", doc, second)
	}

	// Another cluster may reuse the name, by either route.
	inOther := decode[machineBody](t, e.do(e.api, namedMachineCall(author, "k-other-0123456789", other, "3e8d9f41-5c60-4182-9dce-3f4a5b6c7d8e", `"web-1"`)),
		http.StatusCreated)
	if inOther.Name == nil || *inOther.Name != "web-1" {
		t.Fatalf("second cluster inventory: name %v", inOther.Name)
	}
	spare := e.nameMachine(author, "k-spare-0123456789", other, "4f9ea052-6d71-4293-8edf-4a5b6c7d8e9f", "")
	decode[nameChangeBody](t, e.do(e.api, renameCall(author, "k-other-rename-01234", spare, `{"name":"web-2"}`)), http.StatusCreated)

	// The freed name is usable once its holder renames away.
	decode[nameChangeBody](t, e.do(e.api, renameCall(author, "k-free-0123456789012", holder, `{"name":"web-9"}`)), http.StatusCreated)
	decode[nameChangeBody](t, e.do(e.api, renameCall(author, "k-reuse-012345678901", second, `{"name":"web-1"}`)), http.StatusCreated)
}

// PA §3.5: the rename route records the name, a name-change entry at the next machine revision
// holding the previous name or none, the new name, who and role, and its act (machine.name, the
// machine as subject), in one transaction. A replay under the same key writes nothing more; the
// machine's own current name under a new key is 409 conflict naming the machine itself.
func TestMachineNameRename(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	m := e.nameMachine(author, "k-machine-0123456789", cl, uuidA, "")
	rec := e.do(e.api, renameCall(author, "k-rename-1-0123456789", m, `{"name":"web-1"}`))
	b := decode[nameChangeBody](t, rec, http.StatusCreated)
	if b.Machine != m || b.Revision != 1 || b.PreviousName != nil || b.Name != "web-1" || b.Role != "author" ||
		id.MustHave(b.ChangedBy, id.Principal) != nil || id.MustHave(b.Act, id.Act) != nil || b.Epoch != epoch(t, e.db) || b.At.IsZero() ||
		rec.Header().Get("Location") != prefix+"/machines/"+m {
		t.Fatalf("change %+v, Location %q", b, rec.Header().Get("Location"))
	}
	got := decode[machineBody](t, e.do(e.api, call{method: "GET", path: prefix + "/machines/" + m, token: e.human("h-viewer")}), http.StatusOK)
	if got.Name == nil || *got.Name != "web-1" {
		t.Fatalf("GET after the rename: name %v", got.Name)
	}
	if n := count(t, e.db, `SELECT count(*) FROM machine_event v JOIN act a ON a.id = $3
		WHERE v.machine = $1 AND v.revision = 1 AND v.kind = 'name-change' AND v.epoch = $2
		AND v.entry = jsonb_build_object('previous', NULL::text, 'new', 'web-1', 'principal', $4::text, 'role', 'author')
		AND a.principal = $4 AND a.role = 'author' AND a.action = 'machine.name' AND a.subjects = ARRAY[$1]`, m, b.Epoch, b.Act, b.ChangedBy); n != 1 {
		t.Fatalf("%d matching entry and act", n)
	}
	// The timeline read serves the entry as a kind of its own.
	tl := decode[timelinePage](t, e.do(e.api, call{method: "GET", path: prefix + "/machines/" + m + "/timeline", token: e.human("h-viewer")}), http.StatusOK)
	if len(tl.Items) != 1 || tl.Items[0].Kind != "name-change" || tl.Items[0].Revision != 1 {
		t.Fatalf("timeline %+v", tl.Items)
	}
	// A replay answers the same change and writes nothing more.
	if r := decode[nameChangeBody](t, e.do(e.api, renameCall(author, "k-rename-1-0123456789", m, `{"name":"web-1"}`)), http.StatusCreated); r != b {
		t.Fatalf("replay %+v; want %+v", r, b)
	}
	if ev, acts, ctr := nameRows(t, e, m); ev != 1 || acts != 1 || ctr != 1 {
		t.Fatalf("after the replay: entries %d, acts %d, counter %d", ev, acts, ctr)
	}
	// The machine's own current name, under a new key: nothing changed, so nothing is recorded.
	doc := wantProblem(t, e.do(e.api, renameCall(author, "k-rename-same-012345", m, `{"name":"web-1"}`)), http.StatusConflict, "conflict")
	if doc["machine"] != m {
		t.Fatalf("own name: %v; want machine %s", doc, m)
	}
	if ev, acts, ctr := nameRows(t, e, m); ev != 1 || acts != 1 || ctr != 1 {
		t.Fatalf("after the same-name refusal: entries %d, acts %d, counter %d", ev, acts, ctr)
	}
	// The next rename takes the next revision and starts from the name just set.
	b2 := decode[nameChangeBody](t, e.do(e.api, renameCall(author, "k-rename-2-0123456789", m, `{"name":"web-2"}`)), http.StatusCreated)
	if b2.Revision != 2 || b2.PreviousName == nil || *b2.PreviousName != "web-1" || b2.Name != "web-2" {
		t.Fatalf("second change %+v", b2)
	}
	// An unknown machine and a non-machine identifier are 404.
	for _, target := range []string{id.New(id.Machine), id.New(id.Cluster)} {
		wantProblem(t, e.do(e.api, renameCall(author, "k-missing-0123456789", target, `{"name":"web-1"}`)), http.StatusNotFound, "not-found")
	}
}

// PA §7.2: a replay from an earlier epoch is 409 conflict, not the stored answer.
func TestMachineNameReplayFromEarlierEpoch(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	m := e.nameMachine(author, "k-machine-0123456789", cl, uuidA, "")
	decode[nameChangeBody](t, e.do(e.api, renameCall(author, "k-rename-epoch-01234", m, `{"name":"web-1"}`)), http.StatusCreated)
	newEpoch(t, e.db)
	wantProblem(t, e.do(e.api, renameCall(author, "k-rename-epoch-01234", m, `{"name":"web-1"}`)), http.StatusConflict, "conflict")
	if ev, acts, ctr := nameRows(t, e, m); ev != 1 || acts != 1 || ctr != 1 {
		t.Fatalf("after the replay: entries %d, acts %d, counter %d", ev, acts, ctr)
	}
}

// PA §9.2, §10.3: the route is author, human only. Nothing is committed on a refusal.
func TestMachineNameRoles(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	m := e.nameMachine(author, "k-machine-0123456789", cl, uuidA, "")
	for name, tok := range map[string]string{"viewer": e.human("h-viewer"), "publisher": e.human("h-publisher"), "approver": e.human("h-approver"),
		"recovery-admin": e.human("h-recovery"), "automation": e.robot} {
		doc := wantProblem(t, e.do(e.api, renameCall(tok, "k-role-"+name+"-0123456", m, `{"name":"web-1"}`)), http.StatusForbidden, "forbidden")
		if name == "automation" && doc["humanOnly"] != true {
			t.Errorf("automation: %v; want humanOnly", doc)
		}
	}
	// Inventory with a name keeps its own roles: a viewer cannot name a machine by inventory either.
	wantProblem(t, e.do(e.api, namedMachineCall(e.human("h-viewer"), "k-role-inventory-0123", cl, uuidName2, `"web-1"`)), http.StatusForbidden, "forbidden")
	if ev, acts, ctr := nameRows(t, e, m); ev != 0 || acts != 0 || ctr != 0 || storedName(t, e, m) != nil {
		t.Fatalf("after refusals: entries %d, acts %d, counter %d", ev, acts, ctr)
	}
}

// PA §12.2: the route is accepted installation-wide in recovery mode, and the entry carries the
// recovery epoch.
func TestMachineNameInRecoveryMode(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	m := e.nameMachine(author, "k-machine-0123456789", cl, uuidA, "")
	ep := newEpoch(t, e.db)
	mustExec(t, e.db, "UPDATE installation_state SET recovery_mode = true")
	// Recovery entry leaves each machine's scope unaccounted (§12.2); a rename is not a scope act, so
	// it is accepted on a machine whose scope is closed too.
	mustExec(t, e.db, "UPDATE machine SET scope_state = 'pre-restore-unaccounted' WHERE id = $1", m)
	b := decode[nameChangeBody](t, e.do(e.api, renameCall(author, "k-recovery-0123456789", m, `{"name":"web-1"}`)), http.StatusCreated)
	if b.Epoch != ep || count(t, e.db, "SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'name-change' AND epoch = $2", m, ep) != 1 {
		t.Fatalf("change %+v; want epoch %s on the change and its entry", b, ep)
	}
	if n := storedName(t, e, m); n == nil || *n != "web-1" {
		t.Fatalf("stored name %v; want web-1 on the machine with a closed scope", n)
	}
}

// PA §3.5, §5: the name, its entry and its act commit together or not at all (so a backup restores
// all three or none: the restore row of §12.3 holds for a value that lives only in ordinary rows
// written in one transaction). Every attempt fails before COMMIT: nothing is written. The control
// lets the commit through.
func TestMachineNameAtomic(t *testing.T) {
	for _, failing := range []bool{true, false} {
		var fail bool
		e := newEnv(t, options{beforeCommit: func(n int) error {
			if fail {
				return deadlock(maxAttempts)(n)
			}
			return nil
		}})
		author := e.human("h-author")
		cl := e.createCluster(e.api, author, "k-cluster-0123456789")
		m := e.nameMachine(author, "k-machine-0123456789", cl, uuidA, "")
		fail = failing
		rec := e.do(e.api, renameCall(author, "k-atomic-01234567890", m, `{"name":"web-1"}`))
		ev, acts, ctr := nameRows(t, e, m)
		name := storedName(t, e, m)
		switch {
		case failing && (rec.Code != http.StatusServiceUnavailable || ev+acts+ctr != 0 || name != nil):
			t.Errorf("failed commit: %d, entries %d, acts %d, counter %d, name %v; want nothing", rec.Code, ev, acts, ctr, name)
		case !failing && (rec.Code != http.StatusCreated || ev != 1 || acts != 1 || ctr != 1 || name == nil || *name != "web-1"):
			t.Errorf("control: %d, entries %d, acts %d, counter %d, name %v; want all written", rec.Code, ev, acts, ctr, name)
		}
	}
}

// PA §3.5 (T11): two machines renamed to one name at once. The first is held open after its
// effect, so its name is in the index uncommitted; the second waits for it and, once the first
// commits, is refused name-in-use naming the first: never a 500, and one holder.
func TestMachineNameConcurrent(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	a := e.nameMachine(author, "k-machine-a-0123456", cl, uuidA, "")
	b := e.nameMachine(author, "k-machine-b-0123456", cl, uuidName2, "")
	hook, held, release := holdFirst()
	t.Cleanup(release)
	api := e.build(options{afterEffect: hook})
	first, second := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- e.do(api, renameCall(author, "k-first-0123456789", a, `{"name":"web-1"}`)) }()
	select {
	case <-held:
	case rec := <-first:
		t.Fatalf("the first rename answered %d %s without reaching its effect", rec.Code, rec.Body)
	}
	go func() { second <- e.do(api, renameCall(author, "k-second-012345678", b, `{"name":"web-1"}`)) }()
	dbtest.WaitForLockWait(t, e.db)
	release()
	decode[nameChangeBody](t, <-first, http.StatusCreated)
	rec := <-second
	if doc := wantProblem(t, rec, http.StatusConflict, "name-in-use"); doc["machine"] != a {
		t.Fatalf("second: %v; want name-in-use naming %s", doc, a)
	}
	if n := count(t, e.db, "SELECT count(*) FROM machine WHERE name = 'web-1'"); n != 1 {
		t.Fatalf("%d machines hold the name", n)
	}
	if ev, acts, ctr := nameRows(t, e, b); ev != 0 || acts != 0 || ctr != 0 {
		t.Fatalf("the refused rename left entries %d, acts %d, counter %d", ev, acts, ctr)
	}
}

// PA §12.3, §12.4: a restore to a backup taken after the first rename and before the second removes
// the second's name, entry, act and idempotency record and starts a new epoch. The machine reads the
// name as at the backup; the lost name can be taken again; a repeat whose record survived answers
// the earlier epoch's 409, and one whose record the restore removed is refused 412 when it names the
// epoch it expects.
func TestMachineNameAfterRestore(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	m := e.nameMachine(author, "k-machine-0123456789", cl, uuidA, "")
	before := epoch(t, e.db)
	first := renameCall(author, "k-rename-first-01234", m, `{"name":"web-1"}`)
	lost := renameCall(author, "k-rename-lost-012345", m, `{"name":"web-2"}`)
	lost.expected = before
	decode[nameChangeBody](t, e.do(e.api, first), http.StatusCreated)
	lostChange := decode[nameChangeBody](t, e.do(e.api, lost), http.StatusCreated)

	if n := storedName(t, e, m); n == nil || *n != "web-2" {
		t.Fatalf("before the restore: stored name %v; want web-2", n)
	}
	// The restore: the backup holds the first name only.
	for _, table := range []string{"machine_event", "act", "idempotency_record"} {
		mustExec(t, e.db, "ALTER TABLE "+table+" DISABLE TRIGGER immutable_rows")
		defer mustExec(t, e.db, "ALTER TABLE "+table+" ENABLE TRIGGER immutable_rows")
	}
	mustExec(t, e.db, "DELETE FROM machine_event WHERE machine = $1 AND revision = 2", m)
	mustExec(t, e.db, "DELETE FROM act WHERE id = $1", lostChange.Act)
	mustExec(t, e.db, "DELETE FROM idempotency_record WHERE key = $1", "k-rename-lost-012345")
	mustExec(t, e.db, "UPDATE machine SET name = 'web-1', revision_counter = 1 WHERE id = $1", m)
	after := newEpoch(t, e.db)

	author = e.human("h-author")
	got := decode[machineBody](t, e.do(e.api, call{method: "GET", path: prefix + "/machines/" + m, token: e.human("h-viewer")}), http.StatusOK)
	if got.Name == nil || *got.Name != "web-1" {
		t.Fatalf("after the restore: name %v; want web-1", got.Name)
	}
	if n := storedName(t, e, m); n == nil || *n != "web-1" {
		t.Fatalf("after the restore: stored name %v; want web-1", n)
	}
	// The repeat whose record survived still answers the earlier epoch's 409.
	wantProblem(t, e.do(e.api, first), http.StatusConflict, "conflict")
	// The repeat whose record the restore removed is refused, because it names the epoch it expects.
	wantEpochMismatch(t, e.do(e.api, lost), after)
	if ev, acts, ctr := nameRows(t, e, m); ev != 1 || acts != 1 || ctr != 1 {
		t.Fatalf("after the refused repeats: entries %d, acts %d, counter %d", ev, acts, ctr)
	}
	// The lost name can be taken again, at the revision the restored machine reaches next.
	b := decode[nameChangeBody](t, e.do(e.api, renameCall(author, "k-rename-again-0123", m, `{"name":"web-2"}`)), http.StatusCreated)
	if b.Revision != 2 || b.PreviousName == nil || *b.PreviousName != "web-1" || b.Name != "web-2" || b.Epoch != after {
		t.Fatalf("after the restore: %+v", b)
	}
	if n := storedName(t, e, m); n == nil || *n != "web-2" {
		t.Fatalf("after the second rename: stored name %v; want web-2", n)
	}
}
