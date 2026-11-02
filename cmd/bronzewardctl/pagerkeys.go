package main

import (
	"io"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// liveKeys is the keySource of a terminal: it waits with poll(2) on the terminal and on a wake pipe
// that the signal watcher writes. It reads a key only when the terminal has one, one byte at a
// time, and never ahead: a reader goroutine would take the first key typed for the marks prompt
// after the key that quits.
type liveKeys struct {
	fd   int
	wake [2]int
	done chan struct{} // closed when watch has seen every signal that was sent

	mu      sync.Mutex
	ended   os.Signal // the first of SIGINT, SIGTERM and SIGHUP
	resized bool
}

func newLiveKeys(fd int) (*liveKeys, error) {
	k := &liveKeys{fd: fd, done: make(chan struct{})}
	if err := unix.Pipe(k.wake[:]); err != nil {
		return nil, err
	}
	for _, p := range k.wake {
		unix.CloseOnExec(p)
		if err := unix.SetNonblock(p, true); err != nil {
			k.close()
			return nil, err
		}
	}
	return k, nil
}

func (k *liveKeys) close() {
	unix.Close(k.wake[0])
	unix.Close(k.wake[1])
}

// watch notes each signal on terms and winch and wakes next, until both are closed. A full pipe
// already holds a wake-up, so a failed write loses nothing.
func (k *liveKeys) watch(terms, winch <-chan os.Signal) {
	defer close(k.done)
	for terms != nil || winch != nil {
		select {
		case s, ok := <-terms:
			if !ok {
				terms = nil
				continue
			}
			k.mu.Lock()
			if k.ended == nil {
				k.ended = s
			}
			k.mu.Unlock()
		case _, ok := <-winch:
			if !ok {
				winch = nil
				continue
			}
			k.mu.Lock()
			k.resized = true
			k.mu.Unlock()
		}
		_, _ = unix.Write(k.wake[1], []byte{0})
	}
}

// endedBy is the signal that ended the pager, nil when none came. It is complete once done is
// closed.
func (k *liveKeys) endedBy() os.Signal {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.ended
}

func (k *liveKeys) next() (byte, event, error) {
	for {
		fds := []unix.PollFd{{Fd: int32(k.fd), Events: unix.POLLIN}, {Fd: int32(k.wake[0]), Events: unix.POLLIN}}
		if _, err := unix.Poll(fds, -1); err == unix.EINTR {
			continue
		} else if err != nil {
			return 0, evKey, err
		}
		// A signal comes before a key that came with it.
		if fds[1].Revents != 0 {
			var drain [64]byte
			for n, _ := unix.Read(k.wake[0], drain[:]); n > 0; n, _ = unix.Read(k.wake[0], drain[:]) {
			}
			k.mu.Lock()
			ended, resized := k.ended, k.resized
			k.resized = false
			k.mu.Unlock()
			switch {
			case ended != nil:
				return 0, evSignal, nil
			case resized:
				return 0, evResize, nil
			}
		}
		if fds[0].Revents != 0 {
			var b [1]byte
			n, err := unix.Read(k.fd, b[:])
			switch {
			case err == unix.EINTR || err == unix.EAGAIN:
				continue
			case err != nil:
				return 0, evKey, err
			case n == 0:
				return 0, evKey, io.EOF
			}
			return b[0], evKey, nil
		}
	}
}
