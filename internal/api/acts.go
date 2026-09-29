package api

import (
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
	limit, after, ref := page(q)
	if ref != nil {
		writeProblem(w, q.id, ref)
		return
	}
	rows, err := a.db.QueryContext(q.r.Context(), `SELECT id, seq, principal, principal_kind, via, role, action,
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
	var seqs []int64
	for rows.Next() {
		var it actItem
		var seq int64
		var subjects string
		if err := rows.Scan(&it.ID, &seq, &it.Principal, &it.PrincipalKind, &it.Via, &it.Role, &it.Action,
			&subjects, &it.IdempotencyKey, &it.RequestID, &it.Epoch, &it.At); err != nil {
			a.fail(w, q, err)
			return
		}
		it.Subjects = []string{}
		if subjects != "" {
			it.Subjects = strings.Split(subjects, ",")
		}
		out.Items, seqs = append(out.Items, it), append(seqs, seq)
	}
	if err := rows.Err(); err != nil {
		a.fail(w, q, err)
		return
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.Next = makeCursor(q.epoch, seqs[limit-1])
	}
	writeJSON(w, "application/json", http.StatusOK, out)
}

// page reads limit and cursor, refusing anything else (§9.1: unknown fields are refused).
func page(q *request) (limit int, after int64, ref *refusal) {
	vals, err := url.ParseQuery(q.r.URL.RawQuery)
	if err != nil {
		return 0, 0, refuse(http.StatusBadRequest, "invalid-request", "the query does not parse")
	}
	for k, v := range vals {
		if (k != "limit" && k != "cursor") || len(v) != 1 {
			return 0, 0, refuse(http.StatusBadRequest, "invalid-request", "the query takes at most one limit and one cursor")
		}
	}
	limit = defaultLimit
	if vals.Has("limit") {
		n, err := strconv.Atoi(vals.Get("limit"))
		if err != nil || n < 1 || n > maxLimit {
			return 0, 0, refuse(http.StatusBadRequest, "invalid-request", "limit must be 1 to 500")
		}
		limit = n
	}
	if vals.Has("cursor") {
		ep, seq, err := parseCursor(vals.Get("cursor"))
		if err != nil || ep != q.epoch {
			return 0, 0, refuse(http.StatusBadRequest, "cursor-invalid", "")
		}
		after = seq
	}
	return limit, after, nil
}

// A cursor is opaque to clients: the epoch it was issued in and the last act's seq (§9.1). It is
// not signed: every act is readable by any role, so a forged position discloses nothing.
func makeCursor(epoch string, seq int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(epoch + "." + strconv.FormatInt(seq, 10)))
}

func parseCursor(c string) (string, int64, error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", 0, err
	}
	ep, s, ok := strings.Cut(string(b), ".")
	if !ok || id.MustHave(ep, id.Epoch) != nil {
		return "", 0, errors.New("not a cursor")
	}
	seq, err := strconv.ParseInt(s, 10, 64)
	if err != nil || seq < 1 {
		return "", 0, errors.New("not a cursor")
	}
	return ep, seq, nil
}
