package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"

	"github.com/ginsys/bronzeward/internal/client"
	"go.yaml.in/yaml/v3"
)

// Each test names the line of docs/spec/operator-experience.md (§5.8, "Structured output of a
// composite") it checks, as "contract line N".

// settled is a collector after a run that finished step 1 and 2 well and ended step 3 with err.
func settled(err error) *collector {
	co := newCollector("mch_BWSYNTH", "drf_BWSYNTH")
	co.begin(1)
	co.record([]byte(`{"ingestion":"ing_BWSYNTH","operation":"op_BWSYNTH-ing"}`), nil)
	co.settle(1, nil)
	co.begin(2)
	co.record([]byte(`{"id":"op_BWSYNTH-pub","state":"queued"}`), nil)
	co.settle(2, nil)
	co.release("rel_BWSYNTH")
	co.begin(3)
	co.settle(3, err)
	return co
}

// TestCompositeObjectShape_JSON: contract lines 894-896 and 907-908: three members in order, the three
// steps in order with their members in order, a done step with no command, an unrun-write step with no
// answer.
func TestCompositeObjectShape_JSON(t *testing.T) {
	var out bytes.Buffer
	e := &env{output: "json"}
	e.out = tableGuard{e, &out, &out}
	if err := e.printComposite(settled(operationFailed{errors.New("plan operation failed")}).object()); err != nil {
		t.Fatal(err)
	}
	want := `{"machine":"mch_BWSYNTH","draft":"drf_BWSYNTH","steps":[` +
		`{"step":1,"name":"import","role":"author","state":"done","answer":{"ingestion":"ing_BWSYNTH","operation":"op_BWSYNTH-ing"}},` +
		`{"step":2,"name":"publish","role":"publisher","state":"done","answer":{"id":"op_BWSYNTH-pub","state":"queued"},"release":"rel_BWSYNTH"},` +
		`{"step":3,"name":"plan","role":"publisher","state":"failed","command":"bronzewardctl plan create --release 'rel_BWSYNTH' --machine 'mch_BWSYNTH' --operation 'adopt'","error":"plan operation failed"}]}` + "\n"
	if out.String() != want {
		t.Errorf("printed\n%s\nwant\n%s", out.String(), want)
	}
}

