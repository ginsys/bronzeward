package compile

import (
	"testing"

	"github.com/ginsys/bronzeward/internal/ingest"
)

// The composition writes the typed configuration back with the empty values a configuration may omit
// (machine.token on a control plane, machine.ca.key and machine.certSANs on a worker), so a base that omits
// them differs from its release in content, and one that states them differs in form only
// (compilation.md §6, the form-only verdict, ginsys/bronzeward#1098). The ONBOARD-03 journey found it: its
// stand-in nodes omitted the keys and the adoption read "differs".
func TestFormOnlyEmptyDefaults(t *testing.T) {
	const (
		certText = "c3ludGhldGljLWNlcnQ="
		privText = "c3ludGhldGljLXByaXY="
		cluster  = "# Provides cluster specific configuration options.\ncluster:\n  controlPlane:\n    endpoint: https://cp.example.test:6443 # the control plane\n"
		worker   = "# Stand-in worker.\nversion: v1alpha1\nmachine:\n  type: worker\n  token: synthetic\n  ca:\n    crt: " + certText + "\n"
		control  = "# Stand-in control plane.\nversion: v1alpha1\nmachine:\n  type: controlplane\n  ca:\n    crt: " + certText + "\n    key: " + privText + "\n"
	)
	for _, c := range []struct {
		name, text string
		want       bool
	}{
		{"worker omitting the empty defaults", worker + cluster, false},
		{"worker stating them", worker + "    key: \"\"\n  certSANs: []\n" + cluster, true},
		{"control plane omitting the empty defaults", control + cluster, false},
		{"control plane stating them", control + "  token: \"\"\n  certSANs: []\n" + cluster, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := resolved(t, c.text, ingest.Declarations{}, nil)
			m, err := Compose(r, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := sameInForm(r, m); got != c.want {
				t.Errorf("form only %t, want %t", got, c.want)
			}
		})
	}
}
