package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// The source reads (PA §9.2, §9.3): heads, a head's revisions and one revision, for fragments,
// profiles and assignments. Every one is readable by any role; a fragment revision's document is
// sanitized (compilation §2), so none carries a secret value.

// fragmentBody is a fragment head; Revision is null once removed (§3.1).
type fragmentBody struct {
	ID           string    `json:"id"`
	Cluster      string    `json:"cluster"`
	Scope        string    `json:"scope"`
	Name         string    `json:"name"`
	Layer        string    `json:"layer"`
	Revision     *string   `json:"revision"`
	HeadRevision int       `json:"headRevision"`
	CreatedAt    time.Time `json:"createdAt"`
	token        string
}

type profileBody struct {
	ID           string    `json:"id"`
	Cluster      string    `json:"cluster"`
	Scope        string    `json:"scope"`
	Name         string    `json:"name"`
	Revision     *string   `json:"revision"`
	HeadRevision int       `json:"headRevision"`
	CreatedAt    time.Time `json:"createdAt"`
	token        string
}

type assignmentBody struct {
	ID           string    `json:"id"`
	Cluster      string    `json:"cluster"`
	Machine      string    `json:"machine"`
	Revision     *string   `json:"revision"`
	HeadRevision int       `json:"headRevision"`
	CreatedAt    time.Time `json:"createdAt"`
	token        string
}

type fragmentRevisionBody struct {
	ID        string    `json:"id"`
	Cluster   string    `json:"cluster"`
	Name      string    `json:"name"`
	Layer     string    `json:"layer"`
	Document  string    `json:"document"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"createdAt"`
}

type profileRevisionBody struct {
	ID        string    `json:"id"`
	Cluster   string    `json:"cluster"`
	Name      string    `json:"name"`
	Fragments []string  `json:"fragments"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"createdAt"`
}

type assignmentRevisionBody struct {
	ID        string              `json:"id"`
	Cluster   string              `json:"cluster"`
	Machine   string              `json:"machine"`
	Profiles  []string            `json:"profiles"`
	Fragments map[string][]string `json:"fragments"`
	Author    string              `json:"author"`
	CreatedAt time.Time           `json:"createdAt"`
}

type scanner interface{ Scan(...any) error }

const (
	selectFragment = `SELECT id, cluster, scope, name, layer, head_revision_id, head_revision, created_at, etag_token FROM fragment`
	selectProfile  = `SELECT id, cluster, scope, name, head_revision_id, head_revision, created_at, etag_token FROM profile`
	selectAssign   = `SELECT id, cluster, machine, head_revision_id, head_revision, created_at, etag_token FROM assignment`

	selectFragmentRevision   = `SELECT r.id, r.cluster, r.name, r.layer, r.document, r.author, r.created_at FROM fragment_revision r`
	selectProfileRevision    = `SELECT r.id, r.cluster, r.name, r.author, r.created_at FROM profile_revision r`
	selectAssignmentRevision = `SELECT r.id, r.cluster, r.machine, r.author, r.created_at FROM assignment_revision r`
)

func scanFragment(r scanner) (*fragmentBody, error) {
	b := &fragmentBody{}
	err := r.Scan(&b.ID, &b.Cluster, &b.Scope, &b.Name, &b.Layer, &b.Revision, &b.HeadRevision, &b.CreatedAt, &b.token)
	b.CreatedAt = b.CreatedAt.UTC()
	return b, err
}

func scanProfile(r scanner) (*profileBody, error) {
	b := &profileBody{}
	err := r.Scan(&b.ID, &b.Cluster, &b.Scope, &b.Name, &b.Revision, &b.HeadRevision, &b.CreatedAt, &b.token)
	b.CreatedAt = b.CreatedAt.UTC()
	return b, err
}

func scanAssignment(r scanner) (*assignmentBody, error) {
	b := &assignmentBody{}
	err := r.Scan(&b.ID, &b.Cluster, &b.Machine, &b.Revision, &b.HeadRevision, &b.CreatedAt, &b.token)
	b.CreatedAt = b.CreatedAt.UTC()
	return b, err
}

