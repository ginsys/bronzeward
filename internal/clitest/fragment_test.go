package clitest

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/baotest"
)

var update = flag.Bool("update", false, "rewrite testdata/journeys from this run")

// The journey normalizers live here, in a file with no build constraint, because the Linux-only
// protected_test.go and the untagged timeline_test.go both use them.
var (
	// controllerID is a controller's identifier in the CLI's output: its instance, process and a
	// random part, which differ on every run.
	controllerID = regexp.MustCompile(`[^,\s]+/\d+/[A-Z2-7]{26}`)
	// eventTimes are operation watch's event times, the time this client received each event.
	eventTimes = regexp.MustCompile(`(?m)^(received) \d\d:\d\d:\d\d  `)
)

// slowBao is a proxy to the test's OpenBao that holds each KV write for delay: an ingestion
// writes the values it extracts there (internal/provider/paths.go kvDataPath), so a draft update
// it holds is still ingesting, its claim live.
type slowBao struct {
	*httptest.Server
	delay  atomic.Int64
	b      *baotest.Bao
	digest string // this server's digest key, its own so that a case may rotate it
	// hold, when set, keeps each staging decrypt, a continued or taken-over run's first provider
	// call, until it closes or the server that sent it goes away; held counts the decrypts kept.
	hold atomic.Pointer[chan struct{}]
	held atomic.Int32
	// fail, when set, is a path prefix whose writes the proxy answers 503, as an unavailable
	// provider does.
	fail atomic.Pointer[string]
}

// retire makes the digest key's current versions uncomputable: it rotates the key and raises its
// minimum encryption version, below which Transit computes no HMAC.
func (sb *slowBao) retire(t *testing.T) {
	t.Helper()
	status, _, err := sb.b.Do(sb.b.Admin(), http.MethodPost, "/v1/transit/keys/"+sb.digest+"/rotate", nil)
	if err == nil && status/100 == 2 {
		var body []byte
		var latest string
		status, body, err = sb.b.Do(sb.b.Admin(), http.MethodGet, "/v1/transit/keys/"+sb.digest, nil)
		if err == nil && status/100 == 2 {
			latest, err = latestVersion(body)
		}
		if err == nil && status/100 == 2 {
			status, _, err = sb.b.Do(sb.b.Admin(), http.MethodPost, "/v1/transit/keys/"+sb.digest+"/config",
				map[string]any{"min_encryption_version": latest})
		}
	}
	if err != nil || status/100 != 2 {
		t.Errorf("retiring the digest key's versions: status %d, %v", status, err)
	}
}

// latestVersion is the latest_version of a Transit key read. Without one, retire cannot raise the
// minimum, and the case would run without the fault it depends on.
func latestVersion(body []byte) (string, error) {
	m := regexp.MustCompile(`"latest_version":(\d+)`).FindSubmatch(body)
	if m == nil {
		return "", errors.New("the key read has no latest_version")
	}
	return string(m[1]), nil
}

// TestLatestVersion checks that a key read without latest_version is an error, so that retire
// reports it instead of skipping the minimum it raises. It needs no server.
func TestLatestVersion(t *testing.T) {
	if v, err := latestVersion([]byte(`{"data":{"latest_version":3,"min_encryption_version":0}}`)); v != "3" || err != nil {
		t.Errorf("%q, %v; want 3", v, err)
	}
	if v, err := latestVersion([]byte(`{"data":{"min_encryption_version":0}}`)); err == nil {
		t.Errorf("%q, no error; want the missing latest_version reported", v)
	}
}

// startProvider is start with an OpenBao provider, reached through a slowBao, so that a fragment
// write can ingest; it needs BW_TEST_BAO_ADDR (baotest). Its digest key is its own, deleted when
// the test ends.
func startProvider(t *testing.T, roles string) (*fixture, *slowBao) {
	t.Helper()
	return startTimers(t, roles, "{instance: a, heartbeat: 5s, lease: 15s, absoluteExpiry: 10m, sweep: 15s}")
}

