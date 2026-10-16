package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/id"
)

// Recovery-mode entry (persistence-api.md §12.2, T9; execution-recovery.md §7.2): a recovery-admin's
// act, served only by a process started with the recovery-start flag, at most once per start. Its
// transaction takes the key lock, the installation state FOR UPDATE (the route is exclusive, so every
// request holding it FOR SHARE has committed and none starts before this one does), every machine
// FOR UPDATE in id order, the entering identity FOR SHARE, not revoked, and every non-terminal
// apply-config operation FOR UPDATE in id order. The writes run once the act-order lock is held.

func recoveryEntry() *route {
	return &route{method: http.MethodPost, pattern: entryPattern, roles: []auth.Role{auth.RecoveryAdmin}, humanOnly: true,
		exclusive: true, action: "recovery.enter", input: func() input { return &entryRequest{} }, effect: enterRecovery}
}

// restoredBackup is one restored backup family, the backup's identity and when it was taken (§12.1).
type restoredBackup struct {
	Family  string    `json:"family"`
	Backup  string    `json:"backup"`
	TakenAt time.Time `json:"takenAt"`
}

// entryRequest names each restored backup family once, and why the operator enters (§9.3).
type entryRequest struct {
	Restored []restoredBackup `json:"restored"`
	Reason   string           `json:"reason"`
}

// backupFamilies are design §7.7 duty 1's three: the database, the provider and the credential
// store holding Bronzeward's provider tokens.
var backupFamilies = map[string]bool{"database": true, "provider": true, "credential-store": true}

func (in *entryRequest) check(*API) error {
	if len(in.Restored) == 0 || len(in.Restored) > len(backupFamilies) {
		return errors.New("restored must name each restored backup family once, at least one")
	}
	seen := map[string]bool{}
	for _, b := range in.Restored {
		switch {
		case !backupFamilies[b.Family]:
			return errors.New(`a restored family must be "database", "provider" or "credential-store"`)
		case seen[b.Family]:
			return fmt.Errorf("restored names the %s family twice", b.Family)
		case b.TakenAt.IsZero() || b.TakenAt.After(time.Now()):
			return errors.New("a restored backup's takenAt must be given, and not in the future")
		}
		seen[b.Family] = true
		if err := text("a restored backup", b.Backup, 256); err != nil {
			return err
		}
	}
	return text("reason", in.Reason, maxReason)
}

// entryBody is the entry's answer: the epoch it minted and the one it fenced, what it names, and
// what it changed: the scopes it marked and the operations it took over.
type entryBody struct {
	Epoch         string           `json:"epoch"`
	PreviousEpoch string           `json:"previousEpoch"`
	EnteredBy     string           `json:"enteredBy"`
	Role          auth.Role        `json:"role"`
	Reason        string           `json:"reason"`
	Restored      []restoredBackup `json:"restored"`
	Machines      int              `json:"machines"`
	Operations    []string         `json:"operations"`
	Act           string           `json:"act"`
	At            time.Time        `json:"at"`
}

// notRecoveryStart refuses the entry in a process started without the flag (§12.2).
func notRecoveryStart() *refusal {
	return refuse(http.StatusConflict, "conflict", "only a process started with the recovery-start flag serves the recovery entry")
}

// entryReplay is §12.4's refusal of an entry record a request would replay, or nil to replay it. A
// process not started with the flag replays none. A recovery start replays only its own entry: a
// record in the epoch it started in is an earlier entry the restored database holds, and replaying
// it would answer 201 with no epoch minted and no scope closed.
func (a *API) entryReplay(q *request, rec *record) *refusal {
	switch {
	case q.route.pattern != entryPattern:
		return nil
	case !a.d.recoveryStart:
		return notRecoveryStart()
	case rec.epoch == a.d.start:
		return refuse(http.StatusConflict, "conflict", "the key's record is an entry from before this start; retry under a new key").
			with("request", rec.requestID).with("epoch", rec.epoch)
	}
	return nil
}

// takeover is a non-terminal apply-config operation as entry takes it over.
type takeover struct {
	id, machine, state, from, fromEpoch string
	gen                                 int64
}

