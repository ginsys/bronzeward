package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// readIn answers a read route: fn runs in a transaction that holds the installation state FOR
// SHARE, as readActs does, so an entry committed since routing refuses an earlier epoch's service
// token and the epoch answered is the one read. The transaction ends before the response is
// written (§5 rule 1).
func readIn(a *API, w http.ResponseWriter, q *request, fn func(ctx context.Context, tx *sql.Tx) (etag string, body any, err error)) {
	if a.o.beforeRead != nil {
		a.o.beforeRead()
	}
	etag, body, err := func() (string, any, error) {
		ctx := q.r.Context()
		tx, err := a.db.BeginTx(ctx, nil)
		if err != nil {
			return "", nil, fmt.Errorf("%w: %w", errUnavailable, err)
		}
		defer func() { _ = tx.Rollback() }() // it writes nothing
		if err := tx.QueryRowContext(ctx, `SELECT epoch, recovery_mode FROM installation_state FOR SHARE`).Scan(&q.epoch, &q.recovery); err != nil {
			return "", nil, err
		}
		if ref := staleToken(q); ref != nil {
			return "", nil, ref
		}
		return fn(ctx, tx)
	}()
	setEpoch(w, q) // the epoch the read held; a 401 drops it again
	if err != nil {
		a.fail(w, q, err)
		return
	}
	if etag != "" {
		w.Header().Set("ETag", etag)
	}
	writeJSON(w, "application/json", http.StatusOK, body)
}

// listPage is a collection's answer (§9.1).
type listPage[T any] struct {
	Items []T    `json:"items"`
	Next  string `json:"next,omitempty"`
}

// listed answers a collection of entity p in identifier order. query selects the page: its $1 is
// the last identifier listed (the empty string for the first page), $2 the row limit, and it
// orders by the identifier; scan returns one row's item and identifier.
func listed[T any](p id.Prefix, query string, scan func(*sql.Rows) (T, string, error)) readFunc {
	return func(a *API, w http.ResponseWriter, q *request) {
		readIn(a, w, q, func(ctx context.Context, tx *sql.Tx) (string, any, error) {
			out := listPage[T]{Items: []T{}}
			limit, after, ref := page(q, p)
			if ref != nil {
				return "", nil, ref
			}
			rows, err := tx.QueryContext(ctx, query, after, limit+1)
			if err != nil {
				return "", nil, err
			}
			defer rows.Close()
			var ids []string
			for rows.Next() {
				it, itemID, err := scan(rows)
				if err != nil {
					return "", nil, err
				}
				out.Items, ids = append(out.Items, it), append(ids, itemID)
			}
			if err := rows.Err(); err != nil {
				return "", nil, err
			}
			if len(out.Items) > limit {
				out.Items = out.Items[:limit]
				out.Next = makeCursor(q.epoch, ids[limit-1])
			}
			return "", out, nil
		})
	}
}

// item answers one entity p named by the route's {id}. It takes no query (§9.1: unknown fields
// are refused). get returns sql.ErrNoRows for an identifier the database does not hold; that, and
// an identifier of another entity, is 404.
func item(p id.Prefix, get func(ctx context.Context, tx *sql.Tx, id string) (etag string, body any, err error)) readFunc {
	return func(a *API, w http.ResponseWriter, q *request) {
		v := q.r.PathValue("id")
		readIn(a, w, q, func(ctx context.Context, tx *sql.Tx) (string, any, error) {
			if q.r.URL.RawQuery != "" {
				return "", nil, refuse(http.StatusBadRequest, "invalid-request", "an item read takes no query")
			}
			if id.MustHave(v, p) != nil {
				return "", nil, refuse(http.StatusNotFound, "not-found", "no such resource")
			}
			etag, body, err := get(ctx, tx, v)
			if errors.Is(err, sql.ErrNoRows) {
				return "", nil, refuse(http.StatusNotFound, "not-found", "no such resource")
			}
			return etag, body, err
		})
	}
}

const selectCluster = `SELECT id, name, endpoint, contract FROM cluster`

func scanCluster(r interface{ Scan(...any) error }) (clusterBody, error) {
	var b clusterBody
	err := r.Scan(&b.ID, &b.Name, &b.Endpoint, &b.Contract)
	return b, err
}

var (
	listClusters = listed(id.Cluster, selectCluster+` WHERE id > $1 ORDER BY id LIMIT $2`, func(r *sql.Rows) (clusterBody, string, error) {
		b, err := scanCluster(r)
		return b, b.ID, err
	})
	getCluster = item(id.Cluster, func(ctx context.Context, tx *sql.Tx, v string) (string, any, error) {
		b, err := scanCluster(tx.QueryRowContext(ctx, selectCluster+` WHERE id = $1`, v))
		return "", b, err
	})
)

const selectMachine = `SELECT m.id, m.cluster, m.smbios_uuid::text, m.serial, m.frozen, m.scope_state, s.desired,
		s.applied_release, s.applied_source
	FROM machine m JOIN machine_state s ON s.machine = m.id`

func scanMachine(r interface{ Scan(...any) error }) (machineBody, error) {
	var b machineBody
	var rel, source sql.NullString
	err := r.Scan(&b.ID, &b.Cluster, &b.Hardware.SMBIOSUUID, &b.Hardware.Serial, &b.Frozen, &b.ScopeState, &b.Desired, &rel, &source)
	if rel.Valid {
		b.Applied = &applied{Release: rel.String, Source: source.String}
	}
	return b, err
}

