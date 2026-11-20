package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/client/apitypes"
	"github.com/ginsys/bronzeward/internal/talos/discover"
)

// The tests of `cluster onboard` (ginsys/bronzeward#833), each over the stand-ins of
// onboard_fake_test.go: a fake API server and a stub discovery.

// TestOnboardUsageErrorsSendNothing: a missing or refused flag is exit 2 and names the flag, before
// the identity read, any dial and any write. The API and the discovery record no request.
// Written first and red: the command did not exist.
func TestOnboardUsageErrorsSendNothing(t *testing.T) {
	e := newOnboard(t)
	key := filepath.Join(t.TempDir(), "machine-config")
	if err := os.WriteFile(key, []byte("machine: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	without := func(drop ...string) []string {
		var out []string
		skip := map[string]bool{}
		for _, d := range drop {
			skip[d] = true
		}
		base := e.base()
		for i := 0; i < len(base); i++ {
			if skip[base[i]] {
				if i+1 < len(base) && !strings.HasPrefix(base[i+1], "--") {
					i++
				}
				continue
			}
			out = append(out, base[i])
		}
		return out
	}
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"no talosconfig", without("--talosconfig"), "--talosconfig"},
		{"no name", without("--name"), "--name"},
		{"no kubernetes endpoint", without("--kubernetes-endpoint"), "--kubernetes-endpoint"},
		{"a kubernetes endpoint that is not https", append(without("--kubernetes-endpoint"), "--kubernetes-endpoint", "http://k8s.example:6443"), "https"},
		{"a first key kind without its file", append(e.base(), "--first-key-kind", "machine-config"), "--first-key-file"},
		{"a first key file without its kind", append(e.base(), "--first-key-file", key), "--first-key-kind"},
		{"a first key kind that is the default", append(e.base(), "--first-key-kind", "talosconfig", "--first-key-file", key), "--first-key-kind"},
		{"a first key kind that is unknown", append(e.base(), "--first-key-kind", "bogus", "--first-key-file", key), "--first-key-kind"},
		{"skip-first-key with a first key file", append(e.base(), "--skip-first-key", "--first-key-kind", "machine-config", "--first-key-file", key), "--skip-first-key"},
		{"a first key file that cannot be read", append(e.base(), "--first-key-kind", "machine-config", "--first-key-file", key+"-absent"), "--first-key-file"},
		{"an unknown talos context", append(e.base(), "--talos-context", "nope"), "--talos-context"},
		{"review with transient staging", append(e.base(), "--review", "--staging", "transient"), "--review"},
		{"a bad staging", append(e.base(), "--staging", "bogus"), "--staging"},
		{"no-wait", append(e.base(), "--no-wait"), "--no-wait"},
		{"idempotency key", append(e.base(), "--idempotency-key", "k"), "--idempotency-key"},
		{"expect epoch", append(e.base(), "--expect-epoch", "x"), "--expect-epoch"},
		{"expect principal", append(e.base(), "--expect-principal", "x"), "--expect-principal"},
		{"if-match", append(e.base(), "--if-match", "x"), "--if-match"},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, out, errOut := e.runArgs(c.args...)
			if code != 2 || !strings.Contains(errOut, c.want) || e.requests() != 0 {
				t.Errorf("exit %d, stdout %q, stderr %q, %d requests; want exit 2 naming %q and no request", code, out, errOut, e.requests(), c.want)
			}
		})
	}
}

// TestOnboardFullRun: a two-member cluster is recorded, the first key is sent once with the exact
// document, to the first control-plane member's address, after the machines and before the draft; one
// draft is created and both imports are released.
func TestOnboardFullRun(t *testing.T) {
	e := newOnboard(t)
	code, out, errOut := e.run()
	if code != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	a := e.api
	if len(a.clusters) != 1 || len(a.machines) != 2 || a.draft.title != "onboard lab" || len(a.ings) != 2 {
		t.Fatalf("recorded %d clusters, %d machines, draft %q, %d imports", len(a.clusters), len(a.machines), a.draft.title, len(a.ings))
	}
	cl := a.clusters[0]
	if cl.Name != "lab" || cl.Endpoint != "https://10.0.0.1:6443" || cl.Contract != "v1.14" || cl.TalosClusterId != "talos-cluster-1" {
		t.Errorf("the cluster was recorded as %+v", cl)
	}
	want := []string{"POST /clusters", "POST /machines", "POST /machines", "POST /clusters/" + cl.Id + "/first-keys", "POST /drafts", "POST /ingestions", "POST /ingestions"}
	if got := a.writes(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("writes %q; want %q", got, want)
	}
	if !strings.Contains(a.posts[1], `"talosEndpoint":"10.0.0.1"`) || !strings.Contains(a.posts[1], `"smbiosUuid":"11111111-2222-3333-4444-555555555555"`) {
		t.Errorf("the first machine was sent as %q", a.posts[1])
	}
	// One first key: the talosconfig cut to its current context, tried at the control-plane member's address.
	doc, err := discover.OneContext([]byte(twoContextTalosconfig()), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.keys) != 1 || a.keys[0].in.Kind != "talosconfig" || a.keys[0].in.Endpoint != "10.0.0.1" || a.keys[0].in.Document != string(doc) {
		t.Fatalf("first keys sent %+v; want one talosconfig, the one context, for 10.0.0.1", a.keys)
	}
	if len(e.calls) != 1 || e.calls[0] != "|" {
		t.Errorf("discovery calls %q; want one, with no context and no endpoint", e.calls)
	}
	for _, line := range []string{"Cluster " + cl.Id + ": created", "First key: stored", "Draft " + a.draft.id + " (onboard lab)"} {
		if !strings.Contains(out, line) {
			t.Errorf("stdout lacks %q:\n%s", line, out)
		}
	}
	for _, m := range a.machines {
		if !strings.Contains(out, m.id) {
			t.Errorf("stdout lacks the machine %s:\n%s", m.id, out)
		}
	}
	if strings.Count(out, "released") != 2 || !strings.Contains(out, "Next:") {
		t.Errorf("stdout lacks two released imports and a next step:\n%s", out)
	}
	for _, marker := range []string{b64(plantedKeyA), b64(plantedKeyB), plantedKeyA} {
		e.noneOf(marker, out, errOut)
	}
}

