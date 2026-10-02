package api

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/staging"
)

func getIngestionCall(tok, claim string) call {
	return call{method: "GET", path: prefix + "/ingestions/" + claim, token: tok}
}

func abandonCall(tok, k, claim string) call {
	return call{method: "POST", path: prefix + "/ingestions/" + claim + "/abandonments", token: tok, key: k, body: `{}`}
}

// ingestionMembers are the members of the ingestion resource: never the owner string, the
// payload or its digest.
var ingestionMembers = []string{"createdAt", "draft", "expiresAt", "id", "kind", "leaseUntil", "machine", "mode", "operation",
	"ownerGeneration", "state"}

func (ie *ingestEnv) wantIngestion(t *testing.T, b map[string]any, j job, op, state string) {
	t.Helper()
	if got := slices.Sorted(maps.Keys(b)); !slices.Equal(got, ingestionMembers) {
		t.Fatalf("members %v", got)
	}
	want := map[string]any{"id": j.claim.ID, "kind": "import", "mode": j.claim.Mode, "state": state, "machine": ie.machine,
		"draft": ie.draft, "operation": op, "ownerGeneration": float64(1)}
	for k, v := range want {
		if b[k] != v {
			t.Errorf("%s = %v, want %v", k, b[k], v)
		}
	}
	for _, k := range []string{"createdAt", "expiresAt", "leaseUntil"} {
		if _, err := time.Parse(time.RFC3339Nano, b[k].(string)); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
}

// PA §9.2, C §3.5: the ingestion resource reports the claim's state as every read treats it: a
// transient claim past its lease reads abandoned before any sweep writes it. It never carries the
// owner string.
func TestGetIngestionEffectiveState(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j := ie.startJob(t, nil)
	viewer := ie.human("h-viewer")
	rec := ie.do(ie.api, getIngestionCall(viewer, j.claim.ID))
	ie.wantIngestion(t, decode[map[string]any](t, rec, http.StatusOK), j, op, "held")
	if strings.Contains(rec.Body.String(), ie.d.owner.ID) {
		t.Fatal("the body names the owner")
	}
	mustExec(t, ie.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, j.claim.ID)
	rec = ie.do(ie.api, getIngestionCall(viewer, j.claim.ID))
	ie.wantIngestion(t, decode[map[string]any](t, rec, http.StatusOK), j, op, "abandoned")
	if st, _, _ := claimRow(t, ie.db, j.claim.ID); st != "held" {
		t.Fatalf("the read wrote the claim %s", st)
	}
	wantProblem(t, ie.do(ie.api, getIngestionCall(viewer, id.New(id.Ingestion))), http.StatusNotFound, "not-found")
	wantProblem(t, ie.do(ie.api, getIngestionCall(viewer, "ing_nope")), http.StatusNotFound, "not-found")
}

// PA §9.2, T11, §8.2: an operator abandons a live claim, or one a read already treats as
// abandoned: the claim is abandoned with no payload and its running operation fails
// ingestion-abandoned with its terminal event, 200 with the resource. An ended claim refuses 409.
// The route is for human authors only.
func TestAbandonmentRoute(t *testing.T) {
	ie := newIngestEnv(t, options{})
	author := ie.human("h-author")
	op, j := ie.startJob(t, map[string]any{"staging": "encrypted"})
	mustExec(t, ie.db, `UPDATE staging_claim SET payload = 'vault:v1:sealed', payload_digest = sha256('envelope') WHERE id = $1`, j.claim.ID)
	wantProblem(t, ie.do(ie.api, abandonCall(ie.robot, "k-abandon-robot-0123", j.claim.ID)), http.StatusForbidden, "forbidden")
	wantProblem(t, ie.do(ie.api, abandonCall(ie.human("h-viewer"), "k-abandon-view-0123", j.claim.ID)), http.StatusForbidden, "forbidden")
	rec := ie.do(ie.api, abandonCall(author, "k-abandon-0123456789", j.claim.ID))
	ie.wantIngestion(t, decode[map[string]any](t, rec, http.StatusOK), j, op, "abandoned")
	ie.wantFailed(t, op, j.claim.ID, http.StatusConflict, "ingestion-abandoned")
	wantProblem(t, ie.do(ie.api, abandonCall(author, "k-abandon-again-0123", j.claim.ID)), http.StatusConflict, "conflict")

	// A transient claim past its lease has ended as read (compilation §3.5): the abandonment
	// refuses it and leaves the row for the sweep.
	op2, j2 := ie.startJob(t, nil)
	mustExec(t, ie.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, j2.claim.ID)
	wantProblem(t, ie.do(ie.api, abandonCall(author, "k-abandon-lapsed-012", j2.claim.ID)), http.StatusConflict, "conflict")
	if st, _, _ := claimRow(t, ie.db, j2.claim.ID); st != "held" || readOp(t, ie.db, op2).state != "running" {
		t.Fatalf("lapsed transient claim %s, operation %s", st, readOp(t, ie.db, op2).state)
	}
	if n, err := staging.Sweep(t.Context(), ie.db); err != nil || n != 1 {
		t.Fatalf("sweep %d, %v", n, err)
	}
	// An encrypted claim past its lease but not its expiry is live, for a takeover.
	op3, j3 := ie.startJob(t, map[string]any{"staging": "encrypted"})
	mustExec(t, ie.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, j3.claim.ID)
	rec = ie.do(ie.api, abandonCall(author, "k-abandon-lapsed-enc", j3.claim.ID))
	ie.wantIngestion(t, decode[map[string]any](t, rec, http.StatusOK), j3, op3, "abandoned")
	ie.wantFailed(t, op3, j3.claim.ID, http.StatusConflict, "ingestion-abandoned")
	if n := count(t, ie.db, `SELECT count(*) FROM act WHERE action = 'ingestion.abandon'`); n != 2 {
		t.Fatalf("%d abandonment acts", n)
	}
	wantProblem(t, ie.do(ie.api, abandonCall(author, "k-abandon-none-0123", id.New(id.Ingestion))), http.StatusNotFound, "not-found")
}

// An abandonment while the runner works stops it at T1: the release is fenced on a live claim,
// so the draft does not move and the operation keeps the abandonment's outcome.
func TestAbandonmentStopsRunner(t *testing.T) {
	ie := newIngestEnv(t, options{})
	before := ie.currentETag(t)
	op, j := ie.startJob(t, nil)
	ie.runWith(t, options{afterStage: func() {
		if rec := ie.do(ie.api, abandonCall(ie.human("h-author"), "k-abandon-mid-01234", j.claim.ID)); rec.Code != http.StatusOK {
			t.Errorf("abandonment: %d %s", rec.Code, rec.Body)
		}
	}}, j)
	ie.draftUnchanged(t, before)
	ie.wantFailed(t, op, j.claim.ID, http.StatusConflict, "ingestion-abandoned")
}

// PA §5.1 and its epoch-term control: a process started before a recovery-mode entry starts no
// ingestion after it; the control that drops `$my_epoch = $current_epoch` lets it start one.
func TestEpochTermRefusesPreEntryProcess(t *testing.T) {
	for _, control := range []bool{false, true} {
		ie := newIngestEnv(t, options{noEpochTerm: control})
		newEpoch(t, ie.db)
		rec := ie.do(ie.api, ingestCall(ie.human("h-author"), key, ie.etag, ie.body(nil)))
		if control {
			if rec.Code != http.StatusAccepted {
				t.Fatalf("control: %d %s", rec.Code, rec.Body)
			}
			continue
		}
		wantProblem(t, rec, http.StatusServiceUnavailable, "epoch-superseded")
	}
}

// A recovery-mode entry while a job runs: the next heartbeat is refused and stops the run, and
// nothing of the draft is written.
func TestEpochTermStopsRunningJob(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j := ie.startJob(t, map[string]any{"document": twoSecrets, "marks": []string{labelMark}})
	ie.f.onCreate = func(ctx context.Context, n int) error {
		if n == 1 {
			newEpoch(t, ie.db)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
			return nil // the runner was not stopped: let it go on, so the test sees it
		}
	}
	before := ie.currentETag(t)
	ie.d.timers.Heartbeat = 20 * time.Millisecond
	ie.runWith(t, options{}, j)
	ie.draftUnchanged(t, before)
	if r := readOp(t, ie.db, op); r.state != "running" || r.last != 1 {
		t.Fatalf("operation %+v", r)
	}
	if st, _, _ := claimRow(t, ie.db, j.claim.ID); st != "held" {
		t.Fatalf("claim %s", st)
	}
	if !ie.logged("heartbeat refused") || !ie.logged("stopped before the draft transaction") || ie.logged(" fails ") {
		t.Fatal("the run did not stop on the refused heartbeat")
	}
}
