package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Tokens are an OIDC context's stored sign-in (client-contracts.md §9.2 item 3). No command prints
// them, and none goes into argv, the contexts file or the environment (§9.2 item 4).
type Tokens struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
}

// renewBefore is how long before its expiry an access token is renewed (§9.5 item 1).
const renewBefore = 60 * time.Second

// TokenFile is the context's D2 (b) token file, "" without the opt-in. BRONZEWARD_TOKEN_FILE
// overrides its path only in a context that has one (§9.2 item 3).
func (x *Context) TokenFile() string {
	if x.OIDC == nil || x.OIDC.TokenFile == "" {
		return ""
	}
	if p := os.Getenv("BRONZEWARD_TOKEN_FILE"); p != "" {
		return p
	}
	return x.OIDC.TokenFile
}

// ReadTokens reads a token file; a missing file is no sign-in. A file holding an automation token
// is refused: it is the operator's, never this client's own credential.
func ReadTokens(p string) (*Tokens, error) {
	b, err := readOwnerOnly(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("token file %s: %w", p, err)
	}
	if automation(b) {
		return nil, automationFile(p)
	}
	var t Tokens
	if !own(b) || json.Unmarshal(b, &t) != nil {
		return nil, fmt.Errorf("token file %s holds neither bronzewardctl's tokens nor an automation token; bronzewardctl leaves it as it is. "+
			"Point oidc.tokenFile, or BRONZEWARD_TOKEN_FILE, at bronzewardctl's own token file, or move this file away", p)
	}
	if t.AccessToken == "" {
		return &t, fmt.Errorf("token file %s: %w", p, ErrNotTokenFile)
	}
	return &t, nil
}

// own: b is the client's own document, one JSON object with no key but the ones it writes, spelled
// as it writes them, each once and a string; ReadTokens' json.Unmarshal refuses anything after it
// but JSON whitespace. Any other file at the path is someone else's, and is never used or removed.
func own(b []byte) bool {
	d := json.NewDecoder(bytes.NewReader(b))
	if t, err := d.Token(); err != nil || t != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		k, err := d.Token()
		key, _ := k.(string)
		if err != nil || key != "accessToken" && key != "refreshToken" || seen[key] {
			return false
		}
		seen[key] = true
		v, err := d.Token()
		if _, ok := v.(string); err != nil || !ok {
			return false
		}
	}
	t, err := d.Token()
	return err == nil && t == json.Delim('}')
}

// ErrNotTokenFile is the client's own token document without an access token; ReadTokens returns
// it with the document, whose refresh token logout still revokes.
var ErrNotTokenFile = errors.New("it holds no access token")

// automation is the content of a file holding an automation token, as automationToken reads one.
func automation(b []byte) bool { return bytes.HasPrefix(bytes.TrimSpace(b), []byte("bwt_")) }

func automationFile(p string) error {
	return fmt.Errorf("token file %s holds an automation token; bronzewardctl neither signs in with it nor writes or clears it", p)
}

// operators refuses to replace or remove a file that holds an automation token (§9.2 item 3).
func operators(p string) error {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 4096)
	n, _ := io.ReadFull(f, head)
	if automation(head[:n]) {
		return automationFile(p)
	}
	return nil
}

// WriteTokens replaces the token file whole, owner-only, through a rename.
func WriteTokens(p string, t *Tokens) error {
	if err := operators(p); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".bronzewardctl-tokens-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(b)
	if err = errors.Join(err, tmp.Sync(), tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// ClearTokens removes the token file, as logout and an identity-revoked answer do (§8.2), under
// the renewal lock, so that a renewal in another process finds it gone instead of writing it back.
// It returns the tokens it removed, read under the lock, and nil when there was no file. A file of
// this client's that holds no access token is removed too, reported as ErrNotTokenFile; any other
// file is left as it is, and so is one that match, when given, refuses (ErrOtherSignIn).
func ClearTokens(p string, match func(*Tokens) bool) (*Tokens, error) {
	if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
		return nil, nil // nothing to clear, so no lock file is made either
	}
	unlock, err := renewalLock(p)
	if err != nil {
		return nil, err
	}
	defer unlock()
	t, err := ReadTokens(p)
	if err != nil && !errors.Is(err, ErrNotTokenFile) || t == nil && err == nil {
		return nil, err
	}
	if match != nil && !match(t) {
		return t, ErrOtherSignIn
	}
	if rm := os.Remove(p); rm != nil && !errors.Is(rm, os.ErrNotExist) {
		return nil, rm
	}
	return t, err
}

// ErrOtherSignIn: the token file holds another sign-in than the one ClearTokens was to clear.
var ErrOtherSignIn = errors.New("the token file holds another sign-in now")

// Claims are an access token's payload, read without verification: the client's own reading
// (§9.2 item 5, §9.6), never the server's reason.
type Claims struct {
	Issuer   string          `json:"iss"`
	Subject  string          `json:"sub"`
	Audience audience        `json:"aud"`
	Expiry   float64         `json:"exp"`
	IssuedAt float64         `json:"iat"`
	Groups   json.RawMessage `json:"groups"`
	ID       string          `json:"jti"`
	// Present, even as null, they mark an ID token, as the server counts them (auth.go idToken).
	Nonce  json.RawMessage `json:"nonce"`
	AtHash json.RawMessage `json:"at_hash"`
}

type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*a = audience{one}
		return nil
	}
	return json.Unmarshal(b, (*[]string)(a))
}

