package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
)

// endpointMachine inventories a machine at 10.55.0.3:50000 and returns its id.
func (e *env) endpointMachine(author string) string {
	e.t.Helper()
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	return decode[machineBody](e.t, e.do(e.api, machineCall(author, "k-machine-0123456789", cl, uuidA)), http.StatusCreated).ID
}

func replaceEndpoint(tok, k, machine, body string) call {
	return call{method: "POST", path: prefix + "/machines/" + machine + "/talos-endpoints", token: tok, key: k, body: body}
}

// endpointRows counts what a replacement writes for machine: timeline entries, endpoint changes,
// acts, and the machine's revision counter.
func endpointRows(t *testing.T, e *env, machine string) (events, changes, acts, counter int) {
	t.Helper()
	return count(t, e.db, "SELECT count(*) FROM machine_event WHERE machine = $1", machine),
		count(t, e.db, "SELECT count(*) FROM machine_endpoint_change WHERE machine = $1", machine),
		count(t, e.db, "SELECT count(*) FROM act WHERE action = 'machine.talos-endpoint'"),
		count(t, e.db, "SELECT revision_counter FROM machine WHERE id = $1", machine)
}

// PA §3.3: a replacement records the MachineEndpointChange, its endpoint-change entry at the next
// machine revision (T7) holding previous, new, who and role, and its act, and the machine reads
// the new endpoint. A replay answers the same change and writes nothing more.
func TestTalosEndpointReplacement(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	m := e.endpointMachine(author)
	rec := e.do(e.api, replaceEndpoint(author, "k-replace-1-0123456789", m, `{"talosEndpoint":"[FD00::4]"}`))
	b := decode[endpointChangeBody](t, rec, http.StatusCreated)
	if b.Machine != m || b.Revision != 1 || b.PreviousEndpoint != "10.55.0.3:50000" || b.TalosEndpoint != "[fd00::4]:50000" ||
		b.Role != "author" || id.MustHave(b.ChangedBy, id.Principal) != nil || id.MustHave(b.Act, id.Act) != nil ||
		b.Epoch != epoch(t, e.db) || b.At.IsZero() || rec.Header().Get("Location") != prefix+"/machines/"+m {
		t.Fatalf("change %+v, Location %q", b, rec.Header().Get("Location"))
	}
	got := decode[machineBody](t, e.do(e.api, call{method: "GET", path: prefix + "/machines/" + m, token: e.human("h-viewer")}), http.StatusOK)
	if got.TalosEndpoint != "[fd00::4]:50000" {
		t.Fatalf("GET after the replacement: talosEndpoint %q", got.TalosEndpoint)
	}
	if n := count(t, e.db, `SELECT count(*) FROM machine_event v JOIN machine_endpoint_change c USING (machine, revision)
		JOIN act a ON a.id = c.act
		WHERE v.machine = $1 AND v.revision = 1 AND v.kind = 'endpoint-change' AND v.epoch = $2
		AND v.entry = jsonb_build_object('previous', '10.55.0.3:50000', 'new', '[fd00::4]:50000', 'principal', $3::text, 'role', 'author')
		AND c.previous_endpoint = '10.55.0.3:50000' AND c.new_endpoint = '[fd00::4]:50000' AND c.act = $4
		AND a.principal = $3 AND a.role = 'author' AND a.subjects = ARRAY[$1]`, m, b.Epoch, b.ChangedBy, b.Act); n != 1 {
		t.Fatalf("%d matching entry, change and act", n)
	}
	replay := e.do(e.api, replaceEndpoint(author, "k-replace-1-0123456789", m, `{"talosEndpoint":"[FD00::4]"}`))
	if r := decode[endpointChangeBody](t, replay, http.StatusCreated); r != b {
		t.Fatalf("replay %+v; want %+v", r, b)
	}
	if ev, ch, acts, ctr := endpointRows(t, e, m); ev != 1 || ch != 1 || acts != 1 || ctr != 1 {
		t.Fatalf("after the replay: events %d, changes %d, acts %d, counter %d", ev, ch, acts, ctr)
	}
	// The next replacement takes the next revision and starts from the endpoint just set.
	b2 := decode[endpointChangeBody](t, e.do(e.api, replaceEndpoint(author, "k-replace-2-0123456789", m, `{"talosEndpoint":"10.55.0.5:50001"}`)),
		http.StatusCreated)
	if b2.Revision != 2 || b2.PreviousEndpoint != "[fd00::4]:50000" || b2.TalosEndpoint != "10.55.0.5:50001" {
		t.Fatalf("second change %+v", b2)
	}
}

