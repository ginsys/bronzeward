package api

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
)

// record is an idempotency record (§7.1) as a request meets it.
type record struct {
	fingerprint      []byte
	current          bool // its epoch is the current one
	epoch, requestID string
	status           int
	location, etag   sql.NullString
	body             []byte
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// lookup reads the record of q's principal and key, if there is one, with the installation state
// FOR SHARE: a recovery-mode entry commits before the statement reads or after it, so the record's
// epoch is judged against the epoch it sets on q, and a service token is rechecked against it.
func lookup(ctx context.Context, db querier, q *request) (*record, error) {
	var rec record
	var rid sql.NullString
	var cur sql.NullBool
	err := db.QueryRowContext(ctx, `SELECT s.epoch, s.recovery_mode, r.fingerprint, r.epoch = s.epoch, coalesce(r.epoch, ''), r.request_id, coalesce(r.status, 0), r.location, r.etag, r.body
		FROM installation_state s LEFT JOIN idempotency_record r ON r.principal = $1 AND r.key = $2 FOR SHARE OF s`,
		q.principal.ID, q.key).Scan(&q.epoch, &q.recovery, &rec.fingerprint, &cur, &rec.epoch, &rid, &rec.status, &rec.location, &rec.etag, &rec.body)
	if err != nil {
		return nil, err
	}
	if ref := staleToken(q); ref != nil {
		return nil, ref
	}
	if !rid.Valid {
		return nil, nil
	}
	rec.current, rec.requestID = cur.Bool, rid.String
	return &rec, nil
}

// answer is §7.2's table for a key with a record: an earlier epoch's record is 409, another
// request's 422, and the same request's is its stored response, marked replayed unless this
// request committed it.
func (a *API) answer(w http.ResponseWriter, q *request, rec *record, replayed bool) {
	switch {
	case !rec.current:
		a.problem(w, q, refuse(http.StatusConflict, "conflict", "the key's record is from an earlier recovery epoch; retry under a new key").
			with("request", rec.requestID).with("epoch", rec.epoch))
	case !bytes.Equal(rec.fingerprint, q.fingerprint):
		a.problem(w, q, refuse(http.StatusUnprocessableEntity, "idempotency-key-reused", "the key was used for another request").
			with("request", rec.requestID))
	default:
		if rec.location.Valid {
			w.Header().Set("Location", rec.location.String)
		}
		if rec.etag.Valid {
			w.Header().Set("ETag", rec.etag.String)
		}
		if replayed {
			w.Header().Set("Idempotent-Replayed", "true")
		}
		// A stored refusal (§7.2) is a problem document, and so is its replay (§9.4).
		// The log names its instance, the request that stored it (§9.4).
		contentType := "application/json"
		if rec.status >= http.StatusBadRequest {
			contentType = "application/problem+json"
			a.o.logf("%s %s %s: %d, the stored problem of %s (replayed: %t)",
				q.id, q.r.Method, q.r.URL.EscapedPath(), rec.status, rec.requestID, replayed)
		}
		writeBytes(w, contentType, rec.status, rec.body)
	}
}
