package api

import (
	"strings"
	"testing"
)

// nameCases is every value shape of a machine's name (persistence-api.md §3.5): the grammar the API
// check, the database CHECK and the clients share. The database test reads the same table.
var nameCases = []struct {
	name  string
	value string
	ok    bool
}{
	{"one label", "web-1", true},
	{"one character", "a", true},
	{"a digit", "0", true},
	{"an all-digit label", "42", true},
	{"several labels", "alcorak.office.ginsys.net", true},
	{"two labels", "a.b", true},
	{"an inner hyphen", "a-b.c-d", true},
	{"a label of 63", strings.Repeat("a", 63) + ".example", true},
	{"253 characters", strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 61), true},
	{"a digit inside the last label", "api.k8s2", true},
	{"a dotted all-digit label before the last", "1.2.example", true},

	{"empty", "", false},
	{"uppercase", "Web-1", false},
	{"uppercase in a later label", "web.Example", false},
	{"underscore", "web_1", false},
	{"an underscore in a later label", "web.ex_ample", false},
	{"a leading hyphen", "-web", false},
	{"a trailing hyphen", "web-", false},
	{"a leading hyphen in a later label", "web.-example", false},
	{"a trailing hyphen in an earlier label", "web-.example", false},
	{"a leading dot", ".web", false},
	{"a trailing dot", "web.example.", false},
	{"an empty label", "web..example", false},
	{"a lone dot", ".", false},
	{"a space", "web 1", false},
	{"a control", "web\a", false},
	{"a trailing line feed", "web\n", false},
	{"a non-ASCII letter", "wéb", false},
	{"an identifier", "mch_aaaaaaaaaaaaaaaaaaaaaaaaaa", false},
	{"a label of 64", strings.Repeat("a", 64), false},
	{"a label of 64 after a dot", "a." + strings.Repeat("a", 64), false},
	{"254 characters", strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 62), false},
	{"an address", "10.0.0.1", false},
	{"an address shape of one octet", "web.1", false},
	{"an all-digit last label", "web.123", false},
}

// TestMachineNameShape: the API check accepts exactly the names of the grammar, and a one-label name
// of digits stays valid as it was before the grammar took dots.
func TestMachineNameShape(t *testing.T) {
	for _, c := range nameCases {
		t.Run(c.name, func(t *testing.T) {
			err := machineName(c.value)
			if c.ok && err != nil {
				t.Errorf("%q refused: %v", c.value, err)
			}
			if !c.ok && err == nil {
				t.Errorf("%q accepted", c.value)
			}
		})
	}
}
