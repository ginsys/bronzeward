package api

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/dbtest"
	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/staging"
)

const (
	// markSentinel is a value the first run leaves unmarked; the operator's mark extracts it.
	markSentinel = "bw-mark-sentinel-7c1d4e"
	zoneMark     = "doc[0]/machine/nodeLabels/zone"
)

// reviewedMarkable is twoSecrets with a third, unmarked value, reviewed: the first run creates
// two generations and pauses with the sentinel in the staged text.
var reviewedMarkable = twoSecrets + "    zone: " + markSentinel + "\n    rack: bw-mark-second-9a3f2b\n"

func markCall(tok, k, claim string, marks ...string) call {
	return call{method: "POST", path: prefix + "/ingestions/" + claim + "/marks", token: tok, key: k,
		body: `{"marks":["` + strings.Join(marks, `","`) + `"]}`}
}

// pausedForMark starts a reviewed import of reviewedMarkable and runs it to its pause.
func (ie *ingestEnv) pausedForMark(t *testing.T) (string, job, pausedRow) {
	t.Helper()
	return ie.pausedWith(t, map[string]any{"document": reviewedMarkable, "marks": []string{labelMark}})
}

// pausedWith starts a reviewed, encrypted import with over and runs it to its pause.
func (ie *ingestEnv) pausedWith(t *testing.T, over map[string]any) (string, job, pausedRow) {
	t.Helper()
	over["staging"], over["review"] = "encrypted", true
	op, j := ie.startJob(t, over)
	ie.runWith(t, options{}, j)
	r := ie.pausedRow(t, j.claim.ID)
	if r.state != "paused" || r.gen != 1 {
		t.Fatalf("first run: claim %+v", r)
	}
	return op, j, r
}

// mark posts marks on claim and returns the job handed to the runner.
func (ie *ingestEnv) mark(t *testing.T, k, claim, op string, gen int64, marks ...string) job {
	t.Helper()
	before := len(ie.runs())
	rec := ie.do(ie.api, markCall(ie.human("h-author"), k, claim, marks...))
	ie.bodies = append(ie.bodies, rec.Body.String())
	b := decode[map[string]any](t, rec, http.StatusAccepted)
	want := map[string]any{"operation": op, "ingestion": claim, "generation": float64(gen)}
	if fmt.Sprint(b) != fmt.Sprint(want) || rec.Header().Get("Location") != prefix+"/operations/"+op {
		t.Fatalf("body %v, Location %q", b, rec.Header().Get("Location"))
	}
	js := ie.runs()
	if len(js) != before+1 {
		t.Fatalf("%d jobs handed over, want one more than %d", len(js), before)
	}
	j := js[len(js)-1]
	if j.claim.ID != claim || j.claim.Gen != gen || j.claim.Review != "pending" || j.op != op || j.resume == nil || len(j.marks) != len(marks) {
		t.Fatalf("job %+v", j)
	}
	return j
}

