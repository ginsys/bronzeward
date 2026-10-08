package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/staging"
)

func takeoverCall(tok, k, claim string) call {
	return call{method: "POST", path: prefix + "/ingestions/" + claim + "/takeovers", token: tok, key: k, body: `{}`}
}

// taker is a second server over ie's database: another owner string, the same epoch. It records
// each job handed to its runner.
type taker struct {
	*API
	mu   sync.Mutex
	jobs []job
}

func (ie *ingestEnv) newTaker(o options) *taker {
	tk := &taker{}
	d := ie.d
	d.owner = staging.Owner{ID: "b/2/" + rand.Text(), Epoch: ie.d.owner.Epoch}
	o.onRunner = func(j job) {
		tk.mu.Lock()
		defer tk.mu.Unlock()
		tk.jobs = append(tk.jobs, j)
	}
	tk.API = ie.buildWith(d, o)
	return tk
}

func (tk *taker) taken(t *testing.T) []job {
	t.Helper()
	tk.mu.Lock()
	defer tk.mu.Unlock()
	return append([]job(nil), tk.jobs...)
}

// stageThenStop runs j until its envelope is staged, then stops it before T1, as a crash there
// would: the claim stays held with its payload and the operation running.
func (ie *ingestEnv) stageThenStop(t *testing.T, j job) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ie.build(options{afterStage: cancel}).runIngest(ctx, j)
	if st, gen, payload := claimRow(t, ie.db, j.claim.ID); st != "held" || gen != 1 || !payload {
		t.Fatalf("after the stop: claim %s at %d, payload %v", st, gen, payload)
	}
}

func (ie *ingestEnv) lapse(t *testing.T, claim string) {
	t.Helper()
	mustExec(t, ie.db, `UPDATE staging_claim SET lease_until = now() - interval '1 second' WHERE id = $1`, claim)
}

// stagedJob starts an encrypted ingestion of two extracted values, stages it, stops it before T1
// and lets its lease lapse: a taker's case (compilation §3.4).
func (ie *ingestEnv) stagedJob(t *testing.T) (string, job) {
	t.Helper()
	op, j := ie.startJob(t, map[string]any{"staging": "encrypted", "document": twoSecrets, "marks": []string{labelMark}})
	ie.stageThenStop(t, j)
	ie.lapse(t, j.claim.ID)
	return op, j
}

// takeOver posts a takeover of claim to tk and returns the job it hands its runner.
func (ie *ingestEnv) takeOver(t *testing.T, tk *taker, k, claim, op string, gen int64) job {
	t.Helper()
	before := len(tk.taken(t))
	rec := ie.do(tk, takeoverCall(ie.human("h-author"), k, claim))
	ie.bodies = append(ie.bodies, rec.Body.String())
	b := decode[map[string]string](t, rec, http.StatusAccepted)
	if !maps.Equal(b, map[string]string{"operation": op, "ingestion": claim}) || rec.Header().Get("Location") != prefix+"/operations/"+op {
		t.Fatalf("body %v, Location %q", b, rec.Header().Get("Location"))
	}
	if st, g, payload := claimRow(t, ie.db, claim); st != "resumed" || g != gen || !payload {
		t.Fatalf("claim %s at %d, payload %v", st, g, payload)
	}
	js := tk.taken(t)
	if len(js) != before+1 {
		t.Fatalf("%d jobs handed over, want one more than %d", len(js), before)
	}
	j := js[len(js)-1]
	if j.claim.ID != claim || j.claim.Gen != gen || j.claim.Mode != "encrypted" || j.op != op || j.draft != ie.draft || j.resume == nil {
		t.Fatalf("job %+v", j)
	}
	return j
}

