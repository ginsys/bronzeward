package api

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/compile"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/staging"
	"github.com/jackc/pgx/v5/pgconn"
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

	held *sql.Tx // when set, the next commit runs while this transaction holds a row it needs
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

	// A revision's rows are written in its own transaction (refuse_late_revision_row).
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
			provenance: []compile.Record{{Reference: "registry/pass", Version: 1, Kind: "string", Member: -1,
				Source:     compile.Origin{Base: true, Fragment: -1, Revision: p.ibr, Digest: textDigest("machine: {}")},
				SourcePath: "doc[0]/machine/registries", Output: "doc[0]/machine/registries"}},
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
	var rel string
	var ref *refusal
	var err error
	if held := p.held; held != nil {
		p.held = nil
		var holder int
		if err := held.QueryRow(`SELECT pg_backend_pid()`).Scan(&holder); err != nil {
			p.t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			rel, ref, err = p.a.publishCommit(p.t.Context(), p.job, p.unit)
		}()
		waitBlockedBy(p.t, p.db, holder)
		if err := held.Commit(); err != nil {
			p.t.Fatal(err)
		}
		<-done
	} else {
		rel, ref, err = p.a.publishCommit(p.t.Context(), p.job, p.unit)
	}
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
	for name, change := range map[string]func(*releaseUnit){
		"renderer":      func(u *releaseUnit) { u.renderer.KubernetesVersion = "v1.36.1" },
		"configuration": func(u *releaseUnit) { u.machines[0].configuration = sha256.Sum256([]byte("machine: {x: 1}")) },
	} {
		t.Run(name, func(t *testing.T) {
			p := newPublishEnv(t)
			if _, ref := p.commit(); ref != nil {
				t.Fatalf("refused: %v", ref)
			}
			p.job = p.secondJob()
			change(&p.unit)
			p.refused(409, "conflict")
			if p.count(`SELECT count(*) FROM release`) != 1 {
				t.Fatal("a second release was written")
			}
		})
	}
}

