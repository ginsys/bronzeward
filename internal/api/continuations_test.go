package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/staging"
)

func continueCall(tok, k, claim string) call {
	return call{method: "POST", path: prefix + "/ingestions/" + claim + "/continuations", token: tok, key: k, body: `{}`}
}

// pausedForContinue starts a reviewed import and runs it to its pause.
func (ie *ingestEnv) pausedForContinue(t *testing.T) (string, job, pausedRow) {
	t.Helper()
	op, j := ie.reviewedJob(t)
	ie.runWith(t, options{}, j)
	r := ie.pausedRow(t, j.claim.ID)
	if r.state != "paused" || r.gen != 1 {
		t.Fatalf("first run: claim %+v", r)
	}
	return op, j, r
}

// continueReview posts a continuation on claim and returns the job handed to the runner.
func (ie *ingestEnv) continueReview(t *testing.T, k, claim, op string, gen int64) job {
	t.Helper()
	before := len(ie.runs())
	rec := ie.do(ie.api, continueCall(ie.human("h-author"), k, claim))
	b := decode[map[string]any](t, rec, http.StatusAccepted)
	if want := (map[string]any{"operation": op, "ingestion": claim}); fmt.Sprint(b) != fmt.Sprint(want) ||
		rec.Header().Get("Location") != prefix+"/operations/"+op {
		t.Fatalf("body %v, Location %q", b, rec.Header().Get("Location"))
	}
	js := ie.runs()
	if len(js) != before+1 {
		t.Fatalf("%d jobs handed over, want one more than %d", len(js), before)
	}
	j := js[len(js)-1]
	if j.claim.ID != claim || j.claim.Gen != gen || j.claim.Review != "continued" || j.op != op || j.resume == nil || j.marks != nil {
		t.Fatalf("job %+v", j)
	}
	return j
}

// wantContinuedSucceeded asserts op succeeded through the draft transaction with the claim
// released and the draft moved past etagBefore, its events ending in continued then succeeded.
func (ie *ingestEnv) wantContinuedSucceeded(t *testing.T, op, claim, etagBefore string, events_ ...string) {
	t.Helper()
	if r := readOp(t, ie.db, op); r.state != "succeeded" || r.error != nil || r.owner.Valid || r.lease.Valid {
		t.Fatalf("operation %+v", r)
	}
	if st, _, payload := claimRow(t, ie.db, claim); st != "released" || payload {
		t.Fatalf("claim %s, payload %v", st, payload)
	}
	if ie.currentETag(t) == etagBefore {
		t.Fatal("the draft did not move")
	}
	if got := eventTypes(events(t, ie.db, op)); !slices.Equal(got, events_) {
		t.Fatalf("events %v, want %v", got, events_)
	}
}

// Compilation §3.6 item 4, PA §9.3/§8.3: a continuation takes the paused claim at the next owner
// generation, records the review as continued and writes the continued event at the take, before
// any run; its run decrypts the envelope and runs the draft transaction, which releases the claim
// and succeeds the operation. Control: the continued event is present before the run starts.
func TestContinuationRunsDraftTransaction(t *testing.T) {
	ie := newIngestEnv(t, options{})
	before := ie.currentETag(t)
	op, j, _ := ie.pausedForContinue(t)
	cj := ie.continueReview(t, "k-continue-run-0123", j.claim.ID, op, 2)
	if r := ie.pausedRow(t, j.claim.ID); r.state != "held" || r.review != "continued" || r.gen != 2 || r.opGen != 2 || r.opState != "running" {
		t.Fatalf("after the continuation: claim %+v", r)
	}
	evs := events(t, ie.db, op)
	if !slices.Equal(eventTypes(evs), []string{"started", "staged", "paused", "continued"}) ||
		evs[3]["generation"] != float64(2) || len(evs[3]) != 2 {
		t.Fatalf("events at the take %v", evs)
	}
	ie.draftUnchanged(t, before)
	t1 := false
	ie.runWith(t, options{beforeT1: func() { t1 = true }}, cj)
	if !t1 {
		t.Fatal("the continuation's run did not reach the draft transaction")
	}
	ie.wantContinuedSucceeded(t, op, j.claim.ID, before, "started", "staged", "paused", "continued", "succeeded")
}

