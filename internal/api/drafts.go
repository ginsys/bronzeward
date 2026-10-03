package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
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

func fragmentRemoval() effectRoute   { return removal("fragment") }
func profileRemoval() effectRoute    { return removal("profile") }
func assignmentRemoval() effectRoute { return removal("assignment") }

func removal(kind string) effectRoute {
	return effectRoute{action: "draft." + kind + ".remove", input: func() input { return &noBody{} },
		effect: func(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
			return removeSource(ctx, tx, q, kind)
		}}
}

// noBody is a DELETE route's input: it takes no body (§9.3), and decodeBody refuses one.
type noBody struct{}

func (*noBody) check(*API) error { return nil }

// sourceName is a fragment or profile name (choice §17.31), as migration 0009's source_name.
var sourceName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func validName(s string) bool { return len(s) <= 63 && sourceName.MatchString(s) }

// sourceEntry is a draft's fragment, profile or assignment entry (§3.1, §9.3). Head is the head of
// its name or machine, if one exists; Base the head revision it was edited from (null: absent);
// Revision the proposed revision (null: a removal).
type sourceEntry struct {
	Kind     string  `json:"kind"`
	Name     string  `json:"name,omitempty"`
	Machine  string  `json:"machine,omitempty"`
	Head     *string `json:"head"`
	Base     *int    `json:"base"`
	Revision *string `json:"revision"`
}

