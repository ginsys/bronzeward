package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
)

// The scope mark (execution-recovery.md §7.3 steps 3, 4 and 6, §7.4; persistence-api.md §9.2): a
// recovery-admin's act that marks one accounted scope ready, blocked or unresolved from evidence the
// request itself takes (ruling M1). Before its transaction it takes a restoration observation of the
// machine and checks the dependencies dispatch would check at use time for its Desired release
// (choice §10.20). Its transaction takes the key lock, the installation state FOR SHARE, the machine
// FOR UPDATE, the marking identity's principal FOR SHARE, not revoked, and every non-terminal
// apply-config operation on it FOR UPDATE in id order (§5 rule 5); the scope state entry is written
// once the act-order lock is held (rule 4).

func scopeMarkRoute() effectRoute {
	return effectRoute{action: "recovery.mark", input: func() input { return &scopeMarkRequest{} }, prepare: prepareMark,
		effect: markScope}
}

// scopeMarkRequest carries only the facts the marking identity records with the mark: the state is
// the evidence's, never the request's.
type scopeMarkRequest struct {
	Facts map[string]string `json:"facts"`
}

func (in *scopeMarkRequest) check(*API) error { return checkFacts(in.Facts) }

// markEvidence is what a mark's request read before its transaction: the machine, the restoration
// observation, the Desired release it checked (empty for none), and the dependency results.
type markEvidence struct {
	machine, observation, release string
	deps                          map[string]any
	missing                       []string // the dependencies that failed, in order
	otherAccess                   bool     // the observation used another Talos access version than the check passed
}

// scopeMarkBody is a mark's answer: the state the scope moved from and to, the restoration
// observation it rests on, and what is missing, in the order the evidence is checked.
type scopeMarkBody struct {
	Machine      string         `json:"machine"`
	From         string         `json:"from"`
	State        string         `json:"state"`
	Observation  string         `json:"observation"`
	Missing      []string       `json:"missing"`
	Dependencies map[string]any `json:"dependencies"`
	Revision     int64          `json:"revision"`
	MarkedBy     string         `json:"markedBy"`
	Role         string         `json:"role"`
	Act          string         `json:"act"`
	Epoch        string         `json:"epoch"`
	At           time.Time      `json:"at"`
}

// markRefusal is the refusal of a mark on a scope in state, or nil where one may be marked: one
// still pre-restore unaccounted leaves only by the step 1 decision (§7.4), and a released one only
// by recovery-mode exit or a new entry.
func markRefusal(m, state string) *refusal {
	switch state {
	case "blocked", "unresolved", "ready":
		return nil
	case "pre-restore-unaccounted":
		return refuse(http.StatusConflict, "recovery-mode-active", "the scope is still pre-restore unaccounted: "+
			"it leaves only by the post-restore accounting decision").with("machine", m).with("scope", state)
	}
	return refuse(http.StatusConflict, "conflict", "the scope is "+state+" and is not marked").with("machine", m).with("scope", state)
}

// prepareMark refuses what the transaction would refuse before anything is read, then takes the
// restoration observation (§7.3 step 4, after step 1) and the dependency check (step 3) under the
// executor identity: the cluster's Talos access, and the decryption of the Desired release's
// artifact for the machine. A machine with no Desired release has no release a plan could target,
// so only the access is checked (ruling M6).
func prepareMark(ctx context.Context, a *API, q *request) error {
	m := q.r.PathValue("machine")
	if id.MustHave(m, id.Machine) != nil {
		return refuse(http.StatusNotFound, "not-found", "no such machine")
	}
	var recovery bool
	var cluster, state string
	var desired, ct, key sql.NullString
	var version sql.NullInt64
	err := a.db.QueryRowContext(ctx, `SELECT i.recovery_mode, m.cluster, m.scope_state, s.desired, rm.ciphertext, rm.key_name, rm.key_version
		FROM installation_state i, machine m JOIN machine_state s ON s.machine = m.id
		LEFT JOIN release_machine rm ON rm.release = s.desired AND rm.machine = m.id WHERE m.id = $1`, m).Scan(&recovery, &cluster,
		&state, &desired, &ct, &key, &version)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return refuse(http.StatusNotFound, "not-found", "no such machine").with("machine", m)
	case err != nil:
		return err
	case !recovery:
		return refuse(http.StatusConflict, "conflict", "recovery mode is not in effect")
	}
	if ref := markRefusal(m, state); ref != nil {
		return ref
	}
	if a.d.exe == nil || a.d.owner.ID == "" { // after the refusals, which hold in every deployment
		return refuse(http.StatusServiceUnavailable, "dependency-unavailable", "no executor is configured; nothing was recorded")
	}
	obs, err := a.observe(ctx, m, observeFor{purpose: "restoration"})
	if err != nil {
		return err
	}
	ev := &markEvidence{machine: m, observation: obs.ID, release: desired.String}
	var art *boundArtifact
	if desired.Valid { // the Desired release's record for the machine exists: machine_state references it
		art = &boundArtifact{ciphertext: ct.String, key: key.String, version: int(version.Int64)}
	}
	var u useTime
	u, ev.deps, _, err = a.checkDependencies(ctx, cluster, "restoration mark of "+m, art, nil)
	if err != nil {
		return err
	}
	// The check read the access itself: an observation made with another version of it is not evidence
	// of the credential the check passed.
	if d, ok := ev.deps["access"].(map[string]any); ok && d["result"] == "passed" && obs.Access != nil {
		ev.otherAccess = accessKey(*obs.Access) != accessKey(u.access.Version())
	}
	for _, name := range []string{"access", "artifact"} {
		if d, ok := ev.deps[name].(map[string]any); ok && d["result"] == "failed" {
			ev.missing = append(ev.missing, name)
		}
	}
	q.mark = ev
	return nil
}