// startTimers is startProvider with the server's ingestion claim timers, a YAML flow mapping.
func startTimers(t *testing.T, roles, timers string) (*fixture, *slowBao) {
	t.Helper()
	b := baotest.New(t)
	digest := b.Name("digest")
	if status, body, err := b.Do(b.Admin(), http.MethodPost, "/v1/transit/keys/"+digest, map[string]any{}); err != nil || status/100 != 2 {
		t.Fatalf("creating the digest key: %d %v %s", status, err, body)
	}
	t.Cleanup(func() {
		b.Do(b.Admin(), http.MethodPost, "/v1/transit/keys/"+digest+"/config", map[string]any{"deletion_allowed": true})
		if status, _, err := b.Do(b.Admin(), http.MethodDelete, "/v1/transit/keys/"+digest, nil); err != nil || status/100 != 2 {
			t.Errorf("deleting the digest key: status %d, %v", status, err)
		}
	})
	b.Policy("digest", "path \"transit/hmac/"+digest+"\" {\n  capabilities = [\"update\"]\n}\n")
	u, err := url.Parse(b.Addr)
	if err != nil {
		t.Fatal(err)
	}
	rp := newBackendProxy(t, u)
	rp.Transport = wholeTransit(rp.Transport)
	sb := &slowBao{b: b, digest: digest}
	sb.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := sb.fail.Load(); p != nil && r.Method != http.MethodGet && strings.HasPrefix(r.URL.Path, *p) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/secret/data/") {
			time.Sleep(time.Duration(sb.delay.Load()))
		}
		if h := sb.hold.Load(); h != nil && strings.HasPrefix(r.URL.Path, "/v1/transit/decrypt/") {
			sb.held.Add(1)
			select {
			case <-*h:
			case <-r.Context().Done():
				return
			}
		}
		rp.ServeHTTP(w, r)
	}))
	t.Cleanup(sb.Close)
	dir := t.TempDir()
	cfg := "provider:\n  address: " + sb.URL + "\n  keys: {baseline: bw-baseline, staging: bw-staging, digest: " + digest + ", artifact: bw-artifact}\n"
	for _, p := range []string{"ingestion", "compiler", "metadata", "executor"} {
		file := filepath.Join(dir, "openbao-"+p+".token")
		policies := []string{"bw-" + p}
		if p == "ingestion" {
			policies = append(policies, "digest")
		}
		if err := os.WriteFile(file, []byte(b.Token(policies...)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg += "  " + p + "TokenFile: " + file + "\n"
	}
	cfg += "ingestion: " + timers + "\n"
	return startConfig(t, roles, "", "bronzeward", cfg), sb
}

// Synthetic sentinels a fragment document and its marks carry: none may reach argv, standard
// output or error, the CLI's files or a temporary file (client-contracts.md §11.5).
const (
	docSecret = "BWSYNTH-doc-secret"
	markKey   = "BWSYNTH-mark-key"
	markValue = "BWSYNTH-marked-value"
)

var sentinels = []string{docSecret, markKey, markValue}

// protectedLeaks names the sentinels s holds.
func protectedLeaks(s string) []string {
	return slices.DeleteFunc(slices.Clone(sentinels), func(v string) bool { return !strings.Contains(s, v) })
}

// fragmentEnv is one fragment case's state: the seeded draft, the files it writes documents to,
// and the injector's PUT log.
type fragmentEnv struct {
	draft, dir string
	bao        *slowBao
}

// doc writes a synthetic fragment document for name, variant v, and returns its path.
func (e *fragmentEnv) doc(t *testing.T, name, v string) string {
	t.Helper()
	p := filepath.Join(e.dir, name+"-"+v+".yaml")
	body := "machine:\n  token: " + docSecret + "-" + name + "-" + v + "\n  nodeLabels:\n    tier: " + v + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *fragmentEnv) set(name, file string, extra ...string) []string {
	return append([]string{"--context", "human", "fragment", "set", name, "--draft", e.draft, "--layer", "role", "--file", file}, extra...)
}

var ifMatchLine = regexp.MustCompile(`--if-match '([^']*)'`)

// printedRepeat is the repeat flags an outcome-unknown block printed, --if-match included.
func printedRepeat(t *testing.T, stderr string) []string {
	t.Helper()
	m := ifMatchLine.FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("no --if-match in:\n%s", stderr)
	}
	return append(repeatFlags(t, stderr), "--if-match", m[1])
}

// detached is r with its body read, under a context its client's departure does not cancel.
func detached(r *http.Request) *http.Request {
	b, _ := io.ReadAll(r.Body)
	q := r.Clone(context.WithoutCancel(r.Context()))
	q.Body = io.NopCloser(bytes.NewReader(b))
	return q
}

