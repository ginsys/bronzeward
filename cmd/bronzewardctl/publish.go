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

// publish publishes a draft (POST /drafts/{id}/publications, publisher; decision T18 of #256,
// operator-experience.md §5.5). It sends the draft's kept ETag (T11) and, unless --no-wait, follows
// the publish operation with the progress reader (T16), its events on standard error. On success
// it forgets the draft's ETag, since a published draft takes no write, and names the release and
// what each of its machines needs next. A publication sends nothing to any machine.
func (e *env) publish(args []string) (err error) {
	var noWait bool
	var rf repeatFlags
	pos, err := e.parse("publish", args, 1, func(fs *flag.FlagSet) {
		fs.BoolVar(&noWait, "no-wait", false, "")
		rf.flags(fs)
	})
	if err != nil {
		return err
	}
	rp, err := rf.repeat(true)
	if err != nil {
		return err
	}
	d := pos[0]
	var c *client.Client
	var op apitypes.OperationBody
	err = e.writeAfter(func(cl *client.Client) error { c = cl; return nil }, &client.Write{Method: http.MethodPost,
		Path: "/drafts/" + url.PathEscape(d) + "/publications", Body: []byte("{}"), Draft: d, IfMatch: rf.ifMatch.v, Repeat: rp,
		Check: "operation list --kind publish", Read: entryRead(d, "", "")}, func(_ *client.Client, r *client.Response) ([]string, error) {
		if err := json.Unmarshal(r.Body, &op); err != nil {
			return nil, err
		}
		rev := "?"
		if op.Subject != nil {
			rev = orInt(op.Subject.DraftRevision, "?")
		}
		if op.Id == "" {
			return []string{fmt.Sprintf("Publishing draft %s revision %s.", d, rev)}, nil
		}
		lines := []string{fmt.Sprintf("Publishing draft %s revision %s (operation %s).", d, rev, pr(op.Id))}
		if noWait {
			lines = append(lines, "Nothing has been sent to any machine. Follow it with: bronzewardctl operation show "+pr(op.Id))
		}
		return lines, nil
	})
	switch {
	case err != nil:
		return err
	case op.Id == "":
		return fmt.Errorf("%s: the publication was accepted without naming its operation; find it with: bronzewardctl operation list --kind publish", c.Context.Where())
	case noWait:
		return nil
	}
	defer func() { err = errors.Join(err, c.Save()) }()
	o, err := c.Watch(op.Id, client.Watch{
		Show:  func(ev client.Event) { fmt.Fprintf(e.err, "  %s\n", pr(ev.Type)) },
		Clear: func() { fmt.Fprintln(e.err, "  (the events above no longer hold)") },
	})
	if o == nil {
		return err
	}
	// The operation's end decides the exit; a failed read of the events after it is reported beside.
	if err != nil {
		fmt.Fprintf(e.err, "bronzewardctl: %v; the operation's last events were not shown\n", err)
	}
	if o.State != "succeeded" {
		return operationFailed{fmt.Errorf("%s: publication operation %s %s: %s; nothing was sent to any machine", c.Context.Where(), pr(o.Id), pr(o.State), pr(compact(o.Error)))}
	}
	keep(e, c, d, "")
	var res apitypes.PublishResult
	if o.Result != nil {
		res, err = o.Result.AsPublishResult()
	}
	if o.Result == nil || err != nil || res.Release == "" {
		return fmt.Errorf("%s: publication operation %s succeeded without naming its release: %s", c.Context.Where(), pr(o.Id), pr(compact(o.Result)))
	}
	lines, err := nextSteps(c, res.Release)
	if err != nil || e.output != "table" {
		return err
	}
	_, err = fmt.Fprintln(e.out, strings.Join(lines, "\n"))
	return err
}

// pr escapes the control characters of text the server chose (client-contracts.md §6 item 4).
var pr = client.Printable