// opened decrypts and opens claim's stored envelope.
func (ie *ingestEnv) opened(t *testing.T, r pausedRow) ingest.Staged {
	t.Helper()
	plain, err := ie.f.DecryptStaging(t.Context(), provider.Ciphertext(r.payload))
	if err != nil {
		t.Fatal(err)
	}
	st, err := ingest.Open(plain, [32]byte(r.digest))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// Compilation §3.6 item 3, PA §9.3/§8.3: a mark takes the paused claim at the next owner
// generation (202 naming it, no mark path in the body) with the marked event; its run extracts
// the sentinel to a new generation under the same claim and pauses again with a new envelope,
// the earlier generations and the baseline carried over unchanged. Controls: the body equal to
// exactly its three members (an echo of the marks fails it); the new envelope compared with the
// first.
func TestMarkRepausesWithNewEnvelope(t *testing.T) {
	ie := newIngestEnv(t, options{})
	before := ie.currentETag(t)
	op, j, first := ie.pausedForMark(t)
	old := ie.opened(t, first)
	if !strings.Contains(string(old.Sanitized.Documents()), markSentinel) {
		t.Fatal("control: the first run's staged text does not hold the sentinel")
	}
	mj := ie.mark(t, "k-mark-repause-0123", j.claim.ID, op, 2, zoneMark)
	if r := ie.pausedRow(t, j.claim.ID); r.state != "held" || r.gen != 2 || r.opGen != 2 {
		t.Fatalf("after the mark: claim %+v", r)
	}
	ie.runWith(t, options{beforeT1: func() { t.Error("the mark's run reached the draft transaction") }}, mj)
	ie.draftUnchanged(t, before)
	r := ie.pausedRow(t, j.claim.ID)
	if r.state != "paused" || r.gen != 2 || string(r.digest) == string(first.digest) || r.lease.After(r.pausedAt) || r.opState != "running" {
		t.Fatalf("claim %+v", r)
	}
	st := ie.opened(t, r)
	if doc := string(st.Sanitized.Documents()); strings.Contains(doc, markSentinel) || !strings.Contains(doc, "zone: !bwref") {
		t.Fatalf("staged document %q", doc)
	}
	if len(st.Generations) != 3 || st.Baseline.Digest != old.Baseline.Digest || st.Baseline.Configuration != old.Baseline.Configuration ||
		string(st.Baseline.Ciphertext) != string(old.Baseline.Ciphertext) || st.Baseline.DigestKey != old.Baseline.DigestKey {
		t.Fatalf("generations %v, baseline carried over: %v", st.Generations, st.Baseline.Digest == old.Baseline.Digest)
	}
	for name, g := range old.Generations {
		if st.Generations[name] != g {
			t.Fatalf("generation %s moved: %s -> %s", name, g, st.Generations[name])
		}
	}
	if calls, created := ie.f.paths(); calls != 3 || len(created) != 3 {
		t.Fatalf("%d creates, %d created", calls, len(created))
	}
	evs := events(t, ie.db, op)
	if !slices.Equal(eventTypes(evs), []string{"started", "staged", "paused", "marked", "paused"}) ||
		evs[3]["generation"] != float64(2) || evs[3]["paths"] != float64(1) || len(evs[3]) != 3 ||
		evs[4]["generation"] != float64(2) || len(evs[4]) != 2 {
		t.Fatalf("events %v", evs)
	}
	for _, b := range append(ie.bodies, fmt.Sprint(evs)) {
		if strings.Contains(b, markSentinel) || strings.Contains(b, zoneMark) {
			t.Fatalf("a body or event holds the mark or the sentinel: %s", b)
		}
	}
	if ie.logged(markSentinel) || ie.logged(zoneMark) {
		t.Fatal("the log holds the mark or the sentinel")
	}
}

// PA §16, compilation §3.6 items 3-4: a mark on an import carries the baseline ciphertext and
// both its digests over to its new envelope, and the continuation's import base revision records
// them, its document holding the mark's reference and not the sentinel; across the pause, the
// mark and the continuation the sentinel is in no body, log or row. Control: the revision's
// columns are compared with the first envelope's baseline, so a rebuilt envelope that dropped or
// recomputed them fails.
func TestMarkThenContinuationRecordsBaseline(t *testing.T) {
	ie := newIngestEnv(t, options{})
	before := ie.currentETag(t)
	op, j, first := ie.pausedForMark(t)
	old := ie.opened(t, first)
	ie.runWith(t, options{}, ie.mark(t, "k-mark-baseline-012", j.claim.ID, op, 2, zoneMark))
	if r := ie.pausedRow(t, j.claim.ID); r.state != "paused" || r.gen != 2 {
		t.Fatalf("after the mark: claim %+v", r)
	}
	cj := ie.continueReview(t, "k-continue-baseline", j.claim.ID, op, 3)
	ie.runWith(t, options{}, cj)
	ie.wantContinuedSucceeded(t, op, j.claim.ID, before, "started", "staged", "paused", "marked", "paused", "continued", "succeeded")
	var doc, key string
	var ct, digest, conf []byte
	var refs int
	if err := ie.db.QueryRow(`SELECT r.document, r.baseline_ciphertext, r.baseline_digest, r.baseline_digest_key, r.configuration_digest,
			(SELECT count(*) FROM import_base_reference WHERE revision = r.id)
		FROM import_base_revision r WHERE r.machine = $1 ORDER BY r.created_at DESC LIMIT 1`, ie.machine).
		Scan(&doc, &ct, &digest, &key, &conf, &refs); err != nil {
		t.Fatal(err)
	}
	// Failure messages say which column differs, never its bytes or the document.
	ctOK, digestOK := string(ct) == string(old.Baseline.Ciphertext), fmt.Sprintf("%x", digest) == fmt.Sprintf("%x", old.Baseline.Digest)
	keyOK, confOK := key == old.Baseline.DigestKey, fmt.Sprintf("%x", conf) == fmt.Sprintf("%x", old.Baseline.Configuration)
	if !ctOK || !digestOK || !keyOK || !confOK {
		t.Fatalf("import base baseline equals the first envelope's: ciphertext %v, digest %v, digest key %v, configuration %v",
			ctOK, digestOK, keyOK, confOK)
	}
	if sentinel, ref := strings.Contains(doc, markSentinel), strings.Contains(doc, "zone: !bwref"); sentinel || !ref || refs != 3 {
		t.Fatalf("import base document holds the sentinel %v, the zone reference %v; %d references", sentinel, ref, refs)
	}
	assertAbsent(t, ie, markSentinel)
}

// Compilation §3.6 item 4, PA §8.3: a mark refused before any provider write returns the claim to
// paused with its earlier envelope and digest, with the mark-refused event naming the rule and
// the mark's position, never a path. The second mark addresses no node, so its position is 1.
// Control: an abandonment on every refusal fails the state check.
func TestMarkRefusedReturnsPaused(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j, first := ie.pausedForMark(t)
	const absent = "doc[0]/machine/nodeLabels/" + markSentinel
	mj := ie.mark(t, "k-mark-refused-0123", j.claim.ID, op, 2, zoneMark, absent)
	ie.runWith(t, options{}, mj)
	r := ie.pausedRow(t, j.claim.ID)
	if r.state != "paused" || r.gen != 2 || string(r.payload) != string(first.payload) || string(r.digest) != string(first.digest) ||
		r.opState != "running" {
		t.Fatalf("claim %+v", r)
	}
	if calls, _ := ie.f.paths(); calls != 2 {
		t.Fatalf("%d creates: the refused mark wrote to the provider", calls)
	}
	evs := events(t, ie.db, op)
	if !slices.Equal(eventTypes(evs), []string{"started", "staged", "paused", "marked", "mark-refused", "paused"}) {
		t.Fatalf("events %v", eventTypes(evs))
	}
	ref := evs[4]
	p, _ := ref["problem"].(map[string]any)
	if ref["generation"] != float64(2) || p["rule"] != string(ingest.RuleMarkUnaddressed) || p["position"] != float64(1) ||
		p["type"] != "urn:bronzeward:problem:validation-failed" || p["status"] != float64(http.StatusUnprocessableEntity) {
		t.Fatalf("mark-refused generation %v rule %v position %v type %v status %v",
			ref["generation"], p["rule"], p["position"], p["type"], p["status"])
	}
	if _, ok := p["paths"]; ok {
		t.Fatal("the problem names paths")
	}
	if strings.Contains(fmt.Sprint(evs), markSentinel) || ie.logged(markSentinel) {
		t.Fatal("the refused path is in an event or the log")
	}
}

// PA §16, compilation §3.6 items 3-4: a mark on a mapping holding an earlier reference, or on the
// string of an embedded document holding one, is refused mark-kind, and a mark inside an embedded
// JSON document whose re-encoding would assemble an earlier value from separate scalars is
// refused mark-rewrites-text: each before any provider write, the claim paused with its earlier
// envelope and digest and a mark-refused event naming the rule and position. The ingest package's
// remark tests hold each rule's control; here a run that wrote first, or abandoned, fails.
func TestMarkCompilationRefusalsReturnPaused(t *testing.T) {
	const manifest = "doc[0]/cluster/inlineManifests/0/contents"
	embedded := func(format string) map[string]any {
		return map[string]any{"embedded": []map[string]string{{"path": manifest, "format": format}}}
	}
	for _, c := range []struct {
		name string
		over map[string]any
		mark string
		rule ingest.Rule
	}{
		{"mapping holding a reference", map[string]any{"document": reviewedMarkable, "marks": []string{labelMark}},
			"doc[0]/machine/nodeLabels", ingest.RuleMarkKind},
		{"embedded document holding a reference", map[string]any{
			"document": "machine:\n  token: " + runToken + "\ncluster:\n  inlineManifests:\n    - name: s\n      contents: |\n" +
				"        kind: Secret\n        stringData:\n            password: " + markSentinel + "\n            user: admin\n",
			"marks": []string{manifest + "|yaml/stringData/password"}, "declarations": embedded("yaml")},
			manifest, ingest.RuleMarkKind},
		{"embedded JSON re-encoded", map[string]any{
			"document": "machine:\n  token: " + runToken + "\ncluster:\n  inlineManifests:\n    - name: s\n" +
				`      contents: '{"public": "visible", "password": "` + markSentinel + `"}'` + "\n",
			"marks": []string{}, "declarations": embedded("json")},
			manifest + "|json/password", ingest.RuleMarkRewritesText},
	} {
		t.Run(c.name, func(t *testing.T) {
			ie := newIngestEnv(t, options{})
			op, j, first := ie.pausedWith(t, c.over)
			made, _ := ie.f.paths()
			mj := ie.mark(t, "k-mark-compile-0123", j.claim.ID, op, 2, c.mark)
			ie.runWith(t, options{}, mj)
			r := ie.pausedRow(t, j.claim.ID)
			if r.state != "paused" || r.gen != 2 || string(r.payload) != string(first.payload) || string(r.digest) != string(first.digest) ||
				r.opState != "running" {
				t.Fatalf("claim %+v", r)
			}
			if calls, _ := ie.f.paths(); calls != made {
				t.Fatalf("%d creates after %d: the refused mark wrote to the provider", calls, made)
			}
			evs := events(t, ie.db, op)
			if !slices.Equal(eventTypes(evs), []string{"started", "staged", "paused", "marked", "mark-refused", "paused"}) {
				t.Fatalf("events %v", eventTypes(evs))
			}
			if p, _ := evs[4]["problem"].(map[string]any); p["rule"] != string(c.rule) || p["position"] != float64(0) {
				t.Fatalf("mark-refused rule %v position %v", p["rule"], p["position"])
			}
			assertAbsent(t, ie, markSentinel)
		})
	}
}

// PA §16, compilation §3.6 item 4: a refused mark whose path holds a value an earlier mark
// extracted, and a guard hit at a staged path that spells a distinctive string, leave neither in
// the mark-refused event, its problem, a body, the log or a row: the refusal names its rule and
// the mark's position only. A staged key cannot equal an extracted value (the guard refuses it at
// extraction), so the guard row's path spells a string the run never extracted, which a refusal
// naming its path would leak alike. Control (mutation): a refusal that names its paths fails the
// scan.
func TestMarkRefusalNamesNoPath(t *testing.T) {
	t.Run("earlier extracted value in the path", func(t *testing.T) {
		ie := newIngestEnv(t, options{})
		op, j, _ := ie.pausedForMark(t)
		ie.runWith(t, options{}, ie.mark(t, "k-mark-extract-0123", j.claim.ID, op, 2, zoneMark))
		r := ie.pausedRow(t, j.claim.ID)
		if r.state != "paused" || !strings.Contains(string(ie.opened(t, r).Sanitized.Documents()), "zone: !bwref") {
			t.Fatalf("control: the first mark did not extract the sentinel: claim %+v", r)
		}
		spelled := "doc[0]/machine/nodeLabels/" + markSentinel
		ie.runWith(t, options{}, ie.mark(t, "k-mark-spelled-0123", j.claim.ID, op, 3, spelled))
		ie.wantMarkRefused(t, op, j.claim.ID, ingest.RuleMarkUnaddressed, "marked", "paused", "marked", "mark-refused", "paused")
		assertAbsent(t, ie, markSentinel)
	})
	t.Run("guard hit at a path spelling a string", func(t *testing.T) {
		const spelling, repeated = "bw-guard-path-6e0f21", "bw-mark-repeated-41c9a7"
		ie := newIngestEnv(t, options{})
		op, j, _ := ie.pausedWith(t, map[string]any{"marks": []string{labelMark},
			"document": twoSecrets + "    zone: " + repeated + "\n    " + spelling + ": " + repeated + "\n"})
		ie.runWith(t, options{}, ie.mark(t, "k-mark-guard-012345", j.claim.ID, op, 2, zoneMark))
		ie.wantMarkRefused(t, op, j.claim.ID, ingest.RuleGuardValue, "marked", "mark-refused", "paused")
		assertAbsent(t, ie, spelling)
		assertAbsent(t, ie, repeated)
	})
}

// wantMarkRefused checks claim paused with op's events after the first pause equal to tail, the
// last mark-refused naming rule at position 0 and no paths. Its failure messages print event types,
// the rule and the position only: a path the refusal names may spell the value it guards.
func (ie *ingestEnv) wantMarkRefused(t *testing.T, op, claim string, rule ingest.Rule, tail ...string) {
	t.Helper()
	if r := ie.pausedRow(t, claim); r.state != "paused" || r.opState != "running" {
		t.Fatalf("claim %v", r)
	}
	evs := events(t, ie.db, op)
	if !slices.Equal(eventTypes(evs), append([]string{"started", "staged", "paused"}, tail...)) {
		t.Fatalf("events %v", eventTypes(evs))
	}
	p, _ := evs[len(evs)-2]["problem"].(map[string]any)
	if p["rule"] != string(rule) || p["position"] != float64(0) {
		t.Fatalf("mark-refused rule %v position %v", p["rule"], p["position"])
	}
	if _, ok := p["paths"]; ok {
		t.Fatal("the problem names paths")
	}
}

// Compilation §3.6 item 4: a provider failure on the mark's first generation is refused as a
// refusal before any provider write: paused with the earlier envelope and a mark-refused event.
// Control: an abandonment on every provider failure fails the state check.
func TestMarkFirstGenerationFailureReturnsPaused(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j, first := ie.pausedForMark(t)
	mj := ie.mark(t, "k-mark-provider-012", j.claim.ID, op, 2, zoneMark)
	ie.f.onCreate = func(_ context.Context, n int) error {
		if n == 3 {
			return fmt.Errorf("create: %w", provider.ErrUnavailable)
		}
		return nil
	}
	ie.runWith(t, options{}, mj)
	r := ie.pausedRow(t, j.claim.ID)
	if r.state != "paused" || r.gen != 2 || string(r.digest) != string(first.digest) || string(r.payload) != string(first.payload) {
		t.Fatalf("claim %+v", r)
	}
	evs := events(t, ie.db, op)
	if !slices.Equal(eventTypes(evs), []string{"started", "staged", "paused", "marked", "mark-refused", "paused"}) {
		t.Fatalf("events %v", evs)
	}
	if p, _ := evs[4]["problem"].(map[string]any); p["type"] != "urn:bronzeward:problem:dependency-unavailable" {
		t.Fatalf("mark-refused %v", evs[4])
	}
}

// Compilation §3.6 item 4: a mark whose provider write fails after one generation abandons the
// claim with that refusal; its operation fails.
func TestMarkPartWayFailureAbandons(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j, _ := ie.pausedForMark(t)
	mj := ie.mark(t, "k-mark-partway-0123", j.claim.ID, op, 2, zoneMark, "doc[0]/machine/nodeLabels/rack")
	ie.f.onCreate = func(_ context.Context, n int) error {
		if n == 4 {
			return fmt.Errorf("create: %w", provider.ErrUnavailable)
		}
		return nil
	}
	ie.runWith(t, options{}, mj)
	if st, _, payload := claimRow(t, ie.db, j.claim.ID); st != "abandoned" || payload {
		t.Fatalf("claim %s, payload %v", st, payload)
	}
	if r := readOp(t, ie.db, op); r.state != "failed" || r.error["type"] != "urn:bronzeward:problem:dependency-unavailable" {
		t.Fatalf("operation %+v", r)
	}
	if calls, created := ie.f.paths(); calls != 4 || len(created) != 3 {
		t.Fatalf("%d creates, %d created", calls, len(created))
	}
}

// PA §9.3: a mark on a claim not paused, past its absolute expiry, of an earlier epoch or that
// does not exist is refused 409 (404 for none) and nothing changes. Control: the same claim,
// paused and unexpired, is taken.
func TestMarkRefusals(t *testing.T) {
	ie := newIngestEnv(t, options{})
	author := ie.human("h-author")
	op, j, first := ie.pausedForMark(t)
	unchanged := func(label string) {
		t.Helper()
		if r := ie.pausedRow(t, j.claim.ID); r.gen != first.gen || r.state != first.state || string(r.digest) != string(first.digest) {
			t.Fatalf("%s: claim %+v", label, r)
		}
		if n := len(events(t, ie.db, op)); n != 3 {
			t.Fatalf("%s: %d events", label, n)
		}
	}
	wantProblem(t, ie.do(ie.api, markCall(author, "k-mark-none-012345", "ing_aaaaaaaaaaaaaaaaaaaaaaaaaa", zoneMark)),
		http.StatusNotFound, "not-found")
	mustExec(t, ie.db, `UPDATE staging_claim SET state = 'held' WHERE id = $1`, j.claim.ID)
	if p := wantProblem(t, ie.do(ie.api, markCall(author, "k-mark-held-0123456", j.claim.ID, zoneMark)), http.StatusConflict, "conflict"); p["detail"] != "the ingestion is not paused for the operator's review" {
		t.Fatalf("held: %v", p)
	}
	var expires time.Time
	if err := ie.db.QueryRow(`SELECT expires_at FROM staging_claim WHERE id = $1`, j.claim.ID).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	mustExec(t, ie.db, `UPDATE staging_claim SET state = 'paused', expires_at = clock_timestamp() WHERE id = $1`, j.claim.ID)
	if p := wantProblem(t, ie.do(ie.api, markCall(author, "k-mark-expired-01234", j.claim.ID, zoneMark)), http.StatusConflict, "conflict"); p["detail"] != "the ingestion has ended" {
		t.Fatalf("expired: %v", p)
	}
	mustExec(t, ie.db, `UPDATE staging_claim SET expires_at = $2 WHERE id = $1`, j.claim.ID, expires)
	unchanged("refused marks")
	if len(ie.runs()) != 1 {
		t.Fatalf("%d jobs: a refused mark started a run", len(ie.runs()))
	}
	ie.mark(t, "k-mark-control-0123", j.claim.ID, op, 2, zoneMark)
}

// PA §9.3, compilation §3.5: after a recovery-mode entry, a process of the earlier epoch issues no
// mark (503 epoch-superseded), and a process of the current epoch is refused the claim of the
// earlier one (409); the claim is unchanged.
func TestMarkEarlierEpoch(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j, first := ie.pausedForMark(t)
	newEpoch(t, ie.db)
	author := ie.human("h-author")
	wantProblem(t, ie.do(ie.api, markCall(author, "k-mark-superseded-0", j.claim.ID, zoneMark)), http.StatusServiceUnavailable, "epoch-superseded")
	d := ie.d
	d.owner = staging.Owner{ID: "c/3/" + rand.Text(), Epoch: epoch(t, ie.db)}
	if p := wantProblem(t, ie.do(ie.buildWith(d, options{}), markCall(author, "k-mark-epoch-0123456", j.claim.ID, zoneMark)),
		http.StatusConflict, "conflict"); p["detail"] != "the claim predates the current epoch" {
		t.Fatalf("problem %v", p)
	}
	if r := ie.pausedRow(t, j.claim.ID); r.state != "paused" || r.gen != first.gen || len(events(t, ie.db, op)) != 3 {
		t.Fatalf("claim %+v", r)
	}
}

// PA §16: two marks racing one paused claim, exactly one taking it.
func TestMarkRace(t *testing.T) {
	ie := newIngestEnv(t, options{})
	_, j, _ := ie.pausedForMark(t)
	author := ie.human("h-author")
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Go(func() {
			codes[i] = ie.do(ie.api, markCall(author, fmt.Sprintf("k-mark-race-%07d", i), j.claim.ID, zoneMark)).Code
		})
	}
	wg.Wait()
	slices.Sort(codes)
	if !slices.Equal(codes, []int{http.StatusAccepted, http.StatusConflict}) {
		t.Fatalf("codes %v", codes)
	}
	if r := ie.pausedRow(t, j.claim.ID); r.state != "held" || r.gen != 2 {
		t.Fatalf("claim %+v", r)
	}
}

