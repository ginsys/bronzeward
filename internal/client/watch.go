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
type Watch struct {
	Show  func(Event)
	Clear func()
	sleep func(time.Duration) // time.Sleep; tests replace it
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
// sign-in about to expire (§9.5 item 4) and reconnects with Last-Event-ID. A stream that failed
// to open, or ended within a second having shown nothing, is retried after 1 s, doubling to 30 s;
// JSON pages are read again at the same pace.
func (c *Client) Watch(op string, w Watch) (*apitypes.OperationBody, error) {
	if w.sleep == nil {
		w.sleep = time.Sleep
	}
	// steps counts what the watch showed or cleared: an attempt that changed nothing is a failure.
	steps, shows, clears := 0, w.Show, w.Clear
	w.Show = func(e Event) { steps++; shows(e) }
	w.Clear = func() {
		steps++
		if clears != nil {
			clears()
		}
	}
	var last string // the ID of the last event shown
	pages, delay := false, time.Second
	for {
		c.renewSoon()
		before, began := steps, time.Now()
		opened, err := false, error(nil)
		if pages {
			opened = true
			_, err = c.drain(op, &last, w)
		} else {
			opened, pages, err = c.stream(op, &last, w)
		}
		if err != nil {
			return nil, err
		}
		// The attempt's own time: an operation read that is slow does not skip the backoff.
		took := time.Since(began)
		o, err := c.Operation(op)
		if err != nil {
			return nil, err
		}
		if Terminal(o.State) {
			// A drain that crossed into another epoch read events of a restored installation, where
			// the operation can be in another state: read it again before the watch ends.
			if crossed, err := c.drain(op, &last, w); err != nil || !crossed {
				return o, err
			}
			continue
		}
		// JSON pages are read again after the delay, which starts again at 1 s once they show
		// something new.
		progress := steps != before
		if progress {
			delay = time.Second
		}
		if pages || !progress && (!opened || took < time.Second) {
			w.sleep(delay)
			if !progress {
				delay = min(2*delay, 30*time.Second)
			}
		}
	}
}

// Operation reads GET /operations/{op}.
func (c *Client) Operation(op string) (*apitypes.OperationBody, error) {
	r, err := c.Get("/operations/" + url.PathEscape(op))
	if err != nil {
		return nil, err
	}
	var o apitypes.OperationBody
	if err := json.Unmarshal(r.Body, &o); err != nil {
		return nil, c.Context.fail(fmt.Errorf("GET /api/v1/operations/%s: %w", printable(op), err))
	}
	return &o, nil
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

// stream reads the operation's event stream once, showing each event after *last. It reports
// pages when the server answers without a stream. A transport failure, a 5xx, an unavailable
// dependency or a cut connection end it without an error: the watch reads the operation next. A
// 401 on a sign-in is renewed once and the stream opened again (§9.5 item 1).
func (c *Client) stream(op string, last *string, w Watch) (opened, pages bool, err error) {
	path := "/api/v1/operations/" + url.PathEscape(op) + "/events"
	route := "GET " + path
	for renewed := false; ; renewed = true {
		ctx, cancel := context.WithCancel(context.Background())
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
			p := c.problem(route, r, body)
			switch {
			case p != nil && p.Status == http.StatusUnauthorized && !renewed && c.signIn != nil && !c.signIn.failed && c.signIn.tokens.RefreshToken != "":
				if rerr := c.renew(); rerr != nil {
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
		epoch := r.Header.Get("Bronzeward-Epoch")
		sc := bufio.NewScanner(r.Body)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		var ev Event
		for sc.Scan() {
			idle.Reset(streamIdle)
			line := sc.Text()
			if strings.HasPrefix(line, ":") {
				continue // a comment: the keep-alive (§8.6 item 5)
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
					ev.Data = append(ev.Data, value...)
				}
				continue
			}
			switch {
			case ev.Type == "reset":
				c.cleared(w, "the installation was restored or entered recovery mode")
				*last = ""
			case ev.ID == "":
			case epochOf(ev.ID) != epoch:
				// The installation entered recovery mode while the stream stayed open (§8.6 item 3):
				// the operation read that follows applies §8.5, and the stream reopens from the start.
				c.cleared(w, "the installation entered recovery mode or was restored while the events were read")
				*last = ""
				return true, false, nil
			default:
				show(w, ev, last)
			}
			ev = Event{}
		}
		return true, false, nil
	}
}

// drain reads the JSON event pages to their end and shows every event after *last (§8.6 item 2).
// Events of another epoch than the last one shown are read after a restore or a recovery entry:
// what was shown is cleared, every event shown again, and crossed reports it.
func (c *Client) drain(op string, last *string, w Watch) (crossed bool, err error) {
	items, err := c.List("/operations/" + url.PathEscape(op) + "/events")
	if err != nil {
		return false, err
	}
	for i, raw := range items {
		var ev Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			return crossed, c.Context.fail(fmt.Errorf("GET /api/v1/operations/%s/events: an event does not decode: %w", printable(op), err))
		}
		if i == 0 && *last != "" && epochOf(ev.ID) != epochOf(*last) {
			c.cleared(w, "the installation was restored or entered recovery mode")
			*last, crossed = "", true
		}
		show(w, ev, last)
	}
	return crossed, nil
}

// cleared says why what the watch showed no longer holds, then clears it.
func (c *Client) cleared(w Watch, why string) {
	fmt.Fprintf(c.Notices, "%s: %s; the operation's events are shown again from the start.\n", c.Context.Where(), why)
	w.Clear()
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

func number(id string) int64 {
	_, n, _ := strings.Cut(id, ":")
	v, _ := strconv.ParseInt(n, 10, 64)
	return v
}
