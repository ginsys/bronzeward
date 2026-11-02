package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// These are #258's plan commands (ginsys/bronzeward#352): plan create (decision T19), the plan
// display that plan create, plan show and approve print (T20), and plan cancel (T22).

func (e *env) planCmd(args []string) error {
	if v, rest, err := e.verb("plan", args, "list", "show", "create", "cancel"); err != nil {
		return err
	} else if v == "create" {
		return e.planCreate(rest)
	} else if v == "cancel" {
		return e.planCancel(rest)
	}
	var l listing
	var state, machine string
	f := map[string]*string{"state": &state, "machine": &machine}
	v, pos, err := e.noun("plan", args, &l, filters(f))
	if err != nil {
		return err
	}
	if v == "list" {
		return list(e, l, "/plans", values(f), nil, "PLAN\tSTATE\tMACHINE\tOPERATION\tRELEASE\tEXPIRES", "", func(w *tabwriter.Writer, p *apitypes.PlanBody) {
			tf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", p.Id, p.State, p.Machine, p.Operation, p.Release, when(p.ExpiresAt))
		})
	}
	var lines []string
	return showThen(e, "/plans/"+url.PathEscape(pos[0]), func(c *client.Client, r *client.Response, p *apitypes.PlanBody) error {
		if e.output != "table" {
			return nil
		}
		var change string
		if lines, change = planSnapshot(c, r, p); change != "" {
			return fmt.Errorf("%s while the plan was read; nothing is shown. Read it again with: plan show %s", change, client.ShellWord(p.Id))
		}
		return nil
	}, func(w *tabwriter.Writer, _ *apitypes.PlanBody) { printLines(w, lines) })
}

// planSnapshot is planView of the plan answer r, or no lines and the epoch change when one falls
// between that answer and the view's last read: the view's reads then come from different
// snapshots, and its sentences could describe a state that never existed (client-contracts.md
// section 8.5). The plan answer's own epoch is compared, and so is the count of moves, so that an
// epoch changed and changed back is seen too.
func planSnapshot(c *client.Client, r *client.Response, p *apitypes.PlanBody) ([]string, string) {
	first, moves := r.Header.Get("Bronzeward-Epoch"), c.EpochMoves()
	lines := planView(c, p)
	now := c.Context.LastEpoch
	switch {
	case first != "" && now != first:
		return nil, sf("the epoch changed from %s to %s", first, now)
	case c.EpochMoves() != moves:
		return nil, sf("the epoch changed from %s and changed back", first)
	}
	return lines, ""
}

func printLines(w *tabwriter.Writer, lines []string) {
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
}

// seconds is a duration flag sent as whole seconds, and whether it was given.
type seconds struct{ n *int }

func (s *seconds) String() string { return "" }
func (s *seconds) Set(v string) error {
	d, err := time.ParseDuration(v)
	if err != nil || d < time.Second || d%time.Second != 0 {
		return fmt.Errorf("give a duration of whole seconds, at least 1s, such as 90s or 24h")
	}
	n := int(d / time.Second)
	s.n = &n
	return nil
}

