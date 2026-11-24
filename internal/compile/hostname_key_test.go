package compile

import (
	"strings"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"

	"github.com/ginsys/bronzeward/internal/ingest"
)

// The legacy machine.network.hostname key (compilation.md §2.3 step 2a, §6 step 8, §6.1 items 2 and 5). The
// names are synthetic.

const (
	keyPrevious = "previous-1"
	keyFrag     = "machine:\n  network:\n    hostname: " + keyPrevious + "\n"
	hostDelete  = "apiVersion: v1alpha1\nkind: HostnameConfig\n$patch: delete\n"
)

// K4: an import that removed the key records it as not the fill's own, so the machine reads false at its
// first apply even though the stored base and the composition then differ only by the hostname document.
// Control: the same base with the record of an exactly-the-fill document reads true.
func TestFormOnlyOfAReleasedLegacyKeyReadsFalse(t *testing.T) {
	for _, tc := range []struct {
		label    string
		imported ImportedHostname
		want     bool
	}{
		{"a key removed at import: not the fill's own", ImportedHostname{Name: hostMachine}, false},
		{"control: a document removed that was exactly the fill", ImportedHostname{Name: hostMachine, Equal: true}, true},
	} {
		t.Run(tc.label, func(t *testing.T) {
			in := hostIn(hostBase(t), hostMachine, ModeMetal)
			in.Imported = tc.imported
			if got := compiled(t, in).FormOnly(); got != tc.want {
				t.Errorf("FormOnly() = %v, want %v", got, tc.want)
			}
		})
	}
}

// K5, the key half: a base that still holds the key under the previous name is an explicit hostname
// whatever the machine is called now, so the release says `configuration` and the rename does not reach the
// node. The key that equals the current name is explicit too until step 2a removes it at import.
func TestLegacyKeyIsAnExplicitHostname(t *testing.T) {
	for _, name := range []string{hostMachine, "web-2"} {
		in := hostIn(source(t, string(withKey(t, keyPrevious)), ingest.Declarations{}, nil), name, ModeMetal)
		wantHost(t, compiled(t, in), 0, Hostname{HostnameConfiguration, ""}, keyPrevious)
	}
}

// K6: deleting the HostnameConfig a fragment or the base wrote hands the hostname to the fill. There was
// no test for the delete.
func TestPatchDeleteOfAHostnameDocumentHandsItToTheFill(t *testing.T) {
	in := hostIn(hostBase(t), hostMachine, ModeMetal, frag(t, hostDocExplicit), frag(t, hostDelete))
	wantHost(t, compiled(t, in), 2, Hostname{HostnameMachineName, hostMachine}, hostMachine)
	// Control: without the delete the explicit document wins.
	in = hostIn(hostBase(t), hostMachine, ModeMetal, frag(t, hostDocExplicit))
	wantHost(t, compiled(t, in), 1, Hostname{HostnameConfiguration, ""}, hostExplicit)
}

// withKey is the hostBase text with machine.network.hostname set to host: the base an import leaves when it
// keeps the key.
func withKey(t *testing.T, host string) []byte {
	t.Helper()
	base := hostBase(t)
	r, err := ingest.Resolve(base.Text, base.Values)
	if err != nil {
		t.Fatal(err)
	}
	text, err := r.Input().Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return patched(t, text, "machine:\n  network:\n    hostname: "+host+"\n")
}

// patched applies patch to text with the machinery's patcher at the pinned version, as composition does.
func patched(t *testing.T, text []byte, patch string) []byte {
	t.Helper()
	p, err := configpatcher.LoadPatch([]byte(patch))
	if err != nil {
		t.Fatalf("the gate refused the patch at load: %v", err)
	}
	res, err := configpatcher.Apply(configpatcher.WithBytes(text), []configpatcher.Patch{p})
	if err != nil {
		t.Fatalf("applying the patch: %v", err)
	}
	b, err := res.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// K7: is there a fragment route that hands a legacy key's hostname to the fill, as a `$patch: delete` of the
// document does? Each candidate is applied to a base that holds the key under the previous name, and the
// result is compiled for the current name. The answers pinned here are the measurement compilation.md §6.1
// item 5 cites.
func TestLegacyKeyFragmentRoutes(t *testing.T) {
	for _, tc := range []struct {
		label, patch string
	}{
		{"delete the key", "machine:\n  network:\n    hostname:\n      $patch: delete\n"},
		{"delete the mapping that holds it", "machine:\n  network:\n    $patch: delete\n"},
		{"an empty string", "machine:\n  network:\n    hostname: \"\"\n"},
		{"null", "machine:\n  network:\n    hostname: null\n"},
		{"control: another name", "machine:\n  network:\n    hostname: other-1\n"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			p, err := configpatcher.LoadPatch([]byte(tc.patch))
			if err != nil {
				t.Logf("MEASURED %s: the gate refuses the patch at load: %v", tc.label, err)
				return
			}
			res, err := configpatcher.Apply(configpatcher.WithBytes(withKey(t, keyPrevious)), []configpatcher.Patch{p})
			if err != nil {
				t.Logf("MEASURED %s: the patch does not apply: %v", tc.label, err)
				return
			}
			b, err := res.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			held := strings.Contains(string(b), keyPrevious)
			c, err := Compile(hostIn(source(t, string(b), ingest.Declarations{}, nil), hostMachine, ModeMetal))
			if err != nil {
				t.Logf("MEASURED %s: key kept %v, compile refused: %v", tc.label, held, err)
				return
			}
			host, doc := composedHostname(t, c)
			t.Logf("MEASURED %s: key kept %v, decision %+v, composed hostname %q, document %v", tc.label, held, c.Hostname(), host, doc)
		})
	}
}
