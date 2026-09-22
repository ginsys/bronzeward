// Package staging is Phase-0 evidence code for the secret-ingress feasibility experiment
// (ginsys/bronzeward issue 2). It is not the v1 implementation.
//
// Design §7.1 names staging as a persistence surface and leaves two alternatives for it
// undifferentiated: a protected transient store, or explicitly encrypted staging. This package
// implements both behind one interface, over one table, so that the comparison is like-for-like
// and the difference between them is visible as behaviour rather than as description.
//
// The difference that matters is not which is "safer". It is what each one makes possible and what
// each one costs:
//
//   - Transient keeps the pending change in process memory. Nothing reaches the write-ahead log
//     or a backup, and after an interruption nobody can resume — not a different principal, not
//     even a restarted process on the same host. The owner's only correct move is to declare the
//     run dead and re-ingest. That is a finding, not a defect.
//   - Encrypted puts a row in the database on purpose, holding ciphertext. A different principal
//     can resume it, and the cost is a dependency on the provider at recovery time, which the
//     experiment measures by cutting the provider off rather than by arguing about it.
//
// The paired result the report is built around comes from those two facts meeting: after a
// recovery the staging row is deleted, and the deleted ciphertext remains in the write-ahead log
// and in any earlier snapshot, which is harmless. The identical delete under the forbidden
// persist-then-redact design leaves plaintext there instead. Same code path, same instrument,
// opposite outcome.
package staging

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/checkpoint"
	"github.com/ginsys/bronzeward/experiments/e1-secret-ingress/internal/secret"
)

// Mode names a staging alternative.
type Mode string

const (
	// ModeTransient is the protected transient store.
	ModeTransient Mode = "transient"
	// ModeEncrypted is explicitly encrypted staging.
	ModeEncrypted Mode = "encrypted"
)

// DefaultLease is how long a claim is good for without a heartbeat. It is short enough that an
// abandoned run does not block the matrix and long enough to survive a hold at a checkpoint.
const DefaultLease = 5 * time.Minute

// State values a claim can be in.
const (
	StateHeld      = "held"
	StateResumed   = "resumed"
	StateReleased  = "released"
	StateAbandoned = "abandoned"
)

// Claim is one staging row.
type Claim struct {
	RunID            string
	Mode             Mode
	OwnerPrincipal   string
	OwnerPID         int
	OwnerStartToken  string
	PayloadSHA256    string
	ResumeCheckpoint string
	State            string
	CreatedAt        time.Time
	HeartbeatAt      time.Time
	ExpiresAt        time.Time
}

// Expired reports whether the claim's lease has run out as of now.
//
// Expiry is evaluated here, at read, rather than only by a sweeper. A sweeper that has not run
// yet leaves an expired claim looking valid, and the window between expiry and the sweep is
// exactly when a second party would pick up a claim it should not have.
func (c Claim) Expired(now time.Time) bool { return now.After(c.ExpiresAt) }

// Staging is a place a pending change waits for review.
type Staging interface {
	// Mode identifies the alternative, for the journal and the report.
	Mode() Mode
	// Hold puts a sanitized change into staging and claims it.
	Hold(ctx context.Context, runID, principal string, s secret.Sanitized, resumeAt checkpoint.Point) (Claim, error)
	// Heartbeat extends the lease.
	Heartbeat(ctx context.Context, runID string) error
	// Resume takes the change back out, as principal. Whether a principal other than the one that
	// held it may do so is the difference between the two modes.
	Resume(ctx context.Context, runID, principal string) (secret.Sanitized, Claim, error)
	// Release ends the claim and removes the payload.
	Release(ctx context.Context, runID string) error
}

// Cipher is the provider capability encrypted staging needs.
type Cipher interface {
	Encrypt(ctx context.Context, keyName string, plaintext []byte) (string, error)
	Decrypt(ctx context.Context, keyName, ciphertext string) ([]byte, error)
}

