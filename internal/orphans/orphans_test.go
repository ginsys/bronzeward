package orphans

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/migrate"
	"github.com/ginsys/bronzeward/internal/provider"
)

// The selection of persistence-api.md §6.4 against a real database and a listing held in memory;
// the live provider's listing is internal/provider's and the command's check.

// fakeLister is a generation tree, cluster → claim → value ids, and the failure of one step.
type fakeLister struct {
	tree     map[string]map[string][]string
	clusters int // calls of Clusters
	fail     string
	err      error
}

func (f *fakeLister) Clusters(context.Context) (provider.Listing, error) {
	f.clusters++
	if f.fail == "clusters" {
		return provider.Listing{}, f.err
	}
	var l provider.Listing
	for cl := range f.tree {
		l.Names = append(l.Names, cl)
	}
	slices.Sort(l.Names)
	return l, nil
}

func (f *fakeLister) Claims(_ context.Context, cluster string) (provider.Listing, error) {
	if f.fail == "claims" {
		return provider.Listing{}, f.err
	}
	var l provider.Listing
	for claim := range f.tree[cluster] {
		l.Names = append(l.Names, claim)
	}
	slices.Sort(l.Names)
	return l, nil
}

func (f *fakeLister) Values(_ context.Context, cluster, claim string) (provider.Generations, error) {
	if f.fail == "values" {
		return provider.Generations{}, f.err
	}
	var g provider.Generations
	for _, v := range f.tree[cluster][claim] {
		p, err := provider.NewGenerationPath(cluster, claim, v)
		if err != nil {
			panic(err)
		}
		g.Paths = append(g.Paths, p)
	}
	return g, nil
}

// world is one database and generation tree: cluster a (recorded) and cluster b (whose rows a
// restore removed), a machine and an import base revision to reference generations from.
type world struct {
	db                  *sql.DB
	a, b, machine, base string
	tree                map[string]map[string][]string
}

func exec(t *testing.T, db interface {
	Exec(string, ...any) (sql.Result, error)
}, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func newWorld(t *testing.T) *world {
	t.Helper()
	db, _ := dbtest.New(t)
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
	w := &world{db: db, a: id.New(id.Cluster), b: id.New(id.Cluster), machine: id.New(id.Machine), base: id.New(id.ImportBase),
		tree: map[string]map[string][]string{}}
	exec(t, db, `INSERT INTO cluster (id, name, endpoint, contract, talos_cluster_id, created_at)
		VALUES ($1, 'office', 'https://cp.example.test:6443', 'v1.13', '8TMwqXnWOTdw7xFDHSn-f6JMbBQrSWAuyzCfGIRVSL0=', now())`, w.a)
	exec(t, db, `INSERT INTO machine (id, cluster, smbios_uuid, serial, scope_state, talos_endpoint, created_at)
		VALUES ($1, $2, '0b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b', 'SN-1', 'normal', '10.55.0.3:50000', now())`, w.machine, w.a)
	exec(t, db, `INSERT INTO import_base_revision (id, machine, document, baseline_ciphertext, baseline_digest,
		baseline_digest_key, configuration_digest, created_at) VALUES ($1, $2, 'machine: {}', '\x01', $3, 'transit/baseline-digest:1', $3, now())`,
		w.base, w.machine, bytes.Repeat([]byte{1}, 32))
	return w
}

// claim records a claim in cluster a: mode, state, and the lease and expiry relative to now.
func (w *world) claim(t *testing.T, mode, state, lease, expiry string) string {
	t.Helper()
	c := id.New(id.Ingestion)
	exec(t, w.db, `INSERT INTO staging_claim (id, mode, state, owner, owner_gen, owner_epoch, lease_until, expires_at,
		cluster, machine, kind, created_at)
		SELECT $1, $2, $3, 'a/4242/start-1', 1, epoch, now() + $4::interval, now() + $5::interval, $6, $7, 'import', now()
		FROM installation_state`, c, mode, state, lease, expiry, w.a, w.machine)
	return c
}

// gen adds a generation of claim under cluster to the tree and returns its path.
func (w *world) gen(cluster, claim string) string {
	if w.tree[cluster] == nil {
		w.tree[cluster] = map[string][]string{}
	}
	v := provider.NewValueID()
	w.tree[cluster][claim] = append(w.tree[cluster][claim], v)
	return "gen/" + cluster + "/" + claim + "/" + v
}

// reference names path from the import base revision.
func (w *world) reference(t *testing.T, db interface {
	Exec(string, ...any) (sql.Result, error)
}, path string) {
	t.Helper()
	exec(t, db, `INSERT INTO import_base_reference (revision, name, kind, version, encoding, generation)
		VALUES ($1, $2, 'string', 1, NULL, $3)`, w.base, "n"+strings.ToLower(provider.NewValueID()), path)
}

func paths(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Path)
	}
	slices.Sort(out)
	return out
}

