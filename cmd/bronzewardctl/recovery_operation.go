package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// The two acts of T34 on one operation (#362; operator-experience.md §5.9): recovery takeover makes
// the serving controller the owner of a non-terminal operation, and recovery resolve ends an
// unresolved operation that has no attempt as cancelled. Neither sends anything to a node. The
// contract names no typed answer for either, so none is asked (the reason is the record).

// operationNext adds the next step of a refusal of one of the two acts to err.
func operationNext(err error, operation string) error {
	var p *client.Problem
	if !errors.As(err, &p) || p.Code() != "conflict" {
		return err
	}
	detail, _ := p.Doc["detail"].(string)
	op := client.ShellWord(operation)
	switch {
	case strings.Contains(detail, "owner generation is not"):
		// The refusal carries no new generation (G10): the operation must be read again.
		return &client.Blocked{Err: err, More: []string{"The owner generation moved after it was read. Next: operation show " + op + ", then repeat this command."}}
	case strings.Contains(detail, "an attempt is recorded"):
		return &client.Blocked{Err: err, More: []string{"Next: an operation with an attempt is classified after the normal restart, or its attempts are accounted: operation show " + op + "."}}
	case strings.Contains(detail, "the operation is "):
		return &client.Blocked{Err: err, More: []string{"Next: operation show " + op + " names its state."}}
	}
	return err
}

// ownerGeneration is the highest owner generation the operation's events and attempts show (gap
// G2): the operation body carries none, and the server refuses a generation that is not current.
// An operation committed by a controller holds generation 1 until a takeover moves it.
func ownerGeneration(c *client.Client, operation string, op apitypes.OperationBody) (int, error) {
	items, err := c.List("/operations/" + url.PathEscape(operation) + "/events")
	if err != nil {
		return 0, err
	}
	gen := 1
	for _, a := range deref0(op.Attempts) {
		gen = max(gen, a.Generation)
	}
	for _, raw := range items {
		var ev lastEvent
		var data struct {
			Generation int `json:"generation"`
		}
		if json.Unmarshal(raw, &ev) == nil && json.Unmarshal(ev.Data, &data) == nil {
			gen = max(gen, data.Generation)
		}
	}
	return gen, nil
}

// readOperation is GET /operations/{id}, decoded.
// It also answers the epoch of the answer and the move count as that answer left it, which a write
// built from the operation binds to (§8.5).
func readOperation(c *client.Client, operation string) (apitypes.OperationBody, string, int, error) {
	r, err := c.Get("/operations/" + url.PathEscape(operation))
	if err != nil {
		return apitypes.OperationBody{}, "", 0, err
	}
	ops, err := decode[apitypes.OperationBody](c, r.Body)
	if err != nil {
		return apitypes.OperationBody{}, "", 0, err
	}
	return ops[0], r.Header.Get("Bronzeward-Epoch"), r.Moves, nil
}

// operationState is the read for the block of an unknown outcome.
func operationState(operation string) func(*client.Client) (string, error) {
	return func(c *client.Client) (string, error) {
		op, _, _, err := readOperation(c, operation)
		return sf("GET /api/v1/operations/%s shows the state %s", operation, op.State), err
	}
}