// planCreate creates a plan (T19; POST /plans, publisher, no If-Match). An omitted value is left out
// of the body, so the server's configured default applies; --mode defaults to no-reboot for an
// apply-config plan. An adopt plan given a mode, a deadline or an attempt limit is refused before
// sending. The answer is shown through planView. A refusal names its next step, which it does not
// take.
func (e *env) planCreate(args []string) error {
	var in apitypes.PlanInput
	var operation, mode string
	var expires, age, validity, transport, verification seconds
	var attempts given
	var rf repeatFlags
	if _, err := e.parse("plan create", args, 0, func(fs *flag.FlagSet) {
		fs.StringVar(&in.ReleaseId, "release", "", "")
		fs.StringVar(&in.Machine, "machine", "", "")
		fs.StringVar(&operation, "operation", "", "")
		fs.StringVar(&mode, "mode", "", "")
		fs.Var(&expires, "expires-in", "")
		fs.Var(&age, "max-observation-age", "")
		fs.Var(&validity, "check-validity", "")
		fs.Var(&transport, "transport-deadline", "")
		fs.Var(&verification, "verification-deadline", "")
		fs.Var(&attempts, "max-attempts", "")
		rf.flags(fs)
	}); err != nil {
		return err
	}
	in.Operation = apitypes.PlanInputOperation(operation)
	switch {
	case in.ReleaseId == "" || in.Machine == "":
		return usageError{errors.New("plan create needs --release and --machine")}
	case !in.Operation.Valid():
		return usageError{errors.New("--operation: give apply-config or adopt")}
	case in.Operation == apitypes.PlanInputOperationAdopt && (mode != "" || transport.n != nil || verification.n != nil || attempts.set):
		return usageError{errors.New("an adopt plan takes no --mode, --transport-deadline, --verification-deadline or --max-attempts; nothing was sent")}
	case in.Operation == apitypes.PlanInputOperationApplyConfig:
		m := apitypes.PlanInputModeNoReboot
		if mode != "" && apitypes.PlanInputMode(mode) != m {
			return usageError{errors.New("--mode: an apply-config plan takes no-reboot, its only mode")}
		}
		in.Mode = &m
	}
	if attempts.set {
		n, err := strconv.Atoi(attempts.v)
		if err != nil || n < 1 {
			return usageError{errors.New("--max-attempts: give a whole number, at least 1")}
		}
		in.MaxAttempts = &n
	}
	in.ExpiresInSeconds, in.MaxObservationAgeSeconds, in.CheckValiditySeconds = expires.n, age.n, validity.n
	in.TransportDeadlineSeconds, in.VerificationDeadlineSeconds = transport.n, verification.n
	rp, err := rf.repeat(false)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(in)
	// The write layer escapes each line of its answer whole, tabs included, so the plan view, whose
	// cells planView escaped, is printed here, after the write, in its columns.
	var view []string
	err = e.write(&client.Write{Method: http.MethodPost, Path: "/plans", Body: body, Repeat: rp,
		Check: "plan list --machine " + client.ShellWord(in.Machine),
		Read: func(c *client.Client) (string, error) {
			return find(c, "/plans?"+url.Values{"machine": {in.Machine}}.Encode(), "proposed plan for this release and operation", func(p apitypes.PlanBody) string {
				same := p.Release == in.ReleaseId && p.Operation == operation && p.State == "proposed"
				return map[bool]string{true: "the proposed plan " + p.Id + " for this release and operation"}[same]
			})
		}}, func(c *client.Client, r *client.Response) ([]string, error) {
		var p apitypes.PlanBody
		if err := json.Unmarshal(r.Body, &p); err != nil || e.output != "table" {
			return nil, err
		}
		var change string
		if view, change = planSnapshot(c, r, &p); change != "" {
			// The plan was created, which stands; only the view of it is not shown.
			view = []string{sf("Plan %s was created for machine %s, operation %s.", p.Id, p.Machine, p.Operation),
				sf("  %s while the plan was read, so it is not shown. Read it again with: plan show ", change) + client.ShellWord(p.Id)}
		}
		return nil, nil
	})
	if next := createRefusalNext(err, in.ReleaseId, in.Machine); next != "" {
		return fmt.Errorf("%w\n%s", err, next)
	}
	if err != nil || view == nil {
		return err
	}
	w := tabwriter.NewWriter(e.out, 0, 0, 2, ' ', 0)
	printLines(w, view)
	return w.Flush()
}

// createRefusalNext is the next step T19 names for plan creation's conflicts (internal/api/plans.go):
// a release that is not the machine's Desired, a machine with no Applied, and an assignment moved
// since publication. It is "" for any other answer.
func createRefusalNext(err error, release, machine string) string {
	var p *client.Problem
	if !errors.As(err, &p) || p.Code() != "conflict" {
		return ""
	}
	detail, _ := p.Doc["detail"].(string)
	switch {
	case strings.Contains(detail, "is for the machine's Desired release"):
		if d, _ := p.Doc["desired"].(string); d != "" {
			return "Plan the machine's Desired release instead: " + createLine(d, machine, "apply-config")
		}
		return "The machine has no Desired release: publish a release that covers it first."
	case strings.Contains(detail, "has no Applied configuration"):
		return "Hand the machine over with an adopt plan: " + createLine(release, machine, "adopt")
	case strings.Contains(detail, "assignment has moved since the release was compiled"):
		return "Publish a release from the machine's current assignment, then plan that release. The assignment's history: " +
			"bronzewardctl machine timeline " + client.ShellWord(machine)
	}
	return ""
}