// TestCompositeObjectShape_YAML: contract lines 965-966: -o yaml is the same object as one document,
// with no "---" and no duplicate key, at any depth.
func TestCompositeObjectShape_YAML(t *testing.T) {
	var out bytes.Buffer
	e := &env{output: "yaml"}
	e.out = tableGuard{e, &out, &out}
	if err := e.printComposite(settled(nil).object()); err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(out.String(), "---") || strings.Contains(out.String(), "\n---") {
		t.Errorf("printed a document marker:\n%s", out.String())
	}
	dec := yaml.NewDecoder(&out)
	var obj map[string]any // a duplicate key is an error here
	if err := dec.Decode(&obj); err != nil {
		t.Fatalf("not one valid document: %v", err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		t.Errorf("a second document follows: %v", err)
	}
	steps, _ := obj["steps"].([]any)
	if len(steps) != 3 || obj["machine"] != "mch_BWSYNTH" || obj["draft"] != "drf_BWSYNTH" {
		t.Fatalf("object %v; want machine, draft and three steps", obj)
	}
	if first, _ := steps[0].(map[string]any); first["state"] != "done" || first["answer"].(map[string]any)["ingestion"] != "ing_BWSYNTH" {
		t.Errorf("step 1 is %v; want done with its answer unchanged", first)
	}
}

// TestCompositeNotRunSteps: contract lines 914-915 and 939: before any step ran, every step is not-run
// and has the command that runs it; a caller who is not an author gets exactly that.
func TestCompositeNotRunSteps(t *testing.T) {
	obj := newCollector("mch_1", "drf_1").object()
	for i, s := range obj.Steps {
		if s.Step != i+1 || s.State != stateNotRun || s.Command == "" || s.Answer != nil || s.Error != "" {
			t.Errorf("step %d is %+v; want not-run with a command only", i+1, s)
		}
	}
	if !strings.HasPrefix(obj.Steps[0].Command, "bronzewardctl drift adopt 'mch_1' --draft 'drf_1'") ||
		obj.Steps[1].Command != "bronzewardctl publish 'drf_1'" || !strings.Contains(obj.Steps[2].Command, "plan create --release <release> --machine 'mch_1'") {
		t.Errorf("commands are %q, %q, %q", obj.Steps[0].Command, obj.Steps[1].Command, obj.Steps[2].Command)
	}
}

// TestCompositePausedStep: contract lines 912-913 and 940-943: step 1 paused, its command the import
// mark command, the later steps not-run.
func TestCompositePausedStep(t *testing.T) {
	co := newCollector("mch_1", "drf_1")
	co.begin(1)
	co.record([]byte(`{"ingestion":"ing_1","operation":"op_1"}`), nil)
	co.pause("ing_1")
	obj := co.object()
	if s := obj.Steps[0]; s.State != statePaused || s.Command != "bronzewardctl import mark 'ing_1'" || string(s.Answer) == "" {
		t.Errorf("step 1 is %+v; want paused with its answer and the import mark command", s)
	}
	if obj.Steps[1].State != stateNotRun || obj.Steps[2].State != stateNotRun {
		t.Errorf("later steps are %s, %s; want not-run", obj.Steps[1].State, obj.Steps[2].State)
	}
}

// TestClassifyStates: contract lines 909-929, one row per state and error type.
func TestClassifyStates(t *testing.T) {
	plain := errors.New("a read failed")
	for _, c := range []struct {
		name     string
		err      error
		accepted bool
		want     stepState
	}{
		{"done: the step function returned nil (line 909)", nil, true, stateDone},
		{"done: a publication without a readable release (lines 936-939)", releaselessErr{plain}, true, stateDone},
		{"failed: the operation ended failed, rejected or cancelled (lines 916-918)", operationFailed{plain}, true, stateFailed},
		{"failed: a run that recorded resume-failed (lines 919-923)", &client.Blocked{Err: resumeFailedErr{plain, "ing_1"}}, true, stateFailed},
		{"failed: a write the server refused (line 916)", &client.Problem{Status: 409}, false, stateFailed},
		{"failed: a write the client refused before sending (line 917)", notSent{plain}, false, stateFailed},
		{"failed: a repeat refused before sending (line 917)", client.RefusedRepeat{}, false, stateFailed},
		{"unknown: an outcome the client cannot know, exit 3 (line 925)", &client.Unknown{Why: "no answer", Key: "BWSYNTH-aaaaaaaaaaaaaaa"}, false, stateUnknown},
		{"unknown: a lapsed claim, exit 3 (line 925)", outcomeUnknown{plain}, true, stateUnknown},
		{"unknown: Ctrl-C in the step's wait, exit 130 (line 925)", &client.Interrupted{Route: "GET /operations/op_1"}, true, stateUnknown},
		{"unknown: a signal (line 925)", endedBySignal{syscall.SIGTERM}, true, stateUnknown},
		{"unknown: the server refused the read of the operation after an accepted write (lines 925-926)", &client.Problem{Status: 403}, true, stateUnknown},
		{"unknown: a read of the operation that gave no answer (line 926)", plain, true, stateUnknown},
		{"failed: an error before any write was accepted (line 916)", plain, false, stateFailed},
		{"unknown wins where a failed operation is joined with Ctrl-C (line 925)", errors.Join(operationFailed{plain}, &client.Interrupted{Route: "x"}), true, stateUnknown},
	} {
		if got := classify(c.err, c.accepted); got != c.want {
			t.Errorf("%s: %s; want %s", c.name, got, c.want)
		}
	}
}

// TestCompositeSettleKeepsErrorText: contract lines 944-945: the error is the line printed on standard
// error for the step, escaped; a done step has none, and a refused write has no answer (line 905).
func TestCompositeSettleKeepsErrorText(t *testing.T) {
	co := newCollector("mch_1", "drf_1")
	co.begin(1)
	co.settle(1, &client.Problem{Status: 409, Doc: map[string]any{"detail": "stale\x07\u0085"}})
	s := co.object().Steps[0]
	if s.State != stateFailed || s.Answer != nil || s.Error == "" || strings.ContainsAny(s.Error, "\x07\u0085") {
		t.Errorf("step 1 is %+v; want failed, no answer and an escaped error", s)
	}
}

// TestCompositeReleaselessPublication: contract lines 936-939 and 945-946: done without release or
// command, carrying its error, the plan step not-run with its <release> placeholder.
func TestCompositeReleaselessPublication(t *testing.T) {
	co := newCollector("mch_1", "drf_1")
	co.begin(2)
	co.record([]byte(`{"id":"op_1"}`), nil)
	co.settle(2, releaselessErr{errors.New("no release named")})
	obj := co.object()
	if s := obj.Steps[1]; s.State != stateDone || s.Release != "" || s.Command != "" || !strings.Contains(s.Error, "no release named") {
		t.Errorf("step 2 is %+v; want done with no release or command and the error that says no release was named", s)
	}
	if s := obj.Steps[2]; s.State != stateNotRun || !strings.Contains(s.Command, "<release>") {
		t.Errorf("step 3 is %+v; want not-run with the <release> placeholder", s)
	}
}

// TestCompositeUnreadableAnswer: contract lines 934-936 and 944: an answer that is not JSON is absent,
// the state stays that of the step, and error says the answer cannot be read.
func TestCompositeUnreadableAnswer(t *testing.T) {
	co := newCollector("mch_1", "drf_1")
	co.begin(1)
	co.record([]byte("<html>"), errors.New("the answer is not JSON"))
	co.settle(1, nil)
	s := co.object().Steps[0]
	if s.State != stateDone || s.Answer != nil || !strings.Contains(s.Error, "cannot be read") {
		t.Errorf("step 1 is %+v; want done, no answer and an error saying it cannot be read", s)
	}
}

// TestCompositeEscapesTheWholeObject: contract lines 963-965: -o json escapes DEL, C1 and bidirectional
// control characters in the whole object, a nested answer, the command and the error included.
func TestCompositeEscapesTheWholeObject(t *testing.T) {
	const bad = "\x7f\u0085‮"
	co := newCollector("mch_1", "drf_1")
	co.begin(1)
	co.record([]byte(`{"note":"a`+bad+`b","nested":{"deep":["`+bad+`"]}}`), nil)
	co.settle(1, errors.New("refused"+bad))
	co.command(1, "bronzewardctl import status ing"+bad)
	var out bytes.Buffer
	e := &env{output: "json"}
	e.out = tableGuard{e, &out, &out}
	if err := e.printComposite(co.object()); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), bad) {
		t.Errorf("printed a raw DEL, C1 or bidi character:\n%q", out.String())
	}
}

