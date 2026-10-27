package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"

	"github.com/ginsys/bronzeward/internal/id"
)

// maxPrincipalIDs is how many identifiers one principals read takes (persistence-api.md §9.2).
const maxPrincipalIDs = 100

// principalBody is one entry of GET /principals (§9.2, choice §17.40): what an identifier names.
// A member that does not apply to its kind is null; nothing else of the row is served, its
// revocation and responsible human included.
type principalBody struct {
	ID      string  `json:"id"`
	Kind    string  `json:"kind"` // human, service, or unknown for an identifier no row holds
	Issuer  *string `json:"issuer"`
	Subject *string `json:"subject"`
	Service *string `json:"service"`
}

// getPrincipals answers GET /principals: not a paginated collection, it takes only id, 1 to 100
// times, and answers each distinct identifier once, in the order first given. An identifier no
// row holds is unknown, not refused: a restore to an older backup loses the rows created after it
// (§12.3). The read records no act and carries no ETag.
func getPrincipals(a *API, w http.ResponseWriter, q *request) {
	readIn(a, w, q, func(ctx context.Context, tx *sql.Tx) (string, any, error) {
		ids, ref := principalIDs(q.r.URL.RawQuery)
		if ref != nil {
			return "", nil, ref
		}
		items := make([]principalBody, len(ids))
		at := make(map[string]int, len(ids))
		for i, idn := range ids {
			items[i] = principalBody{ID: idn, Kind: "unknown"}
			at[idn] = i
		}
		rows, err := tx.QueryContext(ctx, `SELECT id, kind, iss, sub, name FROM principal WHERE id = ANY ($1)`, ids)
		if err != nil {
			return "", nil, err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var idn, kind string
			var iss, sub, name sql.NullString
			if err := rows.Scan(&idn, &kind, &iss, &sub, &name); err != nil {
				return "", nil, err
			}
			b := &items[at[idn]]
			b.Kind = kind
			if kind == "human" {
				b.Issuer, b.Subject = &iss.String, &sub.String
			} else {
				b.Service = &name.String
			}
		}
		if err := rows.Err(); err != nil {
			return "", nil, err
		}
		return "", listPage[principalBody]{Items: items}, nil
	})
}

// principalIDs is the read's query: id only, 1 to 100 times, each a principal identifier; a
// repeated one is kept once, at its first place.
func principalIDs(raw string) ([]string, *refusal) {
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return nil, refuse(http.StatusBadRequest, "invalid-request", "the query does not parse")
	}
	given := vals["id"]
	if len(vals) != 1 || len(given) == 0 || len(given) > maxPrincipalIDs {
		return nil, refuse(http.StatusBadRequest, "invalid-request", "the read takes only id, 1 to 100 times")
	}
	ids := make([]string, 0, len(given))
	seen := make(map[string]bool, len(given))
	for _, v := range given {
		if id.MustHave(v, id.Principal) != nil {
			return nil, refuse(http.StatusBadRequest, "invalid-request", "each id must be a principal identifier")
		}
		if !seen[v] {
			seen[v] = true
			ids = append(ids, v)
		}
	}
	return ids, nil
}
