package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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
// plan ID typed back sends it; there is no --yes. An approval starts no operation, so approve does
// not wait.
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
		if err := getJSON(c, path, &p); err != nil {
			return err
		}
		tw := tabwriter.NewWriter(e.err, 0, 0, 2, ' ', tabwriter.StripEscape) // as output.go's, for planView's verbatim diff lines
		printLines(tw, planView(c, &p))
		printLines(tw, predict(c, &p))
		if p.Operation == "apply-config" && p.DriftRecord != nil {
			fmt.Fprintf(tw, "Overwrites\tthe drifted configuration of drift record %d, which no reviewer has seen.\n", *p.DriftRecord)
			fmt.Fprintln(tw, "\tApproving records that you approve overwriting it.")
		}
		tw.Flush()
		return e.confirmID(p.Id)
	}
	return e.writeAfter(confirm, &client.Write{Method: http.MethodPost, Path: path + "/approvals", Body: []byte("{}"), HumanOnly: true, Repeat: rp,
		Check: "plan show " + client.ShellWord(plan),
		Read: func(c *client.Client) (string, error) {
			var now apitypes.PlanBody
			err := getJSON(c, path, &now)
			return "GET /api/v1" + path + " shows state " + now.State + ", approval " + or(now.Approval, "none"), err
		}}, func(_ *client.Client, r *client.Response) ([]string, error) {
		var a apitypes.ApprovalBody
		if err := json.Unmarshal(r.Body, &a); err != nil || e.output != "table" {
			return nil, err
		}
		lines := []string{fmt.Sprintf("Approved (%s). Recorded self-approval reasons: %s.", a.Id, orList(a.SelfApproval.Reasons))}
		if a.UnseenOverwrite {
			lines = append(lines, "Overwrites\tthe drifted configuration, which no reviewer has seen: this approval records that you approve overwriting it.")
		}
		if p.Operation == "adopt" {
			return append(lines, "The controller records the adoption without sending anything; Applied becomes "+p.Release+" (adoption) once it does."), nil
		}
		return append(lines, "The controller re-checks the plan at use time; it applies it only if every check passes."), nil
	})
}

// confirmID asks for id typed back: from standard input, a terminal or one line of a pipe, unless
// --token-stdin took it, then from the controlling terminal. A wrong or empty answer sends nothing.
func (e *env) confirmID(id string) error {
	in := io.Reader(stdin)
	if e.tokenStdin {
		tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return notConfirmed{errors.New("--token-stdin took standard input and there is no terminal to type the plan ID on; nothing was sent")}
		}
		defer tty.Close()
		in = tty
	}
	fmt.Fprint(e.err, "Type the plan ID to approve, or press Enter to stop: ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if !isTerminal(stdin) || e.tokenStdin {
		fmt.Fprintln(e.err)
	}
	switch got := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"); {
	case got == id:
		return nil
	case got == "" && (err == nil || errors.Is(err, io.EOF)):
		return notConfirmed{errors.New("stopped: no plan ID was typed; nothing was sent")}
	case err != nil && !errors.Is(err, io.EOF):
		return notConfirmed{fmt.Errorf("reading the plan ID: %w; nothing was sent", err)}
	}
	return notConfirmed{errors.New("the typed text is not the plan ID " + id + "; nothing was sent")}
}

// predict is T21's self-approval prediction (journeys §5.6), labelled as one: created-plan from the
// plan's creator, published from the release's publisher, authored-change and authored-reused from
// the authors of the release's source revisions and the revisions its draft introduced
// (internal/api/approvals.go selfApprovalReasons). Import-base authorship and owned automation
// cannot be predicted, so the approver is never presented as clear.
func predict(c *client.Client, p *apitypes.PlanBody) []string {
	var reasons, unread []string
	var me string
	var id apitypes.IdentityBody
	if err := getJSON(c, "/identity", &id); err != nil {
		unread = append(unread, "your identity: "+err.Error())
	} else if id.Principal != nil {
		me = *id.Principal
	}
	if me != "" && p.CreatedBy.Principal == me {
		reasons = append(reasons, "created-plan (you created this plan)")
	}
	var rel apitypes.ReleaseBody
	if err := getJSON(c, "/releases/"+url.PathEscape(p.Release), &rel); err != nil {
		unread = append(unread, "the release: "+err.Error())
	} else {
		if me != "" && rel.PublishedBy.Principal == me {
			reasons = append(reasons, "published (you published its release)")
		}
		authored, unreadAuthors := authoredReasons(c, &rel, me)
		reasons, unread = append(reasons, authored...), append(unread, unreadAuthors...)
	}
	said := "no reason from the plan's creator, the release's publisher or its fragment, profile and assignment revisions."
	if len(reasons) > 0 {
		said = strings.Join(reasons, ", ") + "."
	}
	lines := []string{"Self-approval\tpredicted: " + said,
		"\tImport-base authorship and automation you own are not predicted. The approval records the actual reasons; self-approval is marked and allowed."}
	for _, u := range unread {
		lines = append(lines, "\tNot predicted, since this read failed: "+u)
	}
	return lines
}

