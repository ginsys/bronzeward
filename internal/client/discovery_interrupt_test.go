package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
	"testing"
	"time"
)

// stalledConfig serves bronzeward-client.json as a 200 and the start of the body, flushes it, and
// then stalls until the request ends or the test is over. flushed is signalled once, after the flush.
func stalledConfig(t *testing.T) (url string, flushed <-chan struct{}) {
	t.Helper()
	sent, hang := make(chan struct{}, 1), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"issuer":`)
		w.(http.Flusher).Flush()
		sent <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-hang:
		}
	}))
	t.Cleanup(func() { close(hang); srv.Close() })
	return srv.URL, sent
}

// TestFetchClientConfigEndsOnCtrlCDuringTheDecode: the server sends 200 and the start of the file, and
// then stalls. The Ctrl-C comes while the client decodes the body. The read ends with *Interrupted, as
// it does when the Ctrl-C comes before the answer, not with the plain error for an unreadable file.
func TestFetchClientConfigEndsOnCtrlCDuringTheDecode(t *testing.T) {
	proxyAsTheProxyTestNamesIt(t)
	srv, flushed := stalledConfig(t)
	// The client reads the first byte of the answer: that event, not the server's flush, tells the test
	// that the headers are on their way to the decode. The client gives no hook after hc.Do returns, so two
	// windows stay open inside the 200 ms wait below: the parse of the headers, and the handoff of the
	// response to roundTrip. A stall in either that outlasts the wait reaches the request-error branch, which
	// also returns *Interrupted, so the test then passes without the fix. The 100/100 probe does not exclude it.
	gotFirstByte := make(chan struct{})
	var once sync.Once
	trace := &httptrace.ClientTrace{GotFirstResponseByte: func() { once.Do(func() { close(gotFirstByte) }) }}
	ctx, cancel := context.WithCancel(httptrace.WithClientTrace(context.Background(), trace))
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := FetchClientConfigContext(ctx, &Context{Name: "lab", Server: srv, Credential: "automation"}, srv, nil)
		done <- err
	}()
	for _, wait := range []<-chan struct{}{flushed, gotFirstByte} { // the server flushed, then the client read it
		select {
		case <-wait:
		case <-time.After(5 * time.Second):
			t.Fatal("the answer did not reach the client")
		}
	}
	time.Sleep(200 * time.Millisecond) // the client parses the headers and waits in the decode for the rest
	cancel()
	select {
	case err := <-done:
		var cut *Interrupted
		if !errors.As(err, &cut) || cut.Route != "GET /bronzeward-client.json" {
			t.Errorf("error %v; want the read stopped by Ctrl-C, naming GET /bronzeward-client.json", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read did not end within 5 s of the Ctrl-C")
	}
}

// TestFetchClientConfigUnreadableBodyIsNoCtrlC is a control: the same start of a file, with the
// connection closed and no Ctrl-C, ends with the plain error for an unreadable file, never *Interrupted.
func TestFetchClientConfigUnreadableBodyIsNoCtrlC(t *testing.T) {
	proxyAsTheProxyTestNamesIt(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"issuer":`)
	}))
	defer srv.Close()
	_, err := FetchClientConfigContext(context.Background(), &Context{Name: "lab", Server: srv.URL, Credential: "automation"}, srv.URL, nil)
	if err == nil || errors.As(err, new(*Interrupted)) || !strings.Contains(err.Error(), "its answer could not be read as a client configuration file") {
		t.Errorf("error %v; want the plain error for an unreadable file and no Ctrl-C", err)
	}
}

// TestFetchClientConfigEndsOnCtrlCBeforeTheAnswer is a control for the first test: a server that
// never answers and a Ctrl-C end the read with *Interrupted, which is the branch that already exists.
func TestFetchClientConfigEndsOnCtrlCBeforeTheAnswer(t *testing.T) {
	proxyAsTheProxyTestNamesIt(t)
	arrived, hang := make(chan struct{}, 1), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-hang:
		}
	}))
	defer srv.Close()
	defer close(hang)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := FetchClientConfigContext(ctx, &Context{Name: "lab", Server: srv.URL, Credential: "automation"}, srv.URL, nil)
		done <- err
	}()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("the request did not reach the server")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.As(err, new(*Interrupted)) {
			t.Errorf("error %v; want the read stopped by Ctrl-C", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read did not end within 5 s of the Ctrl-C")
	}
}
