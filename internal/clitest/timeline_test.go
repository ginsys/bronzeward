package clitest

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// publishLabels publishes a release of the seeded machine whose workers' labels are labels, a
// document of the role layer, and returns it; it becomes the machine's Desired.
func (e *planEnv) publishLabels(t *testing.T, title, labels string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "labels.yaml")
	if err := os.WriteFile(file, []byte(labels), 0o600); err != nil {
		t.Fatal(err)
	}
	h := []string{"--context", "human"}
	draft := regexp.MustCompile(`drf_[a-z2-7]{26}`).FindString(e.ctl(append(h, "draft", "create", "--cluster", e.s.cluster, "--title", title)...).out)
	e.f.importMachine(t, e.s.machine, draft)
	for _, step := range [][]string{{"draft", "show", draft}, {"fragment", "set", "labels", "--draft", draft, "--layer", "role", "--file", file},
		{"profile", "set", "workers", "--draft", draft, "--fragments", "labels"}, {"assignment", "set", e.s.machine, "--draft", draft, "--profiles", "workers"}} {
		e.f.want(e.ctl(append(h, step...)...), 0)
	}
	rel := regexp.MustCompile(`rel_[a-z2-7]{26}`).FindString(e.ctl(append(h, "publish", draft)...).out)
	if rel == "" {
		t.Fatal("the publication named no release")
	}
	return rel
}