// C §3.4, PA §8.2 (T8): a takeover of a staged encrypted claim whose lease lapsed answers 202 at
// the operation and commits the claim resumed at the next generation with the taken-over event,
// under the taker. Its runner decrypts the envelope and goes to T1, which persists exactly what
// the original run sealed: no extraction, no generation created, no digest or guard run again.
func TestTakeoverResumesToT1(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j := ie.stagedJob(t)
	ie.f.mu.Lock()
	envelope := ie.f.envelope
	ie.f.mu.Unlock()
	want, err := ingest.Open(envelope, sha256.Sum256(envelope))
	if err != nil {
		t.Fatal(err)
	}
	creates, _ := ie.f.paths()
	digests := len(ie.f.asked())
	draftRev := j.draftRev

	b := ie.newTaker(options{})
	jb := ie.takeOver(t, b, "k-takeover-0123456789", j.claim.ID, op, 2)
	evs := events(t, ie.db, op)
	if !slices.Equal(eventTypes(evs), []string{"started", "staged", "taken-over"}) || evs[2]["generation"] != float64(2) || len(evs[2]) != 2 {
		t.Fatalf("events %v", evs)
	}
	if n := count(t, ie.db, `SELECT count(*) FROM act WHERE action = 'ingestion.takeover'`); n != 1 {
		t.Fatalf("%d takeover acts", n)
	}

	b.runIngest(t.Context(), jb)
	if r := readOp(t, ie.db, op); r.state != "succeeded" || r.result["draftRevision"] != float64(draftRev+1) {
		t.Fatalf("operation %+v", r)
	}
	if got := eventTypes(events(t, ie.db, op)); !slices.Equal(got, []string{"started", "staged", "taken-over", "succeeded"}) {
		t.Fatalf("events %v", got)
	}
	if st, _, payload := claimRow(t, ie.db, j.claim.ID); st != "released" || payload {
		t.Fatalf("claim %s, payload %v", st, payload)
	}
	var doc, digestKey string
	var ct, dg, conf []byte
	if err := ie.db.QueryRow(`SELECT document, baseline_ciphertext, baseline_digest, baseline_digest_key, configuration_digest
		FROM import_base_revision`).Scan(&doc, &ct, &dg, &digestKey, &conf); err != nil {
		t.Fatal(err)
	}
	if doc != string(want.Sanitized.Documents()) || string(ct) != string(want.Baseline.Ciphertext) || string(dg) != string(want.Baseline.Digest[:]) ||
		digestKey != want.Baseline.DigestKey || string(conf) != string(want.Baseline.Configuration[:]) {
		t.Fatal("the import base is not what the original run sealed")
	}
	gens := map[string]string{}
	rows, err := ie.db.Query(`SELECT name, generation FROM import_base_reference`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n, g string
		if err := rows.Scan(&n, &g); err != nil {
			t.Fatal(err)
		}
		gens[n] = g
	}
	rows.Close()
	if len(gens) != 2 || !maps.Equal(gens, want.Generations) {
		t.Fatalf("references %v, sealed %v", gens, want.Generations)
	}
	if c, _ := ie.f.paths(); c != creates || len(ie.f.asked()) != digests || ie.f.decrypts != 1 {
		t.Fatalf("the resume created %d generations, asked %d digests, decrypted %d times", c-creates, len(ie.f.asked())-digests, ie.f.decrypts)
	}
	// A replay of the takeover hands over nothing.
	rec := ie.do(b, takeoverCall(ie.human("h-author"), "k-takeover-0123456789", j.claim.ID))
	if rec.Code != http.StatusAccepted || len(b.taken(t)) != 1 {
		t.Fatalf("replay: %d, %d jobs", rec.Code, len(b.taken(t)))
	}
}

