package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// The stand-in of cluster first-key's tests: a server that records each first-key request and answers
// as the test says. Every key is a planted marker (plantedKeyA and plantedKeyB of onboard_fake_test.go),
// so a test can search every byte the command prints or writes for it.

const (
	fkCluster = "cl_FKSYNTH"
	fkPlanted = "PLANTED-TALOS-TEXT" // what a hostile or careless server puts in a refusal's words
)

// fkPost is one POST of a first key as the server saw it.
type fkPost struct {
	path, key string
	in        apitypes.FirstKeyInput
}

type firstKeyFake struct {
	mu    sync.Mutex
	posts []fkPost
	log   []string // every request, "METHOD path"
	// answer replies to the n-th POST (from 1); nil is the success: replaced from the second one on.
	answer  func(n int, w http.ResponseWriter, r *http.Request)
	arrived chan struct{} // one value per POST that reached the server
}

func (f *firstKeyFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	w.Header().Set("Bronzeward-Epoch", "ep_FK-1")
	w.Header().Set("Content-Type", "application/json")
	f.mu.Lock()
	f.log = append(f.log, r.Method+" "+path)
	f.mu.Unlock()
	switch {
	case path == "/server":
		fmt.Fprint(w, `{"version":"FK","apiRevision":2,"startMode":"normal"}`)
	case path == "/identity":
		fmt.Fprint(w, `{"kind":"human","principal":"h_FK","roles":["author"]}`)
	case path == "/recovery":
		fmt.Fprint(w, `{}`)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/first-keys"):
		body, _ := io.ReadAll(r.Body)
		p := fkPost{path: path, key: r.Header.Get("Idempotency-Key")}
		_ = json.Unmarshal(body, &p.in)
		f.mu.Lock()
		f.posts = append(f.posts, p)
		n := len(f.posts)
		f.mu.Unlock()
		if f.arrived != nil {
			f.arrived <- struct{}{}
		}
		if f.answer != nil {
			r.Body = io.NopCloser(bytes.NewReader(body)) // an answer may quote the request back
			f.answer(n, w, r)
			return
		}
		fmt.Fprintf(w, `{"cluster":%q,"kind":%q,"replaced":%t,"caKeyVersion":{"version":%d,"createdTime":"2026-11-19T21:00:00Z"}}`,
			fkCluster, p.in.Kind, n > 1, n)
	default:
		http.NotFound(w, r)
	}
}

