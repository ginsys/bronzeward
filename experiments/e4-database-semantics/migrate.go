package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The prototype's own migration runner. The fixtures have no migration surface (issue 6, "open
// gap"), so this is what the migration rows exercise. It is evidence for each engine's
// transactional-DDL and locking behaviour, not a candidate v1 migration tool.

//go:embed migrations
var migrationFS embed.FS

// Migration is one file under migrations/<dialect>/, named NNNN_<name>.sql.
type Migration struct {
	Version int
	Name    string
	SQL     string
	// ForeignKeysOff marks a SQLite table rebuild: the runner turns foreign-key enforcement off
	// on the connection before BEGIN (SQLite ignores the pragma inside a transaction) and checks
	// the result with foreign_key_check before COMMIT. The file says so on its first line.
	ForeignKeysOff bool
}

const foreignKeysOffMarker = "-- e4:foreign-keys-off"

func Migrations(d Dialect) ([]Migration, error) {
	dir := path.Join("migrations", d.String())
	entries, err := fs.ReadDir(migrationFS, dir)
	if err != nil {
		return nil, err
	}
	var ms []Migration
	for _, e := range entries {
		name := e.Name()
		num, rest, ok := strings.Cut(strings.TrimSuffix(name, ".sql"), "_")
		if !ok || !strings.HasSuffix(name, ".sql") {
			return nil, fmt.Errorf("%s/%s: not NNNN_<name>.sql", dir, name)
		}
		v, err := strconv.Atoi(num)
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", dir, name, err)
		}
		body, err := fs.ReadFile(migrationFS, path.Join(dir, name))
		if err != nil {
			return nil, err
		}
		text := string(body)
		ms = append(ms, Migration{
			Version:        v,
			Name:           rest,
			SQL:            text,
			ForeignKeysOff: strings.HasPrefix(text, foreignKeysOffMarker+"\n"),
		})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Version < ms[j].Version })
	return ms, nil
}

// MigrateOptions are the runner's measurement hooks. None of them exists in a real runner.
type MigrateOptions struct {
	// Runner names this process in schema_migrations.applied_by.
	Runner string
	// FailAt injects a failing statement after migration FailAt's body, inside its transaction.
	FailAt int
	// HoldAt holds migration HoldAt's transaction open for Hold, after its body and its
	// schema_migrations row and before COMMIT, calling OnHold first: the window a SIGKILL or a
	// concurrent runner is aimed at.
	HoldAt int
	Hold   time.Duration
	OnHold func(version int)
	// NoLock is the control: no PostgreSQL advisory lock around each migration. (SQLite's lock is
	// the IMMEDIATE transaction itself; its control is a pool opened in deferred mode.)
	NoLock bool
}

// migrationLockKey is the PostgreSQL advisory lock every runner takes inside each migration's
// transaction, so that concurrent runners apply each version once and none fails.
const migrationLockKey = 0x6534_6d69 // "e4mi"

// Migrate applies, in order, every migration not yet recorded in schema_migrations, each in its
// own transaction, and returns the versions this call applied. A failure stops at that migration;
// the ones before it stay applied.
func Migrate(ctx context.Context, db *DB, ms []Migration, opt MigrateOptions) ([]int, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := migrateTx(ctx, conn, db.D, opt, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
  version    INTEGER PRIMARY KEY,
  name       TEXT NOT NULL,
  applied_by TEXT NOT NULL
)`)
		return err
	}); err != nil {
		return nil, fmt.Errorf("schema_migrations: %w", err)
	}
	var applied []int
	for _, m := range ms {
		done, err := migrateOne(ctx, conn, db.D, m, opt)
		if err != nil {
			return applied, fmt.Errorf("migration %d (%s): %w", m.Version, m.Name, err)
		}
		if done {
			applied = append(applied, m.Version)
		}
	}
	return applied, nil
}

func migrateOne(ctx context.Context, conn *sql.Conn, d Dialect, m Migration, opt MigrateOptions) (applied bool, err error) {
	if m.ForeignKeysOff && d == SQLite {
		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
			return false, err
		}
		defer func() {
			if _, e := conn.ExecContext(context.WithoutCancel(ctx), "PRAGMA foreign_keys = ON"); e != nil && err == nil {
				err = e
			}
		}()
	}
	err = migrateTx(ctx, conn, d, opt, func(tx *Tx) error {
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM schema_migrations WHERE version = ?", m.Version).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return errSkip
		}
		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			return err
		}
		if opt.FailAt == m.Version {
			if _, err := tx.ExecContext(ctx, "SELECT * FROM e4_injected_failure"); err != nil {
				return fmt.Errorf("injected failure: %w", err)
			}
		}
		if m.ForeignKeysOff && d == SQLite {
			if err := foreignKeyCheck(ctx, tx); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version, name, applied_by) VALUES (?, ?, ?)",
			m.Version, m.Name, opt.Runner); err != nil {
			return err
		}
		if opt.HoldAt == m.Version {
			if opt.OnHold != nil {
				opt.OnHold(m.Version)
			}
			time.Sleep(opt.Hold)
		}
		return nil
	})
	if errors.Is(err, errSkip) {
		return false, nil
	}
	return err == nil, err
}

var errSkip = errors.New("already applied")

// migrateTx runs fn in one transaction on conn, under the advisory lock on PostgreSQL, and
// commits only if fn returns nil.
func migrateTx(ctx context.Context, conn *sql.Conn, d Dialect, opt MigrateOptions, fn func(*Tx) error) error {
	raw, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	tx := &Tx{raw, d}
	if d == Postgres && !opt.NoLock {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(?)", migrationLockKey); err != nil {
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

func foreignKeyCheck(ctx context.Context, tx *Tx) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("foreign_key_check reports a violation after the rebuild")
	}
	return rows.Err()
}
