package clitest

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestContextsTokensAndOutput covers context selection, automation token input, the output
// formats and the read-only and no-leak properties of every read command.
func TestContextsTokensAndOutput(t *testing.T) {
	f := start(t)
	f.want(f.ctl(nil, "", "context", "list"), 0, "CURRENT  NAME")
	f.want(f.ctl(nil, "", "whoami"), 1, "no context selected")
	f.want(f.ctl(nil, "", "context", "add", "remote", "--server", "http://bronzeward.example.invalid"), 2, "must be https")
	f.want(f.ctl(nil, "", "context", "add", "lab", "--server", f.base, "--automation-token-file", f.tokenFile), 0,
		`Context "lab" added`, "Current context: lab")
	f.want(f.ctl(nil, "", "context", "add", "other", "--server", f.base), 0, "Current context: lab")
	f.want(f.ctl(nil, "", "context", "add", "lab", "--server", f.base), 1, "exists")
	before := f.acts()

	// Output: the table, and json and yaml holding the API resource unchanged.
	f.want(f.ctl(nil, "", "whoami"), 0, "Context    lab ("+f.base+")", "Identity   ci  service", "Roles      viewer", "recovery mode: off")
	q, _ := http.NewRequest(http.MethodGet, f.base+"/api/v1/identity", nil)
	q.Header.Set("Authorization", "Bearer "+f.token)
	var direct, viaJSON, viaYAML map[string]any
	json.Unmarshal(f.do(q, http.StatusOK), &direct)
	r := f.ctl(nil, "", "whoami", "-o", "json")
	f.want(r, 0)
	json.Unmarshal([]byte(r.out), &viaJSON)
	r = f.ctl(nil, "", "-o", "yaml", "whoami")
	f.want(r, 0)
	yaml.Unmarshal([]byte(r.out), &viaYAML)
	if j, _ := json.Marshal(viaYAML); json.Unmarshal(j, &viaYAML) != nil || !reflect.DeepEqual(direct, viaJSON) || !reflect.DeepEqual(direct, viaYAML) {
		t.Errorf("GET /identity %v; -o json %v; -o yaml %v", direct, viaJSON, viaYAML)
	}
	f.want(f.ctl(nil, "", "whoami", "-o", "wide"), 2, "give table, json or yaml")
	f.want(f.ctl(nil, "", "version"), 0, "needs API revision 2 or later", "API revision 2, start mode normal")

	// Selection: --context, then BRONZEWARD_CONTEXT, then the current context.
	other := []string{"BRONZEWARD_TOKEN_FILE=" + f.tokenFile}
	f.want(f.ctl(other, "", "context", "use", "other"), 0, "Current context: other")
	f.want(f.ctl(other, "", "whoami"), 0, "Context    other")
	f.want(f.ctl(append(other, "BRONZEWARD_CONTEXT=lab"), "", "whoami"), 0, "Context    lab")
	f.want(f.ctl(append(other, "BRONZEWARD_CONTEXT=lab"), "", "--context", "other", "whoami"), 0, "Context    other")
	f.want(f.ctl(nil, "", "--context", "nowhere", "whoami"), 1, `context "nowhere", named by --context, does not exist`)

	// Token input: standard input, an owner-only file, never both; a refused token.
	f.want(f.ctl(nil, f.token+"\n", "whoami", "--token-stdin"), 0, "Roles      viewer")
	f.want(f.ctl(nil, "", "whoami"), 1, "no automation token")
	f.want(f.ctl(nil, "", "whoami", "--token-stdin", "--token-file", f.tokenFile), 2, "not both")
	open := filepath.Join(f.home, "open-token")
	os.WriteFile(open, []byte(f.token), 0o644)
	f.want(f.ctl(nil, "", "whoami", "--token-file", open), 1, "make it owner-only")
	f.want(f.ctl(nil, "not-a-token\n", "whoami", "--token-stdin"), 1, "not an automation token")
	id, _, _ := strings.Cut(f.token, ".")
	f.want(f.ctl(nil, id+".BWSYNTH-wrong\n", "whoami", "--token-stdin"), 1, "401", "The automation token was not accepted",
		"other ("+f.base+")", "instance")

	f.want(f.ctl(nil, "", "context", "remove", "other"), 0, `Context "other" removed. No context is current now.`)
	f.want(f.ctl(nil, "", "context", "list", "-o", "json"), 0, `"name":"lab"`, `"lastEpoch":"`)
	if after := f.acts(); after != before {
		t.Errorf("acts changed around the read commands:\nbefore %s\nafter  %s", before, after)
	}
	cfg, err := os.ReadFile(filepath.Join(f.home, "bronzeward", "config.yaml"))
	if err != nil || leaks(string(cfg), f.token) {
		t.Errorf("contexts file: %v, or it holds the token", err)
	}
}

