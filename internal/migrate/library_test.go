package migrate

import (
	"database/sql"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

const (
	// A cluster profile's pin of a library fragment revision says so.
	insertLibraryPin = `INSERT INTO profile_revision_fragment (revision, cluster, position, fragment_revision, library)
		VALUES ($1, $2, $3, $4, true)`
	insertLibraryAdoption = `INSERT INTO library_adoption (cluster, kind, name, fragment, profile, fragment_revision,
		profile_revision, adoption_revision, etag_token) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'm3oxmlfh6phr7aigshdydcb4ji')`
	// A draft entry of a scope: draft, cluster, kind, scope, name, machine, the three revisions,
	// base and adoption base.
	insertScopedEntry = `INSERT INTO draft_source_entry (draft, cluster, kind, scope, name, machine, fragment_revision,
		profile_revision, assignment_revision, base, adoption_base) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`
	insertScopedSource = `INSERT INTO release_source (release, cluster, kind, scope, fragment, profile, assignment, name, machine,
		fragment_revision, profile_revision, assignment_revision, head_revision, adoption_revision, previous_fragment_revision,
		previous_profile_revision) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`
)

// librarySources is releaseRows with a shared fragment ntp at lfrv (lfrv2 a second revision of it),
// a shared profile shared-set at lprv pinning lfrv, and a cluster revision cfrv of the name ntp.
type librarySources struct {
	release
	lfrv, lfrv2, lfrg, lprv, lprf, cfrv string
}

func libraryRows(t *testing.T, db *sql.DB) librarySources {
	t.Helper()
	l := librarySources{release: releaseRows(t, db), lfrv: id.New(id.FragmentRevision), lfrv2: id.New(id.FragmentRevision),
		lfrg: id.New(id.Fragment), lprv: id.New(id.ProfileRevision), lprf: id.New(id.Profile), cfrv: id.New(id.FragmentRevision)}
	doc := "machine: {}\n"
	mustExec(t, db, insertFragmentRevision, l.lfrv, nil, "ntp", "site", doc, l.human)
	mustExec(t, db, insertFragmentRevision, l.lfrv2, nil, "ntp", "site", doc, l.human)
	mustExec(t, db, insertFragmentRevision, l.cfrv, l.cluster, "ntp", "site", doc, l.human)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(t, tx, insertProfileRevision, l.lprv, nil, "shared-set", l.human)
	mustExec(t, tx, insertProfilePin, l.lprv, nil, 0, l.lfrv)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, insertFragment, l.lfrg, nil, "library", "ntp", "site", l.lfrv, 1)
	mustExec(t, db, insertProfile, l.lprf, nil, "library", "shared-set", l.lprv, 1)
	return l
}

