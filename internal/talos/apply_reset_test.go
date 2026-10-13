package talos

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/talos/talostest"
	"golang.org/x/net/http2"
	"google.golang.org/grpc/codes"
)

// resetNode is a TLS endpoint that reads one complete HTTP/2 request and answers it with nothing but
// a RST_STREAM carrying code: no HEADERS, no trailers, no gRPC status. It is what a middlebox or a
// failing proxy in front of apid can do, and the gRPC client then synthesises a status of its own
// from the HTTP/2 error code. received is the request body bytes it read before it reset.
func resetNode(t *testing.T, p talostest.PKI, code http2.ErrCode) (endpoint string, received *atomic.Int64) {
	t.Helper()
	l, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{p.Server}, ClientCAs: p.Pool, ClientAuth: tls.RequireAndVerifyClientCert,
		MinVersion: tls.VersionTLS12, NextProtos: []string{"h2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	received = new(atomic.Int64)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go serveReset(c, code, received)
		}
	}()
	return l.Addr().String(), received
}

func serveReset(c net.Conn, code http2.ErrCode, received *atomic.Int64) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck // a test stand-in
	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(c, preface); err != nil || !bytes.Equal(preface, []byte(http2.ClientPreface)) {
		return
	}
	fr := http2.NewFramer(c, c)
	if err := fr.WriteSettings(); err != nil {
		return
	}
	for {
		f, err := fr.ReadFrame()
		if err != nil {
			return
		}
		switch f := f.(type) {
		case *http2.SettingsFrame:
			if !f.IsAck() {
				if fr.WriteSettingsAck() != nil {
					return
				}
			}
		case *http2.PingFrame:
			if !f.IsAck() {
				if fr.WritePing(true, f.Data) != nil {
					return
				}
			}
		case *http2.DataFrame:
			received.Add(int64(len(f.Data())))
			if f.StreamEnded() {
				if fr.WriteRSTStream(f.StreamID, code) != nil {
					return
				}
			}
		}
	}
}

// A stream reset after the whole request was received is no answer from the node: the client
// synthesises a gRPC status from the HTTP/2 error code (INADEQUATE_SECURITY becomes PermissionDenied,
// ENHANCE_YOUR_CALM ResourceExhausted, REFUSED_STREAM Unavailable, others Internal), and none of them
// shows what the node did with the configuration. Each accounts for nothing.
func TestApplyStreamResetIsNoAnswer(t *testing.T) {
	cases := []struct {
		name string
		code http2.ErrCode
		want codes.Code
	}{
		{"inadequate security", http2.ErrCodeInadequateSecurity, codes.PermissionDenied},
		{"enhance your calm", http2.ErrCodeEnhanceYourCalm, codes.ResourceExhausted},
		{"refused stream", http2.ErrCodeRefusedStream, codes.Unavailable},
		{"internal error", http2.ErrCodeInternal, codes.Internal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := talostest.NewPKI(t)
			endpoint, received := resetNode(t, p, c.code)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			a, err := DialApplier(ctx, p.Talosconfig("", "", ""), endpoint)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			r, err := a.ApplyNoReboot(ctx, []byte(applyConfig))
			if err != nil {
				t.Fatal(err)
			}
			if received.Load() < int64(len(applyConfig)) {
				t.Fatalf("the node read %d request bytes, want the whole request (%d): the control that the reset followed a complete request failed", received.Load(), len(applyConfig))
			}
			if r.Class != ClassTransport || r.Accounts() || r.Code != c.want {
				t.Fatalf("%+v; want a transport outcome with %s, accounting for nothing", r, c.want)
			}
		})
	}
}
