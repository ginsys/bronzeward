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
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/compile"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/staging"
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
	base                      int
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
	if errors.Is(err, errRefused) {
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
	if err := tx.QueryRowContext(ctx, `SELECT epoch FROM installation_state FOR SHARE`).Scan(&epoch); err != nil {
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
	if _, err := tx.ExecContext(ctx, `SELECT id FROM machine WHERE id = ANY ($1) ORDER BY id FOR SHARE`, covered); err != nil {
		return "", nil, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT machine FROM machine_state WHERE machine = ANY ($1) ORDER BY machine FOR UPDATE`,
		covered); err != nil {
		return "", nil, err
	}
	// One pass over every existing head in id order, changed ones FOR UPDATE (§6.2).
	locked := slices.Clone(heads)
	slices.SortFunc(locked, func(x, y sourceHead) int { return strings.Compare(x.head, y.head) })
	for i := range locked {
		h := &locked[i]
		if h.head == "" {
			continue
		}
		k := headKinds[h.kind]
		mode := "SHARE"
		if h.changed {
			mode = "UPDATE"
		}
		if err := tx.QueryRowContext(ctx, `SELECT `+k.key+` FROM `+k.table+` WHERE id = $1 FOR `+mode, h.head).
			Scan(&h.key); err != nil {
			return "", nil, err
		}
	}
	keys := map[string]string{}
	for _, h := range locked {
		keys[h.head] = h.key
	}
	for i := range heads {
		if heads[i].head != "" {
			heads[i].key = keys[heads[i].head]
		}
	}
	var draftState string
	var draftRev int
	if err := tx.QueryRowContext(ctx, `SELECT state, revision FROM draft WHERE id = $1 FOR UPDATE`, j.draft).
		Scan(&draftState, &draftRev); err != nil {
		return "", nil, err
	}
	var by, role, opState string
	var owner, ownerEpoch, opRelease sql.NullString
	var gen int64
	if err := tx.QueryRowContext(ctx, `SELECT created_by, created_role, state, owner, owner_gen, owner_epoch,
		result->>'release' FROM operation WHERE id = $1 AND kind = 'publish' FOR UPDATE`, j.op).
		Scan(&by, &role, &opState, &owner, &gen, &ownerEpoch, &opRelease); err != nil {
		return "", nil, a.fenced(err)
	}
	mine := opState == "running" && owner.String == a.d.owner.ID && gen == j.gen && ownerEpoch.String == a.d.owner.Epoch

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
		case !mine:
			return "", nil, staging.ErrFenced
		case !same:
			return "", refuse(http.StatusConflict, "conflict", "the draft revision was published with other content").
				with("release", existing), nil
		}
		return existing, nil, a.finishPublish(ctx, tx, j, "succeeded", map[string]any{"release": existing}, nil,
			map[string]any{"type": "succeeded", "release": existing})
	case !errors.Is(err, sql.ErrNoRows):
		return "", nil, err
	}
	if !mine {
		return "", nil, staging.ErrFenced
	}

	if err := seedStatuses(ctx, tx, u.statuses); err != nil {
		return "", nil, err
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
		if err := moveHead(ctx, tx, h); err != nil {
			return "", nil, err
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
// the release leaves it at.
func moveHead(ctx context.Context, tx *sql.Tx, h *sourceHead) error {
	k := headKinds[h.kind]
	rev := sql.NullString{String: h.revision, Valid: h.revision != ""}
	if h.head != "" {
		return tx.QueryRowContext(ctx, `UPDATE `+k.table+` SET head_revision_id = $2, head_revision = head_revision + 1,
			etag_token = $3 WHERE id = $1 AND head_revision = $4 RETURNING head_revision`,
			h.head, rev, etagToken(), h.base).Scan(&h.base)
	}
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
// (provider, object, version, created) order (dependency monitor §5.2).
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
			ON CONFLICT (provider, object, version, created) DO NOTHING`,
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
			Source: provenanceSource{Revision: r.Source.Revision, Digest: r.Source.Digest, Path: r.SourcePath}, Output: r.Output}
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
		assignment_revision, mode, ciphertext, ciphertext_digest, configuration_digest, redacted, provenance)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`, rel, cluster, m.machine, m.importBase,
		sql.NullString{String: m.assignment, Valid: m.assignment != ""}, m.mode, string(m.ciphertext), cipherDigest[:],
		m.configuration[:], redacted, string(provenance))
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
