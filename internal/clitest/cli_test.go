// Package clitest runs bronzewardctl as a process against a disposable bronzeward serve on a
// dbtest database (ginsys/bronzeward#254, verification method 1). It sits outside the client's
// closure patterns (internal/client/closure_test.go) because it links the server's test database
// helper. Faults come from an in-process TLS proxy between the two.
package clitest

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/dbtest"
)

var (
	buildOnce sync.Once
	binDir    string
	buildErr  error
)

// binaries builds bronzeward and bronzewardctl once per test binary.
func binaries(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		if binDir, buildErr = os.MkdirTemp("", "bw-clitest-"); buildErr != nil {
			return
		}
		out, err := exec.Command("go", "build", "-o", binDir+string(filepath.Separator),
			"github.com/ginsys/bronzeward/cmd/bronzeward", "github.com/ginsys/bronzeward/cmd/bronzewardctl").CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return binDir
}

func TestMain(m *testing.M) {
	code := m.Run()
	if binDir != "" {
		os.RemoveAll(binDir)
	}
	os.Exit(code)
}

// fixture is one disposable server with an automation token holding roles, and the CLI's own
// configuration directory.
type fixture struct {
	t                                 *testing.T
	bin, base, home, token, tokenFile string
}

var tokenPattern = regexp.MustCompile(`bwt_\S+`)

