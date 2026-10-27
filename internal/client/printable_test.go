package client

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
)

// TestPrintable checks that every control character and every byte that is not UTF-8 is escaped,
// and that other text, non-ASCII included, is left as it is.
func TestPrintable(t *testing.T) {
	for in, want := range map[string]string{
		"BWSYNTH-plain host:443": "BWSYNTH-plain host:443",
		"BWSYNTH-bücher":         "BWSYNTH-bücher",
		"a\x1b[2Jb":              `a\x1b[2Jb`,
		"cr\rbel\adel\x7f":       `cr\x0dbel\x07del\x7f`,
		"c1 \u009b31m csi":       `c1 \x9b31m csi`,
		"raw \x9b byte":          `raw \x9b byte`,
		"tab\tline\n":            `tab\x09line\x0a`,
		"replacement � is kept":  "replacement � is kept",
	} {
		if got := printable(in); got != want {
			t.Errorf("printable(%q) = %q, want %q", in, got, want)
		}
	}
	base := &url.Error{Op: "Get", URL: "https://BWSYNTH.example.invalid", Err: errors.New("BWSYNTH-\x1b]0;title\a")}
	err := printableError(base)
	var ue *url.Error
	if hostile(err.Error()) || !errors.As(err, &ue) || ue != base {
		t.Errorf("printableError: %q, url.Error found: %v; want no control character, the wrapped error still found", err, ue == base)
	}
	if printableError(nil) != nil {
		t.Error("printableError(nil) is not nil")
	}
}

// hostileNames are DNS names a certificate may carry, since Go's x509 parser accepts any ASCII in
// a dNSName; x509.HostnameError lists them unquoted.
var hostileNames = []string{"BWSYNTH-\x1b[2J.example.invalid", "BWSYNTH-cr\r.example.invalid", "BWSYNTH-bel\a.example.invalid"}

// hostile reports a control character in s.
func hostile(s string) bool { return strings.ContainsFunc(s, unicode.IsControl) }

// hostileServer is the URL of a TLS server reached as localhost whose certificate names only
// hostileNames, and that certificate: trusted, verification fails on the host name, and Go lists
// the names in the error (for an IP address it does not).
func hostileServer(t *testing.T, h http.Handler) (string, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "BWSYNTH-hostile\x1b[0m"},
		DNSNames:  hostileNames,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.Config.ErrorLog = log.New(io.Discard, "", 0) // the refused handshakes
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return strings.Replace(srv.URL, "127.0.0.1", "localhost", 1), der
}

func bundleOf(t *testing.T, ders ...[]byte) string {
	t.Helper()
	var b []byte
	for _, d := range ders {
		b = append(b, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d})...)
	}
	p := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestTransportErrorsArePrintable checks #380 on each transport path: the server's transport, an
