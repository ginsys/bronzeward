package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// A recovery-admin's acts on one apply-config operation (persistence-api.md §9.2; execution-recovery.md
// §3.4, §4, §5.2): a takeover request, an accounting decision and a resolution. Each is T11 (§5): the
// key lock, the installation state FOR SHARE, then as the effect's locks the operation's machine FOR
// UPDATE, the deciding identity's principal FOR SHARE, not revoked (rule 2), and the operation FOR
// UPDATE. Every one is taken before the act-order lock, the transaction's last (rule 5): a request
// waiting on an operation read while holding it would stall every other act. The writes run once the
// act-order lock is held, so the entry's time follows every lock wait (rule 4).

func operationTakeover() effectRoute {
	return effectRoute{action: "operation.takeover", input: func() input { return &takeoverRequest{} }, effect: requestTakeover}
}

func accountingDecisionRoute() effectRoute {
	return effectRoute{action: "attempt.account", input: func() input { return &accountingRequest{} }, effect: decideAccounting}
}

func operationResolution() effectRoute {
	return effectRoute{action: "operation.resolve", input: func() input { return &resolutionRequest{} }, effect: resolveByDecision}
}

// operationMachine reads an apply-config operation's machine. An operation's machine never changes,
// so it is read before the machine's lock.
func operationMachine(ctx context.Context, tx *sql.Tx, operation string) (string, error) {
	if id.MustHave(operation, id.Operation) != nil {
		return "", refuse(http.StatusNotFound, "not-found", "no such operation")
	}
	var machine string
	err := tx.QueryRowContext(ctx, `SELECT machine FROM operation WHERE id = $1 AND kind = 'apply-config'`, operation).Scan(&machine)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", refuse(http.StatusNotFound, "not-found", "no such operation").with("operation", operation)
	case err != nil:
		return "", err
	}
	return machine, nil
}

