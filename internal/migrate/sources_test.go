package migrate

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// The statements 0009's tests insert with.
const (
	insertFragmentRevision = `INSERT INTO fragment_revision (id, cluster, name, layer, document, author, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())`
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
	insertSourceEntry = `INSERT INTO draft_source_entry (draft, cluster, kind, name, machine, fragment_revision, profile_revision,
		assignment_revision, base) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
)

// stmt is a statement and its arguments.
type stmt struct {
	q    string
	args []any
}

// sources holds one of each 0009 row, inserted by sourceRows on top of adoptionRows.
type sources struct {
	adoption
	frv1, frv2, frvOther, frvSite, frg, prv, prf, asr, asg string
}

func sourceRows(t *testing.T, db *sql.DB) sources {
	t.Helper()
	s := sources{adoption: adoptionRows(t, db), frv1: id.New(id.FragmentRevision), frv2: id.New(id.FragmentRevision),
		frvOther: id.New(id.FragmentRevision), frvSite: id.New(id.FragmentRevision), frg: id.New(id.Fragment),
		prv: id.New(id.ProfileRevision), prf: id.New(id.Profile), asr: id.New(id.AssignmentRevision), asg: id.New(id.Assignment)}
	doc := "machine:\n  registries: {}\n"
	// A revision's rows are written in its own transaction.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	mustExec(t, tx, insertFragmentRevision, s.frv1, s.cluster, "registries", "override", doc, s.human)
	mustExec(t, tx, insertFragmentReference, s.frv1, "registry/example-pass", "string", 1, nil, generation(s.cluster, s.claim))
	mustExec(t, tx, insertProfileRevision, s.prv, s.cluster, "workers", s.human)
	mustExec(t, tx, insertProfilePin, s.prv, s.cluster, 0, s.frv1)
	mustExec(t, tx, insertAssignmentRevision, s.asr, s.cluster, s.machine, s.human)
	mustExec(t, tx, insertAssignmentProfile, s.asr, 0, "workers")
	mustExec(t, tx, insertAssignmentFragment, s.asr, "site", 0, "site-dns")
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, insertFragmentRevision, s.frv2, s.cluster, "registries", "override", doc, s.human)
	mustExec(t, db, insertFragmentRevision, s.frvOther, s.other, "registries", "override", doc, s.human)
	mustExec(t, db, insertFragmentRevision, s.frvSite, s.cluster, "site-dns", "site", doc, s.human)
	mustExec(t, db, insertFragment, s.frg, s.cluster, "cluster", "registries", "override", s.frv1, 1)
	mustExec(t, db, insertProfile, s.prf, s.cluster, "cluster", "workers", s.prv, 1)
	mustExec(t, db, insertAssignment, s.asg, s.cluster, s.machine, s.asr, 1)
	mustExec(t, db, insertSourceEntry, s.draft, s.cluster, "fragment", "registries", nil, s.frv2, nil, nil, 1)
	mustExec(t, db, insertSourceEntry, s.draft, s.cluster, "profile", "workers", nil, nil, nil, nil, 1)
	mustExec(t, db, insertSourceEntry, s.draft, s.cluster, "assignment", nil, s.machine, nil, nil, s.asr, 1)
	return s
}

// PA §3, §3.1, choice §17.31: names, layers and cluster scope; one head per name and machine, each
// pointing at a revision of its own name (and layer); pins and selections within one cluster; one
// draft entry per name or machine, shaped by its kind.
func TestSourcesConstraints(t *testing.T) {
	db, _ := installed(t)
	s := sourceRows(t, db)
	frv := func() string { return id.New(id.FragmentRevision) }
	doc := "machine: {}\n"
	// A row of a revision is written with it, so those cases first insert one in their transaction.
	frvNew, prvNew, asrNew := frv(), id.New(id.ProfileRevision), id.New(id.AssignmentRevision)
	withFragment := []stmt{{insertFragmentRevision, []any{frvNew, s.cluster, "dns", "site", doc, s.human}},
		{insertFragmentReference, []any{frvNew, "registry/example-pass", "string", 1, nil, generation(s.cluster, s.claim)}}}
	withProfile := []stmt{{insertProfileRevision, []any{prvNew, s.cluster, "workers", s.human}},
		{insertProfilePin, []any{prvNew, s.cluster, 0, s.frv1}}}
	withAssignment := []stmt{{insertAssignmentRevision, []any{asrNew, s.cluster, s.machine, s.human}},
		{insertAssignmentProfile, []any{asrNew, 0, "workers"}}, {insertAssignmentFragment, []any{asrNew, "site", 0, "site-dns"}}}
	for _, c := range []struct {
		name, q string
		args    []any
		want    string
	}{
		{"fragment name with an underscore", insertFragmentRevision, []any{frv(), s.cluster, "bad_name", "site", doc, s.human}, "23514"},
		{"fragment name with a capital", insertFragmentRevision, []any{frv(), s.cluster, "Registries", "site", doc, s.human}, "23514"},
		{"fragment name of 64 bytes", insertFragmentRevision, []any{frv(), s.cluster, "a123456789012345678901234567890123456789012345678901234567890123", "site", doc, s.human}, "23514"},
		{"fragment name ending in a hyphen", insertFragmentRevision, []any{frv(), s.cluster, "dns-", "site", doc, s.human}, "23514"},
		{"machine-intrinsic layer", insertFragmentRevision, []any{frv(), s.cluster, "dns", "machine-intrinsic", doc, s.human}, "23514"},
		{"empty fragment document", insertFragmentRevision, []any{frv(), s.cluster, "dns", "site", "", s.human}, "23514"},
		{"fragment revision of no principal", insertFragmentRevision, []any{frv(), s.cluster, "dns", "site", doc, id.New(id.Principal)}, "23503"},
		{"fragment reference of no revision", insertFragmentReference, []any{frv(), "registry/x", "string", 1, nil, generation(s.cluster, s.claim)}, "23503"},
		{"second reference of one name", insertFragmentReference, []any{frvNew, "registry/example-pass", "string", 2, nil, generation(s.cluster, s.claim)}, "23505"},
		{"library fragment", insertFragment, []any{id.New(id.Fragment), s.cluster, "library", "site-dns", "site", s.frvSite, 1}, "23514"},
		{"second fragment head of one name", insertFragment, []any{id.New(id.Fragment), s.cluster, "cluster", "registries", "override", s.frv2, 1}, "23505"},
		{"fragment head at another name's revision", insertFragment, []any{id.New(id.Fragment), s.cluster, "cluster", "site-dns", "site", s.frv1, 1}, "23503"},
		{"fragment head in another layer than its revision", insertFragment, []any{id.New(id.Fragment), s.cluster, "cluster", "site-dns", "global", s.frvSite, 1}, "23503"},
		{"fragment head at another cluster's revision", insertFragment, []any{id.New(id.Fragment), s.other, "cluster", "site-dns", "site", s.frvSite, 1}, "23503"},
		{"fragment head revision 0", insertFragment, []any{id.New(id.Fragment), s.cluster, "cluster", "site-dns", "site", s.frvSite, 0}, "23514"},
		{"profile pin of another cluster's fragment", insertProfilePin, []any{prvNew, s.cluster, 1, s.frvOther}, "23503"},
		{"one fragment pinned twice", insertProfilePin, []any{prvNew, s.cluster, 1, s.frv1}, "23505"},
		{"profile pin at a negative position", insertProfilePin, []any{prvNew, s.cluster, -1, s.frv2}, "23514"},
		{"library profile", insertProfile, []any{id.New(id.Profile), s.cluster, "library", "workers2", nil, 1}, "23514"},
		{"second profile head of one name", insertProfile, []any{id.New(id.Profile), s.cluster, "cluster", "workers", nil, 1}, "23505"},
		{"assignment revision of another cluster's machine", insertAssignmentRevision, []any{id.New(id.AssignmentRevision), s.cluster, s.otherMachine, s.human}, "23503"},
		{"one profile selected twice", insertAssignmentProfile, []any{asrNew, 1, "workers"}, "23505"},
		{"selection in an unknown layer", insertAssignmentFragment, []any{asrNew, "rack", 0, "rack-dns"}, "23514"},
		{"one fragment selected twice", insertAssignmentFragment, []any{asrNew, "global", 0, "site-dns"}, "23505"},
		{"second assignment head of one machine", insertAssignment, []any{id.New(id.Assignment), s.cluster, s.machine, nil, 1}, "23505"},
		{"entry with two revisions", insertSourceEntry, []any{s.draft2, s.cluster, "fragment", "registries", nil, s.frv2, s.prv, nil, 1}, "23514"},
		{"profile entry naming a machine", insertSourceEntry, []any{s.draft2, s.cluster, "profile", "workers", s.machine, nil, nil, nil, 1}, "23514"},
		{"assignment entry with a name", insertSourceEntry, []any{s.draft2, s.cluster, "assignment", "workers", s.machine, nil, nil, s.asr, 1}, "23514"},
		{"entry of an unknown kind", insertSourceEntry, []any{s.draft2, s.cluster, "import-base", "workers", nil, nil, nil, nil, 1}, "23514"},
		{"entry base 0", insertSourceEntry, []any{s.draft2, s.cluster, "profile", "workers", nil, nil, nil, nil, 0}, "23514"},
		{"second entry of one fragment", insertSourceEntry, []any{s.draft, s.cluster, "fragment", "registries", nil, s.frv1, nil, nil, 1}, "23505"},
		{"second entry of one machine", insertSourceEntry, []any{s.draft, s.cluster, "assignment", nil, s.machine, nil, nil, nil, 1}, "23505"},
		{"entry at another name's revision", insertSourceEntry, []any{s.draft2, s.cluster, "fragment", "site-dns", nil, s.frv1, nil, nil, nil}, "23503"},
		{"entry in another cluster than its draft", insertSourceEntry, []any{s.draft2, s.other, "fragment", "registries", nil, s.frvOther, nil, nil, nil}, "23503"},
	} {
		func() {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			pre := map[any][]stmt{frvNew: withFragment, prvNew: withProfile, asrNew: withAssignment}[c.args[0]]
			for _, p := range pre {
				mustExec(t, tx, p.q, p.args...)
			}
			if _, err := tx.Exec(c.q, c.args...); sqlState(err) != c.want {
				t.Errorf("%s: %v; want SQLSTATE %s", c.name, err, c.want)
			}
		}()
	}
	// Controls: the same shapes commit with valid values.
	mustExec(t, db, insertFragmentRevision, frv(), s.cluster, "a12345678901234567890123456789012345678901234567890123456789012", "global", doc, s.human)
	mustExec(t, db, insertFragment, id.New(id.Fragment), s.cluster, "cluster", "site-dns", "site", s.frvSite, 1)
	mustExec(t, db, insertFragment, id.New(id.Fragment), s.cluster, "cluster", "removed", "site", nil, 2)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range withProfile {
		mustExec(t, tx, p.q, p.args...)
	}
	mustExec(t, tx, insertProfilePin, prvNew, s.cluster, 1, s.frv2)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, insertSourceEntry, s.draft2, s.cluster, "fragment", "registries", nil, s.frv1, nil, nil, 1)
	mustExec(t, db, insertSourceEntry, s.draft2, s.cluster, "assignment", nil, s.machine, nil, nil, nil, 1)
	mustExec(t, db, insertSourceEntry, s.draft2, s.cluster, "profile", "new-profile", nil, nil, nil, nil, nil)
}

// The control for 0009's named checks: with each dropped, the row TestSourcesConstraints expects it
// to refuse commits, so it is that check, not another, that refuses.
func TestSourcesConstraintControl(t *testing.T) {
	db, _ := installed(t)
	s := sourceRows(t, db)
	for _, c := range []struct {
		drop, q string
		args    []any
	}{
		{"ALTER DOMAIN source_name DROP CONSTRAINT source_name_form", insertFragmentRevision,
			[]any{id.New(id.FragmentRevision), s.cluster, "bad_name", "site", "machine: {}\n", s.human}},
		{"ALTER DOMAIN fragment_layer DROP CONSTRAINT fragment_layer_known", insertFragmentRevision,
			[]any{id.New(id.FragmentRevision), s.cluster, "dns", "machine-intrinsic", "machine: {}\n", s.human}},
		{"ALTER TABLE fragment DROP CONSTRAINT fragment_cluster_scope", insertFragment,
			[]any{id.New(id.Fragment), s.cluster, "library", "site-dns", "site", s.frvSite, 1}},
		{"ALTER TABLE fragment DROP CONSTRAINT fragment_head_revision", insertFragment,
			[]any{id.New(id.Fragment), s.cluster, "cluster", "site-dns", "global", s.frvSite, 1}},
		{"ALTER TABLE profile DROP CONSTRAINT profile_cluster_scope", insertProfile,
			[]any{id.New(id.Profile), s.cluster, "library", "workers2", nil, 1}},
		{"ALTER TABLE draft_source_entry DROP CONSTRAINT draft_source_entry_shape", insertSourceEntry,
			[]any{s.draft2, s.cluster, "profile", "workers", s.machine, nil, nil, nil, 1}},
		{"DROP INDEX draft_source_entry_name", insertSourceEntry,
			[]any{s.draft, s.cluster, "fragment", "registries", nil, s.frv1, nil, nil, 1}},
		{"DROP INDEX draft_source_entry_machine", insertSourceEntry,
			[]any{s.draft, s.cluster, "assignment", nil, s.machine, nil, nil, nil, 1}},
	} {
		func() {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.Exec(c.drop); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(c.q, c.args...); err != nil {
				t.Errorf("after %s: %v; want it to commit", c.drop, err)
			}
		}()
	}
}

// PA §3: a revision's rows are written in the transaction that writes the revision, so its
// content cannot grow once it is committed; an insert for a revision that does not exist is the
// foreign key's to refuse.
func TestSourcesRevisionRowsWithTheirRevision(t *testing.T) {
	db, _ := installed(t)
	s := sourceRows(t, db)
	for _, c := range []stmt{
		{insertFragmentReference, []any{s.frv1, "registry/late", "string", 1, nil, generation(s.cluster, s.claim)}},
		{insertProfilePin, []any{s.prv, s.cluster, 1, s.frv2}},
		{insertAssignmentProfile, []any{s.asr, 1, "late"}},
		{insertAssignmentFragment, []any{s.asr, "global", 0, "late"}},
	} {
		if _, err := db.Exec(c.q, c.args...); sqlState(err) != ImmutableSQLState {
			t.Errorf("%s %v: %v; want SQLSTATE %s", c.q, c.args, err, ImmutableSQLState)
		}
	}
	if _, err := db.Exec(insertProfilePin, id.New(id.ProfileRevision), s.cluster, 0, s.frv1); sqlState(err) != "23503" {
		t.Errorf("pin of no revision: %v; want SQLSTATE 23503", err)
	}

	// A revision another transaction has written but not committed is no revision to this one: the
	// row is refused when it is inserted, never left to the foreign key, which runs at the end of
	// the statement and would accept it had that transaction committed meanwhile. The statement
	// below inserts the late row, then its second row, a pin of the statement's own revision, waits
	// on an advisory lock the barrier holds. The writer commits only once the statement has either
	// refused the first row or reached that barrier, and the barrier is released after the commit.
	ctx := context.Background()
	barrier, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = barrier.Close() }()
	var holder int
	if err := barrier.QueryRowContext(ctx, `SELECT pg_backend_pid() FROM pg_advisory_lock(2309)`).Scan(&holder); err != nil {
		t.Fatal(err)
	}
	writer, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback() }()
	late, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = late.Rollback() }()
	pending, mine := id.New(id.ProfileRevision), id.New(id.ProfileRevision)
	mustExec(t, writer, insertProfileRevision, pending, s.cluster, "pending", s.human)
	mustExec(t, late, insertProfileRevision, mine, s.cluster, "mine", s.human)
	done := make(chan error, 1)
	go func() {
		_, err := late.Exec(`INSERT INTO profile_revision_fragment (revision, cluster, position, fragment_revision)
			SELECT v.r, $3, 0, $4 FROM (VALUES (0, $1::text), (1, $2::text)) v (n, r)
			WHERE v.n = 0 OR (SELECT true FROM pg_advisory_xact_lock(2309))`, pending, mine, s.cluster, s.frv1)
		done <- err
	}()
	var lateErr error
	refused := false
	for deadline := time.Now().Add(5 * time.Second); !refused; time.Sleep(10 * time.Millisecond) {
		select {
		case lateErr = <-done:
			refused = true
			continue
		default:
		}
		var waiting bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1 = ANY (pg_blocking_pids(pid)))`, holder).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the statement neither refused its first row nor reached the barrier")
		}
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := barrier.ExecContext(ctx, `SELECT pg_advisory_unlock(2309)`); err != nil {
		t.Fatal(err)
	}
	if !refused {
		lateErr = <-done
	}
	if sqlState(lateErr) != "23503" {
		t.Errorf("pin of a revision committed during the statement: %v; want SQLSTATE 23503", lateErr)
	}

	// The writer is the top-level transaction, also in a savepoint, and whatever an INSERT supplies.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	saved, forged := id.New(id.ProfileRevision), id.New(id.ProfileRevision)
	mustExec(t, tx, `SAVEPOINT s`)
	mustExec(t, tx, insertProfileRevision, saved, s.cluster, "saved", s.human)
	mustExec(t, tx, `RELEASE SAVEPOINT s`)
	mustExec(t, tx, `SAVEPOINT pin`)
	if _, err := tx.Exec(insertProfilePin, saved, s.cluster, 0, s.frv1); err != nil {
		t.Errorf("pin of a revision written in a savepoint of this transaction: %v", err)
		mustExec(t, tx, `ROLLBACK TO SAVEPOINT pin`)
	}
	mustExec(t, tx, `INSERT INTO profile_revision (id, cluster, name, author, created_at, writer) VALUES ($1, $2, 'forged', $3, now(), '3')`,
		forged, s.cluster, s.human)
	if _, err := tx.Exec(insertProfilePin, forged, s.cluster, 0, s.frv1); err != nil {
		t.Errorf("pin of a revision whose INSERT supplied a writer: %v", err)
	}
}

// PA §3: revisions and their rows are immutable; heads and draft entries are not.
func TestSourcesImmutableTables(t *testing.T) {
	db, _ := installed(t)
	sourceRows(t, db)
	for table, column := range map[string]string{"fragment_revision": "name", "fragment_reference": "version",
		"profile_revision": "name", "profile_revision_fragment": "position", "assignment_revision": "machine",
		"assignment_revision_profile": "position", "assignment_revision_fragment": "position"} {
		for _, stmt := range []string{"UPDATE " + table + " SET " + column + " = " + column, "DELETE FROM " + table, "TRUNCATE " + table + " CASCADE"} {
			if _, err := db.Exec(stmt); sqlState(err) != ImmutableSQLState {
				t.Errorf("%s: %v; want SQLSTATE %s", stmt, err, ImmutableSQLState)
			}
		}
		if count(t, db, table) == 0 {
			t.Errorf("%s: a refused statement removed its rows", table)
		}
	}
	for _, stmt := range []string{"UPDATE fragment SET head_revision = head_revision + 1",
		"UPDATE draft_source_entry SET base = base"} {
		if _, err := db.Exec(stmt); err != nil {
			t.Errorf("%s: %v; heads and entries are mutable", stmt, err)
		}
	}
}
