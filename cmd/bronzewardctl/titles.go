package main

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// Titles of releases, plans and operations (client contracts A8): built from fields the bodies carry,
// printed beside the identifier, which stays in its own column. Each distinct draft, release, plan,
// ingestion and machine is read once for the command, by GET with one try. A draft, release or plan
// read that fails leaves the object with no title, and its identifier alone is shown; a machine read
// that fails names the machine by its identifier, and an ingestion read that fails is an import with no
// machine to name (A8 rule 1). What was read is kept for this run only and never reaches a log or an
// error. -o json and -o yaml read nothing and print the resource unchanged. The text is remote text and
// goes through tf or sf like every other (client contracts §6 item 8).

// holdBody keeps the body read at path, nil for a read that failed, so it is not read again.
func (e *env) holdBody(path string, body any) {
	if e.bodies == nil {
		e.bodies = map[string]any{}
	}
	e.bodies[path] = body
}

// titleBody is the body at path as read once in this run, nil when the read or its decoding failed.
func titleBody[T any](e *env, c *client.Client, path string) *T {
	if v, ok := e.bodies[path]; ok {
		t, _ := v.(*T)
		return t
	}
	var t *T
	if r, err := c.GetOnce(path); err == nil {
		var v T
		if json.Unmarshal(r.Body, &v) == nil {
			t = &v
		}
	}
	e.holdBody(path, t)
	return t
}

// heldBody is the body titleBody already read at path in this run, nil when none was read or the read failed.
func heldBody[T any](e *env, path string) *T {
	t, _ := e.bodies[path].(*T)
	return t
}

func titlePath(kind, id string) string { return "/" + kind + "/" + url.PathEscape(id) }

// draftTitle is a draft's title, "" when it could not be read or holds none.
func (e *env) draftTitle(c *client.Client, id string) string {
	if d := titleBody[apitypes.DraftBody](e, c, titlePath("drafts", id)); d != nil {
		return d.Title
	}
	return ""
}

// machineTitle is a machine as a title names it: its name beside its identifier (client contracts A9),
// the whole identifier when it has none or could not be read.
func (e *env) machineTitle(c *client.Client, id string) string {
	if m := titleBody[apitypes.MachineBody](e, c, titlePath("machines", id)); m != nil {
		return machineLabel(id, m.Name)
	}
	return id
}

// releaseTitle is "<draft title>, published <date>", "" while the draft's title is unknown.
func (e *env) releaseTitle(c *client.Client, r *apitypes.ReleaseBody) string {
	d := e.draftTitle(c, r.Draft)
	if d == "" {
		return ""
	}
	return d + ", published " + when(r.PublishedAt)
}

// planTitle is "<Apply or Adopt> of <machine> to <release title>", "" while that title or the
// plan's operation is unknown.
func (e *env) planTitle(c *client.Client, p *apitypes.PlanBody) string {
	verb := map[string]string{"apply-config": "Apply", "adopt": "Adopt"}[p.Operation]
	r := titleBody[apitypes.ReleaseBody](e, c, titlePath("releases", p.Release))
	if verb == "" || r == nil {
		return ""
	}
	rt := e.releaseTitle(c, r)
	if rt == "" {
		return ""
	}
	return verb + " of " + e.machineTitle(c, p.Machine) + " to " + rt
}

// operationTitle is "Publish of <draft title>", "Import of <machine>" ("Import" when its ingestion names
// no machine, is not named or could not be read), or its plan's title for apply-config and adopt.
func (e *env) operationTitle(c *client.Client, o *apitypes.OperationBody) string {
	s := o.Subject
	switch o.Kind {
	case "publish":
		if s != nil && s.Draft != nil {
			if d := e.draftTitle(c, *s.Draft); d != "" {
				return "Publish of " + d
			}
		}
	case "ingest":
		if s != nil && s.Ingestion != nil {
			if ing := titleBody[apitypes.IngestionBody](e, c, titlePath("ingestions", *s.Ingestion)); ing != nil {
				return e.ingestionTitle(c, ing)
			}
		}
		return "Import"
	case "apply-config", "adopt":
		if s != nil && s.Plan != nil {
			if p := titleBody[apitypes.PlanBody](e, c, titlePath("plans", *s.Plan)); p != nil {
				return e.planTitle(c, p)
			}
		}
	}
	return ""
}

// ingestionTitle is "Import of <machine>", "Import" when the ingestion names no machine.
func (e *env) ingestionTitle(c *client.Client, ing *apitypes.IngestionBody) string {
	if ing.Machine == nil {
		return "Import"
	}
	return "Import of " + e.machineTitle(c, *ing.Machine)
}

// titleSet keeps the title of the object kind/id for the rows of the table, when there is one.
func (e *env) titleSet(kind, id, text string) {
	if text == "" {
		return
	}
	if e.titles == nil {
		e.titles = map[string]string{}
	}
	e.titles[kind+"/"+id] = text
}

