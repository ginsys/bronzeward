package main

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestMachineNameNeedsRevision10 (client contracts §7.1 item 1): a server below API revision 10 refuses
// the `name` field and has no names route, so the client refuses before the write, naming both revisions;
// a machine add without a name is sent as before, and revision 10 sends the name.
func TestMachineNameNeedsRevision10(t *testing.T) {
	add := []string{"machine", "add", "--cluster", "cl_c", "--talos-node-id", "n", "--platform", "metal", "--talos-endpoint", "10.0.0.5:50000"}
	rename := []string{"machine", "rename", "mch_a", "--name", "web-2"}
	for _, c := range []struct {
		name     string
		revision int
		args     []string
		sent     bool
	}{
		{"add with a name, revision 9", 9, append(slices.Clone(add), "--name", "web-1"), false},
		{"add with an explicitly empty name, revision 9", 9, append(slices.Clone(add), "--name", ""), false},
		{"add without a name, revision 9", 9, add, true},
		{"add with a name, revision 10", 10, append(slices.Clone(add), "--name", "web-1"), true},
		{"rename, revision 9", 9, rename, false},
		{"rename, revision 10", 10, rename, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			setServerFacts(t, c.revision, nil)
			resetPosted()
			recoveryServer(t, map[string]string{"GET /api/v1/recovery": `{"epoch":"ep_BWSYNTH-1","recoveryMode":false}`},
				`{"id":"mch_new","machine":"mch_a","revision":1,"name":"web-1","previousName":null,"cluster":"cl_c","talosEndpoint":"10.0.0.5:50000"}`)
			var out, errb bytes.Buffer
			code := run(c.args, &out, &errb)
			if got := len(posted()) == 1; got != c.sent {
				t.Errorf("sent %t, want %t (exit %d, stderr %q)", got, c.sent, code, errb.String())
			}
			if !c.sent && (code == 0 || !strings.Contains(errb.String(), fmt.Sprintf("a machine's name needs API revision 10; the server serves revision %d", c.revision))) {
				t.Errorf("exit %d, stderr %q; want a refusal naming both revisions", code, errb.String())
			}
			if c.sent && code != 0 {
				t.Errorf("exit %d, stderr %q; want 0", code, errb.String())
			}
		})
	}
}
