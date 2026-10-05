package staging

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Taken is a takeover's outcome: the claim at the taker's generation with its payload and digest,
// or Payload nil when there was nothing to decrypt, the claim then abandoned.
type Taken struct {
	Claim   Claim
	Payload []byte
	Digest  [32]byte
}

var (
	// ErrNoClaim refuses a takeover of a claim that does not exist.
	ErrNoClaim = errors.New("staging: no such claim")
	// ErrNotEligible is wrapped by every reason a claim cannot be taken over (compilation §3.4).
	ErrNotEligible  = errors.New("staging: the claim cannot be taken over")
	ErrNotEncrypted = fmt.Errorf("%w: only an encrypted claim is taken over", ErrNotEligible)
	ErrEnded        = fmt.Errorf("%w: the ingestion has ended", ErrNotEligible)
	ErrLeaseLive    = fmt.Errorf("%w: the claim's lease has not lapsed", ErrNotEligible)
	ErrClaimEpoch   = fmt.Errorf("%w: the claim predates the current epoch", ErrNotEligible)
)

// eligible is a takeover's predicate (compilation §3.4): an encrypted claim, held or resumed,
// whose lease lapsed and whose absolute expiry has not, of the current epoch. Like the owner
// fence it compares the deadlines with the current time, after lock.
const eligible = `mode = 'encrypted' AND state IN ('held', 'resumed')
	AND lease_until <= clock_timestamp() AND expires_at > clock_timestamp()
	AND owner_epoch = (SELECT epoch FROM installation_state)`

// takeoverOptions are test seams. afterLock runs once the claim is locked, before the write;
// afterWrite runs once the claim's write returned, refused or not; noRecheck is DB row 020's
// control: a read
// before afterLock decides, and the write drops the eligibility terms.
type takeoverOptions struct {
	afterLock  func()
	afterWrite func()
	noRecheck  bool
}

// TakeOver makes o the claim's owner at the next generation, its state resumed with a fresh
// lease of lease (never past its absolute expiry), and moves its running ingest operation's owner
// fields from the generation it took over, in the caller's transaction (T8). The eligibility is
// the UPDATE's predicate; a read after a refused write names the reason. A claim with no payload
// has nothing to decrypt: the same write abandons it under the new generation, and the caller
// fails its operation. The same owner may take its own lapsed claim over.
func TakeOver(ctx context.Context, tx *sql.Tx, o Owner, lease time.Duration, claim string) (Taken, error) {
	return takeOver(ctx, tx, o, lease, claim, takeoverOptions{})
}

func takeOver(ctx context.Context, tx *sql.Tx, o Owner, lease time.Duration, claim string, opts takeoverOptions) (Taken, error) {
	if o.ID == "" || o.Epoch == "" || lease <= 0 {
		return Taken{}, errors.New("staging: a takeover needs an owner with its epoch and a positive lease")
	}
	if err := lock(ctx, tx, claim); err != nil {
		return Taken{}, fmt.Errorf("staging: take over: %w", err)
	}
	pred := eligible
	if opts.noRecheck {
		if err := notEligible(ctx, tx, o, claim); err != nil {
			return Taken{}, err
		}
		pred = "true"
	}
	if opts.afterLock != nil {
		opts.afterLock()
	}
	tk := Taken{Claim: Claim{ID: claim, Kind: "import", Mode: "encrypted"}} // only an import stages encrypted (staging_claim_draft_update_transient)
	var until time.Time
	var digest []byte
	// A claim with no payload is abandoned by this same write: eligibility was settled here, under
	// the lock, so no later fence may refuse it once a deadline passes.
	err := tx.QueryRowContext(ctx, `UPDATE staging_claim SET owner = $2, owner_gen = owner_gen + 1, owner_epoch = $3,
		state = CASE WHEN payload IS NULL THEN 'abandoned' ELSE 'resumed' END,
		lease_until = least(clock_timestamp() + $4::bigint * interval '1 microsecond', expires_at)
		WHERE id = $1 AND $3 = (SELECT epoch FROM installation_state) AND `+pred+`
		RETURNING owner_gen, cluster, machine, lease_until, payload, payload_digest`,
		claim, o.ID, o.Epoch, lease.Microseconds()).Scan(&tk.Claim.Gen, &tk.Claim.Cluster, &tk.Claim.Machine, &until, &tk.Payload, &digest)
	if opts.afterWrite != nil {
		opts.afterWrite()
	}
	if errors.Is(err, sql.ErrNoRows) {
		if err := notEligible(ctx, tx, o, claim); err != nil {
			return Taken{}, err
		}
		// Eligible now, refused at the write: under the claim's lock only time moves, and only a
		// lease can lapse in between (an expiry passing refuses both).
		return Taken{}, ErrLeaseLive
	}
	if err != nil {
		return Taken{}, fmt.Errorf("staging: take over: %w", err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE operation SET owner = $2, owner_gen = $3, owner_epoch = $4, lease_until = $5
		WHERE ingestion = $1 AND state = 'running' AND owner_gen = $3 - 1`, claim, o.ID, tk.Claim.Gen, o.Epoch, until)
	if err != nil {
		return Taken{}, fmt.Errorf("staging: take over: move the operation: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Taken{}, fmt.Errorf("staging: take over: move the operation: %w", err)
	}
	if n != 1 {
		return Taken{}, errors.New("staging: take over: the claim's running operation is not at the generation taken over")
	}
	if tk.Payload == nil {
		return tk, nil
	}
	if len(digest) != len(tk.Digest) {
		return Taken{}, errors.New("staging: take over: the payload digest is not a SHA-256")
	}
	copy(tk.Digest[:], digest)
	return tk, nil
}

// notEligible names why claim cannot be taken over by o now, or returns nil when it can. It
// decides no write: a takeover's write carries its own predicate.
func notEligible(ctx context.Context, tx *sql.Tx, o Owner, claim string) error {
	var mode, state, current string
	var lapsed, expired, claimEpoch bool
	err := tx.QueryRowContext(ctx, `SELECT mode, state, lease_until <= clock_timestamp(), expires_at <= clock_timestamp(),
		coalesce(owner_epoch = (SELECT epoch FROM installation_state), false), (SELECT epoch FROM installation_state)
		FROM staging_claim WHERE id = $1`, claim).Scan(&mode, &state, &lapsed, &expired, &claimEpoch, &current)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNoClaim
	case err != nil:
		return fmt.Errorf("staging: take over: read the claim: %w", err)
	case current != o.Epoch:
		return ErrEpochSuperseded
	case mode != "encrypted":
		return ErrNotEncrypted
	case state != "held" && state != "resumed", expired:
		return ErrEnded
	case !claimEpoch:
		return ErrClaimEpoch
	case !lapsed:
		return ErrLeaseLive
	}
	return nil
}
