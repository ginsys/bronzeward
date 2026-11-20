package main

import (
	"encoding/json"
	"net/url"

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
			if ing := titleBody[apitypes.IngestionBody](e, c, titlePath("ingestions", *s.Ingestion)); ing != nil && ing.Machine != nil {
				return "Import of " + e.machineTitle(c, *ing.Machine)
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
