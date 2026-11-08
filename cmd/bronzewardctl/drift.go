package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"text/tabwriter"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// What a freeze stops and does not stop (operator-experience.md §2.4 and §5.8): machine show,
// drift show and the answer of machine freeze all say it in these words.
const (
	freezeStops  = "no new commitment or attempt"
	freezeLimits = "an attempt already recorded is not stopped, and an adoption can still be recorded"
	// freezeSentence is the frozen status sentence of machine show and drift show.
	freezeSentence = "Frozen: " + freezeStops + " until an approver unfreezes; " + freezeLimits + "."
)

// driftCmd is the drift noun (operator-experience.md §5.8): drift show reads one machine's open
// drift. It offers no ignore, retry or mark-successful verb: a drift ends by adoption or revert.
func (e *env) driftCmd(args []string) error {
	_, rest, err := e.verb("drift", args, "show")
	if err != nil {
		return err
	}
	return e.driftShow(rest)
}

// driftShow prints a machine's open drift: its opening and last observation with their times, the
// late landing, the Applied release it is measured against and the choices (T27). The API serves
// no opening time (api/openapi.yaml OpenDriftBody), so the time shown is that of the observation
// the record names, read from the machine's observations; every page is read, as machine show does.
func (e *env) driftShow(args []string) error {
	pos, err := e.parse("drift show", args, 1, nil)
	if err != nil {
		return err
	}
	id := pos[0]
	return e.session(func(c *client.Client, _ *client.Response, _ *apitypes.ServerBody) error {
		r, err := c.Get("/machines/" + url.PathEscape(id))
		if err != nil {
			return err
		}
		ms, err := decode[apitypes.MachineBody](c, r.Body)
		if err != nil {
			return err
		}
		if e.output == "json" || e.output == "yaml" { // the resource alone; the sentences are the table's
			return e.emit(r.Body, nil)
		}
		m := ms[0]
		var obs []apitypes.ObservationBody
		if m.OpenDrift != nil {
			// The epoch of the machine answer itself: Get may have refreshed LastEpoch past it already.
			first := r.Header.Get("Bronzeward-Epoch")
			items, err := c.List("/machines/" + url.PathEscape(id) + "/observations")
			if err != nil {
				return err
			}
			if obs, err = decode[apitypes.ObservationBody](c, items...); err != nil {
				return err
			}
			if now := c.Context.LastEpoch; first != "" && now != first {
				return fmt.Errorf("the epoch changed from %s to %s while the drift was read; nothing is shown. Read it again with: drift show %s",
					client.ShellWord(first), client.ShellWord(now), client.ShellWord(id))
			}
		}
		return e.emit(r.Body, func(w *tabwriter.Writer) {
			for _, l := range driftLines(m, obs) {
				fmt.Fprintln(w, l)
			}
		})
	})
}

// noDriftWhy says why a machine with no open drift record cannot be adopted or reverted (row 1 of
// #260: "explain why unknown observations ... block changes"): an observation opens a record only
// when it differs from Applied.
func noDriftWhy(machine string) string {
	return sf("Why: only a configuration read that differs from Applied opens a drift record; a read that matches it, or one that could not be read, opens none. See: machine observations %s",
		client.ShellWord(machine))
}

// driftLines are the sentences of drift show for machine m, whose observations are obs.
func driftLines(m apitypes.MachineBody, obs []apitypes.ObservationBody) []string {
	d := m.OpenDrift
	if d == nil {
		s := []string{sf("No open drift on machine %s.", m.Id), noDriftWhy(m.Id)}
		if m.Frozen {
			s = append(s, freezeSentence)
		}
		return s
	}
	seen := func(what, id string) string {
		// Raw text: the one sf below escapes it once.
		s := what + " observation " + id
		if i := slices.IndexFunc(obs, func(o apitypes.ObservationBody) bool { return o.Id == id }); i >= 0 {
			return s + " at " + when(obs[i].At)
		}
		return s + " (its time was not read)"
	}
	late := "no"
	if d.LateLanding != nil {
		late = "yes, operation " + *d.LateLanding
	}
	s := []string{
		sf("Drift opened by%s, %s. Late landing: %s.", seen("", d.Observation), seen("last seen in", d.LastObservation), late),
		sf("A configuration read differed from what Bronzeward last verified: Applied %s.", appliedRelease(m)),
		"Bronzeward cannot show what changed until it is imported for adoption.",
		sf("Choose: freeze while you decide; adopt it into a release; or revert to Desired (%s).", deref(m.Desired)),
	}
	if m.Frozen {
		s = append(s, freezeSentence)
	}
	return s
}

