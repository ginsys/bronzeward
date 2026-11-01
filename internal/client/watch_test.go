package client

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// opServer serves the operation op_BWSYNTH: its nth stream request (from 0, the last repeating) is
// answered by streams, its nth operation read shows states (the last repeating), and the JSON
// event list holds pages. A stream request whose epochs entry is set moves the installation to
// that epoch: from then on every answer carries it. An operation read takes slow. A pagesStatus
// refuses the JSON pages; from the gone-th operation read on (from 1), the operation is not found.
type opServer struct {
	pagesStatus int
	gone        int
	mu          sync.Mutex
	streams     []func(w http.ResponseWriter, r *http.Request)
	epochs      []string
	states      []string
	pages       []string
	epoch       string
	opened      []*http.Request
	reads       int
	slow        time.Duration
}

func (o *opServer) serve(w http.ResponseWriter, r *http.Request) {
	o.mu.Lock()
	if r.URL.Path == "/api/v1/operations/op_BWSYNTH/events" && r.Header.Get("Accept") == "text/event-stream" {
		n := len(o.opened)
		o.opened = append(o.opened, r)
		if n < len(o.epochs) && o.epochs[n] != "" {
			o.epoch = o.epochs[n]
		}
		h := o.streams[min(n, len(o.streams)-1)]
		o.answer(w)
		h(w, r)
		return
	}
	defer o.mu.Unlock()
	o.answerLocked(w)
	switch {
	case r.URL.Path == "/api/v1/operations/op_BWSYNTH/events" && o.pagesStatus != 0:
		problemAnswer(w, o.pagesStatus, "forbidden", "")
	case r.URL.Path == "/api/v1/operations/op_BWSYNTH/events":
		fmt.Fprintf(w, `{"items":[%s]}`, strings.Join(o.pages, ","))
	case r.URL.Path == "/api/v1/operations/op_BWSYNTH" && o.gone > 0 && o.reads+1 >= o.gone:
		o.reads++
		problemAnswer(w, http.StatusNotFound, "not-found", "")
	case r.URL.Path == "/api/v1/operations/op_BWSYNTH":
		time.Sleep(o.slow)
		state := o.states[min(o.reads, len(o.states)-1)]
		o.reads++
		fmt.Fprintf(w, `{"id":"op_BWSYNTH","kind":"publish","state":%q}`, state)
	default:
		problemAnswer(w, http.StatusNotFound, "not-found", "")
	}
}

// answer sets the epoch header and releases the lock; answerLocked only sets the header.
func (o *opServer) answer(w http.ResponseWriter) { o.answerLocked(w); o.mu.Unlock() }

func (o *opServer) answerLocked(w http.ResponseWriter) {
	if o.epoch != "" {
		w.Header().Set("Bronzeward-Epoch", o.epoch)
	}
}

// sse answers a stream of frames.
func sse(frames ...string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, strings.Join(frames, ""))
	}
}

func ev(id, typ string) string {
	return "id: " + id + "\nevent: " + typ + "\ndata: {\"type\":\"" + typ + "\"}\n\n"
}

func item(id, typ string) string {
	return `{"id":"` + id + `","at":"2026-11-01T06:00:00Z","type":"` + typ + `","data":{"type":"` + typ + `"}}`
}

// onShow, when set, runs on the watch's goroutine after each event shown.
var onShow func()

// watched runs a watch of op_BWSYNTH on o and returns the event IDs shown, with "clear" where
// the watch cleared them, the waits between attempts, the final state and the notices.
func watched(t *testing.T, o *opServer, epoch string, prep func(*Client)) ([]string, []time.Duration, string, string, error) {
	t.Helper()
	f := &fake{epoch: epoch, post: func(_ int, w http.ResponseWriter, r *http.Request) { o.serve(w, r) }}
	c, notices, _ := writeClient(t, f, epoch)
	if prep != nil {
		prep(c)
	}
	var shown []string
	var waits []time.Duration
	op, err := c.Watch("op_BWSYNTH", Watch{
		Show: func(e Event) {
			shown = append(shown, e.ID)
			if onShow != nil {
				onShow()
			}
		},
		Clear: func() { shown = append(shown, "clear") },
		sleep: func(d time.Duration) { waits = append(waits, d) },
	})
	state := ""
	if op != nil {
		state = op.State
	}
	return shown, waits, state, notices.String(), err
}

