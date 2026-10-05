package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/staging"
)

// publishEnv is a draft ready for its publication commit (T3, persistence-api.md §6.2): at
// revision 1 it carries the machine's import base, a new revision of an existing fragment, a
// fragment it introduces and the machine's first assignment, which selects both fragments and a
// profile; the profile and the fragment it pins are used unchanged. A running publish operation
// owned by this process binds the draft revision, and unit is its compilation's hand-off.
type publishEnv struct {
	*draftEnv
	a     *API
	owner staging.Owner
	job   publishJob
	unit  releaseUnit

	ibr, kvPath                     string
	network, networkOld, networkNew string // the changed fragment's head and its revisions
	storage                         string // the introduced fragment's revision
	base, baseRev                   string // the unchanged fragment's head and revision
	prf, prv                        string // the unchanged profile's head and revision
	asr                             string // the introduced assignment's revision
}

var (
	kvCreated      = time.Date(2026, 9, 26, 9, 0, 0, 123456789, time.UTC)
	transitCreated = time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC)
	began          = time.Date(2026, 9, 26, 9, 10, 0, 0, time.UTC)
)

func newPublishEnv(t *testing.T) *publishEnv {
	t.Helper()
	d := newDraftEnv(t)
	p := &publishEnv{draftEnv: d}
	var epoch string
	if err := d.db.QueryRow(`SELECT epoch FROM installation_state`).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	p.owner = staging.Owner{ID: "run-1/4242/publish", Epoch: epoch}
	p.a = d.buildWith(deps{owner: p.owner}, options{})

	p.ibr = id.New(id.ImportBase)
	p.kvPath = "gen/" + d.cluster + "/" + id.New(id.Ingestion) + "/pass"
	mustExec(t, d.db, `INSERT INTO import_base_revision (id, machine, document, baseline_ciphertext, baseline_digest,
		baseline_digest_key, configuration_digest, created_at) VALUES ($1, $2, 'machine: {}', '\x01', $3, 'transit/baseline-digest:1', $3, now())`,
		p.ibr, d.machine, make([]byte, 32))
	mustExec(t, d.db, `INSERT INTO import_base_reference (revision, name, kind, version, generation)
		VALUES ($1, 'registry/pass', 'string', 1, $2)`, p.ibr, p.kvPath)
	mustExec(t, d.db, `INSERT INTO draft_entry (draft, cluster, kind, machine, import_base_revision)
		VALUES ($1, $2, 'import-base', $3, $4)`, d.draft, d.cluster, d.machine, p.ibr)

	p.networkOld = d.fragmentRevision(d.cluster, "network", "site")
	p.network = d.fragmentHead("network", "site", p.networkOld, 1)
	p.networkNew = d.fragmentRevision(d.cluster, "network", "site")
	p.storage = d.fragmentRevision(d.cluster, "storage", "role")
	p.baseRev = d.fragmentRevision(d.cluster, "base", "global")
	p.base = d.fragmentHead("base", "global", p.baseRev, 1)

	// A revision's rows are written in its own transaction (0009).
	tx, err := d.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	p.prv, p.prf = id.New(id.ProfileRevision), id.New(id.Profile)
	mustExec(t, tx, `INSERT INTO profile_revision (id, cluster, name, author, created_at) VALUES ($1, $2, 'standard', $3, now())`,
		p.prv, d.cluster, d.seed)
	mustExec(t, tx, `INSERT INTO profile_revision_fragment (revision, cluster, position, fragment_revision) VALUES ($1, $2, 0, $3)`,
		p.prv, d.cluster, p.baseRev)
	p.asr = id.New(id.AssignmentRevision)
	mustExec(t, tx, `INSERT INTO assignment_revision (id, cluster, machine, author, created_at) VALUES ($1, $2, $3, $4, now())`,
		p.asr, d.cluster, d.machine, d.seed)
	mustExec(t, tx, `INSERT INTO assignment_revision_profile (revision, position, profile) VALUES ($1, 0, 'standard')`, p.asr)
	mustExec(t, tx, `INSERT INTO assignment_revision_fragment (revision, layer, position, fragment)
		VALUES ($1, 'site', 0, 'network'), ($1, 'role', 0, 'storage')`, p.asr)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	mustExec(t, d.db, `INSERT INTO profile (id, cluster, scope, name, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, $2, 'cluster', 'standard', $3, 1, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, p.prf, d.cluster, p.prv)

	mustExec(t, d.db, `INSERT INTO draft_source_entry (draft, cluster, kind, name, fragment_revision, base)
		VALUES ($1, $2, 'fragment', 'network', $3, 1), ($1, $2, 'fragment', 'storage', $4, NULL)`,
		d.draft, d.cluster, p.networkNew, p.storage)
	mustExec(t, d.db, `INSERT INTO draft_source_entry (draft, cluster, kind, machine, assignment_revision, base)
		VALUES ($1, $2, 'assignment', $3, $4, NULL)`, d.draft, d.cluster, d.machine, p.asr)

	op := id.New(id.Operation)
	mustExec(t, d.db, `INSERT INTO operation (id, kind, state, owner, owner_gen, owner_epoch, lease_until, draft, draft_revision,
			created_by, created_by_kind, created_role, epoch, created_at)
		VALUES ($1, 'publish', 'running', $2, 1, $3, now() + interval '1 minute', $4, 1, $5, 'human', 'publisher', $3, now())`,
		op, p.owner.ID, epoch, d.draft, d.seed)
	p.job = publishJob{op: op, draft: d.draft, cluster: d.cluster, draftRev: 1, gen: 1}

	redacted := "machine:\n  type: worker\n"
	p.unit = releaseUnit{
		renderer: rendererBody{Contract: "v1.13", MachineryVersion: "v1.13.6",
			MachineryChecksum: "h1:2rBcdYQ4m1u3oPmvbMQw3F9dZb8i0EwQnJ6y5Kx8sJ0=", KubernetesVersion: "v1.36.0"},
		unchanged: []usedHead{{kind: "fragment", head: p.base, revision: p.baseRev, headRevision: 1},
			{kind: "profile", head: p.prf, revision: p.prv, headRevision: 1}},
		machines: []unitMachine{{
			machine: d.machine, importBase: p.ibr, assignment: p.asr, mode: "container",
			ciphertext: releaseCipher, configuration: sha256.Sum256([]byte("machine: {}")), redacted: &redacted,
			effective: []unitDependency{{reference: "registry/pass", object: p.kvPath, version: 1, created: kvCreated}},
			reproduction: []unitDependency{{reference: "registry/pass", object: p.kvPath, version: 1, created: kvCreated,
				source: p.ibr, digest: sha256.Sum256([]byte("machine: {}")), path: "doc[0]/machine/registries", occurrence: 0}},
			encryption: unitDependency{object: "bw-artifact", version: 1, created: transitCreated},
		}},
		statuses: []unitStatus{
			{provider: classify.KV, object: p.kvPath, version: 1, began: began,
				result: classify.Result{Class: classify.Retained, Created: kvCreated}},
			{provider: classify.Transit, object: "bw-artifact", version: 1, began: began,
				result: classify.Result{Class: classify.Retained, Created: transitCreated,
					Date: time.Date(2026, 9, 26, 9, 10, 1, 0, time.UTC)}},
		},
	}
	return p
}

func (p *publishEnv) commit() (string, *refusal) {
	p.t.Helper()
	rel, ref, err := p.a.publishCommit(p.t.Context(), p.job, p.unit)
	if err != nil {
		p.t.Fatalf("publishCommit: %v", err)
	}
	return rel, ref
}

// T3 (persistence-api.md §6.2) commits the whole release: its machine, sources, dependency records
// and the statuses it seeds (dependency monitor §5.2); it moves the changed head, creates the
// introduced ones, selects the release as the machine's Desired, publishes the draft and ends its
// operation succeeded naming the release, with its terminal event, in one transaction.
func TestPublishCommit(t *testing.T) {
	p := newPublishEnv(t)
	rel, ref := p.commit()
	if ref != nil {
		t.Fatalf("refused: %v", ref)
	}

	var draft, op, by, role, epoch string
	var rev int
	var digest []byte
	if err := p.db.QueryRow(`SELECT draft, draft_revision, digest, operation, published_by, published_role, epoch
		FROM release WHERE id = $1`, rel).Scan(&draft, &rev, &digest, &op, &by, &role, &epoch); err != nil {
		t.Fatal(err)
	}
	if draft != p.draft || rev != 1 || op != p.job.op || by != p.seed || role != "publisher" || epoch != p.owner.Epoch {
		t.Fatalf("release %s %d %s %s %s %s", draft, rev, op, by, role, epoch)
	}
	stored, err := releaseDigest(t.Context(), p.db, rel)
	if err != nil || string(stored[:]) != string(digest) {
		t.Fatalf("release digest %x, recomputed %x, %v", digest, stored, err)
	}

	var ibr, asr, cipher, redacted string
	var cdigest, cfg []byte
	if err := p.db.QueryRow(`SELECT import_base_revision, assignment_revision, ciphertext, ciphertext_digest, configuration_digest,
		redacted FROM release_machine WHERE release = $1 AND machine = $2`, rel, p.machine).
		Scan(&ibr, &asr, &cipher, &cdigest, &cfg, &redacted); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(releaseCipher))
	if ibr != p.ibr || asr != p.asr || cipher != releaseCipher || string(cdigest) != string(want[:]) ||
		string(cfg) != string(p.unit.machines[0].configuration[:]) || redacted != *p.unit.machines[0].redacted {
		t.Fatalf("release machine %s %s %s %x %x %q", ibr, asr, cipher, cdigest, cfg, redacted)
	}

	// O3: each source's head revision is the head's counter after the move.
	var storageHead, asg string
	if err := p.db.QueryRow(`SELECT id FROM fragment WHERE cluster = $1 AND name = 'storage'`, p.cluster).Scan(&storageHead); err != nil {
		t.Fatal(err)
	}
	if err := p.db.QueryRow(`SELECT id FROM assignment WHERE machine = $1`, p.machine).Scan(&asg); err != nil {
		t.Fatal(err)
	}
	sources := map[string][2]any{}
	rows, err := p.db.Query(`SELECT coalesce(fragment, profile, assignment), coalesce(fragment_revision, profile_revision,
		assignment_revision), head_revision FROM release_source WHERE release = $1`, rel)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var head string
		var revision sql.NullString
		var n int
		if err := rows.Scan(&head, &revision, &n); err != nil {
			t.Fatal(err)
		}
		sources[head] = [2]any{revision.String, n}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	wantSources := map[string][2]any{p.network: {p.networkNew, 2}, storageHead: {p.storage, 1}, p.base: {p.baseRev, 1},
		p.prf: {p.prv, 1}, asg: {p.asr, 1}}
	if len(sources) != len(wantSources) {
		t.Fatalf("sources %v; want %v", sources, wantSources)
	}
	for h, w := range wantSources {
		if sources[h] != w {
			t.Fatalf("source %s %v; want %v", h, sources[h], w)
		}
	}
	for _, h := range []struct {
		table, id, revision string
		n                   int
	}{{"fragment", p.network, p.networkNew, 2}, {"fragment", storageHead, p.storage, 1}, {"fragment", p.base, p.baseRev, 1},
		{"profile", p.prf, p.prv, 1}, {"assignment", asg, p.asr, 1}} {
		var revision string
		var n int
		if err := p.db.QueryRow(`SELECT head_revision_id, head_revision FROM `+h.table+` WHERE id = $1`, h.id).Scan(&revision, &n); err != nil {
			t.Fatal(err)
		}
		if revision != h.revision || n != h.n {
			t.Fatalf("head %s at %s %d; want %s %d", h.id, revision, n, h.revision, h.n)
		}
	}

	var desired string
	var msRev int
	if err := p.db.QueryRow(`SELECT desired, revision FROM machine_state WHERE machine = $1`, p.machine).Scan(&desired, &msRev); err != nil {
		t.Fatal(err)
	}
	var state, release string
	if err := p.db.QueryRow(`SELECT state, release, revision FROM draft WHERE id = $1`, p.draft).Scan(&state, &release, &rev); err != nil {
		t.Fatal(err)
	}
	if desired != rel || msRev != 2 || state != "published" || release != rel || rev != 2 {
		t.Fatalf("desired %s (revision %d), draft %s %s %d", desired, msRev, state, release, rev)
	}

	// O1: the operation succeeded naming the release, its owner cleared, with its terminal event.
	var result, entry string
	var owner sql.NullString
	var last int
	if err := p.db.QueryRow(`SELECT state, result::text, owner, last_event FROM operation WHERE id = $1`, p.job.op).
		Scan(&state, &result, &owner, &last); err != nil {
		t.Fatal(err)
	}
	if err := p.db.QueryRow(`SELECT entry::text FROM operation_event WHERE operation = $1 AND number = 1 AND kind = 'publish'`,
		p.job.op).Scan(&entry); err != nil {
		t.Fatal(err)
	}
	var res, ev map[string]any
	if json.Unmarshal([]byte(result), &res) != nil || json.Unmarshal([]byte(entry), &ev) != nil || state != "succeeded" ||
		owner.Valid || last != 1 || len(res) != 1 || res["release"] != rel || len(ev) != 2 || ev["type"] != "succeeded" || ev["release"] != rel {
		t.Fatalf("operation %s %s %v %d, event %s", state, result, owner, last, entry)
	}

	// Dependency monitor §5.2: each named version seeded retained, first seen and observed from the
	// time publication began classifying it.
	for _, s := range []struct{ provider, object, created string }{{"kv", p.kvPath, "2026-09-26T09:00:00.123456789Z"},
		{"transit", "bw-artifact", "2026-09-26T08:00:00Z"}} {
		var class string
		var reason sql.NullString
		var first, from time.Time
		var recorded sql.NullTime
		if err := p.db.QueryRow(`SELECT class, reason, first_retained_at, observed_from, recorded_at FROM dependency_status
			WHERE provider = $1 AND object = $2 AND version = 1 AND created = $3`, s.provider, s.object, s.created).
			Scan(&class, &reason, &first, &from, &recorded); err != nil {
			t.Fatalf("status %s: %v", s.object, err)
		}
		if class != "retained" || reason.Valid || !first.Equal(began) || !from.Equal(began) || !recorded.Valid {
			t.Fatalf("status %s: %s %v %v %v %v", s.object, class, reason, first, from, recorded)
		}
	}
	var n int
	if err := p.db.QueryRow(`SELECT count(*) FROM dependency WHERE release = $1`, rel).Scan(&n); err != nil || n != 3 {
		t.Fatalf("%d dependency rows, %v; want 3", n, err)
	}
}

// count is the number of rows query returns.
func (p *publishEnv) count(query string, args ...any) int {
	p.t.Helper()
	var n int
	if err := p.db.QueryRow(query, args...).Scan(&n); err != nil {
		p.t.Fatal(err)
	}
	return n
}

// secondJob is another running publish operation of the same draft revision, as a worker that
// superseded the first one's would hold.
func (p *publishEnv) secondJob() publishJob {
	p.t.Helper()
	op := id.New(id.Operation)
	mustExec(p.t, p.db, `INSERT INTO operation (id, kind, state, owner, owner_gen, owner_epoch, lease_until, draft, draft_revision,
			created_by, created_by_kind, created_role, epoch, created_at)
		VALUES ($1, 'publish', 'running', $2, 1, $3, now() + interval '1 minute', $4, 1, $5, 'human', 'publisher', $3, now())`,
		op, p.owner.ID, p.owner.Epoch, p.draft, p.seed)
	return publishJob{op: op, draft: p.draft, cluster: p.cluster, draftRev: 1, gen: 1}
}

// A commit retried after a commit-unknown meets its own release first and returns it, writing
// nothing (§6.2).
func TestPublishCommitRetry(t *testing.T) {
	p := newPublishEnv(t)
	rel, ref := p.commit()
	if ref != nil {
		t.Fatalf("refused: %v", ref)
	}
	again, ref := p.commit()
	if ref != nil || again != rel {
		t.Fatalf("retry %s %v; want %s", again, ref, rel)
	}
	if n := p.count(`SELECT count(*) FROM release`); n != 1 {
		t.Fatalf("%d releases", n)
	}
	if n := p.count(`SELECT last_event FROM operation WHERE id = $1`, p.job.op); n != 1 {
		t.Fatalf("last event %d", n)
	}
	if n := p.count(`SELECT revision FROM draft WHERE id = $1`, p.draft); n != 2 {
		t.Fatalf("draft revision %d", n)
	}
}

// A second operation of the published draft revision, with the same content, ends succeeded with
// the existing release and its own terminal event, and writes nothing else (§6.2).
func TestPublishCommitExistingRelease(t *testing.T) {
	p := newPublishEnv(t)
	rel, ref := p.commit()
	if ref != nil {
		t.Fatalf("refused: %v", ref)
	}
	p.job = p.secondJob()
	again, ref := p.commit()
	if ref != nil || again != rel {
		t.Fatalf("second operation %s %v; want %s", again, ref, rel)
	}
	var state, result string
	if err := p.db.QueryRow(`SELECT state, result->>'release' FROM operation WHERE id = $1`, p.job.op).Scan(&state, &result); err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" || result != rel || p.count(`SELECT count(*) FROM operation_event WHERE operation = $1`, p.job.op) != 1 {
		t.Fatalf("operation %s %s", state, result)
	}
	if p.count(`SELECT count(*) FROM release`) != 1 || p.count(`SELECT revision FROM draft WHERE id = $1`, p.draft) != 2 ||
		p.count(`SELECT revision FROM machine_state WHERE machine = $1`, p.machine) != 2 {
		t.Fatal("the second commit wrote more than its operation")
	}
}

// Another content for a published draft revision is refused 409 conflict (§6.2).
func TestPublishCommitDifferentContent(t *testing.T) {
	p := newPublishEnv(t)
	if _, ref := p.commit(); ref != nil {
		t.Fatalf("refused: %v", ref)
	}
	p.job = p.secondJob()
	p.unit.renderer.KubernetesVersion = "v1.36.1"
	if _, ref := p.commit(); ref == nil || ref.status != 409 || ref.code != "conflict" {
		t.Fatalf("refusal %v; want 409 conflict", ref)
	}
	if p.count(`SELECT count(*) FROM release`) != 1 {
		t.Fatal("a second release was written")
	}
}
