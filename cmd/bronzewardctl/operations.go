package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// notTerminal is an operation still not terminal when operation watch's --timeout ends: exit 5
// (decision T23 of #258).
type notTerminal struct{ error }

// interrupted is operation watch stopped by Ctrl-C: exit 130. The operation goes on.
type interrupted struct{ error }

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

// eventLine is one event's line: the time this client received it (now), marked as such, its type
// and what its data says. An unknown type prints its data raw.
func eventLine(ev client.Event, now time.Time) string {
	stamp := "received " + now.Format("15:04:05")
	var d map[string]any
	_ = json.Unmarshal(ev.Data, &d)
	return sf("%s  %-20s  %s", stamp, plain(ev.Type), eventText(ev.Type, d, ev.Data))
}

// plain is text the server chose, escaped so that none reaches the terminal as itself
// (client-contracts.md §6 item 8).
func plain(s string) string { return client.Printable(s) }

// eventText says what an event's data holds, by its type.
func eventText(typ string, d map[string]any, raw json.RawMessage) string {
	s := func(k string) string {
		if v, ok := d[k]; ok && v != nil {
			return plain(fmt.Sprint(v))
		}
		return ""
	}
	join := func(parts ...string) string {
		var kept []string
		for i := 0; i+1 < len(parts); i += 2 {
			if v := s(parts[i+1]); v != "" {
				kept = append(kept, parts[i]+v)
			}
		}
		return strings.Join(kept, ", ")
	}
	switch typ {
	case "commitment":
		return join("plan ", "plan", "approval ", "approval", "observation ", "observation", "controller ", "controller", "generation ", "generation")
	case "attempt":
		return join("attempt ", "number", "route ", "route", "transport deadline ", "transportDeadline", "controller ", "controller", "generation ", "generation")
	case "refusal":
		return join("plan ", "plan", "comparison ", "comparison", "cause ", "cause", "observation ", "observation")
	case "use-time-check":
		return join("result ", "result", "plan ", "plan", "controller ", "controller")
	case "response":
		return join("attempt ", "number", "class ", "class", "code ", "code")
	case "accounting":
		return join("attempt ", "attempt", "basis ", "basis", "class ", "class", "decided by ", "decidedBy", "role ", "role")
	case "classification":
		return join("outcome ", "outcome", "observation ", "observation", "controller ", "controller", "generation ", "generation")
	case "ownership-transition":
		return join("from ", "from", "to ", "to", "generation ", "generation", "reason ", "reason", "requested by ", "requestedBy")
	case "operation-state":
		return join("from ", "from", "to ", "to", "comparison ", "comparison", "cause ", "cause", "decided by ", "decidedBy")
	case "adoption":
		return join("plan ", "plan", "approval ", "approval", "observation ", "observation", "controller ", "controller")
	case "applied-change":
		return join("from ", "from", "to ", "to", "source ", "source")
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // a number keeps its digits: float64 would round 9007199254740993
	if dec.Decode(&v) != nil || dec.More() {
		return plain(string(raw))
	}
	return compact(v) // JSON escapes every control character
}
