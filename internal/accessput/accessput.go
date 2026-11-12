// Package accessput writes a cluster's Talos access credential to OpenBao, once, with the
// operator's own token. It is write-only: one KV v2 create at secret/data/access/talos/<cluster>,
// with check-and-set 0, so an existing secret is never replaced. It reads nothing back, lists
// nothing and deletes nothing, and a token under bw-onboard.hcl cannot do any of those either.
//
// It is the client's second exception to decision T2 beside internal/talos/discover. It imports
// no internal/talos, no internal/provider and no OpenBao library: the server reads the secret
// (internal/provider/access.go) and this package only creates it, so the CLI never links the
// server's provider. The request is net/http only, so it is visible here.
//
// Errors carry fixed words. They never quote OpenBao's text, the token, the address or any byte of
// the document: a failed write must not leak what it was writing.
package accessput

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"syscall"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// The four classes of a failed write, and Present for a create that found one already there.
var (
	ErrDenied      = errors.New("accessput: OpenBao refused the token for this path")
	ErrAbsent      = errors.New("accessput: OpenBao has no secret mount at this address")
	ErrUnreachable = errors.New("accessput: OpenBao did not answer")
	ErrUnexpected  = errors.New("accessput: OpenBao gave an answer this tool does not know")
	// ErrPresent means the check-and-set refused: a secret already exists for the cluster. The
	// existing value is neither read nor replaced.
	ErrPresent = errors.New("accessput: a secret already exists for this cluster")
)

const (
	timeout     = 30 * time.Second
	maxResponse = 1 << 20
	// MaxTokenFile bounds the token read: an OpenBao token is about a hundred bytes.
	MaxTokenFile = 4 << 10
)

// Token is an OpenBao token. Every formatting of it prints the same words. It holds the value
// behind a pointer: a struct holding a Token in an unexported field is printed by reflection,
// which no method can intercept, and then shows the pointer and not the string.
type Token struct{ p *string }

const hidden = "accessput.Token(redacted)"

func (t Token) value() string {
	if t.p == nil {
		return ""
	}
	return *t.p
}

func (Token) String() string                 { return hidden }
func (Token) GoString() string               { return hidden }
func (Token) Format(f fmt.State, _ rune)     { io.WriteString(f, hidden) }
func (t Token) MarshalText() ([]byte, error) { return []byte(hidden), nil }

// ParseToken keeps one token of printable ASCII, with at most one trailing newline.
func ParseToken(b []byte) (Token, error) {
	s := string(bytes.TrimSuffix(b, []byte("\n")))
	if s == "" {
		return Token{}, errors.New("accessput: the token is empty")
	}
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] > '~' {
			return Token{}, errors.New("accessput: the token holds a character outside printable ASCII, or more than one line")
		}
	}
	return Token{&s}, nil
}

// ReadTokenStdin reads the token from r, at most MaxTokenFile bytes.
func ReadTokenStdin(r io.Reader) (Token, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxTokenFile+1))
	if err != nil {
		return Token{}, errors.New("accessput: the token could not be read")
	}
	if len(b) > MaxTokenFile {
		return Token{}, fmt.Errorf("accessput: the token is larger than %d bytes", MaxTokenFile)
	}
	return ParseToken(b)
}

// ReadTokenFile reads the token from a regular file reached without following a symlink, with no
// group or other permission bit and at most MaxTokenFile bytes. Errors name the path, never the
// content.
func ReadTokenFile(path string) (Token, error) {
	// O_NONBLOCK keeps the open of a FIFO with no writer from waiting for one; a regular file
	// ignores it.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Token{}, fmt.Errorf("accessput: token file: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Token{}, fmt.Errorf("accessput: token file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return Token{}, fmt.Errorf("accessput: token file %s is not a regular file", path)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return Token{}, fmt.Errorf("accessput: token file %s has mode %o; it must grant nothing to group or others (0600)", path, fi.Mode().Perm())
	}
	return ReadTokenStdin(f)
}

