package api

// The sign-in self-test against the test identity provider (fixtures/dev-idp; mise run dev-idp,
// then mise run go-idp): each grant a client uses, driven headlessly through Zitadel's Session
// API and its OIDC CreateCallback call, ending in a token the server's real verifier accepts
// with the right roles, read back from GET /identity. The live-check record is
// docs/design/research/20261022-zitadel-test-idp.md. Without BW_TEST_IDP_ISSUER every test skips,
// unless BW_REQUIRE_IDP=1, when it fails.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/fixtures/oidc/issuer"
	"github.com/ginsys/bronzeward/internal/auth"
)

type idp struct {
	t                                     *testing.T
	issuer, project, cli, web, webOrigin  string
	password, login                       string            // the humans' password; the login client's token
	deviceLifetime                        string            // the device code's configured lifetime, in seconds
	users                                 map[string]string // human -> Zitadel user ID
	authorize, token, revoke, end, device string            // endpoints from discovery
	userinfo                              string
	hc                                    *http.Client // never follows a redirect
}

func newIDP(t *testing.T) *idp {
	t.Helper()
	p := &idp{t: t, issuer: os.Getenv("BW_TEST_IDP_ISSUER"), users: map[string]string{},
		hc: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if p.issuer == "" {
		if os.Getenv("BW_REQUIRE_IDP") == "1" {
			t.Fatal("BW_REQUIRE_IDP=1 but BW_TEST_IDP_ISSUER is unset (mise run dev-idp)")
		}
		t.Skip("no test identity provider: BW_TEST_IDP_ISSUER is unset (mise run dev-idp, then mise run go-idp)")
	}
	for v, dst := range map[string]*string{"PROJECT": &p.project, "CLI_CLIENT": &p.cli, "WEB_CLIENT": &p.web,
		"WEB_ORIGIN": &p.webOrigin, "PASSWORD": &p.password, "DEVICE_LIFETIME": &p.deviceLifetime} {
		if *dst = os.Getenv("BW_TEST_IDP_" + v); *dst == "" {
			t.Fatalf("BW_TEST_IDP_ISSUER is set but BW_TEST_IDP_%s is not", v)
		}
	}
	tok, err := os.ReadFile(os.Getenv("BW_TEST_IDP_LOGIN_TOKEN_FILE"))
	if err != nil {
		t.Fatalf("the login client's token: %v", err)
	}
	p.login = strings.TrimSpace(string(tok))
	for kv := range strings.SplitSeq(os.Getenv("BW_TEST_IDP_USERS"), ",") {
		if h, id, ok := strings.Cut(kv, "="); ok {
			p.users[h] = id
		}
	}
	var doc struct {
		Issuer   string `json:"issuer"`
		Auth     string `json:"authorization_endpoint"`
		Token    string `json:"token_endpoint"`
		Revoke   string `json:"revocation_endpoint"`
		End      string `json:"end_session_endpoint"`
		Device   string `json:"device_authorization_endpoint"`
		UserInfo string `json:"userinfo_endpoint"`
	}
	if code := p.json("GET", p.issuer+"/.well-known/openid-configuration", nil, "", nil, &doc); code != 200 || doc.Issuer != p.issuer {
		t.Fatalf("the provider's discovery document: %d, issuer %q; want 200, %q (mise run dev-idp)", code, doc.Issuer, p.issuer)
	}
	p.authorize, p.token, p.revoke, p.end, p.device, p.userinfo = doc.Auth, doc.Token, doc.Revoke, doc.End, doc.Device, doc.UserInfo
	return p
}

// with is p for a subtest: its failures belong to t.
func (p *idp) with(t *testing.T) *idp {
	q := *p
	q.t = t
	return &q
}

// json sends one request (a form when form is set, else body as JSON, with bearer when set) and
// decodes the answer into out; it returns the status.
func (p *idp) json(method, u string, form url.Values, bearer string, body, out any) int {
	p.t.Helper()
	var rd io.Reader
	ct := ""
	switch {
	case form != nil:
		rd, ct = strings.NewReader(form.Encode()), "application/x-www-form-urlencoded"
	case body != nil:
		b, err := json.Marshal(body)
		if err != nil {
			p.t.Fatal(err)
		}
		rd, ct = strings.NewReader(string(b)), "application/json"
	}
	r, err := http.NewRequestWithContext(p.t.Context(), method, u, rd)
	if err != nil {
		p.t.Fatal(err)
	}
	if ct != "" {
		r.Header.Set("Content-Type", ct)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := p.hc.Do(r)
	if err != nil {
		p.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		p.t.Fatal(err)
	}
	if out != nil && len(raw) > 0 && json.Unmarshal(raw, out) != nil {
		p.t.Fatalf("%s %s: %d, not JSON: %s", method, u, resp.StatusCode, raw)
	}
	return resp.StatusCode
}

// tokens is a token endpoint's answer, a success or an error.
type tokens struct {
	Access    string `json:"access_token"`
	Refresh   string `json:"refresh_token"`
	ID        string `json:"id_token"`
	ExpiresIn int    `json:"expires_in"`
	Error     string `json:"error"`
}

// session signs human in through the Session API, as a login UI would.
func (p *idp) session(human string) map[string]string {
	p.t.Helper()
	uid := p.users[human]
	if uid == "" {
		p.t.Fatalf("no user %s in BW_TEST_IDP_USERS", human)
	}
	var s struct{ SessionID, SessionToken string }
	if code := p.json("POST", p.issuer+"/v2/sessions", nil, p.login, map[string]any{"checks": map[string]any{
		"user": map[string]string{"userId": uid}, "password": map[string]string{"password": p.password}}}, &s); code != 200 && code != 201 {
		p.t.Fatalf("session for %s: %d", human, code)
	}
	return map[string]string{"sessionId": s.SessionID, "sessionToken": s.SessionToken}
}

func random(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// signIn runs authorization code with PKCE S256 for human on client, redirecting to redirect.
func (p *idp) signIn(client, redirect, human string) tokens {
	p.t.Helper()
	verifier, state := random(48), random(16)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {client}, "redirect_uri": {redirect}, "response_type": {"code"}, "state": {state},
		"scope": {"openid offline_access urn:zitadel:iam:org:project:id:" + p.project + ":aud"}, "nonce": {random(16)},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}}
	resp, err := p.hc.Get(p.authorize + "?" + q.Encode())
	if err != nil {
		p.t.Fatal(err)
	}
	resp.Body.Close()
	loc, err := resp.Location()
	if err != nil || loc.Query().Get("authRequest") == "" {
		p.t.Fatalf("authorize for %s: %d %v; want a redirect to the login UI with an authRequest", human, resp.StatusCode, loc)
	}
	var cb struct{ CallbackURL string }
	if code := p.json("POST", p.issuer+"/v2/oidc/auth_requests/"+loc.Query().Get("authRequest"), nil, p.login,
		map[string]any{"session": p.session(human)}, &cb); code != 200 {
		p.t.Fatalf("callback for %s: %d", human, code)
	}
	back, err := url.Parse(cb.CallbackURL)
	if err != nil || !strings.HasPrefix(cb.CallbackURL, redirect+"?") || back.Query().Get("state") != state || back.Query().Get("code") == "" {
		p.t.Fatalf("callback URL %q; want %s with the state and a code", cb.CallbackURL, redirect)
	}
	var tk tokens
	if code := p.json("POST", p.token, url.Values{"grant_type": {"authorization_code"}, "code": {back.Query().Get("code")},
		"redirect_uri": {redirect}, "client_id": {client}, "code_verifier": {verifier}}, "", nil, &tk); code != 200 || tk.Access == "" || tk.Refresh == "" {
		p.t.Fatalf("code exchange for %s: %d %q", human, code, tk.Error)
	}
	return tk
}

// loopback is the CLI's redirect: Zitadel accepts any port on the registered loopback URI.
func loopback() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(16000))
	return fmt.Sprintf("http://127.0.0.1:%d/callback", 49152+n.Int64())
}

