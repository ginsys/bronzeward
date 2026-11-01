package clitest

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// importTimers are short claim timers: a lease lapses 1.5 s after its last heartbeat, and the
// absolute expiry is far enough away for PROT-06's takeovers.
const importTimers = "{instance: a, heartbeat: 300ms, lease: 1500ms, absoluteExpiry: 3m, sweep: 1s}"

// ingID is an ingestion ID in the CLI's output.
var ingID = regexp.MustCompile(`\bing_[a-z2-7]{26}\b`)

// importEnv is one import test's server, its injector and provider proxy, the seeded cluster,
// machine and draft, and the directory its documents are written to.
type importEnv struct {
	t   *testing.T
	f   *fixture
	in  *injector
	bao *slowBao
	s   seeded
	dir string
	st  *standIn
	tmp string // TMPDIR of the commands run on a terminal, which must stay empty
}

// newImportEnv adds the human context through a new injector and seeds the server.
func newImportEnv(t *testing.T, f *fixture, bao *slowBao) *importEnv {
	t.Helper()
	in := newInjector(t, f.base)
	in.writing = true
	f.want(f.ctl(nil, "", "context", "add", "human", "--server", in.URL, "--certificate-authority", in.ca,
		"--issuer", f.iss.URL, "--client-id", "BWSYNTH-cli", "--audience", "bronzeward", "--oidc-token-file", f.humanFile()), 0, "credential oidc")
	f.store(f.humanFile(), f.mint("h-all", ""), "")
	return &importEnv{t: t, f: f, in: in, bao: bao, s: f.seed(), dir: t.TempDir()}
}

// sub is e reporting to subtest t, signed in afresh, the injector passing every request.
func (e *importEnv) sub(t *testing.T) *importEnv {
	g := *e.f
	g.t = t
	c := *e
	c.t, c.f = t, &g
	g.store(g.humanFile(), g.mint("h-all", ""), "")
	e.in.set(nil, nil)
	return &c
}

// ctl runs bronzewardctl and fails on a token, a sentinel or a secret of the stand-in in its argv,
// standard output or standard error.
func (e *importEnv) ctl(env []string, stdin string, args ...string) result {
	r := e.f.scan(e.f.exec(env, stdin, args...), args)
	for name, s := range map[string]string{"argv": strings.Join(args, " "), "stdout": r.out, "stderr": r.err} {
		if l := protectedLeaks(s); len(l) > 0 {
			e.t.Errorf("bronzewardctl %v: %s holds %v", args, name, l)
		}
		if e.st != nil {
			if l := e.st.leaks(s); len(l) > 0 {
				e.t.Errorf("bronzewardctl %v: %s holds the stand-in's %v", args, name, l)
			}
		}
	}
	return r
}

// run is ctl in the human context.
func (e *importEnv) run(args ...string) result {
	return e.ctl(nil, "", append([]string{"--context", "human"}, args...)...)
}

// start reads the draft, which keeps its ETag, then begins the import.
func (e *importEnv) start(name string, extra ...string) (result, string) {
	e.t.Helper()
	e.f.want(e.run("draft", "show", e.s.draft), 0, e.s.draft)
	return e.begin(name, extra...)
}

// begin imports into the draft the document named name, a sentinel its token, or with name ""
// the machine's configuration from its stand-in. It returns the result and the ingestion it names.
func (e *importEnv) begin(name string, extra ...string) (result, string) {
	e.t.Helper()
	args := []string{"import", "start", "--machine", e.s.machine, "--draft", e.s.draft}
	if name != "" {
		p := filepath.Join(e.dir, name+".yaml")
		if err := os.WriteFile(p, []byte("machine:\n  token: "+docSecret+"-"+name+"\n"), 0o600); err != nil {
			e.t.Fatal(err)
		}
		args = append(args, "--file", p)
	}
	r := e.run(append(args, extra...)...)
	ing := ingID.FindString(r.out)
	if ing == "" {
		e.t.Fatalf("import start named no ingestion:\nstdout: %s\nstderr: %s", r.out, r.err)
	}
	return r, ing
}

// claim is the ingestion's claim state and whether it still holds a staged envelope, and its
// operation's state, as the database shows them.
func (e *importEnv) claim(ing string) (state string, staged bool, op string) {
	e.t.Helper()
	if err := e.f.db.QueryRow(`SELECT c.state, c.payload IS NOT NULL, o.state FROM staging_claim c JOIN operation o ON o.ingestion = c.id
		WHERE c.id = $1`, ing).Scan(&state, &staged, &op); err != nil {
		e.t.Fatal(err)
	}
	return state, staged, op
}

