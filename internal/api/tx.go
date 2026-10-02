package api

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/staging"
)

// maxAttempts is the first try and three retries after a deadlock (§5 rule 5).
const maxAttempts = 4

var (
	errTransient      = errors.New("deadlock retries exhausted")
	errUnavailable    = errors.New("database unavailable; nothing committed")
	errUnknownOutcome = errors.New("commit outcome unknown")
)

// mutate runs a mutating request past the checks that need no transaction, then in one.
func (a *API) mutate(w http.ResponseWriter, q *request) {
	ctx := q.r.Context()
	// No mutating route takes a query (§9.1: unknown fields are refused), and the fingerprint
	// does not cover one, so a query is refused before anything else.
	if q.r.URL.RawQuery != "" {
		a.problem(w, q, refuse(http.StatusBadRequest, "invalid-request", "a mutating request takes no query"))
		return
	}
	q.input = q.route.input()
	canon, err := decodeBody(q.r, q.input, q.route.keyed)
	if err == nil {
		err = q.input.check(a)
	}
	if err != nil {
		a.problem(w, q, refuse(http.StatusBadRequest, "invalid-request", err.Error()))
		return
	}
	q.material = material(q, canon)
	if q.route.keyed == "" {
		q.fingerprint = fingerprint(q, canon)
	} else if ref := a.keyedFingerprint(ctx, q, 0); ref != nil {
		a.problem(w, q, ref)
		return
	}
	if ref := a.admit(ctx, q); ref != nil {
		a.problem(w, q, ref)
		return
	}
	// §7.2: before running anything, look the key up.
	rec, err := lookup(ctx, a.db, q)
	if err != nil {
		a.fail(w, q, fmt.Errorf("%w: %w", errUnavailable, err))
		return
	}
	if rec != nil {
		setEpoch(w, q) // the epoch the replay was decided in
		a.answer(w, q, rec, true)
		return
	}
	if q.route.prepare != nil {
		if err := q.route.prepare(ctx, a, q); err != nil {
			a.fail(w, q, err)
			return
		}
	}
	rec, fresh, err := a.run(ctx, q)
	if err != nil {
		a.fail(w, q, err)
		return
	}
	setEpoch(w, q) // the epoch the transaction committed or found the record in
	a.answer(w, q, rec, !fresh)
	if fresh && rec.afterCommit != nil {
		rec.afterCommit()
	}
}

// keyedFingerprint sets q's fingerprint and its key reference from the provider's HMAC (§7.1),
// under version, or the latest for 0. The provider being unreachable is 503: nothing ran.
func (a *API) keyedFingerprint(ctx context.Context, q *request, version int) *refusal {
	if a.d.ing == nil {
		return refuse(http.StatusServiceUnavailable, "dependency-unavailable", "no provider is configured; nothing was committed")
	}
	in, ok := q.input.(documentInput)
	if !ok {
		a.o.logf("%s: route %s %s is keyed, but its input carries no document", q.id, q.route.method, q.route.pattern)
		return refuse(http.StatusInternalServerError, "internal-error", "nothing was committed")
	}
	var d provider.Digest
	var err error
	if n, ok := in.(interface{ withoutDocument() bool }); ok && n.withoutDocument() {
		d, err = ingest.FingerprintRequest(ctx, a.d.ing.Digest, q.material, version)
	} else {
		d, err = ingest.Fingerprint(ctx, a.d.ing.Digest, q.material, in.document(), version)
	}
	switch {
	case errors.Is(err, ingest.ErrEmptyInput):
		return refuse(http.StatusBadRequest, "invalid-request", "the document is empty")
	case errors.Is(err, provider.ErrUnavailable):
		a.o.logf("%s: fingerprint: %v", q.id, err)
		return refuse(http.StatusServiceUnavailable, "dependency-unavailable", "the provider could not compute the request's fingerprint; nothing was committed")
	case err != nil:
		a.o.logf("%s: fingerprint: %v", q.id, err)
		return refuse(http.StatusInternalServerError, "internal-error", "nothing was committed")
	}
	q.fingerprint, q.fpKey = d.Sum[:], d.KeyRef()
	return nil
}

