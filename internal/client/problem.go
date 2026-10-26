package client

import (
	"fmt"
	"net/url"
	"strings"
)

// Problem is a refusal the server answered with a Bronzeward problem document (RFC 9457).
type Problem struct {
	Status int
	Doc    map[string]any
	Route  string
	Where  string
}

// Code is the problem code: the document's type without its URN prefix.
func (p *Problem) Code() string {
	t, _ := p.Doc["type"].(string)
	return strings.TrimPrefix(t, problemPrefix)
}

// Error shows title, code, detail when present and instance, never a request body
// (client-contracts.md §8.2, journeys §8), and what the operator can do for the codes a read meets.
func (p *Problem) Error() string {
	s := fmt.Sprintf("%s: %s answered %d %s (%s)", p.Where, p.Route, p.Status, p.str("title"), p.Code())
	if d := p.str("detail"); d != "" {
		s += ": " + d
	}
	s += " (instance " + p.str("instance") + ")"
	switch p.Code() {
	case "unauthenticated":
		s += "\nThe automation token was not accepted: it may be revoked, rotated, expired or from before a restore. The operator reissues it with the server-side command."
	case "identity-revoked":
		s += "\nYour access was revoked or is denied by this installation; contact a recovery administrator. The automation token file is left as it is."
	case "forbidden":
		if p.Doc["humanOnly"] == true {
			s += "\nThis needs a person signed in, not an automation token."
		}
		if roles, ok := p.Doc["roles"].([]any); ok {
			s += fmt.Sprintf("\nNeeds one of the roles %v.", roles)
		}
	}
	return s
}

func (p *Problem) str(k string) string { s, _ := p.Doc[k].(string); return s }

// Host is the server's host and port, as a trust failure names it.
func (x *Context) Host() string {
	if u, err := url.Parse(x.Server); err == nil {
		return u.Host
	}
	return x.Server
}
