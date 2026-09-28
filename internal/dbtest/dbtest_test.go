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