// admit creates a human's principal row on its first admitted mutating request, in its own short
// transaction (§10). A denied or revoked subject gets 403 and no row.
func (a *API) admit(ctx context.Context, q *request) *refusal {
	if q.principal.ID != "" {
		return nil
	}
	idn, err := auth.EnsureHuman(ctx, a.db, a.denied, q.principal.Issuer, q.principal.Subject)
	switch {
	case errors.Is(err, auth.ErrIdentityRevoked):
		return refuse(http.StatusForbidden, "identity-revoked", "")
	case err != nil:
		a.o.logf("%s: first-use principal: %v", q.id, err)
		return refuse(http.StatusServiceUnavailable, "dependency-unavailable", "the database could not be written; nothing was committed")
	}
	q.principal.ID = idn
	return nil
}

// run is the request's transaction, retried whole after a deadlock (§5 rule 5). fresh reports a
// record this request committed; otherwise rec is one found under the key lock.
func (a *API) run(ctx context.Context, q *request) (rec *record, fresh bool, err error) {
	for n := 1; ; n++ {
		rec, fresh, err = a.attempt(ctx, q, n)
		if !isDeadlock(err) {
			return rec, fresh, err
		}
		a.o.logf("%s: attempt %d deadlocked: %v", q.id, n, err)
		if n == maxAttempts {
			return nil, false, fmt.Errorf("%w: %w", errTransient, err)
		}
	}
}

// connLost reports the database connection lost or refused (§9.4's dependency-unavailable): a
// connection-exception class, an administrator's or the server's shutdown, a broken or closed
// connection, a timeout. A COMMIT it interrupts is resolved by resolveCommit before this is asked.
func connLost(err error) bool {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return strings.HasPrefix(pe.Code, "08") || pe.Code == "57P01" || pe.Code == "57P02" || pe.Code == "57P03"
	}
	var ne net.Error
	return errors.Is(err, driver.ErrBadConn) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		pgconn.Timeout(err) || errors.As(err, &ne)
}

func isDeadlock(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "40P01"
}