// https proxy's trust refusal, the identity provider's discovery, and the renewal and revocation at
// its endpoints. Each meets a certificate whose DNS names hold control characters, and none reaches
// the error text; a trust refusal still names the host, subject, issuer and trust source (§6 item 4).
func TestTransportErrorsArePrintable(t *testing.T) {
	proxyAsTheProxyTestNamesIt(t)
	bad, badDER := hostileServer(t, http.NotFoundHandler())
	badHost := strings.TrimPrefix(bad, "https://")

	t.Run("server transport", func(t *testing.T) {
		ca := bundleOf(t, badDER)
		err := Live(&Context{Name: "lab", Server: bad, Credential: "automation", CertificateAuthority: ca}, nil)
		var tr *trust
		if err == nil || hostile(err.Error()) || !errors.As(err, &tr) {
			t.Fatalf("live: %q; want a trust refusal without a control character", err)
		}
		for _, want := range []string{"the certificate of " + badHost, `subject "CN=BWSYNTH-hostile\x1b[0m"`,
			`issuer "CN=BWSYNTH-hostile\x1b[0m"`, "not trusted by the bundle " + ca, `BWSYNTH-\x1b[2J.example.invalid`, "nothing was sent"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("live: %q does not hold %q", err, want)
			}
		}
		// Any other transport error is made printable too, and keeps what it wraps.
		raw := &url.Error{Op: "Get", URL: bad, Err: errors.New("BWSYNTH-\x1b[2J")}
		var ue *url.Error
		if e := transportError(&Context{}, badHost, raw); hostile(e.Error()) || !errors.As(e, &ue) {
			t.Errorf("transportError of a plain transport error: %q; want it printable and still a url.Error", e)
		}
	})

	t.Run("https proxy", func(t *testing.T) {
		roots := x509.NewCertPool()
		cert, err := x509.ParseCertificate(badDER)
		if err != nil {
			t.Fatal(err)
		}
		roots.AddCert(cert)
		proxy, _ := url.Parse(bad)
		hc, err := newHTTPClient(&Context{Name: "lab", Server: "https://BWSYNTH.example.invalid", Credential: "automation"}, nil,
			func(*http.Request) (*url.URL, error) { return proxy, nil }, roots)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hc.Get("https://BWSYNTH.example.invalid/livez")
		var tr *trust
		if err == nil || hostile(err.Error()) || !errors.As(err, &tr) {
			t.Fatalf("proxy: %q; want a trust refusal without a control character", err)
		}
		for _, want := range []string{"the certificate of the proxy " + badHost, `subject "CN=BWSYNTH-hostile\x1b[0m"`,
			`issuer "CN=BWSYNTH-hostile\x1b[0m"`, "not trusted by the system trust store", `BWSYNTH-\x1b[2J.example.invalid`, "nothing was sent"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("proxy: %q does not hold %q", err, want)
			}
		}
	})

	t.Run("identity-provider discovery", func(t *testing.T) {
		x := &Context{Name: "lab", OIDC: &OIDC{Issuer: bad, ClientID: "BWSYNTH-cli", CertificateAuthority: bundleOf(t, badDER)}}
		_, err := refresh(x, "BWSYNTH-rt")
		if err == nil || hostile(err.Error()) || !strings.Contains(err.Error(), "cannot reach the identity provider "+bad) ||
			!strings.Contains(err.Error(), `BWSYNTH-\x1b[2J.example.invalid`) {
			t.Errorf("discovery: %q; want the provider unreachable, its certificate's names escaped", err)
		}
	})

	t.Run("renewal", func(t *testing.T) {
		var iss *httptest.Server
		iss = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/.well-known/openid-configuration" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"issuer": iss.URL, "authorization_endpoint": iss.URL + "/authorize",
				"token_endpoint": bad + "/token", "revocation_endpoint": bad + "/revoke", "jwks_uri": iss.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"ES256"}})
		}))
		defer iss.Close()
		x := &Context{Name: "lab", OIDC: &OIDC{Issuer: iss.URL, ClientID: "BWSYNTH-cli", CertificateAuthority: bundleOf(t, iss.Certificate().Raw, badDER)}}
		_, err := refresh(x, "BWSYNTH-rt")
		if err == nil || hostile(err.Error()) || !strings.Contains(err.Error(), "renewing at the identity provider "+iss.URL) ||
			!strings.Contains(err.Error(), `BWSYNTH-\x1b[2J.example.invalid`) {
			t.Errorf("renewal: %q; want the renewal failed at the token endpoint, its certificate's names escaped", err)
		}
		if err := Revoke(x, "BWSYNTH-rt"); err == nil || hostile(err.Error()) || !strings.Contains(err.Error(), "revoking at the identity provider "+iss.URL) ||
			!strings.Contains(err.Error(), `BWSYNTH-\x1b[2J.example.invalid`) {
			t.Errorf("revocation: %q; want the revocation failed at its endpoint, its certificate's names escaped", err)
		}
	})
}

// proxyAsTheProxyTestNamesIt: http.ProxyFromEnvironment reads the environment once per process,
// so whichever test sends the first request fixes it for every later one. A test that may be first
// leaves the proxy TestLiveSaysItUsesAProxy names; Go never proxies loopback, so its own requests
// are not affected.
func proxyAsTheProxyTestNamesIt(t *testing.T) { t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1") }

// rawServer answers each request for a path in answers with that raw HTTP/1.1 response, which
// net/http would not let a handler send: a reason phrase with control characters, for one.
func rawServer(t *testing.T, answers map[string]string) string {
	t.Helper()
	return rawAt(t, func(string) map[string]string { return answers })
}

// rawAt is rawServer with answers that name its own URL, given to them.
func rawAt(t *testing.T, of func(base string) map[string]string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	answers := of("http://" + l.Addr().String())
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				br := bufio.NewReader(c)
				line, _ := br.ReadString('\n')
				n := 0
				for h, err := br.ReadString('\n'); err == nil && h != "\r\n"; h, err = br.ReadString('\n') {
					if k, v, ok := strings.Cut(h, ":"); ok && strings.EqualFold(k, "Content-Length") {
						n, _ = strconv.Atoi(strings.TrimSpace(v))
					}
				}
				io.CopyN(io.Discard, br, int64(n)) // a request body left unread would reset the connection
				if f := strings.Fields(line); len(f) > 1 {
					io.WriteString(c, answers[f[1]])
				}
			}()
		}
	}()
	return "http://" + l.Addr().String()
}

