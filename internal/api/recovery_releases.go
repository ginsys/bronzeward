package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// The scope release (execution-recovery.md §7.3 step 7, §7.4; persistence-api.md §9.2): a
// recovery-admin's act that releases one ready scope in the current epoch, opening comparison 6 for
// that scope only. It is not approval. Its transaction takes the key lock, the installation state FOR
// SHARE, the machine FOR UPDATE and the releasing identity's principal FOR SHARE, not revoked; the
// scope state entry is written once the act-order lock is held (§5 rule 4).

func scopeReleaseRoute() effectRoute {
	return effectRoute{action: "recovery.release", input: func() input { return &scopeReleaseRequest{} }, effect: releaseScope}
}

// scopeReleaseRequest carries the facts the releasing identity records with the release.
type scopeReleaseRequest struct {
	Facts map[string]string `json:"facts"`
}

func (in *scopeReleaseRequest) check(*API) error { return checkFacts(in.Facts) }

// scopeReleaseBody is a release's answer: the state the scope moved from and to, the revision of the
// mark it rests on, and its own scope state entry's revision.
type scopeReleaseBody struct {
	Machine    string    `json:"machine"`
	From       string    `json:"from"`
	State      string    `json:"state"`
	Mark       int64     `json:"mark"`
	Revision   int64     `json:"revision"`
	ReleasedBy string    `json:"releasedBy"`
	Role       string    `json:"role"`
	Act        string    `json:"act"`
	Epoch      string    `json:"epoch"`
	At         time.Time `json:"at"`
}

// releaseScope releases a ready scope whose Desired release is still the one its mark checked the
// dependencies for (ruling G4): a moved Desired needs another mark, since a plan would target a
// release whose dependencies nothing checked. A ready scope's latest scope state entry is its mark,
// the only entry that moves a scope to ready.
func releaseScope(ctx context.Context, a *API, tx *sql.Tx, q *request) (result, error) {
	m := q.r.PathValue("machine")
	if id.MustHave(m, id.Machine) != nil {
		return result{}, refuse(http.StatusNotFound, "not-found", "no such machine")
	}
	if !q.recovery {
		return result{}, refuse(http.StatusConflict, "conflict", "recovery mode is not in effect")
	}
	if q.epoch != a.d.owner.Epoch { // an entry committed after this controller's: it releases nothing in the new epoch
		return result{}, errNotController
	}
	// The lock is its own statement, so the next one reads Desired as publication last left it.
	var locked int
	if err := tx.QueryRowContext(ctx, `WITH l AS (SELECT id FROM machine WHERE id = $1 FOR UPDATE) SELECT count(*) FROM l`,
		m).Scan(&locked); err != nil {
		return result{}, err
	}
	if locked == 0 {
		return result{}, refuse(http.StatusNotFound, "not-found", "no such machine").with("machine", m)
	}
	var state string
	var desired, checked sql.NullString
	var mark sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT m.scope_state, s.desired, e.revision, e.entry->>'release' FROM machine m
			JOIN machine_state s ON s.machine = m.id
			LEFT JOIN LATERAL (SELECT revision, entry FROM machine_event WHERE machine = m.id AND kind = 'scope-state'
				AND entry->>'cause' = 'restoration-mark' AND entry->>'to' = 'ready' ORDER BY revision DESC LIMIT 1) e ON true
		WHERE m.id = $1`, m).Scan(&state, &desired, &mark, &checked)
	if err != nil {
		return result{}, err
	}
	var revoked bool
	if err := tx.QueryRowContext(ctx, `SELECT revoked FROM principal WHERE id = $1 FOR SHARE`, q.principal.ID).Scan(&revoked); err != nil {
		return result{}, err
	}
	if revoked {
		return result{}, refuse(http.StatusForbidden, "identity-revoked", "")
	}
	switch {
	case state == "pre-restore-unaccounted":
		return result{}, refuse(http.StatusConflict, "recovery-mode-active", "the scope is still pre-restore unaccounted: "+
			"it leaves only by the post-restore accounting decision").with("machine", m).with("scope", state)
	case state != "ready" || !mark.Valid:
		return result{}, refuse(http.StatusConflict, "conflict", "the scope is "+state+"; only a ready scope is released").
			with("machine", m).with("scope", state)
	case desired != checked:
		return result{}, refuse(http.StatusConflict, "conflict", "the Desired release changed since the scope was marked; "+
			"mark it again").with("machine", m)
	}
	facts := map[string]any{}
	for k, v := range q.input.(*scopeReleaseRequest).Facts {
		facts[k] = v
	}
	doc, err := json.Marshal(facts)
	if err != nil {
		return result{}, err
	}
	b := &scopeReleaseBody{Machine: m, From: state, State: "released", Mark: mark.Int64, ReleasedBy: q.principal.ID,
		Role: string(q.role), Act: q.actID, Epoch: q.epoch}
	write := func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE machine SET scope_state = 'released' WHERE id = $1`, m); err != nil {
			return err
		}
		rev, when, err := a.appendEntry(ctx, tx, operationLock{machine: m, current: q.epoch}, "scope-state", map[string]any{
			"from": state, "to": b.State, "cause": "release", "releasedBy": b.ReleasedBy, "role": b.Role, "act": b.Act,
			"mark": b.Mark, "facts": facts})
		if err != nil {
			return err
		}
		b.Revision, b.At = rev, when
		_, err = tx.ExecContext(ctx, `INSERT INTO scope_release (machine, epoch, released_by, role, act, mark_revision, facts, at,
			revision) VALUES ($1, $2, $3, 'recovery-admin', $4, $5, $6::jsonb, $7, $8)`, m, q.epoch, b.ReleasedBy, b.Act, b.Mark,
			string(doc), when, rev)
		return err
	}
	return result{status: http.StatusCreated, location: prefix + "/machines/" + m, body: b, subjects: []string{m},
		atActOrder: write}, nil
}
