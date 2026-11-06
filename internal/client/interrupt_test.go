package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// armed is the Ctrl-C hook of a test: arm hands out ctx and disarm reports whether ctx was cancelled.
// calls counts the arms and open the arms not yet disarmed.
type armed struct {
	ctx         context.Context
	cancel      context.CancelFunc
	calls, open atomic.Int32
}

func newArmed(t *testing.T) *armed {
	proxyAsTheProxyTestNamesIt(t) // a test of this file may send the first request of the process
	a := &armed{}
	a.ctx, a.cancel = context.WithCancel(context.Background())
	return a
}

func (a *armed) hook() (context.Context, func() bool) {
	a.calls.Add(1)
	a.open.Add(1)
	return a.ctx, func() bool { a.open.Add(-1); return a.ctx.Err() != nil }
}

// TestRetryWaitEndsOnInterrupt: the server answers a retryable 503. The Ctrl-C comes during the wait
// before the second try. The read ends at once with the route it was reading: no second request, no
// further wait and no notice for a third try. The wait is a fake that records the delay, so the
// test sleeps for none of it.
func TestRetryWaitEndsOnInterrupt(t *testing.T) {
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-1")
		problemAnswer(w, http.StatusServiceUnavailable, "dependency-unavailable", "")
	}))
	defer srv.Close()
	c, notices, _ := writeClient(t, &fake{epoch: "ep_BWSYNTH-1"}, "ep_BWSYNTH-1")
	c.Context.Server = srv.URL
	a := newArmed(t)
	c.Arm = a.hook
	var waits []time.Duration
	waitFor = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		a.cancel() // the Ctrl-C comes while the client waits
		<-ctx.Done()
		return ctx.Err()
	}
	t.Cleanup(func() { waitFor = defaultWait })
	_, err := c.Get("/clusters")
	var cut *Interrupted
	switch {
	case !errors.As(err, &cut) || cut.Route != "GET /api/v1/clusters":
		t.Errorf("error %v; want the read stopped by Ctrl-C, naming GET /api/v1/clusters", err)
	case asked.Load() != 1:
		t.Errorf("%d requests; want 1: a Ctrl-C during the wait sends no second try", asked.Load())
	case len(waits) != 1 || waits[0] != time.Second:
		t.Errorf("waits %v; want the one wait of 1s that the Ctrl-C ended", waits)
	case strings.Contains(notices.String(), "try 3"):
		t.Errorf("notices %q; want no notice for a third try", notices)
	case a.open.Load() != 0:
		t.Errorf("%d arms left open; want the read to disarm", a.open.Load())
	}
}