// TestCompositeOutputFailure: contract lines 956-958: an object that cannot be written is outputFailed,
// which report maps to exit 6 only where no more specific code stands.
func TestCompositeOutputFailure(t *testing.T) {
	e := &env{output: "json"}
	e.out = tableGuard{e, failsWith{syscall.ENOSPC}, failsWith{syscall.ENOSPC}}
	err := e.printComposite(settled(nil).object())
	var lost outputFailed
	if !errors.As(err, &lost) || !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("printed with %v; want an outputFailed that wraps ENOSPC", err)
	}
	var out, errb bytes.Buffer
	if code := report(err, &out, &errb); code != 6 {
		t.Errorf("exit %d; want 6 in place of 0", code)
	}
	if code := report(errors.Join(operationFailed{errors.New("plan failed")}, err), &out, &errb); code != 4 {
		t.Errorf("exit %d; want the step's 4 to stand over 6", code)
	}
}

// TestWriteWithCollectsAnswer: contract lines 931-934, 947: while a composite collects, a write prints
// nothing and keeps its answer as served; one that is not JSON prints its note on standard error and
// leaves the answer out. Standard output stays empty in -o json and yaml.
func TestWriteWithCollectsAnswer(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-1")
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/server":
			fmt.Fprint(w, `{"version":"BWSYNTH","apiRevision":2,"startMode":"normal"}`)
		case "GET /api/v1/identity":
			fmt.Fprint(w, `{"kind":"service","principal":"svc_BWSYNTH"}`)
		case "POST /api/v1/clusters":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, body)
		}
	}))
	defer srv.Close()
	automationContext(t, srv.URL)
	for _, o := range []string{"json", "yaml"} {
		for _, c := range []struct {
			name, body string
			answer     bool
			note       string
		}{
			{"JSON the command reads", `{"id":"cls_BWSYNTH","name":"lab-a","endpoint":"e","contract":"c","talosClusterId":"t"}`, true, ""},
			{"JSON the command cannot read", `{"id":["BWSYNTH"]}`, true, "its answer cannot be read"},
			{"not JSON", `<html>`, false, "its answer cannot be read"},
		} {
			body = c.body
			var out, errb bytes.Buffer
			e := &env{output: o, err: &errb}
			e.out = tableGuard{e, &out, &out}
			e.collect = newCollector("mch_1", "drf_1")
			e.collect.begin(1)
			if err := e.clusterAdd([]string{"--name", "lab-a"}); err != nil {
				t.Fatalf("-o %s, %s: %v", o, c.name, err)
			}
			s := e.collect.obj.Steps[0]
			if out.Len() != 0 {
				t.Errorf("-o %s, %s: wrote %q to standard output while collecting", o, c.name, out.String())
			}
			if (len(s.Answer) != 0) != c.answer || c.answer && string(s.Answer) != c.body {
				t.Errorf("-o %s, %s: answer %q; want the body as served: %v", o, c.name, s.Answer, c.answer)
			}
			if c.note != "" != strings.Contains(errb.String(), "succeeded, but its answer cannot be read") || c.note != "" && !strings.Contains(s.Error, "cannot be read") {
				t.Errorf("-o %s, %s: stderr %q, error %q; want the note %q beside the object", o, c.name, errb.String(), s.Error, c.note)
			}
		}
	}
}

