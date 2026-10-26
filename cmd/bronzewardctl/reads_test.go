package main

import (
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// TestSentences checks operator-experience.md §2.4's status sentences, one row of its table per
// case, on synthetic machine reads.
func TestSentences(t *testing.T) {
	at := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	rel := func(s string) *string { return &s }
	obs := func(id string, basis int, unread map[string]apitypes.UnreadValue) apitypes.ObservationBody {
		return apitypes.ObservationBody{Id: id, Basis: basis, At: at.Add(time.Duration(basis) * time.Minute), Unread: unread}
	}
	applied := &apitypes.Applied{Release: "rel_a", Source: "operation"}
	for _, c := range []struct {
		name string
		m    apitypes.MachineBody
		obs  []apitypes.ObservationBody
		want []string
		not  []string
	}{
		{"nothing recorded", apitypes.MachineBody{}, nil,
			[]string{"No configuration read recorded.", "Not applied or adopted by Bronzeward.", "No release selected.", "Health: not checked."}, nil},
		{"read is the applied release", apitypes.MachineBody{Applied: applied, Desired: rel("rel_a"),
			Observed: &apitypes.ObservedBody{At: at, Observation: "obs_1", Release: rel("rel_a")}}, []apitypes.ObservationBody{obs("obs_1", 1, nil)},
			[]string{"The configuration read at 2026-10-26 10:00:00 UTC is attributed to rel_a, which is the Applied release (by operation)."},
			[]string{"Adopted", "Pending", "carries"}},
		{"adoption with the same desired release", apitypes.MachineBody{Applied: &apitypes.Applied{Release: "rel_a", Source: "adoption"}, Desired: rel("rel_a"),
			Observed: &apitypes.ObservedBody{At: at, Release: rel("rel_a")}}, nil,
			[]string{"Adopted from the node's own bytes", "the API does not say which."}, nil},
		{"pending, applied", apitypes.MachineBody{Applied: applied, Desired: rel("rel_b"), PendingConvergence: true}, nil,
			[]string{"Pending convergence: the configuration of the selected release rel_b for this machine differs from what Bronzeward applied."}, nil},
		{"pending, never applied", apitypes.MachineBody{Desired: rel("rel_b"), PendingConvergence: true}, nil,
			[]string{"Pending convergence: the selected release rel_b has not been applied to this machine.", "Not applied or adopted"}, nil},
		{"new release, same configuration", apitypes.MachineBody{Applied: applied, Desired: rel("rel_b")}, nil,
			[]string{"The selected release rel_b carries this machine's applied configuration unchanged."}, nil},
		{"drift equal to desired", apitypes.MachineBody{Applied: applied, Desired: rel("rel_b"), PendingConvergence: true,
			Observed:  &apitypes.ObservedBody{At: at, Observation: "obs_2", Release: rel("rel_b")},
			OpenDrift: &apitypes.OpenDriftBody{Observation: "obs_2"}}, []apitypes.ObservationBody{obs("obs_2", 2, nil)},
			[]string{"Drifted since 2026-10-26 10:02:00 UTC: a read differed from what Bronzeward last verified (rel_a).", "It equals the selected release; that does not close the drift."},
			[]string{"Pending convergence"}},
		{"read matching neither", apitypes.MachineBody{Applied: applied, Observed: &apitypes.ObservedBody{At: at}}, nil,
			[]string{"matched neither Applied nor Desired, and no drift is open"}, nil},
		{"later unread read, frozen", apitypes.MachineBody{Applied: applied, Frozen: true,
			Observed: &apitypes.ObservedBody{At: at, Observation: "obs_1", Release: rel("rel_a")}},
			[]apitypes.ObservationBody{obs("obs_1", 1, nil), obs("obs_3", 3, map[string]apitypes.UnreadValue{"talosNodeId": {Cause: "timeout"}, "configDigest": {Cause: "unreachable"}})},
			[]string{"The latest read at 2026-10-26 10:03:00 UTC could not read: configDigest: unreachable; talosNodeId: timeout.", "Frozen: no new commitment"}, nil},
		{"earlier unread read", apitypes.MachineBody{Applied: applied, Observed: &apitypes.ObservedBody{At: at, Observation: "obs_2", Release: rel("rel_a")}},
			[]apitypes.ObservationBody{obs("obs_1", 1, map[string]apitypes.UnreadValue{"configDigest": {Cause: "unreachable"}}), obs("obs_2", 2, nil)},
			nil, []string{"could not read"}},
	} {
		got := strings.Join(sentences(c.m, c.obs), "\n")
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: lacks %q in\n%s", c.name, w, got)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("%s: holds %q in\n%s", c.name, n, got)
			}
		}
		if !strings.HasSuffix(got, "Health: not checked.") {
			t.Errorf("%s: does not end with the health sentence:\n%s", c.name, got)
		}
	}
}
