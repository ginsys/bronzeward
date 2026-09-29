package auth

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/migrate"
)

// migrated returns a fresh database at the binary's schema, installed.
func migrated(t *testing.T) *sql.DB {
	t.Helper()
	db, _ := dbtest.New(t)
	ms, err := migrate.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Apply(t.Context(), db, ms); err != nil {
		t.Fatal(err)
	}
	if _, _, err := migrate.Install(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

// testAuth is examples/bronzeward.yaml's auth block with issuerURL.
func testAuth(issuerURL string) config.Auth {
	return config.Auth{
		OIDC: config.OIDC{Issuer: issuerURL, Audience: "bronzeward", GroupsClaim: "groups", MaxTokenLifetime: 15 * time.Minute},
		Roles: config.Roles{
			Viewer: []string{"bw-viewers"}, Author: []string{"bw-authors"}, Publisher: []string{"bw-publishers"},
			Approver: []string{"bw-approvers"}, RecoveryAdmin: []string{"bw-recovery"},
		},
	}
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// newEpoch records a new current epoch, as a recovery-mode entry after a restore does (§12.1):
// every token issued before it is from an earlier epoch.
func newEpoch(t *testing.T, db *sql.DB) {
	t.Helper()
	e := id.New(id.Epoch)
	mustExec(t, db, "INSERT INTO recovery_epoch (epoch, entered_at) VALUES ($1, now())", e)
	mustExec(t, db, "UPDATE installation_state SET epoch = $1", e)
}

func sqlState(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}
