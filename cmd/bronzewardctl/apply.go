package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// apply is ginsys/bronzeward#1096's command: one release applied to the machines the person chooses
// (--machine a,b or --all), each with one apply-config plan and one approval. The plans are made first
// so that their diffs exist; the list and the diffs (with --diff) go to standard error, then one
// question, Approve N plans? [y/N], decides all of them: only y or yes approves, and Enter, n and the
// end of input send no approval and cancel the plans this command made. A plan somebody else prepared
// is read from the machine's position and approved as it stands; it is never cancelled here. A Ctrl-C
// at the question leaves the plans made to expire at the time each one shows. A plan whose change cannot
// be read is left out. The approvals are bound to the epoch the list was shown at. Standard output holds
// one line for each machine. The structured outputs are not offered: the command asks a person a question.
func (e *env) apply(args []string) (err error) {
	var machines string
	var all, diff bool
	pos, err := e.parse("apply", args, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&machines, "machine", "", "")
		fs.BoolVar(&all, "all", false, "")
		fs.BoolVar(&diff, "diff", false, "")
	})
	if err != nil {
		return err
	}
	switch {
	case all == (machines != ""):
		return usageError{errors.New("apply needs --machine <a,b> (names or IDs) or --all, one of them; nothing was sent")}
	case e.output != "table":
		return usageError{errors.New("apply prints a table and asks a question: -o json and -o yaml are not offered; nothing was sent")}
	case e.noInput:
		return notConfirmed{errors.New("apply asks for your y, which --no-input forbids; nothing was sent")}
	}
	c, err := e.open()
	if err != nil {
		return err
	}
	defer func() { err = joinStoredSave(err, c.Save()) }()
	release := pos[0]
	var rel apitypes.ReleaseBody
	if err := getJSON(c, "/releases/"+url.PathEscape(release), &rel); err != nil {
		return err
	}
	startMoves := c.EpochMoves()
	rows, err := e.applyRows(c, &rel, machines, all)
	if err != nil {
		return err
	}
	var made []string
	var failed []error
	for _, r := range rows {
		if r.plan == "" && r.line == "" {
			if r.plan, err = e.applyPlan(c, release, r); err != nil {
				failed = append(failed, fmt.Errorf("%s: %w", r.label(), err))
				r.line = sf("not planned: %v", err)
				var u *client.Unknown
				if errors.As(err, &u) {
					r.line = "not planned: whether its plan was made is unknown (the error above); plan list --machine shows it"
				}
			} else {
				made = append(made, r.plan)
				r.madeID = r.plan
			}
		}
	}
	// Every plan is read before the list is shown, so that none is approved unseen: a plan whose change
	// cannot be read is left out, and one this command made is cancelled.
	e.applyRead(c, rows)
	// The approvals are bound to the epoch and move count of these reads, taken before any cleanup read
	// can show another one. An epoch that moved while the list was read is not shown as a list at all.
	epoch, moves := c.Context.LastEpoch, c.EpochMoves()
	if moves != startMoves {
		failed = append(failed, e.applyCancel(c, made))
		return errors.Join(append(failed, notConfirmed{errors.New("the epoch changed while the plans were read, so nothing was approved; run apply again")})...)
	}
	var unread []string
	planned := 0
	for _, r := range rows {
		switch {
		case r.plan != "":
			planned++
		case r.made:
			unread = append(unread, r.madeID)
		}
	}
	failed = append(failed, e.applyCancel(c, unread))
	made = slices.DeleteFunc(made, func(id string) bool { return slices.Contains(unread, id) })
	if err := e.applyShow(rows, diff); err != nil {
		// The person would confirm evidence that was not shown.
		failed = append(failed, e.applyCancel(c, made))
		return errors.Join(append(failed, notConfirmed{fmt.Errorf("showing the plans: %w; nothing was approved", err)})...)
	}
	if planned == 0 {
		return errors.Join(append(failed, notConfirmed{errors.New("no plan to approve; nothing was approved")})...)
	}
	question := sf("Answering y records your approval of the %d plans listed as you. Approve them?", planned)
	if planned == 1 {
		question = "Answering y records your approval of the 1 plan listed as you. Approve it?"
	}
	if err := e.confirmYes(question); err != nil {
		failed = append(failed, e.applyCancel(c, made))
		// The plans this command made are already created, so "nothing was sent" would be false: nothing was approved.
		msg, _ := strings.CutSuffix(err.Error(), "nothing was sent")
		return errors.Join(append(failed, notConfirmed{errors.New(msg + "nothing was approved")})...)
	}
	return errors.Join(append(failed, e.applyApprove(c, rows, epoch, moves))...)
}

