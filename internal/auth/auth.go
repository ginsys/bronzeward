// Package auth authenticates API requests (persistence-api.md §10): OIDC access tokens for
// humans (§10.1), Bronzeward-issued tokens for service identities (§10.2), and the refusal of
// revoked and deployment-denied subjects (§10.4). Route authorization is the API's (§10.3).
package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
// looked for again after a failure, so the server starts while the issuer is down.
func Discover(issuer string) KeySet {
	return &discovered{issuer: issuer, client: &http.Client{Timeout: issuerTimeout}}
}

// issuerTimeout bounds each request to the issuer. go-oidc fetches keys in the background with
// no cancellation, sharing one fetch between waiters: an unbounded fetch the issuer never
// answers would refuse every token until a restart.
const issuerTimeout = 10 * time.Second

type discovered struct {
	issuer string
	client *http.Client // bounds every request to the issuer
	mu     sync.Mutex
	keys   *oidc.RemoteKeySet
}

func (d *discovered) VerifySignature(ctx context.Context, raw string) ([]byte, error) {
	keys, err := d.keySet(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: discovery: %v", ErrUnavailable, err)
	}
	return keys.VerifySignature(ctx, raw)
}

func (d *discovered) keySet(ctx context.Context) (*oidc.RemoteKeySet, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.keys != nil {
		return d.keys, nil
	}
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
	d.keys = oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), d.client), doc.JWKS)
	return d.keys, nil
}