// createLine is the plan create command for release, machine and operation; the client never
// sends it by itself.
func createLine(release, machine, operation string) string {
	return "bronzewardctl plan create --release " + client.ShellWord(release) + " --machine " + client.ShellWord(machine) +
		" --operation " + client.ShellWord(operation)
}

// planView is T20's plan display: every PlanBody field, the machine and cluster reads, the
// evidence, the expiry with the time left by this client's clock, and the state lines. A read it
// cannot make is named in its line, never guessed.
func planView(c *client.Client, p *apitypes.PlanBody) []string {
	var m apitypes.MachineBody
	mErr := getJSON(c, "/machines/"+url.PathEscape(p.Machine), &m)
	var cl apitypes.ClusterBody
	var cluster string
	if err := getJSON(c, "/clusters/"+url.PathEscape(p.Cluster), &cl); err != nil {
		cluster = sf("%s (unread: %v)", p.Cluster, err)
	} else {
		cluster = sf("%s (%s)", p.Cluster, cl.Name)
	}
	// The sign is the unrounded time's: a plan with 20 seconds left is not expired, though the
	// minutes shown round to 0.
	until := time.Until(p.ExpiresAt)
	expiry := sf("expires %s (in %s by this client's clock)", when(p.ExpiresAt), until.Round(time.Minute))
	if until <= 0 {
		expiry = sf("expired %s (%s ago by this client's clock)", when(p.ExpiresAt), (-until).Round(time.Minute))
	}
	endpoint, desired := "endpoint unread", sf("unread: %v", mErr)
	if mErr == nil {
		endpoint, desired = pr(m.TalosEndpoint), pr(or(m.Desired, "none"))
	}
	what := sf(", mode %s", or(p.Mode, "none"))
	if p.Operation == "adopt" {
		what = ": records Applied from the node's own bytes and sends nothing to the machine"
	}
	// Every cell is escaped as it is built (sf, pr), so the tabs that separate the columns are the
	// only control characters in these lines.
	lines := []string{
		sf("Plan %s\t%s\t", p.Id, p.State) + expiry,
		sf("  Machine\t%s  ", p.Machine) + endpoint + "\tcluster " + cluster + sf(", route %s", p.Route),
		sf("  Operation\t%s", p.Operation) + what,
		sf("  Release\t%s\tmachine Desired: %s at plan creation, ", p.Release, or(p.DesiredRelease, "none")) + desired + " now",
		sf("  Assignment\t%s\tbaseline revision %s, drift record %s", p.AssignmentRevision, orInt(p.BaselineRevision, "none"), orInt(p.DriftRecord, "none")),
		sf("  Limits\ttransport %s, verification %s, observation age %ds, check validity %ds, attempts %s, rollout %d",
			secs(p.TransportDeadlineSeconds), secs(p.VerificationDeadlineSeconds), p.MaxObservationAgeSeconds, p.CheckValiditySeconds,
			orInt(p.MaxAttempts, "-"), p.RolloutLimit),
		sf("  Created by\t%s (%s)\tepoch %s, %s, plan revision %d, timeline revision %d", p.CreatedBy.Principal, p.CreatedBy.Role,
			p.Epoch, when(p.CreatedAt), p.Revision, p.TimelineRevision),
		sf("  Approval\t%s\tpolicy %s, committed operation %s", or(p.Approval, "none"), p.ApprovalPolicy, or(p.CommittedOperation, "none")),
	}
	ev := p.Evidence
	val := ev.Validation
	lines = append(lines, "Evidence", sf("  Validation\t%s at %s (contract %s, machinery %s, Kubernetes %s, %s)", val.Result, val.At,
		val.Contract, val.MachineryVersion, val.KubernetesVersion, val.PlatformMode))
	if ev.DryRun.Available {
		lines = append(lines, "  Dry run\tavailable")
	} else {
		lines = append(lines, "  Dry run\tnot available: "+pr(ev.DryRun.Reason))
	}
	if b := ev.Baseline; b != nil {
		pending := "no: the release's compiled configuration is byte-identical to the node's"
		if b.PendingConvergence {
			pending = "yes: the release's compiled configuration differs from the node's, which the adoption records; a later apply-config plan withholds its diff"
		}
		lines = append(lines, "  Baseline\timport base "+pr(b.ImportBaseRevision)+", the node's own bytes", "  Pending convergence\t"+pending)
	}
	switch d := ev.Diff; {
	case d == nil:
	case d.Withheld:
		lines = append(lines, sf("  Diff\tDiff withheld: the change cannot be shown (%s -> %s)", d.From, d.To))
	case d.Unified == "":
		lines = append(lines, sf("  Diff\tNo differences in the redacted configurations (%s -> %s)", d.From, d.To))
	default:
		lines = append(lines, sf("  Diff\tChanges shown below: the whole redacted configuration, %s -> %s", d.From, d.To))
		for _, l := range strings.Split(strings.TrimSuffix(d.Unified, "\n"), "\n") {
			lines = append(lines, "    "+client.Printable(l))
		}
	}
	return append(lines, stateLines(c, p, m, mErr)...)
}