func sorted(s ...string) []string { slices.Sort(s); return s }

// matrix is the selection cases of §16, by name, in one world.
type matrix struct {
	w                                                      *world
	abandoned, referenced, released, absent, held, resumed string
	heldExpired, resumedExpired, transientLapsed           string
	encryptedLapsed, removed                               string
}

func newMatrix(t *testing.T) *matrix {
	w := newWorld(t)
	m := &matrix{w: w}
	ab := w.claim(t, "encrypted", "abandoned", "-1 hour", "-1 minute")
	m.abandoned, m.referenced = w.gen(w.a, ab), w.gen(w.a, ab)
	w.reference(t, w.db, m.referenced)
	m.released = w.gen(w.a, w.claim(t, "transient", "released", "1 minute", "1 hour"))
	m.absent = w.gen(w.a, id.New(id.Ingestion))
	m.held = w.gen(w.a, w.claim(t, "encrypted", "held", "1 minute", "1 hour"))
	m.resumed = w.gen(w.a, w.claim(t, "encrypted", "resumed", "1 minute", "1 hour"))
	m.heldExpired = w.gen(w.a, w.claim(t, "encrypted", "held", "-2 hours", "-1 hour"))
	m.resumedExpired = w.gen(w.a, w.claim(t, "encrypted", "resumed", "-2 hours", "-1 hour"))
	m.transientLapsed = w.gen(w.a, w.claim(t, "transient", "held", "-1 minute", "1 hour"))
	m.encryptedLapsed = w.gen(w.a, w.claim(t, "encrypted", "held", "-1 minute", "1 hour"))
	m.removed = w.gen(w.b, id.New(id.Ingestion))
	return m
}

func (m *matrix) run(t *testing.T, cluster string, o options) (Report, *fakeLister) {
	t.Helper()
	l := &fakeLister{tree: m.w.tree}
	r, err := collect(t.Context(), l, m.w.db, cluster, o)
	if err != nil {
		t.Fatal(err)
	}
	return r, l
}

