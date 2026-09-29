// Package dbtest gives each test its own PostgreSQL database, created from BW_TEST_PG_DSN (a
// postgres:// URL naming its database in the path, never in a query parameter) and dropped when
// the test ends. Without that variable a test skips, unless BW_REQUIRE_PG=1, when it
// fails: CI's go-db job sets both, so a database that never came up cannot pass as skipped tests.
package dbtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

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
	var b [8]byte
	rand.Read(b[:])
	name := "bw_test_" + hex.EncodeToString(b[:])
	dsn, err := testDSN(admin, name)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adb, err := database.Open(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adb.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		adb.Close()
		t.Fatal(err)
	}
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

// WaitForLockWait polls until some session of this database waits on a lock.
func WaitForLockWait(t testing.TB, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no session started waiting on a lock within 10s")
}

// testDSN is admin with its database replaced by name. It refuses a DSN where pgx would connect
// to another database, as a dbname or database query parameter makes it: the tests would then
// share that database while cleanup dropped the unused fresh one.
func testDSN(admin, name string) (string, error) {
	u, err := url.Parse(admin)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", errors.New("BW_TEST_PG_DSN must be a postgres:// URL")
	}
	u.Path = "/" + name
	dsn := u.String()
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil { // withheld, as database.Open does: the parser's message can quote the password
		return "", errors.New("BW_TEST_PG_DSN does not parse")
	}
	if cfg.Database != name {
		return "", errors.New("BW_TEST_PG_DSN must name its database in the URL path, not in a dbname or database query parameter: each test replaces the path with its own database")
	}
	return dsn, nil
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
