package talos

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// closeCounter is a Reader and Applier that only counts its Close calls.
type closeCounter struct {
	Reader
	Applier
	closes atomic.Int32
}

func (c *closeCounter) Close() error { c.closes.Add(1); return nil }

func (c *closeCounter) Version(ctx context.Context) (string, error) { return "v", ctx.Err() }

// A bound connection is closed by NotAfter itself, once, and not before; Close before NotAfter
// closes it once and the timer closes it no more. The control reads the count just before NotAfter:
// a Bound that closed early, or never, fails one of the two reads.
func TestBoundClosesTheConnectionAtNotAfter(t *testing.T) {
	c := &closeCounter{}
	notAfter := time.Now().Add(400 * time.Millisecond)
	b := Bound(c, notAfter)
	if _, err := b.Version(t.Context()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(notAfter) - 150*time.Millisecond)
	if n := c.closes.Load(); n != 0 {
		t.Fatalf("closed %d times before NotAfter", n)
	}
	time.Sleep(time.Until(notAfter) + 150*time.Millisecond)
	if n := c.closes.Load(); n != 1 {
		t.Fatalf("closed %d times after NotAfter, want once", n)
	}
	if err := b.Close(); err != nil || c.closes.Load() != 1 {
		t.Fatalf("Close after the timer: %v, %d closes, want still one", err, c.closes.Load())
	}
	if _, err := b.Version(t.Context()); err == nil {
		t.Error("a request after NotAfter was made")
	}
}

func TestBoundCloseBeforeNotAfterClosesOnce(t *testing.T) {
	c := &closeCounter{}
	b := Bound(c, time.Now().Add(300*time.Millisecond))
	if err := b.Close(); err != nil || c.closes.Load() != 1 {
		t.Fatalf("Close: %v, %d closes, want one at once", err, c.closes.Load())
	}
	if err := b.Close(); err != nil || c.closes.Load() != 1 {
		t.Fatalf("a second Close: %v, %d closes, want still one", err, c.closes.Load())
	}
	time.Sleep(500 * time.Millisecond)
	if n := c.closes.Load(); n != 1 {
		t.Fatalf("closed %d times, want once", n)
	}
}

func TestBoundApplierClosesTheConnectionAtNotAfter(t *testing.T) {
	c := &closeCounter{}
	b := BoundApplier(c, time.Now().Add(200*time.Millisecond))
	time.Sleep(400 * time.Millisecond)
	if n := c.closes.Load(); n != 1 {
		t.Fatalf("closed %d times after NotAfter, want once", n)
	}
	_ = b.Close()
	if n := c.closes.Load(); n != 1 {
		t.Fatalf("closed %d times after Close, want once", n)
	}
}
