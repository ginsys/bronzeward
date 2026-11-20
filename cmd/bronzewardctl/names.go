package main

import (
	"encoding/json"
	"net/url"
	"slices"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
	"github.com/ginsys/bronzeward/internal/id"
)

// principalReads is the most identifiers one GET /principals read takes (persistence-api.md §9.2).
const principalReads = 100

// preload reads who each of ids names, for the table output that prints them (client contracts A7):
// the identifiers not read yet in this run, each once, in requests of at most principalReads. -o json
// and -o yaml print the identifiers and read nothing. A failed read is no failure of the command: its
// identifiers stay unnamed and are printed as they are, with nothing of the failure said. An
// identifier the route would refuse, and with it the whole read, is never sent. What the route
// answers is kept for this run only and never reaches a log or an error.
func (e *env) preload(c *client.Client, ids ...string) {
	if e.output != "table" {
		return
	}
	var want []string
	for _, s := range ids {
		if _, asked := e.names[s]; !asked && !slices.Contains(want, s) && id.MustHave(s, id.Principal) == nil {
			want = append(want, s)
		}
	}
	if e.names == nil && len(want) > 0 {
		e.names = map[string]*apitypes.PrincipalBody{}
	}
	for chunk := range slices.Chunk(want, principalReads) {
		for _, s := range chunk {
			e.names[s] = nil
		}
		r, err := c.GetOnce("/principals?" + url.Values{"id": chunk}.Encode())
		if err != nil {
			continue
		}
		var got struct {
			Items []apitypes.PrincipalBody `json:"items"`
		}
		if json.Unmarshal(r.Body, &got) != nil {
			continue
		}
		for i := range got.Items {
			if _, asked := e.names[got.Items[i].Id]; asked && slices.Contains(chunk, got.Items[i].Id) {
				e.names[got.Items[i].Id] = &got.Items[i]
			}
		}
	}
}

// principalKeys are the members of a timeline entry's untyped record that hold a principal.
var principalKeys = []string{"principal", "decidedBy", "requestedBy"}

// entryPrincipals are the principals a timeline entry names.
func entryPrincipals(entry any) []string {
	raw, _ := json.Marshal(entry)
	var d map[string]any
	_ = json.Unmarshal(raw, &d)
	var ids []string
	for _, k := range principalKeys {
		if s, ok := d[k].(string); ok {
			ids = append(ids, s)
		}
	}
	return ids
}

// named puts what who prints in the place of each principal in d, the record of a timeline entry.
func (e *env) named(d map[string]any) {
	for _, k := range principalKeys {
		if s, ok := d[k].(string); ok {
			d[k] = e.who(s)
		}
	}
}

// who is what the table output prints for the principal s: what preload read of it, or s itself. The
// text is remote text, and goes through tf or sf like every other (client contracts §6 item 8).
func (e *env) who(s string) string { return principalText(e.names[s], s) }

// principalText is a human by its display name, or by subject and issuer when it has none; a service
// by its name; the identifier itself for one answered unknown, one not read, or an item that names
// nobody. The CLI prints full principal identifiers today and keeps that for these: a terminal user
// copies them. The web client shows the same text for the same item, with the identifier shortened.
func principalText(p *apitypes.PrincipalBody, s string) string {
	if p == nil {
		return s
	}
	switch p.Kind {
	case "human":
		switch {
		case p.Display != nil && *p.Display != "":
			return *p.Display
		case p.Subject != nil && *p.Subject != "" && p.Issuer != nil && *p.Issuer != "":
			return *p.Subject + " at " + *p.Issuer
		}
	case "service":
		if p.Service != nil && *p.Service != "" {
			return *p.Service
		}
	}
	return s
}