// ParseAddress parses an OpenBao address, scheme://host[:port] with at most a trailing '/': http or
// https, no userinfo, path, query or fragment, and plain http only to a loopback host. The errors
// never quote it, because userinfo may hold a password.
func ParseAddress(addr string) (*url.URL, error) {
	u, err := url.Parse(addr)
	switch {
	case addr == "" || err != nil:
		return nil, errors.New("accessput: the address is not a URL")
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, errors.New("accessput: the address must be an http or https URL")
	case u.User != nil:
		return nil, errors.New("accessput: the address must not carry userinfo")
	case u.Host == "" || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/"):
		return nil, errors.New("accessput: the address must be scheme://host[:port], without a path, query or fragment")
	case u.Scheme == "http" && !loopback(u.Hostname()):
		return nil, errors.New("accessput: plain http is allowed only to a loopback host; use https")
	}
	return u, nil
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// secretPath is the one request path this package builds: the KV v2 data path of a cluster's Talos
// access credential. A cluster that is not a cl_ identifier is refused, so the path cannot be
// steered to another secret.
func secretPath(cluster string) (string, error) {
	if err := id.MustHave(cluster, id.Cluster); err != nil {
		return "", errors.New("accessput: the cluster is not a cl_ identifier")
	}
	return "/v1/secret/data/access/talos/" + cluster, nil
}

// Put creates the secret {"talosconfig": document} for cluster. It sends one POST, with
// check-and-set 0, and returns nil only for a 2xx answer. document is sent as the secret's value
// and is never part of an error.
func Put(ctx context.Context, addr string, tok Token, cluster string, document []byte) error {
	u, err := ParseAddress(addr)
	if err != nil {
		return err
	}
	if tok.value() == "" {
		return errors.New("accessput: no token")
	}
	// The server refuses an empty talosconfig, and a create-only token cannot repair the path
	// once it is occupied.
	if len(document) == 0 {
		return errors.New("accessput: the document is empty")
	}
	path, err := secretPath(cluster)
	if err != nil {
		return err
	}
	body, err := json.Marshal(struct {
		Options struct {
			CAS int `json:"cas"`
		} `json:"options"`
		Data struct {
			Talosconfig string `json:"talosconfig"`
		} `json:"data"`
	}{Data: struct {
		Talosconfig string `json:"talosconfig"`
	}{string(document)}})
	if err != nil {
		return errors.New("accessput: the request could not be built")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.Scheme+"://"+u.Host+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("accessput: the request could not be built")
	}
	req.Header.Set("X-Vault-Token", tok.value())
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient(u).Do(req)
	if err != nil {
		return ErrUnreachable
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(payload) > maxResponse {
		// The status was read and the answer was not: the write may have happened.
		return ErrUnexpected
	}
	switch s := resp.StatusCode; {
	case s >= 200 && s < 300:
		return nil
	case s == http.StatusForbidden:
		return ErrDenied
	case s == http.StatusNotFound:
		return ErrAbsent
	case s == http.StatusBadRequest && casRefused(payload):
		return ErrPresent
	}
	return ErrUnexpected
}

// httpClient never follows a redirect: Go keeps a custom header such as X-Vault-Token across one,
// and a 307 or 308 replays the body, which is the talosconfig. A plain-http request goes direct,
// since it is loopback and a proxy would sit on that path; an https request keeps the ambient
// proxy, which only tunnels its TLS.
func httpClient(u *url.URL) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	if u.Scheme == "http" {
		t.Proxy = nil
	}
	return &http.Client{
		Timeout:       timeout,
		Transport:     t,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// casRefused reports whether a 400's errors name KV v2's check-and-set refusal. The text is read
// to classify and never relayed.
func casRefused(payload []byte) bool {
	var parsed struct {
		Errors []string `json:"errors"`
	}
	if json.Unmarshal(payload, &parsed) != nil {
		return false
	}
	for _, e := range parsed.Errors {
		if bytes.Contains([]byte(e), []byte("check-and-set parameter did not match")) {
			return true
		}
	}
	return false
}