// refuse answers with a problem document whose detail is the planted server text.
func refuse(status int, code, extra string) func(int, http.ResponseWriter, *http.Request) {
	return func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"type":"urn:bronzeward:problem:%s","title":"FK","status":%d,"detail":%q,"instance":"req_FK"%s}`, code, status, fkPlanted, extra)
	}
}

func (f *firstKeyFake) requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.log)
}

func (f *firstKeyFake) sent() []fkPost {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fkPost(nil), f.posts...)
}

// newFirstKey starts the fake, gives the CLI a context for it and writes a two-context talosconfig.
func newFirstKey(t *testing.T) (*firstKeyFake, string) {
	t.Helper()
	f := &firstKeyFake{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	automationContext(t, srv.URL)
	file := filepath.Join(t.TempDir(), "talosconfig")
	if err := os.WriteFile(file, []byte(twoContextTalosconfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	return f, file
}

func fkArgs(file string, extra ...string) []string {
	return append([]string{"cluster", "first-key", "--cluster", fkCluster, "--kind", "talosconfig", "--file", file, "--endpoint", "10.0.0.1"}, extra...)
}

// TestFirstKeySendsOneContextOnce: the talosconfig is cut to its current context, or to the one
// --talos-context names, and nothing of the other reaches the server. Another kind is sent as the file
// is. Each run is one POST under a key of its own, so a rerun is never replayed as the first run's
// answer: it says it replaced the key.
func TestFirstKeySendsOneContextOnce(t *testing.T) {
	f, file := newFirstKey(t)
	code, out, errOut := runCtl(t, fkArgs(file)...)
	if code != 0 || !strings.Contains(out, "Cluster "+fkCluster+": first key (talosconfig) stored as CA key version 1.") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	code, out, errOut = runCtl(t, fkArgs(file)...)
	if code != 0 || !strings.Contains(out, "version 2, replacing the earlier key.") {
		t.Fatalf("a rerun: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if code, _, errOut = runCtl(t, fkArgs(file, "--talos-context", "b")...); code != 0 {
		t.Fatalf("--talos-context b: exit %d, stderr %q", code, errOut)
	}
	machine := filepath.Join(filepath.Dir(file), "machine.yaml")
	if err := os.WriteFile(machine, []byte("version: v1alpha1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut = runCtl(t, "cluster", "first-key", "--cluster", fkCluster, "--kind", "machine-config", "--file", machine, "--endpoint", "10.0.0.2:50000"); code != 0 {
		t.Fatalf("machine-config: exit %d, stderr %q", code, errOut)
	}
	posts := f.sent()
	if len(posts) != 4 {
		t.Fatalf("%d first-key requests, want 4 (one per run)", len(posts))
	}
	for i, p := range posts {
		if p.path != "/clusters/"+fkCluster+"/first-keys" || !client.KeyShape.MatchString(p.key) {
			t.Errorf("request %d: path %q, key %q", i, p.path, p.key)
		}
	}
	if posts[0].key == posts[1].key {
		t.Errorf("a rerun carried the first run's key %q: the key is random, never derived from the document", posts[0].key)
	}
	a, b := b64(plantedKeyA), b64(plantedKeyB)
	if d := posts[0].in; d.Kind != "talosconfig" || d.Endpoint != "10.0.0.1" || !strings.Contains(d.Document, a) || strings.Contains(d.Document, b) || strings.Contains(d.Document, b64("ca-b")) {
		t.Errorf("the default context: kind %q, endpoint %q, document %q", d.Kind, d.Endpoint, d.Document)
	}
	if d := posts[2].in.Document; !strings.Contains(d, b) || strings.Contains(d, a) {
		t.Errorf("--talos-context b: document %q", d)
	}
	if d := posts[3].in; d.Kind != "machine-config" || d.Document != "version: v1alpha1\n" || d.Endpoint != "10.0.0.2:50000" {
		t.Errorf("machine-config: %+v", d)
	}
}

// TestFirstKeyRefusals: a refusal names its rule and nothing of the server's words, an unknown rule from
// a newer server included; none is repeated; a 503 that may follow a store is an unknown outcome (3).
func TestFirstKeyRefusals(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer func(int, http.ResponseWriter, *http.Request)
		code   int
		want   []string
	}{
		{"no CA key", refuse(422, "validation-failed", `,"rule":"first-key-no-ca-key"`), 1, []string{"the rule first-key-no-ca-key", "Nothing was stored"}},
		{"a rule of a newer server", refuse(422, "validation-failed", `,"rule":"first-key-cluster-mismatch"`), 1, []string{"the rule first-key-cluster-mismatch"}},
		{"the node could not be tried", refuse(503, "talos-access-unavailable", ""), 1, []string{"could not be tried at that endpoint", "Nothing was stored"}},
		{"the key may be stored", refuse(503, "dependency-unavailable", ""), 3, []string{"outcome unknown", "--idempotency-key"}},
		{"the store may follow a deadlock", refuse(503, "transient-conflict", ""), 3, []string{"outcome unknown", "--idempotency-key"}},
		{"an internal error", refuse(500, "internal-error", ""), 3, []string{"outcome unknown", "--idempotency-key"}},
		{"a 503 that is no problem", func(_ int, w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(503)
			fmt.Fprint(w, fkPlanted)
		}, 3, []string{"outcome unknown", "--idempotency-key"}},
		{"a person is needed", refuse(403, "forbidden", `,"humanOnly":true`), 1, []string{"needs a person signed in"}},
		{"the key moved under it", refuse(409, "conflict", ""), 1, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, file := newFirstKey(t)
			f.answer = c.answer
			code, out, errOut := runCtl(t, fkArgs(file)...)
			if code != c.code || out != "" {
				t.Errorf("exit %d, stdout %q, stderr %q; want %d and no output", code, out, errOut, c.code)
			}
			for _, w := range c.want {
				if !strings.Contains(errOut, w) {
					t.Errorf("no %q in %q", w, errOut)
				}
			}
			if strings.Contains(errOut, fkPlanted) {
				t.Errorf("the server's words reached standard error: %q", errOut)
			}
			if n := len(f.sent()); n != 1 {
				t.Errorf("%d first-key requests, want 1: the route is sent once", n)
			}
		})
	}
}

// TestFirstKeyUsage: every input fault is exit 2 before any request, names its flag and quotes nothing
// of the file, one holding a planted marker included.
func TestFirstKeyUsage(t *testing.T) {
	f, file := newFirstKey(t)
	dir := filepath.Dir(file)
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	big := write("big", []byte(strings.Repeat("a", 513<<10)))
	bad := write("bad", []byte("\xff\xfe"+plantedKeyA))
	empty := write("empty", nil)
	broken := write("broken", []byte("contexts: ["+plantedKeyA))
	cl := []string{"cluster", "first-key"}
	for _, c := range []struct {
		name string
		args []string
		flag string
	}{
		{"no cluster", []string{"--kind", "talosconfig", "--file", file, "--endpoint", "10.0.0.1"}, "--cluster"},
		{"no kind", []string{"--cluster", fkCluster, "--file", file, "--endpoint", "10.0.0.1"}, "--kind"},
		{"no file", []string{"--cluster", fkCluster, "--kind", "talosconfig", "--endpoint", "10.0.0.1"}, "--file"},
		{"no endpoint", []string{"--cluster", fkCluster, "--kind", "talosconfig", "--file", file}, "--endpoint"},
		{"another kind", []string{"--cluster", fkCluster, "--kind", "pem", "--file", file, "--endpoint", "10.0.0.1"}, "--kind"},
		{"a context for a machine config", []string{"--cluster", fkCluster, "--kind", "machine-config", "--file", file, "--endpoint", "10.0.0.1", "--talos-context", "a"}, "--talos-context"},
		{"a context the file lacks", []string{"--cluster", fkCluster, "--kind", "talosconfig", "--file", file, "--endpoint", "10.0.0.1", "--talos-context", "nope"}, "--talos-context"},
		{"no such file", []string{"--cluster", fkCluster, "--kind", "talosconfig", "--file", filepath.Join(dir, "absent"), "--endpoint", "10.0.0.1"}, "--file"},
		{"a file over the limit", []string{"--cluster", fkCluster, "--kind", "machine-config", "--file", big, "--endpoint", "10.0.0.1"}, "--file"},
		{"a file that is not text", []string{"--cluster", fkCluster, "--kind", "machine-config", "--file", bad, "--endpoint", "10.0.0.1"}, "--file"},
		{"an empty file", []string{"--cluster", fkCluster, "--kind", "machine-config", "--file", empty, "--endpoint", "10.0.0.1"}, "--file"},
		{"a talosconfig that does not parse", []string{"--cluster", fkCluster, "--kind", "talosconfig", "--file", broken, "--endpoint", "10.0.0.1"}, "--file"},
		{"half a repeat", []string{"--cluster", fkCluster, "--kind", "talosconfig", "--file", file, "--endpoint", "10.0.0.1", "--idempotency-key", "abcdefghijklmnop"}, "--expect-epoch"},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, out, errOut := runCtl(t, append(cl, c.args...)...)
			if code != 2 || out != "" || !strings.Contains(errOut, c.flag) || strings.Contains(errOut, plantedKeyA) {
				t.Errorf("exit %d, stdout %q, stderr %q; want 2, no output, the flag %s and no marker", code, out, errOut, c.flag)
			}
		})
	}
	if n := f.requests(); n != 0 {
		t.Errorf("%d requests left before the input was whole", n)
	}
}

// TestFirstKeyNeverLeavesTheKey: the canary is the key of the chosen context, and the other context's.
// Across a success, a refusal whose words quote the document back, an unknown outcome, -v, -o json and
// -o yaml, it is in no output, and in no file the command wrote beside the key file or into the
// client's own state. The walk has a control: a file holding the canary is found.
func TestFirstKeyNeverLeavesTheKey(t *testing.T) {
	f, file := newFirstKey(t)
	state := os.Getenv("XDG_CONFIG_HOME")
	canaries := []string{plantedKeyA, b64(plantedKeyA), plantedKeyB, b64(plantedKeyB)}
	found := func(root string) (hits []string) {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || p == file {
				return nil
			}
			b, _ := os.ReadFile(p)
			for _, c := range canaries {
				if strings.Contains(string(b), c) {
					hits = append(hits, p)
					break
				}
			}
			return nil
		})
		return hits
	}
	control := filepath.Join(state, "control")
	if err := os.WriteFile(control, []byte(b64(plantedKeyA)), 0o600); err != nil || len(found(state)) != 1 {
		t.Fatalf("the walk missed a file that holds the canary: %v, err %v", found(state), err)
	}
	if err := os.Remove(control); err != nil {
		t.Fatal(err)
	}
	quoting := func(_ int, w http.ResponseWriter, r *http.Request) {
		// The most hostile server: its detail is the document it was sent.
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(422)
		fmt.Fprintf(w, `{"type":"urn:bronzeward:problem:validation-failed","title":"FK","status":422,"detail":%q,"rule":"first-key-unusable","instance":"req_FK"}`, body)
	}
	var all strings.Builder
	for _, c := range []struct {
		answer func(int, http.ResponseWriter, *http.Request)
		args   []string
		code   int
		shows  string // what the run prints, so a run that stopped before the request cannot pass
	}{
		{nil, nil, 0, "stored as CA key version"},
		{quoting, nil, 1, "the rule first-key-unusable"},
		{refuse(503, "dependency-unavailable", ""), nil, 3, "outcome unknown"},
		{nil, []string{"-o", "json"}, 0, `"caKeyVersion"`},
		{nil, []string{"-o", "yaml"}, 0, "caKeyVersion:"},
		{quoting, []string{"-v"}, 1, "the rule first-key-unusable"},
	} {
		f.answer = c.answer
		args := fkArgs(file)
		if len(c.args) > 0 { // global flags come before the command
			args = append(append([]string(nil), c.args...), args...)
		}
		before := len(f.sent())
		code, out, errOut := runCtl(t, args...)
		if code != c.code || !strings.Contains(out+errOut, c.shows) || len(f.sent()) != before+1 {
			t.Errorf("%v: exit %d (want %d), %d requests sent (want 1), output %q; want it to hold %q", c.args, code, c.code, len(f.sent())-before, out+errOut, c.shows)
		}
		all.WriteString(out + errOut)
	}
	for _, c := range append([]string{fkPlanted}, canaries...) {
		if strings.Contains(all.String(), c) {
			t.Errorf("the canary %q is in what the command printed:\n%s", c, all.String())
		}
	}
	if hits := found(state); len(hits) != 0 {
		t.Errorf("the canary is in the client's state: %v", hits)
	}
	if hits := found(filepath.Dir(file)); len(hits) != 0 {
		t.Errorf("the command copied the key beside the key file: %v", hits)
	}
	entries, _ := os.ReadDir(filepath.Dir(file))
	if len(entries) != 1 {
		t.Errorf("%d files beside the key file, want only the key file", len(entries))
	}
}

// TestFirstKeyRepeatStaysUnknown: a repeat of an unknown outcome, answered by a refusal that is not a
// replay, leaves the outcome unknown (exit 3 and the repeat flags): the refusal says nothing of the
// attempt it repeats, so "nothing was stored" is never printed for it.
func TestFirstKeyRepeatStaysUnknown(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer func(int, http.ResponseWriter, *http.Request)
	}{
		{"a refusal by a rule", refuse(422, "validation-failed", `,"rule":"first-key-no-ca-key"`)},
		{"the node not tried", refuse(503, "talos-access-unavailable", "")},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, file := newFirstKey(t)
			f.answer = c.answer
			code, out, errOut := runCtl(t, fkArgs(file, "--idempotency-key", "abcdefghijklmnop", "--expect-epoch", "ep_FK-1", "--expect-principal", "h_FK")...)
			if code != 3 || out != "" || !strings.Contains(errOut, "outcome unknown") || strings.Contains(errOut, "Nothing was stored") || strings.Contains(errOut, fkPlanted) {
				t.Errorf("exit %d, stdout %q, stderr %q; want 3 and the unknown-outcome block, with no claim that nothing was stored", code, out, errOut)
			}
			if n := len(f.sent()); n != 1 {
				t.Errorf("%d first-key requests, want 1", n)
			}
		})
	}
}

// TestFirstKeyCtrlCLeavesTheOutcomeUnknown: a Ctrl-C while the request is in flight ends it, prints
// the unknown-outcome block and exits 3, with no further request. The block's repeat, given back,
// sends the same key.
func TestFirstKeyCtrlCLeavesTheOutcomeUnknown(t *testing.T) {
	f, file := newFirstKey(t)
	f.arrived = make(chan struct{}, 4)
	f.answer = func(_ int, _ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }
	go func() {
		<-f.arrived // the handler is armed before the request is sent
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
	}()
	start := time.Now()
	code, out, errOut := runCtl(t, fkArgs(file)...)
	if code != 3 || out != "" || !strings.Contains(errOut, "stopped at your request while the request was in flight") {
		t.Fatalf("exit %d, stdout %q, stderr %q; want 3, no output and the block", code, out, errOut)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("the Ctrl-C took %s to end the request", d)
	}
	first := f.sent()
	if len(first) != 1 {
		t.Fatalf("%d first-key requests, want 1", len(first))
	}
	m := regexp.MustCompile(`--idempotency-key (\S+) --expect-epoch '([^']*)' --expect-principal '([^']*)'`).FindStringSubmatch(errOut)
	if m == nil || m[1] != first[0].key {
		t.Fatalf("no repeat of the sent key %q in %q", first[0].key, errOut)
	}
	f.answer = nil
	code, out, errOut = runCtl(t, fkArgs(file, "--idempotency-key", m[1], "--expect-epoch", m[2], "--expect-principal", m[3])...)
	if code != 0 || !strings.Contains(out, "stored as CA key version") {
		t.Fatalf("the repeat: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if all := f.sent(); len(all) != 2 || all[1].key != first[0].key {
		t.Errorf("the repeat sent %+v, want the same key %q", all, first[0].key)
	}
}
