package main

import (
	"regexp"

	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// What an operation did, and what comes next, in words (client contracts A10, ginsys/bronzeward#1014): one
// sentence for each kind and state the server can produce, and the step an operator can take from it. The
// table is the web client's table (web/src/operation-words.ts), word for word, and a test reads that file
// and compares every pair. A kind or state it does not know is printed as the server wrote it, with no
// next step. The outcome facts a state carries (its cause, its attempts, the applied release) stay with
// `outcome`, so a sentence here adds what the state means and never repeats them.

type operationText struct{ now, next string }

var operationWords = map[string]map[string]operationText{
	"publish": {
		"queued":  {"Accepted; no worker has taken it yet.", "Wait; nothing to do."},
		"running": {"A worker is compiling the draft and writing the release.", "Wait; nothing to do."},
		"succeeded": {
			"The draft became an immutable release. Nothing was sent to any machine.",
			"Create a plan for each machine (publisher): an adopt plan where it has no Applied configuration or has open drift to take over, which sends nothing; an apply plan otherwise, or to revert open drift, which sends the release.",
		},
		"failed": {"The draft was not published; no release was made.", "Fix the cause named above and publish again; a publish is safe to repeat."},
	},
	"ingest": {
		"running": {
			"The import is reading its source into the draft, waits for review, or has stalled with its runner gone.",
			"Open the import to see which; an author reviews a paused one and takes a stalled one over or abandons it.",
		},
		"succeeded": {"The imported configuration is stored in the draft. Nothing was written to any machine.", "Publish the draft (publisher)."},
		"failed":    {"No configuration was stored from the import's source, and nothing was written to any machine.", "Start the import again (author)."},
	},
	"apply-config": {
		"committed": {"The controller committed the plan and holds the machine; no request is sent yet.", "Wait for the controller."},
		"sending":   {"A request is in flight, or its outcome is unknown.", "Wait for the controller."},
		"verifying": {"The node accepted the request; the controller is checking the result.", "Wait for the controller."},
		"completed": {"The machine's Applied configuration is updated.", "None needed."},
		"rejected":  {"The machine was not changed.", "Correct the release and create a new plan."},
		"failed": {
			"The machine may have been changed wrongly or not changed at all; its Applied configuration was not updated.",
			"Read the machine's drift and observations, then create a new plan.",
		},
		// No attempt is recorded; after a restore that is no proof that nothing was sent (execution and recovery 3).
		"cancelled": {
			"No attempt is recorded for it, which is not proof that nothing was sent.",
			"Read the machine's Observed configuration, then create a new plan if the change is still wanted.",
		},
		// The next step of an unresolved operation is the Recovery text `outcome` carries.
		"unresolved": {"The controller keeps examining it and may settle it on its own.", ""},
	},
	"adopt": {
		"completed": {
			"The machine's running configuration is recorded as Applied, by observation. Nothing was sent.",
			"None needed; the first apply plan afterwards carries a whole-configuration diff, or says it is withheld.",
		},
	},
}

func operationRow(kind, state string) (operationText, bool) {
	t, ok := operationWords[kind][state]
	return t, ok
}

var vowelStart = regexp.MustCompile(`^[aeiou]`)

// operationNow is the sentence for the kind and state, or for a pair the table does not know the raw values.
func operationNow(kind, state string) string {
	if t, ok := operationRow(kind, state); ok {
		return t.now
	}
	what := "An operation of kind " + kind
	if _, known := operationWords[kind]; known {
		what = map[bool]string{true: "An ", false: "A "}[vowelStart.MatchString(kind)] + kind + " operation"
	}
	return what + " in state " + state + "; this command has no description of it."
}

// operationNextStep is the next step for the kind and state, "" for an unknown pair and for an unresolved one.
func operationNextStep(kind, state string) string {
	t, _ := operationRow(kind, state)
	return t.next
}

// endsWithItsOwnNext is whether outcome's lines already carry the next step of a state: an operation that
// ended without success, or one left unresolved. The table's next step is not printed again beside it.
func endsWithItsOwnNext(state string) bool {
	switch state {
	case "failed", "rejected", "cancelled", "unresolved":
		return true
	}
	return false
}

// startedBy is who started an operation (A10): the principal that made the request, or for an apply or an
// adoption, which the controller creates itself at the commitment of an approved plan, the controller. A
// long answer adds the plan it committed and who created that plan, where the plan's read gave one; the
// short answer, a list cell, names the controller alone. A null createdBy on another kind, which the server
// never writes, is "none".
func (e *env) startedBy(o *apitypes.OperationBody, long bool) string {
	if o.CreatedBy != nil {
		return e.who(o.CreatedBy.Principal) + " (" + o.CreatedBy.Role + ")"
	}
	if o.Kind != "apply-config" && o.Kind != "adopt" {
		return "none"
	}
	if !long || o.Subject == nil || o.Subject.Plan == nil {
		return "the controller"
	}
	by := "the controller, committing plan " + *o.Subject.Plan
	if o.Kind == "adopt" {
		by = "the controller, adopting under plan " + *o.Subject.Plan
	}
	if p := heldBody[apitypes.PlanBody](e, titlePath("plans", *o.Subject.Plan)); p != nil {
		by += " (created by " + e.who(p.CreatedBy.Principal) + " (" + p.CreatedBy.Role + "))"
	}
	return by
}