// ReadClaims decodes a JWT's payload; nil when it is no JWT.
func ReadClaims(token string) *Claims {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	var c Claims
	if err != nil || json.Unmarshal(b, &c) != nil {
		return nil
	}
	return &c
}

// ExpiresAt is the token's exp.
func (c *Claims) ExpiresAt() time.Time { return time.Unix(int64(c.Expiry), 0) }

// signIn is an OIDC context's sign-in for one command.
type signIn struct {
	file   string
	tokens *Tokens
	failed bool // a renewal failed; this command does not try again
}

// openSignIn reads the context's stored sign-in.
func openSignIn(x *Context) (*signIn, error) {
	s := &signIn{file: x.TokenFile()}
	if s.file == "" {
		return nil, errors.New("not signed in: this context has no token file (oidc.tokenFile), and this bronzewardctl cannot sign in by itself yet")
	}
	t, err := ReadTokens(s.file)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, fmt.Errorf("not signed in: the token file %s does not exist", s.file)
	}
	s.tokens = t
	return s, nil
}

// expiring: less than renewBefore of the access token's life is left, by this host's clock.
func (s *signIn) expiring() bool {
	c := ReadClaims(s.tokens.AccessToken)
	return c != nil && time.Until(c.ExpiresAt()) < renewBefore
}

// renew replaces the access token (§9.5) under the token file's renewal lock (decision T6 of #254):
// once it holds the lock it reads the stored sign-in again and uses the pair another process
// stored meanwhile instead of refreshing, since a rotated refresh token works once. A failure
// leaves the current token in use.
func (c *Client) renew() (err error) {
	s := c.signIn
	defer func() { s.failed = s.failed || err != nil }()
	unlock, err := renewalLock(s.file)
	if err != nil {
		return err
	}
	defer unlock()
	old := s.tokens
	t, err := ReadTokens(s.file)
	if err != nil {
		return err
	}
	if t == nil {
		// Logout or an identity-revoked answer removed it meanwhile: never write it back.
		s.tokens = &Tokens{AccessToken: old.AccessToken}
		return errors.New("the stored sign-in was removed meanwhile, so it is not renewed")
	}
	if *t != *old {
		c.adopt(t)
		return nil
	}
	if old.RefreshToken == "" {
		return errors.New("no refresh token is kept, so the sign-in cannot be renewed")
	}
	t, err = refresh(c.Context, old.RefreshToken)
	if err != nil {
		return err
	}
	if t.RefreshToken == old.RefreshToken {
		// Without rotation no refresh token is kept (§9.1 item 3).
		t.RefreshToken = ""
		fmt.Fprintf(c.Notices, "%s: the identity provider did not rotate the refresh token, so none is kept; sign in again when this access token expires.\n", c.Context.Where())
	}
	if err := WriteTokens(s.file, t); err != nil {
		return err
	}
	c.adopt(t)
	return nil
}

// adopt switches to t, showing a different subject or issuer as a new sign-in (§9.5 item 2).
func (c *Client) adopt(t *Tokens) {
	was, now := ReadClaims(c.signIn.tokens.AccessToken), ReadClaims(t.AccessToken)
	if was != nil && now != nil && (was.Subject != now.Subject || was.Issuer != now.Issuer) {
		fmt.Fprintf(c.Notices, "%s: a new sign-in: the renewed token is for %s at %s, not %s at %s.\n",
			c.Context.Where(), now.Subject, now.Issuer, was.Subject, was.Issuer)
	}
	c.signIn.tokens, c.token = t, t.AccessToken
}

// renewalLock takes the token file's renewal lock (§9.2 item 3): the file p.lock beside it, which
// holds nothing secret. It belongs to the file, not the context, so contexts that share a token
// file (BRONZEWARD_TOKEN_FILE overrides every one) renew one at a time.
func renewalLock(p string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}

