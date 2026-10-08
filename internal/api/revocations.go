package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/auth"
	"github.com/ginsys/bronzeward/internal/id"
)

func identityRevocations() *route {
	return &route{method: http.MethodPost, pattern: "/identity-revocations", roles: []auth.Role{auth.RecoveryAdmin}, humanOnly: true,
		action: "identity.revoke", input: func() input { return &revocationInput{} }, prepare: prepareRevocation, effect: revokeIdentity}
}

const maxReason = 1024

// revocationInput names the identity by id, or by the configured issuer's subject (§9.3).
type revocationInput struct {
	Identity string `json:"identity"`
	Iss      string `json:"iss"`
	Sub      string `json:"sub"`
	Reason   string `json:"reason"`
	target   string // the principal, resolved by prepareRevocation
}

func (in *revocationInput) check(a *API) error {
	switch {
	case in.Identity != "" && (in.Iss != "" || in.Sub != ""):
		return errors.New("name the identity by identity, or by iss and sub, not both")
	case in.Identity != "":
		if id.MustHave(in.Identity, id.Principal) != nil {
			return errors.New("identity is not an idn identifier")
		}
	case in.Iss == "" || in.Sub == "":
		return errors.New("name the identity by identity, or by iss and sub")
	case in.Iss != a.issuer:
		return errors.New("iss is not the configured issuer")
	}
	if strings.TrimSpace(in.Reason) == "" || len(in.Reason) > maxReason {
		return fmt.Errorf("reason must be 1 to %d bytes and not blank", maxReason)
	}
	return nil
}

// prepareRevocation resolves the identity. A subject that never signed in gets its row here, in
// its own short transaction, even if deniedSubjects lists it: the operator lists a subject before
// revoking it (§10, §10.4).
func prepareRevocation(ctx context.Context, a *API, q *request) error {
	in := q.input.(*revocationInput)
	if in.Identity != "" {
		in.target = in.Identity
		return nil
	}
	idn, err := auth.RecordHuman(ctx, a.db, in.Iss, in.Sub)
	if err != nil {
		return fmt.Errorf("%w: %w", errUnavailable, err)
	}
	in.target = idn
	return nil
}

const revocationNote = "A database restore can remove this revocation. deniedSubjects in the deployment " +
	"configuration survives a restore, so it must list this identity: a human by iss and sub, a service " +
	"identity by its idn identifier (persistence-api.md §10.4)."

type revocationBody struct {
	Identity             string    `json:"identity"`
	RevokedBy            string    `json:"revokedBy"`
	Role                 auth.Role `json:"role"`
	Reason               string    `json:"reason"`
	Act                  string    `json:"act"`
	Epoch                string    `json:"epoch"`
	At                   time.Time `json:"at"`
	DeniedSubjectsListed bool      `json:"deniedSubjectsListed"`
	Note                 string    `json:"note"`
}