func (p *idp) refresh(client, rt string) (int, tokens) {
	p.t.Helper()
	var tk tokens
	code := p.json("POST", p.token, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {client}}, "", nil, &tk)
	return code, tk
}

// providerAccepts says whether the provider itself still accepts an access token.
func (p *idp) providerAccepts(at string) bool {
	p.t.Helper()
	return p.json("GET", p.userinfo, nil, at, nil, nil) == 200
}

// idpEnv is a database and an API whose verifier trusts the provider: one issuer per server, its
// project ID as the audience (internal/config/config.go:121-125).
func idpEnv(t *testing.T, p *idp) *env {
	t.Helper()
	e := &env{t: t, db: migrated(t)}
	e.cfg = testAuth(p.issuer)
	e.cfg.OIDC.Audience = p.project
	e.api = e.build(options{})
	return e
}

// accepted checks that the server accepts at as human's token and serves it exactly roles.
func (e *env) accepted(p *idp, at, human string, roles []auth.Role) {
	e.t.Helper()
	b, body := e.identity(at)
	if b.Kind != auth.Human || b.Issuer == nil || *b.Issuer != p.issuer || b.Subject == nil || *b.Subject != p.users[human] ||
		!slices.Equal(b.Roles, roles) {
		e.t.Errorf("%s: %s; want roles %v", human, body, roles)
	}
}