// TestRequestCore covers the answers every read meets: epochs, the recovery banner, a missing
// epoch, the 503 read retry, the pre-entry start mode, an unknown problem code and an
// unreachable server.
func TestRequestCore(t *testing.T) {
	f := start(t)
	in := newInjector(t, f.base)
	f.want(f.ctl(nil, "", "context", "add", "lab", "--server", in.URL, "--certificate-authority", in.ca,
		"--automation-token-file", f.tokenFile), 0)
	notice := "The installation entered recovery mode or was restored"
	if r := f.ctl(nil, "", "whoami"); r.code != 0 || strings.Contains(r.err, notice) {
		t.Errorf("first epoch: exit %d, stderr %s; want 0 and no notice", r.code, r.err)
	}

	in.set(nil, func(r *http.Response) {
		if on("/identity")(r.Request) {
			r.Header.Set("Bronzeward-Epoch", "ep_BWSYNTH-other")
			r.Header.Set("Bronzeward-Recovery-Mode", "true")
		}
	})
	r := f.ctl(nil, "", "whoami")
	f.want(r, 0, "lab ("+in.URL+"): "+notice, "RECOVERY MODE is on in lab ("+in.URL+") (start mode: normal)")
	cfg, _ := os.ReadFile(filepath.Join(f.home, "bronzeward", "config.yaml"))
	if !strings.Contains(string(cfg), "lastEpoch: ep_BWSYNTH-other") {
		t.Errorf("the new epoch was not kept:\n%s", cfg)
	}

	in.set(nil, func(r *http.Response) {
		if on("/identity")(r.Request) {
			r.Header.Del("Bronzeward-Epoch")
		}
	})
	f.want(f.ctl(nil, "", "whoami"), 0, "GET /api/v1/identity answered without Bronzeward-Epoch, a server defect")

	failures := 2
	in.set(func(w http.ResponseWriter, r *http.Request) bool {
		if !on("/identity")(r) || failures == 0 {
			return false
		}
		failures--
		problem(w, http.StatusServiceUnavailable, "dependency-unavailable", "")
		return true
	}, nil)
	f.want(f.ctl(nil, "", "whoami"), 0, "503 Injected dependency-unavailable", "try 2 of 5 in 1s", "try 3 of 5 in 2s", "Roles      viewer")

	in.set(func(w http.ResponseWriter, r *http.Request) bool {
		if !on("/identity")(r) {
			return false
		}
		problem(w, http.StatusServiceUnavailable, "epoch-superseded", "")
		return true
	}, nil)
	if r := f.ctl(nil, "", "whoami"); r.code != 1 || strings.Contains(r.err, "try 2") {
		t.Errorf("epoch-superseded: exit %d, stderr %s; want 1 and no retry", r.code, r.err)
	}

	in.set(func(w http.ResponseWriter, r *http.Request) bool {
		if on("/server")(r) {
			problem(w, http.StatusConflict, "recovery-mode-active", "")
			return true
		}
		return false
	}, nil)
	f.want(f.ctl(nil, "", "version"), 0, "Server  unknown")
	cfg, _ = os.ReadFile(filepath.Join(f.home, "bronzeward", "config.yaml"))
	if !strings.Contains(string(cfg), "startMode: recovery") {
		t.Errorf("the pre-entry refusal did not record a recovery start:\n%s", cfg)
	}

	in.set(func(w http.ResponseWriter, r *http.Request) bool {
		if !on("/identity")(r) {
			return false
		}
		problem(w, http.StatusTeapot, "BWSYNTH-unknown", `,"detail":"an unknown code"`)
		return true
	}, nil)
	f.want(f.ctl(nil, "", "whoami"), 1, "418 Injected BWSYNTH-unknown: an unknown code (instance req_injected)")

	in.Close()
	f.want(f.ctl(nil, "", "whoami"), 1, "lab ("+in.URL+")", "try 5 of 5 in 8s", "connection refused")
}
