package auth

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/config"
)

const testIssuerURL = "https://idp.test"

var tokenShape = regexp.MustCompile(`^bwt_tok_[a-z2-7]{26}\.[A-Za-z0-9_-]{43}$`)

func storeFor(db *sql.DB) *Store { return NewStore(db, testAuth(testIssuerURL)) }

func issued(t *testing.T, s *Store, name string) Issued {
	t.Helper()
	is, err := s.Issue(t.Context(), name, []Role{Author, Publisher}, DefaultExpiry, "h-all", "h-operator")
	if err != nil {
		t.Fatal(err)
	}
	return is
}

func unrevoked(t *testing.T, db *sql.DB, identity string) int {
	return count(t, db, "SELECT count(*) FROM automation_token WHERE owner = $1 AND revoked_at IS NULL", identity)
}

func TestIssueStoresOnlyTheDigest(t *testing.T) {
	db := migrated(t)
	is := issued(t, storeFor(db), "ci")
	if !tokenShape.MatchString(is.Token) {
		t.Fatalf("token %q", is.Token)
	}
	_, enc, _ := strings.Cut(is.Token, ".")
	secret, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(secret)
	var stored []byte
	var roles string
	var thirtyDays bool
	if err := db.QueryRow(`SELECT secret_sha256, array_to_string(roles, ','), expires_at - issued_at = interval '720 hours'
		FROM automation_token WHERE id = $1`, is.TokenID).Scan(&stored, &roles, &thirtyDays); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, sum[:]) || roles != "author,publisher" || !thirtyDays {
		t.Fatalf("stored digest match %v, roles %q, 30 days %v", bytes.Equal(stored, sum[:]), roles, thirtyDays)
	}
	var responsible, via, action, subjects string
	var role sql.NullString
	if err := db.QueryRow(`SELECT r.sub FROM principal p JOIN principal r ON r.id = p.responsible WHERE p.id = $1`, is.Identity).Scan(&responsible); err != nil || responsible != "h-all" {
		t.Fatalf("responsible %q, %v", responsible, err)
	}
	if err := db.QueryRow(`SELECT a.via, a.role, a.action, array_to_string(a.subjects, ',')
		FROM act a JOIN principal o ON o.id = a.principal WHERE o.sub = 'h-operator'`).Scan(&via, &role, &action, &subjects); err != nil {
		t.Fatal(err)
	}
	if via != "tool" || role.Valid || action != "token.issue" || subjects != is.Identity+","+is.TokenID {
		t.Fatalf("act: %s %v %s %s", via, role, action, subjects)
	}
}

func TestIssueRefuses(t *testing.T) {
	db := migrated(t)
	cfg := testAuth(testIssuerURL)
	cfg.DeniedSubjects = []config.DeniedSubject{{Iss: testIssuerURL, Sub: "h-denied"}}
	s := NewStore(db, cfg)
	issued(t, s, "ci")
	for _, c := range []struct {
		name, identity, responsible string
		roles                       []Role
		expiry                      time.Duration
	}{
		{"approver", "a", "h-all", []Role{Approver}, DefaultExpiry},
		{"recovery-admin", "b", "h-all", []Role{Author, RecoveryAdmin}, DefaultExpiry},
		{"unknown role", "c", "h-all", []Role{"root"}, DefaultExpiry},
		{"no role", "d", "h-all", nil, DefaultExpiry},
		{"repeated role", "e", "h-all", []Role{Author, Author}, DefaultExpiry},
		{"zero expiry", "f", "h-all", []Role{Author}, 0},
		{"negative expiry", "g", "h-all", []Role{Author}, -time.Hour},
		{"over 90 days", "h", "h-all", []Role{Author}, MaxExpiry + time.Second},
		{"no name", "", "h-all", []Role{Author}, DefaultExpiry},
		{"taken name", "ci", "h-all", []Role{Author}, DefaultExpiry},
		{"denied responsible", "i", "h-denied", []Role{Author}, DefaultExpiry},
	} {
		if _, err := s.Issue(t.Context(), c.identity, c.roles, c.expiry, c.responsible, "h-operator"); err == nil {
			t.Errorf("%s: issued", c.name)
		}
	}
	if n := count(t, db, "SELECT count(*) FROM automation_token"); n != 1 {
		t.Fatalf("%d tokens; the refusals issued some", n)
	}
	if n := count(t, db, "SELECT count(*) FROM principal WHERE sub = 'h-denied'"); n != 0 {
		t.Fatal("a denied subject got a row")
	}
	if _, err := s.Issue(t.Context(), "max", []Role{Viewer}, MaxExpiry, "h-all", "h-operator"); err != nil {
		t.Fatalf("90 days exactly: %v", err)
	}
}

