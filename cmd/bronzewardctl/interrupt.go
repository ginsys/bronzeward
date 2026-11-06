package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/ginsys/bronzeward/internal/client"
)

// arm takes the first Ctrl-C of one read (decision of #643). It returns the context that the Ctrl-C
// cancels, and a disarm that stops the handler and tells whether a Ctrl-C was taken, one that came
// while the read ended included. The handler is armed around a read only: between reads, and in
// every write, the default action of Ctrl-C stays. After the first Ctrl-C the handler is stopped, so
// a second one ends the client at once, as in operation watch.
//
// Nothing is armed when a command holds Ctrl-C itself (operation watch, which has its own handler
// and grace time), or when SIGINT was inherited as ignored (nohup, a background job): it stays
// ignored, as the review pager has it.
func (e *env) arm() (context.Context, func() bool) {
	if e.cutNow(nil) != nil { // a Ctrl-C was taken: no read goes on, and no handler comes back
		done, stop := context.WithCancel(context.Background())
		stop()
		return done, func() bool { return true }
	}
	if held := e.heldCtrlC; held != nil { // a command that holds Ctrl-C for its whole run: its reads share the context
		return held, func() bool { return held.Err() != nil }
	}
	if e.ownsCtrlC || signal.Ignored(syscall.SIGINT) {
		return context.Background(), func() bool { return false }
	}
	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	stop, ended := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(ended)
		select {
		case <-sig:
			signal.Stop(sig) // a second Ctrl-C ends the client at once
			cancel()
		case <-stop:
		}
	}()
	var once sync.Once
	var taken bool
	return ctx, func() bool {
		once.Do(func() { // a second call, from a defer, answers as the first did
			close(stop)
			<-ended
			signal.Stop(sig) // Stop waits for a delivery in flight, so a Ctrl-C taken now is on sig
			select {
			case <-sig:
				cancel()
			default:
			}
			taken = ctx.Err() != nil
			cancel()
		})
		return taken
	}
}

// cutNow records e, the Ctrl-C a read took, when it is the first, and returns the first one of the
// run. A nil e only asks. Reads in goroutines (operation watch, the timeline) call it too.
func (e *env) cutNow(c *client.Interrupted) *client.Interrupted {
	e.cutMu.Lock()
	defer e.cutMu.Unlock()
	if e.cut == nil {
		e.cut = c
	}
	return e.cut
}

// cancellable runs one read that has no client of its own yet (context add) with the Ctrl-C of arm.
// A Ctrl-C taken as the read ends stops the command, whatever the read answered.
func (e *env) cancellable(route string, read func(context.Context) error) error {
	ctx, disarm := e.arm()
	err := read(ctx)
	var cut *client.Interrupted
	if disarm() && !errors.As(err, &cut) {
		cut = &client.Interrupted{Route: route}
		err = cut
	}
	if cut != nil {
		e.cutNow(cut)
	}
	return err
}

// hold arms Ctrl-C for the whole run of a command that reads and waits in turns (machine timeline
// --follow): its pauses and every read share the returned context. release disarms and tells whether
// a Ctrl-C was taken.
func (e *env) hold() (context.Context, func() bool) {
	ctx, disarm := e.arm()
	e.heldCtrlC = ctx
	return ctx, func() bool { e.heldCtrlC = nil; return disarm() }
}
