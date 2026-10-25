package client

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// httpClient follows client-contracts.md §6: TLS 1.2 at least, host names verified, the context's
// bundle replacing the system store, no way to disable verification, no redirect followed. Go
// reaches an https URL through a proxy only by a CONNECT tunnel and never proxies loopback.
func httpClient(x *Context, verbose io.Writer) (*http.Client, error) {
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
	proxy := func(r *http.Request) (*url.URL, error) {
		u, err := http.ProxyFromEnvironment(r)
		if u != nil && verbose != nil {
			fmt.Fprintf(verbose, "%s: through the proxy %s\n", r.URL.Host, u.Redacted())
		}
		return u, err
	}
	return &http.Client{
		Transport: &http.Transport{Proxy: proxy, TLSClientConfig: tc, ForceAttemptHTTP2: true,
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
