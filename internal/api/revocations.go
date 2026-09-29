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

// revokeIdentity is T5c (§5, §10.4), after the key lock and the installation state. Principals
// are locked in id order (rule 5): the identity FOR UPDATE by auth.RevokeIdentity, and the
// revoking human FOR SHARE, refused if revoked by then (rule 2). T5c's machine locks and timeline
// entries join here with the machine and plan tables (ginsys/bronzeward#22, ginsys/bronzeward#27);
// none exists yet.
func revokeIdentity(ctx context.Context, a *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*revocationInput)
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
	if err := tx.QueryRowContext(ctx, `INSERT INTO identity_revocation (identity, revoked_by, role, reason, act, epoch, at)
		VALUES ($1, $2, $3, $4, $5, $6, now()) RETURNING at`,
		in.target, q.principal.ID, string(q.role), in.Reason, q.actID, q.epoch).Scan(&b.At); err != nil {
		return result{}, err
	}
	return result{status: http.StatusCreated, body: b, subjects: []string{in.target}}, nil
}
