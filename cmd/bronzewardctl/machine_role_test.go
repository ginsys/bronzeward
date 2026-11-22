package main

import (
	"bytes"
	"strings"
	"testing"
)

// machine show prints a Role line in a person's words when the server serves a role, and no line when
// it serves none: a machine imported before the role was stored shows nothing, never a sentence about
// it (ginsys/bronzeward#1096, choice §17.49).
func TestMachineShowRole(t *testing.T) {
	for _, c := range []struct {
		name, role, want string
		line             bool
	}{
		{"a control plane", `"controlplane"`, "Role      control plane\n", true},
		{"a worker", `"worker"`, "Role      worker\n", true},
		{"no role", `null`, "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			setServerFacts(t, 3, nil)
			recoveryServer(t, map[string]string{
				"GET /api/v1/machines/mch_a": `{"id":"mch_a","cluster":"c1","name":null,"role":` + c.role + `,"platform":"metal","talosEndpoint":"10.0.0.1",` +
					`"hardware":{"talosNodeId":"n1"},"scopeState":"normal","frozen":false,"pendingConvergence":false,"applied":null,"desired":null,` +
					`"observed":null,"openDrift":null}`,
				"GET /api/v1/machines/mch_a/observations": `{"items":[]}`,
			}, "")
			var out, errb bytes.Buffer
			if rc := run([]string{"machine", "show", "mch_a"}, &out, &errb); rc != 0 {
				t.Fatalf("rc %d, err %q", rc, errb.String())
			}
			if got := strings.Contains(out.String(), "Role"); got != c.line || (c.line && !strings.Contains(out.String(), c.want)) {
				t.Errorf("out %q; want the line %q: %v", out.String(), c.want, c.line)
			}
		})
	}
}
