package api

import (
	"net/http"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/id"
)

// talosAccessBody is GET /clusters/{id}/talos-access's answer (persistence-api.md §3.3 "Presence"):
// whether a version of the cluster's Talos CA key is stored, and nothing of the key, its path, its
// policy or the provider's answer. Version and CreatedTime are set only when it is held, Reason
// only when it is not.
type talosAccessBody struct {
	Present     bool       `json:"present"`
	Version     *int64     `json:"version,omitempty"`
	CreatedTime *time.Time `json:"createdTime,omitempty"`
	Reason      string     `json:"reason,omitempty"`
}

// The two reasons a key is not held, as the specification names them.
const (
	reasonLatestVersionDeleted = "latest-version-deleted"
	reasonNeverWritten         = "never-written-or-removed"
)

// getTalosAccess is GET /clusters/{id}/talos-access (§3.3, choice §17.42): an author's read of whether
// the cluster's Talos CA key is stored, from the metadata of its path under the access-presence
// identity, never from the key. It records no act and answers under no-store (set for every answer).
// The provider call holds no transaction.
func getTalosAccess(a *API, w http.ResponseWriter, q *request) {
	body, ref := a.talosAccess(q)
	if ref != nil {
		a.problem(w, q, ref)
		return
	}
	writeJSON(w, "application/json", http.StatusOK, body)
}

func (a *API) talosAccess(q *request) (talosAccessBody, *refusal) {
	notFound := refuse(http.StatusNotFound, "not-found", "no such cluster")
	if q.r.URL.RawQuery != "" {
		return talosAccessBody{}, refuse(http.StatusBadRequest, "invalid-request", "a talos access read takes no query")
	}
	cluster := q.r.PathValue("id")
	if id.MustHave(cluster, id.Cluster) != nil {
		return talosAccessBody{}, notFound
	}
	var exists bool
	if err := a.db.QueryRowContext(q.r.Context(), `SELECT EXISTS (SELECT 1 FROM cluster WHERE id = $1)`, cluster).Scan(&exists); err != nil {
		a.o.logf("%s: cluster read: %v", q.id, err)
		return talosAccessBody{}, refuse(http.StatusServiceUnavailable, "dependency-unavailable", "the database could not be read")
	}
	if !exists {
		return talosAccessBody{}, notFound
	}
	if a.d.presence == nil {
		return talosAccessBody{}, refuse(http.StatusServiceUnavailable, "dependency-unavailable",
			"no access presence identity is configured (provider.presenceTokenFile)")
	}
	ans, err := a.d.presence.CAKeyMetadata(q.r.Context(), cluster)
	if err != nil {
		// Only a cluster that is not a cl_ identifier, which was checked above: a defect.
		return talosAccessBody{}, refuse(http.StatusInternalServerError, "internal-error", "the presence read failed")
	}
	return presenceOf(ans)
}

// presenceOf maps the provider's answer to the body or the refusal. The order matters and the mapping
// is total: the first row that matches answers, and every class or reason not listed is a 503
// talos-access-unavailable, never present and never absent. Classify returns Malformed for a version
// below 1 before it reads the status, so the status rows come first.
func presenceOf(ans classify.Answer) (talosAccessBody, *refusal) {
	unusable := refuse(http.StatusServiceUnavailable, "talos-access-unavailable",
		"the secret store did not give a usable answer about the Talos access key")
	// Row 1: the answer as a whole. A body that does not parse as the provider's metadata, a refused
	// policy and any status but 200 and 404 are unusable; a missing current_version is refused here, as
	// Unreadable, before CurrentVersion runs.
	switch classify.AnswerReason(classify.KV, ans) {
	case classify.None:
	case classify.Absent:
		// A 404 that is not KV's empty errors list is a route nothing serves, a missing mount for one:
		// that says nothing about the key.
		if !classify.EmptyErrors(ans) {
			return talosAccessBody{}, unusable
		}
		return talosAccessBody{Reason: reasonNeverWritten}, nil
	case classify.Unavailable, classify.Unreachable:
		return talosAccessBody{}, refuse(http.StatusServiceUnavailable, "dependency-unavailable", "the secret store is unavailable")
	default:
		return talosAccessBody{}, unusable
	}
	// Row 2: a current_version the body does not hold. AnswerReason already refused such a body, so this
	// is the second guard for a change in classify.
	version, ok := classify.CurrentVersion(ans)
	if !ok {
		return talosAccessBody{}, unusable
	}
	// Rows 3 and 4: the latest version, classified with the dependency monitor's own procedure and a
	// zero Created, so the answer's created_time is taken, not compared.
	res := classify.Classify(classify.Dependency{Provider: classify.KV, Version: version}, ans)
	switch {
	case res.Class == classify.Retained && (res.Reason == classify.None || res.Reason == classify.DeletionScheduled):
		created := res.Created.UTC()
		return talosAccessBody{Present: true, Version: &version, CreatedTime: &created}, nil
	case res.Class == classify.Blocked && res.Reason == classify.SoftDeleted,
		res.Class == classify.Lost && res.Reason == classify.Destroyed:
		return talosAccessBody{Reason: reasonLatestVersionDeleted}, nil
	case res.Class == classify.Unknown && res.Reason == classify.DeletionTimeUndecidable:
		// A scheduled deletion in the same second as the provider's Date, or no readable Date, cannot be
		// told from a version already deleted: ask again in a second.
		unusable.retryAfter = 1
		return talosAccessBody{}, unusable
	}
	return talosAccessBody{}, unusable
}