// nextSteps names the release and, for each of its machines, the plan create command it needs
// (decision T25 of #258, journeys §5.3, §5.5): an adopt plan hands over a machine with no Applied
// release, an apply-config plan the others. A machine whose Desired release is another one, after
// a later publication or when a repeat recovered an earlier one, gets no plan from this release.
// It names the commands and sends neither.
func nextSteps(c *client.Client, release string) ([]string, error) {
	r, err := c.Get("/releases/" + url.PathEscape(release))
	if err != nil {
		return nil, err
	}
	var rel apitypes.ReleaseBody
	if err := json.Unmarshal(r.Body, &rel); err != nil {
		return nil, fmt.Errorf("GET /api/v1/releases/%s cannot be read: %w", url.PathEscape(release), err)
	}
	machines := fmt.Sprintf("%d machines", len(rel.Machines))
	if len(rel.Machines) == 1 {
		machines = "1 machine"
	}
	lines, replaced := []string{""}, 0
	first, moves := r.Header.Get("Bronzeward-Epoch"), c.EpochMoves()
	for _, m := range rel.Machines {
		r, err := c.Get("/machines/" + url.PathEscape(m.Machine))
		var x apitypes.MachineBody
		if err == nil {
			err = json.Unmarshal(r.Body, &x)
		}
		// The machine answer's own epoch is compared, not the client's last one: a read that showed a
		// new epoch has already refreshed it (client-contracts.md section 8.5). A failed read has no
		// answer to ask, and its refresh can set the epoch back, so the count of moves stands for it.
		// Machines read before are of the earlier snapshot, so none is described.
		now := c.Context.LastEpoch
		if r != nil && r.Header.Get("Bronzeward-Epoch") != "" {
			now = r.Header.Get("Bronzeward-Epoch")
		}
		moved := first != "" && now != "" && now != first || c.EpochMoves() != moves
		if moved {
			change := sf("The epoch changed from %s to %s", client.ShellWord(first), client.ShellWord(now))
			if now == first {
				change = sf("The epoch changed and changed back from %s", client.ShellWord(first))
			}
			return []string{
				sf("Publication succeeded: release %s was published for %s. Nothing has been sent to any machine.", pr(rel.Id), machines),
				sf("  %s while the machines were read, so no next step is shown. Read them again with: release show %s",
					change, client.ShellWord(rel.Id)),
				"Review a machine's rendered configuration: bronzewardctl release review " + pr(rel.Id) + " <machine>"}, nil
		}
		switch {
		case err != nil:
			lines = append(lines, fmt.Sprintf("  %s: its Applied release cannot be read: %s", pr(m.Machine), pr(err.Error())))
		case x.Desired == nil:
			replaced++
			lines = append(lines, fmt.Sprintf("  %s has no Desired release: plan nothing for it from this release.", pr(m.Machine)))
		case *x.Desired != rel.Id:
			replaced++
			lines = append(lines, fmt.Sprintf("  %s has Desired release %s, not this one: plan nothing for it from this release.", pr(m.Machine), pr(*x.Desired)))
		case x.Applied == nil:
			lines = append(lines, fmt.Sprintf("  %s has no Applied release: hand it over with an adopt plan:", pr(m.Machine)),
				"    "+createLine(rel.Id, m.Machine, "adopt"))
		default:
			lines = append(lines, fmt.Sprintf("  %s has Applied release %s: plan an apply-config:", pr(m.Machine), pr(x.Applied.Release)),
				"    "+createLine(rel.Id, m.Machine, "apply-config"))
		}
	}
	lines[0] = fmt.Sprintf("Publication succeeded: release %s is now Desired for %s. Nothing has been sent to any machine.", pr(rel.Id), machines)
	if replaced > 0 {
		lines[0] = fmt.Sprintf("Publication succeeded: release %s was published for %s, but it is no longer Desired for %d of them. Nothing has been sent to any machine.",
			pr(rel.Id), machines, replaced)
	}
	return append(lines, "Review a machine's rendered configuration: bronzewardctl release review "+pr(rel.Id)+" <machine>"), nil
}

// releaseReview prints a machine's review of a release (GET /releases/{id}/machines/{m}/review,
// T18). review prints the rendered configuration verbatim, redaction tokens included
// (operator-experience.md §2.2 rule 1), or a withheld review's notice and no document; provenance
// lists exactly the records served, which cover secret references only (G6). -o json and yaml print
// the review unchanged: it is not protected (client-contracts.md §11.1).
func (e *env) releaseReview(verb string, args []string) error {
	pos, err := e.parse("release "+verb, args, 2, nil)
	if err != nil {
		return err
	}
	return show(e, "/releases/"+url.PathEscape(pos[0])+"/machines/"+url.PathEscape(pos[1])+"/review", func(w *tabwriter.Writer, r *apitypes.ReviewBody) {
		if verb == "provenance" {
			fmt.Fprintln(w, "REFERENCE\tVERSION\tFROM (REVISION, PATH)\tRENDERED AT")
			for _, p := range r.Provenance {
				ref, at := pr(p.Reference), pr(or(p.Output, "-"))
				if p.Member != nil {
					ref += fmt.Sprintf("#%d", *p.Member)
				}
				if p.OverriddenBy != nil {
					at = "overridden by " + pr(p.OverriddenBy.Revision)
				}
				fmt.Fprintf(w, "%s\t%d\t%s, %s\t%s\n", ref, p.Version, pr(p.Source.Revision), pr(p.Source.Path), at)
			}
			fmt.Fprintln(w, "Provenance covers secret references only; other fields are not traced.")
			return
		}
		fmt.Fprintf(w, "Release %s, machine %s, validated: %s\n", pr(r.Release), pr(r.Machine), pr(r.Mode))
		if r.Configuration == nil {
			fmt.Fprintln(w, "Configuration not shown: it could not be redacted.")
			if r.Notice != nil && *r.Notice != "" {
				fmt.Fprintln(w, "Server notice: "+pr(*r.Notice))
			}
			return
		}
		// The line feeds between the document's lines are the CLI's own; a tab or any other control
		// character in a line is escaped (client-contracts.md §6 item 8).
		for _, l := range strings.Split(strings.TrimSuffix(*r.Configuration, "\n"), "\n") {
			fmt.Fprintln(w, pr(l))
		}
	})
}