// revokeIdentity is T5c (§5, §10.4), after the key lock and the installation state. Every machine
// row is locked FOR UPDATE in id order before the principals (rule 5), so the machines whose
// timelines take an entry are read under those locks. Principals are locked in id order: the
// identity FOR UPDATE by auth.RevokeIdentity, and the revoking human FOR SHARE, refused if revoked
// by then (rule 2).
func revokeIdentity(ctx context.Context, a *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*revocationInput)
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM machine ORDER BY id FOR UPDATE`); err != nil {
		return result{}, err
	}
	ids := []string{in.target, q.principal.ID}
	slices.Sort(ids)
	for _, p := range slices.Compact(ids) {
		if p != in.target {
			if a.o.noRevokerCheck {
				continue
			}
			var revoked bool
			if err := tx.QueryRowContext(ctx, `SELECT revoked FROM principal WHERE id = $1 FOR SHARE`, p).Scan(&revoked); err != nil {
				return result{}, err
			}
			if revoked {
				return result{}, refuse(http.StatusForbidden, "identity-revoked", "")
			}
			continue
		}
		already, err := auth.RevokeIdentity(ctx, tx, p)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return result{}, refuse(http.StatusNotFound, "not-found", "no identity "+p)
		case err != nil:
			return result{}, err
		case already && p == q.principal.ID: // a human revoking itself, revoked meanwhile (rule 2)
			return result{}, refuse(http.StatusForbidden, "identity-revoked", "")
		case already:
			return result{}, refuse(http.StatusConflict, "conflict", "the identity is already revoked; a revocation is permanent").with("identity", p)
		}
	}
	var kind string
	var iss, sub sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT kind, iss, sub FROM principal WHERE id = $1`, in.target).Scan(&kind, &iss, &sub); err != nil {
		return result{}, err
	}
	b := revocationBody{Identity: in.target, RevokedBy: q.principal.ID, Role: q.role, Reason: in.Reason, Act: q.actID,
		Epoch: q.epoch, Note: revocationNote}
	if kind == string(auth.Human) {
		b.DeniedSubjectsListed = a.denied.Human(iss.String, sub.String)
	} else {
		b.DeniedSubjectsListed = a.denied.Service(in.target)
	}
	// PA §5 rule 4: the time follows every lock wait, the principals' and the act-order lock's,
	// so a plan read can tell whether the revocation came before its approved plan's expiry (§8.1).
	write := func(ctx context.Context, tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `INSERT INTO identity_revocation (identity, revoked_by, role, reason, act, epoch, at)
			VALUES ($1, $2, $3, $4, $5, $6, clock_timestamp()) RETURNING at`,
			in.target, q.principal.ID, string(q.role), in.Reason, q.actID, q.epoch).Scan(&b.At); err != nil {
			return err
		}
		return identityEntries(ctx, tx, &b)
	}
	return result{status: http.StatusCreated, body: &b, subjects: []string{in.target}, atActOrder: write}, nil
}

// identityEntries appends an identity revocation entry (T7) to the timeline of each machine with
// a plan the revoked identity approved whose plan or operation is not terminal at the revocation's
// time: a plan approved by that identity's approval and not past its expiry, or committed under it
// with an operation not yet terminal (§5 T5c, §8.1; execution-recovery.md §4.1). The entry names
// those plans. The caller holds every machine row, so the plans are read under those locks.
func identityEntries(ctx context.Context, tx *sql.Tx, b *revocationBody) error {
	rows, err := tx.QueryContext(ctx, `SELECT p.machine, jsonb_agg(p.id ORDER BY p.id)
		FROM plan_state s JOIN approval a ON a.id = s.approval JOIN plan p ON p.id = s.plan
		LEFT JOIN operation o ON o.id = s.operation
		WHERE a.approver = $1 AND ((s.state = 'approved' AND p.expires_at > $2)
			OR (s.state = 'committed' AND o.state IN ('committed', 'sending', 'verifying', 'unresolved')))
		GROUP BY p.machine ORDER BY p.machine`, b.Identity, b.At)
	if err != nil {
		return err
	}
	type entry struct{ machine, plans string }
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.machine, &e.plans); err != nil {
			_ = rows.Close()
			return err
		}
		entries = append(entries, e)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, e := range entries {
		if _, err := tx.ExecContext(ctx, `WITH m AS (UPDATE machine SET revision_counter = revision_counter + 1 WHERE id = $1
				RETURNING revision_counter)
			INSERT INTO machine_event (machine, revision, epoch, kind, entry, at)
			SELECT $1, m.revision_counter, $2, 'identity-revocation', jsonb_build_object('identity', $3::text, 'plans', $4::jsonb,
				'principal', $5::text, 'role', $6::text, 'reason', $7::text), $8 FROM m`,
			e.machine, b.Epoch, b.Identity, e.plans, b.RevokedBy, string(b.Role), b.Reason, b.At); err != nil {
			return err
		}
	}
	return nil
}
