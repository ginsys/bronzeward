package api

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/compile"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/staging"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// publishJob is a running publish operation as the worker that claimed it holds it (§5.1): the
// draft revision it binds and the owner generation of the claim; the owner and its epoch are the
// serving process's.
type publishJob struct {
	op, draft, cluster string
	draftRev           int
	gen                int64
}

// releaseUnit is compilation.md §11's hand-off unit: only sanitized or encrypted values. The heads
// the draft changes are the draft's own entries; unchanged are the heads the compilation used as
// its snapshot read them.
type releaseUnit struct {
	renderer  rendererBody
	unchanged []usedHead
	machines  []unitMachine
	statuses  []unitStatus
}

// usedHead is a head the release uses unchanged, at the revision and head revision the snapshot
// read (compilation §6 step 1).
type usedHead struct {
	kind, head, revision string
	headRevision         int
}

// unitMachine is one covered machine's artifact: its import base and assignment revisions, the
// mode stage 3 validated it in, the ciphertext and its plaintext's configuration digest, the
// redacted configuration (nil: it could not be redacted) and provenance, and its dependency
// records (compilation §9).
type unitMachine struct {
	machine, importBase, assignment, mode string
	ciphertext                            provider.Ciphertext
	configuration                         [32]byte
	redacted                              *string
	provenance                            []compile.Record
	effective, reproduction               []unitDependency
	encryption                            unitDependency
}

// unitDependency is one dependency record: a KV generation path or the Transit key name, its
// version and the creation time that identifies it; a reproduction dependency also names its
// source revision, that revision's text digest, the redacted path and its ordinal there.
type unitDependency struct {
	reference, object string
	version           int64
	created           time.Time
	source            string
	digest            [32]byte
	path              string
	occurrence        int
}

// unitStatus is publication's own classification of one provider object version (compilation §6
// step 3, §11), with the database time its request began: the row T3 seeds (dependency monitor
// §5.2).
type unitStatus struct {
	provider classify.Provider
	object   string
	version  int64
	result   classify.Result
	began    time.Time
}

// headKind is one head table and the draft source entry and release source columns that name it.
type headKind struct {
	table, key, revision string
	prefix               id.Prefix
}

var headKinds = map[string]headKind{
	"fragment":   {"fragment", "name", "fragment_revision", id.Fragment},
	"profile":    {"profile", "name", "profile_revision", id.Profile},
	"assignment": {"assignment", "machine", "assignment_revision", id.Assignment},
}

// sourceHead is one head a release names: a draft entry's (changed) or one the compilation used
// unchanged. key is its name, or its machine for an assignment; revision "" is a removal; base is
// the head revision the author edited from, 0 when the draft introduces the name.
type sourceHead struct {
	kind, key, head, revision string
	base, actual              int // actual: the head revision read under its lock
	changed                   bool
}

// publishCommit is the publication commit (T3, persistence-api.md §6.2): the release, its machines,
// sources and dependency records, the statuses it seeds, the heads moved, the covered machines'
// Desired, the draft published and the operation succeeded naming the release, in one
// transaction. A worker that no longer owns the operation writes nothing (staging.ErrFenced).
func (a *API) publishCommit(ctx context.Context, j publishJob, u releaseUnit) (string, *refusal, error) {
	var rel string
	var ref *refusal
	err := a.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		rel, ref, err = a.commitRelease(ctx, tx, j, u)
		if err == nil && ref != nil {
			return errRefused
		}
		return err
	})
	var pe *pgconn.PgError
	switch {
	case errors.Is(err, errRefused):
	case isDeadlock(err):
		ref = refuse(http.StatusServiceUnavailable, "transient-conflict", "the publication deadlocked on every attempt; nothing was committed")
	case errors.Is(err, errStop) && (errors.As(err, &pe) && !connLost(err) || errors.Is(err, pgx.ErrTxCommitRollback)):
		// The server rejected the COMMIT, a deferred trigger for one, or answered it with ROLLBACK:
		// the release rolled back. A lost reply instead leaves the outcome unknown, for a retry to
		// read (§5 rule 6).
		a.o.logf("publication %s: %v", j.op, err)
		ref = refuse(http.StatusInternalServerError, "internal-error", "the publication commit was rejected; nothing was committed")
	case errors.As(err, &pe) && strings.HasPrefix(pe.Code, "23"):
		// A statement broke an integrity constraint the unit should have met (a release machine in
		// another mode than its machine's platform, for one): the transaction rolled back, and a
		// retry would break it again, so the operation fails rather than staying running.
		a.o.logf("publication %s: %v", j.op, err)
		ref = refuse(http.StatusInternalServerError, "internal-error", "the publication broke a database constraint; nothing was committed")
	}
	if ref != nil {
		// The refusal rolled the commit back; a separate transaction records the operation failed
		// with its problem and terminal event (§6.2), fenced on this owner.
		if err := a.inTx(ctx, func(tx *sql.Tx) error {
			return a.finishPublish(ctx, tx, j, "failed", nil, problemDoc(j.op, ref),
				map[string]any{"type": "failed", "code": ref.code})
		}); err != nil {
			return "", nil, err
		}
		return "", ref, nil
	}
	if err != nil {
		return "", nil, err
	}
	return rel, nil, nil
}

