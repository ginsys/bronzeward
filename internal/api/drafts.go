package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ginsys/bronzeward/internal/id"
)

// The draft update routes for profiles and assignments, the removals of all three kinds and the
// discard (persistence-api.md §3.1, §9.2, §9.3, choice §17.31). None of them can carry a secret
// value, so each is a plain T1 (§5) under the SHA-256 fingerprint; the fragment PUT, which
// ingests, is not here.

func profileUpdate() effectRoute {
	return effectRoute{action: "draft.profile.update", input: func() input { return &profileInput{} }, effect: updateProfile}
}

func assignmentUpdate() effectRoute {
	return effectRoute{action: "draft.assignment.update", input: func() input { return &assignmentInput{} }, effect: updateAssignment}
}

func draftDiscard() effectRoute {
	return effectRoute{action: "draft.discard", input: func() input { return &discardInput{} }, effect: discardDraft}
}

// scopeCluster and scopeLibrary are the two scopes of a fragment or profile (§3.1, §3.1.1).
const (
	scopeCluster = "cluster"
	scopeLibrary = "library"
	// scopeQuery is a scoped DELETE's query parameter (§9.2).
	scopeQuery = "scope"
)

// checkScope is a body's scope member: absent is the cluster's.
func checkScope(scope string) error {
	if scope != "" && scope != scopeCluster && scope != scopeLibrary {
		return errors.New("scope must be cluster or library")
	}
	return nil
}

func fragmentRemoval() effectRoute   { return removal("fragment") }
func profileRemoval() effectRoute    { return removal("profile") }
func assignmentRemoval() effectRoute { return removal("assignment") }

func removal(kind string) effectRoute {
	return effectRoute{action: "draft." + kind + ".remove", input: func() input { return &noBody{} }, scoped: kind != "assignment",
		effect: func(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
			return removeSource(ctx, tx, q, kind)
		}}
}

// noBody is a DELETE route's input: it takes no body (§9.3), and decodeBody refuses one.
type noBody struct{}

func (*noBody) check(*API) error { return nil }

// sourceName is a fragment or profile name (choice §17.31), as the schema's source_name domain.
var sourceName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func validName(s string) bool { return len(s) <= 63 && sourceName.MatchString(s) }

// sourceEntry is a draft's fragment, profile or assignment entry (§3.1, §9.3). Head is the head of
// its name or machine, if one exists; Base the head revision it was edited from (null: absent);
// Revision the proposed revision (null: a removal). A shared entry (§3.1.1) has Scope library,
// absent otherwise, its head the shared one; a shared edit also has AdoptionBase, the counter of
// the cluster's adoption it was edited from (null: none).
type sourceEntry struct {
	Kind     string  `json:"kind"`
	Name     string  `json:"name,omitempty"`
	Machine  string  `json:"machine,omitempty"`
	Head     *string `json:"head"`
	Base     *int    `json:"base"`
	Revision *string `json:"revision"`
	Document *string `json:"document,omitempty"` // a fragment update's sanitized document
	Scope    string  `json:"scope,omitempty"`
	// AdoptionBase is a number or null on a shared edit, and absent on every other entry.
	AdoptionBase json.RawMessage `json:"adoptionBase,omitempty"`
}

// adoptionBase is a shared edit's adoption base as an entry answers it.
func adoptionBase(counter sql.NullInt64) json.RawMessage {
	if !counter.Valid {
		return json.RawMessage("null")
	}
	return json.RawMessage(strconv.FormatInt(counter.Int64, 10))
}

// sourceUpdate is a draft update's answer (§9.3); a fragment update names its ingestion.
type sourceUpdate struct {
	Draft     string      `json:"draft"`
	Entry     sourceEntry `json:"entry"`
	Ingestion string      `json:"ingestion,omitempty"`
}

// lockedDraft is a draft its request's transaction holds FOR UPDATE.
type lockedDraft struct {
	id, cluster string
	revision    int
}