var (
	listMachines = listed(id.Machine, selectMachine+` WHERE m.id > $1 ORDER BY m.id LIMIT $2`, func(r *sql.Rows) (machineBody, string, error) {
		b, err := scanMachine(r)
		return b, b.ID, err
	})
	getMachine = item(id.Machine, func(ctx context.Context, tx *sql.Tx, v string) (string, any, error) {
		b, err := scanMachine(tx.QueryRowContext(ctx, selectMachine+` WHERE m.id = $1`, v))
		return "", b, err
	})
)

// draftRow is a draft with its ETag, before its entries are read.
type draftRow struct {
	draftBody
	token string
}

const selectDraft = `SELECT id, cluster, title, state, revision, etag_token FROM draft`

// draftLock is the clause a draft read takes its draft rows with. The entries are read by a second
// statement, and under read committed (§5 rule 3) a draft transaction could commit between the
// two, pairing revision N's ETag with revision N+1's entries. Every write of a draft's entries
// holds the draft FOR UPDATE (T1), so FOR SHARE waits for one in progress and holds off the next
// until the read ends (rule 2); the draft comes after the installation state (rule 5).
func draftLock(a *API) string {
	if a.o.noDraftLock {
		return ""
	}
	return " FOR SHARE"
}

func scanDraft(r interface{ Scan(...any) error }) (draftRow, error) {
	var d draftRow
	err := r.Scan(&d.ID, &d.Cluster, &d.Title, &d.State, &d.Revision, &d.token)
	d.Entries = []draftEntry{}
	return d, err
}

// withEntries reads the entries of ds, in machine order, into them.
func withEntries(ctx context.Context, tx *sql.Tx, ds []*draftBody) error {
	if len(ds) == 0 {
		return nil
	}
	byID := map[string]*draftBody{}
	var ids []string
	for _, d := range ds {
		byID[d.ID] = d
		ids = append(ids, d.ID)
	}
	rows, err := tx.QueryContext(ctx, `SELECT draft, kind, machine, import_base_revision FROM draft_entry
		WHERE draft = ANY (string_to_array($1, ',')) ORDER BY draft, machine`, strings.Join(ids, ","))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		var e draftEntry
		if err := rows.Scan(&d, &e.Kind, &e.Machine, &e.Revision); err != nil {
			return err
		}
		byID[d].Entries = append(byID[d].Entries, e)
	}
	return rows.Err()
}

func listDrafts(a *API, w http.ResponseWriter, q *request) {
	readIn(a, w, q, func(ctx context.Context, tx *sql.Tx) (string, any, error) {
		limit, after, ref := page(q, id.Draft)
		if ref != nil {
			return "", nil, ref
		}
		out := listPage[*draftBody]{Items: []*draftBody{}}
		err := func() error {
			rows, err := tx.QueryContext(ctx, selectDraft+` WHERE id > $1 ORDER BY id LIMIT $2`+draftLock(a), after, limit+1)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				d, err := scanDraft(rows)
				if err != nil {
					return err
				}
				out.Items = append(out.Items, &d.draftBody)
			}
			return rows.Err()
		}()
		if err != nil {
			return "", nil, err
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			out.Next = makeCursor(q.epoch, out.Items[limit-1].ID)
		}
		return "", out, withEntries(ctx, tx, out.Items)
	})
}

func getDraft(a *API, w http.ResponseWriter, q *request) {
	item(id.Draft, func(ctx context.Context, tx *sql.Tx, v string) (string, any, error) {
		d, err := scanDraft(tx.QueryRowContext(ctx, selectDraft+` WHERE id = $1`+draftLock(a), v))
		if err != nil {
			return "", nil, err
		}
		return etag(d.Revision, d.token), &d.draftBody, withEntries(ctx, tx, []*draftBody{&d.draftBody})
	})(a, w, q)
}

type operationSubject struct {
	Draft         string `json:"draft"`
	DraftRevision int    `json:"draftRevision"`
}

type createdBy struct {
	Principal string `json:"principal"`
	Role      string `json:"role"`
}

// operationBody is §8.3's operation resource.
type operationBody struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	State     string            `json:"state"`
	Epoch     string            `json:"epoch"`
	LastEvent int               `json:"lastEvent"`
	Subject   *operationSubject `json:"subject"`
	CreatedBy *createdBy        `json:"createdBy"`
	CreatedAt time.Time         `json:"createdAt"`
	Result    json.RawMessage   `json:"result"`
	Error     json.RawMessage   `json:"error"`
}

var getOperation = item(id.Operation, func(ctx context.Context, tx *sql.Tx, v string) (string, any, error) {
	var b operationBody
	var draft, principal, role, res, fail sql.NullString
	var revision sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT id, kind, state, epoch, last_event, draft, draft_revision, created_by, created_role,
			created_at, result::text, error::text
		FROM operation WHERE id = $1`, v).Scan(&b.ID, &b.Kind, &b.State, &b.Epoch, &b.LastEvent, &draft, &revision,
		&principal, &role, &b.CreatedAt, &res, &fail)
	if err != nil {
		return "", nil, err
	}
	// A NULL result or error stays a nil RawMessage, which marshals as null (§8.3).
	if res.Valid {
		b.Result = json.RawMessage(res.String)
	}
	if fail.Valid {
		b.Error = json.RawMessage(fail.String)
	}
	if draft.Valid {
		b.Subject = &operationSubject{Draft: draft.String, DraftRevision: int(revision.Int64)}
	}
	if principal.Valid {
		b.CreatedBy = &createdBy{Principal: principal.String, Role: role.String}
	}
	b.CreatedAt = b.CreatedAt.UTC()
	return "", b, nil
})