// TestTimeline runs machine timeline (#354, decision T24 of #258) through the CLI against a
// server with a provider and a Talos stand-in, after the seeded machine's adoption: --follow, which
// shows a new entry, reads no more often than every 5 s and ends at Ctrl-C with exit 130; and a
// refused commitment's entry with its plan, comparison and cause, then the plan's cancellation
// with its reason.
func TestTimeline(t *testing.T) {
	e := newWatchEnv(t)
	f := e.f
	h := []string{"--context", "human"}
	file := filepath.Join(f.home, "viewer.json")
	f.store(file, f.mint("h-viewer", ""), "")
	f.want(f.ctl(nil, "", "context", "add", "viewer", "--server", e.in.URL, "--certificate-authority", e.in.ca, "--issuer", f.iss.URL,
		"--client-id", "BWSYNTH-cli", "--audience", "bronzeward", "--oidc-token-file", file), 0)

	// T9 (operator-experience.md §13): the viewer follows the apply; the injector cuts the first
	// stream after its first event, and the stand-in accepts the apply once the watch opened the
	// stream again. Done when the completion is reported with its time. The server's own stop of
	// 60 s is the fixture's run (fixtures/participants/session t9 disconnect).
	t.Run("T9 journey", func(t *testing.T) {
		op, release := e.held(t)
		defer release()
		var streams atomic.Int32
		e.in.set(func(w http.ResponseWriter, r *http.Request) bool {
			if r.Header.Get("Accept") != "text/event-stream" {
				return false
			}
			if streams.Add(1) == 1 {
				f.relay(t, w, r, func(string, string) bool { return false })
				return true
			}
			release()
			return false
		}, nil)
		args := []string{"--context", "viewer", "operation", "watch", op}
		r := e.ctl(args...)
		f.want(r, 0, "Completed at ", " (server): applied "+e.rel+", confirmed by observation obs_")
		if streams.Load() < 2 {
			t.Errorf("the watch opened %d streams; want the cut one and one more", streams.Load())
		}
		out := fmt.Sprintf("(the injector ends the first event stream after its first event)\n$ bronzewardctl %s\n%s%s[exit %d]\n",
			strings.Join(args, " "), r.out, r.err, r.code)
		out = localTimes.ReplaceAllString(eventTimes.ReplaceAllString(out, "$1 <time>  "), "<local time>")
		keepJourney(t, "t9", controllerID.ReplaceAllString(out, "<controller>"), t.TempDir())
	})

	t.Run("T24 follow", func(t *testing.T) {
		var mu sync.Mutex
		var reads []time.Time
		e.in.set(func(w http.ResponseWriter, r *http.Request) bool {
			if strings.HasSuffix(r.URL.Path, "/timeline") {
				mu.Lock()
				reads = append(reads, time.Now())
				mu.Unlock()
			}
			return false
		}, nil)
		args := append(slices.Clone(h), "machine", "timeline", e.s.machine, "--follow", "--limit", "3")
		cmd := f.command(nil, args...)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer cmd.Process.Kill()
		count := func() int { mu.Lock(); defer mu.Unlock(); return len(reads) }
		for deadline := time.Now().Add(time.Minute); count() == 0; time.Sleep(100 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("the follow read nothing within a minute")
			}
		}
		// A new entry: a plan's, which the next forward read shows.
		p := planID.FindString(e.ctl(e.create("apply-config")...).out)
		for n, deadline := count(), time.Now().Add(time.Minute); count() < n+2; time.Sleep(100 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("the follow read the timeline fewer than twice in a minute after the plan")
			}
		}
		cmd.Process.Signal(os.Interrupt)
		waited := make(chan error, 1)
		go func() { waited <- cmd.Wait() }()
		r := result{code: -1}
		select {
		case err := <-waited:
			var exit *exec.ExitError
			if r.code = 0; errors.As(err, &exit) {
				r.code = exit.ExitCode()
			}
		case <-time.After(30 * time.Second):
			t.Fatal("the follow did not end within 30 s of Ctrl-C")
		}
		r.out, r.err = out.String(), errb.String()
		f.scan(r, args)
		f.want(r, 130, "REVISION  ", "Stopped following the timeline of machine "+e.s.machine+" at your request.")
		if !regexp.MustCompile(`(?m)^\d+ +.+  plan +plan ` + p + `, operation apply-config, release ` + e.rel + `, by idn_[a-z2-7]{26} \(publisher\)$`).MatchString(r.out) {
			t.Errorf("the follow did not show plan %s's entry:\n%s", p, r.out)
		}
		mu.Lock()
		defer mu.Unlock()
		for i := 1; i < len(reads); i++ {
			if gap := reads[i].Sub(reads[i-1]); gap < 5*time.Second-100*time.Millisecond {
				t.Errorf("read %d came %v after the one before; want 5 s or more", i, gap)
			}
		}
		e.cancelAll(t)
	})

	// T24 and #258's stale plan: a plan of rel, approved after a later publication moved the
	// machine's Desired, which the controller refuses to commit.
	t.Run("T24 refusal and cancellation", func(t *testing.T) {
		e.in.set(nil, nil)
		p := planID.FindString(e.ctl(e.create("apply-config")...).out)
		rel := e.rel
		e.rel = e.publishLabels(t, "timeline tier", "machine:\n  nodeLabels:\n    node.example.test/pool: workers\n    node.example.test/tier: c\n")
		e.approve(t, p)
		e.until(t, "true", `SELECT EXISTS (SELECT FROM machine_event WHERE machine = $1 AND kind = 'refusal' AND entry->>'plan' = $2)::text`, e.s.machine, p)
		f.want(e.ctl(append(h, "plan", "show", p)...), 0, rel+" at plan creation, "+e.rel+" now")
		f.want(e.ctl(append(h, "plan", "cancel", p, "--reason", "BWSYNTH stale: Desired moved")...), 0)
		r := e.ctl(append(h, "machine", "timeline", e.s.machine, "--all")...)
		f.want(r, 0)
		for _, want := range []string{
			`refusal +plan ` + p + `, comparison \S+, cause [^,]+`,
			`plan-cancellation +plan ` + p + `, by idn_[a-z2-7]{26} \(publisher\), reason BWSYNTH stale: Desired moved$`,
			`approval +approval apr_[a-z2-7]{26}, plan ` + p + `, by idn_[a-z2-7]{26} \(approver\), no self-approval reason$`,
		} {
			if !regexp.MustCompile(`(?m)^\d+ +.+  ` + want).MatchString(r.out) {
				t.Errorf("machine timeline: no line %s in\n%s", want, r.out)
			}
		}
	})
	f.scanFiles()
}
