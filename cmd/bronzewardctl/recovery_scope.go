package main

import (
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/url"
	"strings"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// The two acts of T35 on one scope (#362; operator-experience.md §5.9): recovery mark records the
// state the evidence supports, and recovery release releases a ready scope. The state is the
// server's, never the request's. The contract names no typed answer for either (gap G7).

// missingMeaning is what each item of a mark's `missing` list says and what to do about it (gap G8).
var missingMeaning = map[string]string{
	"observation": "the restoration observation does not support the scope: it read no identity, does not follow the accounting, an operation changed after it, or it used another access version",
	"identity":    "the identity the node showed is not the one the machine record binds, or a later observation showed another",
	"access":      "the cluster's Talos access failed the dependency check",
	"artifact":    "the Desired release's artifact could not be decrypted for the machine; a lost key version leaves through a newly published release",
}

// scopeNext adds the next step of a refusal of a mark or a release to err (gap G9: a refusal with
// code conflict has no shared text).
func scopeNext(err error, machine string) error {
	var p *client.Problem
	if !errors.As(err, &p) {
		return err
	}
	m := client.ShellWord(machine)
	detail, _ := p.Doc["detail"].(string)
	var next string
	switch {
	case p.Code() == "recovery-mode-active" && p.Doc["scope"] == "pre-restore-unaccounted":
		next = "Next: account the scope first: recovery scope-account " + m + "."
	case p.Code() != "conflict":
		return err
	case strings.Contains(detail, "only a ready scope is released"):
		next = "Next: mark the scope until it is ready: recovery mark " + m + ". A release is not an approval."
	case strings.Contains(detail, "Desired release changed"):
		next = "Next: mark the scope again: recovery mark " + m + "."
	case strings.Contains(detail, "is not marked"):
		next = "Next: recovery status names the state and the next step of the scope."
	default:
		return err
	}
	return &client.Blocked{Err: err, More: []string{next}}
}

// recoveryMark is T35's mark (POST /recovery/scopes/{machine}/marks, recovery-admin, human only).
func (e *env) recoveryMark(args []string) error {
	var facts factsFlag
	var rf repeatFlags
	pos, err := e.parse("recovery mark", args, 1, func(fs *flag.FlagSet) {
		fs.Var(&facts, "fact", "")
		rf.flags(fs)
	})
	if err != nil {
		return err
	}
	machine := pos[0]
	rp, err := rf.repeat(false)
	if err != nil {
		return err
	}
	in := apitypes.ScopeMarkRequest{}
	if facts != nil {
		in.Facts = (*map[string]string)(&facts)
	}
	body, _ := json.Marshal(in)
	err = e.write(&client.Write{Method: http.MethodPost, Path: "/recovery/scopes/" + url.PathEscape(machine) + "/marks", Body: body, HumanOnly: true,
		Repeat: rp, Check: "machine timeline " + client.ShellWord(machine), Read: newestEntry(machine)}, func(_ *client.Client, resp *client.Response) ([]string, error) {
		var x apitypes.ScopeMarkBody
		err := json.Unmarshal(resp.Body, &x)
		lines := []string{sf("Scope %s marked %s, was %s (act %s). Observation %s.", x.Machine, x.State, x.From, x.Act, x.Observation)}
		for _, m := range x.Missing {
			meaning, ok := missingMeaning[m]
			if !ok {
				meaning = "the server named it and the client has no text for it"
			}
			lines = append(lines, sf("  missing %s: %s", m, meaning))
		}
		switch x.State {
		case "ready":
			lines = append(lines, "Next: recovery release "+client.ShellWord(x.Machine)+".")
		case "unresolved":
			lines = append(lines, "A restored operation still holds the scope. Next: recovery status.")
		default:
			lines = append(lines, "Next: fix what is missing, then recovery mark "+client.ShellWord(x.Machine)+" again.")
		}
		return lines, err
	})
	return scopeNext(err, machine)
}

// recoveryRelease is T35's release (POST /recovery/scopes/{machine}/releases, recovery-admin, human
// only). It is not an approval and sends nothing.
func (e *env) recoveryRelease(args []string) error {
	var facts factsFlag
	var rf repeatFlags
	pos, err := e.parse("recovery release", args, 1, func(fs *flag.FlagSet) {
		fs.Var(&facts, "fact", "")
		rf.flags(fs)
	})
	if err != nil {
		return err
	}
	machine := pos[0]
	rp, err := rf.repeat(false)
	if err != nil {
		return err
	}
	in := apitypes.ScopeReleaseRequest{}
	if facts != nil {
		in.Facts = (*map[string]string)(&facts)
	}
	body, _ := json.Marshal(in)
	err = e.write(&client.Write{Method: http.MethodPost, Path: "/recovery/scopes/" + url.PathEscape(machine) + "/releases", Body: body, HumanOnly: true,
		Repeat: rp, Check: "machine timeline " + client.ShellWord(machine), Read: newestEntry(machine)}, func(_ *client.Client, resp *client.Response) ([]string, error) {
		var x apitypes.ScopeReleaseBody
		err := json.Unmarshal(resp.Body, &x)
		return []string{sf("Scope %s released, was %s (act %s).", x.Machine, x.From, x.Act),
			"A release is not an approval: it approves no plan and sends nothing. A plan still needs its own approval.",
			"Next: recovery status."}, err
	})
	return scopeNext(err, machine)
}