// PA §9.3: a continuation on a claim not paused, past its absolute expiry, of an earlier epoch or
// that does not exist is refused 409 (404 for none, 503 for a superseded process) and nothing
// changes. Control: the same claim, paused and unexpired, is taken.
func TestContinuationRefusals(t *testing.T) {
	ie := newIngestEnv(t, options{})
	author := ie.human("h-author")
	op, j, first := ie.pausedForContinue(t)
	unchanged := func(label string) {
		t.Helper()
		if r := ie.pausedRow(t, j.claim.ID); r.gen != first.gen || r.review != "pending" || string(r.digest) != string(first.digest) {
			t.Fatalf("%s: claim %+v", label, r)
		}
		if n := len(events(t, ie.db, op)); n != 3 {
			t.Fatalf("%s: %d events", label, n)
		}
	}
	wantProblem(t, ie.do(ie.api, continueCall(author, "k-continue-none-0123", "ing_aaaaaaaaaaaaaaaaaaaaaaaaaa")), http.StatusNotFound, "not-found")
	mustExec(t, ie.db, `UPDATE staging_claim SET state = 'held' WHERE id = $1`, j.claim.ID)
	if p := wantProblem(t, ie.do(ie.api, continueCall(author, "k-continue-held-0123", j.claim.ID)), http.StatusConflict, "conflict"); p["detail"] != "the ingestion is not paused for the operator's review" {
		t.Fatalf("held: %v", p)
	}
	var expires time.Time
	if err := ie.db.QueryRow(`SELECT expires_at FROM staging_claim WHERE id = $1`, j.claim.ID).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	mustExec(t, ie.db, `UPDATE staging_claim SET state = 'paused', expires_at = clock_timestamp() WHERE id = $1`, j.claim.ID)
	if p := wantProblem(t, ie.do(ie.api, continueCall(author, "k-continue-expired-0", j.claim.ID)), http.StatusConflict, "conflict"); p["detail"] != "the ingestion has ended" {
		t.Fatalf("expired: %v", p)
	}
	mustExec(t, ie.db, `UPDATE staging_claim SET expires_at = $2 WHERE id = $1`, j.claim.ID, expires)
	unchanged("refused continuations")
	if len(ie.runs()) != 1 {
		t.Fatalf("%d jobs: a refused continuation started a run", len(ie.runs()))
	}
	ie.continueReview(t, "k-continue-control-0", j.claim.ID, op, 2)
}

// PA §9.3, compilation §3.5: after a recovery-mode entry, a process of the earlier epoch issues no
// continuation (503 epoch-superseded), and a process of the current epoch is refused the claim of
// the earlier one (409); the claim is unchanged.
func TestContinuationEarlierEpoch(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j, first := ie.pausedForContinue(t)
	newEpoch(t, ie.db)
	author := ie.human("h-author")
	wantProblem(t, ie.do(ie.api, continueCall(author, "k-continue-supersede", j.claim.ID)), http.StatusServiceUnavailable, "epoch-superseded")
	d := ie.d
	d.owner = staging.Owner{ID: "c/3/" + rand.Text(), Epoch: epoch(t, ie.db)}
	if p := wantProblem(t, ie.do(ie.buildWith(d, options{}), continueCall(author, "k-continue-epoch-012", j.claim.ID)),
		http.StatusConflict, "conflict"); p["detail"] != "the claim predates the current epoch" {
		t.Fatalf("problem %v", p)
	}
	if r := ie.pausedRow(t, j.claim.ID); r.state != "paused" || r.gen != first.gen || r.review != "pending" || len(events(t, ie.db, op)) != 3 {
		t.Fatalf("claim %+v", r)
	}
}

