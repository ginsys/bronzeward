package api

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// profileRevision inserts a profile revision pinning pins, in its own transaction (§3).
func (p *publishEnv) profileRevision(name string, pins ...string) string {
	p.t.Helper()
	prv := id.New(id.ProfileRevision)
	tx, err := p.db.Begin()
	if err != nil {
		p.t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(p.t, tx, `INSERT INTO profile_revision (id, cluster, name, author, created_at) VALUES ($1, $2, $3, $4, now())`,
		prv, p.cluster, name, p.seed)
	for i, frv := range pins {
		mustExec(p.t, tx, `INSERT INTO profile_revision_fragment (revision, cluster, position, fragment_revision) VALUES ($1, $2, $3, $4)`,
			prv, p.cluster, i, frv)
	}
	if err := tx.Commit(); err != nil {
		p.t.Fatal(err)
	}
	return prv
}

// assignmentRevision inserts an assignment revision of the env's machine selecting profiles and,
// as "layer/name", fragments, each layer's in the order given.
func (p *publishEnv) assignmentRevision(profiles []string, fragments ...string) string {
	p.t.Helper()
	asr := id.New(id.AssignmentRevision)
	tx, err := p.db.Begin()
	if err != nil {
		p.t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(p.t, tx, `INSERT INTO assignment_revision (id, cluster, machine, author, created_at) VALUES ($1, $2, $3, $4, now())`,
		asr, p.cluster, p.machine, p.seed)
	for i, prf := range profiles {
		mustExec(p.t, tx, `INSERT INTO assignment_revision_profile (revision, position, profile) VALUES ($1, $2, $3)`, asr, i, prf)
	}
	positions := map[string]int{}
	for _, f := range fragments {
		layer, name, _ := strings.Cut(f, "/")
		mustExec(p.t, tx, `INSERT INTO assignment_revision_fragment (revision, layer, position, fragment) VALUES ($1, $2, $3, $4)`,
			asr, layer, positions[layer], name)
		positions[layer]++
	}
	if err := tx.Commit(); err != nil {
		p.t.Fatal(err)
	}
	return asr
}

// entry sets the draft's entry for a fragment or profile name, or for the machine's assignment
// (key ""), to revision (nil: a removal).
func (p *publishEnv) entry(kind, key string, revision any) {
	p.t.Helper()
	if kind == "assignment" {
		mustExec(p.t, p.db, `DELETE FROM draft_source_entry WHERE draft = $1 AND kind = 'assignment'`, p.draft)
		mustExec(p.t, p.db, `INSERT INTO draft_source_entry (draft, cluster, kind, machine, assignment_revision)
			VALUES ($1, $2, 'assignment', $3, $4)`, p.draft, p.cluster, p.machine, revision)
		return
	}
	mustExec(p.t, p.db, `DELETE FROM draft_source_entry WHERE draft = $1 AND kind = $2 AND name = $3`, p.draft, kind, key)
	column := kind + "_revision"
	mustExec(p.t, p.db, `INSERT INTO draft_source_entry (draft, cluster, kind, name, `+column+`)
		VALUES ($1, $2, $3, $4, $5)`, p.draft, p.cluster, kind, key, revision)
}

func (p *publishEnv) snapshot() (snapshot, *refusal) {
	p.t.Helper()
	s, ref, err := p.a.readSnapshot(p.t.Context(), p.job)
	if err != nil {
		p.t.Fatalf("readSnapshot: %v", err)
	}
	return s, ref
}

func sortedHeads(h []usedHead) []usedHead {
	h = slices.Clone(h)
	slices.SortFunc(h, func(x, y usedHead) int { return strings.Compare(x.head, y.head) })
	return h
}

// The snapshot (compilation.md §6 step 1) reads, in one transaction, the draft's covered machines
// with their import base, assignment and platform mode, each machine's fragment revisions in
// composition order, every composed source's stored text and declarations, and the heads the
// release uses unchanged: exactly what T3 then commits.
func TestSnapshotComposition(t *testing.T) {
	p := newPublishEnv(t)
	s, ref := p.snapshot()
	if ref != nil {
		t.Fatalf("refused: %v", ref)
	}
	want := []snapshotMachine{{machine: p.machine, mode: "container", importBase: p.ibr, assignment: p.asr,
		fragments: []string{p.baseRev, p.networkNew, p.storage}}}
	if !reflect.DeepEqual(s.machines, want) {
		t.Fatalf("machines %+v, want %+v", s.machines, want)
	}
	if s.contract != "v1.13" {
		t.Fatalf("contract %q", s.contract)
	}
	if got, want := sortedHeads(s.unchanged), sortedHeads(p.unit.unchanged); !reflect.DeepEqual(got, want) {
		t.Fatalf("unchanged %+v, want %+v", got, want)
	}
	base, ok := s.sources[p.ibr]
	if !ok || base.document != "machine: {}" || base.generations["registry/pass"] != p.kvPath ||
		base.declarations.References["registry/pass"].Version != 1 || base.declarations.Embedded == nil {
		t.Fatalf("import base source %+v", base)
	}
	for _, f := range want[0].fragments {
		if _, ok := s.sources[f]; !ok {
			t.Fatalf("fragment revision %s not read", f)
		}
	}
	if len(s.sources) != 4 {
		t.Fatalf("%d sources read, want 4", len(s.sources))
	}

	// The snapshot binds the draft revision the operation bound.
	p.job.draftRev = 2
	if _, ref := p.snapshot(); ref == nil || ref.status != http.StatusConflict {
		t.Fatalf("moved draft: %v", ref)
	}
}

// Within a layer the profiles' pins come first, by profile then pin position, then the
// assignment's own fragments of that layer; a revision selected twice composes once, at its
// first position (ruling R23, choice §16.31).
func TestSnapshotOrder(t *testing.T) {
	p := newPublishEnv(t)
	other := p.fragmentRevision(p.cluster, "other", "site")
	p.entry("fragment", "other", other)
	extra := p.profileRevision("extra", p.networkNew)
	p.entry("profile", "extra", extra)
	asr := p.assignmentRevision([]string{"standard", "extra"}, "global/base", "site/other", "role/storage")
	p.entry("assignment", "", asr)
	s, ref := p.snapshot()
	if ref != nil {
		t.Fatalf("refused: %v", ref)
	}
	if got, want := s.machines[0].fragments, []string{p.baseRev, p.networkNew, other, p.storage}; !slices.Equal(got, want) {
		t.Fatalf("fragments %v, want %v", got, want)
	}
	if got, want := sortedHeads(s.unchanged), sortedHeads(p.unit.unchanged); !reflect.DeepEqual(got, want) {
		t.Fatalf("unchanged %+v, want %+v", got, want)
	}
}

// A machine the draft carries no import base for compiles on its Applied release's (§3.2), and a
// head the draft does not change is used at the revision the snapshot read.
func TestSnapshotAppliedBase(t *testing.T) {
	p := newPublishEnv(t)
	rel, ref := p.commit()
	if ref != nil {
		t.Fatalf("refused: %v", ref)
	}
	mustExec(t, p.db, `UPDATE machine_state SET applied_release = $2, applied_digest = $3, applied_source = 'operation',
		baseline_revision = 1 WHERE machine = $1`, p.machine, rel, make([]byte, 32))
	p.draft = id.New(id.Draft)
	mustExec(t, p.db, `INSERT INTO draft (id, cluster, title, state, revision, etag_token, created_at)
		VALUES ($1, $2, 'next', 'open', 1, 'm3oxmlfh6phr7aigshdydcb4ji', now())`, p.draft, p.cluster)
	p.job.draft = p.draft
	s, ref := p.snapshot()
	if ref != nil {
		t.Fatalf("refused: %v", ref)
	}
	var asg string
	if err := p.db.QueryRow(`SELECT id FROM assignment WHERE machine = $1`, p.machine).Scan(&asg); err != nil {
		t.Fatal(err)
	}
	if m := s.machines[0]; m.importBase != p.ibr || m.assignment != p.asr {
		t.Fatalf("machine %+v", m)
	}
	kinds := map[string]int{}
	for _, h := range s.unchanged {
		kinds[h.kind]++
		if h.head == asg && (h.revision != p.asr || h.headRevision != 1) {
			t.Fatalf("assignment head %+v", h)
		}
	}
	if kinds["assignment"] != 1 || kinds["profile"] != 1 || kinds["fragment"] != 3 {
		t.Fatalf("unchanged %+v", s.unchanged)
	}
}

// A machine is covered when it has an import base after the draft (ruling R21'): a draft that
// changes the assignment of a machine without one, or covers none, is refused 422.
func TestSnapshotCoverage(t *testing.T) {
	p := newPublishEnv(t)
	mustExec(t, p.db, `DELETE FROM draft_entry WHERE draft = $1`, p.draft)
	_, ref := p.snapshot()
	if ref == nil || ref.status != http.StatusUnprocessableEntity || ref.extra["machine"] != p.machine {
		t.Fatalf("assignment without an import base: %+v", ref)
	}
	mustExec(t, p.db, `DELETE FROM draft_source_entry WHERE draft = $1 AND kind = 'assignment'`, p.draft)
	_, ref = p.snapshot()
	if ref == nil || ref.status != http.StatusUnprocessableEntity || ref.extra["cluster"] != p.cluster {
		t.Fatalf("no covered machine: %+v", ref)
	}
}

// Publication checks §3.1's pin and selection rules again on every profile and assignment the
// release names (ruling R30), naming the profile or machine and the body path.
func TestSnapshotRecheck(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(p *publishEnv)
		key   string
		value func(p *publishEnv) string
		path  string
	}{
		{"stale pin", func(p *publishEnv) {
			mustExec(p.t, p.db, `UPDATE fragment SET head_revision_id = $2, head_revision = 2 WHERE id = $1`,
				p.base, p.fragmentRevision(p.cluster, "base", "global"))
		}, "profile", func(*publishEnv) string { return "standard" }, "fragments[0]"},
		{"removed selection", func(p *publishEnv) { p.entry("fragment", "storage", nil) },
			"machine", func(p *publishEnv) string { return p.machine }, "fragments.role[0]"},
		{"unknown profile", func(p *publishEnv) {
			p.entry("assignment", "", p.assignmentRevision([]string{"standard", "missing"}, "site/network"))
		}, "machine", func(p *publishEnv) string { return p.machine }, "profiles[1]"},
		{"wrong layer", func(p *publishEnv) {
			p.entry("assignment", "", p.assignmentRevision([]string{"standard"}, "role/network"))
		}, "machine", func(p *publishEnv) string { return p.machine }, "fragments.role[0]"},
		{"unchanged profile pinning a removed fragment", func(p *publishEnv) { p.entry("fragment", "base", nil) },
			"profile", func(*publishEnv) string { return "standard" }, "fragments[0]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPublishEnv(t)
			tc.setup(p)
			_, ref := p.snapshot()
			if ref == nil || ref.status != http.StatusUnprocessableEntity || ref.extra[tc.key] != tc.value(p) ||
				ref.extra["path"] != tc.path {
				t.Fatalf("refusal %+v", ref)
			}
		})
	}
}

// A pin's recorded identity is the created time of its version's status, the one a KV version
// holds (ruling R31); a version never classified has none.
func TestSnapshotRecorded(t *testing.T) {
	p := newPublishEnv(t)
	if s, _ := p.snapshot(); !s.recorded[pinKey{p.kvPath, 1}].IsZero() {
		t.Fatalf("recorded %v before any status", s.recorded)
	}
	created := time.Date(2026, 9, 1, 0, 0, 0, 5, time.UTC)
	mustExec(t, p.db, `INSERT INTO dependency_status (id, provider, object, version, created, class, observed_from, recorded_at)
		VALUES ($1, 'kv', $2, 1, $3, 'retained', $4, $4)`, id.New(id.Dependency), p.kvPath, createdText(created),
		time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC))
	if s, _ := p.snapshot(); !s.recorded[pinKey{p.kvPath, 1}].Equal(created) {
		t.Fatalf("recorded %v, want %v", s.recorded, created)
	}
}
