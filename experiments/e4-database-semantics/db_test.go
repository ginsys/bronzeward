package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// testDB opens a fresh SQLite database in a temporary directory, or the PostgreSQL database named
// by E4_TEST_PG_DSN when the test is run with one (the fixture's, during a capture). Every test
// below runs against whichever it gets; CI has no PostgreSQL, so there it is SQLite only.
func testDB(t *testing.T) *DB {
	t.Helper()
	ctx := context.Background()
	var (
		db  *DB
		err error
	)
	if dsn := os.Getenv("E4_TEST_PG_DSN"); dsn != "" {
		db, err = OpenPostgres(ctx, dsn)
		if err == nil {
			err = resetPostgres(ctx, db)
		}
	} else {
		db, err = OpenSQLite(ctx, filepath.Join(t.TempDir(), "e4.db"))
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestRebind(t *testing.T) {
	for _, c := range []struct {
		d    Dialect
		in   string
		want string
	}{
		{Postgres, "SELECT 1 WHERE a = ? AND b = ?", "SELECT 1 WHERE a = $1 AND b = $2"},
		{SQLite, "SELECT 1 WHERE a = ? AND b = ?", "SELECT 1 WHERE a = ? AND b = ?"},
		{Postgres, "SELECT '?' , ?", "SELECT '?' , $1"},
	} {
		if got := c.d.Rebind(c.in); got != c.want {
			t.Errorf("%v.Rebind(%q) = %q, want %q", c.d, c.in, got, c.want)
		}
	}
}

func TestClassifyUniqueViolation(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE TABLE t (k TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO t (k) VALUES (?)", "a"); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exec(ctx, "INSERT INTO t (k) VALUES (?)", "a")
	if got := Classify(err); got != ClassUnique {
		t.Fatalf("Classify(%v) = %v, want %v", err, got, ClassUnique)
	}
}

func TestClassifyNil(t *testing.T) {
	if got := Classify(nil); got != ClassNone {
		t.Fatalf("Classify(nil) = %v, want %v", got, ClassNone)
	}
}

func TestSQLiteBusyIsClassified(t *testing.T) {
	if os.Getenv("E4_TEST_PG_DSN") != "" {
		t.Skip("SQLite-only: a writer lock held by another connection")
	}
	path := filepath.Join(t.TempDir(), "busy.db")
	ctx := context.Background()
	a, err := openSQLite(ctx, path, 0, "immediate")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := openSQLite(ctx, path, 0, "immediate")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	tx, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, err = b.BeginTx(ctx, nil)
	if got := Classify(err); got != ClassBusy {
		t.Fatalf("second BEGIN IMMEDIATE: Classify(%v) = %v, want %v", err, got, ClassBusy)
	}
}

func TestEngineVersion(t *testing.T) {
	db := testDB(t)
	v, err := db.EngineVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v == "" {
		t.Fatal("empty engine version")
	}
}
