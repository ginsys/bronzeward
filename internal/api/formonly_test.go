package api

import (
	"bytes"
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// emptyAssignment gives the machine's assignment in the draft a new revision that selects nothing,
// as Connect's adoption does, so the release compiles the import base alone.
func (b *buildEnv) emptyAssignment() {
	b.t.Helper()
	asr := id.New(id.AssignmentRevision)
	mustExec(b.t, b.db, `INSERT INTO assignment_revision (id, cluster, machine, author, created_at) VALUES ($1, $2, $3, $4, now())`,
		asr, b.cluster, b.machine, b.seed)
	mustExec(b.t, b.db, `UPDATE draft_source_entry SET assignment_revision = $3 WHERE draft = $1 AND kind = 'assignment' AND machine = $2`,
		b.draft, b.machine, asr)
	b.asr = asr
}

// formOnlyEvidence is the part of an adopt plan's baseline evidence this change adds.
type formOnlyEvidence struct {
	Evidence struct {
		Baseline *struct {
			PendingConvergence bool `json:"pendingConvergence"`
			FormOnly           bool `json:"formOnly"`
		} `json:"baseline"`
	} `json:"evidence"`
}

// published builds and commits the draft's release and returns it with the digest of the machine's import base.
func (b *buildEnv) published() (string, []byte) {
	b.t.Helper()
	u, ref := b.build()
	if ref != nil {
		b.t.Fatalf("buildRelease refused: %v %v", ref, ref.extra)
	}
	b.unit = u
	rel, ref := b.commit()
	if ref != nil {
		b.t.Fatalf("commit refused: %v %v", ref, ref.extra)
	}
	var base []byte
	if err := b.db.QueryRow(`SELECT configuration_digest FROM import_base_revision WHERE id = $1`, b.ibr).Scan(&base); err != nil {
		b.t.Fatal(err)
	}
	return rel, base
}

// adopted records the machine as adopted from its own bytes, as the adoption does: Applied is the release
// with the import base's digest.
func (b *buildEnv) adopted(rel string, base []byte) {
	b.t.Helper()
	mustExec(b.t, b.db, `UPDATE machine_state SET applied_release = $2, applied_digest = $3, applied_source = 'adoption',
		baseline_revision = 1 WHERE machine = $1`, b.machine, rel, base)
}

func (b *buildEnv) machineRead() machineBody {
	b.t.Helper()
	return decode[machineBody](b.t, b.do(b.api, call{method: "GET", path: prefix + "/machines/" + b.machine, token: b.robot}), http.StatusOK)
}

// The machine read says form only while the release is pending against the import base's digest and no
// drift is open; an open drift, a digest that is not the import base's, and a release that changes
// content all read "differs" (ginsys/bronzeward#1098).
func TestFormOnlyMachineRead(t *testing.T) {
	b := newBuildEnv(t)
	b.emptyAssignment()
	rel, base := b.published()
	if m := b.machineRead(); !m.PendingConvergence || m.FormOnly {
		t.Fatalf("before the adoption: pending %t, formOnly %t (Applied is not recorded yet, so only the adopt plan says it)", m.PendingConvergence, m.FormOnly)
	}
	b.adopted(rel, base)
	if m := b.machineRead(); !m.PendingConvergence || !m.FormOnly {
		t.Fatalf("after the adoption: pending %t, formOnly %t", m.PendingConvergence, m.FormOnly)
	}
	mustExec(t, b.db, `UPDATE machine_state SET applied_digest = $2 WHERE machine = $1`, b.machine, bytes.Repeat([]byte{9}, 32))
	if m := b.machineRead(); m.FormOnly {
		t.Fatal("Applied is not the import base's digest and the machine reads form only")
	}
	b.adopted(rel, base)
	openDriftOn(t, b.db, b.machine)
	if m := b.machineRead(); m.FormOnly {
		t.Fatal("a drift is open and the machine reads form only")
	}
}

// A release that changes content is not form only, though it is pending too.
func TestFormOnlyContentDiffers(t *testing.T) {
	b := newBuildEnv(t) // the fragment adds an environment variable
	rel, base := b.published()
	got := decode[formOnlyEvidence](t, b.plan(b.robot, "k-formonly-differs-0123", adoptBody(rel, b.machine, "")), http.StatusCreated)
	if bl := got.Evidence.Baseline; bl == nil || !bl.PendingConvergence || bl.FormOnly {
		t.Fatalf("the adopt plan's baseline evidence %+v", bl)
	}
	b.adopted(rel, base)
	if m := b.machineRead(); !m.PendingConvergence || m.FormOnly {
		t.Fatalf("pending %t, formOnly %t", m.PendingConvergence, m.FormOnly)
	}
}

// An apply-config plan from the adopted release shows its diff, and says why, while the machine holds its import
// base and the release differs from it in form only: an empty diff for the release itself, a true one for a changed
// target; a drift, another digest or a release that changes content withholds it as before.
func TestFormOnlyDiff(t *testing.T) {
	b := newBuildEnv(t)
	b.emptyAssignment()
	rel, base := b.published()
	var redacted string
	if err := b.db.QueryRow(`SELECT redacted FROM release_machine WHERE release = $1 AND machine = $2`, rel, b.machine).Scan(&redacted); err != nil {
		t.Fatal(err)
	}
	diff := func(applied []byte, target string, drifted bool) *planDiff {
		tx, err := b.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		d, err := planDiffFrom(t.Context(), tx, b.machine, rel, rel, applied, sql.NullString{String: target, Valid: true}, drifted)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if d := diff(base, redacted, false); d.Withheld || d.Unified != "" || !d.FromFormOnly {
		t.Fatalf("the release itself: %+v", d)
	}
	if d := diff(base, redacted+"extra: 1\n", false); d.Withheld || !strings.Contains(d.Unified, "+extra: 1") || !d.FromFormOnly {
		t.Fatalf("a changed target: %+v", d)
	}
	for name, d := range map[string]*planDiff{
		"an open drift":     diff(base, redacted, true),
		"another digest":    diff(bytes.Repeat([]byte{9}, 32), redacted, false),
		"a withheld target": diff(base, "", false),
	} {
		if !d.Withheld || d.FromFormOnly {
			t.Errorf("%s: %+v", name, d)
		}
	}
	// A release that changes content is not form only: its diff is withheld from the adopted bytes.
	c := newBuildEnv(t)
	crel, cbase := c.published()
	tx, err := c.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if d, err := planDiffFrom(t.Context(), tx, c.machine, crel, crel, cbase, sql.NullString{String: redacted, Valid: true}, false); err != nil || !d.Withheld || d.FromFormOnly {
		t.Fatalf("a release that changes content: %+v %v", d, err)
	}
}

// A machine imported with the comments talosctl writes and published with nothing selected is
// configured with the same content as the node: its adopt plan says the release is not applied yet
// and that it differs in form only (ginsys/bronzeward#1098).
func TestFormOnlyAdoption(t *testing.T) {
	b := newBuildEnv(t)
	b.emptyAssignment()
	u, ref := b.build()
	if ref != nil {
		t.Fatalf("buildRelease refused: %v %v", ref, ref.extra)
	}
	b.unit = u
	rel, ref := b.commit()
	if ref != nil {
		t.Fatalf("commit refused: %v %v", ref, ref.extra)
	}
	rec := b.plan(b.robot, "k-formonly-0123456789", adoptBody(rel, b.machine, ""))
	got := decode[formOnlyEvidence](t, rec, http.StatusCreated)
	if got.Evidence.Baseline == nil || !got.Evidence.Baseline.PendingConvergence {
		t.Fatalf("the adopt plan's baseline evidence %+v: want pending convergence (the compile drops the comments)", got.Evidence.Baseline)
	}
	if !got.Evidence.Baseline.FormOnly {
		t.Fatal("the commented import and its release differ in comments and encoding only, and the adopt plan does not say so")
	}
}
