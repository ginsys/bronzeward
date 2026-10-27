package clitest

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ginsys/bronzeward/fixtures/oidc/issuer"
)

// provider adds to the fixture issuer, which offers no sign-in (client-contracts.md G19), the
// endpoints the CLI's sign-in uses: authorization with PKCE, a token endpoint for the code,
// device and refresh grants, device authorization and revocation. Everything it issues is
// synthetic: h-viewer's access tokens and BWSYNTH-rt-<n> refresh tokens.
//
// query is added to the token endpoint, and seen is the query the last token request came with.
// revocation answers a revocation, a 302 redirecting to a page that answers a GET with 200, and
// revoked lists their forms; while it is nil, discovery names no revocation endpoint. endpoint,
// when set, is the revocation_endpoint discovery names instead. discovery, when set, breaks
// discovery: "down" answers 503 with a body of control characters, "bare" 503 with no reason
// phrase and "phrase" with the provider's own, "garbage" 200 with a body that is no document,
// "moved" redirects to plain http off loopback, "fragment" to an empty fragment, "escape" to a
// target holding a C1 control, "other" names another issuer, "keys" keys on plain http off
// loopback, and "keysdown" keys that answer 503 with a body of control characters.
// verification, when set, is the device grant's verification_uri.
type provider struct {
	iss *issuer.Issuer

	mu           sync.Mutex
	discovery    string
	verification string
	query        string
	seen         string
	endpoint     any
	revocation   func() (int, any)
	revoked      []url.Values
	answer       func(rt string) (int, any)  // the refresh grant; nil refuses it
	callback     func(url.Values) url.Values // edits the authorization answer
	nonce        string                      // an ID token nonce other than the request's
	exchanged    func() (int, any)           // answers a valid code exchange instead; nil issues
	codes        map[string]authorization    // codes issued and not yet exchanged
	request      url.Values                  // the last authorization request
	calls        atomic.Int32                // refresh redemptions
	approved     atomic.Bool                 // the operator approved the device request
	exchange     atomic.Int32                // code exchanges
}

type authorization struct{ challenge, nonce, redirect string }

// set answers the refresh grant with answer and zeroes the redemption count.
func (p *provider) set(answer func(rt string) (int, any)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answer = answer
	p.calls.Store(0)
}

// login resets the sign-in side: callback and nonce edit the next answers.
func (p *provider) login(callback func(url.Values) url.Values, nonce string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callback, p.nonce, p.codes, p.request, p.revoked, p.exchanged = callback, nonce, map[string]authorization{}, nil, nil, nil
	p.approved.Store(false)
	p.exchange.Store(0)
}

// revoking answers revocations with answer, nil for no revocation endpoint, and forgets those
// sent before.
func (p *provider) revoking(answer func() (int, any)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.revocation, p.revoked = answer, nil
}

// revocations are the forms sent to the revocation endpoint since revoking or login.
func (p *provider) revocations() []url.Values {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.revoked)
}

// asked is the last authorization request.
func (p *provider) asked() url.Values {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.request
}

