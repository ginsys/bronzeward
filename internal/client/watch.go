package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// Event is one event of an operation, from its stream or its JSON pages (persistence and API §8.3).
type Event struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Watch is how a watch shows what it reads: Show gets each event once, in order, and Clear says
// that what was shown no longer holds, before the events are shown again from the start.
// Until, when set, ends the watch before the operation's state is terminal: it is asked after each
// event shown, at each keep-alive and after each read of the operation, and once it answers true
// the watch returns the operation as it last read it. Its error ends the watch.
type Watch struct {
	Show  func(Event)
	Clear func()
	Until func() (bool, error)
	sleep func(time.Duration) // when set, replaces the wait between attempts; tests set it
	halt  *halt
}

// halt is what Until answered: once done, the watch ends.
type halt struct {
	done bool
	err  error
}

// stop asks Until, once it has not yet ended the watch.
func (w Watch) stop() bool {
	if !w.halt.done && w.Until != nil {
		w.halt.done, w.halt.err = w.Until()
		w.halt.done = w.halt.done || w.halt.err != nil
	}
	return w.halt.done
}

// streamIdle ends a stream that sent nothing, not even its keep-alive comment every 15 s, for this
// long: the connection is taken as cut and the watch reads the operation (§8.6 item 5).
var streamIdle = 45 * time.Second

// Terminal reports that no event follows an operation's state. Unresolved is not terminal: a
// recovery administrator resolves it.
func Terminal(state string) bool {
	switch state {
	case "succeeded", "failed", "completed", "rejected", "cancelled":
		return true
	}
	return false
}

// Watch follows the operation op until its state is terminal and every event before it was shown
// (decision T16 of #256, client-contracts.md §8.6). It reads the event stream, and the JSON pages
// when the server answers without one. When a stream ends it reads the operation: on a terminal
// state it drains the pages of what the stream did not send; on any other state it renews a
// sign-in about to expire (§9.5 item 4) and reconnects with Last-Event-ID. An attempt that showed
// nothing is retried after 1 s, doubling to 30 s; JSON pages are read again at the same pace.
func (c *Client) Watch(op string, w Watch) (*apitypes.OperationBody, error) {
	o, _, err := c.WatchBody(op, w)
	return o, err
}

// OperationRead is one read of an operation as the server answered it: the body as sent and the
// Bronzeward-Epoch header the answer carried, which is the installation's epoch at the read, not
// the epoch the operation was created in.
type OperationRead struct {
	Body  []byte
	Epoch string
}