// Compilation §3.6 item 6: a mark's run that stops before storing its new envelope leaves the
// claim held to its lease with the earlier envelope; a takeover then pauses again without the
// marks. Control: the taken job carries no marks, so a run that resumed them would create a
// generation.
func TestMarkInterruptedTakeoverPausesWithoutMarks(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j, first := ie.pausedForMark(t)
	mj := ie.mark(t, "k-mark-killed-01234", j.claim.ID, op, 2, zoneMark)
	ctx, cancel := context.WithCancel(t.Context())
	ie.f.onCreate = func(context.Context, int) error {
		cancel()
		return context.Canceled
	}
	ie.build(options{}).runIngest(ctx, mj)
	if r := ie.pausedRow(t, j.claim.ID); r.state != "held" || r.gen != 2 || string(r.digest) != string(first.digest) {
		t.Fatalf("after the stop: claim %+v", r)
	}
	// A stopped run is not a failed mark: nothing is recorded as a failed step.
	if ie.logged("generation create") {
		t.Fatal("the stop was handled as a failed generation create")
	}
	ie.f.onCreate = nil
	ie.lapse(t, j.claim.ID)
	tk := ie.newTaker(options{})
	tj := ie.takeOver(t, tk, "k-take-killed-mark1", j.claim.ID, op, 3)
	if tj.marks != nil {
		t.Fatalf("the taken job carries marks %v", tj.marks)
	}
	tk.runIngest(t.Context(), tj)
	r := ie.pausedRow(t, j.claim.ID)
	if r.state != "paused" || r.gen != 3 || string(r.digest) != string(first.digest) {
		t.Fatalf("claim %+v", r)
	}
	if calls, created := ie.f.paths(); calls != 3 || len(created) != 2 {
		t.Fatalf("%d creates, %d created", calls, len(created))
	}
	evs := events(t, ie.db, op)
	if !slices.Equal(eventTypes(evs), []string{"started", "staged", "paused", "marked", "taken-over", "paused"}) {
		t.Fatalf("events %v", evs)
	}
}

