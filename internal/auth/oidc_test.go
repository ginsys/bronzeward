package auth

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/fixtures/oidc/issuer"
	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/id"
)

type fixture struct {
	db   *sql.DB
	iss  *issuer.Issuer
	cfg  config.Auth
	down atomic.Bool // the issuer answers 503 while set
}

// newFixture starts the fixture issuer on a loopback port. The listener comes first, so the
// issuer knows its URL before the server serves anything.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{db: migrated(t)}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := issuer.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if f.iss, err = issuer.New("http://"+l.Addr().String(), "bronzeward", key); err != nil {
		t.Fatal(err)
	}
	h := f.iss.Handler()
	srv := &httptest.Server{Listener: l, Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		h.ServeHTTP(w, r)
	})}}
	srv.Start()
	t.Cleanup(srv.Close)
	f.cfg = testAuth(f.iss.URL)
	return f
}

func (f *fixture) verifier() *Verifier { return NewVerifier(f.cfg, f.db, Discover(f.cfg.OIDC.Issuer)) }

func (f *fixture) bearer(t *testing.T, human, defect string) string {
	t.Helper()
	tok, err := f.iss.Mint(human, defect, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return "Bearer " + tok
}

func TestHumanRoles(t *testing.T) {
	f := newFixture(t)
	v := f.verifier()
	want := map[string][]Role{
		"h-author": {Author}, "h-publisher": {Publisher}, "h-approver": {Approver},
		"h-recovery": {RecoveryAdmin}, "h-viewer": {Viewer}, "h-all": {Author, Publisher, Approver},
	}
	if len(want) != len(issuer.Humans) {
		t.Fatalf("the test knows %d humans, the issuer %d", len(want), len(issuer.Humans))
	}
	for human, roles := range want {
		p, err := v.Authenticate(t.Context(), f.bearer(t, human, ""))
		if err != nil {
			t.Fatalf("%s: %v", human, err)
		}
		if p.Kind != Human || p.ID != "" || p.Issuer != f.iss.URL || p.Subject != human || !slices.Equal(p.Roles, roles) || p.Expiry.IsZero() {
			t.Errorf("%s: %+v", human, p)
		}
	}
	if n := count(t, f.db, "SELECT count(*) FROM principal"); n != 0 {
		t.Fatalf("authentication created %d principal rows", n)
	}
}

// Every defect the fixture issuer can mint, and every malformed header: 401, and no row (AP S0).
func TestEveryDefectUnauthenticated(t *testing.T) {
	f := newFixture(t)
	v := f.verifier()
	for _, d := range issuer.Defects {
		if _, err := v.Authenticate(t.Context(), f.bearer(t, "h-all", d)); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%s: %v; want ErrUnauthenticated", d, err)
		}
	}
	for _, h := range []string{"", "Bearer", "Bearer ", "bearer", "Basic aDpw", "Bearer a b", f.bearer(t, "h-all", "") + "\n"} {
		if _, err := v.Authenticate(t.Context(), h); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("header %q: %v; want ErrUnauthenticated", h, err)
		}
	}
	if n := count(t, f.db, "SELECT count(*) FROM principal"); n != 0 {
		t.Fatalf("refused tokens created %d principal rows", n)
	}
}

