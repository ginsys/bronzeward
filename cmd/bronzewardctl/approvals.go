package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"text/tabwriter"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// These are #258's approval commands (ginsys/bronzeward#353): approve (decision T21), approval
// show, and approval revoke (T22).

// approve approves a plan (T21; POST /plans/{id}/approvals, approver, human only, normal start
// only). The plan display, the self-approval prediction and, for a revert, the overwrite line go to
// standard error with the confirmation prompt; standard output holds only the approval. Only the
// plan ID typed back sends it; there is no --yes. The display and the prediction are one epoch
// snapshot, as plan show's: across an epoch change approve shows nothing and sends nothing, since
// the operator would confirm a state that never existed. An approval starts no operation, so
// approve does not wait.
func (e *env) approve(args []string) error {
	var rf repeatFlags
	pos, err := e.parse("approve", args, 1, rf.flags)
	if err != nil {
		return err
	}
	if e.noInput {
		return notConfirmed{errors.New("approve asks for the plan ID typed back, which --no-input forbids; nothing was sent")}
	}
	rp, err := rf.repeat(false)
	if err != nil {
		return err
	}
	plan := pos[0]
	path := "/plans/" + url.PathEscape(plan)
	var p apitypes.PlanBody
	confirm := func(c *client.Client) error {
		r, err := c.Get(path)
		if err != nil {
			return err
		}
		v, err := decode[apitypes.PlanBody](c, r.Body)
		if err != nil {
			return err
		}
		p = v[0]
		var view, prediction []string
		if _, change := snapshot(c, r, func() []string {
			if view = planView(c, &p); p.State == "proposed" {
				view = view[:len(view)-1] // its one state line points at this approve command
			}
			prediction = predict(c, &p)
			return nil
		}); change != "" {
			return fmt.Errorf("%s while the plan was read; nothing is shown and nothing was sent. Approve it again with: approve %s", change, client.ShellWord(p.Id))
		}
		tw := tabwriter.NewWriter(e.err, 0, 0, 2, ' ', tabwriter.StripEscape) // as output.go's, for planView's verbatim diff lines
		printLines(tw, view)
		tw.Flush() // the prediction keeps its own column, not the evidence's
		printLines(tw, prediction)
		if p.Operation == "apply-config" && p.DriftRecord != nil {
			tf(tw, "Overwrites\tthe drifted configuration of drift record %d, which no reviewer has seen.\n", *p.DriftRecord)
			fmt.Fprintln(tw, "\tApproving records that you approve overwriting it.")
		}
		tw.Flush()
		return e.confirmTyped("the plan ID", p.Id)
	}
	return e.writeAfter(confirm, &client.Write{Method: http.MethodPost, Path: path + "/approvals", Body: []byte("{}"), HumanOnly: true, Repeat: rp,
		Check: "plan show " + client.ShellWord(plan),
		Read: func(c *client.Client) (string, error) {
			var now apitypes.PlanBody
			err := getJSON(c, path, &now)
			return sf("GET /api/v1%s shows state %s, approval %s", path, now.State, or(now.Approval, "none")), err
		}}, func(_ *client.Client, r *client.Response) ([]string, error) {
		var a apitypes.ApprovalBody
		if err := json.Unmarshal(r.Body, &a); err != nil || e.output != "table" {
			return nil, err
		}
		lines := []string{sf("Approved (%s). Recorded self-approval reasons: %s.", a.Id, orList(a.SelfApproval.Reasons))}
		if a.UnseenOverwrite {
			lines = append(lines, "It overwrites the drifted configuration, which no reviewer has seen: this approval records that you approve overwriting it.")
		}
		if p.Operation == "adopt" {
			return append(lines, sf("The controller records the adoption without sending anything; Applied becomes %s (adoption) once it does.", p.Release)), nil
		}
		return append(lines, "The controller re-checks the plan at use time; it applies it only if every check passes."), nil
	})
}

