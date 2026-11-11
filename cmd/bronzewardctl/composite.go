package main

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/ginsys/bronzeward/internal/client"
)

// The structured output of drift adopt (operator-experience.md §5.8, "Structured output of a
// composite"): with -o json or -o yaml the command prints one object, once, when its last step has
// settled. The steps are collected here, as each step's write answers, and printed by one place.

// stepState is a step's state in the object: the five of operator-experience.md §5.8.
type stepState string

const (
	stateDone   stepState = "done"
	statePaused stepState = "paused"
	stateNotRun stepState = "not-run"
	stateFailed stepState = "failed"
	// stateUnknown is a write that was accepted or may have been, with an outcome that is not known.
	stateUnknown stepState = "unknown"
)

// adoptStepNames and adoptStepRoles are the three steps in order: the import, the publication and the
// plan create, each with the role it needs.
var (
	adoptStepNames = [3]string{"import", "publish", "plan"}
	adoptStepRoles = [3]string{"author", "publisher", "publisher"}
)

// compositeStep is a step of the object. The members and their order are the contract's: answer is the
// server's answer as served, release is on a done publication, command is on every step that is not
// done and error is on failed, unknown and any step whose answer cannot be read.
type compositeStep struct {
	Step    int             `json:"step"`
	Name    string          `json:"name"`
	Role    string          `json:"role"`
	State   stepState       `json:"state"`
	Answer  json.RawMessage `json:"answer,omitempty"`
	Release string          `json:"release,omitempty"`
	Command string          `json:"command,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// adoption is the object drift adopt prints: the machine, the draft and always the three steps.
type adoption struct {
	Machine string          `json:"machine"`
	Draft   string          `json:"draft"`
	Steps   []compositeStep `json:"steps"`
}

// resumeFailedErr marks the error of a step 1 run that recorded resume-failed, the one failed step
// whose exit is 1 and whose command is import status (operator-experience.md §5.8, the failed state).
// The import code wraps its resume-failed error in it, with the ingestion that import status reads.
type resumeFailedErr struct {
	error
	ingestion string
}

func (e resumeFailedErr) Unwrap() error { return e.error }

// releaselessErr marks a publication that succeeded without a readable release: the step is done
// without release, and the plan step is not-run, with the exit 1 of the text output.
type releaselessErr struct{ error }

func (e releaselessErr) Unwrap() error { return e.error }

// afterEnd marks an error of the client after a step's operation ended well, such as the read of the
// draft again after an import: it changes no state, and the exit and the error stay those of the text
// output (operator-experience.md §5.8, the exit codes).
type afterEnd struct{ error }

func (e afterEnd) Unwrap() error { return e.error }

// classify is the state a step's error gives it, by the type of the error alone. accepted says that the
// server answered the step's write with success, which tells a refused write from a refused later read.
//
//   - A publication without a release, and a client error after the operation ended well, are done;
//     the caller leaves release out and stops after the first.
//   - Ctrl-C, a signal, an unknown outcome and a lapsed claim leave the outcome unknown.
//   - An operation that ended failed, rejected or cancelled, a resume-failed run, and a write the
//     server or the client refused before it was sent did nothing that stands: failed.
//   - Any other error follows accepted: after an accepted write, a read that failed or was refused
//     leaves the outcome unknown (exit 1 or 3); with no accepted write, nothing was sent that stands.
func classify(err error, accepted bool) stepState {
	var (
		releaseless releaselessErr
		after       afterEnd
		resume      resumeFailedErr
		unsent      client.NotSent
		ended       operationFailed
		refusedHere notSent
		refusedRep  client.RefusedRepeat
		unknown     *client.Unknown
		settled     outcomeUnknown
		cut         *client.Interrupted
		stopped     interrupted
		signalled   endedBySignal
	)
	switch {
	case err == nil, errors.As(err, &releaseless), errors.As(err, &after):
		return stateDone
	case errors.As(err, &unknown), errors.As(err, &settled), errors.As(err, &cut), errors.As(err, &stopped), errors.As(err, &signalled):
		return stateUnknown
	// client.NotSent is the client's refusal, before it sent anything, for a missing kept ETag, a changed
	// epoch or a moved count (operator-experience.md §5.8, the failed state).
	case errors.As(err, &ended), errors.As(err, &resume), errors.As(err, &refusedHere), errors.As(err, &refusedRep), errors.As(err, &unsent):
		return stateFailed
	case accepted:
		return stateUnknown
	}
	return stateFailed
}

// collector gathers the steps of one drift adopt run. A step is begun, its write's answer is recorded
// as it arrives (by writeWith), and it is settled with the error the step ended with.
type collector struct {
	obj      adoption
	cur      int // the step whose write is being answered; 0 before the first
	accepted [3]bool
}

// newCollector is the object before any step ran: every step is not-run, with the command that runs it.
func newCollector(machine, draft string) *collector {
	co := &collector{obj: adoption{Machine: machine, Draft: draft}}
	for i := range adoptStepNames {
		co.obj.Steps = append(co.obj.Steps, compositeStep{Step: i + 1, Name: adoptStepNames[i], Role: adoptStepRoles[i], State: stateNotRun})
	}
	return co
}

// begin says that step n (1 to 3) is the one whose write answers next.
func (co *collector) begin(n int) { co.cur = n }

// record keeps the answer of the current step's write. The write succeeded, so the step is accepted. The
// answer is kept as served when the body is JSON and the caller read it (cause nil); a body that is not
// JSON, or that the command could not read, leaves the step's answer out where it cannot be nested, and
// error says that it cannot be read. The state stays that of the step.
func (co *collector) record(body []byte, cause error) {
	if co.cur < 1 || co.cur > 3 {
		return
	}
	s := &co.obj.Steps[co.cur-1]
	co.accepted[co.cur-1] = true
	if json.Valid(body) {
		s.Answer = json.RawMessage(body)
	}
	if cause != nil {
		s.Error = "its answer cannot be read: " + client.Shown(cause)
	}
}

// settle ends step n with err: its state follows classify, and the line the command prints on standard
// error for it, escaped as every such line is, becomes its error, where the step did not finish well.
func (co *collector) settle(n int, err error) {
	s := &co.obj.Steps[n-1]
	s.State = classify(err, co.accepted[n-1])
	if err != nil && s.State != stateDone {
		s.Error = client.Shown(err)
	}
	// A run that recorded resume-failed is read with import status, not restarted (§5.8, the failed state).
	var resume resumeFailedErr
	if errors.As(err, &resume) {
		s.Command = "bronzewardctl import status " + client.ShellWord(resume.ingestion)
	}
}

// pause marks step 1 as paused for review: the draft has not changed, and its command is import mark.
func (co *collector) pause(ingestion string) {
	co.obj.Steps[0].State = statePaused
	co.obj.Steps[0].Command = "bronzewardctl import mark " + client.ShellWord(ingestion)
}

// release names the release a done publication made; the plan step's command carries it from then on.
func (co *collector) release(r string) {
	co.obj.Steps[1].Release = r
	co.obj.Steps[2].Command = strings.TrimSpace(adoptStep(3, co.obj.Machine, co.obj.Draft, r)[1])
}

// command replaces step n's command, for the cases where it is not the one the table output names first,
// such as import status after a resume-failed run.
func (co *collector) command(n int, c string) { co.obj.Steps[n-1].Command = c }

// object is the object to print: a step that is not done has a command, the default one unless it was
// set, and a done step has none.
func (co *collector) object() adoption {
	out := co.obj
	out.Steps = append([]compositeStep(nil), co.obj.Steps...)
	for i := range out.Steps {
		s := &out.Steps[i]
		switch {
		case s.State == stateDone:
			s.Command = ""
		case s.Command == "":
			s.Command = strings.TrimSpace(adoptStep(i+1, out.Machine, out.Draft, "")[1])
		}
	}
	return out
}