// lockDraft is T1's draft check, which a discard (T11) shares (§3.1, §5): the draft FOR UPDATE,
// open, at the request's If-Match, with no publication of it queued or running.
func lockDraft(ctx context.Context, tx *sql.Tx, q *request) (lockedDraft, error) {
	d := lockedDraft{id: q.r.PathValue("id")}
	if id.MustHave(d.id, id.Draft) != nil {
		return d, refuse(http.StatusNotFound, "not-found", "no such draft") // §9.4: the path's text is not repeated
	}
	var state, token string
	switch err := tx.QueryRowContext(ctx, `SELECT cluster, state, revision, etag_token FROM draft WHERE id = $1 FOR UPDATE`,
		d.id).Scan(&d.cluster, &state, &d.revision, &token); {
	case errors.Is(err, sql.ErrNoRows):
		return d, refuse(http.StatusNotFound, "not-found", "no such draft").with("draft", d.id)
	case err != nil:
		return d, err
	case state != "open":
		return d, refuse(http.StatusConflict, "conflict", "the draft is "+state).with("draft", d.id)
	case etag(d.revision, token) != q.ifMatch:
		return d, refuse(http.StatusPreconditionFailed, "precondition-failed", "the draft has moved").with("draft", d.id)
	}
	var pub string
	switch err := tx.QueryRowContext(ctx, `SELECT id FROM operation WHERE draft = $1 AND kind = 'publish'
		AND state IN ('queued', 'running') ORDER BY id LIMIT 1`, d.id).Scan(&pub); {
	case err == nil:
		return d, refuse(http.StatusConflict, "conflict", "a publication of the draft is active").
			with("draft", d.id).with("operation", pub)
	case !errors.Is(err, sql.ErrNoRows):
		return d, err
	}
	return d, nil
}