// moveDraft moves the draft as another author's write does (internal/api/runner.go).
func (f *fixture) moveDraft(draft, token string) {
	f.t.Helper()
	if _, err := f.db.Exec(`UPDATE draft SET revision = revision + 1, etag_token = $2 WHERE id = $1`, draft, token); err != nil {
		f.t.Fatal(err)
	}
}

// draftETag reads the draft's current ETag as h-all.
func (f *fixture) draftETag(draft string) string {
	f.t.Helper()
	q, _ := http.NewRequest(http.MethodGet, f.base+"/api/v1/drafts/"+draft, nil)
	q.Header.Set("Authorization", "Bearer "+f.mint("h-all", ""))
	r, err := http.DefaultClient.Do(q)
	if err != nil {
		f.t.Fatal(err)
	}
	r.Body.Close()
	return r.Header.Get("ETag")
}

// fragmentCases are the client-contracts.md §12 cases of fragment set (#347);
// testdata/fragment-cases.yaml holds their records, and TestFragments runs exactly those. Each
// starts signed in as h-all, with the draft read and its ETag kept (T11).
var fragmentCases = map[string]func(*testing.T, *fixture, *injector, *fragmentEnv, func(...string) result){
	"AMB-02": func(t *testing.T, f *fixture, in *injector, e *fragmentEnv, ctl func(...string) result) {
		var s sent
		var first sync.WaitGroup
		in.set(s.sending(http.MethodPut, func(n int, w http.ResponseWriter, r *http.Request) {
			if n > 0 {
				in.proxy.ServeHTTP(w, r)
				return
			}
			// The first attempt runs on at the server, its ingestion held at the provider, while
			// the client loses the connection.
			q := detached(r)
			first.Add(1)
			go func() { defer first.Done(); in.proxy.ServeHTTP(httptest.NewRecorder(), q) }()
			drop(w, r)
		}), nil)
		e.bao.delay.Store(int64(5 * time.Second))
		before := f.actCount()
		r := ctl(e.set("amb-02", e.doc(t, "amb-02", "a"))...)
		e.bao.delay.Store(0)
		first.Wait()
		f.want(r, 0, "repeat 1 of 3 with the same key in 1s", "the first request with this key is still ingesting (ingestion ing_",
			"already done earlier; showing that result.", "Fragment amb-02 saved in draft "+e.draft)
		s.mu.Lock()
		n := len(s.keys)
		s.mu.Unlock()
		if n < 3 {
			t.Errorf("%d PUTs: want the first, at least one in-progress 409 and the replay", n)
		}
		s.one(t, n)
		if strings.Contains(r.err, "repeat 2 of 3") {
			t.Error("an in-progress 409 counted as a repeat")
		}
		if got := f.actCount(); got != before+1 {
			t.Errorf("%d acts recorded, want 1", got-before)
		}
	},
	"AMB-04": func(t *testing.T, f *fixture, in *injector, e *fragmentEnv, ctl func(...string) result) {
		var s sent
		in.set(s.sending(http.MethodPut, func(n int, w http.ResponseWriter, r *http.Request) {
			if n == 0 {
				in.commit(w, r)
				return
			}
			problem(w, http.StatusInternalServerError, "internal-error", "")
		}), nil)
		before := f.actCount()
		r := ctl(e.set("amb-04", e.doc(t, "amb-04", "a"))...)
		f.want(r, 3, "repeat 3 of 3", "outcome unknown: an attempt may have committed", "with fragment amb-04 at frv_")
		s.one(t, 4)
		in.set(s.sending(http.MethodPut, func(_ int, w http.ResponseWriter, r *http.Request) { in.proxy.ServeHTTP(w, r) }), nil)
		r = ctl(e.set("amb-04", e.doc(t, "amb-04", "b"), printedRepeat(t, r.err)...)...)
		f.want(r, 3, "outcome unknown: the key is recorded for another request; this send did nothing, and the original outcome is still unknown",
			"with fragment amb-04 at frv_")
		s.one(t, 5)
		if got := f.actCount(); r.out != "" || got != before+1 {
			t.Errorf("standard output %q, %d acts recorded; want none and 1", r.out, got-before)
		}
	},
	"AMB-06": func(t *testing.T, f *fixture, in *injector, e *fragmentEnv, ctl func(...string) result) {
		var s sent
		// The repeat waits until the retire returns: commit closes the connection first, so a slow
		// retire would otherwise let the repeat reach the provider while the old version still works.
		retired := make(chan struct{})
		in.set(s.sending(http.MethodPut, func(n int, w http.ResponseWriter, r *http.Request) {
			if n > 0 {
				<-retired
				in.proxy.ServeHTTP(w, r)
				return
			}
			defer close(retired)
			in.commit(w, r)
			// The version the record was computed under becomes one the provider cannot compute.
			e.bao.retire(t)
		}), nil)
		before := f.actCount()
		r := ctl(e.set("amb-06", e.doc(t, "amb-06", "a"))...)
		f.want(r, 3, "outcome unknown: the key is recorded for another request; this send did nothing, and the original outcome is still unknown",
			"with fragment amb-06 at frv_")
		s.one(t, 2)
		if got := f.actCount(); r.out != "" || got != before+1 {
			t.Errorf("standard output %q, %d acts recorded; want none and 1", r.out, got-before)
		}
	},
	"AMB-10": func(t *testing.T, f *fixture, in *injector, e *fragmentEnv, ctl func(...string) result) {
		var s sent
		in.set(s.sending(http.MethodPut, func(_ int, w http.ResponseWriter, r *http.Request) { drop(w, r) }), nil)
		before := f.actCount()
		r := ctl(e.set("amb-10", e.doc(t, "amb-10", "a"))...)
		f.want(r, 3, "outcome unknown: an attempt may have committed", "with no fragment amb-10 entry")
		s.one(t, 4)
		in.set(s.sending(http.MethodPut, func(_ int, w http.ResponseWriter, r *http.Request) { in.proxy.ServeHTTP(w, r) }), nil)
		r = ctl(e.set("amb-10", e.doc(t, "amb-10", "b"), printedRepeat(t, r.err)...)...)
		f.want(r, 0, "Fragment amb-10 saved in draft "+e.draft)
		if strings.Contains(r.err, "already done earlier") {
			t.Error("the changed input's own answer was reported as a replay")
		}
		s.one(t, 5)
		if got := f.actCount(); got != before+1 {
			t.Errorf("%d acts recorded, want the changed input's", got-before)
		}
	},
	"AMB-11": func(t *testing.T, f *fixture, in *injector, e *fragmentEnv, ctl func(...string) result) {
		var s sent
		in.set(s.sending(http.MethodPut, func(_ int, w http.ResponseWriter, r *http.Request) { drop(w, r) }), nil)
		r := ctl(e.set("amb-11", e.doc(t, "amb-11", "a"))...)
		f.want(r, 3, "outcome unknown")
		in.set(nil, nil)
		r = ctl(e.set("amb-11", e.doc(t, "amb-11", "a"), repeatFlags(t, r.err)...)...)
		f.want(r, 2, "a repeat needs --idempotency-key, --expect-epoch, --expect-principal and --if-match, each as the outcome-unknown block printed it; nothing was sent")
		if seen := in.requests(); len(seen) != 0 {
			t.Errorf("without --if-match the injector saw %v", seen)
		}
		s.one(t, 4)
	},
	"AMB-13": func(t *testing.T, f *fixture, in *injector, e *fragmentEnv, ctl func(...string) result) {
		var s sent
		in.set(s.sending(http.MethodPut, func(_ int, w http.ResponseWriter, r *http.Request) { drop(w, r) }), nil)
		r := ctl(e.set("amb-13", e.doc(t, "amb-13", "a"))...)
		f.want(r, 3, "outcome unknown")
		flags := printedRepeat(t, r.err)
		f.moveDraft(e.draft, "bwsynthambthirteenmovedaaa")
		before := f.actCount()
		in.set(s.sending(http.MethodPut, func(_ int, w http.ResponseWriter, r *http.Request) { in.proxy.ServeHTTP(w, r) }), nil)
		r = ctl(e.set("amb-13", e.doc(t, "amb-13", "a"), flags...)...)
		f.want(r, 3, "412", "precondition-failed", "this send was refused", "and did nothing; the original outcome is still unknown",
			"with no fragment amb-13 entry")
		// The operator gives a freshly read ETag in place of the printed one: no client check can
		// tell, and it executes as the client's own result.
		flags[len(flags)-1] = f.draftETag(e.draft)
		r = ctl(e.set("amb-13", e.doc(t, "amb-13", "a"), flags...)...)
		f.want(r, 0, "Fragment amb-13 saved in draft "+e.draft)
		s.one(t, 6)
		if got := f.actCount(); got != before+1 {
			t.Errorf("%d acts recorded, want the fresh ETag's write only", got-before)
		}
	},
}

