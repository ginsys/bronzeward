package server

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

// start serves srv on a loopback port and returns the address; the server is closed at test end.
func start(t *testing.T, srv *http.Server) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

func TestNewHTTPSettings(t *testing.T) {
	srv := NewHTTP("127.0.0.1:0")
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Fatalf("unbounded read: header=%v read=%v idle=%v", srv.ReadHeaderTimeout, srv.ReadTimeout, srv.IdleTimeout)
	}
	if !srv.DisableGeneralOptionsHandler {
		t.Fatal("net/http's general OPTIONS handler answers `OPTIONS *` without reaching the mux")
	}
}

// `OPTIONS *` must reach the mux, which refuses it, instead of net/http's handler answering 200.
func TestOptionsStarIsNotAnswered(t *testing.T) {
	addr := start(t, NewHTTP(""))
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(c, "OPTIONS * HTTP/1.1\r\nHost: x\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("OPTIONS * = %d", resp.StatusCode)
	}
}

// A request that declares a body and never sends it must not hold the connection open: the
// server closes it once ReadTimeout (shortened here) expires while draining the unread body.
func TestStalledBodyIsDropped(t *testing.T) {
	srv := NewHTTP("")
	srv.ReadTimeout = 300 * time.Millisecond
	addr := start(t, srv)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	io.WriteString(c, "POST /livez HTTP/1.1\r\nHost: x\r\nContent-Length: 1\r\n\r\n")
	r := bufio.NewReader(c)
	resp, err := http.ReadResponse(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if _, err := r.ReadByte(); errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatal("connection still open 3s after a stalled body; the server never gave up on it")
	}
}