// lockHeads holds, FOR SHARE, the fragment and profile heads an update's pins and selections are
// checked against (§5 rule 2), by id and before the draft (rule 5), so a publication advancing or
// removing one (T3, FOR UPDATE) is waited for and its result is what the update checks. The draft's
// cluster names them; a draft that does not exist locks nothing and lockDraft refuses it. Pins name
// their fragment through their immutable revision row. It returns the heads it holds: a head a
// publication creates after this pass is not among them, and the checks treat its name as having
// no head, as it had when the heads were taken.
func lockHeads(ctx context.Context, tx *sql.Tx, q *request, pins, fragments, profiles []string) (map[string]bool, error) {
	held := map[string]bool{}
	draft := q.r.PathValue("id")
	if id.MustHave(draft, id.Draft) != nil {
		return held, nil
	}
	var cluster string
	switch err := tx.QueryRowContext(ctx, `SELECT cluster FROM draft WHERE id = $1`, draft).Scan(&cluster); {
	case errors.Is(err, sql.ErrNoRows):
		return held, nil
	case err != nil:
		return nil, err
	}
	// fragment ids (frg_) sort before profile ids (prf_), so this is one pass in id order.
	for _, s := range []struct {
		query string
		args  []any
	}{
		{`SELECT id FROM fragment WHERE cluster = $1 AND (name = ANY (string_to_array($2, ','))
			OR name IN (SELECT name FROM fragment_revision WHERE cluster = $1 AND id = ANY (string_to_array($3, ','))))
			ORDER BY id FOR SHARE`, []any{cluster, strings.Join(fragments, ","), strings.Join(pins, ",")}},
		{`SELECT id FROM profile WHERE cluster = $1 AND name = ANY (string_to_array($2, ',')) ORDER BY id FOR SHARE`,
			[]any{cluster, strings.Join(profiles, ",")}},
	} {
		rows, err := tx.QueryContext(ctx, s.query, s.args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var h string
			if err := rows.Scan(&h); err != nil {
				_ = rows.Close()
				return nil, err
			}
			held[h] = true
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return held, nil
}

// heldHead reads the head of k as headOf does, but as no head unless lockHeads holds it.
func heldHead(ctx context.Context, tx *sql.Tx, d lockedDraft, k sourceKey, held map[string]bool) (current *string, err error) {
	head, _, current, err := headOf(ctx, tx, d, k)
	if err != nil || head == nil || !held[*head] {
		return nil, err
	}
	return current, nil
}

// advance moves a locked draft to its next revision and returns the new ETag.
func (d lockedDraft) advance(ctx context.Context, tx *sql.Tx) (string, error) {
	token := etagToken()
	if _, err := tx.ExecContext(ctx, `UPDATE draft SET revision = revision + 1, etag_token = $2 WHERE id = $1`, d.id, token); err != nil {
		return "", err
	}
	return etag(d.revision+1, token), nil
}

// sourceKey names an entry: a fragment or profile by name and scope, an assignment by machine.
type sourceKey struct {
	kind, name, machine string
	shared              bool
}

func (k sourceKey) scope() string {
	if k.shared {
		return scopeLibrary
	}
	return scopeCluster
}

// keyOf reads the entry's name or machine from the path. A machine must be in the draft's cluster.
func keyOf(ctx context.Context, tx *sql.Tx, q *request, d lockedDraft, kind string) (sourceKey, error) {
	k := sourceKey{kind: kind}
	if kind != "assignment" {
		k.name = q.r.PathValue("name")
		if !validName(k.name) {
			return k, refuse(http.StatusBadRequest, "invalid-request", "the name must be 1 to 63 lowercase letters, digits and inner hyphens")
		}
		return k, nil
	}
	k.machine = q.r.PathValue("machine")
	if id.MustHave(k.machine, id.Machine) != nil {
		return k, refuse(http.StatusNotFound, "not-found", "no such machine in the draft's cluster")
	}
	var ok bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM machine WHERE id = $1 AND cluster = $2)`,
		k.machine, d.cluster).Scan(&ok); err != nil {
		return k, err
	}
	if !ok {
		return k, refuse(http.StatusNotFound, "not-found", "no such machine in the draft's cluster").with("machine", k.machine)
	}
	return k, nil
}

// headOf reads the head of k, in the draft's cluster or, for a shared key, the shared one: its id,
// head revision and current revision (nil once removed). No head is all nil.
func headOf(ctx context.Context, tx *sql.Tx, d lockedDraft, k sourceKey) (head *string, base *int, current *string, err error) {
	var q string
	args := []any{d.cluster, k.name}
	switch {
	case k.shared:
		q, args = `SELECT id, head_revision, head_revision_id FROM `+headKinds[k.kind].table+
			` WHERE owner = 'library' AND name = $1`, []any{k.name}
	case k.kind == "fragment":
		q = `SELECT id, head_revision, head_revision_id FROM fragment WHERE cluster = $1 AND name = $2`
	case k.kind == "profile":
		q = `SELECT id, head_revision, head_revision_id FROM profile WHERE cluster = $1 AND name = $2`
	default:
		q, args = `SELECT id, head_revision, head_revision_id FROM assignment WHERE cluster = $1 AND machine = $2`, []any{d.cluster, k.machine}
	}
	var h string
	var b int
	var cur sql.NullString
	switch err := tx.QueryRowContext(ctx, q, args...).Scan(&h, &b, &cur); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil, nil, nil
	case err != nil:
		return nil, nil, nil, err
	}
	if cur.Valid {
		current = &cur.String
	}
	return &h, &b, current, nil
}

// entryOf reads the draft's entry for k: whether it exists, its base and its proposed revision.
func entryOf(ctx context.Context, tx *sql.Tx, d lockedDraft, k sourceKey) (found bool, base *int, rev *string, err error) {
	var b sql.NullInt64
	var r sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT base, COALESCE(fragment_revision, profile_revision, assignment_revision)
		FROM draft_source_entry WHERE draft = $1 AND kind = $2 AND name IS NOT DISTINCT FROM $3 AND machine IS NOT DISTINCT FROM $4
			AND scope = $5`,
		d.id, k.kind, nullable(k.name), nullable(k.machine), k.scope()).Scan(&b, &r)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil, nil, nil
	case err != nil:
		return false, nil, nil, err
	}
	if b.Valid {
		n := int(b.Int64)
		base = &n
	}
	if r.Valid {
		rev = &r.String
	}
	return true, base, rev, nil
}

