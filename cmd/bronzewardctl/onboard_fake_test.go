package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/client/apitypes"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/talos/discover"
)

// The stand-ins of cluster onboard's tests: an in-memory API server and a stub for the discovery (the
// CLI's tests may not link the stand-in Talos node; package discover's tests cover the reads). Nothing
// here is a real credential: every secret is a planted marker, so a test can search every output for it.

const (
	plantedKeyA = "synthetic-key-of-context-A"
	plantedKeyB = "synthetic-key-of-context-B"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// twoContextTalosconfig holds contexts a (current) and b, each with its own certificate authority,
// client certificate, key, endpoint and node.
func twoContextTalosconfig() string {
	entry := func(name, key string) string {
		return "  " + name + ":\n    endpoints: [10.9.9.9:50000]\n    nodes: [10.8.8.8]\n    ca: " + b64("ca-"+name) +
			"\n    crt: " + b64("crt-"+name) + "\n    key: " + b64(key) + "\n"
	}
	return "context: a\ncontexts:\n" + entry("a", plantedKeyA) + entry("b", plantedKeyB)
}

// obMachine is a machine of the API fake.
type obMachine struct{ id, cluster, key, platform, endpoint string }

// obIngestion is an import of the API fake.
type obIngestion struct {
	id, machine, draft, op, state, result string
	events                                [][2]string
}

// apiFake is the Bronzeward server of the onboard tests.
type apiFake struct {
	mu       sync.Mutex
	roles    string // the GET /identity answer
	clusters []apitypes.ClusterBody
	machines []obMachine
	draft    struct {
		id, cluster, title, state string
		rev                       int
	}
	ings []obIngestion
	// log is every request as "METHOD /path", in order; posts is each POST's body.
	log, posts []string
	// pauseFirst makes the first import of a review pause; failImportAt, when not 0, is the number of
	// the import start that ends failed; afterImport runs after the n-th released import (a write to the
	// draft by someone else).
	pauseFirst   bool
	failImportAt int
	afterImport  func(n int, f *apiFake)
	released     int
	starts       int
	// failPost maps a POST route to the number of its first request that is answered 503, a write that
	// did not happen; counts is how many POSTs each route has had.
	failPost map[string]int
	counts   map[string]int
	// keys is each first key the route was sent, in order; keyAnswer, when set, answers the n-th one
	// (from 1) instead of the route's success, which says replaced from the second key on.
	keys      []fkPost
	keyAnswer func(n int, w http.ResponseWriter)
	// onPost, when set, runs for every write before it is answered and may set headers on the answer.
	onPost func(path string, w http.ResponseWriter)
	// machineRefusal, when set, is the code of a 409 that answers every POST /machines, naming the
	// machine mch_holder as the server's `conflict` and `name-in-use` both do.
	machineRefusal string
}

func (f *apiFake) writes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var w []string
	for _, l := range f.log {
		if !strings.HasPrefix(l, "GET ") {
			w = append(w, l)
		}
	}
	return w
}

func (f *apiFake) etag() string { return fmt.Sprintf(`"%d-OB"`, f.draft.rev) }

func (f *apiFake) draftJSON() string {
	return fmt.Sprintf(`{"id":%q,"cluster":%q,"title":%q,"state":%q,"revision":%d,"entries":[]}`,
		f.draft.id, f.draft.cluster, f.draft.title, f.draft.state, f.draft.rev)
}

func (f *apiFake) machineJSON(m obMachine) string {
	hw := `"talosNodeId":` + fmt.Sprintf("%q", m.key)
	if strings.Contains(m.key, "-") {
		hw = `"smbiosUuid":` + fmt.Sprintf("%q", m.key)
	}
	return fmt.Sprintf(`{"id":%q,"cluster":%q,"hardware":{%s},"platform":%q,"scopeState":"normal","talosEndpoint":%q,"frozen":false,"pendingConvergence":false}`,
		m.id, m.cluster, hw, m.platform, m.endpoint)
}

