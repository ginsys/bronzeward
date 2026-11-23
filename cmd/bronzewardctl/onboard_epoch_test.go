package main

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// TestOnboardWithNoEpochSendsNothing: a run whose first read shows no epoch has none to send its writes under, and
// says so before it sends any (the web page's NO_EPOCH_YET, in the command's words).
func TestOnboardWithNoEpochSendsNothing(t *testing.T) {
	e := newOnboard(t)
	e.api.noEpoch = true
	code, out, errOut := e.run()
	if code == 0 || !strings.Contains(errOut, "needs the installation's epoch to send its writes under") || !strings.Contains(errOut, "Nothing was sent") {
		t.Errorf("exit %d, stdout %q, stderr %q; want a refusal that names the epoch and says nothing was sent", code, out, errOut)
	}
	if got := e.api.writes(); len(got) != 0 {
		t.Errorf("writes %q were sent; want none", got)
	}
}

// TestRunEpochBindsOnlyAWriteWithNoBindOfItsOwn: a command that holds a run epoch (cluster onboard) binds each
// write to it in writeWith, and a write that already binds itself keeps its own bind (ginsys/bronzeward#1078).
// The fake shows ep_OB-1; the run claims to have begun under ep_OB-0.
func TestRunEpochBindsOnlyAWriteWithNoBindOfItsOwn(t *testing.T) {
	cluster := func() *client.Write {
		return clusterWrite(apitypes.ClusterInput{Name: "lab", Endpoint: "https://10.0.0.1:6443", Contract: "v1.14"}, nil)
	}
	say := func(*client.Client, *client.Response) ([]string, error) { return nil, nil }
	t.Run("a write with no bind is bound to the run's epoch and not sent", func(t *testing.T) {
		e := newOnboard(t)
		env := &env{output: "table", out: io.Discard, err: io.Discard, runEpoch: "ep_OB-0"}
		err := env.writeAfter(nil, cluster(), say)
		if !errors.As(err, new(client.NotSent)) || !strings.Contains(err.Error(), "the epoch changed from 'ep_OB-0' to 'ep_OB-1'") {
			t.Fatalf("error %v; want the client's not-sent refusal naming the run's epoch", err)
		}
		if got := e.api.writes(); len(got) != 0 {
			t.Errorf("writes %q were sent; want none", got)
		}
	})
	t.Run("a write that binds itself keeps its own bind", func(t *testing.T) {
		e := newOnboard(t)
		env := &env{output: "table", out: io.Discard, err: io.Discard, runEpoch: "ep_OB-0"}
		w := cluster()
		// The write's own bind names the epoch the fake shows, which the run's does not.
		err := env.writeAfter(func(c *client.Client) error { w.ReadsAt("ep_OB-1", c.EpochMoves()); return nil }, w, say)
		if err != nil {
			t.Fatalf("error %v; want the write sent under its own bind", err)
		}
		if got := e.api.writes(); len(got) != 1 {
			t.Errorf("writes %q; want the one", got)
		}
	})
}

// TestOnboardEpochChangeStopsTheRun: every write of one run is sent under the epoch the run's first read showed
// (ginsys/bronzeward#1078). The fake changes the installation's epoch after the k-th write is answered; the
// next write of the run, whichever kind it is, is not sent, and nothing after it is.
func TestOnboardEpochChangeStopsTheRun(t *testing.T) {
	// The writes of a full run, in order (TestOnboardFullRun), and whether the refusal of the one after the change
	// is the epoch's own. A draft write that carries a kept ETag (the imports and the assignments after the first)
	// is also refused by the missing ETag of the new epoch, which stops it with or without the run's bind.
	kinds := []struct {
		after  string
		bound  bool
		writes int
	}{
		{"the cluster", true, 1}, {"machine 1", true, 2}, {"machine 2", true, 3}, {"the first key", true, 4}, {"the draft", false, 5},
		{"import 1", false, 6}, {"import 2", true, 7}, {"assignment 1", false, 8},
	}
	for _, k := range kinds {
		t.Run("after "+k.after, func(t *testing.T) {
			e := newOnboard(t)
			e.api.switchAfter = k.writes
			code, out, errOut := e.run()
			if code == 0 {
				t.Fatalf("exit 0 after the epoch changed, stdout %q, stderr %q", out, errOut)
			}
			if got := len(e.api.writes()); got != k.writes {
				t.Errorf("%d writes were sent, want the %d before the change: %q", got, k.writes, e.api.writes())
			}
			if !strings.Contains(errOut, "not sent") {
				t.Errorf("stderr does not say that a write was not sent:\n%s", errOut)
			}
			if k.bound && !strings.Contains(errOut, "the epoch changed from 'ep_OB-1' to 'ep_OB-2'") {
				t.Errorf("stderr lacks the epoch's own refusal:\n%s", errOut)
			}
			// What was done and what was not, by name: the cluster is created, and each machine is a line of its own.
			for _, want := range []string{"Cluster cl_", ": created", "node-1  cp-1", "node-2  w-1"} {
				if !strings.Contains(out, want) {
					t.Errorf("stdout lacks %q:\n%s", want, out)
				}
			}
		})
	}
}
