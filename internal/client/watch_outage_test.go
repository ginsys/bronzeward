package client

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
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
	// ended makes Until true once the operation read has waited d in all (the recorded waits). The cleanup
	// belongs to the subtest that calls it, so Until does not carry into the subtests after it.
	ended := func(t *testing.T, d time.Duration) {
		t.Helper()
		until = func() (bool, error) { return total(reads) >= d, nil }
		t.Cleanup(func() { until = nil })
	}
	// failsAfter makes Until fail with err only once the operation read has waited d in all: its first
	// answers are (false, nil), so the error first arises during a retry wait and not before the read.
	failsAfter := func(t *testing.T, d time.Duration, err error) {
		t.Helper()
		until = func() (bool, error) {
			if total(reads) >= d {
				return true, err
			}
			return false, nil
		}
		t.Cleanup(func() { until = nil })
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
	t.Run("the read after a failed drain of a terminal state is waited out too", func(t *testing.T) {
		reads = nil
		// The terminal read answers, the drain is refused, and the server is then out of reach for
		// 7 reads: the watch is not left to the five tries of a plain read.
		o := &opServer{streams: []handler{sse(ev(e1+":1", "queued"))}, epochs: []string{e1}, states: []string{"failed"},
			pagesStatus: http.StatusForbidden, outage: 7, outageAfter: 1, outageKind: "502"}
		_, _, state, _, err := watched(t, o, e1, nil)
		if state != "failed" || err == nil || !strings.Contains(err.Error(), "403") || total(reads) != 91*time.Second {
			t.Errorf("watch: state %q, error %v after %v of waits; want failed with the 403 after 91s", state, err, total(reads))
		}
	})
	t.Run("an Until that fails during the read after a refused drain ends it with its own error", func(t *testing.T) {
		reads = nil
		boom := errors.New("BWSYNTH until failed")
		failsAfter(t, time.Second, boom)
		o := &opServer{streams: []handler{sse(ev(e1+":1", "queued"))}, epochs: []string{e1}, states: []string{"failed"},
			pagesStatus: http.StatusForbidden, outage: 99, outageAfter: 1, outageKind: "502"}
		_, _, _, _, err := watched(t, o, e1, nil)
		if !errors.Is(err, boom) || !strings.Contains(err.Error(), "403") {
			t.Errorf("watch: %v; want Until's error joined with the refused drain's 403", err)
		}
		// Until fails at the first step of 250 ms that reaches 1 s: the error arose during the first wait.
		if total(reads) != time.Second {
			t.Errorf("the read waited %v in all; want 1s, the wait in which Until failed", total(reads))
		}
	})
	t.Run("the watch ending in an outage returns the operation as the last read showed it", func(t *testing.T) {
		reads = nil
		ended(t, 3*time.Second)
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
		ended(t, 31*time.Second+400*time.Millisecond) // the waits 1+2+4+8+16 = 31 s, then 30 s of the next
		o := &opServer{streams: []handler{sse(ev(e1+":1", "queued"))}, states: []string{"running"},
			outage: 99, outageAfter: 1, outageKind: "502"}
		_, _, state, _, err := watched(t, o, e1, nil)
		if want := 31*time.Second + 500*time.Millisecond; err != nil || state != "running" || total(reads) != want {
			t.Errorf("watch: state %q, error %v after %v of waits; want running, no error, %v (the first step past 31.4s)", state, err, total(reads), want)
		}
	})
	t.Run("the watch ending in an outage with no read of the operation reports that it ended", func(t *testing.T) {
		reads = nil
		ended(t, 2*time.Second)
		o := &opServer{streams: []handler{sse(ev(e1+":1", "queued"))}, states: []string{"running"}, outage: 99, outageKind: "503"}
		_, _, state, _, err := watched(t, o, e1, nil)
		if !errors.Is(err, ErrWatchEnded) || state != "" {
			t.Errorf("watch: state %q, error %v; want ErrWatchEnded and no state", state, err)
		}
	})
	t.Run("an Until that fails ends the outage with its own error", func(t *testing.T) {
		reads = nil
		boom := errors.New("BWSYNTH until failed")
		failsAfter(t, time.Second, boom)
		o := &opServer{streams: []handler{sse(ev(e1+":1", "queued"))}, states: []string{"running"},
			outage: 99, outageAfter: 1, outageKind: "502"}
		_, _, _, _, err := watched(t, o, e1, nil)
		if !errors.Is(err, boom) {
			t.Errorf("watch: %v; want Until's error", err)
		}
		// Until fails at the first step of 250 ms that reaches 1 s: the error arose during the first wait, not
		// after the outage of 99 reads ended.
		if total(reads) != time.Second {
			t.Errorf("the read waited %v in all; want 1s, the wait in which Until failed", total(reads))
		}
	})
	t.Run("a reread after a refused drain that shows a running operation is the last read when an outage ends the watch", func(t *testing.T) {
		reads = nil
		ended(t, 2*time.Second)
		// The first read is terminal and its drain is refused. The reread shows the operation running, so the
		// watch goes on. The next read is out of reach until the watch ends: the terminal read is stale.
		o := &opServer{streams: []handler{sse(ev(e1+":1", "queued"))}, epochs: []string{e1}, states: []string{"failed", "running"},
			pagesStatus: http.StatusForbidden, outage: 99, outageAfter: 2, outageKind: "502"}
		_, _, state, _, err := watched(t, o, e1, nil)
		if err != nil || state != "running" {
			t.Errorf("watch: state %q, error %v; want running (the reread), no error", state, err)
		}
	})
	t.Run("a terminal read stays no result when the drain shows another epoch and an outage ends the watch", func(t *testing.T) {
		reads = nil
		ended(t, 2*time.Second)
		// The first read is terminal in epoch 1. The installation is restored at the drain, whose pages answer epoch 2,
		// so the watch goes on. The next read is out of reach until the watch ends: the terminal read is of the
		// installation that was restored, and it is not the result.
		const e2 = "ep_BWSYNTH2"
		o := &opServer{streams: []handler{sse(ev(e1+":1", "queued"))}, epochs: []string{e1}, states: []string{"succeeded"},
			pages: []string{item(e1+":1", "queued")}, restoreAtPages: e2, outage: 99, outageAfter: 1, outageKind: "502"}
		_, _, state, _, err := watched(t, o, e1, nil)
		if state != "" || !errors.Is(err, ErrWatchEnded) || !errors.Is(err, ErrWatchRestored) {
			t.Errorf("watch: state %q, error %v; want no state and an error that is ErrWatchEnded and ErrWatchRestored", state, err)
		}
	})
	t.Run("a terminal reread of another epoch after a refused drain stays no result when an outage ends the watch", func(t *testing.T) {
		reads = nil
		ended(t, 2*time.Second)
		// The first read is terminal in epoch 1 and its drain is refused, and that request moves the installation to
		// epoch 2. The reread is terminal in epoch 2, which is not the epoch of the first read, so the watch goes on.
		// The next read is out of reach until the watch ends: no read settled the state.
		const e2 = "ep_BWSYNTH2"
		o := &opServer{streams: []handler{sse(ev(e1+":1", "queued"))}, epochs: []string{e1}, states: []string{"failed"},
			pagesStatus: http.StatusForbidden, restoreAtPages: e2, outage: 99, outageAfter: 2, outageKind: "502"}
		_, _, state, _, err := watched(t, o, e1, nil)
		if state != "" || !errors.Is(err, ErrWatchEnded) || !errors.Is(err, ErrWatchRestored) {
			t.Errorf("watch: state %q, error %v; want no state and an error that is ErrWatchEnded and ErrWatchRestored", state, err)
		}
	})
	t.Run("a terminal read stays no result when the refused drain showed another epoch and an outage ends the watch", func(t *testing.T) {
		reads = nil
		ended(t, 2*time.Second)
		// The first read is terminal in epoch 1 and its drain is refused. That answer carries epoch 2, so the
		// installation was restored. The read after it is out of reach until the watch ends: the terminal read
		// is of the installation before the restore, and the refused drain is reported beside that.
		const e2 = "ep_BWSYNTH2"
		o := &opServer{streams: []handler{sse(ev(e1+":1", "queued"))}, epochs: []string{e1}, states: []string{"failed"},
			pagesStatus: http.StatusForbidden, restoreAtPages: e2, outage: 99, outageAfter: 1, outageKind: "502"}
		_, _, state, _, err := watched(t, o, e1, nil)
		if state != "" || !errors.Is(err, ErrWatchEnded) || !errors.Is(err, ErrWatchRestored) || !strings.Contains(err.Error(), "403") {
			t.Errorf("watch: state %q, error %v; want no state and an error that is ErrWatchEnded and ErrWatchRestored, with the 403", state, err)
		}
	})
	t.Run("the watch ending in an outage skips the epoch refresh a new epoch asks for", func(t *testing.T) {
		reads = nil
		ended(t, 2*time.Second)
		// The second stream moves the installation to another epoch, then the operation read is out of reach.
		const e2 = "ep_BWSYNTH2"
		o := &opServer{streams: []handler{sse(ev(e1+":1", "queued")), sse(ev(e2+":1", "queued"))}, epochs: []string{"", e2},
			states: []string{"running"}, outage: 99, outageAfter: 1, outageKind: "502"}
		serverReads := 0
		count := func(c *Client) {
			base := c.hc.Transport
			if base == nil {
				base = http.DefaultTransport
			}
			c.hc.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/api/v1/server" {
					serverReads++
				}
				return base.RoundTrip(r)
			})
		}
		_, _, state, _, err := watched(t, o, e1, count)
		if serverReads != 0 || err != nil || state != "running" {
			t.Errorf("watch: state %q, error %v, %d reads of GET /server; want running, no error and no read of GET /server", state, err, serverReads)
		}
	})
	t.Run("the watch ending in an outage shows the recovery banner the outage answers carried", func(t *testing.T) {
		reads = nil
		ended(t, 2*time.Second)
		// Only the failed reads say that the installation is in recovery mode, so no read has shown the banner yet.
		o := &opServer{streams: []handler{sse(ev(e1+":1", "queued"))}, states: []string{"running"},
			outage: 99, outageAfter: 1, outageKind: "502", recoveryOnOutage: true}
		_, _, state, notices, err := watched(t, o, e1, nil)
		if err != nil || state != "running" || !strings.Contains(notices, "RECOVERY MODE is on") {
			t.Errorf("watch: state %q, error %v, notices %q; want running, no error and the recovery banner", state, err, notices)
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

// TestEpochHoldsHeaderless: a read whose answer has no Bronzeward-Epoch header is judged by the count of
// moves like any other (client-contracts.md §8.6 item 2). The count still advances when an answer of another
// epoch follows it, so that read no longer stands.
func TestEpochHoldsHeaderless(t *testing.T) {
	const e1, e2 = "ep_BWSYNTH1", "ep_BWSYNTH2"
	reads := 0
	f := &fake{epoch: e1, post: func(_ int, w http.ResponseWriter, r *http.Request) {
		reads++
		w.Header().Del("Bronzeward-Epoch") // the first read is a server defect: no header
		if reads > 1 {
			w.Header().Set("Bronzeward-Epoch", e2) // the second answers after a restore
		}
		_, _ = w.Write([]byte(`{"id":"op_BWSYNTH","kind":"publish","state":"succeeded"}`))
	}}
	c, _, _ := writeClient(t, f, e1)
	_, first, err := c.OperationBody("op_BWSYNTH")
	if err != nil || first.Epoch != "" {
		t.Fatalf("first read: epoch %q, error %v; want no header and no error", first.Epoch, err)
	}
	if !c.EpochHolds(first) {
		t.Fatal("the headerless read does not stand with no move since it")
	}
	_, second, err := c.OperationBody("op_BWSYNTH")
	if err != nil || second.Epoch != e2 {
		t.Fatalf("second read: epoch %q, error %v; want %s", second.Epoch, err, e2)
	}
	if c.EpochHolds(first) || c.EpochMoves() <= first.Moves {
		t.Errorf("the headerless read stands after an answer of another epoch (moves %d, read at %d); want the count to have advanced", c.EpochMoves(), first.Moves)
	}
}
