package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/id"
)

// Automation tokens (persistence-api.md §10.2): bwt_<tok id>.<secret>, the secret 256 random
// bits in unpadded base64url. Only its SHA-256 is stored.
const tokenPrefix = "bwt_"

const (
	DefaultExpiry = 30 * 24 * time.Hour // choice §17.17
	MaxExpiry     = 90 * 24 * time.Hour // 0002's CHECK says 2160 hours
)

// Issued is a new token. Token is shown once and never stored.
type Issued struct {
	Identity string
	TokenID  string
	Token    string
	Expires  time.Time
}

// Store is the token tool's database side: the only way to issue, rotate, list or revoke
// tokens (§10.2). Every transaction that issues or revokes a token first locks the identity's
// principal row FOR UPDATE, the lock identity revocation takes.
type Store struct {
	db     *sql.DB
	issuer string // operators and responsible humans are this issuer's subjects
	denied Denied
	o      storeOptions
}

// storeOptions are test hooks and the lock control; the zero value is production behaviour.
type storeOptions struct {
	noPrincipalLock bool                // the control: no FOR UPDATE on the principal row
	afterRevokeOld  func()              // in an issuing or revoking transaction, after revoking the unrevoked token
	beforeLock      func()              // in a token tool transaction, after it began and before the principal locks
	commit          func(*sql.Tx) error // replaces tx.Commit in issuing transactions
}

// issuing runs fn, which issues a token, in a transaction. An error from COMMIT leaves the
// outcome to the server, and is resolved by reading (persistence-api.md §5 rule 6): the token's
// id was generated before the transaction, so its row says whether it committed. Assuming
// failure would lose the only copy of a committed token's secret, after a rotation had revoked
// the one it replaced.
func (s *Store) issuing(ctx context.Context, fn func(*sql.Tx) (Issued, error)) (Issued, error) {
	commit := (*sql.Tx).Commit
	if s.o.commit != nil {
		commit = s.o.commit
	}
	var out Issued
	err := inTxCommit(ctx, s.db, func(tx *sql.Tx) error {
		if err := lockEpoch(ctx, tx); err != nil {
			return err
		}
		var err error
		out, err = fn(tx)
		return err
	}, commit)
	if err == nil || !errors.Is(err, errCommit) {
		return out, err
	}
	// The request's own context may be what ended the commit.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var n int
	if rerr := s.db.QueryRowContext(rctx, `SELECT count(*) FROM automation_token WHERE id = $1`, out.TokenID).Scan(&n); rerr != nil {
		return Issued{}, fmt.Errorf("%w; whether token %s committed is unknown (%v): see token list", err, out.TokenID, rerr)
	}
	if n == 0 {
		return Issued{}, err
	}
	return out, nil
}

func NewStore(db *sql.DB, a config.Auth) *Store {
	return &Store{db: db, issuer: a.OIDC.Issuer, denied: NewDenied(a.DeniedSubjects)}
}