func (f *apiFake) ingJSON(i obIngestion) string {
	review := "null"
	if i.state == "paused" {
		review = `"pending"`
	}
	return fmt.Sprintf(`{"id":%q,"kind":"import","mode":"encrypted","state":%q,"machine":%q,"draft":%q,"operation":%q,"review":%s,`+
		`"ownerGeneration":1,"leaseUntil":%q,"expiresAt":"2030-01-01T00:00:00Z","createdAt":"2026-11-01T06:00:00Z"}`,
		i.id, i.state, i.machine, i.draft, i.op, review, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
}

func (f *apiFake) problem(w http.ResponseWriter, status int, code, extra string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"type":"urn:bronzeward:problem:%s","title":"OB","status":%d,"instance":"req_OB"%s}`, code, status, extra)
}

func (f *apiFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, r.Method+" "+path)
	w.Header().Set("Bronzeward-Epoch", "ep_OB-1")
	w.Header().Set("Content-Type", "application/json")
	var body []byte
	if r.Method != http.MethodGet {
		body, _ = io.ReadAll(r.Body)
		f.posts = append(f.posts, r.Method+" "+path+" "+string(body))
		if f.counts == nil {
			f.counts = map[string]int{}
		}
		f.counts[path]++
		if f.onPost != nil {
			f.onPost(path, w)
		}
		if n := f.failPost[path]; n > 0 && f.counts[path] >= n {
			f.problem(w, http.StatusUnprocessableEntity, "validation-failed", "")
			return
		}
	}
	switch {
	case path == "/server":
		fmt.Fprint(w, `{"version":"OB","apiRevision":2,"startMode":"normal"}`)
	case path == "/identity":
		fmt.Fprint(w, f.roles)
	case path == "/recovery": // the read a repeat of a write is confirmed by
		fmt.Fprint(w, `{}`)
	case r.Method == http.MethodPost && path == "/clusters":
		var in apitypes.ClusterInput
		_ = json.Unmarshal(body, &in)
		for _, c := range f.clusters {
			if c.TalosClusterId == in.TalosClusterId {
				f.problem(w, http.StatusConflict, "conflict", `,"cluster":`+fmt.Sprintf("%q", c.Id))
				return
			}
		}
		c := apitypes.ClusterBody{Id: id.New(id.Cluster), Name: in.Name, Endpoint: in.Endpoint, Contract: in.Contract, TalosClusterId: in.TalosClusterId}
		f.clusters = append(f.clusters, c)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(c)
	case r.Method == http.MethodGet && path == "/clusters":
		b, _ := json.Marshal(f.clusters)
		fmt.Fprintf(w, `{"items":%s}`, b)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/clusters/"):
		for _, c := range f.clusters {
			if "/clusters/"+c.Id == path {
				_ = json.NewEncoder(w).Encode(c)
				return
			}
		}
		f.problem(w, http.StatusNotFound, "not-found", "")
	case r.Method == http.MethodPost && path == "/machines":
		var in apitypes.MachineInput
		_ = json.Unmarshal(body, &in)
		key := ""
		if in.TalosNodeId != nil {
			key = *in.TalosNodeId
		}
		if in.SmbiosUuid != nil {
			key = strings.ToLower(*in.SmbiosUuid)
		}
		if f.machineRefusal != "" {
			f.problem(w, http.StatusConflict, f.machineRefusal, `,"machine":"mch_holder"`)
			return
		}
		for _, m := range f.machines {
			if m.key == key {
				f.problem(w, http.StatusConflict, "conflict", `,"machine":`+fmt.Sprintf("%q", m.id))
				return
			}
		}
		m := obMachine{id: id.New(id.Machine), cluster: in.Cluster, key: key, platform: string(in.Platform), endpoint: in.TalosEndpoint}
		f.machines = append(f.machines, m)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, f.machineJSON(m))
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/first-keys"):
		p := fkPost{path: path, key: r.Header.Get("Idempotency-Key")}
		_ = json.Unmarshal(body, &p.in)
		f.keys = append(f.keys, p)
		if f.keyAnswer != nil {
			f.keyAnswer(len(f.keys), w)
			return
		}
		fmt.Fprintf(w, `{"cluster":%q,"kind":%q,"replaced":%t,"caKeyVersion":{"version":%d,"createdTime":"2026-11-19T21:00:00Z"}}`,
			strings.TrimSuffix(strings.TrimPrefix(path, "/clusters/"), "/first-keys"), p.in.Kind, len(f.keys) > 1, len(f.keys))
	case r.Method == http.MethodGet && path == "/machines":
		var items []string
		for _, m := range f.machines {
			if m.cluster == r.URL.Query().Get("cluster") {
				items = append(items, f.machineJSON(m))
			}
		}
		fmt.Fprintf(w, `{"items":[%s]}`, strings.Join(items, ","))
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/machines/"):
		for _, m := range f.machines {
			if "/machines/"+m.id == path {
				fmt.Fprint(w, f.machineJSON(m))
				return
			}
		}
		f.problem(w, http.StatusNotFound, "not-found", "")
	case r.Method == http.MethodGet && path == "/drafts":
		if f.draft.id == "" {
			fmt.Fprint(w, `{"items":[]}`)
			return
		}
		fmt.Fprintf(w, `{"items":[%s]}`, f.draftJSON())
	case r.Method == http.MethodPost && path == "/drafts":
		var in apitypes.DraftInput
		_ = json.Unmarshal(body, &in)
		f.draft.id, f.draft.cluster, f.draft.title, f.draft.state, f.draft.rev = id.New(id.Draft), in.Cluster, in.Title, "open", 1
		w.Header().Set("ETag", f.etag())
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, f.draftJSON())
	case r.Method == http.MethodGet && f.draft.id != "" && path == "/drafts/"+f.draft.id:
		w.Header().Set("ETag", f.etag())
		fmt.Fprint(w, f.draftJSON())
	case r.Method == http.MethodPost && path == "/ingestions":
		f.startImport(w, r, body)
	case r.Method == http.MethodGet && path == "/ingestions":
		var items []string
		for _, i := range f.ings {
			if i.machine == r.URL.Query().Get("machine") {
				items = append(items, f.ingJSON(i))
			}
		}
		fmt.Fprintf(w, `{"items":[%s]}`, strings.Join(items, ","))
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/ingestions/"):
		for _, i := range f.ings {
			if "/ingestions/"+i.id == path {
				fmt.Fprint(w, f.ingJSON(i))
				return
			}
		}
		f.problem(w, http.StatusNotFound, "not-found", "")
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/operations/"):
		f.operation(w, r, strings.TrimPrefix(path, "/operations/"))
	default:
		f.problem(w, http.StatusNotFound, "not-found", "")
	}
}

func (f *apiFake) startImport(w http.ResponseWriter, r *http.Request, body []byte) {
	var in apitypes.IngestionInput
	_ = json.Unmarshal(body, &in)
	if r.Header.Get("If-Match") != f.etag() {
		f.problem(w, http.StatusPreconditionFailed, "precondition-failed", "")
		return
	}
	i := obIngestion{id: id.New(id.Ingestion), machine: in.Machine, draft: in.Draft, op: id.New(id.Operation)}
	f.starts++
	switch {
	case f.failImportAt != 0 && f.starts == f.failImportAt:
		i.state, i.events = "abandoned", [][2]string{{"started", `{"type":"started"}`}, {"failed", `{"type":"failed","code":"validation-failed"}`}}
		i.result = "FAILED"
	case f.pauseFirst && f.released == 0 && in.Review != nil && *in.Review:
		i.state, i.events = "paused", [][2]string{{"started", `{"type":"started"}`}, {"staged", `{"type":"staged"}`}, {"paused", `{"type":"paused","generation":1}`}}
		i.result = "RUNNING"
	default:
		f.draft.rev++
		i.state, i.events = "released", [][2]string{{"started", `{"type":"started"}`}, {"succeeded", `{"type":"succeeded"}`}}
		i.result = fmt.Sprintf(`{"draft":%q,"draftRevision":%d,"importBaseRevision":"ibr_OB"}`, in.Draft, f.draft.rev)
		f.released++
		if f.afterImport != nil {
			f.afterImport(f.released, f)
		}
	}
	f.ings = append(f.ings, i)
	w.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(w, `{"ingestion":%q,"operation":%q}`, i.id, i.op)
}

func (f *apiFake) operation(w http.ResponseWriter, r *http.Request, rest string) {
	op, events, _ := strings.Cut(rest, "/")
	var ing *obIngestion
	for k := range f.ings {
		if f.ings[k].op == op {
			ing = &f.ings[k]
		}
	}
	if ing == nil {
		f.problem(w, http.StatusNotFound, "not-found", "")
		return
	}
	if events == "events" {
		if r.Header.Get("Accept") == "text/event-stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			for n, e := range ing.events {
				fmt.Fprintf(w, "id: ep_OB-1:%d\nevent: %s\ndata: %s\n\n", n+1, e[0], e[1])
			}
			fmt.Fprint(w, ": keep-alive\n\n")
			return
		}
		var items []string
		for n, e := range ing.events {
			items = append(items, fmt.Sprintf(`{"id":"ep_OB-1:%d","at":"2026-11-01T06:00:00Z","type":%q,"data":%s}`, n+1, e[0], e[1]))
		}
		fmt.Fprintf(w, `{"items":[%s]}`, strings.Join(items, ","))
		return
	}
	state, result, problem := "succeeded", ing.result, "null"
	switch ing.result {
	case "FAILED":
		state, result, problem = "failed", "null", `{"type":"urn:bronzeward:problem:validation-failed","title":"OB","status":422}`
	case "RUNNING":
		state, result = "running", "null"
	}
	fmt.Fprintf(w, `{"id":%q,"kind":"ingest","state":%q,"subject":{"draft":%q,"draftRevision":1},"result":%s,"error":%s,`+
		`"createdAt":"2026-11-01T06:00:00Z","epoch":"ep_OB-1","lastEvent":%d}`, op, state, ing.draft, result, problem, len(ing.events))
}

// onboardEnv is one test's world.
type onboardEnv struct {
	t      *testing.T
	api    *apiFake
	file   string // the talosconfig
	result discover.Result
	calls  []string // "context|endpoint" of each discovery call
	given  [][]byte
}

var obTwoMembers = discover.Result{
	ClusterID: "talos-cluster-1", Version: "v1.14.5", Contract: "v1.14", Supported: true,
	KubernetesEndpoint: "https://10.0.0.1:6443",
	Members: []discover.Member{
		{NodeID: "node-1", Hostname: "cp-1", Address: "10.0.0.1", Role: "controlplane", SMBIOSUUID: "11111111-2222-3333-4444-555555555555", Platform: "metal", Answered: true},
		{NodeID: "node-2", Hostname: "w-1", Address: "10.0.0.2", Role: "worker", SMBIOSUUID: "66666666-7777-8888-9999-000000000000", Platform: "metal", Answered: true},
	},
}

func newOnboard(t *testing.T) *onboardEnv {
	t.Helper()
	e := &onboardEnv{t: t, api: &apiFake{roles: `{"kind":"human","principal":"h_OB","roles":["author"]}`}, result: obTwoMembers}
	api := httptest.NewServer(e.api)
	t.Cleanup(api.Close)
	automationContext(t, api.URL)
	e.file = filepath.Join(t.TempDir(), "talosconfig")
	if err := os.WriteFile(e.file, []byte(twoContextTalosconfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	old, oldMargin := discoverCluster, leaseMargin
	discoverCluster = func(_ context.Context, tc []byte, contextName, endpoint string) (discover.Result, error) {
		e.calls, e.given = append(e.calls, contextName+"|"+endpoint), append(e.given, tc)
		return e.result, nil
	}
	leaseMargin = 0
	t.Cleanup(func() { discoverCluster, leaseMargin = old, oldMargin })
	return e
}

// base is the flags every successful run carries.
func (e *onboardEnv) base() []string {
	return []string{"cluster", "onboard", "--talosconfig", e.file, "--name", "lab", "--kubernetes-endpoint", "proposed"}
}

func (e *onboardEnv) run(extra ...string) (int, string, string) {
	e.t.Helper()
	var o, er bytes.Buffer
	code := run(append(e.base(), extra...), &o, &er)
	return code, o.String(), er.String()
}

// runArgs runs exactly args.
func (e *onboardEnv) runArgs(args ...string) (int, string, string) {
	e.t.Helper()
	var o, er bytes.Buffer
	code := run(args, &o, &er)
	return code, o.String(), er.String()
}

// requests is every request any stand-in saw: nothing may have been sent when a usage error ends the command.
func (e *onboardEnv) requests() int {
	e.api.mu.Lock()
	defer e.api.mu.Unlock()
	return len(e.api.log) + len(e.calls)
}

// everything is every byte a command printed, with the request bodies the stand-ins kept.
func (e *onboardEnv) noneOf(marker string, outputs ...string) {
	e.t.Helper()
	for _, o := range outputs {
		if strings.Contains(o, marker) {
			e.t.Errorf("output holds %q: %q", marker, o)
		}
	}
}
