// Package migrate applies the schema migrations embedded in the binary, records the
// installation epoch, and checks at server start that the database holds exactly the binary's
// migrations (persistence-api.md §11, §12.1).
package migrate

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var embedded embed.FS

// Migration is one file migrations/NNNN_<name>.sql. Checksum is the hex SHA-256 of the file,
// recorded when it is applied and compared at every later run and at server start.
type Migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string
}

// Embedded returns the binary's migrations in version order.
func Embedded() ([]Migration, error) { return load(embedded, "migrations") }

// load refuses anything but NNNN_<name>.sql files numbered 1, 2, 3... without gaps.
func load(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("migrations: %w", err)
	}
	var ms []Migration
	for _, e := range entries {
		name := e.Name()
		base, isSQL := strings.CutSuffix(name, ".sql")
		num, rest, ok := strings.Cut(base, "_")
		v, err := strconv.Atoi(num)
		if !isSQL || !ok || len(num) != 4 || rest == "" || err != nil || v < 1 {
			return nil, fmt.Errorf("migrations: %s is not NNNN_<name>.sql with NNNN from 0001", name)
		}
		body, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("migrations: %w", err)
		}
		sum := sha256.Sum256(body)
		ms = append(ms, Migration{Version: v, Name: rest, SQL: string(body), Checksum: hex.EncodeToString(sum[:])})
	}
	if len(ms) == 0 {
		return nil, fmt.Errorf("migrations: none in %s", dir)
	}
	slices.SortFunc(ms, func(a, b Migration) int { return cmp.Compare(a.Version, b.Version) })
	for i, m := range ms {
		if m.Version != i+1 {
			return nil, fmt.Errorf("migrations: version %d where %d was expected; versions run from 1 without gaps or repeats", m.Version, i+1)
		}
	}
	return ms, nil
}

// Check is completed in Task 5.
func Check(ctx context.Context, db *sql.DB, ms []Migration) error { return errors.New("unimplemented") }

// lockKey is the advisory lock every migrate run takes inside each of its transactions (§11,
// T10), so concurrent runs apply each version once.
const lockKey int64 = 0x62776d69 // "bwmi"

const createSchemaMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
  version    integer PRIMARY KEY,
  name       text NOT NULL,
  checksum   text NOT NULL,
  applied_at timestamptz NOT NULL
)`

// options are test hooks and the lock control; the zero value is production behaviour.
type options struct {
	noLock    bool                   // the control: no advisory lock
	afterBody func(version, pid int) // inside a migration's transaction, before COMMIT
	afterRead func()                 // inside Install's transaction, after it found no installation
}

var errSkip = errors.New("already applied")

// Apply applies, in order, each migration not yet recorded, each in its own transaction on one
// connection under the advisory lock, and returns the versions this call applied. It refuses a
// database holding a migration the binary does not know, or one whose recorded checksum differs.
// A failure stops at that migration; the ones before it stay applied.
func Apply(ctx context.Context, db *sql.DB, ms []Migration) ([]int, error) {
	return apply(ctx, db, ms, options{})
}

func apply(ctx context.Context, db *sql.DB, ms []Migration, o options) ([]int, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	defer conn.Close()
	err = inTx(ctx, conn, o, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, createSchemaMigrations); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
		if err != nil {
			return err
		}
		defer rows.Close()
		var unknown []int
		for rows.Next() {
			var v int
			if err := rows.Scan(&v); err != nil {
				return err
			}
			if !slices.ContainsFunc(ms, func(m Migration) bool { return m.Version == v }) {
				unknown = append(unknown, v)
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(unknown) > 0 {
			return fmt.Errorf("the database holds migrations %v this binary does not know: it is newer than this binary", unknown)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	var applied []int
	for _, m := range ms {
		err := inTx(ctx, conn, o, func(tx *sql.Tx) error { return applyOne(ctx, tx, m, o) })
		switch {
		case errors.Is(err, errSkip):
		case err != nil:
			return applied, fmt.Errorf("migrate: migration %d (%s): %w", m.Version, m.Name, err)
		default:
			applied = append(applied, m.Version)
		}
	}
	return applied, nil
}

func applyOne(ctx context.Context, tx *sql.Tx, m Migration, o options) error {
	var sum string
	err := tx.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE version = $1", m.Version).Scan(&sum)
	switch {
	case err == nil && sum != m.Checksum:
		return fmt.Errorf("recorded with checksum %s, this binary's is %s: edited after it was applied", sum, m.Checksum)
	case err == nil:
		return errSkip
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	// No arguments: pgx sends the file over the simple protocol, which runs every statement in it.
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES ($1, $2, $3, now())",
		m.Version, m.Name, m.Checksum); err != nil {
		return err
	}
	if o.afterBody != nil {
		var pid int
		if err := tx.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			return err
		}
		o.afterBody(m.Version, pid)
	}
	return nil
}

// inTx runs fn in one transaction on conn under the advisory lock, committing only if fn
// returns nil.
func inTx(ctx context.Context, conn *sql.Conn, o options, fn func(*sql.Tx) error) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if !o.noLock {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", lockKey); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