// fragment saves a fragment named name into the draft with the kept ETag: another write.
func (e *importEnv) fragment(name string) result {
	e.t.Helper()
	p := filepath.Join(e.dir, "fragment-"+name+".yaml")
	if err := os.WriteFile(p, []byte("machine:\n  nodeLabels:\n    tier: "+name+"\n"), 0o600); err != nil {
		e.t.Fatal(err)
	}
	return e.run("fragment", "set", name, "--draft", e.s.draft, "--layer", "role", "--file", p)
}

// crash waits until the run holds its staging decrypt at the provider, the run's first step, so
// that it wrote no event, then kills serve and starts it again on the same database and port. The
// client's requests wait meanwhile, as they would for a server that is back. The waiting requests
// are let go however the restart ends, so that the injector's cleanup does not wait on them.
func (e *importEnv) crash(held int32) error {
	e.t.Helper()
	for deadline := time.Now().Add(30 * time.Second); e.bao.held.Load() <= held; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			return errors.New("the run never asked the provider to decrypt its envelope")
		}
	}
	up := make(chan struct{})
	e.in.set(func(http.ResponseWriter, *http.Request) bool { <-up; return false }, nil)
	defer func() {
		close(up)
		e.in.set(nil, nil)
	}()
	e.f.restart()
	return nil
}

