package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/constants"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/staging"
	"github.com/ginsys/bronzeward/internal/talos"
)

// publicationReader is a read-only stand-in; its reports can be changed independently
// of the inventory so a same-minor control cannot hide a missing comparison.
type publicationReader struct {
	identity                talos.Identity
	version                 string
	identityErr, versionErr error
}

func (r *publicationReader) Identity(context.Context) (talos.Identity, error) {
	return r.identity, r.identityErr
}
func (r *publicationReader) Version(context.Context) (string, error) { return r.version, r.versionErr }
func (*publicationReader) MachineConfig(context.Context) (talos.Config, error) {
	return talos.Config{}, errors.New("configuration deliberately unavailable")
}
func (*publicationReader) Close() error { return nil }

// publicationReader makes b's observations sign through the real signer and read a stand-in node, and
// answers the signer's store, where a test makes the access read fail, and the node's reader.
func (b *buildEnv) publicationReader() (*kvStandIn, *publicationReader) {
	b.t.Helper()
	r := &publicationReader{version: "v1.14.5"}
	if err := b.db.QueryRow(`SELECT coalesce(m.smbios_uuid::text, ''), coalesce(m.talos_node_id, 'fixture-node'), c.talos_cluster_id
  FROM machine m JOIN cluster c ON c.id = m.cluster WHERE m.id = $1`, b.machine).Scan(&r.identity.SMBIOSUUID, &r.identity.NodeID, &r.identity.ClusterID); err != nil {
		b.t.Fatal(err)
	}
	b.a.d.exe, b.a.d.sg = &fakeExecutor{}, b.signer()
	b.a.o.dial = func(context.Context, []byte, string) (talos.Reader, error) { return r, nil }
	return b.env.keys.kv, r
}

func TestPublicationRunningMinor(t *testing.T) {
	for _, c := range []struct {
		name, version string
		status        int
	}{
		{"same minor", "v1.14.5", 0}, {"different minor", "v1.12.4", 422},
		{"missing version", "", 503}, {"malformed version", "provider-secret-canary", 503},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newBuildEnv(t)
			_, r := b.publicationReader()
			r.version = c.version
			u, ref := b.build()
			if c.status != 0 {
				if ref == nil || ref.status != c.status {
					t.Fatalf("publication refusal = %+v; want %d", ref, c.status)
				}
				if count(t, b.db, `SELECT count(*) FROM release`) != 0 || count(t, b.db, `SELECT count(*) FROM machine_state WHERE desired IS NOT NULL`) != 0 {
					t.Fatal("refusal changed release or Desired")
				}
				return
			}
			if ref != nil {
				t.Fatalf("matching control refused: %+v", ref)
			}
			b.unit = u
			rel, ref := b.commit()
			if rel == "" || ref != nil {
				t.Fatalf("matching control did not commit: %s %+v", rel, ref)
			}
		})
	}
}

func TestPublicationOffline(t *testing.T) {
	b := newBuildEnv(t)
	b.publicationReader()
	b.noSeed = true // the refusal is proven with no act, and signer() seeds none
	dropFirstKeyActs(t, b.db, b.cluster)
	_, ref := b.build()
	if ref == nil || ref.status != http.StatusServiceUnavailable {
		t.Fatalf("offline publication = %+v; want 503", ref)
	}
	// A cluster with no first-key act: the refusal names the remedy, and nothing creates the act.
	if !strings.Contains(ref.detail, "first-keys") {
		t.Fatalf("the refusal does not name the first-keys route: %q", ref.detail)
	}
	if n := count(t, b.db, `SELECT count(*) FROM first_key_act`); n != 0 {
		t.Fatalf("%d first-key acts after a refused publication; want none", n)
	}
}

// Existing compiler and worker tests need real publication observations without
// adding network dependencies to their pinned-value provider fixtures.
func (b *buildEnv) defaultPublicationReader(a *API) {
	a.d.exe, a.d.sg = &fakeExecutor{}, b.signer()
	a.o.dial = func(ctx context.Context, _ []byte, endpoint string) (talos.Reader, error) {
		r := &publicationReader{version: "v1.14.5"}
		err := b.db.QueryRowContext(ctx, `SELECT coalesce(m.smbios_uuid::text, ''), coalesce(m.talos_node_id, 'fixture-node'), c.talos_cluster_id
   FROM machine m JOIN cluster c ON c.id = m.cluster WHERE m.talos_endpoint = $1 AND m.cluster = $2 ORDER BY m.id LIMIT 1`,
			endpoint, b.cluster).Scan(&r.identity.SMBIOSUUID, &r.identity.NodeID, &r.identity.ClusterID)
		return r, err
	}
}

