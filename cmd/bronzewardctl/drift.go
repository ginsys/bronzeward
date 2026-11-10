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
// drift, adopt and revert end it. It offers no ignore, retry or mark-successful verb.
func (e *env) driftCmd(args []string) error {
	v, rest, err := e.verb("drift", args, "show", "adopt", "revert")
	if err != nil {
		return err
	}
	switch v {
	case "show":
		return e.driftShow(rest)
	case "adopt":
		return e.driftAdopt(rest)
	}
	return e.driftRevert(rest)
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
			// The count of moves tells a move that came back from none: the epoch of the last answer
			// cannot (client-contracts.md §8.5).
			if c.EpochMoves() != r.Moves {
				now := c.Context.LastEpoch
				if now == first {
					return fmt.Errorf("the epoch moved and came back to %s while the drift was read; nothing is shown. Read it again with: drift show %s",
						client.ShellWord(first), client.ShellWord(id))
				}
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
				"Unfreezing approves and commits nothing. An attempt already recorded was not stopped by the freeze, and an adoption could still be recorded."}
			if x.Frozen {
				// "An attempt ..." is freezeLimits with its capital.
				lines = []string{"Frozen. No new commitment or attempt for this machine until an approver unfreezes it.", "An" + freezeLimits[2:] + "."}
			}
			return append(lines, sf("Recorded by %s (%s): machine %s, timeline revision %d, act %s.", x.Principal, x.Role, x.Machine, x.Revision, x.Act)), nil
		})
	return whyBlocked(err, verb)
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
// why held scopes block changes). verb is "freeze" or "unfreeze". The same code carries the scope's
// refusal, which has a scope member, and the process's, which has none (client-contracts.md §8.2);
// problem.go prints which one it is, and this adds why. Any other error is returned as it is.
//
// The server refuses a freeze for no scope, and an unfreeze only while the scope is
// pre-restore-unaccounted (internal/api/freeze.go:72), so that is the one scope with a reason of
// the client's own. The process's refusal is the same code before and after the recovery entry, so
// its text covers both: a normal restart is allowed only once the entry has committed
// (execution-recovery.md §7.2).
func whyBlocked(err error, verb string) error {
	var p *client.Problem
	if err == nil || !errors.As(err, &p) || p.Code() != "recovery-mode-active" {
		return err
	}
	if scope, _ := p.Doc["scope"].(string); scope != "" {
		more := []string{"Next: recovery status names the next step of each scope."}
		if verb == "unfreeze" && scope == "pre-restore-unaccounted" {
			more = append([]string{"Why: a restore can erase the record of a request sent to this machine, and a request sent before the restore may still land. " +
				"Until a recovery administrator accounts for the scope, unfreezing is refused."}, more...)
		}
		return &client.Blocked{Err: err, More: more}
	}
	return &client.Blocked{Err: err, More: []string{
		"Why: a server started for recovery refuses every request but the recovery entry until the entry is recorded. After the entry it still refuses unfreezing, plan creation and approval.",
		"Next: if the recovery entry is not recorded yet, record it with recovery enter. Then restart the server without the recovery flag and repeat this command."}}
}

// driftRevert is T30: T19's plan create of the machine's Desired as apply-config, which binds the
// open drift record and expects its drifted digest before dispatch (internal/api/plans.go). It
// reads the machine and sends the plan create on one client, so the write is bound to the epoch of
// that read (§8.5). With no open drift record, no Desired or no Applied it refuses before sending,
// naming the plan create that fits. It takes no repeat flags: a repeat would read Desired again,
// which may have changed, so the outcome-unknown block names plan create with the release read.
func (e *env) driftRevert(args []string) (err error) {
	var rf repeatFlags
	pos, err := e.parse("drift revert", args, 1, rf.flags)
	if err != nil {
		return err
	}
	if len(rf.args()) > 0 {
		return usageError{errors.New("drift revert reads the machine's Desired release again, and it may have changed, so it cannot repeat a write; nothing was sent. " +
			"Repeat through plan create, with the release and the flags the outcome-unknown block printed")}
	}
	m := pos[0]
	c, err := e.open()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, c.Save()) }()
	if _, _, err := c.Server(); err != nil {
		return err
	}
	path := "/machines/" + url.PathEscape(m)
	r, err := c.Get(path)
	if err != nil {
		return err
	}
	var x apitypes.MachineBody
	if err := json.Unmarshal(r.Body, &x); err != nil {
		return fmt.Errorf("GET /api/v1%s cannot be read: %w", path, err)
	}
	// Without an Applied configuration the server refuses an apply-config plan (internal/api/plans.go),
	// so the machine is handed over with an adopt plan first.
	switch {
	case x.Desired == nil:
		op := "apply-config"
		if x.Applied == nil {
			op = "adopt"
		}
		return notSent{fmt.Errorf("machine %s has no Desired release to revert to; nothing was sent. Publish a release that covers it, then: %s",
			m, createWords("<release>", client.ShellWord(m), client.ShellWord(op)))}
	case x.Applied == nil:
		return notSent{fmt.Errorf("machine %s has no Applied configuration, so it has no drift to revert; nothing was sent. Hand it over with an adopt plan: %s",
			m, createLine(*x.Desired, m, "adopt"))}
	case x.OpenDrift == nil:
		return notSent{fmt.Errorf("machine %s has no open drift record; nothing was sent. To apply its Desired release without a revert: %s",
			m, createLine(*x.Desired, m, "apply-config"))}
	}
	req, err := e.planParse("drift revert", []string{"--release", *x.Desired, "--machine", m, "--operation", "apply-config"})
	if err != nil {
		return err
	}
	// The write is bound to this machine answer's own epoch and move count, not to the client's last
	// ones: a refresh inside the read can have moved them past it (§8.5).
	return e.planCreateOn(c, req, planOpts{extra: revertLines, bind: boundTo(c, r),
		input: "the command " + createLine(*x.Desired, m, "apply-config") + " in place of drift revert"})
}