// TestSelection: the reference rows decide, then the recorded state; held or resumed claims that
// compilation §3.5 treats as abandoned are listed apart; each control changes the outcome.
func TestSelection(t *testing.T) {
	m := newMatrix(t)
	r, _ := m.run(t, "", options{})
	wantOrphans := sorted(m.abandoned, m.released, m.absent, m.removed)
	wantExpired := sorted(m.heldExpired, m.resumedExpired, m.transientLapsed)
	if got := paths(r.Orphans); !slices.Equal(got, wantOrphans) {
		t.Fatalf("orphans %q, want %q", got, wantOrphans)
	}
	if got := paths(r.Expired); !slices.Equal(got, wantExpired) {
		t.Fatalf("expired %q, want %q", got, wantExpired)
	}
	if r.Listed != 11 {
		t.Fatalf("listed %d, want 11", r.Listed)
	}
	for _, e := range r.Orphans {
		if e.Path == m.absent || e.Path == m.removed {
			if e.State != "" || !e.CreatedAt.IsZero() {
				t.Fatalf("a path without a claim row reports %+v", e)
			}
		} else if e.State == "" || e.CreatedAt.IsZero() || e.ExpiresAt.IsZero() {
			t.Fatalf("a recorded claim's orphan lacks its fields: %+v", e)
		}
	}
	for _, e := range r.Expired {
		if e.Mode == "" || e.LeaseUntil.IsZero() || (e.State != "held" && e.State != "resumed") {
			t.Fatalf("an expired entry lacks its fields: %+v", e)
		}
	}
	t.Logf("orphans: abandoned, released, absent claim row, restore-removed cluster; listed apart: two past expiry, one transient past lease")

	controls := []struct {
		name string
		o    options
		bad  func(Report) bool
	}{
		{"select by claim state alone", options{stateOnly: true}, func(r Report) bool { return slices.Contains(paths(r.Orphans), m.referenced) }},
		{"read-time abandonment", options{readTimeAbandon: true}, func(r Report) bool { return slices.Contains(paths(r.Orphans), m.heldExpired) }},
		{"omit every held or resumed claim", options{omitLive: true}, func(r Report) bool { return len(r.Expired) == 0 }},
		{"every lapsed lease abandons", options{lapsedLease: true}, func(r Report) bool { return slices.Contains(paths(r.Expired), m.encryptedLapsed) }},
		{"only the clusters the database records", options{dbClusters: true}, func(r Report) bool { return !slices.Contains(paths(r.Orphans), m.removed) }},
	}
	for _, c := range controls {
		cr, _ := m.run(t, "", c.o)
		if !c.bad(cr) {
			t.Fatalf("control %s: the report did not change", c.name)
		}
		t.Logf("control %s: the report changes", c.name)
	}
}

// TestClusterScope: --cluster lists only that cluster's subtree and never lists gen/.
func TestClusterScope(t *testing.T) {
	m := newMatrix(t)
	r, l := m.run(t, m.w.a, options{})
	if slices.Contains(paths(r.Orphans), m.removed) || !slices.Contains(paths(r.Orphans), m.absent) {
		t.Fatalf("scoped orphans %q", paths(r.Orphans))
	}
	if l.clusters != 0 {
		t.Fatalf("a scoped run listed gen/ %d times", l.clusters)
	}
	if _, err := collect(t.Context(), l, m.w.db, "cl_not-an-id", options{}); err == nil {
		t.Fatal("a cluster that is not an identifier was accepted")
	}
}

// TestReleaseDuringRun: a draft transaction releasing a claim, its reference rows committed with
// it, while the report runs: one statement sees it before or after, never half.
func TestReleaseDuringRun(t *testing.T) {
	for _, split := range []bool{false, true} {
		w := newWorld(t)
		c := w.claim(t, "encrypted", "held", "1 minute", "1 hour")
		p := w.gen(w.a, c)
		commit := func() {
			tx, err := w.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			w.reference(t, tx, p)
			exec(t, tx, `UPDATE staging_claim SET state = 'released' WHERE id = $1`, c)
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}
		r, err := collect(t.Context(), &fakeLister{tree: w.tree}, w.db, "", options{split: split, between: commit})
		if err != nil {
			t.Fatal(err)
		}
		reported := slices.Contains(paths(r.Orphans), p)
		switch {
		case !split && reported:
			t.Fatal("a generation released with its reference rows was reported")
		case split && !reported:
			t.Fatal("control: two statements with the release committed between them did not report it")
		}
	}
	t.Logf("one statement: not reported; control (two statements, release between): reported")
}

