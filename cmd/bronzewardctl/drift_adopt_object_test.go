package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// Each test names the line of docs/spec/operator-experience.md (§5.8, "Structured output of a
// composite") it checks, as "contract line N".

// adoptObject runs drift adopt for the chain with -o json and reads standard output as one object: a
// second value is a failure.
func adoptObject(t *testing.T, extra ...string) (code int, obj adoption, out, errOut string) {
	t.Helper()
	args := append([]string{"-o", "json", "drift", "adopt", "mch_BWSYNTH", "--draft", "drf_BWSYNTH"}, extra...)
	code, out, errOut = runDrift(t, args...)
	dec := json.NewDecoder(strings.NewReader(out))
	if err := dec.Decode(&obj); err != nil {
		t.Fatalf("exit %d, stdout %q, stderr %q: standard output is not an object: %v", code, out, errOut, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		t.Fatalf("stdout %q holds more than one value", out)
	}
	return code, obj, out, errOut
}

// states is the three step states of obj, in order.
func states(obj adoption) []stepState {
	var s []stepState
	for _, st := range obj.Steps {
		s = append(s, st.State)
	}
	return s
}

func wantStates(t *testing.T, obj adoption, want ...stepState) {
	t.Helper()
	got := states(obj)
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("states %v; want %v", got, want)
	}
}

// TestDriftAdoptObject_Done: contract lines 894-896, 909-911 and 931-937: all three steps done, one
// object, each answer as served, the release on step 2, no command on a done step, exit 0.
func TestDriftAdoptObject_Done(t *testing.T) {
	f := adoptChainServer(t)
	code, obj, out, errOut := adoptObject(t)
	if code != 0 || obj.Machine != "mch_BWSYNTH" || obj.Draft != "drf_BWSYNTH" {
		t.Fatalf("exit %d, object %+v, stderr %q; want 0 and the machine and the draft", code, obj, errOut)
	}
	wantStates(t, obj, stateDone, stateDone, stateDone)
	if obj.Steps[1].Release != "rel_BWSYNTH" || !strings.Contains(string(obj.Steps[2].Answer), `"pln_BWSYNTH"`) || !strings.Contains(string(obj.Steps[0].Answer), `"ingestion"`) {
		t.Errorf("steps %+v; want the release on step 2 and the answers of steps 1 and 3 as served", obj.Steps)
	}
	for _, s := range obj.Steps {
		if s.Command != "" || s.Error != "" {
			t.Errorf("step %d has command %q, error %q; want neither on a done step", s.Step, s.Command, s.Error)
		}
	}
	if got := f.plan.took(); len(got) != 1 || strings.Contains(out, "Step 1/3") {
		t.Errorf("plans sent %q, stdout %q; want one plan and none of the text output", got, out)
	}
}

// TestDriftAdoptObject_YAML: contract lines 965-966: the same run with -o yaml is one document with no
// "---" and no duplicate key, with every step in it.
func TestDriftAdoptObject_YAML(t *testing.T) {
	adoptChainServer(t)
	code, out, errOut := runDrift(t, "-o", "yaml", "drift", "adopt", "mch_BWSYNTH", "--draft", "drf_BWSYNTH")
	if code != 0 || strings.HasPrefix(out, "---") || strings.Contains(out, "\n---") {
		t.Fatalf("exit %d, stdout %q, stderr %q; want 0 and one document with no ---", code, out, errOut)
	}
	dec := yaml.NewDecoder(strings.NewReader(out))
	var obj map[string]any // a duplicate key at any depth is an error
	if err := dec.Decode(&obj); err != nil {
		t.Fatalf("stdout is not one valid document: %v\n%s", err, out)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		t.Errorf("a second document follows: %v", err)
	}
	if steps, _ := obj["steps"].([]any); len(steps) != 3 {
		t.Errorf("object %v; want three steps", obj)
	}
}