// WatchBody is Watch that also returns the operation read the result comes from, so a caller that
// prints the resource prints the one it decided the outcome from, and compares the epoch of the
// reads that follow with the one it was decided in.
func (c *Client) WatchBody(op string, w Watch) (*apitypes.OperationBody, OperationRead, error) {
	w.halt = &halt{}
	// shown counts the events shown in the attempt since it began or since the last clear: an attempt
	// with none left is a failure. An event shown and then cleared is not progress, so a stream whose
	// events never match its epoch, cleared on every attempt, backs off.
	shown, shows, clears := 0, w.Show, w.Clear
	w.Show = func(e Event) { shown++; shows(e) }
	// What Until answered held for the events now cleared, the drain's own clears included: it is
	// asked again. An error it returned stands.
	w.Clear = func() {
		shown = 0
		if w.halt.err == nil {
			w.halt.done = false
		}
		if clears != nil {
			clears()
		}
	}
	var last string // the ID of the last event shown
	var lastOp *apitypes.OperationBody
	var lastRead OperationRead // the last read of the operation in this loop
	pages, delay := false, time.Second
	// Until is asked before the first stream: a caller whose time ran out during the reads before the
	// watch has already ended it, and no stream is opened for that. The operation read below is the result.
	first := true
	for {
		c.renewSoon()
		shown = 0
		began := time.Now()
		opened, err := false, error(nil)
		skip := first && w.stop()
		first = false
		switch {
		case skip: // no stream and no drain
		case pages:
			opened = true
			_, err = c.drain(op, &last, w)
		default:
			opened, pages, err = c.stream(op, &last, w)
		}
		if err != nil {
			return nil, OperationRead{}, err
		}
		// The attempt's own time: an operation read that is slow does not skip the backoff.
		took := time.Since(began)
		// The read is repeated through an outage of the server until the watch ends (§8.6 item 2).
		o, epoch, raw, err := c.operationRawUntil(op, w.stop)
		if err != nil {
			// The watch ended while the server was out of reach: the operation as the last read showed
			// it, which is not terminal, or the error when no read in this loop gave one.
			if errors.Is(err, ErrWatchEnded) {
				if w.halt.err != nil { // Until failed: its error stands, as at the stop below
					return nil, OperationRead{}, w.halt.err
				}
				if lastOp != nil {
					return lastOp, lastRead, nil
				}
			}
			return nil, OperationRead{}, err
		}
		lastOp, lastRead = o, OperationRead{raw, epoch}
		if Terminal(o.State) {
			// A terminal state settles the watch, so the drain shows the last events, Until or not.
			// Events of another epoch than the operation read's are of a restored installation, where
			// the operation can be in another state: it is read again before the watch ends, and Until
			// holds again if it goes on. A failed drain shows no epoch, so the operation is read
			// again, and its state stands only if the epoch held.
			all := w
			all.Until, all.halt = nil, &halt{}
			pageEpoch, derr := c.drain(op, &last, all)
			if derr != nil {
				again, now, againRaw, err := c.operationRawUntil(op, w.stop)
				switch {
				case errors.Is(err, ErrWatchEnded):
					// The watch ended before the second read: the terminal read stands, and the failed drain is reported.
					return o, OperationRead{raw, epoch}, derr
				case err != nil:
					return nil, OperationRead{}, errors.Join(derr, err)
				case now == epoch && Terminal(again.State):
					return again, OperationRead{againRaw, now}, derr
				}
			} else {
				held := pageEpoch == "" || pageEpoch == epoch
				if epoch == "" || held && (last == "" || epochOf(last) == epoch) {
					return o, OperationRead{raw, epoch}, nil
				}
				// Pages of another epoch than the operation read's, an empty list included, are of a
				// restored installation: what was shown from the earlier one no longer holds.
				if !held && last != "" && epochOf(last) != pageEpoch {
					c.cleared(w, "the installation was restored or entered recovery mode")
					last = ""
				}
			}
			// The watch goes on after the delay, so a server whose events never match its operation
			// read's epoch is not asked again without a pause.
			if err := c.pause(op, w, delay); err != nil {
				return nil, OperationRead{}, err
			}
			delay = min(2*delay, 30*time.Second)
			continue
		}
		// Events of another epoch than the operation read's are of a restored installation, and the
		// operation can have no event to replace them: they are cleared even when Until does not stop,
		// so what Until kept from them does not hold the watch (client-contracts.md section 8.5).
		if epoch != "" && last != "" && epochOf(last) != epoch {
			c.cleared(w, "the installation was restored or entered recovery mode")
			last = ""
		}
		if w.stop() {
			if w.halt.err != nil {
				return nil, OperationRead{}, w.halt.err
			}
			if epoch == "" || last == "" || epochOf(last) == epoch {
				return o, OperationRead{raw, epoch}, nil
			}
			// What Until answered rests on events of another epoch than the operation read's: they
			// are cleared, which asks Until again, and the next stream shows the events from the start.
			c.cleared(w, "the installation was restored or entered recovery mode")
			last = ""
		}
		// An attempt that showed nothing waits the delay, which doubles until an attempt shows
		// something or a stream stays open for 30 s. JSON pages are read again after the delay.
		// Events shown and then cleared in the attempt are not progress, however they were cleared
		// (a reset, an event of another epoch, or the operation read above): a server that replays
		// them on every attempt would otherwise be asked again with no delay.
		progress := shown > 0
		if progress || opened && !pages && took >= 30*time.Second {
			delay = time.Second
		}
		if pages || !progress {
			if err := c.pause(op, w, delay); err != nil {
				return nil, OperationRead{}, err
			}
			if !progress {
				delay = min(2*delay, 30*time.Second)
			}
		}
	}
}

// operation reads GET /operations/{op} and the epoch the read held.
func (c *Client) operation(op string) (*apitypes.OperationBody, string, error) {
	o, epoch, _, err := c.operationRaw(op)
	return o, epoch, err
}

