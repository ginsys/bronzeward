package client

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// TestWatchBodyHeaderlessTerminalRestored reproduces ginsys/bronzeward#695. The first operation read
// is terminal and has no Bronzeward-Epoch header (a server defect, client-contracts.md section 8.5). The
// pages drained after it answer another epoch, so the installation was restored since that read. The
// read then does not stand: section 8.6 item 2 judges a headerless read by the count of epoch moves.
// WatchBody must not return it as the result with no error.
func TestWatchBodyHeaderlessTerminalRestored(t *testing.T) {
	const e1, e2 = "ep_BWSYNTH1", "ep_BWSYNTH2"
	var opReads atomic.Int32
	var f *fake
	f = &fake{epoch: e1, post: func(_ int, w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/operations/op_BWSYNTH/events" && r.Header.Get("Accept") == "text/event-stream":
			w.Header().Set("Content-Type", "text/event-stream") // a stream that ends with no event
		case r.URL.Path == "/api/v1/operations/op_BWSYNTH/events":
			w.Header().Set("Bronzeward-Epoch", e2) // the pages show the restored installation
			f.mu.Lock()
			f.epoch = e2 // the restored server answers GET /server in e2 too, so the client's re-read moves nothing more
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"items":[` + item(e2+":1", "succeeded") + `]}`))
		case r.URL.Path == "/api/v1/operations/op_BWSYNTH":
			if opReads.Add(1) <= 2 {
				w.Header().Del("Bronzeward-Epoch") // the premise read and the watch's first read have no header
			} else {
				w.Header().Set("Bronzeward-Epoch", e2)
			}
			_, _ = w.Write([]byte(`{"id":"op_BWSYNTH","kind":"publish","state":"succeeded"}`))
		default:
			problemAnswer(w, http.StatusNotFound, "not-found", "")
		}
	}}
	c, _, _ := writeClient(t, f, e1)
	// Premise 1: the client sees no header on such an answer, and the read moves no count.
	if _, first, err := c.OperationBody("op_BWSYNTH"); err != nil || first.Epoch != "" || c.EpochMoves() != 0 {
		t.Fatalf("premise: headerless read has epoch %q, error %v, moves %d; want no header, no error, 0 moves", first.Epoch, err, c.EpochMoves())
	}
	op, rd, err := c.WatchBody("op_BWSYNTH", Watch{Show: func(Event) {}, sleep: func(time.Duration) {}})
	// Premise 2: the drained pages of another epoch moved the count once, through ListAt.
	if c.EpochMoves() != 1 {
		t.Fatalf("premise: count of moves is %d after the pages of %s; want 1", c.EpochMoves(), e2)
	}
	// A watch that reads the operation again ends on a read of e2 and holds: that outcome passes.
	if err == nil && op != nil && !c.EpochHolds(rd) {
		t.Errorf("WatchBody returned the %q read made before the restore (read at move %d, count now %d) with no error; want an error or a read that stands",
			op.State, rd.Moves, c.EpochMoves())
	}
}

// TestWatchBodyHeaderlessNeverEnds shows that the watch ends against a server that never sends the
// header: every read of the operation has none, and the pages show one restore and then stay in the
// new epoch. The first read does not stand, the second is taken after the move, and it stands. The
// sleep of the watch fails the test after 10 reads of the operation, so a loop ends the test.
func TestWatchBodyHeaderlessNeverEnds(t *testing.T) {
	const e1, e2 = "ep_BWSYNTH1", "ep_BWSYNTH2"
	var opReads atomic.Int32
	var f *fake
	f = &fake{epoch: e1, post: func(_ int, w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/operations/op_BWSYNTH/events" && r.Header.Get("Accept") == "text/event-stream":
			w.Header().Set("Content-Type", "text/event-stream") // a stream that ends with no event
		case r.URL.Path == "/api/v1/operations/op_BWSYNTH/events":
			w.Header().Set("Bronzeward-Epoch", e2)
			f.mu.Lock()
			f.epoch = e2
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"items":[` + item(e2+":1", "succeeded") + `]}`))
		case r.URL.Path == "/api/v1/operations/op_BWSYNTH":
			opReads.Add(1)
			w.Header().Del("Bronzeward-Epoch") // no read of the operation has a header
			_, _ = w.Write([]byte(`{"id":"op_BWSYNTH","kind":"publish","state":"succeeded"}`))
		default:
			problemAnswer(w, http.StatusNotFound, "not-found", "")
		}
	}}
	c, _, _ := writeClient(t, f, e1)
	// A watch that goes on after a terminal read waits through sleep before it reads again, and Until is
	// not asked on that path. So sleep ends the test when the reads pass the cap.
	sleep := func(time.Duration) {
		if n := opReads.Load(); n >= 10 {
			t.Fatalf("the operation was read %d times and the watch goes on; want it to end in at most 3 reads", n)
		}
	}
	op, rd, err := c.WatchBody("op_BWSYNTH", Watch{Show: func(Event) {}, sleep: sleep})
	if n := opReads.Load(); n > 3 {
		t.Fatalf("the operation was read %d times; want at most 3: one read before the move, one after it, and the end", n)
	}
	if err != nil || op == nil || !c.EpochHolds(rd) {
		t.Fatalf("WatchBody = %v, %+v, error %v; want a read that stands, with no error", op, rd, err)
	}
}

// TestWatchBodyHeaderlessTerminalSameEpoch is the case beside it that must keep working: the read has
// no Bronzeward-Epoch header, and the pages carry the epoch the client already knew. No answer showed
// another epoch, so the count did not move and the read stands: WatchBody returns it at once.
func TestWatchBodyHeaderlessTerminalSameEpoch(t *testing.T) {
	const e1 = "ep_BWSYNTH1"
	var opReads atomic.Int32
	f := &fake{epoch: e1, post: func(_ int, w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/operations/op_BWSYNTH/events" && r.Header.Get("Accept") == "text/event-stream":
			w.Header().Set("Content-Type", "text/event-stream") // a stream that ends with no event
		case r.URL.Path == "/api/v1/operations/op_BWSYNTH/events":
			w.Header().Set("Bronzeward-Epoch", e1)
			_, _ = w.Write([]byte(`{"items":[` + item(e1+":1", "succeeded") + `]}`))
		case r.URL.Path == "/api/v1/operations/op_BWSYNTH":
			opReads.Add(1)
			w.Header().Del("Bronzeward-Epoch") // every read of the operation has no header
			_, _ = w.Write([]byte(`{"id":"op_BWSYNTH","kind":"publish","state":"succeeded"}`))
		default:
			problemAnswer(w, http.StatusNotFound, "not-found", "")
		}
	}}
	c, _, _ := writeClient(t, f, e1)
	sleep := func(time.Duration) { // a watch that goes on waits here: the test ends instead of looping
		if n := opReads.Load(); n >= 10 {
			t.Fatalf("the operation was read %d times and the watch goes on; want it to end at the first read", n)
		}
	}
	op, rd, err := c.WatchBody("op_BWSYNTH", Watch{Show: func(Event) {}, sleep: sleep})
	if err != nil || op == nil || op.State != "succeeded" || rd.Epoch != "" || !c.EpochHolds(rd) || c.EpochMoves() != 0 {
		t.Fatalf("WatchBody = %v, %+v, error %v, moves %d; want the succeeded read with no header that stands, no error, 0 moves", op, rd, err, c.EpochMoves())
	}
	if n := opReads.Load(); n != 1 {
		t.Errorf("the operation was read %d times; want 1: a read that stands is not read again", n)
	}
}
