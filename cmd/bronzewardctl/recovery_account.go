package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// The two accountings of T33 (#362; operator-experience.md §5.9, §5.7): recovery scope-account
// accounts every unaccounted attempt on a machine after a restore, and recovery account accounts
// one attempt whose response was lost. Both are the operator's judgment, made over two
// observations of the machine; the server checks the settle (internal/api/settle.go) and the
// client shows the window and the risk first, refuses what it can read for itself, and sends only
// when the machine ID is typed back.

// maxExplanation is the longest value of a facts member (checkFacts in the server).
const maxExplanation = 1024

// accountRisk follows the window: what the judgment is, and what it does not show.
const accountRisk = "This records your judgment that %s can still land on this machine. " +
	"It may already have landed; nothing here shows whether it did.\n" +
	"The settle floor is a minimum wait, not a proven bound: a request landing after this can overwrite work " +
	"admitted once the scope is released, and would be seen only as drift at a later observation."

// settleCauses are the server's named refusals of a settle (internal/api/settle.go), each with the
// one next step the operator can take. A cause matches by the text its detail holds.
var settleCauses = []struct{ cause, next string }{
	{"is not the first successful read of the machine after the transport deadline",
		"Next: give the first read of the machine after that deadline that read a node value as --start: machine observations %[1]s."},
	{"is not a recovery observation",
		"Next: give a recovery observation as --end. Bronzeward takes one while the server runs for recovery: machine observations %[1]s."},
	{"was not read at both ends of the settle",
		"Next: give observations that both read the machine-configuration resource version: machine observations %[1]s."},
	{"did not begin after the start observation was recorded",
		"Next: give an --end observation that began after --start was recorded: machine observations %[1]s."},
	{"began before the settle floor of",
		"Next: wait until the floor has passed since the time named, wait for a recovery observation that begins after it, then repeat with that observation as --end: machine observations %[1]s."},
	{"changed during the settle and no explanation is recorded",
		"Next: repeat this command with --explanation <text> that says why the version changed."},
	{"is already accounted for",
		"Next: nothing to record. The accounting is on the machine's timeline: machine timeline %[1]s."},
	{"executor is not stopped or taken over",
		"Next: stop the executor or take the operation over, then repeat this command: recovery takeover %[2]s."},
	{"not pre-restore unaccounted",
		"Next: recovery status names the state and the next step of the scope."},
}

// settleNext adds the next step of a settle refusal to err. machine and operation fill the steps.
func settleNext(err error, machine, operation string) error {
	var p *client.Problem
	if !errors.As(err, &p) || p.Code() != "conflict" {
		return err
	}
	detail, _ := p.Doc["detail"].(string)
	for _, c := range settleCauses {
		if strings.Contains(detail, c.cause) {
			return &client.Blocked{Err: err, More: []string{fmt.Sprintf(c.next, client.ShellWord(machine), client.ShellWord(operation))}}
		}
	}
	return err
}

// shortDuration is d as the contract writes it: 30s, 2m.
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	return s
}

// servedSeconds is a duration the server served in seconds.
func servedSeconds(s float32) time.Duration { return time.Duration(float64(s) * float64(time.Second)) }

// settleFlags checks the flags both accountings take before anything is read or sent.
func settleFlags(start, end, explanation string, noInput bool, typed string) error {
	switch {
	case start == "" || end == "":
		return usageError{errors.New("give --start and --end, the observation IDs the settle runs between")}
	case len(explanation) > maxExplanation:
		return usageError{fmt.Errorf("--explanation: give at most %d bytes; nothing was sent", maxExplanation)}
	case noInput:
		return notConfirmed{fmt.Errorf("this asks for %s typed back, which --no-input forbids; nothing was sent", typed)}
	}
	return nil
}

// machineObservations reads every observation of machine, by ID.
func machineObservations(c *client.Client, machine string) (map[string]apitypes.ObservationBody, error) {
	items, err := c.List("/machines/" + url.PathEscape(machine) + "/observations")
	if err != nil {
		return nil, err
	}
	obs, err := decode[apitypes.ObservationBody](c, items...)
	if err != nil {
		return nil, err
	}
	by := make(map[string]apitypes.ObservationBody, len(obs))
	for _, o := range obs {
		by[o.Id] = o
	}
	return by, nil
}

