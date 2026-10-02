package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
)

const uuidA = "0b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b"

// decode is rec's JSON body, failing the test unless rec answered status with a JSON resource: a
// problem document decodes too, into a value whose members are all absent.
func decode[T any](t *testing.T, rec *httptest.ResponseRecorder, status int) T {
	t.Helper()
	if rec.Code != status || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%d %s %s; want %d with a JSON resource", rec.Code, rec.Header().Get("Content-Type"), rec.Body, status)
	}
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("%d %s: %v", rec.Code, rec.Body, err)
	}
	return v
}

// talosClusterID is a Talos cluster ID of the shape `talosctl get info` prints, distinct per seed.
func talosClusterID(seed string) string {
	s := sha256.Sum256([]byte(seed))
	return base64.StdEncoding.EncodeToString(s[:])
}

// clusterCall records a cluster as tok, its Talos cluster ID derived from k.
func clusterCall(tok, k string) call {
	return call{method: "POST", path: prefix + "/clusters", token: tok, key: k,
		body: `{"name":"office","endpoint":"https://cp.example.test:6443","contract":"v1.13","talosClusterId":"` + talosClusterID(k) + `"}`}
}

// createCluster records a cluster as tok and returns its identifier.
func (e *env) createCluster(h http.Handler, tok, k string) string {
	e.t.Helper()
	rec := e.do(h, clusterCall(tok, k))
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("POST /clusters: %d %s", rec.Code, rec.Body)
	}
	return decode[clusterBody](e.t, rec, http.StatusCreated).ID
}

// validEndpoint takes https://<host>[:<port>] and nothing else: the controls beside the refusals
// show that a name, an IPv4 address and a bracketed IPv6 address, each with or without a port, pass.
func TestValidEndpoint(t *testing.T) {
	for s, want := range map[string]bool{
		"https://cp.example.test":        true,
		"https://cp.example.test:6443":   true,
		"https://10.0.0.1:6443":          true,
		"https://[fd00::1]:6443":         true,
		"https://[fd00::1]":              true,
		"https://cp.example.test::6443":  false,
		"https://cp.example.test:":       false,
		"https://cp.example.test:0":      false,
		"https://cp.example.test:65536":  false,
		"https://cp..example.test:6443":  false,
		"https://-cp.example.test:6443":  false,
		"https://cp_1.example.test:6443": false,
		"https://[cp.example.test]:6443": false,
		"https://[fd00::1%25eth0]:6443":  false,
		"https://fd00::1:6443":           false,
		"https://:6443":                  false,
	} {
		if got := validEndpoint(s); got != want {
			t.Errorf("validEndpoint(%q) = %v; want %v", s, got, want)
		}
	}
}

func machineCall(tok, k, cluster, uuid string) call {
	return call{method: "POST", path: prefix + "/machines", token: tok, key: k,
		body: `{"cluster":"` + cluster + `","smbiosUuid":"` + uuid + `","serial":"SN-1","talosEndpoint":"10.55.0.3"}`}
}

// nodeA is a Talos node ID as `talosctl get identity` prints it.
const nodeA = "7x1SuC8Ege5BGXdAfTEff5iQnlWZLfv9h1LGMxA2pYkC"

// nodeMachineCall inventories a machine that reports no SMBIOS UUID, by its Talos node ID.
func nodeMachineCall(tok, k, cluster, node string) call {
	return call{method: "POST", path: prefix + "/machines", token: tok, key: k,
		body: `{"cluster":"` + cluster + `","talosNodeId":"` + node + `","talosEndpoint":"10.55.0.4"}`}
}

// PA §3.3: POST /machines takes an IPv4 or bracketed IPv6 literal with an optional port and stores
// it as host:port, the port 50000 when absent; GET answers the stored form.
func TestInventoryTalosEndpoint(t *testing.T) {
	e := newEnv(t, options{})
	author, viewer := e.human("h-author"), e.human("h-viewer")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	for i, c := range []struct{ in, want string }{
		{"10.55.0.3", "10.55.0.3:50000"},
		{"10.55.0.4:50001", "10.55.0.4:50001"},
		{"[FD00::3]", "[fd00::3]:50000"},
		{"[fd00::4]:1", "[fd00::4]:1"},
	} {
		uuid := fmt.Sprintf("5a0f1b6d-7e8c-4d0e-9f4a-%012x", i)
		rec := e.do(e.api, call{method: "POST", path: prefix + "/machines", token: author, key: fmt.Sprintf("k-endpoint-%d-0123456789", i),
			body: `{"cluster":"` + cl + `","smbiosUuid":"` + uuid + `","talosEndpoint":"` + c.in + `"}`})
		m := decode[machineBody](t, rec, http.StatusCreated)
		if m.TalosEndpoint != c.want {
			t.Errorf("POST %s: talosEndpoint %q; want %q", c.in, m.TalosEndpoint, c.want)
		}
		got := decode[machineBody](t, e.do(e.api, call{method: "GET", path: prefix + "/machines/" + m.ID, token: viewer}), http.StatusOK)
		if got.TalosEndpoint != c.want {
			t.Errorf("GET after %s: talosEndpoint %q; want %q", c.in, got.TalosEndpoint, c.want)
		}
	}
}

