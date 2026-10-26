package client

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestLiveSaysItUsesAProxy checks §6 item 7: verbose output says a request goes through a proxy.
func TestLiveSaysItUsesAProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	var b strings.Builder
	Live(&Context{Name: "remote", Server: "https://bronzeward.example.invalid", Credential: "automation"}, &b)
	if !strings.Contains(b.String(), "bronzeward.example.invalid: through the proxy http://127.0.0.1:1") {
		t.Errorf("verbose output %q does not name the proxy", b.String())
	}
}

// TestHTTPSProxyTrust checks that an https proxy is verified by its own roots (the system store
// outside tests), never by the context's bundle, which is for the Bronzeward server alone.
func TestHTTPSProxyTrust(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	proxyCert, proxyDER := selfSigned(t)
	var connects atomic.Int32
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		connects.Add(1)
		up, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		conn, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			up.Close()
			return
		}
		conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
		go func() { io.Copy(up, conn); up.Close() }()
		io.Copy(conn, up)
		conn.Close()
	}))
	proxy.TLS = &tls.Config{Certificates: []tls.Certificate{proxyCert}}
	proxy.StartTLS()
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	from := func(*http.Request) (*url.URL, error) { return proxyURL, nil }
	bundle := func(ders ...[]byte) string {
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
	proxyRoots := x509.NewCertPool()
	proxyRoots.AddCert(proxyCert.Leaf)

	for _, c := range []struct {
		name  string
		ca    string
		roots *x509.CertPool
		ok    bool
	}{
		{"proxy trusted by its roots", bundle(srv.Certificate().Raw), proxyRoots, true},
		{"proxy only in the bundle", bundle(srv.Certificate().Raw, proxyDER), x509.NewCertPool(), false},
	} {
		connects.Store(0)
		hc, err := newHTTPClient(&Context{Name: "lab", Server: srv.URL, Credential: "automation", CertificateAuthority: c.ca}, nil, from, c.roots)
		if err != nil {
			t.Fatal(err)
		}
		r, err := hc.Get(srv.URL + "/livez")
		if c.ok {
			if err != nil || r.StatusCode != http.StatusNoContent || connects.Load() != 1 {
				t.Errorf("%s: %v, %v, %d CONNECTs; want 204 through one tunnel", c.name, err, r, connects.Load())
			}
		} else if tr := (*trust)(nil); !errors.As(err, &tr) || !strings.Contains(err.Error(), `subject "CN=BWSYNTH-proxy"`) || connects.Load() != 0 {
			t.Errorf("%s: %v, %d CONNECTs; want a trust refusal naming the proxy's certificate, before any CONNECT", c.name, err, connects.Load())
		}
		if r != nil {
			r.Body.Close()
		}
		hc.CloseIdleConnections()
	}

	// An internationalized https proxy name is refused, never reached without TLS.
	var dials atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { dials.Add(1) }))
	defer plain.Close()
	idn, _ := url.Parse("https://BWSYNTH-user:BWSYNTH-pass@bücher.example.invalid:" + strings.TrimPrefix(plain.Listener.Addr().String(), "127.0.0.1:"))
	hc, err := newHTTPClient(&Context{Name: "lab", Server: srv.URL, Credential: "automation"}, nil, func(*http.Request) (*url.URL, error) { return idn, nil }, proxyRoots)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hc.Get(srv.URL + "/livez"); err == nil || !strings.Contains(err.Error(), "in ASCII") || strings.Contains(err.Error(), "BWSYNTH-pass") || dials.Load() != 0 {
		t.Errorf("internationalized proxy: %v, %d requests; want a refusal without the password and no request", err, dials.Load())
	}
}

// selfSigned is a certificate for 127.0.0.1 that no other test certificate signs.
func selfSigned(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "BWSYNTH-proxy"},
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:   time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, der
}