// TestFragments runs fragment set through the CLI against a server with a provider: the cases of
// testdata/fragment-cases.yaml, in order, the leak scan and the journey tasks T4 and T5.
func TestFragments(t *testing.T) {
	f, bao := startProvider(t, "viewer")
	in := newInjector(t, f.base)
	in.writing = true
	b, err := os.ReadFile("testdata/fragment-cases.yaml")
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
	for id := range fragmentCases {
		if !slices.Contains(ids, id) {
			t.Errorf("case %s runs without a record", id)
		}
	}
	f.want(f.ctl(nil, "", "context", "add", "human", "--server", in.URL, "--certificate-authority", in.ca,
		"--issuer", f.iss.URL, "--client-id", "BWSYNTH-cli", "--audience", "bronzeward", "--oidc-token-file", f.humanFile()), 0, "credential oidc")
	s := f.seed()
	e := &fragmentEnv{draft: s.draft, dir: t.TempDir(), bao: bao}
	for _, id := range ids {
		run := fragmentCases[id]
		if run == nil {
			t.Errorf("record %s has no case", id)
			continue
		}
		t.Run(id, func(t *testing.T) {
			g := *f
			g.t = t
			ctl := func(args ...string) result {
				r := g.scan(g.exec(nil, "", args...), args)
				for _, where := range []string{strings.Join(args, " "), r.out, r.err} {
					if l := protectedLeaks(where); len(l) > 0 {
						t.Errorf("bronzewardctl %v: output or argv holds %v", args, l)
					}
				}
				return r
			}
			g.store(g.humanFile(), g.mint("h-all", ""), "")
			in.set(nil, nil)
			g.want(ctl("--context", "human", "draft", "show", s.draft), 0, s.draft)
			run(t, &g, in, e, ctl)
		})
	}
	t.Run("leak scan", func(t *testing.T) { f.fragmentLeakScan(t, in, e) })
	t.Run("journeys", func(t *testing.T) { f.journeys(t, in, s) })
	f.scanFiles()
}

