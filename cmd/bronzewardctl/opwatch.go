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

// sink is one output stream of the watch and the lock that keeps the lines written to it whole and in
// order. A write that waits on a terminal holds only its own sink.
type sink struct {
	mu sync.Mutex
	w  io.Writer
}

// watchOut is what operation watch prints: a line from the watch goes through show, and finish prints
// the last lines, both on the main sink. Once finish ran, show prints nothing, so a watch still running
// when the process stops waiting for it adds no line after the stop line.
//
// The stop barrier is the flag stopped, which no lock guards: finish sets it before it takes the
// main sink, so a write that takes a sink afterwards prints nothing, and a write that took it before
// is finished first, on that sink only. A write blocked on another sink (standard error blocked while
// the stop lines go to standard output) neither holds up finish nor the check of the watch's failure.
type watchOut struct {
	main    *sink
	stopped atomic.Bool
	emu     sync.Mutex // guards err, and is never held across a write
	err     error      // the first error writing a line, kept for the watch to report
}

func (o *watchOut) show(line string) {
	o.main.mu.Lock()
	defer o.main.mu.Unlock()
	if !o.stopped.Load() {
		o.print(o.main, line)
	}
}

// print writes one line to s and keeps the first failure: a watch whose output is lost must not end
// as if it had shown the operation. The caller holds s.mu.
//
// The error is tagged outputFailed (exit 6). Standard output tags its own failures already; in -o json
// and yaml the lines go to standard error, and they are still the output of the command.
func (o *watchOut) print(s *sink, line string) {
	if _, err := fmt.Fprintln(s.w, line); err != nil {
		o.emu.Lock()
		defer o.emu.Unlock()
		if o.err == nil {
			var tagged outputFailed
			if !errors.As(err, &tagged) {
				err = outputFailed{err}
			}
			o.err = err
		}
	}
}

// failure is the first error writing the watch's output, nil when every line was written. It takes no
// sink, so it returns while a write waits on a blocked terminal.
func (o *watchOut) failure() error {
	o.emu.Lock()
	defer o.emu.Unlock()
	return o.err
}

// gate wraps the sink a writer of the watch's client uses (its notices, its -v output), so that once
// finish ran it prints nothing either. A nil sink stays a nil writer.
func (o *watchOut) gate(s *sink) io.Writer {
	if s == nil {
		return nil
	}
	return gated{o, s}
}

type gated struct {
	o *watchOut
	s *sink
}

func (g gated) Write(p []byte) (int, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	if g.o.stopped.Load() {
		return len(p), nil
	}
	return g.s.w.Write(p)
}

// finish prints the last lines on the main sink. When that stream is blocked, finish waits on it: the
// stop lines and the outcome are the command's output, and no timeout drops them. A second Ctrl-C ends
// the command at once (operator-experience.md section 5.7).
func (o *watchOut) finish(lines ...string) {
	o.stopped.Store(true)
	o.main.mu.Lock()
	defer o.main.mu.Unlock()
	for _, l := range lines {
		o.print(o.main, l)
	}
}

