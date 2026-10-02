package api

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
)

// record is an idempotency record (§7.1) as a request meets it.
type record struct {
	fingerprint      []byte
	fpKey            string // the digest key that computed fingerprint; empty for SHA-256
	current          bool   // its epoch is the current one
	epoch, requestID string
	status           int
	location, etag   sql.NullString
	body             []byte
	afterCommit      func() // the committing request's result.afterCommit; never stored
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
	err := db.QueryRowContext(ctx, `SELECT s.epoch, s.recovery_mode, r.fingerprint, coalesce(r.fingerprint_key, ''), r.epoch = s.epoch, coalesce(r.epoch, ''), r.request_id, coalesce(r.status, 0), r.location, r.etag, r.body
		FROM installation_state s LEFT JOIN idempotency_record r ON r.principal = $1 AND r.key = $2 FOR SHARE OF s`,
		q.principal.ID, q.key).Scan(&q.epoch, &q.recovery, &rec.fingerprint, &rec.fpKey, &cur, &rec.epoch, &rid, &rec.status, &rec.location, &rec.etag, &rec.body)
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
	if !rec.current {
		a.problem(w, q, refuse(http.StatusConflict, "conflict", "the key's record is from an earlier recovery epoch; retry under a new key").
			with("request", rec.requestID).with("epoch", rec.epoch))
		return
	}
	if ref := a.same(q, rec); ref != nil {
		a.problem(w, q, ref)
		return
	}
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

// same refuses q unless it is the request rec records (§7.1). A keyed record computed under
// another version of the digest key is compared by recomputing q under the recorded version, so
// a rotation does not make a retry another request. A version the provider can no longer compute
// under cannot be compared, and is another request; an unreachable provider is 503.
func (a *API) same(q *request, rec *record) *refusal {
	reused := refuse(http.StatusUnprocessableEntity, "idempotency-key-reused", "the key was used for another request").
		with("request", rec.requestID)
	if rec.fpKey == q.fpKey {
		if bytes.Equal(rec.fingerprint, q.fingerprint) {
			return nil
		}
		return reused
	}
	v, ok := recordedVersion(rec.fpKey, q.fpKey)
	if q.route.keyed == "" || !ok {
		return reused
	}
	retry := *q
	if ref := a.keyedFingerprint(q.r.Context(), &retry, v); ref != nil {
		if ref.code == "dependency-unavailable" {
			return ref
		}
		return reused
	}
	if retry.fpKey != rec.fpKey || !bytes.Equal(retry.fingerprint, rec.fingerprint) {
		return reused
	}
	return nil
}

// recordedVersion is the version N of recorded, "transit/<key>@v<N>", when its key is current's.
func recordedVersion(recorded, current string) (int, bool) {
	ri, ci := strings.LastIndex(recorded, "@v"), strings.LastIndex(current, "@v")
	if ri < 0 || ci < 0 || recorded[:ri] != current[:ci] || !strings.HasPrefix(recorded, "transit/") {
		return 0, false
	}
	n := recorded[ri+len("@v"):]
	v, err := strconv.Atoi(n)
	if err != nil || v < 1 || strconv.Itoa(v) != n {
		return 0, false
	}
	return v, true
}
