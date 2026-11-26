package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/ginsys/bronzeward/internal/id"
)

// The routes of a draft's adoption entries (persistence-api.md §3.1.1, §9.2, §9.3): adopt a shared
// name at a revision, stop using it, and detach it into a copy of the cluster's own. Each is a
// plain T1 (§5). None takes a lock on the cluster's view or a shared head: publication compares
// the adoption counter an entry records and holds the head an adoption entry names.

func adoptionUpdateRoute() effectRoute {
	return effectRoute{action: "draft.adoption.update", input: func() input { return &adoptionInput{} }, effect: putAdoption}
}

func adoptionRemovalRoute() effectRoute {
	return effectRoute{action: "draft.adoption.remove", input: func() input { return &noBody{} }, effect: removeAdoption}
}

func detachmentRoute() effectRoute {
	return effectRoute{action: "draft.adoption.detach", input: func() input { return &noBody{} }, effect: detach}
}

// adoptionKinds is the kind each value of the routes' {kind} names.
var adoptionKinds = map[string]string{"fragments": "fragment", "profiles": "profile"}

// adoptionInput is an adoption entry's body (§9.3). Whether revision is one of the name's is
// checked in the transaction.
type adoptionInput struct {
	Revision string `json:"revision"`
}

func (in *adoptionInput) check(*API) error {
	if in.Revision == "" {
		return errors.New("revision is required")
	}
	return nil
}

// adoptionEntry is a draft's adoption entry, or its removal (Revision null), as a draft answers it
// (§9.3). Of is fragment or profile; Head the shared head of the name, if one exists; Base the
// cluster's adoption counter it was written from (null: no adoption).
type adoptionEntry struct {
	Kind     string  `json:"kind"`
	Of       string  `json:"of"`
	Name     string  `json:"name"`
	Head     *string `json:"head"`
	Base     *int    `json:"base"`
	Revision *string `json:"revision"`
}

// adoptionKind is an adoptionEntry's Kind.
const adoptionKind = "adoption"

type adoptionUpdate struct {
	Draft string        `json:"draft"`
	Entry adoptionEntry `json:"entry"`
}

// detachmentUpdate is a detachment's answer: the adoption's removal and the cluster entry it
// wrote.
type detachmentUpdate struct {
	Draft string        `json:"draft"`
	Entry adoptionEntry `json:"entry"`
	Copy  sourceEntry   `json:"copy"`
}

// adoptionState is what a write of a name's adoption reads, in the draft it holds (§3.1.1): the
// shared head as published (head nil: absent; current nil: removed); the cluster's published
// adoption row (counter nil: never adopted) with the revision it holds ("" none); and introduced,
// the revision the draft's shared entry of the name would set an adoption to, an adoption entry's
// or a shared edit's ("" none).
type adoptionState struct {
	d          lockedDraft
	kind, name string
	head       *string
	current    *string
	counter    *int
	adopted    string
	introduced string
}

func readAdoption(ctx context.Context, tx *sql.Tx, q *request) (adoptionState, error) {
	var s adoptionState
	var err error
	if s.d, err = lockDraft(ctx, tx, q); err != nil {
		return s, err
	}
	kind, ok := adoptionKinds[q.r.PathValue("kind")]
	if !ok {
		return s, refuse(http.StatusNotFound, "not-found", "an adoption is of fragments or of profiles")
	}
	k, err := keyOf(ctx, tx, q, s.d, kind)
	if err != nil {
		return s, err
	}
	s.kind, s.name, k.shared = kind, k.name, true
	if s.head, _, s.current, err = headOf(ctx, tx, s.d, k); err != nil {
		return s, err
	}
	// One read of the row: its counter and its revision are of one publication.
	var counter int
	var adopted sql.NullString
	switch err := tx.QueryRowContext(ctx, `SELECT adoption_revision, coalesce(fragment_revision, profile_revision)
		FROM library_adoption WHERE cluster = $1 AND kind = $2 AND name = $3`, s.d.cluster, kind, s.name).
		Scan(&counter, &adopted); {
	case err == nil:
		s.counter, s.adopted = &counter, adopted.String
	case !errors.Is(err, sql.ErrNoRows):
		return s, err
	}
	_, _, introduced, err := entryOf(ctx, tx, s.d, k)
	if introduced != nil {
		s.introduced = *introduced
	}
	return s, err
}

