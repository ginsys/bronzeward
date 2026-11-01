package main

import (
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// hostileProxy is the proxy of this test binary: http.ProxyFromEnvironment reads the environment
// once per process, so TestMain sets it before any test sends a request. Go never proxies loopback,
// so only a request to another host name reaches it. The proxy tunnels every CONNECT to a TLS
// server whose certificate no context trusts, so a command that reaches it fails at once with a
// trust refusal, which the client does not repeat. Its URL holds a C1 control (U+009B) and a
// right-to-left override (U+202E) in the query, as -v prints it.
var hostileProxy string

func TestMain(m *testing.M) {
	tls := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	tls.Config.ErrorLog = log.New(io.Discard, "", 0) // the handshakes of a client that refuses its certificate
	tls.StartTLS()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		up, err := net.Dial("tcp", tls.Listener.Addr().String())
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
		go func() { io.Copy(conn, up); conn.Close() }()
	}))
	hostileProxy = proxy.URL + "/?q=BWSYNTH\xc2\x9b\xe2\x80\xaeEND"
	os.Setenv("HTTP_PROXY", hostileProxy)
	os.Setenv("HTTPS_PROXY", hostileProxy)
	os.Unsetenv("NO_PROXY")
	os.Unsetenv("no_proxy")
	code := m.Run()
	proxy.Close()
	tls.Close()
	os.Exit(code)
}