// PA §9.3, §16: a continuation whose draft moved while the claim was paused fails the operation
// 412; one whose draft was discarded, or has a queued or running publication, fails it 409; each
// abandons the claim and leaves the draft as the other writer left it. Control: the discarded
// draft also advanced its revision, so a check comparing the revision before the state answers
// 412 and fails the case.
func TestContinuationDraftChecks(t *testing.T) {
	cases := []struct {
		name   string
		move   func(t *testing.T, ie *ingestEnv, op string)
		status int
		code   string
	}{
		{"moved", func(t *testing.T, ie *ingestEnv, _ string) {
			mustExec(t, ie.db, `UPDATE draft SET revision = revision + 1 WHERE id = $1`, ie.draft)
		}, http.StatusPreconditionFailed, "precondition-failed"},
		{"discarded", func(t *testing.T, ie *ingestEnv, _ string) {
			mustExec(t, ie.db, `UPDATE draft SET state = 'discarded', revision = revision + 1 WHERE id = $1`, ie.draft)
		}, http.StatusConflict, "conflict"},
		{"queued publication", func(t *testing.T, ie *ingestEnv, op string) {
			publication(t, ie, op, "queued", "NULL::text, 0, NULL::text, NULL::timestamptz")
		}, http.StatusConflict, "conflict"},
		{"running publication", func(t *testing.T, ie *ingestEnv, op string) {
			publication(t, ie, op, "running", "'b/1/x', 1, epoch, now() + interval '1 minute'")
		}, http.StatusConflict, "conflict"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ie := newIngestEnv(t, options{})
			op, j, _ := ie.pausedForContinue(t)
			c.move(t, ie, op)
			moved := ie.currentETag(t)
			cj := ie.continueReview(t, "k-continue-draft-012", j.claim.ID, op, 2)
			ie.runWith(t, options{}, cj)
			ie.draftUnchanged(t, moved)
			r := readOp(t, ie.db, op)
			if r.state != "failed" || r.error["type"] != "urn:bronzeward:problem:"+c.code || r.error["status"] != float64(c.status) {
				t.Fatalf("operation %+v", r)
			}
			if got := eventTypes(events(t, ie.db, op)); !slices.Equal(got, []string{"started", "staged", "paused", "continued", "failed"}) {
				t.Fatalf("events %v", got)
			}
			if st, _, payload := claimRow(t, ie.db, j.claim.ID); st != "abandoned" || payload {
				t.Fatalf("claim %s, payload %v", st, payload)
			}
		})
	}
}

// publication inserts a publish operation in state for op's draft at its bound revision, with
// owner the owner columns' SQL.
func publication(t *testing.T, ie *ingestEnv, op, state, owner string) {
	t.Helper()
	mustExec(t, ie.db, `INSERT INTO operation (id, kind, state, epoch, owner, owner_gen, owner_epoch, lease_until,
		draft, draft_revision, created_by, created_by_kind, created_role, created_at)
		SELECT $1, 'publish', $2, epoch, `+owner+`, draft, draft_revision, created_by, created_by_kind, 'publisher', now()
		FROM operation WHERE id = $3`, id.New(id.Operation), state, op)
}

// Compilation §3.6 item 6, PA §8.3: a continuation's run that cannot decrypt the envelope records
// resume-failed and leaves the claim held to its lease, its review continued and the operation
// running; a takeover once the lease lapses runs the draft transaction instead of pausing again.
func TestContinuationDecryptFailureThenTakeover(t *testing.T) {
	ie := newIngestEnv(t, options{})
	before := ie.currentETag(t)
	op, j, _ := ie.pausedForContinue(t)
	cj := ie.continueReview(t, "k-continue-decrypt-0", j.claim.ID, op, 2)
	ie.f.decryptErr = errors.New("fake: transit unavailable")
	ie.runWith(t, options{}, cj)
	if r := ie.pausedRow(t, j.claim.ID); r.state != "held" || r.review != "continued" || r.gen != 2 || r.opState != "running" {
		t.Fatalf("claim %+v", r)
	}
	if got := eventTypes(events(t, ie.db, op)); !slices.Equal(got, []string{"started", "staged", "paused", "continued", "resume-failed"}) {
		t.Fatalf("events %v", got)
	}
	ie.f.decryptErr = nil
	ie.lapse(t, j.claim.ID)
	tk := ie.newTaker(options{})
	tj := ie.takeOver(t, tk, "k-continue-take-0123", j.claim.ID, op, 3)
	if tj.claim.Review != "continued" {
		t.Fatalf("taken job review %q", tj.claim.Review)
	}
	tk.runIngest(t.Context(), tj)
	ie.wantContinuedSucceeded(t, op, j.claim.ID, before,
		"started", "staged", "paused", "continued", "resume-failed", "taken-over", "succeeded")
}

