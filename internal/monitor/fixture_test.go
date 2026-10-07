package monitor

import (
	"bytes"
	"context"
	"database/sql"
	"strconv"
	"testing"

	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/migrate"
)

// The statements the fixture inserts with, copied from the migrate package's test helpers
// (adoption_test.go, sources_test.go, release_test.go), which this package cannot import. Each
// takes the installation's epoch where it needs one.
const (
	// Every cluster has a Talos cluster ID; each row's is derived from its identifier.
	insertCluster = `INSERT INTO cluster (id, name, endpoint, contract, talos_cluster_id, created_at)
		VALUES ($1, $2, $3, $4, translate(encode(sha256(convert_to($1::text, 'UTF8')), 'base64'), '+/', '-_'), now())`
	// Every machine has a Talos endpoint and a platform; these rows share one of each.
	insertMachine = `INSERT INTO machine (id, cluster, smbios_uuid, serial, scope_state, talos_endpoint, platform, created_at)
		VALUES ($1, $2, $3, $4, $5, '10.55.0.3:50000', 'metal', now())`
	insertMachineState = `INSERT INTO machine_state (machine, applied_release, applied_digest, applied_source, baseline_revision)
		VALUES ($1, $2, $3, $4, $5)`
	insertImportBase = `INSERT INTO import_base_revision (id, machine, document, embedded, baseline_ciphertext, baseline_digest,
		baseline_digest_key, configuration_digest, created_at) VALUES ($1, $2, $3, '[]', $4, $5, $6, $7, now())`
	insertDraft = `INSERT INTO draft (id, cluster, title, state, revision, etag_token, created_at)
		VALUES ($1, $2, $3, $4, 1, $5, now())`
	insertOperation = `INSERT INTO operation (id, kind, state, epoch, owner, owner_gen, owner_epoch, lease_until,
		draft, draft_revision, ingestion, created_by, created_by_kind, created_role, created_at, result, error)
		SELECT $1, $2, $3, epoch, $4, $5, CASE WHEN $4::text IS NULL THEN NULL ELSE epoch END,
		CASE WHEN $4::text IS NULL THEN NULL ELSE now() + interval '1 minute' END,
		$6, $7, $8, $9, CASE WHEN $9::text IS NULL THEN NULL ELSE 'human' END,
		CASE WHEN $9::text IS NULL THEN NULL WHEN $2 = 'publish' THEN 'publisher' ELSE 'author' END, now(), $10::jsonb,
		$11::jsonb FROM installation_state`

	insertFragmentRevision = `INSERT INTO fragment_revision (id, cluster, name, layer, document, author, embedded, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, '[]', now())`
	insertFragmentReference = `INSERT INTO fragment_reference (revision, name, kind, version, encoding, generation)
		VALUES ($1, $2, $3, $4, $5, $6)`
	insertFragment = `INSERT INTO fragment (id, cluster, scope, name, layer, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'm3oxmlfh6phr7aigshdydcb4ji', now())`
	insertProfileRevision = `INSERT INTO profile_revision (id, cluster, name, author, created_at) VALUES ($1, $2, $3, $4, now())`
	insertProfilePin      = `INSERT INTO profile_revision_fragment (revision, cluster, position, fragment_revision) VALUES ($1, $2, $3, $4)`
	insertProfile         = `INSERT INTO profile (id, cluster, scope, name, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'm3oxmlfh6phr7aigshdydcb4ji', now())`
	insertAssignmentRevision = `INSERT INTO assignment_revision (id, cluster, machine, author, created_at) VALUES ($1, $2, $3, $4, now())`
	insertAssignmentProfile  = `INSERT INTO assignment_revision_profile (revision, position, profile) VALUES ($1, $2, $3)`
	insertAssignmentFragment = `INSERT INTO assignment_revision_fragment (revision, layer, position, fragment) VALUES ($1, $2, $3, $4)`
	insertAssignment         = `INSERT INTO assignment (id, cluster, machine, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, $2, $3, $4, $5, 'm3oxmlfh6phr7aigshdydcb4ji', now())`

	insertStatus = `INSERT INTO dependency_status (id, provider, object, version, class, reason, first_retained_at,
		unknown_since, observed_from, recorded_at, created) VALUES ($1, $2, $3, $4, $5, $6, now(), $7, now(), now(), $8)`
	insertRelease = `INSERT INTO release (id, cluster, draft, draft_revision, digest, contract, machinery_version,
		machinery_checksum, kubernetes_version, operation, published_by, published_role, epoch, published_at)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, epoch, now() FROM installation_state`
	insertReleaseMachine = `INSERT INTO release_machine (release, cluster, machine, import_base_revision,
		assignment_revision, mode, ciphertext, ciphertext_digest, configuration_digest, redacted, provenance, key_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12)`
	insertReleaseSource = `INSERT INTO release_source (release, cluster, kind, fragment, profile, assignment, name, machine,
		fragment_revision, profile_revision, assignment_revision, head_revision)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
	insertDependency = `INSERT INTO dependency (release, machine, kind, provider, object, version, created, reference,
		source_revision, source_digest, path, occurrence) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
)

