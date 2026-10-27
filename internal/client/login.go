package client

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// idp is a context's identity provider, read from its discovery document for one command.
type idp struct {
	ctx  context.Context
	p    *oidc.Provider
	conf oauth2.Config
}

// openIDP reads the discovery document (discover), within d. Every endpoint it uses must be https
// or loopback (§6 item 1), and the client, being public, sends its ID in the form.
func openIDP(x *Context, d time.Duration) (*idp, context.CancelFunc, error) {
	ctx, cancel, _, p, err := discover(x, d)
	if err != nil {
		return nil, nil, err
	}
	var keys struct {
		URI string `json:"jwks_uri"`
	}
	if err := p.Claims(&keys); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("the identity provider's jwks_uri: %w", err)
	}
	ep := p.Endpoint()
	ep.AuthStyle = oauth2.AuthStyleInParams
	for _, u := range [][2]string{{"authorization_endpoint", ep.AuthURL}, {"token_endpoint", ep.TokenURL}, {"device_authorization_endpoint", ep.DeviceAuthURL}, {"jwks_uri", keys.URI}} {
		if u[1] != "" {
			if err := providerEndpoint(u[1], "the identity provider's "+u[0]); err != nil {
				cancel()
				return nil, nil, err
			}
		}
	}
	scopes := x.OIDC.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "offline_access"}
	} else if !slices.Contains(scopes, "openid") {
		scopes = append([]string{"openid"}, scopes...)
	}
	return &idp{ctx: ctx, p: p, conf: oauth2.Config{ClientID: x.OIDC.ClientID, Endpoint: ep, Scopes: scopes}}, cancel, nil
}

// refusal shows a token endpoint's refusal by its status and error code only, never its body
// (refusal); doing names the attempt in any other failure. A transport error or a timeout is
// shown, made printable; any other error is oauth2's reading of the answer, which may quote the
// answer's tokens, so it is not.
func (i *idp) refusal(x *Context, what, doing string, err error) error {
	var re *oauth2.RetrieveError
	var ue *url.Error
	switch {
	case errors.As(err, &re):
		return fmt.Errorf("the identity provider %s refused the %s: %s", x.OIDC.Issuer, what, refusal(re.Response.StatusCode, re.ErrorCode))
	case errors.As(err, &ue), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return fmt.Errorf("%s at the identity provider %s: %w", doing, x.OIDC.Issuer, printableError(err))
	}
	return fmt.Errorf("%s at the identity provider %s: its answer could not be read", doing, x.OIDC.Issuer)
}

// Login signs in to x's identity provider (§9.2 items 1-2): the authorization-code grant with
// PKCE S256, state and nonce on a loopback redirect, or with device the device-authorization
// grant. What the operator must do goes to prompts. Its errors do not name the context; the caller
// does.
func Login(x *Context, device bool, prompts io.Writer) (*Tokens, error) {
	if x.Credential != "oidc" || x.OIDC == nil {
		return nil, errors.New("an automation context has no sign-in")
	}
	i, cancel, err := openIDP(x, 10*time.Minute)
	if err != nil {
		return nil, err
	}
	defer cancel()
	var t *oauth2.Token
	if device {
		t, err = i.device(x, prompts)
	} else {
		t, err = i.loopback(x, prompts)
	}
	if err != nil {
		return nil, err
	}
	return &Tokens{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken}, nil
}

func (i *idp) device(x *Context, prompts io.Writer) (*oauth2.Token, error) {
	if i.conf.Endpoint.DeviceAuthURL == "" {
		return nil, fmt.Errorf("the identity provider %s offers no device authorization", x.OIDC.Issuer)
	}
	d, err := i.conf.DeviceAuth(i.ctx)
	if err != nil {
		return nil, i.refusal(x, "device authorization", "device authorization", err)
	}
	// RFC 8628 lets the verification URI carry a query.
	if err := providerEndpoint(d.VerificationURI, "the identity provider's verification_uri"); err != nil {
		return nil, err
	}
	u, _ := url.Parse(d.VerificationURI)
	// The host stands alone, so a look-alike prompt stands out (D4 (a)).
	fmt.Fprintf(prompts, "To sign in to %s, open %s on any device and enter the code %s.\n\n    Check that the page's host is: %s\n\nWaiting for the sign-in...\n",
		x.Where(), d.VerificationURI, d.UserCode, strings.ToUpper(u.Host))
	t, err := i.conf.DeviceAccessToken(i.ctx, d)
	if err != nil {
		return nil, i.refusal(x, "device sign-in", "device sign-in", err)
	}
	return t, nil
}