// entry is the adoption entry or removal (revision nil) the write answers, from the counter read.
func (s adoptionState) entry(revision *string) adoptionEntry {
	return adoptionEntry{Kind: adoptionKind, Of: s.kind, Name: s.name, Head: s.head, Base: s.counter, Revision: revision}
}

// set puts e in the draft's shared slot of its name, replacing whatever it held.
func (s adoptionState) set(ctx context.Context, tx *sql.Tx, e adoptionEntry) error {
	column := headKinds[s.kind].revision
	_, err := tx.ExecContext(ctx, `INSERT INTO draft_source_entry (draft, cluster, kind, name, scope, `+column+`, base, adoption)
		VALUES ($1, $2, $3, $4, 'library', $5, $6, true)
		ON CONFLICT (draft, kind, name, scope) WHERE kind <> 'assignment'
		DO UPDATE SET `+column+` = excluded.`+column+`, base = excluded.base, adoption_base = NULL, adoption = true`,
		s.d.id, s.d.cluster, s.kind, s.name, e.Revision, e.Base)
	return err
}

// unadopt is the removal of the cluster's use of the name (§3.1.1 table, the adoption removal
// rows): of the revision the adoption holds, the removal staged from its counter, with the source
// that revision; or, where the cluster has no row and the draft's shared entry would make one, that
// entry dropped, nothing staged, with the source its revision. Anything else is 404.
func (s adoptionState) unadopt(ctx context.Context, tx *sql.Tx) (e adoptionEntry, source string, err error) {
	switch {
	case s.adopted != "":
		e = s.entry(nil)
		return e, s.adopted, s.set(ctx, tx, e)
	case s.counter == nil && s.introduced != "":
		_, err = tx.ExecContext(ctx, `DELETE FROM draft_source_entry WHERE draft = $1 AND kind = $2 AND name = $3 AND scope = 'library'`,
			s.d.id, s.kind, s.name)
		return s.entry(nil), s.introduced, err
	}
	return e, "", refuse(http.StatusNotFound, "not-found", "the cluster has no adoption of this name").
		with("kind", s.kind).with("name", s.name)
}

// putAdoption records an adoption entry, held to the rules that read it and the published state
// alone (§3.1.1): the name's shared head holds a revision; the entry names a shared revision of
// that name, in its layer, with no reference rows.
func putAdoption(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*adoptionInput)
	s, err := readAdoption(ctx, tx, q)
	if err != nil {
		return result{}, err
	}
	invalid := func(detail string) *refusal {
		return refuse(http.StatusUnprocessableEntity, "validation-failed", detail).with("path", "revision")
	}
	if s.current == nil {
		return result{}, invalid("the library holds no revision of this name to adopt").
			with("rule", "library-removed").with("kind", s.kind).with("name", s.name)
	}
	// A revision no publication wrote can carry another layer than the head's (two drafts that
	// introduced the name), which the name does not keep.
	query := `SELECT true, false FROM profile_revision WHERE id = $1 AND cluster IS NULL AND name = $2 AND $3 <> ''`
	if s.kind == "fragment" {
		query = `SELECT r.layer = h.layer, EXISTS (SELECT FROM fragment_reference f WHERE f.revision = r.id)
			FROM fragment_revision r JOIN fragment h ON h.id = $3 WHERE r.id = $1 AND r.cluster IS NULL AND r.name = $2`
	}
	var layered, referencing bool
	switch err := tx.QueryRowContext(ctx, query, in.Revision, s.name, *s.head).Scan(&layered, &referencing); {
	case errors.Is(err, sql.ErrNoRows):
		return result{}, invalid("revision must be a revision of this shared name").with("revision", in.Revision)
	case err != nil:
		return result{}, err
	case !layered:
		return result{}, invalid("the revision is in another layer than its name").with("revision", in.Revision)
	case referencing:
		return result{}, invalid("a shared revision with a secret reference cannot be adopted").with("rule", "library-reference")
	}
	e := s.entry(&in.Revision)
	if err := s.set(ctx, tx, e); err != nil {
		return result{}, err
	}
	return s.d.respond(ctx, tx, adoptionUpdate{Draft: s.d.id, Entry: e}, in.Revision)
}