// checkSettle refuses, sending nothing, what the observations show for themselves: an observation
// of another machine, an end that is not a recovery observation, a version not read at an end, and
// a version that moved without an explanation. The server states the rest (settleCauses).
func checkSettle(machine string, by map[string]apitypes.ObservationBody, start, end, explanation string) error {
	refuse := func(format string, args ...any) error {
		return usageError{ef(format+"; nothing was sent", args...)}
	}
	s, ok := by[start]
	if !ok {
		return refuse("--start %s: no such observation of machine %s; machine observations %s lists them", start, machine, client.ShellWord(machine))
	}
	e, ok := by[end]
	switch {
	case !ok:
		return refuse("--end %s: no such observation of machine %s; machine observations %s lists them", end, machine, client.ShellWord(machine))
	case start == end:
		return refuse("--start and --end are the same observation, %s; the settle runs between two", start)
	case e.Purpose != "recovery":
		return refuse("--end %s: its purpose is %s, not recovery; the end must be a recovery observation", end, e.Purpose)
	case s.ResourceVersion == nil:
		return refuse("--start %s: it read no machine-configuration resource version, so it is unusable at either end", start)
	case e.ResourceVersion == nil:
		return refuse("--end %s: it read no machine-configuration resource version, so it is unusable at either end", end)
	case *s.ResourceVersion != *e.ResourceVersion && strings.TrimSpace(explanation) == "":
		return refuse("the resource version moved from %s to %s between the two observations: give --explanation <text> that says why", *s.ResourceVersion, *e.ResourceVersion)
	}
	return nil
}

// observationLine shows one observation of the window.
func observationLine(label string, o apitypes.ObservationBody) string {
	version := "none"
	if o.ResourceVersion != nil {
		version = *o.ResourceVersion
	}
	return sf("  %-6s %s, purpose %s, began %s, recorded %s, resource version %s", label+":", o.Id, o.Purpose, when(o.StartedAt), when(o.At), version)
}

// settleFacts is the line for the facts a settle recorded, `settleFloor` and `anchor`. The answer
// of the write carries neither, so they are read from the timeline entry at the answered revision.
func settleFacts(c *client.Client, machine, kind string, revision int) string {
	const none = "The settle floor and its anchor are on the machine's timeline: machine timeline %s."
	if c.Revision < 3 {
		return sf(none, client.ShellWord(machine))
	}
	pg, err := c.PageIf("/machines/"+url.PathEscape(machine)+"/timeline?order=newest&limit=100", "", func() bool { return c.Revision >= 3 })
	if err != nil {
		return sf(none, client.ShellWord(machine))
	}
	entries, err := decode[apitypes.TimelineEntry](c, pg.Items...)
	if err != nil {
		return sf(none, client.ShellWord(machine))
	}
	for _, t := range entries {
		x, _ := t.Entry.(map[string]any)
		facts, _ := x["facts"].(map[string]any)
		if t.Kind != kind || t.Revision != revision || facts == nil {
			continue
		}
		return sf("Settle floor %v, counted from %v.", facts["settleFloor"], facts["anchor"])
	}
	return sf(none, client.ShellWord(machine))
}

// newestEntry is what GET /machines/{id}/timeline shows last, for the block of an unknown outcome.
func newestEntry(machine string) func(*client.Client) (string, error) {
	return func(c *client.Client) (string, error) {
		if c.Revision < 3 {
			return "the timeline cannot be read newest first on this server", nil
		}
		pg, err := c.Page("/machines/"+url.PathEscape(machine)+"/timeline?order=newest&limit=1", "")
		if err != nil {
			return "", err
		}
		entries, err := decode[apitypes.TimelineEntry](c, pg.Items...)
		if err != nil || len(entries) == 0 {
			return "no timeline entry", err
		}
		return sf("the newest timeline entry of machine %s is %s, revision %d", machine, entries[0].Kind, entries[0].Revision), nil
	}
}

// factsOf is the facts a decision sends: the explanation, when there is one.
func factsOf(explanation string) *map[string]string {
	if strings.TrimSpace(explanation) == "" {
		return nil
	}
	return &map[string]string{"explanation": explanation}
}

