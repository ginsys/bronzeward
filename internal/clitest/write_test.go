package clitest

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/id"
)

// clusterAdd is cluster add on the oidc context "human", for a new cluster named name.
func clusterAdd(name string, extra ...string) []string {
	sum := sha256.Sum256([]byte(name))
	return append([]string{"--context", "human", "cluster", "add", "--name", name, "--endpoint", "https://" + name + ".example.test:6443",
		"--contract", "v1.13", "--talos-cluster-id", base64.URLEncoding.EncodeToString(sum[:])}, extra...)
}

// sent records the key and the expected epoch of each write the injector received.
type sent struct {
	mu             sync.Mutex
	keys, expected []string
}

// posting is a hook that records each POST and answers the nth, from 0, with step; reads go to the
// server.
func (s *sent) posting(step func(n int, w http.ResponseWriter, r *http.Request)) func(http.ResponseWriter, *http.Request) bool {
	return s.sending(http.MethodPost, step)
}

// sending is posting for requests of method.
func (s *sent) sending(method string, step func(n int, w http.ResponseWriter, r *http.Request)) func(http.ResponseWriter, *http.Request) bool {
	return func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != method {
			return false
		}
		s.mu.Lock()
		n := len(s.keys)
		s.keys, s.expected = append(s.keys, r.Header.Get("Idempotency-Key")), append(s.expected, r.Header.Get("Bronzeward-Expected-Epoch"))
		s.mu.Unlock()
		step(n, w, r)
		return true
	}
}

// one fails unless n writes were received, all with one key and one expected epoch.
func (s *sent) one(t *testing.T, n int) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.keys) != n || len(slices.Compact(slices.Clone(s.keys))) > 1 || len(slices.Compact(slices.Clone(s.expected))) > 1 {
		t.Errorf("the injector received %d POSTs with keys %v and expected epochs %v; want %d, one key and one epoch", len(s.keys), s.keys, s.expected, n)
	}
}

// drop reads the request and closes its connection without an answer.
func drop(w http.ResponseWriter, r *http.Request) {
	io.Copy(io.Discard, r.Body)
	if c, _, err := http.NewResponseController(w).Hijack(); err == nil {
		c.Close()
	}
}

// commit forwards r to the server, which answers it, and drops that answer.
func (in *injector) commit(w http.ResponseWriter, r *http.Request) {
	in.proxy.ServeHTTP(httptest.NewRecorder(), r)
	drop(w, r)
}

// newEpoch commits a new current epoch as a recovery entry does; the entry route needs a provider
// this server lacks (testdata/write-cases.yaml). It runs on the injector's goroutines too, so it
// reports with Errorf.
func (f *fixture) newEpoch() string {
	e := id.New(id.Epoch)
	for _, q := range []string{"INSERT INTO recovery_epoch (epoch, entered_at) VALUES ($1, now())", "UPDATE installation_state SET epoch = $1"} {
		if _, err := f.db.Exec(q, e); err != nil {
			f.t.Errorf("%s: %v", q, err)
		}
	}
	return e
}

// actCount is the number of acts, read as h-all: the automation token is from an earlier epoch
// once a case commits a new one.
func (f *fixture) actCount() int {
	f.t.Helper()
	q, _ := http.NewRequest(http.MethodGet, f.base+"/api/v1/acts?limit=500", nil)
	q.Header.Set("Authorization", "Bearer "+f.mint("h-all", ""))
	var page struct {
		Items []json.RawMessage `json:"items"`
		Next  string            `json:"next"`
	}
	if err := json.Unmarshal(f.do(q, http.StatusOK), &page); err != nil || page.Next != "" {
		f.t.Fatalf("acts: %v, next %q", err, page.Next)
	}
	return len(page.Items)
}

var repeatLine = regexp.MustCompile(`--idempotency-key (\S+) --expect-epoch '([^']*)' --expect-principal '([^']*)'`)

// repeatFlags are the repeat flags an outcome-unknown block printed.
func repeatFlags(t *testing.T, stderr string) []string {
	t.Helper()
	m := repeatLine.FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("no repeat flags in:\n%s", stderr)
	}
	return []string{"--idempotency-key", m[1], "--expect-epoch", m[2], "--expect-principal", m[3]}
}