// predict is T21's self-approval prediction (journeys §5.6), labelled as one and said in the
// journey's prose: created-plan from the plan's creator, published from the release's publisher,
// authored-change and authored-reused from the authors of the release's source revisions and the
// revisions its draft introduced (internal/api/approvals.go selfApprovalReasons). Import-base
// authorship and owned automation cannot be predicted, so the approver is never presented as clear.
// A failed read leaves out what it would have shown, rather than saying "did not".
func predict(c *client.Client, p *apitypes.PlanBody) []string {
	var unread []string
	said := "nothing."
	var id apitypes.IdentityBody
	if err := getJSON(c, "/identity", &id); err != nil {
		unread = append(unread, sf("your identity: %v", err))
	} else {
		me := or(id.Principal, "") // a human with no principal row created, published and authored nothing
		var w selfWork
		w.created = me != "" && p.CreatedBy.Principal == me
		var rel apitypes.ReleaseBody
		if err := getJSON(c, "/releases/"+url.PathEscape(p.Release), &rel); err != nil {
			unread = append(unread, sf("the release: %v", err))
		} else {
			w.knowPublished, w.published = true, me != "" && rel.PublishedBy.Principal == me
			var unreadAuthors []string
			w.change, w.reused, unreadAuthors = authored(c, &rel, me)
			w.knowAuthored = len(unreadAuthors) == 0
			unread = append(unread, unreadAuthors...)
		}
		said = w.String()
	}
	lines := []string{"Self-approval\tpredicted: " + said,
		"\tImport-base authorship and automation you own are not predicted. The approval records the actual reasons; self-approval is marked and allowed."}
	for _, u := range unread {
		lines = append(lines, "\tNot predicted, since this read failed: "+u)
	}
	return lines
}

// selfWork is what the prediction found the approver did to the plan, as the journey's prose says
// it: one clause per predicted reason. knowPublished and knowAuthored are false when a failed read
// left that part unknown.
type selfWork struct {
	created, published, change, reused bool
	knowPublished, knowAuthored        bool
}

func (w selfWork) String() string {
	var acts string
	switch {
	case w.created && w.published:
		acts = "created this plan and published this release"
	case w.created && w.knowPublished:
		acts = "created this plan but did not publish this release"
	case w.created:
		acts = "created this plan"
	case w.published:
		acts = "published this release but did not create this plan"
	case w.knowPublished:
		acts = "did not create this plan or publish this release"
	default:
		acts = "did not create this plan"
	}
	const revision = "a fragment, profile or assignment revision"
	switch {
	case w.change && w.reused:
		return "you " + acts + ", and authored " + revision + " its draft introduced and one it reuses."
	case w.change:
		return "you " + acts + ", and authored " + revision + " its draft introduced."
	case w.reused:
		return "you " + acts + ", and authored " + revision + " it reuses."
	case w.knowAuthored:
		return "you " + acts + ", and authored no fragment, profile or assignment revision in it."
	}
	return "you " + acts + "."
}

// authored reports authored-change and authored-reused for me in rel: a source revision me
// authored is a change when rel's draft introduced it, else reused.
func authored(c *client.Client, rel *apitypes.ReleaseBody, me string) (change, reused bool, unread []string) {
	if me == "" {
		return false, false, nil // a human with no principal row has authored nothing
	}
	var d struct {
		Entries []struct {
			Revision *string `json:"revision"`
		} `json:"entries"`
	}
	if err := getJSON(c, "/drafts/"+url.PathEscape(rel.Draft), &d); err != nil {
		return false, false, []string{sf("the release's draft: %v", err)}
	}
	introduced := map[string]bool{}
	for _, en := range d.Entries {
		introduced[or(en.Revision, "")] = true
	}
	for _, s := range rel.Sources {
		if s.Revision == nil {
			continue
		}
		var r struct {
			Author string `json:"author"`
		}
		if err := getJSON(c, "/"+s.Kind+"-revisions/"+url.PathEscape(*s.Revision), &r); err != nil {
			unread = append(unread, sf("the %s revision %s: %v", s.Kind, *s.Revision, err))
		} else if r.Author == me {
			change, reused = change || introduced[*s.Revision], reused || !introduced[*s.Revision]
		}
	}
	return change, reused, unread
}

