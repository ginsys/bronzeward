package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// POST /machines/{id}/freezes and /unfreezes place and lift a freeze on a machine scope
// (execution-recovery.md §6.2). Each is T11 (persistence-api.md §5): the key lock, the installation
// state FOR SHARE, the machine row FOR UPDATE, which every commitment and attempt reads frozen
// under (comparison 6), and the acting principal FOR SHARE, not revoked. The fact, its freeze or
// unfreeze entry at the next machine revision (T7), the machine's frozen projection and the act
// commit together. A freeze is accepted on any scope (§7.5); an unfreeze is refused on one still
// pre-restore unaccounted.
func machineFreeze() effectRoute {
	return effectRoute{action: "machine.freeze", input: func() input { return &reasonInput{} },
		effect: func(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
			return setFreeze(ctx, tx, q, true)
		}}
}

func machineUnfreeze() effectRoute {
	return effectRoute{action: "machine.unfreeze", input: func() input { return &reasonInput{} },
		effect: func(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
			return setFreeze(ctx, tx, q, false)
		}}
}

// freezeBody is a freeze's or an unfreeze's answer: the fact with its timeline revision and act.
type freezeBody struct {
	Machine   string    `json:"machine"`
	Revision  int64     `json:"revision"`
	Frozen    bool      `json:"frozen"`
	Principal string    `json:"principal"`
	Role      string    `json:"role"`
	Reason    string    `json:"reason"`
	Act       string    `json:"act"`
	Epoch     string    `json:"epoch"`
	At        time.Time `json:"at"`
}

func setFreeze(ctx context.Context, tx *sql.Tx, q *request, freeze bool) (result, error) {
	m := q.r.PathValue("id")
	if id.MustHave(m, id.Machine) != nil {
		return result{}, refuse(http.StatusNotFound, "not-found", "no such machine")
	}
	b := freezeBody{Machine: m, Frozen: freeze, Principal: q.principal.ID, Role: string(q.role),
		Reason: q.input.(*reasonInput).Reason, Act: q.actID, Epoch: q.epoch}
	var frozen bool
	var scope string
	err := tx.QueryRowContext(ctx, `SELECT frozen, scope_state FROM machine WHERE id = $1 FOR UPDATE`, m).Scan(&frozen, &scope)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return result{}, refuse(http.StatusNotFound, "not-found", "no such machine").with("machine", m)
	case err != nil:
		return result{}, err
	case frozen == freeze && freeze:
		return result{}, refuse(http.StatusConflict, "conflict", "the machine scope is already frozen").with("machine", m)
	case frozen == freeze:
		return result{}, refuse(http.StatusConflict, "conflict", "the machine scope is not frozen").with("machine", m)
	case !freeze && scope == "pre-restore-unaccounted":
		return result{}, refuse(http.StatusConflict, "recovery-mode-active",
			"the machine's scope is still pre-restore unaccounted").with("machine", m).with("scope", scope)
	}
	var revoked bool
	if err := tx.QueryRowContext(ctx, `SELECT revoked FROM principal WHERE id = $1 FOR SHARE`, b.Principal).Scan(&revoked); err != nil {
		return result{}, err
	}
	if revoked {
		return result{}, refuse(http.StatusForbidden, "identity-revoked", "")
	}
	kind := map[bool]string{true: "freeze", false: "unfreeze"}[freeze]
	if err := tx.QueryRowContext(ctx, `UPDATE machine SET frozen = $2, revision_counter = revision_counter + 1
		WHERE id = $1 RETURNING revision_counter`, m, freeze).Scan(&b.Revision); err != nil {
		return result{}, err
	}
	if err := tx.QueryRowContext(ctx, `INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
		VALUES ($1, $2, $3, $4, jsonb_build_object('principal', $5::text, 'role', $6::text, 'reason', $7::text), now())
		RETURNING at`, m, b.Revision, b.Epoch, kind, b.Principal, b.Role, b.Reason).Scan(&b.At); err != nil {
		return result{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO machine_freeze (machine, revision, entry_kind, frozen, principal, role, reason,
		act, epoch, at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		m, b.Revision, kind, freeze, b.Principal, b.Role, b.Reason, b.Act, b.Epoch, b.At); err != nil {
		return result{}, err
	}
	b.At = b.At.UTC()
	return result{status: http.StatusCreated, location: prefix + "/machines/" + m, body: b, subjects: []string{m}}, nil
}