// operationRaw is operation with the body as the server sent it.
func (c *Client) operationRaw(op string) (*apitypes.OperationBody, string, []byte, error) {
	return c.operationRawUntil(op, nil)
}

// operationRawUntil is operationRaw whose retries go on until stop answers true (retryUntil). A nil
// stop is operationRaw's five tries.
func (c *Client) operationRawUntil(op string, stop func() bool) (*apitypes.OperationBody, string, []byte, error) {
	r, err := c.get("/operations/"+url.PathEscape(op), stop)
	if err != nil {
		return nil, "", nil, err
	}
	var o apitypes.OperationBody
	if err := json.Unmarshal(r.Body, &o); err != nil {
		return nil, "", nil, c.Context.fail(fmt.Errorf("GET /api/v1/operations/%s: %w", printable(op), err))
	}
	return &o, r.Header.Get("Bronzeward-Epoch"), r.Body, nil
}

// renewSoon renews a sign-in about to expire, so a long wait goes on (§9.5 item 4). A failure
// leaves the current token in use until the server refuses it.
func (c *Client) renewSoon() {
	s := c.signIn
	if s == nil || s.failed || s.tokens.RefreshToken == "" || !s.expiring() {
		return
	}
	if err := c.renew(); err != nil {
		fmt.Fprintf(c.Notices, "%s: %v; the current token stays in use until the server refuses it.\n", c.Context.Where(), err)
	}
}

// eventsRoute is the route of an operation's event stream, as a stop line names it.
func eventsRoute(op string) string {
	return "GET /api/v1/operations/" + url.PathEscape(op) + "/events"
}

// pause waits d before the watch's next attempt. A Ctrl-C ends the wait and the watch, as it ends the
// stream, with the route of the stream (client-contracts.md §8.2). A Watch with a sleep of its own
// waits with that, and takes no Ctrl-C.
func (c *Client) pause(op string, w Watch, d time.Duration) error {
	if w.sleep != nil {
		w.sleep(d)
		return nil
	}
	ctx, disarm := c.arm()
	_ = waitFor(ctx, d) // a taken Ctrl-C is what disarm reports
	if disarm() {
		return c.cut(&Interrupted{Route: eventsRoute(op)})
	}
	return nil
}

// stream reads the operation's event stream once, showing each event after *last. It reports
// pages when the server answers without a stream. A transport failure, a 5xx, an unavailable
// dependency or a cut connection end it without an error: the watch reads the operation next. A
// 401 on a sign-in is renewed once and the stream opened again (§9.5 item 1). A Ctrl-C ends the
// attempt, the open stream included, with *Interrupted and no notice.
func (c *Client) stream(op string, last *string, w Watch) (opened, pages bool, err error) {
	armed, disarm := c.arm()
	opened, pages, err = c.quietIn(armed, op, last, w)
	if disarm() {
		return opened, pages, c.cut(&Interrupted{Route: eventsRoute(op)})
	}
	return opened, pages, err
}

// cutWriter drops what is written once cut says that a Ctrl-C was taken.
type cutWriter struct {
	to  io.Writer
	cut func() bool
}

func (w cutWriter) Write(p []byte) (int, error) {
	if w.cut() {
		return len(p), nil
	}
	return w.to.Write(p)
}

// quietIn is streamIn under one rule: once armed is done, the attempt writes no notice and runs no
// callback of w. A write that already started is not cut short. The notices go through cutWriter
// and the callbacks through guards, so no sink in the attempt (observe, renew and cleared included)
// needs a check of its own. The writer is restored before this returns, so stream, its cut and the
// rest of the command see the writer the client was given.
func (c *Client) quietIn(armed context.Context, op string, last *string, w Watch) (opened, pages bool, err error) {
	if armed.Done() == nil { // nothing can end it: no Arm, or a command that holds Ctrl-C itself
		return c.streamIn(armed, op, last, w)
	}
	cut := func() bool { return armed.Err() != nil }
	given := c.Notices
	c.Notices = cutWriter{given, cut}
	defer func() { c.Notices = given }()
	showEvent, clearAll, until := w.Show, w.Clear, w.Until
	w.Show = func(e Event) {
		if !cut() {
			showEvent(e)
		}
	}
	if clearAll != nil {
		w.Clear = func() {
			if !cut() {
				clearAll()
			}
		}
	}
	if until != nil {
		// false, not true: true would set halt.done and end the watch as satisfied (stop).
		w.Until = func() (bool, error) {
			if cut() {
				return false, nil
			}
			return until()
		}
	}
	return c.streamIn(armed, op, last, w)
}