// markScope is step 6's mark under the locks above, from the evidence prepareMark took: unresolved
// while a non-terminal operation holds the scope; otherwise blocked naming each missing item — the
// observation, when it did not read the identity, does not follow the step 1 decision, is followed
// by an operation change (§6.1 excludes it from drift) or used another access version than the check; the
// identity, when the one read is not the one the machine record binds (§3.2 comparison 3, choice
// §10.26); each failed dependency — and ready when nothing is missing.
func markScope(ctx context.Context, a *API, tx *sql.Tx, q *request) (result, error) {
	ev := q.mark
	if !q.recovery {
		return result{}, refuse(http.StatusConflict, "conflict", "recovery mode is not in effect")
	}
	// The lock is its own statement: a statement that waits on it keeps the snapshot it started with,
	// so Desired, which publication moves under the machine FOR SHARE, is read by the next one.
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM machine WHERE id = $1 FOR UPDATE`, ev.machine); err != nil {
		return result{}, err
	}
	var state string
	var desired sql.NullString
	// quiet is §6.1's exclusion read the other way: no commitment or operation state change at or
	// after the observation's basis, so its drift rules applied to it.
	var read, matches, accounted, quiet bool
	err := tx.QueryRowContext(ctx, `SELECT m.scope_state, s.desired, o.unread->'identity' IS NULL, coalesce(`+identityMatch+`, false),
			coalesce(o.basis > sa.revision, false), NOT EXISTS (SELECT FROM machine_event e WHERE e.machine = m.id
				AND e.revision >= o.basis AND e.kind IN ('commitment', 'operation-state'))
		FROM machine m JOIN cluster c ON c.id = m.cluster JOIN machine_state s ON s.machine = m.id
		JOIN observation o ON o.id = $2 AND o.machine = m.id
		LEFT JOIN scope_accounting sa ON sa.machine = m.id AND sa.epoch = $3
		WHERE m.id = $1`, ev.machine, ev.observation, q.epoch).Scan(&state, &desired, &read, &matches, &accounted, &quiet)
	if err != nil {
		return result{}, err
	}
	var revoked bool
	if err := tx.QueryRowContext(ctx, `SELECT revoked FROM principal WHERE id = $1 FOR SHARE`, q.principal.ID).Scan(&revoked); err != nil {
		return result{}, err
	}
	if revoked {
		return result{}, refuse(http.StatusForbidden, "identity-revoked", "")
	}
	if ref := markRefusal(ev.machine, state); ref != nil {
		return result{}, ref
	}
	if desired.String != ev.release {
		return result{}, refuse(http.StatusConflict, "conflict", "the Desired release changed while the scope was checked; "+
			"nothing was marked").with("machine", ev.machine)
	}
	var held int
	if err := tx.QueryRowContext(ctx, `WITH l AS (SELECT id FROM operation WHERE machine = $1 AND kind = 'apply-config'
		AND state IN ('committed', 'sending', 'verifying', 'unresolved') ORDER BY id FOR UPDATE) SELECT count(*) FROM l`,
		ev.machine).Scan(&held); err != nil {
		return result{}, err
	}
	b := &scopeMarkBody{Machine: ev.machine, From: state, Observation: ev.observation, Missing: []string{}, Dependencies: ev.deps,
		MarkedBy: q.principal.ID, Role: string(q.role), Act: q.actID, Epoch: q.epoch}
	if !read || !accounted || !quiet || ev.otherAccess {
		b.Missing = append(b.Missing, "observation")
	}
	if read && !matches { // an identity read and not the record's is missing whether or not the observation counts
		b.Missing = append(b.Missing, "identity")
	}
	b.Missing = append(b.Missing, ev.missing...)
	switch {
	case held > 0:
		b.State = "unresolved"
	case len(b.Missing) > 0:
		b.State = "blocked"
	default:
		b.State = "ready"
	}
	facts := map[string]any{}
	for k, v := range q.input.(*scopeMarkRequest).Facts {
		facts[k] = v
	}
	write := func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE machine SET scope_state = $2 WHERE id = $1`, ev.machine, b.State); err != nil {
			return err
		}
		rev, when, err := a.appendEntry(ctx, tx, operationLock{machine: ev.machine, current: q.epoch}, "scope-state", map[string]any{
			"from": state, "to": b.State, "cause": "restoration-mark", "markedBy": b.MarkedBy, "role": b.Role, "act": b.Act,
			"observation": ev.observation, "missing": slices.Clone(b.Missing), "dependencies": ev.deps, "facts": facts})
		b.Revision, b.At = rev, when
		return err
	}
	return result{status: http.StatusCreated, location: prefix + "/machines/" + ev.machine, body: b, subjects: []string{ev.machine},
		atActOrder: write}, nil
}