// enterRecovery is T9 steps 1, 2, 5 and 6. Step 1 compares the installation's epoch, read FOR UPDATE,
// with the one this process started in: another means an entry committed since, and this one is
// refused naming it, so entries under different keys mint one epoch between them.
func enterRecovery(ctx context.Context, a *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*entryRequest)
	switch {
	case !a.d.recoveryStart:
		return result{}, notRecoveryStart()
	case q.epoch != a.d.start:
		return result{}, refuse(http.StatusConflict, "conflict", "a recovery-mode entry has committed since this process started").
			with("epoch", q.epoch)
	case a.d.owner.ID == "":
		return result{}, refuse(http.StatusServiceUnavailable, "dependency-unavailable",
			"no provider is configured, so this process has no owner identity to take operations over; nothing was committed")
	}
	var machines []string
	var scopes []string
	rows, err := tx.QueryContext(ctx, `SELECT id, scope_state FROM machine ORDER BY id FOR UPDATE`)
	if err != nil {
		return result{}, err
	}
	for rows.Next() {
		var m, s string
		if err := rows.Scan(&m, &s); err != nil {
			_ = rows.Close()
			return result{}, err
		}
		machines, scopes = append(machines, m), append(scopes, s)
	}
	if err := rows.Close(); err != nil {
		return result{}, err
	}
	var revoked bool
	if err := tx.QueryRowContext(ctx, `SELECT revoked FROM principal WHERE id = $1 FOR SHARE`, q.principal.ID).Scan(&revoked); err != nil {
		return result{}, err
	}
	if revoked {
		return result{}, refuse(http.StatusForbidden, "identity-revoked", "")
	}
	var ops []takeover
	rows, err = tx.QueryContext(ctx, `SELECT id, machine, state, owner, owner_gen, owner_epoch FROM operation
		WHERE kind = 'apply-config' AND state IN ('committed', 'sending', 'verifying', 'unresolved') ORDER BY id FOR UPDATE`)
	if err != nil {
		return result{}, err
	}
	for rows.Next() {
		var o takeover
		if err := rows.Scan(&o.id, &o.machine, &o.state, &o.from, &o.gen, &o.fromEpoch); err != nil {
			_ = rows.Close()
			return result{}, err
		}
		ops = append(ops, o)
	}
	if err := rows.Close(); err != nil {
		return result{}, err
	}
	ep := id.New(id.Epoch)
	b := &entryBody{Epoch: ep, PreviousEpoch: q.epoch, EnteredBy: q.principal.ID, Role: q.role, Reason: in.Reason,
		Restored: in.Restored, Machines: len(machines), Operations: []string{}, Act: q.actID}
	write := func(ctx context.Context, tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `INSERT INTO recovery_epoch (epoch, entered_at, entered_by, reason) VALUES ($1, now(), $2, $3)
			RETURNING entered_at`, ep, b.EnteredBy, b.Reason).Scan(&b.At); err != nil {
			return err
		}
		for _, r := range in.Restored {
			if _, err := tx.ExecContext(ctx, `INSERT INTO recovery_restore (epoch, family, backup, taken_at) VALUES ($1, $2, $3, $4)`,
				ep, r.Family, r.Backup, r.TakenAt); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE installation_state SET epoch = $1, recovery_mode = true`, ep); err != nil {
			return err
		}
		// Step 5: every non-terminal operation into the new epoch, then every scope's gate closed by
		// marking it pre-restore unaccounted, each last on its machine's timeline.
		for _, o := range ops {
			if _, err := tx.ExecContext(ctx, `UPDATE operation SET state = 'unresolved', owner = $2, owner_gen = owner_gen + 1,
					owner_epoch = $3 WHERE id = $1`, o.id, a.d.owner.ID, ep); err != nil {
				return err
			}
			l := operationLock{id: o.id, machine: o.machine, current: ep}
			if _, _, err := a.appendEntry(ctx, tx, l, "ownership-transition", map[string]any{"operation": o.id, "from": o.from,
				"to": a.d.owner.ID, "generation": o.gen + 1, "fromGeneration": o.gen, "fromEpoch": o.fromEpoch, "epoch": ep,
				"reason": "recovery-entry", "requestedBy": b.EnteredBy, "role": b.Role, "act": b.Act}); err != nil {
				return err
			}
			if o.state != "unresolved" {
				if _, _, err := a.appendEntry(ctx, tx, l, "operation-state", map[string]any{"operation": o.id, "from": o.state,
					"to": "unresolved", "controller": a.d.owner.ID, "generation": o.gen + 1, "cause": "recovery-entry"}); err != nil {
					return err
				}
			}
			b.Operations = append(b.Operations, o.id)
		}
		for i, m := range machines {
			if _, err := tx.ExecContext(ctx, `UPDATE machine SET scope_state = 'pre-restore-unaccounted' WHERE id = $1`, m); err != nil {
				return err
			}
			if _, _, err := a.appendEntry(ctx, tx, operationLock{machine: m, current: ep}, "scope-state", map[string]any{
				"from": scopes[i], "to": "pre-restore-unaccounted", "cause": "recovery-entry", "enteredBy": b.EnteredBy,
				"role": b.Role, "act": b.Act}); err != nil {
				return err
			}
		}
		// Step 6: the act and the record are written in the epoch the entry minted.
		q.epoch, q.recovery = ep, true
		return nil
	}
	return result{status: http.StatusCreated, location: prefix + "/recovery", body: b, subjects: []string{ep}, atActOrder: write,
		afterCommit: func() { a.entered(ep) }}, nil
}

// entered makes the epoch this process's entry minted its own (§5.1), then ends the pre-entry gate:
// a request that finds the gate open reads that owner (§12.2). onEntry then starts what a recovery
// start runs after entry, the dependency monitor (dependency-monitor.md §6.1).
func (a *API) entered(epoch string) {
	a.d.owner.Epoch = epoch
	a.d.preEntry.Store(false)
	if a.d.onEntry != nil {
		a.d.onEntry()
	}
}