// TestHeldPastExpiryUnderLock: a claim past its expiry, still recorded held while a draft
// transaction that passed its owner check holds its lock: listed apart, never an orphan.
func TestHeldPastExpiryUnderLock(t *testing.T) {
	for _, readTime := range []bool{false, true} {
		w := newWorld(t)
		c := w.claim(t, "encrypted", "held", "-2 hours", "-1 minute")
		p := w.gen(w.a, c)
		tx, err := w.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		var got string
		if err := tx.QueryRow(`SELECT id FROM staging_claim WHERE id = $1 FOR UPDATE`, c).Scan(&got); err != nil {
			t.Fatal(err)
		}
		r, err := collect(t.Context(), &fakeLister{tree: w.tree}, w.db, "", options{readTimeAbandon: readTime})
		if err != nil {
			t.Fatal(err)
		}
		w.reference(t, tx, p)
		exec(t, tx, `UPDATE staging_claim SET state = 'released' WHERE id = $1`, c)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		orphan, apart := slices.Contains(paths(r.Orphans), p), slices.Contains(paths(r.Expired), p)
		switch {
		case !readTime && (orphan || !apart):
			t.Fatalf("held past expiry under lock: orphan %v, listed apart %v", orphan, apart)
		case readTime && !orphan:
			t.Fatal("control: read-time abandonment did not report it")
		}
	}
	t.Logf("held past expiry under a passed owner check: listed apart, not an orphan; control (read-time abandonment): reported")
}

// TestFailures: every failure names its step and class, never a path or an identifier, and
// returns no report.
func TestFailures(t *testing.T) {
	m := newMatrix(t)
	for _, c := range []struct {
		fail, step string
		err        error
		class      string
	}{
		{"clusters", "listing clusters", provider.ErrUnavailable, "the provider is unavailable"},
		{"claims", "listing a cluster's claims", provider.ErrDenied, "the provider refused the request"},
		{"values", "listing a claim's generations", provider.ErrProtocol, "the provider's response was not understood"},
		{"values", "listing a claim's generations", errors.New("provider: GET /v1/secret/metadata/" + m.absent + "?list=true: status 500"), "the provider answered with an unexpected status"},
	} {
		l := &fakeLister{tree: m.w.tree, fail: c.fail, err: fmt.Errorf("provider: GET /v1/secret/metadata/%s: %w", m.absent, c.err)}
		r, err := collect(t.Context(), l, m.w.db, "", options{})
		checkFailure(t, m, r, err, c.step, c.class)
	}
	closed, _ := dbtest.New(t)
	closed.Close()
	r, err := collect(t.Context(), &fakeLister{tree: m.w.tree}, closed, "", options{})
	checkFailure(t, m, r, err, "reading references and claims", "the database failed")
}

func checkFailure(t *testing.T, m *matrix, r Report, err error, step, class string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: no error", step)
	}
	if want := "orphans: " + step + ": " + class; err.Error() != want {
		t.Fatalf("error %q, want %q", err, want)
	}
	if strings.Contains(err.Error(), "gen/") || strings.Contains(err.Error(), "cl_") || strings.Contains(err.Error(), "ing_") {
		t.Fatalf("the error names a path or identifier: %q", err)
	}
	if len(r.Orphans)+len(r.Expired) != 0 {
		t.Fatalf("%s: a failed run returned entries", step)
	}
}

// TestWrite: the output names paths, claim ids, states, modes and times, one line each, and a
// summary; nothing else.
func TestWrite(t *testing.T) {
	m := newMatrix(t)
	r, _ := m.run(t, "", options{})
	var buf bytes.Buffer
	if err := Write(&buf, r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != len(r.Orphans)+len(r.Expired)+1 {
		t.Fatalf("%d lines:\n%s", len(lines), out)
	}
	for _, p := range append(paths(r.Orphans), paths(r.Expired)...) {
		if !strings.Contains(out, p) {
			t.Fatalf("output lacks %s:\n%s", p, out)
		}
	}
	for _, p := range []string{m.referenced, m.held, m.resumed, m.encryptedLapsed} {
		if strings.Contains(out, p) {
			t.Fatalf("output names %s:\n%s", p, out)
		}
	}
	if !strings.Contains(lines[len(lines)-1], "4 orphans, 3 expired and not yet abandoned, 11 generations listed") {
		t.Fatalf("summary %q", lines[len(lines)-1])
	}
	t.Logf("output:\n%s", out)
}
