package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// automation verifies a Bronzeward-issued token (§10.2), in this order:
//  1. the secret, compared in constant time, so a caller without it learns nothing;
//  2. a revoked or denied identity, 403, although identity revocation revoked its token too
//     (§10.4, §14);
//  3. the token itself: revoked, expired, or from an earlier epoch (§12.3), 401.
func (v *Verifier) automation(ctx context.Context, raw string) (Principal, error) {
	tok, secret, err := parseToken(raw)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: %v", ErrUnauthenticated, err)
	}
	sum := sha256.Sum256(secret)
	var (
		owner, roles                                         string
		stored                                               []byte
		expires                                              time.Time
		expired, tokenRevoked, earlierEpoch, identityRevoked bool
	)
	err = v.db.QueryRowContext(ctx, `SELECT t.owner, t.secret_sha256, array_to_string(t.roles, ','), t.expires_at,
			t.expires_at <= now(), t.revoked_at IS NOT NULL, t.epoch <> i.epoch, p.revoked
		FROM automation_token t JOIN principal p ON p.id = t.owner CROSS JOIN installation_state i
		WHERE t.id = $1`, tok).Scan(&owner, &stored, &roles, &expires, &expired, &tokenRevoked, &earlierEpoch, &identityRevoked)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Principal{}, fmt.Errorf("%w: unknown token %s", ErrUnauthenticated, tok)
	case err != nil:
		return Principal{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	case subtle.ConstantTimeCompare(sum[:], stored) != 1:
		return Principal{}, fmt.Errorf("%w: wrong secret for %s", ErrUnauthenticated, tok)
	case identityRevoked || v.denied.Service(owner):
		return Principal{}, fmt.Errorf("%w: %s", ErrIdentityRevoked, owner)
	case tokenRevoked:
		return Principal{}, fmt.Errorf("%w: token %s is revoked", ErrUnauthenticated, tok)
	case expired:
		return Principal{}, fmt.Errorf("%w: token %s expired", ErrUnauthenticated, tok)
	case earlierEpoch:
		return Principal{}, fmt.Errorf("%w: token %s is from an earlier epoch", ErrUnauthenticated, tok)
	}
	has := map[Role]bool{}
	for _, r := range ParseRoles(roles) {
		has[r] = true
	}
	return Principal{Kind: Service, ID: owner, TokenID: tok, Roles: ordered(has), Expiry: expires}, nil
}

// parseToken splits bwt_<tok id>.<secret>, refusing anything the tool could not have printed.
func parseToken(raw string) (tok string, secret []byte, err error) {
	body, _ := strings.CutPrefix(raw, tokenPrefix)
	tok, enc, ok := strings.Cut(body, ".")
	if !ok {
		return "", nil, errors.New("not bwt_<token id>.<secret>")
	}
	if err := id.MustHave(tok, id.Token); err != nil {
		return "", nil, err
	}
	// Strict decoding still skips CR and LF (encoding/base64/base64.go:111-112, Go 1.27), so the
	// encoded length is checked first: 32 bytes are exactly 43 unpadded characters.
	if len(enc) != 43 {
		return "", nil, errors.New("the secret is not 43 characters")
	}
	secret, err = base64.RawURLEncoding.Strict().DecodeString(enc)
	if err != nil || len(secret) != 32 {
		return "", nil, errors.New("the secret is not 256 bits of unpadded base64url")
	}
	return tok, secret, nil
}