// TestImports runs the import lifecycle through the CLI against a server with a provider and short
// claim timers: the cases of testdata/import-cases.yaml (AMB-08, ETAG-02, PROT-06's second and
// third), the #349 scenarios, and the leak scan of the ingestion document.
func TestImports(t *testing.T) {
	b, err := os.ReadFile("testdata/import-cases.yaml")
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
	if !slices.Equal(ids, []string{"AMB-08", "ETAG-02", "PROT-06"}) {
		t.Fatalf("import-cases.yaml holds %v; want the records AMB-08, ETAG-02 and PROT-06", ids)
	}
	t.Run("AMB-08", importUnavailable)
	t.Run("absolute expiry", importExpiry)

	f, bao := startTimers(t, "viewer", importTimers)
	e := newImportEnv(t, f, bao)
	e.st = f.newStandIn(t, bao.b, e.s, func(args ...string) result { return e.ctl(nil, "", args...) })

	t.Run("continuation, draft unmoved, ETAG-02", func(t *testing.T) {
		e := e.sub(t)
		r, ing := e.start("", "--review")
		e.f.want(r, 0, "Import "+ing+" started", "Staging: encrypted. Review: requested.", "Paused for review (generation 1, expires ",
			"bronzewardctl import continue "+ing)
		if state, staged, op := e.claim(ing); state != "paused" || !staged || op != "running" {
			t.Errorf("claim %s (staged %v), operation %s; want paused, staged, running", state, staged, op)
		}
		e.f.want(e.run("import", "status", "--state", "paused"), 0, ing+"  paused")
		r = e.run("import", "continue", ing)
		e.f.want(r, 0, "Continued import "+ing, "  continued (generation 2)", "  succeeded", "Import "+ing+" released into draft "+e.s.draft+" (revision ",
			"), import base ibr_")
		if state, staged, op := e.claim(ing); state != "released" || staged || op != "succeeded" {
			t.Errorf("claim %s (staged %v), operation %s; want released, cleared, succeeded", state, staged, op)
		}
		// ETAG-02, first case: the next write sends the import's revision, with no read between.
		e.f.want(e.fragment("etag-02"), 0, "Fragment etag-02 saved in draft "+e.s.draft)
	})

	t.Run("ETAG-02 overtaken", func(t *testing.T) {
		e := e.sub(t)
		e.f.want(e.run("draft", "show", e.s.draft), 0, e.s.draft)
		var moved atomic.Bool
		e.in.set(func(w http.ResponseWriter, r *http.Request) bool {
			// Another author writes between the import's success and the client's read.
			if r.Method == http.MethodGet && on("/drafts/"+e.s.draft)(r) && moved.CompareAndSwap(false, true) {
				if _, err := e.f.db.Exec(`UPDATE draft SET revision = revision + 1, etag_token = 'bwsynthetagtwoovertakenaaa' WHERE id = $1`, e.s.draft); err != nil {
					t.Errorf("moving the draft: %v", err)
				}
			}
			return false
		}, nil)
		r, ing := e.begin("etag-02")
		e.f.want(r, 1, "Import "+ing+" released into draft "+e.s.draft, "another write followed this import's revision", "Its ETag is not kept.")
		if !moved.Load() {
			t.Fatal("the client did not read the draft after the import")
		}
		e.in.set(nil, nil)
		// The kept ETag is still the one before the import: the next write is refused, not resent.
		e.f.want(e.fragment("etag-02-b"), 1, "The draft changed since you last read it")
	})

	t.Run("continuation, draft moved", func(t *testing.T) {
		e := e.sub(t)
		r, ing := e.start("moved", "--review")
		e.f.want(r, 0, "Paused for review (generation 1")
		e.f.want(e.fragment("moved"), 0, "saved in draft")
		r = e.run("import", "continue", ing)
		e.f.want(r, 4, "precondition-failed", "The draft changed after this import started, so the import was not applied and its claim is abandoned.",
			"bronzewardctl import start --machine '"+e.s.machine+"' --draft '"+e.s.draft+"'")
		if state, staged, op := e.claim(ing); state != "abandoned" || staged || op != "failed" {
			t.Errorf("claim %s (staged %v), operation %s; want abandoned, cleared, failed", state, staged, op)
		}
	})

	t.Run("PROT-06, lease lapse and takeover", func(t *testing.T) {
		e := e.sub(t)
		r, ing := e.start("prot-06", "--review")
		e.f.want(r, 0, "Paused for review (generation 1")
		hold := make(chan struct{})
		e.bao.hold.Store(&hold)
		released := false
		release := func() {
			if !released {
				released = true
				e.bao.hold.Store(nil)
				close(hold)
			}
		}
		defer release()
		// The second case kills serve during a continuation, the third during a takeover.
		for _, c := range []struct{ verb, state string }{{"continue", "held"}, {"takeover", "resumed"}} {
			held := e.bao.held.Load()
			done := make(chan result, 1)
			began := time.Now()
			go func() { done <- e.run("import", c.verb, ing) }()
			// The command's goroutine reports to t, so t waits for it before any failure ends t.
			err := e.crash(held)
			r := <-done
			if err != nil {
				t.Fatal(err)
			}
			e.f.want(r, 3, "import "+ing+": its claim is "+c.state+" and its lease lapsed", "still runs: the outcome is unknown",
				"bronzewardctl import status "+ing, "bronzewardctl import takeover "+ing, "bronzewardctl import abandon "+ing)
			if strings.Contains(r.err, "not applied") {
				t.Errorf("%s: the outcome was reported as not applied", c.verb)
			}
			if took := time.Since(began); took > time.Minute {
				t.Errorf("%s: the wait took %v; it must end once the lease lapsed, not at the absolute expiry", c.verb, took)
			}
			if state, staged, op := e.claim(ing); state != c.state || !staged || op != "running" {
				t.Errorf("%s: claim %s (staged %v), operation %s; want %s, staged, running", c.verb, state, staged, op, c.state)
			}
		}
		e.f.want(e.run("import", "status", ing), 0, "Its run stopped renewing the lease. Take it over to resume, or abandon it:")
		release()
		r = e.run("import", "takeover", ing)
		e.f.want(r, 0, "Took over import "+ing, "  taken-over (generation 4)", "Import "+ing+" released into draft "+e.s.draft)
		if state, staged, op := e.claim(ing); state != "released" || staged || op != "succeeded" {
			t.Errorf("claim %s (staged %v), operation %s; want released, cleared, succeeded", state, staged, op)
		}
	})

	t.Run("abandonment", func(t *testing.T) {
		e := e.sub(t)
		r, ing := e.start("abandon", "--review")
		e.f.want(r, 0, "Paused for review (generation 1")
		e.f.want(e.run("import", "abandon", ing), 0, "Import "+ing+" abandoned: its staged input is cleared, and nothing of it reaches draft "+e.s.draft)
		if state, staged, op := e.claim(ing); state != "abandoned" || staged || op != "failed" {
			t.Errorf("claim %s (staged %v), operation %s; want abandoned, cleared, failed", state, staged, op)
		}
		e.f.want(e.run("import", "status", ing), 0, "Ended: abandoned. Import again:")
		e.f.want(e.run("import", "continue", ing), 1, "the ingestion has ended")
	})

	t.Run("leak scan", func(t *testing.T) { e.sub(t).leakScan(t) })
	f.scanFiles()
}

