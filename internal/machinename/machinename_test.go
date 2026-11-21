package machinename

import (
	"strings"
	"testing"
)

// TestFromHostname: the name a node's hostname gives, and the note that says why it gives none or how
// lowercasing changed it. The grammar's own value shapes are tested where the API and the database
// state it (internal/api).
func TestFromHostname(t *testing.T) {
	long := strings.Repeat(strings.Repeat("a", 63)+".", 3) + strings.Repeat("a", 61)
	for _, c := range []struct{ name, hostname, want, note string }{
		{"a short hostname", "cp-1", "cp-1", ""},
		{"a full hostname", "alcorak.office.ginsys.net", "alcorak.office.ginsys.net", ""},
		{"a one-label hostname of digits", "42", "42", ""},
		{"the longest hostname", long, long, ""},
		{"upper case", "Alcorak.Office.Ginsys.Net", "alcorak.office.ginsys.net", "named by its hostname in lower case"},
		{"no hostname", "", "", "no name: the node reported no hostname"},
		{"an underscore", "W_2", "", `no name: the hostname "W_2" is not a valid name`},
		{"a trailing dot", "alcorak.office.", "", `no name: the hostname "alcorak.office." is not a valid name`},
		{"an address", "10.0.0.1", "", `no name: the hostname "10.0.0.1" is not a valid name`},
		{"a hostname cut at 64 characters", long + "a", "", `no name: the hostname "` + long[:64] + `…" is not a valid name`},
		{"a control character", "web\a", "", `no name: the hostname "web\a" is not a valid name`},
	} {
		t.Run(c.name, func(t *testing.T) {
			name, note := FromHostname(c.hostname)
			if name != c.want || note != c.note {
				t.Errorf("FromHostname(%q) = %q, %q; want %q, %q", c.hostname, name, note, c.want, c.note)
			}
		})
	}
}
