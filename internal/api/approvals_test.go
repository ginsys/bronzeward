package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
)

func (p *planEnv) approve(token, k, plan string) *httptest.ResponseRecorder {
	return p.do(p.api, call{method: "POST", path: prefix + "/plans/" + plan + "/approvals", token: token, key: k, body: `{}`})
}

// principalOf returns the human principal for sub at e's issuer, recording it as a first sign-in
// would when it has none yet.
func (e *env) principalOf(sub string) string {
	e.t.Helper()
	mustExec(e.t, e.db, `INSERT INTO principal (id, kind, iss, sub, created_at) VALUES ($1, 'human', $2, $3, now())
		ON CONFLICT (iss, sub) DO NOTHING`, id.New(id.Principal), e.iss.URL, sub)
	var v string
	if err := e.db.QueryRow(`SELECT id FROM principal WHERE iss = $1 AND sub = $2`, e.iss.URL, sub).Scan(&v); err != nil {
		e.t.Fatal(err)
	}
	return v
}

// §10.5: an approval records every self-approval reason that holds, in the table's order. A
// release contains its sources' revisions and its machines' import base revisions; its draft
// introduced the revisions of its own entries; the test robot's responsible human is h-all.
func TestApprovalSelfApproval(t *testing.T) {
	// authoredBy makes sub the author of every revision the releases contain, the seed still
	// publishing them.
	authoredBy := func(sub string) func(*draftEnv) {
		return func(d *draftEnv) { d.publishedBy, d.seed = d.seed, d.principalOf(sub) }
	}
	// introduce makes the target release's draft name its fragment revision as an entry.
	introduce := func(p *planEnv) {
		mustExec(t, p.db, `INSERT INTO draft_source_entry (draft, cluster, kind, name, fragment_revision)
			SELECT r.draft, r.cluster, 'fragment', f.name, f.id FROM release r JOIN fragment_revision f ON f.id = $2
			WHERE r.id = $1`, p.target.rel, p.target.frv)
	}
	for _, tc := range []struct {
		name              string
		setup             func(*draftEnv)
		after             func(*planEnv)
		creator, approver string
		want              []string
	}{
		{name: "none", creator: "robot", approver: "h-approver", want: []string{}},
		{name: "created the plan", creator: "h-all", approver: "h-all", want: []string{"created-plan"}},
		{name: "published", setup: func(d *draftEnv) { d.publishedBy = d.principalOf("h-approver") }, creator: "robot",
			approver: "h-approver", want: []string{"published"}},
		{name: "authored reused revisions", setup: authoredBy("h-approver"), creator: "robot", approver: "h-approver",
			want: []string{"authored-reused"}},
		{name: "authored an introduced revision", setup: authoredBy("h-approver"), after: introduce, creator: "robot",
			approver: "h-approver", want: []string{"authored-change", "authored-reused"}},
		{name: "automation created the plan", creator: "robot", approver: "h-all", want: []string{"owned-automation"}},
		{name: "automation published", setup: func(d *draftEnv) { d.publishedBy = d.robotID }, creator: "h-publisher",
			approver: "h-all", want: []string{"owned-automation"}},
		{name: "automation authored", setup: func(d *draftEnv) { d.publishedBy, d.seed = d.seed, d.robotID },
			creator: "h-publisher", approver: "h-all", want: []string{"owned-automation"}},
		{name: "every reason in order", setup: func(d *draftEnv) {
			h := d.principalOf("h-all")
			d.seed, d.publishedBy = h, h
		}, after: introduce, creator: "h-all", approver: "h-all",
			want: []string{"created-plan", "published", "authored-change", "authored-reused"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var setup []func(*draftEnv)
			if tc.setup != nil {
				setup = append(setup, tc.setup)
			}
			p := newPlanEnv(t, setup...)
			if tc.after != nil {
				tc.after(p)
			}
			creator := p.robot
			if tc.creator != "robot" {
				creator = p.human(tc.creator)
			}
			plan := decode[planBody](t, p.plan(creator, "k-plan-0123456789ab", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
			b := decode[approvalBody](t, p.approve(p.human(tc.approver), "k-approve-0123456789", plan.ID), http.StatusCreated)
			if want := (selfApproval{Marked: len(tc.want) > 0, Reasons: tc.want}); !reflect.DeepEqual(b.SelfApproval, want) {
				t.Fatalf("self-approval %+v, want %+v", b.SelfApproval, want)
			}
			if n := count(t, p.db, `SELECT count(*) FROM approval a JOIN machine_event e ON e.machine = a.machine AND e.revision = a.revision
				WHERE a.id = $1 AND a.self_approval = string_to_array($2, ',') AND e.entry->'selfApproval' = $3::jsonb`,
				b.ID, strings.Join(tc.want, ","), mustJSON(t, b.SelfApproval)); n != 1 {
				t.Fatalf("stored self-approval differs from %+v", b.SelfApproval)
			}
		})
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// T5a (persistence-api.md §5, §9.2): a human approver's approval of a proposed plan is one approval
// entry on the plan's machine's timeline, recorded under the current epoch with its self-approval
// mark; the plan reads approved, naming it, at its next revision, and the act names the approval,
// the plan and the machine. A replay answers the same approval.
func TestApproval(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	plan := decode[planBody](t, p.plan(p.robot, "k-plan-0123456789ab", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
	bearer := p.human("h-approver")
	rec := p.approve(bearer, "k-approve-0123456789", plan.ID)
	b := decode[approvalBody](t, rec, http.StatusCreated)
	if loc := rec.Header().Get("Location"); loc != prefix+"/approvals/"+b.ID || id.MustHave(b.ID, id.Approval) != nil {
		t.Fatalf("Location %q, id %q", loc, b.ID)
	}
	approver, ep := p.principalOf("h-approver"), epoch(t, p.db)
	want := approvalBody{ID: b.ID, Plan: plan.ID, Approver: approvalBy{Principal: approver, Role: "approver"}, Epoch: ep,
		SelfApproval: selfApproval{Marked: false, Reasons: []string{}}}
	if !reflect.DeepEqual(b, want) {
		t.Fatalf("approval %+v, want %+v", b, want)
	}
	var (
		at, entryAt, updated time.Time
		rev                  int64
		entry, act           string
	)
	if err := p.db.QueryRow(`SELECT a.at, a.revision, a.act, e.at, e.entry::text, s.updated_at
		FROM approval a JOIN machine_event e ON e.machine = a.machine AND e.revision = a.revision AND e.kind = 'approval'
			JOIN plan_state s ON s.plan = a.plan
		WHERE a.id = $1 AND a.plan = $2 AND a.machine = $3 AND a.approver = $4 AND a.role = 'approver' AND a.epoch = $5
			AND a.self_approval = '{}' AND e.epoch = $5`, b.ID, plan.ID, p.machine, approver, ep).Scan(&at, &rev, &act, &entryAt,
		&entry, &updated); err != nil {
		t.Fatal(err)
	}
	if rev != plan.TimelineRevision+1 || !entryAt.Equal(at) || !updated.Equal(at) {
		t.Fatalf("approval at %s revision %d, entry at %s, state at %s; plan entry %d", at, rev, entryAt, updated, plan.TimelineRevision)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(entry), &got); err != nil {
		t.Fatal(err)
	}
	wantEntry := map[string]any{"plan": plan.ID, "approval": b.ID, "principal": approver, "role": "approver",
		"selfApproval": map[string]any{"marked": false, "reasons": []any{}}}
	if !reflect.DeepEqual(got, wantEntry) {
		t.Fatalf("timeline entry %v, want %v", got, wantEntry)
	}
	if n := count(t, p.db, `SELECT count(*) FROM act WHERE id = $1 AND action = 'plan.approve' AND principal = $2 AND role = 'approver'
		AND subjects = ARRAY[$3, $4, $5]`, act, approver, b.ID, plan.ID, p.machine); n != 1 {
		t.Fatalf("%d approval acts", n)
	}
	read := decode[planBody](t, p.do(p.api, call{method: "GET", path: prefix + "/plans/" + plan.ID, token: p.human("h-viewer")}),
		http.StatusOK)
	if read.State != "approved" || read.Approval == nil || *read.Approval != b.ID || read.Revision != 2 {
		t.Fatalf("plan after approval: state %q, approval %v, revision %d", read.State, read.Approval, read.Revision)
	}
	if again := decode[approvalBody](t, p.approve(bearer, "k-approve-0123456789", plan.ID), http.StatusCreated); !reflect.DeepEqual(again, b) {
		t.Fatalf("replay %+v, want %+v", again, b)
	}
}

// wantUnapproved fails unless plan has no approval and its stored state is still state.
func (p *planEnv) wantUnapproved(t *testing.T, plan, state string) {
	t.Helper()
	if n := count(t, p.db, `SELECT count(*) FROM approval WHERE plan = $1`, plan); n != 0 {
		t.Fatalf("%d approvals of %s", n, plan)
	}
	if n := count(t, p.db, `SELECT count(*) FROM plan_state WHERE plan = $1 AND state = $2 AND approval IS NULL`, plan, state); n != 1 {
		t.Fatalf("plan %s is no longer %s", plan, state)
	}
}

// T5a's refusals (§9.4): a plan that does not exist; a second approval in one epoch; a plan that
// is cancelled, or past its expiry though its state was never written; a machine whose scope is
// pre-restore unaccounted. None writes anything.
func TestApprovalRefusals(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	approver := p.human("h-approver")
	plan := func(k, extra string) string {
		return decode[planBody](t, p.plan(p.robot, k, applyBody(p.target.rel, p.machine, extra)), http.StatusCreated).ID
	}
	wantProblem(t, p.approve(approver, "k-approve-unknown-0123", pln), http.StatusNotFound, "not-found")
	wantProblem(t, p.approve(approver, "k-approve-malformed-01", "pln_x"), http.StatusNotFound, "not-found")

	once := plan("k-plan-once-0123456789", "")
	first := decode[approvalBody](t, p.approve(approver, "k-approve-first-012345", once), http.StatusCreated)
	wantProblem(t, p.approve(p.human("h-all"), "k-approve-second-01234", once), http.StatusConflict, "conflict")
	if n := count(t, p.db, `SELECT count(*) FROM plan_state WHERE plan = $1 AND approval = $2 AND revision = 2`, once, first.ID); n != 1 {
		t.Fatal("the second approval changed the plan's state")
	}

	cancelled := plan("k-plan-cancelled-012345", "")
	mustExec(t, p.db, `UPDATE plan_state SET state = 'cancelled', reason = 'cancelled', revision = 2 WHERE plan = $1`, cancelled)
	wantProblem(t, p.approve(approver, "k-approve-cancelled-01", cancelled), http.StatusConflict, "conflict")
	p.wantUnapproved(t, cancelled, "cancelled")

	expired := decode[planBody](t, p.plan(p.robot, "k-plan-expired-0123456", applyBody(p.target.rel, p.machine, `,"expiresInSeconds":1`)),
		http.StatusCreated)
	time.Sleep(time.Until(expired.ExpiresAt.Add(100 * time.Millisecond)))
	if doc := wantProblem(t, p.approve(approver, "k-approve-expired-0123", expired.ID), http.StatusConflict, "conflict"); doc["state"] != "expired" {
		t.Fatalf("expired plan refused with %v", doc)
	}
	p.wantUnapproved(t, expired.ID, "proposed")

	scoped := plan("k-plan-scoped-01234567", "")
	mustExec(t, p.db, `UPDATE machine SET scope_state = 'pre-restore-unaccounted' WHERE id = $1`, p.machine)
	wantProblem(t, p.approve(approver, "k-approve-scoped-01234", scoped), http.StatusConflict, "recovery-mode-active")
	mustExec(t, p.db, `UPDATE machine SET scope_state = 'normal' WHERE id = $1`, p.machine)
	p.wantUnapproved(t, scoped, "proposed")
}

// An approval of an earlier epoch is void (execution-recovery.md §2): the plan is approved again
// in the current epoch, at its next revision, unless its earlier approver is revoked, when it reads
// revoked and is refused.
func TestApprovalAfterEpochChange(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	plan := func(k string) string {
		return decode[planBody](t, p.plan(p.robot, k, applyBody(p.target.rel, p.machine, "")), http.StatusCreated).ID
	}
	again, revoked := plan("k-plan-again-012345678"), plan("k-plan-revoked-01234567")
	approver := p.human("h-approver")
	before := decode[approvalBody](t, p.approve(approver, "k-approve-before-01234", again), http.StatusCreated)
	gone := p.principalOf("h-approver-gone")
	p.approveAs(t, revoked, gone)
	ep := newEpoch(t, p.db)
	revocationOf(t, revoke(p.env, p.human("h-recovery"), key, `{"identity":"`+gone+`","reason":"left"}`))

	after := decode[approvalBody](t, p.approve(approver, "k-approve-after-012345", again), http.StatusCreated)
	if after.ID == before.ID || after.Epoch != ep {
		t.Fatalf("re-approval %+v after %+v", after, before)
	}
	read := decode[planBody](t, p.do(p.api, call{method: "GET", path: prefix + "/plans/" + again, token: p.human("h-viewer")}),
		http.StatusOK)
	if read.State != "approved" || read.Approval == nil || *read.Approval != after.ID || read.Revision != 3 {
		t.Fatalf("plan after re-approval: state %q, approval %v, revision %d", read.State, read.Approval, read.Revision)
	}
	if doc := wantProblem(t, p.approve(approver, "k-approve-revoked-0123", revoked), http.StatusConflict, "conflict"); doc["state"] != "revoked" {
		t.Fatalf("plan with a revoked approver refused with %v", doc)
	}
}

// Rule 2: an approver revoked while the approval waits on the machine's lock is refused once the
// approval holds its principal, and nothing is written.
func TestApprovalApproverRevokedInWait(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	plan := decode[planBody](t, p.plan(p.robot, "k-plan-0123456789ab", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)
	approver, recovery := p.human("h-approver"), p.human("h-recovery")
	id := p.principalOf("h-approver")
	lock, err := p.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback() }()
	if _, err := lock.Exec(`SELECT 1 FROM machine WHERE id = $1 FOR UPDATE`, p.machine); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- p.approve(approver, "k-approve-0123456789", plan.ID) }()
	dbtest.WaitForLockWait(t, p.db)
	revocationOf(t, revoke(p.env, recovery, key, `{"identity":"`+id+`","reason":"left"}`))
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, <-done, http.StatusForbidden, "identity-revoked")
	p.wantUnapproved(t, plan.ID, "proposed")
}

// Rule 4: an approval's time and the expiry it is judged against follow the act-order wait. One
// queued there past its plan's expiry is refused and writes nothing; one released in time is
// recorded at a time after the release.
func TestApprovalAfterActOrderWait(t *testing.T) {
	p := newPlanEnv(t)
	t.Parallel()
	approver := p.human("h-approver")
	late := decode[planBody](t, p.plan(p.robot, "k-plan-late-0123456789", applyBody(p.target.rel, p.machine, `,"expiresInSeconds":1`)),
		http.StatusCreated)
	timely := decode[planBody](t, p.plan(p.robot, "k-plan-timely-01234567", applyBody(p.target.rel, p.machine, "")), http.StatusCreated)

	lock := holdActOrder(t, p.db)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- p.approve(approver, "k-approve-late-012345", late.ID) }()
	dbtest.WaitForLockWait(t, p.db)
	time.Sleep(time.Until(late.ExpiresAt.Add(200 * time.Millisecond)))
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	if doc := wantProblem(t, <-done, http.StatusConflict, "conflict"); doc["state"] != "expired" {
		t.Fatalf("approval queued past expiry refused with %v", doc)
	}
	p.wantUnapproved(t, late.ID, "proposed")
	if n := count(t, p.db, `SELECT count(*) FROM act WHERE action = 'plan.approve'`); n != 0 {
		t.Fatalf("%d approval acts after a refusal", n)
	}

	lock = holdActOrder(t, p.db)
	go func() { done <- p.approve(approver, "k-approve-timely-0123", timely.ID) }()
	dbtest.WaitForLockWait(t, p.db)
	time.Sleep(300 * time.Millisecond)
	released := time.Now()
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	b := decode[approvalBody](t, <-done, http.StatusCreated)
	var at, event, updated time.Time
	if err := p.db.QueryRow(`SELECT a.at, e.at, s.updated_at FROM approval a
		JOIN machine_event e ON e.machine = a.machine AND e.revision = a.revision JOIN plan_state s ON s.plan = a.plan
		WHERE a.id = $1`, b.ID).Scan(&at, &event, &updated); err != nil {
		t.Fatal(err)
	}
	if !at.After(released) || !event.Equal(at) || !updated.Equal(at) {
		t.Fatalf("approval at %s, entry %s, state %s, lock released %s", at, event, updated, released)
	}
}