// titleOf is the title kept for the object, "" for none.
func (e *env) titleOf(kind, id string) string { return e.titles[kind+"/"+id] }

// titleCell is a title in a table column: a dash for none.
func (e *env) titleCell(kind, id string) string {
	if t := e.titleOf(kind, id); t != "" {
		return t
	}
	return "-"
}

func (e *env) titleReleases(c *client.Client, rs ...apitypes.ReleaseBody) {
	for i := range rs {
		if e.output == "table" {
			e.titleSet("release", rs[i].Id, e.releaseTitle(c, &rs[i]))
		}
	}
}

func (e *env) titlePlans(c *client.Client, ps ...apitypes.PlanBody) {
	for i := range ps {
		if e.output == "table" {
			e.titleSet("plan", ps[i].Id, e.planTitle(c, &ps[i]))
		}
	}
}

func (e *env) titleOperations(c *client.Client, ops ...apitypes.OperationBody) {
	for i := range ops {
		if e.output == "table" {
			e.titleSet("operation", ops[i].Id, e.operationTitle(c, &ops[i]))
		}
	}
}

// kindOfID is the kind an identifier's prefix names (persistence-api.md section 2), for the six kinds that
// have a title; any other prefix is "" and its identifier is shown as it is.
func kindOfID(id string) string {
	prefix, _, ok := strings.Cut(id, "_")
	if !ok {
		return ""
	}
	return map[string]string{"rel": "release", "pln": "plan", "op": "operation", "drf": "draft", "ing": "ingestion", "mch": "machine"}[prefix]
}

// titleRef keeps the title of the object kind/id that a command names beside its identifier, read once
// for the command through the cache above; a failed read keeps none (client contracts A8, ginsys/bronzeward#1017).
func (e *env) titleRef(c *client.Client, kind, id string) {
	if e.output != "table" || id == "" || e.titleOf(kind, id) != "" {
		return
	}
	var t string
	switch kind {
	case "draft":
		t = e.draftTitle(c, id)
	case "machine":
		if m := titleBody[apitypes.MachineBody](e, c, titlePath("machines", id)); m != nil {
			t = machineLabel(id, m.Name)
		}
	case "release":
		if r := titleBody[apitypes.ReleaseBody](e, c, titlePath("releases", id)); r != nil {
			t = e.releaseTitle(c, r)
		}
	case "plan":
		if p := titleBody[apitypes.PlanBody](e, c, titlePath("plans", id)); p != nil {
			t = e.planTitle(c, p)
		}
	case "operation":
		if o := titleBody[apitypes.OperationBody](e, c, titlePath("operations", id)); o != nil {
			t = e.operationTitle(c, o)
		}
	case "ingestion":
		if ing := titleBody[apitypes.IngestionBody](e, c, titlePath("ingestions", id)); ing != nil {
			t = e.ingestionTitle(c, ing)
		}
	}
	e.titleSet(kind, id, t)
}

// titleRefs is titleRef for each identifier of one kind; "" is skipped.
func (e *env) titleRefs(c *client.Client, kind string, ids ...string) {
	for _, id := range ids {
		e.titleRef(c, kind, id)
	}
}

// titleIngestion holds an ingestion the command read, so a title built on it is not read again, and
// reads the titles of the draft and the operation it names.
func (e *env) titleIngestion(c *client.Client, b *apitypes.IngestionBody) {
	if e.output == "table" {
		e.holdBody(titlePath("ingestions", b.Id), b)
	}
	e.titleRefs(c, "draft", b.Draft)
	e.titleRefs(c, "operation", or(b.Operation, ""))
}

// titleSubjects is titleRef for each act subject whose prefix names a kind.
func (e *env) titleSubjects(c *client.Client, subjects []string) {
	for _, s := range subjects {
		if k := kindOfID(s); k != "" {
			e.titleRef(c, k, s)
		}
	}
}

// ref is an identifier followed by its title in parentheses, as a machine is by its name (A9): the
// identifier alone when no title was kept. A machine's kept title is that whole label already.
func (e *env) ref(kind, id string) string {
	t := e.titleOf(kind, id)
	switch {
	case t == "":
		return id
	case kind == "machine":
		return t
	}
	return id + " (" + t + ")"
}

// refp is ref for an optional identifier: nil for none.
func (e *env) refp(kind string, id *string) *string {
	if id == nil {
		return nil
	}
	r := e.ref(kind, *id)
	return &r
}

// refSubjects is an act's subjects, each followed by its title where its prefix names a kind.
func (e *env) refSubjects(subjects []string) string {
	out := make([]string, len(subjects))
	for i, s := range subjects {
		out[i] = s
		if k := kindOfID(s); k != "" {
			out[i] = e.ref(k, s)
		}
	}
	return strings.Join(out, ", ")
}