func scanFragmentRevision(r scanner) (*fragmentRevisionBody, error) {
	b := &fragmentRevisionBody{}
	err := r.Scan(&b.ID, &b.Cluster, &b.Name, &b.Layer, &b.Document, &b.Author, &b.CreatedAt)
	b.CreatedAt = b.CreatedAt.UTC()
	return b, err
}

func scanProfileRevision(r scanner) (*profileRevisionBody, error) {
	b := &profileRevisionBody{Fragments: []string{}}
	err := r.Scan(&b.ID, &b.Cluster, &b.Name, &b.Author, &b.CreatedAt)
	b.CreatedAt = b.CreatedAt.UTC()
	return b, err
}

func scanAssignmentRevision(r scanner) (*assignmentRevisionBody, error) {
	b := &assignmentRevisionBody{Profiles: []string{}, Fragments: map[string][]string{}}
	err := r.Scan(&b.ID, &b.Cluster, &b.Machine, &b.Author, &b.CreatedAt)
	b.CreatedAt = b.CreatedAt.UTC()
	return b, err
}

// withPins reads the pinned fragment revisions of bs, in order, into them.
func withPins(ctx context.Context, tx *sql.Tx, bs []*profileRevisionBody) error {
	byID := map[string]*profileRevisionBody{}
	var ids []string
	for _, b := range bs {
		byID[b.ID], ids = b, append(ids, b.ID)
	}
	return eachRow(ctx, tx, `SELECT revision, fragment_revision FROM profile_revision_fragment
		WHERE revision = ANY (string_to_array($1, ',')) ORDER BY revision, position`, strings.Join(ids, ","), func(r *sql.Rows) error {
		var rev, frv string
		err := r.Scan(&rev, &frv)
		byID[rev].Fragments = append(byID[rev].Fragments, frv)
		return err
	})
}

// withSelections reads the profiles and per-layer fragments of bs, in order, into them.
func withSelections(ctx context.Context, tx *sql.Tx, bs []*assignmentRevisionBody) error {
	byID := map[string]*assignmentRevisionBody{}
	var ids []string
	for _, b := range bs {
		byID[b.ID], ids = b, append(ids, b.ID)
	}
	err := eachRow(ctx, tx, `SELECT revision, profile FROM assignment_revision_profile
		WHERE revision = ANY (string_to_array($1, ',')) ORDER BY revision, position`, strings.Join(ids, ","), func(r *sql.Rows) error {
		var rev, p string
		err := r.Scan(&rev, &p)
		byID[rev].Profiles = append(byID[rev].Profiles, p)
		return err
	})
	if err != nil {
		return err
	}
	return eachRow(ctx, tx, `SELECT revision, layer, fragment FROM assignment_revision_fragment
		WHERE revision = ANY (string_to_array($1, ',')) ORDER BY revision, layer, position`, strings.Join(ids, ","), func(r *sql.Rows) error {
		var rev, layer, f string
		err := r.Scan(&rev, &layer, &f)
		byID[rev].Fragments[layer] = append(byID[rev].Fragments[layer], f)
		return err
	})
}