// A takeover's new lease runs from after the act-order lock, its last wait: a takeover that waited
// on it longer than the lease commits a live claim (PA §1.2 rules 4 and 5).
func TestTakeoverAfterActOrderWait(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j := ie.stagedJob(t)
	d := ie.d
	d.owner = staging.Owner{ID: "b/2/" + rand.Text(), Epoch: ie.d.owner.Epoch}
	d.timers.Lease = time.Second
	b := ie.buildWith(d, options{onRunner: func(job) {}})
	lock := holdActOrder(t, ie.db)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- ie.do(b, takeoverCall(ie.human("h-author"), "k-takeover-order-0123", j.claim.ID)) }()
	dbtest.WaitForLockWait(t, ie.db)
	time.Sleep(1200 * time.Millisecond)
	released := time.Now()
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	rec := <-done
	if got := decode[map[string]string](t, rec, http.StatusAccepted); got["operation"] != op {
		t.Fatalf("body %v", got)
	}
	leaseLiveAfter(t, ie.db, j.claim.ID, released)
}

// PA §9.2, C §3.4: each refusal answers its problem and leaves the claim and its operation as they
// were. The route is for human authors only.
func TestTakeoverRefusals(t *testing.T) {
	ie := newIngestEnv(t, options{})
	author := ie.human("h-author")
	b := ie.newTaker(options{})
	_, staged := ie.stagedJob(t)
	ie.bump(t)
	wantProblem(t, ie.do(b, takeoverCall(ie.robot, "k-take-robot-012345", staged.claim.ID)), http.StatusForbidden, "forbidden")
	wantProblem(t, ie.do(b, takeoverCall(ie.human("h-viewer"), "k-take-viewer-01234", staged.claim.ID)), http.StatusForbidden, "forbidden")
	wantProblem(t, ie.do(b, takeoverCall(author, "k-take-none-0123456", id.New(id.Ingestion))), http.StatusNotFound, "not-found")
	wantProblem(t, ie.do(b, takeoverCall(author, "k-take-bad-01234567", "ing_nope")), http.StatusNotFound, "not-found")

	_, released := ie.startJob(t, map[string]any{"staging": "encrypted"})
	ie.runWith(t, options{}, released) // T1 moves the draft to its next revision
	ie.lapse(t, released.claim.ID)
	_, live := ie.startJob(t, map[string]any{"staging": "encrypted"})
	ie.bump(t)
	_, transient := ie.startJob(t, nil)
	ie.bump(t)
	for _, tc := range []struct {
		name, claim, detail string
	}{
		{"a live lease", live.claim.ID, "the claim's lease has not lapsed"},
		{"a transient claim", transient.claim.ID, "only an encrypted claim is taken over"},
		{"released", released.claim.ID, "the ingestion has ended"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ie.wantTakeoverRefused(t, b, tc.claim, http.StatusConflict, "conflict", tc.detail)
		})
	}
	t.Run("past its absolute expiry", func(t *testing.T) {
		_, j := ie.stagedJob(t)
		mustExec(t, ie.db, `UPDATE staging_claim SET expires_at = now() - interval '1 second' WHERE id = $1`, j.claim.ID)
		ie.wantTakeoverRefused(t, b, j.claim.ID, http.StatusConflict, "conflict", "the ingestion has ended")
	})
	t.Run("not configured", func(t *testing.T) {
		const detail = "ingestion is not configured; nothing was committed"
		none := ie.buildWith(deps{life: ie.d.life, runs: ie.d.runs}, options{})
		ie.wantTakeoverRefused(t, none, staged.claim.ID, http.StatusServiceUnavailable, "dependency-unavailable", detail)
		d := ie.d
		d.ing = nil // an owner, but no provider to decrypt with
		ie.wantTakeoverRefused(t, ie.buildWith(d, options{}), staged.claim.ID, http.StatusServiceUnavailable, "dependency-unavailable", detail)
	})
	t.Run("epochs", func(t *testing.T) {
		newEpoch(t, ie.db)
		// This process started before the entry: it may issue no ownership.
		ie.wantTakeoverRefused(t, b, staged.claim.ID, http.StatusServiceUnavailable, "epoch-superseded", "")
		// A process of the current epoch: the claim is of the earlier one.
		d := ie.d
		d.owner = staging.Owner{ID: "c/3/" + rand.Text(), Epoch: epoch(t, ie.db)}
		ie.wantTakeoverRefused(t, ie.buildWith(d, options{}), staged.claim.ID, http.StatusConflict, "conflict",
			"the claim predates the current epoch")
	})
	if n := count(t, ie.db, `SELECT count(*) FROM act WHERE action = 'ingestion.takeover'`); n != 0 {
		t.Fatalf("%d takeover acts", n)
	}
	if len(b.taken(t)) != 0 {
		t.Fatal("a refused takeover handed a job over")
	}
}