func TestRotateReplacesAndKeepsRoles(t *testing.T) {
	db := migrated(t)
	s := storeFor(db)
	first := issued(t, s, "ci")
	second, err := s.Rotate(t.Context(), first.Identity, nil, DefaultExpiry, "h-operator")
	if err != nil {
		t.Fatal(err)
	}
	var roles, subjects string
	if err := db.QueryRow(`SELECT array_to_string(roles, ',') FROM automation_token WHERE id = $1 AND revoked_at IS NULL`, second.TokenID).Scan(&roles); err != nil || roles != "author,publisher" {
		t.Fatalf("new token: %q, %v", roles, err)
	}
	if unrevoked(t, db, first.Identity) != 1 {
		t.Fatal("rotation left other than one unrevoked token")
	}
	if err := db.QueryRow(`SELECT array_to_string(subjects, ',') FROM act WHERE action = 'token.rotate'`).Scan(&subjects); err != nil ||
		subjects != first.Identity+","+second.TokenID+","+first.TokenID {
		t.Fatalf("rotate act subjects %q, %v", subjects, err)
	}
	third, err := s.Rotate(t.Context(), first.Identity, []Role{Viewer}, time.Hour, "h-operator")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT array_to_string(roles, ',') FROM automation_token WHERE id = $1`, third.TokenID).Scan(&roles); err != nil || roles != "viewer" {
		t.Fatalf("rotate with roles: %q, %v", roles, err)
	}
}

func TestRevokeKeepsTheIdentity(t *testing.T) {
	db := migrated(t)
	s := storeFor(db)
	is := issued(t, s, "ci")
	got, err := s.Revoke(t.Context(), is.Identity, "h-operator")
	if err != nil || len(got) != 1 || got[0] != is.TokenID {
		t.Fatalf("revoke: %v, %v", got, err)
	}
	if unrevoked(t, db, is.Identity) != 0 || count(t, db, "SELECT count(*) FROM principal WHERE id = $1 AND revoked", is.Identity) != 0 {
		t.Fatal("revoke left a valid token or revoked the identity")
	}
	if got, err := s.Revoke(t.Context(), is.Identity, "h-operator"); err != nil || len(got) != 0 {
		t.Fatalf("second revoke: %v, %v", got, err)
	}
	if count(t, db, "SELECT count(*) FROM act WHERE action = 'token.revoke'") != 1 {
		t.Fatal("a revoke that revoked nothing recorded an act")
	}
	if _, err := s.Rotate(t.Context(), is.Identity, nil, DefaultExpiry, "h-operator"); err != nil {
		t.Fatalf("rotate after revoke: %v", err)
	}
}

// §12.3, §16: after a restore lost the revocation, the row is unrevoked and the token is from an
// earlier epoch; only deniedSubjects stops the reissue.
func TestReissueAfterRestoreRefusedForDeniedIdentity(t *testing.T) {
	db := migrated(t)
	is := issued(t, storeFor(db), "ci")
	newEpoch(t, db)
	cfg := testAuth(testIssuerURL)
	cfg.DeniedSubjects = []config.DeniedSubject{{Identity: is.Identity}}
	if _, err := NewStore(db, cfg).Rotate(t.Context(), is.Identity, nil, DefaultExpiry, "h-operator"); !errors.Is(err, ErrIdentityRevoked) {
		t.Fatalf("reissue to a denied identity: %v", err)
	}
	// The control: without the list entry the same reissue proceeds.
	if _, err := storeFor(db).Rotate(t.Context(), is.Identity, nil, DefaultExpiry, "h-operator"); err != nil {
		t.Fatalf("control: %v", err)
	}
}

func TestRotateRefusesARevokedIdentity(t *testing.T) {
	db := migrated(t)
	is := issued(t, storeFor(db), "ci")
	if err := revokeIdentity(t, db, is.Identity); err != nil {
		t.Fatal(err)
	}
	if _, err := storeFor(db).Rotate(t.Context(), is.Identity, nil, DefaultExpiry, "h-operator"); !errors.Is(err, ErrIdentityRevoked) {
		t.Fatalf("rotate a revoked identity: %v", err)
	}
}

// §10: of two concurrent first uses, one inserts and the other reads the committed row.
func TestEnsureHumanConcurrent(t *testing.T) {
	db := migrated(t)
	ids := make(chan string, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			idn, err := EnsureHuman(t.Context(), db, Denied{}, testIssuerURL, "h-author")
			if err != nil {
				t.Error(err)
			}
			ids <- idn
		})
	}
	wg.Wait()
	close(ids)
	a, b := <-ids, <-ids
	if a == "" || a != b || count(t, db, "SELECT count(*) FROM principal") != 1 {
		t.Fatalf("ids %q %q", a, b)
	}
}

func TestList(t *testing.T) {
	db := migrated(t)
	s := storeFor(db)
	is := issued(t, s, "ci")
	ls, err := s.List(t.Context())
	if err != nil || len(ls) != 1 {
		t.Fatalf("list: %+v, %v", ls, err)
	}
	l := ls[0]
	if l.Identity != is.Identity || l.Name != "ci" || l.Responsible != "h-all" || l.TokenID != is.TokenID ||
		!l.CurrentEpoch || l.Expired || l.IdentityRevoked || l.TokenRevoked != nil || len(l.Roles) != 2 {
		t.Fatalf("listed %+v", l)
	}
}

// revokeIdentity commits RevokeIdentity alone, standing in for PR 4's T5c transaction.
func revokeIdentity(t *testing.T, db *sql.DB, identity string) error {
	return inTx(t.Context(), db, func(tx *sql.Tx) error {
		_, err := RevokeIdentity(t.Context(), tx, identity)
		return err
	})
}
