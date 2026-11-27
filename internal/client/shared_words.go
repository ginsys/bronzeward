package client

import (
	"fmt"
	"strings"
)

// SharedNext is the next step a person takes after a refusal about a shared fragment or profile
// (persistence-api.md §3.1.1, operator-experience.md §5.4), from the problem code and its members, or
// "" for any other refusal. The write routes answer them directly and a failed publication carries
// them in its operation's error, so one text serves both. Every value the server chose is printable.
func SharedNext(code string, doc map[string]any) string {
	s := func(k string) string { v, _ := doc[k].(string); return printable(v) }
	named := strings.TrimSpace(s("kind") + " " + s("name"))
	switch {
	case code == "validation-failed" && s("rule") == "library-reference":
		return "a shared document holds no secret reference, so a fragment that holds a secret stays in one cluster: set it there, without --library."
	case code == "validation-failed" && s("rule") == "library-removed":
		return fmt.Sprintf("the shared %s has no head to adopt: bring it back with a shared edit (fragment set --library or profile set --library) and publish that, then adopt it. A cluster that adopted it keeps its revision.", or(named, "name"))
	case code == "validation-failed" && s("rule") == "name-in-two-scopes":
		return fmt.Sprintf("this cluster holds the %s as its own and as a shared one: in the same draft remove one of them (rm for the cluster's own, adopt --remove to stop following the shared one) or copy the shared one into the cluster with detach, then publish again.", or(named, "name"))
	case code == "validation-failed" && s("rule") == "library-pin-conflict":
		return fmt.Sprintf("two sources of this release pin different revisions of the fragment %s: make them agree, by setting the profile that pins the older one again or by adopting the revision the other source pins, then publish again.", or(s("fragment"), "name"))
	case code == "validation-failed" && s("rule") == "detach-pin-not-in-view":
		return fmt.Sprintf("a profile of this cluster pins the fragment %s at a revision the cluster does not hold: adopt that fragment at the pinned revision (fragment adopt %s --revision <the pinned one>), or set the profile again, then publish again.", or(s("fragment"), "name"), or(s("fragment"), "<name>"))
	case code == "stale-input":
		return staleAdoption(doc)
	}
	return ""
}

// staleAdoption words the conflicts of a stale-input refusal that name a shared head (scope adoption):
// the adoption or the head the draft was based on moved.
func staleAdoption(doc map[string]any) string {
	cs, _ := doc["conflicts"].([]any)
	var moved []string
	for _, c := range cs {
		m, _ := c.(map[string]any)
		if m["scope"] != "adoption" {
			continue
		}
		moved = append(moved, fmt.Sprintf("%s was at adoption %s and is at %s", printable(fmt.Sprint(m["head"])), printable(fmt.Sprint(m["expected"])), printable(fmt.Sprint(m["actual"]))))
	}
	if len(moved) == 0 {
		return ""
	}
	return "the shared name moved after the draft was read (" + strings.Join(moved, "; ") + "): read the draft again with draft show, stage the adoption again in a new draft and publish that."
}

func or(s, none string) string {
	if s == "" {
		return none
	}
	return s
}