// bump moves the draft to its next revision, so another ingestion can start while one runs at
// the revision before (PA §9.2: one running ingestion per draft revision).
func (ie *ingestEnv) bump(t *testing.T) {
	t.Helper()
	mustExec(t, ie.db, `UPDATE draft SET revision = revision + 1 WHERE id = $1`, ie.draft)
}

func (ie *ingestEnv) wantTakeoverRefused(t *testing.T, h http.Handler, claim string, status int, code, detail string) {
	t.Helper()
	before := ingestionRow(t, ie.db, claim)
	ie.n++
	doc := wantProblem(t, ie.do(h, takeoverCall(ie.human("h-author"), fmt.Sprintf("k-take-refused-%06d", ie.n), claim)), status, code)
	if detail != "" && doc["detail"] != detail {
		t.Errorf("detail %q, want %q", doc["detail"], detail)
	}
	if after := ingestionRow(t, ie.db, claim); after != before {
		t.Errorf("changed: %+v -> %+v", before, after)
	}
}

// ingestionRow is a claim's state, generation, owner and lease, with its operation's.
type ingestionState struct {
	claimState, opState, claimOwner string
	opOwner                         sql.NullString
	claimGen, opGen                 int64
	lease                           time.Time
	events                          int
}

func ingestionRow(t *testing.T, db *sql.DB, claim string) ingestionState {
	t.Helper()
	var s ingestionState
	if err := db.QueryRow(`SELECT c.state, o.state, c.owner, o.owner, c.owner_gen, o.owner_gen, c.lease_until, o.last_event
		FROM staging_claim c JOIN operation o ON o.ingestion = c.id WHERE c.id = $1`, claim).
		Scan(&s.claimState, &s.opState, &s.claimOwner, &s.opOwner, &s.claimGen, &s.opGen, &s.lease, &s.events); err != nil {
		t.Fatal(err)
	}
	return s
}

// C §3.4: a claim whose envelope was never stored has nothing to decrypt. The takeover abandons
// it and fails its operation ingestion-abandoned with the cause, under the new generation, still
// 202 at the operation; no job is handed over.
func TestTakeoverNothingToDecrypt(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j := ie.startJob(t, map[string]any{"staging": "encrypted"}) // never run: no payload
	ie.lapse(t, j.claim.ID)
	b := ie.newTaker(options{})
	rec := ie.do(b, takeoverCall(ie.human("h-author"), "k-take-nothing-0123", j.claim.ID))
	if body := decode[map[string]string](t, rec, http.StatusAccepted); body["operation"] != op {
		t.Fatalf("body %v", body)
	}
	r := readOp(t, ie.db, op)
	if r.state != "failed" || r.owner.Valid || r.lease.Valid || r.error["type"] != "urn:bronzeward:problem:ingestion-abandoned" ||
		r.error["status"] != float64(http.StatusConflict) || r.error["instance"] != op {
		t.Fatalf("operation %+v", r)
	}
	evs := events(t, ie.db, op)
	if !slices.Equal(eventTypes(evs), []string{"started", "failed"}) || evs[1]["code"] != "ingestion-abandoned" ||
		evs[1]["cause"] != "nothing-to-decrypt" || len(evs[1]) != 3 {
		t.Fatalf("events %v", evs)
	}
	if st, gen, payload := claimRow(t, ie.db, j.claim.ID); st != "abandoned" || gen != 2 || payload {
		t.Fatalf("claim %s at %d, payload %v", st, gen, payload)
	}
	if len(b.taken(t)) != 0 {
		t.Fatal("a job was handed over")
	}
}

