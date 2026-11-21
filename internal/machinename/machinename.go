// Package machinename is the grammar of a machine's name (persistence-api.md §3.5), shared by the API
// check and the command line's onboarding, so that the two cannot drift. The database CHECK on
// machine.name, the OpenAPI pattern and the web client's constant state the same grammar in their own
// languages.
package machinename

import (
	"regexp"
	"strconv"
	"strings"
)

// A name is a hostname: one or more labels joined by a dot, 253 characters at most; a label is 1 to 63
// of lowercase a-z, digits and the hyphen, neither the first nor the last a hyphen. With a dot, the last
// label is not all digits, so that an address is no name; one label has no such limit. Uppercase is
// refused, never folded.
var (
	shape      = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
	numberTail = regexp.MustCompile(`\.[0-9]+$`)
)

// Valid reports whether s is a name.
func Valid(s string) bool {
	return len(s) <= 253 && shape.MatchString(s) && !numberTail.MatchString(s)
}

// FromHostname is the name a machine takes from the hostname its node reports (operator experience
// §5.2): the hostname lowercased, when that is a valid name. Otherwise name is empty and note says why;
// note is also set, with the name, where lowercasing changed it. The hostname is remote text: note holds
// it quoted and cut at 64 characters.
func FromHostname(hostname string) (name, note string) {
	if hostname == "" {
		return "", "no name: the node reported no hostname, so it shows its address"
	}
	name = strings.ToLower(hostname)
	if !Valid(name) {
		shown := hostname
		if r := []rune(shown); len(r) > 64 {
			shown = string(r[:64]) + "…"
		}
		return "", "no name: the hostname " + strconv.Quote(shown) + " is not a valid name"
	}
	if name != hostname {
		return name, "named by its hostname in lower case"
	}
	return name, ""
}