// fixture is an installed database holding one cluster, one machine and its sources, and the
// releases published over them, whose dependency records each name one KV version and one Transit
// key version.
type fixture struct {
	db                      *sql.DB
	cluster, machine, claim string
	kv                      string // the KV object: gen/<cluster>/<claim>/v1
	kvCreated               string
	key                     string // the Transit key name
	keyCreated              string
	depKV, depKey           string   // their dependency_status ids
	releases                []string // the releases published so far, in order

	human, ibr, frv1, frvSite, frg, frgSite, prv, prf, asr, asg string
}

// execer is a *sql.DB or a *sql.Tx.
type execer interface {
	Exec(string, ...any) (sql.Result, error)
}

func mustExec(t *testing.T, db execer, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func digest(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

// seed installs a fresh database and publishes one release over the fixture's sources.
func seed(t *testing.T) *fixture {
	t.Helper()
	db, _ := dbtest.New(t)
	ms, err := migrate.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Apply(context.Background(), db, ms); err != nil {
		t.Fatal(err)
	}
	if _, _, err := migrate.Install(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	f := &fixture{db: db, human: id.New(id.Principal), cluster: id.New(id.Cluster), machine: id.New(id.Machine),
		claim: id.New(id.Ingestion), ibr: id.New(id.ImportBase), frv1: id.New(id.FragmentRevision),
		frvSite: id.New(id.FragmentRevision), frg: id.New(id.Fragment), frgSite: id.New(id.Fragment),
		prv: id.New(id.ProfileRevision), prf: id.New(id.Profile), asr: id.New(id.AssignmentRevision),
		asg: id.New(id.Assignment), depKV: id.New(id.Dependency), depKey: id.New(id.Dependency),
		kvCreated: "2026-09-26T09:12:40.123456789Z", key: "bw-artifact", keyCreated: "2026-09-26T09:12:40Z"}
	// The generation's path names its staging claim, which no row of the fixture needs.
	f.kv = "gen/" + f.cluster + "/" + f.claim + "/v1"

	// Inventory (adoption_test.go adoptionRows).
	mustExec(t, db, `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', 'https://idp.test', 'alice', now())`, f.human)
	mustExec(t, db, insertCluster, f.cluster, "office", "https://cp.example.test:6443", "v1.13")
	mustExec(t, db, insertMachine, f.machine, f.cluster, "0b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b", "SN-1", "normal")
	mustExec(t, db, insertMachineState, f.machine, nil, nil, nil, nil)
	mustExec(t, db, insertImportBase, f.ibr, f.machine, "machine:\n  type: worker\n", []byte{1}, digest(1), "transit/baseline-digest:1", digest(2))

	// Sources (sources_test.go sourceRows): a revision's rows are written in its own transaction.
	doc := "machine:\n  registries: {}\n"
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(t, tx, insertFragmentRevision, f.frv1, f.cluster, "registries", "override", doc, f.human)
	mustExec(t, tx, insertFragmentReference, f.frv1, "registry/example-pass", "string", 1, nil, f.kv)
	mustExec(t, tx, insertProfileRevision, f.prv, f.cluster, "workers", f.human)
	mustExec(t, tx, insertProfilePin, f.prv, f.cluster, 0, f.frv1)
	mustExec(t, tx, insertAssignmentRevision, f.asr, f.cluster, f.machine, f.human)
	mustExec(t, tx, insertAssignmentProfile, f.asr, 0, "workers")
	mustExec(t, tx, insertAssignmentFragment, f.asr, "site", 0, "site-dns")
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, insertFragmentRevision, f.frvSite, f.cluster, "site-dns", "site", doc, f.human)
	mustExec(t, db, insertFragment, f.frg, f.cluster, "cluster", "registries", "override", f.frv1, 1)
	mustExec(t, db, insertProfile, f.prf, f.cluster, "cluster", "workers", f.prv, 1)
	mustExec(t, db, insertAssignment, f.asg, f.cluster, f.machine, f.asr, 1)
	// The fragment the assignment selects under the site layer (release_test.go releaseRows).
	mustExec(t, db, insertFragment, f.frgSite, f.cluster, "cluster", "site-dns", "site", f.frvSite, 1)

	// The statuses publication seeds, retained (release_test.go releaseRows).
	mustExec(t, db, insertStatus, f.depKV, "kv", f.kv, 1, "retained", nil, nil, f.kvCreated)
	mustExec(t, db, insertStatus, f.depKey, "transit", f.key, 1, "retained", nil, nil, f.keyCreated)
	f.publishAgain(t)
	return f
}

// publishAgain publishes another release over the fixture's sources, from a new draft by a new
// publish operation, naming the same KV and Transit versions, and returns its id.
func (f *fixture) publishAgain(t *testing.T) string {
	t.Helper()
	db := f.db
	n := len(f.releases) + 1
	draft, publish, rel := id.New(id.Draft), id.New(id.Operation), id.New(id.Release)
	mustExec(t, db, insertDraft, draft, f.cluster, "release "+strconv.Itoa(n), "open", "m3oxmlfh6phr7aigshdydcb4ji")
	mustExec(t, db, insertOperation, publish, "publish", "running", "run-1/4242/publish-"+strconv.Itoa(n), 1, draft, 1, nil,
		f.human, nil, nil)
	// A release's rows are written in its own transaction (release_test.go releaseRows).
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(t, tx, insertRelease, rel, f.cluster, draft, 1, digest(3), "v1.13", "v1.13.6",
		"h1:2rBcdYQ4m1u3oPmvbMQw3F9dZb8i0EwQnJ6y5Kx8sJ0=", "v1.36.0", publish, f.human, "publisher")
	mustExec(t, tx, insertReleaseMachine, rel, f.cluster, f.machine, f.ibr, f.asr, "metal", "vault:v1:YWJj", digest(4), digest(5),
		"machine:\n  type: worker\n", `[]`, f.key)
	mustExec(t, tx, insertReleaseSource, rel, f.cluster, "fragment", f.frg, nil, nil, "registries", nil, f.frv1, nil, nil, 1)
	mustExec(t, tx, insertReleaseSource, rel, f.cluster, "fragment", f.frgSite, nil, nil, "site-dns", nil, f.frvSite, nil, nil, 1)
	mustExec(t, tx, insertReleaseSource, rel, f.cluster, "profile", nil, f.prf, nil, "workers", nil, nil, f.prv, nil, 1)
	mustExec(t, tx, insertReleaseSource, rel, f.cluster, "assignment", nil, nil, f.asg, nil, f.machine, nil, nil, f.asr, 1)
	mustExec(t, tx, insertDependency, rel, f.machine, "effective", "kv", f.kv, 1, f.kvCreated, "registry/example-pass", nil, nil, nil, nil)
	mustExec(t, tx, insertDependency, rel, f.machine, "reproduction", "kv", f.kv, 1, f.kvCreated, "registry/example-pass",
		f.frv1, digest(6), "registries:/machine/registries", 0)
	mustExec(t, tx, insertDependency, rel, f.machine, "encryption", "transit", f.key, 1, f.keyCreated,
		nil, nil, nil, nil, nil)
	mustExec(t, tx, `UPDATE draft SET state = 'published', release = $2, revision = revision + 1 WHERE id = $1`, draft, rel)
	mustExec(t, tx, `UPDATE machine_state SET desired = $2, revision = revision + 1 WHERE machine = $1`, f.machine, rel)
	mustExec(t, tx, `UPDATE operation SET state = 'succeeded', result = jsonb_build_object('release', $2::text) WHERE id = $1`,
		publish, rel)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.releases = append(f.releases, rel)
	return rel
}

// TestFixture checks that the fixture's releases each name its KV and Transit key versions.
func TestFixture(t *testing.T) {
	f := seed(t)
	f.publishAgain(t)
	if len(f.releases) != 2 {
		t.Fatalf("releases = %v; want 2", f.releases)
	}
	for _, c := range []struct{ provider, object string }{{"kv", f.kv}, {"transit", f.key}} {
		var n int
		if err := f.db.QueryRow(`SELECT count(DISTINCT release) FROM dependency WHERE provider = $1 AND object = $2 AND version = 1`,
			c.provider, c.object).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 2 {
			t.Errorf("%s %s: %d releases; want 2", c.provider, c.object, n)
		}
	}
}