// discover reads the provider's discovery document through issuerClient, within a minute; it
// returns that client and a context that carries it. The issuer is checked first, as context add
// checks it: logout reaches here without Open, and a hand-edited plain http issuer would let an
// on-path host name the endpoint the refresh token is sent to.
func discover(x *Context) (context.Context, context.CancelFunc, *http.Client, *oidc.Provider, error) {
	if err := endpoint(x.OIDC.Issuer, "oidc.issuer"); err != nil {
		return nil, nil, nil, nil, err
	}
	hc, err := issuerClient(x)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	ctx, cancel := context.WithTimeout(oidc.ClientContext(context.Background(), hc), time.Minute)
	p, err := oidc.NewProvider(ctx, x.OIDC.Issuer)
	if err != nil {
		cancel()
		return nil, nil, nil, nil, discoveryError(x.OIDC.Issuer, err)
	}
	return ctx, cancel, hc, p, nil
}

// discoveryError is a failed discovery as shown. A transport error is the client's own. go-oidc's
// other errors quote what the provider sent (its reason phrase, its response body, the issuer it
// names), which is not shown: only the status of a refusal, which go-oidc puts first.
func discoveryError(iss string, err error) error {
	var ue *url.Error
	var me *oidc.IssuerMismatchError
	switch s := err.Error(); {
	case errors.As(err, &ue):
		return fmt.Errorf("cannot reach the identity provider %s: %w", iss, err)
	case errors.As(err, &me):
		return fmt.Errorf("the identity provider %s: its discovery document names another issuer", iss)
	case len(s) > 4 && s[3] == ' ':
		if status, e := strconv.Atoi(s[:3]); e == nil && http.StatusText(status) != "" {
			return fmt.Errorf("the identity provider %s refused its discovery: %s", iss, refusal(status, ""))
		}
	}
	return fmt.Errorf("the identity provider %s: its discovery document could not be read", iss)
}

// ErrNoRevocation: the provider's discovery names no revocation_endpoint.
var ErrNoRevocation = errors.New("the identity provider offers no revocation endpoint")

// Revoke revokes a refresh token at the provider's revocation endpoint (RFC 7009, §9.7 item 1), as
// a public client: its ID in the form, no secret. A redirect is not followed: it would resend the
// token elsewhere, or turn the POST into a GET whose 200 revokes nothing. Only the status and the
// provider's error code are shown, never its response body.
func Revoke(x *Context, rt string) error {
	ctx, cancel, hc, p, err := discover(x)
	if err != nil {
		return err
	}
	defer cancel()
	var d struct {
		URL string `json:"revocation_endpoint"`
	}
	if err := p.Claims(&d); err != nil {
		return fmt.Errorf("the identity provider's revocation_endpoint: %w", err)
	}
	if d.URL == "" {
		return ErrNoRevocation
	}
	if err := providerEndpoint(d.URL, "the identity provider's revocation_endpoint"); err != nil {
		return err
	}
	form := url.Values{"token": {rt}, "token_type_hint": {"refresh_token"}, "client_id": {x.OIDC.ClientID}}
	q, err := http.NewRequestWithContext(ctx, http.MethodPost, d.URL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	q.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	once := *hc
	once.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	r, err := once.Do(q)
	if err != nil {
		return fmt.Errorf("revoking at the identity provider %s: %w", x.OIDC.Issuer, err)
	}
	defer r.Body.Close()
	if r.StatusCode == http.StatusOK {
		return nil
	}
	var e struct {
		Code string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&e) // a body that is no error document names no code
	return fmt.Errorf("the identity provider %s refused the revocation: %s", x.OIDC.Issuer, refusal(r.StatusCode, e.Code))
}

// errorCode is an error code as RFC 6749 §5.2 allows one; anything else a provider sends is not shown.
var errorCode = regexp.MustCompile(`^[\x20\x21\x23-\x5B\x5D-\x7E]{1,64}$`)

// refusal is a provider's refusal as shown: the status code and its standard text, and the error
// code when it is one. The provider's own reason phrase is not shown.
func refusal(status int, code string) string {
	s := fmt.Sprintf("%d %s", status, http.StatusText(status))
	if errorCode.MatchString(code) {
		s += " " + code
	}
	return s
}

// refresh redeems the refresh token at the provider's token endpoint, found by its discovery.
func refresh(x *Context, rt string) (*Tokens, error) {
	ctx, cancel, _, p, err := discover(x)
	if err != nil {
		return nil, err
	}
	defer cancel()
	// A public client sends its ID in the form; auto-detection would redeem a refused token twice.
	ep := p.Endpoint()
	if err := providerEndpoint(ep.TokenURL, "the identity provider's token_endpoint"); err != nil {
		return nil, err
	}
	ep.AuthStyle = oauth2.AuthStyleInParams
	conf := oauth2.Config{ClientID: x.OIDC.ClientID, Endpoint: ep, Scopes: x.OIDC.Scopes}
	t, err := conf.TokenSource(ctx, &oauth2.Token{RefreshToken: rt}).Token()
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		// The provider's own error code only: its response body is not shown.
		return nil, fmt.Errorf("the identity provider %s refused the renewal: %s", x.OIDC.Issuer, refusal(re.Response.StatusCode, re.ErrorCode))
	} else if err != nil {
		return nil, fmt.Errorf("renewing at the identity provider %s: %w", x.OIDC.Issuer, err)
	}
	return &Tokens{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken}, nil
}