// sourceUpdate is a draft update's answer (§9.3).
type sourceUpdate struct {
	Draft string      `json:"draft"`
	Entry sourceEntry `json:"entry"`
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
// their fragment through their immutable revision row. A head a publication creates meanwhile is
// not held: the name had none when checked, so the check refused it or took the draft's own entry.
func lockHeads(ctx context.Context, tx *sql.Tx, q *request, pins, fragments, profiles []string) error {
	draft := q.r.PathValue("id")
	if id.MustHave(draft, id.Draft) != nil {
		return nil
	}
	var cluster string
	switch err := tx.QueryRowContext(ctx, `SELECT cluster FROM draft WHERE id = $1`, draft).Scan(&cluster); {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return err
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
			return err
		}
		for rows.Next() {
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}
	return nil
}

// advance moves a locked draft to its next revision and returns the new ETag.
func (d lockedDraft) advance(ctx context.Context, tx *sql.Tx) (string, error) {
	token := etagToken()
	if _, err := tx.ExecContext(ctx, `UPDATE draft SET revision = revision + 1, etag_token = $2 WHERE id = $1`, d.id, token); err != nil {
		return "", err
	}
	return etag(d.revision+1, token), nil
}

// sourceKey names an entry: a fragment or profile by name, an assignment by machine.
type sourceKey struct{ kind, name, machine string }

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

// headOf reads the head of k in the draft's cluster: its id, head revision and current revision
// (nil once removed). No head is all nil.
func headOf(ctx context.Context, tx *sql.Tx, d lockedDraft, k sourceKey) (head *string, base *int, current *string, err error) {
	var q string
	args := []any{d.cluster, k.name}
	switch k.kind {
	case "fragment":
		q = `SELECT id, head_revision, head_revision_id FROM fragment WHERE cluster = $1 AND name = $2`
	case "profile":
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
		FROM draft_source_entry WHERE draft = $1 AND kind = $2 AND name IS NOT DISTINCT FROM $3 AND machine IS NOT DISTINCT FROM $4`,
		d.id, k.kind, nullable(k.name), nullable(k.machine)).Scan(&b, &r)
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
// (§4.2) instead of being overwritten.
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
	found, base, _, err := entryOf(ctx, tx, d, k)
	if err != nil {
		return e, err
	}
	cols := map[string]sql.NullString{"fragment": {}, "profile": {}, "assignment": {}}
	cols[k.kind] = nullable(rev)
	if found {
		e.Base = base
		_, err = tx.ExecContext(ctx, `UPDATE draft_source_entry SET fragment_revision = $5, profile_revision = $6, assignment_revision = $7
			WHERE draft = $1 AND kind = $2 AND name IS NOT DISTINCT FROM $3 AND machine IS NOT DISTINCT FROM $4`,
			d.id, k.kind, nullable(k.name), nullable(k.machine), cols["fragment"], cols["profile"], cols["assignment"])
		return e, err
	}
	e.Base = headRev
	_, err = tx.ExecContext(ctx, `INSERT INTO draft_source_entry (draft, cluster, kind, name, machine, fragment_revision,
		profile_revision, assignment_revision, base) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		d.id, d.cluster, k.kind, nullable(k.name), nullable(k.machine), cols["fragment"], cols["profile"], cols["assignment"], headRev)
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
	found, _, _, err := entryOf(ctx, tx, d, k)
	if err != nil {
		return result{}, err
	}
	if !found {
		head, _, _, err := headOf(ctx, tx, d, k)
		if err != nil {
			return result{}, err
		}
		if head == nil {
			r := refuse(http.StatusNotFound, "not-found", "no such "+kind+" in the draft or its cluster")
			if kind == "assignment" {
				return result{}, r.with("machine", k.machine)
			}
			return result{}, r.with("name", k.name)
		}
	}
	e, err := setEntry(ctx, tx, d, k, "")
	if err != nil {
		return result{}, err
	}
	return d.answer(ctx, tx, e)
}

// profileInput is a profile revision's body: fragment revisions in order (§9.3).
type profileInput struct {
	Fragments []string `json:"fragments"`
}

func (in *profileInput) check(*API) error {
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
// fragment's head revision. The entry decides over the head, which publication replaces with it.
func pinnable(ctx context.Context, tx *sql.Tx, d lockedDraft, frv string) (bool, error) {
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
	_, _, current, err := headOf(ctx, tx, d, k)
	return current != nil && *current == frv, err
}

func updateProfile(ctx context.Context, _ *API, tx *sql.Tx, q *request) (result, error) {
	in := q.input.(*profileInput)
	if err := lockHeads(ctx, tx, q, in.Fragments, nil, nil); err != nil {
		return result{}, err
	}
	d, err := lockDraft(ctx, tx, q)
	if err != nil {
		return result{}, err
	}
	k, err := keyOf(ctx, tx, q, d, "profile")
	if err != nil {
		return result{}, err
	}
	for i, f := range in.Fragments {
		ok, err := pinnable(ctx, tx, d, f)
		if err != nil {
			return result{}, err
		}
		if !ok {
			return result{}, refuse(http.StatusUnprocessableEntity, "validation-failed",
				"a pin must be the revision this draft proposes for its fragment or, with no entry for it, the fragment's head revision").
				with("path", fmt.Sprintf("fragments[%d]", i)).with("revision", f)
		}
	}
	prv := id.New(id.ProfileRevision)
	if _, err := tx.ExecContext(ctx, `INSERT INTO profile_revision (id, cluster, name, author, created_at) VALUES ($1, $2, $3, $4, now())`,
		prv, d.cluster, k.name, q.principal.ID); err != nil {
		return result{}, err
	}
	for i, f := range in.Fragments {
		if _, err := tx.ExecContext(ctx, `INSERT INTO profile_revision_fragment (revision, cluster, position, fragment_revision)
			VALUES ($1, $2, $3, $4)`, prv, d.cluster, i, f); err != nil {
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
	if n == 0 || n > 256 {
		return errors.New("an assignment must select 1 to 256 profiles and fragments")
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
// entry for it, a head with a revision. For a fragment it also returns that revision's layer.
func selectable(ctx context.Context, tx *sql.Tx, d lockedDraft, kind, name string) (ok bool, layer string, err error) {
	k := sourceKey{kind: kind, name: name}
	found, _, rev, err := entryOf(ctx, tx, d, k)
	if err == nil && !found {
		_, _, rev, err = headOf(ctx, tx, d, k)
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
	if err := lockHeads(ctx, tx, q, nil, fragments, in.Profiles); err != nil {
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
	invalid := func(path string, i int) error {
		return refuse(http.StatusUnprocessableEntity, "validation-failed",
			"a name must have a head or be proposed by this draft, not removed by it, and a fragment must carry its layer").
			with("path", fmt.Sprintf("%s[%d]", path, i))
	}
	for i, p := range in.Profiles {
		if ok, _, err := selectable(ctx, tx, d, "profile", p); err != nil {
			return result{}, err
		} else if !ok {
			return result{}, invalid("profiles", i)
		}
	}
	for _, layer := range fragmentLayers {
		for i, f := range in.Fragments[layer] {
			if ok, l, err := selectable(ctx, tx, d, "fragment", f); err != nil {
				return result{}, err
			} else if !ok || l != layer {
				return result{}, invalid("fragments."+layer, i)
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