func removeAdoption(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
	s, err := readAdoption(ctx, tx, q)
	if err != nil {
		return result{}, err
	}
	e, _, err := s.unadopt(ctx, tx)
	if err != nil {
		return result{}, err
	}
	return s.d.respond(ctx, tx, adoptionUpdate{Draft: s.d.id, Entry: e})
}

// detach stops the cluster following a name (§3.1.1 "Stopping"): in this one transaction it
// removes the adoption, as removeAdoption does, and writes the cluster's entry of a copy of the
// revision it followed, marked so that publication holds a profile's pins under
// detach-pin-not-in-view. The copy is a new revision of the cluster's, authored by the caller.
func detach(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
	s, err := readAdoption(ctx, tx, q)
	if err != nil {
		return result{}, err
	}
	e, source, err := s.unadopt(ctx, tx)
	if err != nil {
		return result{}, err
	}
	copied, document, err := s.copyOf(ctx, tx, source, q.principal.ID)
	if err != nil {
		return result{}, err
	}
	own, err := setEntry(ctx, tx, s.d, sourceKey{kind: s.kind, name: s.name}, copied)
	if err != nil {
		return result{}, err
	}
	own.Document = document
	if _, err := tx.ExecContext(ctx, `UPDATE draft_source_entry SET detached = true
		WHERE draft = $1 AND kind = $2 AND name = $3 AND scope = 'cluster'`, s.d.id, s.kind, s.name); err != nil {
		return result{}, err
	}
	return s.d.respond(ctx, tx, detachmentUpdate{Draft: s.d.id, Entry: e, Copy: own}, copied)
}

// copyOf writes the cluster's revision that copies the shared revision source, and returns it with
// a fragment's document. A shared revision holds no reference rows, so the copy has none; a
// profile's pins are the source's, of shared revisions. A refusal rolls the transaction back.
func (s adoptionState) copyOf(ctx context.Context, tx *sql.Tx, source, author string) (string, *string, error) {
	if s.kind == "profile" {
		prv := id.New(id.ProfileRevision)
		if _, err := tx.ExecContext(ctx, `INSERT INTO profile_revision (id, cluster, name, author, created_at)
			VALUES ($1, $2, $3, $4, now())`, prv, s.d.cluster, s.name, author); err != nil {
			return "", nil, err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO profile_revision_fragment (revision, cluster, position, fragment_revision, library)
			SELECT $1, $2, position, fragment_revision, true FROM profile_revision_fragment WHERE revision = $3`,
			prv, s.d.cluster, source)
		return prv, nil, err
	}
	// The cluster's own head of the name keeps its layer, a removed one too (§3.1).
	var other bool
	switch err := tx.QueryRowContext(ctx, `SELECT h.layer <> r.layer FROM fragment h, fragment_revision r
		WHERE h.cluster = $1 AND h.name = $2 AND r.id = $3`, s.d.cluster, s.name, source).Scan(&other); {
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return "", nil, err
	case other:
		return "", nil, layerRefusal("The cluster's fragment of this name keeps the layer it was published in.", s.name)
	}
	frv := id.New(id.FragmentRevision)
	var document string
	err := tx.QueryRowContext(ctx, `INSERT INTO fragment_revision (id, cluster, name, layer, document, author, created_at, embedded)
		SELECT $1, $2, name, layer, document, $3, now(), embedded FROM fragment_revision WHERE id = $4 RETURNING document`,
		frv, s.d.cluster, author, source).Scan(&document)
	return frv, &document, err
}