// TestServerTextIsPrintable checks the server's own text that bronzewardctl shows: the reason
// phrase of every status it names, and the problem document's members (§6 item 4).
func TestServerTextIsPrintable(t *testing.T) {
	proxyAsTheProxyTestNamesIt(t)
	const reason = "HTTP/1.1 %d BWSYNTH-\x1b[2J\aphrase\r\n%sConnection: close\r\n\r\n%s"
	srv := rawServer(t, map[string]string{
		"/livez":                  fmt.Sprintf(reason, 500, "Content-Length: 0\r\n", ""),
		"/bronzeward-client.json": fmt.Sprintf(reason, 404, "Content-Length: 0\r\n", ""),
		"/api/v1/plain":           fmt.Sprintf(reason, 418, "Content-Length: 0\r\n", ""),
		"/api/v1/redirect":        fmt.Sprintf(reason, 302, "Location: /elsewhere\r\nContent-Length: 0\r\n", ""),
		"/api/v1/cut":             fmt.Sprintf(reason, 418, "Content-Length: 10\r\n", "ab"),
	})
	moved := rawServer(t, map[string]string{
		"/livez": fmt.Sprintf(reason, 302, "Location: /elsewhere\r\nContent-Length: 0\r\n", ""),
	})
	x := &Context{Name: "lab", Server: srv, Credential: "automation"}
	hc, err := httpClient(x, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{Context: x, Notices: io.Discard, hc: hc, warned: map[string]bool{}, token: "BWSYNTH-token"}
	once := func(path string) error { _, _, err := c.once(path); return err }
	// The client-configuration host's reason phrase is not shown at all (remote.go).
	if _, err := FetchClientConfig(x, srv, nil); err == nil || strings.Contains(err.Error(), "BWSYNTH") || !strings.Contains(err.Error(), "answered 404 Not Found") {
		t.Errorf("client configuration: %q; want the status alone", err)
	}
	for name, err := range map[string]error{
		"live": Live(x, nil), "live redirect": Live(&Context{Name: "lab", Server: moved, Credential: "automation"}, nil),
		"not a problem": once("/plain"), "redirect": once("/redirect"), "cut-off answer": once("/cut"),
	} {
		if err == nil || hostile(err.Error()) || !strings.Contains(err.Error(), `BWSYNTH-\x1b[2J\x07phrase`) {
			t.Errorf("%s: %q; want the reason phrase escaped", name, err)
		}
	}

	shown := func(s string) bool {
		return !strings.ContainsFunc(s, func(r rune) bool { return r != '\n' && unicode.IsControl(r) })
	}
	for _, p := range []*Problem{
		{Status: 403, Doc: map[string]any{"type": problemPrefix + "forbidden", "title": "BWSYNTH-\x1b[2J", "detail": "BWSYNTH-\r",
			"instance": "BWSYNTH-\a", "roles": []any{"BWSYNTH-\x1b]0;r\a"}}, Subject: "BWSYNTH-\u009b"},
		{Status: 409, Doc: map[string]any{"type": problemPrefix + "recovery-mode-active", "scope": "BWSYNTH-\x1b", "machine": "BWSYNTH-\x7f"}},
		{Status: 401, Doc: map[string]any{"type": problemPrefix + "unauthenticated"}, Reading: "BWSYNTH-\x1b[2J"},
		{Status: 403, Doc: map[string]any{"type": problemPrefix + "identity-revoked"}, Cleared: "BWSYNTH-\x1b[2J"},
		{Status: 400, Doc: map[string]any{"type": problemPrefix + "BWSYNTH-\x1b[2J"}},
	} {
		if s := p.Error(); !shown(s) || !strings.Contains(s, `BWSYNTH-\x`) {
			t.Errorf("problem %s: %q; want each member's control characters escaped", p.Code(), s)
		}
	}
}

// TestURLsWithControlCharactersAreRefused checks that a URL the errors show as it is (an issuer
// from an imported client configuration, a server, an endpoint the identity provider names) holds
// no character that printable would escape: url.Parse admits C1 controls and bytes that are not
// UTF-8 (§6 item 4).
func TestURLsWithControlCharactersAreRefused(t *testing.T) {
	for _, raw := range []string{"https://BWSYNTH.example.invalid/\u009b2J", "https://BWSYNTH.example.invalid/\xff"} {
		x := &Context{Name: "lab", Server: "https://BWSYNTH.example.invalid", Credential: "oidc",
			OIDC: &OIDC{Issuer: raw, ClientID: "BWSYNTH-cli"}}
		for name, err := range map[string]error{
			"issuer": x.Check(), "server": (&Context{Name: "lab", Server: raw, Credential: "automation"}).Check(),
			"token endpoint": providerEndpoint(raw, "token_endpoint"),
		} {
			if err == nil || hostile(err.Error()) || !strings.Contains(err.Error(), "no control characters") {
				t.Errorf("%s %q: %v; want it refused, shown escaped", name, raw, err)
			}
		}
	}
	if err := providerEndpoint("https://BWSYNTH.example.invalid/é?a=b", "token_endpoint"); err != nil {
		t.Errorf("a printable non-ASCII URL: %v; want it accepted", err)
	}
	// A command that shows a context without checking it (logout, context use) still escapes it.
	x := &Context{Name: "lab\x1b[2J", Server: "https://BWSYNTH.example.invalid/\u009b2J"}
	if w := x.Where(); hostile(w) || !strings.Contains(w, `lab\x1b[2J (https://BWSYNTH.example.invalid/\x9b2J)`) {
		t.Errorf("Where: %q; want the name and server escaped", w)
	}
}
