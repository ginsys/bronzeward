// Package contract is the list of Talos contracts Bronzeward supports, apart from the version of the
// Talos library it is built with (compilation.md §10.2, choice §16.33). Adding a cluster, publishing
// and discovery all read it, so they agree on which contracts they accept.
package contract

import (
	"regexp"
	"strconv"

	"github.com/siderolabs/talos/pkg/machinery/compatibility/talos114"
)

// Entry is one supported contract and the Kubernetes versions it runs: from Min, inclusive, to Max,
// exclusive, compared on major, minor and patch (compatibility.KubernetesVersion.SupportedWith).
type Entry struct {
	Contract string
	Min, Max [3]uint64
}

// List is a list of supported contracts.
type List []Entry

// Supported is the list every place reads. It holds v1.14 only: a contract joins it with evidence
// for it and with the checks compilation.md §10.2 names, never by being linkable. Its window is the
// machinery's own constants for that minor, which stay in the module after the library moves on.
var Supported = List{{
	Contract: "v1.14",
	Min:      [3]uint64{talos114.MinimumKubernetesVersion.Major, talos114.MinimumKubernetesVersion.Minor, talos114.MinimumKubernetesVersion.Patch},
	Max:      [3]uint64{talos114.MaximumKubernetesVersion.Major, talos114.MaximumKubernetesVersion.Minor, talos114.MaximumKubernetesVersion.Patch},
}}

var (
	// moduleVersion is a canonical Go module version, as the release's column holds it.
	moduleVersion = regexp.MustCompile(`^v(0|[1-9][0-9]{0,3})\.(0|[1-9][0-9]{0,3})\.(0|[1-9][0-9]{0,3})` +
		`(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?$`)
	contractRe = regexp.MustCompile(`^v(0|[1-9][0-9]{0,3})\.(0|[1-9][0-9]{0,3})$`)
)

// Minor is the Talos contract minor of a machinery module version, "v1.14" for "v1.14.5"; false when
// the version is not a canonical module version.
func Minor(version string) (string, bool) {
	m := moduleVersion.FindStringSubmatch(version)
	if m == nil {
		return "", false
	}
	return "v" + m[1] + "." + m[2], true
}

// Listed reports whether contract is on the list and the library, a machinery module version, is not
// older than it. A contract newer than the library is refused because the library renders its own
// contract silently instead (E3 §4.1); an older one is rendered as itself.
func (l List) Listed(contract, library string) bool {
	for _, e := range l {
		if e.Contract == contract {
			return e.within(library)
		}
	}
	return false
}

// Contracts are the listed contracts the library can render, in the list's order.
func (l List) Contracts(library string) []string {
	var out []string
	for _, e := range l {
		if e.within(library) {
			out = append(out, e.Contract)
		}
	}
	return out
}

// Window is the Kubernetes window of a listed contract, whatever the library: from min, inclusive,
// to max, exclusive.
func (l List) Window(contract string) (min, max [3]uint64, ok bool) {
	for _, e := range l {
		if e.Contract == contract {
			return e.Min, e.Max, true
		}
	}
	return min, max, false
}

// within reports whether the entry is well formed and no newer than the library's minor.
func (e Entry) within(library string) bool {
	want := contractRe.FindStringSubmatch(e.Contract)
	have := moduleVersion.FindStringSubmatch(library)
	if want == nil || have == nil {
		return false
	}
	for i := 1; i <= 2; i++ {
		if w, h := number(want[i]), number(have[i]); w != h {
			return w < h
		}
	}
	return true
}

// number is a numeric part the patterns matched: at most four digits, so it parses.
func number(s string) uint64 {
	n, _ := strconv.ParseUint(s, 10, 64)
	return n
}
