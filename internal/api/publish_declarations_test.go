package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/compile"
	"github.com/ginsys/bronzeward/internal/id"
)

// textDigest is the hex SHA-256 of a source revision's text, as compile.Origin carries it.
func textDigest(text string) string {
	d := sha256.Sum256([]byte(text))
	return hex.EncodeToString(d[:])
}

// declaredFragment inserts a fragment revision declaring reference at generation, in the
// revision's own transaction (0009).
func (p *publishEnv) declaredFragment(name, layer, reference, generation string) string {
	p.t.Helper()
	tx, err := p.db.Begin()
	if err != nil {
		p.t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	frv := id.New(id.FragmentRevision)
	mustExec(p.t, tx, `INSERT INTO fragment_revision (id, cluster, name, layer, document, author, embedded, created_at)
		VALUES ($1, $2, $3, $4, 'machine: {}', $5, '[]', now())`, frv, p.cluster, name, layer, p.seed)
	mustExec(p.t, tx, `INSERT INTO fragment_reference (revision, name, kind, version, generation) VALUES ($1, $2, 'string', 1, $3)`,
		frv, reference, generation)
	if err := tx.Commit(); err != nil {
		p.t.Fatal(err)
	}
	return frv
}

// importBase points the draft and the unit at a new import base revision of the same text that
// declares registry/pass as kind, with encoding (nil for none); the record is compiled as kind.
func (p *publishEnv) importBase(kind string, encoding any) {
	p.t.Helper()
	ibr := id.New(id.ImportBase)
	mustExec(p.t, p.db, `INSERT INTO import_base_revision (id, machine, document, baseline_ciphertext, baseline_digest,
		baseline_digest_key, configuration_digest, created_at) VALUES ($1, $2, 'machine: {}', '\x01', $3, 'transit/baseline-digest:1', $3, now())`,
		ibr, p.machine, make([]byte, 32))
	mustExec(p.t, p.db, `INSERT INTO import_base_reference (revision, name, kind, version, encoding, generation)
		VALUES ($1, 'registry/pass', $2, 1, $3, $4)`, ibr, kind, encoding, p.kvPath)
	mustExec(p.t, p.db, `UPDATE draft_entry SET import_base_revision = $2 WHERE draft = $1`, p.draft, ibr)
	m := &p.unit.machines[0]
	m.importBase, m.reproduction[0].source, m.provenance[0].Source.Revision = ibr, ibr, ibr
	m.provenance[0].Kind = kind
}

// classified adds publication's retained classification of a KV object version to the unit.
func (p *publishEnv) classified(object string, version int64) {
	p.unit.statuses = append(p.unit.statuses, unitStatus{provider: classify.KV, object: object, version: version, began: began,
		result: classify.Result{Class: classify.Retained, Created: kvCreated}})
}

// secondMachine adds a covered machine with no assignment, whose composition is its import base
// alone, declaring and using registry/pass like the first, and returns it.
func (p *publishEnv) secondMachine() *unitMachine {
	p.t.Helper()
	rec := p.do(p.api, machineCall(p.human("h-author"), "k-machine-2-0123456789", p.cluster, "1c6b7d2f-3a4e-4f60-9bac-1d2e3f4a5b6c"))
	machine := decode[machineBody](p.t, rec, http.StatusCreated).ID
	ibr := id.New(id.ImportBase)
	mustExec(p.t, p.db, `INSERT INTO import_base_revision (id, machine, document, baseline_ciphertext, baseline_digest,
		baseline_digest_key, configuration_digest, created_at) VALUES ($1, $2, 'machine: {}', '\x01', $3, 'transit/baseline-digest:1', $3, now())`,
		ibr, machine, make([]byte, 32))
	mustExec(p.t, p.db, `INSERT INTO import_base_reference (revision, name, kind, version, generation)
		VALUES ($1, 'registry/pass', 'string', 1, $2)`, ibr, p.kvPath)
	mustExec(p.t, p.db, `INSERT INTO draft_entry (draft, cluster, kind, machine, import_base_revision)
		VALUES ($1, $2, 'import-base', $3, $4)`, p.draft, p.cluster, machine, ibr)
	m := p.unit.machines[0]
	m.machine, m.importBase, m.assignment = machine, ibr, ""
	m.reproduction = []unitDependency{m.reproduction[0]}
	m.reproduction[0].source = ibr
	m.provenance = []compile.Record{m.provenance[0]}
	m.provenance[0].Source.Revision = ibr
	p.unit.machines = append(p.unit.machines, m)
	return &p.unit.machines[len(p.unit.machines)-1]
}

// extraFragment adds a fragment head the release uses unchanged but that neither the machine's
// assignment selects nor any of its profiles pins, declaring registry/pass, and returns its
// revision.
func (p *publishEnv) extraFragment() string {
	p.t.Helper()
	frv := p.declaredFragment("extra", "workload", "registry/pass", p.kvPath)
	frg := p.fragmentHead("extra", "workload", frv, 1)
	p.unit.unchanged = append(p.unit.unchanged, usedHead{kind: "fragment", head: frg, revision: frv, headRevision: 1})
	return frv
}

// The compiled unit's reproduction dependencies and provenance agree with the reference
// declarations and the text of the sources the machine's composition names (compilation §5.2,
// §8.2, §9): any disagreement is the unit's defect, and the commit fails closed naming the
// clause, writing nothing (rulings R16, R20).
func TestPublishCommitDeclarations(t *testing.T) {
	other := sha256.Sum256([]byte("machine: {other: true}"))
	for _, c := range []struct {
		name, reason string
		mutate       func(p *publishEnv)
	}{
		{"reproduction source outside", "reproduction source outside the composition", func(p *publishEnv) {
			p.unit.machines[0].reproduction[0].source = p.extraFragment()
		}},
		// A fragment the first machine composes is not in the second's.
		{"reproduction source of another machine", "reproduction source outside the composition", func(p *publishEnv) {
			m := p.secondMachine()
			m.reproduction[0].source = p.networkNew
		}},
		{"provenance source of another machine", "provenance source outside the composition", func(p *publishEnv) {
			m := p.secondMachine()
			m.provenance[0].Source.Revision = p.networkNew
		}},
		{"reproduction reference", "reproduction names no declaration", func(p *publishEnv) {
			p.unit.machines[0].reproduction[0].reference = "registry/other"
		}},
		{"reproduction version", "reproduction version differs from the declaration", func(p *publishEnv) {
			p.unit.machines[0].reproduction[0].version = 2
			p.classified(p.kvPath, 2)
		}},
		{"reproduction object", "reproduction object differs from the declared generation", func(p *publishEnv) {
			object := "gen/" + p.cluster + "/" + id.New(id.Ingestion) + "/pass"
			p.unit.machines[0].reproduction[0].object = object
			p.classified(object, 1)
		}},
		{"reproduction digest", "reproduction source digest differs", func(p *publishEnv) {
			p.unit.machines[0].reproduction[0].digest = other
		}},
		{"reproduction occurrence gap", "reproduction occurrences are not numbered from 0", func(p *publishEnv) {
			p.unit.machines[0].reproduction[0].occurrence = 1
		}},
		{"reproduction occurrence repeated", "reproduction occurrences are not numbered from 0", func(p *publishEnv) {
			m := &p.unit.machines[0]
			d := m.reproduction[0]
			d.path = "doc[0]/machine/other"
			m.reproduction = append(m.reproduction, d)
		}},
		{"provenance source outside", "provenance source outside the composition", func(p *publishEnv) {
			p.unit.machines[0].provenance[0].Source.Revision = p.extraFragment()
		}},
		{"provenance digest", "provenance source digest differs", func(p *publishEnv) {
			p.unit.machines[0].provenance[0].Source.Digest = hex.EncodeToString(other[:])
		}},
		{"provenance reference", "provenance names no declaration", func(p *publishEnv) {
			p.unit.machines[0].provenance[0].Reference = "registry/other"
		}},
		{"provenance version", "provenance version differs from the declaration", func(p *publishEnv) {
			p.unit.machines[0].provenance[0].Version = 2
		}},
		{"provenance encoding added", "provenance encoding differs from the declaration", func(p *publishEnv) {
			p.unit.machines[0].provenance[0].Encoding = "base64"
		}},
		{"provenance encoding dropped", "provenance encoding differs from the declaration", func(p *publishEnv) {
			p.importBase("string", "base64")
		}},
		{"provenance member on a scalar", "provenance member does not fit the declared kind", func(p *publishEnv) {
			p.unit.machines[0].provenance[0].Member = 0
		}},
		{"provenance mapping without member", "provenance member does not fit the declared kind", func(p *publishEnv) {
			p.importBase("mapping", nil)
		}},
		{"provenance scalar with members", "provenance member does not fit the declared kind", func(p *publishEnv) {
			p.unit.machines[0].provenance[0].Members = 1
		}},
		{"provenance member past its mapping", "provenance member does not fit the declared kind", func(p *publishEnv) {
			p.importBase("mapping", nil)
			r := &p.unit.machines[0].provenance[0]
			r.Member, r.Members = 2, 2
		}},
		// Each member of a mapping occurrence has an outcome (compilation §8.2).
		{"provenance mapping member missing", "provenance occurrence without an outcome for every member", func(p *publishEnv) {
			p.importBase("mapping", nil)
			r := &p.unit.machines[0].provenance[0]
			r.Member, r.Members, r.Output = 1, 2, "doc[0]/machine/registries/<redacted>"
		}},
		{"provenance member counts differ", "provenance member count differs within an occurrence", func(p *publishEnv) {
			p.importBase("mapping", nil)
			m := &p.unit.machines[0]
			r := m.provenance[0]
			r.Member, r.Members, r.Output = 0, 2, "doc[0]/machine/registries/<redacted>"
			m.provenance[0] = r
			r.Member, r.Members = 1, 3
			m.provenance = append(m.provenance, r)
		}},
		// Every scalar kind has no member, so the kind itself is compared.
		{"provenance scalar kind", "provenance kind differs from the declaration", func(p *publishEnv) {
			p.unit.machines[0].provenance[0].Kind = "boolean"
		}},
		{"provenance mapping member as a scalar kind", "provenance kind differs from the declaration", func(p *publishEnv) {
			p.importBase("mapping", nil)
			r := &p.unit.machines[0].provenance[0]
			r.Member, r.Members, r.Kind = 0, 1, "string"
		}},
		{"provenance override outside", "provenance override is not a fragment of the composition", func(p *publishEnv) {
			r := &p.unit.machines[0].provenance[0]
			r.Output, r.OverriddenBy = "", &compile.Origin{Fragment: 0, Revision: p.extraFragment(), Digest: textDigest("machine: {}")}
		}},
		{"provenance override by the import base", "provenance override is not a fragment of the composition", func(p *publishEnv) {
			r := &p.unit.machines[0].provenance[0]
			r.Output, r.OverriddenBy = "", &compile.Origin{Base: true, Fragment: -1, Revision: p.ibr, Digest: textDigest("machine: {}")}
		}},
		{"provenance override digest", "provenance override digest differs", func(p *publishEnv) {
			r := &p.unit.machines[0].provenance[0]
			r.Output, r.OverriddenBy = "", &compile.Origin{Fragment: 0, Revision: p.networkNew, Digest: hex.EncodeToString(other[:])}
		}},
		{"declaration without reproduction", "declaration without a reproduction dependency", func(p *publishEnv) {
			p.unit.machines[0].reproduction = nil
		}},
		{"declaration without provenance", "declaration without a provenance record", func(p *publishEnv) {
			p.unit.machines[0].provenance = nil
		}},
		{"provenance path", "provenance occurrence without a reproduction dependency", func(p *publishEnv) {
			p.unit.machines[0].provenance[0].SourcePath = "doc[0]/machine/other"
		}},
		{"provenance occurrence", "provenance occurrence without a reproduction dependency", func(p *publishEnv) {
			p.unit.machines[0].provenance[0].Occurrence = 1
		}},
		// Each record has exactly one outcome and stored paths, as the review read requires.
		{"provenance without outcome", "provenance record without exactly one outcome", func(p *publishEnv) {
			p.unit.machines[0].provenance[0].Output = ""
		}},
		{"provenance with both outcomes", "provenance record without exactly one outcome", func(p *publishEnv) {
			r := &p.unit.machines[0].provenance[0]
			r.OverriddenBy = &compile.Origin{Fragment: 0, Revision: p.networkNew, Digest: textDigest("machine: {}")}
		}},
		{"provenance output not a path", "provenance path is not a stored path", func(p *publishEnv) {
			p.unit.machines[0].provenance[0].Output = "doc[0]/machine/a~2"
		}},
		{"provenance source path not a path", "provenance path is not a stored path", func(p *publishEnv) {
			m := &p.unit.machines[0]
			m.provenance[0].SourcePath, m.reproduction[0].path = "doc[0]/machine/a~2", "doc[0]/machine/a~2"
		}},
		// An occurrence (a mapping member) has one outcome: one override, or output records.
		{"provenance override repeated", "provenance outcome repeated", func(p *publishEnv) {
			m := &p.unit.machines[0]
			r := &m.provenance[0]
			r.Output, r.OverriddenBy = "", &compile.Origin{Fragment: 0, Revision: p.networkNew, Digest: textDigest("machine: {}")}
			m.provenance = append(m.provenance, *r)
		}},
		{"provenance output then override", "provenance outcome repeated", func(p *publishEnv) {
			m := &p.unit.machines[0]
			r := m.provenance[0]
			r.Output, r.OverriddenBy = "", &compile.Origin{Fragment: 0, Revision: p.networkNew, Digest: textDigest("machine: {}")}
			m.provenance = append(m.provenance, r)
		}},
		{"provenance override then output", "provenance outcome repeated", func(p *publishEnv) {
			m := &p.unit.machines[0]
			out := m.provenance[0]
			r := &m.provenance[0]
			r.Output, r.OverriddenBy = "", &compile.Origin{Fragment: 0, Revision: p.networkNew, Digest: textDigest("machine: {}")}
			m.provenance = append(m.provenance, out)
		}},
		{"reproduction path", "reproduction dependency without a provenance record", func(p *publishEnv) {
			m := &p.unit.machines[0]
			d := m.reproduction[0]
			d.path, d.occurrence = "doc[0]/machine/other", 1
			m.reproduction = append(m.reproduction, d)
		}},
		// Two occurrences whose redacted paths read the same are two dependencies (§9), and each
		// needs its own provenance.
		{"redacted paths alike", "reproduction dependency without a provenance record", func(p *publishEnv) {
			m := &p.unit.machines[0]
			d := m.reproduction[0]
			d.occurrence = 1
			m.reproduction = append(m.reproduction, d)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newPublishEnv(t)
			c.mutate(p)
			p.refused(500, "internal-error")
			// The disagreeing machine is the one a case mutates: the second when it adds one.
			if want := "machine " + p.unit.machines[len(p.unit.machines)-1].machine + ": " + c.reason; !p.logged(want) {
				t.Fatalf("no log %q in %q", want, p.logs)
			}
		})
	}
}

// A mapping reference's members, an encoded string and fragments the machine's composition names,
// by its assignment's selection and through its profile's pin, all agree: the commit succeeds.
func TestPublishCommitDeclarationsAgree(t *testing.T) {
	t.Run("mapping", func(t *testing.T) {
		p := newPublishEnv(t)
		p.importBase("mapping", nil)
		m := &p.unit.machines[0]
		// Each member's key is a value, so both output paths read the same (compilation §8.3).
		r := m.provenance[0]
		r.Member, r.Members, r.Output = 0, 2, "doc[0]/machine/registries/<redacted>"
		m.provenance[0] = r
		r.Member = 1
		m.provenance = append(m.provenance, r)
		if rel, ref := p.commit(); ref != nil || rel == "" {
			t.Fatalf("commit %s %v", rel, ref)
		}
	})
	t.Run("redacted paths alike", func(t *testing.T) {
		p := newPublishEnv(t)
		m := &p.unit.machines[0]
		d, r := m.reproduction[0], m.provenance[0]
		d.occurrence, r.Occurrence, r.Output = 1, 1, "doc[0]/machine/registries/<redacted>"
		m.reproduction, m.provenance = append(m.reproduction, d), append(m.provenance, r)
		rel, ref := p.commit()
		if ref != nil || rel == "" {
			t.Fatalf("commit %s %v", rel, ref)
		}
		// The stored records keep each occurrence's position.
		var stored string
		if err := p.db.QueryRow(`SELECT jsonb_path_query_array(provenance, '$[*].source.occurrence')::text FROM release_machine
			WHERE release = $1`, rel).Scan(&stored); err != nil || stored != "[0, 1]" {
			t.Fatalf("stored occurrences %q %v, want [0, 1]", stored, err)
		}
	})
	// A fragment that overrides a mapping overrides each of its members: one override per member.
	t.Run("mapping overridden", func(t *testing.T) {
		p := newPublishEnv(t)
		p.importBase("mapping", nil)
		m := &p.unit.machines[0]
		r := m.provenance[0]
		r.Member, r.Members, r.Output = 0, 2, ""
		r.OverriddenBy = &compile.Origin{Fragment: 0, Revision: p.networkNew, Digest: textDigest("machine: {}")}
		m.provenance[0] = r
		r.Member = 1
		m.provenance = append(m.provenance, r)
		if rel, ref := p.commit(); ref != nil || rel == "" {
			t.Fatalf("commit %s %v", rel, ref)
		}
	})
	// Two alias outputs whose document token holds the value both read <redacted> (compilation
	// §8.3): two identical records, both accepted.
	t.Run("alias outputs alike", func(t *testing.T) {
		p := newPublishEnv(t)
		m := &p.unit.machines[0]
		m.provenance[0].Output = "<redacted>"
		m.provenance = append(m.provenance, m.provenance[0])
		if rel, ref := p.commit(); ref != nil || rel == "" {
			t.Fatalf("commit %s %v", rel, ref)
		}
	})
	t.Run("two machines", func(t *testing.T) {
		p := newPublishEnv(t)
		p.secondMachine()
		if rel, ref := p.commit(); ref != nil || rel == "" {
			t.Fatalf("commit %s %v", rel, ref)
		}
	})
	t.Run("encoded", func(t *testing.T) {
		p := newPublishEnv(t)
		p.importBase("string", "base64")
		p.unit.machines[0].provenance[0].Encoding = "base64"
		if rel, ref := p.commit(); ref != nil || rel == "" {
			t.Fatalf("commit %s %v", rel, ref)
		}
	})
	t.Run("fragments", func(t *testing.T) {
		p := newPublishEnv(t)
		gen := func(v string) string { return "gen/" + p.cluster + "/" + id.New(id.Ingestion) + "/" + v }
		// The selected fragment storage, introduced by the draft, now declares fs/key.
		fsGen := gen("key")
		storage := p.declaredFragment("storage", "role", "fs/key", fsGen)
		mustExec(t, p.db, `UPDATE draft_source_entry SET fragment_revision = $2 WHERE draft = $1 AND name = 'storage'`, p.draft, storage)
		// The profile's pinned fragment base declares base/token, at a new head revision and pin.
		baseGen := gen("token")
		base := p.declaredFragment("base", "global", "base/token", baseGen)
		mustExec(t, p.db, `UPDATE fragment SET head_revision_id = $2 WHERE id = $1`, p.base, base)
		tx, err := p.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		prv := id.New(id.ProfileRevision)
		mustExec(t, tx, `INSERT INTO profile_revision (id, cluster, name, author, created_at) VALUES ($1, $2, 'standard', $3, now())`,
			prv, p.cluster, p.seed)
		mustExec(t, tx, `INSERT INTO profile_revision_fragment (revision, cluster, position, fragment_revision) VALUES ($1, $2, 0, $3)`,
			prv, p.cluster, base)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		mustExec(t, p.db, `UPDATE profile SET head_revision_id = $2 WHERE id = $1`, p.prf, prv)
		p.unit.unchanged = []usedHead{{kind: "fragment", head: p.base, revision: base, headRevision: 1},
			{kind: "profile", head: p.prf, revision: prv, headRevision: 1}}

		m := &p.unit.machines[0]
		for i, s := range []struct{ source, reference, object, path string }{
			{storage, "fs/key", fsGen, "doc[0]/machine/disks"}, {base, "base/token", baseGen, "doc[0]/cluster/token"}} {
			m.reproduction = append(m.reproduction, unitDependency{reference: s.reference, object: s.object, version: 1,
				created: kvCreated, source: s.source, digest: sha256.Sum256([]byte("machine: {}")), path: s.path})
			m.provenance = append(m.provenance, compile.Record{Reference: s.reference, Version: 1, Kind: "string", Member: -1,
				Source:     compile.Origin{Fragment: i, Revision: s.source, Digest: textDigest("machine: {}")},
				SourcePath: s.path, Output: s.path})
			p.classified(s.object, 1)
		}
		if rel, ref := p.commit(); ref != nil || rel == "" {
			t.Fatalf("commit %s %v", rel, ref)
		}
	})
}
