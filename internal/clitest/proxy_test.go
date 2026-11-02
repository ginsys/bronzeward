package clitest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"sync/atomic"
	"testing"
)

// newBackendProxy is a reverse proxy to target on a transport of its own, closed with the test.
// A proxy on http.DefaultTransport shares its connections with every other fixture, baotest and
// the tests' own clients in the process, and a pooled connection of that kind was closed under a
// proxy mid-body, so that an ordinary Transit answer reached serve as a status line and a cut body
// (ginsys/bronzeward#536).
func newBackendProxy(t *testing.T, target *url.URL) *httputil.ReverseProxy {
	t.Helper()
	tr := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(tr.CloseIdleConnections)
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.Transport = tr
	return rp
}

// transitCall matches the Transit requests that are safe to send twice: encrypt, decrypt and hmac
// compute from their input and change nothing in OpenBao.
var transitCall = regexp.MustCompile(`^/v1/transit/(encrypt|decrypt|hmac)/`)

// wholeTransit reads the whole answer to a repeatable Transit request before it returns it, and
// sends the request once more when the backend fails before the answer is complete. The proxy
// then answers with the whole body or with a refusal, never with a status line and a cut body. A
// canceled request is not repeated, and no other request is touched.
func wholeTransit(rt http.RoundTripper) http.RoundTripper { return &wholeTransport{rt: rt} }

type wholeTransport struct {
	rt      http.RoundTripper
	retried atomic.Int32
}

func (w *wholeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !transitCall.MatchString(req.URL.Path) {
		return w.rt.RoundTrip(req)
	}
	var in []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		in = b
	}
	for attempt := 0; ; attempt++ {
		r := req.Clone(req.Context())
		if in != nil {
			r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(in)), int64(len(in))
		}
		resp, err := w.rt.RoundTrip(r)
		if err == nil {
			var out []byte
			out, err = io.ReadAll(resp.Body)
			resp.Body.Close()
			if err == nil {
				resp.Body = io.NopCloser(bytes.NewReader(out))
				return resp, nil
			}
		}
		if attempt > 0 || req.Context().Err() != nil {
			return nil, err
		}
		w.retried.Add(1)
		log.Printf("clitest: repeating %s %s after a backend failure: %v", req.Method, req.URL.Path, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// cutOnce is a backend that answers its first request with a status line, a declared length and
// half the body, then closes the connection, and every later request in full.
func cutOnce(t *testing.T, whole string) (*url.URL, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			conn, buf, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: " + strconv.Itoa(len(whole)) + "\r\n\r\n" + whole[:len(whole)/2])
			buf.Flush()
			conn.Close()
			return
		}
		io.WriteString(w, whole)
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u, &calls
}

// TestWholeTransit checks that a Transit answer the backend cuts reaches the client whole, that
// the same cut on another path is not repeated, and that a canceled request is not repeated.
func TestWholeTransit(t *testing.T) {
	const answer = `{"data":{"ciphertext":"vault:v1:0123456789abcdef0123456789abcdef"}}`
	get := func(t *testing.T, proxyURL, path string) (string, error) {
		t.Helper()
		resp, err := http.Post(proxyURL+path, "application/json", bytes.NewReader([]byte(`{"plaintext":"eA=="}`)))
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		return string(b), err
	}

	t.Run("transit answer cut once", func(t *testing.T) {
		u, calls := cutOnce(t, answer)
		rp := newBackendProxy(t, u)
		wt := wholeTransit(rp.Transport).(*wholeTransport)
		rp.Transport = wt
		px := httptest.NewServer(rp)
		t.Cleanup(px.Close)
		got, err := get(t, px.URL, "/v1/transit/encrypt/bw-artifact")
		if err != nil || got != answer {
			t.Errorf("answer %q, %v; want %q whole", got, err, answer)
		}
		if calls.Load() != 2 || wt.retried.Load() != 1 {
			t.Errorf("backend saw %d requests, %d repeated; want 2 and 1", calls.Load(), wt.retried.Load())
		}
	})

	t.Run("other path not repeated", func(t *testing.T) {
		u, calls := cutOnce(t, answer)
		rp := newBackendProxy(t, u)
		rp.Transport = wholeTransit(rp.Transport)
		px := httptest.NewServer(rp)
		t.Cleanup(px.Close)
		if got, err := get(t, px.URL, "/v1/secret/data/x"); err == nil && got == answer {
			t.Errorf("a KV write was answered whole: %q", got)
		}
		if calls.Load() != 1 {
			t.Errorf("backend saw %d requests; want 1", calls.Load())
		}
	})

	t.Run("canceled request not repeated", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls atomic.Int32
		wt := &wholeTransport{rt: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			cancel()
			return nil, ctx.Err()
		})}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://backend.invalid/v1/transit/encrypt/bw-artifact", bytes.NewReader([]byte(`{}`)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := wt.RoundTrip(req); !errors.Is(err, context.Canceled) {
			t.Errorf("error %v; want context canceled", err)
		}
		if calls.Load() != 1 || wt.retried.Load() != 0 {
			t.Errorf("transport saw %d calls, %d repeated; want 1 and 0", calls.Load(), wt.retried.Load())
		}
	})

	t.Run("answer cut twice is a refusal", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			conn, buf, _ := w.(http.Hijacker).Hijack()
			buf.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 50\r\n\r\nabc")
			buf.Flush()
			conn.Close()
		}))
		t.Cleanup(srv.Close)
		u, _ := url.Parse(srv.URL)
		rp := newBackendProxy(t, u)
		rp.Transport = wholeTransit(rp.Transport)
		rp.ErrorLog = log.New(io.Discard, "", 0)
		px := httptest.NewServer(rp)
		t.Cleanup(px.Close)
		resp, err := http.Post(px.URL+"/v1/transit/decrypt/bw-staging", "application/json", bytes.NewReader([]byte(`{}`)))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadGateway {
			t.Errorf("status %d; want 502, never 200 with a cut body", resp.StatusCode)
		}
		if calls.Load() != 2 {
			t.Errorf("backend saw %d requests; want 2", calls.Load())
		}
	})
}
