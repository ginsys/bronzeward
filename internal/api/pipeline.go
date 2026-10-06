package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
)

// snapshot is what a publication compiles (compilation.md §6 step 1), as one read-only
// repeatable-read transaction read it: the cluster's contract; the covered machines, each with
// its platform mode, import base, assignment revision ("" for none) and fragment revisions in
// composition order; every composed source's stored text and declarations; the heads the release
// uses unchanged; and each declared KV version's recorded created time (ruling R31).
type snapshot struct {
	contract  string
	machines  []snapshotMachine // in machine order
	sources   map[string]storedSource
	unchanged []usedHead
	recorded  map[pinKey]time.Time
}

type snapshotMachine struct {
	machine, mode, importBase, assignment string
	fragments                             []string
}

// storedSource is one source revision as stored: its sanitized text, its declarations, and the
// provider generation each reference resolves to.
type storedSource struct {
	document     string
	declarations ingest.Declarations
	generations  map[string]string
}

// pinKey is one KV version: a generation path and a version.
type pinKey struct {
	path    string
	version int64
}

// sourceState is one fragment, profile or assignment as the draft leaves it: the draft's entry
// when it has one, else the head. revision "" is none (removed, or never published); head is ""
// when no head exists.
type sourceState struct {
	revision, head string
	headRevision   int
	entry          bool
}

// readSnapshot reads j's snapshot. The draft must be open at the revision the operation bound,
// else 409 as T3 refuses it. A machine is covered when it has an import base after the draft
// (ruling R21'); a draft that changes the assignment of a machine without one, or that covers no
// machine, is refused 422. Every profile and assignment the release names is held to §3.1's pin
// and selection rules again (ruling R30), refused 422 naming it and the body path.
func (a *API) readSnapshot(ctx context.Context, j publishJob) (snapshot, *refusal, error) {
	tx, err := a.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return snapshot{}, nil, err
	}
	defer func() { _ = tx.Rollback() }() // it writes nothing
	r := snapshotReader{ctx: ctx, tx: tx, j: j}
	s, ref, err := r.read()
	if err != nil || ref != nil {
		return snapshot{}, ref, err
	}
	return s, nil, nil
}

type snapshotReader struct {
	ctx context.Context
	tx  *sql.Tx
	j   publishJob
	// states by kind, then by name (by machine for an assignment)
	states map[string]map[string]sourceState
}

