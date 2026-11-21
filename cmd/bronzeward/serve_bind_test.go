package main

import (
	"context"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// addrWriter is the output a test gives serveContext: it keeps what serve wrote and sends the
// address of the first complete "listening on <address>" line on addr, once.
type addrWriter struct {
	mu   sync.Mutex
	text string
	sent bool
	addr chan string
}

func newAddrWriter() *addrWriter { return &addrWriter{addr: make(chan string, 1)} }

func (w *addrWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.text += string(p)
	if !w.sent {
		for _, line := range strings.SplitAfter(w.text, "\n") {
			if a, ok := strings.CutPrefix(strings.TrimSuffix(line, "\n"), "listening on "); ok && strings.HasSuffix(line, "\n") {
				w.sent = true
				w.addr <- a
				break
			}
		}
	}
	return len(p), nil
}

func (w *addrWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.text
}

// databaseStandIn is a TCP listener that takes connections and answers none, in place of
// PostgreSQL: serve blocks in its database open on it, and the connections it counts are the
// proof that serve reached the database.
func databaseStandIn(t *testing.T) (dsn string, connections func() int) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = l.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return "postgres://bw@" + l.Addr().String() + "/bw?sslmode=disable", func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(conns)
	}
}

// waitFor polls cond for five seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen", what)
		}
	}
}

// startServe runs serveContext on cfg and extra flags until the test ends or stop is called, and
// returns what it writes and a channel for its end.
func startServe(t *testing.T, cfg string, extra ...string) (out *addrWriter, stop func(), ended <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	out = newAddrWriter()
	errc := make(chan error, 1)
	go func() { errc <- serveContext(ctx, append([]string{"-config", cfg}, extra...), out) }()
	return out, cancel, errc
}

// serve binds its address before it touches the database (ginsys/bronzeward#948): an address that
// is taken ends it at once, with the address in the error, and the database sees no connection.
// The control: the same database stand-in with a free address is dialed, so the zero connections
// are the order and not a database serve would never have reached.
func TestServeBindsBeforeTheDatabase(t *testing.T) {
	dsn, connections := databaseStandIn(t)
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	out, _, ended := startServe(t, configFileAt(t, dsn, held.Addr().String()))
	select {
	case err := <-ended:
		if err == nil || !strings.Contains(err.Error(), held.Addr().String()) || !strings.Contains(err.Error(), "address already in use") {
			t.Fatalf("serve on a taken address: %v; want an error naming %s and the bind", err, held.Addr())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve on a taken address did not end")
	}
	if got := out.String(); got != "" {
		t.Fatalf("serve reported %q for an address it did not bind", got)
	}
	if n := connections(); n != 0 {
		t.Fatalf("the database saw %d connections before the bind failed", n)
	}

	_, stop, ended := startServe(t, configFile(t, dsn))
	waitFor(t, "serve dialing the database on a free address (the control)", func() bool { return connections() > 0 })
	stop()
	if err := <-ended; err == nil {
		t.Fatal("serve ended with no error before its database answered")
	}
}

// serve reports the address it bound, one line, before its database work, and a configuration
// with port 0 gets the port the kernel chose, which is already accepting connections.
func TestServeReportsTheAddressItBound(t *testing.T) {
	dsn, connections := databaseStandIn(t)
	out, stop, ended := startServe(t, configFile(t, dsn)) // listen: 127.0.0.1:0
	var addr string
	select {
	case addr = <-out.addr:
	case err := <-ended:
		t.Fatalf("serve ended before it reported an address: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("serve reported no address")
	}
	if m := regexp.MustCompile(`^listening on 127\.0\.0\.1:([0-9]+)\n$`).FindStringSubmatch(out.String()); m == nil {
		t.Fatalf("serve wrote %q, want exactly one line \"listening on 127.0.0.1:<port>\"", out.String())
	} else if port, _ := strconv.Atoi(m[1]); port == 0 || m[1] != addr[strings.LastIndex(addr, ":")+1:] {
		t.Fatalf("port %s in %q: not a bound port of %s", m[1], out.String(), addr)
	}
	// The database stand-in never answers, so serve is not serving yet: the kernel accepts this dial
	// for the bound address alone. The order of bind and database is TestServeBindsBeforeTheDatabase's.
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("the reported address %s takes no connection: %v", addr, err)
	}
	_ = c.Close()
	waitFor(t, "serve reaching its database after the report", func() bool { return connections() > 0 })
	stop()
	if err := <-ended; err == nil {
		t.Fatal("serve ended with no error before its database answered")
	}
}
