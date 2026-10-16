package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// enterStopped enters recovery mode on ae's database through a recovery start, every pre-restoration
// instance stopped at stop, and answers that start.
func (ae *attemptEnv) enterStopped(t *testing.T, stop time.Time) *API {
	t.Helper()
	var entered atomic.Int32
	r := recoveryStart(t, ae.env, &entered)
	body := strings.Replace(entryBodyText, "2026-09-25T03:00:00Z", stop.UTC().Format(time.RFC3339Nano), 1)
	decode[entryBody](t, ae.enter(r, "stopped", body), http.StatusCreated)
	return r
}

// account is h-recovery's post-restore accounting decision on a under key.
func (ae *attemptEnv) account(a *API, key, body string) *httptest.ResponseRecorder {
	return ae.do(a, call{method: "POST", path: prefix + "/recovery/accountings", token: ae.human("h-recovery"),
		key: "k-account-" + key + "-0123456789", body: body})
}

// recoveryRead records a recovery observation of machine m, its start and record shift from now,
// reading the machine-configuration resource version rv.
func (ae *attemptEnv) recoveryRead(t *testing.T, m string, shift time.Duration, rv string) string {
	t.Helper()
	own := ae.machine
	ae.machine = m
	defer func() { ae.machine = own }()
	return ae.observed(t, func(c *cobs) { c.purpose, c.shift, c.resource, c.scope = "recovery", shift, rv, true })
}

// The post-restore accounting decision (execution-recovery.md §7.3 step 1, persistence-api.md §9.2):
// one request accounts each scope it names by §5.2, the settle floor counted from the latest of the
// stop of the pre-restoration instances plus the maximum transport deadline (here the latest), the
// restored attempts' transport deadlines and the first successful read after them, which is the
// settle's start. Each scope leaves pre-restore unaccounted: to unresolved where a restored
// operation holds it, to blocked otherwise, with its accounting record and its scope state entry
// last; every restored unaccounted attempt on it is accounted under the decision's act. A request
// one scope of which fails §5.2 accounts none, and a scope accounted once is not accounted again.
func TestRecoveryAccounting(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	att := ae.sent(t)
	ae.second(t)
	r := ae.enterStopped(t, time.Now().Add(-time.Minute))
	early := ae.recoveryRead(t, ae.machine, 3*time.Minute, "rv1")
	start := ae.recoveryRead(t, ae.machine, 5*time.Minute, "rv1")
	end := ae.recoveryRead(t, ae.machine, 6*time.Minute, "rv1")
	start2 := ae.recoveryRead(t, ae.machine2, 5*time.Minute, "rv1")
	soon := ae.recoveryRead(t, ae.machine2, 5*time.Minute+10*time.Second, "rv2")
	end2 := ae.recoveryRead(t, ae.machine2, 6*time.Minute, "rv2")
	body := func(s1, e2, facts string) string {
		return fmt.Sprintf(`{"scopes":[{"machine":%q,"start":%q,"end":%q},{"machine":%q,"start":%q,"end":%q}],"facts":%s}`,
			ae.machine, s1, end, ae.machine2, start2, e2, facts)
	}
	const explained = `{"explanation":"the operator re-applied the restored configuration"}`
	for key, b := range map[string]string{
		"early":       body(early, end2, explained), // the start precedes the first read after the deadline
		"floor":       body(start, soon, explained), // the end began within the settle floor
		"unexplained": body(start, end2, `{}`),      // the second scope's version moved
	} {
		wantProblem(t, ae.account(r, key, b), http.StatusConflict, "conflict")
	}
	if n := count(t, ae.db, `SELECT count(*) FROM scope_accounting`) + count(t, ae.db, `SELECT count(*) FROM attempt_accounting`) +
		count(t, ae.db, `SELECT count(*) FROM machine WHERE scope_state <> 'pre-restore-unaccounted'`); n != 0 {
		t.Fatalf("a refused decision accounted something: %d", n)
	}
	rec := ae.account(r, "ok", body(start, end2, explained))
	b := decode[scopeAccountingBody](t, rec, http.StatusCreated)
	human := ae.principalOf("h-recovery")
	if len(b.Scopes) != 2 || b.DecidedBy != human || b.Role != "recovery-admin" || b.Epoch != epoch(t, ae.db) {
		t.Fatalf("answered %+v", b)
	}
	want := map[string]string{ae.machine: "unresolved", ae.machine2: "blocked"}
	for _, s := range b.Scopes {
		if s.State != want[s.Machine] {
			t.Fatalf("scope %s answered %s; want %s", s.Machine, s.State, want[s.Machine])
		}
		if n := count(t, ae.db, `SELECT count(*) FROM machine m JOIN scope_accounting c ON c.machine = m.id
				JOIN machine_event e ON e.machine = m.id AND e.revision = c.revision AND e.revision = m.revision_counter
			WHERE m.id = $1 AND m.scope_state = $2 AND c.epoch = $3 AND c.decided_by = $4 AND c.act = $5 AND c.revision = $6
				AND c.facts->>'explanation' IS NOT NULL AND c.facts->>'judgement' = 'true' AND e.kind = 'scope-state'
				AND e.entry->>'from' = 'pre-restore-unaccounted' AND e.entry->>'to' = $2
				AND e.entry->>'cause' = 'post-restore-accounting' AND e.entry->>'act' = $5`,
			s.Machine, want[s.Machine], b.Epoch, human, b.Act, s.Revision); n != 1 {
			t.Fatalf("scope %s: no accounting record with its scope state entry last", s.Machine)
		}
	}
	if n := count(t, ae.db, `SELECT count(*) FROM attempt_accounting c JOIN machine_event e ON e.machine = c.machine
			AND e.revision = c.revision AND e.kind = 'accounting'
		WHERE c.attempt = $1 AND c.basis = 'decision' AND c.decided_by = $2 AND c.act = $3 AND c.start_observation = $4
			AND c.end_observation = $5 AND e.entry->>'scope' = 'true'`, att.Attempt, human, b.Act, start, end); n != 1 {
		t.Fatal("the restored attempt is not accounted under the decision")
	}
	if n := count(t, ae.db, `SELECT count(*) FROM act WHERE id = $1 AND action = 'recovery.account' AND role = 'recovery-admin'
			AND subjects = $2`, b.Act, slices.Sorted(slices.Values([]string{ae.machine, ae.machine2}))); n != 1 {
		t.Fatal("no act of the decision")
	}
	// Accounted once: a scope no longer pre-restore unaccounted is refused.
	again := fmt.Sprintf(`{"scopes":[{"machine":%q,"start":%q,"end":%q}]}`, ae.machine2, start2, end2)
	wantProblem(t, ae.account(r, "again", again), http.StatusConflict, "conflict")
	foreign := fmt.Sprintf(`{"scopes":[{"machine":%q,"start":%q,"end":%q}]}`, ae.machine, start2, end)
	wantProblem(t, ae.account(r, "foreign", foreign), http.StatusNotFound, "not-found")
	for key, bad := range map[string]string{
		"none": `{"scopes":[]}`,
		"twice": fmt.Sprintf(`{"scopes":[{"machine":%q,"start":%q,"end":%q},{"machine":%q,"start":%q,"end":%q}]}`,
			ae.machine2, start2, end2, ae.machine2, start2, end2),
		"ids": `{"scopes":[{"machine":"x","start":"y","end":"z"}]}`,
	} {
		wantProblem(t, ae.account(r, key, bad), http.StatusBadRequest, "invalid-request")
	}
}

