package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
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
	// stallJWKS makes the key set endpoint hold requests until the client gives up.
	stallJWKS atomic.Bool
	// stallDiscovery holds discovery requests the same way; held receives one value per request
	// held.
	stallDiscovery atomic.Bool
	held           chan struct{}
	stop           chan struct{} // closed at cleanup, releasing any held request
	jwksHits       atomic.Int32  // requests the key set endpoint answered
	discoveryHits  atomic.Int32  // requests for the discovery document, answered or not
	// jwksURI, when set, replaces the jwks_uri the discovery document advertises; redirect maps
	// a path to the URL it answers with a 302.
	jwksURI  atomic.Value // string
	redirect sync.Map     // path → URL
	// jwksBody, when not empty, is the key set endpoint's 200 answer in place of the key set.
	jwksBody atomic.Value // string
	// padPath, when set, names a path whose JSON answer carries an extra "pad" member of padSize
	// bytes: still valid, only large.
	padPath atomic.Value // string
}

const padSize = 2 << 20 // past issuerBodyLimit

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
	f.stop, f.held = make(chan struct{}), make(chan struct{}, 16)
	srv := &httptest.Server{Listener: l, Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			f.discoveryHits.Add(1)
		}
		if f.down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		if (r.URL.Path == "/jwks" && f.stallJWKS.Load()) ||
			(r.URL.Path == "/.well-known/openid-configuration" && f.stallDiscovery.Load()) {
			select {
			case f.held <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
			case <-f.stop:
			}
			return
		}
		if to, ok := f.redirect.Load(r.URL.Path); ok {
			http.Redirect(w, r, to.(string), http.StatusFound)
			return
		}
		if p, ok := f.padPath.Load().(string); ok && r.URL.Path == p {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			var doc map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			doc["pad"] = strings.Repeat("x", padSize)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doc)
			return
		}
		if u, ok := f.jwksURI.Load().(string); ok && r.URL.Path == "/.well-known/openid-configuration" {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			var doc map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			doc["jwks_uri"] = u
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doc)
			return
		}
		if b, _ := f.jwksBody.Load().(string); r.URL.Path == "/jwks" && b != "" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(b))
			return
		}
		if r.URL.Path == "/jwks" {
			f.jwksHits.Add(1)
		}
		h.ServeHTTP(w, r)
	})}}
	srv.Start()
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(f.stop) }) // runs first: Close waits for held requests
	f.cfg = testAuth(f.iss.URL)
	return f
}

func (f *fixture) verifier() *Verifier { return NewVerifier(f.cfg, f.db, Discover(f.cfg.OIDC)) }

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

// A key set fetch the issuer never answers must end, so a later request fetches again instead of
// waiting on the stalled one until the server restarts.
func TestStalledKeySetFetchRecovers(t *testing.T) {
	f := newFixture(t)
	d := Discover(f.cfg.OIDC).(*discovered)
	d.client = &http.Client{Timeout: 200 * time.Millisecond}
	v := NewVerifier(f.cfg, f.db, d)
	h := f.bearer(t, "h-viewer", "")
	f.stallJWKS.Store(true)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err := v.Authenticate(ctx, h); err == nil {
		t.Fatal("authenticated while the key set endpoint stalls")
	}
	f.stallJWKS.Store(false)
	ctx, cancel = context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err := v.Authenticate(ctx, h); err != nil {
		t.Fatalf("key set endpoint answering again: %v", err)
	}
}