// applyRow is a machine of the release as the command reads it. plan is the one to approve ("" for
// none, with line saying why), made says that this command created it.
type applyRow struct {
	id, name, role string
	profiles       []string
	plan, line     string
	made           bool
	madeID         string // the plan this command made, kept when the plan is left out of the approval
	machine        apitypes.MachineBody
	body           apitypes.PlanBody // the plan as read, the change and the expiry the list shows
}

func (r *applyRow) label() string {
	if r.name != "" {
		return r.name
	}
	return r.id
}

// applyRows reads the release's machines, keeps those the person chose, and reads each one's position.
// A chosen name or ID that is not a machine of the release is a usage error before anything is sent.
func (e *env) applyRows(c *client.Client, rel *apitypes.ReleaseBody, chosen string, all bool) ([]*applyRow, error) {
	profiles := map[string][]string{}
	for _, s := range rel.Sources {
		if s.Kind != string(apitypes.SourceEntryKindAssignment) || s.Revision == nil {
			continue
		}
		var a apitypes.AssignmentRevisionBody
		if err := getJSON(c, "/assignment-revisions/"+url.PathEscape(*s.Revision), &a); err == nil {
			profiles[a.Machine] = a.Profiles
		}
	}
	want := map[string]bool{}
	for _, n := range strings.Split(chosen, ",") {
		if n = strings.TrimSpace(n); n != "" {
			want[n] = true
		}
	}
	var rows []*applyRow
	for _, rm := range rel.Machines {
		var pos apitypes.PositionBody
		if err := getJSON(c, "/machines/"+url.PathEscape(rm.Machine)+"/position", &pos); err != nil {
			return nil, err
		}
		m := pos.Machine
		r := &applyRow{id: rm.Machine, name: or(m.Name, ""), profiles: profiles[rm.Machine], machine: m}
		if m.Role != nil {
			r.role = string(*m.Role)
		}
		if !all && !want[r.id] && !want[r.name] {
			continue
		}
		delete(want, r.id)
		delete(want, r.name)
		r.plan, r.line = applyStanding(&pos, rel.Id)
		rows = append(rows, r)
	}
	if len(want) > 0 {
		var left []string
		for n := range want {
			left = append(left, client.ShellWord(n))
		}
		return nil, usageError{fmt.Errorf("not a machine of release %s: %s; nothing was sent", client.ShellWord(rel.Id), strings.Join(left, ", "))}
	}
	return rows, nil
}

// applyStanding says where a machine stands for the release: the plan to approve where one is ready
// (somebody prepared it), the reason there is nothing to do, or neither, which means a plan is to be made.
func applyStanding(p *apitypes.PositionBody, release string) (plan, line string) {
	m := p.Machine
	ofRelease := p.Plan != nil && p.Plan.Release == release
	mine := ofRelease && p.Plan.Operation == apitypes.PositionPlanOperationApplyConfig
	waiting := p.Step == apitypes.PositionBodyStepAwaitingApproval || p.Step == apitypes.PositionBodyStepAwaitingDispatch
	switch {
	case p.Step == apitypes.PositionBodyStepOperationUnresolved:
		return "", "stopped: an operation on this machine is unresolved; open it with operation list"
	case p.Step == apitypes.PositionBodyStepOperationInFlight:
		return "", "applying: its operation is in progress"
	case ofRelease && !mine && waiting:
		return "", "stopped: the plan waiting for this machine is not an apply plan; open it with plan show " + client.ShellWord(p.Plan.Id)
	case p.Step == apitypes.PositionBodyStepAwaitingDispatch && mine:
		return "", "approved already: the controller sends it when its gates allow"
	case m.OpenDrift != nil:
		return "", "stopped: the machine changed since Bronzeward last verified it; decide on the drift first"
	case m.Frozen:
		return "", "stopped: the machine is frozen; an approver can unfreeze it"
	case p.Step == apitypes.PositionBodyStepAwaitingApproval && mine:
		return p.Plan.Id, ""
	// The apply page's rule (rowOf in web/src/apply.ts): this release is what the machine runs, or it is the Desired one
	// and leaves the Applied configuration unchanged.
	case m.Applied != nil && !m.PendingConvergence && (m.Applied.Release == release || (m.Desired != nil && *m.Desired == release)):
		return "", "applied: this release is what the machine runs"
	}
	return "", ""
}