// streamIn is stream with the context a Ctrl-C cancels.
func (c *Client) streamIn(armed context.Context, op string, last *string, w Watch) (opened, pages bool, err error) {
	path := "/api/v1/operations/" + url.PathEscape(op) + "/events"
	route := "GET " + path
	for renewed := false; ; renewed = true {
		ctx, cancel := context.WithCancel(armed)
		defer cancel()
		q, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(c.Context.Server, "/")+path, nil)
		if err != nil {
			return false, false, c.Context.fail(err)
		}
		q.Header.Set("Authorization", "Bearer "+c.token)
		q.Header.Set("Accept", "text/event-stream")
		if *last != "" {
			q.Header.Set("Last-Event-ID", *last)
		}
		hc := *c.hc
		hc.Timeout = 0 // a stream lasts; streamIdle bounds a silent one
		// The idle timer runs from the request on, so a stalled answer, a refusal's body included,
		// ends after streamIdle as a silent stream does.
		idle := time.AfterFunc(streamIdle, cancel)
		defer idle.Stop()
		r, err := hc.Do(q)
		if err != nil {
			err = transportError(c.Context, c.Context.Host(), err)
			var t *trust
			if errors.As(err, &t) {
				return false, false, c.Context.fail(fmt.Errorf("%s: %w", route, err))
			}
			fmt.Fprintf(c.Notices, "%s: %s: %v\n", c.Context.Where(), route, err)
			return false, false, nil
		}
		defer r.Body.Close()
		c.observe(route, r)
		if r.StatusCode >= 300 && r.StatusCode < 400 {
			return false, false, c.Context.fail(fmt.Errorf("%s answered %s redirecting to %q; bronzewardctl follows no redirect",
				route, printable(r.Status), r.Header.Get("Location")))
		}
		if r.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			// A Ctrl-C ends this read with the bytes it had. renew is no sink and takes no context, so
			// only a check here keeps an answer built from those bytes from starting a renewal.
			if armed.Err() != nil {
				return false, false, nil
			}
			p := c.problem(route, r, body)
			switch {
			case p != nil && p.Status == http.StatusUnauthorized && !renewed && c.signIn != nil && !c.signIn.failed && c.signIn.tokens.RefreshToken != "":
				rerr := c.renew()
				// renew cannot be cut short. The check keeps `continue` from sending a new request after it.
				if armed.Err() != nil {
					return false, false, nil
				}
				if rerr != nil {
					fmt.Fprintf(c.Notices, "%s: %v.\n", c.Context.Where(), rerr)
					return false, false, p
				}
				continue
			case p != nil && !p.unavailable() && p.Status < 500:
				return false, false, p
			case p == nil && r.StatusCode < 500:
				return false, false, c.Context.fail(fmt.Errorf("%s answered %s, not a Bronzeward problem", route, printable(r.Status)))
			}
			fmt.Fprintf(c.Notices, "%s: %s answered %s\n", c.Context.Where(), route, printable(r.Status))
			return false, false, nil
		}
		if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "text/event-stream" {
			return true, true, nil
		}
		// Without the header no event ID can be compared with it: the defect is reported (§8.5) and
		// the events are shown as they come.
		epoch := r.Header.Get("Bronzeward-Epoch")
		if epoch == "" {
			fmt.Fprintf(c.Notices, "%s: %s answered without Bronzeward-Epoch, a server defect\n", c.Context.Where(), route)
		}
		sc := bufio.NewScanner(r.Body)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		var ev Event
		data := false // ev has a data line
		// Until can read (the import follow reads its claim), and a read that starts after a Ctrl-C took
		// the stream would arm a context that Ctrl-C already passed. quietIn guards Until, so it is not
		// asked after the Ctrl-C.
		quit := w.stop
		for sc.Scan() {
			// The scanner delivers lines it buffered before the Ctrl-C. The callbacks are guarded, but
			// *last and ev must not move for lines that nothing shows, so the loop ends here.
			if armed.Err() != nil {
				return true, false, nil
			}
			idle.Reset(streamIdle)
			line := sc.Text()
			if strings.HasPrefix(line, ":") {
				// A comment: the keep-alive (§8.6 item 5).
				if quit() {
					return true, false, nil
				}
				continue
			}
			if line != "" {
				field, value, _ := strings.Cut(line, ":")
				value = strings.TrimPrefix(value, " ")
				switch field {
				case "id":
					ev.ID = value
				case "event":
					ev.Type = value
				case "data":
					// The data lines of one event are joined with a line feed (the SSE format).
					if data {
						ev.Data = append(ev.Data, '\n')
					}
					ev.Data, data = append(ev.Data, value...), true
				}
				continue
			}
			switch {
			case ev.Type == "reset":
				c.cleared(w, "the installation was restored or entered recovery mode")
				*last = ""
			case ev.ID == "":
			case epoch != "" && epochOf(ev.ID) != epoch:
				// The installation entered recovery mode while the stream stayed open (§8.6 item 3):
				// the operation read that follows applies §8.5, and the stream reopens from the start.
				c.cleared(w, "the installation entered recovery mode or was restored while the events were read")
				*last = ""
				return true, false, nil
			default:
				show(w, ev, last)
				if quit() {
					return true, false, nil
				}
			}
			ev, data = Event{}, false
		}
		return true, false, nil
	}
}