// PA §16, compilation §3.1: a continuation's run that finds the envelope not matching its digest,
// or a malformed envelope stored with its matching digest, abandons the claim and fails the
// operation 500 internal-error, writing nothing to the draft. Control: pausing again instead
// leaves the claim held or paused and fails each case.
func TestContinuationIntegrityFailureAbandons(t *testing.T) {
	for _, c := range envelopeCorruptions {
		t.Run(c.name, func(t *testing.T) {
			ie := newIngestEnv(t, options{})
			before := ie.currentETag(t)
			op, j, _ := ie.pausedForContinue(t)
			ct, _ := ie.stagedPayload(t, j.claim.ID)
			c.corrupt(t, ie, j.claim.ID, ct)
			cj := ie.continueReview(t, "k-continue-integrity", j.claim.ID, op, 2)
			ie.runWith(t, options{beforeT1: func() { t.Error("the run reached the draft transaction") }}, cj)
			ie.wantIntegrityFailure(t, op, j.claim.ID, "continued")
			ie.draftUnchanged(t, before)
		})
	}
}

// envelopeCorruptions are the stored envelopes a run that opens one must refuse as integrity
// failures (PA §16): one not matching its digest, and a malformed one stored with its digest.
var envelopeCorruptions = []struct {
	name    string
	corrupt func(t *testing.T, ie *ingestEnv, claim string, ct provider.Ciphertext)
}{
	{"digest mismatch", func(_ *testing.T, ie *ingestEnv, _ string, ct provider.Ciphertext) {
		ie.f.mu.Lock()
		defer ie.f.mu.Unlock()
		ie.f.staged[ct] = append(append([]byte(nil), ie.f.staged[ct]...), ' ')
	}},
	{"malformed envelope", func(t *testing.T, ie *ingestEnv, claim string, ct provider.Ciphertext) {
		malformed := []byte(`{"version":1,"documents":"machine:\n  hostname: x\n"}`)
		digest := sha256.Sum256(malformed)
		ie.f.mu.Lock()
		ie.f.staged[ct] = malformed
		ie.f.mu.Unlock()
		mustExec(t, ie.db, `UPDATE staging_claim SET payload_digest = $2 WHERE id = $1`, claim, digest[:])
	}},
}

// wantIntegrityFailure checks that the run taken by act (continued or marked) failed op 500
// internal-error and abandoned claim without its payload.
func (ie *ingestEnv) wantIntegrityFailure(t *testing.T, op, claim, act string) {
	t.Helper()
	r := readOp(t, ie.db, op)
	if r.state != "failed" || r.error["type"] != "urn:bronzeward:problem:internal-error" ||
		r.error["status"] != float64(http.StatusInternalServerError) {
		t.Fatalf("operation %+v", r)
	}
	if got := eventTypes(events(t, ie.db, op)); !slices.Equal(got, []string{"started", "staged", "paused", act, "failed"}) {
		t.Fatalf("events %v", got)
	}
	if st, _, payload := claimRow(t, ie.db, claim); st != "abandoned" || payload {
		t.Fatalf("claim %s, payload %v", st, payload)
	}
}

