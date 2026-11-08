package ingest

import (
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"
)

// Machinery v1.14.5 changes config/merge (ginsys/bronzeward#712, contract 9). Each test below pins
// what the accept gate (LoadPatch, then Resolved.Patch) and the composition answer at v1.14.5. The
// same kinds of patch were also run against v1.13.6 in a throwaway probe on a base like the one
// below (it also tried an empty string, list and map, and a null), and each test says what that run
// answered. These tests themselves do not build against v1.13.6.

// patchBase holds a v1alpha1 document and a KubeSpanConfig document, both kinds that v1.13.6 loads.
const patchBase = "version: v1alpha1\nmachine:\n  type: controlplane\n  install:\n    disk: /dev/sda\n    wipe: true\n" +
	"cluster:\n  allowSchedulingOnControlPlanes: true\n" +
	"---\napiVersion: v1alpha1\nkind: KubeSpanConfig\nenabled: true\nmtu: 1380\nharvestExtraEndpoints: true\n"

// applyPatch loads patch as the gate does and applies it to patchBase.
func applyPatch(t *testing.T, patch string) string {
	t.Helper()
	p, err := configpatcher.LoadPatch([]byte(patch))
	if err != nil {
		t.Fatalf("the gate refused the patch at load: %v", err)
	}
	res, err := configpatcher.Apply(configpatcher.WithBytes([]byte(patchBase)), []configpatcher.Patch{p})
	if err != nil {
		t.Fatalf("applying the patch: %v", err)
	}
	b, err := res.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A zero value on the right is still merged. The release note says the opposite, so the case was
// measured: a v1alpha1 boolean, a document boolean and a document integer, each set to its zero
// value, replace the base's value at v1.14.5. At v1.13.6 the result was the same.
func TestPatchZeroValueIsStillMerged(t *testing.T) {
	for name, c := range map[string]struct{ patch, want, gone string }{
		"v1alpha1 boolean": {"machine:\n  install:\n    wipe: false\n", "wipe: false", "wipe: true"},
		"document boolean": {"apiVersion: v1alpha1\nkind: KubeSpanConfig\nenabled: false\n", "enabled: false", "enabled: true"},
		"document integer": {"apiVersion: v1alpha1\nkind: KubeSpanConfig\nmtu: 0\n", "mtu: 0", "mtu: 1380"},
	} {
		t.Run(name, func(t *testing.T) {
			out := applyPatch(t, c.patch)
			if !strings.Contains(out, c.want) || strings.Contains(out, c.gone) {
				t.Errorf("the zero value was not merged:\n%s", out)
			}
		})
	}
}

// A delete selector for a document the base does not hold is skipped: the gate accepts it, the base
// is unchanged and no document is added. At v1.13.6 it was accepted too, and the output gained an
// empty document ("---" and "{}") after the base's last one.
func TestPatchDeleteOfAMissingDocumentIsSkipped(t *testing.T) {
	out := applyPatch(t, "apiVersion: v1alpha1\nkind: ResolverConfig\n$patch: delete\n")
	if !strings.Contains(out, "kind: KubeSpanConfig") || !strings.Contains(out, "mtu: 1380") {
		t.Errorf("the base changed:\n%s", out)
	}
	if strings.Contains(out, "ResolverConfig") || strings.Contains(out, "{}") || strings.Count(out, "---") != 1 {
		t.Errorf("the output holds a document the base did not:\n%s", out)
	}
}

// LoadPatch does not validate the patch's values: a machine type no node has loads, so the gate
// accepts it and only validating the composition refuses it. At v1.13.6 it loaded the same way. An
// unknown key is refused at load in both versions.
func TestPatchThatFailsValidationLoads(t *testing.T) {
	if _, err := configpatcher.LoadPatch([]byte("machine:\n  type: bogus\n")); err != nil {
		t.Errorf("the gate refused an invalid value at load: %v", err)
	}
	if _, err := configpatcher.LoadPatch([]byte("machine:\n  nosuchfield: 1\n")); err == nil {
		t.Error("the gate accepted an unknown key")
	}
}
