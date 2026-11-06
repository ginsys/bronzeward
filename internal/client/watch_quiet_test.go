package client

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// cancelOnWrite is the notice writer of a terminal that blocks. Its first write stands for the write
// that a Ctrl-C releases: the write completes, and the Ctrl-C is taken at the same moment.
type cancelOnWrite struct {
	to     *bytes.Buffer
	cancel func()
	first  bool
}

func (w *cancelOnWrite) Write(p []byte) (int, error) {
	n, err := w.to.Write(p)
	if !w.first {
		w.first = true
		w.cancel()
	}
	return n, err
}

// epochChangeStream answers the stream with an epoch other than the one the client knows and with a
// Deprecation header, so that observe has two notices to write. The stream then stays open.
func epochChangeStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-2")
	w.Header().Set("Deprecation", "true")
	w.(http.Flusher).Flush()
	<-r.Context().Done()
}

// TestStreamWritesNoSecondNoticeAfterACtrlCInTheFirst: the Ctrl-C comes while observe writes the epoch
// notice. The deprecation notice that follows it in observe must not be written. The epoch is still
// recorded, so the state is saved.
func TestStreamWritesNoSecondNoticeAfterACtrlCInTheFirst(t *testing.T) {
	s := newStreamStall(t, epochChangeStream)
	c, notices, _ := writeClient(t, &fake{epoch: "ep_BWSYNTH-1"}, "ep_BWSYNTH-1")
	c.Context.Server = s.srv.URL
	a := newArmed(t)
	c.Arm = a.hook
	c.Notices = &cancelOnWrite{to: notices, cancel: a.cancel}
	err := ended(t, c, Watch{})
	switch {
	case !errors.As(err, new(*Interrupted)):
		t.Errorf("error %v; want the watch stopped by Ctrl-C", err)
	case strings.Count(notices.String(), "\n") != 1 || strings.Contains(notices.String(), "deprecated"):
		t.Errorf("notices %q; want the epoch notice only", notices)
	case c.Context.LastEpoch != "ep_BWSYNTH-2":
		t.Errorf("epoch %q; want the epoch of the answer, recorded for the save", c.Context.LastEpoch)
	}
}

// TestStreamClearsNothingAfterACtrlCInTheResetNotice: a reset arrives, and the Ctrl-C comes while the
// reset notice is written. Clear must not run after it.
func TestStreamClearsNothingAfterACtrlCInTheResetNotice(t *testing.T) {
	s := newStreamStall(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: reset\ndata: {}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	c, notices, _ := writeClient(t, &fake{epoch: "ep_BWSYNTH-1"}, "ep_BWSYNTH-1")
	c.Context.Server = s.srv.URL
	a := newArmed(t)
	c.Arm = a.hook
	c.Notices = &cancelOnWrite{to: notices, cancel: a.cancel}
	var cleared int
	err := ended(t, c, Watch{Clear: func() { cleared++ }})
	switch {
	case !errors.As(err, new(*Interrupted)):
		t.Errorf("error %v; want the watch stopped by Ctrl-C", err)
	case cleared != 0:
		t.Errorf("Clear ran %d times; want none after the Ctrl-C", cleared)
	case strings.Count(notices.String(), "\n") != 1:
		t.Errorf("notices %q; want the reset notice only", notices)
	}
}

// TestStreamNoticesPassBeforeACtrlC: with no Ctrl-C yet, the notices of the attempt reach the writer.
// The Ctrl-C comes later and ends the stream.
func TestStreamNoticesPassBeforeACtrlC(t *testing.T) {
	s := newStreamStall(t, epochChangeStream)
	c, notices, _ := writeClient(t, &fake{epoch: "ep_BWSYNTH-1"}, "ep_BWSYNTH-1")
	c.Context.Server = s.srv.URL
	a := newArmed(t)
	c.Arm = a.hook
	go func() {
		<-s.arrived
		time.Sleep(200 * time.Millisecond) // the headers reach the client, and the stream stays open
		a.cancel()
	}()
	err := ended(t, c, Watch{})
	if !errors.As(err, new(*Interrupted)) || !strings.Contains(notices.String(), "recovery mode") || !strings.Contains(notices.String(), "deprecated") {
		t.Errorf("error %v, notices %q; want the stop and both notices of the answer", err, notices)
	}
}

// TestStreamRestoresNoticesAfterACtrlC: the stream drops notices only while it runs. After a Ctrl-C
// ended it, the client writes to the writer it was given.
func TestStreamRestoresNoticesAfterACtrlC(t *testing.T) {
	s := newStreamStall(t, epochChangeStream)
	c, notices, _ := writeClient(t, &fake{epoch: "ep_BWSYNTH-1"}, "ep_BWSYNTH-1")
	c.Context.Server = s.srv.URL
	a := newArmed(t)
	c.Arm = a.hook
	var given io.Writer = &cancelOnWrite{to: notices, cancel: a.cancel}
	c.Notices = given
	err := ended(t, c, Watch{})
	if !errors.As(err, new(*Interrupted)) {
		t.Fatalf("error %v; want the watch stopped by Ctrl-C", err)
	}
	if c.Notices != given {
		t.Errorf("Notices is %T; want the writer the client was given", c.Notices)
	}
	before := notices.Len()
	fmt.Fprint(c.Notices, "after the stop\n")
	if !strings.HasSuffix(notices.String(), "after the stop\n") || notices.Len() == before {
		t.Errorf("notices %q; want a notice written after the stop to reach the writer", notices)
	}
}