// PA §16, compilation §3.6 item 6: a continuation's run killed before its draft transaction leaves
// the claim held with its review continued and the operation running; a takeover once the lease
// lapses commits the draft transaction. Control: a continuation that recorded continued only as
// its run ended would leave the review pending, and the takeover would pause again.
func TestContinuationKilledBeforeT1TakeoverCommits(t *testing.T) {
	ie := newIngestEnv(t, options{})
	before := ie.currentETag(t)
	op, j, _ := ie.pausedForContinue(t)
	cj := ie.continueReview(t, "k-continue-killed-0", j.claim.ID, op, 2)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ie.build(options{beforeT1: cancel}).runIngest(ctx, cj)
	if r := ie.pausedRow(t, j.claim.ID); r.state != "held" || r.review != "continued" || r.gen != 2 || r.opState != "running" {
		t.Fatalf("after the kill: claim %+v", r)
	}
	ie.draftUnchanged(t, before)
	if got := eventTypes(events(t, ie.db, op)); !slices.Equal(got, []string{"started", "staged", "paused", "continued"}) {
		t.Fatalf("events after the kill %v", got)
	}
	ie.lapse(t, j.claim.ID)
	tk := ie.newTaker(options{})
	tj := ie.takeOver(t, tk, "k-continue-take-kill", j.claim.ID, op, 3)
	tk.runIngest(t.Context(), tj)
	ie.wantContinuedSucceeded(t, op, j.claim.ID, before, "started", "staged", "paused", "continued", "taken-over", "succeeded")
}

// A continuation's new lease runs from after the act-order lock, its last wait, and a continuation
// whose claim reaches its absolute expiry in that wait is refused as ended: nothing is taken and
// no run starts (PA §5 rules 4 and 5, compilation §3.6 item 4).
func TestContinuationActOrderWait(t *testing.T) {
	t.Run("lease after the wait", func(t *testing.T) {
		ie := newIngestEnv(t, options{})
		op, j, _ := ie.pausedForContinue(t)
		d := ie.d
		d.timers.Lease = time.Second
		b := ie.buildWith(d, options{onRunner: func(job) {}})
		c := continueCall(ie.human("h-author"), "k-continue-order-01", j.claim.ID)
		lock := holdActOrder(t, ie.db)
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- ie.do(b, c) }()
		dbtest.WaitForLockWait(t, ie.db)
		time.Sleep(1200 * time.Millisecond)
		released := time.Now()
		if err := lock.Rollback(); err != nil {
			t.Fatal(err)
		}
		if got := decode[map[string]any](t, <-done, http.StatusAccepted); got["operation"] != op {
			t.Fatalf("body %v", got)
		}
		leaseLiveAfter(t, ie.db, j.claim.ID, released)
	})
	t.Run("expiry in the wait", func(t *testing.T) {
		ie := newIngestEnv(t, options{})
		op, j, first := ie.pausedForContinue(t)
		mustExec(t, ie.db, `UPDATE staging_claim SET expires_at = clock_timestamp() + interval '1 second' WHERE id = $1`, j.claim.ID)
		started := make(chan job, 1)
		b := ie.buildWith(ie.d, options{onRunner: func(j job) { started <- j }})
		c := continueCall(ie.human("h-author"), "k-continue-expiry-0", j.claim.ID)
		lock := holdActOrder(t, ie.db)
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- ie.do(b, c) }()
		dbtest.WaitForLockWait(t, ie.db)
		time.Sleep(1200 * time.Millisecond)
		if err := lock.Rollback(); err != nil {
			t.Fatal(err)
		}
		if p := wantProblem(t, <-done, http.StatusConflict, "conflict"); p["detail"] != "the ingestion has ended" {
			t.Fatalf("problem %v", p)
		}
		if r := ie.pausedRow(t, j.claim.ID); r.state != "paused" || r.review != "pending" || r.gen != first.gen ||
			r.opGen != first.gen || len(events(t, ie.db, op)) != 3 {
			t.Fatalf("claim %+v after a refused continuation", r)
		}
		select {
		case j := <-started:
			t.Fatalf("a refused continuation started %+v", j)
		default:
		}
	})
}