// stateLines say what a plan's state means and what can be done next (T20). An approved plan's
// approval is read for its epoch; an uncommitted one is checked against the machine and its
// assignment, a prediction the controller's commitment settles.
func stateLines(c *client.Client, p *apitypes.PlanBody, m apitypes.MachineBody, mErr error) []string {
	again := "Create a new plan: " + createLine(p.Release, p.Machine, p.Operation)
	switch p.State {
	case "proposed":
		return []string{"It is not approved and nothing runs until an approver approves: bronzewardctl approve " + client.ShellWord(p.Id)}
	case "committed":
		op := or(p.CommittedOperation, "?")
		return []string{sf("Committed: operation %s. Follow it with: bronzewardctl operation watch ", op) + client.ShellWord(op)}
	case "expired":
		return []string{"Expired: it can no longer be approved or committed. " + again}
	case "revoked":
		return []string{"Revoked: its approval was revoked, so it cannot be committed. " + again}
	case "cancelled":
		return []string{"Cancelled: it cannot be committed. " + again}
	case "approved":
	default:
		return []string{sf("State %s: this client does not know it.", p.State)}
	}
	var a apitypes.ApprovalBody
	if p.Approval == nil {
		return []string{"Approved, but the plan names no approval."}
	}
	if err := getJSON(c, "/approvals/"+url.PathEscape(*p.Approval), &a); err != nil {
		return []string{sf("Approved by %s, which cannot be read: %v", *p.Approval, err)}
	}
	if a.Epoch != c.Context.LastEpoch {
		return []string{sf("Approved by %s (%s) in epoch %s. This approval is from before the last recovery entry and authorizes nothing; "+
			"an approver may approve again: bronzewardctl approve ", a.Approver.Principal, a.Approver.Role, a.Epoch) + client.ShellWord(p.Id), again}
	}
	lines := []string{sf("Approved by %s (%s) as %s. Not committed: the controller commits it only if its own checks pass; plan show then names the operation.",
		a.Approver.Principal, a.Approver.Role, a.Id)}
	var stale []string
	if mErr != nil {
		return append(lines, sf("Whether it is stale cannot be predicted: the machine cannot be read: %v", mErr))
	}
	if or(m.Desired, "") != or(p.DesiredRelease, "") {
		stale = append(stale, sf("the machine's Desired is now %s", or(m.Desired, "none")))
	}
	if m.OpenDrift == nil && p.DriftRecord != nil || m.OpenDrift != nil && (p.DriftRecord == nil || *p.DriftRecord != m.OpenDrift.Revision) {
		open := "none"
		if m.OpenDrift != nil {
			open = strconv.Itoa(m.OpenDrift.Revision)
		}
		stale = append(stale, "the machine's open drift record is "+open+", not the plan's")
	}
	// An assignment that cannot be read leaves the reasons already found standing.
	head, err := assignmentHead(c, p.Machine)
	if err == nil && head != p.AssignmentRevision {
		stale = append(stale, sf("the machine's assignment is now at %s", or(&head, "none")))
	}
	if len(stale) > 0 {
		lines = append(lines, "Predicted stale, by this client's reads: "+strings.Join(stale, "; ")+
			". The controller will refuse its commitment, so it cannot run. "+again)
	}
	if err != nil {
		return append(lines, sf("The assignment cannot be read, so whether it is stale is not fully predicted: %v", err))
	}
	if len(stale) > 0 {
		return lines
	}
	return append(lines, "A change of the machine's baseline revision is served by no read: the controller's refusal shows it on the timeline: "+
		"bronzewardctl machine timeline "+client.ShellWord(p.Machine))
}

