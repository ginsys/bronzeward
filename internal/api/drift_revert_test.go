package api

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/talos"
)

// newRevertEnv is an attemptEnv whose plan is a revert: an apply-config plan of the machine's
// Desired release made while a drift record, observed at digest 09…, is open on it. It answers the
// record's opening revision; the plan is neither approved nor committed.
func newRevertEnv(t *testing.T) (*attemptEnv, int64) {
	t.Helper()
	ae := &attemptEnv{commitEnv: &commitEnv{planEnv: newPlanEnv(t)}}
	ae.a = observer(t, ae.env, &fakeExecutor{}, options{})
	drift := openDriftOn(t, ae.db, ae.machine)
	b := decode[planBody](t, ae.plan(ae.robot, "k-revert-0123456789a", applyBody(ae.target.rel, ae.machine, "")), http.StatusCreated)
	ae.pid, ae.expires = b.ID, b.ExpiresAt
	return ae, drift
}

// Execution and recovery §2, §6.4, choice §10.16: an apply-config plan made on a drifted machine is
// a revert, binding the open drift record, the machine's baseline revision and, as its pre-dispatch
// digest, the drifted digest the record names; its approval is marked as approving an unseen
// overwrite, in the approval resource and its entry. An ordinary plan's approval is not.
func TestRevertPlan(t *testing.T) {
	t.Parallel()
	ae, drift := newRevertEnv(t)
	b := decode[planBody](t, ae.do(ae.api, call{method: "GET", path: prefix + "/plans/" + ae.pid, token: ae.human("h-viewer")}),
		http.StatusOK)
	if b.Operation != "apply-config" || b.DriftRecord == nil || *b.DriftRecord != drift || b.BaselineRevision == nil ||
		*b.BaselineRevision != 4 {
		t.Fatalf("revert %+v", b)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM plan p JOIN drift_record d ON d.machine = p.machine AND d.opened = p.drift_record
		WHERE p.id = $1 AND p.expected_digest = d.observed_digest AND p.expected_digest = $2`,
		ae.pid, bytes.Repeat([]byte{9}, 32)); n != 1 {
		t.Fatal("the revert does not expect the drifted digest")
	}
	ap := decode[approvalBody](t, ae.approve(ae.human("h-approver"), ae.key(), ae.pid), http.StatusCreated)
	if !ap.UnseenOverwrite {
		t.Fatalf("approval %+v", ap)
	}
	got := decode[approvalBody](t, ae.do(ae.api, call{method: "GET", path: prefix + "/approvals/" + ap.ID, token: ae.human("h-viewer")}),
		http.StatusOK)
	if !got.UnseenOverwrite {
		t.Fatalf("read %+v", got)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM machine_event WHERE machine = $1 AND kind = 'approval'
		AND entry->>'approval' = $2 AND entry->'unseenOverwrite' = 'true'`, ae.machine, ap.ID); n != 1 {
		t.Fatal("the approval entry does not record the unseen overwrite")
	}

	ce := newCommitEnv(t, "")
	ce.approveIt(t)
	if got := decode[approvalBody](t, ce.do(ce.api, call{method: "GET", path: prefix + "/approvals/" + ce.approval,
		token: ce.human("h-viewer")}), http.StatusOK); got.UnseenOverwrite {
		t.Fatalf("ordinary approval %+v", got)
	}
}

// §3.2 comparison 6 admits the record a revert binds, so it commits and sends; completion advances
// Applied and the baseline revision and closes the record reverted, in a drift entry after the
// Applied change, resting on the completion observation (§6.4, §6.5).
func TestRevert(t *testing.T) {
	t.Parallel()
	ae, drift := newRevertEnv(t)
	ae.commit(t)
	ae.reach(t, "verifying")
	obs := ae.observed(t, nil)
	r, err := ae.a.settleOperation(context.Background(), ae.op, 1, obs)
	if err != nil || r.State != "completed" {
		t.Fatalf("settled %+v: %v", r, err)
	}
	if n := count(t, ae.db, `SELECT count(*) FROM machine_state ms JOIN plan p ON p.machine = ms.machine
			JOIN release_machine rm ON rm.release = p.release AND rm.machine = p.machine
		WHERE p.id = $1 AND ms.applied_release = p.release AND ms.applied_digest = rm.configuration_digest
			AND ms.baseline_revision = p.baseline_revision + 1`, ae.pid); n != 1 {
		t.Fatal("Applied is not the revert's release and digest, the baseline advanced")
	}
	if n := count(t, ae.db, `SELECT count(*) FROM drift_record d JOIN machine m ON m.id = d.machine AND m.revision_counter = d.closed
			JOIN machine_event e ON e.machine = d.machine AND e.revision = d.closed
			JOIN machine_event c ON c.machine = d.machine AND c.revision = d.closed - 1
		WHERE d.machine = $1 AND d.opened = $2 AND d.resolution = 'reverted' AND e.kind = 'drift'
			AND e.entry->>'change' = 'closed' AND e.entry->>'resolution' = 'reverted' AND (e.entry->>'record')::bigint = $2
			AND e.entry->>'observation' = $3 AND c.kind = 'applied-change'`, ae.machine, drift, obs); n != 1 {
		t.Fatal("the record is not closed reverted by the entry after the Applied change")
	}
}