// §10.3, §9.2: inventory is author, human only. Automation is refused whatever its roles, a
// human without author by role, and neither refusal writes an act; a human author succeeds.
func TestInventoryIsHumanOnly(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	for _, c := range []call{
		clusterCall(e.robot, key),
		machineCall(e.robot, key, cl, uuidA),
	} {
		doc := wantProblem(t, e.do(e.api, c), http.StatusForbidden, "forbidden")
		if doc["humanOnly"] != true {
			t.Errorf("%s: %v; want humanOnly", c.path, doc)
		}
	}
	wantProblem(t, e.do(e.api, machineCall(e.human("h-viewer"), key, cl, uuidA)), http.StatusForbidden, "forbidden")
	if n := count(t, e.db, "SELECT count(*) FROM act WHERE via = 'api'"); n != 1 {
		t.Fatalf("%d acts; want only the cluster's", n)
	}
	rec := e.do(e.api, machineCall(author, key, cl, uuidA))
	if rec.Code != http.StatusCreated {
		t.Fatalf("human author: %d %s", rec.Code, rec.Body)
	}
	m := decode[machineBody](t, rec, http.StatusCreated)
	if rec.Header().Get("Location") != prefix+"/machines/"+m.ID || m.Cluster != cl || m.Hardware.SMBIOSUUID == nil ||
		*m.Hardware.SMBIOSUUID != uuidA || m.Hardware.TalosNodeID != nil || m.ScopeState != "normal" || m.Frozen || m.Applied != nil || m.Desired != nil {
		t.Fatalf("machine %+v, Location %s", m, rec.Header().Get("Location"))
	}
	if n := count(t, e.db, "SELECT count(*) FROM act WHERE action = 'machine.inventory' AND role = 'author' AND $1 = ANY (subjects)", m.ID); n != 1 {
		t.Fatalf("%d inventory acts naming the machine", n)
	}
	if n := count(t, e.db, "SELECT count(*) FROM machine_state WHERE machine = $1", m.ID); n != 1 {
		t.Fatalf("%d MachineState rows", n)
	}
}

// §7.3: two inventory requests for one SMBIOS UUID under different idempotency keys, by one human
// or two, commit one machine; the others are 409 conflict naming it, and commit nothing.
func TestSMBIOSUUIDIsOneMachine(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	first := e.do(e.api, machineCall(author, "k-first-0123456789", cl, uuidA))
	if first.Code != http.StatusCreated {
		t.Fatalf("first: %d %s", first.Code, first.Body)
	}
	existing := decode[machineBody](t, first, http.StatusCreated).ID
	for _, c := range []call{
		machineCall(author, "k-second-012345678", cl, uuidA),
		machineCall(e.human("h-all"), "k-third-0123456789", cl, strings.ToUpper(uuidA)),
	} {
		doc := wantProblem(t, e.do(e.api, c), http.StatusConflict, "conflict")
		if doc["machine"] != existing {
			t.Errorf("409 names %v; want %s", doc["machine"], existing)
		}
	}
	if n := count(t, e.db, "SELECT count(*) FROM machine"); n != 1 {
		t.Fatalf("%d machines", n)
	}
	if n := count(t, e.db, "SELECT count(*) FROM act WHERE action = 'machine.inventory'"); n != 1 {
		t.Fatalf("%d inventory acts", n)
	}
	// The first key still replays its machine.
	replay := e.do(e.api, machineCall(author, "k-first-0123456789", cl, uuidA))
	if replay.Code != http.StatusCreated || replay.Header().Get("Idempotent-Replayed") != "true" || decode[machineBody](t, replay, http.StatusCreated).ID != existing {
		t.Fatalf("replay: %d %v %s", replay.Code, replay.Header(), replay.Body)
	}
}

// The control of the test above: without the unique index, the second request commits a second
// machine, so it is the index that refuses it.
func TestSMBIOSUUIDControl(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	mustExec(t, e.db, "DROP INDEX machine_smbios_uuid")
	for _, k := range []string{"k-first-0123456789", "k-second-012345678"} {
		if rec := e.do(e.api, machineCall(author, k, cl, uuidA)); rec.Code != http.StatusCreated {
			t.Fatalf("%s without the index: %d %s", k, rec.Code, rec.Body)
		}
	}
	if n := count(t, e.db, "SELECT count(*) FROM machine"); n != 2 {
		t.Fatalf("%d machines without the index; want 2", n)
	}
}