func start(t *testing.T, roles string) *fixture {
	t.Helper()
	_, dsn := dbtest.New(t)
	f := &fixture{t: t, bin: binaries(t), home: t.TempDir()}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	f.base = "http://" + addr
	cfg := filepath.Join(t.TempDir(), "bronzeward.yaml")
	body := "listen: " + addr + "\ndatabase:\n  dsn: " + dsn + "\nauth:\n  oidc:\n    issuer: https://idp.test\n    audience: bronzeward\n" +
		"execution: {maxTransportDeadline: 5m}\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	f.server("migrate", "-config", cfg)
	f.token = tokenPattern.FindString(f.server("token", "issue", "-config", cfg, "-name", "ci", "-roles", roles,
		"-responsible", "h-all", "-operator", "h-operator"))
	if f.token == "" {
		t.Fatal("token issue printed no token")
	}
	f.tokenFile = filepath.Join(f.home, "token")
	if err := os.WriteFile(f.tokenFile, []byte(f.token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	serve := exec.Command(filepath.Join(f.bin, "bronzeward"), "serve", "-config", cfg)
	serve.Stdout, serve.Stderr = &log, &log
	if err := serve.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		serve.Process.Kill()
		serve.Wait()
		if t.Failed() {
			t.Logf("serve log:\n%s", log.String())
		}
	})
	for i := 0; ; i++ {
		if r, err := http.Get(f.base + "/livez"); err == nil && r.StatusCode == http.StatusNoContent {
			r.Body.Close()
			return f
		} else if i == 100 {
			t.Fatalf("serve did not answer /livez: %v\n%s", err, log.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// server runs a bronzeward subcommand and returns its standard output.
func (f *fixture) server(args ...string) string {
	f.t.Helper()
	var out, errb bytes.Buffer
	cmd := exec.Command(filepath.Join(f.bin, "bronzeward"), args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		f.t.Fatalf("bronzeward %s: %v\n%s", args[0], err, errb.String())
	}
	return out.String()
}

type result struct {
	out, err string
	code     int
}

// ctl runs bronzewardctl with a clean environment plus env, and scans its argv, standard output
// and standard error for the fixture's token and any token given on standard input (§11.5
// item 1).
func (f *fixture) ctl(env []string, stdin string, args ...string) result {
	f.t.Helper()
	r := f.exec(env, stdin, args...)
	for _, where := range r.leaked(f.token, stdin, args) {
		f.t.Errorf("bronzewardctl %v: %s holds a token", args, where)
	}
	return r
}

func (f *fixture) exec(env []string, stdin string, args ...string) result {
	f.t.Helper()
	cmd := f.command(env, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	r := result{}
	var exit *exec.ExitError
	if err := cmd.Run(); errors.As(err, &exit) {
		r.code = exit.ExitCode()
	} else if err != nil {
		// Errorf, not Fatal: commands also run from goroutines.
		f.t.Errorf("bronzewardctl %v: %v", args, err)
		r.code = -1
	}
	r.out, r.err = out.String(), errb.String()
	return r
}

// command is bronzewardctl with a clean environment plus env and the fixture's home.
func (f *fixture) command(env []string, args ...string) *exec.Cmd {
	cmd := exec.Command(filepath.Join(f.bin, "bronzewardctl"), args...)
	cmd.Env = append([]string{"HOME=" + f.home, "XDG_CONFIG_HOME=" + f.home, "PATH=" + os.Getenv("PATH")}, env...)
	return cmd
}

// leaked names the captures (argv, stdout, stderr) that hold token or a token in stdin.
func (r result) leaked(token, stdin string, args []string) []string {
	var where []string
	for _, w := range []struct{ name, s string }{{"argv", strings.Join(args, " ")}, {"stdout", r.out}, {"stderr", r.err}} {
		for _, tok := range append(tokenPattern.FindAllString(stdin, -1), token) {
			if leaks(w.s, tok) {
				where = append(where, w.name)
				break
			}
		}
	}
	return where
}

// want fails unless r exited with code and its output holds every part.
func (f *fixture) want(r result, code int, parts ...string) {
	f.t.Helper()
	if r.code != code {
		f.t.Errorf("exit %d, want %d\nstdout: %s\nstderr: %s", r.code, code, r.out, r.err)
	}
	for _, p := range parts {
		if !strings.Contains(r.out+r.err, p) {
			f.t.Errorf("output lacks %q\nstdout: %s\nstderr: %s", p, r.out, r.err)
		}
	}
}

// leaks reports whether s holds token or its secret part.
func leaks(s, token string) bool {
	_, secret, _ := strings.Cut(token, ".")
	return strings.Contains(s, token) || secret != "" && strings.Contains(s, secret)
}

// TestLeakScanFindsTheToken is the scan's positive control.
func TestLeakScanFindsTheToken(t *testing.T) {
	tok := "bwt_tok_abc.BWSYNTH-secret"
	if !leaks("x "+tok+" y", tok) || !leaks("BWSYNTH-secret", tok) || leaks("bwt_tok_abc only", tok) {
		t.Fatal("the leak scan misses the token or its secret, or flags the token ID alone")
	}
}

// acts is the act count and the newest act, compared around read commands (verification
// method 2).
func (f *fixture) acts() string {
	f.t.Helper()
	q, _ := http.NewRequest(http.MethodGet, f.base+"/api/v1/acts?limit=500", nil)
	q.Header.Set("Authorization", "Bearer "+f.token)
	b := f.do(q, http.StatusOK)
	var page struct {
		Items []json.RawMessage `json:"items"`
		Next  string            `json:"next"`
	}
	if err := json.Unmarshal(b, &page); err != nil || page.Next != "" {
		f.t.Fatalf("acts: %v, next %q", err, page.Next)
	}
	if len(page.Items) == 0 {
		return "0"
	}
	return fmt.Sprintf("%d %s", len(page.Items), page.Items[len(page.Items)-1])
}

func (f *fixture) do(q *http.Request, status int) []byte {
	f.t.Helper()
	r, err := http.DefaultClient.Do(q)
	if err != nil {
		f.t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != status {
		f.t.Fatalf("%s %s: %s, want %d: %s", q.Method, q.URL.Path, r.Status, status, b)
	}
	return b
}

// injector is a TLS proxy to the server. hook may answer a request itself; edit may change a
// proxied answer. seen lists the requests it received since the last set; writes lists every
// request it ever received that was not a GET.
type injector struct {
	*httptest.Server
	mu           sync.Mutex
	hook         func(http.ResponseWriter, *http.Request) bool
	edit         func(*http.Response)
	seen, writes []string
	ca           string
}

func newInjector(t *testing.T, target string) *injector {
	u, _ := url.Parse(target)
	in := &injector{}
	rp := httputil.NewSingleHostReverseProxy(u)
	rp.ModifyResponse = func(r *http.Response) error {
		in.mu.Lock()
		edit := in.edit
		in.mu.Unlock()
		if edit != nil {
			edit(r)
		}
		return nil
	}
	in.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		in.mu.Lock()
		in.seen = append(in.seen, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodGet {
			in.writes = append(in.writes, r.Method+" "+r.URL.Path)
		}
		hook := in.hook
		in.mu.Unlock()
		if hook == nil || !hook(w, r) {
			rp.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(func() {
		in.Close()
		if len(in.writes) > 0 {
			t.Errorf("read commands sent %v", in.writes)
		}
	})
	in.ca = filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(in.ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: in.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	return in
}

func (in *injector) set(hook func(http.ResponseWriter, *http.Request) bool, edit func(*http.Response)) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.hook, in.edit, in.seen = hook, edit, nil
}

func (in *injector) requests() []string {
	in.mu.Lock()
	defer in.mu.Unlock()
	return slices.Clone(in.seen)
}

// rewrite replaces old with new in a proxied answer's body.
func rewrite(r *http.Response, old, new string) {
	b, _ := io.ReadAll(r.Body)
	b = bytes.ReplaceAll(b, []byte(old), []byte(new))
	r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(b)), int64(len(b))
	r.Header.Set("Content-Length", fmt.Sprint(len(b)))
}

func problem(w http.ResponseWriter, status int, code string, extra string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"type":"urn:bronzeward:problem:%s","title":"Injected refusal","status":%d,"instance":"req_injected"%s}`, code, status, extra)
}

// refuse answers the read of path with a problem.
func refuse(path string, status int, code, extra string) func(http.ResponseWriter, *http.Request) bool {
	return func(w http.ResponseWriter, r *http.Request) bool {
		if !on(path)(r) {
			return false
		}
		problem(w, status, code, extra)
		return true
	}
}

func on(path string) func(*http.Request) bool {
	return func(r *http.Request) bool { return r.URL.Path == "/api/v1"+path }
}

// sharedCases are the client-contracts.md §12 cases this changeset runs; testdata/cases.yaml
// holds their records, and TestSharedCases runs exactly the cases recorded there.
var sharedCases = map[string]func(*testing.T, *fixture, *injector){
	"TLS-01": func(t *testing.T, f *fixture, in *injector) {
		in.set(nil, nil)
		f.want(f.ctl(nil, "", "context", "add", "untrusted", "--server", in.URL), 1,
			"untrusted ("+in.URL+")", "is not trusted by the system trust store", "subject", "issuer", "nothing was sent")
		if seen := in.requests(); len(seen) != 0 {
			t.Errorf("the injector received %v", seen)
		}
		f.want(f.ctl(nil, "", "context", "add", "trusted", "--server", in.URL, "--certificate-authority", in.ca,
			"--automation-token-file", f.tokenFile), 0, "the bundle "+in.ca)
	},
	"TLS-02": func(t *testing.T, f *fixture, in *injector) {
		var hits atomic.Int32
		target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
		defer target.Close()
		in.set(func(w http.ResponseWriter, r *http.Request) bool {
			if !on("/identity")(r) {
				return false
			}
			http.Redirect(w, r, target.URL+"/api/v1/identity", http.StatusTemporaryRedirect)
			return true
		}, nil)
		f.want(f.ctl(nil, "", "--context", "trusted", "whoami"), 1, "307", target.URL+"/api/v1/identity", "follows no redirect")
		if hits.Load() != 0 || strings.Count(strings.Join(in.requests(), "\n"), "/api/v1/identity") != 1 {
			t.Errorf("target hits %d, injector saw %v: want none, and the identity read once", hits.Load(), in.requests())
		}
	},
	"COMP-01": func(t *testing.T, f *fixture, in *injector) {
		in.set(nil, func(r *http.Response) {
			if on("/server")(r.Request) {
				rewrite(r, `"apiRevision":2`, `"apiRevision":1`)
			}
		})
		f.want(f.ctl(nil, "", "--context", "trusted", "whoami"), 1, "API revision 1", "needs revision 2 or later")
		if slices.Contains(in.requests(), "GET /api/v1/identity") {
			t.Error("the identity read was sent after the refusal")
		}
	},
	"COMP-02": func(t *testing.T, f *fixture, in *injector) {
		in.set(nil, func(r *http.Response) { r.Header.Set("Deprecation", "@1798761600") })
		r := f.ctl(nil, "", "--context", "trusted", "whoami")
		f.want(r, 0, "GET /api/v1/identity is deprecated (Deprecation: @1798761600)")
		if n := strings.Count(r.err, "/identity is deprecated"); n != 1 {
			t.Errorf("the identity deprecation was shown %d times", n)
		}
	},
}

func TestSharedCases(t *testing.T) {
	f := start(t, "viewer")
	in := newInjector(t, f.base)
	b, err := os.ReadFile("testdata/cases.yaml")
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
	for id := range sharedCases {
		if !slices.Contains(ids, id) {
			t.Errorf("case %s runs without a record", id)
		}
	}
	f.ctl(nil, "", "context", "add", "lab", "--server", f.base, "--automation-token-file", f.tokenFile)
	before := f.acts()
	for _, id := range ids {
		run := sharedCases[id]
		if run == nil {
			t.Errorf("record %s has no case", id)
			continue
		}
		t.Run(id, func(t *testing.T) {
			g := *f
			g.t = t
			run(t, &g, in)
		})
	}
	if after := f.acts(); after != before {
		t.Errorf("acts changed around the read commands:\nbefore %s\nafter  %s", before, after)
	}
}