// TestCompositeDoneStepKeepsItsError: contract lines 945-946: a done step whose result could not be read
// after its operation ended well, here a client failure marked afterEnd, carries that error.
func TestCompositeDoneStepKeepsItsError(t *testing.T) {
	co := newCollector("mch_1", "drf_1")
	co.begin(1)
	co.record([]byte(`{"ingestion":"ing_1"}`), nil)
	co.settle(1, afterEnd{errors.New("the draft cannot be read again")})
	if s := co.object().Steps[0]; s.State != stateDone || !strings.Contains(s.Error, "cannot be read again") {
		t.Errorf("step 1 is %+v; want done with the error of the read after it", s)
	}
}

// TestJoinSave: contract lines 924-929 keep a finished import done when the client cannot save its
// context after follow: the save error is afterEnd and classifies done. An error follow ended with stays
// the step's. This tests the seam follow uses; the real command reaches it only through a save failing
// inside follow, which the tests of the command cannot cause without ending the follow too.
func TestJoinSave(t *testing.T) {
	boom := errors.New("keeping the epoch: permission denied")
	if err := joinSave(nil, nil); err != nil {
		t.Errorf("joinSave(nil, nil) = %v; want nil", err)
	}
	if got := classify(joinSave(nil, boom), true); got != stateDone {
		t.Errorf("a Save error after a finished import classifies %v; want done", got)
	}
	if got := classify(joinSave(operationFailed{errors.New("import failed")}, boom), true); got != stateFailed {
		t.Errorf("a failed import with a Save error classifies %v; want failed", got)
	}
}

// TestCompositeKeepsTheUnreadableAnswerNote: contract lines 936-937 and 945-946: a publication answered
// with a body that cannot be read, and then ended by a later error, keeps the note that its answer
// cannot be read beside that error, on a done step as on a failed one.
func TestCompositeKeepsTheUnreadableAnswerNote(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want stepState
	}{
		{"failed", errors.New("accepted without naming its operation"), stateUnknown},
		{"releaseless", releaselessErr{errors.New("accepted without naming its operation")}, stateDone},
	} {
		co := newCollector("mch_1", "drf_1")
		co.begin(2)
		co.record([]byte("<html>"), errors.New("the answer is not JSON"))
		co.settle(2, c.err)
		s := co.object().Steps[1]
		if s.State != c.want || !strings.Contains(s.Error, "its answer cannot be read") || !strings.Contains(s.Error, "accepted without naming its operation") {
			t.Errorf("%s: step 2 is %+v; want state %v and both the note that its answer cannot be read and the later error", c.name, s, c.want)
		}
	}
}

// TestJoinSaveKeepsThePauseReadable: a Save error after a follow that ended well or paused is one the
// drift adopt command recognizes, so that it reads the import again before it settles step 1.
func TestJoinSaveKeepsThePauseReadable(t *testing.T) {
	var lost saveFailed
	if err := joinSave(nil, errors.New("keeping the epoch: permission denied")); !errors.As(err, &lost) {
		t.Errorf("joinSave(nil, err) = %v; want a saveFailed", err)
	}
	if err := joinSave(operationFailed{errors.New("import failed")}, errors.New("keeping the epoch")); errors.As(err, &lost) {
		t.Errorf("joinSave(an import error, err) = %v; want that error to stay plain", err)
	}
}
