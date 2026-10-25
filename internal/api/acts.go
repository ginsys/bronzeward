package api

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
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

type actsPage struct {
	Items []actItem `json:"items"`
	Next  string    `json:"next,omitempty"`
}

// listActs is GET /acts: every act in recording order, paginated (§9.1). The page is read in a
// transaction that ends before the response is written (§5 rule 1): a slow client never holds the
// installation state.
func listActs(a *API, w http.ResponseWriter, q *request) {
	if a.o.beforeRead != nil {
		a.o.beforeRead()
	}
	out, err := readActs(a, q)
	setEpoch(w, q) // the epoch the listing read; a 401 drops it again
	if err != nil {
		a.fail(w, q, err)
		return
	}
	writeJSON(w, "application/json", http.StatusOK, out)
}

// readActs holds the installation state FOR SHARE: an entry committed since routing refuses an
// earlier epoch's service token here, and the epoch answered is the one it read.
func readActs(a *API, q *request) (actsPage, error) {
	out := actsPage{Items: []actItem{}}
	ctx := q.r.Context()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return out, fmt.Errorf("%w: %w", errUnavailable, err)
	}
	defer func() { _ = tx.Rollback() }() // it writes nothing
	if err := tx.QueryRowContext(ctx, `SELECT epoch, recovery_mode FROM installation_state FOR SHARE`).Scan(&q.epoch, &q.recovery); err != nil {
		return out, err
	}
	if ref := staleToken(q); ref != nil {
		return out, ref
	}
	limit, afterAct, ref := page(q, id.Act)
	if ref != nil {
		return out, ref
	}
	// seq orders acts but never leaves the database (§2): the cursor names the last act listed.
	var after int64
	if afterAct != "" {
		err := tx.QueryRowContext(ctx, `SELECT seq FROM act WHERE id = $1`, afterAct).Scan(&after)
		if errors.Is(err, sql.ErrNoRows) {
			return out, refuse(http.StatusBadRequest, "cursor-invalid", "")
		}
		if err != nil {
			return out, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, principal, principal_kind, via, role, action,
			array_to_string(subjects, ','), idempotency_key, request_id, epoch, at
		FROM act WHERE seq > $1 ORDER BY seq LIMIT $2`, after, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var it actItem
		var subjects string
		if err := rows.Scan(&it.ID, &it.Principal, &it.PrincipalKind, &it.Via, &it.Role, &it.Action,
			&subjects, &it.IdempotencyKey, &it.RequestID, &it.Epoch, &it.At); err != nil {
			return out, err
		}
		it.Subjects = []string{}
		if subjects != "" {
			it.Subjects = strings.Split(subjects, ",")
		}
		out.Items = append(out.Items, it)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.Next = makeCursor(q.epoch, out.Items[limit-1].ID)
	}
	return out, nil
}

// page reads limit, cursor and at most one of each of the route's filters, refusing anything else
// (§9.1: unknown fields are refused); the route reads its filters. The cursor names the last item
// listed, an identifier of p.
func page(q *request, p id.Prefix) (limit int, after string, ref *refusal) {
	return pageBy(q, func(last string) bool { return id.MustHave(last, p) == nil })
}

// pageSeq is page for a collection ordered by a number, an entry's revision or an event's number:
// the cursor names the last number listed, and the first page starts after 0.
func pageSeq(q *request) (limit int, after string, ref *refusal) {
	limit, after, ref = pageBy(q, func(last string) bool { n, err := strconv.ParseInt(last, 10, 64); return err == nil && n >= 1 })
	if after == "" {
		after = "0"
	}
	return limit, after, ref
}

// pageBy is page for a cursor whose last item valid accepts. The query parameters it admits are
// the route's (routes()), the ones the description lists.
func pageBy(q *request, valid func(last string) bool) (limit int, after string, ref *refusal) {
	if !slices.Contains(q.route.query, "limit") {
		return 0, "", refuse(http.StatusInternalServerError, "internal-error", "")
	}
	vals, err := url.ParseQuery(q.r.URL.RawQuery)
	if err != nil {
		return 0, "", refuse(http.StatusBadRequest, "invalid-request", "the query does not parse")
	}
	for k, v := range vals {
		if !slices.Contains(q.route.query, k) || len(v) != 1 {
			return 0, "", refuse(http.StatusBadRequest, "invalid-request", "the query takes at most one limit, one cursor and one of each filter")
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
		ep, last, err := parseCursor(vals.Get("cursor"), valid)
		if err != nil || ep != q.epoch {
			return 0, "", refuse(http.StatusBadRequest, "cursor-invalid", "")
		}
		after = last
	}
	return limit, after, nil
}

// A cursor is opaque to clients: the epoch it was issued in and the last item listed (§9.1),
// never an act's seq (§2). It is not signed: every listed item is readable by any role, so a
// forged position discloses nothing.
func makeCursor(epoch, last string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(epoch + "." + last))
}

func parseCursor(c string, valid func(last string) bool) (epoch, last string, err error) {
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", "", err
	}
	epoch, last, ok := strings.Cut(string(b), ".")
	if !ok || id.MustHave(epoch, id.Epoch) != nil || !valid(last) {
		return "", "", errors.New("not a cursor")
	}
	return epoch, last, nil
}
