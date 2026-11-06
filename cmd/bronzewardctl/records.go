package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// These reads of operator-experience.md §4: the machine timeline and observations, source heads
// and revisions, drafts, releases, plans, operations and acts. A show prints one API
// resource; a list reads one page, or with --all every page, as listing does.

// show prints the resource at path; table gets it decoded.
func show[T any](e *env, path string, table func(*tabwriter.Writer, *T)) error {
	return showThen(e, path, nil, table)
}

// showThen is show, running then (when not nil) on the decoded answer before it prints it; an
// error from then prints nothing.
func showThen[T any](e *env, path string, then func(*client.Client, *client.Response, *T) error, table func(*tabwriter.Writer, *T)) error {
	return e.session(func(c *client.Client, _ *client.Response, _ *apitypes.ServerBody) error {
		r, err := c.Get(path)
		if err != nil {
			return err
		}
		v, err := decode[T](c, r.Body)
		if err != nil {
			return err
		}
		if then != nil {
			if err := then(c, r, &v[0]); err != nil {
				return err
			}
		}
		return e.emit(r.Body, func(w *tabwriter.Writer) { table(w, &v[0]) })
	})
}

// list prints the items at path with q, read as l asks, that keep accepts (nil keeps all): under
// header one row each, the next page's cursor, then foot. An empty page that is not the whole list
// (one with a next cursor, or one reached by a cursor) says so. A filter the API does not serve (G9)
// applies to the items read; -o then prints the items kept, with the page's next cursor.
func list[T any](e *env, l listing, path string, q url.Values, keep func(*T) bool, header, foot string, row func(*tabwriter.Writer, *T)) error {
	return e.session(func(c *client.Client, _ *client.Response, _ *apitypes.ServerBody) error {
		raw, items, next, err := l.read(c, path, q, nil)
		if err != nil {
			return err
		}
		vs, err := decode[T](c, items...)
		if err != nil {
			return err
		}
		if keep != nil {
			kept, keptRaw := vs[:0], []json.RawMessage{}
			for i := range vs {
				if keep(&vs[i]) {
					kept, keptRaw = append(kept, vs[i]), append(keptRaw, items[i])
				}
			}
			doc := map[string]any{"items": keptRaw}
			if next != "" {
				doc["next"] = next
			}
			if raw, err = json.Marshal(doc); err != nil {
				return err
			}
			vs = kept
		}
		return e.emit(raw, func(w *tabwriter.Writer) {
			switch {
			case len(vs) == 0 && (next != "" || l.cursor != ""):
				fmt.Fprintln(w, "None on this page.")
			case len(vs) == 0:
				fmt.Fprintln(w, "None found.")
			default:
				fmt.Fprintln(w, header)
			}
			for i := range vs {
				row(w, &vs[i])
			}
			more(w, next)
			if foot != "" {
				fmt.Fprintln(w, foot)
			}
		})
	})
}

// verb parses the global flags before a noun's verb and returns the verb, one of verbs, and the
// arguments after it.
func (e *env) verb(noun string, args []string, verbs ...string) (string, []string, error) {
	pos, err := e.parse(noun, args, -1, nil)
	if err != nil {
		return "", nil, err
	}
	if len(pos) == 0 || !slices.Contains(verbs, pos[0]) {
		return "", nil, usageError{fmt.Errorf("%s needs %s", noun, strings.Join(verbs, " or "))}
	}
	return pos[0], pos[1:], nil
}

// filters declares one string flag per name, for parse.
func filters(into map[string]*string) func(*flag.FlagSet) {
	return func(fs *flag.FlagSet) {
		for name, p := range into {
			fs.StringVar(p, name, "", "")
		}
	}
}

// values are the non-empty filters, as a query.
func values(into map[string]*string) url.Values {
	q := url.Values{}
	for name, p := range into {
		if *p != "" {
			q.Set(name, *p)
		}
	}
	return q
}

func when(t time.Time) string { return t.Format("2006-01-02 15:04:05 MST") }

func or(s *string, none string) string {
	if s == nil {
		return none
	}
	return *s
}

func orInt(n *int, none string) string {
	if n == nil {
		return none
	}
	return fmt.Sprint(*n)
}

// secs is a number of seconds, or "-".
func secs(n *int) string {
	if n == nil {
		return "-"
	}
	return fmt.Sprintf("%ds", *n)
}

