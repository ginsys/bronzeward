package dbtest

import "testing"

func TestDecide(t *testing.T) {
	for _, c := range []struct {
		dsn, require string
		skip, fail   bool
	}{
		{"postgres://x", "", false, false},
		{"postgres://x", "1", false, false},
		{"", "", true, false},
		{"", "1", false, true},
		{"", "0", true, false},
	} {
		skip, fail := decide(c.dsn, c.require)
		if skip != c.skip || fail != c.fail {
			t.Errorf("decide(%q, %q) = %v, %v; want %v, %v", c.dsn, c.require, skip, fail, c.skip, c.fail)
		}
	}
}

// The fresh database goes in the path; a DSN whose query names a database would override it in
// pgx, so every test would run against that shared database while cleanup dropped the fresh one.
func TestTestDSN(t *testing.T) {
	const name = "bw_test_0123456789abcdef"
	for _, c := range []struct {
		admin, want string
	}{
		{"postgres://u@h:5432/bronzeward?sslmode=disable", "postgres://u@h:5432/" + name + "?sslmode=disable"},
		{"postgresql://u@h/", "postgresql://u@h/" + name},
		{"postgres://u@h:5432/?dbname=bronzeward&sslmode=disable", ""},
		{"postgres://u@h:5432/bronzeward?database=bronzeward", ""},
		{"postgres://u@h/?sslmode=disable&db%6Eame=bronzeward", ""},
		{"mysql://u@h/bronzeward", ""},
		{"host=h dbname=bronzeward", ""},
	} {
		got, err := testDSN(c.admin, name)
		switch {
		case c.want == "" && err == nil:
			t.Errorf("testDSN(%q) = %q; want a refusal", c.admin, got)
		case c.want != "" && (err != nil || got != c.want):
			t.Errorf("testDSN(%q) = %q, %v; want %q", c.admin, got, err, c.want)
		}
	}
}

func TestNewGivesAnEmptyDatabase(t *testing.T) {
	db, dsn := New(t)
	var n int
	if err := db.QueryRow("SELECT count(*) FROM pg_tables WHERE schemaname = 'public'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("fresh database: %d tables, %v", n, err)
	}
	if dsn == "" {
		t.Fatal("no DSN returned")
	}
}

// A copy is the database as it was when copied: a row written to the source afterwards is not in
// it, its sequences continue from where the source's were then, and the source keeps working.
func TestCopyIsABackup(t *testing.T) {
	db, _ := New(t)
	for _, q := range []string{`CREATE TABLE t (n bigint GENERATED ALWAYS AS IDENTITY, v text)`,
		`INSERT INTO t (v) VALUES ('before')`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	restored := Copy(t, db)
	var after int64
	if err := db.QueryRow(`INSERT INTO t (v) VALUES ('after') RETURNING n`).Scan(&after); err != nil {
		t.Fatalf("source after the copy: %v", err)
	}
	var vs string
	if err := restored.QueryRow(`SELECT string_agg(v, ',' ORDER BY n) FROM t`).Scan(&vs); err != nil || vs != "before" {
		t.Fatalf("copy holds %q, %v; want before", vs, err)
	}
	var again int64
	if err := restored.QueryRow(`INSERT INTO t (v) VALUES ('again') RETURNING n`).Scan(&again); err != nil || again != after {
		t.Fatalf("copy issued %d, %v; want %d, the number the source issued after the copy", again, err, after)
	}
}
