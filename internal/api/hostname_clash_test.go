package api

import (
	"strings"
	"testing"
)

// The sentences of compilation.md §6.1 item 6, asserted whole. The names are synthetic.

// A hostname filled from a name against another machine's explicit hostname that gives the same node
// name: the filled machine is named first, the explicit one by name or, with none, by identifier.
func TestHostnameClashFilledAgainstExplicit(t *testing.T) {
	filled := nodeClaim{machine: "mch_filled", name: "node-a", node: "node-a", filled: true}
	for _, c := range []struct {
		label  string
		claims []nodeClaim
		want   string
	}{
		{"the explicit machine has a name",
			[]nodeClaim{filled, {machine: "mch_explicit", name: "node-b", node: "node-a"}},
			"Machine node-a would take the hostname node-a from its name, but the hostname configured on machine node-b " +
				"gives the same Kubernetes node name. Rename machine node-a, or change the hostname in the configuration of machine node-b."},
		{"the explicit machine has none: named by identifier",
			[]nodeClaim{filled, {machine: "mch_explicit", node: "node-a"}},
			"Machine node-a would take the hostname node-a from its name, but the hostname configured on machine mch_explicit " +
				"gives the same Kubernetes node name. Rename machine node-a, or change the hostname in the configuration of machine mch_explicit."},
		{"the explicit machine comes first in the release: the filled one is still named first",
			[]nodeClaim{{machine: "mch_explicit", name: "node-b", node: "node-a"}, filled},
			"Machine node-a would take the hostname node-a from its name, but the hostname configured on machine node-b " +
				"gives the same Kubernetes node name. Rename machine node-a, or change the hostname in the configuration of machine node-b."},
	} {
		ref := hostnameClash(c.claims)
		if ref == nil || ref.status != 422 || ref.code != "validation-failed" || ref.detail != c.want {
			t.Errorf("%s: got %+v, want 422 validation-failed %q", c.label, ref, c.want)
			continue
		}
		if ref.extra["rule"] != "hostname" || ref.extra["machine"] != "mch_filled" || ref.extra["other"] != "mch_explicit" {
			t.Errorf("%s: extension members %v", c.label, ref.extra)
		}
	}
}

// Two filled hostnames that give one node name (the names a.x.org and a.y.org: Talos derives `a` from
// the first label unless registerWithFQDN is true).
func TestHostnameClashFilledAgainstFilled(t *testing.T) {
	ref := hostnameClash([]nodeClaim{
		{machine: "mch_one", name: "a.x.org", node: "a", filled: true},
		{machine: "mch_two", name: "a.y.org", node: "a", filled: true},
	})
	want := "Machine a.x.org and machine a.y.org would both register the Kubernetes node name a. " +
		"Rename one of them, or set `registerWithFQDN` on both."
	if ref == nil || ref.status != 422 || ref.code != "validation-failed" || ref.detail != want {
		t.Fatalf("got %+v, want 422 validation-failed %q", ref, want)
	}
	if ref.extra["rule"] != "hostname" || ref.extra["machine"] != "mch_one" || ref.extra["other"] != "mch_two" {
		t.Errorf("extension members %v", ref.extra)
	}
}

// What is not refused: other node names, two explicit hostnames that were already equal, a machine that
// makes no claim, and nothing at all.
func TestHostnameClashAccepts(t *testing.T) {
	for _, c := range []struct {
		label  string
		claims []nodeClaim
	}{
		{"none", nil},
		{"one machine", []nodeClaim{{machine: "mch_one", name: "a", node: "a", filled: true}}},
		{"filled against filled, node names differ (registerWithFQDN true on both)", []nodeClaim{
			{machine: "mch_one", name: "a.x.org", node: "a.x.org", filled: true},
			{machine: "mch_two", name: "a.y.org", node: "a.y.org", filled: true}}},
		{"registerWithFQDN true on one only: the compared values are a.x.org and a", []nodeClaim{
			{machine: "mch_one", name: "a.x.org", node: "a.x.org", filled: true},
			{machine: "mch_two", name: "a.y.org", node: "a", filled: true}}},
		{"two explicit hostnames already equal", []nodeClaim{
			{machine: "mch_one", name: "x", node: "same"}, {machine: "mch_two", name: "y", node: "same"}}},
	} {
		if ref := hostnameClash(c.claims); ref != nil {
			t.Errorf("%s: refused: %v", c.label, ref)
		}
	}
}

// No refusal prints an explicit value: the claim holds only the derived node name, and the text of an
// explicit machine names the machine, never the hostname configured on it.
func TestHostnameClashPrintsNoConfiguredValue(t *testing.T) {
	ref := hostnameClash([]nodeClaim{
		{machine: "mch_filled", name: "node-a", node: "node-a", filled: true},
		{machine: "mch_explicit", node: "node-a"},
	})
	if ref == nil {
		t.Fatal("not refused")
	}
	for _, s := range []string{"secret-host", "node-a.example.org"} {
		if strings.Contains(ref.detail, s) {
			t.Errorf("the refusal shows %q", s)
		}
	}
}
