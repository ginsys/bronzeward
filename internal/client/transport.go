package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// httpClient follows client-contracts.md §6: TLS 1.2 at least, host names verified, the context's
// bundle replacing the system store, no way to disable verification, no redirect followed. Go
// reaches an https URL through a proxy only by a CONNECT tunnel and never proxies loopback.
func httpClient(x *Context, verbose io.Writer) (*http.Client, error) {
	return newHTTPClient(x, verbose, http.ProxyFromEnvironment, nil)
}

// newHTTPClient takes the proxy's source and the roots an https proxy is verified by (nil: the
// system store). Go would verify an https proxy with the context's bundle, which is for the
// Bronzeward server alone, so such a proxy is named to Go as a plain one and dialled with TLS here.
func newHTTPClient(x *Context, verbose io.Writer, from func(*http.Request) (*url.URL, error), proxyRoots *x509.CertPool) (*http.Client, error) {
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if x.CertificateAuthority != "" {
		pem, err := os.ReadFile(x.CertificateAuthority)
		if err != nil {
			return nil, fmt.Errorf("certificateAuthority: %w", err)
		}
		tc.RootCAs = x509.NewCertPool()
		if !tc.RootCAs.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("certificateAuthority %s holds no PEM certificate", x.CertificateAuthority)
		}
	}
	var tlsProxies sync.Map // the dial address of each https proxy, to its host name
	proxy := func(r *http.Request) (*url.URL, error) {
		u, err := from(r)
		if u == nil {
			return nil, err
		}
		if verbose != nil {
			fmt.Fprintf(verbose, "%s: through the proxy %s\n", r.URL.Host, u.Redacted())
		}
		if u.Scheme == "https" {
			// Go dials an internationalized name by its ASCII form, which the dialer would not
			// find below and would reach without TLS.
			if strings.IndexFunc(u.Hostname(), func(r rune) bool { return r >= utf8.RuneSelf }) >= 0 {
				return nil, fmt.Errorf("the https proxy %s: give its host name in ASCII (punycode)", u.Redacted())
			}
			plain := *u
			plain.Scheme = "http"
			if plain.Port() == "" {
				plain.Host = net.JoinHostPort(plain.Hostname(), "443")
			}
			tlsProxies.Store(plain.Host, plain.Hostname())
			u = &plain
		}
		return u, err
	}
	var d net.Dialer
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := d.DialContext(ctx, network, addr)
		name, ok := tlsProxies.Load(addr)
		if err != nil || !ok {
			return conn, err
		}
		pc := tls.Client(conn, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: name.(string), RootCAs: proxyRoots})
		if err := pc.HandshakeContext(ctx); err != nil {
			conn.Close()
			var v *tls.CertificateVerificationError
			if errors.As(err, &v) && len(v.UnverifiedCertificates) > 0 {
				c := v.UnverifiedCertificates[0]
				return nil, &trust{fmt.Errorf("the certificate of the proxy %s (subject %q, issuer %q) is not trusted by the system trust store: %v; nothing was sent",
					addr, c.Subject, c.Issuer, v.Err)}
			}
			return nil, fmt.Errorf("the proxy %s, verified by the system trust store: %w", addr, err)
		}
		return pc, nil
	}
	return &http.Client{
		Transport: &http.Transport{Proxy: proxy, DialContext: dial, TLSClientConfig: tc, ForceAttemptHTTP2: true,
			TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       time.Minute,
	}, nil
}

// trust is a refused server certificate (§6 item 4).
type trust struct{ error }

// transportError names the host, the presented certificate's subject and issuer and the trust
// source when verification refused the server (§6 item 4); the request was not sent.
func transportError(x *Context, host string, err error) error {
	var v *tls.CertificateVerificationError
	if !errors.As(err, &v) || len(v.UnverifiedCertificates) == 0 {
		return err
	}
	c, source := v.UnverifiedCertificates[0], "the system trust store"
	if x.CertificateAuthority != "" {
		source = "the bundle " + x.CertificateAuthority
	}
	return &trust{fmt.Errorf("the certificate of %s (subject %q, issuer %q) is not trusted by %s: %v; nothing was sent",
		host, c.Subject, c.Issuer, source, v.Err)}
}
