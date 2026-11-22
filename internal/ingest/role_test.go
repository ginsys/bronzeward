package ingest

import "testing"

// MachineRole reads the machine's type from the sanitized documents for the console's role label
// (ginsys/bronzeward#1096). It answers only what a Talos type says; anything it cannot read is no role,
// and never a refused import.
func TestMachineRole(t *testing.T) {
	for _, c := range []struct{ name, docs, want string }{
		{"worker", "version: v1alpha1\nmachine:\n  type: worker\n", "worker"},
		{"controlplane", "version: v1alpha1\nmachine:\n  type: controlplane\n", "controlplane"},
		{"the type in the second document", "apiVersion: v1alpha1\nkind: Other\n---\nmachine:\n  type: worker\n", "worker"},
		{"two documents that agree", "machine:\n  type: worker\n---\nmachine:\n  type: worker\n", "worker"},
		{"two documents that disagree", "machine:\n  type: worker\n---\nmachine:\n  type: controlplane\n", ""},
		{"init is no role", "machine:\n  type: init\n", ""},
		{"an unknown type", "machine:\n  type: bastion\n", ""},
		{"a non-string type", "machine:\n  type: 1\n", ""},
		{"a null type", "machine:\n  type:\n", ""},
		{"no type", "machine:\n  token: !bwref x\n", ""},
		{"no machine key", "version: v1alpha1\ncluster: {}\n", ""},
		{"machine is not a mapping", "machine: worker\n", ""},
		{"malformed text", "machine: [unclosed\n  type: worker\n", ""},
		{"empty", "", ""},
		{"a reference elsewhere does not matter", "machine:\n  type: controlplane\n  token: !bwref abc\n", "controlplane"},
	} {
		if got := MachineRole([]byte(c.docs)); got != c.want {
			t.Errorf("%s: MachineRole = %q, want %q", c.name, got, c.want)
		}
	}
}