func (a *API) commitRelease(ctx context.Context, tx *sql.Tx, j publishJob, u releaseUnit) (string, *refusal, error) {
	// The draft's entries name the heads it changes (ruling R9); its revision, checked under its
	// lock below, holds them, since every draft update moves it.
	heads, err := draftHeads(ctx, tx, j.draft)
	if err != nil {
		return "", nil, err
	}
	for _, h := range u.unchanged {
		heads = append(heads, sourceHead{kind: h.kind, head: h.head, revision: h.revision, base: h.headRevision})
	}

	var epoch string
	var recovery bool
	if err := tx.QueryRowContext(ctx, `SELECT epoch, recovery_mode FROM installation_state FOR SHARE`).
		Scan(&epoch, &recovery); err != nil {
		return "", nil, err
	}
	if epoch != a.d.owner.Epoch {
		return "", nil, staging.ErrFenced
	}
	covered := make([]string, len(u.machines))
	for i, m := range u.machines {
		covered[i] = m.machine
	}
	slices.Sort(covered)
	scopes := map[string]string{}
	err = eachRow(ctx, tx, `SELECT id, scope_state FROM machine WHERE id = ANY ($1) ORDER BY id FOR SHARE`, covered,
		func(row *sql.Rows) error {
			var m, s string
			err := row.Scan(&m, &s)
			scopes[m] = s
			return err
		})
	if err != nil {
		return "", nil, err
	}
	// Every machine state of the cluster is locked, not only the covered ones, so the coverage
	// (ruling R21') cannot change before COMMIT. Each machine's import base, the draft's entry or
	// its Applied release's (§3.2), is read by the next statement: a locking read that waited
	// re-checks the locked row alone, not rows joined to it.
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM machine_state s JOIN machine m ON m.id = s.machine
		WHERE m.cluster = $1 ORDER BY s.machine FOR UPDATE OF s`, j.cluster); err != nil {
		return "", nil, err
	}
	bases := map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT s.machine, coalesce(e.import_base_revision, m.import_base_revision, '')
		FROM machine_state s
		JOIN machine c ON c.id = s.machine
		LEFT JOIN draft_entry e ON e.draft = $2 AND e.machine = s.machine
		LEFT JOIN release_machine m ON m.release = s.applied_release AND m.machine = s.machine
		WHERE c.cluster = $1 ORDER BY s.machine`, j.cluster, j.draft)
	if err != nil {
		return "", nil, err
	}
	for rows.Next() {
		var m, b string
		if err := rows.Scan(&m, &b); err != nil {
			_ = rows.Close()
			return "", nil, err
		}
		bases[m] = b
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	if len(scopes) != len(covered) || slices.ContainsFunc(covered, func(m string) bool { _, ok := bases[m]; return !ok }) {
		return "", nil, errors.New("a covered machine has no record or no state of the cluster")
	}
	// One pass over every existing head in id order, changed ones FOR UPDATE (§6.2).
	locked := slices.Clone(heads)
	slices.SortFunc(locked, func(x, y sourceHead) int { return strings.Compare(x.head, y.head) })
	for i := range locked {
		h := &locked[i]
		if h.head == "" {
			continue
		}
		k, ok := headKinds[h.kind]
		if !ok {
			return "", nil, fmt.Errorf("head %s of an unknown kind", h.head)
		}
		mode := "SHARE"
		if h.changed {
			mode = "UPDATE"
		}
		if err := tx.QueryRowContext(ctx, `SELECT `+k.key+`, head_revision FROM `+k.table+` WHERE id = $1 FOR `+mode, h.head).
			Scan(&h.key, &h.actual); err != nil {
			return "", nil, err
		}
	}
	read := map[string]sourceHead{}
	for _, h := range locked {
		if _, twice := read[h.head]; twice && h.head != "" {
			return "", nil, fmt.Errorf("head %s used twice", h.head)
		}
		read[h.head] = h
	}
	for i := range heads {
		if heads[i].head != "" {
			heads[i].key, heads[i].actual = read[heads[i].head].key, read[heads[i].head].actual
		}
	}
	var draftState string
	var draftRev int
	if err := tx.QueryRowContext(ctx, `SELECT state, revision FROM draft WHERE id = $1 FOR UPDATE`, j.draft).
		Scan(&draftState, &draftRev); err != nil {
		return "", nil, err
	}
	// The operation's lock holds its owner, generation and epoch until COMMIT; the fenced UPDATE
	// that ends it (finishPublish) is the ownership check, and a superseded worker's whole
	// transaction rolls back with it, its refusal included (§5.1).
	var by, role, opState string
	var opRelease sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT created_by, created_role, state, result->>'release' FROM operation
		WHERE id = $1 AND kind = 'publish' FOR UPDATE`, j.op).Scan(&by, &role, &opState, &opRelease); err != nil {
		return "", nil, a.fenced(err)
	}

	content := releaseContent{cluster: j.cluster, draft: j.draft, draftRev: j.draftRev, renderer: u.renderer}
	for _, h := range heads {
		content.sources = append(content.sources, contentSource{kind: h.kind, key: h.key, revision: h.revision})
	}
	for _, m := range u.machines {
		content.machines = append(content.machines, contentMachine{machine: m.machine, importBase: m.importBase,
			assignment: m.assignment, mode: m.mode, configuration: m.configuration[:]})
	}
	digest := content.digest()

	// The release of this draft revision comes first (§6.2): a commit-unknown retry, or a worker
	// that superseded the first, meets it before the checks its commit has made fail.
	var existing string
	var existingDigest []byte
	switch err := tx.QueryRowContext(ctx, `SELECT id, digest FROM release WHERE draft = $1 AND draft_revision = $2`,
		j.draft, j.draftRev).Scan(&existing, &existingDigest); {
	case err == nil:
		same := string(existingDigest) == string(digest[:])
		switch {
		case same && opState == "succeeded" && opRelease.String == existing:
			return existing, nil, nil
		case !same:
			return "", refuse(http.StatusConflict, "conflict", "the draft revision was published with other content").
				with("release", existing), nil
		}
		return existing, nil, a.finishPublish(ctx, tx, j, "succeeded", map[string]any{"release": existing}, nil,
			map[string]any{"type": "succeeded", "release": existing})
	case !errors.Is(err, sql.ErrNoRows):
		return "", nil, err
	}
	if ref, err := checkInputs(j, u, draftState, draftRev, heads, locked, bases, scopes, recovery); ref != nil || err != nil {
		return "", ref, err
	}

	if err := seedStatuses(ctx, tx, u.statuses); err != nil {
		return "", nil, err
	}
	if ref, err := recheckStatuses(ctx, tx, u); ref != nil || err != nil {
		return "", ref, err
	}

	rel := id.New(id.Release)
	r := u.renderer
	if _, err := tx.ExecContext(ctx, `INSERT INTO release (id, cluster, draft, draft_revision, digest, contract,
		machinery_version, machinery_checksum, kubernetes_version, operation, published_by, published_role, epoch,
		published_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now())`,
		rel, j.cluster, j.draft, j.draftRev, digest[:], r.Contract, r.MachineryVersion, r.MachineryChecksum,
		r.KubernetesVersion, j.op, by, role, epoch); err != nil {
		return "", nil, err
	}
	for _, m := range u.machines {
		if err := insertReleaseMachine(ctx, tx, rel, j.cluster, m); err != nil {
			return "", nil, err
		}
	}
	// The heads move before the release's sources are written, so each source names the head
	// revision the release left its head at (ruling R10).
	for i := range heads {
		h := &heads[i]
		if !h.changed {
			continue
		}
		if ref, err := moveHead(ctx, tx, j.cluster, h); err != nil || ref != nil {
			return "", ref, err
		}
	}
	for _, h := range heads {
		k := headKinds[h.kind]
		name, machine := sql.NullString{String: h.key, Valid: h.kind != "assignment"}, sql.NullString{String: h.key, Valid: h.kind == "assignment"}
		if _, err := tx.ExecContext(ctx, `INSERT INTO release_source (release, cluster, kind, `+h.kind+`, name, machine, `+
			k.revision+`, head_revision) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, rel, j.cluster, h.kind, h.head, name, machine,
			sql.NullString{String: h.revision, Valid: h.revision != ""}, h.base); err != nil {
			return "", nil, err
		}
	}
	// The unit's records agree with the declarations of the sources the release now names; a
	// disagreement is the unit's defect, which a retry would repeat (ruling R16).
	mismatch, err := checkDeclarations(ctx, tx, rel, u)
	if err != nil {
		return "", nil, err
	}
	if mismatch != "" {
		a.o.logf("publication %s: %s", j.op, mismatch)
		return "", refuse(http.StatusInternalServerError, "internal-error",
			"the compiled release disagrees with its sources' reference declarations; nothing was committed"), nil
	}
	for _, m := range u.machines {
		if err := insertDependencies(ctx, tx, rel, m); err != nil {
			return "", nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE machine_state SET desired = $2, revision = revision + 1 WHERE machine = ANY ($1)`,
		covered, rel); err != nil {
		return "", nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE draft SET state = 'published', release = $2, revision = revision + 1,
		etag_token = $3 WHERE id = $1`, j.draft, rel, etagToken()); err != nil {
		return "", nil, err
	}
	// The digest the release carries is the one its stored rows give (ruling R14).
	stored, err := releaseDigest(ctx, tx, rel)
	if err != nil {
		return "", nil, err
	}
	if stored != digest {
		return "", nil, fmt.Errorf("release %s: the stored rows give another digest", rel)
	}
	if err := a.finishPublish(ctx, tx, j, "succeeded", map[string]any{"release": rel}, nil,
		map[string]any{"type": "succeeded", "release": rel}); err != nil {
		return "", nil, err
	}
	return rel, nil, nil
}