func nullable(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// setEntry records the draft's entry for k with revision rev (empty: a removal) and answers it. A
// new entry takes the head's revision as its base (absent with no head); an existing one keeps the
// base it was first edited from, so a head published since is caught as stale at publication
// (§4.2) instead of being overwritten. A shared entry is setShared's.
func setEntry(ctx context.Context, tx *sql.Tx, d lockedDraft, k sourceKey, rev string) (sourceEntry, error) {
	e := sourceEntry{Kind: k.kind, Name: k.name, Machine: k.machine}
	if rev != "" {
		e.Revision = &rev
	}
	head, headRev, _, err := headOf(ctx, tx, d, k)
	if err != nil {
		return e, err
	}
	e.Head = head
	if k.shared {
		return setShared(ctx, tx, d, k, e, headRev)
	}
	found, base, _, err := entryOf(ctx, tx, d, k)
	if err != nil {
		return e, err
	}
	cols := map[string]sql.NullString{"fragment": {}, "profile": {}, "assignment": {}}
	cols[k.kind] = nullable(rev)
	if found {
		e.Base = base
		_, err = tx.ExecContext(ctx, `UPDATE draft_source_entry SET fragment_revision = $5, profile_revision = $6, assignment_revision = $7
			WHERE draft = $1 AND kind = $2 AND name IS NOT DISTINCT FROM $3 AND machine IS NOT DISTINCT FROM $4 AND scope = 'cluster'`,
			d.id, k.kind, nullable(k.name), nullable(k.machine), cols["fragment"], cols["profile"], cols["assignment"])
		return e, err
	}
	e.Base = headRev
	_, err = tx.ExecContext(ctx, `INSERT INTO draft_source_entry (draft, cluster, kind, name, machine, fragment_revision,
		profile_revision, assignment_revision, base) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		d.id, d.cluster, k.kind, nullable(k.name), nullable(k.machine), cols["fragment"], cols["profile"], cols["assignment"], headRev)
	return e, err
}

// setShared records the draft's shared entry e, replacing any other of its name, with the bases
// this write reads (§3.1.1): the shared head's counter and, for an edit, the cluster's adoption
// counter. It takes no lock on the cluster's view: publication compares both.
func setShared(ctx context.Context, tx *sql.Tx, d lockedDraft, k sourceKey, e sourceEntry, head *int) (sourceEntry, error) {
	e.Scope, e.Base = scopeLibrary, head
	var adopted sql.NullInt64
	if e.Revision != nil {
		err := tx.QueryRowContext(ctx, `SELECT adoption_revision FROM library_adoption WHERE cluster = $1 AND kind = $2 AND name = $3`,
			d.cluster, k.kind, k.name).Scan(&adopted)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return e, err
		}
		e.AdoptionBase = adoptionBase(adopted)
	}
	column := headKinds[k.kind].revision
	_, err := tx.ExecContext(ctx, `INSERT INTO draft_source_entry (draft, cluster, kind, name, scope, `+column+`, base, adoption_base)
		VALUES ($1, $2, $3, $4, 'library', $5, $6, $7)
		ON CONFLICT (draft, kind, name, scope) WHERE kind <> 'assignment'
		DO UPDATE SET `+column+` = excluded.`+column+`, base = excluded.base, adoption_base = excluded.adoption_base`,
		d.id, d.cluster, k.kind, k.name, e.Revision, head, adopted)
	return e, err
}

// answer advances the draft and builds the 200 a draft update answers (§9.2).
func (d lockedDraft) answer(ctx context.Context, tx *sql.Tx, e sourceEntry, subjects ...string) (result, error) {
	tag, err := d.advance(ctx, tx)
	if err != nil {
		return result{}, err
	}
	return result{status: http.StatusOK, etag: tag, body: sourceUpdate{Draft: d.id, Entry: e}, subjects: append([]string{d.id}, subjects...)}, nil
}

// removeSource proposes the removal of a fragment, profile or assignment (§3.1, choice §17.31): an
// entry with no revision. A name with neither a head (removed already or not) nor an entry in this
// draft is 404.
func removeSource(ctx context.Context, tx *sql.Tx, q *request, kind string) (result, error) {
	d, err := lockDraft(ctx, tx, q)
	if err != nil {
		return result{}, err
	}
	k, err := keyOf(ctx, tx, q, d, kind)
	if err != nil {
		return result{}, err
	}
	k.shared = q.r.URL.Query().Get("scope") == scopeLibrary // mutate admitted the query
	found, _, _, err := entryOf(ctx, tx, d, k)
	if err != nil {
		return result{}, err
	}
	head, _, current, err := headOf(ctx, tx, d, k)
	if err != nil {
		return result{}, err
	}
	// A shared head an earlier publication removed is no such name, whatever the draft holds
	// (§3.1.1).
	if head == nil && !found || k.shared && head != nil && current == nil {
		where := "the draft or its cluster"
		if k.shared {
			where = "the draft or the library"
		}
		r := refuse(http.StatusNotFound, "not-found", "no such "+kind+" in "+where)
		if kind == "assignment" {
			return result{}, r.with("machine", k.machine)
		}
		return result{}, r.with("name", k.name)
	}
	e, err := setEntry(ctx, tx, d, k, "")
	if err != nil {
		return result{}, err
	}
	return d.answer(ctx, tx, e)
}

// profileInput is a profile revision's body: fragment revisions in order (§9.3), at a scope.
type profileInput struct {
	Fragments []string `json:"fragments"`
	Scope     string   `json:"scope,omitempty"`
}

func (in *profileInput) check(*API) error {
	if err := checkScope(in.Scope); err != nil {
		return err
	}
	if len(in.Fragments) == 0 || len(in.Fragments) > 256 {
		return errors.New("fragments must list 1 to 256 fragment revisions")
	}
	seen := map[string]bool{}
	for i, f := range in.Fragments {
		if id.MustHave(f, id.FragmentRevision) != nil {
			return fmt.Errorf("fragments[%d] must be an frv identifier", i)
		}
		if seen[f] {
			return fmt.Errorf("fragments[%d] repeats a revision", i)
		}
		seen[f] = true
	}
	return nil
}

// pinnable reports whether frv may be pinned in the draft (§3.1): a revision of the draft's cluster
// that is the revision this draft proposes for its fragment or, with no entry for it, the
// fragment's head revision, a head lockHeads holds. The entry decides over the head, which
// publication replaces with it.
func pinnable(ctx context.Context, tx *sql.Tx, d lockedDraft, held map[string]bool, frv string) (bool, error) {
	var name string
	switch err := tx.QueryRowContext(ctx, `SELECT name FROM fragment_revision WHERE id = $1 AND cluster = $2`, frv, d.cluster).Scan(&name); {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, err
	}
	k := sourceKey{kind: "fragment", name: name}
	found, _, proposed, err := entryOf(ctx, tx, d, k)
	if err != nil || found {
		return found && proposed != nil && *proposed == frv, err
	}
	current, err := heldHead(ctx, tx, d, k, held)
	return current != nil && *current == frv, err
}

// sharedRevision reports whether frv is a shared fragment revision.
func sharedRevision(ctx context.Context, tx *sql.Tx, frv string) (bool, error) {
	var ok bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM fragment_revision WHERE id = $1 AND cluster IS NULL)`, frv).Scan(&ok)
	return ok, err
}

// sharedName reports whether name is a shared name of kind: it has a shared head, a removed one
// included (adopting clusters keep compiling it), or the draft has a shared entry of it. A pin or
// selection of one is judged at publication, over the whole draft (§3.1.1).
func sharedName(ctx context.Context, tx *sql.Tx, d lockedDraft, kind, name string) (bool, error) {
	var ok bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM `+headKinds[kind].table+` WHERE owner = 'library' AND name = $1)
		OR EXISTS (SELECT 1 FROM draft_source_entry WHERE draft = $2 AND kind = $3 AND name = $1 AND scope = 'library')`,
		name, d.id, kind).Scan(&ok)
	return ok, err
}

func updateProfile(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*profileInput)
	library := in.Scope == scopeLibrary
	var held map[string]bool
	if !library { // T1 takes no lock on a shared name's view (§3.1.1)
		var err error
		if held, err = lockHeads(ctx, tx, q, in.Fragments, nil, nil); err != nil {
			return result{}, err
		}
	}
	d, err := lockDraft(ctx, tx, q)
	if err != nil {
		return result{}, err
	}
	k, err := keyOf(ctx, tx, q, d, "profile")
	if err != nil {
		return result{}, err
	}
	k.shared = library
	// A shared pin of a cluster profile is judged at publication; a shared profile pins shared
	// revisions only, and whether each is the head or proposed is publication's too.
	marked := make([]bool, len(in.Fragments))
	for i, f := range in.Fragments {
		shared, err := sharedRevision(ctx, tx, f)
		if err != nil {
			return result{}, err
		}
		marked[i] = shared && !library
		ok, detail := shared, "a shared profile's pin must be a revision of a shared fragment"
		if !library && !shared {
			if ok, err = pinnable(ctx, tx, d, held, f); err != nil {
				return result{}, err
			}
			detail = "a pin must be the revision this draft proposes for its fragment or, with no entry for it, the fragment's head revision"
		}
		if !ok {
			return result{}, refuse(http.StatusUnprocessableEntity, "validation-failed", detail).
				with("path", fmt.Sprintf("fragments[%d]", i)).with("revision", f)
		}
	}
	prv, cluster := id.New(id.ProfileRevision), nullable(d.cluster)
	if library {
		cluster = sql.NullString{}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO profile_revision (id, cluster, name, author, created_at) VALUES ($1, $2, $3, $4, now())`,
		prv, cluster, k.name, q.principal.ID); err != nil {
		return result{}, err
	}
	for i, f := range in.Fragments {
		if _, err := tx.ExecContext(ctx, `INSERT INTO profile_revision_fragment (revision, cluster, position, fragment_revision, library)
			VALUES ($1, $2, $3, $4, $5)`, prv, cluster, i, f, marked[i]); err != nil {
			return result{}, err
		}
	}
	e, err := setEntry(ctx, tx, d, k, prv)
	if err != nil {
		return result{}, err
	}
	return d.answer(ctx, tx, e, prv)
}

