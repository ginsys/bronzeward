package main

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// outcome is an operation's outcome lines (T23) and whether it ended well: completed or succeeded.
// last, the event that records the operation's end when read from the JSON pages, gives the server's time and a
// cancellation's cause, since a cancelled operation records neither result nor error. Only a
// completed apply-config with an applied release is reported as applied, and only an adoption as
// adopted.
func outcome(o *apitypes.OperationBody, last *lastEvent) ([]string, bool) {
	stamp := ""
	if last != nil && last.At != nil {
		stamp = " at " + when(last.At.Local()) + " (server)"
	}
	plan, machine := "", ""
	if o.Subject != nil {
		plan, machine = or(o.Subject.Plan, ""), or(o.Subject.Machine, "")
	}
	state := strings.ToUpper(o.State[:min(1, len(o.State))]) + o.State[min(1, len(o.State)):]
	switch o.State {
	case "completed":
		var r struct {
			AppliedRelease string `json:"appliedRelease"`
			Observation    string `json:"observation"`
			AdoptionRecord string `json:"adoptionRecord"`
		}
		_ = remarshal(o.Result, &r)
		switch {
		case r.AppliedRelease != "":
			return []string{sf("Completed%s: applied %s, confirmed by observation %s.", stamp, r.AppliedRelease, or(&r.Observation, "none"))}, true
		case r.AdoptionRecord != "":
			return []string{sf("Completed%s: adopted; the adoption of plan %s records Applied from the node's own bytes. Nothing was sent to the machine.",
				stamp, plain(r.AdoptionRecord))}, true
		}
		return []string{sf("Completed%s, with no applied release recorded: %s", stamp, compact(o.Result))}, true
	case "succeeded":
		return []string{sf("Succeeded%s: %s", stamp, compact(o.Result))}, true
	case "failed", "rejected", "cancelled":
		var e struct {
			Cause    string `json:"cause"`
			Attempts *int   `json:"attempts"`
			Title    string `json:"title"`
			Detail   string `json:"detail"`
			Instance string `json:"instance"`
		}
		_ = remarshal(o.Error, &e)
		if e.Cause == "" && last != nil && last.Type == "operation-state" {
			var s struct{ To, Cause string }
			if json.Unmarshal(last.Data, &s) == nil && s.To == o.State {
				e.Cause = s.Cause
			}
		}
		// A failed publish or ingest operation records the problem that ended it. The client shows its title,
		// its detail when present and its instance (client-contracts.md §8.2), each escaped, and the cause
		// of an apply-config first.
		var parts []string
		for _, p := range []string{e.Cause, e.Title, e.Detail} {
			if p = plain(p); p != "" && !slices.Contains(parts, p) {
				parts = append(parts, p)
			}
		}
		cause := strings.Join(parts, ": ")
		if cause == "" {
			cause = "no cause in the operation; its machine timeline holds the entry that ended it"
		}
		if e.Instance != "" {
			cause += sf(" (instance %s)", e.Instance)
		}
		attempts := ""
		if e.Attempts != nil {
			attempts = sf(", after %d attempt%s", *e.Attempts, map[bool]string{false: "s"}[*e.Attempts == 1])
		}
		lines := []string{sf("%s%s: %s%s.", state, stamp, cause, attempts)}
		return append(lines, endNextStep(o.State, o.Id, plan, machine)), false
	case "unresolved":
		return []string{unresolvedGuidance(o.Attempts)}, false
	}
	return []string{sf("Operation %s is %s.", o.Id, o.State)}, false
}

// endNextStep is the next safe step after an operation that ended without success.
func endNextStep(state, op, plan, machine string) string {
	switch {
	case plan == "":
		return sf("Read it: bronzewardctl operation show %s", client.ShellWord(op))
	case state == "rejected":
		return sf("The node refused the configuration as invalid, so it applied nothing. Review the plan's release before a new plan: bronzewardctl plan show %s", client.ShellWord(plan))
	case state == "cancelled":
		return sf("Nothing more is sent for this plan. The plan names the command for a new one: bronzewardctl plan show %s", client.ShellWord(plan))
	}
	return sf("Nothing more is sent for this plan. Read what the machine runs before a new plan: bronzewardctl machine show %s", client.ShellWord(machine))
}

// remarshal decodes v, an API union held as a generated type, into out.
func remarshal(v, out any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// plain is text the server chose, escaped so that none reaches the terminal as itself
// (client-contracts.md §6 item 8).
func plain(s string) string { return client.Printable(s) }

// recordsEnd is whether ev records the operation's end in state, by the event types the server writes
// for each kind of operation that ends:
//   - apply-config: an operation-state event whose data.to is the state (internal/api/outcome.go:114,
//     attempt.go:441, recovery_acts.go:300).
//   - adopt: the operation is inserted completed together with its adoption event
//     (internal/api/adoption.go:293 and :299); the applied-change event after it does not end it.
//   - publish and ingest: an event named after the state, succeeded or failed (publish.go:158, :328,
//     :415, runner.go:445, :457, ingestions.go:428, recovery_entries.go:276, staging/sweep.go:145).
//
// An event recorded after the end, such as the response of an attempt a takeover left behind
// (outcome.go:292), records no end.
func recordsEnd(ev lastEvent, state string) bool {
	switch ev.Type {
	case "operation-state":
		var to struct {
			To string `json:"to"`
		}
		return json.Unmarshal(ev.Data, &to) == nil && to.To == state
	case "adoption":
		return state == "completed"
	case "succeeded", "failed":
		return ev.Type == state
	}
	return false
}

// lastEvent is the event that ends an operation as the JSON pages carry it, with the server's time.
type lastEvent struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	At   *time.Time      `json:"at"`
	Data json.RawMessage `json:"data"`
}

// readLast reads from the JSON pages the last event that records the operation's end in state
// (recordsEnd). It is nil when the pages or the operation cannot be read, when no event records the
// end, or when the event is not of the epoch of the operation read that follows:
// an installation restored meanwhile has other events, so the event's time is not the operation's. It
// is also nil when that read shows another state, or another Bronzeward-Epoch header, than the read
// the caller read (state, watched). The operation's own epoch member is the one it was created in
// and does not change in a restore, so it is not the epoch to compare. The outcome and the time
// printed together then belong to one operation.
func readLast(c *client.Client, id, state, watched string) *lastEvent {
	items, err := c.List("/operations/" + url.PathEscape(id) + "/events")
	if err != nil || len(items) == 0 {
		return nil
	}
	var ev lastEvent
	found := false
	for i := len(items) - 1; i >= 0 && !found; i-- {
		var x lastEvent
		found = json.Unmarshal(items[i], &x) == nil && recordsEnd(x, state)
		if found {
			ev = x
		}
	}
	if !found {
		return nil
	}
	o, epoch, err := c.OperationEpoch(id)
	if err != nil || !client.Terminal(o.State) || o.State != state || epoch == "" || epoch != watched || client.EpochOf(ev.ID) != epoch {
		return nil
	}
	return &ev
}
