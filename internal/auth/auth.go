// Package auth authenticates API requests (persistence-api.md §10): OIDC access tokens for
// humans (§10.1), Bronzeward-issued tokens for service identities (§10.2), and the refusal of
// revoked and deployment-denied subjects (§10.4). Route authorization is the API's (§10.3).
package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/ginsys/bronzeward/internal/config"
)

type Kind string

const (
	Human   Kind = "human"
	Service Kind = "service"
)

type Role string

const (
	Viewer        Role = "viewer"
	Author        Role = "author"
	Publisher     Role = "publisher"
	Approver      Role = "approver"
	RecoveryAdmin Role = "recovery-admin"
)

// allRoles is design §13.7's order; a principal's roles are listed in it.
var allRoles = []Role{Viewer, Author, Publisher, Approver, RecoveryAdmin}

// ParseRoles splits a comma-separated role list without validating it.
func ParseRoles(s string) []Role {
	if s == "" {
		return nil
	}
	var rs []Role
	for _, r := range strings.Split(s, ",") {
		rs = append(rs, Role(strings.TrimSpace(r)))
	}
	return rs
}

func ordered(has map[Role]bool) []Role {
	var rs []Role
	for _, r := range allRoles {
		if has[r] {
			rs = append(rs, r)
		}
	}
	return rs
}

// Principal is who a request is from, with the roles its credential carries.
type Principal struct {
	Kind    Kind
	ID      string // idn identifier; empty for a human with no row yet (§10: the first admitted mutating request creates it)
	Issuer  string // human only
	Subject string // human only
	TokenID string // service only
	Roles   []Role
	Expiry  time.Time // the credential's end: an event stream ends no later (§8.3)
}

var (
	// ErrUnauthenticated is 401: no credential, or one that fails a token check.
	ErrUnauthenticated = errors.New("unauthenticated")
	// ErrIdentityRevoked is 403 identity-revoked: a valid credential naming a revoked or denied subject.
	ErrIdentityRevoked = errors.New("identity revoked")
	// ErrUnavailable: the issuer's discovery document or the database could not be read.
	ErrUnavailable = errors.New("authentication unavailable")
)

// KeySet verifies a JWS against the issuer's published keys; *oidc.RemoteKeySet is one.
type KeySet interface {
	VerifySignature(ctx context.Context, jwt string) ([]byte, error)
}

const skew = 60 * time.Second

// asymmetric is every accepted signature algorithm: no "none", no HMAC (§10.1).
var asymmetric = []jose.SignatureAlgorithm{
	jose.RS256, jose.RS384, jose.RS512, jose.PS256, jose.PS384, jose.PS512,
	jose.ES256, jose.ES384, jose.ES512, jose.EdDSA,
}

type Verifier struct {
	oidc   config.OIDC
	groups map[string][]Role
	denied Denied
	db     *sql.DB
	keys   KeySet
	clock  func(context.Context) (time.Time, error) // the database's now() (PA §5 rule 4); tests replace it
}

func NewVerifier(a config.Auth, db *sql.DB, keys KeySet) *Verifier {
	groups := map[string][]Role{}
	for role, gs := range map[Role][]string{
		Viewer: a.Roles.Viewer, Author: a.Roles.Author, Publisher: a.Roles.Publisher,
		Approver: a.Roles.Approver, RecoveryAdmin: a.Roles.RecoveryAdmin,
	} {
		for _, g := range gs {
			groups[g] = append(groups[g], role)
		}
	}
	clock := func(ctx context.Context) (time.Time, error) {
		var now time.Time
		err := db.QueryRowContext(ctx, `SELECT now()`).Scan(&now)
		return now, err
	}
	return &Verifier{oidc: a.OIDC, groups: groups, denied: NewDenied(a.DeniedSubjects), db: db, keys: keys, clock: clock}
}

