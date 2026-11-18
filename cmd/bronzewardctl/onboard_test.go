package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/talos/discover"
)

// The tests of `cluster onboard` (ginsys/bronzeward#833), each over the stand-ins of
// onboard_fake_test.go: a fake API server, a fake OpenBao and a stub discovery.

// TestOnboardUsageErrorsSendNothing: a missing or refused flag is exit 2 and names the flag, before
// the identity read, any dial and any write. The API, OpenBao and the discovery record no request.
// Written first and red: the command did not exist.
func TestOnboardUsageErrorsSendNothing(t *testing.T) {
	e := newOnboard(t)
	without := func(drop ...string) []string {
		var out []string
		skip := map[string]bool{}
		for _, d := range drop {
			skip[d] = true
		}
		base := e.base()
		for i := 0; i < len(base); i++ {
			if skip[base[i]] {
				if base[i] != "--skip-secret" && i+1 < len(base) && !strings.HasPrefix(base[i+1], "--") {
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
		env  string
		want string
	}{
		{"no talosconfig", without("--talosconfig"), "", "--talosconfig"},
		{"no name", without("--name"), "", "--name"},
		{"no kubernetes endpoint", without("--kubernetes-endpoint"), "", "--kubernetes-endpoint"},
		{"a kubernetes endpoint that is not https", append(without("--kubernetes-endpoint"), "--kubernetes-endpoint", "http://k8s.example:6443"), "", "https"},
		{"no OpenBao address", without("--bao-addr"), "", "--bao-addr"},
		{"no token source", without("--bao-token-file"), "", "--bao-token-file"},
		{"both token sources", append(e.base(), "--bao-token-stdin"), "", "--bao-token-stdin"},
		{"plain http to a remote OpenBao", append(without("--bao-addr"), "--bao-addr", "http://bao.example:8200"), "", "loopback"},
		{"skip-secret with an OpenBao address", append(e.base(), "--skip-secret"), "", "--skip-secret"},
		{"an unknown talos context", append(e.base(), "--talos-context", "nope"), "", "--talos-context"},
		{"review with transient staging", append(e.base(), "--review", "--staging", "transient"), "", "--review"},
		{"a bad staging", append(e.base(), "--staging", "bogus"), "", "--staging"},
		{"no-wait", append(e.base(), "--no-wait"), "", "--no-wait"},
		{"idempotency key", append(e.base(), "--idempotency-key", "k"), "", "--idempotency-key"},
		{"expect epoch", append(e.base(), "--expect-epoch", "x"), "", "--expect-epoch"},
		{"expect principal", append(e.base(), "--expect-principal", "x"), "", "--expect-principal"},
		{"if-match", append(e.base(), "--if-match", "x"), "", "--if-match"},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, out, errOut := e.runArgs(c.args...)
			if code != 2 || !strings.Contains(errOut, c.want) || e.requests() != 0 {
				t.Errorf("exit %d, stdout %q, stderr %q, %d requests; want exit 2 naming %q and no request", code, out, errOut, e.requests(), c.want)
			}
		})
	}
	// The address may come from BAO_ADDR instead of the flag.
	t.Setenv("BAO_ADDR", e.baoURL)
	if code, out, errOut := e.runArgs(without("--bao-addr")...); code != 0 {
		t.Errorf("BAO_ADDR given: exit %d, stdout %q, stderr %q; want 0", code, out, errOut)
	}
}

// TestOnboardFullRun: a two-member cluster is recorded, the secret is written once with the exact
// body, one draft is created and both imports are released.
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
	want := []string{"POST /clusters", "POST /machines", "POST /machines", "POST /drafts", "POST /ingestions", "POST /ingestions"}
	if got := a.writes(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("writes %q; want %q", got, want)
	}
	if !strings.Contains(a.posts[1], `"talosEndpoint":"10.0.0.1"`) || !strings.Contains(a.posts[1], `"smbiosUuid":"11111111-2222-3333-4444-555555555555"`) {
		t.Errorf("the first machine was sent as %q", a.posts[1])
	}
	// One POST to OpenBao, with the token and the exact body; never a GET.
	doc, err := discover.OneContext([]byte(twoContextTalosconfig()), "")
	if err != nil {
		t.Fatal(err)
	}
	var wantStruct struct {
		Options struct {
			CAS int `json:"cas"`
		} `json:"options"`
		Data struct {
			Talosconfig string `json:"talosconfig"`
		} `json:"data"`
	}
	wantStruct.Data.Talosconfig = string(doc)
	wantBody, _ := json.Marshal(wantStruct)
	if got, wantReq := e.bao.requests, []string{"POST /v1/secret/data/access/talos/" + cl.Id}; strings.Join(got, ",") != strings.Join(wantReq, ",") {
		t.Fatalf("OpenBao saw %q; want %q", got, wantReq)
	}
	if e.bao.bodies[0] != string(wantBody) || e.bao.tokens[0] != plantedToken {
		t.Errorf("OpenBao body %q, token %q; want %q and the token", e.bao.bodies[0], e.bao.tokens[0], wantBody)
	}
	if len(e.calls) != 1 || e.calls[0] != "|" {
		t.Errorf("discovery calls %q; want one, with no context and no endpoint", e.calls)
	}
	for _, line := range []string{"Cluster " + cl.Id + ": created", "Secret: written", "Draft " + a.draft.id + " (onboard lab)"} {
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
	for _, marker := range []string{plantedToken, b64(plantedKeyA), b64(plantedKeyB), plantedKeyA} {
		e.noneOf(marker, out, errOut)
	}
}

// TestOnboardJSONIsOneObject: -o json prints one object, once, at the end, with the cluster, the
// secret, the draft and each machine with its add and its import. The 202 answer of an import never
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
	if obj.Cluster.State != "created" || obj.Cluster.ID != e.api.clusters[0].Id || obj.Secret.State != "written" || obj.Draft == nil || obj.Draft.ID != e.api.draft.id {
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
	for _, marker := range []string{plantedToken, b64(plantedKeyA), b64(plantedKeyB)} {
		e.noneOf(marker, out, errOut)
	}
}

// TestOnboardUsesTheChosenContext: without --talos-context the run uses the file's current context A;
// with it, B. Either way the secret holds one context, with the chosen name, and no byte of the other
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
			var req struct {
				Data struct{ Talosconfig string } `json:"data"`
			}
			if err := json.Unmarshal([]byte(e.bao.bodies[0]), &req); err != nil {
				t.Fatal(err)
			}
			doc := req.Data.Talosconfig
			if !strings.HasPrefix(doc, "context: "+c.ctx+"\n") || strings.Count(doc, "ca: ") != 1 || !strings.Contains(doc, b64(c.keep)) {
				t.Errorf("the written document is not one context %q with its key:\n%s", c.ctx, doc)
			}
			e.noneOf(b64(c.drop), e.bao.bodies[0])
			e.noneOf(c.drop, e.bao.bodies[0])
		})
	}
}