// issuerClient is the http client toward the identity provider: the TLS rules of §6, trust from
// oidc.certificateAuthority, and redirects followed only to https or loopback (§6 item 7).
func issuerClient(x *Context) (*http.Client, error) {
	y := *x
	y.CertificateAuthority = x.OIDC.CertificateAuthority
	hc, err := httpClient(&y, nil)
	if err != nil {
		return nil, err
	}
	hc.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 10 || providerEndpoint(r.URL.String(), "redirect") != nil {
			return fmt.Errorf("the identity provider redirected to %s, which bronzewardctl does not follow", r.URL.Redacted())
		}
		return nil
	}
	return hc, nil
}

// reading is the client's own reading of an access token the server answered 401 to (§9.6),
// labelled as such; the server's 401 carries no reason.
func (c *Client) reading() string {
	x, t := c.Context, ReadClaims(c.token)
	say := func(s string) string {
		return "bronzewardctl's own reading of the token, not the server's reason: " + s
	}
	switch {
	case t == nil:
	case time.Now().After(t.ExpiresAt()):
		return say("Your sign-in expired: sign in again.")
	case t.Issuer != x.OIDC.Issuer:
		return say(fmt.Sprintf("The token is from %s; this context expects %s. Check the context or the server's configuration.", t.Issuer, x.OIDC.Issuer))
	case x.OIDC.Audience != "" && !slices.Contains(t.Audience, x.OIDC.Audience):
		return say(fmt.Sprintf("The token is not issued for %s; the identity provider needs an audience mapping.", x.OIDC.Audience))
	case x.MaxTokenLifetimeSeconds > 0 && t.Expiry-t.IssuedAt > float64(x.MaxTokenLifetimeSeconds):
		return say("The token lives longer than this server accepts; the identity provider's access-token lifetime must be shortened.")
	case t.Nonce != nil || t.AtHash != nil || t.ID == "":
		return say("The identity provider sent an ID token, or an access token without jti; the client or the provider's configuration is at fault.")
	}
	return say(fmt.Sprintf("Not accepted by %s: sign in again.", x.Where()))
}

// signedIn completes a refusal of a sign-in (§8.2, §9.6): a 401 with the client's own reading,
// and an identity-revoked answer by clearing the stored sign-in, which is not replaced: signing in
// again cannot restore access the installation revoked.
func (c *Client) signedIn(p *Problem) {
	if c.signIn == nil {
		return
	}
	switch {
	case p.Status == http.StatusUnauthorized:
		p.Reading = c.reading()
	case p.Code() == "identity-revoked" && c.signIn.file != "":
		// Only the refused identity's sign-in is cleared: another process may have stored a new
		// sign-in, for another subject or issuer, while this request was in flight.
		refused := ReadClaims(c.token)
		t, err := ClearTokens(c.signIn.file, func(t *Tokens) bool { return sameIdentity(ReadClaims(t.AccessToken), refused) })
		switch {
		case errors.Is(err, ErrOtherSignIn):
			now := ReadClaims(t.AccessToken)
			p.Cleared = fmt.Sprintf("The stored sign-in is now for %s at %s, not the refused identity, and is kept.", now.Subject, now.Issuer)
		case err != nil && !errors.Is(err, ErrNotTokenFile):
			p.Cleared = fmt.Sprintf("Clearing the stored sign-in failed: %v.", err)
		default:
			p.Cleared = "The stored sign-in was cleared; signing in again does not restore access."
		}
	}
}

// sameIdentity: the tokens carry the same subject at the same issuer, or either cannot be read: an
// unreadable token is no other identity, so a document without an access token matches.
func sameIdentity(a, b *Claims) bool {
	return a == nil || b == nil || a.Subject == b.Subject && a.Issuer == b.Issuer
}

// Subject is the signed-in subject by the client's reading, "" for automation.
func (c *Client) Subject() string {
	if t := ReadClaims(c.token); c.signIn != nil && t != nil {
		return t.Subject
	}
	return ""
}

// TokenClaims is the client's reading of an OIDC context's access token, nil for automation.
func (c *Client) TokenClaims() *Claims {
	if c.signIn == nil {
		return nil
	}
	return ReadClaims(c.token)
}