// The release digest is SHA-256 over the versioned, length-prefixed encoding ruling R14 names,
// built here independently of digest; it covers every field of the content and no order.
func TestReleaseDigest(t *testing.T) {
	cfg := sha256.Sum256([]byte("machine: {}"))
	base := func() releaseContent {
		return releaseContent{cluster: "cl_a", draft: "dr_a", draftRev: 3,
			renderer: rendererBody{Contract: "c/1", MachineryVersion: "v1.13.6", MachineryChecksum: "sha256:ab",
				KubernetesVersion: "v1.36.0"},
			sources:  []contentSource{{kind: "fragment", key: "net", revision: "2"}, {kind: "assignment", key: "m_a", revision: ""}},
			machines: []contentMachine{{machine: "m_a", importBase: "ibr_a", assignment: "1", mode: "apply", configuration: cfg[:]}},
		}
	}
	var want bytes.Buffer
	put := func(s string) {
		_ = binary.Write(&want, binary.BigEndian, uint64(len(s)))
		want.WriteString(s)
	}
	num := func(n int) { _ = binary.Write(&want, binary.BigEndian, uint64(n)) }
	put("bronzeward-release-digest/1")
	put("cl_a")
	put("dr_a")
	num(3)
	for _, s := range []string{"c/1", "v1.13.6", "sha256:ab", "v1.36.0"} {
		put(s)
	}
	num(2)
	for _, s := range []string{"assignment", "m_a", "", "fragment", "net", "2"} {
		put(s)
	}
	num(1)
	for _, s := range []string{"m_a", "ibr_a", "1", "apply", string(cfg[:])} {
		put(s)
	}
	if got, w := base().digest(), sha256.Sum256(want.Bytes()); got != w {
		t.Fatalf("digest %x; want %x", got, w)
	}

	reordered := base()
	slices.Reverse(reordered.sources)
	if reordered.digest() != base().digest() {
		t.Error("source order changes the digest")
	}
	other := sha256.Sum256([]byte("machine: {x: 1}"))
	for name, change := range map[string]func(*releaseContent){
		"cluster":           func(c *releaseContent) { c.cluster = "cl_b" },
		"draft":             func(c *releaseContent) { c.draft = "dr_b" },
		"draft revision":    func(c *releaseContent) { c.draftRev = 4 },
		"contract":          func(c *releaseContent) { c.renderer.Contract = "c/2" },
		"machinery version": func(c *releaseContent) { c.renderer.MachineryVersion = "v1.13.7" },
		"machinery sum":     func(c *releaseContent) { c.renderer.MachineryChecksum = "sha256:cd" },
		"kubernetes":        func(c *releaseContent) { c.renderer.KubernetesVersion = "v1.36.1" },
		"source kind":       func(c *releaseContent) { c.sources[0].kind = "profile" },
		"source key":        func(c *releaseContent) { c.sources[0].key = "dns" },
		"source revision":   func(c *releaseContent) { c.sources[0].revision = "3" },
		"source removal":    func(c *releaseContent) { c.sources[0].revision = "" },
		"source added":      func(c *releaseContent) { c.sources = append(c.sources, contentSource{"fragment", "dns", "1"}) },
		"machine":           func(c *releaseContent) { c.machines[0].machine = "m_b" },
		"import base":       func(c *releaseContent) { c.machines[0].importBase = "ibr_b" },
		"assignment":        func(c *releaseContent) { c.machines[0].assignment = "2" },
		"mode":              func(c *releaseContent) { c.machines[0].mode = "staged" },
		"configuration":     func(c *releaseContent) { c.machines[0].configuration = other[:] },
		// Length prefixes keep a boundary shift between adjacent fields distinct.
		"boundary": func(c *releaseContent) { c.cluster, c.draft = "cl_ad", "r_a" },
	} {
		c := base()
		change(&c)
		if c.digest() == base().digest() {
			t.Errorf("%s does not change the digest", name)
		}
	}
}

// A worker whose ownership was superseded, or whose epoch is no longer current, writes nothing:
// no release, and no failure record either (§5.1, §6.2).
func TestPublishCommitFenced(t *testing.T) {
	for name, fence := range map[string]string{
		"generation": `UPDATE operation SET owner_gen = 2 WHERE id = $1`,
		"owner":      `UPDATE operation SET owner = 'run-2/1/publish' WHERE id = $1`,
		// A superseded worker that would be refused records no failure either.
		"refused": `WITH d AS (UPDATE draft SET state = 'discarded' WHERE id = (SELECT draft FROM operation WHERE id = $1))
			UPDATE operation SET owner_gen = 2 WHERE id = $1`,
		// A recovery entry moved the installation's epoch; the operation still names this owner's.
		"epoch": `WITH e AS (INSERT INTO recovery_epoch (epoch, entered_at) VALUES ('ep_` + strings.Repeat("b", 26) + `', now())
			RETURNING epoch) UPDATE installation_state SET epoch = (SELECT epoch FROM e) WHERE $1 <> ''`,
	} {
		t.Run(name, func(t *testing.T) {
			p := newPublishEnv(t)
			mustExec(t, p.db, fence, p.job.op)
			_, ref, err := p.a.publishCommit(t.Context(), p.job, p.unit)
			if !errors.Is(err, staging.ErrFenced) || ref != nil {
				t.Fatalf("%v %v; want fenced", ref, err)
			}
			var state string
			var events int
			if err := p.db.QueryRow(`SELECT state, last_event FROM operation WHERE id = $1`, p.job.op).Scan(&state, &events); err != nil {
				t.Fatal(err)
			}
			if state != "running" || events != 0 || p.count(`SELECT count(*) FROM release`) != 0 ||
				p.count(`SELECT count(*) FROM dependency_status`) != 0 {
				t.Fatalf("a fenced commit wrote: %s, %d events", state, events)
			}
		})
	}
}