// fragmentLeakScan runs fragment set with sentinels in the document and the marks, -v on, the
// document from a file and from standard input, and scans argv, standard output and error, the
// CLI's state and configuration files and its temporary directory (client-contracts.md §11.5,
// #347 acceptance item 2). The scan's positive control plants each sentinel first.
func (f *fixture) fragmentLeakScan(t *testing.T, in *injector, e *fragmentEnv) {
	g := *f
	g.t = t
	tmp := t.TempDir()
	plant := filepath.Join(tmp, "planted")
	for _, v := range sentinels {
		if err := os.WriteFile(plant, []byte("x "+v+" y"), 0o600); err != nil {
			t.Fatal(err)
		}
		if leaked := g.protectedFiles(tmp); !slices.Equal(leaked, []string{plant}) || !slices.Equal(protectedLeaks("x"+v), []string{v}) {
			t.Fatalf("the scan misses the planted %s: %v", v, leaked)
		}
	}
	os.Remove(plant)
	g.store(g.humanFile(), g.mint("h-all", ""), "")
	in.set(nil, nil)
	doc := "machine:\n  token: " + docSecret + "\n  nodeLabels:\n    " + markKey + ": " + markValue + "\n"
	file := filepath.Join(e.dir, "leak.yaml")
	if err := os.WriteFile(file, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"TMPDIR=" + tmp}
	runs := []struct {
		args  []string
		stdin string
	}{
		{e.set("leak-file", file, "--marks"), "doc[0]/machine/nodeLabels/" + markKey + "\n"},
		{e.set("leak-stdin", "-"), doc},
	}
	for _, run := range runs {
		g.want(g.exec(nil, "", "--context", "human", "draft", "show", e.draft), 0)
		args := append([]string{"-v"}, run.args...)
		r := g.scan(g.exec(env, run.stdin, args...), args)
		g.want(r, 0, "saved in draft "+e.draft)
		for name, s := range map[string]string{"argv": strings.Join(args, " "), "stdout": r.out, "stderr": r.err} {
			if l := protectedLeaks(s); len(l) > 0 {
				t.Errorf("%v: %s holds %v", args, name, l)
			}
		}
	}
	if entries, err := os.ReadDir(tmp); err != nil || len(entries) != 0 {
		t.Errorf("the CLI's temporary directory holds %v (%v)", entries, err)
	}
	files := 0
	for _, dir := range []string{filepath.Join(f.home, ".local", "state", "bronzeward"), filepath.Join(f.home, "bronzeward")} {
		if leaked := g.protectedFiles(dir); len(leaked) > 0 {
			t.Errorf("%v hold a sentinel", leaked)
		}
		entries, _ := os.ReadDir(dir)
		files += len(entries)
	}
	if files == 0 {
		t.Error("the scan read no file")
	}
}

