package main

import (
	"os"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// itemEight is client contracts A9 item 8, whitespace collapsed so a sentence the document wraps still matches.
func itemEight(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../docs/spec/client-contracts.md")
	if err != nil {
		t.Fatal(err)
	}
	_, item, ok := strings.Cut(string(b), "8. **A plan says when it waits for another machine's hostname**")
	if !ok {
		t.Fatal("client-contracts.md has no A9 item 8")
	}
	item, _, ok = strings.Cut(item, "**A10.")
	if !ok {
		t.Fatal("client-contracts.md has no A10 after A9 item 8")
	}
	return strings.Join(strings.Fields(item), " ")
}

// TestHostWaits: the waiting sentence is the document's own, word for word, with the machines named as A8 rule 1 names
// them in the CLI, and says "waits", never "refused" or "failed" (A9 item 8).
func TestHostWaits(t *testing.T) {
	item := itemEight(t)
	got := hostWaits("node-a", "node-a", "node-b")
	const want = "This plan waits. The last release applied to machine node-a records the Kubernetes node name node-a, and this release gives the same name to machine node-b. " +
		"This plan has sent nothing to either machine. Apply the release for machine node-a first, or give machine node-b another hostname."
	if got != want {
		t.Errorf("sentence\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(item, want) {
		t.Errorf("A9 item 8 does not hold the sentence %q", want)
	}
	if !strings.Contains(item, hostWaitsUnread) {
		t.Errorf("A9 item 8 does not hold the sentence %q", hostWaitsUnread)
	}
	for _, s := range []string{got, hostWaitsUnread} {
		if strings.Contains(s, "refused") || strings.Contains(s, "failed") {
			t.Errorf("%q says refused or failed", s)
		}
	}
	// The three values are three things: the holder, the node name and this machine, each where the sentence puts it.
	if g := hostWaits("web-1", "cp-9", "web-2"); !strings.Contains(g, "machine web-1 records the Kubernetes node name cp-9, and this release gives the same name to machine web-2.") ||
		!strings.Contains(g, "Apply the release for machine web-1 first, or give machine web-2 another hostname.") {
		t.Errorf("values in the wrong places: %q", g)
	}
}

// TestWaitsMachine: a machine is named by its name where it has one, else by its whole identifier (A8 rule 1, CLI).
func TestWaitsMachine(t *testing.T) {
	name, empty := "node-a", ""
	for _, c := range []struct {
		label string
		name  *string
		want  string
	}{{"a name", &name, "node-a"}, {"no name", nil, "mch_k2pq3rstuvwxyz234567abcdef"}, {"an empty name", &empty, "mch_k2pq3rstuvwxyz234567abcdef"}} {
		if got := waitsMachine("mch_k2pq3rstuvwxyz234567abcdef", c.name); got != c.want {
			t.Errorf("%s: %q, want %q", c.label, got, c.want)
		}
	}
}

// TestNodeNameKind: the review's nodeName is absent (not recorded), null (not compared) or a string, and the typed
// body cannot tell the first two apart (a *string with omitempty), so the raw body says.
func TestNodeNameKind(t *testing.T) {
	for _, c := range []struct {
		label, body string
		want        nodeNameKind
	}{
		{"absent", `{"hostname":{"source":"machine-name","name":"node-a"}}`, nodeNameAbsent},
		{"null", `{"hostname":{"source":"configuration","nodeName":null}}`, nodeNameNull},
		{"a string", `{"hostname":{"source":"machine-name","name":"node-a","nodeName":"node-a"}}`, nodeNameSet},
		{"no hostname member", `{"release":"rel_a"}`, nodeNameAbsent},
		{"not JSON", `not json`, nodeNameUnknown},
	} {
		if got := nodeNameKindOf([]byte(c.body)); got != c.want {
			t.Errorf("%s: kind %d, want %d", c.label, got, c.want)
		}
	}
}

// TestNodeNotice: where no node-name clash was checked, a review ends with the one notice of A9 item 8, in the document's
// words and naming no cause and no value; a source whose item 7 sentence already says so, and one this client does not
// know, get none.
func TestNodeNotice(t *testing.T) {
	item := itemEight(t)
	line := []string{"Hostname: x"}
	for _, c := range []struct {
		label  string
		source string
		kind   nodeNameKind
		lines  []string
		want   string
	}{
		{"absent, machine-name", "machine-name", nodeNameAbsent, line, nodeNotCheckedOld},
		{"absent, unrecorded", "unrecorded", nodeNameAbsent, line, nodeNotCheckedOld},
		{"absent, none", "none", nodeNameAbsent, line, nodeNotCheckedOld},
		{"null, machine-name", "machine-name", nodeNameNull, line, nodeNotCheckedHidden},
		{"null, configuration", "configuration", nodeNameNull, line, nodeNotCheckedHidden},
		{"null, none", "none", nodeNameNull, line, ""},
		{"null, legacy-stable", "legacy-stable", nodeNameNull, line, ""},
		{"a name", "machine-name", nodeNameSet, line, ""},
		{"an unknown source", "from-the-future", nodeNameNull, line, ""},
		{"no line to follow", "machine-name", nodeNameNull, nil, ""},
		{"an unreadable body", "machine-name", nodeNameUnknown, line, ""},
	} {
		got := withNodeNotice(c.lines, apitypes.HostnameBody{Source: apitypes.HostnameBodySource(c.source)}, c.kind)
		if c.want == "" {
			if len(got) != len(c.lines) {
				t.Errorf("%s: %q, want no notice", c.label, got)
			}
			continue
		}
		if len(got) != len(c.lines)+1 || got[len(got)-1] != c.want {
			t.Errorf("%s: %q, want the notice last", c.label, got)
		}
	}
	for _, s := range []string{nodeNotCheckedHidden, nodeNotCheckedOld} {
		if !strings.Contains(item, s) {
			t.Errorf("A9 item 8 does not hold the sentence %q", s)
		}
	}
}