// A revert's retry and its failure (§5.1, §6.5): a lost response accounted by decision, then a
// recovery observation at the drifted digest, classifies it safe to retry, as comparison 6 admits
// its bound record; a completion observation contradicting it fails it and leaves the record open,
// with that observation recorded on it.
func TestRevertRetryAndFailure(t *testing.T) {
	t.Parallel()
	t.Run("safe to retry", func(t *testing.T) {
		t.Parallel()
		ae, _ := newRevertEnv(t)
		ae.commit(t)
		att := ae.sent(t)
		if _, err := ae.a.recordResponse(context.Background(), att.Attempt, answer(talos.ClassTransport)); err != nil {
			t.Fatal(err)
		}
		next := ae.takenOverBy(t)
		start := ae.observed(t, func(c *cobs) { c.purpose, c.shift = "recovery", settleWindow })
		end := ae.observed(t, func(c *cobs) { c.purpose, c.shift = "recovery", settleWindow+time.Minute })
		if _, err := ae.decide(t, next, att.Attempt, start, end, nil); err != nil {
			t.Fatal(err)
		}
		obs := ae.observed(t, func(c *cobs) { c.digest = bytes.Repeat([]byte{9}, 32) })
		if r, err := next.settleOperation(context.Background(), ae.op, 2, obs); err != nil || r.Outcome != "safe-to-retry" {
			t.Fatalf("settled %+v: %v", r, err)
		}
	})
	t.Run("failed", func(t *testing.T) {
		t.Parallel()
		ae, drift := newRevertEnv(t)
		ae.commit(t)
		ae.reach(t, "verifying")
		obs := ae.observed(t, func(c *cobs) { c.digest = bytes.Repeat([]byte{0xee}, 32) })
		if r, err := ae.a.settleOperation(context.Background(), ae.op, 1, obs); err != nil || r.State != "failed" ||
			r.Drift != driftObserved {
			t.Fatalf("settled %+v: %v", r, err)
		}
		if n := count(t, ae.db, `SELECT count(*) FROM drift_record WHERE machine = $1 AND opened = $2 AND closed IS NULL
			AND last_observation = $3`, ae.machine, drift, obs); n != 1 {
			t.Fatal("the failed revert's observation is not recorded on the still open record")
		}
	})
}

// A revert refused at commitment (§3.2, §6.4, §6.5): re-drift before commitment fails comparison 3
// and is recorded on the open record; a manual return fails it too and closes the record returned;
// a bound record already closed, or another record opened after it, fails comparison 6.
func TestRevertRefusals(t *testing.T) {
	t.Parallel()
	applied := func(t *testing.T, ae *attemptEnv) []byte {
		var d []byte
		if err := ae.db.QueryRow(`SELECT applied_digest FROM machine_state WHERE machine = $1`, ae.machine).Scan(&d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	for _, c := range []struct {
		name, comparison, cause string
		digest                  func(*testing.T, *attemptEnv) []byte
		setup                   func(*testing.T, *attemptEnv, int64)
		want                    string // the record's state after the refusal
	}{
		{name: "re-drift", comparison: "3", cause: "the evidence shows a configuration digest other than the plan's",
			digest: func(*testing.T, *attemptEnv) []byte { return bytes.Repeat([]byte{10}, 32) },
			want:   `closed IS NULL AND observed_digest = decode(repeat('0a', 32), 'hex')`},
		{name: "manual return", comparison: "3", cause: "the evidence shows a configuration digest other than the plan's",
			digest: applied, want: `resolution = 'returned'`},
		{name: "record closed", comparison: "6", cause: "the drift record the plan binds is closed",
			setup: func(t *testing.T, ae *attemptEnv, drift int64) { closeDriftOn(t, ae.db, ae.machine, drift, "returned") },
			want:  `resolution = 'returned'`},
		{name: "another record", comparison: "6", cause: "a drift record is open on the machine",
			setup: func(t *testing.T, ae *attemptEnv, drift int64) {
				closeDriftOn(t, ae.db, ae.machine, drift, "returned")
				openDriftOn(t, ae.db, ae.machine)
			},
			want: `resolution = 'returned'`},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ae, drift := newRevertEnv(t)
			ae.approveIt(t)
			if c.setup != nil {
				c.setup(t, ae, drift)
			}
			var digest []byte
			if c.digest != nil {
				digest = c.digest(t, ae)
			}
			obs := ae.evidence(t, ae.pid, func(s *evSeed) {
				if digest != nil {
					s.digest = digest
				}
			})
			_, err := ae.a.commitPlan(context.Background(), ae.pid)
			if c.comparison == "6" {
				obs = ""
			}
			ae.wantRefused(t, err, c.comparison, c.cause, obs)
			if n := count(t, ae.db, `SELECT count(*) FROM drift_record WHERE machine = $1 AND opened = $2 AND `+c.want,
				ae.machine, drift); n != 1 {
				t.Fatalf("the bound record is not %s", c.want)
			}
		})
	}
}