// stateLeaks names the files under dir that hold a sign-in token, a refresh token or token.
func stateLeaks(t *testing.T, dir, token string) (leaked []string, files int) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		files++
		if s := string(b); err != nil || jwtPattern.MatchString(s) || refreshPattern.MatchString(s) || leaks(s, token) {
			leaked = append(leaked, p)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return leaked, files
}

// TestStateLeakScan is the state directory scan's positive control.
func TestStateLeakScan(t *testing.T) {
	dir := t.TempDir()
	for name, s := range map[string]string{"a": "x eyJhbGciOi.eyJzdWIiOi.sig y", "b": "BWSYNTH-rt-1", "c": "bwt_tok_abc.BWSYNTH-secret", "d": "epoch: ep_x"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if leaked, files := stateLeaks(t, dir, "bwt_tok_abc.BWSYNTH-secret"); len(leaked) != 3 || files != 4 || slices.Contains(leaked, filepath.Join(dir, "d")) {
		t.Fatalf("the scan flagged %v of %d files, want a, b and c of 4", leaked, files)
	}
}

// writeCases are the client-contracts.md §12 cases of the write layer (#346);
// testdata/write-cases.yaml holds their records, and TestWriteCases runs exactly those. Each starts
// signed in as h-all, with the context's epoch read.
var writeCases = map[string]func(*testing.T, *fixture, *injector, func(...string) result){
	"AMB-01": func(t *testing.T, f *fixture, in *injector, ctl func(...string) result) {
		var s sent
		post := s.posting(func(_ int, w http.ResponseWriter, r *http.Request) { in.proxy.ServeHTTP(w, r) })
		in.set(func(w http.ResponseWriter, r *http.Request) bool {
			w.Header().Set("Connection", "close")
			if on("/server")(r) {
				in.dropNext.Store(true) // the POST's connection, the next one taken
			}
			return post(w, r)
		}, nil)
		before := f.actCount()
		r := ctl(clusterAdd("amb-01")...)
		f.want(r, 0, "repeat 1 of 3 with the same key in 1s", "Cluster cl_")
		s.one(t, 1)
		if want := []string{"GET /api/v1/server", "GET /api/v1/recovery", "POST /api/v1/clusters"}; !slices.Equal(in.requests(), want) {
			t.Errorf("the injector saw %v, want %v", in.requests(), want)
		}
		if n := f.actCount(); n != before+1 {
			t.Errorf("%d acts recorded, want 1", n-before)
		}
	},
	"AMB-03": func(t *testing.T, f *fixture, in *injector, ctl func(...string) result) {
		var s sent
		in.set(s.posting(func(n int, w http.ResponseWriter, r *http.Request) {
			if n > 0 {
				in.proxy.ServeHTTP(w, r)
				return
			}
			in.proxy.ServeHTTP(httptest.NewRecorder(), r)
			problem(w, http.StatusInternalServerError, "internal-error", "")
		}), nil)
		before := f.actCount()
		r := ctl(clusterAdd("amb-03")...)
		f.want(r, 0, "500", "repeat 1 of 3", "POST /api/v1/clusters: already done earlier; showing that result.", "Cluster cl_")
		s.one(t, 2)
		if n := f.actCount(); n != before+1 {
			t.Errorf("%d acts recorded, want 1", n-before)
		}
	},
	"AMB-05": func(t *testing.T, f *fixture, in *injector, ctl func(...string) result) {
		var s sent
		var epoch atomic.Value // written by the injector's goroutine
		in.set(s.posting(func(_ int, w http.ResponseWriter, r *http.Request) {
			in.commit(w, r)
			epoch.Store(f.newEpoch())
		}), nil)
		before := f.actCount()
		r := ctl(clusterAdd("amb-05")...)
		f.want(r, 3, "set aside, not repeated: the epoch is now "+fmt.Sprint(epoch.Load())+", not the reference epoch", "GET /api/v1/clusters shows the cluster cl_",
			"(amb-05) with this Talos cluster ID", "read it again with: bronzewardctl --context 'human' cluster list", "--idempotency-key ")
		s.one(t, 1)
		if n := f.actCount(); r.out != "" || n != before+1 {
			t.Errorf("standard output %q, %d acts recorded; want none and 1", r.out, n-before)
		}
	},
	"AMB-07": func(t *testing.T, f *fixture, in *injector, ctl func(...string) result) {
		var s sent
		in.set(s.posting(func(n int, w http.ResponseWriter, r *http.Request) {
			if n == 0 {
				in.commit(w, r)
				return
			}
			f.newEpoch()
			in.proxy.ServeHTTP(w, r)
		}), nil)
		before := f.actCount()
		r := ctl(clusterAdd("amb-07")...)
		f.want(r, 3, "repeat 1 of 3", "set aside, not repeated: the key's record is from an earlier epoch", "GET /api/v1/clusters shows the cluster cl_")
		s.one(t, 2)
		if n := f.actCount(); r.out != "" || n != before+1 {
			t.Errorf("standard output %q, %d acts recorded; want none and 1", r.out, n-before)
		}
	},
	"AMB-09": func(t *testing.T, f *fixture, in *injector, ctl func(...string) result) {
		var s sent
		in.set(s.posting(func(n int, w http.ResponseWriter, r *http.Request) {
			if n == 0 {
				in.commit(w, r)
				return
			}
			problem(w, http.StatusServiceUnavailable, "dependency-unavailable", "")
		}), nil)
		before := f.actCount()
		r := ctl(clusterAdd("amb-09")...)
		f.want(r, 3, "repeat 3 of 3 with the same key in 4s", "outcome unknown: an attempt may have committed", "GET /api/v1/clusters shows the cluster cl_")
		if strings.Contains(r.err, "Not done") || r.out != "" {
			t.Errorf("reported not done, or printed %q", r.out)
		}
		s.one(t, 4)
		// The operator repeats exactly that request: the server replays the first attempt's answer.
		in.set(s.posting(func(_ int, w http.ResponseWriter, r *http.Request) { in.proxy.ServeHTTP(w, r) }), nil)
		f.want(ctl(clusterAdd("amb-09", repeatFlags(t, r.err)...)...), 0, "already done earlier; showing that result.", "Cluster cl_")
		s.one(t, 5)
		if want := []string{"GET /api/v1/recovery", "GET /api/v1/server", "POST /api/v1/clusters"}; !slices.Equal(in.requests(), want) {
			t.Errorf("the repeat sent %v, want %v", in.requests(), want)
		}
		if n := f.actCount(); n != before+1 {
			t.Errorf("%d acts recorded, want 1", n-before)
		}
	},
	"AMB-11": func(t *testing.T, f *fixture, in *injector, ctl func(...string) result) {
		var s sent
		in.set(s.posting(func(_ int, w http.ResponseWriter, r *http.Request) {
			in.commit(w, r)
			f.newEpoch()
		}), nil)
		r := ctl(clusterAdd("amb-11")...)
		f.want(r, 3, "set aside")
		flags := repeatFlags(t, r.err)
		before := f.actCount()
		f.store(f.humanFile(), f.mint("h-author", ""), "")
		in.set(s.posting(func(_ int, w http.ResponseWriter, r *http.Request) { in.proxy.ServeHTTP(w, r) }), nil)
		f.want(ctl(clusterAdd("amb-11", flags...)...), 2, "the write is not sent: the credential names "+f.iss.URL+"|h-author, not the given --expect-principal "+f.iss.URL+"|h-all")
		if seen := in.requests(); len(seen) != 0 {
			t.Errorf("under another principal the injector saw %v", seen)
		}
		f.store(f.humanFile(), f.mint("h-all", ""), "")
		in.set(s.posting(func(_ int, w http.ResponseWriter, r *http.Request) { in.proxy.ServeHTTP(w, r) }), nil)
		f.want(ctl(clusterAdd("amb-11", flags...)...), 2, "POST /api/v1/clusters not sent: the epoch is now", "not the given --expect-epoch "+flags[3])
		if seen := in.requests(); !slices.Equal(seen, []string{"GET /api/v1/recovery"}) {
			t.Errorf("in another epoch the injector saw %v, want the confirming read only", seen)
		}
		s.one(t, 1)
		if n := f.actCount(); n != before {
			t.Errorf("%d acts recorded by the refused repeats", n-before)
		}
	},
	"AMB-12": func(t *testing.T, f *fixture, in *injector, ctl func(...string) result) {
		var s sent
		var epoch atomic.Value // written by the injector's goroutine
		in.set(s.posting(func(n int, w http.ResponseWriter, r *http.Request) {
			if n == 0 {
				drop(w, r)
				return
			}
			epoch.Store(f.newEpoch())
			in.proxy.ServeHTTP(w, r)
		}), nil)
		before := f.actCount()
		r := ctl(clusterAdd("amb-12")...)
		f.want(r, 3, "set aside, not repeated: the epoch is now "+fmt.Sprint(epoch.Load())+", and this send committed nothing",
			"GET /api/v1/clusters shows no cluster with this Talos cluster ID")
		s.one(t, 2)
		if n := f.actCount(); r.out != "" || n != before {
			t.Errorf("standard output %q, %d acts recorded; want none", r.out, n-before)
		}
	},
	"TLS-02 write": func(t *testing.T, f *fixture, in *injector, ctl func(...string) result) {
		var hits atomic.Int32
		target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
		defer target.Close()
		var s sent
		in.set(s.posting(func(_ int, w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+"/api/v1/clusters", http.StatusTemporaryRedirect)
		}), nil)
		before := f.actCount()
		r := ctl(clusterAdd("tls-02")...)
		f.want(r, 3, "307", `redirecting to "`+target.URL+`/api/v1/clusters", which bronzewardctl does not follow; it is not repeated`, "outcome unknown")
		s.one(t, 1)
		if hits.Load() != 0 || r.out != "" || f.actCount() != before {
			t.Errorf("target hits %d, standard output %q: want none and no act", hits.Load(), r.out)
		}
	},
	"AUTH-07 set-aside": func(t *testing.T, f *fixture, in *injector, ctl func(...string) result) {
		f.store(f.humanFile(), f.mint("h-all", ""), "BWSYNTH-rt-1")
		f.idp.set(func(string) (int, any) { return issued(f.mint("h-author", ""), "BWSYNTH-rt-2") })
		defer f.idp.set(nil)
		var s sent
		in.set(s.posting(func(_ int, w http.ResponseWriter, _ *http.Request) {
			problem(w, http.StatusUnauthorized, "unauthenticated", "")
		}), nil)
		before := f.actCount()
		r := ctl(clusterAdd("auth-07")...)
		f.want(r, 3, "set aside, not repeated: the credential now names "+f.iss.URL+"|h-author, not the principal "+f.iss.URL+"|h-all the write was sent as")
		s.one(t, 1)
		if n := f.idp.calls.Load(); n != 1 || r.out != "" || f.actCount() != before {
			t.Errorf("%d refresh grants, standard output %q: want 1, none and no act", n, r.out)
		}
	},
	"ETAG-01": func(t *testing.T, f *fixture, in *injector, ctl func(...string) result) {
		s := f.seed()
		var deletes atomic.Int32
		in.set(func(_ http.ResponseWriter, r *http.Request) bool {
			if r.Method == http.MethodDelete {
				deletes.Add(1)
			}
			return false
		}, nil)
		f.want(ctl("--context", "human", "draft", "show", s.draft), 0, s.draft)
		// Another author's write moves the draft as a draft update does (internal/api/runner.go); an
		// update that saves an entry needs the provider this server lacks.
		if _, err := f.db.Exec(`UPDATE draft SET revision = revision + 1, etag_token = $2 WHERE id = $1`, s.draft, "bwsynthotherauthorwritesaa"); err != nil {
			t.Fatal(err)
		}
		before := f.actCount()
		rm := []string{"--context", "human", "profile", "rm", "workers", "--draft", s.draft}
		for n := int32(1); n <= 2; n++ { // run again: the kept ETag was neither refetched nor replaced
			r := ctl(rm...)
			f.want(r, 1, "412", "precondition-failed", "The draft changed since you last read it (you saw revision 1, it is now revision 2). Nothing was saved.",
				"bronzewardctl --context 'human' draft show '"+s.draft+"'")
			if deletes.Load() != n || r.out != "" {
				t.Errorf("run %d: %d DELETEs, standard output %q; want %d and none", n, deletes.Load(), r.out, n)
			}
		}
		if f.actCount() != before {
			t.Error("a refused draft write recorded an act")
		}
	},
}

