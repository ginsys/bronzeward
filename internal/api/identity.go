package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/ginsys/bronzeward/internal/auth"
)

// identityPattern is the caller's own identity read, the one route that needs no role: a
// credential that maps to none is served it with no roles (persistence-api.md §10.3, decision R1).
// The route lists no role, so the role check records none for it.
const identityPattern = "/identity"

// identityBody is GET /identity (persistence-api.md §9.2, §10.3 decision R1): the caller's own
// principal, the roles its credential maps to, the credential's end and the epoch the read held.
// A member that does not apply to the caller's kind is null; nothing else of the credential is
// served, its token ID and claims included.
type identityBody struct {
	Kind      auth.Kind   `json:"kind"`
	Issuer    *string     `json:"issuer"`
	Subject   *string     `json:"subject"`
	Service   *string     `json:"service"`
	Principal *string     `json:"principal"` // null for a human with no row yet; the read creates none (§10)
	Roles     []auth.Role `json:"roles"`
	ExpiresAt time.Time   `json:"expiresAt"`
	Epoch     string      `json:"epoch"`
}

func getIdentity(a *API, w http.ResponseWriter, q *request) {
	readIn(a, w, q, func(ctx context.Context, tx *sql.Tx) (string, any, error) {
		if q.r.URL.RawQuery != "" {
			return "", nil, refuse(http.StatusBadRequest, "invalid-request", "the identity read takes no query")
		}
		p := q.principal
		b := identityBody{Kind: p.Kind, Roles: append([]auth.Role{}, p.Roles...), ExpiresAt: p.Expiry.UTC(), Epoch: q.epoch}
		if p.Kind == auth.Human {
			b.Issuer, b.Subject = &p.Issuer, &p.Subject
			var idn string
			switch err := tx.QueryRowContext(ctx, `SELECT id FROM principal WHERE kind = 'human' AND iss = $1 AND sub = $2`,
				p.Issuer, p.Subject).Scan(&idn); {
			case err == nil:
				b.Principal = &idn
			case !errors.Is(err, sql.ErrNoRows):
				return "", nil, err
			}
			return "", b, nil
		}
		var name string
		if err := tx.QueryRowContext(ctx, `SELECT name FROM principal WHERE id = $1 AND kind = 'service'`, p.ID).Scan(&name); err != nil {
			return "", nil, err
		}
		b.Service, b.Principal = &name, &p.ID
		return "", b, nil
	})
}
