package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// interruptGrace is how long the first Ctrl-C waits for the watch to return the operation it last
// read before the process stops waiting; tests shorten it.
var interruptGrace = 3 * time.Second

// watchOut is what operation watch prints: a line from the watch goes through show, and finish prints
// the last lines. Once finish ran, show prints nothing, so a watch still running when the process
// stops waiting for it adds no line after the stop line.
type watchOut struct {
	mu      sync.Mutex
	w       io.Writer
	stopped bool
}

func (o *watchOut) show(line string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.stopped {
		fmt.Fprintln(o.w, line)
	}
}

// gate wraps a writer the watch's client writes to (its notices, its -v output), so that once finish
// ran it prints nothing either.
func (o *watchOut) gate(w io.Writer) io.Writer {
	if w == nil {
		return nil
	}
	return gated{o, w}
}

type gated struct {
	o *watchOut
	w io.Writer
}

func (g gated) Write(p []byte) (int, error) {
	g.o.mu.Lock()
	defer g.o.mu.Unlock()
	if g.o.stopped {
		return len(p), nil
	}
	return g.w.Write(p)
}

func (o *watchOut) finish(lines ...string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stopped = true
	for _, l := range lines {
		fmt.Fprintln(o.w, l)
	}
}

// What ended the watch, held in operationWatch's ended.
const (
	endInterrupt = 1
	endTimeout   = 2
)