// recoveryScopeAccount is T33's scope accounting (POST /recovery/accountings, recovery-admin,
// human only, one scope): it accounts every unaccounted attempt on the machine at once, and is
// refused on a scope that is not pre-restore unaccounted.
func (e *env) recoveryScopeAccount(args []string) error {
	var start, end, explanation string
	var rf repeatFlags
	pos, err := e.parse("recovery scope-account", args, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&start, "start", "", "")
		fs.StringVar(&end, "end", "", "")
		fs.StringVar(&explanation, "explanation", "", "")
		rf.flags(fs)
	})
	if err != nil {
		return err
	}
	machine := pos[0]
	if err := settleFlags(start, end, explanation, e.noInput, "the machine ID"); err != nil {
		return err
	}
	rp, err := rf.repeat(false)
	if err != nil {
		return err
	}
	w := &client.Write{Method: http.MethodPost, Path: "/recovery/accountings", HumanOnly: true, Repeat: rp,
		Check: "machine timeline " + client.ShellWord(machine), Read: newestEntry(machine)}
	prep := func(c *client.Client) error {
		r, err := c.Get("/server")
		if err != nil {
			return err
		}
		srvs, err := decode[apitypes.ServerBody](c, r.Body)
		if err != nil {
			return err
		}
		r, err = c.Get("/recovery")
		if err != nil {
			return err
		}
		recs, err := decode[apitypes.RecoveryBody](c, r.Body)
		if err != nil {
			return err
		}
		if recs[0].Entry == nil {
			return ef("GET /api/v1/recovery shows no recovery entry on %s; nothing was sent. Enter recovery first: recovery enter", c.Context.Where())
		}
		by, err := machineObservations(c, machine)
		if err != nil {
			return err
		}
		if err := checkSettle(machine, by, start, end, explanation); err != nil {
			return err
		}
		srv := srvs[0]
		tf(e.err, "Scope accounting of machine %s in %s\n  restore stopped: %s\n", machine, c.Context.Where(), when(recs[0].Entry.StoppedAt))
		tf(e.err, "  Start must be the first successful read after the latest of that stop plus the maximum transport deadline and any restored unaccounted attempt's deadline.\n")
		tf(e.err, "  End must be a recovery observation begun at least the settle floor after the latest of that point and the start.\n")
		tf(e.err, "  Settle floor %s, maximum transport deadline %s, as GET /server serves them.\n", shortDuration(servedSeconds(srv.SettleFloorSeconds)),
			shortDuration(servedSeconds(srv.MaxTransportDeadlineSeconds)))
		fmt.Fprintln(e.err, observationLine("start", by[start]))
		fmt.Fprintln(e.err, observationLine("end", by[end]))
		tf(e.err, "  Explanation: %s\n", orNone(explanation))
		tf(e.err, accountRisk+"\n", "any request sent before the restore, from any attempt the restored journal shows or one it lost,")
		body, _ := json.Marshal(apitypes.ScopeAccountingRequest{Scopes: []apitypes.ScopeSettle{{Machine: machine, Start: start, End: end}}, Facts: factsOf(explanation)})
		// The decision is sent only under the epoch of the reads the operator saw (§8.5): Send refuses
		// a first send once its own preflight read shows another.
		w.Body, w.Reads = body, c.Context.LastEpoch
		return e.confirmTyped("the machine ID", machine)
	}
	err = e.writeAfter(prep, w, func(c *client.Client, resp *client.Response) ([]string, error) {
		var x apitypes.ScopeAccountingBody
		if err := json.Unmarshal(resp.Body, &x); err != nil || len(x.Scopes) != 1 {
			return nil, errors.Join(err, errors.New("the answer names no single scope"))
		}
		s := x.Scopes[0]
		held := "blocked until it is marked"
		next := "Next: recovery mark " + client.ShellWord(machine) + "."
		if s.State == "unresolved" {
			held = "unresolved: a restored operation still holds it"
			next = "Next: recovery status names what that operation needs."
		}
		lines := []string{sf("Recorded. Scope %s is now %s.", s.Machine, held), sf("Attempts accounted: %s.", shownIDs(s.Attempts))}
		if e.output != "table" {
			return lines, nil
		}
		return append(lines, settleFacts(c, machine, "scope-state", s.Revision), sf("Recorded by %s (%s): act %s.", x.DecidedBy, x.Role, x.Act), next), nil
	})
	return settleNext(err, machine, "")
}