// assignmentHead is the revision of machine's assignment head, "" when it has none or it was
// removed. Every page is read: the API serves no machine filter for assignments.
func assignmentHead(c *client.Client, machine string) (string, error) {
	items, err := c.List("/assignments")
	if err != nil {
		return "", err
	}
	for _, raw := range items {
		var a apitypes.AssignmentBody
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", fmt.Errorf("GET /api/v1/assignments: an item cannot be read: %w", err)
		}
		if a.Machine == machine {
			return or(a.Revision, ""), nil
		}
	}
	return "", nil
}

// getJSON reads path into v.
func getJSON(c *client.Client, path string, v any) error {
	r, err := c.Get(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(r.Body, v); err != nil {
		return fmt.Errorf("GET /api/v1%s cannot be read: %w", path, err)
	}
	return nil
}

// planCancel records a plan's cancellation (T22; POST /plans/{id}/cancellations, publisher for its
// own plan, approver, recovery-admin). The reason is not protected, so it is a flag; 1 to 1024
// bytes (ReasonInput). The plan is read after the answer, which does not say what the cancellation
// stopped: a proposed or approved plan becomes cancelled; a committed one stays committed, and its
// operation ends only while no attempt is recorded.
func (e *env) planCancel(args []string) error {
	var reason given
	var rf repeatFlags
	pos, err := e.parse("plan cancel", args, 1, func(fs *flag.FlagSet) {
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
	plan := pos[0]
	path := "/plans/" + url.PathEscape(plan)
	body, _ := json.Marshal(apitypes.ReasonInput{Reason: reason.v})
	return e.write(&client.Write{Method: http.MethodPost, Path: path + "/cancellations", Body: body, Repeat: rp,
		Check: "plan show " + client.ShellWord(plan),
		Read: func(c *client.Client) (string, error) {
			var p apitypes.PlanBody
			err := getJSON(c, path, &p)
			return "GET /api/v1" + path + " shows state " + p.State, err
		}}, func(c *client.Client, r *client.Response) ([]string, error) {
		var x apitypes.CancellationBody
		if err := json.Unmarshal(r.Body, &x); err != nil || e.output != "table" {
			return nil, err
		}
		lines := []string{fmt.Sprintf("Cancellation of plan %s recorded by %s (%s), act %s.", x.Plan, x.CancelledBy, x.Role, x.Act)}
		var p apitypes.PlanBody
		if err := getJSON(c, path, &p); err != nil {
			return append(lines, "The plan cannot be read: "+err.Error(), cancelLimits), nil
		}
		if p.State != "committed" {
			return append(lines, "The plan is "+p.State+": it cannot be committed."), nil
		}
		op := or(p.CommittedOperation, "?")
		return append(lines, "The plan stays committed to operation "+op+". "+cancelLimits,
			"Follow it with: bronzewardctl operation watch "+client.ShellWord(op)), nil
	})
}

// cancelLimits is what a cancellation of a committed plan does not do (journeys §8,
// internal/api/attempt.go:362-366).
const cancelLimits = "A cancellation ends a committed plan's operation only while no attempt is recorded; it does not stop an attempt already recorded."
