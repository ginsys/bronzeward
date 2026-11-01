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
		lines := []string{fmt.Sprintf("Publishing draft %s revision %s (operation %s).", d, rev, op.Id)}
		if noWait {
			lines = append(lines, "Nothing has been sent to any machine. Follow it with: bronzewardctl operation show "+op.Id)
		}
		return lines, nil
	})
	if err != nil || noWait || op.Id == "" {
		return err
	}
	defer func() { err = errors.Join(err, c.Save()) }()
	o, err := c.Watch(op.Id, client.Watch{
		Show:  func(ev client.Event) { fmt.Fprintf(e.err, "  %s\n", ev.Type) },
		Clear: func() { fmt.Fprintln(e.err, "  (the events above no longer hold)") },
	})
	if err != nil {
		return err
	}
	if o.State != "succeeded" {
		return fmt.Errorf("%s: publication operation %s %s: %s; nothing was sent to any machine", c.Context.Where(), o.Id, o.State, compact(o.Error))
	}
	keep(e, c, d, "")
	var res apitypes.PublishResult
	if o.Result != nil {
		res, err = o.Result.AsPublishResult()
	}
	if o.Result == nil || err != nil || res.Release == "" {
		return fmt.Errorf("%s: publication operation %s succeeded without naming its release: %s", c.Context.Where(), o.Id, compact(o.Result))
	}
	lines, err := nextSteps(c, res.Release)
	if err != nil || e.output != "table" {
		return err
	}
	_, err = fmt.Fprintln(e.out, strings.Join(lines, "\n"))
	return err
}

// nextSteps names the release and, for each of its machines, the plan it needs: an adopt plan
// hands over a machine with no Applied release, an apply-config plan the others. Plans are made
// with #258's commands; this names them.
func nextSteps(c *client.Client, release string) ([]string, error) {
	r, err := c.Get("/releases/" + url.PathEscape(release))
	if err != nil {
		return nil, err
	}
	var rel apitypes.ReleaseBody
	if err := json.Unmarshal(r.Body, &rel); err != nil {
		return nil, fmt.Errorf("GET /api/v1/releases/%s cannot be read: %w", url.PathEscape(release), err)
	}
	lines := []string{fmt.Sprintf("Publication succeeded: release %s is now Desired for %d machines. Nothing has been sent to any machine.", rel.Id, len(rel.Machines))}
	for _, m := range rel.Machines {
		r, err := c.Get("/machines/" + url.PathEscape(m.Machine))
		var x apitypes.MachineBody
		if err == nil {
			err = json.Unmarshal(r.Body, &x)
		}
		switch {
		case err != nil:
			lines = append(lines, fmt.Sprintf("  %s: its Applied release cannot be read: %v", m.Machine, err))
		case x.Applied == nil:
			lines = append(lines, fmt.Sprintf("  %s has no Applied release: hand it over with an adopt plan.", m.Machine))
		default:
			lines = append(lines, fmt.Sprintf("  %s has Applied release %s: plan an apply-config.", m.Machine, x.Applied.Release))
		}
	}
	return append(lines, "Review a machine's rendered configuration: bronzewardctl release review "+rel.Id+" <machine>"), nil
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
				ref, at := p.Reference, or(p.Output, "-")
				if p.Member != nil {
					ref += fmt.Sprintf("#%d", *p.Member)
				}
				if p.OverriddenBy != nil {
					at = "overridden by " + p.OverriddenBy.Revision
				}
				fmt.Fprintf(w, "%s\t%d\t%s, %s\t%s\n", ref, p.Version, p.Source.Revision, verbatim(p.Source.Path), verbatim(at))
			}
			fmt.Fprintln(w, "Provenance covers secret references only; other fields are not traced.")
			return
		}
		fmt.Fprintf(w, "Release %s, machine %s, validated: %s\n", r.Release, r.Machine, r.Mode)
		if r.Configuration == nil {
			fmt.Fprintln(w, "Configuration not shown: it could not be redacted.")
			if r.Notice != nil && *r.Notice != "" {
				fmt.Fprintln(w, "Server notice: "+verbatim(*r.Notice))
			}
			return
		}
		for _, l := range strings.Split(strings.TrimSuffix(*r.Configuration, "\n"), "\n") {
			fmt.Fprintln(w, verbatim(l))
		}
	})
}