// Compilation §3.4, ruling 7: a decryption failure leaves the claim resumed under the taker, with
// a resume-failed event, and the run stops heartbeating. Once its lease lapses another takeover
// succeeds (generation 3); after the absolute expiry a takeover is refused and the claim reads
// abandoned.
func TestTakeoverDecryptFailureLeavesResumed(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j := ie.stagedJob(t)
	before := ie.currentETag(t)
	ie.f.decryptErr = provider.ErrUnavailable
	ie.d.timers.Heartbeat = 20 * time.Millisecond
	reachedT1 := false
	b := ie.newTaker(options{beforeT1: func() { reachedT1 = true }})
	jb := ie.takeOver(t, b, "k-take-decrypt-0123", j.claim.ID, op, 2)
	b.runIngest(t.Context(), jb)
	if reachedT1 {
		t.Fatal("the run went on to T1 after the failed decryption")
	}
	evs := events(t, ie.db, op)
	if !slices.Equal(eventTypes(evs), []string{"started", "staged", "taken-over", "resume-failed"}) ||
		evs[3]["code"] != "dependency-unavailable" || len(evs[3]) != 2 {
		t.Fatalf("events %v", evs)
	}
	if r := readOp(t, ie.db, op); r.state != "running" {
		t.Fatalf("operation %+v", r)
	}
	lease := ingestionRow(t, ie.db, j.claim.ID).lease
	time.Sleep(100 * time.Millisecond) // five heartbeat periods: none extends the lease
	if s := ingestionRow(t, ie.db, j.claim.ID); s.claimState != "resumed" || s.claimGen != 2 || !s.lease.Equal(lease) {
		t.Fatalf("after the failure: %+v (lease was %v)", s, lease)
	}
	if !ie.logged("decrypting the staged envelope") {
		t.Fatal("the failure was not logged")
	}
	ie.draftUnchanged(t, before)

	ie.lapse(t, j.claim.ID)
	c := ie.newTaker(options{})
	jc := ie.takeOver(t, c, "k-take-decrypt-again", j.claim.ID, op, 3)
	c.runIngest(t.Context(), jc)
	if got := eventTypes(events(t, ie.db, op)); !slices.Equal(got, []string{"started", "staged", "taken-over", "resume-failed",
		"taken-over", "resume-failed"}) {
		t.Fatalf("events %v", got)
	}

	mustExec(t, ie.db, `UPDATE staging_claim SET expires_at = now() - interval '1 second', lease_until = now() - interval '2 seconds'
		WHERE id = $1`, j.claim.ID)
	ie.wantTakeoverRefused(t, c, j.claim.ID, http.StatusConflict, "conflict", "the ingestion has ended")
	if st := decode[map[string]any](t, ie.do(ie.api, getIngestionCall(ie.human("h-viewer"), j.claim.ID)), http.StatusOK)["state"]; st != "abandoned" {
		t.Fatalf("the expired claim reads %v", st)
	}
}