// A mark's run stopped while its new envelope is encrypted leaves the claim held with its earlier
// envelope and the operation running, for a takeover: the stop is no failed step.
func TestMarkStoppedAtEncryptionHolds(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j, first := ie.pausedForMark(t)
	mj := ie.mark(t, "k-mark-stop-enc-012", j.claim.ID, op, 2, zoneMark)
	ctx, cancel := context.WithCancel(t.Context())
	ie.f.onEncrypt = func(context.Context) error {
		cancel()
		return context.Canceled
	}
	ie.build(options{}).runIngest(ctx, mj)
	r := ie.pausedRow(t, j.claim.ID)
	if r.state != "held" || r.gen != 2 || string(r.digest) != string(first.digest) || r.opState != "running" {
		t.Fatalf("after the stop: claim %+v", r)
	}
	if ie.logged("envelope encryption") {
		t.Fatal("the stop was handled as a failed envelope encryption")
	}
	if evs := events(t, ie.db, op); !slices.Equal(eventTypes(evs), []string{"started", "staged", "paused", "marked"}) {
		t.Fatalf("events %v", evs)
	}
}

// Compilation §3.6 item 6, PA §8.3: a mark's run that cannot decrypt the envelope records
// resume-failed and leaves the claim held to its lease, the operation running.
func TestMarkDecryptFailureLeavesHeld(t *testing.T) {
	ie := newIngestEnv(t, options{})
	op, j, _ := ie.pausedForMark(t)
	mj := ie.mark(t, "k-mark-decrypt-0123", j.claim.ID, op, 2, zoneMark)
	ie.f.decryptErr = errors.New("fake: transit unavailable")
	ie.runWith(t, options{}, mj)
	if r := ie.pausedRow(t, j.claim.ID); r.state != "held" || r.gen != 2 || r.opState != "running" {
		t.Fatalf("claim %+v", r)
	}
	evs := events(t, ie.db, op)
	if !slices.Equal(eventTypes(evs), []string{"started", "staged", "paused", "marked", "resume-failed"}) {
		t.Fatalf("events %v", evs)
	}
}

