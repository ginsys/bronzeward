package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// Leaving recovery mode (execution-recovery.md §7.6; persistence-api.md §9.2, §12.2): a
// recovery-admin's act, refused until every machine scope is released in the current epoch. Its
// transaction takes the installation state FOR UPDATE before it checks the scopes (T11), so it waits
// for an inventory request, which holds that row FOR SHARE, and then sees the machine that request
// inserted; then every machine FOR UPDATE in id order and the leaving identity's principal FOR SHARE,
// not revoked. The writes follow the act-order lock (§5 rule 4).

// exitPattern is the exit's route, under prefix.
const exitPattern = "/recovery/exits"

func exitRoute() effectRoute {
	return effectRoute{action: "recovery.exit", input: func() input { return &exitRequest{} }, effect: leaveRecovery}
}

// exitRequest carries the facts the leaving identity records with the exit.
type exitRequest struct {
	Facts map[string]string `json:"facts"`
}

func (in *exitRequest) check(*API) error { return checkFacts(in.Facts) }

// exitBody is an exit's answer: the epoch it ends recovery mode in and the scopes it returned to
// normal, in identifier order.
type exitBody struct {
	Epoch    string    `json:"epoch"`
	ExitedBy string    `json:"exitedBy"`
	Role     string    `json:"role"`
	Act      string    `json:"act"`
	At       time.Time `json:"at"`
	Scopes   []string  `json:"scopes"`
}