// §7.3, §16: the same race with both transactions open, for each identity key: one SMBIOS UUID,
// one Talos node ID, one Talos cluster ID. The second waits on the first's uncommitted row, then
// answers 409 naming it; without the key's index both commit.
func TestIdentityKeyConcurrent(t *testing.T) {
	for _, k := range []struct {
		name, index, table, member string
		mk                         func(tok, k, cl string) call
	}{
		{"SMBIOS UUID", "machine_smbios_uuid", "machine", "machine", func(tok, k, cl string) call { return machineCall(tok, k, cl, uuidA) }},
		{"Talos node ID", "machine_talos_node_id", "machine", "machine", func(tok, k, cl string) call { return nodeMachineCall(tok, k, cl, nodeA) }},
		{"Talos cluster ID", "cluster_talos_cluster_id", "cluster", "cluster", func(tok, k, _ string) call {
			c := clusterCall(tok, "k-cluster-shared-0123") // one Talos cluster ID under two keys
			c.key = k
			return c
		}},
	} {
		for _, control := range []bool{false, true} {
			e := newEnv(t, options{})
			author := e.human("h-author")
			cl := e.createCluster(e.api, author, "k-cluster-0123456789")
			before := count(t, e.db, "SELECT count(*) FROM "+k.table)
			if control {
				mustExec(t, e.db, "DROP INDEX "+k.index)
			}
			hook, held, release := holdFirst()
			t.Cleanup(release)
			// The control drops the act-order lock too: the held first request holds it (§5 rule 5), so
			// the second could not commit while the first is held.
			api := e.build(options{afterEffect: hook, noActOrder: control})
			first, second := k.mk(author, "k-first-0123456789", cl), k.mk(author, "k-second-012345678", cl)
			a, b := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
			go func() { a <- e.do(api, first) }()
			<-held
			go func() { b <- e.do(api, second) }()
			if !control {
				dbtest.WaitForLockWait(t, e.db)
			} else if rec := <-b; rec.Code != http.StatusCreated { // nothing to wait on: it commits while the first is held
				t.Fatalf("%s control, second: %d %s", k.name, rec.Code, rec.Body)
			}
			release()
			ra := <-a
			if ra.Code != http.StatusCreated {
				t.Fatalf("%s control %t, first: %d %s", k.name, control, ra.Code, ra.Body)
			}
			n := count(t, e.db, "SELECT count(*) FROM "+k.table) - before
			if control {
				if n != 2 {
					t.Fatalf("%s control: %d new rows; want 2", k.name, n)
				}
				continue
			}
			doc := wantProblem(t, <-b, http.StatusConflict, "conflict")
			if doc[k.member] != decode[struct{ ID string }](t, ra, http.StatusCreated).ID {
				t.Fatalf("%s: 409 names %v; want the first's %s", k.name, doc[k.member], k.member)
			}
			if n != 1 {
				t.Fatalf("%s: %d new rows", k.name, n)
			}
		}
	}
}