// TestDriftAdoptObject_NotAuthorOrNotPerson: contract lines 914-915 and 950-951: a caller who is not an
// author, or not a person, gets three not-run steps with their commands, exit 0, and nothing is sent.
func TestDriftAdoptObject_NotAuthorOrNotPerson(t *testing.T) {
	for _, c := range []struct{ name, identity string }{
		{"a publisher, not an author", `{"kind":"human","principal":"h_BWSYNTH","roles":["publisher"]}`},
		{"an author who is not a person", `{"kind":"service","principal":"svc_BWSYNTH","roles":["author","publisher"]}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := adoptChainServer(t)
			f.identity = c.identity
			f.log = nil
			code, obj, _, errOut := adoptObject(t)
			wantStates(t, obj, stateNotRun, stateNotRun, stateNotRun)
			for _, s := range obj.Steps {
				if s.Command == "" || s.Answer != nil {
					t.Errorf("step %d is %+v; want a command and no answer", s.Step, s)
				}
			}
			for _, l := range f.log {
				if !strings.HasPrefix(l, "GET ") {
					t.Errorf("sent %q; want nothing but reads", l)
				}
			}
			if code != 0 {
				t.Errorf("exit %d, stderr %q; want 0", code, errOut)
			}
		})
	}
}

// TestDriftAdoptObject_NoPublisher: contract lines 914-915: an author who is not a publisher runs step 1
// and leaves steps 2 and 3 not-run, exit 0.
func TestDriftAdoptObject_NoPublisher(t *testing.T) {
	f := adoptChainServer(t)
	f.identity = `{"kind":"human","principal":"h_BWSYNTH","roles":["author"]}`
	code, obj, _, errOut := adoptObject(t)
	wantStates(t, obj, stateDone, stateNotRun, stateNotRun)
	if code != 0 || obj.Steps[1].Command != "bronzewardctl publish 'drf_BWSYNTH'" {
		t.Errorf("exit %d, step 2 %+v, stderr %q; want 0 and the publish command", code, obj.Steps[1], errOut)
	}
}

// TestDriftAdoptObject_Paused: contract lines 912-913 and 940-943: an import paused for review is
// paused, its command is import mark, the later steps are not-run, exit 0.
func TestDriftAdoptObject_Paused(t *testing.T) {
	f := adoptChainServer(t)
	f.imp.set("paused", 1, time.Hour, "running", "", "", ev("started", `{"type":"started"}`), ev("staged", `{"type":"staged"}`), ev("paused", `{"type":"paused","generation":1}`))
	code, obj, _, errOut := adoptObject(t, "--review")
	wantStates(t, obj, statePaused, stateNotRun, stateNotRun)
	if code != 0 || obj.Steps[0].Command != "bronzewardctl import mark 'ing_BWSYNTH'" || obj.Steps[0].Answer == nil {
		t.Errorf("exit %d, step 1 %+v, stderr %q; want 0, the import mark command and the answer", code, obj.Steps[0], errOut)
	}
}

// TestDriftAdoptObject_NotSent: contract lines 916-918 and 944: a write the client refuses before it
// sends anything is failed, with no answer; step 2 when the epoch moved while the import was read, and
// step 3 when it moved after the publication. The later steps are not-run.
func TestDriftAdoptObject_NotSent(t *testing.T) {
	f := adoptChainServer(t)
	f.moveOnReread = true
	code, obj, _, errOut := adoptObject(t)
	wantStates(t, obj, stateDone, stateFailed, stateNotRun)
	if code == 0 || obj.Steps[1].Answer != nil || obj.Steps[1].Error == "" || obj.Steps[1].Command != "bronzewardctl publish 'drf_BWSYNTH'" {
		t.Errorf("exit %d, step 2 %+v, stderr %q; want a failed step 2 with its error, its command and no answer", code, obj.Steps[1], errOut)
	}

	g := adoptChainServer(t)
	g.moveAfterPublish = true
	code, obj, _, errOut = adoptObject(t)
	wantStates(t, obj, stateDone, stateDone, stateFailed)
	if code == 0 || obj.Steps[2].Answer != nil || !strings.Contains(obj.Steps[2].Command, "plan create --release 'rel_BWSYNTH'") || obj.Steps[1].Release != "rel_BWSYNTH" {
		t.Errorf("exit %d, steps %+v, stderr %q; want a failed step 3 whose command names the release", code, obj.Steps, errOut)
	}
}

// TestDriftAdoptObject_FailedPublication: contract lines 918-919: a publication that ended failed is
// failed with exit 4 and the publish command, not exit 1 and an import command.
func TestDriftAdoptObject_FailedPublication(t *testing.T) {
	f := adoptChainServer(t)
	f.pub.state = "failed"
	code, obj, _, errOut := adoptObject(t)
	wantStates(t, obj, stateDone, stateFailed, stateNotRun)
	if code != 4 || obj.Steps[1].Command != "bronzewardctl publish 'drf_BWSYNTH'" || obj.Steps[1].Answer == nil {
		t.Errorf("exit %d, step 2 %+v, stderr %q; want 4, the publish command and the accepted answer", code, obj.Steps[1], errOut)
	}
}

// TestDriftAdoptObject_ResumeFailed: contract lines 919-923: a step 1 run that recorded resume-failed is
// failed with exit 1 and the import status command.
func TestDriftAdoptObject_ResumeFailed(t *testing.T) {
	f := adoptChainServer(t)
	f.imp.set("released", 1, time.Hour, "running", "", "", ev("started", `{"type":"started"}`), ev("resume-failed", `{"type":"resume-failed","code":"dependency-unavailable"}`))
	code, obj, _, errOut := adoptObject(t)
	wantStates(t, obj, stateFailed, stateNotRun, stateNotRun)
	if code != 1 || obj.Steps[0].Command != "bronzewardctl import status 'ing_BWSYNTH'" {
		t.Errorf("exit %d, step 1 %+v, stderr %q; want 1 and the import status command", code, obj.Steps[0], errOut)
	}
}

// TestDriftAdoptObject_Unknown: contract lines 924-929 and 947: an unknown outcome is unknown with
// exit 3, and the object is printed on that exit; the block with the key stays on standard error.
func TestDriftAdoptObject_Unknown(t *testing.T) {
	f := adoptChainServer(t)
	f.pubUnknown = true
	code, obj, _, errOut := adoptObject(t)
	wantStates(t, obj, stateDone, stateUnknown, stateNotRun)
	if code != 3 || !strings.Contains(errOut, "--idempotency-key ") || obj.Steps[1].Command != "bronzewardctl publish 'drf_BWSYNTH'" {
		t.Errorf("exit %d, step 2 %+v, stderr %q; want 3, the key block on stderr and the publish command", code, obj.Steps[1], errOut)
	}
}

// TestDriftAdoptObject_PrintsNothingBeforeTheIdentityRead: contract lines 947-949: arguments refused, and
// an identity read that fails, run no step, so standard output is empty in json and in yaml, not only the
// exit code is checked.
func TestDriftAdoptObject_PrintsNothingBeforeTheIdentityRead(t *testing.T) {
	adoptChainServer(t)
	for _, o := range []string{"json", "yaml"} {
		for _, c := range []struct {
			name string
			args []string
			code int
		}{
			{"no --draft", []string{"drift", "adopt", "mch_BWSYNTH"}, 2},
			{"no machine", []string{"drift", "adopt", "--draft", "drf_BWSYNTH"}, 2},
			{"a staging that is neither", []string{"drift", "adopt", "mch_BWSYNTH", "--draft", "drf_BWSYNTH", "--staging", "plain"}, 2},
			{"--review with transient staging", []string{"drift", "adopt", "mch_BWSYNTH", "--draft", "drf_BWSYNTH", "--staging", "transient", "--review"}, 2},
			{"half a repeat", []string{"drift", "adopt", "mch_BWSYNTH", "--draft", "drf_BWSYNTH", "--idempotency-key", "BWSYNTH-aaaaaaaaaaaaaaa"}, 2},
			{"an unknown flag", []string{"drift", "adopt", "mch_BWSYNTH", "--draft", "drf_BWSYNTH", "--nope"}, 2},
		} {
			code, out, errOut := runDrift(t, append([]string{"-o", o}, c.args...)...)
			if code != c.code || out != "" || errOut == "" {
				t.Errorf("-o %s, %s: exit %d, stdout %q, stderr %q; want %d, nothing on standard output and the refusal on standard error", o, c.name, code, out, errOut, c.code)
			}
		}
	}
	// The identity read fails: /server answers, /identity answers a problem document. The test says that
	// the identity was asked for, so that it cannot pass on a command that failed before it.
	var asked int
	f := adoptChainServer(t)
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/identity" {
			f.ServeHTTP(w, r)
			return
		}
		asked++
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `{"type":"urn:bronzeward:problem:dependency-unavailable","title":"Service Unavailable","status":503}`)
	}))
	t.Cleanup(down.Close)
	automationContext(t, down.URL)
	for _, o := range []string{"json", "yaml"} {
		before := asked
		var out, errb bytes.Buffer
		if code := run([]string{"-o", o, "drift", "adopt", "mch_BWSYNTH", "--draft", "drf_BWSYNTH"}, &out, &errb); code == 0 || out.Len() != 0 || errb.Len() == 0 || asked == before {
			t.Errorf("-o %s, identity read failing: exit %d, stdout %q, stderr %q, identity asked %d times; want a failure after the identity was asked for, with nothing on standard output", o, code, out.String(), errb.String(), asked-before)
		}
	}
}

// Contract lines 959 to 961: a client failure after the last step leaves every step as it ended and
// the exit code nonzero. Here the plan's answer carries a new epoch, the client must keep it, and
// the configuration directory cannot be written.
func TestDriftAdoptObject_SaveFailsAfterStep3(t *testing.T) {
	f := adoptChainServer(t)
	f.plan.post = func(w http.ResponseWriter, _ string) {
		w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-2")
		dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "bronzeward")
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Errorf("the configuration directory stays writable: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		w.WriteHeader(http.StatusCreated)
		f.plan.mu.Unlock()
		fmt.Fprint(w, f.plan.plan())
		f.plan.mu.Lock()
	}
	code, obj, out, errOut := adoptObject(t)
	wantStates(t, obj, stateDone, stateDone, stateDone)
	if code == 0 || !strings.Contains(errOut, "keeping the epoch") {
		t.Errorf("exit %d, stderr %q, stdout %q; want a nonzero exit naming the epoch that could not be kept, with the object printed", code, errOut, out)
	}
}

// TestDriftAdoptObject_OutputFailureKeepsTheSpecificExit: contract lines 956-958: a failure to write the
// object gives exit 6 only where nothing more specific stands. Here step 1 ended resume-failed (exit
// 1) and standard output is full.
func TestDriftAdoptObject_OutputFailureKeepsTheSpecificExit(t *testing.T) {
	f := adoptChainServer(t)
	f.imp.set("released", 1, time.Hour, "running", "", "", ev("started", `{"type":"started"}`), ev("resume-failed", `{"type":"resume-failed","code":"dependency-unavailable"}`))
	var errb bytes.Buffer
	code := run([]string{"-o", "json", "drift", "adopt", "mch_BWSYNTH", "--draft", "drf_BWSYNTH"}, failsWith{syscall.ENOSPC}, &errb)
	if code != 1 || !strings.Contains(errb.String(), "no space left") {
		t.Errorf("exit %d, stderr %q; want 1 and the write failure named on standard error", code, errb.String())
	}
}

// TestDriftAdoptObject_SaveFailsAfterIdentity: contract lines 948-952: the identity read succeeded and
// then the client cannot keep the epoch it showed. No step ran, and the object is printed with three
// not-run steps and a nonzero exit.
func TestDriftAdoptObject_SaveFailsAfterIdentity(t *testing.T) {
	f := adoptChainServer(t)
	f.identityEpoch = "ep_BWSYNTH-9"
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "bronzeward")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	code, obj, _, errOut := adoptObject(t)
	wantStates(t, obj, stateNotRun, stateNotRun, stateNotRun)
	if code == 0 || !strings.Contains(errOut, "keeping the epoch") {
		t.Errorf("exit %d, stderr %q; want a nonzero exit naming the epoch that could not be kept", code, errOut)
	}
}

// TestDriftAdoptObject_EmptyMachineRefused: an empty machine argument is a usage error before the
// identity read, with nothing on standard output, in json and in yaml.
func TestDriftAdoptObject_EmptyMachineRefused(t *testing.T) {
	adoptChainServer(t)
	for _, o := range []string{"json", "yaml"} {
		code, out, errOut := runDrift(t, "-o", o, "drift", "adopt", "", "--draft", "drf_BWSYNTH")
		if code != 2 || out != "" || errOut == "" {
			t.Errorf("-o %s: exit %d, stdout %q, stderr %q; want 2, nothing on standard output and the refusal on standard error", o, code, out, errOut)
		}
	}
}

// TestDriftAdoptObject_PausedThenSaveFails: contract lines 912-913 and 960-962: an import that paused
// for review is paused, with its import mark command, when the client then cannot keep the epoch the
// follow showed. The exit is nonzero and the error is on standard error.
func TestDriftAdoptObject_PausedThenSaveFails(t *testing.T) {
	f := adoptChainServer(t)
	f.imp.set("paused", 1, time.Hour, "running", "", "", ev("started", `{"type":"started"}`), ev("staged", `{"type":"staged"}`), ev("paused", `{"type":"paused","generation":1}`))
	f.moveInFollow = true
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "bronzeward")
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	old := f.imp.onIngested
	f.imp.onIngested = func() {
		old()
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Errorf("the configuration directory stays writable: %v", err)
		}
	}
	code, obj, _, errOut := adoptObject(t, "--review")
	wantStates(t, obj, statePaused, stateNotRun, stateNotRun)
	if code == 0 || !strings.Contains(errOut, "keeping the epoch") || obj.Steps[0].Command != "bronzewardctl import mark 'ing_BWSYNTH'" {
		t.Errorf("exit %d, step 1 %+v, stderr %q; want a nonzero exit naming the epoch that could not be kept, and the import mark command", code, obj.Steps[0], errOut)
	}
}

// wantReadCommand fails where s is unknown with an accepted write and its command is not a read of what
// the write started: import status or operation show, never the repeat of the write (contract lines
// 940-941). A write that was never confirmed accepted keeps the keyed repeat, which is not covered here.
func wantReadCommand(t *testing.T, s compositeStep) {
	t.Helper()
	if s.State != stateUnknown {
		t.Errorf("step %d is %v; want unknown", s.Step, s.State)
	}
	if !strings.HasPrefix(s.Command, "bronzewardctl import status ") && !strings.HasPrefix(s.Command, "bronzewardctl operation show ") {
		t.Errorf("step %d is unknown with the command %q; want import status or operation show, never the repeat of the write", s.Step, s.Command)
	}
}

// TestDriftAdoptObject_LapsedClaim: contract lines 924-929 and 940-941: an import whose claim lapsed
// while its operation still runs is unknown with exit 3, and its command is import status, which reads
// the claim, never the drift adopt repeat, which would start another import.
func TestDriftAdoptObject_LapsedClaim(t *testing.T) {
	f := adoptChainServer(t)
	f.imp.set("held", 1, -time.Hour, "running", "", "", ev("started", `{"type":"started"}`))
	code, obj, _, errOut := adoptObject(t)
	wantStates(t, obj, stateUnknown, stateNotRun, stateNotRun)
	wantReadCommand(t, obj.Steps[0])
	if code != 3 || obj.Steps[0].Command != "bronzewardctl import status 'ing_BWSYNTH'" {
		t.Errorf("exit %d, step 1 %+v, stderr %q; want 3 and the import status command", code, obj.Steps[0], errOut)
	}
}

// TestDriftAdopt_TableSaveFailsKeepsTheTextOutput: the table output of drift adopt does not change
// (operator-experience.md §5.8, the first paragraph): a client that cannot keep the epoch after the
// import ended prints the later steps as before, and does not read the import again, which only the
// structured object needs to state step 1.
func TestDriftAdopt_TableSaveFailsKeepsTheTextOutput(t *testing.T) {
	f := adoptChainServer(t)
	f.moveInFollow = true
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "bronzeward")
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	old := f.imp.onIngested
	f.imp.onIngested = func() {
		old()
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Errorf("the configuration directory stays writable: %v", err)
		}
	}
	code, out, errOut := runDrift(t, "drift", "adopt", "mch_BWSYNTH", "--draft", "drf_BWSYNTH")
	f.mu.Lock()
	rereads := f.rereads
	f.mu.Unlock()
	if code == 0 || !strings.Contains(errOut, "keeping the epoch") || !strings.Contains(out, "The later steps:\n") ||
		strings.Contains(out, "the context could not be saved") || rereads != 1 {
		t.Errorf("exit %d, stdout %q, stderr %q, reads of the import %d; want a nonzero exit, the later steps, no reread and no recovery text", code, out, errOut, rereads)
	}
}

// TestDriftAdoptObject_RereadFails: contract lines 924-929 and 940-941: when the read of an accepted
// import that step 1 ends with fails, step 1 is unknown and its command is the one the table output
// names, import status, never the drift adopt repeat, which would write again.
func TestDriftAdoptObject_RereadFails(t *testing.T) {
	f := adoptChainServer(t)
	f.failReread = true
	code, obj, _, errOut := adoptObject(t)
	wantStates(t, obj, stateUnknown, stateNotRun, stateNotRun)
	wantReadCommand(t, obj.Steps[0])
	if code == 0 || obj.Steps[0].Command != "bronzewardctl import status 'ing_BWSYNTH'" || obj.Steps[0].Answer == nil {
		t.Errorf("exit %d, step 1 %+v, stderr %q; want a nonzero exit, the import status command and the accepted answer", code, obj.Steps[0], errOut)
	}
}

// TestDriftAdoptObject_OperationNotRead: contract lines 924-929 and 940-941: when the operation of an
// accepted write cannot be read, its step is unknown and its command is the read of that operation,
// never the repeat of the write. It holds for the import of step 1 and for the publication of step 2.
func TestDriftAdoptObject_OperationNotRead(t *testing.T) {
	f := adoptChainServer(t)
	f.failIngOp = true
	code, obj, _, errOut := adoptObject(t)
	wantStates(t, obj, stateUnknown, stateNotRun, stateNotRun)
	wantReadCommand(t, obj.Steps[0])
	if code == 0 || obj.Steps[0].Command != "bronzewardctl operation show 'op_BWSYNTH-ing'" {
		t.Errorf("import: exit %d, step 1 %+v, stderr %q; want a nonzero exit and the operation show command", code, obj.Steps[0], errOut)
	}

	g := adoptChainServer(t)
	g.failPubOp = true
	code, obj, _, errOut = adoptObject(t)
	wantStates(t, obj, stateDone, stateUnknown, stateNotRun)
	wantReadCommand(t, obj.Steps[1])
	if code == 0 || obj.Steps[1].Command != "bronzewardctl operation show 'op_BWSYNTH-pub'" {
		t.Errorf("publication: exit %d, step 2 %+v, stderr %q; want a nonzero exit and the operation show command", code, obj.Steps[1], errOut)
	}
}