// operationWatch follows an operation until its state is terminal (decision T23 of #258, journeys
// §5.7, client-contracts.md §8.6) through the progress reader, and prints one line per event and
// then the outcome. A streamed event carries no time, so its line shows when this client received
// it, marked as such; the completion line shows the server's time from the last event of the JSON
// pages. Unresolved is not terminal: the watch goes on past it. --timeout ends the watch with exit 5
// and Ctrl-C with exit 130; neither stops the operation, which the watch names as it last read it.
// The reader asks for the end only between its reads, so the timeout can pass by about 45 s, and the
// first Ctrl-C waits for the reader no longer than interruptGrace. Exit 130 is only for "stopped
// watching" an operation that is not terminal or was not read: what the reader returns within the
// grace is reported as it is, so an operation that ended exits 0 or 4 as its own answer, and a reader
// error keeps its own exit code. When the grace passes, the process returns at once and exits after
// the stop line, so the abandoned reader prints nothing more in the CLI.
func (e *env) operationWatch(args []string) (err error) {
	timeout := 30 * time.Minute
	pos, err := e.parse("operation watch", args, 1, func(fs *flag.FlagSet) { fs.DurationVar(&timeout, "timeout", timeout, "") })
	if err != nil {
		return err
	}
	if timeout <= 0 {
		return usageError{errors.New("--timeout: give a positive duration, such as 30m")}
	}
	id := pos[0]
	w := e.out // -o json and yaml keep standard output for the operation resource
	if e.output != "table" {
		w = e.err
	}
	out := &watchOut{w: w}
	c, err := e.openTo(out.gate(e.err), out.gate(e.verboseOut()))
	if err != nil {
		return err
	}
	// A watch left running when the process stops waiting for it still uses c, so c is not saved then.
	abandoned := false
	defer func() {
		if !abandoned {
			err = errors.Join(err, c.Save())
		}
	}()
	if _, _, err := c.Server(); err != nil {
		return err
	}
	o, _, err := c.OperationEpoch(id)
	if err != nil {
		return err
	}
	out.show(sf("%s  %s", o.Kind, subject(o.Subject)))
	out.show("Times marked \"received\" are this client's clock when the event arrived; the completion line shows the server's time.")

	var ended atomic.Int32 // 0 while it runs, then endInterrupt or endTimeout
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	type result struct {
		o    *apitypes.OperationBody
		read client.OperationRead // the operation read o comes from
		err  error
	}
	done := make(chan result, 1)
	go func() {
		o, read, err := c.WatchBody(id, client.Watch{
			Show:  func(ev client.Event) { out.show(eventLine(ev, time.Now())) },
			Clear: func() { out.show("(the events above no longer hold; they are shown again from the start)") },
			Until: func() (bool, error) { return ended.Load() != 0, nil },
		})
		done <- result{o, read, err}
	}()

	var res result
	gaveUp := false
	interrupt := func() { // the first Ctrl-C: the watch gets interruptGrace to return
		ended.Store(endInterrupt)
		signal.Stop(sig) // a second Ctrl-C ends the client at once
		grace := time.NewTimer(interruptGrace)
		defer grace.Stop()
		select {
		case res = <-done:
		case <-grace.C:
			gaveUp = true
		}
	}
	select {
	case res = <-done:
	case <-sig:
		interrupt()
	case <-timer.C:
		ended.Store(endTimeout)
		select {
		case res = <-done:
		case <-sig:
			interrupt()
		}
	}
	// The reads below take no Ctrl-C: from here the default action ends the client at once, as a
	// second Ctrl-C does, instead of a handler nobody reads swallowing it.
	signal.Stop(sig)
	follow := sf("Follow it again with: bronzewardctl operation watch %s", client.ShellWord(id))
	if gaveUp {
		abandoned = true
		out.finish(sf("Stopped watching at your request. Operation %s goes on; the state it has now was not read.", id), follow)
		return interrupted{ef("stopped watching operation %s", id)}
	}
	if res.err != nil {
		return res.err
	}
	o = res.o
	var last *lastEvent
	if client.Terminal(o.State) {
		last = readLast(c, id, o.State, res.read.Epoch)
	}
	lines, ok := outcome(o, last)
	if client.Terminal(o.State) && (last == nil || last.At == nil) {
		lines = append(lines, "The server's time of this end was not read.")
	}
	if !client.Terminal(o.State) {
		if ended.Load() == endInterrupt {
			out.finish(sf("Stopped watching at your request. Operation %s is still %s, as the server last read it; it goes on.", id, o.State), follow)
			return interrupted{ef("stopped watching operation %s, which is still %s", id, o.State)}
		}
		lines = append([]string{sf("Still %s after --timeout %s.", o.State, timeout)}, lines...)
		lines = append(lines, follow)
	}
	out.finish(lines...)
	if e.output != "table" { // the body the outcome and the exit code come from, not a second read
		if err := e.emit(res.read.Body, nil); err != nil {
			return err
		}
	}
	switch {
	case !client.Terminal(o.State):
		return notTerminal{ef("operation %s is still %s after --timeout %s", id, o.State, timeout)}
	case !ok:
		return operationFailed{ef("operation %s ended %s", id, o.State)}
	}
	return nil
}

// lastEvent is an operation's last event as the JSON pages carry it, with the server's time.
type lastEvent struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	At   *time.Time      `json:"at"`
	Data json.RawMessage `json:"data"`
}

// readLast reads the operation's last event from the JSON pages. It is nil when the pages or the
// operation cannot be read, or when the event is not of the epoch of the operation read that follows:
// an installation restored meanwhile has other events, so the event's time is not the operation's. It
// is also nil when that read shows another state, or another Bronzeward-Epoch header, than the read
// the watch returned (state, watched). The operation's own epoch member is the one it was created in
// and does not change in a restore, so it is not the epoch to compare. The outcome and the time
// printed together then belong to one operation.
func readLast(c *client.Client, id, state, watched string) *lastEvent {
	items, err := c.List("/operations/" + url.PathEscape(id) + "/events")
	if err != nil || len(items) == 0 {
		return nil
	}
	var ev lastEvent
	if json.Unmarshal(items[len(items)-1], &ev) != nil {
		return nil
	}
	o, epoch, err := c.OperationEpoch(id)
	if err != nil || !client.Terminal(o.State) || o.State != state || epoch == "" || epoch != watched || client.EpochOf(ev.ID) != epoch {
		return nil
	}
	return &ev
}