// applyPlan creates the apply-config plan of one machine under the epoch the reads showed.
func (e *env) applyPlan(c *client.Client, release string, r *applyRow) (string, error) {
	mode := apitypes.PlanInputModeNoReboot
	body, _ := json.Marshal(apitypes.PlanInput{ReleaseId: release, Machine: r.id, Operation: apitypes.PlanInputOperationApplyConfig, Mode: &mode})
	id := ""
	w := &client.Write{Method: http.MethodPost, Path: "/plans", Body: body,
		Check: "plan list --machine " + client.ShellWord(r.id),
		Read: func(c *client.Client) (string, error) {
			return find(c, "/plans?"+url.Values{"machine": {r.id}}.Encode(), "proposed plan for this release", func(p apitypes.PlanBody) string {
				return map[bool]string{true: "the proposed plan " + p.Id + " for this release"}[p.Release == release && p.State == "proposed"]
			})
		}}
	w.ReadsFrom(c)
	err := e.writeWith(c, nil, w, func(_ *client.Client, resp *client.Response) ([]string, error) {
		var p apitypes.PlanBody
		err := json.Unmarshal(resp.Body, &p)
		id = p.Id
		return nil, err
	})
	if err == nil && id == "" {
		err = errors.New("the plan was created, but the answer named no plan; read it with plan list --machine " + client.ShellWord(r.id))
	}
	r.made = err == nil
	return id, err
}

// applyRead reads each row's plan. One that cannot be read is left out of the approval, with the reason,
// so that no plan is approved unseen; r.madeID keeps the ID of one this command made, to be cancelled.
func (e *env) applyRead(c *client.Client, rows []*applyRow) {
	for _, r := range rows {
		if r.plan == "" {
			continue
		}
		if err := getJSON(c, "/plans/"+url.PathEscape(r.plan), &r.body); err != nil {
			r.plan, r.line = "", sf("stopped: the change of its plan could not be read (%v), so it is left out", err)
			continue
		}
		// The server withholds a change it cannot redact or cannot compare, and a plan with a drift record overwrites a
		// configuration no reviewer has seen (single approve says so); a batch is never approved over either.
		var why string
		if d := r.body.Evidence.Diff; d != nil && d.Withheld {
			why = "stopped: the change of its plan cannot be shown, either because the configuration could not be redacted or because the machine holds a configuration other than the one Bronzeward last applied; it is left out. "
		} else if r.body.DriftRecord != nil {
			why = sf("stopped: its plan overwrites the drifted configuration of drift record %d, which no reviewer has seen; it is left out. ", *r.body.DriftRecord)
		}
		if why == "" {
			continue
		}
		if r.made {
			// The plan is this command's own and is cancelled below, so approving it is no way forward.
			why += "This command asks for the plan it made to be cancelled; to apply this machine anyway, make a plan with plan create, read it with plan show and approve it with approve, or leave this machine out"
		} else {
			why += sf("Read it with plan show %s, approve it with approve %[1]s, or leave this machine out", client.ShellWord(r.plan))
		}
		r.plan, r.line = "", why
	}
}

// applyShow prints the list on standard error: each machine with its role and profiles and its plan
// (with its own expiry) or the reason it has none, the diff of each plan with --diff, then the notes
// the lead ruled on.
func (e *env) applyShow(rows []*applyRow, diff bool) error {
	// The first failure to write is kept: a person must never be asked to approve evidence that was not shown.
	var shown error
	tf := func(w io.Writer, format string, args ...any) {
		if err := tf(w, format, args...); err != nil && shown == nil {
			shown = err
		}
	}
	controlPlane, noRole := false, false
	for _, r := range rows {
		role := "no role"
		if r.role != "" {
			role = r.role
		} else {
			noRole = true
		}
		controlPlane = controlPlane || r.role == "controlplane"
		what := r.line
		if r.plan != "" {
			what = "plan " + r.plan
			if r.made {
				what += " (made by this command)"
			} else {
				what += " (prepared by somebody else)"
			}
			what += ", expires " + r.body.ExpiresAt.UTC().Format("2006-01-02 15:04:05 MST")
		}
		tf(e.err, "%s  %s  %s  %s\n", r.label(), role, strings.Join(r.profiles, ","), what)
		if r.plan != "" {
			d := r.body.Evidence.Diff
			switch {
			case d == nil:
				tf(e.err, "  the plan records no change\n")
			case !diff:
				tf(e.err, "  the plan's change is %d lines; --diff shows it\n", strings.Count(d.Unified, "\n"))
			default:
				if d.Unified == "" {
					tf(e.err, "  no differences in the redacted configurations\n")
				}
				if d.FromFormOnly {
					tf(e.err, "%s\n", strings.Replace(commentsLine, "\t", " ", 1))
				}
				for _, l := range strings.Split(strings.TrimSuffix(d.Unified, "\n"), "\n") {
					if l != "" {
						tf(e.err, "    %s\n", client.Printable(l))
					}
				}
			}
		}
	}
	if controlPlane {
		tf(e.err, "A control plane machine is chosen. Applying to a control plane machine was not covered by this version's acceptance tests. The server does not refuse it, machines are applied one at a time, and you decide.\n")
	}
	if noRole {
		tf(e.err, "A machine imported before its role was stored shows no role until it is imported again.\n")
	}
	tf(e.err, "The controller re-checks each plan when it sends it and applies it only if every check passes, one machine of a cluster at a time, in an order the controller does not promise. A plan that is not applied expires at the time shown for it.\n")
	return shown
}