// discardInput is a discard's body: an empty object (§9.3).
type discardInput struct{}

func (*discardInput) check(*API) error { return nil }

// discardDraft ends an open draft (§3.1, T11): it takes T1's draft check, moves the draft to its
// next revision as `discarded` and answers it.
func discardDraft(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
	d, err := lockDraft(ctx, tx, q)
	if err != nil {
		return result{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE draft SET state = 'discarded', revision = revision + 1, etag_token = $2 WHERE id = $1`,
		d.id, etagToken()); err != nil {
		return result{}, err
	}
	row, err := scanDraft(tx.QueryRowContext(ctx, selectDraft+` WHERE id = $1`, d.id))
	if err != nil {
		return result{}, err
	}
	if err := withEntries(ctx, tx, []*draftBody{&row.draftBody}); err != nil {
		return result{}, err
	}
	return result{status: http.StatusOK, body: &row.draftBody, subjects: []string{d.id}}, nil
}

// fragmentLayers are the layers in composition order (§9.3, choice §17.31): design §6.2's seven
// minus machine-intrinsic, which is the import base.
var fragmentLayers = []string{"global", "site", "cluster", "role", "workload", "override"}

// assignmentInput is an assignment revision's body: profiles, then fragments per layer, by name
// (§9.3).
type assignmentInput struct {
	Profiles  []string            `json:"profiles"`
	Fragments map[string][]string `json:"fragments"`
}

func (in *assignmentInput) check(*API) error {
	n := len(in.Profiles)
	if err := names("profiles", in.Profiles, map[string]bool{}); err != nil {
		return err
	}
	seen := map[string]bool{}
	for layer, list := range in.Fragments {
		if !slices.Contains(fragmentLayers, layer) {
			return errors.New("fragments has a member that is not a layer: global, site, cluster, role, workload or override")
		}
		if len(list) == 0 {
			return fmt.Errorf("fragments.%s must list a fragment", layer)
		}
		if err := names("fragments."+layer, list, seen); err != nil {
			return err
		}
		n += len(list)
	}
	if n > 256 {
		return errors.New("an assignment must select at most 256 profiles and fragments")
	}
	return nil
}

// names checks a list of names, none in seen, and adds them to it.
func names(path string, list []string, seen map[string]bool) error {
	for i, s := range list {
		if !validName(s) {
			return fmt.Errorf("%s[%d] must be 1 to 63 lowercase letters, digits and inner hyphens", path, i)
		}
		if seen[s] {
			return fmt.Errorf("%s[%d] repeats a name", path, i)
		}
		seen[s] = true
	}
	return nil
}

// selectable reports whether the draft may select name (§3.1): proposed by this draft or, with no
// entry for it, a head lockHeads holds with a revision. For a fragment it also returns that
// revision's layer.
func selectable(ctx context.Context, tx *sql.Tx, d lockedDraft, held map[string]bool, kind, name string) (ok bool, layer string, err error) {
	k := sourceKey{kind: kind, name: name}
	found, _, rev, err := entryOf(ctx, tx, d, k)
	if err == nil && !found {
		rev, err = heldHead(ctx, tx, d, k, held)
	}
	if err != nil || rev == nil {
		return false, "", err
	}
	if kind == "fragment" {
		err = tx.QueryRowContext(ctx, `SELECT layer FROM fragment_revision WHERE id = $1`, *rev).Scan(&layer)
	}
	return true, layer, err
}

func updateAssignment(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*assignmentInput)
	var fragments []string
	for _, list := range in.Fragments {
		fragments = append(fragments, list...)
	}
	held, err := lockHeads(ctx, tx, q, nil, fragments, in.Profiles)
	if err != nil {
		return result{}, err
	}
	d, err := lockDraft(ctx, tx, q)
	if err != nil {
		return result{}, err
	}
	k, err := keyOf(ctx, tx, q, d, "assignment")
	if err != nil {
		return result{}, err
	}
	// A selection that fails the cluster's rules stands when its name is a shared one: the cluster's
	// view of it, and its layer, are judged at publication (§3.1.1).
	deferred := func(kind, name, path string, i int) error {
		shared, err := sharedName(ctx, tx, d, kind, name)
		if err != nil || shared {
			return err
		}
		return refuse(http.StatusUnprocessableEntity, "validation-failed",
			"a name must have a head or be proposed by this draft, not removed by it, and a fragment must carry its layer").
			with("path", fmt.Sprintf("%s[%d]", path, i))
	}
	for i, p := range in.Profiles {
		ok, _, err := selectable(ctx, tx, d, held, "profile", p)
		if err == nil && !ok {
			err = deferred("profile", p, "profiles", i)
		}
		if err != nil {
			return result{}, err
		}
	}
	for _, layer := range fragmentLayers {
		for i, f := range in.Fragments[layer] {
			ok, l, err := selectable(ctx, tx, d, held, "fragment", f)
			if err == nil && (!ok || l != layer) {
				err = deferred("fragment", f, "fragments."+layer, i)
			}
			if err != nil {
				return result{}, err
			}
		}
	}
	asr := id.New(id.AssignmentRevision)
	if _, err := tx.ExecContext(ctx, `INSERT INTO assignment_revision (id, cluster, machine, author, created_at) VALUES ($1, $2, $3, $4, now())`,
		asr, d.cluster, k.machine, q.principal.ID); err != nil {
		return result{}, err
	}
	for i, p := range in.Profiles {
		if _, err := tx.ExecContext(ctx, `INSERT INTO assignment_revision_profile (revision, position, profile) VALUES ($1, $2, $3)`,
			asr, i, p); err != nil {
			return result{}, err
		}
	}
	for _, layer := range fragmentLayers {
		for i, f := range in.Fragments[layer] {
			if _, err := tx.ExecContext(ctx, `INSERT INTO assignment_revision_fragment (revision, layer, position, fragment)
				VALUES ($1, $2, $3, $4)`, asr, layer, i, f); err != nil {
				return result{}, err
			}
		}
	}
	e, err := setEntry(ctx, tx, d, k, asr)
	if err != nil {
		return result{}, err
	}
	return d.answer(ctx, tx, e, asr)
}