func orList(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

func (e *env) approvalCmd(args []string) error {
	v, rest, err := e.verb("approval", args, "show", "revoke")
	if err != nil {
		return err
	} else if v == "revoke" {
		return e.approvalRevoke(rest)
	}
	pos, err := e.parse("approval show", rest, 1, nil)
	if err != nil {
		return err
	}
	// current is the epoch of the approval answer itself, so that a later read cannot move it.
	var current string
	return showThen(e, "/approvals/"+url.PathEscape(pos[0]), func(c *client.Client, r *client.Response, _ *apitypes.ApprovalBody) error {
		if current = r.Header.Get("Bronzeward-Epoch"); current == "" {
			current = c.Context.LastEpoch
		}
		return nil
	}, func(w *tabwriter.Writer, a *apitypes.ApprovalBody) {
		tf(w, "Approval %s of plan %s, epoch %s\n", a.Id, a.Plan, a.Epoch)
		tf(w, "  Approver\t%s (%s)\n", a.Approver.Principal, a.Approver.Role)
		tf(w, "  Self-approval\trecorded reasons: %s\n", orList(a.SelfApproval.Reasons))
		if a.UnseenOverwrite {
			fmt.Fprintln(w, "  Overwrites\tthe drifted configuration, which no reviewer had seen: the approver approved overwriting it")
		} else {
			fmt.Fprintln(w, "  Overwrites\tnothing unseen")
		}
		if a.Epoch != current {
			fmt.Fprintln(w, "From before the last recovery entry: it authorizes nothing.")
		}
		fmt.Fprintln(w, "Whether it was revoked, and the plan's state: bronzewardctl plan show "+client.ShellWord(a.Plan))
	})
}

// approvalRevoke records an approval's revocation (T22; POST /approvals/{id}/revocations, approver or
// recovery-admin). The reason is not protected, so it is a flag; 1 to 1024 bytes (ReasonInput). The
// plan is read after the answer, which does not say what the revocation ended.
func (e *env) approvalRevoke(args []string) error {
	var reason given
	var rf repeatFlags
	pos, err := e.parse("approval revoke", args, 1, func(fs *flag.FlagSet) {
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
	path := "/approvals/" + url.PathEscape(pos[0])
	body, _ := json.Marshal(apitypes.ReasonInput{Reason: reason.v})
	return e.write(&client.Write{Method: http.MethodPost, Path: path + "/revocations", Body: body, Repeat: rp,
		Check: "approval show " + client.ShellWord(pos[0]),
		Read: func(c *client.Client) (string, error) {
			var a apitypes.ApprovalBody
			if err := getJSON(c, path, &a); err != nil {
				return "", err
			}
			var p apitypes.PlanBody
			err := getJSON(c, "/plans/"+url.PathEscape(a.Plan), &p)
			return sf("GET /api/v1/plans/%s shows state %s, approval %s", a.Plan, p.State, or(p.Approval, "none")), err
		}}, func(c *client.Client, r *client.Response) ([]string, error) {
		var x apitypes.ApprovalRevocationBody
		if err := json.Unmarshal(r.Body, &x); err != nil || e.output != "table" {
			return nil, err
		}
		lines := []string{sf("Revocation of approval %s recorded by %s (%s), act %s.", x.Approval, x.RevokedBy, x.Role, x.Act)}
		var p apitypes.PlanBody
		var err error
		// A restore between the answer and the read can undo the revocation, so the read says what
		// it ended only from the answer's epoch (client-contracts.md section 8.5).
		if _, change := snapshot(c, r, func() []string { err = getJSON(c, "/plans/"+url.PathEscape(x.Plan), &p); return nil }); change != "" {
			return append(lines, sf("%s after the revocation was recorded, so whether it still stands is not known. Read the plan with: plan show ", change)+
				client.ShellWord(x.Plan), revokeLimits), nil
		}
		if err != nil {
			return append(lines, sf("The plan %s cannot be read: %v", x.Plan, err), revokeLimits), nil
		}
		switch p.State {
		case "revoked":
			return append(lines, sf("The plan %s is revoked: it cannot be committed. ", p.Id)+revokeLimits), nil
		case "committed":
			op := or(p.CommittedOperation, "?")
			return append(lines, sf("The plan %s stays committed to operation %s. ", p.Id, op)+revokeLimits,
				"Follow it with: bronzewardctl operation watch "+client.ShellWord(op)), nil
		}
		return append(lines, sf("The plan %s is %s, with approval %s. ", p.Id, p.State, or(p.Approval, "none"))+revokeLimits), nil
	})
}

// revokeLimits is what a revocation does not do (T22, journeys §8, internal/api/approval_revocations.go).
const revokeLimits = "A revocation ends an uncommitted approval and undoes no commitment; it does not stop an attempt already recorded."