// PA §16, compilation §3.1: a mark's run that finds the envelope not matching its digest, or a
// malformed envelope stored with its matching digest, abandons the claim and fails the operation
// 500 internal-error before any provider write. Control: pausing again instead leaves the claim
// paused and fails each case.
func TestMarkIntegrityFailureAbandons(t *testing.T) {
	for _, c := range envelopeCorruptions {
		t.Run(c.name, func(t *testing.T) {
			ie := newIngestEnv(t, options{})
			op, j, _ := ie.pausedForMark(t)
			ct, _ := ie.stagedPayload(t, j.claim.ID)
			c.corrupt(t, ie, j.claim.ID, ct)
			mj := ie.mark(t, "k-mark-integrity-01", j.claim.ID, op, 2, zoneMark)
			ie.runWith(t, options{}, mj)
			ie.wantIntegrityFailure(t, op, j.claim.ID, "marked")
			if calls, _ := ie.f.paths(); calls != 2 {
				t.Fatalf("%d creates: the mark wrote to the provider", calls)
			}
		})
	}
}

// A mark's new lease runs from after the act-order lock, its last wait, and a mark whose claim
// reaches its absolute expiry in that wait is refused as ended: nothing is taken and no run starts
// (PA §5 rules 4 and 5, compilation §3.6 item 3).
func TestMarkActOrderWait(t *testing.T) {
	t.Run("lease after the wait", func(t *testing.T) {
		ie := newIngestEnv(t, options{})
		op, j, _ := ie.pausedForMark(t)
		d := ie.d
		d.timers.Lease = time.Second
		b := ie.buildWith(d, options{onRunner: func(job) {}})
		lock := holdActOrder(t, ie.db)
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- ie.do(b, markCall(ie.human("h-author"), "k-mark-order-012345", j.claim.ID, zoneMark)) }()
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
		op, j, first := ie.pausedForMark(t)
		mustExec(t, ie.db, `UPDATE staging_claim SET expires_at = clock_timestamp() + interval '1 second' WHERE id = $1`, j.claim.ID)
		started := make(chan job, 1)
		b := ie.buildWith(ie.d, options{onRunner: func(j job) { started <- j }})
		lock := holdActOrder(t, ie.db)
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- ie.do(b, markCall(ie.human("h-author"), "k-mark-expiry-01234", j.claim.ID, zoneMark)) }()
		dbtest.WaitForLockWait(t, ie.db)
		time.Sleep(1200 * time.Millisecond)
		if err := lock.Rollback(); err != nil {
			t.Fatal(err)
		}
		if p := wantProblem(t, <-done, http.StatusConflict, "conflict"); p["detail"] != "the ingestion has ended" {
			t.Fatalf("problem %v", p)
		}
		if r := ie.pausedRow(t, j.claim.ID); r.state != "paused" || r.gen != first.gen || r.opGen != first.gen || len(events(t, ie.db, op)) != 3 {
			t.Fatalf("claim %+v after a refused mark", r)
		}
		select {
		case j := <-started:
			t.Fatalf("a refused mark started %+v", j)
		default:
		}
	})
}