func (p *provider) wrap(iss *issuer.Issuer) http.Handler {
	p.iss = iss
	p.login(nil, "")
	inner := iss.Handler()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		inner.ServeHTTP(rec, r)
		var d map[string]any
		json.Unmarshal(rec.Body.Bytes(), &d)
		p.mu.Lock()
		switch p.discovery {
		case "down":
			p.mu.Unlock()
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("\x1b[2J down"))
			return
		case "moved":
			p.mu.Unlock()
			http.Redirect(w, r, "http://idp.example.invalid/.well-known/openid-configuration", http.StatusFound)
			return
		case "fragment":
			// The target serves the document, so only refusing the redirect fails discovery.
			if !r.URL.Query().Has("moved") {
				p.mu.Unlock()
				w.Header().Set("Location", iss.URL+"/.well-known/openid-configuration?moved#")
				w.WriteHeader(http.StatusFound)
				return
			}
		case "escape":
			// Set directly: http.Redirect would percent-encode the C1 control.
			p.mu.Unlock()
			w.Header().Set("Location", iss.URL+"/.well-known/openid-configuration?\u009b2J")
			w.WriteHeader(http.StatusFound)
			return
		case "garbage":
			p.mu.Unlock()
			w.Write([]byte("\x1b[2J BWSYNTH-body"))
			return
		case "bare", "phrase":
			// A status line without a reason phrase, or with the provider's own.
			line := map[string]string{"bare": "HTTP/1.1 503\r\n", "phrase": "HTTP/1.1 503 BWSYNTH-phrase\r\n"}[p.discovery]
			p.mu.Unlock()
			c, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				c.Write([]byte(line + "Content-Length: 12\r\nConnection: close\r\n\r\nBWSYNTH-body"))
				c.Close()
			}
			return
		case "other":
			d["issuer"] = "https://idp.example.invalid"
		case "keys":
			d["jwks_uri"] = "http://idp.example.invalid/keys"
		case "keysdown":
			d["jwks_uri"] = iss.URL + "/keysdown"
		}
		d["authorization_endpoint"], d["token_endpoint"] = iss.URL+"/authorize", iss.URL+"/token"+p.query
		d["device_authorization_endpoint"] = iss.URL + "/device"
		if p.revocation != nil {
			d["revocation_endpoint"] = iss.URL + "/revoke"
		}
		if p.endpoint != nil {
			d["revocation_endpoint"] = p.endpoint
		}
		p.mu.Unlock()
		writeJSON(w, http.StatusOK, d)
	})
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		p.mu.Lock()
		code := fmt.Sprintf("BWSYNTH-code-%d", len(p.codes))
		p.request, p.codes[code] = q, authorization{q.Get("code_challenge"), q.Get("nonce"), q.Get("redirect_uri")}
		back := url.Values{"code": {code}, "state": {q.Get("state")}}
		if p.callback != nil {
			back = p.callback(back)
		}
		p.mu.Unlock()
		http.Redirect(w, r, q.Get("redirect_uri")+"?"+back.Encode(), http.StatusFound)
	})
	mux.HandleFunc("POST /device", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		verify := p.verification
		p.mu.Unlock()
		if verify == "" {
			verify = iss.URL + "/activate?client=cli"
		}
		writeJSON(w, http.StatusOK, map[string]any{"device_code": "BWSYNTH-device", "user_code": "BWSY-NTH1",
			"verification_uri": verify, "expires_in": 60, "interval": 1})
	})
	mux.HandleFunc("GET /keysdown", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("\x1b[2J BWSYNTH-body"))
	})
	mux.HandleFunc("GET /revoked", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, map[string]any{}) })
	mux.HandleFunc("POST /revoke", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		p.mu.Lock()
		answer := p.revocation
		p.revoked = append(p.revoked, r.PostForm)
		p.mu.Unlock()
		if answer == nil {
			http.NotFound(w, r)
			return
		}
		status, body := answer()
		if status == http.StatusFound {
			http.Redirect(w, r, "/revoked", status)
			return
		}
		writeJSON(w, status, body)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f := r.PostForm
		p.mu.Lock()
		p.seen = r.URL.RawQuery
		p.mu.Unlock()
		refuse := func(code string) { writeJSON(w, http.StatusBadRequest, map[string]string{"error": code}) }
		switch f.Get("grant_type") {
		case "refresh_token":
			p.calls.Add(1)
			p.mu.Lock()
			answer := p.answer
			p.mu.Unlock()
			if answer == nil {
				refuse("invalid_grant")
				return
			}
			status, body := answer(f.Get("refresh_token"))
			writeJSON(w, status, body)
		case "authorization_code":
			p.exchange.Add(1)
			p.mu.Lock()
			a, ok := p.codes[f.Get("code")]
			delete(p.codes, f.Get("code"))
			nonce, exchanged := p.nonce, p.exchanged
			p.mu.Unlock()
			sum := sha256.Sum256([]byte(f.Get("code_verifier")))
			if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != a.challenge || f.Get("redirect_uri") != a.redirect || f.Get("client_id") != "BWSYNTH-cli" {
				refuse("invalid_grant")
				return
			}
			if exchanged != nil {
				status, body := exchanged()
				writeJSON(w, status, body)
				return
			}
			if nonce == "" {
				nonce = a.nonce
			}
			now := time.Now()
			id, err := iss.Sign(map[string]any{"iss": iss.URL, "aud": "BWSYNTH-cli", "sub": "h-viewer", "nonce": nonce,
				"iat": now.Unix(), "exp": now.Add(issuer.Lifetime).Unix()})
			if err != nil {
				refuse("server_error")
				return
			}
			p.issue(w, id)
		case "urn:ietf:params:oauth:grant-type:device_code":
			if !p.approved.Load() {
				refuse("authorization_pending")
				return
			}
			p.issue(w, "")
		default:
			refuse("unsupported_grant_type")
		}
	})
	mux.Handle("/", inner)
	return mux
}

// issue answers a sign-in with h-viewer's access token, BWSYNTH-rt-1 and the ID token id.
func (p *provider) issue(w http.ResponseWriter, id string) {
	at, err := p.iss.Mint("h-viewer", "", time.Now())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}
	body := map[string]any{"access_token": at, "token_type": "Bearer", "expires_in": 300, "refresh_token": "BWSYNTH-rt-1"}
	if id != "" {
		body["id_token"] = id
	}
	writeJSON(w, http.StatusOK, body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// issued is a refresh answer: access token at, refresh token rt.
func issued(at, rt string) (int, any) {
	return http.StatusOK, map[string]any{"access_token": at, "token_type": "Bearer", "expires_in": 300, "refresh_token": rt}
}

// unreadable is a token answer oauth2 cannot read, with a token where expires_in belongs: its
// error quotes the value.
func unreadable() (int, any) {
	return http.StatusOK, map[string]any{"access_token": "BWSYNTH-at-77", "token_type": "Bearer", "expires_in": "BWSYNTH-rt-77",
		"refresh_token": "BWSYNTH-rt-77"}
}