func (i *idp) loopback(x *Context, prompts io.Writer) (*oauth2.Token, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer l.Close()
	conf := i.conf
	conf.RedirectURL = fmt.Sprintf("http://%s/callback", l.Addr())
	verifier, state, nonce := oauth2.GenerateVerifier(), random(), random()
	authURL := conf.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce))
	opened := openBrowser(authURL)
	fmt.Fprintf(prompts, "Sign in to %s in the browser%s:\n\n    %s\n\nWaiting for the sign-in...\n",
		x.Where(), map[bool]string{true: " that opened, or open this address on this host", false: ": open this address on this host"}[opened], authURL)
	// One callback, checked against state; anything else on the listener is not found.
	got := make(chan url.Values, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "bronzewardctl received the sign-in answer; you can close this page.")
		select {
		case got <- r.URL.Query():
		default:
		}
	})}
	go srv.Serve(l)
	// Shutdown, not Close: the callback's page is still being written when its answer arrives.
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if srv.Shutdown(ctx) != nil {
			srv.Close()
		}
	}()
	var q url.Values
	select {
	case q = <-got:
	case <-i.ctx.Done():
		return nil, errors.New("no sign-in answer arrived in time")
	}
	switch {
	case q.Get("state") != state:
		return nil, errors.New("the sign-in answer carries another state, so it is not this sign-in's; nothing was exchanged")
	case q.Get("error") != "":
		// Only an error code as RFC 6749 allows one: the rest of the answer is not shown.
		code := q.Get("error")
		if !oauthCodes[code] {
			code = "an error code that is not shown"
		}
		return nil, fmt.Errorf("the identity provider %s refused the sign-in: %s", x.OIDC.Issuer, code)
	case q.Get("code") == "":
		return nil, errors.New("the sign-in answer carries no code")
	}
	t, err := conf.Exchange(i.ctx, q.Get("code"), oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, i.refusal(x, "code exchange", "code exchange", err)
	}
	// The ID token proves the nonce; it is checked and dropped, never sent to the API (§9.1).
	raw, _ := t.Extra("id_token").(string)
	if raw == "" {
		return nil, errors.New("the identity provider answered without an ID token, so the nonce cannot be checked")
	}
	id, err := i.p.Verifier(&oidc.Config{ClientID: x.OIDC.ClientID}).Verify(i.ctx, raw)
	if err != nil && strings.HasPrefix(err.Error(), "failed to verify signature") {
		// go-oidc puts a failed key fetch here, with the provider's status and response body,
		// which are not shown.
		return nil, fmt.Errorf("the identity provider %s: its keys (jwks_uri) could not be read, or do not verify the ID token", x.OIDC.Issuer)
	} else if err != nil {
		return nil, fmt.Errorf("the ID token: %w", printableError(err))
	}
	if id.Nonce != nonce {
		return nil, errors.New("the ID token carries another nonce, so it is not this sign-in's")
	}
	return t, nil
}

func random() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// desktop: this host has a desktop whose browser openBrowser can open.
func desktop() bool {
	return runtime.GOOS == "linux" && (os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "")
}

// openBrowser opens u in the desktop's browser, where there is a desktop (§9.2 item 1).
func openBrowser(u string) bool {
	if !desktop() {
		return false
	}
	cmd := exec.Command("xdg-open", u)
	if cmd.Start() != nil {
		return false
	}
	go cmd.Wait()
	return true
}

