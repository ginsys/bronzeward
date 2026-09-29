package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/id"
)

// A verifier that never meets a human token needs no key set.
func automationVerifier(db *sql.DB, a config.Auth) *Verifier { return NewVerifier(a, db, nil) }

func TestAutomationToken(t *testing.T) {
	db := migrated(t)
	is := issued(t, storeFor(db), "ci")
	p, err := automationVerifier(db, testAuth(testIssuerURL)).Authenticate(t.Context(), "Bearer "+is.Token)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != Service || p.ID != is.Identity || p.TokenID != is.TokenID || !slices.Equal(p.Roles, []Role{Author, Publisher}) || !p.Expiry.Equal(is.Expires) {
		t.Fatalf("principal %+v", p)
	}
}

func randomSecret() string {
	var b [32]byte
	rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// A secret whose last character carries non-zero padding bits: strict decoding refuses it.
func trailingBits(secret string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	i := strings.IndexByte(alphabet, secret[len(secret)-1])
	return secret[:len(secret)-1] + string(alphabet[i|1])
}

func TestAutomationDefects(t *testing.T) {
	db := migrated(t)
	s := storeFor(db)
	first := issued(t, s, "ci")
	current, err := s.Rotate(t.Context(), first.Identity, nil, DefaultExpiry, "h-operator")
	if err != nil {
		t.Fatal(err)
	}
	_, secret, _ := strings.Cut(current.Token, ".")
	short := base64.RawURLEncoding.EncodeToString(make([]byte, 31))
	v := automationVerifier(db, testAuth(testIssuerURL))
	for name, tok := range map[string]string{
		"prefix only":   "bwt_",
		"no secret":     "bwt_" + current.TokenID,
		"not a tok id":  "bwt_" + id.New(id.Principal) + "." + secret,
		"short secret":  "bwt_" + current.TokenID + "." + short,
		"padded secret": "bwt_" + current.TokenID + "." + secret + "=",
		"trailing LF":   "bwt_" + current.TokenID + "." + secret + "\n",
		"inner CR":      "bwt_" + current.TokenID + "." + secret[:20] + "\r" + secret[20:],
		"trailing bits": "bwt_" + current.TokenID + "." + trailingBits(secret),
		"unknown token": "bwt_" + id.New(id.Token) + "." + secret,
		"wrong secret":  "bwt_" + current.TokenID + "." + randomSecret(),
		"rotated away":  first.Token,
	} {
		if _, err := v.Authenticate(t.Context(), "Bearer "+tok); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%s: %v; want ErrUnauthenticated", name, err)
		}
	}
}

// parseToken on its own: the header check in Authenticate already refuses CR and LF, so only a
// direct call shows the length check that base64's strict mode does not provide.
func TestParseTokenLength(t *testing.T) {
	tok := id.New(id.Token)
	secret := randomSecret()
	if _, _, err := parseToken("bwt_" + tok + "." + secret); err != nil {
		t.Fatalf("valid: %v", err)
	}
	for name, s := range map[string]string{"trailing LF": secret + "\n", "inner CR": secret[:20] + "\r" + secret[20:], "44 characters": secret + "A"} {
		if _, _, err := parseToken("bwt_" + tok + "." + s); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestAutomationExpiredRevokedEarlierEpoch(t *testing.T) {
	db := migrated(t)
	s := storeFor(db)
	v := automationVerifier(db, testAuth(testIssuerURL))
	expired := issued(t, s, "ci-expired")
	mustExec(t, db, `UPDATE automation_token SET issued_at = now() - interval '2 days', expires_at = now() - interval '1 day' WHERE id = $1`, expired.TokenID)
	revoked := issued(t, s, "ci-revoked")
	if _, err := s.Revoke(t.Context(), revoked.Identity, "h-operator"); err != nil {
		t.Fatal(err)
	}
	for name, tok := range map[string]string{"expired": expired.Token, "revoked by the tool": revoked.Token} {
		if _, err := v.Authenticate(t.Context(), "Bearer "+tok); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// §12.3: after a restore and a new epoch, an unrevoked row's token is refused anyway.
	earlier := issued(t, s, "ci-epoch")
	newEpoch(t, db)
	if _, err := v.Authenticate(t.Context(), "Bearer "+earlier.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("earlier epoch: %v", err)
	}
}

func TestAutomationRevokedOrDeniedIdentity(t *testing.T) {
	db := migrated(t)
	s := storeFor(db)
	a, b := issued(t, s, "ci-a"), issued(t, s, "ci-b")
	if err := revokeIdentity(t, db, a.Identity); err != nil {
		t.Fatal(err)
	}
	cfg := testAuth(testIssuerURL)
	cfg.DeniedSubjects = []config.DeniedSubject{{Identity: b.Identity}}
	v := automationVerifier(db, cfg)
	if _, err := v.Authenticate(t.Context(), "Bearer "+a.Token); !errors.Is(err, ErrIdentityRevoked) {
		t.Errorf("revoked identity (its token revoked with it): %v; want ErrIdentityRevoked", err)
	}
	if _, err := v.Authenticate(t.Context(), "Bearer "+b.Token); !errors.Is(err, ErrIdentityRevoked) {
		t.Errorf("denied identity: %v; want ErrIdentityRevoked", err)
	}
	if _, err := v.Authenticate(t.Context(), "Bearer bwt_"+b.TokenID+"."+randomSecret()); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("denied identity, wrong secret: %v; want ErrUnauthenticated", err)
	}
	newEpoch(t, db)
	if _, err := v.Authenticate(t.Context(), "Bearer "+b.Token); !errors.Is(err, ErrIdentityRevoked) {
		t.Errorf("denied identity after a restore: %v; want ErrIdentityRevoked", err)
	}
}