// Authenticate verifies an Authorization header value. The wrapped reason is for logs, never
// for the client. It never writes: a human's principal row is the API's to create (§10).
func (v *Verifier) Authenticate(ctx context.Context, authorization string) (Principal, error) {
	scheme, raw, ok := strings.Cut(authorization, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || raw == "" || strings.ContainsAny(raw, " \t\r\n") {
		return Principal{}, fmt.Errorf("%w: no bearer token", ErrUnauthenticated)
	}
	if strings.HasPrefix(raw, tokenPrefix) {
		return v.automation(ctx, raw)
	}
	return v.human(ctx, raw)
}

// human verifies an OIDC access token (§10.1). ValidateWithLeeway treats exp and iat as optional
// (go-jose v4 jwt/validation.go), so their presence is checked here.
func (v *Verifier) human(ctx context.Context, raw string) (Principal, error) {
	if _, err := jwt.ParseSigned(raw, asymmetric); err != nil {
		return Principal{}, fmt.Errorf("%w: %v", ErrUnauthenticated, err)
	}
	payload, err := v.keys.VerifySignature(ctx, raw)
	if errors.Is(err, ErrUnavailable) {
		return Principal{}, err
	}
	if err != nil {
		return Principal{}, fmt.Errorf("%w: signature: %v", ErrUnauthenticated, err)
	}
	var c jwt.Claims
	if err := json.Unmarshal(payload, &c); err != nil { // a numeric sub fails here
		return Principal{}, fmt.Errorf("%w: claims: %v", ErrUnauthenticated, err)
	}
	// Time checks use the database's clock, never this process's (PA §5 rule 4, §10.1 "now()").
	now, err := v.clock(ctx)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: clock: %v", ErrUnavailable, err)
	}
	if err := c.ValidateWithLeeway(jwt.Expected{Issuer: v.oidc.Issuer, AnyAudience: jwt.Audience{v.oidc.Audience}, Time: now}, skew); err != nil {
		return Principal{}, fmt.Errorf("%w: %v", ErrUnauthenticated, err)
	}
	switch {
	case c.Expiry == nil:
		return Principal{}, fmt.Errorf("%w: no exp", ErrUnauthenticated)
	case c.IssuedAt == nil:
		return Principal{}, fmt.Errorf("%w: no iat", ErrUnauthenticated)
	case c.Subject == "":
		return Principal{}, fmt.Errorf("%w: no sub", ErrUnauthenticated)
	case c.Expiry.Time().Sub(c.IssuedAt.Time()) > v.oidc.MaxTokenLifetime:
		return Principal{}, fmt.Errorf("%w: lifetime over %s", ErrUnauthenticated, v.oidc.MaxTokenLifetime)
	}
	roles, err := v.roles(payload)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: %v", ErrUnauthenticated, err)
	}
	if v.denied.Human(c.Issuer, c.Subject) {
		return Principal{}, fmt.Errorf("%w: %s is in deniedSubjects", ErrIdentityRevoked, c.Subject)
	}
	p := Principal{Kind: Human, Issuer: c.Issuer, Subject: c.Subject, Roles: roles, Expiry: c.Expiry.Time()}
	var revoked bool
	err = v.db.QueryRowContext(ctx, `SELECT id, revoked FROM principal WHERE kind = 'human' AND iss = $1 AND sub = $2`,
		c.Issuer, c.Subject).Scan(&p.ID, &revoked)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		p.ID = ""
	case err != nil:
		return Principal{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	case revoked:
		return Principal{}, fmt.Errorf("%w: %s", ErrIdentityRevoked, p.ID)
	}
	return p, nil
}

// roles maps the groups claim to roles (§10.1). A missing or null claim grants none; one that is
// not an array whose every element is a string is a defect. It decodes into []any, not
// []string: encoding/json leaves a string untouched for a JSON null element, without error
// (encoding/json/decode.go:94-97, Go 1.27).
func (v *Verifier) roles(payload []byte) ([]Role, error) {
	var all map[string]any
	if err := json.Unmarshal(payload, &all); err != nil {
		return nil, err
	}
	claim := all[v.oidc.GroupsClaim]
	if claim == nil {
		return nil, nil
	}
	elems, ok := claim.([]any)
	if !ok {
		return nil, fmt.Errorf("claim %s is not an array", v.oidc.GroupsClaim)
	}
	has := map[Role]bool{}
	for _, e := range elems {
		g, ok := e.(string)
		if !ok {
			return nil, fmt.Errorf("claim %s holds a non-string element", v.oidc.GroupsClaim)
		}
		for _, r := range v.groups[g] {
			has[r] = true
		}
	}
	return ordered(has), nil
}

// Discover returns the issuer's key set, found through its discovery document on first use and
// looked for again once discoveryRetryInterval has passed after a failure, so the server starts
// while the issuer is down.
func Discover(issuer string) KeySet {
	return &discovered{issuer: issuer, client: &http.Client{Timeout: issuerTimeout, CheckRedirect: secureRedirect},
		refetch: keyRefetchInterval, retry: discoveryRetryInterval, discovering: make(chan struct{}, 1)}
}

