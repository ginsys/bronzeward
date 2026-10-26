package clitest

// bronzewardctl's sign-in against the test identity provider (fixtures/dev-idp), driven
// headlessly through Zitadel's Session API as internal/api/idp_test.go drives it. Without
// BW_TEST_IDP_ISSUER every test here skips, unless BW_REQUIRE_IDP=1, when it fails.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/client"
)

type zitadel struct {
	t                                     *testing.T
	issuer, project, cli, password, login string
	authorize, token                      string
	users                                 map[string]string // human -> Zitadel user ID
	hc                                    *http.Client      // never follows a redirect
}

func newZitadel(t *testing.T) *zitadel {
	t.Helper()
	z := &zitadel{t: t, issuer: os.Getenv("BW_TEST_IDP_ISSUER"), users: map[string]string{},
		hc: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if z.issuer == "" {
		if os.Getenv("BW_REQUIRE_IDP") == "1" {
			t.Fatal("BW_REQUIRE_IDP=1 but BW_TEST_IDP_ISSUER is unset (mise run dev-idp)")
		}
		t.Skip("no test identity provider: BW_TEST_IDP_ISSUER is unset (mise run dev-idp)")
	}
	for v, dst := range map[string]*string{"PROJECT": &z.project, "CLI_CLIENT": &z.cli, "PASSWORD": &z.password} {
		if *dst = os.Getenv("BW_TEST_IDP_" + v); *dst == "" {
			t.Fatalf("BW_TEST_IDP_ISSUER is set but BW_TEST_IDP_%s is not", v)
		}
	}
	tok, err := os.ReadFile(os.Getenv("BW_TEST_IDP_LOGIN_TOKEN_FILE"))
	if err != nil {
		t.Fatalf("the login client's token: %v", err)
	}
	z.login = strings.TrimSpace(string(tok))
	for kv := range strings.SplitSeq(os.Getenv("BW_TEST_IDP_USERS"), ",") {
		if h, id, ok := strings.Cut(kv, "="); ok {
			z.users[h] = id
		}
	}
	var doc struct {
		Auth  string `json:"authorization_endpoint"`
		Token string `json:"token_endpoint"`
	}
	if code := z.json("GET", z.issuer+"/.well-known/openid-configuration", nil, "", nil, &doc); code != 200 {
		t.Fatalf("the provider's discovery document: %d (mise run dev-idp)", code)
	}
	z.authorize, z.token = doc.Auth, doc.Token
	return z
}

// json sends one request, a form or a JSON body, and decodes the answer into out.
func (z *zitadel) json(method, u string, form url.Values, bearer string, body, out any) int {
	z.t.Helper()
	var rd io.Reader
	ct := ""
	if form != nil {
		rd, ct = strings.NewReader(form.Encode()), "application/x-www-form-urlencoded"
	} else if body != nil {
		b, _ := json.Marshal(body)
		rd, ct = strings.NewReader(string(b)), "application/json"
	}
	r, err := http.NewRequestWithContext(z.t.Context(), method, u, rd)
	if err != nil {
		z.t.Fatal(err)
	}
	if ct != "" {
		r.Header.Set("Content-Type", ct)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := z.hc.Do(r)
	if err != nil {
		z.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 && json.Unmarshal(raw, out) != nil {
		z.t.Fatalf("%s %s: %d, not JSON", method, u, resp.StatusCode)
	}
	return resp.StatusCode
}

// session signs human in through the Session API, as a login UI would.
func (z *zitadel) session(human string) map[string]string {
	z.t.Helper()
	var s struct{ SessionID, SessionToken string }
	if code := z.json("POST", z.issuer+"/v2/sessions", nil, z.login, map[string]any{"checks": map[string]any{
		"user": map[string]string{"userId": z.users[human]}, "password": map[string]string{"password": z.password}}}, &s); code != 200 && code != 201 {
		z.t.Fatalf("session for %s: %d", human, code)
	}
	return map[string]string{"sessionId": s.SessionID, "sessionToken": s.SessionToken}
}

// browser plays the operator's browser for human: it follows the authorization address
// bronzewardctl prints to the login UI's auth request, completes it through the Session API and
// opens the callback, which is bronzewardctl's loopback listener.
func (z *zitadel) browser(human string) func(string) {
	return func(line string) {
		u := strings.TrimSpace(line)
		if !strings.HasPrefix(u, z.authorize+"?") {
			return
		}
		resp, err := z.hc.Get(u)
		if err != nil {
			z.t.Errorf("authorize: %v", err)
			return
		}
		resp.Body.Close()
		loc, err := resp.Location()
		if err != nil || loc.Query().Get("authRequest") == "" {
			z.t.Errorf("authorize: %d %v; want a redirect to the login UI with an authRequest", resp.StatusCode, loc)
			return
		}
		var cb struct{ CallbackURL string }
		if code := z.json("POST", z.issuer+"/v2/oidc/auth_requests/"+loc.Query().Get("authRequest"), nil, z.login,
			map[string]any{"session": z.session(human)}, &cb); code != 200 {
			z.t.Errorf("callback for %s: %d", human, code)
			return
		}
		if resp, err = http.Get(cb.CallbackURL); err != nil {
			z.t.Errorf("the loopback callback: %v", err)
			return
		}
		resp.Body.Close()
	}
}

var userCode = regexp.MustCompile(`enter the code (\S+)\.`)

// device approves, as human, the device request whose code bronzewardctl prints.
func (z *zitadel) device(human string) func(string) {
	return func(line string) {
		m := userCode.FindStringSubmatch(line)
		if m == nil {
			return
		}
		var req struct {
			DeviceAuthorizationRequest struct{ ID string } `json:"deviceAuthorizationRequest"`
		}
		// A request just created may not be readable yet: 404 for a moment.
		for i := 0; ; i++ {
			code := z.json("GET", z.issuer+"/v2/oidc/device_authorization/"+m[1], nil, z.login, nil, &req)
			if code == 200 {
				break
			}
			if code != 404 || i == 20 {
				z.t.Errorf("device request: %d", code)
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
		if code := z.json("POST", z.issuer+"/v2/oidc/device_authorization/"+req.DeviceAuthorizationRequest.ID, nil, z.login,
			map[string]any{"session": z.session(human)}, nil); code != 200 {
			z.t.Errorf("device decision: %d", code)
		}
	}
}

// refreshes says whether the provider still redeems rt for the CLI's client.
func (z *zitadel) refreshes(rt string) bool {
	z.t.Helper()
	return z.json("POST", z.token, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {z.cli}}, "", nil, nil) == 200
}

// TestLoginIdentityProvider signs in to the test identity provider with bronzewardctl: AUTH-01
// through the loopback, renewal with rotation past the access token's expiry (AUTH-06's renewal
// half), the device grant, and AUTH-09's logout with revocation.
func TestLoginIdentityProvider(t *testing.T) {
	z := newZitadel(t)
	f := startFor(t, "viewer", z.issuer, z.project)
	file := filepath.Join(f.home, "idp.json")
	f.want(f.ctl(nil, "", "context", "add", "idp", "--server", f.base, "--issuer", z.issuer, "--client-id", z.cli, "--audience", z.project,
		"--scopes", "openid,offline_access,urn:zitadel:iam:org:project:id:"+z.project+":aud", "--oidc-token-file", file), 0, "credential oidc")
	// Every token the file has held is looked for in every later output.
	var held []string
	check := func(r result) result {
		t.Helper()
		if s, err := client.ReadTokens(file); err == nil && s != nil {
			held = append(held, s.AccessToken, s.RefreshToken)
		}
		for _, tok := range held {
			if tok != "" && strings.Contains(r.out+r.err, tok) {
				t.Error("bronzewardctl printed a sign-in token")
			}
		}
		return r
	}
	ctl := func(args ...string) result { return check(f.scan(f.exec(nil, "", args...), args)) }
	interact := func(act func(string), args ...string) result {
		return check(f.interact(nil, act, append([]string{"--context", "idp"}, args...)...))
	}

	t.Run("AUTH-01", func(t *testing.T) {
		f.want(interact(z.browser("h-viewer"), "login"), 0, "Signed in to idp", "as "+z.users["h-viewer"]+" at "+z.issuer,
			"kept in the token file "+file)
		f.want(ctl("--context", "idp", "whoami"), 0, "viewer", "subject "+z.users["h-viewer"])
		f.want(ctl("--context", "idp", "machine", "list"), 0)
		f.want(ctl("context", "list", "-o", "json"), 0, `"name":"idp"`, `"lastEpoch":"`)
	})

	t.Run("renewal past expiry", func(t *testing.T) {
		if testing.Short() {
			t.Skip("waits out a 5-minute access token")
		}
		before := f.stored(file)
		exp := client.ReadClaims(before.AccessToken).ExpiresAt()
		time.Sleep(time.Until(exp.Add(5 * time.Second)))
		f.want(ctl("--context", "idp", "machine", "list"), 0)
		after := f.stored(file)
		if after.AccessToken == before.AccessToken || after.RefreshToken == "" || after.RefreshToken == before.RefreshToken {
			t.Fatalf("the file after a read past expiry: renewed %v, refresh token rotated %v",
				after.AccessToken != before.AccessToken, after.RefreshToken != "" && after.RefreshToken != before.RefreshToken)
		}
		if z.refreshes(before.RefreshToken) {
			t.Error("the provider still redeems the rotated-out refresh token")
		}
		f.want(ctl("--context", "idp", "machine", "list"), 0)
		if again := f.stored(file); again.RefreshToken != after.RefreshToken {
			t.Error("a read with a live token renewed it again")
		}
	})

	t.Run("device", func(t *testing.T) {
		host := strings.ToUpper(strings.TrimPrefix(z.issuer, "http://"))
		f.want(interact(z.device("h-publisher"), "login", "--device"), 0, "Check that the page's host is: "+host,
			"Signed in to idp", "as "+z.users["h-publisher"]+" at "+z.issuer)
		f.want(ctl("--context", "idp", "whoami"), 0, "publisher", "subject "+z.users["h-publisher"])
	})

	t.Run("AUTH-09", func(t *testing.T) {
		s := f.stored(file)
		f.want(ctl("--context", "idp", "logout"), 0, "the sign-in is removed from the token file "+file,
			"The refresh token is revoked at "+z.issuer, "stays valid at the server until")
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Errorf("the token file is still there: %v", err)
		}
		if z.refreshes(s.RefreshToken) {
			t.Error("the provider still redeems the revoked refresh token")
		}
		// The residual logout states: the server verifies the access token itself, until exp.
		q, _ := http.NewRequest(http.MethodGet, f.base+"/api/v1/identity", nil)
		q.Header.Set("Authorization", "Bearer "+s.AccessToken)
		f.do(q, http.StatusOK)
	})
}
