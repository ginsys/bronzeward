package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
	"github.com/ginsys/bronzeward/internal/talos/discover"
)

// TestOnboardEpochChangeStopsTheRun: every write of one run is sent under the epoch the run's first read showed
// (ginsys/bronzeward#1078). The fake changes the installation's epoch after the k-th write is answered; the
// next write of the run, whichever kind it is, is not sent, nothing after it is, and the output says once that the
// epoch changed, whichever guard stopped the write.
func TestOnboardEpochChangeStopsTheRun(t *testing.T) {
	// The writes of a full run, in order (TestOnboardFullRun), the line the refusal of the one after the change prints,
	// and the number of writes sent before it. A draft write that carries a kept ETag (the second import onward and the
	// assignments) is stopped by the client's missing-ETag refusal, which comes before its epoch comparison in Send, so
	// the person reads that line there and the run's own sentence for the cause.
	const epochLine, etagLine = "the epoch changed from 'ep_OB-1' to 'ep_OB-2'", "no ETag of draft"
	kinds := []struct {
		after, line string
		writes      int
	}{
		{"the cluster", epochLine, 1}, {"machine 1", epochLine, 2}, {"machine 2", epochLine, 3}, {"the first key", epochLine, 4},
		{"the draft", etagLine, 5}, {"import 1", etagLine, 6}, {"import 2", epochLine, 7}, {"assignment 1", etagLine, 8},
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
			if !strings.Contains(errOut, "not sent") || !strings.Contains(errOut, k.line) {
				t.Errorf("stderr lacks the refusal line %q:\n%s", k.line, errOut)
			}
			// The one cause, the same in every case, before the commands that carry on.
			const cause = "The server's epoch changed from 'ep_OB-1' to 'ep_OB-2' since this run began; nothing more was sent."
			if strings.Count(out, cause) != 1 || strings.Index(out, cause) > strings.Index(out, "Next, one command at a time:") {
				t.Errorf("stdout does not say the cause once, before the commands:\n%s", out)
			}
			// What was done, by name: the cluster is created, and each machine is a line of its own.
			for _, want := range []string{"Cluster cl_", ": created", "node-1  cp-1", "node-2  w-1"} {
				if !strings.Contains(out, want) {
					t.Errorf("stdout lacks %q:\n%s", want, out)
				}
			}
		})
	}
}

// TestOnboardEpochChangeDuringTheDiscoveryStopsBeforeTheFirstWrite: the discovery sits between the run's first
// read (which pins the epoch) and its first write, and with several endpoints to try it can take a minute. An
// epoch that changes inside it sends nothing: the first write is refused before it goes.
func TestOnboardEpochChangeDuringTheDiscoveryStopsBeforeTheFirstWrite(t *testing.T) {
	e := newOnboard(t)
	inner := discoverCluster
	discoverCluster = func(ctx context.Context, tc []byte, contextName, endpoint string) (discover.Result, string, error) {
		e.api.mu.Lock()
		e.api.switchAfter, e.api.writesDone = 1, 1 // the epoch is ep_OB-2 from here on
		e.api.mu.Unlock()
		return inner(ctx, tc, contextName, endpoint)
	}
	code, out, errOut := e.run()
	if code == 0 || len(e.api.writes()) != 0 {
		t.Errorf("exit %d, writes %q; want a failure and no write sent", code, e.api.writes())
	}
	if !strings.Contains(out+errOut, "the epoch changed from 'ep_OB-1' to 'ep_OB-2'") {
		t.Errorf("the run does not say the epoch changed:\nstdout %q\nstderr %q", out, errOut)
	}
}

// TestOnboardEpochSentenceOnlyWhenTheEpochMoved: a run that stops for another reason says nothing about the epoch.
func TestOnboardEpochSentenceOnlyWhenTheEpochMoved(t *testing.T) {
	e := newOnboard(t)
	e.api.failPost = map[string]int{"/machines": 1}
	code, out, errOut := e.run()
	if code == 0 || strings.Contains(out+errOut, "epoch") {
		t.Errorf("exit %d, stdout %q, stderr %q; want a failure that does not mention the epoch", code, out, errOut)
	}
}

// TestOnboardEpochSentenceAfterAnImportFollowSawIt: the epoch changes with the first import's start, and the import
// then fails with no write after it. The follow's reads showed the new epoch, and the output says so
// (no write answered under it, so only the follow could tell).
func TestOnboardEpochSentenceAfterAnImportFollowSawIt(t *testing.T) {
	e := newOnboard(t)
	e.api.switchAfter, e.api.failImportAt = 6, 1
	code, out, errOut := e.run()
	const cause = "The server's epoch changed from 'ep_OB-1' to 'ep_OB-2' since this run began; nothing more was sent."
	if code == 0 || strings.Count(out, cause) != 1 {
		t.Errorf("exit %d, stdout %q, stderr %q; want the failure and the epoch sentence once", code, out, errOut)
	}
}

// TestOnboardWithNoEpochSendsNothing: a run whose first read shows no epoch has none to send its writes under, and
// says so before it sends any, also where the context kept an epoch from an earlier command (the web page's
// NO_EPOCH_YET, in the command's words).
func TestOnboardWithNoEpochSendsNothing(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(map[bool]string{false: "a context that has seen none", true: "a context that kept one"}[cached], func(t *testing.T) {
			e := newOnboard(t)
			if cached {
				// An earlier command stores the epoch the fake shows in the context.
				if code, out, errOut := e.runArgs("cluster", "list"); code != 0 {
					t.Fatalf("cluster list: exit %d, stdout %q, stderr %q", code, out, errOut)
				}
			}
			e.api.noEpoch = true
			code, out, errOut := e.run()
			if code == 0 || !strings.Contains(errOut, "needs the installation's epoch to send its writes under") || !strings.Contains(errOut, "Nothing was sent") {
				t.Errorf("exit %d, stdout %q, stderr %q; want a refusal that names the epoch and says nothing was sent", code, out, errOut)
			}
			if got := e.api.writes(); len(got) != 0 {
				t.Errorf("writes %q were sent; want none", got)
			}
		})
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
	pinned := func() *env {
		return &env{output: "table", out: io.Discard, err: io.Discard, runEpoch: &runPin{began: "ep_OB-0", now: "ep_OB-0"}}
	}
	t.Run("a write with no bind is bound to the run's epoch and not sent", func(t *testing.T) {
		e := newOnboard(t)
		err := pinned().writeAfter(nil, cluster(), say)
		if !errors.As(err, new(client.NotSent)) || !strings.Contains(err.Error(), "the epoch changed from 'ep_OB-0' to 'ep_OB-1'") {
			t.Fatalf("error %v; want the client's not-sent refusal naming the run's epoch", err)
		}
		if got := e.api.writes(); len(got) != 0 {
			t.Errorf("writes %q were sent; want none", got)
		}
	})
	t.Run("a write that binds itself keeps its own bind", func(t *testing.T) {
		e := newOnboard(t)
		w := cluster()
		// The write's own bind names the epoch the fake shows, which the run's does not.
		err := pinned().writeAfter(func(c *client.Client) error { w.ReadsAt("ep_OB-1", c.EpochMoves()); return nil }, w, say)
		if err != nil {
			t.Fatalf("error %v; want the write sent under its own bind", err)
		}
		if got := e.api.writes(); len(got) != 1 {
			t.Errorf("writes %q; want the one", got)
		}
	})
}