// Issue creates the service identity name, with the human responsible for it, and its first
// token. operator is recorded in the act.
func (s *Store) Issue(ctx context.Context, name string, roles []Role, expiry time.Duration, responsible, operator string) (Issued, error) {
	if name == "" {
		return Issued{}, errors.New("token: a service identity needs a name")
	}
	if err := checkGrant(roles, expiry); err != nil {
		return Issued{}, err
	}
	resp, err := EnsureHuman(ctx, s.db, s.denied, s.issuer, responsible)
	if err != nil {
		return Issued{}, fmt.Errorf("token: responsible human: %w", err)
	}
	op, err := EnsureHuman(ctx, s.db, s.denied, s.issuer, operator)
	if err != nil {
		return Issued{}, fmt.Errorf("token: operator: %w", err)
	}
	identity := id.New(id.Principal)
	out, err := s.issuing(ctx, func(tx *sql.Tx) (Issued, error) {
		if s.o.beforeLock != nil {
			s.o.beforeLock()
		}
		if _, err := s.lockPrincipals(ctx, tx, "", resp, op); err != nil {
			return Issued{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO principal (id, kind, name, responsible, created_at)
			VALUES ($1, 'service', $2, $3, now())`, identity, name, resp); err != nil {
			return Issued{}, err
		}
		return s.issue(ctx, tx, identity, roles, expiry, op, "token.issue")
	})
	if err != nil {
		return Issued{}, fmt.Errorf("token: issue: %w", err)
	}
	return out, nil
}

// Rotate issues identity a new token and revokes its unrevoked one, expired or not, in one
// transaction; it is also the reissue after a restore (§12.3). roles nil keeps the latest
// token's. It refuses a revoked identity and one that deniedSubjects lists.
func (s *Store) Rotate(ctx context.Context, identity string, roles []Role, expiry time.Duration, operator string) (Issued, error) {
	if err := id.MustHave(identity, id.Principal); err != nil {
		return Issued{}, fmt.Errorf("token: %w", err)
	}
	if s.denied.Service(identity) {
		return Issued{}, fmt.Errorf("token: %w: %s is in deniedSubjects", ErrIdentityRevoked, identity)
	}
	check := checkExpiry(expiry)
	if roles != nil {
		check = checkGrant(roles, expiry)
	}
	if check != nil {
		return Issued{}, check
	}
	op, err := EnsureHuman(ctx, s.db, s.denied, s.issuer, operator)
	if err != nil {
		return Issued{}, fmt.Errorf("token: operator: %w", err)
	}
	out, err := s.issuing(ctx, func(tx *sql.Tx) (Issued, error) {
		if s.o.beforeLock != nil {
			s.o.beforeLock()
		}
		revoked, err := s.lockPrincipals(ctx, tx, identity, op)
		if err != nil {
			return Issued{}, err
		}
		if revoked {
			return Issued{}, fmt.Errorf("%w: %s", ErrIdentityRevoked, identity)
		}
		grant := roles
		if grant == nil {
			var r string
			// The last issued token's grant: seq, not issued_at, orders tokens by replacement.
			if err := tx.QueryRowContext(ctx, `SELECT array_to_string(roles, ',') FROM automation_token
				WHERE owner = $1 ORDER BY seq DESC LIMIT 1`, identity).Scan(&r); err != nil {
				return Issued{}, err
			}
			grant = ParseRoles(r)
		}
		return s.issue(ctx, tx, identity, grant, expiry, op, "token.rotate")
	})
	if err != nil {
		return Issued{}, fmt.Errorf("token: rotate: %w", err)
	}
	return out, nil
}

// Revoke revokes identity's unrevoked token without revoking the identity; Rotate issues it a
// new one. It returns what it revoked and records an act only if that is something.
func (s *Store) Revoke(ctx context.Context, identity, operator string) ([]string, error) {
	if err := id.MustHave(identity, id.Principal); err != nil {
		return nil, fmt.Errorf("token: %w", err)
	}
	op, err := EnsureHuman(ctx, s.db, s.denied, s.issuer, operator)
	if err != nil {
		return nil, fmt.Errorf("token: operator: %w", err)
	}
	var revoked []string
	err = inTx(ctx, s.db, func(tx *sql.Tx) error {
		if err := lockEpoch(ctx, tx); err != nil {
			return err
		}
		if s.o.beforeLock != nil {
			s.o.beforeLock()
		}
		if _, err := s.lockPrincipals(ctx, tx, identity, op); err != nil {
			return err
		}
		var err error
		if revoked, err = revokeTokens(ctx, tx, identity); err != nil || len(revoked) == 0 {
			return err
		}
		if s.o.afterRevokeOld != nil {
			s.o.afterRevokeOld()
		}
		return recordToolAct(ctx, tx, op, "token.revoke", append([]string{identity}, revoked...))
	})
	if err != nil {
		return nil, fmt.Errorf("token: revoke: %w", err)
	}
	return revoked, nil
}

// lockEpoch takes the installation state FOR SHARE, first in every token tool transaction
// (persistence-api.md §5 rule 5's order). Recovery-mode entry takes it FOR UPDATE (T9), so it
// cannot commit a new epoch between a token's or act's epoch read and the commit.
func lockEpoch(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SELECT 1 FROM installation_state FOR SHARE`)
	return err
}

// lockPrincipals locks the principals a tool transaction acts on, in id order (rule 5): the
// service identity, if service is not empty, FOR UPDATE, and each human FOR SHARE, against
// identity revocation's FOR UPDATE (T5c). EnsureHuman's check ran before the transaction, so a
// human is checked again here, under the lock (rule 2). It reports whether service is revoked.
func (s *Store) lockPrincipals(ctx context.Context, tx *sql.Tx, service string, humans ...string) (revoked bool, err error) {
	ids := slices.Clone(humans)
	if service != "" {
		ids = append(ids, service)
	}
	slices.Sort(ids)
	for _, p := range slices.Compact(ids) {
		if p == service {
			if revoked, err = s.lockService(ctx, tx, p); err != nil {
				return false, err
			}
			continue
		}
		var r bool
		if err := tx.QueryRowContext(ctx, `SELECT revoked FROM principal WHERE id = $1 FOR SHARE`, p).Scan(&r); err != nil {
			return false, err
		}
		if r {
			return false, fmt.Errorf("%w: %s", ErrIdentityRevoked, p)
		}
	}
	return revoked, nil
}

// lockService locks identity's principal row FOR UPDATE and reports whether it is revoked.
func (s *Store) lockService(ctx context.Context, tx *sql.Tx, identity string) (revoked bool, err error) {
	q := `SELECT kind, revoked FROM principal WHERE id = $1`
	if !s.o.noPrincipalLock {
		q += ` FOR UPDATE`
	}
	var kind string
	switch err := tx.QueryRowContext(ctx, q, identity).Scan(&kind, &revoked); {
	case errors.Is(err, sql.ErrNoRows):
		return false, fmt.Errorf("no identity %s", identity)
	case err != nil:
		return false, err
	case kind != string(Service):
		return false, fmt.Errorf("%s is a human, not a service identity", identity)
	}
	return revoked, nil
}

// issue revokes identity's unrevoked token and inserts a new one in the current epoch. The caller
// holds the principal lock, or created the row in tx; the partial unique index backs both.
func (s *Store) issue(ctx context.Context, tx *sql.Tx, identity string, roles []Role, expiry time.Duration, operator, action string) (Issued, error) {
	old, err := revokeTokens(ctx, tx, identity)
	if err != nil {
		return Issued{}, err
	}
	if s.o.afterRevokeOld != nil {
		s.o.afterRevokeOld()
	}
	var secret [32]byte
	rand.Read(secret[:])
	sum := sha256.Sum256(secret[:])
	tok := id.New(id.Token)
	var expires time.Time
	if err := tx.QueryRowContext(ctx, `INSERT INTO automation_token (id, owner, secret_sha256, roles, epoch, issued_at, expires_at)
		SELECT $1, $2, $3, string_to_array($4, ','), epoch, now(), now() + $5::bigint * interval '1 microsecond'
		FROM installation_state RETURNING expires_at`,
		tok, identity, sum[:], joinRoles(roles), expiry.Microseconds()).Scan(&expires); err != nil {
		return Issued{}, err
	}
	if err := recordToolAct(ctx, tx, operator, action, append([]string{identity, tok}, old...)); err != nil {
		return Issued{}, err
	}
	return Issued{Identity: identity, TokenID: tok, Expires: expires,
		Token: tokenPrefix + tok + "." + base64.RawURLEncoding.EncodeToString(secret[:])}, nil
}

func revokeTokens(ctx context.Context, tx *sql.Tx, identity string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `UPDATE automation_token SET revoked_at = now()
		WHERE owner = $1 AND revoked_at IS NULL RETURNING id`, identity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		ids = append(ids, t)
	}
	return ids, rows.Err()
}

// recordToolAct writes the act of a token command: the operator, no API role (§10.2, §10.5).
func recordToolAct(ctx context.Context, tx *sql.Tx, operator, action string, subjects []string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO act (id, principal, principal_kind, via, action, subjects, epoch, at)
		SELECT $1, $2, 'human', 'tool', $3, string_to_array($4, ','), epoch, now() FROM installation_state`,
		id.New(id.Act), operator, action, strings.Join(subjects, ","))
	return err
}

// checkGrant refuses a role outside viewer, author and publisher, a repeat, no role, or a bad
// expiry (§10.2; design §13.7 item 2).
func checkGrant(roles []Role, expiry time.Duration) error {
	if len(roles) == 0 {
		return errors.New("token: at least one role is required")
	}
	seen := map[Role]bool{}
	for _, r := range roles {
		switch r {
		case Viewer, Author, Publisher:
		case Approver, RecoveryAdmin:
			return fmt.Errorf("token: automation never holds %s", r)
		default:
			return fmt.Errorf("token: unknown role %q", r)
		}
		if seen[r] {
			return fmt.Errorf("token: role %s given twice", r)
		}
		seen[r] = true
	}
	return checkExpiry(expiry)
}

func checkExpiry(d time.Duration) error {
	if d <= 0 || d > MaxExpiry {
		return fmt.Errorf("token: expiry %s is outside (0, %s]", d, MaxExpiry)
	}
	return nil
}

func joinRoles(rs []Role) string {
	s := make([]string, len(rs))
	for i, r := range rs {
		s[i] = string(r)
	}
	return strings.Join(s, ",")
}

// Listed is one token of a service identity, for `bronzeward token list`. It holds no secret
// and no digest.
type Listed struct {
	Identity, Name, Responsible, TokenID   string
	IdentityRevoked, CurrentEpoch, Expired bool
	IdentityDenied                         bool // deniedSubjects lists it (§10.4)
	Roles                                  []Role
	Issued, Expires                        time.Time
	TokenRevoked                           *time.Time
}

// List returns every token of every service identity, in issuance order per identity.
func (s *Store) List(ctx context.Context) ([]Listed, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id, p.name, r.sub, p.revoked, t.id, array_to_string(t.roles, ','),
			t.issued_at, t.expires_at, t.epoch = i.epoch, t.expires_at <= now(), t.revoked_at
		FROM principal p
		JOIN principal r ON r.id = p.responsible
		JOIN automation_token t ON t.owner = p.id
		CROSS JOIN installation_state i
		WHERE p.kind = 'service'
		ORDER BY p.name, t.seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Listed
	for rows.Next() {
		var l Listed
		var roles string
		var revokedAt sql.NullTime
		if err := rows.Scan(&l.Identity, &l.Name, &l.Responsible, &l.IdentityRevoked, &l.TokenID, &roles,
			&l.Issued, &l.Expires, &l.CurrentEpoch, &l.Expired, &revokedAt); err != nil {
			return nil, err
		}
		l.Roles = ParseRoles(roles)
		l.IdentityDenied = s.denied.Service(l.Identity)
		if revokedAt.Valid {
			l.TokenRevoked = &revokedAt.Time
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
