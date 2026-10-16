package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// The post-restore accounting decision (execution-recovery.md §7.3 step 1, persistence-api.md §9.2):
// a recovery-admin's act that accounts one or more pre-restore unaccounted scopes by §5.2, each with
// its own recovery observations. Its transaction takes the key lock, the installation state FOR
// SHARE, every named machine FOR UPDATE in id order, the deciding identity's principal FOR SHARE, not
// revoked, and every non-terminal apply-config operation on them FOR UPDATE in id order (rule 5); the
// writes run once the act-order lock is held, so each entry's time follows every lock wait (rule 4).

func scopeAccountingRoute() effectRoute {
	return effectRoute{action: "recovery.account", input: func() input { return &scopeAccountingRequest{} }, effect: accountScopes}
}

// scopeSettle names a scope and the observations read at the start and at the end of its settle.
type scopeSettle struct {
	Machine string `json:"machine"`
	Start   string `json:"start"`
	End     string `json:"end"`
}

// scopeAccountingRequest names each scope once, and the facts the decision holds for all of them:
// `explanation` accounts for a changed machine-configuration resource version (§5.2).
type scopeAccountingRequest struct {
	Scopes []scopeSettle     `json:"scopes"`
	Facts  map[string]string `json:"facts"`
}

const maxAccountedScopes = 256

func (in *scopeAccountingRequest) check(*API) error {
	if len(in.Scopes) == 0 || len(in.Scopes) > maxAccountedScopes {
		return fmt.Errorf("scopes must name 1 to %d scopes", maxAccountedScopes)
	}
	seen := map[string]bool{}
	for _, s := range in.Scopes {
		switch {
		case id.MustHave(s.Machine, id.Machine) != nil:
			return errors.New("a scope's machine must be a machine identifier")
		case id.MustHave(s.Start, id.Observation) != nil || id.MustHave(s.End, id.Observation) != nil:
			return errors.New("a scope's start and end must be observation identifiers")
		case seen[s.Machine]:
			return fmt.Errorf("scopes names machine %s twice", s.Machine)
		}
		seen[s.Machine] = true
	}
	return checkFacts(in.Facts)
}

// scopeAccounted is one scope the decision accounted: the state it moved to, its scope state
// entry's revision and the restored attempts it accounted.
type scopeAccounted struct {
	Machine  string   `json:"machine"`
	State    string   `json:"state"`
	Revision int64    `json:"revision"`
	Start    string   `json:"start"`
	End      string   `json:"end"`
	Attempts []string `json:"attempts"`
}

// scopeAccountingBody is the decision's answer, its scopes in machine id order.
type scopeAccountingBody struct {
	Scopes    []scopeAccounted `json:"scopes"`
	DecidedBy string           `json:"decidedBy"`
	Role      string           `json:"role"`
	Act       string           `json:"act"`
	Epoch     string           `json:"epoch"`
	At        time.Time        `json:"at"`
}