// refused commits and wants the refusal: nothing of the release committed, and the operation
// failed with its problem document and terminal event (§6.2, §8.2).
func (p *publishEnv) refused(status int, code string) *refusal {
	p.t.Helper()
	statuses := p.count(`SELECT count(*) FROM dependency_status`)
	rel, ref := p.commit()
	if ref == nil || ref.status != status || ref.code != code {
		p.t.Fatalf("commit %s %v; want %d %s", rel, ref, status, code)
	}
	if n := p.count(`SELECT count(*) FROM release WHERE operation = $1`, p.job.op); n != 0 {
		p.t.Fatalf("%d releases", n)
	}
	if n := p.count(`SELECT count(*) FROM dependency_status`); n != statuses {
		p.t.Fatalf("%d statuses; want %d", n, statuses)
	}
	var state, typ, entry string
	var owner sql.NullString
	if err := p.db.QueryRow(`SELECT state, error->>'type', owner FROM operation WHERE id = $1`, p.job.op).
		Scan(&state, &typ, &owner); err != nil {
		p.t.Fatal(err)
	}
	if err := p.db.QueryRow(`SELECT entry::text FROM operation_event WHERE operation = $1 AND number = 1`, p.job.op).
		Scan(&entry); err != nil {
		p.t.Fatal(err)
	}
	var ev map[string]any
	if state != "failed" || typ != "urn:bronzeward:problem:"+code || owner.Valid || json.Unmarshal([]byte(entry), &ev) != nil ||
		len(ev) != 2 || ev["type"] != "failed" || ev["code"] != code {
		p.t.Fatalf("operation %s %s %v, event %s", state, typ, owner, entry)
	}
	return ref
}

// wantConflicts wants a stale-input refusal naming exactly these inputs, in this order.
func wantConflicts(t *testing.T, ref *refusal, want ...map[string]any) {
	t.Helper()
	got, _ := json.Marshal(ref.extra["conflicts"])
	exp, _ := json.Marshal(want)
	if string(got) != string(exp) {
		t.Fatalf("conflicts %s; want %s", got, exp)
	}
}

func TestPublishCommitDraftClosed(t *testing.T) {
	p := newPublishEnv(t)
	mustExec(t, p.db, `UPDATE draft SET state = 'discarded' WHERE id = $1`, p.draft)
	p.refused(409, "conflict")
}

func TestPublishCommitDraftMoved(t *testing.T) {
	p := newPublishEnv(t)
	mustExec(t, p.db, `UPDATE draft SET revision = 2 WHERE id = $1`, p.draft)
	p.refused(409, "conflict")
}

// A head the draft changes that moved since the draft's base (§4.2).
func TestPublishCommitChangedHeadMoved(t *testing.T) {
	p := newPublishEnv(t)
	mustExec(t, p.db, `UPDATE fragment SET head_revision = 2 WHERE id = $1`, p.network)
	wantConflicts(t, p.refused(409, "stale-input"), map[string]any{"head": p.network, "expected": 1, "actual": 2})
}

// A head the release uses unchanged that moved since the snapshot (§4.2, DB row 011).
func TestPublishCommitUnchangedHeadMoved(t *testing.T) {
	p := newPublishEnv(t)
	mustExec(t, p.db, `UPDATE fragment SET head_revision = 2 WHERE id = $1`, p.base)
	wantConflicts(t, p.refused(409, "stale-input"), map[string]any{"head": p.base, "expected": 1, "actual": 2})
}

// A name the draft introduces that another publication introduced first (§4.2).
func TestPublishCommitIntroducedNameExists(t *testing.T) {
	p := newPublishEnv(t)
	other := p.fragmentHead("storage", "role", p.fragmentRevision(p.cluster, "storage", "role"), 1)
	wantConflicts(t, p.refused(409, "stale-input"), map[string]any{"head": other, "expected": "absent", "actual": 1})
}