// importUnavailable is AMB-08: import start against a server with no provider, so no ingestion.
func importUnavailable(t *testing.T) {
	f := start(t, "viewer")
	e := newImportEnv(t, f, nil)
	var s sent
	e.in.set(s.posting(func(_ int, w http.ResponseWriter, r *http.Request) { e.in.proxy.ServeHTTP(w, r) }), nil)
	e.f.want(e.run("draft", "show", e.s.draft), 0, e.s.draft)
	r := e.run("import", "start", "--machine", e.s.machine, "--draft", e.s.draft)
	e.f.want(r, 1, "dependency-unavailable", "repeat 3 of 3 with the same key", "Not done: none of the 4 attempts committed anything of its own.")
	s.one(t, 4)
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM operation WHERE kind = 'ingest'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d ingest operations (%v); want none", n, err)
	}
}

// importExpiry is a paused claim past its absolute expiry: the sweep abandons it, the client says
// so and offers a new start, and a continuation is refused.
func importExpiry(t *testing.T) {
	f, bao := startTimers(t, "viewer", "{instance: a, heartbeat: 300ms, lease: 1500ms, absoluteExpiry: 4s, sweep: 500ms}")
	e := newImportEnv(t, f, bao)
	r, ing := e.start("expiry", "--review")
	e.f.want(r, 0, "Paused for review (generation 1")
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		if state, staged, _ := e.claim(ing); state == "abandoned" && !staged {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("the sweep left the claim %s (staged %v)", state, staged)
		}
	}
	e.f.want(e.run("import", "status", ing), 0, "Ended: abandoned. Import again:",
		"bronzewardctl import start --machine '"+e.s.machine+"' --draft '"+e.s.draft+"'")
	e.f.want(e.run("import", "continue", ing), 1, "the ingestion has ended")
}

// leakScan imports documents holding sentinels, from a file with marks on standard input and from
// standard input, with -v, and scans argv, standard output and error, the CLI's state and
// configuration files and its temporary directory (client-contracts.md §11.5). The scan's positive
// control plants each sentinel first.
func (e *importEnv) leakScan(t *testing.T) {
	tmp := t.TempDir()
	plant := filepath.Join(tmp, "planted")
	for _, v := range sentinels {
		if err := os.WriteFile(plant, []byte("x "+v+" y"), 0o600); err != nil {
			t.Fatal(err)
		}
		if leaked := e.f.protectedFiles(tmp); !slices.Equal(leaked, []string{plant}) || !slices.Equal(protectedLeaks("x"+v), []string{v}) {
			t.Fatalf("the scan misses the planted %s: %v", v, leaked)
		}
	}
	os.Remove(plant)
	doc := "machine:\n  token: " + docSecret + "\n  nodeLabels:\n    " + markKey + ": " + markValue + "\n"
	file := filepath.Join(e.dir, "leak.yaml")
	if err := os.WriteFile(file, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, run := range []struct {
		args  []string
		stdin string
	}{
		{[]string{"--file", file, "--marks"}, "doc[0]/machine/nodeLabels/" + markKey + "\n"},
		{[]string{"--file", "-"}, doc},
	} {
		e.f.want(e.run("draft", "show", e.s.draft), 0)
		args := append([]string{"-v", "--context", "human", "import", "start", "--machine", e.s.machine, "--draft", e.s.draft}, run.args...)
		e.f.want(e.ctl([]string{"TMPDIR=" + tmp}, run.stdin, args...), 0, "released into draft "+e.s.draft)
	}
	if entries, err := os.ReadDir(tmp); err != nil || len(entries) != 0 {
		t.Errorf("the CLI's temporary directory holds %v (%v)", entries, err)
	}
	files := 0
	for _, dir := range []string{filepath.Join(e.f.home, ".local", "state", "bronzeward"), filepath.Join(e.f.home, "bronzeward")} {
		if leaked := e.f.protectedFiles(dir); len(leaked) > 0 {
			t.Errorf("%v hold a sentinel", leaked)
		}
		entries, _ := os.ReadDir(dir)
		files += len(entries)
	}
	if files == 0 {
		t.Error("the scan read no file")
	}
}