// jwtClaims is the access token's payload as the provider sent it, decoded without a signature
// check (the server's verifier checks the token).
func jwtClaims(t *testing.T, at string) map[string]json.RawMessage {
	t.Helper()
	parts := strings.Split(at, ".")
	if len(parts) != 3 {
		t.Fatalf("the access token has %d parts; want a compact JWS", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

// lifetime is the access token's exp - iat, in seconds.
func lifetime(t *testing.T, at string) int64 {
	t.Helper()
	claims := jwtClaims(t, at)
	var exp, iat int64
	if json.Unmarshal(claims["exp"], &exp) != nil || json.Unmarshal(claims["iat"], &iat) != nil {
		t.Fatalf("exp %s, iat %s; want NumericDates", claims["exp"], claims["iat"])
	}
	return exp - iat
}

// groupsClaim is the access token's groups claim: roles: [] cannot tell a [] claim from an absent
// one, which the server also maps to no role.
func groupsClaim(t *testing.T, at string) (groups []string, present bool) {
	t.Helper()
	raw, ok := jwtClaims(t, at)["groups"]
	if !ok {
		return nil, false
	}
	if err := json.Unmarshal(raw, &groups); err != nil || groups == nil {
		t.Fatalf("groups claim %s; want an array of strings", raw)
	}
	return groups, true
}

// idpRoles is issuer.Humans' roles, as TestIdentityServesEachPrincipal reads them, and h-none,
// the user with no grant.
var idpRoles = map[string][]auth.Role{
	"h-author": {auth.Author}, "h-publisher": {auth.Publisher}, "h-approver": {auth.Approver},
	"h-recovery": {auth.RecoveryAdmin}, "h-viewer": {auth.Viewer}, "h-all": {auth.Author, auth.Publisher, auth.Approver},
	"h-none": {},
}

// Each synthetic human signs in on the CLI's Native app, on a fresh loopback port, and is served
// exactly its roles. Its token lives the configured 300 s (exp - iat, check 12), and its groups
// claim is present and holds exactly its groups, sorted:
// [] for the user with no grant, who is served roles: []. One web sign-in shows the User Agent
// app's token is accepted the same way.
func TestIdentityProviderRoles(t *testing.T) {
	p := newIDP(t)
	e := idpEnv(t, p)
	if len(idpRoles) != len(issuer.Humans)+1 {
		t.Fatalf("the test knows %d humans, the issuer %d plus h-none", len(idpRoles), len(issuer.Humans))
	}
	for human, roles := range idpRoles {
		tk := p.signIn(p.cli, loopback(), human)
		if tk.ExpiresIn <= 0 || tk.ExpiresIn > 300 {
			t.Errorf("%s: expires_in %d; want at most the 5-minute lifetime", human, tk.ExpiresIn)
		}
		// Zitadel truncates exp and iat to whole seconds from separate clock reads, so the
		// configured 300 reads 299 when they fall across a second boundary.
		if got := lifetime(t, tk.Access); got < 299 || got > 300 {
			t.Errorf("%s: access token exp - iat = %d; want the configured 300 (299 across a second boundary)", human, got)
		}
		want := slices.Sorted(slices.Values(issuer.Humans[human]))
		if got, ok := groupsClaim(t, tk.Access); !ok || !slices.Equal(got, want) {
			t.Errorf("%s: groups claim %q (present %v); want %q", human, got, ok, want)
		}
		e.accepted(p, tk.Access, human, roles)
	}
	e.accepted(p, p.signIn(p.web, p.webOrigin+"/callback", "h-viewer").Access, "h-viewer", []auth.Role{auth.Viewer})
}

// Renewal on the web app rotates the refresh token. The rotated-out one is refused, and so is
// the current one presented by the CLI's client ID, from the same project. A provider residual,
// asserted so that a change shows: reusing the old token does not end the session.
func TestIdentityProviderRefresh(t *testing.T) {
	p := newIDP(t)
	e := idpEnv(t, p)
	first := p.signIn(p.web, p.webOrigin+"/callback", "h-author")
	code, second := p.refresh(p.web, first.Refresh)
	if code != 200 || second.Refresh == "" || second.Refresh == first.Refresh {
		t.Fatalf("refresh: %d %q; want 200 and a new refresh token", code, second.Error)
	}
	e.accepted(p, second.Access, "h-author", []auth.Role{auth.Author})
	if code, tk := p.refresh(p.web, first.Refresh); code != 400 || tk.Access != "" {
		t.Errorf("the rotated-out refresh token: %d %q; want 400", code, tk.Error)
	}
	if code, tk := p.refresh(p.cli, second.Refresh); code != 400 || tk.Error != "invalid_client" {
		t.Errorf("the web refresh token at the CLI client: %d %q; want 400 invalid_client", code, tk.Error)
	}
	if code, tk := p.refresh(p.web, second.Refresh); code != 200 || tk.Access == "" {
		t.Errorf("the current refresh token after a reuse: %d %q; want 200 (no reuse detection)", code, tk.Error)
	}
}

// Renewal on the CLI's Native app rotates its refresh token too. Revocation by a public client,
// with its client ID alone, then ends the provider's session: the refresh token is refused, and
// so is the access token at the provider, which accepted it before. A residual: the server
// verifies the token itself, so it accepts the access token until exp.
func TestIdentityProviderRevocation(t *testing.T) {
	p := newIDP(t)
	e := idpEnv(t, p)
	first := p.signIn(p.cli, loopback(), "h-approver")
	code, tk := p.refresh(p.cli, first.Refresh)
	if code != 200 || tk.Access == "" || tk.Refresh == "" || tk.Refresh == first.Refresh {
		t.Fatalf("CLI refresh: %d %q; want 200 and a new refresh token", code, tk.Error)
	}
	if !p.providerAccepts(tk.Access) {
		t.Fatal("the provider refuses a live session's access token, so its refusal after revocation would prove nothing")
	}
	if code := p.json("POST", p.revoke, url.Values{"token": {tk.Refresh}, "client_id": {p.cli}}, "", nil, nil); code != 200 {
		t.Fatalf("revoke: %d", code)
	}
	if code, r := p.refresh(p.cli, tk.Refresh); code != 400 || r.Access != "" {
		t.Errorf("the revoked refresh token: %d %q; want 400", code, r.Error)
	}
	if p.providerAccepts(tk.Access) {
		t.Error("the provider still accepts the revoked session's access token")
	}
	e.accepted(p, tk.Access, "h-approver", []auth.Role{auth.Approver})
}

// End-session on the web app redirects to its post-logout URI with the state, and the provider
// stops accepting the session's access token. A residual: it leaves the offline refresh token
// usable, which is why the web sign-out revokes it first (client-contracts.md §9.7 item 2).
func TestIdentityProviderEndSession(t *testing.T) {
	p := newIDP(t)
	tk := p.signIn(p.web, p.webOrigin+"/callback", "h-viewer")
	if !p.providerAccepts(tk.Access) {
		t.Fatal("the provider refuses a live session's access token, so its refusal after end-session would prove nothing")
	}
	state := random(16)
	q := url.Values{"id_token_hint": {tk.ID}, "client_id": {p.web}, "post_logout_redirect_uri": {p.webOrigin + "/signed-out"}, "state": {state}}
	resp, err := p.hc.Get(p.end + "?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); resp.StatusCode != http.StatusFound || loc != p.webOrigin+"/signed-out?state="+state {
		t.Fatalf("end-session: %d %q; want 302 to the post-logout URI with the state", resp.StatusCode, loc)
	}
	if p.providerAccepts(tk.Access) {
		t.Error("the provider still accepts the ended session's access token")
	}
	if code, r := p.refresh(p.web, tk.Refresh); code != 200 {
		t.Errorf("the refresh token after end-session: %d %q; want 200 (the residual §9.7 item 2 revokes)", code, r.Error)
	}
}

// startDevice asks for a device code on the CLI's Native app.
func (p *idp) startDevice() (deviceCode, userCode string, expiresIn, interval int) {
	p.t.Helper()
	var d struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		ExpiresIn  int    `json:"expires_in"`
		Interval   int    `json:"interval"`
	}
	if code := p.json("POST", p.device, url.Values{"client_id": {p.cli},
		"scope": {"openid offline_access urn:zitadel:iam:org:project:id:" + p.project + ":aud"}}, "", nil, &d); code != 200 || d.DeviceCode == "" {
		p.t.Fatalf("device authorization: %d", code)
	}
	return d.DeviceCode, d.UserCode, d.ExpiresIn, max(d.Interval, 1)
}

// decide approves (as human) or, with human "", denies a device request through the Session API.
func (p *idp) decide(userCode, human string) {
	p.t.Helper()
	var req struct {
		DeviceAuthorizationRequest struct{ ID string } `json:"deviceAuthorizationRequest"`
	}
	// A request just created may not be readable yet: 404 for a moment (seen once in a few runs).
	for i := 0; ; i++ {
		code := p.json("GET", p.issuer+"/v2/oidc/device_authorization/"+userCode, nil, p.login, nil, &req)
		if code == 200 {
			break
		}
		if code != 404 || i == 20 {
			p.t.Fatalf("device request %s: %d", userCode, code)
		}
		time.Sleep(250 * time.Millisecond)
	}
	body := map[string]any{"deny": map[string]any{}}
	if human != "" {
		body = map[string]any{"session": p.session(human)}
	}
	if code := p.json("POST", p.issuer+"/v2/oidc/device_authorization/"+req.DeviceAuthorizationRequest.ID, nil, p.login, body, nil); code != 200 {
		p.t.Fatalf("device decision: %d", code)
	}
}

// pollOnce is one device-code token request.
func (p *idp) pollOnce(deviceCode string) tokens {
	p.t.Helper()
	var tk tokens
	p.json("POST", p.token, url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceCode}, "client_id": {p.cli}}, "", nil, &tk)
	return tk
}

