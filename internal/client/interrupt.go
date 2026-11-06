package client

import (
	"context"
	"time"
)

// Interrupted is a read that a Ctrl-C ended, in a request or in the wait before a retry. Route is the
// request it was reading. The retry rules of client-contracts.md §8.2 are unchanged: the read stops
// and tries no more. A write never returns it (Send arms nothing), because a Ctrl-C there must leave
// the outcome unknown (T9).
type Interrupted struct{ Route string }

func (e *Interrupted) Error() string {
	return "stopped at your request while reading " + printable(e.Route)
}

// arm takes the Ctrl-C for one read. Arm is set by the command and returns the context that the first
// Ctrl-C cancels, and a disarm that tells whether it did. A client with no Arm takes none, and neither
// does a write in flight: its reads (the principal, the epoch, the draft's ETag) leave the default
// action of Ctrl-C alone.
func (c *Client) arm() (context.Context, func() bool) {
	if c.Arm == nil || c.sending {
		return context.Background(), func() bool { return false }
	}
	return c.Arm()
}

// waitFor waits d before a retry and returns early, with the error of ctx, when ctx ends. A variable,
// so that a test waits for none of it.
var waitFor = defaultWait

func defaultWait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