// checkInputs compares what the commit read under its locks with what the draft and the
// compilation bound (§6.2): the draft open at the bound revision; stale input in its three forms
// (§4.2), heads in id order, then machines; and, for an assignment change in recovery mode, the
// machine's scope released (§12.2). A changed assignment of a machine the release does not cover
// is the unit's defect, and an error (ruling R13).
func checkInputs(j publishJob, u releaseUnit, draftState string, draftRev int, heads, locked []sourceHead,
	bases, scopes map[string]string, recovery bool) (*refusal, error) {
	switch {
	case draftState != "open":
		return refuse(http.StatusConflict, "conflict", "the draft is no longer open").with("draft", j.draft), nil
	case draftRev != j.draftRev:
		return refuse(http.StatusConflict, "conflict", "the draft moved after the publication bound it").with("draft", j.draft), nil
	}
	conflicts := []map[string]any{}
	for _, h := range locked {
		switch {
		case h.head == "":
		case h.changed && h.base == 0:
			conflicts = append(conflicts, map[string]any{"head": h.head, "expected": "absent", "actual": h.actual})
		case h.actual != h.base:
			conflicts = append(conflicts, map[string]any{"head": h.head, "expected": h.base, "actual": h.actual})
		}
	}
	machines := slices.Clone(u.machines)
	slices.SortFunc(machines, func(x, y unitMachine) int { return strings.Compare(x.machine, y.machine) })
	for _, m := range machines {
		if b := bases[m.machine]; b != m.importBase {
			actual := any(b)
			if b == "" {
				actual = "absent"
			}
			conflicts = append(conflicts, map[string]any{"machine": m.machine, "expected": m.importBase, "actual": actual})
		}
	}
	// A machine covered now that the unit does not cover (ruling R21').
	for _, m := range slices.Sorted(maps.Keys(bases)) {
		if b := bases[m]; b != "" && !slices.ContainsFunc(machines, func(x unitMachine) bool { return x.machine == m }) {
			conflicts = append(conflicts, map[string]any{"machine": m, "expected": "absent", "actual": b})
		}
	}
	if len(conflicts) > 0 {
		return refuse(http.StatusConflict, "stale-input", fmt.Sprintf("Publication refused: %d input(s) moved.", len(conflicts))).
			with("conflicts", conflicts), nil
	}
	for _, h := range heads {
		if !h.changed || h.kind != "assignment" {
			continue
		}
		scope, covered := scopes[h.key]
		switch {
		case !covered:
			return nil, fmt.Errorf("the draft changes the assignment of machine %s, which the release does not cover", h.key)
		case recovery && scope != "released":
			return refuse(http.StatusConflict, "recovery-mode-active",
				"recovery mode refuses an assignment change on a scope not released in the current epoch").with("scope", h.key), nil
		}
	}
	return nil, nil
}