// poll polls the token endpoint at the interval until the answer is not authorization_pending.
func (p *idp) poll(deviceCode string, interval int, until time.Time) tokens {
	p.t.Helper()
	for {
		if tk := p.pollOnce(deviceCode); tk.Error != "authorization_pending" {
			return tk
		}
		if time.Now().After(until) {
			p.t.Fatal("still authorization_pending past the device code's lifetime")
		}
		time.Sleep(time.Duration(interval) * time.Second)
	}
}

// The device-authorization grant on the CLI's Native app: approved through the Session API, it
// yields a token the server accepts with the human's roles; denied, access_denied; left alone,
// it expires at its lifetime (dev-idp shortens Zitadel's 5-minute default to 1 minute).
func TestIdentityProviderDevice(t *testing.T) {
	parent := newIDP(t)
	t.Run("approved", func(t *testing.T) {
		t.Parallel()
		p := parent.with(t)
		e := idpEnv(t, p)
		dc, uc, exp, iv := p.startDevice()
		if tk := p.pollOnce(dc); tk.Error != "authorization_pending" {
			t.Fatalf("before approval: %q; want authorization_pending", tk.Error)
		}
		p.decide(uc, "h-publisher")
		tk := p.poll(dc, iv, time.Now().Add(time.Duration(exp)*time.Second))
		if tk.Access == "" || tk.Refresh == "" {
			t.Fatalf("approved device code: %q; want an access and a refresh token", tk.Error)
		}
		e.accepted(p, tk.Access, "h-publisher", []auth.Role{auth.Publisher})
	})
	t.Run("denied", func(t *testing.T) {
		t.Parallel()
		p := parent.with(t)
		dc, uc, exp, iv := p.startDevice()
		p.decide(uc, "")
		if tk := p.poll(dc, iv, time.Now().Add(time.Duration(exp)*time.Second)); tk.Error != "access_denied" {
			t.Fatalf("denied device code: %q; want access_denied", tk.Error)
		}
	})
	t.Run("expires", func(t *testing.T) {
		t.Parallel()
		p := parent.with(t)
		start := time.Now()
		dc, _, exp, iv := p.startDevice()
		if fmt.Sprint(exp) != p.deviceLifetime {
			t.Fatalf("expires_in %d; want the configured %s", exp, p.deviceLifetime)
		}
		tk := p.poll(dc, iv, start.Add(time.Duration(exp+2*iv)*time.Second))
		if tk.Error != "expired_token" || time.Since(start) < time.Duration(exp)*time.Second {
			t.Fatalf("unapproved device code after %v: %q; want expired_token after %ds", time.Since(start), tk.Error, exp)
		}
	})
}
