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

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ginsys/bronzeward/internal/id"
)

//go:embed migrations/*.sql
var embedded embed.FS

// Migration is one file migrations/NNNN_<name>.sql. Checksum is the hex SHA-256 of the file.
// Name and Checksum are recorded when it is applied and compared at every later run and at
// server start.
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

// Check refuses unless schema_migrations holds exactly ms with matching names and checksums and
// the installation is recorded (§11 rule 2). The server calls it before serving; it never migrates.
func Check(ctx context.Context, db *sql.DB, ms []Migration) error {
	recorded, err := recordedMigrations(ctx, db)
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "42P01" { // undefined_table
			return errors.New("schema: the database has no schema_migrations table; run bronzeward migrate")
		}
		return fmt.Errorf("schema: %w", err)
	}
	var missing, edited, unknown []int
	for _, m := range ms {
		r, ok := recorded[m.Version]
		switch {
		case !ok:
			missing = append(missing, m.Version)
		case r.name != m.Name || r.checksum != m.Checksum:
			edited = append(edited, m.Version)
		}
		delete(recorded, m.Version)
	}
	for v := range recorded {
		unknown = append(unknown, v)
	}
	slices.Sort(unknown)
	var problems []string
	if len(missing) > 0 {
		problems = append(problems, fmt.Sprintf("migrations %v are not applied: the schema is older than this binary; run bronzeward migrate", missing))
	}
	if len(unknown) > 0 {
		problems = append(problems, fmt.Sprintf("migrations %v are applied but unknown to this binary: the schema is newer than this binary", unknown))
	}
	if len(edited) > 0 {
		problems = append(problems, fmt.Sprintf("migrations %v were applied with another name or checksum: edited after they were applied", edited))
	}
	if len(problems) > 0 {
		return fmt.Errorf("schema: %s", strings.Join(problems, "; "))
	}
	// A migrate run stopped between Apply and Install leaves schema_version behind (§12.1).
	var version int
	err = db.QueryRowContext(ctx, "SELECT schema_version FROM installation_state").Scan(&version)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return errors.New("schema: no installation is recorded; run bronzeward migrate")
	case err != nil:
		return fmt.Errorf("schema: %w", err)
	case version != len(ms):
		return fmt.Errorf("schema: the installation records schema version %d, the migrations reach %d; run bronzeward migrate", version, len(ms))
	}
	return nil
}

// recordedRow is one schema_migrations row's identity beyond its version.
type recordedRow struct{ name, checksum string }

func recordedMigrations(ctx context.Context, db *sql.DB) (map[int]recordedRow, error) {
	rows, err := db.QueryContext(ctx, "SELECT version, name, checksum FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]recordedRow{}
	for rows.Next() {
		var v int
		var r recordedRow
		if err := rows.Scan(&v, &r.name, &r.checksum); err != nil {
			return nil, err
		}
		out[v] = r
	}
	return out, rows.Err()
}

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
// database holding a migration the binary does not know, or one whose recorded name or checksum
// differs. A failure stops at that migration; the ones before it stay applied.
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
	var r recordedRow
	err := tx.QueryRowContext(ctx, "SELECT name, checksum FROM schema_migrations WHERE version = $1", m.Version).Scan(&r.name, &r.checksum)
	switch {
	case err == nil && (r.name != m.Name || r.checksum != m.Checksum):
		return fmt.Errorf("recorded as %s with checksum %s, this binary's is %s with checksum %s: edited after it was applied",
			r.name, r.checksum, m.Name, m.Checksum)
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

// ImmutableSQLState is the SQLSTATE an immutable table's trigger raises (0001_foundation.sql).
const ImmutableSQLState = "BW001"

// Install records the installation once (§12.1): the first run mints its epoch, with that
// epoch's recovery_epoch row, and every run sets schema_version to the highest applied
// migration. Run it after Apply; it takes the same lock, so concurrent runs mint one epoch.
func Install(ctx context.Context, db *sql.DB) (epoch string, created bool, err error) {
	return install(ctx, db, options{})
}

func install(ctx context.Context, db *sql.DB, o options) (string, bool, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return "", false, fmt.Errorf("migrate: install: %w", err)
	}
	defer conn.Close()
	var epoch string
	var created bool
	err = inTx(ctx, conn, o, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, "SELECT epoch FROM installation_state FOR UPDATE").Scan(&epoch)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if o.afterRead != nil {
				o.afterRead()
			}
			epoch, created = id.New(id.Epoch), true
			if _, err := tx.ExecContext(ctx, "INSERT INTO recovery_epoch (epoch, entered_at) VALUES ($1, now())", epoch); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO installation_state (epoch, schema_version) SELECT $1, max(version) FROM schema_migrations", epoch); err != nil {
				return err
			}
			return nil
		case err != nil:
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE installation_state SET schema_version = (SELECT max(version) FROM schema_migrations)")
		return err
	})
	if err != nil {
		return "", false, fmt.Errorf("migrate: install: %w", err)
	}
	return epoch, created, nil
}
