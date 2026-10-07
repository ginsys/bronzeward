package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/monitor"
)

// staleAfter is the monitor's: a class is served stale once the request that observed it began
// more than three intervals before the database's time (dependency monitor §6.3), measured from
// that dependency's own observation, never from the last completed pass (§10.1 item 12).
var staleAfter = monitor.Defaults().Stale().Seconds()

var dependencyClasses = []string{"retained", "unknown", "blocked", "lost"}

// selectDependency reads dependencyBody's columns; $N in staleBy is the stale age in seconds.
func selectDependency(staleBy string) string {
	return `SELECT id, provider, object, version, created, class, reason, first_retained_at, unknown_since, observed_from,
		recorded_at, observed_from < statement_timestamp() - make_interval(secs => ` + staleBy + `) FROM dependency_status`
}

func scanDependency(r interface{ Scan(...any) error }) (dependencyBody, error) {
	var b dependencyBody
	err := r.Scan(&b.ID, &b.Provider, &b.Object, &b.Version, &b.Created, &b.Class, &b.Reason, &b.FirstRetainedAt,
		&b.UnknownSince, &b.ObservedFrom, &b.RecordedAt, &b.Stale)
	return b, err
}

// listDependencies is GET /dependencies[?class=]: every monitored dependency, or those of one
// class, in identifier order, with the last completed pass's time.
func listDependencies(a *API, w http.ResponseWriter, q *request) {
	readIn(a, w, q, func(ctx context.Context, tx *sql.Tx) (string, any, error) {
		class := q.r.URL.Query().Get("class")
		if q.r.URL.Query().Has("class") && !slices.Contains(dependencyClasses, class) {
			return "", nil, refuse(http.StatusBadRequest, "invalid-request", "class must be retained, unknown, blocked or lost")
		}
		limit, after, ref := page(q, id.Dependency, "class")
		if ref != nil {
			return "", nil, ref
		}
		items, err := pageRows(ctx, tx, q, limit, after, selectDependency("$3")+` WHERE id > $1 AND ($4 = '' OR class = $4)
			ORDER BY id LIMIT $2`, func(r *sql.Rows) (dependencyBody, string, error) {
			b, err := scanDependency(r)
			return b, b.ID, err
		}, staleAfter, class)
		if err != nil {
			return "", nil, err
		}
		out := dependencyPage{Items: items.Items, Next: items.Next}
		return "", out, tx.QueryRowContext(ctx, `SELECT last_pass FROM dependency_monitor`).Scan(&out.LastPass)
	})
}

var getDependency = item(id.Dependency, func(ctx context.Context, tx *sql.Tx, v string) (string, any, error) {
	b, err := scanDependency(tx.QueryRowContext(ctx, selectDependency("$2")+` WHERE id = $1`, v, staleAfter))
	return "", b, err
})

// dependencyNamed answers 404 unless the route's {id} names a recorded dependency.
func dependencyNamed(ctx context.Context, tx *sql.Tx, v string) error {
	notFound := refuse(http.StatusNotFound, "not-found", "no such resource")
	if id.MustHave(v, id.Dependency) != nil {
		return notFound
	}
	err := tx.QueryRowContext(ctx, `SELECT id FROM dependency_status WHERE id = $1`, v).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound
	}
	return err
}

// The records of one dependency: its provider object version is the status's identity.
const dependencyRecords = `FROM dependency d JOIN dependency_status s USING (provider, object, version, created)`

