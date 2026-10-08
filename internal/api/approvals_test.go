package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

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