// TestOnboardJSONIsOneObject: -o json prints one object, once, at the end, with the cluster, the
// first key, the draft and each machine with its add and its import. The 202 answer of an import never
// reaches standard output.
func TestOnboardJSONIsOneObject(t *testing.T) {
	e := newOnboard(t)
	code, out, errOut := e.run("-o", "json")
	if code != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var obj onboarding
	if err := dec.Decode(&obj); err != nil {
		t.Fatalf("stdout is not one object: %v\n%s", err, out)
	}
	if _, err := dec.Token(); err != io.EOF {
		t.Errorf("stdout holds more than one value:\n%s", out)
	}
	if strings.Contains(out, `"ingestion"`) || strings.Contains(out, `"operation"`) {
		t.Errorf("an import's 202 answer reached stdout:\n%s", out)
	}
	if obj.Cluster.State != "created" || obj.Cluster.ID != e.api.clusters[0].Id || obj.FirstKey.State != "stored" || obj.Draft == nil || obj.Draft.ID != e.api.draft.id {
		t.Errorf("object %+v", obj)
	}
	if len(obj.Machines) != 2 {
		t.Fatalf("%d machines in the object", len(obj.Machines))
	}
	for i, m := range obj.Machines {
		if m.Node != obTwoMembers.Members[i].NodeID || m.Machine != e.api.machines[i].id || m.Add.State != "created" || m.Import.State != "released" || m.Import.Command != "" || m.Import.Error != "" {
			t.Errorf("machine %d: %+v", i, m)
		}
	}
	if !strings.Contains(out, `"firstKey"`) {
		t.Errorf("the object has no firstKey step:\n%s", out)
	}
	for _, marker := range []string{b64(plantedKeyA), b64(plantedKeyB)} {
		e.noneOf(marker, out, errOut)
	}
}

// TestOnboardUsesTheChosenContext: without --talos-context the run uses the file's current context A;
// with it, B. Either way the first key holds one context, with the chosen name, and no byte of the other
// context's key.
func TestOnboardUsesTheChosenContext(t *testing.T) {
	for _, c := range []struct {
		flag, ctx, keep, drop string
	}{
		{"", "a", plantedKeyA, plantedKeyB},
		{"b", "b", plantedKeyB, plantedKeyA},
	} {
		t.Run("context "+c.ctx, func(t *testing.T) {
			e := newOnboard(t)
			args := []string{}
			if c.flag != "" {
				args = append(args, "--talos-context", c.flag)
			}
			if code, out, errOut := e.run(args...); code != 0 {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
			}
			if len(e.calls) != 1 || e.calls[0] != c.flag+"|" {
				t.Errorf("discovery calls %q; want one with context %q", e.calls, c.flag)
			}
			if len(e.api.keys) != 1 {
				t.Fatalf("%d first keys sent, want one", len(e.api.keys))
			}
			doc := e.api.keys[0].in.Document
			if !strings.HasPrefix(doc, "context: "+c.ctx+"\n") || strings.Count(doc, "ca: ") != 1 || !strings.Contains(doc, b64(c.keep)) {
				t.Errorf("the sent document is not one context %q with its key:\n%s", c.ctx, doc)
			}
			e.noneOf(b64(c.drop), e.api.posts...)
			e.noneOf(c.drop, e.api.posts...)
		})
	}
}