// ErrNotRecoverable is what transient staging returns for a resume. It is the experiment's result
// for that alternative, expressed as an error a caller must handle rather than as a caveat in a
// document nobody reads at the moment it matters.
var ErrNotRecoverable = errors.New("staging: this run's pending change was held in the memory of a " +
	"process that is gone; no principal can resume it, and the correct action is to declare the run " +
	"dead and re-ingest from the source")

// ErrExpired is returned when a claim's lease has run out.
var ErrExpired = errors.New("staging: the claim's lease has expired")

// ErrNotOwner is returned when a principal that does not hold a claim tries to use it.
var ErrNotOwner = errors.New("staging: the caller does not hold this claim")

// claims is the shared table access both modes use, so ownership and expiry are expressed once.
type claims struct {
	db    *sql.DB
	lease time.Duration
}

// insert writes the claim row.
func (c *claims) insert(ctx context.Context, cl Claim, payload *string) error {
	_, err := c.db.ExecContext(ctx,
		`INSERT INTO staging_claim
		   (run_id, mode, owner_principal, owner_pid, owner_start_token,
		    payload, payload_sha256, resume_checkpoint, state, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		cl.RunID, string(cl.Mode), cl.OwnerPrincipal, nullableInt(cl.OwnerPID),
		nullableString(cl.OwnerStartToken), payload, cl.PayloadSHA256,
		cl.ResumeCheckpoint, cl.State, cl.ExpiresAt)
	if err != nil {
		return fmt.Errorf("staging: claiming %s: %w", cl.RunID, err)
	}
	return nil
}

// read returns the claim and its payload column.
func (c *claims) read(ctx context.Context, runID string) (Claim, *string, error) {
	var (
		cl         Claim
		mode       string
		pid        sql.NullInt64
		startToken sql.NullString
		payload    sql.NullString
	)
	err := c.db.QueryRowContext(ctx,
		`SELECT run_id, mode, owner_principal, owner_pid, owner_start_token,
		        payload, payload_sha256, resume_checkpoint, state,
		        created_at, heartbeat_at, expires_at
		   FROM staging_claim WHERE run_id = $1`, runID).
		Scan(&cl.RunID, &mode, &cl.OwnerPrincipal, &pid, &startToken,
			&payload, &cl.PayloadSHA256, &cl.ResumeCheckpoint, &cl.State,
			&cl.CreatedAt, &cl.HeartbeatAt, &cl.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Claim{}, nil, fmt.Errorf("staging: no claim for run %s", runID)
	}
	if err != nil {
		return Claim{}, nil, fmt.Errorf("staging: reading the claim for %s: %w", runID, err)
	}

	cl.Mode = Mode(mode)
	cl.OwnerPID = int(pid.Int64)
	cl.OwnerStartToken = startToken.String
	if payload.Valid {
		value := payload.String
		return cl, &value, nil
	}
	return cl, nil, nil
}

// setState moves a claim to a terminal state and drops its payload.
func (c *claims) setState(ctx context.Context, runID, state string) error {
	_, err := c.db.ExecContext(ctx,
		`UPDATE staging_claim SET state = $2, payload = NULL WHERE run_id = $1`, runID, state)
	if err != nil {
		return fmt.Errorf("staging: setting %s to %s: %w", runID, state, err)
	}
	return nil
}

// heartbeat extends the lease from the server's clock, not the caller's. A caller whose clock is
// behind would otherwise extend its own claim indefinitely.
func (c *claims) heartbeat(ctx context.Context, runID string) error {
	result, err := c.db.ExecContext(ctx,
		`UPDATE staging_claim
		    SET heartbeat_at = clock_timestamp(), expires_at = clock_timestamp() + $2::interval
		  WHERE run_id = $1 AND state = 'held'`,
		runID, fmt.Sprintf("%d seconds", int(c.lease.Seconds())))
	if err != nil {
		return fmt.Errorf("staging: extending the lease on %s: %w", runID, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("staging: extending the lease on %s: %w", runID, err)
	}
	if n == 0 {
		return fmt.Errorf("staging: no held claim for run %s to extend", runID)
	}
	return nil
}

// Transient is the protected transient store. The pending change lives in this process's memory;
// the table holds only the claim, so nothing of the change reaches the write-ahead log.
type Transient struct {
	claims claims
	// held is the change itself. It is a map rather than a single value so the type behaves the
	// same way under a matrix that runs several ingestions in one process, and it is keyed by run
	// id so a mismatched resume cannot reach another run's change.
	held map[string]secret.Sanitized
}

// NewTransient builds transient staging.
func NewTransient(db *sql.DB, lease time.Duration) *Transient {
	if lease <= 0 {
		lease = DefaultLease
	}
	return &Transient{
		claims: claims{db: db, lease: lease},
		held:   map[string]secret.Sanitized{},
	}
}

// Mode implements Staging.
func (t *Transient) Mode() Mode { return ModeTransient }

// Hold implements Staging. The claim identifies the process, not the principal: this mode's whole
// proposition is that the change cannot outlive the process holding it.
func (t *Transient) Hold(ctx context.Context, runID, principal string, s secret.Sanitized, resumeAt checkpoint.Point) (Claim, error) {
	if !s.Valid() {
		return Claim{}, errors.New("staging: the change did not come from extraction; refusing to stage it")
	}
	token, err := ProcessStartToken()
	if err != nil {
		return Claim{}, err
	}

	sum := sha256.Sum256(s.Document())
	cl := Claim{
		RunID:            runID,
		Mode:             ModeTransient,
		OwnerPrincipal:   principal,
		OwnerPID:         os.Getpid(),
		OwnerStartToken:  token,
		PayloadSHA256:    hex.EncodeToString(sum[:]),
		ResumeCheckpoint: resumeAt.String(),
		State:            StateHeld,
		ExpiresAt:        time.Now().Add(t.claims.lease),
	}
	// payload stays NULL: the change is in memory and must not reach the log.
	if err := t.claims.insert(ctx, cl, nil); err != nil {
		return Claim{}, err
	}
	t.held[runID] = s
	return cl, nil
}

// Heartbeat implements Staging.
func (t *Transient) Heartbeat(ctx context.Context, runID string) error {
	return t.claims.heartbeat(ctx, runID)
}

// Resume implements Staging.
//
// It succeeds only for the same process that held the change, and even then only while the lease
// is good. For any other caller it returns ErrNotRecoverable, which is this alternative's result:
// an interrupted transient run has no recovery owner at all.
func (t *Transient) Resume(ctx context.Context, runID, principal string) (secret.Sanitized, Claim, error) {
	cl, _, err := t.claims.read(ctx, runID)
	if err != nil {
		return secret.Sanitized{}, Claim{}, err
	}
	if cl.State != StateHeld {
		return secret.Sanitized{}, cl, fmt.Errorf("staging: run %s is %s, not held", runID, cl.State)
	}
	if cl.Expired(time.Now()) {
		return secret.Sanitized{}, cl, fmt.Errorf("staging: run %s: %w", runID, ErrExpired)
	}

	held, inMemory := t.held[runID]
	if !inMemory {
		// Either a different process, or this one after a restart. Both are the same situation,
		// and the table cannot tell them apart from the change's point of view: it is gone.
		return secret.Sanitized{}, cl, fmt.Errorf("staging: run %s held by %s (pid %d): %w",
			runID, cl.OwnerPrincipal, cl.OwnerPID, ErrNotRecoverable)
	}

	token, err := ProcessStartToken()
	if err != nil {
		return secret.Sanitized{}, cl, err
	}
	// A PID alone is not an identity: PIDs are reused, and a new process that happened to get the
	// dead owner's PID must not inherit its claim. The start token is what makes that impossible.
	if cl.OwnerPID != os.Getpid() || cl.OwnerStartToken != token {
		return secret.Sanitized{}, cl, fmt.Errorf("staging: run %s: %w", runID, ErrNotRecoverable)
	}
	if cl.OwnerPrincipal != principal {
		return secret.Sanitized{}, cl, fmt.Errorf("staging: run %s held by %s: %w",
			runID, cl.OwnerPrincipal, ErrNotOwner)
	}

	if err := t.claims.setState(ctx, runID, StateResumed); err != nil {
		return secret.Sanitized{}, cl, err
	}
	delete(t.held, runID)
	return held, cl, nil
}

// Release implements Staging.
func (t *Transient) Release(ctx context.Context, runID string) error {
	delete(t.held, runID)
	return t.claims.setState(ctx, runID, StateReleased)
}

// Encrypted is explicitly encrypted staging. The pending change is a row holding ciphertext, which
// is what makes it recoverable by another principal and what puts it in the write-ahead log.
type Encrypted struct {
	claims  claims
	cipher  Cipher
	keyName string
}

// NewEncrypted builds encrypted staging.
func NewEncrypted(db *sql.DB, c Cipher, keyName string, lease time.Duration) (*Encrypted, error) {
	if c == nil {
		return nil, errors.New("staging: encrypted staging needs a cipher")
	}
	if keyName == "" {
		return nil, errors.New("staging: encrypted staging needs a key name")
	}
	if lease <= 0 {
		lease = DefaultLease
	}
	return &Encrypted{
		claims:  claims{db: db, lease: lease},
		cipher:  c,
		keyName: keyName,
	}, nil
}

// Mode implements Staging.
func (e *Encrypted) Mode() Mode { return ModeEncrypted }

// Hold implements Staging. The claim identifies a principal, with no process identity, because
// the point of this mode is that the change outlives the process.
func (e *Encrypted) Hold(ctx context.Context, runID, principal string, s secret.Sanitized, resumeAt checkpoint.Point) (Claim, error) {
	if !s.Valid() {
		return Claim{}, errors.New("staging: the change did not come from extraction; refusing to stage it")
	}
	if principal == "" {
		return Claim{}, errors.New("staging: encrypted staging needs a principal; an unowned claim can be taken by anyone")
	}

	ciphertext, err := e.cipher.Encrypt(ctx, e.keyName, s.Document())
	if err != nil {
		return Claim{}, fmt.Errorf("staging: encrypting the pending change: %w", err)
	}

	sum := sha256.Sum256(s.Document())
	cl := Claim{
		RunID:            runID,
		Mode:             ModeEncrypted,
		OwnerPrincipal:   principal,
		PayloadSHA256:    hex.EncodeToString(sum[:]),
		ResumeCheckpoint: resumeAt.String(),
		State:            StateHeld,
		ExpiresAt:        time.Now().Add(e.claims.lease),
	}
	if err := e.claims.insert(ctx, cl, &ciphertext); err != nil {
		return Claim{}, err
	}
	return cl, nil
}

// Heartbeat implements Staging.
func (e *Encrypted) Heartbeat(ctx context.Context, runID string) error {
	return e.claims.heartbeat(ctx, runID)
}

// Resume implements Staging. A principal other than the one that held the claim may take it, which
// is the capability this mode exists to provide.
//
// It depends on the provider being reachable. The experiment measures that dependency by cutting
// the provider off during a resume rather than by describing it: under a netsplit this returns the
// decryption error and the change stays staged.
func (e *Encrypted) Resume(ctx context.Context, runID, principal string) (secret.Sanitized, Claim, error) {
	cl, payload, err := e.claims.read(ctx, runID)
	if err != nil {
		return secret.Sanitized{}, Claim{}, err
	}
	if cl.State != StateHeld {
		return secret.Sanitized{}, cl, fmt.Errorf("staging: run %s is %s, not held", runID, cl.State)
	}
	// Expiry is checked here, at read. A sweeper that has not run yet leaves an expired claim
	// looking valid, and that window is exactly when a second party would take one it should not.
	if cl.Expired(time.Now()) {
		return secret.Sanitized{}, cl, fmt.Errorf("staging: run %s: %w", runID, ErrExpired)
	}
	if payload == nil {
		return secret.Sanitized{}, cl, fmt.Errorf("staging: run %s has a claim but no payload", runID)
	}
	if principal == "" {
		return secret.Sanitized{}, cl, errors.New("staging: a resume needs a principal")
	}

	body, err := e.cipher.Decrypt(ctx, e.keyName, *payload)
	if err != nil {
		return secret.Sanitized{}, cl, fmt.Errorf("staging: decrypting the pending change for run %s "+
			"(this mode cannot recover without the provider): %w", runID, err)
	}

	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != cl.PayloadSHA256 {
		return secret.Sanitized{}, cl, fmt.Errorf("staging: run %s decrypts to digest %s, claimed as %s",
			runID, got[:12], cl.PayloadSHA256[:12])
	}

	// The resumed change re-enters the program as a Sanitized. That is sound here and nowhere
	// else: what was encrypted was already sanitized, its digest has just been checked against the
	// claim, and the ciphertext came from this program's own Hold.
	resumed := secret.NewSanitized(body, nil)

	if err := e.claims.setState(ctx, runID, StateResumed); err != nil {
		return secret.Sanitized{}, cl, err
	}
	return resumed, cl, nil
}

// Release implements Staging. It sets the payload to NULL, which is the delete the report's paired
// result is about: the ciphertext remains in the write-ahead log and in any earlier snapshot, and
// that is harmless. The same delete under the forbidden persist-then-redact design leaves
// plaintext there instead.
func (e *Encrypted) Release(ctx context.Context, runID string) error {
	return e.claims.setState(ctx, runID, StateReleased)
}

// ProcessStartToken identifies this process beyond its PID.
//
// A PID is not an identity: the kernel reuses them, and a new process that happens to get a dead
// owner's PID must not inherit its claim. Field 22 of /proc/self/stat is the process's start time
// in clock ticks since boot, which no later process with the same PID can match.
//
// On a system without /proc this fails rather than falling back to the PID alone. A weaker
// identity that looks like the real one is worse than no identity: it would make the reuse case
// pass silently, and the experiment would record a resume that should have been refused.
func ProcessStartToken() (string, error) {
	body, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return "", fmt.Errorf("staging: reading this process's start time: %w", err)
	}
	// The second field is the executable name in parentheses and may itself contain spaces and
	// parentheses, so the fields after it are counted from the last ')'.
	closeParen := strings.LastIndex(string(body), ")")
	if closeParen < 0 {
		return "", errors.New("staging: /proc/self/stat is not in the expected form")
	}
	fields := strings.Fields(string(body)[closeParen+1:])
	// After the ')' the first field is state, which is field 3; start time is field 22, so index
	// 22-3 = 19 here.
	const startTimeIndex = 19
	if len(fields) <= startTimeIndex {
		return "", fmt.Errorf("staging: /proc/self/stat has %d fields after the command name, want more than %d",
			len(fields), startTimeIndex)
	}
	if _, err := strconv.ParseUint(fields[startTimeIndex], 10, 64); err != nil {
		return "", fmt.Errorf("staging: /proc/self/stat start time %q is not a number: %w", fields[startTimeIndex], err)
	}
	return fields[startTimeIndex], nil
}

// NewPrincipal returns a random principal identifier, for a recovery run that is deliberately not
// the one that held the claim.
func NewPrincipal(prefix string) (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("staging: generating a principal: %w", err)
	}
	return prefix + "-" + hex.EncodeToString(buf), nil
}

func nullableInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