// hold runs stmt in a transaction left open, so that the next commit meets the row it wrote.
func (p *publishEnv) hold(stmt string, args ...any) {
	p.t.Helper()
	tx, err := p.db.Begin()
	if err != nil {
		p.t.Fatal(err)
	}
	p.t.Cleanup(func() { _ = tx.Rollback() })
	mustExec(p.t, tx, stmt, args...)
	p.held = tx
}

// DB rows 010 and 011 (§4.2, §6.2): another publication moves a head the release uses unchanged
// and commits while the commit waits on its FOR SHARE read; the commit then finds the move.
func TestPublishCommitUnchangedHeadMovedConcurrently(t *testing.T) {
	p := newPublishEnv(t)
	p.hold(`UPDATE fragment SET head_revision = 2 WHERE id = $1`, p.base)
	wantConflicts(t, p.refused(409, "stale-input"), map[string]any{"head": p.base, "expected": 1, "actual": 2})
}

// Two publications introducing one name meet at the unique index (§6.2): the commit waits for the
// other's insert and, once it commits, fails naming the other's head.
func TestPublishCommitIntroducedNameConcurrently(t *testing.T) {
	p := newPublishEnv(t)
	other := id.New(id.Fragment)
	p.hold(`INSERT INTO fragment (id, cluster, scope, name, layer, head_revision_id, head_revision, etag_token, created_at)
		SELECT $1, cluster, 'cluster', name, layer, id, 1, 'm3oxmlfh6phr7aigshdydcb4ji', now() FROM fragment_revision WHERE id = $2`,
		other, p.fragmentRevision(p.cluster, "storage", "role"))
	wantConflicts(t, p.refused(409, "stale-input"), map[string]any{"head": other, "expected": "absent", "actual": 1})
}

// committing answers the first n COMMITs of the commit with err, rolled back, and commits the
// rest, so the failure transaction that follows commits.
func (p *publishEnv) committing(n int, err error) *int {
	calls := new(int)
	p.a = p.buildWith(deps{owner: p.owner}, options{commit: func(tx *sql.Tx) error {
		if *calls++; *calls <= n {
			_ = tx.Rollback()
			return err
		}
		return tx.Commit()
	}})
	return calls
}

// §5 rule 5: a deadlock is retried whole, at most three times; then the operation fails 503
// transient-conflict.
func TestPublishCommitDeadlocked(t *testing.T) {
	p := newPublishEnv(t)
	calls := p.committing(maxAttempts, &pgconn.PgError{Code: "40P01", Message: "deadlock detected (test)"})
	p.refused(503, "transient-conflict")
	if *calls != maxAttempts+1 {
		t.Fatalf("%d COMMITs; want %d attempts and the failure", *calls, maxAttempts)
	}
}

// A COMMIT the server rejected, such as a deferred trigger's, rolled the release back: the
// operation fails 500 internal-error. A lost reply is its control: the outcome is unknown, so the
// operation stays running for a retry, which reads the natural key first (§5 rule 6).
func TestPublishCommitRejected(t *testing.T) {
	p := newPublishEnv(t)
	first := true
	p.a = p.buildWith(deps{owner: p.owner}, options{commit: func(tx *sql.Tx) error {
		if first {
			// A source naming a head that does not exist fails its deferred foreign key at COMMIT.
			first = false
			mustExec(t, tx, `INSERT INTO release_source (release, cluster, kind, profile, name, head_revision)
				SELECT id, cluster, 'profile', 'prf_`+strings.Repeat("a", 26)+`', 'ghost', 1 FROM release WHERE operation = $1`, p.job.op)
		}
		return tx.Commit()
	}})
	p.refused(500, "internal-error")

	p = newPublishEnv(t)
	p.committing(1, &pgconn.PgError{Code: "08006", Message: "connection lost at COMMIT (test)"})
	if _, ref, err := p.a.publishCommit(t.Context(), p.job, p.unit); err == nil || ref != nil {
		t.Fatalf("%v %v; want the error", ref, err)
	}
	var state string
	if err := p.db.QueryRow(`SELECT state FROM operation WHERE id = $1`, p.job.op).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "running" {
		t.Fatalf("operation %s after a lost reply", state)
	}
	if _, ref := p.commit(); ref != nil {
		t.Fatalf("the retry: %v", ref)
	}
}

