package staging

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotPaused refuses a mark on a claim that is not paused for the operator's review
// (compilation §3.6 item 3): live under its run, lapsed for a takeover, or transient.
var ErrNotPaused = fmt.Errorf("%w: the claim is not paused for the operator's review", ErrNotEligible)

// markable is a mark's predicate (compilation §3.6 item 3) over $5, the owner generation the
// mark read under the claim's lock: a paused claim at that generation whose absolute expiry has
// not passed, of the current epoch. Like the owner fence it compares the expiry with the current
// time, after lock.
const markable = `state = 'paused' AND owner_gen = $5 AND expires_at > clock_timestamp()
	AND owner_epoch = (SELECT epoch FROM installation_state)`

// Mark takes a paused claim for an operator's mark, in the caller's transaction (T11): o becomes
// its owner at the next generation with a new lease of lease (never past its absolute expiry),
// the state held and its review still pending, and its running ingest operation's owner fields
// move with it. The eligibility is the UPDATE's predicate; a read after a refused write names the
// reason. The payload and its digest come back for the run to decrypt: a paused claim always
// holds them.
func Mark(ctx context.Context, tx *sql.Tx, o Owner, lease time.Duration, claim string) (Taken, error) {
	return mark(ctx, tx, o, lease, claim, "pending", takeoverOptions{})
}

// Continue takes a paused claim for the operator's continuation (compilation §3.6 item 4) as
// Mark does, and records its review as continued: the run that follows decrypts the envelope and
// runs the draft transaction, and a takeover of the claim does the same instead of pausing again.
func Continue(ctx context.Context, tx *sql.Tx, o Owner, lease time.Duration, claim string) (Taken, error) {
	return mark(ctx, tx, o, lease, claim, "continued", takeoverOptions{})
}

// mark is Mark and Continue: review is the review the taken claim records, pending for a mark
// and continued for a continuation.
func mark(ctx context.Context, tx *sql.Tx, o Owner, lease time.Duration, claim, review string, opts takeoverOptions) (Taken, error) {
	if o.ID == "" || o.Epoch == "" || lease <= 0 {
		return Taken{}, errors.New("staging: a mark needs an owner with its epoch and a positive lease")
	}
	if err := lock(ctx, tx, claim); err != nil {
		return Taken{}, fmt.Errorf("staging: mark: %w", err)
	}
	var gen int64
	switch err := tx.QueryRowContext(ctx, `SELECT owner_gen FROM staging_claim WHERE id = $1`, claim).Scan(&gen); {
	case errors.Is(err, sql.ErrNoRows):
		return Taken{}, ErrNoClaim
	case err != nil:
		return Taken{}, fmt.Errorf("staging: mark: read the claim: %w", err)
	}
	if opts.afterLock != nil {
		opts.afterLock()
	}
	tk := Taken{Claim: Claim{ID: claim, Kind: "import", Mode: "encrypted"}} // only an import stages encrypted, and only encrypted pauses
	var until time.Time
	var digest []byte
	err := tx.QueryRowContext(ctx, `UPDATE staging_claim SET owner = $2, owner_gen = owner_gen + 1, owner_epoch = $3, state = 'held', review = $6,
		lease_until = least(clock_timestamp() + $4::bigint * interval '1 microsecond', expires_at)
		WHERE id = $1 AND $3 = (SELECT epoch FROM installation_state) AND `+markable+`
		RETURNING owner_gen, cluster, machine, review, lease_until, payload, payload_digest`,
		claim, o.ID, o.Epoch, lease.Microseconds(), gen, review).Scan(&tk.Claim.Gen, &tk.Claim.Cluster, &tk.Claim.Machine, &tk.Claim.Review,
		&until, &tk.Payload, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return Taken{}, notMarkable(ctx, tx, o, claim)
	}
	if err != nil {
		return Taken{}, fmt.Errorf("staging: mark: %w", err)
	}
	if err := moveOperation(ctx, tx, o, tk.Claim, until); err != nil {
		return Taken{}, fmt.Errorf("staging: mark: %w", err)
	}
	if err := tk.setDigest(digest); err != nil {
		return Taken{}, fmt.Errorf("staging: mark: %w", err)
	}
	return tk, nil
}

// notMarkable names why a mark by o was refused. It decides no write: the mark's write was
// already refused, under the claim's lock, where only time moves.
func notMarkable(ctx context.Context, tx *sql.Tx, o Owner, claim string) error {
	var state, current string
	var expired, claimEpoch bool
	err := tx.QueryRowContext(ctx, `SELECT state, expires_at <= clock_timestamp(),
		coalesce(owner_epoch = (SELECT epoch FROM installation_state), false), (SELECT epoch FROM installation_state)
		FROM staging_claim WHERE id = $1`, claim).Scan(&state, &expired, &claimEpoch, &current)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNoClaim
	case err != nil:
		return fmt.Errorf("staging: mark: read the claim: %w", err)
	case current != o.Epoch:
		return ErrEpochSuperseded
	case state == "released", state == "abandoned", state == "paused" && expired:
		return ErrEnded
	case state != "paused":
		return ErrNotPaused
	case !claimEpoch:
		return ErrClaimEpoch
	}
	// Paused, unexpired and of the current epoch now, so also at the write before it: under the
	// claim's lock only time moves, and it can only end a claim.
	return errors.New("staging: mark: refused on a claim this read finds markable")
}