// protectedFiles names the files under dir that hold a sentinel.
func (f *fixture) protectedFiles(dir string) []string {
	var leaked []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil || len(protectedLeaks(string(b))) > 0 {
			leaked = append(leaked, p)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		f.t.Fatal(err)
	}
	return leaked
}

// journeyIDs are the generated identifiers, times and ports a journey's kept output replaces. A
// time ends in Z or in a numeric offset, as an attempt's transport deadline does.
var journeyIDs = regexp.MustCompile(`\b(drf|frv|frg|prv|prf|asr|asg|ing|req|cl|mch|ep|act|op|rel|ibr|pln|apr|idn|obs|att)_[a-z2-7]{26}\b|\d{4}-\d\d-\d\dT[0-9:.]+(?:Z|[+-]\d\d:\d\d)|127\.0\.0\.1:\d+`)

// TestJourneyIDs checks that an observation ID, an attempt ID and a time with a numeric offset are
// replaced in a journey's kept output, and that a time ending in Z still is.
func TestJourneyIDs(t *testing.T) {
	const id = "abcdefghijklmnopqrstuvwxyz"
	for in, want := range map[string]string{
		"observation obs_" + id + ",":               "observation <id>,",
		"attempt att_" + id + ",":                   "attempt <id>,",
		"deadline 2026-11-07T08:41:00.714945+00:00": "deadline <t>",
		"deadline 2026-11-07T08:41:00Z":             "deadline <t>",
		"obs_short":                                 "obs_short",
	} {
		got := journeyIDs.ReplaceAllStringFunc(in, func(m string) string {
			if strings.Contains(m, ":") {
				return "<t>"
			}
			return "<id>"
		})
		if got != want {
			t.Errorf("%q became %q; want %q", in, got, want)
		}
	}
}

// generatedBase64 is a run of base64 of 40 characters or more, as a certificate or ID the test
// generates is written in a rendered configuration: standard, or URL-safe and padded, as the
// generated Talos cluster ID is. One alphabet holds both, so a URL-safe value whose first - or _
// comes after its 40th character is replaced whole, not up to that character.
var generatedBase64 = regexp.MustCompile(`[A-Za-z0-9+/_-]{40,}={0,2}`)

var (
	// pemBlock is a PEM block of a rendered configuration: the BEGIN line, the indentation of the
	// body, the body, and the END line. A certificate is wrapped in 64-character lines and its
	// last line is shorter, so the base64 mask alone leaves that line, which differs on every run.
	pemBlock = regexp.MustCompile(`(?s)(-----BEGIN [A-Z ]+-----\n)([ \t]*).*?(\n[ \t]*-----END [A-Z ]+-----)`)
	// pemTail is the last line of a PEM body, found by the END line after it, for a hunk of a diff
	// that shows no BEGIN line. A hunk marker + or - may start the line, or the END line, and is
	// kept. Base64 may start with + too, so the marker is the one followed by blanks.
	pemTail = regexp.MustCompile(`(?m)^([ \t]*(?:[+-][ \t]+)?)[A-Za-z0-9+/_-]+={0,2}(\n[ \t]*(?:[+-][ \t]+)?-----END [A-Z ]+-----)`)
	// docPathRun is a base64 run, or a path that follows a document index, such as doc[21]/a/b/c.
	docPathRun = regexp.MustCompile(`(doc\[[0-9]+\])?` + generatedBase64.String())
)