// PA §3.1, §3.1.1: a shared head and revision belong to no cluster, a name is unique per scope, and
// every key still holds with no cluster to compare: nothing of one cluster reaches another's rows,
// and a shared row names shared rows only. A cluster's adoption names a shared head and a revision
// of its name, and a release source of a shared name records the adoption.
func TestLibraryKeys(t *testing.T) {
	db, _ := installed(t)
	l := libraryRows(t, db)
	op2, rel2 := id.New(id.Operation), id.New(id.Release)
	mustExec(t, db, insertOperation, op2, "publish", "running", "run-1/4242/publish-2", 1, l.draft2, 1, nil, l.human, nil, nil)
	withRelease := []stmt{{insertRelease, []any{rel2, l.cluster, l.draft2, 1, digest(3), "v1.13", machinery, checksum,
		"v1.36.0", op2, l.human, "publisher"}}, {"SET CONSTRAINTS ALL IMMEDIATE", nil}}
	prvNew := id.New(id.ProfileRevision)
	withClusterProfile := []stmt{{insertProfileRevision, []any{prvNew, l.cluster, "workers", l.human}}}
	withLibraryProfile := []stmt{{insertProfileRevision, []any{prvNew, nil, "shared-set", l.human}}}
	adopted := []stmt{{insertLibraryAdoption, []any{l.cluster, "fragment", "ntp", l.lfrg, nil, l.lfrv, nil, 1}}}
	frg, prf := func() string { return id.New(id.Fragment) }, func() string { return id.New(id.Profile) }
	// A fragment source of ntp in rel2: scope, head, revision, head revision, adoption counter, previous.
	source := func(scope, head string, revision, headRevision, adoption, previous any) []any {
		return []any{rel2, l.cluster, "fragment", scope, head, nil, nil, "ntp", nil, revision, nil, nil, headRevision,
			adoption, previous, nil}
	}
	for _, c := range []struct {
		name string
		pre  []stmt
		q    string
		args []any
		want string
	}{
		{"shared head at a cluster's revision", nil, insertFragment,
			[]any{frg(), nil, "library", "registries", "override", l.frv1, 1}, "23503"},
		{"cluster head at a shared revision", nil, insertFragment, []any{frg(), l.cluster, "cluster", "ntp", "site", l.lfrv, 1}, "23503"},
		{"shared profile head at a cluster's revision", nil, insertProfile, []any{prf(), nil, "library", "workers", l.prv, 1}, "23503"},
		{"cluster profile head at a shared revision", nil, insertProfile,
			[]any{prf(), l.cluster, "cluster", "shared-set", l.lprv, 1}, "23503"},
		{"library scope with a cluster", nil, insertFragment, []any{frg(), l.cluster, "library", "ntp", "site", l.cfrv, 1}, "23514"},
		{"cluster scope with no cluster", nil, insertFragment, []any{frg(), nil, "cluster", "other", "site", nil, 1}, "23514"},
		{"profile cluster scope with no cluster", nil, insertProfile, []any{prf(), nil, "cluster", "other", nil, 1}, "23514"},
		{"second shared fragment of one name", nil, insertFragment, []any{frg(), nil, "library", "ntp", "site", l.lfrv2, 1}, "23505"},
		{"second shared profile of one name", nil, insertProfile, []any{prf(), nil, "library", "shared-set", nil, 1}, "23505"},
		{"shared profile pinning a cluster's revision", withLibraryProfile, insertProfilePin, []any{prvNew, nil, 0, l.frv1}, "23503"},
		{"cluster profile pinning another cluster's revision", withClusterProfile, insertProfilePin,
			[]any{prvNew, l.cluster, 0, l.frvOther}, "23503"},
		{"cluster profile pinning a shared revision unmarked", withClusterProfile, insertProfilePin,
			[]any{prvNew, l.cluster, 0, l.lfrv}, "23503"},
		{"cluster revision marked as a shared pin", withClusterProfile, insertLibraryPin, []any{prvNew, l.cluster, 0, l.frv1}, "23503"},
		{"another cluster's revision marked as a shared pin", withClusterProfile, insertLibraryPin,
			[]any{prvNew, l.cluster, 0, l.frvOther}, "23503"},
		{"cluster profile's pin with no cluster", withClusterProfile, insertProfilePin, []any{prvNew, nil, 0, l.lfrv}, "23503"},
		{"draft entry at a shared revision", nil, insertSourceEntry,
			[]any{l.draft2, l.cluster, "fragment", "ntp", nil, l.lfrv, nil, nil, nil}, "23503"},

		{"adoption of a cluster's head", nil, insertLibraryAdoption,
			[]any{l.cluster, "fragment", "registries", l.frg, nil, l.frv1, nil, 1}, "23503"},
		{"adoption of a cluster's revision", nil, insertLibraryAdoption,
			[]any{l.cluster, "fragment", "ntp", l.lfrg, nil, l.cfrv, nil, 1}, "23503"},
		{"adoption under another name than its head", nil, insertLibraryAdoption,
			[]any{l.cluster, "fragment", "other", l.lfrg, nil, nil, nil, 1}, "23503"},
		{"adoption with two heads", nil, insertLibraryAdoption,
			[]any{l.cluster, "fragment", "ntp", l.lfrg, l.lprf, nil, nil, 1}, "23514"},
		{"adoption of an assignment", nil, insertLibraryAdoption,
			[]any{l.cluster, "assignment", "ntp", l.lfrg, nil, nil, nil, 1}, "23514"},
		{"adoption counter 0", nil, insertLibraryAdoption, []any{l.cluster, "fragment", "ntp", l.lfrg, nil, l.lfrv, nil, 0}, "23514"},
		{"second adoption of one name", adopted, insertLibraryAdoption,
			[]any{l.cluster, "fragment", "ntp", l.lfrg, nil, l.lfrv2, nil, 2}, "23505"},

		{"shared source with a head revision", withRelease, insertScopedSource, source("library", l.lfrg, l.lfrv, 1, 1, nil), "23514"},
		{"cluster source with no head revision", withRelease, insertScopedSource,
			source("cluster", l.lfrg, nil, nil, nil, nil), "23514"},
		{"cluster source with an adoption counter", withRelease, insertScopedSource,
			source("cluster", l.lfrg, nil, 1, 1, nil), "23514"},
		{"cluster source naming a shared head", withRelease, insertScopedSource,
			source("cluster", l.lfrg, nil, 1, nil, nil), "23503"},
		{"shared source with a previous and no counter", withRelease, insertScopedSource,
			source("library", l.lfrg, l.lfrv, nil, nil, l.lfrv), "23514"},
		{"shared source at a cluster's revision", withRelease, insertScopedSource,
			source("library", l.lfrg, l.cfrv, nil, 1, nil), "23503"},
		{"shared source whose previous is a cluster's revision", withRelease, insertScopedSource,
			source("library", l.lfrg, l.lfrv, nil, 1, l.cfrv), "23503"},
		{"shared source naming a cluster's head", withRelease, insertScopedSource,
			[]any{rel2, l.cluster, "fragment", "library", l.frg, nil, nil, "registries", nil, nil, nil, nil, nil, 1, nil, nil}, "23503"},
		{"shared profile source with no counter", withRelease, insertScopedSource,
			[]any{rel2, l.cluster, "profile", "library", nil, l.lprf, nil, "shared-set", nil, nil, nil, nil, nil, nil, nil, nil}, "23514"},
		{"shared assignment source", withRelease, insertScopedSource,
			[]any{rel2, l.cluster, "assignment", "library", nil, nil, l.asg, nil, l.machine, nil, nil, l.asr, nil, 1, nil, nil}, "23514"},
		{"source of an unknown scope", withRelease, insertScopedSource, source("tenant", l.lfrg, l.lfrv, 1, nil, nil), "23514"},
		{"second counted shared source of one head", append(withRelease,
			stmt{insertScopedSource, source("library", l.lfrg, l.lfrv, nil, 1, nil)}), insertScopedSource,
			source("library", l.lfrg, l.lfrv2, nil, 2, nil), "23505"},
		{"second pin-only source of one revision", append(withRelease,
			stmt{insertScopedSource, source("library", l.lfrg, l.lfrv, nil, nil, nil)}), insertScopedSource,
			source("library", l.lfrg, l.lfrv, nil, nil, nil), "23505"},

		{"shared draft entry at a cluster's revision", nil, insertScopedEntry,
			[]any{l.draft2, l.cluster, "fragment", "library", "ntp", nil, l.cfrv, nil, nil, nil, nil}, "23503"},
		{"shared assignment entry", nil, insertScopedEntry,
			[]any{l.draft2, l.cluster, "assignment", "library", nil, l.machine, nil, nil, nil, nil, nil}, "23514"},
		{"cluster entry with an adoption base", nil, insertScopedEntry,
			[]any{l.draft2, l.cluster, "fragment", "cluster", "ntp", nil, l.cfrv, nil, nil, nil, 1}, "23514"},
		{"shared removal with an adoption base", nil, insertScopedEntry,
			[]any{l.draft2, l.cluster, "fragment", "library", "ntp", nil, nil, nil, nil, 1, 1}, "23514"},
		{"draft entry of an unknown scope", nil, insertScopedEntry,
			[]any{l.draft2, l.cluster, "fragment", "tenant", "ntp", nil, nil, nil, nil, nil, nil}, "23514"},
		{"second shared entry of one name", []stmt{{insertScopedEntry,
			[]any{l.draft2, l.cluster, "fragment", "library", "ntp", nil, l.lfrv, nil, nil, 1, 1}}}, insertScopedEntry,
			[]any{l.draft2, l.cluster, "fragment", "library", "ntp", nil, l.lfrv2, nil, nil, 1, 1}, "23505"},
	} {
		func() {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			for _, p := range c.pre {
				mustExec(t, tx, p.q, p.args...)
			}
			if _, err := tx.Exec(c.q, c.args...); sqlState(err) != c.want {
				t.Errorf("%s: %v; want SQLSTATE %s", c.name, err, c.want)
			}
		}()
	}

	// Controls: the same shapes commit with valid values. A cluster holds a shared name at the
	// revision it adopted, a removed adoption keeps its row, a cluster profile pins a shared
	// revision, and a shared profile's pin is a source with no adoption counter.
	mustExec(t, db, insertFragment, frg(), nil, "library", "registries", "override", nil, 2)
	mustExec(t, db, insertLibraryAdoption, l.cluster, "fragment", "ntp", l.lfrg, nil, l.lfrv, nil, 1)
	mustExec(t, db, insertLibraryAdoption, l.other, "fragment", "ntp", l.lfrg, nil, nil, nil, 2)
	mustExec(t, db, insertLibraryAdoption, l.cluster, "profile", "shared-set", nil, l.lprf, nil, l.lprv, 1)
	for _, rows := range [][]stmt{
		append(withClusterProfile, stmt{insertLibraryPin, []any{prvNew, l.cluster, 0, l.lfrv}},
			stmt{insertProfilePin, []any{prvNew, l.cluster, 1, l.frv1}}),
		append(withRelease[:1:1], stmt{insertScopedSource, source("library", l.lfrg, l.lfrv, nil, 3, l.lfrv2)},
			// A shared profile's pin of another revision of that head is a source beside it.
			stmt{insertScopedSource, source("library", l.lfrg, l.lfrv2, nil, nil, nil)},
			stmt{insertScopedSource, []any{rel2, l.cluster, "profile", "library", nil, l.lprf, nil, "shared-set", nil, nil,
				l.lprv, nil, nil, 1, nil, l.lprv}}),
		// A draft holds a cluster entry and a shared entry of one name.
		{{insertScopedEntry, []any{l.draft2, l.cluster, "fragment", "cluster", "ntp", nil, l.cfrv, nil, nil, nil, nil}},
			{insertScopedEntry, []any{l.draft2, l.cluster, "fragment", "library", "ntp", nil, l.lfrv, nil, nil, 1, 1}}},
	} {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range rows {
			mustExec(t, tx, s.q, s.args...)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	// Dropped, the head key admits what it refused above: it is that key which refuses it.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(t, tx, `ALTER TABLE fragment DROP CONSTRAINT fragment_head_revision`)
	mustExec(t, tx, insertFragment, frg(), nil, "library", "site-dns", "site", l.frvSite, 1)
}

// A fragment a shared profile revision pins is a release source with no adoption counter (PA §6.2).
func TestLibraryPinnedSource(t *testing.T) {
	db, _ := installed(t)
	l := libraryRows(t, db)
	op2, rel2 := id.New(id.Operation), id.New(id.Release)
	mustExec(t, db, insertOperation, op2, "publish", "running", "run-1/4242/publish-2", 1, l.draft2, 1, nil, l.human, nil, nil)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(t, tx, insertRelease, rel2, l.cluster, l.draft2, 1, digest(3), "v1.13", machinery, checksum, "v1.36.0", op2,
		l.human, "publisher")
	mustExec(t, tx, insertScopedSource, rel2, l.cluster, "fragment", "library", l.lfrg, nil, nil, "ntp", nil, l.lfrv, nil, nil,
		nil, nil, nil, nil)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