// The recovery start's reads (execution-recovery.md §7.3 step 1, ruling S3): a pass takes a recovery
// observation, naming no plan or operation, of each pre-restore unaccounted scope whose deadline has
// passed, none of a scope before its restored attempt's transport deadline, and none again within the
// settle floor; the loop ends when no scope is pre-restore unaccounted, or under a superseded epoch.
func TestRecoveryReads(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	ae.sent(t)
	ae.enterStopped(t, time.Now().Add(-time.Hour))
	old := ae.a
	r := observer(t, ae.env, &fakeExecutor{err: errors.New("no access in this test")}, options{})
	reads := func(m string) int {
		return count(t, ae.db, `SELECT count(*) FROM observation_start s JOIN observation o ON o.machine = s.machine AND o.basis = s.revision
			WHERE s.machine = $1 AND s.purpose = 'recovery' AND s.plan IS NULL AND s.operation IS NULL`, m)
	}
	for range 2 {
		if !r.recoveryReadPass(t.Context()) {
			t.Fatal("the loop ended with scopes pre-restore unaccounted")
		}
		if reads(ae.machine) != 0 || reads(ae.target.machine2) != 1 {
			t.Fatalf("read the attempt's scope %d times, the other %d; want 0 and 1", reads(ae.machine), reads(ae.target.machine2))
		}
	}
	if old.recoveryReadPass(t.Context()) {
		t.Fatal("a superseded epoch's loop did not end")
	}
	mustExec(t, ae.db, `UPDATE machine SET scope_state = 'blocked' WHERE scope_state = 'pre-restore-unaccounted'`)
	if r.recoveryReadPass(t.Context()) {
		t.Fatal("the loop did not end with no scope pre-restore unaccounted")
	}
}

// A restored attempt's transport deadline later than the stop plus the maximum transport deadline
// is the deadline the settle's start follows: the first read after it is the start, and a read
// before it is not, though it follows the stop's deadline.
func TestRecoveryAccountingAttemptDeadline(t *testing.T) {
	t.Parallel()
	ae := newAttemptEnv(t, "")
	att := ae.sent(t)
	var deadline time.Time
	if err := ae.db.QueryRow(`SELECT transport_deadline FROM attempt WHERE id = $1`, att.Attempt).Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	r := ae.enterStopped(t, time.Now().Add(-time.Hour))
	before := ae.recoveryRead(t, ae.machine, time.Until(deadline)-5*time.Second, "rv1")
	start := ae.recoveryRead(t, ae.machine, time.Until(deadline)+5*time.Second, "rv1")
	end := ae.recoveryRead(t, ae.machine, time.Until(deadline)+2*time.Minute, "rv1")
	scope := func(s string) string {
		return fmt.Sprintf(`{"scopes":[{"machine":%q,"start":%q,"end":%q}]}`, ae.machine, s, end)
	}
	doc := wantProblem(t, ae.account(r, "before", scope(before)), http.StatusConflict, "conflict")
	if !strings.Contains(fmt.Sprint(doc["detail"]), "first successful read") {
		t.Fatalf("refused for another cause: %v", doc)
	}
	b := decode[scopeAccountingBody](t, ae.account(r, "after", scope(start)), http.StatusCreated)
	if len(b.Scopes) != 1 || len(b.Scopes[0].Attempts) != 1 || b.Scopes[0].Attempts[0] != att.Attempt {
		t.Fatalf("answered %+v", b)
	}
}