// recoveryTakeover is T34's takeover (POST /operations/{id}/takeovers, recovery-admin, human only).
func (e *env) recoveryTakeover(args []string) error {
	var reason string
	var rf repeatFlags
	pos, err := e.parse("recovery takeover", args, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&reason, "reason", "", "")
		rf.flags(fs)
	})
	if err != nil {
		return err
	}
	operation := pos[0]
	if strings.TrimSpace(reason) == "" {
		return usageError{fmt.Errorf("give --reason")}
	}
	rp, err := rf.repeat(false)
	if err != nil {
		return err
	}
	w := &client.Write{Method: http.MethodPost, Path: "/operations/" + url.PathEscape(operation) + "/takeovers", HumanOnly: true, Repeat: rp,
		Check: "operation show " + client.ShellWord(operation), Read: operationState(operation)}
	prep := func(c *client.Client) error {
		op, epoch, moves, err := readOperation(c, operation)
		if err != nil {
			return err
		}
		if client.Terminal(op.State) {
			return ef("operation %s is %s: a finished operation is not taken over; nothing was sent", operation, op.State)
		}
		gen, err := ownerGeneration(c, operation, op)
		if err != nil {
			return err
		}
		body, _ := json.Marshal(apitypes.TakeoverRequest{Generation: gen, Reason: reason})
		w.Body = body
		// The body is built from the operation answer and the events that follow it. The send is bound to the
		// epoch and move count of the first answer, so an epoch that moved during the later reads, even
		// one that came back, refuses it (§8.5, §8.6 item 3).
		w.ReadsAt(epoch, moves)
		tf(e.err, "Takeover of operation %s (%s) in %s, read at owner generation %d\n  reason: %s\n", operation, op.State, c.Context.Where(), gen, client.Printable(reason))
		tf(e.err, "  The serving controller becomes the owner at generation %d and the operation becomes unresolved.\n  It sends nothing to the node and classifies nothing.\n", gen+1)
		return nil
	}
	err = e.writeAfter(prep, w, func(_ *client.Client, resp *client.Response) ([]string, error) {
		var x apitypes.TakeoverBody
		err := json.Unmarshal(resp.Body, &x)
		return []string{sf("Operation %s taken over (act %s): %s at generation %d.", x.Operation, x.Act, x.State, x.Generation),
			sf("Recorded by %s (%s).", x.RequestedBy, x.Role),
			"Next: recovery account, or recovery resolve if the operation has no attempt."}, err
	})
	return operationNext(err, operation)
}

// recoveryResolve is T34's resolution (POST /operations/{id}/resolutions, recovery-admin, human
// only). The only resolution an operator records is cancelled, and only for an unresolved
// operation that recorded no attempt.
func (e *env) recoveryResolve(args []string) error {
	var reason string
	var rf repeatFlags
	pos, err := e.parse("recovery resolve", args, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&reason, "reason", "", "")
		rf.flags(fs)
	})
	if err != nil {
		return err
	}
	operation := pos[0]
	if strings.TrimSpace(reason) == "" {
		return usageError{fmt.Errorf("give --reason")}
	}
	rp, err := rf.repeat(false)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(apitypes.ResolutionRequest{To: apitypes.ResolutionRequestToCancelled, Reason: reason})
	w := &client.Write{Method: http.MethodPost, Path: "/operations/" + url.PathEscape(operation) + "/resolutions", Body: body, HumanOnly: true,
		Repeat: rp, Check: "operation show " + client.ShellWord(operation), Read: operationState(operation)}
	prep := func(c *client.Client) error {
		op, epoch, moves, err := readOperation(c, operation)
		if err != nil {
			return err
		}
		if op.State != "unresolved" {
			return ef("operation %s is %s: only an unresolved operation is resolved; operation show %s; nothing was sent", operation, op.State, client.ShellWord(operation))
		}
		if len(deref0(op.Attempts)) > 0 {
			return ef("operation %s has an attempt: it is classified after the normal restart, or its attempts are accounted (recovery account); nothing was sent", operation)
		}
		// The checks above read the operation, so the send is bound to the epoch and move count of that answer (§8.5).
		w.ReadsAt(epoch, moves)
		tf(e.err, "Resolution of operation %s in %s\n  reason: %s\n", operation, c.Context.Where(), client.Printable(reason))
		tf(e.err, "  The operation ends cancelled. It records no remote outcome: no attempt was sent, and nothing is sent now.\n")
		return nil
	}
	err = e.writeAfter(prep, w, func(_ *client.Client, resp *client.Response) ([]string, error) {
		var x apitypes.ResolutionBody
		err := json.Unmarshal(resp.Body, &x)
		return []string{sf("Operation %s resolved (act %s): %s.", x.Operation, x.Act, x.State),
			sf("Recorded by %s (%s). It records no remote outcome.", x.ResolvedBy, x.Role),
			"Next: recovery status names the scope's next step."}, err
	})
	return operationNext(err, operation)
}
