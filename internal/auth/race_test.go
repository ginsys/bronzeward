package auth

import (
	"errors"
	"testing"

	"github.com/ginsys/bronzeward/internal/dbtest"
)

// rotateHeld starts a rotation that stops after revoking the unrevoked token, and returns once it
// is stopped there. Closing release lets it continue; done receives its result.
func rotateHeld(t *testing.T, s *Store, identity string) (release chan struct{}, done chan error) {
	t.Helper()
	held := make(chan struct{})
	release, done = make(chan struct{}), make(chan error, 1)
	s.o.afterRevokeOld = func() { close(held); <-release }
	go func() {
		_, err := s.Rotate(t.Context(), identity, nil, DefaultExpiry, "h-operator")
		done <- err
	}()
	select {
	case <-held:
	case err := <-done:
		t.Fatalf("rotation ended before its hook: %v", err)
	}
	return release, done
}

func rotate(t *testing.T, s *Store, identity string) chan error {
	done := make(chan error, 1)
	go func() {
		_, err := s.Rotate(t.Context(), identity, nil, DefaultExpiry, "h-operator")
		done <- err
	}()
	return done
}

// A rotation that began before another but took the lock after it replaces that one's token, so
// its grant is the current one although its issued_at (the transaction's start) is the earlier.
// A later rotation without -roles inherits that grant, not the replaced token's.
func TestRotationInheritsTheLastIssuedGrant(t *testing.T) {
	db := migrated(t)
	is := issued(t, storeFor(db), "ci")
	started, release, first := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	a := storeFor(db)
	a.o.beforeLock = func() { close(started); <-release }
	go func() {
		_, err := a.Rotate(t.Context(), is.Identity, []Role{Viewer}, DefaultExpiry, "h-operator")
		first <- err
	}()
	<-started
	if _, err := storeFor(db).Rotate(t.Context(), is.Identity, []Role{Publisher}, DefaultExpiry, "h-operator"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT count(*) FROM automation_token t WHERE owner = $1 AND revoked_at IS NULL
		AND issued_at < (SELECT max(issued_at) FROM automation_token WHERE owner = $1)`, is.Identity); n != 1 {
		t.Fatal("the race did not happen: the current token is not the earlier-stamped one")
	}
	if _, err := storeFor(db).Rotate(t.Context(), is.Identity, nil, DefaultExpiry, "h-operator"); err != nil {
		t.Fatal(err)
	}
	var roles string
	if err := db.QueryRow(`SELECT array_to_string(roles, ',') FROM automation_token
		WHERE owner = $1 AND revoked_at IS NULL`, is.Identity).Scan(&roles); err != nil {
		t.Fatal(err)
	}
	if roles != "viewer" {
		t.Fatalf("rotation without -roles granted %q; want viewer, the grant of the token it replaced", roles)
	}
}

// §10.2, §16: two rotations of one identity serialize on the principal lock; the second replaces
// the first's token.
func TestConcurrentRotationsSerialize(t *testing.T) {
	db := migrated(t)
	is := issued(t, storeFor(db), "ci")
	release, first := rotateHeld(t, storeFor(db), is.Identity)
	second := rotate(t, storeFor(db), is.Identity)
	dbtest.WaitForLockWait(t, db)
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if n := unrevoked(t, db, is.Identity); n != 1 {
		t.Fatalf("%d unrevoked tokens after two rotations", n)
	}
}

// The control: without the principal lock, the second rotation waits on the token row the first
// revoked, finds nothing left to revoke, and its insert hits the one-unrevoked index.
func TestConcurrentRotationsNoLockControl(t *testing.T) {
	db := migrated(t)
	is := issued(t, storeFor(db), "ci")
	a, b := storeFor(db), storeFor(db)
	a.o.noPrincipalLock, b.o.noPrincipalLock = true, true
	release, first := rotateHeld(t, a, is.Identity)
	second := rotate(t, b, is.Identity)
	dbtest.WaitForLockWait(t, db)
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; sqlState(err) != "23505" {
		t.Fatalf("second rotation without the lock: %v; want 23505 from the one-unrevoked index", err)
	}
}

// The index's control: with neither the lock nor the index, both rotations succeed and two
// tokens are valid.
func TestConcurrentRotationsNoLockNoIndexControl(t *testing.T) {
	db := migrated(t)
	is := issued(t, storeFor(db), "ci")
	mustExec(t, db, "DROP INDEX automation_token_one_unrevoked")
	a, b := storeFor(db), storeFor(db)
	a.o.noPrincipalLock, b.o.noPrincipalLock = true, true
	release, first := rotateHeld(t, a, is.Identity)
	second := rotate(t, b, is.Identity)
	dbtest.WaitForLockWait(t, db)
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if n := unrevoked(t, db, is.Identity); n != 2 {
		t.Fatalf("control: %d unrevoked tokens; without lock and index there should be 2", n)
	}
}

// §10.2: a rotation that holds the lock first commits its token; the revocation waits, then
// revokes it.
func TestRotationThenIdentityRevocation(t *testing.T) {
	db := migrated(t)
	is := issued(t, storeFor(db), "ci")
	release, rotated := rotateHeld(t, storeFor(db), is.Identity)
	revoked := make(chan error, 1)
	go func() { revoked <- revokeIdentity(t, db, is.Identity) }()
	dbtest.WaitForLockWait(t, db)
	close(release)
	if err := <-rotated; err != nil {
		t.Fatal(err)
	}
	if err := <-revoked; err != nil {
		t.Fatal(err)
	}
	if n := unrevoked(t, db, is.Identity); n != 0 {
		t.Fatalf("%d valid tokens after the revocation", n)
	}
}

// §10.2: a revocation that holds the lock first makes the waiting rotation refuse.
func TestIdentityRevocationThenRotation(t *testing.T) {
	db := migrated(t)
	is := issued(t, storeFor(db), "ci")
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := RevokeIdentity(t.Context(), tx, is.Identity); err != nil {
		t.Fatal(err)
	}
	rotated := rotate(t, storeFor(db), is.Identity)
	dbtest.WaitForLockWait(t, db)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-rotated; !errors.Is(err, ErrIdentityRevoked) {
		t.Fatalf("rotation after the revocation: %v; want ErrIdentityRevoked", err)
	}
	if n := unrevoked(t, db, is.Identity); n != 0 {
		t.Fatalf("%d valid tokens after the revocation", n)
	}
}

// The control: without the principal lock, the rotation reads the identity unrevoked, waits only
// on the token row, and commits an unrevoked token after the revocation. Authentication still
// refuses it (the owner is revoked); the control shows issuance the lock exists to prevent.
func TestRevocationRaceNoLockControl(t *testing.T) {
	db := migrated(t)
	is := issued(t, storeFor(db), "ci")
	s := storeFor(db)
	s.o.noPrincipalLock = true
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := RevokeIdentity(t.Context(), tx, is.Identity); err != nil {
		t.Fatal(err)
	}
	rotated := rotate(t, s, is.Identity)
	dbtest.WaitForLockWait(t, db)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-rotated; err != nil {
		t.Fatalf("control: the rotation without the lock was refused: %v", err)
	}
	if n := unrevoked(t, db, is.Identity); n != 1 {
		t.Fatalf("control: %d unrevoked tokens issued after the revocation; without the lock there should be 1", n)
	}
}

// now() is the transaction's start. A revocation whose transaction began before the rotation it
// then waits on writes a revoked_at earlier than the new token's issued_at; nothing may refuse
// that (0002 has no revoked_at-after-issued_at CHECK for this reason).
func TestRevocationBegunBeforeRotation(t *testing.T) {
	db := migrated(t)
	is := issued(t, storeFor(db), "ci")
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT 1`); err != nil { // fixes tx's now()
		t.Fatal(err)
	}
	if _, err := storeFor(db).Rotate(t.Context(), is.Identity, nil, DefaultExpiry, "h-operator"); err != nil {
		t.Fatal(err)
	}
	if _, err := RevokeIdentity(t.Context(), tx, is.Identity); err != nil {
		t.Fatalf("revocation begun before the rotation: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n := unrevoked(t, db, is.Identity); n != 0 {
		t.Fatalf("%d unrevoked tokens after the revocation", n)
	}
}

// The control: with the ordering CHECK the earlier draft carried, the same schedule fails 23514
// and the identity stays active.
func TestRevocationBegunBeforeRotationCheckControl(t *testing.T) {
	db := migrated(t)
	mustExec(t, db, `ALTER TABLE automation_token ADD CONSTRAINT revoked_after_issued CHECK (revoked_at >= issued_at)`)
	is := issued(t, storeFor(db), "ci")
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := storeFor(db).Rotate(t.Context(), is.Identity, nil, DefaultExpiry, "h-operator"); err != nil {
		t.Fatal(err)
	}
	if _, err := RevokeIdentity(t.Context(), tx, is.Identity); sqlState(err) != "23514" {
		t.Fatalf("control: %v; want 23514 from the ordering CHECK", err)
	}
}