func (a *API) attempt(ctx context.Context, q *request, n int) (*record, bool, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", errUnavailable, err)
	}
	defer func() { _ = tx.Rollback() }() // a no-op once committed
	// §7.2: the key's lock is the first statement (rule 5's order), then the lookup again under it,
	// which takes the installation state FOR SHARE for the rest of the transaction.
	if err := a.lockKey(ctx, tx, q); err != nil {
		return nil, false, err
	}
	if rec, err := lookup(ctx, tx, q); err != nil || rec != nil {
		return rec, false, err
	}
	q.actID = id.New(id.Act)
	res, err := q.route.effect(ctx, a, tx, q)
	if err != nil {
		return nil, false, err
	}
	body, err := json.Marshal(res.body)
	if err != nil {
		return nil, false, err
	}
	if !a.o.noActOrder { // the last lock (rule 5), so acts commit in seq order
		if err := auth.LockActOrder(ctx, tx); err != nil {
			return nil, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO act (id, principal, principal_kind, via, role, action, subjects, idempotency_key, request_id, epoch, at)
		VALUES ($1, $2, $3, 'api', $4, $5, string_to_array($6, ','), $7, $8, $9, now())`,
		q.actID, q.principal.ID, string(q.principal.Kind), string(q.role), q.route.action, strings.Join(res.subjects, ","),
		q.key, q.id, q.epoch); err != nil {
		return nil, false, err
	}
	rec := &record{fingerprint: q.fingerprint, fpKey: q.fpKey, current: true, epoch: q.epoch, requestID: q.id, status: res.status,
		location: sql.NullString{String: res.location, Valid: res.location != ""},
		etag:     sql.NullString{String: res.etag, Valid: res.etag != ""}, body: body,
		afterCommit: res.afterCommit}
	if _, err := tx.ExecContext(ctx, `INSERT INTO idempotency_record (principal, key, fingerprint, fingerprint_key, request_id, epoch, status, location, etag, body, operation_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())`,
		q.principal.ID, q.key, rec.fingerprint, rec.fpKey, q.id, q.epoch, rec.status, rec.location, rec.etag, rec.body,
		sql.NullString{String: res.operation, Valid: res.operation != ""}); err != nil {
		return nil, false, err
	}
	if a.o.afterEffect != nil {
		a.o.afterEffect()
	}
	if a.o.beforeCommit != nil {
		if err := a.o.beforeCommit(n); err != nil {
			return nil, false, err
		}
	}
	commit := (*sql.Tx).Commit
	if a.o.commit != nil {
		commit = a.o.commit
	}
	if err := commit(tx); err != nil {
		return a.resolveCommit(q, rec, err)
	}
	return rec, true, nil
}

// lockKey takes the lock of q's principal and key for tx's lifetime (§7.2).
func (a *API) lockKey(ctx context.Context, tx *sql.Tx, q *request) error {
	if a.o.noKeyLock {
		return nil
	}
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, q.principal.ID+"/"+q.key)
	return err
}

// resolveCommit settles a failed COMMIT by reading back (§5 rule 6): the record carries this
// request's id only if the transaction committed. The server may still be committing after the
// client lost the reply, so the read waits on the key lock, which that transaction holds until
// it ends; a wait past the bound leaves the outcome unknown.
func (a *API) resolveCommit(q *request, rec *record, cerr error) (*record, bool, error) {
	// A server answer that is not a connection loss is a definitive rollback, a deferred
	// constraint's violation for one: nothing to read back, and not a database outage. A
	// deadlock stays retryable (run).
	var pe *pgconn.PgError
	if errors.As(cerr, &pe) && !connLost(cerr) {
		return nil, false, cerr
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(q.r.Context()), 10*time.Second)
	defer cancel()
	var n int
	err := func() error {
		tx, err := a.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err := a.lockKey(ctx, tx, q); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM idempotency_record WHERE principal = $1 AND key = $2 AND request_id = $3`,
			q.principal.ID, q.key, q.id).Scan(&n)
	}()
	switch {
	case err != nil:
		return nil, false, fmt.Errorf("%w: commit: %v; read back: %v", errUnknownOutcome, cerr, err)
	case n == 1:
		a.o.logf("%s: commit reported %v, but the record is there: committed", q.id, cerr)
		return rec, true, nil
	default:
		return nil, false, fmt.Errorf("%w: commit: %w", errUnavailable, cerr)
	}
}

// fail answers an error from a mutating request and logs its cause under the request id.
func (a *API) fail(w http.ResponseWriter, q *request, err error) {
	a.o.logf("%s %s %s: %v", q.id, q.r.Method, q.r.URL.EscapedPath(), err)
	var ref *refusal
	switch {
	case errors.As(err, &ref):
	case errors.Is(err, staging.ErrEpochSuperseded):
		ref = refuse(http.StatusServiceUnavailable, "epoch-superseded", "this server started before the current recovery epoch and may issue no ownership; nothing was committed")
	case errors.Is(err, errTransient):
		ref = refuse(http.StatusServiceUnavailable, "transient-conflict", "the request deadlocked on every attempt; nothing was committed")
	case errors.Is(err, errUnknownOutcome):
		ref = refuse(http.StatusInternalServerError, "internal-error", "the outcome is unknown; retrying with the same Idempotency-Key answers it")
	case errors.Is(err, errUnavailable), connLost(err):
		ref = refuse(http.StatusServiceUnavailable, "dependency-unavailable", "the database failed; nothing was committed")
	default:
		ref = refuse(http.StatusInternalServerError, "internal-error", "nothing was committed")
	}
	a.problem(w, q, ref)
}