// TestOnboardSecondRunWritesNothingNew: after a full run, a second one with --skip-secret changes no
// state and sends no request to OpenBao, creates no draft and starts no import. Its cluster and machine
// POSTs are each answered 409, which is how it finds what exists; the flags are not applied to the
// existing cluster.
func TestOnboardSecondRunWritesNothingNew(t *testing.T) {
	e := newOnboard(t)
	if code, out, errOut := e.run(); code != 0 {
		t.Fatalf("first run: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	before := len(e.api.writes())
	rev, ings, bao := e.api.draft.rev, len(e.api.ings), len(e.bao.requests)
	args := []string{"cluster", "onboard", "--talosconfig", e.file, "--name", "another-name", "--kubernetes-endpoint", "https://elsewhere:6443", "--skip-secret"}
	code, out, errOut := e.runArgs(args...)
	if code != 0 {
		t.Fatalf("second run: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	later := e.api.writes()[before:]
	if strings.Join(later, ",") != "POST /clusters,POST /machines,POST /machines" {
		t.Errorf("second run's writes %q; want the three refused creates only", later)
	}
	if len(e.api.clusters) != 1 || len(e.api.machines) != 2 || e.api.draft.rev != rev || len(e.api.ings) != ings || len(e.bao.requests) != bao {
		t.Errorf("the second run changed state: %d clusters, %d machines, draft revision %d, %d imports, %d OpenBao requests", len(e.api.clusters), len(e.api.machines), e.api.draft.rev, len(e.api.ings), len(e.bao.requests))
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

// TestOnboardRerunWithACreateOnlyToken: with the default flags and a create-only policy a rerun sends one
// refused POST to OpenBao. The refusal is 403 for both a missing right and an occupied path, so the
// command says so, names --skip-secret, stops with exit 1 and starts no import; cluster and machines
// stay recorded.
func TestOnboardRerunWithACreateOnlyToken(t *testing.T) {
	e := newOnboard(t)
	e.bao.mode = "occupied"
	if code, out, errOut := e.run(); code != 0 {
		t.Fatalf("first run: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	ings := len(e.api.ings)
	code, out, errOut := e.run()
	if code != 1 || len(e.bao.requests) != 2 || len(e.api.ings) != ings {
		t.Errorf("rerun: exit %d, %d OpenBao requests, %d imports; want exit 1, 2 and %d", code, len(e.bao.requests), len(e.api.ings), ings)
	}
	for _, want := range []string{"Secret: refused", "403", "may not create the secret, or a secret is already there", "--skip-secret"} {
		if !strings.Contains(out+errOut, want) {
			t.Errorf("output lacks %q:\nstdout %s\nstderr %s", want, out, errOut)
		}
	}
	if len(e.api.clusters) != 1 || len(e.api.machines) != 2 {
		t.Errorf("the cluster and the machines did not stay recorded")
	}
}

// TestOnboardOpenBaoAnswers: each class of answer, with nothing of OpenBao's text, the token or the
// talosconfig in any output, one request only and a redirect never followed.
func TestOnboardOpenBaoAnswers(t *testing.T) {
	for _, c := range []struct {
		mode, class string
		code        int
		imports     int
	}{
		{"denied", "403", 1, 0},
		{"error", "unexpected answer", 3, 0},
		{"redirect", "unexpected answer", 3, 0},
		{"absent", "no secret mount", 1, 0},
		{"cas", "present", 0, 2},
	} {
		t.Run(c.mode, func(t *testing.T) {
			e := newOnboard(t)
			e.bao.mode = c.mode
			if c.mode == "absent" {
				e.bao.mode = "absent"
			}
			code, out, errOut := e.run()
			if code != c.code || !strings.Contains(out+errOut, c.class) || len(e.api.ings) != c.imports {
				t.Errorf("exit %d, %d imports, stdout %q, stderr %q; want %d, %d, %q", code, len(e.api.ings), out, errOut, c.code, c.imports, c.class)
			}
			if len(e.bao.requests) != 1 || e.bao.requests[0][:4] != "POST" {
				t.Errorf("OpenBao saw %q; want one POST, never a GET and no followed redirect", e.bao.requests)
			}
			for _, marker := range []string{plantedToken, b64(plantedKeyA), b64(plantedKeyB), "PLANTED-OPENBAO-TEXT"} {
				e.noneOf(marker, out, errOut)
			}
			if len(e.api.clusters) != 1 || len(e.api.machines) != 2 {
				t.Errorf("the cluster and the machines did not stay recorded")
			}
		})
	}
	t.Run("unreachable", func(t *testing.T) {
		e := newOnboard(t)
		code, out, errOut := e.runArgs(append(e.base()[:8:8], "--bao-addr", "http://127.0.0.1:1", "--bao-token-file", e.tokenFile)...)
		if code != 3 || !strings.Contains(out+errOut, "unreachable") {
			t.Errorf("exit %d, stdout %q, stderr %q; want 3 and the class unreachable", code, out, errOut)
		}
		e.noneOf(plantedToken, out, errOut)
	})
}

// TestOnboardTokenFromStandardInput: the token can come from standard input, never from an argument.
func TestOnboardTokenFromStandardInput(t *testing.T) {
	e := newOnboard(t)
	setStdin(t, plantedToken+"\n")
	args := append(e.base()[:8:8], "--bao-addr", e.baoURL, "--bao-token-stdin")
	if code, out, errOut := e.runArgs(args...); code != 0 || e.bao.tokens[0] != plantedToken {
		t.Errorf("exit %d, stdout %q, stderr %q, token seen %q", code, out, errOut, e.bao.tokens)
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
		return strings.Join([]string{strconv.Itoa(len(e.api.clusters)), strconv.Itoa(len(e.api.machines)), e.api.draft.title, strconv.Itoa(e.api.draft.rev), strconv.Itoa(released), strconv.Itoa(e.bao.written)}, "/")
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
		{"the secret refused", func(e *onboardEnv) { e.bao.mode = "denied" }, func(e *onboardEnv) { e.bao.mode = "ok" }},
		{"the second import failed", func(e *onboardEnv) { e.api.failImportAt = 2 }, func(e *onboardEnv) { e.api.failImportAt = 0 }},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newOnboard(t)
			c.inject(e)
			if code, out, errOut := e.run(); code == 0 {
				t.Fatalf("the injected failure did not stop the run: stdout %q, stderr %q", out, errOut)
			}
			c.fix(e)
			// The secret was written by the first run unless it was the step that failed.
			extra := []string{}
			if e.bao.written > 0 {
				extra = append(extra, "--skip-secret")
			}
			args := e.base()
			if len(extra) > 0 {
				args = []string{"cluster", "onboard", "--talosconfig", e.file, "--name", "lab", "--kubernetes-endpoint", "proposed", "--skip-secret"}
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
// names both cluster IDs, and leaves the secret and every later step undone.
func TestOnboardMachineInAnotherCluster(t *testing.T) {
	e := newOnboard(t)
	other := "cl_" + strings.Repeat("0", 26)
	e.api.machines = append(e.api.machines, obMachine{id: "mch_" + strings.Repeat("1", 26), cluster: other, key: "11111111-2222-3333-4444-555555555555", platform: "metal", endpoint: "10.0.0.1"})
	code, out, errOut := e.run()
	if code != 1 || !strings.Contains(out+errOut, other) || !strings.Contains(out+errOut, e.api.clusters[0].Id) {
		t.Errorf("exit %d, stdout %q, stderr %q; want 1 naming %s and %s", code, out, errOut, other, e.api.clusters[0].Id)
	}
	if len(e.bao.requests) != 0 || e.api.draft.id != "" || len(e.api.ings) != 0 {
		t.Errorf("a later step ran: %d OpenBao requests, draft %q, %d imports", len(e.bao.requests), e.api.draft.id, len(e.api.ings))
	}
}

// TestOnboardSendsNothingForAnAuthorlessCaller: a caller who is not a person with the author role gets
// exit 1 that names the role, and nothing is written or dialled.
func TestOnboardSendsNothingForAnAuthorlessCaller(t *testing.T) {
	for _, roles := range []string{`{"kind":"human","principal":"h_OB","roles":["publisher"]}`, `{"kind":"service","principal":"svc_OB","roles":["author"]}`} {
		e := newOnboard(t)
		e.api.roles = roles
		code, out, errOut := e.run()
		if code != 1 || !strings.Contains(out+errOut, "author") || len(e.api.writes()) != 0 || len(e.calls) != 0 || len(e.bao.requests) != 0 {
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
			if code != 1 || len(e.api.writes()) != 0 || len(e.bao.requests) != 0 {
				t.Errorf("exit %d, stdout %q, stderr %q, writes %q", code, out, errOut, e.api.writes())
			}
		})
	}
}

// TestOnboardKubernetesEndpointGiven: an https URL is sent as it is, in place of the proposal.
func TestOnboardKubernetesEndpointGiven(t *testing.T) {
	e := newOnboard(t)
	args := append(e.base()[:6:6], e.base()[8:]...)
	args = append(args, "--kubernetes-endpoint", "https://k8s.lab.example:6443")
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

// TestOnboardNoImport: --no-import stops after the secret, with no draft and no import.
func TestOnboardNoImport(t *testing.T) {
	e := newOnboard(t)
	code, out, errOut := e.run("--no-import")
	if code != 0 || e.api.draft.id != "" || len(e.api.ings) != 0 || len(e.bao.requests) != 1 {
		t.Errorf("exit %d, draft %q, %d imports, %d OpenBao requests, stdout %q, stderr %q", code, e.api.draft.id, len(e.api.ings), len(e.bao.requests), out, errOut)
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
	skip := []string{"cluster", "onboard", "--talosconfig", e.file, "--name", "lab", "--kubernetes-endpoint", "proposed", "--skip-secret", "--review"}
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
	args := []string{"cluster", "onboard", "--talosconfig", e.file, "--name", "lab", "--kubernetes-endpoint", "proposed", "--skip-secret"}
	code, out, errOut := e.runArgs(args...)
	if code != 1 || len(e.api.ings) != 1 || !strings.Contains(out+errOut, "import status") {
		t.Errorf("exit %d, %d imports, stdout %q, stderr %q; want 1, no new import and import status named", code, len(e.api.ings), out, errOut)
	}
}

// TestOnboardRecoveryCommandsKeepTheirFlags: the commands a stopped run prints reach the server the run
// did (the global --context), and a recovery command keeps --skip-secret where the run was given it.
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
	args := []string{"cluster", "onboard", "--talosconfig", e2.file, "--name", "lab", "--kubernetes-endpoint", "proposed", "--skip-secret"}
	code, out, errOut = e2.runArgs(args...)
	if code == 0 || !strings.Contains(out, "--skip-secret") || strings.Contains(out, "--bao-") {
		t.Errorf("exit %d, stdout %q, stderr %q; want a failure whose recovery command keeps --skip-secret", code, out, errOut)
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