// stop is finish for the first Ctrl-C: the user ended the watch, so its lines are printed last and
// the exit stays 130. When the output of the watch failed already, they go to alt (standard error)
// instead of to the sink that refused the lines before, and a failure to print them changes nothing.
func (o *watchOut) stop(alt *sink, lines ...string) {
	o.stopped.Store(true)
	s := o.main
	if o.failure() != nil {
		s = alt
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range lines {
		_, _ = fmt.Fprintln(s.w, l)
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
// it, marked as such; the completion line shows the server's time of the event of the JSON pages
// that records the end (recordsEnd), not of the last one. Unresolved is not terminal: the watch goes on past it. --timeout ends the watch with exit 5
// and Ctrl-C with exit 130; neither stops the operation, which the watch names as it last read it.
// Both count from the start of the command, so they reach the opening of the client (a wait on the
// token file's lock for a renewal, or an interactive sign-in) and the first read of the server and of
// the operation too, which run in the reader's goroutine behind the handler. A Ctrl-C at a sign-in
// prompt therefore gets the grace and exit 130, not the default kill. An error of the opening is
// returned as it is, as no request was sent.
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
	// One sink for standard error, which the client's notices and -v output use, and in -o json and yaml
	// the lines of the watch too, so that lines and notices never interleave. In table mode the lines and
	// the stop lines go to a sink of standard output, so a blocked standard error cannot hold them.
	errSink := &sink{w: e.err}
	mainSink := errSink // -o json and yaml keep standard output for the operation resource
	if e.output == "table" {
		mainSink = &sink{w: e.out}
	}
	var verbose *sink
	if e.verboseOut() != nil {
		verbose = errSink
	}
	out := &watchOut{main: mainSink}
	// c is set from the reader's result, and stays nil when the client was not opened (its opening failed
	// or was abandoned). A watch left running when the process stops waiting for it still uses c, so c is
	// not saved then.
	var c *client.Client
	abandoned := false
	saved := false // the stop path below saved before its stop lines, so the deferred save does not repeat it
	defer func() {
		if c != nil && !abandoned && !saved {
			err = errors.Join(err, c.Save())
		}
	}()
	// The handler and the timeout come before the opening of the client and the first read, so a Ctrl-C or
	// the timeout during any of them gets the grace and the stop lines. The opening can wait on the lock of
	// the token file for its renewal, and an interactive sign-in can wait on the terminal.
	var ended atomic.Int32 // 0 while it runs, then endInterrupt or endTimeout
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	type result struct {
		c       *client.Client // the opened client; nil when the opening failed
		openErr error          // the error of the opening, returned as it is: no request was sent
		o       *apitypes.OperationBody
		read    client.OperationRead // the operation read o comes from
		err     error
	}
	done := make(chan result, 1)
	go func() {
		oc, err := e.openTo(out.gate(errSink), out.gate(verbose))
		if err != nil {
			done <- result{openErr: err}
			return
		}
		// Every read of the watch that gives nothing maps through unreadState: the server's own problem keeps
		// its exit 1, anything else leaves the state unknown (exit 3, with the command that reads it). The
		// two first reads give no operation, so they return through the same path as the watch's own.
		if _, _, err := oc.Server(); err != nil {
			done <- result{c: oc, err: err}
			return
		}
		first, _, err := oc.OperationEpoch(id)
		if err != nil {
			done <- result{c: oc, err: err}
			return
		}
		out.show(sf("%s  %s", first.Kind, subject(first.Subject)))
		out.show("Times marked \"received\" are this client's clock when the event arrived; the completion line shows the server's time.")
		o, read, err := oc.WatchBody(id, client.Watch{
			Show:  func(ev client.Event) { out.show(eventLine(ev, time.Now())) },
			Clear: func() { out.show("(the events above no longer hold; they are shown again from the start)") },
			// A watch whose output cannot be written has nothing left to show: it stops reading.
			Until: func() (bool, error) { return ended.Load() != 0 || out.failure() != nil, nil },
		})
		done <- result{c: oc, o: o, read: read, err: err}
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
		out.stop(errSink, sf("Stopped watching at your request. Operation %s goes on; the state it has now was not read.", id), follow)
		return interrupted{ef("stopped watching operation %s", id)}
	}
	c = res.c
	// The client was not opened: no request was sent, so the operation's state is not in question and the
	// error of the opening stands as it is, with its own exit code.
	if res.openErr != nil {
		return res.openErr
	}
	// No operation read: the state is unknown, whatever the output did. The write error, if any, stays
	// beside it, and the error of the read decides the exit code.
	if res.o == nil {
		return errors.Join(unreadState(id, res.err), out.failure())
	}
	// The watch stopped because its output failed, with no end and no Ctrl-C or timeout: the operation
	// goes on, and the watch says why it stopped instead of waiting on.
	if werr := out.failure(); werr != nil && ended.Load() == 0 && !client.Terminal(res.o.State) {
		return ef("the output of the watch cannot be written: %w; operation %s goes on. Follow it again with: bronzewardctl operation watch %s", werr, id, client.ShellWord(id))
	}
	// The operation's end decides the outcome and the exit; a failed read of the events after it is
	// reported beside, as publish and import do.
	if res.err != nil {
		fmt.Fprintf(e.err, "bronzewardctl: %s; the operation's last events were not shown\n", client.Shown(res.err))
	}
	o := res.o
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
			// The resource the stop line names goes to standard output first. If it cannot be written, one
			// note says so before the stop lines: the user ended the watch, so the exit stays 130 and the
			// follow line stays last.
			if e.output != "table" {
				if err := e.emit(res.read.Body, nil); err != nil {
					fmt.Fprintf(e.err, "bronzewardctl: %s; the operation was not printed\n", client.Shown(err))
				}
			}
			// A failure to keep the client's state is printed before the stop lines, so the follow line
			// stays last; report returns 130 for a stop and prints nothing itself.
			saved = true
			if serr := c.Save(); serr != nil {
				fmt.Fprintf(e.err, "bronzewardctl: %s\n", client.Shown(serr))
			}
			out.stop(errSink, sf("Stopped watching at your request. Operation %s is still %s, as the server last read it; it goes on.", id, o.State), follow)
			return interrupted{ef("stopped watching operation %s, which is still %s", id, o.State)}
		}
		lines = append([]string{sf("Still %s after --timeout %s.", o.State, timeout)}, lines...)
		lines = append(lines, follow)
	}
	out.finish(lines...)
	var emitted error
	if e.output != "table" { // the body the outcome and the exit code come from, not a second read
		emitted = e.emit(res.read.Body, nil)
	}
	var final error
	switch {
	case !client.Terminal(o.State):
		final = notTerminal{ef("operation %s is still %s after --timeout %s", id, o.State, timeout)}
	case !ok:
		final = operationFailed{ef("operation %s ended %s", id, o.State)}
	}
	// A line or the resource that was not written is an error beside the outcome, whose exit code stays.
	return errors.Join(final, out.failure(), emitted)
}

