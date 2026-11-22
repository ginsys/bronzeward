package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
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
// at the question leaves the plans made to expire (24 hours). Standard output holds one line for each
// machine. The structured outputs are not offered: the command asks a person a question.
func (e *env) apply(args []string) error {
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
	rows, err := e.applyRows(c, &rel, machines, all)
	if err != nil {
		return err
	}
	var made []string
	for _, r := range rows {
		if r.plan == "" && r.line == "" {
			if r.plan, err = e.applyPlan(c, release, r); err != nil {
				r.line = sf("not planned: %v", err)
			} else {
				made = append(made, r.plan)
			}
		}
	}
	planned := 0
	for _, r := range rows {
		if r.plan != "" {
			planned++
		}
	}
	e.applyShow(c, rows, diff)
	if planned == 0 {
		return notConfirmed{errors.New("no plan to approve; nothing was approved")}
	}
	if err := e.confirmYes(sf("Answering y records your approval of the %d plans listed as you. Approve them?", planned)); err != nil {
		e.applyCancel(c, made)
		return err
	}
	return e.applyApprove(c, rows)
}

// applyRow is a machine of the release as the command reads it. plan is the one to approve ("" for
// none, with line saying why), made says that this command created it.
type applyRow struct {
	id, name, role string
	profiles       []string
	plan, line     string
	made           bool
	machine        apitypes.MachineBody
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
	mine := p.Plan != nil && p.Plan.Release == release
	switch {
	case p.Step == apitypes.PositionBodyStepOperationUnresolved:
		return "", "stopped: an operation on this machine is unresolved; open it with operation list"
	case p.Step == apitypes.PositionBodyStepOperationInFlight:
		return "", "applying: its operation is in progress"
	case p.Step == apitypes.PositionBodyStepAwaitingDispatch && mine:
		return "", "approved already: the controller sends it when its gates allow"
	case m.OpenDrift != nil:
		return "", "stopped: the machine changed since Bronzeward last verified it; decide on the drift first"
	case m.Frozen:
		return "", "stopped: the machine is frozen; an approver can unfreeze it"
	case p.Step == apitypes.PositionBodyStepAwaitingApproval && mine:
		return p.Plan.Id, ""
	case m.Applied != nil && m.Applied.Release == release && !m.PendingConvergence:
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

// applyShow prints the list on standard error: each machine with its role and profiles and its plan
// or the reason it has none, the diff of each plan with --diff, then the notes the lead ruled on.
func (e *env) applyShow(c *client.Client, rows []*applyRow, diff bool) {
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
		}
		tf(e.err, "%s  %s  %s  %s\n", r.label(), role, strings.Join(r.profiles, ","), what)
		if diff && r.plan != "" {
			var p apitypes.PlanBody
			if err := getJSON(c, "/plans/"+url.PathEscape(r.plan), &p); err != nil {
				tf(e.err, "  the plan's change could not be read: %v\n", err)
			} else if d := p.Evidence.Diff; d == nil || d.Withheld {
				tf(e.err, "  the plan's change is withheld\n")
			} else {
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
	tf(e.err, "The controller re-checks each plan when it sends it and applies it only if every check passes, one machine of a cluster at a time, in the order the plans were approved, which is the order listed. A plan that is not applied expires 24 hours after it was made.\n")
}

// applyApprove approves each listed plan and carries on past a refusal; the error is every refusal.
func (e *env) applyApprove(c *client.Client, rows []*applyRow) error {
	var errs []error
	for _, r := range rows {
		if r.plan == "" {
			tf(e.out, "%s: %s\n", r.label(), r.line)
			continue
		}
		w := &client.Write{Method: http.MethodPost, Path: "/plans/" + url.PathEscape(r.plan) + "/approvals", Body: []byte("{}"), HumanOnly: true,
			Check: "plan show " + client.ShellWord(r.plan),
			Read: func(c *client.Client) (string, error) {
				var now apitypes.PlanBody
				err := getJSON(c, "/plans/"+url.PathEscape(r.plan), &now)
				return sf("GET /api/v1/plans/%s shows state %s, approval %s", r.plan, now.State, or(now.Approval, "none")), err
			}}
		w.ReadsFrom(c)
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
			errs = append(errs, fmt.Errorf("%s: %w; nothing was approved for this machine", r.label(), err))
			continue
		}
		tf(e.out, "%s\n", approved)
	}
	return errors.Join(errs...)
}

// applyCancel cancels the plans this command made and no others, and says what it left.
func (e *env) applyCancel(c *client.Client, made []string) {
	var cancelled []string
	for _, id := range made {
		body, _ := json.Marshal(apitypes.ReasonInput{Reason: "Not approved: apply was stopped at its question."})
		w := &client.Write{Method: http.MethodPost, Path: "/plans/" + url.PathEscape(id) + "/cancellations", Body: body,
			Check: "plan show " + client.ShellWord(id)}
		w.ReadsFrom(c)
		if err := e.writeWith(c, nil, w, func(*client.Client, *client.Response) ([]string, error) { return nil, nil }); err != nil {
			tf(e.err, "plan %s was not cancelled (%v); it expires 24 hours after it was made\n", id, err)
			continue
		}
		cancelled = append(cancelled, id)
	}
	if len(cancelled) > 0 {
		tf(e.err, "Cancelled the plans this command made: %s. Plans prepared by somebody else were left as they were.\n", strings.Join(cancelled, ", "))
	}
}
