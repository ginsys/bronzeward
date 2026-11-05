package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// followEvery is how long machine timeline --follow waits between its reads: the timeline has no
// stream, and a client polls it no more often than every 5 s (client-contracts.md §8.6 item 6).
// Tests shorten it.
var followEvery = 5 * time.Second

// followRevision is the lowest API revision whose timeline serves order and tail, which --follow
// reads. The plain read sends neither and works on any revision the client does.
const followRevision = 3

// machineTimeline reads a machine's timeline (decision T24 of #258): one page, or with --all every
// page, as every list does; with --follow the newest page, then each later entry as the server
// records it, until Ctrl-C. Each entry's line says what it records, by its kind.
func (e *env) machineTimeline(args []string) error {
	var l listing
	var follow bool
	pos, err := e.paged("machine timeline", args, 1, &l, func(fs *flag.FlagSet) { fs.BoolVar(&follow, "follow", false, "") })
	if err != nil {
		return err
	}
	path := "/machines/" + url.PathEscape(pos[0]) + "/timeline"
	if !follow {
		return list(e, l, path, url.Values{}, nil, "REVISION\tAT\tKIND\tENTRY", "", func(w *tabwriter.Writer, t *apitypes.TimelineEntry) {
			tf(w, "%d\t%s\t%s\t%s\n", t.Revision, when(t.At), t.Kind, timelineText(t.Kind, t.Entry))
		})
	}
	if l.all || l.cursor != "" {
		return usageError{errors.New("--follow starts at the newest entries: give no --cursor or --all")}
	}
	return e.session(func(c *client.Client, _ *client.Response, _ *apitypes.ServerBody) error {
		return e.followTimeline(c, path, l.limit, pos[0])
	})
}

// needFollowRevision refuses a server below followRevision before any request that sends order or
// reads tail (client-contracts.md §8.6 item 6; §7 item 1).
func needFollowRevision(c *client.Client) error {
	if c.Revision < followRevision {
		return ef("the server serves API revision %d; machine timeline --follow needs revision %d or later", c.Revision, followRevision)
	}
	return nil
}