// A unit that breaks a constraint checked at its statement, here a release machine in a mode other
// than its machine's platform (§3.3, choice §17.32), rolls back: the operation fails 500
// internal-error with its terminal event rather than staying running. The other tests, whose unit
// is in the machine's platform, are its control.
func TestPublishCommitConstraintViolated(t *testing.T) {
	p := newPublishEnv(t)
	p.unit.machines[0].mode = "metal"
	p.refused(500, "internal-error")
}

// A recovery entry between the rolled-back commit and the failure transaction supersedes the
// owner's epoch: the failure is not recorded either (§5.1, an owner's own transition).
func TestPublishCommitFailureFencedOnEpoch(t *testing.T) {
	p := newPublishEnv(t)
	first := true
	p.a = p.buildWith(deps{owner: p.owner}, options{commit: func(tx *sql.Tx) error {
		if !first {
			return tx.Commit()
		}
		first = false
		_ = tx.Rollback()
		mustExec(t, p.db, `WITH e AS (INSERT INTO recovery_epoch (epoch, entered_at) VALUES ('ep_`+strings.Repeat("b", 26)+`', now())
			RETURNING epoch) UPDATE installation_state SET epoch = (SELECT epoch FROM e)`)
		return &pgconn.PgError{Code: "23514", Message: "deferred check violated at COMMIT (test)"}
	}})
	if _, ref, err := p.a.publishCommit(t.Context(), p.job, p.unit); !errors.Is(err, staging.ErrFenced) || ref != nil {
		t.Fatalf("%v %v; want fenced", ref, err)
	}
	var state string
	var events int
	if err := p.db.QueryRow(`SELECT state, last_event FROM operation WHERE id = $1`, p.job.op).Scan(&state, &events); err != nil {
		t.Fatal(err)
	}
	if state != "running" || events != 0 {
		t.Fatalf("operation %s, %d events after the epoch moved", state, events)
	}
}

// A COMMIT PostgreSQL answers with ROLLBACK, after a statement whose error went unseen, is a
// known rollback too: the operation fails 500 internal-error.
func TestPublishCommitAnsweredRollback(t *testing.T) {
	p := newPublishEnv(t)
	first := true
	p.a = p.buildWith(deps{owner: p.owner}, options{commit: func(tx *sql.Tx) error {
		if first {
			first = false
			_, _ = tx.Exec(`SELECT 1/0`)
		}
		return tx.Commit()
	}})
	p.refused(500, "internal-error")
}

// A covered machine whose import base the draft does not carry and which has no Applied release
// has none (§3.2).
func TestPublishCommitNoImportBase(t *testing.T) {
	p := newPublishEnv(t)
	mustExec(t, p.db, `DELETE FROM draft_entry WHERE draft = $1`, p.draft)
	wantConflicts(t, p.refused(409, "stale-input"), map[string]any{"machine": p.machine, "expected": p.ibr, "actual": "absent"})
}

