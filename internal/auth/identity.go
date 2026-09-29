package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ginsys/bronzeward/internal/id"
)

// EnsureHuman returns the principal of the human (iss, sub), inserting it in its own short
// transaction if it has none (persistence-api.md §10). Of two concurrent callers, one inserts and
// the other reads the committed row. A subject that deniedSubjects lists gets no row, and a
// revoked one is refused (§10.4).
func EnsureHuman(ctx context.Context, db *sql.DB, d Denied, iss, sub string) (string, error) {
	if sub == "" {
		return "", errors.New("a human needs a subject")
	}
	if d.Human(iss, sub) {
		return "", fmt.Errorf("%w: %s is in deniedSubjects", ErrIdentityRevoked, sub)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO principal (id, kind, iss, sub, created_at)
		VALUES ($1, 'human', $2, $3, now()) ON CONFLICT (iss, sub) DO NOTHING`, id.New(id.Principal), iss, sub); err != nil {
		return "", err
	}
	var idn string
	var revoked bool
	if err := db.QueryRowContext(ctx, `SELECT id, revoked FROM principal WHERE kind = 'human' AND iss = $1 AND sub = $2`,
		iss, sub).Scan(&idn, &revoked); err != nil {
		return "", err
	}
	if revoked {
		return "", fmt.Errorf("%w: %s", ErrIdentityRevoked, idn)
	}
	return idn, nil
}

// RevokeIdentity marks identity revoked and revokes its unrevoked token, in tx, after locking the
// principal row FOR UPDATE (§10.4, T5c). Token-issuing transactions take the same lock first, so
// a rotation either precedes the revocation, its token then revoked here, or waits and is
// refused. The caller's transaction records the revocation row and act (PR 4's route). already
// reports an identity revoked before.
func RevokeIdentity(ctx context.Context, tx *sql.Tx, identity string) (already bool, err error) {
	if err := tx.QueryRowContext(ctx, `SELECT revoked FROM principal WHERE id = $1 FOR UPDATE`, identity).Scan(&already); err != nil {
		return false, err
	}
	if already {
		return true, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE principal SET revoked = true WHERE id = $1`, identity); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE automation_token SET revoked_at = now() WHERE owner = $1 AND revoked_at IS NULL`, identity); err != nil {
		return false, err
	}
	return false, nil
}

func inTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	return inTxCommit(ctx, db, fn, (*sql.Tx).Commit)
}

// errCommit marks an error from COMMIT: the transaction may have committed all the same.
var errCommit = errors.New("commit")

func inTxCommit(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error, commit func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := commit(tx); err != nil {
		return fmt.Errorf("%w: %w", errCommit, err)
	}
	return nil
}
