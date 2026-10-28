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
	"github.com/ginsys/bronzeward/internal/id"
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

// A COMMIT whose reply is lost leaves the outcome to the server (persistence-api.md §5 rule 6).
// The token is read back by its id: a committed issue or rotation still hands over the only copy
// of its secret, and one that did not commit is still an error.
func TestCommitUnknownIsResolvedByReading(t *testing.T) {
	db := migrated(t)
	s := storeFor(db)
	first := issued(t, s, "ci")
	lost := errors.New("connection reset after COMMIT was sent")
	s.o.commit = func(tx *sql.Tx) error {
		if err := tx.Commit(); err != nil {
			return err
		}
		return lost
	}
	second, err := s.Rotate(t.Context(), first.Identity, nil, DefaultExpiry, "h-operator")
	if err != nil || !tokenShape.MatchString(second.Token) {
		t.Fatalf("committed rotation, reply lost: %q, %v", second.Token, err)
	}
	if is, err := s.Issue(t.Context(), "deploy", []Role{Author}, DefaultExpiry, "h-all", "h-operator"); err != nil || !tokenShape.MatchString(is.Token) {
		t.Fatalf("committed issue, reply lost: %q, %v", is.Token, err)
	}
	s.o.commit = func(tx *sql.Tx) error {
		if err := tx.Rollback(); err != nil {
			return err
		}
		return lost
	}
	if _, err := s.Rotate(t.Context(), first.Identity, nil, DefaultExpiry, "h-operator"); !errors.Is(err, lost) {
		t.Fatalf("rotation that did not commit: %v; want the commit error", err)
	}
	if _, err := s.Issue(t.Context(), "other", []Role{Author}, DefaultExpiry, "h-all", "h-operator"); !errors.Is(err, lost) {
		t.Fatalf("issue that did not commit: %v; want the commit error", err)
	}
	if n := count(t, db, "SELECT count(*) FROM automation_token WHERE id = $1 AND revoked_at IS NULL", second.TokenID); n != 1 {
		t.Fatal("the rotation that did not commit revoked the current token")
	}
}