// Compilation §3.1, ruling 7: an envelope that does not match its digest is an integrity failure:
// the resume abandons the claim with 500 internal-error, and T1 writes nothing.
func TestTakeoverDigestMismatchAbandons(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j := ie.stagedJob(t)
	before := ie.currentETag(t)
	mustExec(t, ie.db, `UPDATE staging_claim SET payload_digest = sha256('another envelope') WHERE id = $1`, j.claim.ID)
	b := ie.newTaker(options{})
	jb := ie.takeOver(t, b, "k-take-digest-01234", j.claim.ID, op, 2)
	b.runIngest(t.Context(), jb)
	r := readOp(t, ie.db, op)
	if r.state != "failed" || r.error["type"] != "urn:bronzeward:problem:internal-error" || r.error["status"] != float64(http.StatusInternalServerError) {
		t.Fatalf("operation %+v", r)
	}
	if got := eventTypes(events(t, ie.db, op)); !slices.Equal(got, []string{"started", "staged", "taken-over", "failed"}) {
		t.Fatalf("events %v", got)
	}
	if st, _, payload := claimRow(t, ie.db, j.claim.ID); st != "abandoned" || payload {
		t.Fatalf("claim %s, payload %v", st, payload)
	}
	ie.draftUnchanged(t, before)
}

// PA §5.1, Review Focus 1: a takeover racing the old owner's T1. T1 first: the takeover waits for
// the claim, then finds it released and refuses 409. Takeover first: T1's release is fenced and
// nothing of the draft is written; the operation stays the taker's.
func TestTakeoverRacesT1(t *testing.T) {
	t.Run("T1 first", func(t *testing.T) {
		ie := newIngestEnv(t, options{})
		op, j := ie.startJob(t, map[string]any{"staging": "encrypted"})
		ie.d.timers.Heartbeat = time.Hour // no heartbeat waits on the claim during the race
		b := ie.newTaker(options{})
		tok := ie.human("h-author")
		inT1 := false
		var lapsed time.Time
		done := make(chan *httptest.ResponseRecorder, 1)
		ie.runWith(t, options{beforeT1: func() {
			// A lease that outlives T1's release and lapses before its COMMIT: the takeover then
			// finds a claim eligible as last committed, and only T1's lock holds it off.
			mustExec(t, ie.db, `UPDATE staging_claim SET lease_until = clock_timestamp() + interval '2 seconds' WHERE id = $1`, j.claim.ID)
			lapsed = time.Now().Add(2100 * time.Millisecond)
			inT1 = true
		}, commit: func(tx *sql.Tx) error {
			if inT1 {
				var t1 int
				if err := tx.QueryRow(`SELECT pg_backend_pid()`).Scan(&t1); err != nil {
					return err
				}
				time.Sleep(time.Until(lapsed))
				go func() { done <- ie.do(b, takeoverCall(tok, "k-take-race-t1-0123", j.claim.ID)) }()
				waitBlockedBy(t, ie.db, t1)
			}
			return tx.Commit()
		}}, j)
		if !inT1 {
			t.Fatal("the run never reached T1")
		}
		var rec *httptest.ResponseRecorder
		select {
		case rec = <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("no takeover answered: T1 never reached its COMMIT, or the takeover hung")
		}
		doc := wantProblem(t, rec, http.StatusConflict, "conflict")
		if doc["detail"] != "the ingestion has ended" {
			t.Fatalf("detail %v", doc["detail"])
		}
		if r := readOp(t, ie.db, op); r.state != "succeeded" {
			t.Fatalf("operation %+v", r)
		}
	})
	t.Run("takeover first", func(t *testing.T) {
		ie := newIngestEnv(t, options{})
		op, j := ie.startJob(t, map[string]any{"staging": "encrypted"})
		before := ie.currentETag(t)
		b := ie.newTaker(options{})
		ie.runWith(t, options{beforeT1: func() {
			ie.lapse(t, j.claim.ID)
			if rec := ie.do(b, takeoverCall(ie.human("h-author"), "k-take-race-first-0", j.claim.ID)); rec.Code != http.StatusAccepted {
				t.Errorf("takeover %d %s", rec.Code, rec.Body)
			}
		}}, j)
		ie.draftUnchanged(t, before)
		s := ingestionRow(t, ie.db, j.claim.ID)
		if s.claimState != "resumed" || s.claimGen != 2 || s.opState != "running" || s.opOwner.String != s.claimOwner ||
			s.claimOwner == ie.d.owner.ID {
			t.Fatalf("after the race: %+v", s)
		}
		if got := eventTypes(events(t, ie.db, op)); !slices.Equal(got, []string{"started", "staged", "taken-over"}) {
			t.Fatalf("events %v", got)
		}
	})
}

