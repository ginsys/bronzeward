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
	t.Skip("ginsys/bronzeward#728: WatchBody returns a headerless terminal read after a restore; remove this skip with the fix")
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