// recoveryAccount is T33's accounting of one attempt (POST /operations/{id}/attempts/{attempt}/
// accountings, recovery-admin, human only). It changes no state: it records that the attempt is
// accounted for, so the controller or an operator can go on.
func (e *env) recoveryAccount(args []string) error {
	var start, end, explanation string
	var rf repeatFlags
	pos, err := e.parse("recovery account", args, 2, func(fs *flag.FlagSet) {
		fs.StringVar(&start, "start", "", "")
		fs.StringVar(&end, "end", "", "")
		fs.StringVar(&explanation, "explanation", "", "")
		rf.flags(fs)
	})
	if err != nil {
		return err
	}
	operation, attempt := pos[0], pos[1]
	if err := settleFlags(start, end, explanation, e.noInput, "the machine ID"); err != nil {
		return err
	}
	rp, err := rf.repeat(false)
	if err != nil {
		return err
	}
	var machine string
	w := &client.Write{Method: http.MethodPost, Path: "/operations/" + url.PathEscape(operation) + "/attempts/" + url.PathEscape(attempt) + "/accountings",
		HumanOnly: true, Repeat: rp, Check: "operation show " + client.ShellWord(operation)}
	prep := func(c *client.Client) error {
		r, err := c.Get("/operations/" + url.PathEscape(operation))
		if err != nil {
			return err
		}
		ops, err := decode[apitypes.OperationBody](c, r.Body)
		if err != nil {
			return err
		}
		op := ops[0]
		if op.Subject == nil || op.Subject.Machine == nil {
			return ef("operation %s has no machine: only an apply-config operation has attempts to account; nothing was sent", operation)
		}
		machine = *op.Subject.Machine
		var at *apitypes.AttemptBody
		attempts := deref0(op.Attempts)
		for i := range attempts {
			if attempts[i].Id == attempt {
				at = &attempts[i]
			}
		}
		if at == nil {
			return usageError{ef("operation %s has no attempt %s; operation show %s lists its attempts; nothing was sent", operation, attempt, client.ShellWord(operation))}
		}
		r, err = c.Get("/server")
		if err != nil {
			return err
		}
		srvs, err := decode[apitypes.ServerBody](c, r.Body)
		if err != nil {
			return err
		}
		by, err := machineObservations(c, machine)
		if err != nil {
			return err
		}
		if err := checkSettle(machine, by, start, end, explanation); err != nil {
			return err
		}
		srv := srvs[0]
		tf(e.err, "Accounting of attempt %s of operation %s on machine %s in %s\n", attempt, operation, machine, c.Context.Where())
		tf(e.err, "  attempt %d, generation %d, transport deadline %s\n", at.Number, at.Generation, when(at.TransportDeadline))
		tf(e.err, "  Start must be the first successful read after that deadline; end must be a recovery observation begun at least the settle floor after the latest of the deadline, any takeover and the start.\n")
		tf(e.err, "  Settle floor %s, as GET /server serves it.\n", shortDuration(servedSeconds(srv.SettleFloorSeconds)))
		fmt.Fprintln(e.err, observationLine("start", by[start]))
		fmt.Fprintln(e.err, observationLine("end", by[end]))
		tf(e.err, "  Explanation: %s\n", orNone(explanation))
		tf(e.err, accountRisk+"\n", "the request this attempt sent, whose response was lost,")
		body, _ := json.Marshal(apitypes.AccountingRequest{Start: start, End: end, Facts: factsOf(explanation)})
		// Sent only under the epoch of the reads the operator saw (§8.5), as for the scope accounting.
		w.Body, w.Reads = body, c.Context.LastEpoch
		w.Read = newestEntry(machine)
		return e.confirmTyped("the machine ID", machine)
	}
	err = e.writeAfter(prep, w, func(c *client.Client, resp *client.Response) ([]string, error) {
		var x apitypes.AccountingBody
		err := json.Unmarshal(resp.Body, &x)
		lines := []string{sf("Recorded. Attempt %s of operation %s is accounted for by decision.", x.Attempt, x.Operation),
			"It changes no state: the operation stays as it was."}
		if e.output != "table" {
			return lines, err
		}
		return append(lines, settleFacts(c, machine, "accounting", x.Revision), sf("Recorded by %s (%s): act %s.", x.DecidedBy, x.Role, x.Act),
			"Next: operation show "+client.ShellWord(operation)+"."), err
	})
	return settleNext(err, machine, operation)
}

// deref0 is the attempts of an operation body, none when it holds none.
func deref0(a *[]apitypes.AttemptBody) []apitypes.AttemptBody {
	if a == nil {
		return nil
	}
	return *a
}

// orNone is s, or "none" for a blank one.
func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "none"
	}
	return s
}