// secureRedirect refuses a redirect that leaves authenticated transport, where an on-path
// attacker could answer in the issuer's place.
func secureRedirect(r *http.Request, via []*http.Request) error {
	if !config.SecureTransport(r.URL) {
		return fmt.Errorf("refusing a redirect to %s: not https and not this host", r.URL.Redacted())
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

const (
	// issuerTimeout bounds each request to the issuer. go-oidc fetches keys in the background
	// with no cancellation, sharing one fetch between waiters: an unbounded fetch the issuer
	// never answers would refuse every token until a restart.
	issuerTimeout = 10 * time.Second
	// keyRefetchInterval is the least time between successful key set fetches. go-oidc fetches
	// again for every token whose signature the cached keys do not verify, so without it any
	// caller could make each request a fetch from the issuer. A token signed with a key the
	// issuer published since the last fetch is refused until the interval has passed.
	keyRefetchInterval = time.Minute
	// discoveryRetryInterval is how long a failed discovery is answered without asking the issuer
	// again, so requests are not forwarded to an issuer that is down at request rate, nor queued
	// behind one timed-out attempt after another. Tokens are refused for up to this long after
	// the issuer recovers.
	discoveryRetryInterval = 5 * time.Second
)

type discovered struct {
	issuer  string
	client  *http.Client  // bounds every request to the issuer
	refetch time.Duration // the least time between successful key set fetches
	retry   time.Duration // how long a failed discovery is answered without asking the issuer
	// discovering holds a value while one request runs discovery. It is a lock a waiter can give
	// up on: a request queued behind a slow discovery fails at its own deadline.
	discovering chan struct{}
	keys        *oidc.RemoteKeySet
	failed      error // the last discovery failure, answered until failedAt + retry
	failedAt    time.Time
}

// limitedFetch refuses a key set fetch within every of the last successful one: a 200 whose body
// decodes as a key set. A failed fetch sets no limit, so the key set recovers as soon as the
// issuer does.
type limitedFetch struct {
	next  http.RoundTripper
	every time.Duration
	mu    sync.Mutex
	last  time.Time
}

func (l *limitedFetch) RoundTrip(r *http.Request) (*http.Response, error) {
	l.mu.Lock()
	limited := !l.last.IsZero() && time.Since(l.last) < l.every
	l.mu.Unlock()
	if limited {
		return nil, fmt.Errorf("key set fetched less than %s ago", l.every)
	}
	resp, err := l.next.RoundTrip(r)
	if err != nil || resp.StatusCode != http.StatusOK {
		return resp, err
	}
	// go-oidc decodes the body only after this returns, so decode it here too: a truncated or
	// malformed answer is a failed fetch.
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if keySetDecodes(body) {
		l.mu.Lock()
		l.last = time.Now()
		l.mu.Unlock()
	}
	return resp, nil
}

// keySetDecodes reports whether go-oidc accepts body as a key set, by its own steps (v3.21.0
// jwks.go:244-300): it skips a key whose alg it does not support or whose type go-jose does not
// know, and fails the whole set on any other key that does not decode.
func keySetDecodes(body []byte) bool {
	var set struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if json.Unmarshal(body, &set) != nil {
		return false
	}
	for _, k := range set.Keys {
		var meta struct {
			Alg string `json:"alg"`
		}
		if json.Unmarshal(k, &meta) != nil {
			return false
		}
		if meta.Alg != "" && !oidcAlgs[meta.Alg] {
			continue
		}
		var jwk jose.JSONWebKey
		if err := json.Unmarshal(k, &jwk); err != nil && !errors.Is(err, jose.ErrUnsupportedKeyType) {
			return false
		}
	}
	return true
}

// oidcAlgs are the algorithms go-oidc's key set supports (v3.21.0 jose.go allAlgs).
var oidcAlgs = map[string]bool{
	oidc.RS256: true, oidc.RS384: true, oidc.RS512: true,
	oidc.ES256: true, oidc.ES384: true, oidc.ES512: true,
	oidc.PS256: true, oidc.PS384: true, oidc.PS512: true,
	oidc.EdDSA: true,
}

func (d *discovered) VerifySignature(ctx context.Context, raw string) ([]byte, error) {
	keys, err := d.keySet(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: discovery: %v", ErrUnavailable, err)
	}
	return keys.VerifySignature(ctx, raw)
}

func (d *discovered) keySet(ctx context.Context) (*oidc.RemoteKeySet, error) {
	select {
	case d.discovering <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-d.discovering }()
	if d.keys != nil {
		return d.keys, nil
	}
	if d.failed != nil && time.Since(d.failedAt) < d.retry {
		return nil, fmt.Errorf("%w (discovery is tried again %s after a failure)", d.failed, d.retry)
	}
	keys, err := d.discover(ctx)
	switch {
	case err == nil:
		d.keys, d.failed = keys, nil
	case ctx.Err() == nil: // a request that gave up says nothing about the issuer
		d.failed, d.failedAt = err, time.Now()
	}
	return keys, err
}

// discover reads the issuer's discovery document and returns its key set.
func (d *discovered) discover(ctx context.Context) (*oidc.RemoteKeySet, error) {
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, d.client), d.issuer) // refuses a document whose issuer differs
	if err != nil {
		return nil, err
	}
	var doc struct {
		JWKS string `json:"jwks_uri"`
	}
	if err := p.Claims(&doc); err != nil {
		return nil, err
	}
	if doc.JWKS == "" {
		return nil, errors.New("the discovery document names no jwks_uri")
	}
	// The keys decide whose tokens verify, so they need the same transport as the issuer.
	if u, err := url.Parse(doc.JWKS); err != nil || u.Host == "" || !config.SecureTransport(u) {
		return nil, fmt.Errorf("the discovery document's jwks_uri %q is not https and not this host", doc.JWKS)
	}
	next := d.client.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	fetch := &http.Client{Timeout: d.client.Timeout, CheckRedirect: d.client.CheckRedirect,
		Transport: &limitedFetch{next: next, every: d.refetch}}
	return oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), fetch), doc.JWKS), nil
}