// drain reads the JSON event pages to their end and shows every event after *last (§8.6 item 2).
// Events of another epoch than the last one shown are read after a restore or a recovery entry:
// what was shown is cleared and every event shown again. It answers the Bronzeward-Epoch of the
// pages, empty when they had none or the read failed: a list with no event still names an epoch.
func (c *Client) drain(op string, last *string, w Watch) (string, error) {
	items, epoch, err := c.ListAt("/operations/" + url.PathEscape(op) + "/events")
	if err != nil {
		return "", err
	}
	for i, raw := range items {
		var ev Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			return "", c.Context.fail(fmt.Errorf("GET /api/v1/operations/%s/events: an event does not decode: %w", printable(op), err))
		}
		if i == 0 && *last != "" && epochOf(ev.ID) != epochOf(*last) {
			c.cleared(w, "the installation was restored or entered recovery mode")
			*last = ""
		}
		show(w, ev, last)
		if w.stop() {
			return epoch, nil
		}
	}
	return epoch, nil
}

// cleared says why what the watch showed no longer holds, then clears it.
func (c *Client) cleared(w Watch, why string) {
	fmt.Fprintf(c.Notices, "%s: %s; the operation's events are shown again from the start.\n", c.Context.Where(), why)
	if w.Clear != nil {
		w.Clear()
	}
}

// show shows ev unless it is not after *last, which a stream and the pages that follow it can
// both carry.
func show(w Watch, ev Event, last *string) {
	if *last != "" && epochOf(ev.ID) == epochOf(*last) && number(ev.ID) <= number(*last) {
		return
	}
	w.Show(ev)
	*last = ev.ID
}

func epochOf(id string) string { e, _, _ := strings.Cut(id, ":"); return e }

// EpochOf is the epoch of an event ID, which has the form <epoch>:<number>: the part a caller
// compares with the Bronzeward-Epoch header of an answer, as the watch does.
func EpochOf(id string) string { return epochOf(id) }

// OperationBody reads GET /operations/{op} once, as OperationEpoch does, and returns the read with the
// body as the server sent it.
func (c *Client) OperationBody(op string) (*apitypes.OperationBody, OperationRead, error) {
	o, epoch, raw, err := c.operationRaw(op)
	return o, OperationRead{raw, epoch}, err
}

// OperationEpoch reads GET /operations/{op} once and returns the operation and the epoch of that
// answer, as the watch reads it.
func (c *Client) OperationEpoch(op string) (*apitypes.OperationBody, string, error) {
	return c.operation(op)
}

func number(id string) int64 {
	_, n, _ := strings.Cut(id, ":")
	v, _ := strconv.ParseInt(n, 10, 64)
	return v
}
