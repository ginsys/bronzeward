package main

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// itemSeven is client contracts A9 item 7, whitespace collapsed so a sentence the document wraps still matches.
func itemSeven(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../docs/spec/client-contracts.md")
	if err != nil {
		t.Fatal(err)
	}
	_, item, ok := strings.Cut(string(b), "7. **A release says where each machine's hostname came from**")
	if !ok {
		t.Fatal("client-contracts.md has no A9 item 7")
	}
	item, _, ok = strings.Cut(item, "8. **A plan says when it waits")
	if !ok {
		t.Fatal("client-contracts.md has no A9 item 8 after item 7")
	}
	return strings.Join(strings.Fields(item), " ")
}

func hb(source string, name string) apitypes.HostnameBody {
	h := apitypes.HostnameBody{Source: apitypes.HostnameBodySource(source)}
	if name != "" {
		h.Name = &name
	}
	return h
}

func hostStr(s string) *string { return &s }

// TestHostnameLines: every line a review prints for a machine's hostname is a sentence of A9 item 7, word for
// word, apart from the restart sentence, which is item 6's. The cases are the document's own: each source, each
// kind of release last applied, a changed name, and the comparison that could not be read.
func TestHostnameLines(t *testing.T) {
	item := itemSeven(t)
	const taken = "Hostname: node-a, taken from the machine's name."
	const sets = "This release sets the machine's hostname to node-a, taken from its name."
	restart := renameReaches[1]
	node := "The machine's Kubernetes node name normally follows the hostname, so it may change too."
	for _, c := range []struct {
		name    string
		h       apitypes.HostnameBody
		applied hostnameApplied
		current *string
		want    []string
	}{
		{"machine-name, nothing applied", hb("machine-name", "node-a"), hostnameApplied{none: true}, hostStr("node-a"), []string{taken}},
		{"applied none", hb("machine-name", "node-a"), hostnameApplied{h: hb("none", "")}, hostStr("node-a"),
			[]string{taken, sets + " The last release applied to it left the hostname to the node. " + node, restart}},
		{"applied legacy-stable", hb("machine-name", "node-a"), hostnameApplied{h: hb("legacy-stable", "")}, hostStr("node-a"),
			[]string{taken, sets + " The last release applied to it left the hostname to the node. " + node, restart}},
		{"applied unrecorded", hb("machine-name", "node-a"), hostnameApplied{h: hb("unrecorded", "")}, hostStr("node-a"),
			[]string{taken, sets, restart}},
		{"applied configuration", hb("machine-name", "node-a"), hostnameApplied{h: hb("configuration", "")}, hostStr("node-a"),
			[]string{taken, "This release now takes the machine's hostname from its name, node-a, in place of the setting it had before.", node, restart}},
		{"applied another name", hb("machine-name", "node-b"), hostnameApplied{h: hb("machine-name", "node-a")}, hostStr("node-b"),
			[]string{"Hostname: node-b, taken from the machine's name.",
				"This release sets the machine's hostname to node-b, taken from its name. The last release applied to it set node-a.", node, restart}},
		{"applied the same name", hb("machine-name", "node-a"), hostnameApplied{h: hb("machine-name", "node-a")}, hostStr("node-a"), []string{taken}},
		{"the machine was renamed since", hb("machine-name", "node-a"), hostnameApplied{h: hb("machine-name", "node-a")}, hostStr("node-b"),
			[]string{taken, "This release used the earlier name node-a. While its hostname comes from the machine's name, the next release will use node-b."}},
		{"the comparison could not be read", hb("machine-name", "node-a"), hostnameApplied{unread: true}, nil,
			[]string{taken, hostUnread}},
		{"the comparison could not be read, and the machine was renamed", hb("machine-name", "node-a"), hostnameApplied{unread: true}, hostStr("node-b"),
			[]string{taken, hostUnread}},
		{"configuration", hb("configuration", ""), hostnameApplied{none: true}, nil, []string{"Hostname: set in the configuration. The machine's name does not change it."}},
		{"none", hb("none", ""), hostnameApplied{none: true}, nil, []string{"Hostname: not set in the configuration; the node chooses its own."}},
		{"legacy-stable", hb("legacy-stable", ""), hostnameApplied{none: true}, nil, []string{
			"Hostname: the configuration holds the legacy stable-hostname setting, so the machine's name is not used. Remove that setting to use the name."}},
		{"unrecorded", hb("unrecorded", ""), hostnameApplied{none: true}, nil, []string{"Hostname: not recorded for this release."}},
		{"a source this client does not know", hb("from-the-moon", ""), hostnameApplied{none: true}, nil, []string{"Hostname source: from-the-moon"}},
		{"a review that carries no source", hb("", ""), hostnameApplied{none: true}, nil, nil},
	} {
		got := hostnameLines(c.h, c.applied, c.current)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: lines = %q; want %q", c.name, got, c.want)
		}
		if c.h.Source == "from-the-moon" {
			continue
		}
		for _, l := range got {
			l = strings.Replace(l, "Hostname: node-b,", "Hostname: node-a,", 1) // the document gives the sentence once, for node-a
			if l != restart && !strings.Contains(item, l) {
				t.Errorf("%s: %q is no sentence of A9 item 7", c.name, l)
			}
		}
	}
	if strings.Contains(strings.Join(hostnameLines(hb("configuration", ""), hostnameApplied{none: true}, nil), "\n"), "node") {
		t.Error("a configuration hostname must print no value")
	}
}

