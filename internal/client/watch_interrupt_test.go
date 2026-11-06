package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const streamRoute = "GET /api/v1/operations/op_BWSYNTH/events"

// streamStall serves op_BWSYNTH: the event stream answers as stream says, and an operation read
// shows the state running. arrived gets one value at each stream request.
type streamStall struct {
	srv     *httptest.Server
	arrived chan struct{}
	hang    chan struct{}
	streams atomic.Int32
}

func newStreamStall(t *testing.T, stream func(w http.ResponseWriter, r *http.Request)) *streamStall {
	t.Helper()
	s := &streamStall{arrived: make(chan struct{}, 8), hang: make(chan struct{})}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-1")
		switch {
		case r.URL.Path == "/api/v1/operations/op_BWSYNTH/events" && r.Header.Get("Accept") == "text/event-stream":
			s.streams.Add(1)
			s.arrived <- struct{}{}
			stream(w, r)
		case r.URL.Path == "/api/v1/operations/op_BWSYNTH":
			fmt.Fprint(w, `{"id":"op_BWSYNTH","kind":"publish","state":"running"}`)
		default:
			problemAnswer(w, http.StatusNotFound, "not-found", "")
		}
	}))
	t.Cleanup(s.srv.Close)
	t.Cleanup(func() { close(s.hang) })
	return s
}

// ended runs a watch of op_BWSYNTH on c and returns its error, or fails when it does not end within 5 s.
func ended(t *testing.T, c *Client, w Watch) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { _, err := c.Watch("op_BWSYNTH", w); done <- err }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the watch did not end within 5 s of the Ctrl-C")
		return nil
	}
}

// TestStreamEndsOnInterrupt: the server takes the stream request and never answers. The Ctrl-C ends
// the watch at once with the route of the stream, with no transport notice and no second request.
func TestStreamEndsOnInterrupt(t *testing.T) {
	s := newStreamStall(t, func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	c, notices, _ := writeClient(t, &fake{epoch: "ep_BWSYNTH-1"}, "ep_BWSYNTH-1")
	c.Context.Server = s.srv.URL
	a := newArmed(t)
	c.Arm = a.hook
	go func() { <-s.arrived; a.cancel() }()
	err := ended(t, c, Watch{})
	var cut *Interrupted
	switch {
	case !errors.As(err, &cut) || cut.Route != streamRoute:
		t.Errorf("error %v; want the watch stopped by Ctrl-C, naming %s", err, streamRoute)
	case notices.Len() != 0:
		t.Errorf("notices %q; want none for a stream the Ctrl-C ended", notices)
	case s.streams.Load() != 1:
		t.Errorf("%d stream requests; want 1", s.streams.Load())
	case a.open.Load() != 0:
		t.Errorf("%d arms left open; want the stream to disarm", a.open.Load())
	}
}

// TestStreamBodyReadEndsOnInterrupt: the stream is open and showed one event, then went silent. The
// Ctrl-C ends the body read, not the 45 s the idle timer would take.
func TestStreamBodyReadEndsOnInterrupt(t *testing.T) {
	s := newStreamStall(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ev("ep_BWSYNTH-1:1", "queued"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	c, notices, _ := writeClient(t, &fake{epoch: "ep_BWSYNTH-1"}, "ep_BWSYNTH-1")
	c.Context.Server = s.srv.URL
	a := newArmed(t)
	c.Arm = a.hook
	var shown []string
	got := make(chan struct{}, 1)
	go func() { <-got; a.cancel() }() // the Ctrl-C comes once the event is shown
	err := ended(t, c, Watch{Show: func(e Event) { shown = append(shown, e.ID); got <- struct{}{} }})
	var cut *Interrupted
	switch {
	case !errors.As(err, &cut) || cut.Route != streamRoute:
		t.Errorf("error %v; want the watch stopped by Ctrl-C, naming %s", err, streamRoute)
	case len(shown) != 1:
		t.Errorf("events shown %v; want the one before the silence", shown)
	case notices.Len() != 0:
		t.Errorf("notices %q; want none", notices)
	case s.streams.Load() != 1:
		t.Errorf("%d stream requests; want 1", s.streams.Load())
	}
}

// TestStreamBackoffEndsOnInterrupt: the stream ends with no event and the operation is still running,
// so the watch waits 1 s before the next attempt. The Ctrl-C comes during that wait and ends the watch
// with the route of the stream: no second stream request, no further wait.
func TestStreamBackoffEndsOnInterrupt(t *testing.T) {
	s := newStreamStall(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
	})
	c, _, _ := writeClient(t, &fake{epoch: "ep_BWSYNTH-1"}, "ep_BWSYNTH-1")
	c.Context.Server = s.srv.URL
	a := newArmed(t)
	c.Arm = a.hook
	var waits []time.Duration
	waitFor = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		a.cancel() // the Ctrl-C comes while the watch waits
		<-ctx.Done()
		return ctx.Err()
	}
	t.Cleanup(func() { waitFor = defaultWait })
	err := ended(t, c, Watch{})
	var cut *Interrupted
	switch {
	case !errors.As(err, &cut) || cut.Route != streamRoute:
		t.Errorf("error %v; want the watch stopped by Ctrl-C, naming %s", err, streamRoute)
	case len(waits) != 1 || waits[0] != time.Second:
		t.Errorf("waits %v; want the one wait of 1s that the Ctrl-C ended", waits)
	case s.streams.Load() != 1:
		t.Errorf("%d stream requests; want 1: a Ctrl-C during the wait opens no second stream", s.streams.Load())
	case a.open.Load() != 0:
		t.Errorf("%d arms left open; want the wait to disarm", a.open.Load())
	}
}

// TestStreamArmsNothingWithoutAHook: a client with no Arm keeps the default action of Ctrl-C, and
// the watch still shows its events and ends on Until.
func TestStreamArmsNothingWithoutAHook(t *testing.T) {
	s := newStreamStall(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ev("ep_BWSYNTH-1:1", "queued")+ev("ep_BWSYNTH-1:2", "succeeded"))
	})
	c, _, _ := writeClient(t, &fake{epoch: "ep_BWSYNTH-1"}, "ep_BWSYNTH-1")
	c.Context.Server = s.srv.URL
	var shown []string
	_, err := c.Watch("op_BWSYNTH", Watch{Show: func(e Event) { shown = append(shown, e.ID) }, Until: func() (bool, error) { return len(shown) == 2, nil }, sleep: func(time.Duration) {}})
	if err != nil || len(shown) != 2 {
		t.Errorf("shown %v, error %v; want both events and no stop", shown, err)
	}
}