// accountScopes is step 1's decision. Condition 1 of §5.2 is the stop of every pre-restoration
// instance, recorded at entry; the transport deadline condition 2 counts from is the latest of that
// stop plus the maximum transport deadline (the latest an attempt the restore erased could have had)
// and the transport deadlines of the scope's restored unaccounted attempts. Each scope must still be
// pre-restore unaccounted; one that fails a condition refuses the whole decision. A scope leaves to
// unresolved where a restored operation holds it, otherwise to blocked until its restoration
// observation and dependency check (ruling S4); each restored unaccounted attempt on it is
// accounted under the decision's act, then the scope state entry is written last.
func accountScopes(ctx context.Context, a *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*scopeAccountingRequest)
	var stopped sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT stopped_at FROM recovery_epoch WHERE epoch = $1`, q.epoch).Scan(&stopped); err != nil {
		return result{}, err
	}
	if !q.recovery || !stopped.Valid {
		return result{}, refuse(http.StatusConflict, "conflict", "recovery mode is not in effect after a restore")
	}
	scopes := slices.Clone(in.Scopes)
	slices.SortFunc(scopes, func(x, y scopeSettle) int { return strings.Compare(x.Machine, y.Machine) })
	machines := make([]string, len(scopes))
	for i, s := range scopes {
		machines[i] = s.Machine
		// Observations are immutable: both must be this machine's.
		var found int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM observation WHERE machine = $1 AND id IN ($2, $3)`,
			s.Machine, s.Start, s.End).Scan(&found); err != nil {
			return result{}, err
		}
		if want := map[bool]int{true: 1, false: 2}[s.Start == s.End]; found != want {
			return result{}, refuse(http.StatusNotFound, "not-found", "no such observation of the scope's machine").with("machine", s.Machine)
		}
	}
	var locked int
	if err := tx.QueryRowContext(ctx, `WITH l AS (SELECT id FROM machine WHERE id = ANY($1) ORDER BY id FOR UPDATE)
		SELECT count(*) FROM l`, machines).Scan(&locked); err != nil {
		return result{}, err
	}
	if locked != len(machines) {
		return result{}, refuse(http.StatusNotFound, "not-found", "a named scope's machine does not exist")
	}
	var revoked bool
	if err := tx.QueryRowContext(ctx, `SELECT revoked FROM principal WHERE id = $1 FOR SHARE`, q.principal.ID).Scan(&revoked); err != nil {
		return result{}, err
	}
	if revoked {
		return result{}, refuse(http.StatusForbidden, "identity-revoked", "")
	}
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM operation WHERE machine = ANY($1) AND kind = 'apply-config'
		AND state IN ('committed', 'sending', 'verifying', 'unresolved') ORDER BY id FOR UPDATE`, machines); err != nil {
		return result{}, err
	}
	b := &scopeAccountingBody{Scopes: []scopeAccounted{}, DecidedBy: q.principal.ID, Role: string(q.role), Act: q.actID, Epoch: q.epoch}
	given := make(map[string]any, len(in.Facts))
	for k, v := range in.Facts {
		given[k] = v
	}
	floorFrom := stopped.Time.Add(a.d.exec.MaxTransportDeadline)
	write := func(ctx context.Context, tx *sql.Tx) error {
		for _, s := range scopes {
			r, err := a.accountScope(ctx, tx, q.epoch, s, floorFrom, b, given)
			if err != nil {
				return err
			}
			b.Scopes = append(b.Scopes, r)
		}
		return nil
	}
	return result{status: http.StatusCreated, body: b, subjects: machines, atActOrder: write}, nil
}

// accountScope is the decision for one scope, under the locks accountScopes holds.
func (a *API) accountScope(ctx context.Context, tx *sql.Tx, epoch string, s scopeSettle, deadline time.Time, b *scopeAccountingBody,
	given map[string]any) (scopeAccounted, error) {
	r := scopeAccounted{Machine: s.Machine, Start: s.Start, End: s.End, Attempts: []string{}}
	conflict := func(cause string) error {
		return refuse(http.StatusConflict, "conflict", cause).with("machine", s.Machine)
	}
	var state string
	var holds bool
	if err := tx.QueryRowContext(ctx, `SELECT m.scope_state, EXISTS (SELECT FROM operation o WHERE o.machine = m.id
			AND o.kind = 'apply-config' AND o.state IN ('committed', 'sending', 'verifying', 'unresolved'))
		FROM machine m WHERE m.id = $1`, s.Machine).Scan(&state, &holds); err != nil {
		return r, err
	}
	if state != "pre-restore-unaccounted" {
		return r, conflict("the scope is " + state + ", not pre-restore unaccounted")
	}
	type restored struct{ id, operation string }
	var attempts []restored
	rows, err := tx.QueryContext(ctx, `SELECT a.id, a.operation, a.transport_deadline FROM attempt a WHERE a.machine = $1
		AND NOT EXISTS (SELECT FROM attempt_accounting c WHERE c.attempt = a.id) ORDER BY a.id`, s.Machine)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var at restored
		var transport time.Time
		if err := rows.Scan(&at.id, &at.operation, &transport); err != nil {
			_ = rows.Close()
			return r, err
		}
		if transport.After(deadline) {
			deadline = transport
		}
		attempts = append(attempts, at)
	}
	if err := rows.Close(); err != nil {
		return r, err
	}
	facts, cause, err := a.settled(ctx, tx, s.Machine, deadline, deadline, s.Start, s.End, given)
	switch {
	case err != nil:
		return r, fmt.Errorf("accounting of scope %s: %w", s.Machine, err)
	case cause != "":
		return r, conflict(cause)
	}
	doc, err := json.Marshal(facts)
	if err != nil {
		return r, err
	}
	l := operationLock{machine: s.Machine, current: epoch}
	for _, at := range attempts {
		rev, when, err := a.appendEntry(ctx, tx, l, "accounting", map[string]any{"operation": at.operation, "attempt": at.id,
			"basis": "decision", "scope": true, "decidedBy": b.DecidedBy, "role": b.Role, "act": b.Act, "start": s.Start,
			"end": s.End, "facts": facts})
		if err != nil {
			return r, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO attempt_accounting (attempt, machine, basis, decided_by, role, act,
				start_observation, end_observation, facts, epoch, at, revision)
			VALUES ($1, $2, 'decision', $3, 'recovery-admin', $4, $5, $6, $7::jsonb, $8, $9, $10)`,
			at.id, s.Machine, b.DecidedBy, b.Act, s.Start, s.End, string(doc), epoch, when, rev); err != nil {
			return r, err
		}
		r.Attempts = append(r.Attempts, at.id)
	}
	r.State = "blocked"
	if holds {
		r.State = "unresolved"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE machine SET scope_state = $2 WHERE id = $1`, s.Machine, r.State); err != nil {
		return r, err
	}
	entry := map[string]any{"from": state, "to": r.State, "cause": "post-restore-accounting", "decidedBy": b.DecidedBy,
		"role": b.Role, "act": b.Act, "start": s.Start, "end": s.End, "facts": facts, "attempts": r.Attempts}
	if !holds {
		entry["pending"] = "a restoration observation and the dependency check after step 1"
	}
	rev, when, err := a.appendEntry(ctx, tx, l, "scope-state", entry)
	if err != nil {
		return r, err
	}
	r.Revision, b.At = rev, when
	_, err = tx.ExecContext(ctx, `INSERT INTO scope_accounting (machine, epoch, decided_by, role, act, start_observation,
			end_observation, facts, at, revision) VALUES ($1, $2, $3, 'recovery-admin', $4, $5, $6, $7::jsonb, $8, $9)`,
		s.Machine, epoch, b.DecidedBy, b.Act, s.Start, s.End, string(doc), when, rev)
	return r, err
}
