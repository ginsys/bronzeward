package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
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

// hold arms Ctrl-C for the whole run of a command that reads and waits in turns (machine timeline
// --follow): its pauses and every read share the returned context. release disarms and tells whether
// a Ctrl-C was taken.
func (e *env) hold() (context.Context, func() bool) {
	ctx, disarm := e.arm()
	e.heldCtrlC = ctx
	return ctx, func() bool { e.heldCtrlC = nil; return disarm() }
}
