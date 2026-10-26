package api

// The ID-token probe of ginsys/bronzeward#370 against the test identity provider
// (fixtures/dev-idp; mise run dev-idp, then mise run go-idp): for each grant a client uses, the
// ID token and the access token are compared header by header and claim by claim, each candidate
// check is applied to both, and the ID token is presented to the server's real verifier. The
// record is docs/design/research/20261026-id-token-as-access-token.md. It asserts what the pinned
// provider and the server do today, the finding included, so that a change shows.

import (
	"encoding/base64"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/auth"
)

// jwtHeader is a compact JWS's protected header, decoded without a signature check.
func jwtHeader(t *testing.T, tok string) map[string]json.RawMessage {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.Split(tok, ".")[0])
	if err != nil {
		t.Fatal(err)
	}
	var h map[string]json.RawMessage
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatal(err)
	}
	return h
}

// shape is what the probe records of one token: its header type, which of the discriminating
// claims it carries, and its audience. No token, signature or claim value other than these is
// kept.
type shape struct {
	typ    string
	claims map[string]bool
	aud    []string
}

// probed is every claim the record compares: the ID token's own (OIDC Core §2, §3.1.3.6), RFC
// 9068 §2.2's access-token claims, and the groups claim the deployment maps to roles.
var probed = []string{"nonce", "at_hash", "azp", "auth_time", "amr", "sid", "client_id", "jti", "groups"}

func shapeOf(t *testing.T, tok string) shape {
	t.Helper()
	var s shape
	if err := json.Unmarshal(jwtHeader(t, tok)["typ"], &s.typ); err != nil {
		t.Fatalf("header typ: %v", err)
	}
	claims := jwtClaims(t, tok)
	s.claims = map[string]bool{}
	for _, c := range probed {
		_, s.claims[c] = claims[c]
	}
	if err := json.Unmarshal(claims["aud"], &s.aud); err != nil {
		t.Fatalf("aud %s: want an array", claims["aud"])
	}
	slices.Sort(s.aud)
	return s
}

func (s shape) String() string {
	var have []string
	for _, c := range probed {
		if s.claims[c] {
			have = append(have, c)
		}
	}
	return "typ " + s.typ + ", claims [" + strings.Join(have, " ") + "], aud " + strings.Join(s.aud, " ")
}

// candidates are the checks the record weighs; each says whether a token passes.
var candidates = []struct {
	name string
	pass func(shape) bool
}{
	{"1 typ at+jwt (RFC 9068 §2.1)", func(s shape) bool { return strings.EqualFold(s.typ, "at+jwt") }},
	{"2 no nonce and no at_hash", func(s shape) bool { return !s.claims["nonce"] && !s.claims["at_hash"] }},
	// The pinned provider puts client_id in both tokens (zitadel/oidc NewIDTokenClaims), so of RFC
	// 9068's required claims only jti tells them apart.
	{"2b jti present (RFC 9068 §2.2)", func(s shape) bool { return s.claims["jti"] }},
}

// Each grant's ID token and access token, compared. On the pinned provider both are typ JWT and
// name the same audience, so neither alternative 1 nor 3 can tell them apart; the ID token alone
// carries at_hash (and nonce, from an authorization request that sent one), the access token
// alone jti, so alternatives 2 and 2b each refuse every ID token and accept every access token.
// Both carry client_id. The server today accepts the ID token as the human, with no role: it
// carries no groups claim.
func TestIdentityProviderIDToken(t *testing.T) {
	p := newIDP(t)
	e := idpEnv(t, p)
	code := p.signIn(p.cli, loopback(), "h-viewer")
	web := p.signIn(p.web, p.webOrigin+"/callback", "h-viewer")
	st, renewed := p.refresh(p.web, web.Refresh)
	if st != 200 || renewed.ID == "" {
		t.Fatalf("refresh: %d %q; want 200 with an ID token", st, renewed.Error)
	}
	aud := []string{p.cli, p.web, p.project}
	slices.Sort(aud)
	// want is the record's table: the claims each token carries, every other probed claim absent.
	want := func(nonce bool) (idWant, atWant map[string]bool) {
		idWant, atWant = map[string]bool{}, map[string]bool{}
		for _, c := range probed {
			idWant[c], atWant[c] = false, false
		}
		for _, c := range []string{"at_hash", "azp", "auth_time", "amr", "sid", "client_id"} {
			idWant[c] = true
		}
		idWant["nonce"] = nonce
		for _, c := range []string{"client_id", "jti", "groups"} {
			atWant[c] = true
		}
		return idWant, atWant
	}
	dc, uc, exp, iv := p.startDevice()
	p.decide(uc, "h-viewer")
	device := p.poll(dc, iv, time.Now().Add(time.Duration(exp)*time.Second))
	for _, g := range []struct {
		grant string
		tk    tokens
		nonce bool // the authorization request sent a nonce
	}{
		{"authorization code, CLI", code, true}, {"authorization code, web", web, true},
		{"refresh, web", renewed, true}, {"device authorization, CLI", device, false},
	} {
		t.Run(g.grant, func(t *testing.T) {
			if g.tk.ID == "" || g.tk.Access == "" {
				t.Fatalf("%q: want an ID token and an access token", g.tk.Error)
			}
			id, at := shapeOf(t, g.tk.ID), shapeOf(t, g.tk.Access)
			t.Logf("ID token:     %s", id)
			t.Logf("access token: %s", at)
			if id.typ != "JWT" || at.typ != "JWT" {
				t.Errorf("header typ %q and %q; want JWT for both (internal/api/oidc/sign/signer.go:50)", id.typ, at.typ)
			}
			if !slices.Equal(id.aud, aud) || !slices.Equal(at.aud, aud) {
				t.Errorf("aud %v and %v; want both the CLI client, the web client and the project", id.aud, at.aud)
			}
			idWant, atWant := want(g.nonce)
			if !maps.Equal(id.claims, idWant) {
				t.Errorf("ID token claims %v; want %v", id.claims, idWant)
			}
			if !maps.Equal(at.claims, atWant) {
				t.Errorf("access token claims %v; want %v", at.claims, atWant)
			}
			for _, c := range candidates {
				t.Logf("check %s: ID token passes %v, access token passes %v", c.name, c.pass(id), c.pass(at))
			}
			e.accepted(p, g.tk.Access, "h-viewer", []auth.Role{auth.Viewer})
			// The finding: the ID token is served as the same human, with no role.
			e.accepted(p, g.tk.ID, "h-viewer", []auth.Role{})
		})
	}
}
