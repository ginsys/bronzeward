package client

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// sent stands for a token the identity provider or the client-configuration host may send.
const sent = "BWSYNTH-SENT"

// answer is a raw HTTP/1.1 response, closing the connection.
func answer(status, headers, body string) string {
	return fmt.Sprintf("HTTP/1.1 %s\r\n%sContent-Length: %d\r\nConnection: close\r\n\r\n%s", status, headers, len(body), body)
}

func jsonAnswer(status, body string) string {
	return answer(status, "Content-Type: application/json\r\n", body)
}

// discoveryOf is a discovery document for iss whose endpoints are paths on it; extra replaces the
// revocation_endpoint member.
func discoveryOf(iss, revocation string) string {
	return answer("200 OK", "Content-Type: application/json\r\n", fmt.Sprintf(`{"issuer":%q,"authorization_endpoint":"%s/authorize",`+
		`"token_endpoint":"%s/token","jwks_uri":"%s/jwks","id_token_signing_alg_values_supported":["ES256"],"revocation_endpoint":%s}`,
		iss, iss, iss, iss, revocation))
}

// offLoopback is a redirect target bronzewardctl refuses, its user, path and query the sentinel.
const offLoopback = "http://" + sent + ":" + sent + "@BWSYNTH.example.invalid/" + sent + "?" + sent + "=" + sent

