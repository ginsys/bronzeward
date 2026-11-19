package talos

import (
	"context"
	"sync"
	"time"
)

// A certificate Bronzeward signs per use is the only credential a connection has, and expiry is the
// only revocation (persistence-api.md §3.4): Talos checks a certificate when a connection is made, and
// a connection held open answered a request after NotAfter in the one sample measured. So Bound and
// BoundApplier close a connection at the certificate's NotAfter themselves.

// bound is the part of Bound and BoundApplier that ends a connection at notAfter: a timer that
// closes it, which ends a request in flight, and a context deadline for each request, so a request
// made at or after notAfter never reaches the node.
type bound struct {
	notAfter time.Time
	timer    *time.Timer
	once     sync.Once
	closeErr error
	close    func() error
}

func newBound(notAfter time.Time, closeConn func() error) *bound {
	b := &bound{notAfter: notAfter, close: closeConn}
	// The timer's callback closes the connection and touches nothing else of b: it can run before
	// this function returns, and b.timer is assigned after the timer starts.
	b.timer = time.AfterFunc(time.Until(notAfter), func() { _ = b.shut() })
	return b
}

// shut closes the connection, once.
func (b *bound) shut() error {
	b.once.Do(func() { b.closeErr = b.close() })
	return b.closeErr
}

// Close stops the timer and closes the connection, once.
func (b *bound) Close() error {
	b.timer.Stop()
	return b.shut()
}

// ctx is the request's context, ended at notAfter at the latest.
func (b *bound) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithDeadline(parent, b.notAfter)
}

type boundReader struct {
	*bound
	r Reader
}

// Bound makes r unusable from notAfter, the NotAfter of the certificate r was dialled with: its
// connection is closed then, a request in flight ends, and a later request fails.
func Bound(r Reader, notAfter time.Time) Reader {
	return &boundReader{bound: newBound(notAfter, r.Close), r: r}
}

func (b *boundReader) MachineConfig(ctx context.Context) (Config, error) {
	ctx, cancel := b.ctx(ctx)
	defer cancel()
	return b.r.MachineConfig(ctx)
}

func (b *boundReader) Version(ctx context.Context) (string, error) {
	ctx, cancel := b.ctx(ctx)
	defer cancel()
	return b.r.Version(ctx)
}

func (b *boundReader) Identity(ctx context.Context) (Identity, error) {
	ctx, cancel := b.ctx(ctx)
	defer cancel()
	return b.r.Identity(ctx)
}

type boundApplier struct {
	*bound
	a Applier
}

// BoundApplier is Bound for an Applier.
func BoundApplier(a Applier, notAfter time.Time) Applier {
	return &boundApplier{bound: newBound(notAfter, a.Close), a: a}
}

func (b *boundApplier) ApplyNoReboot(ctx context.Context, configuration []byte) (Response, error) {
	ctx, cancel := b.ctx(ctx)
	defer cancel()
	return b.a.ApplyNoReboot(ctx, configuration)
}
