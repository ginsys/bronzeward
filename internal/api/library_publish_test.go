package api

import (
	"crypto/sha256"
	"database/sql"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/compile"
	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
)

// The publication of a draft's shared (library) entries (persistence-api.md §3.1.1, §6.2). No
// route writes one yet, so each case seeds its draft's entries.

// share sets the draft's shared entry for a name: revision (nil: a removal of the shared head),
// from the head revision base and the adoption counter adoptionBase (nil: absent).
func (p *publishEnv) share(kind, name string, revision, base, adoptionBase any) {
	p.t.Helper()
	mustExec(p.t, p.db, `DELETE FROM draft_source_entry WHERE draft = $1 AND kind = $2 AND name = $3 AND scope = 'library'`,
		p.draft, kind, name)
	mustExec(p.t, p.db, `INSERT INTO draft_source_entry (draft, cluster, kind, name, scope, `+kind+`_revision, base, adoption_base)
		VALUES ($1, $2, $3, $4, 'library', $5, $6, $7)`, p.draft, p.cluster, kind, name, revision, base, adoptionBase)
}

// sharedProfileRevision is a shared profile revision of name pinning the shared revisions pins.
func (p *publishEnv) sharedProfileRevision(name string, pins ...string) string {
	p.t.Helper()
	prv := id.New(id.ProfileRevision)
	tx, err := p.db.Begin()
	if err != nil {
		p.t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(p.t, tx, `INSERT INTO profile_revision (id, cluster, name, author, created_at) VALUES ($1, NULL, $2, $3, now())`,
		prv, name, p.seed)
	for i, frv := range pins {
		mustExec(p.t, tx, `INSERT INTO profile_revision_fragment (revision, cluster, position, fragment_revision) VALUES ($1, NULL, $2, $3)`,
			prv, i, frv)
	}
	if err := tx.Commit(); err != nil {
		p.t.Fatal(err)
	}
	return prv
}

// adoptedSharedProfile is a published shared profile of name pinning pins, adopted by the cluster
// at counter 3.
func (p *publishEnv) adoptedSharedProfile(name string, pins ...string) (head, prv string) {
	p.t.Helper()
	prv, head = p.sharedProfileRevision(name, pins...), id.New(id.Profile)
	mustExec(p.t, p.db, `INSERT INTO profile (id, cluster, scope, name, head_revision_id, head_revision, etag_token, created_at)
		VALUES ($1, NULL, 'library', $2, $3, 1, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, head, name, prv)
	p.adopt("profile", name, head, prv, 3)
	return head, prv
}

// sharedRow is a shared source as a case wants it: counter 0 for a pin-only row.
func sharedRow(revision, previous string, counter int64) sourceRow {
	return sourceRow{scope: "library", revision: sql.NullString{String: revision, Valid: revision != ""},
		previous: sql.NullString{String: previous, Valid: previous != ""},
		adoption: sql.NullInt64{Int64: counter, Valid: counter != 0}}
}

// sourcesOf are the release's source rows of one name, by revision.
func (p *publishEnv) sourcesOf(rel, kind, name string) []sourceRow {
	p.t.Helper()
	rows, err := p.db.Query(`SELECT scope, `+kind+`_revision, previous_`+kind+`_revision, head_revision, adoption_revision
		FROM release_source WHERE release = $1 AND kind = $2 AND name = $3`, rel, kind, name)
	if err != nil {
		p.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []sourceRow
	for rows.Next() {
		var r sourceRow
		if err := rows.Scan(&r.scope, &r.revision, &r.previous, &r.headRevision, &r.adoption); err != nil {
			p.t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		p.t.Fatal(err)
	}
	slices.SortFunc(out, func(x, y sourceRow) int { return strings.Compare(x.revision.String, y.revision.String) })
	return out
}

func (p *publishEnv) wantSources(rel, kind, name string, want ...sourceRow) {
	p.t.Helper()
	slices.SortFunc(want, func(x, y sourceRow) int { return strings.Compare(x.revision.String, y.revision.String) })
	if got := p.sourcesOf(rel, kind, name); !slices.Equal(got, want) {
		p.t.Fatalf("%s %s sources %+v; want %+v", kind, name, got, want)
	}
}

func (p *publishEnv) wantDigest(rel string) {
	p.t.Helper()
	stored, err := releaseDigest(p.t.Context(), p.db, rel)
	if err != nil {
		p.t.Fatal(err)
	}
	if p.count(`SELECT count(*) FROM release WHERE id = $1 AND digest = $2`, rel, stored[:]) != 1 {
		p.t.Fatal("the stored rows give another digest than the release's")
	}
}

// wantHead wants the shared head of name at revision ("" removed) and counter, and returns it.
func (p *publishEnv) wantHead(kind, name, revision string, counter int) string {
	p.t.Helper()
	var head string
	if err := p.db.QueryRow(`SELECT id FROM `+kind+` WHERE scope = 'library' AND name = $1
		AND coalesce(head_revision_id, '') = $2 AND head_revision = $3`, name, revision, counter).Scan(&head); err != nil {
		p.t.Fatalf("shared %s %s is not at %q, revision %d: %v", kind, name, revision, counter, err)
	}
	return head
}

// wantAdoption wants the cluster's adoption of name at revision ("" none) and counter.
func (p *publishEnv) wantAdoption(cluster, kind, name, revision string, counter int) {
	p.t.Helper()
	if p.count(`SELECT count(*) FROM library_adoption WHERE cluster = $1 AND kind = $2 AND name = $3
		AND coalesce(fragment_revision, profile_revision, '') = $4 AND adoption_revision = $5`,
		cluster, kind, name, revision, counter) != 1 {
		p.t.Fatalf("the adoption of %s %s by %s is not at %q, counter %d", kind, name, cluster, revision, counter)
	}
}

// publishedEntries takes the snapshot and commits; the release must be accepted.
func (p *publishEnv) publishedEntries() string {
	p.t.Helper()
	p.compiled()
	rel := p.published()
	p.wantDigest(rel)
	return rel
}

// PA §3.1.1 table, the library edit rows: the shared head advances, or is inserted, and the
// draft's cluster alone adopts the revision written; the release records the adoption.
func TestLibraryPublishEdit(t *testing.T) {
	t.Run("head present", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.adopt("fragment", "ntp", f.head, f.older, 4)
		other := p.createCluster(p.api, p.human("h-author"), "k-cluster-other-012345")
		mustExec(t, p.db, `INSERT INTO library_adoption (cluster, kind, name, fragment, fragment_revision, adoption_revision, etag_token)
			VALUES ($1, 'fragment', 'ntp', $2, $3, 7, 'm3oxmlfh6phr7aigshdydcb4ji')`, other, f.head, f.older)
		next := p.sharedRevision("ntp")
		p.share("fragment", "ntp", next, 2, 4)
		p.selectToo(nil, "cluster/ntp")
		s := p.compiled()
		if got := s.machines[0].fragments; !slices.Contains(got, next) || slices.Contains(got, f.older) {
			t.Fatalf("fragments %v; want the proposed %s", got, next)
		}
		rel := p.published()
		p.wantDigest(rel)
		p.wantHead("fragment", "ntp", next, 3)
		p.wantAdoption(p.cluster, "fragment", "ntp", next, 5)
		p.wantAdoption(other, "fragment", "ntp", f.older, 7)
		p.wantSources(rel, "fragment", "ntp", sharedRow(next, f.older, 5))
	})
	t.Run("the draft introduces the name", func(t *testing.T) {
		p := newPublishEnv(t)
		frv := p.sharedRevision("ntp")
		p.share("fragment", "ntp", frv, nil, nil)
		p.selectToo(nil, "cluster/ntp")
		rel := p.publishedEntries()
		p.wantHead("fragment", "ntp", frv, 1)
		p.wantAdoption(p.cluster, "fragment", "ntp", frv, 1)
		p.wantSources(rel, "fragment", "ntp", sharedRow(frv, "", 1))
	})
	t.Run("a removed head and adoption are brought back", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		mustExec(t, p.db, `UPDATE fragment SET head_revision_id = NULL, head_revision = 3 WHERE id = $1`, f.head)
		p.adopt("fragment", "ntp", f.head, nil, 2)
		next := p.sharedRevision("ntp")
		p.share("fragment", "ntp", next, 3, 2)
		rel := p.publishedEntries()
		p.wantHead("fragment", "ntp", next, 4)
		p.wantAdoption(p.cluster, "fragment", "ntp", next, 3)
		p.wantSources(rel, "fragment", "ntp", sharedRow(next, "", 3))
	})
	for name, c := range map[string]struct {
		base, adoptionBase any
		want               func(head string) []map[string]any
	}{
		"the head moved": {1, 4, func(head string) []map[string]any {
			return []map[string]any{{"head": head, "expected": 1, "actual": 2}}
		}},
		"the adoption moved": {2, 3, func(head string) []map[string]any {
			return []map[string]any{{"head": head, "scope": "adoption", "expected": 3, "actual": 4}}
		}},
		"an adoption appeared": {2, nil, func(head string) []map[string]any {
			return []map[string]any{{"head": head, "scope": "adoption", "expected": "absent", "actual": 4}}
		}},
		"the head appeared": {nil, nil, func(head string) []map[string]any {
			return []map[string]any{{"head": head, "expected": "absent", "actual": 2},
				{"head": head, "scope": "adoption", "expected": "absent", "actual": 4}}
		}},
	} {
		t.Run("stale: "+name, func(t *testing.T) {
			p := newPublishEnv(t)
			f := p.sharedFragment("ntp")
			p.adopt("fragment", "ntp", f.head, f.older, 4)
			p.share("fragment", "ntp", p.sharedRevision("ntp"), c.base, c.adoptionBase)
			p.compiled()
			wantConflicts(t, p.refused(409, "stale-input"), c.want(f.head)...)
		})
	}
}

// PA §3.1.1: a shared head's removal moves the head alone. Adoptions keep compiling, and the
// release records the adoption it uses, never the removal.
func TestLibraryPublishRemoval(t *testing.T) {
	p := newPublishEnv(t)
	f := p.sharedFragment("ntp")
	p.adopt("fragment", "ntp", f.head, f.older, 4)
	p.share("fragment", "ntp", nil, 2, nil)
	p.selectToo(nil, "cluster/ntp")
	rel := p.publishedEntries()
	p.wantHead("fragment", "ntp", "", 3)
	p.wantAdoption(p.cluster, "fragment", "ntp", f.older, 4)
	p.wantSources(rel, "fragment", "ntp", adoptedRow(f.older, 4))

	t.Run("unused: no source", func(t *testing.T) {
		p := newPublishEnv(t)
		p.sharedFragment("ntp")
		p.share("fragment", "ntp", nil, 2, nil)
		rel := p.publishedEntries()
		p.wantHead("fragment", "ntp", "", 3)
		p.wantSources(rel, "fragment", "ntp")
	})
	t.Run("stale", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.share("fragment", "ntp", nil, 1, nil)
		p.compiled()
		wantConflicts(t, p.refused(409, "stale-input"), map[string]any{"head": f.head, "expected": 1, "actual": 2})
	})
}

// PA §3.1: a removal of a name the draft introduces, at either scope, publishes nothing.
func TestLibraryPublishRemovalOfAnIntroducedName(t *testing.T) {
	for _, scope := range []string{"cluster", "library"} {
		t.Run(scope, func(t *testing.T) {
			p := newPublishEnv(t)
			if scope == "cluster" {
				p.entry("fragment", "extra", nil)
			} else {
				p.share("fragment", "extra", nil, nil, nil)
			}
			rel := p.publishedEntries()
			p.wantSources(rel, "fragment", "extra")
			if n := p.count(`SELECT count(*) FROM fragment WHERE name = 'extra'`); n != 0 {
				t.Fatalf("%d heads of the removed name", n)
			}
		})
	}
}

// PA §3.1.1: a shared profile's pins are the library head or proposed by the draft when it is
// published, and compile as written once adopted.
func TestLibraryProfilePins(t *testing.T) {
	t.Run("a draft's profile pins the head", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		prv := p.sharedProfileRevision("shared-set", f.newer)
		p.share("profile", "shared-set", prv, nil, nil)
		p.selectToo([]string{"shared-set"})
		s := p.compiled()
		if got := s.machines[0].fragments; !slices.Contains(got, f.newer) {
			t.Fatalf("fragments %v; want the pinned %s", got, f.newer)
		}
		rel := p.published()
		p.wantDigest(rel)
		p.wantAdoption(p.cluster, "profile", "shared-set", prv, 1)
		p.wantSources(rel, "profile", "shared-set", sharedRow(prv, "", 1))
		p.wantSources(rel, "fragment", "ntp", sharedRow(f.newer, "", 0))
		if n := p.count(`SELECT count(*) FROM library_adoption WHERE kind = 'fragment'`); n != 0 {
			t.Fatal("the publication adopted the pinned fragment")
		}
	})
	t.Run("a draft's profile pins the draft's proposal", func(t *testing.T) {
		p := newPublishEnv(t)
		next := p.sharedRevision("ntp")
		p.share("fragment", "ntp", next, nil, nil)
		p.share("profile", "shared-set", p.sharedProfileRevision("shared-set", next), nil, nil)
		p.selectToo([]string{"shared-set"})
		rel := p.publishedEntries()
		p.wantSources(rel, "fragment", "ntp", sharedRow(next, "", 1))
	})
	t.Run("a draft's profile pins neither", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.share("profile", "shared-set", p.sharedProfileRevision("shared-set", f.older), nil, nil)
		_, ref := p.snapshot()
		if ref == nil || ref.status != http.StatusUnprocessableEntity || ref.extra["profile"] != "shared-set" ||
			ref.extra["path"] != "fragments[0]" || ref.extra["revision"] != f.older {
			t.Fatalf("snapshot %v; want 422 naming the pin %s", ref, f.older)
		}
	})
	t.Run("the pinned head moves before the commit", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.share("profile", "shared-set", p.sharedProfileRevision("shared-set", f.newer), nil, nil)
		p.compiled()
		p.hold(`UPDATE fragment SET head_revision_id = $2, head_revision = 3 WHERE id = $1`, f.head, f.older)
		ref := p.refused(422, "validation-failed")
		if ref.extra["profile"] != "shared-set" || ref.extra["revision"] != f.newer {
			t.Fatalf("refusal %v %v; want the pin %s", ref, ref.extra, f.newer)
		}
	})
}

// G1: a release composes one revision of a fragment name.
func TestLibraryPinConflict(t *testing.T) {
	for name, seed := range map[string]func(*publishEnv, sharedFragment){
		"beside an adoption": func(p *publishEnv, f sharedFragment) {
			p.adopt("fragment", "ntp", f.head, f.newer, 1)
			p.adoptedSharedProfile("shared-set", f.older)
			p.selectToo([]string{"shared-set"}, "cluster/ntp")
		},
		"beside a cluster fragment": func(p *publishEnv, f sharedFragment) {
			p.fragmentHead("ntp", "cluster", p.fragmentRevision(p.cluster, "ntp", "cluster"), 1)
			p.adoptedSharedProfile("shared-set", f.older)
			p.selectToo([]string{"shared-set"}, "cluster/ntp")
		},
		"between two shared profiles": func(p *publishEnv, f sharedFragment) {
			p.adoptedSharedProfile("shared-set", f.older)
			p.adoptedSharedProfile("other-set", f.newer)
			p.selectToo([]string{"shared-set", "other-set"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := newPublishEnv(t)
			seed(p, p.sharedFragment("ntp"))
			_, ref := p.snapshot()
			if ref == nil || ref.status != http.StatusUnprocessableEntity || ref.extra["rule"] != "library-pin-conflict" ||
				ref.extra["fragment"] != "ntp" {
				t.Fatalf("snapshot %v; want 422 library-pin-conflict of ntp", ref)
			}
			if sources, ok := ref.extra["sources"].([]map[string]any); !ok || len(sources) != 2 ||
				sources[0]["revision"] == sources[1]["revision"] {
				t.Fatalf("sources %v; want the two", ref.extra["sources"])
			}
		})
	}
	t.Run("one revision reached twice is one source, the adoption's", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.adopt("fragment", "ntp", f.head, f.older, 2)
		p.adoptedSharedProfile("shared-set", f.older)
		p.selectToo([]string{"shared-set"}, "cluster/ntp")
		rel := p.publishedEntries()
		p.wantSources(rel, "fragment", "ntp", adoptedRow(f.older, 2))
	})
	t.Run("a profile no machine selects does not conflict", func(t *testing.T) {
		p := newPublishEnv(t)
		f := p.sharedFragment("ntp")
		p.adopt("fragment", "ntp", f.head, f.older, 2)
		p.share("profile", "shared-set", p.sharedProfileRevision("shared-set", f.newer), nil, nil)
		p.selectToo(nil, "cluster/ntp")
		rel := p.publishedEntries()
		p.wantSources(rel, "fragment", "ntp", adoptedRow(f.older, 2), sharedRow(f.newer, "", 0))
	})
}

// PA §3.1.1: a cluster holds a name at cluster scope or by adoption, never both, after the draft.
func TestLibraryNameInTwoScopes(t *testing.T) {
	t.Run("a shared edit beside the cluster's own head", func(t *testing.T) {
		p := newPublishEnv(t)
		p.fragmentHead("ntp", "cluster", p.fragmentRevision(p.cluster, "ntp", "cluster"), 1)
		p.share("fragment", "ntp", p.sharedRevision("ntp"), nil, nil)
		p.compiled()
		ref := p.refused(422, "validation-failed")
		if ref.extra["rule"] != "name-in-two-scopes" || ref.extra["name"] != "ntp" || ref.extra["kind"] != "fragment" {
			t.Fatalf("refusal %v %v; want name-in-two-scopes of fragment ntp", ref, ref.extra)
		}
		if p.count(`SELECT count(*) FROM library_adoption`) != 0 {
			t.Fatal("the refused publication adopted the name")
		}
	})
	t.Run("the draft moves the name to the library", func(t *testing.T) {
		p := newPublishEnv(t)
		p.fragmentHead("ntp", "cluster", p.fragmentRevision(p.cluster, "ntp", "cluster"), 1)
		frv := p.sharedRevision("ntp")
		p.entry("fragment", "ntp", nil)
		mustExec(t, p.db, `UPDATE draft_source_entry SET base = 1 WHERE draft = $1 AND name = 'ntp'`, p.draft)
		p.share("fragment", "ntp", frv, nil, nil)
		p.selectToo(nil, "cluster/ntp")
		if got := p.compiled().machines[0].fragments; !slices.Contains(got, frv) {
			t.Fatalf("fragments %v; want the shared %s", got, frv)
		}
		p.wantDigest(p.published())
		p.wantAdoption(p.cluster, "fragment", "ntp", frv, 1)
	})
}

// bare is a publication of a new draft of cluster that covers machine on a base of its own and
// carries no other entry; its artifact key is key, so two of them meet at no dependency status.
func bare(d *draftEnv, cluster, machine, draftKey, key string) *publishEnv {
	d.t.Helper()
	c := *d
	c.cluster, c.machine = cluster, machine
	p := &publishEnv{draftEnv: &c}
	rec := d.do(d.api, call{method: "POST", path: prefix + "/drafts", token: d.human("h-author"), key: draftKey,
		body: `{"cluster":"` + cluster + `","title":"bare"}`})
	p.draft = decode[draftBody](d.t, rec, http.StatusCreated).ID
	var epoch string
	if err := d.db.QueryRow(`SELECT epoch FROM installation_state`).Scan(&epoch); err != nil {
		d.t.Fatal(err)
	}
	p.owner.ID, p.owner.Epoch = "run-1/4242/publish", epoch
	p.a = d.buildWith(deps{owner: p.owner}, options{})
	ibr, kvPath := id.New(id.ImportBase), "gen/"+cluster+"/"+id.New(id.Ingestion)+"/pass"
	storeImportBase(d.t, d.db, ibr, machine, "string", nil, kvPath)
	mustExec(d.t, d.db, `INSERT INTO draft_entry (draft, cluster, kind, machine, import_base_revision)
		VALUES ($1, $2, 'import-base', $3, $4)`, p.draft, cluster, machine, ibr)
	op := id.New(id.Operation)
	mustExec(d.t, d.db, `INSERT INTO operation (id, kind, state, owner, owner_gen, owner_epoch, lease_until, draft, draft_revision,
			created_by, created_by_kind, created_role, epoch, created_at)
		VALUES ($1, 'publish', 'running', $2, 1, $3, now() + interval '1 minute', $4, 1, $5, 'human', 'publisher', $3, now())`,
		op, p.owner.ID, epoch, p.draft, d.seed)
	p.job = publishJob{op: op, draft: p.draft, cluster: cluster, draftRev: 1, gen: 1}
	redacted := "machine:\n  type: worker\n"
	p.unit = releaseUnit{
		renderer: rendererBody{Contract: "v1.14", MachineryVersion: "v1.14.5",
			MachineryChecksum: "h1:2rBcdYQ4m1u3oPmvbMQw3F9dZb8i0EwQnJ6y5Kx8sJ0=", KubernetesVersion: "v1.36.0"},
		machines: []unitMachine{{
			machine: machine, importBase: ibr, mode: "container",
			hostname:   compile.Hostname{Source: compile.HostnameNone},
			ciphertext: releaseCipher, configuration: sha256.Sum256([]byte("machine: {}")), redacted: &redacted,
			effective: []unitDependency{{reference: "registry/pass", object: kvPath, version: 1, created: kvCreated}},
			reproduction: []unitDependency{{reference: "registry/pass", object: kvPath, version: 1, created: kvCreated,
				source: ibr, digest: sha256.Sum256([]byte("machine: {}")), path: "doc[0]/machine/registries", occurrence: 0}},
			provenance: []compile.Record{{Reference: "registry/pass", Version: 1, Kind: "string", Member: -1,
				Source:     compile.Origin{Base: true, Fragment: -1, Revision: ibr, Digest: textDigest("machine: {}")},
				SourcePath: "doc[0]/machine/registries", Output: "doc[0]/machine/registries"}},
			encryption: unitDependency{object: key, version: 1, created: transitCreated},
		}},
		statuses: []unitStatus{
			{provider: classify.KV, object: kvPath, version: 1, began: began,
				result: classify.Result{Class: classify.Retained, Created: kvCreated}},
			{provider: classify.Transit, object: key, version: 1, began: began,
				result: classify.Result{Class: classify.Retained, Created: transitCreated, Date: began}},
		},
	}
	p.unit.machines[0].observation = dbtest.PublicationObservation(d.t, d.db, machine, op)
	return p
}

type raced struct {
	release string
	ref     *refusal
	err     error
}

// race commits first, held just before its COMMIT, then second, which must wait for it; then it
// lets first commit and answers both.
func race(t *testing.T, first, second *publishEnv) (raced, raced) {
	t.Helper()
	pid, release := make(chan int, 1), make(chan struct{})
	held := false
	first.a = first.buildWith(deps{owner: first.owner}, options{commit: func(tx *sql.Tx) error {
		if !held {
			held = true
			var n int
			if err := tx.QueryRow(`SELECT pg_backend_pid()`).Scan(&n); err != nil {
				return err
			}
			pid <- n
			<-release
		}
		return tx.Commit()
	}})
	run := func(p *publishEnv) chan raced {
		done := make(chan raced, 1)
		go func() {
			var c raced
			c.release, c.ref, c.err = p.a.publishCommit(p.t.Context(), p.job, p.unit)
			done <- c
		}()
		return done
	}
	a := run(first)
	var holder int
	select {
	case holder = <-pid:
	case c := <-a:
		t.Fatalf("the first publication ended before its COMMIT: %v %v", c.ref, c.err)
	}
	b := run(second)
	waitBlockedBy(t, first.db, holder)
	close(release)
	return <-a, <-b
}

// wantRace wants the first publication committed and the second refused.
func wantRace(t *testing.T, a, b raced, status int, code string) *refusal {
	t.Helper()
	if a.err != nil || a.ref != nil {
		t.Fatalf("first publication: %v %v", a.ref, a.err)
	}
	if b.err != nil || b.ref == nil || b.ref.status != status || b.ref.code != code {
		t.Fatalf("second publication: release %q, %v %v; want %d %s", b.release, b.ref, b.err, status, code)
	}
	return b.ref
}

// Contract 16: two publications of one cluster, one introducing the cluster's own head of a name
// and one the shared head it adopts, cannot both commit, whichever is first.
func TestLibraryConcurrentTwoScopes(t *testing.T) {
	for name, ownFirst := range map[string]bool{"the cluster entry first": true, "the shared entry first": false} {
		t.Run(name, func(t *testing.T) {
			d := newDraftEnv(t)
			own := bare(d, d.cluster, d.machine, "k-draft-own-012345678", "bw-artifact")
			own.entry("fragment", "ntp", own.fragmentRevision(own.cluster, "ntp", "cluster"))
			shared := bare(d, d.cluster, d.machine, "k-draft-shared-012345", "bw-artifact-b")
			shared.share("fragment", "ntp", shared.sharedRevision("ntp"), nil, nil)
			own.compiled()
			shared.compiled()
			first, second := own, shared
			if !ownFirst {
				first, second = shared, own
			}
			a, b := race(t, first, second)
			ref := wantRace(t, a, b, http.StatusUnprocessableEntity, "validation-failed")
			if ref.extra["rule"] != "name-in-two-scopes" {
				t.Fatalf("refusal %v; want name-in-two-scopes", ref.extra)
			}
			if n := count(t, d.db, `SELECT (SELECT count(*) FROM fragment WHERE cluster = $1 AND name = 'ntp')
				+ (SELECT count(*) FROM library_adoption WHERE cluster = $1 AND name = 'ntp')`, d.cluster); n != 1 {
				t.Fatalf("the cluster holds the name %d times", n)
			}
		})
	}
	t.Run("a compiled adoption against a restored cluster head", func(t *testing.T) {
		d := newDraftEnv(t)
		using := bare(d, d.cluster, d.machine, "k-draft-using-0123456", "bw-artifact")
		f := using.sharedFragment("ntp")
		own := using.fragmentRevision(d.cluster, "ntp", "cluster")
		using.fragmentHead("ntp", "cluster", nil, 2)
		using.adopt("fragment", "ntp", f.head, f.older, 1)
		asr := using.assignmentRevision(nil, "cluster/ntp")
		using.entry("assignment", "", asr)
		using.unit.machines[0].assignment = asr
		restoring := bare(d, d.cluster, d.machine, "k-draft-restore-01234", "bw-artifact-b")
		restoring.entry("fragment", "ntp", own)
		mustExec(t, d.db, `UPDATE draft_source_entry SET base = 2 WHERE draft = $1 AND name = 'ntp'`, restoring.draft)
		using.compiled()
		restoring.compiled()
		a, b := race(t, using, restoring)
		if ref := wantRace(t, a, b, http.StatusUnprocessableEntity, "validation-failed"); ref.extra["rule"] != "name-in-two-scopes" {
			t.Fatalf("refusal %v; want name-in-two-scopes", ref.extra)
		}
	})
}

// PA §3.1.1 table, "draft introduces the name": two clusters introducing one shared name share no
// cluster lock; they meet at the unique (owner, name) index, and the loser is stale input.
func TestLibraryConcurrentIntroduction(t *testing.T) {
	for _, kind := range []string{"fragment", "profile"} {
		t.Run(kind, func(t *testing.T) {
			d := newDraftEnv(t)
			other := d.createCluster(d.api, d.human("h-author"), "k-cluster-other-012345")
			rec := d.do(d.api, machineCall(d.human("h-author"), "k-machine-other-012345", other, "1b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b"))
			otherMachine := decode[machineBody](t, rec, http.StatusCreated).ID
			first := bare(d, d.cluster, d.machine, "k-draft-first-0123456", "bw-artifact")
			second := bare(d, other, otherMachine, "k-draft-second-012345", "bw-artifact-b")
			pin := ""
			if kind == "profile" {
				pin = first.sharedFragment("ntp").newer
			}
			for _, p := range []*publishEnv{first, second} {
				var rev string
				if kind == "fragment" {
					rev = p.sharedRevision("shared-name")
				} else {
					rev = p.sharedProfileRevision("shared-name", pin)
				}
				p.share(kind, "shared-name", rev, nil, nil)
				p.compiled()
			}
			a, b := race(t, first, second)
			ref := wantRace(t, a, b, http.StatusConflict, "stale-input")
			var head string
			if err := d.db.QueryRow(`SELECT id FROM ` + kind + ` WHERE scope = 'library' AND name = 'shared-name'`).Scan(&head); err != nil {
				t.Fatal(err)
			}
			wantConflicts(t, ref, map[string]any{"head": head, "expected": "absent", "actual": 1})
		})
	}
}
