package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/staging"
)

// ingestEnv is an env serving ingestion with a fake provider, over a cluster with one machine and
// an open draft, and a second cluster with a machine of its own. jobs records each job handed to
// the runner.
type ingestEnv struct {
	*env
	f                       *fakeIngester
	cluster, machine, draft string
	otherMachine            string
	etag                    string
	mu                      sync.Mutex
	jobs                    []job
	captured                options  // the API's options: o with onRunner recording each job
	n                       int      // the keys startJob used
	bodies                  []string // the response bodies startJob saw
}

var testTimers = config.Ingestion{Instance: "a", Heartbeat: 5 * time.Second, Lease: 15 * time.Second,
	AbsoluteExpiry: 10 * time.Minute, Sweep: 15 * time.Second}

func newIngestEnv(t *testing.T, o options) *ingestEnv {
	t.Helper()
	ie := setupIngestEnv(t)
	o.onRunner = func(j job) {
		ie.mu.Lock()
		defer ie.mu.Unlock()
		ie.jobs = append(ie.jobs, j)
	}
	ie.captured = o
	ie.api = ie.build(o)
	return ie
}

// newRunningIngestEnv is newIngestEnv whose jobs the server's runner takes, as serve's does.
func newRunningIngestEnv(t *testing.T, o options) *ingestEnv {
	t.Helper()
	ie := setupIngestEnv(t)
	ie.api = ie.build(o)
	return ie
}

func setupIngestEnv(t *testing.T) *ingestEnv {
	t.Helper()
	ie := &ingestEnv{f: &fakeIngester{latest: 1}}
	// The fixture is set up without the test's options; the process epoch is the one the
	// installation had when the API was built. The runners end before the database closes.
	ie.env = newEnvWith(t, deps{ing: ie.f, timers: testTimers, runs: &sync.WaitGroup{}}, options{})
	t.Cleanup(ie.d.runs.Wait)
	ie.d.owner = staging.Owner{ID: "a/1/" + rand.Text(), Epoch: epoch(t, ie.db)}
	author := ie.human("h-author")
	ie.cluster = ie.createCluster(ie.api, author, "k-cluster-0123456789")
	ie.machine = decode[machineBody](t, ie.do(ie.api, machineCall(author, "k-machine-0123456789", ie.cluster, uuidA)), http.StatusCreated).ID
	other := decode[clusterBody](t, ie.do(ie.api, call{method: "POST", path: prefix + "/clusters", token: author, key: "k-cluster-other-0123",
		body: `{"name":"lab","endpoint":"https://cp.lab.example.test:6443","contract":"v1.13"}`}), http.StatusCreated).ID
	ie.otherMachine = decode[machineBody](t, ie.do(ie.api, machineCall(author, "k-machine-other-0123", other,
		"1c6b7d2f-3a4e-4f60-9bac-1d2e3f4a5b6c")), http.StatusCreated).ID
	rec := ie.do(ie.api, call{method: "POST", path: prefix + "/drafts", token: author, key: "k-draft-0123456789ab",
		body: `{"cluster":"` + ie.cluster + `","title":"import"}`})
	ie.draft, ie.etag = decode[draftBody](t, rec, http.StatusCreated).ID, rec.Header().Get("ETag")
	return ie
}

func (ie *ingestEnv) runs() []job {
	ie.mu.Lock()
	defer ie.mu.Unlock()
	return append([]job(nil), ie.jobs...)
}

