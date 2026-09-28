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