// followTimeline is machine timeline --follow (client-contracts.md §8.6 item 6). It reads the newest page
// with order=newest and keeps its tail, then every followEvery reads forward from tail in oldest
// order, follows next while a page has one, and keeps the tail of the last page. A cursor the
// server refuses, as it does every cursor of an earlier epoch, means the entries shown may no
// longer hold: it says so and starts again from the newest page. Ctrl-C ends it with exit 130.
func (e *env) followTimeline(c *client.Client, path string, limit int, machine string) error {
	// The cursor goes in the query, not to Page, which would read a refused one's list from its
	// start instead of returning the refusal.
	read := func(order, cursor string) (*client.Page, error) {
		// A server below revision 3 refuses order and serves no tail (client-contracts.md §8.6 item
		// 6), so no request is sent to it (§7 item 1). A page can show a new epoch and a server of
		// another revision, so each request checks it again, as recovery status does.
		if err := needFollowRevision(c); err != nil {
			return nil, err
		}
		q := url.Values{"order": {order}}
		if limit != 0 {
			q.Set("limit", strconv.Itoa(limit))
		}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		return c.Page(path+"?"+q.Encode(), "")
	}
	if err := needFollowRevision(c); err != nil { // before the header: a refusal prints nothing
		return err
	}
	if e.output == "table" {
		tf(e.out, "%-8s  %-23s  %-20s  %s\n", "REVISION", "AT", "KIND", "ENTRY")
	}
	newest := func() (string, error) {
		pg, err := read("newest", "")
		if err != nil {
			return "", err
		}
		slices.Reverse(pg.Items)
		return pg.Tail, e.entries(c, pg.Items)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	stopped := func() error {
		signal.Stop(sig) // a second Ctrl-C ends the client at once
		tf(e.err, "Stopped following the timeline of machine %s at your request.\n", machine)
		return interrupted{ef("stopped following the timeline of machine %s", machine)}
	}
	// pause waits followEvery before the next read, and ends on Ctrl-C.
	pause := func() error {
		select {
		case <-sig:
			return stopped()
		case <-time.After(followEvery):
			return nil
		}
	}
	tail, err := newest()
	for err == nil {
		if err = pause(); err != nil {
			return err
		}
		for cursor, again := tail, false; ; again = true {
			if again { // a next page waits as the first read of the cycle did
				if err = pause(); err != nil {
					return err
				}
			}
			var pg *client.Page
			if pg, err = read("oldest", cursor); err != nil {
				if p, ok := err.(*client.Problem); ok && p.Code() == "cursor-invalid" {
					tf(e.err, "%s: the timeline's cursor is no longer valid: the installation was restored or entered recovery mode; "+
						"its newest entries are shown again\n", c.Context.Where())
					if e.output == "table" {
						fmt.Fprintln(e.out, "(the entries above may no longer hold; the newest are shown again)")
					}
					if err = pause(); err != nil { // the newest page is a read too
						return err
					}
					tail, err = newest()
				}
				break
			}
			if err = e.entries(c, pg.Items); err != nil || pg.Next == "" {
				tail = pg.Tail
				break
			}
			cursor = pg.Next
		}
	}
	// A read that fails while Ctrl-C is pending still ends with exit 130, as a stop does. Stop waits
	// for a delivery in flight, so the signal is on sig, which holds one, before the check.
	signal.Stop(sig)
	select {
	case <-sig:
		return stopped()
	default:
	}
	return err
}

// entries prints timeline entries as they arrive: in a table, a line each; with -o json each entry
// unchanged on its own line; with -o yaml each as its own document.
func (e *env) entries(c *client.Client, items []json.RawMessage) error {
	for _, raw := range items {
		switch e.output {
		case "table":
			ts, err := decode[apitypes.TimelineEntry](c, raw)
			if err != nil {
				return err
			}
			t := ts[0]
			tf(e.out, "%-8d  %-23s  %-20s  %s\n", t.Revision, when(t.At), t.Kind, timelineText(t.Kind, t.Entry))
		case "yaml":
			fmt.Fprintln(e.out, "---")
			fallthrough
		default:
			if err := e.emit(raw, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// timelineText says what a timeline entry records, by its kind (decision T24 of #258): a plan,
// its approval, an approval's revocation and a plan's cancellation by name, an operation's entry
// as its event says it with the operation first; any other kind prints its record as JSON.
func timelineText(kind string, entry any) string {
	raw, _ := json.Marshal(entry)
	var d map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // a number keeps its digits: float64 would round 9007199254740993
	_ = dec.Decode(&d)
	by := ""
	if p := field(d, "principal"); p != "" {
		by = "by " + p + " (" + field(d, "role") + ")"
	}
	join := func(parts ...string) string {
		var kept []string
		for _, p := range parts {
			if p != "" {
				kept = append(kept, p)
			}
		}
		return strings.Join(kept, ", ")
	}
	switch kind {
	case "plan":
		return join(fields(d, "plan ", "plan", "operation ", "operation", "release ", "release"), by)
	case "approval":
		self := "no self-approval reason"
		var s struct {
			Marked  bool     `json:"marked"`
			Reasons []string `json:"reasons"`
		}
		if b, err := json.Marshal(d["selfApproval"]); err == nil && json.Unmarshal(b, &s) == nil && s.Marked {
			self = "self-approval: " + plain(strings.Join(s.Reasons, ", "))
		}
		overwrite := ""
		if d["unseenOverwrite"] == true {
			overwrite = "overwrites a configuration no reviewer has seen"
		}
		return join(fields(d, "approval ", "approval", "plan ", "plan"), by, self, overwrite)
	case "approval-revocation":
		return join(fields(d, "approval ", "approval", "plan ", "plan"), by, fields(d, "reason ", "reason"))
	case "plan-cancellation":
		return join(fields(d, "plan ", "plan"), by, fields(d, "reason ", "reason"))
	}
	if text, known := eventFields(kind, d); known {
		return join(fields(d, "operation ", "operation"), text)
	}
	return eventText(kind, d, raw)
}