func eachRow(ctx context.Context, tx *sql.Tx, query string, arg any, fn func(*sql.Rows) error) error {
	rows, err := tx.QueryContext(ctx, query, arg)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// headItem answers one head with its head revision and token as the ETag (§4.1).
func headItem[T any](p id.Prefix, query string, scan func(scanner) (T, error), tag func(T) string) readFunc {
	return item(p, func(ctx context.Context, tx *sql.Tx, v string) (string, any, error) {
		b, err := scan(tx.QueryRowContext(ctx, query+` WHERE id = $1`, v))
		if err != nil {
			return "", nil, err
		}
		return tag(b), b, nil
	})
}

// revisionsOf answers the revisions of the head the route's {id} names, published or not, in
// identifier order; a head the database does not hold is 404. query takes the last identifier
// listed ($1), the row limit ($2) and the head ($3).
func revisionsOf[T any](head, p id.Prefix, table, query string, scan func(scanner) (T, error), fill func(context.Context, *sql.Tx, []T) error) readFunc {
	return func(a *API, w http.ResponseWriter, q *request) {
		v := q.r.PathValue("id")
		readIn(a, w, q, func(ctx context.Context, tx *sql.Tx) (string, any, error) {
			notFound := refuse(http.StatusNotFound, "not-found", "no such resource")
			if id.MustHave(v, head) != nil {
				return "", nil, notFound
			}
			var ok bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE id = $1)`, v).Scan(&ok); err != nil {
				return "", nil, err
			} else if !ok {
				return "", nil, notFound
			}
			out, err := listRows(ctx, tx, q, p, query, func(r *sql.Rows) (T, string, error) {
				b, err := scan(r)
				return b, idOf(b), err
			}, v)
			if err == nil && fill != nil && len(out.Items) > 0 {
				err = fill(ctx, tx, out.Items)
			}
			return "", out, err
		})
	}
}

// idOf is a source body's identifier.
func idOf(b any) string {
	switch b := b.(type) {
	case *fragmentBody:
		return b.ID
	case *profileBody:
		return b.ID
	case *assignmentBody:
		return b.ID
	case *fragmentRevisionBody:
		return b.ID
	case *profileRevisionBody:
		return b.ID
	case *assignmentRevisionBody:
		return b.ID
	}
	return ""
}

func headList[T any](p id.Prefix, query string, scan func(scanner) (T, error)) readFunc {
	return listed(p, query+` WHERE id > $1 ORDER BY id LIMIT $2`, func(r *sql.Rows) (T, string, error) {
		b, err := scan(r)
		return b, idOf(b), err
	})
}

// revisionItem answers one revision; fill reads its rows.
func revisionItem[T any](p id.Prefix, query string, scan func(scanner) (T, error), fill func(context.Context, *sql.Tx, []T) error) readFunc {
	return item(p, func(ctx context.Context, tx *sql.Tx, v string) (string, any, error) {
		b, err := scan(tx.QueryRowContext(ctx, query+` WHERE r.id = $1`, v))
		if err == nil && fill != nil {
			err = fill(ctx, tx, []T{b})
		}
		return "", b, err
	})
}

// sourceReads are the routes' read functions by pattern.
var sourceReads = map[string]readFunc{
	"/fragments": headList(id.Fragment, selectFragment, scanFragment),
	"/fragments/{id}": headItem(id.Fragment, selectFragment, scanFragment,
		func(b *fragmentBody) string { return etag(b.HeadRevision, b.token) }),
	"/fragments/{id}/revisions": revisionsOf(id.Fragment, id.FragmentRevision, "fragment", selectFragmentRevision+
		` JOIN fragment h ON h.cluster = r.cluster AND h.name = r.name WHERE h.id = $3 AND r.id > $1 ORDER BY r.id LIMIT $2`,
		scanFragmentRevision, nil),
	"/fragment-revisions/{id}": revisionItem(id.FragmentRevision, selectFragmentRevision, scanFragmentRevision, nil),

	"/profiles": headList(id.Profile, selectProfile, scanProfile),
	"/profiles/{id}": headItem(id.Profile, selectProfile, scanProfile,
		func(b *profileBody) string { return etag(b.HeadRevision, b.token) }),
	"/profiles/{id}/revisions": revisionsOf(id.Profile, id.ProfileRevision, "profile", selectProfileRevision+
		` JOIN profile h ON h.cluster = r.cluster AND h.name = r.name WHERE h.id = $3 AND r.id > $1 ORDER BY r.id LIMIT $2`,
		scanProfileRevision, withPins),
	"/profile-revisions/{id}": revisionItem(id.ProfileRevision, selectProfileRevision, scanProfileRevision, withPins),

	"/assignments": headList(id.Assignment, selectAssign, scanAssignment),
	"/assignments/{id}": headItem(id.Assignment, selectAssign, scanAssignment,
		func(b *assignmentBody) string { return etag(b.HeadRevision, b.token) }),
	"/assignments/{id}/revisions": revisionsOf(id.Assignment, id.AssignmentRevision, "assignment", selectAssignmentRevision+
		` JOIN assignment h ON h.machine = r.machine WHERE h.id = $3 AND r.id > $1 ORDER BY r.id LIMIT $2`,
		scanAssignmentRevision, withSelections),
	"/assignment-revisions/{id}": revisionItem(id.AssignmentRevision, selectAssignmentRevision, scanAssignmentRevision, withSelections),
}
