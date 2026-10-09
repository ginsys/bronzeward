package api

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/staging"
)

// reviewedJob starts an encrypted import of two extracted values with the operator review
// requested (compilation §3.6) and returns its operation and the job handed to the runner.
func (ie *ingestEnv) reviewedJob(t *testing.T) (string, job) {
	t.Helper()
	return ie.startJob(t, map[string]any{"staging": "encrypted", "review": true, "document": twoSecrets, "marks": []string{labelMark}})
}

// pausedRow is a claim's pause-relevant state and its operation's.
type pausedRow struct {
	state, review   string
	gen             int64
	payload, digest []byte
	lease, pausedAt time.Time
	opState         string
	opLease         time.Time
	opOwner, owner  string
	opGen           int64
}

// String is the row as a failure message prints it: the envelope by length only, never its
// ciphertext or digest.
func (r pausedRow) String() string {
	return fmt.Sprintf("{state:%s review:%s gen:%d payload:%d bytes opState:%s opGen:%d}",
		r.state, r.review, r.gen, len(r.payload), r.opState, r.opGen)
}

func (ie *ingestEnv) pausedRow(t *testing.T, claim string) pausedRow {
	t.Helper()
	var r pausedRow
	if err := ie.db.QueryRow(`SELECT c.state, coalesce(c.review, ''), c.owner_gen, c.payload, c.payload_digest, c.lease_until,
		clock_timestamp(), o.state, o.lease_until, o.owner, o.owner_gen, c.owner
		FROM staging_claim c JOIN operation o ON o.ingestion = c.id WHERE c.id = $1`, claim).
		Scan(&r.state, &r.review, &r.gen, &r.payload, &r.digest, &r.lease, &r.pausedAt, &r.opState, &r.opLease, &r.opOwner, &r.opGen,
			&r.owner); err != nil {
		t.Fatal(err)
	}
	return r
}

// PA §9.3: a review under transient staging is refused 422 review-needs-encrypted-staging before
// any claim exists. Control: the same request under encrypted staging is accepted.
func TestReviewNeedsEncryptedStaging(t *testing.T) {
	ie := newIngestEnv(t, options{})
	author := ie.human("h-author")
	rec := ie.do(ie.api, ingestCall(author, "k-review-transient-01", ie.etag, ie.body(map[string]any{"review": true})))
	if p := wantProblem(t, rec, http.StatusUnprocessableEntity, "validation-failed"); p["rule"] != "review-needs-encrypted-staging" {
		t.Fatalf("problem %v", p)
	}
	if claims, ops := ie.rowCounts(t); claims != 0 || ops != 0 || len(ie.runs()) != 0 {
		t.Fatalf("%d claims, %d operations, %d jobs after the refusal", claims, ops, len(ie.runs()))
	}
	rec = ie.do(ie.api, ingestCall(author, "k-review-encrypted-01", ie.etag, ie.body(map[string]any{"review": true, "staging": "encrypted"})))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("control: %d %s", rec.Code, rec.Body)
	}
	t.Logf("transient review refused 422 with no claim; control (encrypted): accepted")
}

// Compilation §3.6 item 1, PA §8.2/§8.3: a reviewed ingestion stages its envelope and pauses in
// one transaction instead of the draft transaction: the claim paused with its envelope and a
// pending review, its lease and its operation's ended at the pause, the operation running under
// the same owner with the staged and paused events, and no draft revision written. The resource
// reads paused with the review pending.
func TestReviewPausesAfterStage(t *testing.T) {
	ie := newIngestEnv(t, options{})
	before := ie.currentETag(t)
	op, j := ie.reviewedJob(t)
	t1 := false
	ie.runWith(t, options{beforeT1: func() { t1 = true }}, j)
	if t1 {
		t.Fatal("the reviewed run reached the draft transaction")
	}
	ie.draftUnchanged(t, before)
	r := ie.pausedRow(t, j.claim.ID)
	if r.state != "paused" || r.review != "pending" || r.gen != 1 || len(r.payload) == 0 || len(r.digest) != 32 {
		t.Fatalf("claim %+v", r)
	}
	if r.lease.After(r.pausedAt) || !r.opLease.Equal(r.lease) || r.opState != "running" || r.opOwner != r.owner || r.opGen != 1 {
		t.Fatalf("leases and operation %+v", r)
	}
	evs := events(t, ie.db, op)
	if !slices.Equal(eventTypes(evs), []string{"started", "staged", "paused"}) || evs[2]["generation"] != float64(1) || len(evs[2]) != 2 {
		t.Fatalf("events %v", evs)
	}
	b := decode[map[string]any](t, ie.do(ie.api, call{method: "GET", path: prefix + "/ingestions/" + j.claim.ID,
		token: ie.human("h-author")}), http.StatusOK)
	if b["state"] != "paused" || b["review"] != "pending" {
		t.Fatalf("resource %v", b)
	}
}

// PA §9.3: an ingestion that requested no review reads review null.
func TestIngestionResourceReviewNull(t *testing.T) {
	ie := newIngestEnv(t, options{})
	_, j := ie.startJob(t, nil)
	b := decode[map[string]any](t, ie.do(ie.api, call{method: "GET", path: prefix + "/ingestions/" + j.claim.ID,
		token: ie.human("h-author")}), http.StatusOK)
	if v, ok := b["review"]; !ok || v != nil {
		t.Fatalf("resource %v", b)
	}
}

