package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// sumWaits is the time the recorded waits add up to.
func sumWaits(ds []time.Duration) (sum time.Duration) {
	for _, d := range ds {
		sum += d
	}
	return sum
}

// pauseWatch runs a watch of op_BWSYNTH whose pause between two attempts is the real one, with the
// waits recorded in place of slept: Until answers true, with err, once the recorded waits reach endAfter.
func pauseWatch(t *testing.T, o *opServer, epoch string, endAfter time.Duration, err error) (shown []string, waits []time.Duration, state string, werr error) {
	t.Helper()
	// A watch that never ends blocks at its hundred and first wait, so the case fails after 10 s instead of hanging the package.
	waitFor = func(ctx context.Context, d time.Duration) error {
		if len(waits) > 100 {
			<-make(chan struct{})
		}
		waits = append(waits, d)
		return ctx.Err()
	}
	t.Cleanup(func() { waitFor = defaultWait })
	f := &fake{epoch: epoch, post: func(_ int, w http.ResponseWriter, r *http.Request) { o.serve(w, r) }}
	c, _, _ := writeClient(t, f, epoch)
	done := make(chan struct{})
	go func() {
		defer close(done)
		op, _, e := c.Watch("op_BWSYNTH", Watch{
			Show:  func(e Event) { shown = append(shown, e.ID) },
			Clear: func() { shown = append(shown, "clear") },
			Until: func() (bool, error) {
				if sumWaits(waits) >= endAfter {
					return true, err
				}
				return false, nil
			},
		})
		werr = e
		if op != nil {
			state = op.State
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("the watch did not end after %d waits", len(waits))
	}
	return shown, waits, state, werr
}

// TestWatchPauseEnds checks that the pause between two watch attempts ends within 250 ms of the watch
// ending, as every other wait of a watch does (client-contracts.md section 8.6 item 2): in steps of 250 ms
// that ask Until, so a watch that ends through --timeout does not wait out a backoff of up to 30 s.
func TestWatchPauseEnds(t *testing.T) {
	const e1, e2 = "ep_BWSYNTH1", "ep_BWSYNTH2"
	step := []time.Duration{250 * time.Millisecond, 250 * time.Millisecond}
	// Pages where no stream opens: each attempt that shows nothing new waits 1 s before the next.
	pages := func() *opServer {
		return &opServer{streams: []func(http.ResponseWriter, *http.Request){func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"items":[]}`) }},
			states: []string{"running"}, pages: []string{item(e1+":1", "queued")}}
	}
	t.Run("the watch ending in the pause of an attempt ends it with the operation as last read", func(t *testing.T) {
		_, waits, state, err := pauseWatch(t, pages(), e1, 500*time.Millisecond, nil)
		if err != nil || state != "running" || !slices.Equal(waits, step) {
			t.Errorf("watch: state %q, error %v, waits %v; want running, no error, %v", state, err, waits, step)
		}
	})
	t.Run("an error of Until in the pause stands", func(t *testing.T) {
		boom := errors.New("BWSYNTH until failed")
		_, waits, state, err := pauseWatch(t, pages(), e1, 500*time.Millisecond, boom)
		if !errors.Is(err, boom) || state != "" || !slices.Equal(waits, step) {
			t.Errorf("watch: state %q, error %v, waits %v; want no state, Until's error, %v", state, err, waits, step)
		}
	})
	t.Run("the watch ending in the pause after a terminal read of a restored installation reports that no read stands", func(t *testing.T) {
		// The read is terminal in e1 and the pages answer e2: the read is void, and the watch waits before it reads again.
		o := &opServer{streams: []func(http.ResponseWriter, *http.Request){sse(ev(e1+":1", "queued"))}, epochs: []string{e1},
			states: []string{"succeeded"}, pages: []string{item(e1+":1", "queued")}, restoreAtPages: e2}
		_, waits, state, err := pauseWatch(t, o, e1, 500*time.Millisecond, nil)
		if state != "" || !errors.Is(err, ErrWatchEnded) || !errors.Is(err, ErrWatchRestored) || !slices.Equal(waits, step) {
			t.Errorf("watch: state %q, error %v, waits %v; want no state, an error that is ErrWatchEnded and ErrWatchRestored, %v", state, err, waits, step)
		}
	})
	t.Run("a pause that nothing ends is waited out whole, in steps", func(t *testing.T) {
		// Until answers true only once 2 s are waited: two pauses of 1 s, each 4 steps, the second ended by Until at its last step.
		_, waits, state, err := pauseWatch(t, pages(), e1, 2*time.Second, nil)
		want := slices.Repeat([]time.Duration{250 * time.Millisecond}, 8)
		if err != nil || state != "running" || !slices.Equal(waits, want) {
			t.Errorf("watch: state %q, error %v, waits %v; want running, no error, %v", state, err, waits, want)
		}
	})
}

// TestWatchEmptyListOfTheOperationsEpoch checks that a terminal operation whose events list is empty at
// the operation's epoch clears the events kept from another epoch and ends, as the web does, instead of
// being read again until an event replaces them (client-contracts.md section 8.5).
func TestWatchEmptyListOfTheOperationsEpoch(t *testing.T) {
	const e1, e2 = "ep_BWSYNTH1", "ep_BWSYNTH2"
	type handler = func(http.ResponseWriter, *http.Request)
	// A stream under e1 shows one event and ends. The operation is terminal under e2, whose events list is empty.
	// A second stream, which a watch that does not end would open, would show an event of e2.
	o := &opServer{streams: []handler{sse(ev(e1+":1", "queued")), sse(ev(e2+":1", "succeeded"))}, epochs: []string{e1, e2},
		opEpoch: e2, restoreAtPages: e2, states: []string{"succeeded"}}
	shown, waits, state, notices, err := watched(t, o, e1, nil)
	if err != nil || state != "succeeded" {
		t.Fatalf("watch: state %q, error %v", state, err)
	}
	if want := []string{e1 + ":1", "clear"}; !slices.Equal(shown, want) {
		t.Errorf("shown %q; want %q: the kept event cleared, none shown again", shown, want)
	}
	if len(o.opened) != 1 || len(waits) != 0 {
		t.Errorf("%d streams opened and waits %v; want one stream and no wait", len(o.opened), waits)
	}
	if !strings.Contains(notices, "the operation's events are shown again from the start") {
		t.Errorf("notices %q; want the notice that the events were cleared", notices)
	}
}

// TestWatchTrustRefusalIsRepeated checks that a refused server certificate does not end a watch: the
// watch's reads that run until the watch ends repeat it, with the refusal as the notice of each try, and
// end with it when the watch ends. A read that does not run until the watch ends refuses at once, and the
// repeated read never reaches the server (client-contracts.md section 8.6 item 2, section 6 item 4).
func TestWatchTrustRefusalIsRepeated(t *testing.T) {
	var hits atomic.Int32
	untrusted := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(untrusted.Close)
	var waits []time.Duration
	waitFor = func(ctx context.Context, d time.Duration) error { waits = append(waits, d); return ctx.Err() }
	t.Cleanup(func() { waitFor = defaultWait })
	f := &fake{epoch: "ep_BWSYNTH1", post: func(_ int, w http.ResponseWriter, _ *http.Request) { created(w) }}
	c, notices, _ := writeClient(t, f, "ep_BWSYNTH1")
	c.Context.Server = untrusted.URL

	t.Run("an ordinary read is refused at once", func(t *testing.T) {
		waits = nil
		_, err := c.Get("/operations/op_BWSYNTH")
		if !errors.As(err, new(*trust)) || len(waits) != 0 {
			t.Errorf("read: %v after waits %v; want the trust refusal and no wait", err, waits)
		}
	})
	t.Run("a watch repeats it until the watch ends, and ends with it", func(t *testing.T) {
		waits = nil
		notices.Reset()
		_, _, err := c.Watch("op_BWSYNTH", Watch{
			Show:  func(Event) {},
			Until: func() (bool, error) { return sumWaits(waits) >= 3*time.Second, nil },
		})
		if errors.Is(err, ErrWatchEnded) || !errors.As(err, new(*trust)) || !strings.Contains(err.Error(), "is not trusted by the system trust store") {
			t.Errorf("watch: %v; want the trust refusal itself, which is not ErrWatchEnded (the command would then show the first read)", err)
		}
		if sumWaits(waits) < 3*time.Second || strings.Count(notices.String(), "is not trusted by the system trust store") < 3 {
			t.Errorf("waits %v, notices %q; want 3 s of waits and a notice naming the refusal at each try", waits, notices)
		}
		if n := hits.Load(); n != 0 {
			t.Errorf("the untrusted server answered %d requests; want none, since the handshake is refused", n)
		}
	})
}

// TestWatchEndedInTrustRefusalAfterARead checks that a watch that read the operation, and then met a server whose
// certificate is refused until the watch ended, returns the refusal and not the earlier read (§8.6 item 2).
func TestWatchEndedInTrustRefusalAfterARead(t *testing.T) {
	const e1 = "ep_BWSYNTH1"
	untrusted := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(untrusted.Close)
	o := &opServer{streams: []func(http.ResponseWriter, *http.Request){sse()}, epochs: []string{e1}, states: []string{"running"}}
	f := &fake{epoch: e1, post: func(_ int, w http.ResponseWriter, r *http.Request) { o.serve(w, r) }}
	c, _, _ := writeClient(t, f, e1)
	var waits []time.Duration
	waitFor = func(ctx context.Context, d time.Duration) error {
		if waits = append(waits, d); len(waits) == 1 {
			c.Context.Server = untrusted.URL // the server restarts behind a certificate the client refuses
		}
		return ctx.Err()
	}
	t.Cleanup(func() { waitFor = defaultWait })
	op, _, err := c.Watch("op_BWSYNTH", Watch{Show: func(Event) {}, Until: func() (bool, error) { return sumWaits(waits) >= 3*time.Second, nil }})
	if op != nil || errors.Is(err, ErrWatchEnded) || !errors.As(err, new(*trust)) {
		t.Errorf("watch: %v, %v; want no operation and the trust refusal, which is not ErrWatchEnded", op, err)
	}
}

// TestWatchEndedInTrustRefusalAfterAFailedDrain checks the same for a terminal read whose drain failed: the read
// of the operation after the failed drain meets a refused certificate until the watch ends, and the refusal is
// returned with the failed drain, not the terminal read before them (§8.6 item 2).
func TestWatchEndedInTrustRefusalAfterAFailedDrain(t *testing.T) {
	const e1 = "ep_BWSYNTH1"
	untrusted := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(untrusted.Close)
	o := &opServer{streams: []func(http.ResponseWriter, *http.Request){sse(ev(e1+":1", "queued"))}, epochs: []string{e1}, states: []string{"failed"}, pagesStatus: http.StatusForbidden}
	var c *Client
	f := &fake{epoch: e1, post: func(_ int, w http.ResponseWriter, r *http.Request) {
		o.serve(w, r)
		if r.Header.Get("Accept") != "text/event-stream" && r.URL.Path == "/api/v1/operations/op_BWSYNTH/events" {
			c.Context.Server = untrusted.URL // the drain is refused, and the server then presents a refused certificate
		}
	}}
	c, _, _ = writeClient(t, f, e1)
	var waits []time.Duration
	waitFor = func(ctx context.Context, d time.Duration) error { waits = append(waits, d); return ctx.Err() }
	t.Cleanup(func() { waitFor = defaultWait })
	op, _, err := c.Watch("op_BWSYNTH", Watch{Show: func(Event) {}, Until: func() (bool, error) { return sumWaits(waits) >= 3*time.Second, nil }})
	if op != nil || errors.Is(err, ErrWatchEnded) || !errors.As(err, new(*trust)) || !strings.Contains(err.Error(), "403") {
		t.Errorf("watch: %v, %v; want no operation and the refused drain with the trust refusal, not ErrWatchEnded", op, err)
	}
}