// SignIn runs Login and keeps the sign-in under the renewal lock, so that neither a renewal nor
// an identity-revoked clear in another process interleaves with the write; it returns where it is
// kept. A credential store that fails, and a token file that is not this client's (ReadTokens),
// are refused before the sign-in, so nothing is issued that cannot be kept; a sign-in whose write
// still fails has its refresh token revoked.
func SignIn(x *Context, device bool, prompts io.Writer) (*Tokens, string, error) {
	if err := x.Check(); err != nil {
		return nil, "", x.fail(err)
	}
	if x.Credential != "oidc" {
		return nil, "", x.fail(errors.New("an automation context has no sign-in"))
	}
	s := openStore(x)
	switch {
	case s.file != "":
		if err := ownTokenFile(s.file); err != nil {
			return nil, "", x.fail(err)
		}
	case s.none == "":
		if _, err := s.read(); err != nil {
			return nil, "", x.fail(fmt.Errorf("the system credential store failed, so a sign-in could not be kept there: %w", err))
		}
	}
	t, err := Login(x, device, prompts)
	if err != nil {
		return nil, "", x.fail(err)
	}
	keep := func() error {
		if s.none != "" {
			return nil
		}
		unlock, err := s.lock()
		if err != nil {
			return err
		}
		defer unlock()
		// Read again under the lock: the file may have been replaced during the sign-in.
		if s.file != "" {
			if err := ownTokenFile(s.file); err != nil {
				return err
			}
		}
		return s.save(t)
	}
	if err := keep(); err != nil {
		err = fmt.Errorf("keeping the sign-in in %s: %w", s.kept(), err)
		if t.RefreshToken == "" {
			return nil, "", x.fail(fmt.Errorf("%w; the identity provider issued no refresh token", err))
		}
		switch rerr := Revoke(x, t.RefreshToken); {
		case rerr == nil:
			err = fmt.Errorf("%w; its refresh token is revoked at the identity provider", err)
		case errors.Is(rerr, ErrNoRevocation):
			err = fmt.Errorf("%w; its refresh token is not revoked: the identity provider offers no revocation endpoint, so it stays usable until the provider expires it", err)
		default:
			err = errors.Join(fmt.Errorf("%w; its refresh token is not revoked, so it stays usable until the identity provider expires it", err), rerr)
		}
		return nil, "", x.fail(err)
	}
	return t, s.kept(), nil
}

// ownTokenFile refuses a token file that login must not overwrite: anything but a missing file or
// this client's own document, a refresh-only one included (ReadTokens).
func ownTokenFile(p string) error {
	if _, err := ReadTokens(p); err != nil && !errors.Is(err, ErrNotTokenFile) {
		return err
	}
	return nil
}

// SignOut removes x's stored sign-in, one without an access token too, and revokes its refresh
// token at the provider's revocation endpoint where discovery names one (RFC 7009, §9.7 item 1).
// An access token already issued stays valid at the server until it expires, plus its leeway. It
// returns what it did, for the operator; a failed revocation still signs out, and is returned
// beside that text.
func SignOut(x *Context) (string, error) {
	// Only what reaching the store needs: a refused server or issuer still has its sign-in removed.
	if err := x.checkName(); err != nil {
		return "", x.fail(err)
	}
	if x.Credential == "oidc" && x.OIDC == nil {
		return "", x.fail(errors.New("an oidc context needs its issuer and client ID"))
	}
	if x.Credential != "oidc" {
		return "", x.fail(errors.New("an automation context has no sign-in; its token file is the operator's and is left as it is"))
	}
	s := openStore(x)
	if s.file == "" && s.none != "" {
		return fmt.Sprintf("Not signed in to %s: the sign-in is kept in %s, so none is stored.%s", x.Where(), s.kept(), unreached()), nil
	}
	// Read under the renewal lock: a renewal it waited for may have stored a newer token.
	t, err := s.clear(nil)
	noAccess := errors.Is(err, ErrNotTokenFile)
	if err != nil && !noAccess {
		return "", x.fail(err)
	}
	if t == nil {
		return fmt.Sprintf("Not signed in to %s: %s.", x.Where(), s.gone()), nil
	}
	// A document without an access token says nothing about one issued earlier, which may still be
	// valid for its whole lifetime.
	valid := "An access token issued earlier with this sign-in may stay valid at the server until it expires, plus the server's 60 s leeway."
	if c := ReadClaims(t.AccessToken); c != nil && time.Now().Before(c.ExpiresAt()) {
		valid = "The access token already issued stays valid at the server until " + c.ExpiresAt().UTC().Format("2006-01-02 15:04:05 MST") + ", plus the server's 60 s leeway."
	} else if c != nil {
		valid = "The access token already issued expired at " + c.ExpiresAt().UTC().Format("2006-01-02 15:04:05 MST") + "; the server accepts it for at most 60 s after that."
	} else if !noAccess {
		valid = "The access token already issued stays valid at the server until it expires, plus the server's 60 s leeway."
	}
	var failed error
	revoked := ""
	if t.RefreshToken != "" {
		switch err := Revoke(x, t.RefreshToken); {
		case err == nil:
			revoked = " The refresh token is revoked at the identity provider."
		case errors.Is(err, ErrNoRevocation):
			revoked = " The refresh token is not revoked: the identity provider offers no revocation endpoint, so it stays usable until the provider expires it."
		default:
			revoked = " The refresh token is not revoked, so it stays usable until the identity provider expires it."
			failed = x.fail(err)
		}
	}
	return fmt.Sprintf("Signed out of %s: %s. %s%s", x.Where(), s.removed(err), valid, revoked), failed
}
