package client

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"
)

// TestWatchOutage covers the operation read of a watch whose stream was cut by a server that then
// stays out of reach for longer than a plain read waits (client-contracts.md §8.2, §8.6 item 2;
// #354's T9, where the server stops for 60 s): the read is repeated after 1 s doubling to 30 s with no
// cap of five tries, and the watch then resumes with Last-Event-ID.
func TestWatchOutage(t *testing.T) {
	const e1 = "ep_BWSYNTH1"
	type handler = func(http.ResponseWriter, *http.Request)
	// reads records the delays the operation read waits, in place of the waiting.
	var reads []time.Duration
	waitFor = func(ctx context.Context, d time.Duration) error { reads = append(reads, d); return ctx.Err() }
	t.Cleanup(func() { waitFor = defaultWait })
	total := func(ds []time.Duration) (sum time.Duration) {
		for _, d := range ds {
			sum += d
		}
		return sum
	}
	for _, kind := range []string{"502", "503", "drop"} {
		t.Run("an outage of seven reads ("+kind+") is waited out, and the stream resumes with Last-Event-ID", func(t *testing.T) {
			reads = nil
			// The first stream is cut after one event; the next 7 operation reads fail: 1+2+4+8+16+30+30 = 91 s.
			o := &opServer{streams: []handler{sse(ev(e1+":1", "queued")), sse(ev(e1+":2", "succeeded"))},
				states: []string{"running", "succeeded"}, outage: 7, outageKind: kind,
				pages: []string{item(e1+":1", "queued"), item(e1+":2", "succeeded")}}
			shown, _, state, _, err := watched(t, o, e1, nil)
			var last []string
			for _, r := range o.opened {
				last = append(last, r.Header.Get("Last-Event-ID"))
			}
			switch {
			case err != nil || state != "succeeded":
				t.Fatalf("watch: state %q, error %v", state, err)
			case !slices.Equal(shown, []string{e1 + ":1", e1 + ":2"}):
				t.Errorf("shown %q", shown)
			case !slices.Equal(last, []string{"", e1 + ":1"}):
				t.Errorf("Last-Event-ID per stream %q; want the second to resume after %s:1", last, e1)
			case kind == "drop" && total(reads) <= 15*time.Second:
				// net/http sends a GET again once on a connection that closed before an answer, and the
				// server counts that send too, so the reads the client saw fail are 6 or 7: more than the
				// 1+2+4+8 = 15 s of five tries is what this proves.
				t.Errorf("the read waited %v in all; want more than the 15s of five tries", total(reads))
			case kind != "drop" && total(reads) != 91*time.Second:
				t.Errorf("the read waited %v in all (%v); want 91s", total(reads), reads)
			}
		})
	}
	// ended makes Until true once the operation read has waited d in all (the recorded waits).
	ended := func(d time.Duration) {
		until = func() (bool, error) { return total(reads) >= d, nil }
		t.Cleanup(func() { until = nil })
	}
	t.Run("the watch ending in an outage returns the operation as the last read showed it", func(t *testing.T) {
		reads = nil
		ended(3 * time.Second)
		o := &opServer{streams: []handler{sse(ev(e1+":1", "queued"))}, states: []string{"running"},
			outage: 99, outageAfter: 1, outageKind: "502"}
		_, _, state, _, err := watched(t, o, e1, nil)
		// 1 s and 2 s, in steps of 250 ms: the watch ends at the step that reaches 3 s.
		if err != nil || state != "running" || total(reads) != 3*time.Second {
			t.Errorf("watch: state %q, error %v after %v of waits; want running, no error, 3s", state, err, total(reads))
		}
	})
	t.Run("the watch ending inside a wait of 30 s ends it within one step of 250 ms", func(t *testing.T) {
		reads = nil
		ended(31*time.Second + 400*time.Millisecond) // the waits 1+2+4+8+16 = 31 s, then 30 s of the next
		o := &opServer{streams: []handler{sse(ev(e1+":1", "queued"))}, states: []string{"running"},
			outage: 99, outageAfter: 1, outageKind: "502"}
		_, _, state, _, err := watched(t, o, e1, nil)
		if want := 31*time.Second + 500*time.Millisecond; err != nil || state != "running" || total(reads) != want {
			t.Errorf("watch: state %q, error %v after %v of waits; want running, no error, %v (the first step past 31.4s)", state, err, total(reads), want)
		}
	})
	t.Run("the watch ending in an outage with no read of the operation reports that it ended", func(t *testing.T) {
		reads = nil
		ended(2 * time.Second)
		o := &opServer{streams: []handler{sse(ev(e1+":1", "queued"))}, states: []string{"running"}, outage: 99, outageKind: "503"}
		_, _, state, _, err := watched(t, o, e1, nil)
		if !errors.Is(err, ErrWatchEnded) || state != "" {
			t.Errorf("watch: state %q, error %v; want ErrWatchEnded and no state", state, err)
		}
	})
	t.Run("an Until that fails ends the outage with its own error", func(t *testing.T) {
		reads = nil
		boom := errors.New("BWSYNTH until failed")
		until = func() (bool, error) { return total(reads) >= time.Second, boom }
		t.Cleanup(func() { until = nil })
		o := &opServer{streams: []handler{sse(ev(e1+":1", "queued"))}, states: []string{"running"},
			outage: 99, outageAfter: 1, outageKind: "502"}
		_, _, _, _, err := watched(t, o, e1, nil)
		if !errors.Is(err, boom) {
			t.Errorf("watch: %v; want Until's error", err)
		}
	})
	t.Run("a read refused outside the retryable set is not repeated", func(t *testing.T) {
		reads = nil
		o := &opServer{streams: []handler{sse(ev(e1+":1", "queued"))}, states: []string{"running"}, gone: 1}
		_, _, _, _, err := watched(t, o, e1, nil)
		if err == nil || errors.Is(err, ErrWatchEnded) || len(reads) != 0 {
			t.Errorf("watch: %v after %d waits; want the 404 and no wait", err, len(reads))
		}
	})
}