// waitBlockedBy returns once a session waits for a lock the backend blocker holds, or fails the
// test.
func waitBlockedBy(t *testing.T, db *sql.DB, blocker int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if count(t, db, `SELECT count(*) FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))`, blocker) > 0 {
			return
		}
	}
	t.Error("no session waited for T1's lock")
}

// Review Focus 2: two takers of one lapsed claim: exactly one 202, the other 409 on the winner's
// live lease; the generation goes up by exactly one.
func TestTakeoverConcurrentTakers(t *testing.T) {
	ie := newIngestEnv(t, options{})
	_, j := ie.stagedJob(t)
	b, c := ie.newTaker(options{}), ie.newTaker(options{})
	codes := make(chan int, 2)
	for i, tk := range []*taker{b, c} {
		go func() {
			codes <- ie.do(tk, takeoverCall(ie.human("h-author"), "k-take-concurrent-"+string(rune('a'+i)), j.claim.ID)).Code
		}()
	}
	got := []int{<-codes, <-codes}
	slices.Sort(got)
	if !slices.Equal(got, []int{http.StatusAccepted, http.StatusConflict}) {
		t.Fatalf("codes %v", got)
	}
	if _, gen, _ := claimRow(t, ie.db, j.claim.ID); gen != 2 {
		t.Fatalf("generation %d", gen)
	}
	if n := len(b.taken(t)) + len(c.taken(t)); n != 1 {
		t.Fatalf("%d jobs handed over", n)
	}
}

// C §13: a resumed run echoes no input, in a response, a stored row or the log. The decrypted
// envelope's other contents, the sanitized text kept in the document and the baseline
// ciphertext, are T1's to store, and are emitted nowhere: no response, event, problem or log.
func TestTakeoverNoEcho(t *testing.T) {
	ie := newIngestEnv(t, options{})
	canary := "bw-canary-" + strings.ToLower(rand.Text())
	const kept = "bw-kept-label-canary" // unmarked, so it stays in the sanitized document
	op, j := ie.startJob(t, map[string]any{"staging": "encrypted",
		"document": "machine:\n  token: " + canary + "\n  nodeLabels:\n    tier: " + kept + "\n"})
	ie.stageThenStop(t, j)
	ie.lapse(t, j.claim.ID)
	b := ie.newTaker(options{})
	jb := ie.takeOver(t, b, "k-take-noecho-01234", j.claim.ID, op, 2)
	b.runIngest(t.Context(), jb)
	if r := readOp(t, ie.db, op); r.state != "succeeded" {
		t.Fatalf("operation %+v", r)
	}
	assertAbsent(t, ie, canary)
	var ct []byte
	if err := ie.db.QueryRow(`SELECT baseline_ciphertext FROM import_base_revision WHERE document LIKE '%' || $1 || '%'`,
		kept).Scan(&ct); err != nil {
		t.Fatalf("the kept text is not in the stored document: %v", err)
	}
	for _, s := range []string{kept, string(ct)} {
		for _, body := range ie.bodies {
			if strings.Contains(body, s) {
				t.Errorf("a response body holds the envelope: %s", body)
			}
		}
		if ie.logged(s) {
			t.Error("the log holds the envelope")
		}
		if n := count(t, ie.db, `SELECT count(*) FROM operation o LEFT JOIN operation_event e ON e.operation = o.id
			WHERE o.id = $1 AND (o::text LIKE '%' || $2 || '%' OR e::text LIKE '%' || $2 || '%')`, op, s); n != 0 {
			t.Error("the operation or its events hold the envelope")
		}
	}
}