// A token whose signature fails against the cached keys makes go-oidc fetch the key set again.
// Unauthenticated callers must not turn every request into a fetch from the issuer: after a
// successful fetch, the next waits out the refetch interval.
func TestKeySetRefetchIsRateLimited(t *testing.T) {
	f := newFixture(t)
	d := Discover(f.cfg.OIDC).(*discovered)
	d.refetch = 300 * time.Millisecond
	v := NewVerifier(f.cfg, f.db, d)
	if _, err := v.Authenticate(t.Context(), f.bearer(t, "h-viewer", "")); err != nil {
		t.Fatal(err)
	}
	bad := func() {
		t.Helper()
		for _, defect := range []string{"unknown-kid", "wrong-key", "bad-signature"} {
			if _, err := v.Authenticate(t.Context(), f.bearer(t, "h-viewer", defect)); !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("%s: %v; want ErrUnauthenticated", defect, err)
			}
		}
	}
	bad()
	if n := f.jwksHits.Load(); n != 1 {
		t.Fatalf("%d key set fetches within the refetch interval; want 1", n)
	}
	time.Sleep(400 * time.Millisecond)
	bad()
	if n := f.jwksHits.Load(); n != 2 {
		t.Fatalf("%d key set fetches after the interval; want 2", n)
	}
	if _, err := v.Authenticate(t.Context(), f.bearer(t, "h-viewer", "")); err != nil {
		t.Fatalf("a valid token after limited refetches: %v", err)
	}
}

