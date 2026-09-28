// Package database opens Bronzeward's PostgreSQL database through pgx's database/sql driver
// (persistence-api.md: read committed, the default, is the only isolation level used).
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Open connects to dsn and checks the connection. Errors never carry the DSN's password: a parse
// error is replaced by a fixed message (pgx's redaction is best effort and misses
// "password = x"), and a connection error names only user, database and host.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("database: the DSN does not parse (the parser's message is withheld: it can quote the password)")
	}
	db := stdlib.OpenDB(*cfg)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("database: %w", err)
	}
	return db, nil
}
