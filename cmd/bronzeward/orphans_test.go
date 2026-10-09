package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/ginsys/bronzeward/internal/baotest"
	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/migrate"
	"github.com/ginsys/bronzeward/internal/orphans"
	"github.com/ginsys/bronzeward/internal/provider"
)

// The command checks of persistence-api.md §16 for the orphan report, against a real database and
// the shared test OpenBao behind a proxy that counts requests and can answer as a sealed provider.

// baoProxy forwards to the test OpenBao. It keeps each request's method and URI and token in
// memory only, and once seal reports true for an answered request, answers every later one 503,
// as a sealed OpenBao does.
type baoProxy struct {
	url    string
	mu     sync.Mutex
	reqs   []string
	tokens []string
	seal   func(uri string) bool
	sealed bool
}

func newBaoProxy(t *testing.T, target string) *baoProxy {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	p := &baoProxy{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.reqs = append(p.reqs, r.Method+" "+r.RequestURI)
		p.tokens = append(p.tokens, r.Header.Get("X-Vault-Token"))
		sealed := p.sealed
		p.mu.Unlock()
		if sealed {
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"errors":["Vault is sealed"]}`)
			return
		}
		rp.ServeHTTP(w, r)
		p.mu.Lock()
		if p.seal != nil && p.seal(r.RequestURI) {
			p.sealed = true
		}
		p.mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	p.url = srv.URL
	return p
}

func (p *baoProxy) requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.reqs)
}

func (p *baoProxy) sentTokens() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.tokens)
}

// orphanEnv is one database with cluster a recorded and a machine, the test OpenBao behind a proxy,
// and a configuration whose report token file holds the orphan-report identity's token.
type orphanEnv struct {
	db                *sql.DB
	b                 *baotest.Bao
	p                 *baoProxy
	config, tokenFile string
	a, machine, user  string
}

func newOrphanEnv(t *testing.T) *orphanEnv {
	t.Helper()
	db, dsn := dbtest.New(t)
	ms, err := migrate.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Apply(t.Context(), db, ms); err != nil {
		t.Fatal(err)
	}
	if _, _, err := migrate.Install(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	b := baotest.New(t)
	e := &orphanEnv{db: db, b: b, p: newBaoProxy(t, b.Addr), a: id.New(id.Cluster), machine: id.New(id.Machine),
		user: id.New(id.Principal)}
	dir := t.TempDir()
	e.tokenFile = filepath.Join(dir, "openbao-report.token")
	writeFile(t, e.tokenFile, b.Token("bw-orphan-report"), 0o600)
	e.config = filepath.Join(dir, "bronzeward.yaml")
	writeFile(t, e.config, "listen: 127.0.0.1:0\ndatabase:\n  dsn: "+dsn+"\nauth:\n  oidc:\n    issuer: https://idp.test\n    audience: bronzeward\n"+
		"execution: {maxTransportDeadline: 5m}\nprovider:\n  address: "+e.p.url+"\n"+
		"  keys: {baseline: bw-baseline, staging: bw-staging, digest: bw-digest, artifact: bw-artifact}\n"+
		"  ingestionTokenFile: "+filepath.Join(dir, "openbao-ingestion.token")+"\n  reportTokenFile: "+e.tokenFile+"\n"+
		"  compilerTokenFile: "+filepath.Join(dir, "openbao-compiler.token")+"\n"+
		"  metadataTokenFile: "+filepath.Join(dir, "openbao-metadata.token")+"\n"+
		"  executorTokenFile: "+filepath.Join(dir, "openbao-executor.token")+"\n"+
		"ingestion: {instance: a, heartbeat: 5s, lease: 15s, absoluteExpiry: 10m, sweep: 15s}\n", 0o600)
	mustDB(t, db, `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', 'https://idp.test', 'alice', now())`, e.user)
	mustDB(t, db, `INSERT INTO cluster (id, name, endpoint, contract, talos_cluster_id, created_at)
		VALUES ($1, 'office', 'https://cp.example.test:6443', 'v1.13', '8TMwqXnWOTdw7xFDHSn-f6JMbBQrSWAuyzCfGIRVSL0=', now())`, e.a)
	mustDB(t, db, `INSERT INTO machine (id, cluster, smbios_uuid, serial, scope_state, talos_endpoint, platform, created_at)
		VALUES ($1, $2, '0b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b', 'SN-1', 'normal', '10.55.0.3:50000', 'metal', now())`, e.machine, e.a)
	t.Cleanup(func() { orphanHooks = orphanTestHooks{} })
	return e
}

func writeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func mustDB(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// claim records a claim in cluster a.
func (e *orphanEnv) claim(t *testing.T, mode, state, lease, expiry string) string {
	t.Helper()
	c := id.New(id.Ingestion)
	mustDB(t, e.db, `INSERT INTO staging_claim (id, mode, state, owner, owner_gen, owner_epoch, lease_until, expires_at,
		cluster, machine, kind, created_at)
		SELECT $1, $2, $3, 'a/4242/start-1', 1, epoch, now() + $4::interval, now() + $5::interval, $6, $7, 'import', now()
		FROM installation_state`, c, mode, state, lease, expiry, e.a, e.machine)
	return c
}

// gen creates a generation under cluster and claim as the administrator, deleted when the test ends.
func (e *orphanEnv) gen(t *testing.T, cluster, claim string) string {
	t.Helper()
	p := "gen/" + cluster + "/" + claim + "/" + provider.NewValueID()
	if s, body, err := e.b.Do(e.b.Admin(), http.MethodPost, "/v1/secret/data/"+p,
		map[string]any{"data": map[string]string{"kind": "string", "value": "synthetic"}}); err != nil || s != http.StatusOK {
		t.Fatalf("seeding a generation: status %d, %v: %s", s, err, body)
	}
	t.Cleanup(func() {
		if s, _, err := e.b.Do(e.b.Admin(), http.MethodDelete, "/v1/secret/metadata/"+p, nil); err != nil || s/100 != 2 {
			t.Errorf("deleting a seeded generation: status %d, %v", s, err)
		}
	})
	return p
}

// reference names path from a new import base revision of the machine, written with it in one
// transaction, as ingestion's draft transaction writes a revision and its reference rows (PA §3).
func (e *orphanEnv) reference(t *testing.T, path string) {
	t.Helper()
	tx, err := e.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	base := id.New(id.ImportBase)
	for _, s := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO import_base_revision (id, machine, document, embedded, baseline_ciphertext, baseline_digest,
		baseline_digest_key, configuration_digest, created_at, author) VALUES ($1, $2, 'machine: {}', '[]', '\x01', $3, 'transit/baseline-digest:1', $3, now(),
		(SELECT min(id) FROM principal))`, []any{base, e.machine, bytes.Repeat([]byte{1}, 32)}},
		{`INSERT INTO import_base_reference (revision, name, kind, version, encoding, generation)
		VALUES ($1, $2, 'string', 1, NULL, $3)`, []any{base, "n" + strings.ToLower(provider.NewValueID()), path}},
	} {
		if _, err := tx.Exec(s.q, s.args...); err != nil {
			t.Fatalf("%s: %v", s.q, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// run runs the command and returns what it printed on its stdout, and everything else it wrote:
// its stderr, the process's own stdout and stderr, and the log. The outputs are restored however
// the command ends, a control's t.Fatal included.
func (e *orphanEnv) run(args ...string) (printed, other string, err error) {
	var out, errb, logb bytes.Buffer
	r, w, err := os.Pipe()
	if err != nil {
		return "", "", err
	}
	read := make(chan []byte)
	go func() {
		b, _ := io.ReadAll(r)
		read <- b
	}()
	stdout, stderr, logw := os.Stdout, os.Stderr, log.Writer()
	os.Stdout, os.Stderr = w, w
	log.SetOutput(&logb)
	defer func() {
		os.Stdout, os.Stderr = stdout, stderr
		log.SetOutput(logw)
		w.Close()
		errb.Write(<-read)
		r.Close()
		errb.Write(logb.Bytes())
		printed, other = out.String(), errb.String()
	}()
	err = runOrphans(append([]string{"-config", e.config}, args...), &out, &errb)
	return
}

// TestOrphansRunRestoresOutputs: run restores the process's stdout, stderr and log output even when
// the command never returns, as when a control's t.Fatal ends the goroutine.
func TestOrphansRunRestoresOutputs(t *testing.T) {
	e := newOrphanEnv(t)
	stdout, stderr, logw := os.Stdout, os.Stderr, log.Writer()
	orphanHooks.afterLoad = runtime.Goexit
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.run("-cluster", e.a)
	}()
	<-done
	if os.Stdout != stdout || os.Stderr != stderr || log.Writer() != logw {
		os.Stdout, os.Stderr = stdout, stderr
		log.SetOutput(logw)
		t.Fatal("an exit inside the command left the outputs redirected")
	}
}

// leaks reports whether text names a generation path or an identifier.
func leaks(text string) bool {
	return strings.Contains(text, "gen/") || strings.Contains(text, "cl_") || strings.Contains(text, "ing_")
}

// printing is the control that prints each generation path as it is listed.
type printing struct {
	orphans.Lister
	w io.Writer
}

func (p printing) Values(ctx context.Context, cluster, claim string) (provider.Generations, error) {
	g, err := p.Lister.Values(ctx, cluster, claim)
	for _, x := range g.Paths {
		fmt.Fprintln(p.w, x)
	}
	return g, err
}

// TestOrphansSelects: walking every cluster directory, the command reports the unreferenced
// generations of an abandoned and a released claim, of a claim without a row and of a cluster a
// restore removed, and not a referenced generation or a held, resumed or paused claim's.
func TestOrphansSelects(t *testing.T) {
	e := newOrphanEnv(t)
	ab := e.claim(t, "encrypted", "abandoned", "-1 hour", "-1 minute")
	abandoned, referenced := e.gen(t, e.a, ab), e.gen(t, e.a, ab)
	e.reference(t, referenced)
	released := e.gen(t, e.a, e.claim(t, "transient", "released", "1 minute", "1 hour"))
	absent := e.gen(t, e.a, id.New(id.Ingestion))
	held := e.gen(t, e.a, e.claim(t, "encrypted", "held", "1 minute", "1 hour"))
	resumed := e.gen(t, e.a, e.claim(t, "encrypted", "resumed", "1 minute", "1 hour"))
	// A paused claim is live with its lease ended (compilation §3.6).
	pc := e.claim(t, "encrypted", "held", "1 minute", "1 hour")
	mustDB(t, e.db, `UPDATE staging_claim SET state = 'paused', review = 'pending', lease_until = now() - interval '1 minute',
		payload = '\x01', payload_digest = decode(repeat('00', 32), 'hex') WHERE id = $1`, pc)
	paused := e.gen(t, e.a, pc)
	removed := e.gen(t, id.New(id.Cluster), id.New(id.Ingestion))
	out, _, err := e.run()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{abandoned, released, absent, removed} {
		if !strings.Contains(out, "orphan\t"+p+"\t") {
			t.Fatalf("not reported: %s", p)
		}
	}
	for _, p := range []string{referenced, held, resumed, paused} {
		if strings.Contains(out, p) {
			t.Fatalf("reported: %s", p)
		}
	}
	t.Logf("reported abandoned, released, absent-row and restore-removed generations; not referenced, held, resumed or paused")
}

// TestOrphansClusterScope: --cluster lists and reports only that cluster's subtree, and never
// lists gen/; control: a run that always walks every cluster reports the other.
func TestOrphansClusterScope(t *testing.T) {
	e := newOrphanEnv(t)
	other := id.New(id.Cluster)
	mine, theirs := e.gen(t, e.a, id.New(id.Ingestion)), e.gen(t, other, id.New(id.Ingestion))
	out, _, err := e.run("-cluster", e.a)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, mine) || strings.Contains(out, theirs) {
		t.Fatalf("scoped output:\n%s", out)
	}
	for _, r := range e.p.requests() {
		if !strings.HasPrefix(r, "GET /v1/secret/metadata/gen/"+e.a+"/") {
			t.Fatalf("a scoped run requested %s", r)
		}
	}
	orphanHooks.cluster = func(string) string { return "" }
	out, _, err = e.run("-cluster", e.a)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, theirs) {
		t.Fatal("control: walking every cluster did not report the other cluster")
	}
	t.Logf("scoped: only %s's subtree requested and reported; control (walk every cluster): the other reported", "cluster a")
}

// TestOrphansTokenFileRefused: a token file with a group or other permission bit, a FIFO, or a
// symlink is refused, for that reason, before any provider request. Baseline: the same token in a
// regular 0600 file reaches the provider.
func TestOrphansTokenFileRefused(t *testing.T) {
	e := newOrphanEnv(t)
	good := e.b.Token("bw-orphan-report")
	e.gen(t, e.a, id.New(id.Ingestion))
	if _, _, err := e.run("-cluster", e.a); err != nil || len(e.p.requests()) == 0 {
		t.Fatalf("baseline: err %v, %d provider requests", err, len(e.p.requests()))
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.token")
	writeFile(t, target, good, 0o600)
	for name, c := range map[string]struct {
		prepare func()
		reason  string
	}{
		"group-readable": {func() { writeFile(t, e.tokenFile, good, 0o640) }, "has mode 640"},
		"FIFO": {func() {
			if err := syscall.Mkfifo(e.tokenFile, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "is not a regular file"},
		"symlink": {func() {
			if err := os.Symlink(target, e.tokenFile); err != nil {
				t.Fatal(err)
			}
		}, syscall.ELOOP.Error()},
	} {
		if err := os.Remove(e.tokenFile); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		c.prepare()
		before := len(e.p.requests())
		out, _, err := e.run("-cluster", e.a)
		if err == nil || out != "" || !strings.HasPrefix(err.Error(), "reading the report token: ") ||
			!strings.Contains(err.Error(), c.reason) {
			t.Fatalf("%s: err %v, printed %q", name, err, out)
		}
		if n := len(e.p.requests()) - before; n != 0 {
			t.Fatalf("%s: %d provider requests before the refusal", name, n)
		}
		t.Logf("%s token file refused before any provider request: %v", name, err)
	}
}

// TestOrphansStartupFailuresNameTheStep: a configuration that does not load and a database that
// does not answer each exit nonzero naming the step, before any provider request, printing nothing.
func TestOrphansStartupFailuresNameTheStep(t *testing.T) {
	e := newOrphanEnv(t)
	body, err := os.ReadFile(e.config)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	unreachable := filepath.Join(dir, "unreachable.yaml")
	dsn := regexp.MustCompile(`dsn: .*`).ReplaceAllString(string(body), "dsn: postgres://bw@127.0.0.1:1/bw?connect_timeout=2")
	writeFile(t, unreachable, dsn, 0o600)
	for config, step := range map[string]string{
		filepath.Join(dir, "absent.yaml"): "reading the configuration: ",
		unreachable:                       "opening the database: ",
	} {
		e.config = config
		out, _, err := e.run("-cluster", e.a)
		if err == nil || out != "" || !strings.HasPrefix(err.Error(), step) || len(e.p.requests()) != 0 {
			t.Fatalf("%s: err %v, printed %q, %d provider requests", step, err, out, len(e.p.requests()))
		}
		t.Logf("%v", err)
	}
}

// TestOrphansTokenReadAtUse: the token file replaced after the configuration loaded and before the
// first provider request: the replacement reaches the provider. Control: a token read at load sends
// the old one.
func TestOrphansTokenReadAtUse(t *testing.T) {
	e := newOrphanEnv(t)
	old, replacement := e.b.Token("bw-ingestion"), e.b.Token("bw-orphan-report")
	e.gen(t, e.a, id.New(id.Ingestion))
	for _, atLoad := range []bool{false, true} {
		writeFile(t, e.tokenFile, old, 0o600)
		orphanHooks.tokenAtLoad = atLoad
		orphanHooks.afterLoad = func() { writeFile(t, e.tokenFile, replacement, 0o600) }
		before := len(e.p.sentTokens())
		_, _, err := e.run("-cluster", e.a)
		sent := e.p.sentTokens()[before:]
		if len(sent) == 0 {
			t.Fatalf("at load %v: no provider request", atLoad)
		}
		switch {
		case !atLoad && (err != nil || slices.Contains(sent, old) || !slices.Contains(sent, replacement)):
			t.Fatalf("read at use: err %v; the old token sent %v", err, slices.Contains(sent, old))
		case atLoad && (err == nil || !slices.Contains(sent, old)):
			t.Fatalf("control: a token read at load did not send the old token: %v", err)
		}
	}
	t.Logf("the replacement token reached the provider; control (read at load): the old token sent and refused")
}

// TestOrphansFailuresPrintNothing: the provider sealed after the first claim's generations are
// listed, or the database statement failing, exits nonzero naming the step, with no path printed.
// Control: printing each generation as it is listed prints the first.
func TestOrphansFailuresPrintNothing(t *testing.T) {
	for _, printAsListed := range []bool{false, true} {
		e := newOrphanEnv(t)
		c1, c2 := id.New(id.Ingestion), id.New(id.Ingestion)
		first := min(c1, c2)
		g1, g2 := e.gen(t, e.a, c1), e.gen(t, e.a, c2)
		firstGen := map[string]string{c1: g1, c2: g2}[first]
		e.p.seal = func(uri string) bool { return uri == "/v1/secret/metadata/gen/"+e.a+"/"+first+"/?list=true" }
		if printAsListed {
			orphanHooks.lister = func(l orphans.Lister, w io.Writer) orphans.Lister { return printing{l, w} }
		}
		out, stderr, err := e.run("-cluster", e.a)
		if err == nil {
			t.Fatal("sealed mid-run: no error")
		}
		if printAsListed {
			if !strings.Contains(out, firstGen) {
				t.Fatal("control: printing as listed did not print the first generation")
			}
			continue
		}
		if out != "" || err.Error() != "listing a claim's generations: the provider is unavailable" || leaks(err.Error()+stderr) {
			t.Fatalf("sealed mid-run: err %q, stdout %q, stderr %q", err, out, stderr)
		}

		e.p.mu.Lock()
		e.p.seal, e.p.sealed = nil, false
		e.p.mu.Unlock()
		orphanHooks.lister = func(l orphans.Lister, _ io.Writer) orphans.Lister {
			return breakDB{l, e.db, t}
		}
		out, stderr, err = e.run("-cluster", e.a)
		if err == nil || out != "" || err.Error() != "reading references and claims: the database failed" || leaks(err.Error()+stderr) {
			t.Fatalf("database failure: err %v, stdout %q, stderr %q", err, out, stderr)
		}
	}
	t.Logf("sealed mid-run and database failure: nonzero, step named, nothing printed; control (print as listed): first generation printed")
}

// breakDB renames staging_claim once the claims are listed, so the statement fails.
type breakDB struct {
	orphans.Lister
	db *sql.DB
	t  *testing.T
}

func (b breakDB) Claims(ctx context.Context, cluster string) (provider.Listing, error) {
	l, err := b.Lister.Claims(ctx, cluster)
	mustDB(b.t, b.db, `ALTER TABLE staging_claim RENAME TO staging_claim_gone`)
	return l, err
}

// TestOrphansNoCustomMetadata: a generation's custom metadata holding a sentinel never reaches the
// output, the process's stdout or stderr, the log or an error. Control: the provider's metadata
// response written to each of those surfaces is seen there.
func TestOrphansNoCustomMetadata(t *testing.T) {
	e := newOrphanEnv(t)
	p := e.gen(t, e.a, id.New(id.Ingestion))
	sentinel := "sentinel-" + provider.NewValueID()
	if s, body, err := e.b.Do(e.b.Admin(), http.MethodPost, "/v1/secret/metadata/"+p,
		map[string]any{"custom_metadata": map[string]string{"note": sentinel}}); err != nil || s/100 != 2 {
		t.Fatalf("writing custom metadata: status %d, %v: %s", s, err, body)
	}
	out, other, err := e.run("-cluster", e.a)
	if err != nil || !strings.Contains(out, p) || strings.Contains(out+other, sentinel) {
		t.Fatalf("err %v; output holds the sentinel %v", err, strings.Contains(out+other, sentinel))
	}
	tok := e.b.Token("bw-orphan-report")
	// An error is not a surface the control can reach: Collect reduces every listing error to its
	// step and class, whose exact text TestOrphansFailuresPrintNothing asserts.
	for _, surface := range []string{"output", "stderr", "log"} {
		orphanHooks.lister = func(l orphans.Lister, w io.Writer) orphans.Lister {
			return metadataPrinting{l, w, e.b, tok, surface}
		}
		out, other, err = e.run("-cluster", e.a)
		seen := out + other
		if err != nil {
			seen += err.Error()
		}
		if !strings.Contains(seen, sentinel) {
			t.Fatalf("control: the metadata response written to the %s was not seen: %v", surface, err)
		}
	}
	t.Logf("the sentinel is absent; control (the metadata response on output, stderr, log): seen on each")
}

// metadataPrinting is the control that writes each listed generation's metadata response to one
// surface: the command's output, the process's stderr, or the log.
type metadataPrinting struct {
	orphans.Lister
	w       io.Writer
	b       *baotest.Bao
	tok     string
	surface string
}

func (m metadataPrinting) Values(ctx context.Context, cluster, claim string) (provider.Generations, error) {
	g, err := m.Lister.Values(ctx, cluster, claim)
	for _, x := range g.Paths {
		_, body, _ := m.b.Do(m.tok, http.MethodGet, "/v1/secret/metadata/"+x.String(), nil)
		switch m.surface {
		case "output":
			m.w.Write(body)
		case "stderr":
			os.Stderr.Write(body)
		case "log":
			log.Print(string(body))
		}
	}
	return g, err
}

// TestOrphansChangesNothing: the provider's versions and every table's rows are unchanged by a run.
// Control: recording one audit row changes the dump.
func TestOrphansChangesNothing(t *testing.T) {
	e := newOrphanEnv(t)
	ab := e.claim(t, "encrypted", "abandoned", "-1 hour", "-1 minute")
	gens := []string{e.gen(t, e.a, ab), e.gen(t, e.a, ab), e.gen(t, e.a, id.New(id.Ingestion))}
	e.reference(t, gens[1])
	for _, audit := range []bool{false, true} {
		before := e.state(t, gens)
		if audit {
			orphanHooks.afterCollect = func(db *sql.DB) error {
				_, err := db.Exec(`INSERT INTO act (id, principal, principal_kind, via, action, subjects, epoch, at)
					SELECT $1, $2, 'human', 'tool', 'orphans', '{}', epoch, now() FROM installation_state`, id.New(id.Act), e.user)
				return err
			}
		}
		sent := len(e.p.requests())
		out, _, err := e.run("-cluster", e.a)
		if err != nil {
			t.Fatal(err)
		}
		if len(e.p.requests()) == sent || !strings.Contains(out, "orphan\t"+gens[0]+"\t") ||
			!strings.Contains(out, "orphan\t"+gens[2]+"\t") || strings.Contains(out, gens[1]) {
			t.Fatalf("the run did not list and report the seeded orphans:\n%s", out)
		}
		after := e.state(t, gens)
		switch {
		case !audit && after != before:
			t.Fatalf("a run changed the provider or the database:\nbefore %s\nafter  %s", before, after)
		case audit && after == before:
			t.Fatal("control: an audit row left the dump unchanged")
		}
	}
	t.Logf("provider versions and every table's rows unchanged; control (one audit row): changed")
}

// state is every table's rows, hashed per table, and each generation's metadata as the provider
// holds it.
func (e *orphanEnv) state(t *testing.T, gens []string) string {
	t.Helper()
	rows, err := e.db.Query(`SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE' ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	rows.Close()
	if len(tables) < 15 {
		t.Fatalf("only %d tables", len(tables))
	}
	var b strings.Builder
	for _, n := range tables {
		var h string
		if err := e.db.QueryRow(`SELECT n || ':' || count(*) || ':' || coalesce(md5(string_agg(x::text, E'\n' ORDER BY x::text)), '')
			FROM (SELECT $1::text AS n) q, `+n+` x GROUP BY n`, n).Scan(&h); err != nil {
			if err == sql.ErrNoRows {
				h = n + ":0:"
			} else {
				t.Fatal(err)
			}
		}
		b.WriteString(h + "\n")
	}
	for _, g := range gens {
		s, body, err := e.b.Do(e.b.Admin(), http.MethodGet, "/v1/secret/metadata/"+g, nil)
		if err != nil || s != http.StatusOK {
			t.Fatalf("reading metadata: status %d, %v", s, err)
		}
		// The metadata only: each response carries its own request_id.
		var m struct{ Data json.RawMessage }
		if err := json.Unmarshal(body, &m); err != nil || len(m.Data) == 0 {
			t.Fatalf("reading metadata: %v", err)
		}
		b.Write(m.Data)
		b.WriteString("\n")
	}
	return b.String()
}