// lines splits a document or a diff on its own line feeds and escapes each line, the tab and the
// carriage return included (client-contracts.md §6 item 8). Its line feeds are its lines, so they
// are the one line feed that is not escaped.
func lines(w *tabwriter.Writer, indent, text string) {
	for _, l := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		fmt.Fprintln(w, indent+client.Printable(l))
	}
}

func compact(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// machineObservations reads a machine's observations.
func (e *env) machineObservations(args []string) error {
	var l listing
	pos, err := e.paged("machine observations", args, 1, &l, nil)
	if err != nil {
		return err
	}
	return list(e, l, "/machines/"+url.PathEscape(pos[0])+"/observations", url.Values{}, nil, "OBSERVATION\tAT\tPURPOSE\tBASIS\tENDPOINT\tUNREAD", "Health is not checked in this installation.",
		func(w *tabwriter.Writer, o *apitypes.ObservationBody) {
			var unread []string
			for k, u := range o.Unread {
				unread = append(unread, k+": "+u.Cause)
			}
			slices.Sort(unread)
			tf(w, "%s\t%s\t%s\t%d\t%s\t%s\n", o.Id, when(o.At), o.Purpose, o.Basis, o.Endpoint,
				map[bool]string{true: "nothing"}[len(unread) == 0]+strings.Join(unread, "; "))
		})
}

// head prints a source head: what its revision is now, or that it was removed (persistence-api.md
// §3.1).
func head(w *tabwriter.Writer, kind, id, what, cluster string, rev *string, n int, created time.Time) {
	tf(w, "%s %s\t%s\tcluster %s\n", kind, id, what, cluster)
	if rev == nil {
		tf(w, "Revision\tnone: removed at head revision %d\n", n)
	} else {
		tf(w, "Revision\t%s, head revision %d\tbronzewardctl %s show %s\n", *rev, n, strings.ToLower(kind), *rev)
	}
	tf(w, "Created\t%s\n", when(created))
}

// sourceShow reads a head, or a revision when id has the revision prefix, or a head's revisions:
// a revision's row is its history line, which show follows with its detail.
func sourceShow[H, R any](e *env, noun, revPrefix string, args []string, h func(*tabwriter.Writer, *H), row, detail func(*tabwriter.Writer, *R)) error {
	v, rest, err := e.verb(noun, args, "show", "history")
	if err != nil {
		return err
	}
	var l listing
	pos, err := e.paged(noun+" "+v, rest, 1, &l, nil)
	if err != nil {
		return err
	}
	id := url.PathEscape(pos[0])
	switch {
	case v == "history":
		return list(e, l, "/"+noun+"s/"+id+"/revisions", url.Values{}, nil, "REVISION\tAUTHOR\tCREATED\tCONTENT", "", row)
	case l != listing{}:
		return usageError{fmt.Errorf("%s show reads one resource: --limit, --cursor and --all are history's", noun)}
	case strings.HasPrefix(pos[0], revPrefix+"_"):
		return show(e, "/"+noun+"-revisions/"+id, func(w *tabwriter.Writer, x *R) {
			row(w, x)
			detail(w, x)
		})
	}
	return show(e, "/"+noun+"s/"+id, h)
}

func (e *env) fragmentCmd(args []string) error {
	if v, rest, err := e.verb("fragment", args, "show", "history", "set", "rm"); err != nil {
		return err
	} else if v == "set" {
		return e.fragmentSet(rest)
	} else if v == "rm" {
		return e.sourceRm(apitypes.SourceEntryKindFragment, rest)
	}
	return sourceShow(e, "fragment", "frv", args, func(w *tabwriter.Writer, f *apitypes.FragmentBody) {
		head(w, "Fragment", f.Id, f.Name+", layer "+f.Layer+", scope "+f.Scope, f.Cluster, f.Revision, f.HeadRevision, f.CreatedAt)
	}, func(w *tabwriter.Writer, r *apitypes.FragmentRevisionBody) {
		tf(w, "%s\t%s\t%s\tfragment %s, layer %s\n", r.Id, r.Author, when(r.CreatedAt), r.Name, r.Layer)
	}, func(w *tabwriter.Writer, r *apitypes.FragmentRevisionBody) {
		tf(w, "Cluster\t%s\n", r.Cluster)
		var refs []string
		for name, ref := range r.Declarations.References {
			refs = append(refs, fmt.Sprintf("%s (%s, version %d)", name, ref.Kind, ref.Version))
		}
		slices.Sort(refs)
		tf(w, "References\t%s\n", map[bool]string{true: "none"}[len(refs) == 0]+strings.Join(refs, ", "))
		fmt.Fprintln(w, "Document (sanitized: secret values are references):")
		lines(w, "  ", r.Document)
	})
}

func (e *env) profileCmd(args []string) error {
	if v, rest, err := e.verb("profile", args, "show", "history", "set", "rm"); err != nil {
		return err
	} else if v == "set" {
		return e.profileSet(rest)
	} else if v == "rm" {
		return e.sourceRm(apitypes.SourceEntryKindProfile, rest)
	}
	return sourceShow(e, "profile", "prv", args, func(w *tabwriter.Writer, p *apitypes.ProfileBody) {
		head(w, "Profile", p.Id, p.Name+", scope "+p.Scope, p.Cluster, p.Revision, p.HeadRevision, p.CreatedAt)
	}, func(w *tabwriter.Writer, r *apitypes.ProfileRevisionBody) {
		tf(w, "%s\t%s\t%s\tprofile %s pins %s\n", r.Id, r.Author, when(r.CreatedAt), r.Name, strings.Join(r.Fragments, ", "))
	}, func(w *tabwriter.Writer, r *apitypes.ProfileRevisionBody) {
		tf(w, "Cluster\t%s\n", r.Cluster)
	})
}

func (e *env) assignmentCmd(args []string) error {
	if v, rest, err := e.verb("assignment", args, "show", "history", "set", "rm"); err != nil {
		return err
	} else if v == "set" {
		return e.assignmentSet(rest)
	} else if v == "rm" {
		return e.sourceRm(apitypes.SourceEntryKindAssignment, rest)
	}
	return sourceShow(e, "assignment", "asr", args, func(w *tabwriter.Writer, a *apitypes.AssignmentBody) {
		head(w, "Assignment", a.Id, "machine "+a.Machine, a.Cluster, a.Revision, a.HeadRevision, a.CreatedAt)
	}, func(w *tabwriter.Writer, r *apitypes.AssignmentRevisionBody) {
		parts := []string{"profiles: " + map[bool]string{true: "none"}[len(r.Profiles) == 0] + strings.Join(r.Profiles, ", ")}
		for _, layer := range []string{"global", "site", "cluster", "role", "workload", "override"} {
			if names := r.Fragments[layer]; len(names) > 0 {
				parts = append(parts, layer+": "+strings.Join(names, ", "))
			}
		}
		tf(w, "%s\t%s\t%s\tmachine %s, %s\n", r.Id, r.Author, when(r.CreatedAt), r.Machine, strings.Join(parts, "; "))
	}, func(w *tabwriter.Writer, r *apitypes.AssignmentRevisionBody) {
		tf(w, "Cluster\t%s\n", r.Cluster)
	})
}

func (e *env) draftCmd(args []string) error {
	if v, rest, err := e.verb("draft", args, "list", "show", "create", "discard"); err != nil {
		return err
	} else if v == "create" {
		return e.draftCreate(rest)
	} else if v == "discard" {
		return e.draftDiscard(rest)
	}
	var l listing
	var cluster string
	v, pos, err := e.noun("draft", args, &l, filters(map[string]*string{"cluster": &cluster}))
	if err != nil {
		return err
	}
	if v == "list" {
		return list(e, l, "/drafts", url.Values{}, func(d *apitypes.DraftBody) bool { return cluster == "" || d.Cluster == cluster },
			"DRAFT\tCLUSTER\tSTATE\tREVISION\tENTRIES\tTITLE", "", func(w *tabwriter.Writer, d *apitypes.DraftBody) {
				tf(w, "%s\t%s\t%s\t%d\t%d\t%s\n", d.Id, d.Cluster, d.State, d.Revision, len(d.Entries), d.Title)
			})
	}
	// T11: the read's ETag is the next write's If-Match while the draft is open; a closed draft's
	// is dropped, and so is one read at an epoch older than the newest this command saw (§8.5
	// item 1): the GET /server a new epoch calls for can show a newer one still.
	return showThen(e, "/drafts/"+url.PathEscape(pos[0]), func(c *client.Client, r *client.Response, d *apitypes.DraftBody) error {
		current := d.State == "open" && r.Header.Get("Bronzeward-Epoch") == c.Context.LastEpoch
		keep(e, c, d.Id, map[bool]string{true: r.Header.Get("ETag")}[current])
		return nil
	}, func(w *tabwriter.Writer, d *apitypes.DraftBody) {
		tf(w, "Draft %s \"%s\" %s, revision %d, cluster %s\n", d.Id, client.Quoted(d.Title), d.State, d.Revision, d.Cluster)
		for _, item := range d.Entries {
			x, err := item.AsSourceEntry()
			if err != nil {
				tf(w, "  unreadable entry\t%v\n", err)
				continue
			}
			name, change := or(x.Name, or(x.Machine, "")), ""
			switch {
			case x.Kind == "import-base":
				change = "import base"
			case x.Revision == nil:
				change = "removed"
			case x.Head == nil:
				change = "new"
			default:
				change = "changed (based on head revision " + orInt(x.Base, "none") + ")"
			}
			tf(w, "  %s\t%s\t%s\t%s\n", x.Kind, name, or(x.Revision, "-"), change)
		}
		fmt.Fprintln(w, "Not rendered: publish to see each machine's configuration.")
	})
}

func (e *env) releaseCmd(args []string) error {
	if v, rest, err := e.verb("release", args, "list", "show", "review", "provenance"); err != nil {
		return err
	} else if v == "review" || v == "provenance" {
		return e.releaseReview(v, rest)
	}
	var l listing
	var cluster, draft string
	v, pos, err := e.noun("release", args, &l, filters(map[string]*string{"cluster": &cluster, "draft": &draft}))
	if err != nil {
		return err
	}
	if v == "list" {
		return list(e, l, "/releases", url.Values{}, func(r *apitypes.ReleaseBody) bool {
			return (cluster == "" || r.Cluster == cluster) && (draft == "" || r.Draft == draft)
		}, "RELEASE\tCLUSTER\tDRAFT\tPUBLISHED\tBY\tMACHINES", "", func(w *tabwriter.Writer, r *apitypes.ReleaseBody) {
			tf(w, "%s\t%s\t%s r%d\t%s\t%s (%s)\t%d\n", r.Id, r.Cluster, r.Draft, r.DraftRevision, when(r.PublishedAt),
				r.PublishedBy.Principal, r.PublishedBy.Role, len(r.Machines))
		})
	}
	return show(e, "/releases/"+url.PathEscape(pos[0]), func(w *tabwriter.Writer, r *apitypes.ReleaseBody) {
		tf(w, "Release %s\tcluster %s\tpublished by %s (%s) %s\n", r.Id, r.Cluster, r.PublishedBy.Principal, r.PublishedBy.Role, when(r.PublishedAt))
		tf(w, "Draft\t%s revision %d, operation %s\n", r.Draft, r.DraftRevision, r.Operation)
		tf(w, "Renderer\tcontract %s, machinery %s, Kubernetes %s\n", r.Renderer.Contract, r.Renderer.MachineryVersion, r.Renderer.KubernetesVersion)
		fmt.Fprintln(w, "Built from (source revisions, not configuration):")
		for _, s := range r.Sources {
			tf(w, "  %s\t%s\t%s\thead revision %d\n", s.Kind, s.Head, or(s.Revision, "removed"), s.HeadRevision)
		}
		tf(w, "Rendered configuration per machine (%d):\n", len(r.Machines))
		for _, m := range r.Machines {
			tf(w, "  %s\tvalidated: %s\tobservation %s\n", m.Machine, m.Mode, m.PublicationObservation)
		}
	})
}

func (e *env) operationCmd(args []string) error {
	if a, err := e.parse("operation", args, -1, nil); err == nil && len(a) > 0 && a[0] == "watch" {
		return e.operationWatch(a[1:])
	}
	var l listing
	var plan, machine, state, kind string
	f := map[string]*string{"plan": &plan, "machine": &machine, "state": &state, "kind": &kind}
	v, pos, err := e.noun("operation", args, &l, filters(f))
	if err != nil {
		return err
	}
	if v == "list" {
		return list(e, l, "/operations", values(f), nil, "OPERATION\tKIND\tSTATE\tSUBJECT\tCREATED", "", func(w *tabwriter.Writer, o *apitypes.OperationBody) {
			tf(w, "%s\t%s\t%s\t%s\t%s\n", o.Id, o.Kind, o.State, subject(o.Subject), when(o.CreatedAt))
		})
	}
	// A cancelled operation records no cause; its last event holds it. The event is read once and
	// kept only when it is of the epoch of the operation shown; its time is not shown.
	var cause *lastEvent
	findCause := func(c *client.Client, r *client.Response, o *apitypes.OperationBody) error {
		if e.output == "table" && o.State == "cancelled" {
			var err error
			if cause, err = readLastOrStop(c, pos[0], o.State, r.Header.Get("Bronzeward-Epoch")); err != nil {
				return err // a Ctrl-C taken by the event read ends the command
			}
			if cause != nil {
				cause.At = nil
			}
		}
		return nil
	}
	return showThen(e, "/operations/"+url.PathEscape(pos[0]), findCause, func(w *tabwriter.Writer, o *apitypes.OperationBody) {
		tf(w, "Operation %s\t%s\t%s\n", o.Id, o.Kind, o.State)
		tf(w, "Subject\t%s\n", subject(o.Subject))
		by := "the controller"
		if o.CreatedBy != nil {
			by = o.CreatedBy.Principal + " (" + o.CreatedBy.Role + ")"
		}
		tf(w, "Created\t%s by %s\tepoch %s, last event %d\n", when(o.CreatedAt), by, o.Epoch, o.LastEvent)
		tf(w, "Result\t%s\n", map[bool]string{true: "none"}[o.Result == nil]+map[bool]string{false: compact(o.Result)}[o.Result == nil])
		tf(w, "Error\t%s\n", map[bool]string{true: "none"}[o.Error == nil]+map[bool]string{false: compact(o.Error)}[o.Error == nil])
		if o.Attempts != nil {
			for _, a := range *o.Attempts {
				tf(w, "Attempt %d\t%s at %s\tcontroller %s, generation %d, route %s, classification %s\n", a.Number, a.Id, when(a.At),
					a.Controller, a.Generation, a.Route, orInt(a.Classification, "none"))
			}
		}
		if o.State == "unresolved" || client.Terminal(o.State) {
			lines, _ := outcome(o, cause)
			fmt.Fprintln(w, strings.Join(lines, "\n"))
		}
	})
}

// unresolvedGuidance says what releases an unresolved operation (execution-recovery.md §4). With
// no attempt, the server retries it, the controller cancels it when its approval can no longer be
// used, or a recovery administrator resolves it as cancelled. Otherwise every recorded attempt
// must be accounted for, and the server refuses a second accounting of one; once all are, the
// server retries or settles it from a new observation.
func unresolvedGuidance(attempts *[]apitypes.AttemptBody) string {
	const hold = " No conflicting work on this machine proceeds until then."
	if attempts == nil || len(*attempts) == 0 {
		return "Unresolved: no attempt was recorded. The server retries it once an observation shows that is safe, " +
			"the controller cancels it when its approval can no longer be used, or a recovery administrator resolves it as cancelled." + hold
	}
	var open []string
	for _, a := range *attempts {
		if a.Accounting == nil {
			open = append(open, fmt.Sprint(a.Number))
		}
	}
	if len(open) == 0 {
		return "Unresolved: every attempt is accounted for. The server retries or settles it from a new observation." + hold
	}
	noun := "attempt"
	if len(open) > 1 {
		noun = "attempts"
	}
	return "Unresolved: completion and retry safety are unknown. A recovery administrator must account for " +
		noun + " " + strings.Join(open, ", ") + "." + hold
}

// subject names what an operation acts on.
func subject(s *apitypes.OperationSubject) string {
	if s == nil {
		return "none"
	}
	var parts []string
	for _, p := range []struct {
		name string
		v    *string
	}{{"plan", s.Plan}, {"machine", s.Machine}, {"draft", s.Draft}, {"ingestion", s.Ingestion}} {
		if p.v != nil {
			parts = append(parts, p.name+" "+*p.v)
		}
	}
	if s.DraftRevision != nil {
		parts = append(parts, "draft revision "+fmt.Sprint(*s.DraftRevision))
	}
	return strings.Join(parts, ", ")
}

func (e *env) actCmd(args []string) error {
	v, rest, err := e.verb("act", args, "list")
	if err != nil {
		return err
	}
	var l listing
	if _, err := e.paged("act "+v, rest, 0, &l, nil); err != nil {
		return err
	}
	return list(e, l, "/acts", url.Values{}, nil, "ACT\tAT\tACTION\tPRINCIPAL\tROLE\tVIA\tSUBJECTS", "", func(w *tabwriter.Writer, a *apitypes.ActItem) {
		tf(w, "%s\t%s\t%s\t%s (%s)\t%s\t%s\t%s\n", a.Id, when(a.At), a.Action, a.Principal, a.PrincipalKind, or(a.Role, "-"),
			a.Via, strings.Join(a.Subjects, ", "))
	})
}