// lockRecoveryAct takes the machine FOR UPDATE, the requesting identity's principal FOR SHARE and
// then the operation FOR UPDATE, answering the operation's lock; takeover is lockOperationAs's.
func (a *API) lockRecoveryAct(ctx context.Context, tx *sql.Tx, q *request, operation, machine string, takeover bool) (operationLock, error) {
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM machine WHERE id = $1 FOR UPDATE`, machine); err != nil {
		return operationLock{}, err
	}
	var revoked bool
	if err := tx.QueryRowContext(ctx, `SELECT revoked FROM principal WHERE id = $1 FOR SHARE`, q.principal.ID).Scan(&revoked); err != nil {
		return operationLock{}, err
	}
	if revoked {
		return operationLock{}, refuse(http.StatusForbidden, "identity-revoked", "")
	}
	return a.lockOperationAs(ctx, tx, operation, takeover)
}

// conflictOf answers a transaction's refusal as 409 conflict naming the transaction.
func conflictOf(err error) error {
	var r *outcomeRefused
	if errors.As(err, &r) {
		return refuse(http.StatusConflict, "conflict", r.Cause).with("transaction", r.Transaction)
	}
	return err
}

// takeoverRequest names the owner generation the requester read (an operation's `generation`): the
// takeover happens only if it is still current, as the controller's own does (§3.4).
type takeoverRequest struct {
	Generation int64  `json:"generation"`
	Reason     string `json:"reason"`
}

func (in *takeoverRequest) check(*API) error {
	if in.Generation < 1 {
		return errors.New("generation must be the operation's owner generation, at least 1")
	}
	return text("reason", in.Reason, maxReason)
}

// takeoverBody is the takeover's answer: the generation and state the operation now holds and the
// ownership transition entry's revision.
type takeoverBody struct {
	Operation   string    `json:"operation"`
	State       string    `json:"state"`
	Generation  int64     `json:"generation"`
	Revision    int64     `json:"revision"`
	RequestedBy string    `json:"requestedBy"`
	Role        string    `json:"role"`
	Reason      string    `json:"reason"`
	Act         string    `json:"act"`
	Epoch       string    `json:"epoch"`
	At          time.Time `json:"at"`
}

// requestTakeover is a recovery-admin's explicit takeover request (execution-recovery.md §3.4): the
// serving controller becomes the owner at the next generation, as at its own start, naming the
// requester in the ownership transition entry. It sends nothing and classifies nothing.
func requestTakeover(ctx context.Context, a *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*takeoverRequest)
	operation := q.r.PathValue("id")
	machine, err := operationMachine(ctx, tx, operation)
	if err != nil {
		return result{}, err
	}
	if _, err := a.lockRecoveryAct(ctx, tx, q, operation, machine, true); err != nil {
		return result{}, err
	}
	b := &takeoverBody{Operation: operation, RequestedBy: q.principal.ID, Role: string(q.role), Reason: in.Reason, Act: q.actID,
		Epoch: q.epoch}
	write := func(ctx context.Context, tx *sql.Tx) error {
		r, err := a.takeOverTx(ctx, tx, operation, in.Generation, in.Reason,
			map[string]any{"requestedBy": b.RequestedBy, "role": b.Role, "act": b.Act})
		if err != nil {
			return conflictOf(err)
		}
		b.State, b.Generation, b.Revision, b.At = r.State, r.Generation, r.Revision, r.At
		return nil
	}
	return result{status: http.StatusCreated, body: b, subjects: []string{operation, machine}, atActOrder: write}, nil
}

// accountingRequest names the observation read at the start of the settle and the recovery
// observation that ends it (§5.2), and the facts the decision holds: how a change of the
// machine-configuration resource version is explained is `explanation`. The decision's own facts
// (judgement, the settle floor, its anchor and both versions) are the server's and override these.
type accountingRequest struct {
	Start string            `json:"start"`
	End   string            `json:"end"`
	Facts map[string]string `json:"facts"`
}

const maxFacts = 16

func (in *accountingRequest) check(*API) error {
	if id.MustHave(in.Start, id.Observation) != nil || id.MustHave(in.End, id.Observation) != nil {
		return errors.New("start and end must be observation identifiers")
	}
	return checkFacts(in.Facts)
}

// checkFacts bounds a decision's facts: at most maxFacts members, each a short name and a nonblank
// text.
func checkFacts(facts map[string]string) error {
	if len(facts) > maxFacts {
		return fmt.Errorf("facts holds at most %d members", maxFacts)
	}
	for k, v := range facts {
		if err := text("a facts member name", k, 64); err != nil {
			return err
		}
		if err := text("facts."+k, v, maxReason); err != nil {
			return err
		}
	}
	return nil
}

// accountingBody is the decision's answer: its accounting entry's revision and the decider.
type accountingBody struct {
	Operation string    `json:"operation"`
	Attempt   string    `json:"attempt"`
	Revision  int64     `json:"revision"`
	DecidedBy string    `json:"decidedBy"`
	Role      string    `json:"role"`
	Start     string    `json:"start"`
	End       string    `json:"end"`
	Act       string    `json:"act"`
	Epoch     string    `json:"epoch"`
	At        time.Time `json:"at"`
}

// decideAccounting is a recovery-admin's decision that an attempt whose response was lost is
// accounted for (execution-recovery.md §5.2), by accountByDecision: its executor stopped or taken
// over, the settle floor elapsed, a recovery observation after it, and the version change explained.
func decideAccounting(ctx context.Context, a *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*accountingRequest)
	operation, attempt := q.r.PathValue("id"), q.r.PathValue("attempt")
	machine, err := operationMachine(ctx, tx, operation)
	if err != nil {
		return result{}, err
	}
	// An attempt row is immutable: its operation is read before the locks.
	var of string
	err = tx.QueryRowContext(ctx, `SELECT operation FROM attempt WHERE id = $1`, attempt).Scan(&of)
	switch {
	case id.MustHave(attempt, id.Attempt) != nil || errors.Is(err, sql.ErrNoRows) || (err == nil && of != operation):
		return result{}, refuse(http.StatusNotFound, "not-found", "no such attempt of the operation").
			with("operation", operation).with("attempt", attempt)
	case err != nil:
		return result{}, err
	}
	// The observations are immutable too: both must be this machine's.
	var found int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM observation WHERE machine = $1 AND id IN ($2, $3)`,
		machine, in.Start, in.End).Scan(&found); err != nil {
		return result{}, err
	}
	if want := map[bool]int{true: 1, false: 2}[in.Start == in.End]; found != want {
		return result{}, refuse(http.StatusNotFound, "not-found", "no such observation of the operation's machine").
			with("operation", operation)
	}
	if _, err := a.lockRecoveryAct(ctx, tx, q, operation, machine, false); err != nil {
		return result{}, err
	}
	b := &accountingBody{Operation: operation, Attempt: attempt, DecidedBy: q.principal.ID, Role: string(q.role), Start: in.Start,
		End: in.End, Act: q.actID, Epoch: q.epoch}
	facts := make(map[string]any, len(in.Facts))
	for k, v := range in.Facts {
		facts[k] = v
	}
	write := func(ctx context.Context, tx *sql.Tx) error {
		r, err := a.accountByDecision(ctx, tx, accountingDecision{Attempt: attempt, DecidedBy: b.DecidedBy, Act: b.Act,
			Start: in.Start, End: in.End, Facts: facts})
		if err != nil {
			return conflictOf(err)
		}
		b.Revision = r.Revision
		return tx.QueryRowContext(ctx, `SELECT at FROM attempt_accounting WHERE attempt = $1`, attempt).Scan(&b.At)
	}
	return result{status: http.StatusCreated, body: b, subjects: []string{operation, attempt, machine}, atActOrder: write}, nil
}