// TestRemoteTextIsNotShown checks ginsys/bronzeward#395 at each site on the identity-provider and
// client-configuration paths: a response or failure carrying the sentinel shows none of it, only
// the fixed text, the status and its standard text, and what bronzewardctl itself formed.
func TestRemoteTextIsNotShown(t *testing.T) {
	proxyAsTheProxyTestNamesIt(t)
	idp := func(t *testing.T, at map[string]func(iss string) string) *Context {
		iss := rawAt(t, func(base string) map[string]string {
			m := map[string]string{}
			for p, f := range at {
				m[p] = f(base)
			}
			return m
		})
		return &Context{Name: "lab", OIDC: &OIDC{Issuer: iss, ClientID: "BWSYNTH-cli"}}
	}
	disc := func(f func(iss string) string) map[string]func(string) string {
		return map[string]func(string) string{"/.well-known/openid-configuration": f}
	}
	const wk = "/.well-known/openid-configuration"
	withToken := func(token, revocation string, more map[string]string) map[string]func(string) string {
		// REV in a revocation_endpoint is the issuer, known only once the server listens.
		m := map[string]func(string) string{wk: func(iss string) string { return discoveryOf(iss, strings.ReplaceAll(revocation, "REV", iss)) }}
		for p, a := range more {
			m[p] = func(string) string { return a }
		}
		if token != "" {
			m["/token"] = func(string) string { return token }
		}
		return m
	}
	renew := func(x *Context) error { _, err := refresh(x, "BWSYNTH-rt"); return err }
	revoke := func(x *Context) error { return Revoke(x, "BWSYNTH-rt") }
	type site struct {
		at   map[string]func(string) string
		call func(*Context) error
		want string // after the issuer
	}
	for name, c := range map[string]site{
		"discovery redirected off loopback": {disc(func(string) string { return answer("302 Found", "Location: "+offLoopback+"\r\n", "") }), renew,
			": it redirected to http://BWSYNTH.example.invalid, which bronzewardctl does not follow"},
		"discovery redirect unparsable": {disc(func(string) string { return answer("302 Found", "Location: http://["+sent+"\r\n", "") }), renew,
			": the request failed"},
		"discovery status garbled": {disc(func(string) string { return "HTTP/1.1 " + sent + "\r\nConnection: close\r\n\r\n" }), renew,
			": the request failed"},
		"discovery refused": {disc(func(string) string { return jsonAnswer("503 "+sent, `{"error":"`+sent+`"}`) }), renew,
			" refused its discovery: 503 Service Unavailable"},
		"discovery undecodable": {disc(func(string) string { return jsonAnswer("200 OK", `{"issuer":`+sent+`}`) }), renew,
			": its discovery document could not be read"},
		"renewal refused": {withToken(jsonAnswer("400 "+sent, `{"error":"`+sent+`","error_description":"`+sent+`"}`), `null`, nil), renew,
			" refused the renewal: 400 Bad Request"},
		"renewal answer undecodable": {withToken(jsonAnswer("200 OK", `{"access_token":"`+sent+`","token_type":"Bearer","expires_in":"`+sent+`"}`), `null`, nil), renew,
			": its answer could not be read"},
		"renewal status garbled": {withToken("HTTP/1.1 "+sent+"\r\nConnection: close\r\n\r\n", `null`, nil), renew,
			": the request failed"},
		"renewal redirected off loopback": {withToken(answer("307 Temporary Redirect", "Location: "+offLoopback+"\r\n", ""), `null`, nil), renew,
			": it redirected to http://BWSYNTH.example.invalid, which bronzewardctl does not follow"},
		"revocation refused": {withToken("", `"REV/revoke"`, map[string]string{"/revoke": jsonAnswer("400 "+sent, `{"error":"`+sent+`"}`)}), revoke,
			" refused the revocation: 400 Bad Request"},
		"revocation status garbled": {withToken("", `"REV/revoke"`, map[string]string{"/revoke": "HTTP/1.1 " + sent + "\r\nConnection: close\r\n\r\n"}), revoke,
			": the request failed"},
		"revocation endpoint off loopback": {withToken("", fmt.Sprintf("%q", offLoopback), nil), revoke,
			"the identity provider's revocation_endpoint http://BWSYNTH.example.invalid: give a URL with a host and no fragment or user"},
	} {
		t.Run(name, func(t *testing.T) {
			x := idp(t, c.at)
			err := c.call(x)
			want := c.want
			if !strings.HasPrefix(want, "the identity provider's") {
				want = x.OIDC.Issuer + want
			}
			if err == nil || strings.Contains(err.Error(), sent) || hostile(err.Error()) || !strings.Contains(err.Error(), want) {
				t.Errorf("%q; want %q and nothing the provider sent", err, want)
			}
		})
	}

	t.Run("client configuration", func(t *testing.T) {
		for name, c := range map[string]struct{ answer, want string }{
			"status garbled": {"HTTP/1.1 " + sent + "\r\nConnection: close\r\n\r\n", ": the request failed"},
			"refused":        {jsonAnswer("404 "+sent, sent), " answered 404 Not Found, not the client configuration file"},
			"redirected":     {answer("302 Found", "Location: "+offLoopback+"\r\n", ""), " answered 302 Found"},
			// textproto quotes a trailer line it cannot parse, read while the JSON is incomplete
			"bad trailer": {"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\na\r\n{\"issuer\":\r\n0\r\n" + sent + "\r\n\r\n",
				": its answer could not be read as a client configuration file"},
		} {
			srv := rawServer(t, map[string]string{"/bronzeward-client.json": c.answer})
			want := "GET " + srv + "/bronzeward-client.json" + c.want
			_, err := FetchClientConfig(&Context{Name: "lab", Server: srv, Credential: "automation"}, srv, nil)
			if err == nil || strings.Contains(err.Error(), sent) || !strings.Contains(err.Error(), want) {
				t.Errorf("%s: %q; want %q and nothing the host sent", name, err, want)
			}
		}
	})

	// x509 quotes a certificate's URI it cannot parse; tls puts that error in the handshake's.
	t.Run("certificate x509 cannot parse", func(t *testing.T) {
		bad := uriServer(t)
		x := &Context{Name: "lab", OIDC: &OIDC{Issuer: bad, ClientID: "BWSYNTH-cli"}}
		_, ferr := FetchClientConfig(&Context{Name: "lab", Server: bad, Credential: "automation"}, bad, nil)
		proxy, _ := url.Parse(bad)
		hc, err := newHTTPClient(&Context{Name: "lab", Server: "https://BWSYNTH.example.invalid", Credential: "automation"}, nil,
			func(*http.Request) (*url.URL, error) { return proxy, nil }, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, perr := hc.Get("https://BWSYNTH.example.invalid/livez")
		for name, c := range map[string]struct {
			err  error
			want string
		}{
			"discovery":            {renew(x), "cannot reach the identity provider " + bad + ": the request failed"},
			"client configuration": {ferr, "GET " + bad + "/bronzeward-client.json: the request failed"},
			"https proxy":          {perr, "verified by the system trust store: the TLS handshake failed"},
		} {
			if c.err == nil || strings.Contains(c.err.Error(), sent) || !strings.Contains(c.err.Error(), c.want) {
				t.Errorf("%s: %q; want %q and nothing the certificate holds", name, c.err, c.want)
			}
		}
	})
}

// uriServer is the https URL, as localhost, of a server whose certificate holds a URI that x509
// cannot parse and quotes in its error.
func uriServer(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "BWSYNTH-uri"},
		DNSNames: []string{"localhost"}, URIs: []*url.URL{{Scheme: "https", Host: sent + "..invalid"}},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x509.ParseCertificate(der); err == nil || !strings.Contains(err.Error(), sent) {
		t.Fatalf("parsing the certificate: %v; want x509 to quote its URI, or this test shows nothing", err)
	}
	// StartTLS parses the certificate, so the TLS listener is made here.
	srv := httptest.NewUnstartedServer(http.NotFoundHandler())
	srv.Listener = tls.NewListener(srv.Listener, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}})
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.Start()
	t.Cleanup(srv.Close)
	return strings.Replace(srv.URL, "http://127.0.0.1", "https://localhost", 1)
}
