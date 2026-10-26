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
	// Subject is the signed-in subject by the client's reading, "" for automation; Reading the
	// client's own reading of a token refused 401, and Cleared what an identity-revoked answer
	// cleared (§9.6).
	Subject, Reading, Cleared string
}

// Code is the problem code: the document's type without its URN prefix.
func (p *Problem) Code() string {
	t, _ := p.Doc["type"].(string)
	return strings.TrimPrefix(t, problemPrefix)
}

// Error shows title, code, detail when present and instance, never a request body
// (client-contracts.md §8.2, journeys §8), and what the operator can do for the codes a read meets.
// Each value the server or a token chose is printable (§6 item 4).
func (p *Problem) Error() string {
	s := fmt.Sprintf("%s: %s answered %d %s (%s)", p.Where, p.Route, p.Status, p.str("title"), printable(p.Code()))
	if d := p.str("detail"); d != "" {
		s += ": " + d
	}
	s += " (instance " + p.str("instance") + ")"
	switch p.Code() {
	case "unauthenticated":
		if p.Reading != "" {
			s += "\n" + printable(p.Reading)
		} else {
			s += "\nThe automation token was not accepted: it may be revoked, rotated, expired or from before a restore. The operator reissues it with the server-side command."
		}
	case "identity-revoked":
		s += "\nYour access was revoked or is denied by this installation; contact a recovery administrator."
		if p.Cleared != "" {
			s += " " + printable(p.Cleared)
		} else if p.Subject == "" {
			s += " The automation token file is left as it is."
		}
	case "forbidden":
		if p.Doc["humanOnly"] == true {
			s += "\nThis needs a person signed in, not an automation token."
		}
		// A plan member makes the detail the reason, not the roles (§9.6).
		if roles, ok := p.Doc["roles"].([]any); ok && p.Doc["plan"] == nil {
			s += "\nNeeds one of the roles " + printable(fmt.Sprint(roles)) + "."
		}
		if p.Subject != "" {
			s += " You are signed in as " + printable(p.Subject) + "."
		}
	case "recovery-mode-active":
		// A scope refusal names the machine and its scope state; without a scope it is the
		// process's own restriction (§8.2, §8.5 item 1).
		if scope := p.str("scope"); scope != "" {
			s += fmt.Sprintf("\nRecovery mode: the scope of machine %s is %s.", p.str("machine"), scope)
		} else {
			s += "\nRecovery mode: a process restriction of a server started for recovery, not of one scope."
		}
	}
	return s
}

func (p *Problem) str(k string) string { s, _ := p.Doc[k].(string); return printable(s) }

// Host is the server's host and port, as a trust failure names it.
func (x *Context) Host() string {
	if u, err := url.Parse(x.Server); err == nil {
		return u.Host
	}
	return x.Server
}