// TestWatch covers each item of decision T16 of #256 (client-contracts.md §8.6 items 1-3 and 5).
func TestWatch(t *testing.T) {
	const e1, e2 = "ep_BWSYNTH1", "ep_BWSYNTH2"
	type handler = func(http.ResponseWriter, *http.Request)
	failed := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }
	for _, c := range []struct {
		name       string
		o          *opServer
		shown      []string
		waits      []time.Duration
		lastEvents []string // the Last-Event-ID of each stream opened
		notice     string
	}{{
		name: "a stream to its end, its keep-alive ignored",
		o: &opServer{streams: []handler{sse(ev(e1+":1", "queued"), ": keep-alive\n\n", ev(e1+":2", "succeeded"))},
			states: []string{"succeeded"}, pages: []string{item(e1+":1", "queued"), item(e1+":2", "succeeded")}},
		shown: []string{e1 + ":1", e1 + ":2"}, lastEvents: []string{""},
	}, {
		name: "a stream cut after the terminal state, before its last events (EVT-04): the pages are drained",
		o: &opServer{streams: []handler{sse(ev(e1+":1", "queued"))}, states: []string{"succeeded"},
			pages: []string{item(e1+":1", "queued"), item(e1+":2", "started"), item(e1+":3", "succeeded")}},
		shown: []string{e1 + ":1", e1 + ":2", e1 + ":3"}, lastEvents: []string{""},
	}, {
		name: "a stream that ends before the operation does is resumed with Last-Event-ID, unresolved included",
		o: &opServer{streams: []handler{sse(ev(e1+":1", "queued")), sse(ev(e1+":2", "succeeded"))},
			states: []string{"unresolved", "succeeded"}, pages: []string{item(e1+":1", "queued"), item(e1+":2", "succeeded")}},
		shown: []string{e1 + ":1", e1 + ":2"}, lastEvents: []string{"", e1 + ":1"},
	}, {
		name: "a reset clears what was shown and shows the replay",
		o: &opServer{streams: []handler{sse(ev(e1+":1", "queued")), sse("event: reset\ndata: {}\n\n", ev(e2+":1", "queued"), ev(e2+":2", "succeeded"))},
			epochs: []string{e1, e2}, states: []string{"running", "succeeded"}, pages: []string{item(e2+":1", "queued"), item(e2+":2", "succeeded")}},
		shown: []string{e1 + ":1", "clear", e2 + ":1", e2 + ":2"}, lastEvents: []string{"", e1 + ":1"},
		notice: "restored or entered recovery mode; the operation's events are shown again from the start",
	}, {
		name: "an event ID of another epoch ends the stream, which reopens without Last-Event-ID (EVT-05's client half)",
		o: &opServer{streams: []handler{sse(ev(e1+":1", "queued"), ev(e2+":2", "started")), sse(ev(e2+":1", "queued"), ev(e2+":2", "succeeded"))},
			epochs: []string{e1, e2}, states: []string{"running", "succeeded"}, pages: []string{item(e2+":1", "queued"), item(e2+":2", "succeeded")}},
		shown: []string{e1 + ":1", "clear", e2 + ":1", e2 + ":2"}, lastEvents: []string{"", ""},
		notice: "entered recovery mode or was restored while the events were read",
	}, {
		name: "JSON pages where no stream opens, read again after 1 s",
		o: &opServer{streams: []handler{func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"items":[]}`) }},
			states: []string{"running", "running", "succeeded"}, pages: []string{item(e1+":1", "queued")}},
		shown: []string{e1 + ":1"}, waits: []time.Duration{time.Second, time.Second}, lastEvents: []string{""},
	}, {
		name: "failures back off from 1 s to 30 s",
		o: &opServer{streams: []handler{failed, failed, failed, failed, failed, failed, failed, sse(ev(e1+":1", "succeeded"))},
			states: []string{"running", "running", "running", "running", "running", "running", "running", "succeeded"},
			pages:  []string{item(e1+":1", "succeeded")}},
		shown: []string{e1 + ":1"}, lastEvents: []string{"", "", "", "", "", "", "", ""},
		waits: []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second},
	}} {
		t.Run(c.name, func(t *testing.T) {
			shown, waits, state, notices, err := watched(t, c.o, e1, nil)
			var last []string
			for _, r := range c.o.opened {
				last = append(last, r.Header.Get("Last-Event-ID"))
			}
			switch {
			case err != nil || state != "succeeded":
				t.Errorf("watch: %v, state %q", err, state)
			case !slices.Equal(shown, c.shown):
				t.Errorf("shown %q; want %q", shown, c.shown)
			case !slices.Equal(waits, c.waits):
				t.Errorf("waits %v; want %v", waits, c.waits)
			case !slices.Equal(last, c.lastEvents):
				t.Errorf("Last-Event-ID per stream %q; want %q", last, c.lastEvents)
			case !strings.Contains(notices, c.notice):
				t.Errorf("notices %q; want %q", notices, c.notice)
			}
		})
	}
	t.Run("a drain that crosses into another epoch reads the operation again", func(t *testing.T) {
		// The operation read says failed in e1; the pages are of a restored installation, e2, where
		// it went on and succeeded.
		o := &opServer{streams: []handler{sse(ev(e1+":1", "queued")), sse(ev(e2+":3", "succeeded"))}, epochs: []string{e1, e2},
			states: []string{"failed", "succeeded"}, pages: []string{item(e2+":1", "queued"), item(e2+":2", "started"), item(e2+":3", "succeeded")}}
		shown, _, state, notices, err := watched(t, o, e1, nil)
		if want := []string{e1 + ":1", "clear", e2 + ":1", e2 + ":2", e2 + ":3"}; err != nil || state != "succeeded" || !slices.Equal(shown, want) ||
			!strings.Contains(notices, "restored or entered recovery mode") || o.reads != 2 {
			t.Errorf("watch: %v, state %q, shown %q, %d operation reads, notices %q; want succeeded, %q, 2 reads", err, state, shown, o.reads, notices, want)
		}
	})
	t.Run("a drain of another epoch than the operation read's, nothing shown before, reads the operation again", func(t *testing.T) {
		o := &opServer{streams: []handler{sse(), sse(ev(e2+":3", "succeeded"))}, epochs: []string{e1, e2},
			states: []string{"failed", "succeeded"}, pages: []string{item(e2+":1", "queued"), item(e2+":2", "started"), item(e2+":3", "succeeded")}}
		shown, _, state, _, err := watched(t, o, e1, nil)
		if want := []string{e2 + ":1", e2 + ":2", e2 + ":3"}; err != nil || state != "succeeded" || !slices.Equal(shown, want) || o.reads != 2 {
			t.Errorf("watch: %v, state %q, shown %q, %d operation reads; want succeeded, %q, 2 reads", err, state, shown, o.reads, want)
		}
	})
	t.Run("a refused drain keeps a terminal state read again in the same epoch, with the refusal", func(t *testing.T) {
		o := &opServer{streams: []handler{sse(ev(e1+":1", "queued"))}, epochs: []string{e1}, states: []string{"failed"}, pagesStatus: http.StatusForbidden}
		if _, _, state, _, err := watched(t, o, e1, nil); state != "failed" || err == nil || !strings.Contains(err.Error(), "403") || o.reads != 2 {
			t.Errorf("watch: %v, state %q, %d operation reads; want failed with the 403 after 2 reads", err, state, o.reads)
		}
	})
	t.Run("a refused drain of an operation gone on the read again ends with no state", func(t *testing.T) {
		o := &opServer{streams: []handler{sse(ev(e1+":1", "queued"))}, epochs: []string{e1}, states: []string{"failed"},
			pagesStatus: http.StatusNotFound, gone: 2}
		if _, _, state, _, err := watched(t, o, e1, nil); state != "" || err == nil || !strings.Contains(err.Error(), "404") {
			t.Errorf("watch: %v, state %q; want no state and the 404", err, state)
		}
	})
	t.Run("a slow operation read does not skip the backoff", func(t *testing.T) {
		o := &opServer{streams: []handler{sse(), sse(ev(e1+":1", "succeeded"))}, states: []string{"running", "succeeded"},
			pages: []string{item(e1+":1", "succeeded")}, slow: 1100 * time.Millisecond}
		if _, waits, state, _, err := watched(t, o, e1, nil); err != nil || state != "succeeded" || !slices.Equal(waits, []time.Duration{time.Second}) {
			t.Errorf("watch: %v, state %q, waits %v; want succeeded after one 1 s wait", err, state, waits)
		}
	})
	t.Run("a refusal whose body stalls ends after the idle limit", func(t *testing.T) {
		defer func(d time.Duration) { streamIdle = d }(streamIdle)
		streamIdle = 200 * time.Millisecond
		stalled := func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"type":`)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
		o := &opServer{streams: []handler{stalled, sse(ev(e1+":1", "succeeded"))}, states: []string{"running", "succeeded"},
			pages: []string{item(e1+":1", "succeeded")}}
		shown, waits, state, _, err := watched(t, o, e1, nil)
		if err != nil || state != "succeeded" || !slices.Equal(shown, []string{e1 + ":1"}) || !slices.Equal(waits, []time.Duration{time.Second}) {
			t.Errorf("watch: %v, state %q, shown %q, waits %v; want succeeded after one 1 s wait", err, state, shown, waits)
		}
	})
	t.Run("a refused stream ends the watch with the refusal", func(t *testing.T) {
		o := &opServer{streams: []handler{func(w http.ResponseWriter, _ *http.Request) {
			problemAnswer(w, http.StatusForbidden, "forbidden", "")
		}}, states: []string{"running"}}
		if _, _, _, _, err := watched(t, o, e1, nil); err == nil || !strings.Contains(err.Error(), "403") {
			t.Errorf("watch: %v; want the 403 refusal", err)
		}
	})
}