// recheckStatuses locks every named version's status FOR SHARE in dep order, rows a concurrent
// publication inserted included, and refuses one recorded other than retained after publication
// began classifying that version (dependency monitor §5.2).
func recheckStatuses(ctx context.Context, tx *sql.Tx, u releaseUnit) (*refusal, error) {
	type key struct {
		provider, object string
		version          int64
		created          string
	}
	began := map[key]time.Time{}
	for _, s := range u.statuses {
		began[key{string(s.provider), s.object, s.version, createdText(s.result.Created)}] = s.began
	}
	type version struct {
		provider, object string
		version          int64
	}
	var providers, objects []string
	var versions []int64
	named := map[version]string{} // the creation time the unit names
	name := func(provider string, d unitDependency) error {
		k := key{provider, d.object, d.version, createdText(d.created)}
		if _, ok := began[k]; !ok {
			return fmt.Errorf("the %s dependency %s version %d has no classification", provider, d.object, d.version)
		}
		v := version{provider, d.object, d.version}
		if c, ok := named[v]; ok && c != k.created {
			return fmt.Errorf("the %s dependency %s version %d is named under two creation times", provider, d.object, d.version)
		} else if !ok {
			named[v] = k.created
			providers, objects, versions = append(providers, k.provider), append(objects, k.object), append(versions, k.version)
		}
		return nil
	}
	for _, m := range u.machines {
		for _, d := range slices.Concat(m.effective, m.reproduction) {
			if err := name("kv", d); err != nil {
				return nil, err
			}
		}
		if err := name("transit", m.encryption); err != nil {
			return nil, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT provider, object, version, created, class, recorded_at FROM dependency_status
		WHERE (provider, object, version) IN (SELECT * FROM unnest($1::text[], $2::text[], $3::bigint[]))
		ORDER BY id FOR SHARE`, providers, objects, versions)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ref *refusal
	for rows.Next() {
		var k key
		var class string
		var recorded time.Time
		if err := rows.Scan(&k.provider, &k.object, &k.version, &k.created, &class, &recorded); err != nil {
			return nil, err
		}
		if k.created != named[version{k.provider, k.object, k.version}] {
			// A Transit key version keeps the identity of each key it was created under; a KV
			// version holds one, so another means the version was created again (PA §6.1).
			if ref == nil && k.provider == string(classify.KV) {
				ref = refuse(http.StatusUnprocessableEntity, "validation-failed",
					fmt.Sprintf("Publication refused: the kv dependency %s version %d was recorded under another creation time.",
						k.object, k.version)).
					with("dependency", map[string]any{"provider": k.provider, "object": k.object, "version": k.version,
						"reason": compile.ReasonCreatedChanged})
			}
			continue
		}
		if ref == nil && class != string(classify.Retained) && recorded.After(began[k]) {
			ref = refuse(http.StatusUnprocessableEntity, "validation-failed",
				fmt.Sprintf("Publication refused: the %s dependency %s version %d was recorded %s after publication classified it.",
					k.provider, k.object, k.version, class)).
				with("dependency", map[string]any{"provider": k.provider, "object": k.object, "version": k.version})
		}
	}
	return ref, rows.Err()
}

// draftHeads reads the draft's source entries with the head each names, if one exists yet.
func draftHeads(ctx context.Context, tx *sql.Tx, draft string) ([]sourceHead, error) {
	var out []sourceHead
	err := eachRow(ctx, tx, `SELECT e.kind, coalesce(e.name, e.machine),
			coalesce(e.fragment_revision, e.profile_revision, e.assignment_revision, ''), coalesce(e.base, 0),
			coalesce(f.id, p.id, a.id, '')
		FROM draft_source_entry e
		LEFT JOIN fragment f ON e.kind = 'fragment' AND f.cluster = e.cluster AND f.name = e.name
		LEFT JOIN profile p ON e.kind = 'profile' AND p.cluster = e.cluster AND p.name = e.name
		LEFT JOIN assignment a ON e.kind = 'assignment' AND a.machine = e.machine
		WHERE e.draft = $1`, draft, func(row *sql.Rows) error {
		h := sourceHead{changed: true}
		if err := row.Scan(&h.kind, &h.key, &h.revision, &h.base, &h.head); err != nil {
			return err
		}
		out = append(out, h)
		return nil
	})
	return out, err
}

// moveHead points a changed head at its revision, from the base the author edited, or inserts
// the head of a name the draft introduces at head revision 1; h.base becomes the head revision
// the release leaves it at. A name has no row to lock until it is introduced: another publication
// that introduced it meanwhile fails the insert at the unique index once it commits, and the
// commit refuses stale-input naming that head (§6.2).
func moveHead(ctx context.Context, tx *sql.Tx, cluster string, h *sourceHead) (*refusal, error) {
	k := headKinds[h.kind]
	rev := sql.NullString{String: h.revision, Valid: h.revision != ""}
	if h.head != "" {
		return nil, tx.QueryRowContext(ctx, `UPDATE `+k.table+` SET head_revision_id = $2, head_revision = head_revision + 1,
			etag_token = $3 WHERE id = $1 AND head_revision = $4 RETURNING head_revision`,
			h.head, rev, etagToken(), h.base).Scan(&h.base)
	}
	if _, err := tx.ExecContext(ctx, `SAVEPOINT introduce`); err != nil {
		return nil, err
	}
	err := insertHead(ctx, tx, h, rev)
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23505" {
		if err == nil {
			_, err = tx.ExecContext(ctx, `RELEASE SAVEPOINT introduce`)
		}
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT introduce`); err != nil {
		return nil, err
	}
	key := `machine = $1`
	args := []any{h.key}
	if h.kind != "assignment" {
		key, args = `cluster = $1 AND name = $2`, []any{cluster, h.key}
	}
	var other string
	var actual int
	if err := tx.QueryRowContext(ctx, `SELECT id, head_revision FROM `+k.table+` WHERE `+key, args...).Scan(&other, &actual); err != nil {
		return nil, fmt.Errorf("%s %s: %w after %w", h.kind, h.key, err, pe)
	}
	return refuse(http.StatusConflict, "stale-input", "Publication refused: 1 input(s) moved.").
		with("conflicts", []map[string]any{{"head": other, "expected": "absent", "actual": actual}}), nil
}

// insertHead inserts the head of a name the draft introduces at head revision 1.
func insertHead(ctx context.Context, tx *sql.Tx, h *sourceHead, rev sql.NullString) error {
	k := headKinds[h.kind]
	h.head = id.New(k.prefix)
	h.base = 1
	var q string
	switch h.kind {
	case "fragment":
		q = `INSERT INTO fragment (id, cluster, scope, name, layer, head_revision_id, head_revision, etag_token, created_at)
			SELECT $1, cluster, 'cluster', name, layer, id, 1, $3, now() FROM fragment_revision WHERE id = $2`
	case "profile":
		q = `INSERT INTO profile (id, cluster, scope, name, head_revision_id, head_revision, etag_token, created_at)
			SELECT $1, cluster, 'cluster', name, id, 1, $3, now() FROM profile_revision WHERE id = $2`
	default:
		q = `INSERT INTO assignment (id, cluster, machine, head_revision_id, head_revision, etag_token, created_at)
			SELECT $1, cluster, machine, id, 1, $3, now() FROM assignment_revision WHERE id = $2`
	}
	res, err := tx.ExecContext(ctx, q, h.head, rev, etagToken())
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("%s %s: the draft introduces it without a revision", h.kind, h.key)
	}
	return nil
}