// leaveRecovery ends recovery mode once every scope is released, and returns each released scope to
// normal with its scope state entry (ruling X1): a release authorizes within its epoch, and the next
// entry starts every scope from normal again.
func leaveRecovery(ctx context.Context, a *API, tx *sql.Tx, q *request) (result, error) {
	if !q.recovery {
		return result{}, refuse(http.StatusConflict, "conflict", "recovery mode is not in effect")
	}
	if q.epoch != a.d.owner.Epoch { // an entry committed after this controller's: it leaves nothing in the new epoch
		return result{}, errNotController
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, scope_state FROM machine ORDER BY id FOR UPDATE`)
	if err != nil {
		return result{}, err
	}
	var scopes []string
	var held *refusal
	for rows.Next() {
		var m, state string
		if err := rows.Scan(&m, &state); err != nil {
			_ = rows.Close()
			return result{}, err
		}
		if state != "released" && held == nil {
			held = refuse(http.StatusConflict, "conflict", "the scope is "+state+"; recovery mode is left only once every scope "+
				"is released").with("machine", m).with("scope", state)
		}
		scopes = append(scopes, m)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return result{}, err
	}
	var revoked bool
	if err := tx.QueryRowContext(ctx, `SELECT revoked FROM principal WHERE id = $1 FOR SHARE`, q.principal.ID).Scan(&revoked); err != nil {
		return result{}, err
	}
	if revoked {
		return result{}, refuse(http.StatusForbidden, "identity-revoked", "")
	}
	if held != nil {
		return result{}, held
	}
	facts := map[string]any{}
	for k, v := range q.input.(*exitRequest).Facts {
		facts[k] = v
	}
	doc, err := json.Marshal(facts)
	if err != nil {
		return result{}, err
	}
	b := &exitBody{Epoch: q.epoch, ExitedBy: q.principal.ID, Role: string(q.role), Act: q.actID, Scopes: append([]string{}, scopes...)}
	write := func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE installation_state SET recovery_mode = false`); err != nil {
			return err
		}
		for _, m := range scopes {
			if _, err := tx.ExecContext(ctx, `UPDATE machine SET scope_state = 'normal' WHERE id = $1`, m); err != nil {
				return err
			}
			if _, _, err := a.appendEntry(ctx, tx, operationLock{machine: m, current: q.epoch}, "scope-state", map[string]any{
				"from": "released", "to": "normal", "cause": "exit", "exitedBy": b.ExitedBy, "role": b.Role, "act": b.Act}); err != nil {
				return err
			}
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO recovery_exit (epoch, exited_by, role, act, facts, at)
			VALUES ($1, $2, 'recovery-admin', $3, $4::jsonb, clock_timestamp()) RETURNING at`, q.epoch, b.ExitedBy, b.Act,
			string(doc)).Scan(&b.At); err != nil {
			return err
		}
		q.recovery = false // the answer's headers state the mode it leaves
		return nil
	}
	return result{status: http.StatusCreated, location: prefix + "/recovery", body: b, subjects: []string{q.epoch},
		atActOrder: write}, nil
}

// recoveryBody is GET /recovery (persistence-api.md §9.2; ruling X2): the current epoch, whether
// recovery mode is in effect, the entry that minted the epoch (null for the installation's), the exit
// that ended its recovery mode (null before one), and every machine's scope state in identifier
// order. Each scope's recovery timeline is its machine's timeline (§9.2), its scope-state entries.
type recoveryBody struct {
	Epoch        string            `json:"epoch"`
	RecoveryMode bool              `json:"recoveryMode"`
	Entry        *recoveryEntryRef `json:"entry"`
	Exit         *recoveryExitRef  `json:"exit"`
	Scopes       []recoveryScope   `json:"scopes"`
}

type recoveryEntryRef struct {
	EnteredBy string           `json:"enteredBy"`
	EnteredAt time.Time        `json:"enteredAt"`
	StoppedAt time.Time        `json:"stoppedAt"`
	Reason    string           `json:"reason"`
	Restored  []restoredBackup `json:"restored"`
}

type recoveryExitRef struct {
	ExitedBy string    `json:"exitedBy"`
	Act      string    `json:"act"`
	At       time.Time `json:"at"`
}

type recoveryScope struct {
	Machine string `json:"machine"`
	State   string `json:"state"`
}

func getRecovery(a *API, w http.ResponseWriter, q *request) {
	readIn(a, w, q, func(ctx context.Context, tx *sql.Tx) (string, any, error) {
		if q.r.URL.RawQuery != "" {
			return "", nil, refuse(http.StatusBadRequest, "invalid-request", "the recovery read takes no query")
		}
		b := recoveryBody{Epoch: q.epoch, RecoveryMode: q.recovery, Scopes: []recoveryScope{}}
		var by, reason sql.NullString
		var stopped sql.NullTime
		var at time.Time
		if err := tx.QueryRowContext(ctx, `SELECT entered_by, entered_at, stopped_at, reason FROM recovery_epoch WHERE epoch = $1`,
			q.epoch).Scan(&by, &at, &stopped, &reason); err != nil {
			return "", nil, err
		}
		if by.Valid {
			b.Entry = &recoveryEntryRef{EnteredBy: by.String, EnteredAt: at, StoppedAt: stopped.Time, Reason: reason.String,
				Restored: []restoredBackup{}}
			if err := each(ctx, tx, `SELECT family, backup, taken_at FROM recovery_restore WHERE epoch = $1 ORDER BY family`,
				func(r *sql.Rows) error {
					var rb restoredBackup
					err := r.Scan(&rb.Family, &rb.Backup, &rb.TakenAt)
					b.Entry.Restored = append(b.Entry.Restored, rb)
					return err
				}, q.epoch); err != nil {
				return "", nil, err
			}
		}
		var x recoveryExitRef
		switch err := tx.QueryRowContext(ctx, `SELECT exited_by, act, at FROM recovery_exit WHERE epoch = $1`, q.epoch).
			Scan(&x.ExitedBy, &x.Act, &x.At); {
		case err == nil:
			b.Exit = &x
		case !errors.Is(err, sql.ErrNoRows):
			return "", nil, err
		}
		err := each(ctx, tx, `SELECT id, scope_state FROM machine ORDER BY id`, func(r *sql.Rows) error {
			var s recoveryScope
			err := r.Scan(&s.Machine, &s.State)
			b.Scopes = append(b.Scopes, s)
			return err
		})
		return "", b, err
	})
}

// each runs fn on every row query returns.
func each(ctx context.Context, tx *sql.Tx, query string, fn func(*sql.Rows) error, args ...any) error {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		if err := fn(rows); err != nil {
			_ = rows.Close()
			return err
		}
	}
	return errors.Join(rows.Err(), rows.Close())
}

// normalStartRefusal is the refusal, by a recovery start after its entry, of the acts it does not
// serve (execution-recovery.md §7.2; persistence-api.md §12.2; ruling X3): a normal restart serves them.
func normalStartRefusal() *refusal {
	return refuse(http.StatusConflict, "recovery-mode-active",
		"this process started with the recovery-start flag; restart it without the flag for this act")
}
