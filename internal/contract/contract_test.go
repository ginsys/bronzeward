package contract

import (
	"slices"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/compatibility/talos114"
)

// A listed contract is supported when the library is not older than it (compilation.md §10.2): a
// newer one would be rendered silently as the library's own (E3 §4.1), an older one is rendered as
// itself. The list here holds entries the real one does not, so that each side of the comparison is
// exercised.
func TestListed(t *testing.T) {
	l := List{{Contract: "v1.9"}, {Contract: "v1.14"}, {Contract: "v1.15"}, {Contract: "v1.100"}}
	for name, c := range map[string]struct {
		contract, library string
		want              bool
	}{
		"the library's own minor":              {"v1.14", "v1.14.5", true},
		"a library one minor newer":            {"v1.14", "v1.15.0", true},
		"a prerelease of a newer minor":        {"v1.14", "v1.15.0-beta.1", true},
		"a prerelease of the listed minor":     {"v1.15", "v1.15.0-beta.1", true},
		"a listed contract newer than library": {"v1.15", "v1.14.5", false},
		"a numerically larger minor":           {"v1.100", "v1.14.5", false},
		"a numerically smaller minor":          {"v1.9", "v1.14.5", true},
		"a contract no entry names":            {"v1.13", "v1.14.5", false},
		"a contract of no form":                {"1.14", "v1.14.5", false},
		"a contract with a patch":              {"v1.14.5", "v1.14.5", false},
		"an empty contract":                    {"", "v1.14.5", false},
		"a leading zero":                       {"v1.014", "v1.14.5", false},
		"an empty library":                     {"v1.14", "", false},
		"a library minor only":                 {"v1.14", "v1.14", false},
		"a development library":                {"v1.14", "devel", false},
		"a library with a leading zero":        {"v1.14", "v1.014.5", false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := l.Listed(c.contract, c.library); got != c.want {
				t.Errorf("Listed(%q, %q) = %v, want %v", c.contract, c.library, got, c.want)
			}
		})
	}
}

// Contracts lists the entries a library of that version can render, in the list's order.
func TestContracts(t *testing.T) {
	l := List{{Contract: "v1.13"}, {Contract: "v1.14"}, {Contract: "v1.15"}}
	for library, want := range map[string][]string{
		"v1.14.5":        {"v1.13", "v1.14"},
		"v1.15.0-beta.1": {"v1.13", "v1.14", "v1.15"},
		"v1.12.12":       nil,
		"devel":          nil,
	} {
		if got := l.Contracts(library); !slices.Equal(got, want) {
			t.Errorf("Contracts(%q) = %v, want %v", library, got, want)
		}
	}
}

// The window is the contract's own, whatever the library: v1.14's is the machinery's talos114
// window (compilation.md §10.2).
func TestWindow(t *testing.T) {
	min, max, ok := Supported.Window("v1.14")
	wantMin := [3]uint64{talos114.MinimumKubernetesVersion.Major, talos114.MinimumKubernetesVersion.Minor, talos114.MinimumKubernetesVersion.Patch}
	wantMax := [3]uint64{talos114.MaximumKubernetesVersion.Major, talos114.MaximumKubernetesVersion.Minor, talos114.MaximumKubernetesVersion.Patch}
	if !ok || min != wantMin || max != wantMax {
		t.Errorf("Window(v1.14) = %v %v %v, want %v %v", min, max, ok, wantMin, wantMax)
	}
	if _, _, ok := Supported.Window("v1.15"); ok {
		t.Error("a contract no entry names has a window")
	}
}

// The list holds v1.14 only (ginsys/bronzeward#1213): a contract joins it with evidence for it
// (ginsys/bronzeward#1212), never by being linkable.
func TestSupportedHoldsV114Only(t *testing.T) {
	if got := Supported.Contracts("v1.99.0"); !slices.Equal(got, []string{"v1.14"}) {
		t.Errorf("the list holds %v, want [v1.14]", got)
	}
}

func TestMinor(t *testing.T) {
	for in, want := range map[string]string{
		"v1.14.5": "v1.14", "v1.15.0-beta.1": "v1.15", "v2.0.0": "v2.0",
		"": "", "v1.14": "", "1.14.5": "", "v1.014.5": "", "devel": "",
	} {
		got, ok := Minor(in)
		if got != want || ok != (want != "") {
			t.Errorf("Minor(%q) = %q, %v, want %q", in, got, ok, want)
		}
	}
}
