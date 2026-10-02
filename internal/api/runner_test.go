package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ginsys/bronzeward/internal/baotest"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos"
)

const (
	runToken = "bw-synthetic-1"
	runLabel = "bw-synthetic-label-2"
	// twoSecrets holds a schema secret and a marked label: two generations, in that order.
	twoSecrets = "machine:\n  token: " + runToken + "\n  nodeLabels:\n    tier: " + runLabel + "\n"
	labelMark  = "doc[0]/machine/nodeLabels/tier"
)

var generationPath = regexp.MustCompile(`^gen/cl_[a-z2-7]{26}/ing_[a-z2-7]{26}/[A-Za-z0-9_-]{1,128}$`)

// startJob starts an ingestion of ie's machine and draft at the draft's current ETag and returns
// its operation and the job handed to the runner.
func (ie *ingestEnv) startJob(t *testing.T, over map[string]any) (string, job) {
	t.Helper()
	ie.n++
	rec := ie.do(ie.api, ingestCall(ie.human("h-author"), fmt.Sprintf("k-run-%014d", ie.n), ie.currentETag(t), ie.body(over)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	ie.bodies = append(ie.bodies, rec.Body.String())
	js := ie.runs()
	if len(js) == 0 {
		t.Fatal("no job was handed to the runner")
	}
	return strings.TrimPrefix(rec.Header().Get("Location"), prefix+"/operations/"), js[len(js)-1]
}

// runWith runs j to its end in an API built with o, in the test's goroutine.
func (ie *ingestEnv) runWith(t *testing.T, o options, j job) {
	t.Helper()
	ie.build(o).runIngest(t.Context(), j)
}

func (ie *ingestEnv) currentETag(t *testing.T) string {
	t.Helper()
	var rev int
	var tok string
	if err := ie.db.QueryRow(`SELECT revision, etag_token FROM draft WHERE id = $1`, ie.draft).Scan(&rev, &tok); err != nil {
		t.Fatal(err)
	}
	return etag(rev, tok)
}

type opRow struct {
	state         string
	result, error map[string]any
	last          int
	owner         sql.NullString
	lease         sql.NullTime
}

func readOp(t *testing.T, db *sql.DB, op string) opRow {
	t.Helper()
	var r opRow
	var res, perr sql.NullString
	if err := db.QueryRow(`SELECT state, result::text, error::text, last_event, owner, lease_until FROM operation WHERE id = $1`, op).
		Scan(&r.state, &res, &perr, &r.last, &r.owner, &r.lease); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct {
		s   sql.NullString
		out *map[string]any
	}{{res, &r.result}, {perr, &r.error}} {
		if x.s.Valid {
			if err := json.Unmarshal([]byte(x.s.String), x.out); err != nil {
				t.Fatal(err)
			}
		}
	}
	return r
}

// events are op's event entries in number order; the numbers must run 1..n.
func events(t *testing.T, db *sql.DB, op string) []map[string]any {
	t.Helper()
	rows, err := db.Query(`SELECT number, kind, entry::text FROM operation_event WHERE operation = $1 ORDER BY number`, op)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var n int
		var kind, entry string
		if err := rows.Scan(&n, &kind, &entry); err != nil {
			t.Fatal(err)
		}
		if n != len(out)+1 || kind != "ingest" {
			t.Fatalf("event %d (want %d) of kind %s", n, len(out)+1, kind)
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(entry), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func eventTypes(evs []map[string]any) []string {
	var out []string
	for _, e := range evs {
		out = append(out, fmt.Sprint(e["type"]))
	}
	return out
}

func claimRow(t *testing.T, db *sql.DB, claim string) (state string, gen int64, payload bool) {
	t.Helper()
	if err := db.QueryRow(`SELECT state, owner_gen, payload IS NOT NULL FROM staging_claim WHERE id = $1`, claim).
		Scan(&state, &gen, &payload); err != nil {
		t.Fatal(err)
	}
	return state, gen, payload
}

// waitTerminal waits, at most 5 s, for op to leave running.
func waitTerminal(t *testing.T, db *sql.DB, op string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if readOp(t, db, op).state != "running" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("operation %s still running after 5 s", op)
}

// draftUnchanged asserts the draft is still at etag with no entry and no import base revision.
func (ie *ingestEnv) draftUnchanged(t *testing.T, etagBefore string) {
	t.Helper()
	if got := ie.currentETag(t); got != etagBefore {
		t.Errorf("the draft moved: %s, was %s", got, etagBefore)
	}
	if n := count(t, ie.db, `SELECT count(*) FROM draft_entry`) + count(t, ie.db, `SELECT count(*) FROM import_base_revision`) +
		count(t, ie.db, `SELECT count(*) FROM import_base_reference`); n != 0 {
		t.Errorf("%d draft entry, import base or reference rows", n)
	}
}

// wantFailed asserts op failed with code, its claim abandoned with no payload, and its events
// started, then failed naming the code; it returns the problem document.
func (ie *ingestEnv) wantFailed(t *testing.T, op, claim string, status int, code string) map[string]any {
	t.Helper()
	r := readOp(t, ie.db, op)
	if r.state != "failed" || r.result != nil || r.owner.Valid || r.lease.Valid {
		t.Fatalf("operation %+v", r)
	}
	if r.error["type"] != "urn:bronzeward:problem:"+code || r.error["status"] != float64(status) || r.error["title"] != titles[code] ||
		r.error["instance"] != op {
		t.Fatalf("error %v", r.error)
	}
	evs := events(t, ie.db, op)
	if !slices.Equal(eventTypes(evs), []string{"started", "failed"}) || evs[1]["code"] != code || r.last != 2 {
		t.Fatalf("events %v, last %d", evs, r.last)
	}
	if st, _, payload := claimRow(t, ie.db, claim); st != "abandoned" || payload {
		t.Fatalf("claim %s, payload %v", st, payload)
	}
	return r.error
}

// T1 (PA §5, C §2.3 steps 2-9): a transient import, run by the server's runner after T11's
// COMMIT, records the import base revision, its references and the draft entry, moves the draft
// to its next revision and token, succeeds the operation with its terminal event and releases
// the claim, all in one transaction.
func TestIngestTransientToDraft(t *testing.T) {
	ie := newRunningIngestEnv(t, options{})
	before := ie.currentETag(t)
	rec := ie.do(ie.api, ingestCall(ie.human("h-author"), key, before, ie.body(map[string]any{"document": twoSecrets,
		"marks": []string{labelMark}})))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	op := strings.TrimPrefix(rec.Header().Get("Location"), prefix+"/operations/")
	waitTerminal(t, ie.db, op)
	r := readOp(t, ie.db, op)
	var ibr, claim string
	if err := ie.db.QueryRow(`SELECT import_base_revision FROM draft_entry WHERE draft = $1 AND machine = $2 AND cluster = $3
		AND kind = 'import-base'`, ie.draft, ie.machine, ie.cluster).Scan(&ibr); err != nil {
		t.Fatal(err)
	}
	if err := ie.db.QueryRow(`SELECT ingestion FROM operation WHERE id = $1`, op).Scan(&claim); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"draft": ie.draft, "draftRevision": float64(2), "importBaseRevision": ibr}
	if r.state != "succeeded" || !reflect.DeepEqual(r.result, want) || r.error != nil || r.owner.Valid || r.lease.Valid || r.last != 2 {
		t.Fatalf("operation %+v", r)
	}
	evs := events(t, ie.db, op)
	if !slices.Equal(eventTypes(evs), []string{"started", "succeeded"}) || evs[1]["importBaseRevision"] != ibr || len(evs[1]) != 2 {
		t.Fatalf("events %v", evs)
	}
	if got := ie.currentETag(t); !strings.HasPrefix(got, `"2-`) || strings.TrimPrefix(got, `"2-`) == strings.TrimPrefix(before, `"1-`) {
		t.Fatalf("draft ETag %s, was %s", got, before)
	}
	if st, _, payload := claimRow(t, ie.db, claim); st != "released" || payload {
		t.Fatalf("claim %s, payload %v", st, payload)
	}
	var machine, doc, digestKey string
	var ct, digest, conf []byte
	if err := ie.db.QueryRow(`SELECT machine, document, baseline_ciphertext, baseline_digest, baseline_digest_key, configuration_digest
		FROM import_base_revision WHERE id = $1`, ibr).Scan(&machine, &doc, &ct, &digest, &digestKey, &conf); err != nil {
		t.Fatal(err)
	}
	in := []byte(twoSecrets)
	m := hmac.New(sha256.New, []byte("1"))
	m.Write(in)
	confWant := talos.ConfigurationDigest(in)
	if machine != ie.machine || strings.Contains(doc, runToken) || strings.Contains(doc, runLabel) || !strings.Contains(doc, "!bwref") ||
		string(ct) != string(fakeCiphertext("baseline", in)) || !hmac.Equal(digest, m.Sum(nil)) ||
		digestKey != (provider.Digest{Key: "bw-digest", Version: 1}).KeyRef() || string(conf) != string(confWant[:]) {
		t.Fatalf("import base revision: machine %s key %s document:\n%s", machine, digestKey, doc)
	}
	_, created := ie.f.paths()
	if len(created) != 2 {
		t.Fatalf("created %v", created)
	}
	rows, err := ie.db.Query(`SELECT name, kind, version, encoding IS NULL, generation FROM import_base_reference WHERE revision = $1`, ibr)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var gens []string
	for rows.Next() {
		var name, kind, gen string
		var version int
		var noEncoding bool
		if err := rows.Scan(&name, &kind, &version, &noEncoding, &gen); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(doc, "!bwref "+name) || kind != "string" || version != 1 || !noEncoding || !generationPath.MatchString(gen) ||
			!strings.Contains(gen, "/"+claim+"/") {
			t.Errorf("reference %s %s v%d %s", name, kind, version, gen)
		}
		gens = append(gens, gen)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(gens)
	slices.Sort(created)
	if !slices.Equal(gens, created) {
		t.Fatalf("references name %v; created %v", gens, created)
	}
}

// C §3: an encrypted claim stores the sealed envelope and its digest before T1, with the staged
// event, and T1 clears both.
func TestIngestEncryptedStoresPayloadThenClears(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j := ie.startJob(t, map[string]any{"staging": "encrypted"})
	var staged []byte
	var digest []byte
	var evs []map[string]any
	ie.runWith(t, options{afterStage: func() {
		if err := ie.db.QueryRow(`SELECT payload, payload_digest FROM staging_claim WHERE id = $1`, j.claim.ID).Scan(&staged, &digest); err != nil {
			t.Error(err)
		}
		evs = events(t, ie.db, op)
	}}, j)
	ie.f.mu.Lock()
	envelope := ie.f.envelope
	ie.f.mu.Unlock()
	sum := sha256.Sum256(envelope)
	if len(envelope) == 0 || string(staged) != string(fakeCiphertext("staging", envelope)) || string(digest) != string(sum[:]) ||
		strings.Contains(string(envelope), runToken) {
		t.Fatalf("payload %q digest %x; envelope %d bytes", staged, digest, len(envelope))
	}
	if !slices.Equal(eventTypes(evs), []string{"started", "staged"}) || len(evs[1]) != 1 {
		t.Fatalf("events at the stage: %v", evs)
	}
	if r := readOp(t, ie.db, op); r.state != "succeeded" || r.last != 3 {
		t.Fatalf("operation %+v", r)
	}
	if got := eventTypes(events(t, ie.db, op)); !slices.Equal(got, []string{"started", "staged", "succeeded"}) {
		t.Fatalf("events %v", got)
	}
	if st, _, payload := claimRow(t, ie.db, j.claim.ID); st != "released" || payload ||
		count(t, ie.db, `SELECT count(*) FROM staging_claim WHERE payload_digest IS NOT NULL`) != 0 {
		t.Fatalf("claim %s, payload %v", st, payload)
	}
}

// PA §5.1: a claim whose owner generation moved before T1 is not this owner's; T1 writes nothing
// and leaves the operation to the new generation.
func TestT1RefusedAfterGenBump(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j := ie.startJob(t, nil)
	before := ie.currentETag(t)
	ie.runWith(t, options{beforeT1: func() {
		mustExec(t, ie.db, `UPDATE staging_claim SET owner_gen = owner_gen + 1 WHERE id = $1`, j.claim.ID)
	}}, j)
	ie.draftUnchanged(t, before)
	if r := readOp(t, ie.db, op); r.state != "running" || r.last != 1 {
		t.Fatalf("operation %+v", r)
	}
	if st, gen, _ := claimRow(t, ie.db, j.claim.ID); st != "held" || gen != 2 {
		t.Fatalf("claim %s at generation %d", st, gen)
	}
}

// A failed COMMIT of T1 leaves the draft where it was and the claim held; its lease decides.
func TestCrashInsideT1LeavesDraft(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j := ie.startJob(t, nil)
	before := ie.currentETag(t)
	ie.runWith(t, options{commit: func(tx *sql.Tx) error {
		_ = tx.Rollback()
		return &pgconn.PgError{Code: "08006", Message: "connection lost at COMMIT (test)"}
	}}, j)
	ie.draftUnchanged(t, before)
	if r := readOp(t, ie.db, op); r.state != "running" || r.last != 1 {
		t.Fatalf("operation %+v", r)
	}
	if st, gen, _ := claimRow(t, ie.db, j.claim.ID); st != "held" || gen != 1 {
		t.Fatalf("claim %s at generation %d", st, gen)
	}
}

// PA §8.2, spec gap 3: a draft that moved while the import ran fails the operation 412 and
// abandons the claim; the draft keeps the other writer's revision.
func TestT1MovedDraftAbandons(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j := ie.startJob(t, nil)
	var moved string
	ie.runWith(t, options{beforeT1: func() {
		mustExec(t, ie.db, `UPDATE draft SET revision = revision + 1 WHERE id = $1`, ie.draft)
		moved = ie.currentETag(t)
	}}, j)
	ie.draftUnchanged(t, moved)
	ie.wantFailed(t, op, j.claim.ID, http.StatusPreconditionFailed, "precondition-failed")
}

// A draft discarded while the import ran takes no entry: the operation fails 409.
func TestT1ClosedDraftAbandons(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j := ie.startJob(t, nil)
	before := ie.currentETag(t)
	ie.runWith(t, options{beforeT1: func() {
		mustExec(t, ie.db, `UPDATE draft SET state = 'discarded' WHERE id = $1`, ie.draft)
	}}, j)
	ie.draftUnchanged(t, before)
	ie.wantFailed(t, op, j.claim.ID, http.StatusConflict, "conflict")
}

// T1 refuses a draft with a publish operation queued or running for it (persistence-api.md §5
// T1): the import would advance the revision the publication bound.
func TestT1ActivePublicationAbandons(t *testing.T) {
	for _, state := range []string{"queued", "running"} {
		t.Run(state, func(t *testing.T) {
			ie := newIngestEnv(t, options{})
			op, j := ie.startJob(t, nil)
			before := ie.currentETag(t)
			pub := id.New(id.Operation)
			ie.runWith(t, options{beforeT1: func() {
				owner := "NULL::text, 0, NULL::text, NULL::timestamptz"
				if state == "running" {
					owner = "'b/1/x', 1, epoch, now() + interval '1 minute'"
				}
				mustExec(t, ie.db, `INSERT INTO operation (id, kind, state, epoch, owner, owner_gen, owner_epoch, lease_until,
					draft, draft_revision, created_by, created_by_kind, created_role, created_at)
					SELECT $1, 'publish', $2, epoch, `+owner+`, draft, draft_revision, created_by, created_by_kind, 'publisher', now()
					FROM operation WHERE id = $3`, pub, state, op)
			}}, j)
			ie.draftUnchanged(t, before)
			if e := ie.wantFailed(t, op, j.claim.ID, http.StatusConflict, "conflict"); e["operation"] != pub {
				t.Fatalf("error %v names no publication %s", e, pub)
			}
		})
	}
}

// Spec gap 12: a second import of the machine replaces its draft entry.
func TestIngestReimportReplacesEntry(t *testing.T) {
	ie := newIngestEnv(t, options{})
	first, j := ie.startJob(t, nil)
	ie.runWith(t, options{}, j)
	second, j := ie.startJob(t, map[string]any{"document": twoSecrets, "marks": []string{labelMark}})
	ie.runWith(t, options{}, j)
	a, b := readOp(t, ie.db, first), readOp(t, ie.db, second)
	if a.state != "succeeded" || b.state != "succeeded" || b.result["draftRevision"] != float64(3) {
		t.Fatalf("operations %+v, %+v", a, b)
	}
	var ibr string
	if err := ie.db.QueryRow(`SELECT import_base_revision FROM draft_entry WHERE draft = $1`, ie.draft).Scan(&ibr); err != nil ||
		ibr != b.result["importBaseRevision"] || count(t, ie.db, `SELECT count(*) FROM draft_entry`) != 1 {
		t.Fatalf("entry %s, %v; want %v", ibr, err, b.result["importBaseRevision"])
	}
}

// The owner's abandonment is fenced: once the generation moved, a refusal writes nothing.
func TestAbandonRefusedAfterGenBump(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j := ie.startJob(t, nil)
	ie.f.onCreate = func(context.Context, int) error {
		mustExec(t, ie.db, `UPDATE staging_claim SET owner_gen = owner_gen + 1 WHERE id = $1`, j.claim.ID)
		return provider.ErrUnavailable
	}
	ie.runWith(t, options{}, j)
	if r := readOp(t, ie.db, op); r.state != "running" || r.last != 1 {
		t.Fatalf("operation %+v", r)
	}
	if st, gen, _ := claimRow(t, ie.db, j.claim.ID); st != "held" || gen != 2 {
		t.Fatalf("claim %s at generation %d", st, gen)
	}
}

// C §2.3 step 5, PA §9.4: a guard refusal abandons the claim, fails the operation 422 with the
// rule and paths and no value, and creates no generation.
func TestRefusalAbandons(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j := ie.startJob(t, map[string]any{"document": "machine:\n  token: " + runToken + "\n  nodeLabels:\n    copy: " + runToken + "\n"})
	ie.runWith(t, options{}, j)
	doc := ie.wantFailed(t, op, j.claim.ID, http.StatusUnprocessableEntity, "validation-failed")
	if doc["rule"] != "guard-value" || !reflect.DeepEqual(doc["paths"], []any{"doc[0]/machine/nodeLabels/copy"}) {
		t.Fatalf("problem %v", doc)
	}
	if calls, _ := ie.f.paths(); calls != 0 {
		t.Fatalf("%d creates", calls)
	}
}

// C §2.3 step 6: a provider that fails part-way abandons the claim and fails the operation 503;
// the generation created first remains, unused.
func TestProviderUnavailableAbandons(t *testing.T) {
	ie := newIngestEnv(t, options{})
	ie.f.onCreate = func(_ context.Context, n int) error {
		if n == 2 {
			return fmt.Errorf("sealed: %w", provider.ErrUnavailable)
		}
		return nil
	}
	op, j := ie.startJob(t, map[string]any{"document": twoSecrets, "marks": []string{labelMark}})
	before := ie.currentETag(t)
	ie.runWith(t, options{}, j)
	ie.wantFailed(t, op, j.claim.ID, http.StatusServiceUnavailable, "dependency-unavailable")
	ie.draftUnchanged(t, before)
	if calls, created := ie.f.paths(); calls != 2 || len(created) != 1 {
		t.Fatalf("%d creates, created %v", calls, created)
	}
}

// PA §5.1: a heartbeat refused because the generation moved stops the runner: the create in
// flight is cancelled, no further create runs, and nothing is written.
func TestHeartbeatLossStopsRunner(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j := ie.startJob(t, map[string]any{"document": twoSecrets, "marks": []string{labelMark}})
	ie.f.onCreate = func(ctx context.Context, n int) error {
		if n == 1 {
			mustExec(t, ie.db, `UPDATE staging_claim SET owner_gen = owner_gen + 1 WHERE id = $1`, j.claim.ID)
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
	if calls, created := ie.f.paths(); calls != 1 || len(created) != 0 {
		t.Fatalf("%d creates, created %v", calls, created)
	}
	ie.draftUnchanged(t, before)
	if r := readOp(t, ie.db, op); r.state != "running" || r.last != 1 {
		t.Fatalf("operation %+v", r)
	}
	if st, gen, _ := claimRow(t, ie.db, j.claim.ID); st != "held" || gen != 2 {
		t.Fatalf("claim %s at generation %d", st, gen)
	}
	if !ie.logged("stopped before the draft transaction") || ie.logged(" fails ") {
		t.Fatal("the stopped run went on to an outcome")
	}
}

// C §13: no refusal echoes the input, in a response, a stored row or the log.
func TestNoEchoOfInput(t *testing.T) {
	ie := newIngestEnv(t, options{})
	canary := "bw-canary-" + strings.ToLower(rand.Text())
	for _, tc := range []struct {
		name string
		over map[string]any
	}{
		{"unparsable", map[string]any{"document": "machine: [" + canary + "\n"}},
		{"guard", map[string]any{"document": "machine:\n  token: " + canary + "\n  nodeLabels:\n    copy: x" + canary + "\n"}},
		{"mark addresses nothing", map[string]any{"document": "machine:\n  token: " + canary + "\n", "marks": []string{"doc[0]/machine/nope"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			op, j := ie.startJob(t, tc.over)
			ie.runWith(t, options{}, j)
			if r := readOp(t, ie.db, op); r.state != "failed" || r.error["status"] != float64(http.StatusUnprocessableEntity) {
				t.Fatalf("operation %+v", r)
			}
			assertAbsent(t, ie, canary)
		})
	}
}

// assertAbsent scans every response body ie recorded, its log and every row of every table in
// the schema for s.
func assertAbsent(t *testing.T, ie *ingestEnv, s string) {
	t.Helper()
	for _, b := range ie.bodies {
		if strings.Contains(b, s) {
			t.Errorf("a response body holds the input: %s", b)
		}
	}
	if ie.logged(s) {
		t.Error("the log holds the input")
	}
	rows, err := ie.db.Query(`SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	rows.Close()
	if len(tables) < 10 {
		t.Fatalf("scanned only %d tables", len(tables))
	}
	for _, tb := range tables {
		if n := count(t, ie.db, `SELECT count(*) FROM "`+tb+`" AS r WHERE r::text LIKE '%' || $1 || '%'`, s); n != 0 {
			t.Errorf("%d rows of %s hold the input", n, tb)
		}
	}
}

// Keys holding "/", "~" and ".", a whole marked URL carrying credentials and a second document:
// every value is extracted, the stored stream holds none, and each has one reference row.
func TestIngestOddKeysAndURL(t *testing.T) {
	ie := newIngestEnv(t, options{})
	const (
		odd     = "bw-synthetic-odd-3"
		inURL   = "bw-synthetic-url-4"
		wgPriv  = "c3ludGhldGljLXByaXZhdGUta2V5LWZvci10ZXN0cyE="
		wgPub   = "c3ludGhldGljLXB1YmxpYy1rZXktZm9yLXRlc3RzISE="
		wgShare = "c3ludGhldGljLXByZXNoYXJlZC1rZXktZm9yLXRlc3Q="
	)
	doc := "machine:\n  token: " + runToken + "\n  nodeLabels:\n    a/b~c.d: " + odd + "\n  kubelet:\n    extraArgs:\n" +
		"      u: https://u:" + inURL + "@h.example.test/\n---\napiVersion: v1alpha1\nkind: WireguardConfig\nname: wg0\nprivateKey: " +
		wgPriv + "\npeers:\n  - publicKey: " + wgPub + "\n    presharedKey: " + wgShare + "\n    allowedIPs: [10.0.0.0/8]\n"
	op, j := ie.startJob(t, map[string]any{"document": doc,
		"marks": []string{"doc[0]/machine/nodeLabels/a~1b~0c.d", "doc[0]/machine/kubelet/extraArgs/u"}})
	ie.runWith(t, options{}, j)
	r := readOp(t, ie.db, op)
	if r.state != "succeeded" {
		t.Fatalf("operation %+v", r)
	}
	ibr := fmt.Sprint(r.result["importBaseRevision"])
	var stored string
	if err := ie.db.QueryRow(`SELECT document FROM import_base_revision WHERE id = $1`, ibr).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{runToken, odd, inURL, wgPriv, wgShare} {
		if strings.Contains(stored, s) {
			t.Errorf("the stored stream holds a value:\n%s", stored)
		}
	}
	if !strings.Contains(stored, wgPub) || !strings.Contains(stored, "a/b~c.d") {
		t.Errorf("the stored stream lost what it keeps:\n%s", stored)
	}
	_, created := ie.f.paths()
	if n := count(t, ie.db, `SELECT count(*) FROM import_base_reference WHERE revision = $1 AND generation = ANY ($2)`, ibr, created); len(created) != 5 || n != 5 {
		t.Fatalf("%d of %d created generations referenced", n, len(created))
	}
}

// The runner against the fixture's OpenBao, as the ingestion identity: the generations exist at
// version 1 and the baseline digest is the provider's.
func TestIngestLiveBao(t *testing.T) {
	b := baotest.New(t)
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(b.Token("bw-ingestion")), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := provider.ReadTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ing, err := provider.NewIngestion(b.Addr, tok, provider.Keys{Baseline: "bw-baseline", Staging: "bw-staging", Digest: "bw-digest"})
	if err != nil {
		t.Fatal(err)
	}
	ie := newIngestEnv(t, options{})
	ie.d.ing = ing
	ie.api = ie.build(ie.captured)
	op, j := ie.startJob(t, map[string]any{"document": twoSecrets, "marks": []string{labelMark}, "staging": "encrypted"})
	ie.runWith(t, options{}, j)
	r := readOp(t, ie.db, op)
	if r.state != "succeeded" {
		t.Fatalf("operation %+v", r)
	}
	var key string
	var digest []byte
	if err := ie.db.QueryRow(`SELECT baseline_digest_key, baseline_digest FROM import_base_revision WHERE id = $1`,
		r.result["importBaseRevision"]).Scan(&key, &digest); err != nil {
		t.Fatal(err)
	}
	d, err := ing.Digest(t.Context(), []byte(twoSecrets), 0)
	if err != nil || key != d.KeyRef() || !hmac.Equal(digest, d.Sum[:]) {
		t.Fatalf("baseline digest key %s, %v", key, err)
	}
	rows, err := ie.db.Query(`SELECT generation FROM import_base_reference WHERE revision = $1`, r.result["importBaseRevision"])
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var gen string
		if err := rows.Scan(&gen); err != nil {
			t.Fatal(err)
		}
		status, body, err := b.Do(b.Admin(), http.MethodGet, "/v1/secret/metadata/"+gen, nil)
		if err != nil || status != http.StatusOK || !strings.Contains(string(body), `"current_version":1`) {
			t.Errorf("%s: %d %v", gen, status, err)
		}
		n++
	}
	if err := rows.Err(); err != nil || n != 2 {
		t.Fatalf("%d references, %v", n, err)
	}
}