func appliedRelease(m apitypes.MachineBody) string {
	if m.Applied == nil {
		return "none"
	}
	return m.Applied.Release
}

// machineFreeze records a freeze or an unfreeze (operator-experience.md §10: POST
// /machines/{id}/freezes for author, publisher, approver and recovery-admin; POST
// /machines/{id}/unfreezes for approver only). The reason is a flag of 1 to 1024 bytes
// (ReasonInput). An unfreeze that a scope or the process refuses says why.
func (e *env) machineFreeze(verb string, args []string) error {
	var reason given
	var rf repeatFlags
	pos, err := e.parse("machine "+verb, args, 1, func(fs *flag.FlagSet) {
		fs.Var(&reason, "reason", "")
		rf.flags(fs)
	})
	if err != nil {
		return err
	}
	if len(reason.v) < 1 || len(reason.v) > 1024 {
		return usageError{errors.New("--reason: give a reason of 1 to 1024 bytes; nothing was sent")}
	}
	rp, err := rf.repeat(false)
	if err != nil {
		return err
	}
	m := url.PathEscape(pos[0])
	body, _ := json.Marshal(apitypes.ReasonInput{Reason: reason.v})
	err = e.write(&client.Write{Method: http.MethodPost, Path: "/machines/" + m + "/" + verb + "s", Body: body, Repeat: rp,
		Check: "machine timeline " + client.ShellWord(pos[0]), Read: freezeRead(m)},
		func(_ *client.Client, r *client.Response) ([]string, error) {
			var x apitypes.FreezeBody
			if err := json.Unmarshal(r.Body, &x); err != nil {
				return nil, err
			}
			lines := []string{"Unfrozen. The freeze no longer stops a commitment or an attempt for this machine.",
				"Unfreezing approves and commits nothing."}
			if x.Frozen {
				// "An attempt ..." is freezeLimits with its capital.
				lines = []string{"Frozen. No new commitment or attempt for this machine until an approver unfreezes it.", "An" + freezeLimits[2:] + "."}
			}
			return append(lines, sf("Recorded by %s (%s): machine %s, timeline revision %d, act %s.", x.Principal, x.Role, x.Machine, x.Revision, x.Act)), nil
		})
	return whyBlocked(err, map[string]string{"freeze": "freezing", "unfreeze": "unfreezing"}[verb])
}

// freezeRead is the read that tells, after a freeze whose outcome is unknown, whether it landed.
// The machine's frozen flag cannot tell this write from another one's, so the newest timeline
// entry is shown with it, where the server serves the order (API revision 3).
func freezeRead(m string) func(*client.Client) (string, error) {
	return func(c *client.Client) (string, error) {
		path := "/machines/" + m
		var x apitypes.MachineBody
		if err := getJSON(c, path, &x); err != nil {
			return "", err
		}
		s := sf("GET /api/v1%s shows frozen %t", path, x.Frozen)
		if c.Revision < 3 {
			return s, nil
		}
		pg, err := c.Page(path+"/timeline?order=newest&limit=1", "")
		if err != nil {
			return "", err
		}
		es, err := decode[apitypes.TimelineEntry](c, pg.Items...)
		if err != nil || len(es) == 0 {
			return s, err
		}
		return s + sf("; its newest timeline entry is revision %d, %s at %s: %s", es[0].Revision, es[0].Kind, when(es[0].At), compact(es[0].Entry)), nil // s was escaped by its own sf
	}
}

// whyBlocked adds to a refusal by recovery mode the reason and the next step (row 1 of #260: say
// why held scopes block changes). change names what was refused, such as "unfreezing". The same
// code carries the scope's refusal, which has a scope member, and the process's, which has none
// (client-contracts.md §8.2); problem.go prints which one it is, and this adds why. Any other
// error is returned as it is.
func whyBlocked(err error, change string) error {
	var p *client.Problem
	if err == nil || !errors.As(err, &p) || p.Code() != "recovery-mode-active" {
		return err
	}
	if scope, _ := p.Doc["scope"].(string); scope != "" {
		why := sf("Why: the scope is %s, not released in this epoch, so %s is refused until a recovery administrator releases it.", scope, change)
		if scope == "pre-restore-unaccounted" {
			why = sf("Why: a restore can erase the record of a request sent to this machine, and a request sent before the restore may still land. Until a recovery administrator accounts for the scope and releases it, %s is refused.", change)
		}
		return &client.Blocked{Err: err, More: []string{why, "Next: recovery status names the next step of each scope."}}
	}
	return &client.Blocked{Err: err, More: []string{
		"Why: a server started for recovery refuses unfreezing, plan creation and approval, even after its recovery entry.",
		"Next: restart the server without the recovery flag. Recovery mode continues. Then repeat this command."}}
}
