package main

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// Log writes one line per event: an RFC 3339 timestamp with nanoseconds, the actor, then
// key=value pairs. The harness orders lines from several processes by that timestamp, so every
// line carries the host clock at the moment of the event.
type Log struct {
	mu    sync.Mutex
	w     io.Writer
	actor string
}

func NewLog(w io.Writer, actor string) *Log { return &Log{w: w, actor: actor} }

func (l *Log) Printf(format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "%s actor=%s %s\n", time.Now().UTC().Format(time.RFC3339Nano), l.actor, fmt.Sprintf(format, a...))
}