func TestWriteCases(t *testing.T) {
	f := start(t, "viewer")
	in := newInjector(t, f.base)
	in.writing = true
	b, err := os.ReadFile("testdata/write-cases.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var records []struct {
		ID string `yaml:"id"`
	}
	if err := yaml.Unmarshal(b, &records); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range records {
		ids = append(ids, r.ID)
	}
	for id := range writeCases {
		if !slices.Contains(ids, id) {
			t.Errorf("case %s runs without a record", id)
		}
	}
	f.want(f.ctl(nil, "", "context", "add", "human", "--server", in.URL, "--certificate-authority", in.ca,
		"--issuer", f.iss.URL, "--client-id", "BWSYNTH-cli", "--audience", "bronzeward", "--oidc-token-file", f.humanFile()), 0, "credential oidc")
	for _, id := range ids {
		run := writeCases[id]
		if run == nil {
			t.Errorf("record %s has no case", id)
			continue
		}
		t.Run(id, func(t *testing.T) {
			g := *f
			g.t = t
			ctl := func(args ...string) result { return g.scan(g.exec(nil, "", args...), args) }
			g.store(g.humanFile(), g.mint("h-all", ""), "")
			in.set(nil, nil)
			g.want(ctl("--context", "human", "whoami"), 0, "h-all")
			run(t, &g, in, ctl)
		})
	}
	f.scanFiles()
}