// maskGenerated replaces what a run generates and a kept journey must not hold: a PEM body whole,
// then each base64 run, except a path after a document index, which stays readable.
func maskGenerated(s string) string {
	s = pemBlock.ReplaceAllString(s, "${1}${2}<pem>${3}")
	s = pemTail.ReplaceAllString(s, "${1}<pem>${2}")
	return docPathRun.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasPrefix(m, "doc[") {
			return m
		}
		return "<base64>"
	})
}

// TestMaskGeneratedPEM checks that a certificate's body goes whole, its short last line included,
// and that a long path after a document index stays readable.
func TestMaskGeneratedPEM(t *testing.T) {
	long := strings.Repeat("A", 64)
	for name, c := range map[string]struct{ in, want string }{
		"tail line": {
			"cert: |\n    -----BEGIN CERTIFICATE-----\n    " + long + "\n    m1Rkt7R3CaUMuIn0EA==\n    -----END CERTIFICATE-----\nkey: x\n",
			"cert: |\n    -----BEGIN CERTIFICATE-----\n    <pem>\n    -----END CERTIFICATE-----\nkey: x\n",
		},
		"two blocks": {
			"-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----\n-----BEGIN CERTIFICATE-----\ndef\n-----END CERTIFICATE-----\n",
			"-----BEGIN CERTIFICATE-----\n<pem>\n-----END CERTIFICATE-----\n-----BEGIN CERTIFICATE-----\n<pem>\n-----END CERTIFICATE-----\n",
		},
		"path": {
			"doc[21]/config/resources/0/providers/0/secretbox/keys/0/secret\n",
			"doc[21]/config/resources/0/providers/0/secretbox/keys/0/secret\n",
		},
		"id": {"id: " + long + "\n", "id: <base64>\n"},
		// A review's diff shows a hunk of a certificate with no BEGIN line, so its last line has
		// to go by the END line that follows it. That line may start with + as base64 does.
		"hunk tail": {
			"    @@ -1 +1 @@\n         " + long + "\n         +D/JXaiva7wm5rRFa0k=\n         -----END CERTIFICATE-----\n  key: x\n",
			"    @@ -1 +1 @@\n         <base64>\n         <pem>\n         -----END CERTIFICATE-----\n  key: x\n",
		},
		"hunk tail with marker": {
			"    -    TEaJE2HdPcnITCgAYsAn7rOAOQ==\n    -    -----END CERTIFICATE-----\n",
			"    -    <pem>\n    -    -----END CERTIFICATE-----\n",
		},
	} {
		if got := maskGenerated(c.in); got != c.want {
			t.Errorf("%s: got %q; want %q", name, got, c.want)
		}
	}
}

// TestGeneratedBase64 checks that each generated form is replaced whole, a URL-safe one whose
// first - or _ comes late included, and that text shorter than 40 characters stays.
func TestGeneratedBase64(t *testing.T) {
	long := strings.Repeat("A", 40)
	for in, want := range map[string]string{
		"id: " + long + "+/8=\n": "id: <base64>\n",
		"id: " + long + "-_8=\n": "id: <base64>\n",
		"id: -_" + long + "\n":   "id: <base64>\n",
		"id: BWSYNTH-short-id\n": "id: BWSYNTH-short-id\n",
	} {
		if got := generatedBase64.ReplaceAllString(in, "<base64>"); got != want {
			t.Errorf("%q became %q; want %q", in, got, want)
		}
	}
}

// secretRefs are the provider references a run's import generates, shown in a review and its
// provenance.
var secretRefs = regexp.MustCompile(`\bs-[a-z2-7]{26}\b`)