func TestPublicationIdentityAndAccess(t *testing.T) {
	status := func(code int) func(*buildEnv, *publicationReader) {
		return func(b *buildEnv, _ *publicationReader) {
			b.env.keys.kv.mu.Lock()
			defer b.env.keys.kv.mu.Unlock()
			b.env.keys.kv.status["GET"] = code
		}
	}
	for _, c := range []struct {
		name     string
		setup    func(*buildEnv, *publicationReader)
		status   int
		noAccess bool // the access read fails: no node value is recorded
	}{
		{"wrong node", func(_ *buildEnv, r *publicationReader) {
			r.identity.SMBIOSUUID = "1c6b7d2f-3e4a-4f60-9b0c-1d2e3f4a5b6c"
		}, 422, false},
		{"wrong cluster", func(_ *buildEnv, r *publicationReader) {
			r.identity.ClusterID = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
		}, 422, false},
		{"unreadable identity", func(_ *buildEnv, r *publicationReader) { r.identityErr = errors.New("secret-canary") }, 503, false},
		{"unreadable version", func(_ *buildEnv, r *publicationReader) { r.versionErr = errors.New("secret-canary") }, 503, false},
		{"absent", func(b *buildEnv, _ *publicationReader) {
			b.noSeed = true // the refusal is proven with no act, and signer() seeds none
			dropFirstKeyActs(b.t, b.db, b.cluster)
		}, 503, true},
		{"denied", status(http.StatusForbidden), 503, true},
		{"malformed", func(b *buildEnv, _ *publicationReader) {
			b.env.keys.kv.mu.Lock()
			defer b.env.keys.kv.mu.Unlock()
			b.env.keys.kv.garble = true
		}, 503, true},
		{"unavailable", status(http.StatusServiceUnavailable), 503, true},
		{"status", status(http.StatusTeapot), 503, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newBuildEnv(t)
			kv, r := b.publicationReader()
			kv.sentinel = "secret-canary"
			c.setup(b, r)
			// If compiler reads start, this different error would mask the expected node rule.
			b.held.readErr = errors.New("compiler should not be reached")
			_, ref := b.build()
			if ref == nil || ref.status != c.status || !strings.HasPrefix(ref.extra["rule"].(string), "publication-") {
				t.Fatalf("refusal %+v; want publication %d", ref, c.status)
			}
			o := readObs(t, b.db, b.machine)
			if o.purpose != "publication" || o.operation.String != b.job.op || o.plan.Valid {
				t.Fatalf("observation binding %+v", o)
			}
			if c.noAccess && (o.accessVersion.Valid || o.runningVersion.Valid || o.nodeID.Valid || len(o.digest) != 0 || o.assignment.Valid) {
				t.Fatal("failed access read recorded node values")
			}
			if b.logged("secret-canary") || count(t, b.db, `SELECT count(*) FROM machine_event WHERE entry::text LIKE '%secret-canary%'`) != 0 {
				t.Fatal("error text leaked")
			}
		})
	}
}

func TestPublicationEvidenceAtCommit(t *testing.T) {
	for _, c := range []string{"matching newer", "different newer", "unreadable newer", "endpoint", "endpoint changed back", "missing evidence", "wrong purpose", "old generation"} {
		t.Run(c, func(t *testing.T) {
			b := newBuildEnv(t)
			_, r := b.publicationReader()
			u, ref := b.build()
			if ref != nil {
				t.Fatal(ref)
			}
			switch c {
			case "matching newer", "different newer", "unreadable newer", "wrong purpose":
				if c == "different newer" {
					r.version = "v1.12.4"
				}
				if c == "unreadable newer" {
					r.version = ""
				}
				o, err := b.a.observe(t.Context(), b.machine, observeFor{purpose: "drift"})
				if err != nil {
					t.Fatal(err)
				}
				if c == "wrong purpose" {
					u.machines[0].observation = o.ID
				}
			case "endpoint", "endpoint changed back":
				for i, ep := range []string{"10.55.0.9", "10.55.0.3"} {
					if i == 1 && c == "endpoint" {
						break
					}
					decode[endpointChangeBody](t, b.do(b.api, replaceEndpoint(b.human("h-author"), fmt.Sprintf("k-endpoint-%d-0123456789", i), b.machine, `{"talosEndpoint":"`+ep+`"}`)), 201)
				}
			case "missing evidence":
				u.machines[0].observation = ""
			case "old generation":
				mustExec(t, b.db, `UPDATE operation SET owner_gen = 2 WHERE id = $1`, b.job.op)
				b.job.gen = 2
			}
			b.unit = u
			rel, ref := b.commit()
			if c == "matching newer" {
				if ref != nil || rel == "" {
					t.Fatalf("matching control %s %v", rel, ref)
				}
				return
			}
			if ref == nil || ref.status != 409 || rel != "" {
				t.Fatalf("stale evidence published: %s %+v", rel, ref)
			}
			if count(t, b.db, `SELECT count(*) FROM release`) != 0 || count(t, b.db, `SELECT count(*) FROM machine_state WHERE desired IS NOT NULL`) != 0 {
				t.Fatal("refused commit changed state")
			}
		})
	}
}