// scanFiles fails on a token in the CLI's non-secret files: its state directory, which holds the
// draft ETags ETAG-01 kept (a token file's renewal lock sits beside it), and its contexts file,
// which every case rewrote, so the scan never reads nothing.
func (f *fixture) scanFiles() {
	f.t.Helper()
	files := 0
	for _, dir := range []string{filepath.Join(f.home, ".local", "state", "bronzeward"), filepath.Join(f.home, "bronzeward")} {
		leaked, n := stateLeaks(f.t, dir, f.token)
		if files += n; len(leaked) > 0 {
			f.t.Errorf("%v hold a token", leaked)
		}
	}
	if files == 0 {
		f.t.Error("the scan read no file")
	}
}

// warnedBeforeAnswer reports whether standard error holds the warning line and then answer in the
// lines after it. The warning line starts with the context's address, which holds the test
// server's random port, so a search from the start of standard error can match a status code in
// the port (40312 holds "403").
func warnedBeforeAnswer(stderr, warning, answer string) bool {
	i := strings.Index(stderr, warning)
	if i < 0 {
		return false
	}
	_, after, _ := strings.Cut(stderr[i:], "\n")
	return strings.Contains(after, answer)
}

// TestWarnedBeforeAnswer is the negative control for the order check of TestWrites. The context
// address holds "403" in its port, and the old check, the first "403" against the warning's
// position, reports the warning as late. The helper must not.
func TestWarnedBeforeAnswer(t *testing.T) {
	const warning = "is for a person signed in"
	line := func(port string) string {
		return "lab (https://127.0.0.1:" + port + "): POST /api/v1/clusters " + warning + ", not an automation token: the server refuses it.\n"
	}
	answer := "403 (forbidden)\nThis needs a person signed in, not an automation token.\n"
	for _, c := range []struct {
		name, stderr string
		want         bool
	}{
		{"a port without 403", line("41234") + answer, true},
		{"a port that starts with 403", line("40312") + answer, true},
		{"a port that ends with 403", line("34403") + answer, true},
		{"the answer before the warning", answer + line("41234"), false},
		{"no answer after the warning", line("40312"), false},
		{"no warning", answer, false},
	} {
		if got := warnedBeforeAnswer(c.stderr, warning, "403"); got != c.want {
			t.Errorf("%s: warnedBeforeAnswer = %v, want %v\n%s", c.name, got, c.want, c.stderr)
		}
	}
	// The old check, kept here only to show that the control catches it.
	old := line("40312") + answer
	if strings.Index(old, warning) <= strings.Index(old, "403") {
		t.Errorf("the control no longer holds: the old check passes on a port with 403, so this test cannot show the fix")
	}
}

