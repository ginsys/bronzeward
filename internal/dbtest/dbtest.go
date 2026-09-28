// Package dbtest gives each test its own PostgreSQL database, created from BW_TEST_PG_DSN and
// dropped when the test ends. Without that variable a test skips, unless BW_REQUIRE_PG=1, when it
// fails: CI's go-db job sets both, so a database that never came up cannot pass as skipped tests.
package dbtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/ginsys/bronzeward/internal/database"
)

// New returns a connection pool on a fresh, empty database and that database's DSN.
func New(t testing.TB) (*sql.DB, string) {
	t.Helper()
	admin := os.Getenv("BW_TEST_PG_DSN")
	skip, fail := decide(admin, os.Getenv("BW_REQUIRE_PG"))
	if fail {
		t.Fatal("BW_REQUIRE_PG=1 but BW_TEST_PG_DSN is unset")
	}
	if skip {
		t.Skip("BW_TEST_PG_DSN unset")
	}
	u, err := url.Parse(admin)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("BW_TEST_PG_DSN must be a postgres:// URL")
	}
	ctx := context.Background()
	adb, err := database.Open(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	var b [8]byte
	rand.Read(b[:])
	name := "bw_test_" + hex.EncodeToString(b[:])
	if _, err := adb.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		adb.Close()
		t.Fatal(err)
	}
	u.Path = "/" + name
	dsn := u.String()
	db, err := database.Open(ctx, dsn)
	// One cleanup, in order: close the pool, drop the database, close the admin pool.
	t.Cleanup(func() {
		if db != nil {
			db.Close()
		}
		if _, err := adb.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
		adb.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	return db, dsn
}

func decide(dsn, require string) (skip, fail bool) {
	switch {
	case dsn != "":
		return false, false
	case require == "1":
		return false, true
	default:
		return true, false
	}
}
