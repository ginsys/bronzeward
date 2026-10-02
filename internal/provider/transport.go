package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// requestTimeout bounds every request, whatever the caller's context allows.
const requestTimeout = 30 * time.Second

// maxResponse caps what is read of any response. A larger 2xx is ErrProtocol.
const maxResponse = 4 << 20

// client speaks OpenBao's HTTP API with net/http and no client library, as E1 did: the requests
// are visible in this package rather than behind a library's retries and caching, and go.sum
// stays short. It relays no server text: E1 relayed OpenBao's error strings after removing the
// plaintext's spellings, and a redaction by value misses any spelling it did not list.
type client struct {
	base  string // scheme://host[:port], no path
	token Token
	http  *http.Client
	proxy func(*http.Request) (*url.URL, error) // the ambient proxy: http.ProxyFromEnvironment
}

// ParseAddress parses an OpenBao address: an http or https URL, scheme://host[:port] with at most a
// trailing '/'. The errors never quote it: userinfo may hold a password. Which hosts may use plain
// http is the caller's policy (config's plainHTTPHosts).
func ParseAddress(addr string) (*url.URL, error) {
	u, err := url.Parse(addr)
	switch {
	case addr == "" || err != nil:
		return nil, errors.New("provider: the address is not a URL")
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, errors.New("provider: the address must be an http or https URL")
	case u.User != nil:
		return nil, errors.New("provider: the address must not carry userinfo")
	case u.Host == "" || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/"):
		return nil, errors.New("provider: the address must be scheme://host[:port], without a path, query or fragment")
	}
	return u, nil
}

func newClient(addr string, tok Token) (*client, error) {
	u, err := ParseAddress(addr)
	if err != nil {
		return nil, err
	}
	if tok.value() == "" {
		return nil, errors.New("provider: no token")
	}
	c := &client{base: u.Scheme + "://" + u.Host, token: tok, proxy: http.ProxyFromEnvironment}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = c.proxyFor
	c.http = &http.Client{
		Timeout:   requestTimeout,
		Transport: t,
		// Never follow a redirect: Go keeps a custom header such as X-Vault-Token on a
		// redirect, and a 307 or 308 replays the body, which for a create or an encrypt is
		// the plaintext. A 3xx is an unclassified status.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return c, nil
}

// proxyFor chooses the proxy for a request to the provider. A plain-http request goes direct:
// configuration admits plain http only to loopback or a host in provider.plainHTTPHosts, whose
// network path the operator states is protected, and a proxy the environment names (HTTP_PROXY
// without the host in NO_PROXY) would sit on that path and receive the token and the plaintext.
// An https request keeps the ambient proxy, which only tunnels its TLS, as the identity provider's
// requests do (persistence-api.md §10.1).
func (c *client) proxyFor(r *http.Request) (*url.URL, error) {
	if r.URL.Scheme == "http" {
		return nil, nil
	}
	return c.proxy(r)
}

// response is a 2xx response, read in full under the cap.
type response struct {
	method, path string
	status       int
	body         []byte
}

// bad is ErrProtocol for this response, with this package's fixed words.
func (r *response) bad(detail string) error {
	return &requestError{method: r.method, path: r.path, status: r.status, kind: ErrProtocol, detail: detail}
}

// decode reads the body into out: exactly one JSON value, no trailing data. A failure is
// ErrProtocol with fixed words: a JSON error quotes a character of the input, which for a decrypt
// is the plaintext.
func (r *response) decode(out any) error {
	dec := json.NewDecoder(bytes.NewReader(r.body))
	if dec.Decode(out) != nil {
		return r.bad("the response is not the expected JSON")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return r.bad("the response holds data after its JSON value")
	}
	return nil
}

// do sends body (JSON, or nil) and returns a 2xx response. create marks a KV create, the one
// request whose 400 is checked for the check-and-set refusal.
func (c *client) do(ctx context.Context, method, path string, body []byte, create bool) (*response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return nil, &requestError{method: method, path: path, detail: "the request could not be built"}
	}
	req.Header.Set("X-Vault-Token", c.token.value())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, transportError(ctx, method, path, err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		// The status was read, the body was not: for a write the outcome is unknown.
		e := transportError(ctx, method, path, err).(*requestError)
		e.status = resp.StatusCode
		return nil, e
	}
	switch s := resp.StatusCode; {
	case s >= 200 && s < 300:
		if len(payload) > maxResponse {
			return nil, &requestError{method: method, path: path, status: s, kind: ErrProtocol, detail: "the response exceeds 4 MiB"}
		}
		return &response{method: method, path: path, status: s, body: payload}, nil
	case s == http.StatusNotFound && method == http.MethodGet:
		return nil, &requestError{method: method, path: path, status: s, kind: ErrAbsent}
	case s == http.StatusForbidden:
		return nil, &requestError{method: method, path: path, status: s, kind: ErrDenied}
	case s == http.StatusBadRequest && create && casRefused(payload):
		return nil, &requestError{method: method, path: path, status: s, kind: ErrExists}
	case s == http.StatusServiceUnavailable || s == http.StatusTooManyRequests:
		return nil, &requestError{method: method, path: path, status: s, kind: ErrUnavailable}
	default:
		return nil, &requestError{method: method, path: path, status: s}
	}
}

// casRefused reports whether a 400's errors name KV v2's check-and-set refusal. The text is read
// to classify, never relayed.
func casRefused(payload []byte) bool {
	var parsed struct {
		Errors []string `json:"errors"`
	}
	if json.Unmarshal(payload, &parsed) != nil {
		return false
	}
	for _, e := range parsed.Errors {
		if strings.Contains(e, "check-and-set parameter did not match") {
			return true
		}
	}
	return false
}

// transportError is ErrUnavailable with fixed words. The transport error's own text is not kept:
// net/http quotes the first bytes of a malformed response, which a server can fill with what it
// was sent.
func transportError(ctx context.Context, method, path string, err error) error {
	e := &requestError{method: method, path: path, kind: ErrUnavailable, detail: "no response"}
	var ue *url.Error
	switch {
	case ctx.Err() != nil:
		e.cause, e.detail = ctx.Err(), ctx.Err().Error()
	case errors.As(err, &ue) && ue.Timeout():
		e.detail = "the request timed out"
	case errors.Is(err, syscall.ECONNREFUSED):
		e.detail = "connection refused"
	}
	return e
}
