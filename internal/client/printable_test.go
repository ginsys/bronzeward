package client

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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

// TestTransportErrorsArePrintable checks #380 on the three paths: the server's transport, the
// identity provider's discovery and the renewal at its token endpoint. Each meets a certificate
// whose DNS names hold control characters, and none reaches the error text; the trust refusal
// still names the host, subject, issuer and trust source (§6 item 4).
func TestTransportErrorsArePrintable(t *testing.T) {
	// http.ProxyFromEnvironment reads the environment once per process, so the first request
	// fixes it for every later test: this one leaves the proxy TestLiveSaysItUsesAProxy names.
	// Go never proxies localhost, so the requests here are not affected.
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
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
				"token_endpoint": bad + "/token", "jwks_uri": iss.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"ES256"}})
		}))
		defer iss.Close()
		x := &Context{Name: "lab", OIDC: &OIDC{Issuer: iss.URL, ClientID: "BWSYNTH-cli", CertificateAuthority: bundleOf(t, iss.Certificate().Raw, badDER)}}
		_, err := refresh(x, "BWSYNTH-rt")
		if err == nil || hostile(err.Error()) || !strings.Contains(err.Error(), "renewing at the identity provider "+iss.URL) ||
			!strings.Contains(err.Error(), `BWSYNTH-\x1b[2J.example.invalid`) {
			t.Errorf("renewal: %q; want the renewal failed at the token endpoint, its certificate's names escaped", err)
		}
	})
}
