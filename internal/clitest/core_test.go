package clitest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/client"
)

// TestContextsTokensAndOutput covers context selection, automation token input, the output
// formats and the read-only and no-leak properties of every read command.
func TestContextsTokensAndOutput(t *testing.T) {
	f := start(t, "viewer")
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
	f.want(f.ctl(nil, "", "version"), 0, "needs API revision 2 or later", "API revision 4, start mode normal")

	// Selection: --context, then BRONZEWARD_CONTEXT, then the current context.
	other := []string{"BRONZEWARD_TOKEN_FILE=" + f.tokenFile}
	f.want(f.ctl(other, "", "context", "use", "other"), 0, "Current context: other")
	f.want(f.ctl(other, "", "whoami"), 0, "Context    other")
	f.want(f.ctl(append(other, "BRONZEWARD_CONTEXT=lab"), "", "whoami"), 0, "Context    lab")
	f.want(f.ctl(append(other, "BRONZEWARD_CONTEXT=lab"), "", "--context", "other", "whoami"), 0, "Context    other")
	f.want(f.ctl(nil, "", "--context", "nowhere", "whoami"), 1, `context "nowhere", named by --context, does not exist`)

	// Token input: standard input, an owner-only file, never both; a refused token.
	f.want(f.ctl(nil, f.token+"\n", "whoami", "--token-stdin"), 0, "Roles      viewer")
	f.checkHardened()
	f.want(f.ctl(nil, "", "whoami"), 1, "no automation token")
	f.want(f.ctl(nil, "", "whoami", "--token-stdin", "--token-file", f.tokenFile), 2, "not both")
	open := filepath.Join(f.home, "open-token")
	if err := os.WriteFile(open, []byte(f.token), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o644); err != nil {
		t.Fatal(err)
	}
	f.want(f.ctl(nil, "", "whoami", "--token-file", open), 1, "make it owner-only")
	f.want(f.ctl(nil, "not-a-token\n", "whoami", "--token-stdin"), 1, "not an automation token")
	id, _, _ := strings.Cut(f.token, ".")
	f.want(f.ctl(nil, id+".BWSYNTH-wrong\n", "whoami", "--token-stdin"), 1, "401", "The automation token was not accepted",
		"other ("+f.base+")", "instance")
	f.want(f.ctl(nil, id+".BWSYNTH-wrong\n", "version", "--token-stdin"), 1, "Server  unknown", "401")
	fifo := filepath.Join(f.home, "fifo-token")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	f.want(f.ctl(nil, "", "whoami", "--token-file", fifo), 1, "not a regular file")

	// The leak scan's positive control through the capture path: a planted token, from the
	// fixture and from standard input, that the CLI echoes in an error.
	for _, plant := range []struct{ stdin, arg string }{{"", f.token}, {"bwt_p.BWSYNTH-planted\n", "BWSYNTH-planted"}} {
		args := []string{"context", "use", plant.arg}
		if got := f.exec(nil, plant.stdin, args...).leaked(f.token, plant.stdin, args); !slices.Equal(got, []string{"argv", "stderr"}) {
			t.Errorf("planted %q found in %v, want argv and stderr", plant.arg, got)
		}
	}
	full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer full.Close()
	use := f.command(nil, "context", "use", "other")
	use.Stdout = full
	if err := use.Run(); err == nil {
		t.Error("context use reported success when its output could not be written")
	}

	f.want(f.ctl(nil, "", "context", "remove", "other"), 0, `Context "other" removed. No context is current now.`)
	f.want(f.ctl(nil, "", "context", "list", "-o", "json"), 0, `"name":"lab"`, `"lastEpoch":"`)

	// Concurrent context changes all survive.
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() { f.want(f.ctl(nil, "", "context", "add", fmt.Sprint("c", i), "--server", f.base), 0) })
	}
	wg.Wait()
	var contexts client.Config
	r = f.ctl(nil, "", "context", "-o", "json", "list")
	if json.Unmarshal([]byte(r.out), &contexts); len(contexts.Contexts) != 9 {
		t.Errorf("after 8 concurrent adds: %s", r.out)
	}

	// A context edited by hand to plain http to a remote host is refused before the token is read.
	contexts.Contexts = append(contexts.Contexts, client.Context{Name: "plain", Server: "http://bronzeward.example.invalid", Credential: "automation"})
	b, _ := yaml.Marshal(contexts)
	if err := os.WriteFile(filepath.Join(f.home, "bronzeward", "config.yaml"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	f.want(f.ctl(nil, "", "--context", "plain", "whoami", "--token-file", f.tokenFile), 1, "plain (http://bronzeward.example.invalid)", "must be https")
	if after := f.acts(); after != before {
		t.Errorf("acts changed around the read commands:\nbefore %s\nafter  %s", before, after)
	}
	cfg, err := os.ReadFile(filepath.Join(f.home, "bronzeward", "config.yaml"))
	if err != nil || leaks(string(cfg), f.token) {
		t.Errorf("contexts file: %v, or it holds the token", err)
	}
}

// TestRequestCore covers the answers every read meets: epochs, the recovery banner, a missing
// epoch, a redirect, the 503 read retry, the pre-entry start mode, an unknown problem code and an
// unreachable server. Its token may write, so the unchanged acts and the injector's GET-only
// record show the reads send no write.
func TestRequestCore(t *testing.T) {
	f := start(t, "viewer,author,publisher")
	in := newInjector(t, f.base)
	in.set(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/livez" {
			http.Redirect(w, r, "https://bronzeward.example.invalid/livez", http.StatusFound)
			return true
		}
		return false
	}, nil)
	f.want(f.ctl(nil, "", "context", "add", "moved", "--server", in.URL, "--certificate-authority", in.ca), 1,
		`302 Found redirecting to "https://bronzeward.example.invalid/livez"`)
	in.set(nil, nil)
	// Relative paths are kept absolute.
	cwd, _ := os.Getwd()
	relCA, err1 := filepath.Rel(cwd, in.ca)
	relToken, err2 := filepath.Rel(cwd, f.tokenFile)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	f.want(f.ctl(nil, "", "context", "add", "lab", "--server", in.URL, "--certificate-authority", relCA,
		"--automation-token-file", relToken), 0, "the bundle "+in.ca)
	config := filepath.Join(f.home, "bronzeward")
	if cfg, _ := os.ReadFile(filepath.Join(config, "config.yaml")); !strings.Contains(string(cfg), "certificateAuthority: "+in.ca) ||
		!strings.Contains(string(cfg), "tokenFile: "+f.tokenFile) {
		t.Errorf("relative paths were not kept absolute:\n%s", cfg)
	}
	before := f.acts()
	notice := "The installation entered recovery mode or was restored"
	if r := f.ctl(nil, "", "whoami"); r.code != 0 || strings.Contains(r.err, notice) {
		t.Errorf("first epoch: exit %d, stderr %s; want 0 and no notice", r.code, r.err)
	}

	// A recovery entry seen first on /identity: the client reads /server again, and the banner
	// shows the start mode that read served.
	var entered atomic.Bool
	in.set(nil, func(r *http.Response) {
		if on("/identity")(r.Request) {
			entered.Store(true)
		}
		if entered.Load() {
			r.Header.Set("Bronzeward-Epoch", "ep_BWSYNTH-other")
			r.Header.Set("Bronzeward-Recovery-Mode", "true")
			if on("/server")(r.Request) {
				rewrite(r, `"startMode":"normal"`, `"startMode":"recovery"`)
			}
		}
	})
	r := f.ctl(nil, "", "whoami")
	f.want(r, 0, "lab ("+in.URL+"): "+notice, "RECOVERY MODE is on in lab ("+in.URL+") (start mode: recovery)")
	if n := strings.Count(strings.Join(in.requests(), "\n"), "/api/v1/server"); n != 2 {
		t.Errorf("GET /server sent %d times, want 2: %v", n, in.requests())
	}
	cfg, _ := os.ReadFile(filepath.Join(config, "config.yaml"))
	if !strings.Contains(string(cfg), "lastEpoch: ep_BWSYNTH-other") {
		t.Errorf("the new epoch was not kept:\n%s", cfg)
	}

	// A context added again for another server while a command ran keeps its own epoch: what the
	// command read belongs to the old server. The edit runs in the injector, so it uses Errorf.
	in.set(nil, func(r *http.Response) {
		if !on("/identity")(r.Request) {
			return
		}
		var c client.Config
		if err := yaml.Unmarshal(cfg, &c); err != nil || c.Find("lab") == nil {
			t.Errorf("contexts file: %v\n%s", err, cfg)
			return
		}
		c.Find("lab").Server, c.Find("lab").LastEpoch = "https://bronzeward.example.invalid", ""
		b, _ := yaml.Marshal(c)
		if err := os.WriteFile(filepath.Join(config, "config.yaml"), b, 0o600); err != nil {
			t.Error(err)
		}
		r.Header.Set("Bronzeward-Epoch", "ep_BWSYNTH-replaced")
	})
	f.want(f.ctl(nil, "", "whoami"), 0)
	var replaced client.Config
	b, _ := os.ReadFile(filepath.Join(config, "config.yaml"))
	if yaml.Unmarshal(b, &replaced); replaced.Find("lab") == nil || replaced.Find("lab").LastEpoch != "" {
		t.Errorf("the replaced context took the old server's epoch:\n%s", b)
	}
	if err := os.WriteFile(filepath.Join(config, "config.yaml"), cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	// A new epoch that cannot be kept fails the command, naming the context. Root writes past
	// the mode, so the check needs another user.
	in.set(nil, func(r *http.Response) { r.Header.Set("Bronzeward-Epoch", "ep_BWSYNTH-third") })
	if os.Geteuid() != 0 {
		if err := os.Chmod(config, 0o500); err != nil {
			t.Fatal(err)
		}
		f.want(f.ctl(nil, "", "whoami"), 1, "lab ("+in.URL+"): keeping the epoch, start mode and maximum token lifetime")
		if err := os.Chmod(config, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	// A redirect whose body is cut short is reported, not resent.
	in.set(func(w http.ResponseWriter, r *http.Request) bool {
		if !on("/identity")(r) {
			return false
		}
		w.Header().Set("Location", "https://bronzeward.example.invalid/")
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusTemporaryRedirect)
		w.Write([]byte("short"))
		return true
	}, nil)
	f.want(f.ctl(nil, "", "whoami"), 1, "follows no redirect")
	if n := strings.Count(strings.Join(in.requests(), "\n"), "/api/v1/identity"); n != 1 {
		t.Errorf("the identity read was sent %d times", n)
	}

	// A refusal whose body is cut short is the refusal its status names, not repeated.
	in.set(func(w http.ResponseWriter, r *http.Request) bool {
		if !on("/identity")(r) {
			return false
		}
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("short"))
		return true
	}, nil)
	if r := f.ctl(nil, "", "whoami"); r.code != 1 || !strings.Contains(r.err, "401 Unauthorized, and reading the answer failed") || strings.Contains(r.err, "try 2") {
		t.Errorf("cut-short 401: exit %d, stderr %s; want 1, the status and no retry", r.code, r.err)
	}

	// A failed GET /server re-read after a new epoch fails the read it followed.
	var servers atomic.Int32
	in.set(func(w http.ResponseWriter, r *http.Request) bool {
		if on("/server")(r) && servers.Add(1) > 1 {
			problem(w, http.StatusConflict, "recovery-mode-active", "")
			return true
		}
		return false
	}, func(r *http.Response) {
		if on("/identity")(r.Request) {
			r.Header.Set("Bronzeward-Epoch", "ep_BWSYNTH-fourth")
		}
	})
	f.want(f.ctl(nil, "", "whoami"), 1, notice, "409 Injected refusal (recovery-mode-active)")

	in.set(nil, func(r *http.Response) {
		if on("/identity")(r.Request) {
			r.Header.Del("Bronzeward-Epoch")
		}
	})
	f.want(f.ctl(nil, "", "whoami"), 0, "GET /api/v1/identity answered without Bronzeward-Epoch, a server defect")

	var failures atomic.Int32
	failures.Store(2)
	in.set(func(w http.ResponseWriter, r *http.Request) bool {
		if !on("/identity")(r) || failures.Add(-1) < 0 {
			return false
		}
		problem(w, http.StatusServiceUnavailable, "dependency-unavailable", "")
		return true
	}, nil)
	f.want(f.ctl(nil, "", "whoami"), 0, "503 Injected refusal (dependency-unavailable)", "try 2 of 5 in 1s", "try 3 of 5 in 2s", "Identity   ci")

	// Only the three listed codes are retried (§8.2).
	for _, code := range []string{"epoch-superseded", "BWSYNTH-unknown"} {
		in.set(func(w http.ResponseWriter, r *http.Request) bool {
			if !on("/identity")(r) {
				return false
			}
			problem(w, http.StatusServiceUnavailable, code, "")
			return true
		}, nil)
		if r := f.ctl(nil, "", "whoami"); r.code != 1 || strings.Contains(r.err, "try 2") {
			t.Errorf("%s: exit %d, stderr %s; want 1 and no retry", code, r.code, r.err)
		}
	}

	// An incompatible server's start mode is still the one the banner shows.
	in.set(nil, func(r *http.Response) {
		if on("/server")(r.Request) {
			r.Header.Set("Bronzeward-Recovery-Mode", "true")
			revisionOne(r)
			rewrite(r, `"startMode":"normal"`, `"startMode":"recovery"`)
		}
	})
	f.want(f.ctl(nil, "", "version"), 1, "needs revision 2 or later", "(start mode: recovery)")
	in.set(nil, nil)
	f.want(f.ctl(nil, "", "version"), 0, "start mode normal")

	// Any read answered 409 recovery-mode-active without a scope is a recovery start before its
	// entry (§8.5 item 1); one with a scope is not.
	in.set(refuse("/identity", http.StatusConflict, "recovery-mode-active", `,"scope":"BWSYNTH-scope"`), nil)
	f.want(f.ctl(nil, "", "whoami"), 1, "409 Injected refusal (recovery-mode-active)")
	if cfg, _ = os.ReadFile(filepath.Join(config, "config.yaml")); strings.Contains(string(cfg), "startMode: recovery") {
		t.Errorf("a scoped refusal recorded a recovery start:\n%s", cfg)
	}
	in.set(refuse("/identity", http.StatusConflict, "recovery-mode-active", ""), nil)
	f.want(f.ctl(nil, "", "whoami"), 1, "409 Injected refusal (recovery-mode-active)")
	if cfg, _ = os.ReadFile(filepath.Join(config, "config.yaml")); !strings.Contains(string(cfg), "startMode: recovery") {
		t.Errorf("the refused read did not record a recovery start:\n%s", cfg)
	}
	in.set(nil, nil)
	f.want(f.ctl(nil, "", "version"), 0, "start mode normal")

	in.set(refuse("/server", http.StatusConflict, "recovery-mode-active", ""), nil)
	f.want(f.ctl(nil, "", "version"), 1, "Server  unknown", "409 Injected refusal (recovery-mode-active)")
	cfg, _ = os.ReadFile(filepath.Join(config, "config.yaml"))
	if !strings.Contains(string(cfg), "startMode: recovery") {
		t.Errorf("the pre-entry refusal did not record a recovery start:\n%s", cfg)
	}
	f.want(f.ctl(nil, "", "whoami"), 1, "recovery-mode-active")
	if slices.Contains(in.requests(), "GET /api/v1/identity") {
		t.Error("the identity read was sent before the recovery entry")
	}

	in.set(func(w http.ResponseWriter, r *http.Request) bool {
		if !on("/identity")(r) {
			return false
		}
		problem(w, http.StatusTeapot, "BWSYNTH-unknown", `,"detail":"an unknown code"`)
		return true
	}, nil)
	f.want(f.ctl(nil, "", "whoami"), 1, "418 Injected refusal (BWSYNTH-unknown): an unknown code (instance req_injected)")
	// Both hints of a forbidden read are shown.
	in.set(refuse("/identity", http.StatusForbidden, "forbidden", `,"humanOnly":true,"roles":["approver"]`), nil)
	f.want(f.ctl(nil, "", "whoami"), 1, "This needs a person signed in", "Needs one of the roles [approver]")
	if after := f.acts(); after != before {
		t.Errorf("acts changed around the read commands:\nbefore %s\nafter  %s", before, after)
	}

	in.Close()
	f.want(f.ctl(nil, "", "whoami"), 1, "lab ("+in.URL+")", "try 5 of 5 in 8s", "connection refused")
}