// A key set answer go-oidc rejects is a failed fetch: it starts no refetch interval, so the next
// request fetches again once the issuer answers properly.
func TestUndecodableKeySetSetsNoLimit(t *testing.T) {
	for name, body := range map[string]string{
		"truncated":          `{"keys":[`,
		"empty key":          `{"keys":[{}]}`,
		"broken RSA modulus": `{"keys":[{"kty":"RSA","alg":"RS256","kid":"k","n":"!","e":"AQAB"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			v := f.verifier() // the production interval, a minute
			h := f.bearer(t, "h-viewer", "")
			f.jwksBody.Store(body)
			if _, err := v.Authenticate(t.Context(), h); err == nil {
				t.Fatal("accepted with an undecodable key set")
			}
			f.jwksBody.Store("")
			if _, err := v.Authenticate(t.Context(), h); err != nil {
				t.Fatalf("after the key set recovered: %v", err)
			}
		})
	}
}

// An issuer answer is read into memory, so its size is bounded: an oversized discovery document
// or key set is a failed request, even when it would otherwise decode.
func TestOversizedIssuerAnswerIsRefused(t *testing.T) {
	for _, path := range []string{"/.well-known/openid-configuration", "/jwks"} {
		t.Run(path, func(t *testing.T) {
			f := newFixture(t)
			v := f.verifier()
			h := f.bearer(t, "h-viewer", "")
			f.padPath.Store(path)
			if _, err := v.Authenticate(t.Context(), h); err == nil {
				t.Fatalf("accepted a %d-byte %s answer", padSize, path)
			}
		})
	}
}

func TestCappedReaderBoundary(t *testing.T) {
	for size, ok := range map[int]bool{issuerBodyLimit - 1: true, issuerBodyLimit: true, issuerBodyLimit + 1: false} {
		body := io.NopCloser(strings.NewReader(strings.Repeat("x", size)))
		b, err := io.ReadAll(&cappedReader{ReadCloser: body, left: issuerBodyLimit})
		if (err == nil) != ok || (ok && len(b) != size) {
			t.Errorf("%d bytes: read %d, %v; want ok=%v", size, len(b), err, ok)
		}
	}
}

// offLoopback fails, and records, every request to a host that is not a loopback IP.
type offLoopback struct {
	mu   sync.Mutex
	urls []string
}

func (o *offLoopback) RoundTrip(r *http.Request) (*http.Response, error) {
	if ip := net.ParseIP(r.URL.Hostname()); ip == nil || !ip.IsLoopback() {
		o.mu.Lock()
		o.urls = append(o.urls, r.URL.String())
		o.mu.Unlock()
		return nil, errors.New("request off loopback")
	}
	return http.DefaultTransport.RoundTrip(r)
}

// Discovery and keys come over https or from this host only: neither the advertised jwks_uri
// nor a redirect may move a fetch onto plain http elsewhere, where an on-path attacker could
// serve a signing key.
func TestIssuerFetchesStayOnAuthenticatedTransport(t *testing.T) {
	for name, set := range map[string]func(*fixture){
		"advertised jwks_uri": func(f *fixture) { f.jwksURI.Store("http://keys.example.test/jwks") },
		"key set redirect":    func(f *fixture) { f.redirect.Store("/jwks", "http://keys.example.test/jwks") },
		"discovery redirect": func(f *fixture) {
			f.redirect.Store("/.well-known/openid-configuration", "http://idp.example.test/.well-known/openid-configuration")
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			set(f)
			d := Discover(f.cfg.OIDC).(*discovered)
			o := &offLoopback{}
			d.client.Transport = o
			if _, err := NewVerifier(f.cfg, f.db, d).Authenticate(t.Context(), f.bearer(t, "h-viewer", "")); err == nil {
				t.Fatal("token accepted")
			}
			o.mu.Lock()
			defer o.mu.Unlock()
			if len(o.urls) > 0 {
				t.Fatalf("fetched over plain http off this host: %v", o.urls)
			}
		})
	}
}

// namedHosts sends a request for a host in names to addr, as a cluster's DNS would, and records
// every host asked for.
type namedHosts struct {
	addr  string
	names []string
	mu    sync.Mutex
	asked []string
}

func (n *namedHosts) RoundTrip(r *http.Request) (*http.Response, error) {
	n.mu.Lock()
	n.asked = append(n.asked, r.URL.Hostname())
	n.mu.Unlock()
	if slices.Contains(n.names, r.URL.Hostname()) {
		r = r.Clone(r.Context())
		r.URL.Host = n.addr
	}
	return http.DefaultTransport.RoundTrip(r)
}

// persistence-api.md §10.1: listing a host for plain http lists that host alone. The key set a
// listed issuer advertises, or redirects to, on another plain-http host is refused and never
// fetched; the control lists that host too, and the same token verifies.
func TestPlainHTTPHostsDoNotExtendToKeySet(t *testing.T) {
	for name, set := range map[string]func(f *fixture, keys string){
		"advertised jwks_uri": func(f *fixture, keys string) { f.jwksURI.Store(keys) },
		"key set redirect":    func(f *fixture, keys string) { f.redirect.Store("/jwks", keys) },
	} {
		for _, listed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, keys host listed %v", name, listed), func(t *testing.T) {
				f := newFixture(t)
				// The key host serves the issuer's own key set, without the fixture's redirects.
				keys := httptest.NewServer(f.iss.Handler())
				t.Cleanup(keys.Close)
				addr := strings.TrimPrefix(keys.URL, "http://")
				_, port, err := net.SplitHostPort(addr)
				if err != nil {
					t.Fatal(err)
				}
				set(f, "http://keys.cluster.test:"+port+"/jwks")
				f.cfg.OIDC.PlainHTTPHosts = []string{"idp.cluster.test"}
				if listed {
					f.cfg.OIDC.PlainHTTPHosts = append(f.cfg.OIDC.PlainHTTPHosts, "keys.cluster.test")
				}
				d := Discover(f.cfg.OIDC).(*discovered)
				n := &namedHosts{addr: addr, names: []string{"keys.cluster.test"}}
				d.client.Transport = n
				_, err = NewVerifier(f.cfg, f.db, d).Authenticate(t.Context(), f.bearer(t, "h-viewer", ""))
				n.mu.Lock()
				defer n.mu.Unlock()
				if listed {
					if err != nil {
						t.Fatalf("control: %v", err)
					}
					return
				}
				if err == nil {
					t.Fatal("token accepted with keys from an unlisted plain-http host")
				}
				if slices.Contains(n.asked, "keys.cluster.test") {
					t.Fatalf("the unlisted host was asked: %v", n.asked)
				}
			})
		}
	}
}

// persistence-api.md §10.1: a plain-http issuer is trusted for its network path, so an ambient
// proxy (HTTP_PROXY without the host in NO_PROXY) must not carry its discovery or key set, where
// the proxy could answer with its own key. The control: an https request still takes the proxy.
func TestPlainHTTPIssuerBypassesAmbientProxy(t *testing.T) {
	f := newFixture(t)
	var hits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "proxied", http.StatusBadGateway)
	}))
	t.Cleanup(proxy.Close)
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	d := Discover(f.cfg.OIDC).(*discovered)
	d.proxy = func(*http.Request) (*url.URL, error) { return proxyURL, nil }
	if _, err := NewVerifier(f.cfg, f.db, d).Authenticate(t.Context(), f.bearer(t, "h-viewer", "")); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("the proxy carried %d plain-http requests to the issuer", n)
	}
	r := httptest.NewRequest(http.MethodGet, "https://idp.example.test/.well-known/openid-configuration", nil)
	if got, err := d.client.Transport.(*http.Transport).Proxy(r); err != nil || got != proxyURL {
		t.Fatalf("an https request's proxy = %v, %v; want the ambient proxy", got, err)
	}
}

// While one request waits on a slow discovery, another must give up at its own deadline rather
// than queue behind it.
func TestDiscoveryWaitHonoursTheDeadline(t *testing.T) {
	f := newFixture(t)
	v := f.verifier()
	h := f.bearer(t, "h-viewer", "")
	f.stallDiscovery.Store(true)
	first := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		_, err := v.Authenticate(ctx, h)
		first <- err
	}()
	select {
	case <-f.held:
	case <-time.After(5 * time.Second):
		t.Fatal("the first request never reached discovery")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := v.Authenticate(ctx, h); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("second request: %v; want ErrUnavailable", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("second request returned after %s, past its 200ms deadline", d)
	}
	if err := <-first; !errors.Is(err, ErrUnavailable) {
		t.Fatalf("first request: %v; want ErrUnavailable", err)
	}
}

func TestIssuerDownThenUp(t *testing.T) {
	f := newFixture(t)
	d := Discover(f.cfg.OIDC).(*discovered)
	d.retry = 100 * time.Millisecond
	v := NewVerifier(f.cfg, f.db, d)
	h := f.bearer(t, "h-viewer", "")
	f.down.Store(true)
	if _, err := v.Authenticate(t.Context(), h); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("issuer down: %v; want ErrUnavailable", err)
	}
	f.down.Store(false)
	time.Sleep(150 * time.Millisecond)
	if _, err := v.Authenticate(t.Context(), h); err != nil {
		t.Fatalf("issuer back: %v", err)
	}
}

// A failed discovery is not repeated for every request: until the retry interval has passed,
// requests get the failure without asking the issuer again. After it, discovery runs again.
func TestFailedDiscoveryIsNotRepeated(t *testing.T) {
	f := newFixture(t)
	d := Discover(f.cfg.OIDC).(*discovered)
	d.retry = 300 * time.Millisecond
	v := NewVerifier(f.cfg, f.db, d)
	h := f.bearer(t, "h-viewer", "")
	f.down.Store(true)
	for range 5 {
		if _, err := v.Authenticate(t.Context(), h); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("issuer down: %v; want ErrUnavailable", err)
		}
	}
	if n := f.discoveryHits.Load(); n != 1 {
		t.Fatalf("%d discovery requests within the retry interval; want 1", n)
	}
	f.down.Store(false)
	time.Sleep(400 * time.Millisecond)
	if _, err := v.Authenticate(t.Context(), h); err != nil {
		t.Fatalf("issuer back after the retry interval: %v", err)
	}
	if n := f.discoveryHits.Load(); n != 2 {
		t.Fatalf("%d discovery requests after the retry interval; want 2", n)
	}
}

// A discovery the request itself gave up on says nothing about the issuer, so it is not kept as
// a failure: the next request discovers at once.
func TestAbandonedDiscoveryIsNotKept(t *testing.T) {
	f := newFixture(t)
	v := f.verifier() // the production retry interval
	h := f.bearer(t, "h-viewer", "")
	f.stallDiscovery.Store(true)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if _, err := v.Authenticate(ctx, h); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("stalled discovery: %v; want ErrUnavailable", err)
	}
	f.stallDiscovery.Store(false)
	if _, err := v.Authenticate(t.Context(), h); err != nil {
		t.Fatalf("after an abandoned discovery: %v", err)
	}
}