// TestWatchRenewal checks that a wait renews in process (§9.5 item 4): a sign-in about to expire
// is renewed before the stream is opened again, and a 401 on a stream is renewed once, the stream
// then opened with the renewed token.
func TestWatchRenewal(t *testing.T) {
	jwt := func(exp time.Time) string {
		return "e30." + base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, `{"iss":"https://idp.example.invalid","sub":"h-pub","exp":%d}`, exp.Unix())) + ".BWSYNTH-sig"
	}
	renewed := jwt(time.Now().Add(2 * time.Hour))
	for _, c := range []struct {
		name   string
		token  string
		first  func(http.ResponseWriter, *http.Request)
		states []string
		ages   bool // the token comes within renewBefore of its expiry once the first event is shown
	}{
		{"about to expire during the wait", jwt(time.Now().Add(time.Hour)), sse(ev("ep_BWSYNTH1:1", "queued")), []string{"running", "succeeded"}, true},
		{"refused 401", jwt(time.Now().Add(time.Hour)) + "x", func(w http.ResponseWriter, _ *http.Request) {
			problemAnswer(w, http.StatusUnauthorized, "unauthenticated", "")
		}, []string{"succeeded"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.ages {
				onShow = func() { renewBefore = 90 * time.Minute }
				t.Cleanup(func() { onShow, renewBefore = nil, 60*time.Second })
			}
			o := &opServer{streams: []func(http.ResponseWriter, *http.Request){c.first, sse(ev("ep_BWSYNTH1:2", "succeeded"))},
				states: c.states, pages: []string{item("ep_BWSYNTH1:2", "succeeded")}}
			_, _, state, notices, err := watched(t, o, "ep_BWSYNTH1", func(cl *Client) {
				tokens := filepath.Join(t.TempDir(), "tokens")
				// The renewal finds the pair another process stored meanwhile, so it calls no provider.
				if err := WriteTokens(tokens, &Tokens{AccessToken: renewed, RefreshToken: "BWSYNTH-rt2"}); err != nil {
					t.Fatal(err)
				}
				cl.Context.Credential = "oidc"
				cl.Context.OIDC = &OIDC{Issuer: "https://idp.example.invalid", TokenFile: tokens}
				cl.signIn = &signIn{store: openStore(cl.Context, true), tokens: &Tokens{AccessToken: c.token, RefreshToken: "BWSYNTH-rt"}}
				cl.token = c.token
			})
			switch {
			case err != nil || state != "succeeded":
				t.Errorf("watch: %v, state %q; notices %q", err, state, notices)
			case len(o.opened) != 2:
				t.Errorf("%d streams opened; want 2", len(o.opened))
			case o.opened[0].Header.Get("Authorization") != "Bearer "+c.token || o.opened[1].Header.Get("Authorization") != "Bearer "+renewed:
				t.Errorf("the streams were opened with %q and %q; want the first token, then the renewed one",
					o.opened[0].Header.Get("Authorization"), o.opened[1].Header.Get("Authorization"))
			case strings.Contains(notices, "BWSYNTH-rt"):
				t.Errorf("notices show a refresh token: %q", notices)
			}
		})
	}
}