// revertLines say what the created revert binds and what it needs before it can commit.
func revertLines(c *client.Client, p *apitypes.PlanBody) []string {
	if p.DriftRecord == nil {
		return []string{"This plan binds no drift record: the drift closed before it was created, so it is an ordinary apply-config, not a revert."}
	}
	lines := []string{
		sf("Revert of drift record %d to Desired %s. The plan expects the drifted digest before dispatch: if the machine drifts again first, it fails and must be made again.",
			*p.DriftRecord, p.Release),
		"Approving it approves an unseen overwrite: it replaces the drifted configuration, which no reviewer has seen.",
	}
	var m apitypes.MachineBody
	if err := getJSON(c, "/machines/"+url.PathEscape(p.Machine), &m); err != nil {
		return append(lines, "Whether the machine is frozen cannot be read: "+pr(err.Error()))
	}
	if m.Frozen {
		lines = append(lines, "The machine is frozen: the plan cannot commit until an approver approves it and, separately, unfreezes the machine: bronzewardctl machine unfreeze "+
			client.ShellWord(p.Machine)+" --reason <text>")
	}
	return lines
}

// driftAdopt is T29's composite over existing routes, its steps numbered as journeys §5.8: 1 the
// import of #349 as a drift-adoption (author, a person), 2 publish (publisher), 3 the adopt plan
// create (publisher), which binds the open drift record, then the approval (approver), which it
// never runs. It runs each step while the roles GET /identity serves hold that step's and the step
// before it succeeded. It stops at the first it cannot run, a missing role, an import paused for
// review or a step not succeeding, and prints that step and each later one as its command with its
// role. Its repeat flags repeat step 1; a later step is repeated by its own command.
func (e *env) driftAdopt(args []string) (err error) {
	var draft, staging string
	var review, marks bool
	var rf repeatFlags
	pos, err := e.parse("drift adopt", args, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&draft, "draft", "", "")
		fs.StringVar(&staging, "staging", "encrypted", "")
		fs.BoolVar(&review, "review", false, "")
		fs.BoolVar(&marks, "marks", false, "")
		rf.flags(fs)
	})
	if err != nil {
		return err
	}
	if draft == "" {
		return usageError{errors.New("drift adopt needs --draft")}
	}
	// The repeat flags and the staging and review rules of the import are checked before any role
	// guidance, so that a caller without the author role gets the usage error an author would get.
	if _, err := rf.repeat(true); err != nil {
		return err
	}
	if err := checkStaging(staging, review); err != nil {
		return err
	}
	m := pos[0]
	var id apitypes.IdentityBody
	if err := e.session(func(c *client.Client, _ *client.Response, _ *apitypes.ServerBody) error {
		return getJSON(c, "/identity", &id)
	}); err != nil {
		return err
	}
	has := func(role string) bool { return slices.Contains(id.Roles, apitypes.IdentityBodyRoles(role)) }
	from := func(n int, release string) []string {
		var l []string
		for i := n; i <= 3; i++ {
			l = append(l, adoptStep(i, m, draft, release)...)
		}
		return l
	}
	if !has("author") || id.Kind != "human" {
		return e.say(append([]string{"Not run: step 1 needs an author signed in as a person. The steps:"}, from(1, "")...)...)
	}
	importArgs := append([]string{"--machine", m, "--draft", draft, "--staging", staging}, rf.args()...)
	if review {
		importArgs = append(importArgs, "--review")
	}
	if marks {
		importArgs = append(importArgs, "--marks")
	}
	if err := e.say(fmt.Sprintf("Step 1/3  import (drift-adoption) of machine %s into draft %s", m, draft)); err != nil {
		return err
	}
	c, a, err := e.ingest("drift adopt", "drift-adoption", importArgs)
	if err != nil {
		return errors.Join(err, e.say(append([]string{"The later steps:"}, from(2, "")...)...))
	}
	// The read can show another epoch, which this client keeps, as ingest saved its own before it; a
	// failed read leaves the steps unrun, so they are printed.
	b, bind, err := readIngestionBound(c, a.Ingestion)
	saved := c.Save()
	if err != nil || saved != nil {
		// A failed read leaves the state unknown; a read that worked and a save that did not leaves it known.
		head := "The import was accepted, but its state is not known. Read it with: bronzewardctl import status " + pr(a.Ingestion)
		if err == nil {
			head = "The import was accepted and is " + pr(b.State) + ", but the context could not be saved, so no later step ran. Read it with: bronzewardctl import status " + pr(a.Ingestion)
		}
		return errors.Join(err, saved, e.say(append([]string{head, "The later steps, once it is released into the draft:"}, from(2, "")...)...))
	}
	if b.State != "released" {
		return e.say(append([]string{"Mark values while it is paused: bronzewardctl import mark " + client.ShellWord(b.Id),
			"Once the import is continued and released into the draft, the later steps:"}, from(2, "")...)...)
	}
	if !has("publisher") {
		return e.say(append([]string{"Not run: step 2 needs a publisher. The later steps:"}, from(2, "")...)...)
	}
	if err := e.say("Step 2/3  publish draft " + draft + ": it publishes the whole draft"); err != nil {
		return err
	}
	// The publication follows the read of the import, so it is bound to the epoch and move count of that
	// answer (§8.5): an epoch that changed, even if it came back, while the import was read means the
	// state read was given under another epoch. Nothing is sent then. The same client carries on, and
	// is saved once, at the end, so no earlier client writes an older epoch over it.
	defer func() { err = errors.Join(err, c.Save()) }()
	// An unknown outcome of this write names the publish command, never drift adopt: the repeat flags it
	// prints are this publication's, and drift adopt would give them to the import of step 1.
	release, err := e.publishDraftOn(c, bind, draft, nil, "", "the command bronzewardctl publish "+client.ShellWord(draft)+" in place of drift adopt", false)
	if err != nil {
		// A refusal the client makes because the epoch moved or the draft's ETag is gone sends nothing, so
		// step 2 runs again. Any other error, a failed preflight read included, leaves the outcome to the
		// block it carries, and names the step after it.
		if errors.As(err, new(client.NotSent)) {
			return errors.Join(err, e.say(append([]string{"Step 2 again:"}, from(2, "")...)...))
		}
		return errors.Join(err, e.say(append([]string{"The later step:"}, from(3, "")...)...))
	}
	if err := e.say("Step 3/3  plan an adoption of release " + pr(release)); err != nil {
		return err
	}
	// The plan is created on the publication's client, bound to the epoch and move count the publication
	// left it at: its terminal read stood (restoredRead), and the release comes from that read (§8.5).
	// One client is also saved once, at the end, so no earlier client writes an older epoch over it.
	req, err := e.planParse("drift adopt", []string{"--release", release, "--machine", m, "--operation", "adopt"})
	if err == nil {
		err = e.planCreateOn(c, req, planOpts{extra: adoptLines, bind: &epochBind{epoch: c.Context.LastEpoch, moves: c.EpochMoves()},
			input: "the command " + createLine(release, m, "adopt") + " in place of drift adopt"})
	}
	if err != nil {
		return errors.Join(err, e.say(append([]string{"Step 3 again:"}, from(3, release)...)...))
	}
	return nil
}