// TestWrites runs the three writes through the CLI: success, the human-only warning and refusal
// for an automation token on each route, and the roles a human without author is shown.
func TestWrites(t *testing.T) {
	f := start(t, "viewer,author")
	s := f.seed()
	in := newInjector(t, f.base)
	in.writing = true
	f.want(f.ctl(nil, "", "context", "add", "lab", "--server", in.URL, "--certificate-authority", in.ca, "--automation-token-file", f.tokenFile), 0)
	f.want(f.ctl(nil, "", "context", "add", "human", "--server", in.URL, "--certificate-authority", in.ca,
		"--issuer", f.iss.URL, "--client-id", "BWSYNTH-cli", "--audience", "bronzeward", "--oidc-token-file", f.humanFile()), 0)
	ctl := func(args ...string) result { return f.scan(f.exec(nil, "", args...), args) }
	routes := map[string][]string{
		"POST /api/v1/clusters": clusterAdd("lab-w")[2:],
		"POST /api/v1/machines": {"machine", "add", "--cluster", s.cluster, "--smbios-uuid", "1c6b7d2f-3a4e-4f60-9bac-1d2e3f4a5b6c",
			"--serial", "SN-2", "--platform", "metal", "--talos-endpoint", "10.55.0.9"},
		"POST /api/v1/machines/" + s.machine + "/talos-endpoints": {"machine", "set-endpoint", s.machine, "--talos-endpoint", "10.55.0.4:50001"},
	}
	before := f.actCount()
	for route, args := range routes {
		in.set(nil, nil)
		r := ctl(append([]string{"--context", "lab"}, args...)...)
		f.want(r, 1, route+" is for a person signed in, not an automation token: the server refuses it.", "403", "(forbidden)",
			"This needs a person signed in, not an automation token.")
		if !warnedBeforeAnswer(r.err, "is for a person signed in", "403") || r.out != "" {
			t.Errorf("%s: the warning does not come before the answer, or standard output holds %q", route, r.out)
		}
		if seen := in.requests(); !slices.Contains(seen, route) {
			t.Errorf("%s was not sent: %v", route, seen)
		}
		f.store(f.humanFile(), f.mint("h-viewer", ""), "")
		f.want(ctl(append([]string{"--context", "human"}, args...)...), 1, "403", "Needs one of the roles [author", "You are signed in as h-viewer.")
	}
	if n := f.actCount(); n != before {
		t.Errorf("%d acts recorded by refused writes", n-before)
	}
	f.store(f.humanFile(), f.mint("h-all", ""), "")
	in.set(nil, nil)
	f.want(ctl(append([]string{"--context", "human"}, routes["POST /api/v1/clusters"]...)...), 0, "Cluster cl_", "recorded: lab-w, endpoint https://lab-w.example.test:6443, contract v1.13")
	r := ctl(append([]string{"--context", "human", "-o", "json"}, routes["POST /api/v1/machines"]...)...)
	var m struct{ ID, TalosEndpoint string }
	if err := json.Unmarshal([]byte(r.out), &m); r.code != 0 || err != nil || !strings.HasPrefix(m.ID, "mch_") || m.TalosEndpoint != "10.55.0.9:50000" {
		t.Errorf("machine add -o json: exit %d, %v, %+v; want the machine with the default port\n%s", r.code, err, m, r.err)
	}
	r = ctl("--context", "human", "machine", "add", "--cluster", s.cluster, "--talos-node-id", "BWSYNTH-node-3", "--platform", "container", "--talos-endpoint", "10.55.0.10")
	f.want(r, 0, "recorded in cluster "+s.cluster+" (lab-a). Endpoint 10.55.0.10:50000.",
		"Next: give Bronzeward the cluster's Talos access in the provider, then import its configuration.")
	f.want(ctl(append([]string{"--context", "human"}, routes["POST /api/v1/machines/"+s.machine+"/talos-endpoints"]...)...), 0,
		fmt.Sprintf("Machine %s: Talos endpoint 10.55.0.4:50001, was 10.55.0.3:50000", s.machine))
	if n := f.actCount(); n != before+4 {
		t.Errorf("%d acts recorded by four writes", n-before)
	}
	f.scanFiles()
}
