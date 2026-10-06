package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/ginsys/bronzeward/internal/id"
)

// The publication request (T2, persistence-api.md §5, §7.3, §9.3): it queues one publish operation
// for the draft revision it binds, or answers the one already queued or running for it. The
// worker takes the job after the COMMIT.

func publicationRequest() effectRoute {
	return effectRoute{action: "draft.publish", input: func() input { return &publicationInput{} }, effect: requestPublication}
}

// publicationInput is the request's body: an empty object (§9.3).
type publicationInput struct{}

func (*publicationInput) check(*API) error { return nil }

// requestPublication is T2: the draft FOR UPDATE, a published one refused naming its release, then
// open at If-Match; the active publish operation of that revision if there is one, otherwise a
// new `queued` one with its first event.
func requestPublication(ctx context.Context, a *API, tx *sql.Tx, q *request) (result, error) {
	if a.d.owner.ID == "" || a.d.pub == nil {
		return result{}, refuse(http.StatusServiceUnavailable, "dependency-unavailable", "publication is not configured; nothing was committed")
	}
	draft := q.r.PathValue("id")
	if id.MustHave(draft, id.Draft) != nil {
		return result{}, refuse(http.StatusNotFound, "not-found", "no such draft") // §9.4: the path's text is not repeated
	}
	var state, token string
	var revision int
	var release sql.NullString
	switch err := tx.QueryRowContext(ctx, `SELECT state, revision, etag_token, release FROM draft WHERE id = $1 FOR UPDATE`,
		draft).Scan(&state, &revision, &token, &release); {
	case errors.Is(err, sql.ErrNoRows):
		return result{}, refuse(http.StatusNotFound, "not-found", "no such draft").with("draft", draft)
	case err != nil:
		return result{}, err
	case state == "published":
		return result{}, refuse(http.StatusConflict, "conflict", "the draft is published").with("draft", draft).with("release", release.String)
	case state != "open":
		return result{}, refuse(http.StatusConflict, "conflict", "the draft is "+state).with("draft", draft)
	case etag(revision, token) != q.ifMatch:
		return result{}, refuse(http.StatusPreconditionFailed, "precondition-failed", "the draft has moved").with("draft", draft)
	}
	// The natural key (§7.3): the draft row's lock orders two requests, so the second reads the
	// first's operation here and never meets the index.
	var op string
	switch err := tx.QueryRowContext(ctx, `SELECT id FROM operation WHERE kind = 'publish' AND draft = $1 AND draft_revision = $2
		AND state IN ('queued', 'running')`, draft, revision).Scan(&op); {
	case errors.Is(err, sql.ErrNoRows):
		op = id.New(id.Operation)
		if _, err := tx.ExecContext(ctx, `INSERT INTO operation (id, kind, state, epoch, owner_gen, last_event, draft, draft_revision,
			created_by, created_by_kind, created_role, created_at)
			VALUES ($1, 'publish', 'queued', $2, 0, 1, $3, $4, $5, $6, $7, now())`,
			op, q.epoch, draft, revision, q.principal.ID, string(q.principal.Kind), string(q.role)); err != nil {
			return result{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO operation_event (operation, number, epoch, kind, entry, at)
			VALUES ($1, 1, $2, 'publish', '{"type": "queued"}', now())`, op, q.epoch); err != nil {
			return result{}, err
		}
	case err != nil:
		return result{}, err
	}
	b, err := readOperation(ctx, tx, op)
	if err != nil {
		return result{}, err
	}
	return result{status: http.StatusAccepted, location: prefix + "/operations/" + op, body: b, subjects: []string{op, draft},
		operation: op, afterCommit: a.wakePublisher}, nil
}

// wakePublisher tells this process's publish worker a job is queued; a wake already pending
// covers it.
func (a *API) wakePublisher() {
	if a.o.onPublish != nil {
		a.o.onPublish()
		return
	}
	select {
	case a.d.wake <- struct{}{}:
	default:
	}
}