// adoptStep is drift adopt's step n as its commands, each with the role it needs; release is ""
// until step 2 names it.
func adoptStep(n int, m, draft, release string) []string {
	// A release not yet named is a bare placeholder, which the reader replaces; a named one is quoted.
	word := "<release>"
	if release != "" {
		word = client.ShellWord(release)
	}
	switch n {
	case 1:
		return []string{"Step 1/3  import (drift-adoption): an author, signed in as a person.",
			"          bronzewardctl drift adopt " + client.ShellWord(m) + " --draft " + client.ShellWord(draft) + " --review"}
	case 2:
		return []string{"Step 2/3  publish, which publishes the whole draft: a publisher.", "          bronzewardctl publish " + client.ShellWord(draft)}
	}
	return []string{"Step 3/3  plan an adoption: a publisher, then an approver.", "          " + createWords(word, client.ShellWord(m), client.ShellWord("adopt")),
		"          bronzewardctl approve <plan>"}
}

// adoptLines say what the created adopt plan binds and what its approval leads to; the approval
// itself is always left to an approver.
func adoptLines(_ *client.Client, p *apitypes.PlanBody) []string {
	bound := "This plan binds no drift record."
	if p.DriftRecord != nil {
		bound = fmt.Sprintf("This plan binds drift record %d.", *p.DriftRecord)
	}
	return []string{bound + " drift adopt never approves: an approver runs bronzewardctl approve " + client.ShellWord(p.Id),
		"Approval leads the controller to record the adoption without sending anything, even while the machine is frozen.",
		"The adopted base holds each marked value as a reference: a fragment that overrides such a value refuses publication, so stop selecting it."}
}