// nextDraft publishes the fixture's draft, records its release Applied, and binds a second draft
// that changes nothing, compiled on the Applied release's import base.
func (p *publishEnv) nextDraft() {
	p.t.Helper()
	rel, ref := p.commit()
	if ref != nil {
		p.t.Fatalf("refused: %v", ref)
	}
	mustExec(p.t, p.db, `UPDATE machine_state SET applied_release = $2, applied_digest = $3, applied_source = 'operation',
		baseline_revision = 1 WHERE machine = $1`, p.machine, rel, make([]byte, 32))
	var storageHead, asg string
	if err := p.db.QueryRow(`SELECT id FROM fragment WHERE cluster = $1 AND name = 'storage'`, p.cluster).Scan(&storageHead); err != nil {
		p.t.Fatal(err)
	}
	if err := p.db.QueryRow(`SELECT id FROM assignment WHERE machine = $1`, p.machine).Scan(&asg); err != nil {
		p.t.Fatal(err)
	}
	d2 := id.New(id.Draft)
	mustExec(p.t, p.db, `INSERT INTO draft (id, cluster, title, state, revision, etag_token, created_at)
		VALUES ($1, $2, 'second', 'open', 1, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, d2, p.cluster)
	op := id.New(id.Operation)
	mustExec(p.t, p.db, `INSERT INTO operation (id, kind, state, owner, owner_gen, owner_epoch, lease_until, draft, draft_revision,
			created_by, created_by_kind, created_role, epoch, created_at)
		VALUES ($1, 'publish', 'running', $2, 1, $3, now() + interval '1 minute', $4, 1, $5, 'human', 'publisher', $3, now())`,
		op, p.owner.ID, p.owner.Epoch, d2, p.seed)
	p.job = publishJob{op: op, draft: d2, cluster: p.cluster, draftRev: 1, gen: 1}
	p.unit.unchanged = []usedHead{{kind: "fragment", head: p.base, revision: p.baseRev, headRevision: 1},
		{kind: "profile", head: p.prf, revision: p.prv, headRevision: 1},
		{kind: "fragment", head: p.network, revision: p.networkNew, headRevision: 2},
		{kind: "fragment", head: storageHead, revision: p.storage, headRevision: 1},
		{kind: "assignment", head: asg, revision: p.asr, headRevision: 1}}
}

// A draft without an import base entry compiles on its machine's Applied release's (§3.2).
func TestPublishCommitAppliedImportBase(t *testing.T) {
	p := newPublishEnv(t)
	p.nextDraft()
	if rel, ref := p.commit(); ref != nil || rel == "" {
		t.Fatalf("commit %s %v", rel, ref)
	}
}

// An adoption that changed the machine's Applied import base after the snapshot (§3.2, §4.2).
func TestPublishCommitAppliedImportBaseChanged(t *testing.T) {
	p := newPublishEnv(t)
	p.nextDraft()
	other := id.New(id.ImportBase)
	mustExec(t, p.db, `INSERT INTO import_base_revision (id, machine, document, baseline_ciphertext, baseline_digest,
		baseline_digest_key, configuration_digest, created_at) VALUES ($1, $2, 'machine: {}', '\x01', $3, 'transit/baseline-digest:1', $3, now())`,
		other, p.machine, make([]byte, 32))
	p.unit.machines[0].importBase = other
	p.unit.machines[0].reproduction[0].source = other
	wantConflicts(t, p.refused(409, "stale-input"), map[string]any{"machine": p.machine, "expected": other, "actual": p.ibr})
}

// In recovery mode, an assignment change on a scope not released is refused, naming the scope
// (§6.2, §12.2); a released scope publishes.
func TestPublishCommitRecoveryMode(t *testing.T) {
	p := newPublishEnv(t)
	mustExec(t, p.db, `UPDATE installation_state SET recovery_mode = true`)
	if ref := p.refused(409, "recovery-mode-active"); ref.extra["scope"] != p.machine {
		t.Fatalf("scope %v", ref.extra["scope"])
	}

	p = newPublishEnv(t)
	mustExec(t, p.db, `UPDATE installation_state SET recovery_mode = true`)
	mustExec(t, p.db, `UPDATE machine SET scope_state = 'released' WHERE id = $1`, p.machine)
	if _, ref := p.commit(); ref != nil {
		t.Fatalf("released scope refused: %v", ref)
	}
}

// A status of a named version recorded other than retained after publication began classifying
// it refuses the publication (dependency monitor §5.2); one recorded before it does not.
func TestPublishCommitStatusRecordedAfter(t *testing.T) {
	seed := func(p *publishEnv, observed, recorded time.Time) {
		mustExec(p.t, p.db, `INSERT INTO dependency_status (id, provider, object, version, created, class, reason,
			observed_from, recorded_at) VALUES ($1, 'kv', $2, 1, '2026-09-26T09:00:00.123456789Z', 'blocked', 'soft-deleted', $3, $4)`,
			id.New(id.Dependency), p.kvPath, observed, recorded)
	}
	p := newPublishEnv(t)
	seed(p, began.Add(time.Second), began.Add(time.Second))
	if ref := p.refused(422, "validation-failed"); ref.extra["dependency"] == nil {
		t.Fatalf("refusal names no dependency: %v", ref.extra)
	}

	// A request that began before publication's but was recorded after it began refuses too: the
	// comparison takes the time the class was recorded.
	p = newPublishEnv(t)
	seed(p, began.Add(-time.Second), began.Add(time.Second))
	p.refused(422, "validation-failed")

	p = newPublishEnv(t)
	seed(p, began.Add(-time.Second), began.Add(-time.Second))
	if _, ref := p.commit(); ref != nil {
		t.Fatalf("an earlier transition refused: %v", ref)
	}
}

// The re-check holds every named version's status FOR SHARE until the commit (dependency monitor
// §5.2): a monitor transition starting after it waits for the release, then reads it as a
// referencing release, so the two are ordered. The probe takes the lock a transition's UPDATE
// takes, NO KEY UPDATE, which a foreign key's KEY SHARE alone does not block.
func TestPublishCommitHoldsStatuses(t *testing.T) {
	p := newPublishEnv(t)
	statuses := [][3]any{{"kv", p.kvPath, createdText(kvCreated)}, {"transit", "bw-artifact", createdText(transitCreated)}}
	for _, s := range statuses {
		mustExec(t, p.db, `INSERT INTO dependency_status (id, provider, object, version, created, class, first_retained_at,
			observed_from, recorded_at) VALUES ($1, $2, $3, 1, $4, 'retained', $5, $5, $5)`,
			id.New(id.Dependency), s[0], s[1], s[2], began.Add(-time.Hour))
	}
	first := true
	p.a = p.buildWith(deps{owner: p.owner}, options{commit: func(tx *sql.Tx) error {
		if first {
			first = false
			for _, s := range statuses {
				_, err := p.db.Exec(`SELECT 1 FROM dependency_status WHERE provider = $1 AND object = $2 AND created = $3
					FOR NO KEY UPDATE NOWAIT`, s[0], s[1], s[2])
				var pe *pgconn.PgError
				if !errors.As(err, &pe) || pe.Code != "55P03" {
					t.Errorf("%s status not held at COMMIT: %v", s[0], err)
				}
			}
		}
		return tx.Commit()
	}})
	if _, ref := p.commit(); ref != nil {
		t.Fatalf("refused: %v", ref)
	}
}

// A monitor transition of a version the release names, committed while the commit waits on that
// row, is read by the re-check and refuses the publication (dependency monitor §5.2).
func TestPublishCommitStatusTransitionConcurrently(t *testing.T) {
	p := newPublishEnv(t)
	mustExec(t, p.db, `INSERT INTO dependency_status (id, provider, object, version, created, class, first_retained_at,
		observed_from, recorded_at) VALUES ($1, 'kv', $2, 1, '2026-09-26T09:00:00.123456789Z', 'retained', $3, $3, $3)`,
		id.New(id.Dependency), p.kvPath, began.Add(-time.Hour))
	p.hold(`UPDATE dependency_status SET class = 'blocked', reason = 'soft-deleted', observed_from = clock_timestamp(),
		recorded_at = clock_timestamp() WHERE provider = 'kv' AND object = $1`, p.kvPath)
	p.refused(422, "validation-failed")
}