func (r *snapshotReader) query(query string, fn func(*sql.Rows) error, args ...any) error {
	rows, err := r.tx.QueryContext(r.ctx, query, args...)
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

func (r *snapshotReader) read() (snapshot, *refusal, error) {
	var s snapshot
	var state, cluster string
	var rev int
	if err := r.tx.QueryRowContext(r.ctx, `SELECT d.state, d.revision, d.cluster, c.contract FROM draft d
		JOIN cluster c ON c.id = d.cluster WHERE d.id = $1`, r.j.draft).Scan(&state, &rev, &cluster, &s.contract); err != nil {
		return s, nil, err
	}
	switch {
	case cluster != r.j.cluster:
		return s, nil, fmt.Errorf("draft %s is of cluster %s, not the operation's %s", r.j.draft, cluster, r.j.cluster)
	case state != "open":
		return s, refuse(http.StatusConflict, "conflict", "the draft is no longer open").with("draft", r.j.draft), nil
	case rev != r.j.draftRev:
		return s, refuse(http.StatusConflict, "conflict", "the draft moved after the publication bound it").with("draft", r.j.draft), nil
	}
	if err := r.readStates(); err != nil {
		return s, nil, err
	}

	// The covered machines (ruling R21'): an import base after the draft, its entry's or its
	// Applied release's (§3.2).
	var machines []snapshotMachine
	err := r.query(`SELECT m.id, m.platform, coalesce(e.import_base_revision, rm.import_base_revision, '')
		FROM machine m
		LEFT JOIN draft_entry e ON e.draft = $1 AND e.machine = m.id
		LEFT JOIN machine_state st ON st.machine = m.id
		LEFT JOIN release_machine rm ON rm.release = st.applied_release AND rm.machine = m.id
		WHERE m.cluster = $2 ORDER BY m.id`, func(row *sql.Rows) error {
		var m snapshotMachine
		if err := row.Scan(&m.machine, &m.mode, &m.importBase); err != nil {
			return err
		}
		machines = append(machines, m)
		return nil
	}, r.j.draft, r.j.cluster)
	if err != nil {
		return s, nil, err
	}
	for _, m := range machines {
		if m.importBase != "" {
			m.assignment = r.states["assignment"][m.machine].revision
			s.machines = append(s.machines, m)
		} else if r.states["assignment"][m.machine].entry {
			return s, refuse(http.StatusUnprocessableEntity, "validation-failed",
				"Publication refused: the draft changes the assignment of a machine with no import base.").
				with("machine", m.machine), nil
		}
	}
	if len(s.machines) == 0 {
		return s, refuse(http.StatusUnprocessableEntity, "validation-failed",
			"Publication refused: no machine of the cluster has an import base.").with("cluster", r.j.cluster), nil
	}

	sel, ref, err := r.readSelections(s.machines)
	if err != nil || ref != nil {
		return s, ref, err
	}
	for i := range s.machines {
		s.machines[i].fragments = sel.compose(r, s.machines[i].assignment)
	}
	s.unchanged = r.unchanged(s.machines, sel)
	if s.sources, err = r.readSources(s.machines); err != nil {
		return s, nil, err
	}
	if s.recorded, err = r.readRecorded(s.sources); err != nil {
		return s, nil, err
	}
	return s, nil, nil
}

// readStates reads every fragment, profile and assignment of the cluster as the draft leaves it.
func (r *snapshotReader) readStates() error {
	r.states = map[string]map[string]sourceState{"fragment": {}, "profile": {}, "assignment": {}}
	err := r.query(`SELECT 'fragment', name, id, coalesce(head_revision_id, ''), head_revision FROM fragment WHERE cluster = $1
		UNION ALL SELECT 'profile', name, id, coalesce(head_revision_id, ''), head_revision FROM profile WHERE cluster = $1
		UNION ALL SELECT 'assignment', machine, id, coalesce(head_revision_id, ''), head_revision FROM assignment WHERE cluster = $1`,
		func(row *sql.Rows) error {
			var kind, key string
			var st sourceState
			if err := row.Scan(&kind, &key, &st.head, &st.revision, &st.headRevision); err != nil {
				return err
			}
			r.states[kind][key] = st
			return nil
		}, r.j.cluster)
	if err != nil {
		return err
	}
	return r.query(`SELECT kind, coalesce(name, machine), coalesce(fragment_revision, profile_revision, assignment_revision, '')
		FROM draft_source_entry WHERE draft = $1`, func(row *sql.Rows) error {
		var kind, key, revision string
		if err := row.Scan(&kind, &key, &revision); err != nil {
			return err
		}
		st := r.states[kind][key]
		st.revision, st.entry = revision, true
		r.states[kind][key] = st
		return nil
	}, r.j.draft)
}

// selections is what the release's assignment and profile revisions select: each assignment's
// profiles in order and fragments by layer in order, each profile's pins in order, and every
// fragment revision's name and layer that these name or resolve to.
type selections struct {
	profiles  map[string][]string            // assignment revision -> profile names
	fragments map[string]map[string][]string // assignment revision -> layer -> fragment names
	pins      map[string][]string            // profile revision -> fragment revisions
	named     map[string]string              // profile revision -> its name
	revisions map[string][2]string           // fragment revision -> name, layer
}

// readSelections reads the selections of the covered machines' assignments and of every profile
// revision the release names (the draft's own and those the assignments select), and holds each to
// §3.1's rules as the draft leaves the heads (ruling R30).
func (r *snapshotReader) readSelections(machines []snapshotMachine) (selections, *refusal, error) {
	sel := selections{profiles: map[string][]string{}, fragments: map[string]map[string][]string{},
		pins: map[string][]string{}, named: map[string]string{}, revisions: map[string][2]string{}}
	var assignments []string
	for _, m := range machines {
		if m.assignment != "" {
			assignments = append(assignments, m.assignment)
			sel.fragments[m.assignment] = map[string][]string{}
		}
	}
	err := r.query(`SELECT revision, profile FROM assignment_revision_profile WHERE revision = ANY ($1) ORDER BY revision, position`,
		func(row *sql.Rows) error {
			var asr, name string
			if err := row.Scan(&asr, &name); err != nil {
				return err
			}
			sel.profiles[asr] = append(sel.profiles[asr], name)
			return nil
		}, assignments)
	if err != nil {
		return sel, nil, err
	}
	err = r.query(`SELECT revision, layer, fragment FROM assignment_revision_fragment WHERE revision = ANY ($1)
		ORDER BY revision, layer, position`, func(row *sql.Rows) error {
		var asr, layer, name string
		if err := row.Scan(&asr, &layer, &name); err != nil {
			return err
		}
		sel.fragments[asr][layer] = append(sel.fragments[asr][layer], name)
		return nil
	}, assignments)
	if err != nil {
		return sel, nil, err
	}

	// The release's profile revisions: the draft's, and each one a covered assignment selects.
	var profiles []string
	addProfile := func(name string) {
		if prv := r.states["profile"][name].revision; prv != "" && sel.named[prv] == "" {
			sel.named[prv] = name
			profiles = append(profiles, prv)
		}
	}
	for name, st := range r.states["profile"] {
		if st.entry {
			addProfile(name)
		}
	}
	for _, asr := range assignments {
		for _, name := range sel.profiles[asr] {
			addProfile(name)
		}
	}
	err = r.query(`SELECT revision, fragment_revision FROM profile_revision_fragment WHERE revision = ANY ($1)
		ORDER BY revision, position`, func(row *sql.Rows) error {
		var prv, frv string
		if err := row.Scan(&prv, &frv); err != nil {
			return err
		}
		sel.pins[prv] = append(sel.pins[prv], frv)
		return nil
	}, profiles)
	if err != nil {
		return sel, nil, err
	}

	// The name and layer of every pinned revision and of every selected name's revision.
	var frvs []string
	for _, pins := range sel.pins {
		frvs = append(frvs, pins...)
	}
	for _, layers := range sel.fragments {
		for _, names := range layers {
			for _, name := range names {
				if frv := r.states["fragment"][name].revision; frv != "" {
					frvs = append(frvs, frv)
				}
			}
		}
	}
	err = r.query(`SELECT id, name, layer FROM fragment_revision WHERE id = ANY ($1)`, func(row *sql.Rows) error {
		var frv, name, layer string
		if err := row.Scan(&frv, &name, &layer); err != nil {
			return err
		}
		sel.revisions[frv] = [2]string{name, layer}
		return nil
	}, frvs)
	if err != nil {
		return sel, nil, err
	}

	// §3.1 again (ruling R30): every pin is its fragment's revision after the draft; every selected
	// name has a revision after the draft, a fragment one in the layer it is listed under.
	slices.SortFunc(profiles, func(x, y string) int { return strings.Compare(sel.named[x], sel.named[y]) })
	for _, prv := range profiles {
		for i, frv := range sel.pins[prv] {
			if r.states["fragment"][sel.revisions[frv][0]].revision != frv {
				return sel, refuse(http.StatusUnprocessableEntity, "validation-failed",
					"Publication refused: a pin is not its fragment's revision after the draft.").
					with("profile", sel.named[prv]).with("path", fmt.Sprintf("fragments[%d]", i)).with("revision", frv), nil
			}
		}
	}
	for _, m := range machines {
		if m.assignment == "" {
			continue
		}
		invalid := func(path string, i int) *refusal {
			return refuse(http.StatusUnprocessableEntity, "validation-failed",
				"Publication refused: an assignment selects a name with no revision after the draft, or a fragment outside its layer.").
				with("machine", m.machine).with("path", fmt.Sprintf("%s[%d]", path, i))
		}
		for i, name := range sel.profiles[m.assignment] {
			if r.states["profile"][name].revision == "" {
				return sel, invalid("profiles", i), nil
			}
		}
		for _, layer := range fragmentLayers {
			for i, name := range sel.fragments[m.assignment][layer] {
				frv := r.states["fragment"][name].revision
				if frv == "" || sel.revisions[frv][1] != layer {
					return sel, invalid("fragments."+layer, i), nil
				}
			}
		}
	}
	return sel, nil, nil
}

// compose is an assignment's fragment revisions in composition order (ruling R23): per layer, the
// profiles' pins of that layer by profile then pin position, then the assignment's own fragments
// of that layer by position; a revision selected twice composes once, at its first position
// (choice §16.31).
func (sel selections) compose(r *snapshotReader, asr string) []string {
	if asr == "" {
		return nil
	}
	out := []string{}
	seen := map[string]bool{}
	add := func(frv string) {
		if !seen[frv] {
			seen[frv] = true
			out = append(out, frv)
		}
	}
	for _, layer := range fragmentLayers {
		for _, name := range sel.profiles[asr] {
			for _, frv := range sel.pins[r.states["profile"][name].revision] {
				if sel.revisions[frv][1] == layer {
					add(frv)
				}
			}
		}
		for _, name := range sel.fragments[asr][layer] {
			add(r.states["fragment"][name].revision)
		}
	}
	return out
}

// unchanged is every head the release names without a draft entry: each covered machine's
// assignment, each profile its assignments select, and each fragment they select or a named
// profile pins, at the revision and head revision read.
func (r *snapshotReader) unchanged(machines []snapshotMachine, sel selections) []usedHead {
	used := map[[2]string]bool{}
	for _, m := range machines {
		used[[2]string{"assignment", m.machine}] = true
		for _, name := range sel.profiles[m.assignment] {
			used[[2]string{"profile", name}] = true
		}
		for _, names := range sel.fragments[m.assignment] {
			for _, name := range names {
				used[[2]string{"fragment", name}] = true
			}
		}
	}
	for _, pins := range sel.pins {
		for _, frv := range pins {
			used[[2]string{"fragment", sel.revisions[frv][0]}] = true
		}
	}
	var out []usedHead
	for k := range used {
		st := r.states[k[0]][k[1]]
		if !st.entry && st.head != "" {
			out = append(out, usedHead{kind: k[0], head: st.head, revision: st.revision, headRevision: st.headRevision})
		}
	}
	slices.SortFunc(out, func(x, y usedHead) int { return strings.Compare(x.head, y.head) })
	return out
}

// readSources reads every composed source revision's stored text, embedded identifications and
// reference rows.
func (r *snapshotReader) readSources(machines []snapshotMachine) (map[string]storedSource, error) {
	var revisions []string
	for _, m := range machines {
		revisions = append(revisions, m.importBase)
		revisions = append(revisions, m.fragments...)
	}
	slices.Sort(revisions)
	revisions = slices.Compact(revisions)
	out := map[string]storedSource{}
	err := r.query(`SELECT id, document, embedded FROM import_base_revision WHERE id = ANY ($1)
		UNION ALL SELECT id, document, embedded FROM fragment_revision WHERE id = ANY ($1)`, func(row *sql.Rows) error {
		var revision string
		var embedded []byte
		s := storedSource{declarations: ingest.Declarations{References: map[string]ingest.Reference{}}, generations: map[string]string{}}
		if err := row.Scan(&revision, &s.document, &embedded); err != nil {
			return err
		}
		if err := json.Unmarshal(embedded, &s.declarations.Embedded); err != nil {
			return fmt.Errorf("source %s: embedded: %w", revision, err)
		}
		if s.declarations.Embedded == nil {
			s.declarations.Embedded = []ingest.Embedded{}
		}
		out[revision] = s
		return nil
	}, revisions)
	if err != nil {
		return nil, err
	}
	if len(out) != len(revisions) {
		return nil, fmt.Errorf("%d of %d composed source revisions read", len(out), len(revisions))
	}
	err = r.query(`SELECT revision, name, kind, version, coalesce(encoding, ''), generation FROM import_base_reference
			WHERE revision = ANY ($1)
		UNION ALL SELECT revision, name, kind, version, coalesce(encoding, ''), generation FROM fragment_reference
			WHERE revision = ANY ($1)`, func(row *sql.Rows) error {
		var revision, name, kind, generation string
		var ref ingest.Reference
		if err := row.Scan(&revision, &name, &kind, &ref.Version, &ref.Encoding, &generation); err != nil {
			return err
		}
		ref.Kind = provider.Kind(kind)
		out[revision].declarations.References[name] = ref
		out[revision].generations[name] = generation
		return nil
	}, revisions)
	return out, err
}

// readRecorded reads each declared KV version's recorded created time, its status's: a KV version
// holds one (dependency_status_kv_version). A version with none is absent.
func (r *snapshotReader) readRecorded(sources map[string]storedSource) (map[pinKey]time.Time, error) {
	var paths []string
	var versions []int64
	seen := map[pinKey]bool{}
	for _, s := range sources {
		for name, ref := range s.declarations.References {
			k := pinKey{s.generations[name], ref.Version}
			if !seen[k] {
				seen[k] = true
				paths, versions = append(paths, k.path), append(versions, k.version)
			}
		}
	}
	out := map[pinKey]time.Time{}
	err := r.query(`SELECT object, version, created FROM dependency_status
		WHERE provider = 'kv' AND (object, version) IN (SELECT * FROM unnest($1::text[], $2::bigint[]))`, func(row *sql.Rows) error {
		var k pinKey
		var created string
		if err := row.Scan(&k.path, &k.version, &created); err != nil {
			return err
		}
		t, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return fmt.Errorf("status of %s version %d: created: %w", k.path, k.version, err)
		}
		out[k] = t
		return nil
	}, paths, versions)
	return out, err
}