// TestOnboardSecondRunWritesNothingNew: after a full run, a second one with --skip-first-key changes no
// state, sends no first key, creates no draft and starts no import. Its cluster and machine POSTs are
// each answered 409, which is how it finds what exists; the flags are not applied to the existing
// cluster.
func TestOnboardSecondRunWritesNothingNew(t *testing.T) {
	e := newOnboard(t)
	if code, out, errOut := e.run(); code != 0 {
		t.Fatalf("first run: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	before := len(e.api.writes())
	rev, ings, keys := e.api.draft.rev, len(e.api.ings), len(e.api.keys)
	args := []string{"cluster", "onboard", "--talosconfig", e.file, "--name", "another-name", "--kubernetes-endpoint", "https://elsewhere:6443", "--skip-first-key"}
	code, out, errOut := e.runArgs(args...)
	if code != 0 {
		t.Fatalf("second run: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	later := e.api.writes()[before:]
	if strings.Join(later, ",") != "POST /clusters,POST /machines,POST /machines" {
		t.Errorf("second run's writes %q; want the three refused creates only", later)
	}
	if len(e.api.clusters) != 1 || len(e.api.machines) != 2 || e.api.draft.rev != rev || len(e.api.ings) != ings || len(e.api.keys) != keys {
		t.Errorf("the second run changed state: %d clusters, %d machines, draft revision %d, %d imports, %d first keys", len(e.api.clusters), len(e.api.machines), e.api.draft.rev, len(e.api.ings), len(e.api.keys))
	}
	for _, want := range []string{"existing", "lab", "skipped", "https://10.0.0.1:6443", "(onboard lab): existing"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "another-name") {
		t.Errorf("the flags were applied to the existing cluster:\n%s", out)
	}
}

// TestOnboardFirstKey: the first key is the step after the machines and before the draft, sent to the
// route of the recorded cluster once; --skip-first-key sends no first-key request at all and the rest of
// the run is the same.
func TestOnboardFirstKey(t *testing.T) {
	for _, skip := range []bool{false, true} {
		e := newOnboard(t)
		var extra []string
		if skip {
			extra = append(extra, "--skip-first-key")
		}
		code, out, errOut := e.run(extra...)
		if code != 0 {
			t.Fatalf("skip %v: exit %d, stdout %q, stderr %q", skip, code, out, errOut)
		}
		var sawKey, sawDraft bool
		for _, w := range e.api.writes() {
			switch {
			case strings.HasSuffix(w, "/first-keys"):
				sawKey = true
				if sawDraft || w != "POST /clusters/"+e.api.clusters[0].Id+"/first-keys" {
					t.Errorf("skip %v: first key %q sent after the draft or to another cluster", skip, w)
				}
			case w == "POST /drafts":
				sawDraft = true
			}
		}
		if sawKey == skip || len(e.api.keys) != map[bool]int{false: 1, true: 0}[skip] || len(e.api.ings) != 2 {
			t.Errorf("skip %v: %d first keys, %d imports; want %d and 2", skip, len(e.api.keys), len(e.api.ings), map[bool]int{false: 1, true: 0}[skip])
		}
	}
}

// TestOnboardRerunReplacesTheFirstKey: a rerun with the default flags supplies the first key again, the
// route answers that it replaced one, and the command says so; the draft and the imports of the first
// run stand and nothing is created twice.
func TestOnboardRerunReplacesTheFirstKey(t *testing.T) {
	e := newOnboard(t)
	if code, out, errOut := e.run(); code != 0 {
		t.Fatalf("first run: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	ings, rev := len(e.api.ings), e.api.draft.rev
	code, out, errOut := e.run()
	if code != 0 || len(e.api.keys) != 2 || len(e.api.ings) != ings || e.api.draft.rev != rev {
		t.Errorf("rerun: exit %d, %d first keys, %d imports, draft revision %d; want 0, 2, %d and %d", code, len(e.api.keys), len(e.api.ings), e.api.draft.rev, ings, rev)
	}
	if !strings.Contains(out, "First key: replaced") {
		t.Errorf("stdout lacks the replaced first key:\n%s%s", out, errOut)
	}
}

// TestOnboardOtherFirstKey: --first-key-kind and --first-key-file send that document instead of the
// talosconfig, as that kind, to the same member address; the rerun command a stopped run prints names
// the pair.
func TestOnboardOtherFirstKey(t *testing.T) {
	e := newOnboard(t)
	const planted = "machine: {token: PLANTED-MACHINE-CONFIG}\n"
	key := filepath.Join(t.TempDir(), "controlplane.yaml")
	if err := os.WriteFile(key, []byte(planted), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := e.run("--first-key-kind", "machine-config", "--first-key-file", key); code != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if len(e.api.keys) != 1 || e.api.keys[0].in.Kind != "machine-config" || e.api.keys[0].in.Document != planted || e.api.keys[0].in.Endpoint != "10.0.0.1" {
		t.Fatalf("first keys sent %+v; want the machine configuration for 10.0.0.1", e.api.keys)
	}
	// A run stopped after the first key's refusal names the pair in its rerun command, not the default.
	e2 := newOnboard(t)
	e2.api.keyAnswer = func(_ int, w http.ResponseWriter) {
		refuse(http.StatusUnprocessableEntity, "validation-failed", `,"rule":"first-key-unusable"`)(0, w, nil)
	}
	_, out, _ := e2.run("--first-key-kind", "machine-config", "--first-key-file", key)
	if !strings.Contains(out, "--first-key-kind machine-config --first-key-file '"+key+"'") {
		t.Errorf("the rerun command lacks the first key pair:\n%s", out)
	}
}

// TestOnboardFirstKeySaveFails: a first key the server stored stays stored in the report when keeping
// what the answers taught the context fails afterwards, as plan create's plan does: the run still ends
// with the error, and its rerun command skips the key, which a repeat would replace.
func TestOnboardFirstKeySaveFails(t *testing.T) {
	for _, c := range []struct{ name, body string }{
		{"a readable answer", `{"cluster":"cl_x","kind":"talosconfig","replaced":false,"caKeyVersion":{"version":1,"createdTime":"2026-11-19T21:00:00Z"}}`},
		{"an answer that is not JSON", `{`},
	} {
		e := newOnboard(t)
		e.api.keyAnswer = func(_ int, w http.ResponseWriter) {
			if err := os.WriteFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "bronzeward", "config.yaml"), []byte("{"), 0o600); err != nil {
				t.Error(err)
			}
			w.Header().Set("Bronzeward-Epoch", "ep_OB-2")
			_, _ = io.WriteString(w, c.body)
		}
		code, out, errOut := e.run()
		if code != 1 || len(e.api.keys) != 1 || !strings.Contains(out, "First key: stored") || !strings.Contains(out, "--skip-first-key") || !strings.Contains(errOut, "keeping the epoch") {
			t.Errorf("%s: exit %d, %d first keys, stdout %q, stderr %q; want 1, 1, the key stored, --skip-first-key in the rerun and the save failure reported", c.name, code, len(e.api.keys), out, errOut)
		}
	}
}

// TestOnboardFirstKeyAnswers: each class of answer of the first-key route. A refusal the route decides
// before it stores anything is exit 1, naming the rule and nothing of the server's words; every other
// answer leaves the outcome unknown, exit 3. Exactly one first key is sent, nothing after it runs, and
// the cluster and the machines stay recorded.
func TestOnboardFirstKeyAnswers(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer func(int, http.ResponseWriter, *http.Request)
		code   int
		state  string
		says   string
	}{
		{"a refusing rule", refuse(http.StatusUnprocessableEntity, "validation-failed", `,"rule":"first-key-no-ca-key"`), 1, "failed", "first-key-no-ca-key"},
		{"a key that could not be tried", refuse(http.StatusServiceUnavailable, "talos-access-unavailable", ""), 1, "failed", "could not be tried"},
		{"a refused role", refuse(http.StatusForbidden, "forbidden", ""), 1, "failed", "forbidden"},
		{"a 500", refuse(http.StatusInternalServerError, "internal-error", ""), 3, "unknown", "outcome unknown"},
		{"a conflict of a code the client does not know", refuse(http.StatusConflict, "BWSYNTH-new-error", ""), 3, "unknown", "outcome unknown"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newOnboard(t)
			e.api.keyAnswer = func(n int, w http.ResponseWriter) { c.answer(n, w, nil) }
			code, out, errOut := e.run("-o", "json")
			if code != c.code || !strings.Contains(out+errOut, c.says) {
				t.Errorf("exit %d, stdout %q, stderr %q; want %d and %q", code, out, errOut, c.code, c.says)
			}
			var obj onboarding
			if err := json.Unmarshal([]byte(out), &obj); err != nil {
				t.Fatalf("stdout is not the object: %v\n%s", err, out)
			}
			if obj.FirstKey.State != c.state || obj.FirstKey.Command == "" || obj.FirstKey.Error == "" || obj.Draft != nil {
				t.Errorf("first key %+v, draft %v; want state %q with its command and error, and no draft", obj.FirstKey, obj.Draft, c.state)
			}
			if len(e.api.keys) != 1 || e.api.draft.id != "" || len(e.api.ings) != 0 || len(e.api.clusters) != 1 || len(e.api.machines) != 2 {
				t.Errorf("%d first keys, draft %q, %d imports, %d clusters, %d machines", len(e.api.keys), e.api.draft.id, len(e.api.ings), len(e.api.clusters), len(e.api.machines))
			}
			for _, marker := range []string{b64(plantedKeyA), b64(plantedKeyB), fkPlanted} {
				e.noneOf(marker, out, errOut)
			}
		})
	}
}

// TestOnboardStopsWhenNoControlPlaneAnswered: a cluster whose control-plane members did not answer has no
// address for the first key, so the run stops at discovery with exit 1 and sends nothing.
func TestOnboardStopsWhenNoControlPlaneAnswered(t *testing.T) {
	e := newOnboard(t)
	r := obTwoMembers
	r.Members = append([]discover.Member(nil), obTwoMembers.Members...)
	r.Members[0] = discover.Member{NodeID: "node-1", Hostname: "cp-1", Address: "10.0.0.1", Role: "controlplane"}
	e.result = r
	code, out, errOut := e.run()
	if code != 1 || len(e.api.writes()) != 0 || len(e.api.keys) != 0 || !strings.Contains(out+errOut, "no control-plane member answered") {
		t.Errorf("exit %d, writes %q, stdout %q, stderr %q; want 1, no write and the reason", code, e.api.writes(), out, errOut)
	}
}

// TestOnboardRerunAfterAFailure: a failure injected at each of four steps, then a rerun, ends in the
// state of an uninterrupted run.
func TestOnboardRerunAfterAFailure(t *testing.T) {
	state := func(e *onboardEnv) string {
		released := 0
		for _, i := range e.api.ings {
			if i.state == "released" {
				released++
			}
		}
		return strings.Join([]string{strconv.Itoa(len(e.api.clusters)), strconv.Itoa(len(e.api.machines)), e.api.draft.title, strconv.Itoa(e.api.draft.rev), strconv.Itoa(released)}, "/")
	}
	clean := newOnboard(t)
	if code, out, errOut := clean.run(); code != 0 {
		t.Fatalf("uninterrupted run: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	want := state(clean)
	for _, c := range []struct {
		name   string
		inject func(*onboardEnv)
		fix    func(*onboardEnv)
	}{
		{"after the cluster", func(e *onboardEnv) { e.api.failPost = map[string]int{"/machines": 1} }, func(e *onboardEnv) { e.api.failPost = nil }},
		{"after the first machine", func(e *onboardEnv) { e.api.failPost = map[string]int{"/machines": 2} }, func(e *onboardEnv) { e.api.failPost = nil }},
		{"the first key refused", func(e *onboardEnv) {
			e.api.keyAnswer = func(n int, w http.ResponseWriter) {
				refuse(http.StatusUnprocessableEntity, "validation-failed", `,"rule":"first-key-unusable"`)(n, w, nil)
			}
		}, func(e *onboardEnv) { e.api.keyAnswer = nil }},
		{"the first key lost", func(e *onboardEnv) {
			e.api.keyAnswer = func(n int, w http.ResponseWriter) {
				refuse(http.StatusInternalServerError, "internal-error", "")(n, w, nil)
			}
		}, func(e *onboardEnv) { e.api.keyAnswer = nil }},
		{"the second import failed", func(e *onboardEnv) { e.api.failImportAt = 2 }, func(e *onboardEnv) { e.api.failImportAt = 0 }},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newOnboard(t)
			c.inject(e)
			if code, out, errOut := e.run(); code == 0 {
				t.Fatalf("the injected failure did not stop the run: stdout %q, stderr %q", out, errOut)
			}
			c.fix(e)
			// The rerun supplies the first key again where the first run did not store one, and says
			// --skip-first-key where it stored it: the second state is that of an uninterrupted run too.
			args := e.base()
			if len(e.api.keys) > 0 && c.name == "the second import failed" {
				args = append(args, "--skip-first-key")
			}
			if code, out, errOut := e.runArgs(args...); code != 0 {
				t.Fatalf("rerun: exit %d, stdout %q, stderr %q", code, out, errOut)
			}
			if got := state(e); got != want {
				t.Errorf("state after the rerun %q; want %q, that of the uninterrupted run", got, want)
			}
		})
	}
}

// TestOnboardMachineInAnotherCluster: a machine already recorded in another cluster stops the command,
// names both cluster IDs, and leaves the first key and every later step undone.
func TestOnboardMachineInAnotherCluster(t *testing.T) {
	e := newOnboard(t)
	other := "cl_" + strings.Repeat("0", 26)
	e.api.machines = append(e.api.machines, obMachine{id: "mch_" + strings.Repeat("1", 26), cluster: other, key: "11111111-2222-3333-4444-555555555555", platform: "metal", endpoint: "10.0.0.1"})
	code, out, errOut := e.run()
	if code != 1 || !strings.Contains(out+errOut, other) || !strings.Contains(out+errOut, e.api.clusters[0].Id) {
		t.Errorf("exit %d, stdout %q, stderr %q; want 1 naming %s and %s", code, out, errOut, other, e.api.clusters[0].Id)
	}
	if len(e.api.keys) != 0 || e.api.draft.id != "" || len(e.api.ings) != 0 {
		t.Errorf("a later step ran: %d first keys, draft %q, %d imports", len(e.api.keys), e.api.draft.id, len(e.api.ings))
	}
}

// TestOnboardSendsNothingForAnAuthorlessCaller: a caller who is not a person with the author role gets
// exit 1 that names the role, and nothing is written or dialled.
func TestOnboardSendsNothingForAnAuthorlessCaller(t *testing.T) {
	for _, roles := range []string{`{"kind":"human","principal":"h_OB","roles":["publisher"]}`, `{"kind":"service","principal":"svc_OB","roles":["author"]}`} {
		e := newOnboard(t)
		e.api.roles = roles
		code, out, errOut := e.run()
		if code != 1 || !strings.Contains(out+errOut, "author") || len(e.api.writes()) != 0 || len(e.calls) != 0 {
			t.Errorf("roles %s: exit %d, stdout %q, stderr %q, writes %q, dials %d", roles, code, out, errOut, e.api.writes(), len(e.calls))
		}
	}
}

// TestOnboardStopsBeforeSendingWhenTheClusterCannotBeRecorded: a contract the machinery does not
// support, and a proposed Kubernetes endpoint that is missing or not https, are exit 1 and send nothing.
func TestOnboardStopsBeforeSendingWhenTheClusterCannotBeRecorded(t *testing.T) {
	for name, edit := range map[string]func(*discover.Result){
		"unsupported contract": func(r *discover.Result) { r.Supported = false },
		"no proposal":          func(r *discover.Result) { r.KubernetesEndpoint = "" },
		"proposal not https":   func(r *discover.Result) { r.KubernetesEndpoint = "http://10.0.0.1:6443" },
	} {
		t.Run(name, func(t *testing.T) {
			e := newOnboard(t)
			edit(&e.result)
			code, out, errOut := e.run()
			if code != 1 || len(e.api.writes()) != 0 {
				t.Errorf("exit %d, stdout %q, stderr %q, writes %q", code, out, errOut, e.api.writes())
			}
		})
	}
}

// TestOnboardKubernetesEndpointGiven: an https URL is sent as it is, in place of the proposal.
func TestOnboardKubernetesEndpointGiven(t *testing.T) {
	e := newOnboard(t)
	args := append(e.base()[:6:6], "--kubernetes-endpoint", "https://k8s.lab.example:6443")
	if code, out, errOut := e.runArgs(args...); code != 0 || e.api.clusters[0].Endpoint != "https://k8s.lab.example:6443" {
		t.Errorf("exit %d, stdout %q, stderr %q, endpoint %q", code, out, errOut, e.api.clusters[0].Endpoint)
	}
}

// TestOnboardMembersThatDidNotAnswer: a member that did not answer, or reported no platform, is listed as
// not recorded with the reason, and gets no machine and no import.
func TestOnboardMembersThatDidNotAnswer(t *testing.T) {
	e := newOnboard(t)
	r := obTwoMembers
	r.Members = append([]discover.Member(nil), obTwoMembers.Members...)
	r.Members[1] = discover.Member{NodeID: "node-2", Hostname: "w-1", Address: "10.0.0.2", Role: "worker"}
	e.result = r
	code, out, errOut := e.run()
	if code != 0 || len(e.api.machines) != 1 || len(e.api.ings) != 1 {
		t.Errorf("exit %d, %d machines, %d imports, stdout %q, stderr %q", code, len(e.api.machines), len(e.api.ings), out, errOut)
	}
	if !strings.Contains(out, "not recorded") || !strings.Contains(out, "did not answer") {
		t.Errorf("stdout lacks the not-recorded member:\n%s", out)
	}
}

// TestOnboardNoImport: --no-import stops after the first key, with no draft and no import.
func TestOnboardNoImport(t *testing.T) {
	e := newOnboard(t)
	code, out, errOut := e.run("--no-import")
	if code != 0 || e.api.draft.id != "" || len(e.api.ings) != 0 || len(e.api.keys) != 1 {
		t.Errorf("exit %d, draft %q, %d imports, %d first keys, stdout %q, stderr %q", code, e.api.draft.id, len(e.api.ings), len(e.api.keys), out, errOut)
	}
}

// TestOnboardReviewPause: with --review the first import pauses, the command exits 0 and prints the
// review, continue and rerun commands. A rerun before the continue stops at that paused import; after the
// continue, a rerun imports the next machine only.
func TestOnboardReviewPause(t *testing.T) {
	e := newOnboard(t)
	e.api.pauseFirst = true
	code, out, errOut := e.run("--review")
	if code != 0 || len(e.api.ings) != 1 || e.api.ings[0].state != "paused" {
		t.Fatalf("exit %d, %d imports, stdout %q, stderr %q; want 0 and one paused import", code, len(e.api.ings), out, errOut)
	}
	paused := e.api.ings[0].id
	for _, want := range []string{"import review " + paused, "import continue " + paused, "cluster onboard", "paused"} {
		if !strings.Contains(strings.ReplaceAll(out, "'", ""), want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	skip := []string{"cluster", "onboard", "--talosconfig", e.file, "--name", "lab", "--kubernetes-endpoint", "proposed", "--skip-first-key", "--review"}
	if code, out, errOut := e.runArgs(skip...); code != 0 || len(e.api.ings) != 1 || !strings.Contains(out, "import status") {
		t.Errorf("rerun before the continue: exit %d, %d imports, stdout %q, stderr %q; want a stop at the paused import", code, len(e.api.ings), out, errOut)
	}
	// The operator continues it: the import is released into the draft.
	e.api.mu.Lock()
	e.api.ings[0].state = "released"
	e.api.draft.rev++
	e.api.released++
	e.api.mu.Unlock()
	if code, out, errOut := e.runArgs(skip...); code != 0 || len(e.api.ings) != 2 || e.api.ings[1].machine != e.api.machines[1].id {
		t.Errorf("rerun after the continue: exit %d, %d imports, stdout %q, stderr %q; want the second machine imported only", code, len(e.api.ings), out, errOut)
	}
}

// TestOnboardDraftMovedBetweenImports: a write to the draft after the first import stops the command with
// no retry: the second machine is not imported, and the kept ETag is not taken over that edit.
func TestOnboardDraftMovedBetweenImports(t *testing.T) {
	e := newOnboard(t)
	e.api.afterImport = func(n int, f *apiFake) {
		if n == 1 {
			f.draft.rev++
		}
	}
	code, out, errOut := e.run()
	if code != 1 || len(e.api.ings) != 1 || !strings.Contains(out+errOut, "another write followed") {
		t.Errorf("exit %d, %d imports, stdout %q, stderr %q; want 1, one import and the other write named", code, len(e.api.ings), out, errOut)
	}
	if !strings.Contains(out, "not-run") {
		t.Errorf("stdout does not show the second import as not-run:\n%s", out)
	}
}

// TestOnboardFailedImportStops: an import that ends failed stops the command (exit 4) and the later
// machines are not-run.
func TestOnboardFailedImportStops(t *testing.T) {
	e := newOnboard(t)
	e.api.failImportAt = 1
	code, out, errOut := e.run()
	if code != 4 || len(e.api.ings) != 1 || !strings.Contains(out, "not-run") {
		t.Errorf("exit %d, %d imports, stdout %q, stderr %q; want 4, one import and a not-run import", code, len(e.api.ings), out, errOut)
	}
}

// TestOnboardHeldImportStops: a machine whose import into the draft is held stops the command with exit 1
// and names import status.
func TestOnboardHeldImportStops(t *testing.T) {
	e := newOnboard(t)
	if code, out, errOut := e.run("--no-import"); code != 0 {
		t.Fatalf("setup: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	e.api.mu.Lock()
	e.api.draft.id, e.api.draft.cluster, e.api.draft.title, e.api.draft.state, e.api.draft.rev = "drf_"+strings.Repeat("2", 26), e.api.clusters[0].Id, "onboard lab", "open", 1
	e.api.ings = append(e.api.ings, obIngestion{id: "ing_" + strings.Repeat("3", 26), machine: e.api.machines[0].id, draft: e.api.draft.id, op: "op_" + strings.Repeat("4", 26), state: "held"})
	e.api.mu.Unlock()
	args := []string{"cluster", "onboard", "--talosconfig", e.file, "--name", "lab", "--kubernetes-endpoint", "proposed", "--skip-first-key"}
	code, out, errOut := e.runArgs(args...)
	if code != 1 || len(e.api.ings) != 1 || !strings.Contains(out+errOut, "import status") {
		t.Errorf("exit %d, %d imports, stdout %q, stderr %q; want 1, no new import and import status named", code, len(e.api.ings), out, errOut)
	}
}

// TestOnboardActiveImportOutranksAnEarlierRelease: a machine with a released import and a newer held one
// into the same draft stops the run, as the held one alone does.
func TestOnboardActiveImportOutranksAnEarlierRelease(t *testing.T) {
	e := newOnboard(t)
	if code, out, errOut := e.run("--no-import"); code != 0 {
		t.Fatalf("setup: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	e.api.mu.Lock()
	e.api.draft.id, e.api.draft.cluster, e.api.draft.title, e.api.draft.state, e.api.draft.rev = "drf_"+strings.Repeat("2", 26), e.api.clusters[0].Id, "onboard lab", "open", 1
	for i, state := range []string{"released", "held"} {
		e.api.ings = append(e.api.ings, obIngestion{id: "ing_" + strings.Repeat(string(rune('3'+i)), 26), machine: e.api.machines[0].id, draft: e.api.draft.id, op: "op_" + strings.Repeat("5", 26), state: state})
	}
	e.api.mu.Unlock()
	args := []string{"cluster", "onboard", "--talosconfig", e.file, "--name", "lab", "--kubernetes-endpoint", "proposed", "--skip-first-key"}
	code, out, errOut := e.runArgs(args...)
	if code != 1 || len(e.api.ings) != 2 || !strings.Contains(out+errOut, "import status") {
		t.Errorf("exit %d, %d imports, stdout %q, stderr %q; want 1, no new import and import status named", code, len(e.api.ings), out, errOut)
	}
}

// TestOnboardRecoveryCommandsKeepTheirFlags: the commands a stopped run prints reach the server the run
// did (the global --context), and a recovery command keeps --skip-first-key where the run was given it.
func TestOnboardRecoveryCommandsKeepTheirFlags(t *testing.T) {
	e := newOnboard(t)
	e.api.pauseFirst = true
	code, out, errOut := e.run("--review", "--context", "lab")
	if code != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	for _, cmd := range []string{"import status", "import review", "import continue", "cluster onboard"} {
		if !strings.Contains(out, "bronzewardctl --context 'lab' "+cmd) {
			t.Errorf("no %q command with the context in:\n%s", cmd, out)
		}
	}
	// A run that completes names the context in its last line too.
	e3 := newOnboard(t)
	if code, out, errOut := e3.run("--context", "lab"); code != 0 || !strings.Contains(out, "bronzewardctl --context 'lab' draft show") {
		t.Errorf("exit %d, stdout %q, stderr %q; want the draft show command with the context", code, out, errOut)
	}
	e2 := newOnboard(t)
	e2.api.failPost = map[string]int{"/machines": 1}
	args := []string{"cluster", "onboard", "--talosconfig", e2.file, "--name", "lab", "--kubernetes-endpoint", "proposed", "--skip-first-key"}
	code, out, errOut = e2.runArgs(args...)
	if code == 0 || !strings.Contains(out, "--skip-first-key") {
		t.Errorf("exit %d, stdout %q, stderr %q; want a failure whose recovery command keeps --skip-first-key", code, out, errOut)
	}
}

// TestOnboardRefusesBeforeAnyRequest: a context with no endpoint and none given, and a file name that
// cannot be printed back in a command, are exit 2 before the identity read.
func TestOnboardRefusesBeforeAnyRequest(t *testing.T) {
	e := newOnboard(t)
	noEndpoint := strings.ReplaceAll(twoContextTalosconfig(), "endpoints: [10.9.9.9:50000]", "endpoints: []")
	if err := os.WriteFile(e.file, []byte(noEndpoint), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := e.run(); code != 2 || !strings.Contains(errOut, "--endpoint") || e.requests() != 0 {
		t.Errorf("no endpoint: exit %d, stdout %q, stderr %q, %d requests", code, out, errOut, e.requests())
	}
	// The endpoint check reads the context the run uses: --talos-context b, not the file's current a.
	oneEmpty := strings.Replace(twoContextTalosconfig(), "endpoints: [10.9.9.9:50000]", "endpoints: []", 1) // context a only
	if err := os.WriteFile(e.file, []byte(oneEmpty), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := e.run("--talos-context", "b"); code != 0 {
		t.Errorf("context b has an endpoint: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	before := e.requests()
	tabbed := filepath.Join(filepath.Dir(e.file), "talos\tconfig")
	if err := os.WriteFile(tabbed, []byte(twoContextTalosconfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"cluster", "onboard", "--talosconfig", tabbed}, e.base()[4:]...)
	if code, out, errOut := e.runArgs(args...); code != 2 || !strings.Contains(errOut, "rename the file") || e.requests() != before {
		t.Errorf("unprintable name: exit %d, stdout %q, stderr %q, %d new requests", code, out, errOut, e.requests()-before)
	}
}

// TestOnboardPrintedCommandsAreShell: no printed command line carries text after the command, so copying
// a line whole into a shell runs it.
func TestOnboardPrintedCommandsAreShell(t *testing.T) {
	e := newOnboard(t)
	e.api.pauseFirst = true
	code, out, errOut := e.run("--review")
	if code != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "bronzewardctl ") && strings.Contains(l, " (") {
			t.Errorf("a command line carries text after the command: %q", l)
		}
	}
}

// TestOnboardHeldImportErrorKeepsTheContext: the error text that names import status carries the
// global --context, like every printed command.
func TestOnboardHeldImportErrorKeepsTheContext(t *testing.T) {
	e := newOnboard(t)
	if code, out, errOut := e.run("--no-import"); code != 0 {
		t.Fatalf("setup: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	e.api.mu.Lock()
	e.api.draft.id, e.api.draft.cluster, e.api.draft.title, e.api.draft.state, e.api.draft.rev = "drf_"+strings.Repeat("2", 26), e.api.clusters[0].Id, "onboard lab", "open", 1
	e.api.ings = append(e.api.ings, obIngestion{id: "ing_" + strings.Repeat("3", 26), machine: e.api.machines[0].id, draft: e.api.draft.id, op: "op_" + strings.Repeat("4", 26), state: "held"})
	e.api.mu.Unlock()
	args := []string{"cluster", "onboard", "--talosconfig", e.file, "--name", "lab", "--kubernetes-endpoint", "proposed", "--skip-first-key", "--context", "lab"}
	_, out, errOut := e.runArgs(args...)
	if !strings.Contains(errOut, "bronzewardctl --context 'lab' import status") {
		t.Errorf("stderr %q (stdout %q) names import status without the context", errOut, out)
	}
}

// TestOnboardSettleAfterASaveFailure: an import whose follow ended well or paused but whose context could
// not be saved (the afterEnd class classify reads as done) is settled by reading the ingestion, never
// assumed released. The class is the input the branch decides on, so the test builds it.
func TestOnboardSettleAfterASaveFailure(t *testing.T) {
	if got := classify(saveFailed{afterEnd{errors.New("disk full")}}, true); got != stateDone {
		t.Fatalf("a save failure after an accepted import classifies as %q, not done: the case no longer reaches the branch", got)
	}
	id := "ing_" + strings.Repeat("3", 26)
	for _, c := range []struct {
		name, state, command string
		read                 error
		want                 string
	}{
		{"released", "released", "", nil, obReleased},
		{"paused", "paused", "bronzewardctl --context 'lab' import status", nil, statePaused.s()},
		{"another state", "held", "bronzewardctl --context 'lab' import status", nil, stateUnknown.s()},
		{"read failed", "", "bronzewardctl --context 'lab' import status", errors.New("no answer"), stateUnknown.s()},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := &onboardMachine{Machine: "mch_x", Import: onboardPart{State: stateDone.s(), ID: id, Error: "disk full"}}
			read := func(got string) (*apitypes.IngestionBody, error) {
				if got != id {
					t.Errorf("read ingestion %q, want %q", got, id)
				}
				if c.read != nil {
					return nil, c.read
				}
				return &apitypes.IngestionBody{Id: id, State: c.state}, nil
			}
			onboardSettle(m, id, "--context 'lab' ", read)
			if m.Import.State != c.want || !strings.HasPrefix(m.Import.Command, c.command) || m.Import.ID != id {
				t.Errorf("import %+v; want state %q, a command starting %q and id %q", m.Import, c.want, c.command, id)
			}
			if m.Import.Error != "disk full" {
				t.Errorf("the error that stopped the run is %q, want it kept as \"disk full\"", m.Import.Error)
			}
		})
	}
}

// TestQualifyNamesTheContextOnce: a command that already names --context is left alone, and so are an
// empty prefix and an empty command.
func TestQualifyNamesTheContextOnce(t *testing.T) {
	const ctx = "--context 'lab' "
	for _, c := range []struct{ ctx, cmd, want string }{
		{ctx, "bronzewardctl import status 'i'", "bronzewardctl --context 'lab' import status 'i'"},
		{ctx, "bronzewardctl --context 'lab' import status 'i'", "bronzewardctl --context 'lab' import status 'i'"},
		{"", "bronzewardctl import status 'i'", "bronzewardctl import status 'i'"},
		{ctx, "", ""},
	} {
		if got := qualify(c.ctx, c.cmd); got != c.want {
			t.Errorf("qualify(%q, %q) = %q, want %q", c.ctx, c.cmd, got, c.want)
		}
	}
}