// TestStalledReadEndsOnInterrupt: the server takes the request and never answers. The Ctrl-C ends the
// read within a few seconds, not after the request timeout of one minute, which no test waits for.
func TestStalledReadEndsOnInterrupt(t *testing.T) {
	arrived, hang := make(chan struct{}, 1), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-hang
	}))
	defer srv.Close()
	defer close(hang)
	c, _, _ := writeClient(t, &fake{epoch: "ep_BWSYNTH-1"}, "ep_BWSYNTH-1")
	c.Context.Server = srv.URL
	a := newArmed(t)
	c.Arm = a.hook
	done := make(chan error, 1)
	go func() { _, err := c.Get("/clusters"); done <- err }()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("the request did not reach the server")
	}
	a.cancel()
	select {
	case err := <-done:
		var cut *Interrupted
		if !errors.As(err, &cut) {
			t.Errorf("error %v; want the read stopped by Ctrl-C", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read did not end within 5 s of the Ctrl-C: the stalled request holds it")
	}
}

// TestInterruptAfterTheAnswerStillStops: a Ctrl-C taken as the answer comes in is not lost. The server
// answers 200, and the disarm then reports the Ctrl-C that the handler took meanwhile, though the
// context of the request was never cancelled. The read that succeeded reports the stop, as the
// timeline follow does.
func TestInterruptAfterTheAnswerStillStops(t *testing.T) {
	c, _, _ := writeClient(t, &fake{epoch: "ep_BWSYNTH-1"}, "ep_BWSYNTH-1")
	proxyAsTheProxyTestNamesIt(t)
	c.Arm = func() (context.Context, func() bool) {
		return context.Background(), func() bool { return true }
	}
	r, err := c.Get("/clusters")
	if !errors.As(err, new(*Interrupted)) || r != nil {
		t.Errorf("answer %v, error %v; want the read stopped by the Ctrl-C taken as it ended", r, err)
	}
}

// TestInterruptedReadRefreshesNothing: the answer to a read showed a new epoch, which makes Get read
// GET /server again. A Ctrl-C taken during the wait before a retry ends the command at once: the
// refresh sends no request, so it arms no new handler and the second Ctrl-C keeps its default action.
func TestInterruptedReadRefreshesNothing(t *testing.T) {
	var paths []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-2") // another epoch than the client knows
		problemAnswer(w, http.StatusServiceUnavailable, "dependency-unavailable", "")
	}))
	defer srv.Close()
	c, _, _ := writeClient(t, &fake{epoch: "ep_BWSYNTH-1"}, "ep_BWSYNTH-1")
	c.Context.Server = srv.URL
	a := newArmed(t)
	c.Arm = a.hook
	waitFor = func(ctx context.Context, d time.Duration) error {
		a.cancel() // the Ctrl-C comes while the client waits
		<-ctx.Done()
		return ctx.Err()
	}
	t.Cleanup(func() { waitFor = defaultWait })
	before := a.calls.Load()
	_, err := c.Get("/clusters")
	mu.Lock()
	defer mu.Unlock()
	switch {
	case !errors.As(err, new(*Interrupted)):
		t.Errorf("error %v; want the read stopped by Ctrl-C", err)
	case len(paths) != 1 || paths[0] != "/api/v1/clusters":
		t.Errorf("requests %v; want the one read and no GET /server after the Ctrl-C", paths)
	case a.calls.Load()-before != 1:
		t.Errorf("%d arms for the command; want 1", a.calls.Load()-before)
	}
}

// TestSendTakesNoCtrlC: a write arms nothing, and nor do the reads inside it (the principal, the
// epoch, the draft's ETag). The default action of Ctrl-C then stays what the rules for an
// unknown outcome (T9) depend on. A read after the write arms again.
func TestSendTakesNoCtrlC(t *testing.T) {
	a := newArmed(t)
	f := &fake{epoch: "ep_BWSYNTH-1", post: func(_ int, w http.ResponseWriter, _ *http.Request) {
		if a.open.Load() != 0 {
			t.Errorf("%d Ctrl-C handlers armed while the write was in flight; want none", a.open.Load())
		}
		created(w)
	}}
	c, _, _ := writeClient(t, f, "ep_BWSYNTH-1")
	c.Arm = a.hook
	if _, err := c.Send(clusterWrite()); err != nil {
		t.Fatal(err)
	}
	if n := a.calls.Load(); n != 0 {
		t.Errorf("Send armed the Ctrl-C handler %d times; want 0", n)
	}
	if _, err := c.Get("/clusters"); err != nil {
		t.Fatal(err)
	}
	if n := a.calls.Load(); n != 1 {
		t.Errorf("a read after the write armed %d times; want 1", n)
	}
}

// TestLiveEndsOnCancel: the probe of a new context's server ends when its context does.
func TestLiveEndsOnCancel(t *testing.T) {
	proxyAsTheProxyTestNamesIt(t) // this test may send the first request of the process
	arrived, hang := make(chan struct{}, 1), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-hang
	}))
	defer srv.Close()
	defer close(hang)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- LiveContext(ctx, &Context{Name: "lab", Server: srv.URL, Credential: "automation"}, nil)
	}()
	<-arrived
	cancel()
	select {
	case err := <-done:
		if !errors.As(err, new(*Interrupted)) {
			t.Errorf("error %v; want the probe stopped by Ctrl-C", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the probe did not end within 5 s of the cancellation")
	}
}
