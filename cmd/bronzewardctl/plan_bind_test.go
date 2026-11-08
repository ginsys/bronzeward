package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/client"
)

func planCreated(f *planFake) {
	f.post = func(w http.ResponseWriter, _ string) {
		w.WriteHeader(http.StatusCreated)
		f.mu.Unlock()
		fmt.Fprint(w, f.plan())
		f.mu.Lock()
	}
}

// TestPlanCreateOpensTheContextOfItsGlobalFlag: a global flag after the command, here --context,
// picks the server the write goes to. The command parses its arguments before it opens the client,
// so the other context's server gets the plan and the current context's gets nothing.
func TestPlanCreateOpensTheContextOfItsGlobalFlag(t *testing.T) {
	lab := planServer(t)
	lab.set(planCreated)
	other := &planFake{state: "proposed", op: "apply-config", evidence: diffEvidence, desired: "rel_BWSYNTH", assignment: "asr_BWSYNTH-1"}
	other.set(planCreated)
	srv := httptest.NewServer(other)
	t.Cleanup(srv.Close)
	if err := client.UpdateConfig(func(c *client.Config) error {
		c.Contexts = append(c.Contexts, client.Context{Name: "other", Server: srv.URL, Credential: "automation", Automation: c.Contexts[0].Automation})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCtl(t, "plan", "create", "--release", "rel_BWSYNTH", "--machine", "mch_a", "--operation", "apply-config", "--context", "other")
	if got, none := other.took(), lab.took(); code != 0 || len(got) != 1 || len(none) != 0 {
		t.Errorf("exit %d, other context got %q, current context got %q, stdout %q, stderr %q; want the one plan on the other context only", code, got, none, out, errOut)
	}
}

// TestDriftRevertBindsTheAnswerBeforeARefresh: the machine answer shows ep_BWSYNTH-2 and the
// /server read that Get makes for the new epoch shows ep_BWSYNTH-3, which stays. The request was
// built from the ep_BWSYNTH-2 answer, so the send is refused. Binding to the client's last epoch
// would take ep_BWSYNTH-3 and send it.
func TestDriftRevertBindsTheAnswerBeforeARefresh(t *testing.T) {
	f := revertServer(t)
	read := false
	f.set(func(f *planFake) {
		f.epochFor = func(path string) string {
			switch {
			case path == "/machines/mch_a" && !read:
				read = true
				return "ep_BWSYNTH-2"
			case !read:
				return "ep_BWSYNTH-1"
			}
			return "ep_BWSYNTH-3"
		}
	})
	code, out, errOut := runDrift(t, "drift", "revert", "mch_a")
	const want = "the epoch changed from 'ep_BWSYNTH-2' to 'ep_BWSYNTH-3' after the request was built"
	if got := f.took(); code == 0 || out != "" || len(got) != 0 || !strings.Contains(errOut, want) {
		t.Errorf("exit %d, stdout %q, sent %q, stderr %q; want a refusal that sent nothing and says %q", code, out, got, errOut, want)
	}
}