func TestPublicationReadFenced(t *testing.T) {
	for _, during := range []bool{false, true} {
		t.Run(fmt.Sprint(during), func(t *testing.T) {
			b := newBuildEnv(t)
			kv, _ := b.publicationReader()
			takeover := func() { mustExec(t, b.db, `UPDATE operation SET owner_gen = 2 WHERE id = $1`, b.job.op) }
			if during {
				// The takeover lands while the signer reads the CA key, as the access read it replaces did.
				kv.before = func(method, _ string) {
					if method == http.MethodGet {
						takeover()
					}
				}
			} else {
				takeover()
			}
			_, ref, err := b.a.buildRelease(t.Context(), b.job, publishClients{meta: b.held, reader: b.held, encrypter: b.held})
			if !errors.Is(err, staging.ErrFenced) || ref != nil {
				t.Fatalf("fenced read: ref=%v err=%v", ref, err)
			}
			want := 0
			if during {
				want = 1
			}
			if count(t, b.db, `SELECT count(*) FROM observation_start`) != want || count(t, b.db, `SELECT count(*) FROM observation`) != 0 {
				t.Fatal("superseded pass recorded completed evidence")
			}
		})
	}
}

func TestPublicationMixedCoverage(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(fmt.Sprint(bad), func(t *testing.T) {
			b := newBuildEnv(t)
			second := b.secondMachine(constants.DefaultKubernetesVersion)
			last := second
			if b.machine > last {
				last = b.machine
			}
			var endpoint string
			if err := b.db.QueryRow(`SELECT talos_endpoint FROM machine WHERE id = $1`, last).Scan(&endpoint); err != nil {
				t.Fatal(err)
			}
			dial := b.a.o.dial
			b.a.o.dial = func(ctx context.Context, tc []byte, ep string) (talos.Reader, error) {
				r, err := dial(ctx, tc, ep)
				if err == nil && bad && ep == endpoint {
					r.(*publicationReader).version = "v1.12.4"
				}
				return r, err
			}
			u, ref := b.build()
			if count(t, b.db, `SELECT count(*) FROM observation_start WHERE purpose = 'publication'`) != 2 {
				t.Fatal("coverage was not read in machine order")
			}
			if bad {
				if ref == nil || ref.status != 422 || ref.extra["machine"] != last {
					t.Fatalf("mixed coverage %+v", ref)
				}
				return
			}
			if ref != nil {
				t.Fatal(ref)
			}
			b.unit = u
			rel, ref := b.commit()
			if ref != nil || rel == "" {
				t.Fatalf("two-machine control %s %v", rel, ref)
			}
			if count(t, b.db, `SELECT count(*) FROM release_machine WHERE release = $1 AND publication_observation IS NOT NULL`, rel) != 2 {
				t.Fatal("release omitted evidence")
			}
		})
	}
}