// authoredReasons are authored-change and authored-reused for me in rel: a source revision me
// authored is a change when rel's draft introduced it, else reused.
func authoredReasons(c *client.Client, rel *apitypes.ReleaseBody, me string) (reasons, unread []string) {
	if me == "" {
		return nil, nil // a human with no principal row has authored nothing
	}
	var d struct {
		Entries []struct {
			Revision *string `json:"revision"`
		} `json:"entries"`
	}
	if err := getJSON(c, "/drafts/"+url.PathEscape(rel.Draft), &d); err != nil {
		return nil, []string{"the release's draft: " + err.Error()}
	}
	introduced := map[string]bool{}
	for _, en := range d.Entries {
		introduced[or(en.Revision, "")] = true
	}
	var change, reused bool
	for _, s := range rel.Sources {
		if s.Revision == nil {
			continue
		}
		var r struct {
			Author string `json:"author"`
		}
		if err := getJSON(c, "/"+s.Kind+"-revisions/"+url.PathEscape(*s.Revision), &r); err != nil {
			unread = append(unread, "the "+s.Kind+" revision "+*s.Revision+": "+err.Error())
		} else if r.Author == me {
			change, reused = change || introduced[*s.Revision], reused || !introduced[*s.Revision]
		}
	}
	if change {
		reasons = append(reasons, "authored-change (you authored a revision its release's draft introduced)")
	}
	if reused {
		reasons = append(reasons, "authored-reused (you authored a revision its release reuses)")
	}
	return reasons, unread
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
	var current string
	return showThen(e, "/approvals/"+url.PathEscape(pos[0]), func(c *client.Client, _ *client.Response, _ *apitypes.ApprovalBody) error {
		current = c.Context.LastEpoch
		return nil
	}, func(w *tabwriter.Writer, a *apitypes.ApprovalBody) {
		fmt.Fprintf(w, "Approval %s of plan %s, epoch %s\n", a.Id, a.Plan, a.Epoch)
		fmt.Fprintf(w, "  Approver\t%s (%s)\n", a.Approver.Principal, a.Approver.Role)
		fmt.Fprintf(w, "  Self-approval\trecorded reasons: %s\n", orList(a.SelfApproval.Reasons))
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
			return "GET /api/v1/plans/" + a.Plan + " shows state " + p.State + ", approval " + or(p.Approval, "none"), err
		}}, func(c *client.Client, r *client.Response) ([]string, error) {
		var x apitypes.ApprovalRevocationBody
		if err := json.Unmarshal(r.Body, &x); err != nil || e.output != "table" {
			return nil, err
		}
		lines := []string{fmt.Sprintf("Revocation of approval %s recorded by %s (%s), act %s.", x.Approval, x.RevokedBy, x.Role, x.Act)}
		var p apitypes.PlanBody
		if err := getJSON(c, "/plans/"+url.PathEscape(x.Plan), &p); err != nil {
			return append(lines, "The plan "+x.Plan+" cannot be read: "+err.Error(), revokeLimits), nil
		}
		switch p.State {
		case "revoked":
			return append(lines, "The plan "+p.Id+" is revoked: it cannot be committed. "+revokeLimits), nil
		case "committed":
			op := or(p.CommittedOperation, "?")
			return append(lines, "The plan "+p.Id+" stays committed to operation "+op+". "+revokeLimits,
				"Follow it with: bronzewardctl operation watch "+client.ShellWord(op)), nil
		}
		return append(lines, "The plan "+p.Id+" is "+p.State+", with approval "+or(p.Approval, "none")+". "+revokeLimits), nil
	})
}

// revokeLimits is what a revocation does not do (T22, journeys §8, internal/api/approval_revocations.go).
const revokeLimits = "A revocation ends an uncommitted approval and undoes no commitment; it does not stop an attempt already recorded."