// applyApprove approves each listed plan, bound to the epoch and move count the list was shown from, and
// carries on past a refusal; the error is every refusal. Only a refused write is said to have approved
// nothing: an unknown outcome may have been committed, and its error says so.
func (e *env) applyApprove(c *client.Client, rows []*applyRow, epoch string, moves int) error {
	var errs []error
	// unknownAt is the first approval of unknown outcome's error in errs, and tried counts the plans sent after it.
	unknownAt, tried := -1, 0
	for _, r := range rows {
		if r.plan == "" {
			tf(e.out, "%s: %s\n", r.label(), r.line)
			continue
		}
		if unknownAt >= 0 {
			tried++
		}
		w := &client.Write{Method: http.MethodPost, Path: "/plans/" + url.PathEscape(r.plan) + "/approvals", Body: []byte("{}"), HumanOnly: true,
			Check: "plan show " + client.ShellWord(r.plan),
			Read: func(c *client.Client) (string, error) {
				var now apitypes.PlanBody
				err := getJSON(c, "/plans/"+url.PathEscape(r.plan), &now)
				return sf("GET /api/v1/plans/%s shows state %s, approval %s", r.plan, now.State, or(now.Approval, "none")), err
			}}
		w.ReadsAt(epoch, moves)
		approved := ""
		err := e.writeWith(c, nil, w, func(_ *client.Client, resp *client.Response) ([]string, error) {
			var a apitypes.ApprovalBody
			if err := json.Unmarshal(resp.Body, &a); err != nil {
				return nil, err
			}
			approved = sf("%s: approved (%s). Recorded self-approval reasons: %s.", r.label(), a.Id, orList(a.SelfApproval.Reasons))
			return nil, nil
		})
		if err != nil {
			if mayHaveCommitted(err) {
				if unknownAt < 0 {
					unknownAt = len(errs)
				}
				errs = append(errs, fmt.Errorf("%s: %w", r.label(), err))
			} else {
				errs = append(errs, fmt.Errorf("%s: %w; nothing was approved for this machine", r.label(), err))
			}
			continue
		}
		tf(e.out, "%s\n", approved)
	}
	// Unlike the apply page, which holds its batch on an unknown outcome, the command goes on while the epoch
	// holds: each approval is its own plan, and the person confirmed them all with one y. The first unknown line
	// says that, in one sentence that classifies nothing: each plan's own line says what happened to it.
	if unknownAt >= 0 && tried > 0 {
		errs[unknownAt] = fmt.Errorf("%w; the plans after it were tried in order, and each plan's own line says what happened to it", errs[unknownAt])
	}
	return errors.Join(errs...)
}

// mayHaveCommitted is whether a failed write may still have been committed: an Unknown that was not set aside (its
// outcome is unknown) or was set aside after an attempt that may have committed (client.Unknown.Ambiguous).
// A refusal, a write not sent (the epoch moved) and a write set aside with no attempt that could have committed
// are all certain not to have been.
func mayHaveCommitted(err error) bool {
	var u *client.Unknown
	return errors.As(err, &u) && (!u.SetAside || u.Ambiguous)
}

// applyCancel cancels the plans this command made and no others, and says what it left. A cancellation
// of unknown outcome is said to be unknown, never as done or as not done, and its error is returned so
// that the command exits 3.
func (e *env) applyCancel(c *client.Client, made []string) error {
	var cancelled []string
	var unknown []error
	for _, id := range made {
		body, _ := json.Marshal(apitypes.ReasonInput{Reason: "Not approved: apply stopped before approving this plan."})
		w := &client.Write{Method: http.MethodPost, Path: "/plans/" + url.PathEscape(id) + "/cancellations", Body: body,
			Check: "plan show " + client.ShellWord(id)}
		w.ReadsFrom(c)
		if err := e.writeWith(c, nil, w, func(*client.Client, *client.Response) ([]string, error) { return nil, nil }); err != nil {
			var u *client.Unknown
			if errors.As(err, &u) {
				tf(e.err, "whether plan %s was cancelled is unknown (the error above); plan show %s reads it\n", id, client.ShellWord(id))
				unknown = append(unknown, fmt.Errorf("plan %s: %w", id, err))
				continue
			}
			tf(e.err, "the request to cancel plan %s was refused (%v); plan show %s reads its state\n", id, err, client.ShellWord(id))
			continue
		}
		cancelled = append(cancelled, id)
	}
	if len(cancelled) > 0 {
		tf(e.err, "Cancellation recorded for the plans this command made: %s. %s Plans prepared by somebody else were left as they were.\n", strings.Join(cancelled, ", "), cancelLimits)
	}
	return errors.Join(unknown...)
}