// A revocation whose COMMIT reply is lost is resolved by reading its act back (§5 rule 6): the
// operator learns whether it landed, where a retry could only say no token is left to revoke.
func TestRevokeCommitUnknownIsResolvedByReading(t *testing.T) {
	db := migrated(t)
	s := storeFor(db)
	a, b := issued(t, s, "a"), issued(t, s, "b")
	lost := errors.New("connection reset after COMMIT was sent")
	s.o.commit = func(tx *sql.Tx) error {
		if err := tx.Rollback(); err != nil {
			return err
		}
		return lost
	}
	if _, err := s.Revoke(t.Context(), a.Identity, "h-operator"); !errors.Is(err, lost) {
		t.Fatalf("revocation that did not commit: %v; want the commit error", err)
	}
	if unrevoked(t, db, a.Identity) != 1 {
		t.Fatal("the revocation that did not commit revoked the token")
	}
	s.o.commit = func(tx *sql.Tx) error {
		if err := tx.Commit(); err != nil {
			return err
		}
		return lost
	}
	revoked, err := s.Revoke(t.Context(), b.Identity, "h-operator")
	if err != nil || len(revoked) != 1 || revoked[0] != b.TokenID {
		t.Fatalf("committed revocation, reply lost: %v, %v; want [%s]", revoked, err, b.TokenID)
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
	cfg.DeniedSubjects = []config.DeniedSubject{{Identity: is.Identity, Name: "ci"}}
	if _, err := NewStore(db, cfg).Rotate(t.Context(), is.Identity, nil, DefaultExpiry, "h-operator"); !errors.Is(err, ErrIdentityRevoked) {
		t.Fatalf("reissue to a denied identity: %v", err)
	}
	// #391: an entry listing its name under another identifier refuses it too, the name read
	// under the lock.
	cfg.DeniedSubjects = []config.DeniedSubject{{Identity: id.New(id.Principal), Name: "ci"}}
	if _, err := NewStore(db, cfg).Rotate(t.Context(), is.Identity, nil, DefaultExpiry, "h-operator"); !errors.Is(err, ErrIdentityRevoked) {
		t.Fatalf("reissue to an identity whose name is denied: %v", err)
	}
	// The control: without the list entry the same reissue proceeds.
	if _, err := storeFor(db).Rotate(t.Context(), is.Identity, nil, DefaultExpiry, "h-operator"); err != nil {
		t.Fatalf("control: %v", err)
	}
}

// An entry without a name, which config refuses, matches no lookup without a name.
func TestDeniedEmptyNameMatchesNothing(t *testing.T) {
	listed, other := id.New(id.Principal), id.New(id.Principal)
	d := NewDenied([]config.DeniedSubject{{Identity: listed}})
	if !d.Service(listed, "") || d.Service(other, "") || d.Service("", "") || d.ServiceEntry(listed, "ci") {
		t.Fatalf("Service(listed) %v, Service(other) %v, Service(none) %v, ServiceEntry %v",
			d.Service(listed, ""), d.Service(other, ""), d.Service("", ""), d.ServiceEntry(listed, "ci"))
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

// §10.4: a revoked human is refused as the tool's operator and as a new identity's responsible
// human, as deniedSubjects' humans are.
func TestToolRefusesARevokedHuman(t *testing.T) {
	db := migrated(t)
	s := storeFor(db)
	is := issued(t, s, "ci")
	h, err := EnsureHuman(t.Context(), db, Denied{}, testIssuerURL, "h-gone")
	if err != nil {
		t.Fatal(err)
	}
	if err := revokeIdentity(t, db, h); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Issue(t.Context(), "x", []Role{Author}, DefaultExpiry, "h-gone", "h-operator"); !errors.Is(err, ErrIdentityRevoked) {
		t.Errorf("issue with a revoked responsible human: %v", err)
	}
	if _, err := s.Rotate(t.Context(), is.Identity, nil, DefaultExpiry, "h-gone"); !errors.Is(err, ErrIdentityRevoked) {
		t.Errorf("rotate by a revoked operator: %v", err)
	}
	if _, err := s.Revoke(t.Context(), is.Identity, "h-gone"); !errors.Is(err, ErrIdentityRevoked) {
		t.Errorf("revoke by a revoked operator: %v", err)
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
	// §10.4: an identity deniedSubjects lists is refused whatever its row says; list says so.
	cfg := testAuth(testIssuerURL)
	cfg.DeniedSubjects = []config.DeniedSubject{{Identity: is.Identity, Name: "ci"}}
	if ls, err = NewStore(db, cfg).List(t.Context()); err != nil || len(ls) != 1 || !ls[0].IdentityDenied {
		t.Fatalf("list with the identity denied: %+v, %v", ls, err)
	}
	// #391: an entry whose identifier a restore lost denies an identity created under its name.
	cfg.DeniedSubjects = []config.DeniedSubject{{Identity: id.New(id.Principal), Name: "ci"}}
	if ls, err = NewStore(db, cfg).List(t.Context()); err != nil || len(ls) != 1 || !ls[0].IdentityDenied {
		t.Fatalf("list with the name denied: %+v, %v", ls, err)
	}
	cfg.DeniedSubjects = []config.DeniedSubject{{Identity: id.New(id.Principal), Name: "other"}}
	if ls, err = NewStore(db, cfg).List(t.Context()); err != nil || len(ls) != 1 || ls[0].IdentityDenied {
		t.Fatalf("list with another name denied: %+v, %v", ls, err)
	}
}

// §16, #391: a service identity created and revoked after the backup loses its row, name included,
// to a restore. deniedSubjects lists it by identifier and name, so the tool refuses to create a new
// identity under that name, and the refusal leaves no row.
func TestIssueAfterRestoreRefusedForDeniedServiceName(t *testing.T) {
	db := migrated(t)
	cfg := testAuth(testIssuerURL)
	cfg.DeniedSubjects = []config.DeniedSubject{{Identity: id.New(id.Principal), Name: "ci"}}
	if _, err := NewStore(db, cfg).Issue(t.Context(), "ci", []Role{Author}, DefaultExpiry, "h-all", "h-operator"); !errors.Is(err, ErrIdentityRevoked) {
		t.Fatalf("issue under a denied name: %v; want ErrIdentityRevoked", err)
	}
	if n := count(t, db, "SELECT count(*) FROM principal"); n != 0 {
		t.Fatalf("%d principal rows after the refusal", n)
	}
	if n := count(t, db, "SELECT count(*) FROM automation_token"); n != 0 {
		t.Fatalf("%d tokens after the refusal", n)
	}
	// The control: without the entry the same issue proceeds.
	if _, err := storeFor(db).Issue(t.Context(), "ci", []Role{Author}, DefaultExpiry, "h-all", "h-operator"); err != nil {
		t.Fatalf("control: %v", err)
	}
}

// #391, no restore: a revoked identity that deniedSubjects lists by identifier and name keeps its
// row and is refused as before, by the tool and at authentication.
func TestDeniedServiceNameWithoutRestore(t *testing.T) {
	db := migrated(t)
	is := issued(t, storeFor(db), "ci")
	if err := revokeIdentity(t, db, is.Identity); err != nil {
		t.Fatal(err)
	}
	row := func() string {
		var r string
		if err := db.QueryRow(`SELECT concat_ws('|', id, kind, name, responsible, revoked) FROM principal WHERE id = $1`, is.Identity).Scan(&r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	before, tokens := row(), count(t, db, "SELECT count(*) FROM automation_token")
	cfg := testAuth(testIssuerURL)
	cfg.DeniedSubjects = []config.DeniedSubject{{Identity: is.Identity, Name: "ci"}}
	s := NewStore(db, cfg)
	if _, err := s.Rotate(t.Context(), is.Identity, nil, DefaultExpiry, "h-operator"); !errors.Is(err, ErrIdentityRevoked) {
		t.Errorf("rotate: %v; want ErrIdentityRevoked", err)
	}
	if _, err := s.Issue(t.Context(), "ci", []Role{Author}, DefaultExpiry, "h-all", "h-operator"); !errors.Is(err, ErrIdentityRevoked) {
		t.Errorf("issue under its name: %v; want ErrIdentityRevoked", err)
	}
	if _, err := automationVerifier(db, cfg).Authenticate(t.Context(), "Bearer "+is.Token); !errors.Is(err, ErrIdentityRevoked) {
		t.Errorf("authenticate: %v; want ErrIdentityRevoked", err)
	}
	if after := row(); after != before || count(t, db, "SELECT count(*) FROM automation_token") != tokens {
		t.Fatalf("row %q -> %q, tokens %d -> %d", before, after, tokens, count(t, db, "SELECT count(*) FROM automation_token"))
	}
}

// revokeIdentity commits RevokeIdentity alone, standing in for PR 4's T5c transaction.
func revokeIdentity(t *testing.T, db *sql.DB, identity string) error {
	return inTx(t.Context(), db, func(tx *sql.Tx) error {
		_, err := RevokeIdentity(t.Context(), tx, identity)
		return err
	})
}