// §7.3: a machine that reports no SMBIOS UUID is inventoried by its Talos node ID, answered with
// talosNodeId and a null smbiosUuid; SMBIOS's nil and all-ones values are accepted as a UUID, each
// once; and a cluster answers its Talos cluster ID.
func TestInventoryIdentityKeys(t *testing.T) {
	e := newEnv(t, options{})
	author, viewer := e.human("h-author"), e.human("h-viewer")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	if got := decode[clusterBody](t, e.do(e.api, call{method: "GET", path: prefix + "/clusters/" + cl, token: viewer}), http.StatusOK); got.TalosClusterID != talosClusterID("k-cluster-0123456789") {
		t.Fatalf("cluster talosClusterId %q", got.TalosClusterID)
	}
	m := decode[machineBody](t, e.do(e.api, nodeMachineCall(author, "k-node-0123456789ab", cl, nodeA)), http.StatusCreated)
	rec := e.do(e.api, call{method: "GET", path: prefix + "/machines/" + m.ID, token: viewer})
	var raw struct {
		Hardware map[string]any `json:"hardware"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil || raw.Hardware["talosNodeId"] != nodeA {
		t.Fatalf("GET node-ID machine: %v %s", err, rec.Body)
	}
	if v, ok := raw.Hardware["smbiosUuid"]; !ok || v != nil {
		t.Fatalf("smbiosUuid %v (present %t); want null", v, ok)
	}
	for i, u := range []string{"00000000-0000-0000-0000-000000000000", "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF"} {
		got := decode[machineBody](t, e.do(e.api, machineCall(author, "k-nil-"+strconv.Itoa(i)+"-0123456789", cl, u)), http.StatusCreated)
		if got.Hardware.SMBIOSUUID == nil || *got.Hardware.SMBIOSUUID != strings.ToLower(u) {
			t.Fatalf("%s: %+v", u, got.Hardware)
		}
		doc := wantProblem(t, e.do(e.api, machineCall(author, "k-nil-again-"+strconv.Itoa(i)+"-012345", cl, u)), http.StatusConflict, "conflict")
		if doc["machine"] != got.ID {
			t.Fatalf("second %s names %v; want %s", u, doc["machine"], got.ID)
		}
	}
}

// §9.1, §9.4: inventory bodies are checked before any transaction, and neither the answer nor the
// log repeats a submitted value.
func TestInventoryRefusals(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	const marker = "zq-marker-7f3a"
	tc := `,"talosClusterId":"` + talosClusterID("k-refused") + `"`
	short := base64.StdEncoding.EncodeToString(make([]byte, 31))
	// A Talos cluster ID decoding to 32 bytes in a spelling other than the canonical one: nonzero
	// padding bits, and a line break the decoder skips.
	loose, broken := talosClusterID("x")[:42]+"B=", talosClusterID("x")[:20]+`\n`+talosClusterID("x")[20:]
	m := func(members string) string {
		return `{"cluster":"` + cl + `",` + members + `,"talosEndpoint":"10.55.0.3"}`
	}
	for name, c := range map[string]struct {
		path, body string
		status     int
		code       string
	}{
		"plain http":               {"/clusters", `{"name":"x","endpoint":"http://` + marker + `.test","contract":"v1.13"` + tc + `}`, 400, "invalid-request"},
		"endpoint with path":       {"/clusters", `{"name":"x","endpoint":"https://` + marker + `.test/api","contract":"v1.13"` + tc + `}`, 400, "invalid-request"},
		"endpoint with user":       {"/clusters", `{"name":"x","endpoint":"https://u@` + marker + `.test","contract":"v1.13"` + tc + `}`, 400, "invalid-request"},
		"doubled port colon":       {"/clusters", `{"name":"x","endpoint":"https://` + marker + `.test::6443","contract":"v1.13"` + tc + `}`, 400, "invalid-request"},
		"empty port":               {"/clusters", `{"name":"x","endpoint":"https://` + marker + `.test:","contract":"v1.13"` + tc + `}`, 400, "invalid-request"},
		"contract patch":           {"/clusters", `{"name":"x","endpoint":"https://a.test","contract":"v1.13.6-` + marker + `"` + tc + `}`, 400, "invalid-request"},
		"blank name":               {"/clusters", `{"name":"  ","endpoint":"https://a.test","contract":"v1.13"` + tc + `}`, 400, "invalid-request"},
		"name missing":             {"/clusters", `{"endpoint":"https://a.test","contract":"v1.13"` + tc + `}`, 400, "invalid-request"},
		"no talos endpoint":        {"/machines", `{"cluster":"` + cl + `","smbiosUuid":"` + uuidA + `"}`, 400, "invalid-request"},
		"DNS talos endpoint":       {"/machines", `{"cluster":"` + cl + `","smbiosUuid":"` + uuidA + `","talosEndpoint":"` + marker + `.test"}`, 400, "invalid-request"},
		"endpoint scheme":          {"/machines", `{"cluster":"` + cl + `","smbiosUuid":"` + uuidA + `","talosEndpoint":"https://10.55.0.3"}`, 400, "invalid-request"},
		"endpoint path":            {"/machines", `{"cluster":"` + cl + `","smbiosUuid":"` + uuidA + `","talosEndpoint":"10.55.0.3:50000/` + marker + `"}`, 400, "invalid-request"},
		"endpoint user part":       {"/machines", `{"cluster":"` + cl + `","smbiosUuid":"` + uuidA + `","talosEndpoint":"` + marker + `@10.55.0.3"}`, 400, "invalid-request"},
		"endpoint whitespace":      {"/machines", `{"cluster":"` + cl + `","smbiosUuid":"` + uuidA + `","talosEndpoint":" 10.55.0.3"}`, 400, "invalid-request"},
		"bare IPv6 endpoint":       {"/machines", `{"cluster":"` + cl + `","smbiosUuid":"` + uuidA + `","talosEndpoint":"fd00::3"}`, 400, "invalid-request"},
		"endpoint port 65536":      {"/machines", `{"cluster":"` + cl + `","smbiosUuid":"` + uuidA + `","talosEndpoint":"10.55.0.3:65536"}`, 400, "invalid-request"},
		"endpoint not a string":    {"/machines", `{"cluster":"` + cl + `","smbiosUuid":"` + uuidA + `","talosEndpoint":["10.55.0.3"]}`, 400, "invalid-request"},
		"malformed UUID":           {"/machines", `{"cluster":"` + cl + `","smbiosUuid":"` + marker + `","talosEndpoint":"10.55.0.3"}`, 400, "invalid-request"},
		"no identity key":          {"/machines", m(`"serial":"` + marker + `"`), 400, "invalid-request"},
		"both identity keys":       {"/machines", m(`"smbiosUuid":"` + uuidA + `","talosNodeId":"` + marker + `"`), 400, "invalid-request"},
		"both keys null":           {"/machines", m(`"smbiosUuid":null,"talosNodeId":null`), 400, "invalid-request"},
		"empty node ID":            {"/machines", m(`"talosNodeId":""`), 400, "invalid-request"},
		"node ID with a space":     {"/machines", m(`"talosNodeId":"node ` + marker + `"`), 400, "invalid-request"},
		"node ID of 129 bytes":     {"/machines", m(`"talosNodeId":"` + marker + strings.Repeat("a", 129-len(marker)) + `"`), 400, "invalid-request"},
		"non-ASCII node ID":        {"/machines", m(`"talosNodeId":"` + marker + `é"`), 400, "invalid-request"},
		"node ID not a string":     {"/machines", m(`"talosNodeId":7`), 400, "invalid-request"},
		"no Talos cluster ID":      {"/clusters", `{"name":"x","endpoint":"https://a.test","contract":"v1.13"}`, 400, "invalid-request"},
		"malformed cluster ID":     {"/clusters", `{"name":"x","endpoint":"https://a.test","contract":"v1.13","talosClusterId":"` + marker + `"}`, 400, "invalid-request"},
		"cluster ID of 31 bytes":   {"/clusters", `{"name":"x","endpoint":"https://a.test","contract":"v1.13","talosClusterId":"` + short + `"}`, 400, "invalid-request"},
		"unpadded cluster ID":      {"/clusters", `{"name":"x","endpoint":"https://a.test","contract":"v1.13","talosClusterId":"` + strings.TrimSuffix(talosClusterID("x"), "=") + `"}`, 400, "invalid-request"},
		"non-canonical cluster ID": {"/clusters", `{"name":"x","endpoint":"https://a.test","contract":"v1.13","talosClusterId":"` + loose + `"}`, 400, "invalid-request"},
		"cluster ID with a break":  {"/clusters", `{"name":"x","endpoint":"https://a.test","contract":"v1.13","talosClusterId":"` + broken + `"}`, 400, "invalid-request"},
		"blank serial":             {"/machines", `{"cluster":"` + cl + `","smbiosUuid":"` + uuidA + `","serial":" ","talosEndpoint":"10.55.0.3"}`, 400, "invalid-request"},
		"cluster of a draft":       {"/machines", `{"cluster":"` + drf + `","smbiosUuid":"` + uuidA + `","talosEndpoint":"10.55.0.3"}`, 400, "invalid-request"},
		"unknown cluster":          {"/machines", `{"cluster":"` + id.New(id.Cluster) + `","smbiosUuid":"` + uuidA + `","serial":"` + marker + `","talosEndpoint":"10.55.0.3"}`, 404, "not-found"},
		"hardware member":          {"/machines", `{"cluster":"` + cl + `","smbiosUuid":"` + uuidA + `","hardware":{"smbiosUuid":"` + uuidA + `"},"talosEndpoint":"10.55.0.3"}`, 400, "invalid-request"},
		"draft of no cluster":      {"/drafts", `{"cluster":"` + id.New(id.Cluster) + `","title":"` + marker + `"}`, 404, "not-found"},
		"blank title":              {"/drafts", `{"cluster":"` + cl + `","title":""}`, 400, "invalid-request"},
		// A mutating route takes no query (§9.1), and the fingerprint does not cover one.
		"cluster with a query": {"/clusters?dryRun=" + marker, `{"name":"x","endpoint":"https://a.test","contract":"v1.13"` + tc + `}`, 400, "invalid-request"},
		"machine with a query": {"/machines?x=" + marker, `{"cluster":"` + cl + `","smbiosUuid":"` + uuidA + `","talosEndpoint":"10.55.0.3"}`, 400, "invalid-request"},
		"draft with a query":   {"/drafts?x=" + marker, `{"cluster":"` + cl + `","title":"t"}`, 400, "invalid-request"},
	} {
		rec := e.do(e.api, call{method: "POST", path: prefix + c.path, token: author, key: "k-refused-" + strings.ReplaceAll(name, " ", "-") + "-0123456", body: c.body})
		wantProblem(t, rec, c.status, c.code)
		if strings.Contains(rec.Body.String(), marker) {
			t.Errorf("%s: the answer repeats the submitted value: %s", name, rec.Body)
		}
	}
	if e.logged(marker) {
		t.Fatal("the server log holds a submitted value")
	}
	if n := count(t, e.db, "SELECT count(*) FROM machine"); n != 0 {
		t.Fatalf("%d machines from refused requests", n)
	}
	if n := count(t, e.db, "SELECT count(*) FROM act WHERE via = 'api'"); n != 1 {
		t.Fatalf("%d acts; want only the cluster's", n)
	}
}

// PA §12.2, execution and recovery §7.4: a machine inventoried while recovery mode is in effect
// starts pre-restore unaccounted.
func TestInventoryInRecoveryMode(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	newEpoch(t, e.db)
	mustExec(t, e.db, "UPDATE installation_state SET recovery_mode = true")
	rec := e.do(e.api, machineCall(author, key, cl, uuidA))
	if decode[machineBody](t, rec, http.StatusCreated).ScopeState != "pre-restore-unaccounted" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

var etagShape = regexp.MustCompile(`^"1-[a-z2-7]{26}"$`)

// §9.2, §9.3: POST /drafts is author, automation included; it answers 201 with the draft's ETag,
// which GET /drafts/{id} repeats.
func TestCreateDraft(t *testing.T) {
	e := newEnv(t, options{})
	cl := e.createCluster(e.api, e.human("h-author"), "k-cluster-0123456789")
	body := `{"cluster":"` + cl + `","title":"registry mirror"}`
	wantProblem(t, e.do(e.api, call{method: "POST", path: prefix + "/drafts", token: e.human("h-viewer"), key: key, body: body}),
		http.StatusForbidden, "forbidden")
	for _, tok := range []string{e.human("h-author"), e.robot} {
		rec := e.do(e.api, call{method: "POST", path: prefix + "/drafts", token: tok, key: key, body: body})
		if rec.Code != http.StatusCreated || !etagShape.MatchString(rec.Header().Get("ETag")) {
			t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body)
		}
		d := decode[draftBody](t, rec, http.StatusCreated)
		if rec.Header().Get("Location") != prefix+"/drafts/"+d.ID || d.Cluster != cl || d.Title != "registry mirror" ||
			d.State != "open" || d.Revision != 1 || d.Entries == nil || len(d.Entries) != 0 {
			t.Fatalf("draft %+v", d)
		}
		got := e.do(e.api, call{method: "GET", path: prefix + "/drafts/" + d.ID, token: e.human("h-viewer")})
		if got.Header().Get("ETag") != rec.Header().Get("ETag") || decode[draftBody](t, got, http.StatusOK).ID != d.ID {
			t.Fatalf("GET: %d %v %s", got.Code, got.Header(), got.Body)
		}
	}
	if n := count(t, e.db, "SELECT count(*) FROM act WHERE action = 'draft.create'"); n != 2 {
		t.Fatalf("%d draft acts", n)
	}
}

// §4.1, §5 rules 2 and 3: a draft read takes the draft rows FOR SHARE before it reads their entries
// in a second statement. A draft transaction holding the draft FOR UPDATE, as T1 does, therefore
// cannot commit between the two and pair revision N's ETag with revision N+1's entries: the read
// waits for it and answers the committed revision whole. The control, without the lock, answers
// while that transaction is still open, so the wait is the lock's.
func TestDraftReadsLockTheDraft(t *testing.T) {
	for _, control := range []bool{false, true} {
		e := newEnv(t, options{})
		author, viewer := e.human("h-author"), e.human("h-viewer")
		cl := e.createCluster(e.api, author, "k-cluster-0123456789")
		d := decode[draftBody](t, e.do(e.api, call{method: "POST", path: prefix + "/drafts", token: author, key: key,
			body: `{"cluster":"` + cl + `","title":"import"}`}), http.StatusCreated)
		api := e.build(options{noDraftLock: control})
		for i, path := range []string{"/drafts/" + d.ID, "/drafts"} {
			uuid := []string{uuidA, "1c6b7d2f-3a4e-4f6a-9b0c-1d2e3f4a5b6c"}[i]
			rec := e.do(e.api, machineCall(author, "k-machine-"+strconv.Itoa(i)+"-0123456789", cl, uuid))
			m, ibr := decode[machineBody](t, rec, http.StatusCreated).ID, id.New(id.ImportBase)
			mustExec(t, e.db, `INSERT INTO import_base_revision (id, machine, document, baseline_ciphertext, baseline_digest,
				baseline_digest_key, configuration_digest, created_at) VALUES ($1, $2, 'machine: {}', '\x01', $3, 'k:1', $3, now())`,
				ibr, m, make([]byte, 32))
			var before int
			if err := e.db.QueryRow("SELECT revision FROM draft WHERE id = $1", d.ID).Scan(&before); err != nil {
				t.Fatal(err)
			}
			// The draft transaction: the draft FOR UPDATE, an entry, the next revision and token.
			tx, err := e.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tx.Rollback() })
			tok := etagToken()
			for _, s := range []struct {
				q    string
				args []any
			}{
				{"SELECT 1 FROM draft WHERE id = $1 FOR UPDATE", []any{d.ID}},
				{"INSERT INTO draft_entry (draft, cluster, kind, machine, import_base_revision) VALUES ($1, $2, 'import-base', $3, $4)",
					[]any{d.ID, cl, m, ibr}},
				{"UPDATE draft SET revision = revision + 1, etag_token = $2 WHERE id = $1", []any{d.ID, tok}},
			} {
				if _, err := tx.Exec(s.q, s.args...); err != nil {
					t.Fatalf("%s: %v", s.q, err)
				}
			}
			got := make(chan *httptest.ResponseRecorder, 1)
			go func() { got <- e.do(api, call{method: "GET", path: prefix + path, token: viewer}) }()
			want, wantETag := before, ""
			if control {
				rec = <-got // answered with the transaction open
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			} else {
				dbtest.WaitForLockWait(t, e.db)
				select {
				case rec := <-got:
					t.Fatalf("GET %s answered while the draft transaction held the draft: %d %s", path, rec.Code, rec.Body)
				default:
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				rec = <-got
				want, wantETag = before+1, etag(before+1, tok)
			}
			var b draftBody
			if i == 0 {
				b = decode[draftBody](t, rec, http.StatusOK)
				if !control && rec.Header().Get("ETag") != wantETag {
					t.Errorf("GET %s: ETag %s; want %s", path, rec.Header().Get("ETag"), wantETag)
				}
			} else if pg := decode[listPage[draftBody]](t, rec, http.StatusOK); len(pg.Items) == 1 {
				b = pg.Items[0]
			}
			// Before the draft transaction every entry is of a machine already listed; after it, one more.
			if b.ID != d.ID || b.Revision != want || len(b.Entries) != want-1 {
				t.Fatalf("control %t, GET %s: %s; want revision %d with %d entries", control, path, rec.Body, want, want-1)
			}
		}
	}
}

// §9.1: the collections page by limit and cursor, a cursor from another epoch is refused, and an
// item that is not there, or an identifier of another entity, is 404.
func TestInventoryReads(t *testing.T) {
	e := newEnv(t, options{})
	author, viewer := e.human("h-author"), e.human("h-viewer")
	var clusters []string
	for _, k := range []string{"k-cluster-a-012345678", "k-cluster-b-012345678", "k-cluster-c-012345678"} {
		clusters = append(clusters, e.createCluster(e.api, author, k))
	}
	var seen []string
	next := ""
	for range 4 {
		p := prefix + "/clusters?limit=2"
		if next != "" {
			p += "&cursor=" + next
		}
		rec := e.do(e.api, call{method: "GET", path: p, token: viewer})
		pg := decode[listPage[clusterBody]](t, rec, http.StatusOK)
		if rec.Header().Get("Bronzeward-Epoch") == "" {
			t.Fatalf("no epoch: %v", rec.Header())
		}
		for _, c := range pg.Items {
			seen = append(seen, c.ID)
		}
		if next = pg.Next; next == "" {
			break
		}
	}
	if len(seen) != 3 {
		t.Fatalf("listed %v; want the 3 clusters", seen)
	}
	for _, c := range clusters {
		rec := e.do(e.api, call{method: "GET", path: prefix + "/clusters/" + c, token: viewer})
		if b := decode[clusterBody](t, rec, http.StatusOK); b.ID != c || b.Endpoint != "https://cp.example.test:6443" || b.Contract != "v1.13" {
			t.Fatalf("GET cluster: %d %s", rec.Code, rec.Body)
		}
	}
	m := decode[machineBody](t, e.do(e.api, machineCall(author, key, clusters[0], uuidA)), http.StatusCreated)
	rec := e.do(e.api, call{method: "GET", path: prefix + "/machines/" + m.ID, token: viewer})
	if got := decode[machineBody](t, rec, http.StatusOK); got.Hardware.Serial == nil || *got.Hardware.Serial != "SN-1" || got.Cluster != clusters[0] ||
		got.TalosEndpoint != "10.55.0.3:50000" {
		t.Fatalf("GET machine: %d %s", rec.Code, rec.Body)
	}
	if pg := decode[listPage[machineBody]](t, e.do(e.api, call{method: "GET", path: prefix + "/machines", token: viewer}), http.StatusOK); len(pg.Items) != 1 || pg.Items[0].ID != m.ID {
		t.Fatalf("machines %+v", pg)
	}
	// No draft yet: a present, empty items array (§9.1), not an absent or null one.
	if pg := decode[listPage[draftBody]](t, e.do(e.api, call{method: "GET", path: prefix + "/drafts", token: viewer}), http.StatusOK); pg.Items == nil || len(pg.Items) != 0 {
		t.Fatalf("drafts %+v; want an empty items array", pg)
	}
	old := decode[listPage[clusterBody]](t, e.do(e.api, call{method: "GET", path: prefix + "/clusters?limit=1", token: viewer}), http.StatusOK).Next
	newEpoch(t, e.db)
	wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/clusters?cursor=" + old, token: viewer}), http.StatusBadRequest, "cursor-invalid")
	wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/clusters?limit=0", token: viewer}), http.StatusBadRequest, "invalid-request")
	wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + "/machines?cluster=" + clusters[0], token: viewer}), http.StatusBadRequest, "invalid-request")
	// An item read takes no query at all (§9.1: unknown fields are refused), before any lookup.
	for _, p := range []string{"/clusters/" + clusters[0] + "?x=1", "/machines/" + m.ID + "?limit=1",
		"/drafts/" + id.New(id.Draft) + "?x", "/operations/" + id.New(id.Operation) + "?x=1"} {
		wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + p, token: viewer}), http.StatusBadRequest, "invalid-request")
	}
	for _, p := range []string{"/clusters/" + id.New(id.Cluster), "/clusters/" + m.ID, "/machines/" + id.New(id.Machine),
		"/drafts/" + id.New(id.Draft), "/drafts/x", "/operations/" + id.New(id.Operation)} {
		wantProblem(t, e.do(e.api, call{method: "GET", path: prefix + p, token: viewer}), http.StatusNotFound, "not-found")
	}
}

// §8.3: GET /operations/{id} answers the operation resource. No route of this change creates one,
// so the test inserts the ingest operation POST /ingestions will create.
func TestGetOperation(t *testing.T) {
	e := newEnv(t, options{})
	author := e.human("h-author")
	cl := e.createCluster(e.api, author, "k-cluster-0123456789")
	d := decode[draftBody](t, e.do(e.api, call{method: "POST", path: prefix + "/drafts", token: author, key: key,
		body: `{"cluster":"` + cl + `","title":"import"}`}), http.StatusCreated)
	var human string
	if err := e.db.QueryRow("SELECT id FROM principal WHERE sub = 'h-author'").Scan(&human); err != nil {
		t.Fatal(err)
	}
	m := decode[machineBody](t, e.do(e.api, machineCall(author, "k-machine-0123456789", cl, uuidA)), http.StatusCreated)
	claim, opID := id.New(id.Ingestion), id.New(id.Operation)
	mustExec(t, e.db, `INSERT INTO staging_claim (id, mode, state, owner, owner_gen, owner_epoch, lease_until, expires_at,
		principal, idempotency_key, cluster, machine, kind, created_at)
		SELECT $1, 'transient', 'held', 'run/1/start', 1, epoch, now() + interval '1 minute', now() + interval '1 hour',
		$2, 'k-ingest-0123456789', $3, $4, 'import', now() FROM installation_state`, claim, human, cl, m.ID)
	mustExec(t, e.db, `INSERT INTO operation (id, kind, state, epoch, owner, owner_gen, owner_epoch, lease_until, last_event,
		draft, draft_revision, ingestion, created_by, created_by_kind, created_role, created_at)
		SELECT $1, 'ingest', 'running', epoch, 'run/1/start', 1, epoch, now() + interval '1 minute', 2, $2, 1, $3, $4, 'human',
		'author', now() FROM installation_state`, opID, d.ID, claim, human)
	rec := e.do(e.api, call{method: "GET", path: prefix + "/operations/" + opID, token: e.human("h-viewer")})
	got := decode[map[string]any](t, rec, http.StatusOK)
	subject, _ := got["subject"].(map[string]any)
	by, _ := got["createdBy"].(map[string]any)
	if got["id"] != opID || got["kind"] != "ingest" || got["state"] != "running" || got["epoch"] != epoch(t, e.db) ||
		got["lastEvent"] != float64(2) || subject["draft"] != d.ID || subject["draftRevision"] != float64(1) ||
		by["principal"] != human || by["role"] != "author" || got["result"] != nil || got["error"] != nil || got["createdAt"] == nil {
		t.Fatalf("operation %s", rec.Body)
	}
	for _, k := range []string{"result", "error"} {
		if _, ok := got[k]; !ok {
			t.Errorf("%s is absent; §8.3 shows it null", k)
		}
	}
}