func TestPublicationOlderReadCompletingLate(t *testing.T) {
	b := newBuildEnv(t)
	_, r := b.publicationReader()
	f := observeFor{purpose: "drift"}
	older := observed{ID: id.New(id.Observation), Unread: map[string]unreadValue{"configuration": {Cause: "fixture"}, "health": {Cause: "none-bound"}}}
	if err := b.a.inTx(t.Context(), func(tx *sql.Tx) error {
		_, _, err := b.a.startObservation(t.Context(), tx, b.machine, f, &older)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	older.Identity = &r.identity
	older.RunningVersion = "v1.12.4"
	older.Access = &observedAccess{Path: "access/talos-ca/" + b.cluster, Version: 3, CreatedTime: accessCreated, Serial: "a1",
		NotAfter: time.Now().Add(time.Minute)}
	u, ref := b.build()
	if ref != nil {
		t.Fatal(ref)
	}
	if err := b.a.recordObservation(t.Context(), b.machine, f, &older); err != nil {
		t.Fatal(err)
	}
	b.unit = u
	rel, ref := b.commit()
	if rel == "" || ref != nil {
		t.Fatalf("lower-basis late completion superseded fresh evidence: %s %v", rel, ref)
	}
	body := decode[releaseBody](t, b.do(b.api, call{method: "GET", path: prefix + "/releases/" + rel, token: b.human("h-viewer")}), 200)
	if len(body.Machines) != 1 || body.Machines[0].PublicationObservation != u.machines[0].observation {
		t.Fatal("release API lost evidence link")
	}
	list := decode[listPage[observationBody]](t, b.do(b.api, call{method: "GET", path: prefix + "/machines/" + b.machine + "/observations", token: b.human("h-viewer")}), 200)
	found := false
	for _, o := range list.Items {
		if o.ID == u.machines[0].observation {
			found = true
			if o.PublicationGeneration == nil || *o.PublicationGeneration != 1 || o.Operation == nil || *o.Operation != b.job.op || o.Plan != nil {
				t.Fatalf("observation API lost binding %+v", o)
			}
		}
	}
	if !found {
		t.Fatal("publication observation absent from API")
	}
}

func TestPublicationWorkerRefusal(t *testing.T) {
	b := newBuildEnv(t)
	_, r := b.publicationReader()
	r.version = "v1.12.4"
	b.a.d.pub = &publishClients{meta: b.held, reader: b.held, encrypter: b.held}
	b.a.d.timers = idle
	b.a.runPublish(t.Context(), b.job)
	var state, code, rule string
	if err := b.db.QueryRow(`SELECT state, split_part(error->>'type', ':', 4), error->>'rule' FROM operation WHERE id = $1`, b.job.op).Scan(&state, &code, &rule); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || code != "validation-failed" || rule != "publication-version" {
		t.Fatalf("worker outcome %s %s %s", state, code, rule)
	}
	if count(t, b.db, `SELECT count(*) FROM release`) != 0 || count(t, b.db, `SELECT count(*) FROM machine_state WHERE desired IS NOT NULL`) != 0 || count(t, b.db, `SELECT count(*) FROM observation`) != 1 {
		t.Fatal("worker refusal lost evidence or changed Desired")
	}
}

func TestPublicationEndpointRace(t *testing.T) {
	p := newPublishEnv(t)
	p.hold(`UPDATE machine SET talos_endpoint = '10.55.0.9:50000' WHERE id = $1`, p.machine)
	// commit() proves T3 blocked behind this writer before releasing it.
	_, ref := p.commit()
	if ref == nil || ref.status != 409 || ref.extra["cause"] != "endpoint-changed" {
		t.Fatalf("endpoint race %+v", ref)
	}
}

func TestPublicationLeaseAfterLockWait(t *testing.T) {
	b := newBuildEnv(t)
	b.publicationReader()
	mustExec(t, b.db, `UPDATE operation SET lease_until = clock_timestamp() + interval '2 seconds' WHERE id = $1`, b.job.op)
	held, err := b.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Rollback() }()
	mustExec(t, held, `SELECT 1 FROM operation WHERE id = $1 FOR UPDATE`, b.job.op)
	var holder int
	if err := held.QueryRow(`SELECT pg_backend_pid()`).Scan(&holder); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := b.a.observe(t.Context(), b.machine, observeFor{purpose: "publication", operation: b.job.op, generation: b.job.gen})
		done <- err
	}()
	waitBlockedBy(t, b.db, holder)
	// Let the lease expire while the lock holder makes no row change (no EPQ).
	mustExec(t, held, `SELECT pg_sleep(GREATEST(0, EXTRACT(EPOCH FROM lease_until - clock_timestamp())) + 0.02) FROM operation WHERE id = $1`, b.job.op)
	if err := held.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, staging.ErrFenced) {
		t.Fatalf("lapsed read after lock wait: %v", err)
	}
	if count(t, b.db, `SELECT count(*) FROM observation_start`) != 0 {
		t.Fatal("expired owner started a read")
	}
}
