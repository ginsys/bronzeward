package main

import (
	"errors"
	"io"
	"time"
)

// errBlocked is the error of a write that did not complete in time, or that follows one that is
// still waiting in the terminal.
var errBlocked = errors.New("the terminal did not take the output")

// exitWait is how long the pager waits for the terminal to take the bytes that leave the alternate
// screen before it restores the terminal without them. It is a variable so that a test can make the
// wait long enough to resume output inside it at a point of its choice.
var exitWait = 250 * time.Millisecond

// abortWait is how long a write that abort cancels may still finish: a terminal that drains takes
// it at once, so a signal that comes with a write in flight does not lose the write, and a
// terminal that does not is given up on after this.
const abortWait = 50 * time.Millisecond

// pageOut is the pager's output. A write to a terminal that no longer drains its output waits in
// the kernel and no signal interrupts it, so pageOut makes the write in a goroutine of its own
// and gives up on it shortly after abort closes (a termination signal came in): the pager can then restore
// the terminal and end by the signal, and the abandoned write ends with the process. It lets only
// one write wait at a time, so the bytes of a later write never reach the terminal before an
// earlier one that is still waiting.
type pageOut struct {
	w        io.Writer
	abort    <-chan struct{}
	inflight chan struct{} // closed when the latest write has returned; the pager's goroutine alone uses it
}

type written struct {
	n   int
	err error
}

func (o *pageOut) Write(p []byte) (int, error) { return o.write(p, nil, o.abort) }

// busy reports whether a write that was given up is still waiting in the terminal.
func (o *pageOut) busy() bool {
	if o.inflight == nil {
		return false
	}
	select {
	case <-o.inflight:
		return false
	default:
		return true
	}
}

// last writes what leaves the alternate screen. A signal does not cancel it, since the terminal
// must be left whatever ended the pager, and it waits no longer than exitWait in all, for a write
// still waiting from before as well, so that the bytes keep their order.
func (o *pageOut) last(p []byte) error {
	t := time.NewTimer(exitWait)
	defer t.Stop()
	if o.busy() {
		select {
		case <-o.inflight:
		case <-t.C:
			return errBlocked
		}
	}
	_, err := o.write(p, t.C, nil)
	return err
}

func (o *pageOut) write(p []byte, limit <-chan time.Time, abort <-chan struct{}) (int, error) {
	if o.busy() {
		return 0, errBlocked
	}
	fin := make(chan struct{})
	o.inflight = fin
	done := make(chan written, 1)
	go func() {
		n, err := o.w.Write(p)
		close(fin) // before done: a caller that has the result must not find the write still in flight
		done <- written{n, err}
	}()
	select {
	case r := <-done:
		return r.n, r.err
	case <-abort:
		t := time.NewTimer(abortWait)
		defer t.Stop()
		select {
		case r := <-done:
			return r.n, r.err
		case <-t.C:
			return 0, errSignalled
		}
	case <-limit:
		return 0, errBlocked
	}
}
