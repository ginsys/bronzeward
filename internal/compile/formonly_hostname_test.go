package compile

import (
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/ingest"
)

// compilation.md §6.1 item 5: the import removed the fill's own HostnameConfig from the stored base, so
// the composition (which has it back) is form-only of the base when that one document, exactly as the fill
// writes it for the recorded name, is the only difference. hostBase is such a stored base.
func TestFormOnlyOfAnOnboardedMachine(t *testing.T) {
	tooMany := "apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: " + hostMachine + "\n"
	other := "machine:\n  network:\n    nameservers:\n      - 192.0.2.53\n"
	for _, tc := range []struct {
		label    string
		name     string
		imported ImportedHostname
		frags    []string
		want     bool
	}{
		{"unchanged: the document set aside", hostMachine, ImportedHostname{hostMachine, true}, nil, true},
		{"the removed document was not the fill's own", hostMachine, ImportedHostname{hostMachine, false}, nil, false},
		{"nothing was removed", hostMachine, ImportedHostname{}, nil, false},
		{"renamed since: the change is the hostname", "web-2", ImportedHostname{hostMachine, true}, nil, false},
		{"unnamed: no fill, the base alone is form-only as it always was", "", ImportedHostname{hostMachine, true}, nil, true},
		{"another document differs", hostMachine, ImportedHostname{hostMachine, true}, []string{other}, false},
		{"a fragment writes the hostname: today's rule", hostMachine, ImportedHostname{hostMachine, true}, []string{tooMany}, false},
		{"a fragment writes another hostname", hostMachine, ImportedHostname{hostMachine, true}, []string{hostDocExplicit}, false},
	} {
		t.Run(tc.label, func(t *testing.T) {
			var frags []Source
			for _, f := range tc.frags {
				frags = append(frags, frag(t, f))
			}
			in := hostIn(hostBase(t), tc.name, ModeMetal, frags...)
			in.Imported = tc.imported
			if got := compiled(t, in).FormOnly(); got != tc.want {
				t.Errorf("FormOnly() = %v, want %v", got, tc.want)
			}
		})
	}
}

// A release whose own fragment writes a HostnameConfig equal in text to the name is no fill: the verdict
// follows today's rule and the record does not move it.
func TestFormOnlyOfAnExplicitHostnameIgnoresTheRecord(t *testing.T) {
	explicit := "apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: " + hostMachine + "\n"
	in := hostIn(hostBase(t), hostMachine, ModeMetal, frag(t, explicit))
	without := compiled(t, in).FormOnly()
	in.Imported = ImportedHostname{hostMachine, true}
	if with := compiled(t, in).FormOnly(); with != without || with {
		t.Errorf("explicit document: %v with the record, %v without; both must be false", with, without)
	}
}

// The set-aside document must be exactly {apiVersion, kind, auto: off, hostname: name}: another member,
// another value, a missing member or a non-string each keep the verdict false.
func TestIsFill(t *testing.T) {
	for _, tc := range []struct {
		doc  string
		want bool
	}{
		{"apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: web-1\n", true},
		{"hostname: web-1\n# comment\nauto: off\nkind: HostnameConfig\napiVersion: v1alpha1\n", true},
		{"apiVersion: v1alpha1\nkind: HostnameConfig\nauto: stable\nhostname: web-1\n", false},
		{"apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: web-2\n", false},
		{"apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\n", false},
		{"apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: web-1\nextra: x\n", false},
		{"apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: [web-1]\n", false},
		{"apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: !bwref app/h\n", false},
		{"apiVersion: v1alpha1\nkind: HostnameConfig\nauto: \"off\"\nhostname: web-1\n", true},
	} {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(tc.doc), &n); err != nil {
			t.Fatal(err)
		}
		if got := isFill(&n, hostMachine); got != tc.want {
			t.Errorf("isFill(%q) = %v, want %v", tc.doc, got, tc.want)
		}
	}
}

// sameInFormBut itself: a composition whose HostnameConfig is not exactly the fill's is not form-only,
// though every other document equals the base. The compile path cannot reach this (the patch replaces the
// document whole), so the composition is built by hand.
func TestSameInFormButNeedsTheExactFill(t *testing.T) {
	base := source(t, "machine:\n  type: worker\n", ingest.Declarations{}, nil)
	r, err := ingest.Resolve(base.Text, base.Values)
	if err != nil {
		t.Fatal(err)
	}
	fill := "apiVersion: v1alpha1\nkind: HostnameConfig\nauto: off\nhostname: " + hostMachine + "\n"
	for _, tc := range []struct {
		label, composed string
		want            bool
	}{
		{"exact fill", "machine:\n  type: worker\n---\n" + fill, true},
		{"fill first", fill + "---\nmachine:\n  type: worker\n", true},
		{"another value", "machine:\n  type: worker\n---\n" + fill + "extra: x\n", false},
		{"another auto", "machine:\n  type: worker\n---\napiVersion: v1alpha1\nkind: HostnameConfig\nauto: stable\nhostname: " + hostMachine + "\n", false},
		{"no fill", "machine:\n  type: worker\n", false},
		{"two fills", "machine:\n  type: worker\n---\n" + fill + "---\n" + fill, false},
		{"other document differs", "machine:\n  type: controlplane\n---\n" + fill, false},
	} {
		s := tc.composed
		if got := sameInFormBut(r, Materialized{s: &s}, hostMachine); got != tc.want {
			t.Errorf("%s: sameInFormBut() = %v, want %v", tc.label, got, tc.want)
		}
	}
}