// keepJourney keeps a scripted journey's output in testdata/journeys/<name>.txt, identifiers
// replaced by their prefix and dir's paths by their file name, or with -update rewrites it.
func keepJourney(t *testing.T, name, out, dir string) {
	t.Helper()
	// The steps are separated by a blank line; the file ends with the last step's exit line.
	kept := journeyIDs.ReplaceAllStringFunc(strings.TrimSuffix(out, "\n"), func(m string) string {
		if p, _, ok := strings.Cut(m, "_"); ok && !strings.Contains(m, ":") {
			return p + "_…"
		}
		if strings.HasPrefix(m, "127.") {
			return "127.0.0.1:<port>"
		}
		return "<time>"
	})
	// A generated certificate or ID differs on every run.
	// The run's directory goes first: its path is a run of base64 characters too.
	kept = strings.ReplaceAll(kept, dir+string(filepath.Separator), "")
	kept = maskGenerated(kept)
	kept = secretRefs.ReplaceAllString(kept, "s-…")
	path := filepath.Join("testdata", "journeys", name+".txt")
	if *update {
		if err := os.WriteFile(path, []byte(kept), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil || string(want) != kept {
		t.Errorf("%s: the run's output differs from %s (rerun with -update to keep it):\n%s", name, path, kept)
	}
}

// journeys runs the journey tasks T4 and T5 (operator-experience.md §13) as scripts through the
// CLI, and keeps their output in testdata/journeys, identifiers replaced by their prefix; -update
// rewrites it. T4 is done when the draft entry is saved; its probe "Has any machine changed?" is
// answered by machine show. T5 is done when the other author's change is read and the own change
// applied again.
func (f *fixture) journeys(t *testing.T, in *injector, s seeded) {
	g := *f
	g.t = t
	in.set(nil, nil)
	g.store(g.humanFile(), g.mint("h-all", ""), "")
	other := filepath.Join(f.home, "other.json")
	g.store(other, g.mint("h-author", ""), "")
	g.want(g.ctl(nil, "", "context", "add", "other", "--server", in.URL, "--certificate-authority", in.ca,
		"--issuer", f.iss.URL, "--client-id", "BWSYNTH-cli", "--audience", "bronzeward", "--oidc-token-file", other), 0)
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	labels := write("worker-labels.yaml", "machine:\n  nodeLabels:\n    node.example.test/pool: workers\n")
	labels2 := write("worker-labels-2.yaml", "machine:\n  nodeLabels:\n    node.example.test/pool: workers\n    node.example.test/tier: b\n")
	sysctls := write("worker-sysctls.yaml", "machine:\n  sysctls:\n    vm.max_map_count: \"262144\"\n")
	var draft string
	script := func(name string, steps [][]string) {
		var out bytes.Buffer
		for _, step := range steps {
			args := slices.Clone(step)
			for i, a := range args {
				args[i] = strings.NewReplacer("{draft}", draft, "{machine}", s.machine, "{cluster}", s.cluster).Replace(a)
			}
			r := g.scan(g.exec(nil, "", args...), args)
			if args[2] == "draft" && args[3] == "create" {
				draft = regexp.MustCompile(`drf_[a-z2-7]{26}`).FindString(r.out)
			}
			fmt.Fprintf(&out, "$ bronzewardctl %s\n%s%s[exit %d]\n\n", strings.Join(step, " "), r.out, r.err, r.code)
		}
		keepJourney(t, name, out.String(), dir)
	}
	h := []string{"--context", "human"}
	o := []string{"--context", "other"}
	script("t4", [][]string{
		append(h, "draft", "create", "--cluster", "{cluster}", "--title", "worker labels"),
		append(h, "fragment", "set", "worker-labels", "--draft", "{draft}", "--layer", "role", "--file", labels),
		append(h, "profile", "set", "workers", "--draft", "{draft}", "--fragments", "worker-labels,frv_aaaaaaaaaaaaaaaaaaaaaaaaaa"),
		append(h, "profile", "set", "workers", "--draft", "{draft}", "--fragments", "worker-labels"),
		append(h, "assignment", "set", "{machine}", "--draft", "{draft}", "--profiles", "workers"),
		append(h, "draft", "show", "{draft}"),
		append(h, "machine", "show", "{machine}"),
	})
	script("t5", [][]string{
		append(h, "draft", "show", "{draft}"),
		append(o, "draft", "show", "{draft}"),
		append(o, "fragment", "set", "worker-sysctls", "--draft", "{draft}", "--layer", "role", "--file", sysctls),
		append(h, "fragment", "set", "worker-labels", "--draft", "{draft}", "--layer", "role", "--file", labels2),
		append(h, "draft", "show", "{draft}"),
		append(h, "fragment", "set", "worker-labels", "--draft", "{draft}", "--layer", "role", "--file", labels2),
	})
}