// seedStatuses inserts a retained status for each version the unit classified that has none, in
// (provider, object, version, created) order (dependency monitor §5.2). A KV version holds one
// identity (PA §3): a status of it under another creation time, a concurrent publication's
// included once that commits, keeps the version, and recheckStatuses refuses.
func seedStatuses(ctx context.Context, tx *sql.Tx, statuses []unitStatus) error {
	ordered := slices.Clone(statuses)
	slices.SortFunc(ordered, func(x, y unitStatus) int {
		return cmp.Or(strings.Compare(string(x.provider), string(y.provider)), strings.Compare(x.object, y.object),
			cmp.Compare(x.version, y.version), strings.Compare(createdText(x.result.Created), createdText(y.result.Created)))
	})
	for _, s := range ordered {
		if s.result.Class != classify.Retained {
			return fmt.Errorf("a %s status of class %s reached the publication commit", s.provider, s.result.Class)
		}
		var deletion sql.NullTime
		if s.result.Reason == classify.DeletionScheduled {
			deletion = sql.NullTime{Time: s.result.Deletion, Valid: true}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO dependency_status (id, provider, object, version, created, class, reason,
			deletion_observed, first_retained_at, observed_from, recorded_at, answer_date)
			VALUES ($1, $2, $3, $4, $5, 'retained', $6, $7, $8, $8, clock_timestamp(), $9)
			ON CONFLICT DO NOTHING`,
			id.New(id.Dependency), string(s.provider), s.object, s.version, createdText(s.result.Created),
			sql.NullString{String: string(s.result.Reason), Valid: s.result.Reason != classify.None}, deletion, s.began,
			sql.NullTime{Time: s.result.Date, Valid: !s.result.Date.IsZero()}); err != nil {
			return err
		}
	}
	return nil
}

// createdText is a provider creation time as the status and dependency rows keep it: RFC 3339 in
// UTC with its nanoseconds, trailing zeros dropped.
func createdText(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func insertReleaseMachine(ctx context.Context, tx *sql.Tx, rel, cluster string, m unitMachine) error {
	records := make([]provenanceRecord, 0, len(m.provenance))
	for _, r := range m.provenance {
		p := provenanceRecord{Reference: r.Reference, Version: r.Version, Encoding: r.Encoding,
			Source: provenanceSource{Revision: r.Source.Revision, Digest: r.Source.Digest, Path: r.SourcePath, Occurrence: r.Occurrence},
			Output: r.Output}
		if r.Member >= 0 {
			p.Member = &r.Member
		}
		if r.OverriddenBy != nil {
			p.OverriddenBy = &provenanceOrigin{Revision: r.OverriddenBy.Revision, Digest: r.OverriddenBy.Digest}
		}
		records = append(records, p)
	}
	provenance, err := json.Marshal(records)
	if err != nil {
		return err
	}
	cipherDigest := sha256.Sum256([]byte(m.ciphertext))
	var redacted sql.NullString
	if m.redacted != nil {
		redacted = sql.NullString{String: *m.redacted, Valid: true}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO release_machine (release, cluster, machine, import_base_revision,
		assignment_revision, mode, ciphertext, ciphertext_digest, configuration_digest, redacted, provenance, key_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`, rel, cluster, m.machine, m.importBase,
		sql.NullString{String: m.assignment, Valid: m.assignment != ""}, m.mode, string(m.ciphertext), cipherDigest[:],
		m.configuration[:], redacted, string(provenance), m.encryption.object)
	return err
}

func insertDependencies(ctx context.Context, tx *sql.Tx, rel string, m unitMachine) error {
	insert := func(kind, prov string, d unitDependency, withSource bool) error {
		var source, path sql.NullString
		var digest []byte
		var occurrence sql.NullInt64
		if withSource {
			source, path = sql.NullString{String: d.source, Valid: true}, sql.NullString{String: d.path, Valid: true}
			digest, occurrence = d.digest[:], sql.NullInt64{Int64: int64(d.occurrence), Valid: true}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO dependency (release, machine, kind, provider, object, version, created,
			reference, source_revision, source_digest, path, occurrence)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`, rel, m.machine, kind, prov, d.object, d.version,
			createdText(d.created), sql.NullString{String: d.reference, Valid: d.reference != ""}, source, digest, path, occurrence)
		return err
	}
	for _, d := range m.effective {
		if err := insert("effective", "kv", d, false); err != nil {
			return err
		}
	}
	for _, d := range m.reproduction {
		if err := insert("reproduction", "kv", d, true); err != nil {
			return err
		}
	}
	return insert("encryption", "transit", m.encryption, false)
}

// finishPublish moves j's running operation to state with its result or problem, clears its
// owner and lease, and appends the terminal event (§8.2), fenced on this owner's generation and
// epoch.
func (a *API) finishPublish(ctx context.Context, tx *sql.Tx, j publishJob, state string, result, problem, entry map[string]any) error {
	// An owner's own transition requires its epoch to be the current one (§5.1), read here also in
	// the failure transaction, which takes no other lock first.
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT epoch FROM installation_state FOR SHARE`).Scan(&current); err != nil {
		return err
	}
	if current != a.d.owner.Epoch {
		return staging.ErrFenced
	}
	var n int
	err := tx.QueryRowContext(ctx, `UPDATE operation SET state = $5, result = $6::jsonb, error = $7::jsonb,
		owner = NULL, owner_epoch = NULL, lease_until = NULL, last_event = last_event + 1
		WHERE id = $1 AND owner = $2 AND owner_gen = $3 AND owner_epoch = $4 AND state = 'running' RETURNING last_event`,
		j.op, a.d.owner.ID, j.gen, a.d.owner.Epoch, state, jsonOrNull(result), jsonOrNull(problem)).Scan(&n)
	if err != nil {
		return a.fenced(err)
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO operation_event (operation, number, epoch, kind, entry, at)
		VALUES ($1, $2, $3, 'publish', $4, now())`, j.op, n, a.d.owner.Epoch, string(b))
	return err
}

// releaseContent is what a release's digest covers (ruling R14): its cluster, draft revision and
// renderer record, each source by kind and name (or machine) with its revision ("" for a removal),
// and each machine with its import base, assignment revision, mode and configuration digest. No
// head identifier, ciphertext or time.
type releaseContent struct {
	cluster, draft string
	draftRev       int
	renderer       rendererBody
	sources        []contentSource
	machines       []contentMachine
}

type contentSource struct{ kind, key, revision string }

type contentMachine struct {
	machine, importBase, assignment, mode string
	configuration                         []byte
}

// digest is SHA-256 over a versioned encoding of c in which every string is length-prefixed and
// sources and machines are sorted, so no two contents share an encoding.
func (c releaseContent) digest() [32]byte {
	h := sha256.New()
	put := func(s string) {
		_ = binary.Write(h, binary.BigEndian, uint64(len(s)))
		h.Write([]byte(s))
	}
	count := func(n int) { _ = binary.Write(h, binary.BigEndian, uint64(n)) }
	sources := slices.Clone(c.sources)
	slices.SortFunc(sources, func(x, y contentSource) int {
		return cmp.Or(strings.Compare(x.kind, y.kind), strings.Compare(x.key, y.key))
	})
	machines := slices.Clone(c.machines)
	slices.SortFunc(machines, func(x, y contentMachine) int { return strings.Compare(x.machine, y.machine) })

	put("bronzeward-release-digest/1")
	put(c.cluster)
	put(c.draft)
	count(c.draftRev)
	put(c.renderer.Contract)
	put(c.renderer.MachineryVersion)
	put(c.renderer.MachineryChecksum)
	put(c.renderer.KubernetesVersion)
	count(len(sources))
	for _, s := range sources {
		put(s.kind)
		put(s.key)
		put(s.revision)
	}
	count(len(machines))
	for _, m := range machines {
		put(m.machine)
		put(m.importBase)
		put(m.assignment)
		put(m.mode)
		put(string(m.configuration))
	}
	var out [32]byte
	h.Sum(out[:0])
	return out
}

// rowsQuerier reads rows as well as one row.
type rowsQuerier interface {
	querier
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// releaseDigest is a stored release's content digest, recomputed from its stored rows.
func releaseDigest(ctx context.Context, q rowsQuerier, release string) ([32]byte, error) {
	var c releaseContent
	r := &c.renderer
	if err := q.QueryRowContext(ctx, `SELECT cluster, draft, draft_revision, contract, machinery_version, machinery_checksum,
		kubernetes_version FROM release WHERE id = $1`, release).Scan(&c.cluster, &c.draft, &c.draftRev, &r.Contract,
		&r.MachineryVersion, &r.MachineryChecksum, &r.KubernetesVersion); err != nil {
		return [32]byte{}, err
	}
	err := eachQueried(ctx, q, `SELECT kind, coalesce(name, machine), coalesce(fragment_revision, profile_revision,
		assignment_revision, '') FROM release_source WHERE release = $1`, release, func(row *sql.Rows) error {
		var s contentSource
		if err := row.Scan(&s.kind, &s.key, &s.revision); err != nil {
			return err
		}
		c.sources = append(c.sources, s)
		return nil
	})
	if err != nil {
		return [32]byte{}, err
	}
	err = eachQueried(ctx, q, `SELECT machine, import_base_revision, coalesce(assignment_revision, ''), mode,
		configuration_digest FROM release_machine WHERE release = $1`, release, func(row *sql.Rows) error {
		var m contentMachine
		if err := row.Scan(&m.machine, &m.importBase, &m.assignment, &m.mode, &m.configuration); err != nil {
			return err
		}
		c.machines = append(c.machines, m)
		return nil
	})
	if err != nil {
		return [32]byte{}, err
	}
	return c.digest(), nil
}

func eachQueried(ctx context.Context, q rowsQuerier, query string, arg any, fn func(*sql.Rows) error) error {
	rows, err := q.QueryContext(ctx, query, arg)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}