// Compilation §3.4, PA §9.3: a takeover of a paused claim is 409 conflict, its lease ended or not,
// and nothing changes.
func TestTakeoverOfPausedRefused(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j := ie.reviewedJob(t)
	ie.runWith(t, options{}, j)
	tk := ie.newTaker(options{})
	rec := ie.do(tk, takeoverCall(ie.human("h-author"), "k-take-paused-0123", j.claim.ID))
	if p := wantProblem(t, rec, http.StatusConflict, "conflict"); p["detail"] != "the ingestion is paused for the operator's review" {
		t.Fatalf("problem %v", p)
	}
	if r := ie.pausedRow(t, j.claim.ID); r.state != "paused" || r.gen != 1 || len(tk.taken(t)) != 0 ||
		!slices.Equal(eventTypes(events(t, ie.db, op)), []string{"started", "staged", "paused"}) {
		t.Fatalf("claim %+v after a refused takeover", r)
	}
}

// Compilation §3.4, §3.6 item 6: a taken-over claim whose review is still pending (a mark's or a
// continuation's run interrupted after taking it) pauses once its envelope is decrypted and
// checked, its stored envelope unchanged, instead of running the draft transaction.
func TestTakenOverReviewPausesAgain(t *testing.T) {
	ie := newIngestEnv(t, options{})
	before := ie.currentETag(t)
	op, j := ie.reviewedJob(t)
	ie.runWith(t, options{}, j)
	first := ie.pausedRow(t, j.claim.ID)
	// A mark's run took the claim and stopped (compilation §3.6 item 6): held at the next
	// generation, its lease lapsed, the review still pending.
	mustExec(t, ie.db, `UPDATE staging_claim SET state = 'held', owner_gen = 2, lease_until = now() - interval '1 second' WHERE id = $1`,
		j.claim.ID)
	mustExec(t, ie.db, `UPDATE operation SET owner_gen = 2 WHERE ingestion = $1`, j.claim.ID)
	tk := ie.newTaker(options{})
	tj := ie.takeOver(t, tk, "k-take-review-01234", j.claim.ID, op, 3)
	if tj.claim.Review != "pending" {
		t.Fatalf("taken job review %q", tj.claim.Review)
	}
	t1 := false
	tk.API.o.beforeT1 = func() { t1 = true }
	tk.runIngest(t.Context(), tj)
	if t1 {
		t.Fatal("the taken-over run reached the draft transaction")
	}
	ie.draftUnchanged(t, before)
	r := ie.pausedRow(t, j.claim.ID)
	if r.state != "paused" || r.gen != 3 || string(r.payload) != string(first.payload) || string(r.digest) != string(first.digest) ||
		r.lease.After(r.pausedAt) || !r.opLease.Equal(r.lease) || r.opState != "running" {
		t.Fatalf("claim %+v", r)
	}
	evs := events(t, ie.db, op)
	if !slices.Equal(eventTypes(evs), []string{"started", "staged", "paused", "taken-over", "paused"}) || evs[4]["generation"] != float64(3) {
		t.Fatalf("events %v", evs)
	}
}

// Compilation §3.6 item 7, PA §9.3: the operator abandons a paused claim; its payload is cleared
// and its operation fails ingestion-abandoned.
func TestAbandonPaused(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j := ie.reviewedJob(t)
	ie.runWith(t, options{}, j)
	rec := ie.do(ie.api, call{method: "POST", path: prefix + "/ingestions/" + j.claim.ID + "/abandonments", token: ie.human("h-author"),
		key: "k-abandon-paused-012", body: `{}`})
	if b := decode[map[string]any](t, rec, http.StatusOK); b["state"] != "abandoned" {
		t.Fatalf("resource %v", b)
	}
	if st, _, payload := claimRow(t, ie.db, j.claim.ID); st != "abandoned" || payload {
		t.Fatalf("claim %s, payload %v", st, payload)
	}
	if r := readOp(t, ie.db, op); r.state != "failed" || r.error["type"] != "urn:bronzeward:problem:ingestion-abandoned" {
		t.Fatalf("operation %+v", r)
	}
}

// PA §7.2: a paused claim is live for its key (compilation §3.6). A fragment PUT under the key of
// a paused import with no record refuses 409 naming it and creates nothing.
func TestFragmentPutPausedClaimForKey(t *testing.T) {
	ie := newFragmentEnv(t, options{})
	const k = "k-fragment-paused-01"
	var principal string
	if err := ie.db.QueryRow(`SELECT id FROM principal WHERE sub = 'h-author'`).Scan(&principal); err != nil {
		t.Fatal(err)
	}
	c := staging.Claim{ID: id.New(id.Ingestion), Kind: "import", Mode: "encrypted", Cluster: ie.cluster, Machine: ie.machine,
		Review: "pending", Gen: 1}
	tx, err := ie.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := staging.Create(t.Context(), tx, ie.d.owner, staging.Timers{Lease: time.Minute, AbsoluteExpiry: time.Hour}, c, principal, k); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	mustExec(t, ie.db, `UPDATE staging_claim SET state = 'paused', lease_until = clock_timestamp(), payload = '\x01',
		payload_digest = decode(repeat('00', 32), 'hex') WHERE id = $1`, c.ID)
	body := fragmentPut(t, "cluster", labelDoc, []string{labelMark}, nil)
	if p := wantProblem(t, ie.putFragment("registries", body, ie.etag, k), http.StatusConflict, "conflict"); p["ingestion"] != c.ID {
		t.Fatalf("paused claim refusal %v", p)
	}
	if calls, _ := ie.f.paths(); calls != 0 {
		t.Fatalf("%d generation creates under a paused claim", calls)
	}
	if n := count(t, ie.db, `SELECT count(*) FROM staging_claim WHERE idempotency_key = $1`, k); n != 1 {
		t.Fatalf("%d claims under the key", n)
	}
}