func TestClockSkewAndLifetime(t *testing.T) {
	f := newFixture(t)
	v := f.verifier()
	now := time.Now()
	at := func(d time.Duration) int64 { return now.Add(d).Unix() }
	for _, c := range []struct {
		name string
		edit func(map[string]any)
		ok   bool
	}{
		{"exp 30s ago", func(m map[string]any) {
			m["iat"], m["nbf"], m["exp"] = at(-5*time.Minute), at(-5*time.Minute), at(-30*time.Second)
		}, true},
		{"exp 90s ago", func(m map[string]any) {
			m["iat"], m["nbf"], m["exp"] = at(-5*time.Minute), at(-5*time.Minute), at(-90*time.Second)
		}, false},
		{"nbf in 30s", func(m map[string]any) { m["nbf"] = at(30 * time.Second) }, true},
		{"nbf in 90s", func(m map[string]any) { m["nbf"] = at(90 * time.Second) }, false},
		{"iat in 30s", func(m map[string]any) { m["iat"], m["exp"] = at(30*time.Second), at(30*time.Second+5*time.Minute) }, true},
		{"iat in 90s", func(m map[string]any) { m["iat"], m["exp"] = at(90*time.Second), at(90*time.Second+5*time.Minute) }, false},
		{"lifetime exactly 15m", func(m map[string]any) { m["exp"] = at(15 * time.Minute) }, true},
		{"lifetime 15m1s", func(m map[string]any) { m["exp"] = at(15*time.Minute + time.Second) }, false},
		{"aud array holding ours", func(m map[string]any) { m["aud"] = []string{"other", "bronzeward"} }, true},
		{"no groups claim", func(m map[string]any) { delete(m, "groups") }, true},
	} {
		m := map[string]any{"iss": f.iss.URL, "aud": "bronzeward", "sub": "h-viewer", "groups": []string{"bw-viewers"},
			"iat": at(0), "nbf": at(0), "exp": at(5 * time.Minute)}
		c.edit(m)
		tok, err := f.iss.Sign(m)
		if err != nil {
			t.Fatal(err)
		}
		_, err = v.Authenticate(t.Context(), "Bearer "+tok)
		if c.ok && err != nil || !c.ok && !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestRevokedAndDeniedHumans(t *testing.T) {
	f := newFixture(t)
	f.cfg.DeniedSubjects = []config.DeniedSubject{{Iss: f.iss.URL, Sub: "h-viewer"}}
	v := f.verifier()
	revoked, active := id.New(id.Principal), id.New(id.Principal)
	mustExec(t, f.db, `INSERT INTO principal (id, kind, iss, sub, created_at, revoked) VALUES ($1, 'human', $2, 'h-author', now(), true)`, revoked, f.iss.URL)
	mustExec(t, f.db, `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', $2, 'h-publisher', now())`, active, f.iss.URL)
	if _, err := v.Authenticate(t.Context(), f.bearer(t, "h-author", "")); !errors.Is(err, ErrIdentityRevoked) {
		t.Errorf("revoked human: %v", err)
	}
	if _, err := v.Authenticate(t.Context(), f.bearer(t, "h-viewer", "")); !errors.Is(err, ErrIdentityRevoked) {
		t.Errorf("denied human: %v", err)
	}
	// A defect outranks a denial: the caller learns nothing without a valid token.
	if _, err := v.Authenticate(t.Context(), f.bearer(t, "h-viewer", "expired")); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("denied human's expired token: %v", err)
	}
	if p, err := v.Authenticate(t.Context(), f.bearer(t, "h-publisher", "")); err != nil || p.ID != active {
		t.Errorf("active human: %+v, %v", p, err)
	}
	if n := count(t, f.db, "SELECT count(*) FROM principal WHERE sub = 'h-viewer'"); n != 0 {
		t.Fatal("a denied subject got a row")
	}
}

// PA §5 rule 4: the time checks read the database's clock. With it 10 minutes ahead of this
// process, a token this process just minted is expired; with it 10 minutes behind, not yet valid.
func TestTimeChecksUseDatabaseClock(t *testing.T) {
	f := newFixture(t)
	h := f.bearer(t, "h-viewer", "")
	var dbNow time.Time
	if err := f.db.QueryRow(`SELECT now()`).Scan(&dbNow); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(dbNow); d > 5*time.Second || d < -5*time.Second {
		t.Fatalf("the test database's clock is %s off this host's; the cases below assume they agree", d)
	}
	for name, offset := range map[string]time.Duration{"database ahead": 10 * time.Minute, "database behind": -10 * time.Minute} {
		v := f.verifier()
		v.clock = func(ctx context.Context) (time.Time, error) {
			var now time.Time
			err := f.db.QueryRowContext(ctx, `SELECT now() + $1::bigint * interval '1 microsecond'`, offset.Microseconds()).Scan(&now)
			return now, err
		}
		if _, err := v.Authenticate(t.Context(), h); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("%s: %v; want ErrUnauthenticated", name, err)
		}
	}
	if _, err := f.verifier().Authenticate(t.Context(), h); err != nil {
		t.Fatalf("the database's own clock: %v", err)
	}
}

func TestIssuerDownThenUp(t *testing.T) {
	f := newFixture(t)
	v := f.verifier()
	h := f.bearer(t, "h-viewer", "")
	f.down.Store(true)
	if _, err := v.Authenticate(t.Context(), h); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("issuer down: %v; want ErrUnavailable", err)
	}
	f.down.Store(false)
	if _, err := v.Authenticate(t.Context(), h); err != nil {
		t.Fatalf("issuer back: %v", err)
	}
}