const reviewBody = `{"release":"rel_a","machine":"mch_a","mode":"metal","configuration":"machine:\n  type: worker\n","provenance":[],"hostname":{"source":"machine-name","name":"node-a","nodeName":"node-a"}}`

func reviewRun(t *testing.T, answers map[string]string, extra ...string) (string, string) {
	t.Helper()
	setServerFacts(t, 16, nil)
	resetPosted()
	recoveryServer(t, answers, "{}")
	var out, errb bytes.Buffer
	args := append([]string{"release", "review", "rel_a", "mch_a"}, extra...)
	if code := run(args, &out, &errb); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	return out.String(), strings.Join(uris(), "\n")
}

func lineCount(list, line string) int { return strings.Count("\n"+list+"\n", "\n"+line+"\n") }

// TestReleaseReviewHostname: the review prints the hostname lines after its first line; the machine and the
// release last applied to it are each read once; a read that fails prints the one line saying so and no guess;
// -o json reads nothing more and prints the answer unchanged.
func TestReleaseReviewHostname(t *testing.T) {
	answers := map[string]string{
		"GET /api/v1/releases/rel_a/machines/mch_a/review": reviewBody,
		"GET /api/v1/machines/mch_a":                       `{"id":"mch_a","name":"node-a","applied":{"release":"rel_old","source":"operation"}}`,
		"GET /api/v1/releases/rel_old":                     `{"id":"rel_old","machines":[{"machine":"mch_b","hostname":{"source":"none"}},{"machine":"mch_a","hostname":{"source":"none"}}]}`,
	}
	out, seen := reviewRun(t, answers)
	wantHead := "Release rel_a, machine mch_a, validated: metal\n" +
		"Hostname: node-a, taken from the machine's name.\n" +
		"This release sets the machine's hostname to node-a, taken from its name. The last release applied to it left the hostname to the node. " +
		"The machine's Kubernetes node name normally follows the hostname, so it may change too.\n" +
		renameReaches[1] + "\n" + "machine:\n"
	if !strings.HasPrefix(out, wantHead) {
		t.Errorf("review = %q; want it to start %q", out, wantHead)
	}
	if n := lineCount(seen, "GET /api/v1/machines/mch_a"); n != 1 {
		t.Errorf("the machine was read %d times; want once: %q", n, seen)
	}
	if n := lineCount(seen, "GET /api/v1/releases/rel_old"); n != 1 {
		t.Errorf("the release last applied was read %d times; want once: %q", n, seen)
	}

	delete(answers, "GET /api/v1/releases/rel_old")
	out, _ = reviewRun(t, answers)
	want := "Hostname: node-a, taken from the machine's name.\n" + hostUnread + "\nmachine:\n"
	if !strings.Contains(out, want) || strings.Contains(out, "left the hostname to the node") || strings.Contains(out, renameReaches[1]) {
		t.Errorf("review with the applied release unread = %q; want %q and no guessed comparison", out, want)
	}

	// The release last applied is the one under review: the run already holds its body, so it is read once.
	answers["GET /api/v1/machines/mch_a"] = `{"id":"mch_a","name":"node-a","applied":{"release":"rel_a","source":"operation"}}`
	answers["GET /api/v1/releases/rel_a"] = `{"id":"rel_a","machines":[{"machine":"mch_a","hostname":{"source":"machine-name","name":"node-a"}}]}`
	out, seen = reviewRun(t, answers)
	if n := lineCount(seen, "GET /api/v1/releases/rel_a"); n != 1 {
		t.Errorf("the release under review, also the one last applied, was read %d times; want once: %q", n, seen)
	}
	if !strings.Contains(out, "Hostname: node-a, taken from the machine's name.\nmachine:\n") {
		t.Errorf("review with the same name applied = %q; want the source sentence and no comparison", out)
	}
	answers["GET /api/v1/machines/mch_a"] = `{"id":"mch_a","name":"node-a","applied":{"release":"rel_old","source":"operation"}}`

	out, seen = reviewRun(t, answers, "-o", "json")
	if strings.Contains(out, "Hostname:") || strings.Contains(out, hostUnread) || !strings.Contains(out, `"source": "machine-name"`) && !strings.Contains(out, `"source":"machine-name"`) {
		t.Errorf("-o json = %q; want the review unchanged", out)
	}
	if n := lineCount(seen, "GET /api/v1/machines/mch_a"); n != 0 {
		t.Errorf("-o json read the machine %d times; want none", n)
	}
}