// dependencyReleases is GET /dependencies/{id}/releases: the releases whose records name the
// dependency, in release order, each with the machine and kind of every such record.
func dependencyReleases(a *API, w http.ResponseWriter, q *request) {
	v := q.r.PathValue("id")
	readIn(a, w, q, func(ctx context.Context, tx *sql.Tx) (string, any, error) {
		if err := dependencyNamed(ctx, tx, v); err != nil {
			return "", nil, err
		}
		out, err := listRows(ctx, tx, q, id.Release, `SELECT r.id, r.cluster, r.published_at FROM release r
			WHERE r.id > $1 AND EXISTS (SELECT `+dependencyRecords+` WHERE s.id = $3 AND d.release = r.id)
			ORDER BY r.id LIMIT $2`, func(r *sql.Rows) (*dependencyReleaseBody, string, error) {
			b := &dependencyReleaseBody{Records: []dependencyRecordBody{}}
			err := r.Scan(&b.Release, &b.Cluster, &b.PublishedAt)
			return b, b.Release, err
		}, v)
		if err != nil || len(out.Items) == 0 {
			return "", out, err
		}
		byID := map[string]*dependencyReleaseBody{}
		var ids []string
		for _, b := range out.Items {
			byID[b.Release] = b
			ids = append(ids, b.Release)
		}
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT d.release, d.machine, d.kind `+dependencyRecords+`
			WHERE s.id = $1 AND d.release = ANY (string_to_array($2, ',')) ORDER BY 1, 2, 3`, v, strings.Join(ids, ","))
		if err != nil {
			return "", nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var rel string
			var m dependencyRecordBody
			if err := rows.Scan(&rel, &m.Machine, &m.Kind); err != nil {
				return "", nil, err
			}
			byID[rel].Records = append(byID[rel].Records, m)
		}
		return "", out, rows.Err()
	})
}

// listAlerts answers alerts in recording order: every alert, or with byDependency the alerts of
// the route's {id}. The recording sequence never leaves the database (PA §2): the cursor names the
// last alert listed, and on a dependency's list it must be one of that dependency's.
func listAlerts(byDependency bool) readFunc {
	return func(a *API, w http.ResponseWriter, q *request) {
		v := q.r.PathValue("id")
		readIn(a, w, q, func(ctx context.Context, tx *sql.Tx) (string, any, error) {
			out := listPage[alertBody]{Items: []alertBody{}}
			if byDependency {
				if err := dependencyNamed(ctx, tx, v); err != nil {
					return "", nil, err
				}
			}
			limit, afterAlert, ref := page(q, id.DependencyAlert)
			if ref != nil {
				return "", nil, ref
			}
			var after int64
			if afterAlert != "" {
				err := tx.QueryRowContext(ctx, `SELECT seq FROM dependency_alert WHERE id = $1 AND ($2 = '' OR dependency = $2)`,
					afterAlert, v).Scan(&after)
				if errors.Is(err, sql.ErrNoRows) {
					return "", nil, refuse(http.StatusBadRequest, "cursor-invalid", "")
				}
				if err != nil {
					return "", nil, err
				}
			}
			rows, err := tx.QueryContext(ctx, `SELECT id, kind, dependency, provider, object, version, created, class, reason,
					array_to_string(releases, ','), deletion, observed_from, answer_date, epoch, recorded_at
				FROM dependency_alert WHERE seq > $1 AND ($3 = '' OR dependency = $3) ORDER BY seq LIMIT $2`, after, limit+1, v)
			if err != nil {
				return "", nil, err
			}
			defer rows.Close()
			for rows.Next() {
				var b alertBody
				var releases *string
				if err := rows.Scan(&b.ID, &b.Kind, &b.Dependency, &b.Provider, &b.Object, &b.Version, &b.Created, &b.Class,
					&b.Reason, &releases, &b.Deletion, &b.ObservedFrom, &b.AnswerDate, &b.Epoch, &b.RecordedAt); err != nil {
					return "", nil, err
				}
				if releases != nil {
					b.Releases = strings.Split(*releases, ",")
				}
				out.Items = append(out.Items, b)
			}
			if err := rows.Err(); err != nil {
				return "", nil, err
			}
			if len(out.Items) > limit {
				out.Items = out.Items[:limit]
				out.Next = makeCursor(q.epoch, out.Items[limit-1].ID)
			}
			return "", out, nil
		})
	}
}

// dependencyBody is one monitored dependency (dependency monitor §7.2): its provider object
// version, by reference name only, and its recorded class. RecordedAt is the time of its latest
// recorded classification; Stale says that classification's request began more than three
// intervals before the database's time (§6.3).
type dependencyBody struct {
	ID              string     `json:"id"`
	Provider        string     `json:"provider"`
	Object          string     `json:"object"`
	Version         int64      `json:"version"`
	Created         string     `json:"created"`
	Class           string     `json:"class"`
	Reason          *string    `json:"reason"`
	FirstRetainedAt *time.Time `json:"firstRetainedAt"`
	UnknownSince    *time.Time `json:"unknownSince"`
	ObservedFrom    time.Time  `json:"observedFrom"`
	RecordedAt      time.Time  `json:"recordedAt"`
	Stale           bool       `json:"stale"`
}

// dependencyPage is GET /dependencies: a page of dependencies and the last completed pass's time,
// null before the first.
type dependencyPage struct {
	Items    []dependencyBody `json:"items"`
	Next     string           `json:"next,omitempty"`
	LastPass *time.Time       `json:"lastPass"`
}

// dependencyReleaseBody is one release naming a dependency, with the records that name it.
type dependencyReleaseBody struct {
	Release     string                 `json:"release"`
	Cluster     string                 `json:"cluster"`
	PublishedAt time.Time              `json:"publishedAt"`
	Records     []dependencyRecordBody `json:"records"`
}

// dependencyRecordBody is a machine's dependency record kind (compilation §9).
type dependencyRecordBody struct {
	Machine string `json:"machine"`
	Kind    string `json:"kind"`
}

// alertBody is one recorded alert (dependency monitor §5.1, §6.2). A monitor-stalled alert
// concerns no dependency: its dependency fields are null.
type alertBody struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	Dependency   *string    `json:"dependency"`
	Provider     *string    `json:"provider"`
	Object       *string    `json:"object"`
	Version      *int64     `json:"version"`
	Created      *string    `json:"created"`
	Class        *string    `json:"class"`
	Reason       *string    `json:"reason"`
	Releases     []string   `json:"releases"`
	Deletion     *time.Time `json:"deletion"`
	ObservedFrom *time.Time `json:"observedFrom"`
	AnswerDate   *time.Time `json:"answerDate"`
	Epoch        string     `json:"epoch"`
	RecordedAt   time.Time  `json:"recordedAt"`
}