// resolutionRequest names the state a recovery-admin resolves an unresolved operation into and why.
// The only one an operator may record is cancelled (execution-recovery.md §4): completed, failed,
// rejected and a retry follow from evidence the controller decides on, and no decision stands in
// for it (§8 invariant 6).
type resolutionRequest struct {
	To     string `json:"to"`
	Reason string `json:"reason"`
}

func (in *resolutionRequest) check(*API) error {
	if in.To != "cancelled" {
		return errors.New(`to must be "cancelled", the only resolution an operator records`)
	}
	return text("reason", in.Reason, maxReason)
}

// resolutionBody is the resolution's answer: the operation's state and the operation state entry's
// revision.
type resolutionBody struct {
	Operation  string    `json:"operation"`
	State      string    `json:"state"`
	Revision   int64     `json:"revision"`
	ResolvedBy string    `json:"resolvedBy"`
	Role       string    `json:"role"`
	Reason     string    `json:"reason"`
	Act        string    `json:"act"`
	Epoch      string    `json:"epoch"`
	At         time.Time `json:"at"`
}

// resolveByDecision is a recovery-admin's resolution of an unresolved operation that recorded no
// attempt into cancelled, on a recorded reason (execution-recovery.md §4). The operation keeps its
// owner token; the operation state entry names who resolved it and why.
func resolveByDecision(ctx context.Context, a *API, tx *sql.Tx, q *request) (result, error) {
	if a.d.owner.ID == "" {
		return result{}, errNotController
	}
	in := q.input.(*resolutionRequest)
	operation := q.r.PathValue("id")
	machine, err := operationMachine(ctx, tx, operation)
	if err != nil {
		return result{}, err
	}
	l, err := a.lockRecoveryAct(ctx, tx, q, operation, machine, false)
	if err != nil {
		return result{}, err
	}
	b := &resolutionBody{Operation: operation, State: "cancelled", ResolvedBy: q.principal.ID, Role: string(q.role), Reason: in.Reason,
		Act: q.actID, Epoch: q.epoch}
	write := func(ctx context.Context, tx *sql.Tx) error {
		if l.state != "unresolved" {
			return conflictOf(refuseOutcome("resolution", "the operation is "+l.state))
		}
		var attempted bool
		var latest *string
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM attempt WHERE operation = $1),
				(SELECT id FROM observation WHERE machine = $2 ORDER BY revision DESC LIMIT 1)`, operation, machine).Scan(&attempted,
			&latest); err != nil {
			return err
		}
		// Cancelled is never proof that nothing was sent, so an operation with an attempt is not one.
		if attempted {
			return conflictOf(refuseOutcome("resolution", "an attempt is recorded for the operation"))
		}
		if _, err := tx.ExecContext(ctx, `UPDATE operation SET state = 'cancelled' WHERE id = $1`, operation); err != nil {
			return err
		}
		b.Revision, b.At, err = a.appendEntry(ctx, tx, l, "operation-state", map[string]any{"operation": operation, "from": "unresolved",
			"to": "cancelled", "controller": l.owner.String, "generation": l.gen, "decidedBy": a.d.owner.ID, "cause": "resolution",
			"resolvedBy": b.ResolvedBy, "role": b.Role, "reason": b.Reason, "act": b.Act,
			// The evidence relied on (§4): what this resolution checked, no attempt recorded, and the
			// machine's latest recorded observation, none if it was never read.
			"evidence": map[string]any{"attempts": 0, "observation": latest}})
		return err
	}
	return result{status: http.StatusCreated, body: b, subjects: []string{operation, machine}, atActOrder: write}, nil
}