// unreadState is the error of a watch that ended with no operation read. A problem document the
// server named stands as it is: exit 1 says that nothing was decided. Any other failure, an answer
// that cannot be read, a refused certificate or a connection that failed, leaves the operation's
// state unknown, as it may be running or ended: exit 3 (operator-experience.md section 4), with the
// command that reads it.
//
// A joined error (the events of the end, then the operation read) is classified by its last part, the
// operation read: a refusal of the events says nothing of the state the read could not give. The text
// stays whole.
func unreadState(id string, err error) error {
	decisive := err
	for {
		joined, ok := decisive.(interface{ Unwrap() []error })
		if !ok || len(joined.Unwrap()) == 0 {
			break
		}
		parts := joined.Unwrap()
		decisive = parts[len(parts)-1]
	}
	var problem *client.Problem
	var settled outcomeUnknown
	if errors.As(decisive, &problem) || errors.As(decisive, &settled) {
		return err
	}
	return outcomeUnknown{ef("%w; the state of operation %s was not read, so it may still be running or may have ended. Read it before deciding: bronzewardctl operation show %s",
		err, id, client.ShellWord(id))}
}

// recordsEnd is whether ev records the operation's end in state, by the event types the server writes
// for each kind of operation that ends:
//   - apply-config: an operation-state event whose data.to is the state (internal/api/outcome.go:114,
//     attempt.go:441, recovery_acts.go:300).
//   - adopt: the operation is inserted completed together with its adoption event
//     (internal/api/adoption.go:293 and :299); the applied-change event after it does not end it.
//   - publish and ingest: an event named after the state, succeeded or failed (publish.go:158, :328,
//     :415, runner.go:445, :457, ingestions.go:428, recovery_entries.go:276, staging/sweep.go:145).
//
// An event recorded after the end, such as the response of an attempt a takeover left behind
// (outcome.go:292), records no end.
func recordsEnd(ev lastEvent, state string) bool {
	switch ev.Type {
	case "operation-state":
		var to struct {
			To string `json:"to"`
		}
		return json.Unmarshal(ev.Data, &to) == nil && to.To == state
	case "adoption":
		return state == "completed"
	case "succeeded", "failed":
		return ev.Type == state
	}
	return false
}

// lastEvent is the event that ends an operation as the JSON pages carry it, with the server's time.
type lastEvent struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	At   *time.Time      `json:"at"`
	Data json.RawMessage `json:"data"`
}

// readLast reads from the JSON pages the last event that records the operation's end in state
// (recordsEnd). It is nil when the pages or the operation cannot be read, when no event records the
// end, or when the event is not of the epoch of the operation read that follows:
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
	found := false
	for i := len(items) - 1; i >= 0 && !found; i-- {
		var x lastEvent
		found = json.Unmarshal(items[i], &x) == nil && recordsEnd(x, state)
		if found {
			ev = x
		}
	}
	if !found {
		return nil
	}
	o, epoch, err := c.OperationEpoch(id)
	if err != nil || !client.Terminal(o.State) || o.State != state || epoch == "" || epoch != watched || client.EpochOf(ev.ID) != epoch {
		return nil
	}
	return &ev
}
