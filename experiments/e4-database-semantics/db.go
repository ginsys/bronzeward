package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"syscall"

	"github.com/lib/pq"
	"modernc.org/sqlite"
)

// Dialect is the backend under test. Every place the SQL or the transaction handling differs by
// dialect is a branch on this value, so the dialect-specific cost can be counted from the source
// (`grep -n 'case Postgres\|case SQLite\|== Postgres\|== SQLite'`).
type Dialect int

const (
	Postgres Dialect = iota + 1
	SQLite
)

func (d Dialect) String() string {
	switch d {
	case Postgres:
		return "postgres"
	case SQLite:
		return "sqlite"
	}
	return "unknown"
}

// Rebind turns the portable `?` placeholders into PostgreSQL's `$n`. A `?` inside a single-quoted
// literal is left alone; nothing here writes one outside a test, but the scanner should not guess.
func (d Dialect) Rebind(q string) string {
	if d != Postgres {
		return q
	}
	var b strings.Builder
	n, quoted := 0, false
	for _, r := range q {
		switch {
		case r == '\'':
			quoted = !quoted
			b.WriteRune(r)
		case r == '?' && !quoted:
			n++
			b.WriteString("$" + strconv.Itoa(n))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// DB is a connection pool with its dialect. The embedded *sql.DB's methods take SQL as written;
// Exec, Query and QueryRow rebind placeholders first.
type DB struct {
	*sql.DB
	D Dialect
}

func (db *DB) Exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return db.ExecContext(ctx, db.D.Rebind(q), args...)
}

func (db *DB) Query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return db.QueryContext(ctx, db.D.Rebind(q), args...)
}

func (db *DB) QueryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return db.QueryRowContext(ctx, db.D.Rebind(q), args...)
}

// Tx is a transaction with its dialect, rebinding as DB does.
type Tx struct {
	*sql.Tx
	D Dialect
}

func (db *DB) Begin(ctx context.Context) (*Tx, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &Tx{tx, db.D}, nil
}

func (tx *Tx) Exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return tx.ExecContext(ctx, tx.D.Rebind(q), args...)
}

func (tx *Tx) Query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return tx.QueryContext(ctx, tx.D.Rebind(q), args...)
}

func (tx *Tx) QueryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return tx.QueryRowContext(ctx, tx.D.Rebind(q), args...)
}

// OpenPostgres opens the database named by dsn with lib/pq, the driver E1 used. The DSN carries
// the password, so it comes from the environment and never from argv (see main.go).
func OpenPostgres(ctx context.Context, dsn string) (*DB, error) {
	pool, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.PingContext(ctx); err != nil {
		_ = pool.Close()
		return nil, err
	}
	return &DB{pool, Postgres}, nil
}

// sqliteBusyTimeoutMS is how long a SQLite connection waits for another's write lock before
// SQLITE_BUSY. It is the single-writer cost the report measures, so it is fixed, not tuned per run.
const sqliteBusyTimeoutMS = 5000

// OpenSQLite opens path with the pure-Go modernc driver. Every connection runs in WAL mode with
// foreign keys on, and every transaction begins IMMEDIATE, taking the write lock at BEGIN rather
// than at the first write: a deferred transaction that reads and then writes can fail with
// SQLITE_BUSY at the write however long the busy timeout, because waiting cannot help a reader
// whose snapshot another writer has already moved past.
func OpenSQLite(ctx context.Context, path string) (*DB, error) {
	return openSQLite(ctx, path, sqliteBusyTimeoutMS, "immediate")
}

// openSQLite with txlock "deferred" is the control for the IMMEDIATE default.
func openSQLite(ctx context.Context, path string, busyMS int, txlock string) (*DB, error) {
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyMS))
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Set("_txlock", txlock)
	pool, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if err := pool.PingContext(ctx); err != nil {
		_ = pool.Close()
		return nil, err
	}
	return &DB{pool, SQLite}, nil
}

// resetPostgres empties the public schema, for tests run against the fixture's database.
func resetPostgres(ctx context.Context, db *DB) error {
	_, err := db.ExecContext(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public")
	return err
}

// EngineVersion is what the report cites: sqlite_version() for the linked SQLite, the server's own
// version string for PostgreSQL.
func (db *DB) EngineVersion(ctx context.Context) (string, error) {
	q := "SELECT sqlite_version()"
	if db.D == Postgres {
		q = "SELECT version()"
	}
	var v string
	err := db.QueryRowContext(ctx, q).Scan(&v)
	return v, err
}

// Class is what a scenario needs to know about an error: whether the database refused on a
// constraint, could not take a lock in time, aborted a transaction to keep it serializable, or
// lost the connection. Anything else is ClassOther and ends the scenario.
type Class string

const (
	ClassNone          Class = "none"
	ClassUnique        Class = "unique"
	ClassBusy          Class = "busy"
	ClassSerialization Class = "serialization"
	ClassConn          Class = "conn"
	ClassOther         Class = "other"
)

// SQLite primary result codes; the extended code's low byte is the primary one.
const (
	sqliteBusy       = 5
	sqliteLocked     = 6
	sqliteConstraint = 19
	// Extended constraint codes that mean a duplicate key.
	sqliteConstraintPrimaryKey = 1555
	sqliteConstraintUnique     = 2067
)

func Classify(err error) Class {
	if err == nil {
		return ClassNone
	}
	var pe *pq.Error
	if errors.As(err, &pe) {
		switch pe.Code {
		case "23505":
			return ClassUnique
		case "40001", "40P01":
			return ClassSerialization
		case "55P03":
			return ClassBusy
		case "57P01", "57P02", "57P03":
			return ClassConn
		}
		return ClassOther
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch c := se.Code(); {
		case c == sqliteConstraintUnique || c == sqliteConstraintPrimaryKey:
			return ClassUnique
		case c&0xff == sqliteBusy || c&0xff == sqliteLocked:
			return ClassBusy
		case c&0xff == sqliteConstraint:
			return ClassOther
		}
		return ClassOther
	}
	var ne net.Error
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, sql.ErrConnDone) || errors.As(err, &ne) {
		return ClassConn
	}
	return ClassOther
}