// §9.2, §10.3: the replacement is author, human only. A malformed endpoint is 400 invalid-request
// without the value, an unknown machine 404, the endpoint the machine already has 409; none
// commits anything.
func TestTalosEndpointRefusals(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	m := e.endpointMachine(author)
	const marker = "zq-marker-7f3a"
	for name, c := range map[string]struct {
		tok, machine, body string
		status             int
		code               string
	}{
		"viewer":             {e.human("h-viewer"), m, `{"talosEndpoint":"10.55.0.4"}`, 403, "forbidden"},
		"automation":         {e.robot, m, `{"talosEndpoint":"10.55.0.4"}`, 403, "forbidden"},
		"no endpoint":        {author, m, `{}`, 400, "invalid-request"},
		"DNS endpoint":       {author, m, `{"talosEndpoint":"` + marker + `.test"}`, 400, "invalid-request"},
		"endpoint scheme":    {author, m, `{"talosEndpoint":"https://10.55.0.4"}`, 400, "invalid-request"},
		"endpoint user part": {author, m, `{"talosEndpoint":"` + marker + `@10.55.0.4"}`, 400, "invalid-request"},
		"bare IPv6 endpoint": {author, m, `{"talosEndpoint":"fd00::4"}`, 400, "invalid-request"},
		"port 0":             {author, m, `{"talosEndpoint":"10.55.0.4:0"}`, 400, "invalid-request"},
		"unknown member":     {author, m, `{"talosEndpoint":"10.55.0.4","port":1}`, 400, "invalid-request"},
		"unknown machine":    {author, id.New(id.Machine), `{"talosEndpoint":"10.55.0.4"}`, 404, "not-found"},
		"not a machine id":   {author, id.New(id.Cluster), `{"talosEndpoint":"10.55.0.4"}`, 404, "not-found"},
		"same endpoint":      {author, m, `{"talosEndpoint":"10.55.0.3"}`, 409, "conflict"},
	} {
		rec := e.do(e.api, replaceEndpoint(c.tok, "k-refused-0123456789", c.machine, c.body))
		doc := wantProblem(t, rec, c.status, c.code)
		if strings.Contains(rec.Body.String(), marker) {
			t.Errorf("%s: the problem quotes the value: %s", name, rec.Body)
		}
		if name == "automation" && doc["humanOnly"] != true {
			t.Errorf("automation: %v; want humanOnly", doc)
		}
	}
	if ev, ch, acts, ctr := endpointRows(t, e, m); ev != 0 || ch != 0 || acts != 0 || ctr != 0 {
		t.Fatalf("after refusals: events %d, changes %d, acts %d, counter %d", ev, ch, acts, ctr)
	}
	var ep string
	if err := e.db.QueryRow("SELECT talos_endpoint FROM machine WHERE id = $1", m).Scan(&ep); err != nil || ep != "10.55.0.3:50000" {
		t.Fatalf("endpoint %q, %v", ep, err)
	}
}

// PA §3.3, §5: the change, its timeline entry, the endpoint and the act commit together or not at
// all. Every attempt fails before COMMIT: nothing is written. The control lets the commit through.
func TestTalosEndpointAtomic(t *testing.T) {
	for _, failing := range []bool{true, false} {
		var fail bool
		e := newEnv(t, options{beforeCommit: func(n int) error {
			if fail {
				return deadlock(maxAttempts)(n)
			}
			return nil
		}})
		author := e.human("h-author")
		m := e.endpointMachine(author)
		fail = failing
		rec := e.do(e.api, replaceEndpoint(author, "k-atomic-0123456789", m, `{"talosEndpoint":"10.55.0.4"}`))
		var ep string
		if err := e.db.QueryRow("SELECT talos_endpoint FROM machine WHERE id = $1", m).Scan(&ep); err != nil {
			t.Fatal(err)
		}
		ev, ch, acts, ctr := endpointRows(t, e, m)
		switch {
		case failing && (rec.Code != http.StatusServiceUnavailable || ev+ch+acts+ctr != 0 || ep != "10.55.0.3:50000"):
			t.Errorf("failed commit: %d, events %d, changes %d, acts %d, counter %d, endpoint %s; want nothing", rec.Code, ev, ch, acts, ctr, ep)
		case !failing && (rec.Code != http.StatusCreated || ev != 1 || ch != 1 || acts != 1 || ctr != 1 || ep != "10.55.0.4:50000"):
			t.Errorf("control: %d, events %d, changes %d, acts %d, counter %d, endpoint %s; want all written", rec.Code, ev, ch, acts, ctr, ep)
		}
	}
}

// §3.3 (T11): the replacement reads the previous endpoint under the machine row FOR UPDATE. With
// the first replacement held open after its effect, a second waits for it and records the
// endpoint the first set as its previous one; reading the row unlocked, it would record the
// endpoint from before the first.
func TestTalosEndpointConcurrent(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	m := e.endpointMachine(author)
	hook, held, release := holdFirst()
	t.Cleanup(release)
	api := e.build(options{afterEffect: hook})
	a, b := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
	go func() {
		a <- e.do(api, replaceEndpoint(author, "k-first-0123456789", m, `{"talosEndpoint":"10.55.0.4"}`))
	}()
	<-held
	go func() {
		b <- e.do(api, replaceEndpoint(author, "k-second-012345678", m, `{"talosEndpoint":"10.55.0.5"}`))
	}()
	dbtest.WaitForLockWait(t, e.db)
	release()
	first := decode[endpointChangeBody](t, <-a, http.StatusCreated)
	second := decode[endpointChangeBody](t, <-b, http.StatusCreated)
	if first.Revision != 1 || second.Revision != 2 || second.PreviousEndpoint != first.TalosEndpoint {
		t.Fatalf("first %+v, second %+v; want the second to start from the first's endpoint", first, second)
	}
}

// §12.2: the replacement is accepted installation-wide in recovery mode, and the entry carries
// the recovery epoch.
func TestTalosEndpointInRecoveryMode(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	m := e.endpointMachine(author)
	ep := newEpoch(t, e.db)
	mustExec(t, e.db, "UPDATE installation_state SET recovery_mode = true")
	b := decode[endpointChangeBody](t, e.do(e.api, replaceEndpoint(author, "k-recovery-0123456789", m, `{"talosEndpoint":"10.55.0.4"}`)),
		http.StatusCreated)
	if b.Epoch != ep || count(t, e.db, "SELECT count(*) FROM machine_event WHERE machine = $1 AND epoch = $2", m, ep) != 1 {
		t.Fatalf("change %+v; want epoch %s on the change and its entry", b, ep)
	}
}
