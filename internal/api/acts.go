package api

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// actItem is one act (§10.5) as GET /acts lists it.
type actItem struct {
	ID             string    `json:"id"`
	Principal      string    `json:"principal"`
	PrincipalKind  string    `json:"principalKind"`
	Via            string    `json:"via"`
	Role           *string   `json:"role"`
	Action         string    `json:"action"`
	Subjects       []string  `json:"subjects"`
	IdempotencyKey *string   `json:"idempotencyKey"`
	RequestID      *string   `json:"requestId"`
	Epoch          string    `json:"epoch"`
	At             time.Time `json:"at"`
}

const defaultLimit, maxLimit = 50, 500

// listActs is GET /acts: every act in recording order, paginated (§9.1).
func listActs(a *API, w http.ResponseWriter, q *request) {
	limit, afterAct, ref := page(q)
	if ref != nil {
		a.problem(w, q, ref)
		return
	}
	// seq orders acts but never leaves the database (§2): the cursor names the last act listed.
	var after int64
	if afterAct != "" {
		err := a.db.QueryRowContext(q.r.Context(), `SELECT seq FROM act WHERE id = $1`, afterAct).Scan(&after)
		if errors.Is(err, sql.ErrNoRows) {
			a.problem(w, q, refuse(http.StatusBadRequest, "cursor-invalid", ""))
			return
		}
		if err != nil {
			a.fail(w, q, err)
			return
		}
	}
	rows, err := a.db.QueryContext(q.r.Context(), `SELECT id, principal, principal_kind, via, role, action,
			array_to_string(subjects, ','), idempotency_key, request_id, epoch, at
		FROM act WHERE seq > $1 ORDER BY seq LIMIT $2`, after, limit+1)
	if err != nil {
		a.fail(w, q, err)
		return
	}
	defer rows.Close()
	out := struct {
		Items []actItem `json:"items"`
		Next  string    `json:"next,omitempty"`
	}{Items: []actItem{}}
	for rows.Next() {
		var it actItem
		var subjects string
		if err := rows.Scan(&it.ID, &it.Principal, &it.PrincipalKind, &it.Via, &it.Role, &it.Action,
			&subjects, &it.IdempotencyKey, &it.RequestID, &it.Epoch, &it.At); err != nil {
			a.fail(w, q, err)
			return
		}
		it.Subjects = []string{}
		if subjects != "" {
			it.Subjects = strings.Split(subjects, ",")
		}
		out.Items = append(out.Items, it)
	}
	if err := rows.Err(); err != nil {
		a.fail(w, q, err)
		return
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.Next = makeCursor(q.epoch, out.Items[limit-1].ID)
	}
	writeJSON(w, "application/json", http.StatusOK, out)
}

// page reads limit and cursor, refusing anything else (§9.1: unknown fields are refused).
func page(q *request) (limit int, afterAct string, ref *refusal) {
	vals, err := url.ParseQuery(q.r.URL.RawQuery)
	if err != nil {
		return 0, "", refuse(http.StatusBadRequest, "invalid-request", "the query does not parse")
	}
	for k, v := range vals {
		if (k != "limit" && k != "cursor") || len(v) != 1 {
			return 0, "", refuse(http.StatusBadRequest, "invalid-request", "the query takes at most one limit and one cursor")
		}
	}
	limit = defaultLimit
	if vals.Has("limit") {
		n, err := strconv.Atoi(vals.Get("limit"))
		if err != nil || n < 1 || n > maxLimit {
			return 0, "", refuse(http.StatusBadRequest, "invalid-request", "limit must be 1 to 500")
		}
		limit = n
	}
	if vals.Has("cursor") {
		ep, act, err := parseCursor(vals.Get("cursor"))
		if err != nil || ep != q.epoch {
			return 0, "", refuse(http.StatusBadRequest, "cursor-invalid", "")
		}
		afterAct = act
	}
	return limit, afterAct, nil
}

// A cursor is opaque to clients: the epoch it was issued in and the last act listed (§9.1), never
// the act's seq (§2). It is not signed: every act is readable by any role, so a forged position
// discloses nothing.
func makeCursor(epoch, act string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(epoch + "." + act))
}

func parseCursor(c string) (epoch, act string, err error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", "", err
	}
	epoch, act, ok := strings.Cut(string(b), ".")
	if !ok || id.MustHave(epoch, id.Epoch) != nil || id.MustHave(act, id.Act) != nil {
		return "", "", errors.New("not a cursor")
	}
	return epoch, act, nil
}