// body is an ingestion request of ie's machine and draft, with over replacing or removing (nil)
// members.
func (ie *ingestEnv) body(over map[string]any) string {
	m := map[string]any{"kind": "import", "machine": ie.machine, "draft": ie.draft, "source": "document",
		"staging": "transient", "marks": []string{}, "document": "machine:\n  token: bw-synthetic-1\n"}
	maps.Copy(m, over)
	for k, v := range m {
		if v == nil {
			delete(m, k)
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		ie.t.Fatal(err)
	}
	return string(b)
}

func ingestCall(tok, k, ifMatch, body string) call {
	return call{method: "POST", path: prefix + "/ingestions", token: tok, key: k, ifMatch: ifMatch, body: body}
}

func (ie *ingestEnv) rowCounts(t *testing.T) (claims, ops int) {
	t.Helper()
	return count(t, ie.db, "SELECT count(*) FROM staging_claim"), count(t, ie.db, "SELECT count(*) FROM operation")
}

// PA §8, T11: the start answers 202 at the operation, and commits the claim held at generation 1
// and its ingest operation running under the same owner, lease and epoch, with event 1, the act
// and the record naming the operation. Only then is the job handed to the runner; a replay hands
// over nothing.
func TestIngestionStart(t *testing.T) {
	ie := newIngestEnv(t, options{})
	author := ie.human("h-author")
	rec := ie.do(ie.api, ingestCall(author, key, ie.etag, ie.body(nil)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	loc := rec.Header().Get("Location")
	op, ok := strings.CutPrefix(loc, prefix+"/operations/")
	if !ok {
		t.Fatalf("Location %q", loc)
	}
	var claim, mode, state, owner, oEpoch, cl, mch string
	var gen int64
	var lease, expires time.Time
	if err := ie.db.QueryRow(`SELECT id, mode, state, owner, owner_gen, owner_epoch, cluster, machine, lease_until, expires_at
		FROM staging_claim`).Scan(&claim, &mode, &state, &owner, &gen, &oEpoch, &cl, &mch, &lease, &expires); err != nil {
		t.Fatal(err)
	}
	if mode != "transient" || state != "held" || gen != 1 || owner != ie.d.owner.ID || oEpoch != ie.d.owner.Epoch ||
		cl != ie.cluster || mch != ie.machine {
		t.Fatalf("claim %s %s %s gen %d owner %s epoch %s cluster %s machine %s", claim, mode, state, gen, owner, oEpoch, cl, mch)
	}
	if d := expires.Sub(lease); d != testTimers.AbsoluteExpiry-testTimers.Lease {
		t.Fatalf("expiry - lease = %v", d)
	}
	var kind, opState, opOwner, opOEpoch, opEpoch, draft, ing string
	var opGen int64
	var draftRev, last int
	var opLease time.Time
	if err := ie.db.QueryRow(`SELECT kind, state, owner, owner_gen, owner_epoch, epoch, lease_until, draft, draft_revision,
		ingestion, last_event FROM operation WHERE id = $1`, op).Scan(&kind, &opState, &opOwner, &opGen, &opOEpoch, &opEpoch,
		&opLease, &draft, &draftRev, &ing, &last); err != nil {
		t.Fatal(err)
	}
	if kind != "ingest" || opState != "running" || opOwner != owner || opGen != 1 || opOEpoch != oEpoch || opEpoch != oEpoch ||
		!opLease.Equal(lease) || draft != ie.draft || draftRev != 1 || ing != claim || last != 1 {
		t.Fatalf("operation %s %s owner %s gen %d epoch %s/%s lease %v/%v draft %s@%d ingestion %s last %d",
			kind, opState, opOwner, opGen, opOEpoch, opEpoch, opLease, lease, draft, draftRev, ing, last)
	}
	var entry, evKind string
	if err := ie.db.QueryRow(`SELECT entry::text, kind FROM operation_event WHERE operation = $1 AND number = 1`, op).Scan(&entry, &evKind); err != nil {
		t.Fatal(err)
	}
	if entry != `{"type": "started"}` || evKind != "ingest" {
		t.Fatalf("event 1 %s %s", entry, evKind)
	}
	var recOp string
	if err := ie.db.QueryRow(`SELECT operation_id FROM idempotency_record WHERE key = $1`, key).Scan(&recOp); err != nil || recOp != op {
		t.Fatalf("record operation %q, %v", recOp, err)
	}
	if n := count(t, ie.db, `SELECT count(*) FROM act WHERE action = 'ingestion.start' AND $1 = ANY (subjects) AND $2 = ANY (subjects)`, op, claim); n != 1 {
		t.Fatalf("%d acts naming the operation and the claim", n)
	}
	if js := ie.runs(); len(js) != 1 || js[0].claim.ID != claim || js[0].claim.Gen != 1 || js[0].op != op || js[0].draftRev != 1 ||
		js[0].input.Size() == 0 {
		t.Fatalf("jobs %+v", js)
	}
	again := ie.do(ie.api, ingestCall(author, key, ie.etag, ie.body(nil)))
	if again.Code != http.StatusAccepted || again.Header().Get("Location") != loc || again.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay: %d %v %s", again.Code, again.Header(), again.Body)
	}
	if claims, ops := ie.rowCounts(t); claims != 1 || ops != 1 || len(ie.runs()) != 1 {
		t.Fatalf("after the replay: %d claims, %d operations, %d jobs", claims, ops, len(ie.runs()))
	}
}

// The refusals before and inside T11 write no claim and no operation, and start nothing.
func TestIngestionStartRefusals(t *testing.T) {
	ie := newIngestEnv(t, options{})
	author := ie.human("h-author")
	for i, tc := range []struct {
		name, body, ifMatch, token string
		status                     int
		code                       string
	}{
		{"automation", ie.body(nil), ie.etag, ie.robot, http.StatusForbidden, "forbidden"},
		{"machine source", ie.body(map[string]any{"source": "machine", "document": nil}), ie.etag, author, http.StatusBadRequest, "invalid-request"},
		{"drift kind", ie.body(map[string]any{"kind": "drift-adoption"}), ie.etag, author, http.StatusBadRequest, "invalid-request"},
		{"staging mode", ie.body(map[string]any{"staging": "disk"}), ie.etag, author, http.StatusBadRequest, "invalid-request"},
		{"bad mark", ie.body(map[string]any{"marks": []string{"~"}}), ie.etag, author, http.StatusBadRequest, "invalid-request"},
		{"declared references", ie.body(map[string]any{"declarations": map[string]any{"references": map[string]any{
			"s-a": map[string]any{"kind": "string", "version": 1}}}}), ie.etag, author, http.StatusBadRequest, "invalid-request"},
		{"no document", ie.body(map[string]any{"document": nil}), ie.etag, author, http.StatusBadRequest, "invalid-request"},
		{"machine id", ie.body(map[string]any{"machine": "m-1"}), ie.etag, author, http.StatusBadRequest, "invalid-request"},
		{"no if-match", ie.body(nil), "", author, http.StatusPreconditionRequired, "precondition-required"},
		{"stale draft", ie.body(nil), `"9-aaaaaaaaaaaaaaaaaaaaaaaaaa"`, author, http.StatusPreconditionFailed, "precondition-failed"},
		{"unknown draft", ie.body(map[string]any{"draft": "drf_aaaaaaaaaaaaaaaaaaaaaaaaaa"}), ie.etag, author, http.StatusNotFound, "not-found"},
		{"unknown machine", ie.body(map[string]any{"machine": "mch_aaaaaaaaaaaaaaaaaaaaaaaaaa"}), ie.etag, author, http.StatusNotFound, "not-found"},
		{"other cluster machine", ie.body(map[string]any{"machine": ie.otherMachine}), ie.etag, author, http.StatusUnprocessableEntity, "validation-failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := "k-refusal-" + strings.Repeat("0", 6) + string(rune('a'+i))
			wantProblem(t, ie.do(ie.api, ingestCall(tc.token, k, tc.ifMatch, tc.body)), tc.status, tc.code)
			if claims, ops := ie.rowCounts(t); claims != 0 || ops != 0 || len(ie.runs()) != 0 {
				t.Fatalf("%d claims, %d operations, %d jobs", claims, ops, len(ie.runs()))
			}
		})
	}
}

// A discarded draft takes no import, whatever its ETag; a server with a provider but no ingestion
// owner starts nothing.
func TestIngestionStartClosedDraftAndNoOwner(t *testing.T) {
	ie := newIngestEnv(t, options{})
	mustExec(t, ie.db, `UPDATE draft SET state = 'discarded' WHERE id = $1`, ie.draft)
	wantProblem(t, ie.do(ie.api, ingestCall(ie.human("h-author"), key, ie.etag, ie.body(nil))), http.StatusConflict, "conflict")
	mustExec(t, ie.db, `UPDATE draft SET state = 'open' WHERE id = $1`, ie.draft)
	ie.d.owner = staging.Owner{}
	ie.api = ie.build(options{})
	wantProblem(t, ie.do(ie.api, ingestCall(ie.human("h-author"), "k-no-owner-0123456789", ie.etag, ie.body(nil))),
		http.StatusServiceUnavailable, "dependency-unavailable")
	if claims, ops := ie.rowCounts(t); claims != 0 || ops != 0 {
		t.Fatalf("%d claims, %d operations", claims, ops)
	}
}

// PA §7.3: a second start of the same draft revision under another key is refused 409 while the
// first's operation is running, and the refusal names it.
func TestIngestionNaturalKey(t *testing.T) {
	ie := newIngestEnv(t, options{})
	author := ie.human("h-author")
	rec := ie.do(ie.api, ingestCall(author, key, ie.etag, ie.body(nil)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	op := strings.TrimPrefix(rec.Header().Get("Location"), prefix+"/operations/")
	doc := wantProblem(t, ie.do(ie.api, ingestCall(author, "k-second-0123456789", ie.etag, ie.body(nil))), http.StatusConflict, "conflict")
	if doc["operation"] != op {
		t.Fatalf("the refusal names %v; want %s", doc["operation"], op)
	}
	if claims, ops := ie.rowCounts(t); claims != 1 || ops != 1 {
		t.Fatalf("%d claims, %d operations", claims, ops)
	}
}

// Compilation §3.5: a claim a read treats as abandoned refuses nothing, sweep or no sweep. A start
// meeting the running operation of a due claim writes that claim abandoned and fails its
// operation, then starts; an encrypted claim whose lease lapsed is not due and still refuses.
func TestIngestionNaturalKeyAfterLapse(t *testing.T) {
	ie := newIngestEnv(t, options{})
	author := ie.human("h-author")
	start := func(k, mode string) *httptest.ResponseRecorder {
		return ie.do(ie.api, ingestCall(author, k, ie.etag, ie.body(map[string]any{"staging": mode})))
	}
	rec := start(key, "encrypted")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	first := strings.TrimPrefix(rec.Header().Get("Location"), prefix+"/operations/")
	lapse := `UPDATE staging_claim SET lease_until = now() - interval '1 second'`
	if _, err := ie.db.Exec(lapse); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, start("k-second-0123456789", "transient"), http.StatusConflict, "conflict") // control: not due
	if _, err := ie.db.Exec(`UPDATE staging_claim SET mode = 'transient'`); err != nil {
		t.Fatal(err)
	}
	if rec := start("k-third-01234567890", "transient"); rec.Code != http.StatusAccepted {
		t.Fatalf("a due claim's operation refused a start: %d %s", rec.Code, rec.Body)
	}
	var state, code string
	var payload sql.NullString
	var events int
	if err := ie.db.QueryRow(`SELECT o.state, o.error->>'type', c.payload::text,
		(SELECT count(*) FROM operation_event e WHERE e.operation = o.id AND e.entry->>'code' = 'ingestion-abandoned')
		FROM operation o JOIN staging_claim c ON c.id = o.ingestion WHERE o.id = $1 AND c.state = 'abandoned'`, first).
		Scan(&state, &code, &payload, &events); err != nil {
		t.Fatalf("the due claim is not written abandoned: %v", err)
	}
	if state != "failed" || code != "urn:bronzeward:problem:ingestion-abandoned" || payload.Valid || events != 1 {
		t.Fatalf("operation %s %s, payload kept %t, %d terminal events", state, code, payload.Valid, events)
	}
	if claims, ops := ie.rowCounts(t); claims != 2 || ops != 2 {
		t.Fatalf("%d claims, %d operations", claims, ops)
	}
}

// T1 locks its claim before the draft a start holds, so a start never waits for a due claim's
// row: one another transaction holds is refused 409 at once, not deadlocked.
func TestIngestionStartSkipsAHeldClaim(t *testing.T) {
	ie := newIngestEnv(t, options{})
	author := ie.human("h-author")
	if rec := ie.do(ie.api, ingestCall(author, key, ie.etag, ie.body(nil))); rec.Code != http.StatusAccepted {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if _, err := ie.db.Exec(`UPDATE staging_claim SET lease_until = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	holder, err := ie.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback() }()
	if _, err := holder.Exec(`SELECT 1 FROM staging_claim FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- ie.do(ie.api, ingestCall(author, "k-second-0123456789", ie.etag, ie.body(nil))) }()
	select {
	case rec := <-done:
		wantProblem(t, rec, http.StatusConflict, "conflict")
	case <-time.After(5 * time.Second):
		_ = holder.Rollback()
		<-done
		t.Fatal("the start waited for a claim another transaction holds")
	}
}

// PA §5.1: a process whose epoch is no longer current issues no ownership: 503, nothing written.
func TestIngestionStartEpochSuperseded(t *testing.T) {
	ie := newIngestEnv(t, options{})
	newEpoch(t, ie.db)
	wantProblem(t, ie.do(ie.api, ingestCall(ie.human("h-author"), key, ie.etag, ie.body(nil))), http.StatusServiceUnavailable, "epoch-superseded")
	if claims, ops := ie.rowCounts(t); claims != 0 || ops != 0 || len(ie.runs()) != 0 {
		t.Fatalf("%d claims, %d operations, %d jobs", claims, ops, len(ie.runs()))
	}
}

// T11 writes the claim and its operation together: a COMMIT refused leaves neither, no record,
// and starts no job.
func TestIngestionStartWritesTogether(t *testing.T) {
	ie := newIngestEnv(t, options{commit: func(tx *sql.Tx) error {
		_ = tx.Rollback()
		return &pgconn.PgError{Code: "23503", Message: "deferred foreign key violated at COMMIT (test)"}
	}})
	rec := ie.do(ie.api, ingestCall(ie.human("h-author"), key, ie.etag, ie.body(nil)))
	if rec.Code < http.StatusInternalServerError {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if claims, ops := ie.rowCounts(t); claims != 0 || ops != 0 || len(ie.runs()) != 0 ||
		count(t, ie.db, "SELECT count(*) FROM idempotency_record WHERE key = $1", key) != 0 {
		t.Fatalf("%d claims, %d operations, %d jobs", claims, ops, len(ie.runs()))
	}
}

// C §2.3: a document that is not a string is refused without echoing what it held.
func TestDecodeErrorNoEcho(t *testing.T) {
	ie := newIngestEnv(t, options{})
	canary := "bw-canary-" + rand.Text()
	rec := ie.do(ie.api, ingestCall(ie.human("h-author"), key, ie.etag, ie.body(map[string]any{"document": map[string]string{"token": canary}})))
	wantProblem(t, rec, http.StatusBadRequest, "invalid-request")
	if strings.Contains(rec.Body.String(), canary) || ie.logged(canary) {
		t.Fatal("the refusal echoed the document")
	}
}
