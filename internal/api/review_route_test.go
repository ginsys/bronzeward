package api

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
)

// reviewSentinel is an unmarked value: nothing identifies it, so the staged document still holds
// it and only the review route may answer it.
const reviewSentinel = "bw-review-sentinel-7f3a"

// pausedReview runs a reviewed import of twoSecrets plus an unmarked sentinel to its pause and
// returns the claim.
func (ie *ingestEnv) pausedReview(t *testing.T) string {
	t.Helper()
	_, j := ie.startJob(t, map[string]any{"staging": "encrypted", "review": true,
		"document": twoSecrets + "  network:\n    hostname: " + reviewSentinel + "\n", "marks": []string{labelMark}})
	ie.runWith(t, options{}, j)
	if r := ie.pausedRow(t, j.claim.ID); r.state != "paused" {
		t.Fatalf("the reviewed run did not pause: %+v", r)
	}
	return j.claim.ID
}

func reviewCall(tok, claim string) call {
	return call{method: "GET", path: prefix + "/ingestions/" + claim + "/review", token: tok}
}

// refused makes a call expected to be refused and keeps its body for assertAbsent.
func (ie *ingestEnv) refused(c call) *httptest.ResponseRecorder {
	rec := ie.do(ie.api, c)
	ie.bodies = append(ie.bodies, rec.Body.String())
	return rec
}

// stagedPayload is a claim's stored ciphertext and digest.
func (ie *ingestEnv) stagedPayload(t *testing.T, claim string) (provider.Ciphertext, []byte) {
	t.Helper()
	var ct string
	var sum []byte
	if err := ie.db.QueryRow(`SELECT convert_from(payload, 'UTF8'), payload_digest FROM staging_claim WHERE id = $1`, claim).
		Scan(&ct, &sum); err != nil {
		t.Fatal(err)
	}
	return provider.Ciphertext(ct), sum
}

// PA §9.1-§9.3, compilation §3.6 step 2: the review answers a paused claim's staged sanitized
// document and declarations to an author, under no-store, and nothing else: no baseline,
// generation path or digest. It records no act and changes nothing; the unmarked sentinel it
// answers reaches no log and no table.
func TestReviewRouteAnswersPaused(t *testing.T) {
	ie := newIngestEnv(t, options{})
	claim := ie.pausedReview(t)
	before := ie.pausedRow(t, claim)
	ct, _ := ie.stagedPayload(t, claim)
	acts := count(t, ie.db, "SELECT count(*) FROM act")
	rec := ie.do(ie.api, reviewCall(ie.human("h-author"), claim))
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Bronzeward-Epoch") == "" {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	b := decode[map[string]any](t, rec, http.StatusOK)
	doc, _ := b["document"].(string)
	decl, _ := b["declarations"].(map[string]any)
	refs, _ := decl["references"].(map[string]any)
	emb, embOK := decl["embedded"].([]any)
	if len(b) != 3 || b["ingestion"] != claim || !strings.Contains(doc, reviewSentinel) || !strings.Contains(doc, "!bwref") ||
		strings.Contains(doc, runToken) || strings.Contains(doc, runLabel) || len(refs) != 2 || !embOK || len(emb) != 0 || len(decl) != 2 {
		t.Fatalf("body %s", rec.Body)
	}
	for _, ref := range refs {
		if m, _ := ref.(map[string]any); m["kind"] == nil || m["version"] == nil || m["path"] != nil || m["generation"] != nil {
			t.Fatalf("reference %v", ref)
		}
	}
	if strings.Contains(rec.Body.String(), "gen/") || strings.Contains(rec.Body.String(), "vault:") {
		t.Fatalf("the review holds a generation path or a ciphertext: %s", rec.Body)
	}
	if after := ie.pausedRow(t, claim); after.state != "paused" || string(after.digest) != string(before.digest) ||
		!after.lease.Equal(before.lease) || after.gen != before.gen || count(t, ie.db, "SELECT count(*) FROM act") != acts {
		t.Fatalf("the review changed the claim or recorded an act: %+v", after)
	}
	if got, _ := ie.stagedPayload(t, claim); got != ct {
		t.Fatalf("the review replaced the staged payload")
	}
	assertAbsent(t, ie, reviewSentinel)
}

// Control for assertAbsent's bytea scan: a payload holding the sentinel's bytes is named, which a
// scan of rows' text forms (bytea in hex) would miss.
func TestReviewRouteLeakScanSeesBytes(t *testing.T) {
	ie := newIngestEnv(t, options{})
	claim := ie.pausedReview(t)
	if hits := tablesHolding(t, ie.db, reviewSentinel); len(hits) != 0 {
		t.Fatalf("before: %v", hits)
	}
	mustExec(t, ie.db, `UPDATE staging_claim SET payload = convert_to('x ' || $2 || ' x', 'UTF8') WHERE id = $1`, claim, reviewSentinel)
	if n := count(t, ie.db, `SELECT count(*) FROM staging_claim AS r WHERE r::text LIKE '%' || $1 || '%'`, reviewSentinel); n != 0 {
		t.Fatalf("control: the row's text form holds the sentinel (%d), so the bytea scan is not what finds it", n)
	}
	if hits := tablesHolding(t, ie.db, reviewSentinel); len(hits) != 1 || !strings.Contains(hits[0], "staging_claim.payload") {
		t.Fatalf("the scan missed the payload's bytes: %v", hits)
	}
}

// PA §9.2, §10.3: the review is for an author, human only: a service identity holding author and
// a human viewer are refused 403, and neither reads the claim's envelope.
func TestReviewRouteRoles(t *testing.T) {
	ie := newIngestEnv(t, options{})
	claim := ie.pausedReview(t)
	for name, tok := range map[string]string{"service": ie.robot, "viewer": ie.human("h-viewer")} {
		if p := wantProblem(t, ie.refused(reviewCall(tok, claim)), http.StatusForbidden, "forbidden"); strings.Contains(p["detail"].(string), reviewSentinel) {
			t.Fatalf("%s: problem %v", name, p)
		}
	}
	if ie.f.decrypts != 0 {
		t.Fatalf("%d decryptions for refused reads", ie.f.decrypts)
	}
}

// PA §9.3: a claim not paused as a read treats it is 409 conflict without a decryption: one still
// held, and a paused one past its absolute expiry (abandoned once due). An unknown ingestion and
// another entity's identifier are 404.
func TestReviewRouteNotPaused(t *testing.T) {
	ie := newIngestEnv(t, options{})
	_, held := ie.reviewedJob(t)
	author := ie.human("h-author")
	if p := wantProblem(t, ie.refused(reviewCall(author, held.claim.ID)), http.StatusConflict, "conflict"); p["ingestion"] != held.claim.ID {
		t.Fatalf("held: problem %v", p)
	}
	if ie.f.decrypts != 0 {
		t.Fatalf("%d decryptions for a held claim", ie.f.decrypts)
	}

	ie = newIngestEnv(t, options{})
	author = ie.human("h-author")
	claim := ie.pausedReview(t)
	mustExec(t, ie.db, `UPDATE staging_claim SET expires_at = now() - interval '1 second', lease_until = now() - interval '2 seconds'
		WHERE id = $1`, claim)
	wantProblem(t, ie.refused(reviewCall(author, claim)), http.StatusConflict, "conflict")
	// An item read takes no query (§9.1), before any lookup.
	wantProblem(t, ie.refused(call{method: "GET", path: prefix + "/ingestions/" + claim + "/review?x=1", token: author}),
		http.StatusBadRequest, "invalid-request")
	wantProblem(t, ie.refused(reviewCall(author, id.New(id.Ingestion))), http.StatusNotFound, "not-found")
	wantProblem(t, ie.refused(reviewCall(author, ie.draft)), http.StatusNotFound, "not-found")
	if ie.f.decrypts != 0 {
		t.Fatalf("%d decryptions for refused reads", ie.f.decrypts)
	}
}

// PA §9.3, §5 rule 1: the review decrypts after its read transaction ends, so no provider call holds
// the installation state: a recovery-mode entry could take it FOR UPDATE during the decryption.
func TestReviewRouteDecryptsAfterRead(t *testing.T) {
	ie := newIngestEnv(t, options{})
	claim := ie.pausedReview(t)
	probe := func() error {
		tx, err := ie.db.Begin()
		if err != nil {
			t.Error(err)
			return err
		}
		defer func() { _ = tx.Rollback() }()
		_, err = tx.Exec(`SELECT 1 FROM installation_state FOR UPDATE NOWAIT`)
		return err
	}
	// Control: the probe sees a share lock that is held.
	held, err := ie.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := held.Exec(`SELECT 1 FROM installation_state FOR SHARE`); err != nil {
		t.Fatal(err)
	}
	if probe() == nil {
		t.Fatal("control: the probe took the installation state while a share lock was held")
	}
	_ = held.Rollback()

	var during error
	ran := false
	ie.f.mu.Lock()
	ie.f.onDecrypt = func(context.Context) {
		if err := probe(); err != nil && during == nil {
			during = err
		}
		ran = true
	}
	ie.f.mu.Unlock()
	if rec := ie.do(ie.api, reviewCall(ie.human("h-author"), claim)); rec.Code != http.StatusOK || !ran || during != nil {
		t.Fatalf("%d, decrypted %v; the installation state was locked during the decryption: %v", rec.Code, ran, during)
	}
}

// PA §9.3, §16: the review answers 503 when the staging key is unavailable, and 500 for an
// envelope whose digest does not match and for a malformed envelope stored with its matching
// digest; after each the claim is still paused with its payload and digest unchanged, and the
// sentinel reaches no problem, log or table.
func TestReviewRouteFailuresLeaveClaim(t *testing.T) {
	ie := newIngestEnv(t, options{})
	claim := ie.pausedReview(t)
	author := ie.human("h-author")
	ct, sum := ie.stagedPayload(t, claim)
	unchanged := func(name string, digest []byte) {
		t.Helper()
		r := ie.pausedRow(t, claim)
		got, gotSum := ie.stagedPayload(t, claim)
		if r.state != "paused" || r.review != "pending" || got != ct || string(gotSum) != string(digest) {
			t.Fatalf("%s: claim %+v", name, r)
		}
	}

	ie.f.mu.Lock()
	ie.f.decryptErr = provider.ErrUnavailable
	ie.f.mu.Unlock()
	wantProblem(t, ie.refused(reviewCall(author, claim)), http.StatusServiceUnavailable, "dependency-unavailable")
	unchanged("key unavailable", sum)

	ie.f.mu.Lock()
	ie.f.decryptErr = nil
	envelope := ie.f.staged[ct]
	ie.f.staged[ct] = append(append([]byte(nil), envelope...), ' ')
	ie.f.mu.Unlock()
	wantProblem(t, ie.refused(reviewCall(author, claim)), http.StatusInternalServerError, "internal-error")
	unchanged("digest mismatch", sum)

	malformed := []byte(`{"version":1,"documents":"machine:\n  hostname: ` + reviewSentinel + `\n"}`)
	digest := sha256.Sum256(malformed)
	ie.f.mu.Lock()
	ie.f.staged[ct] = malformed
	ie.f.mu.Unlock()
	mustExec(t, ie.db, `UPDATE staging_claim SET payload_digest = $2 WHERE id = $1`, claim, digest[:])
	wantProblem(t, ie.refused(reviewCall(author, claim)), http.StatusInternalServerError, "internal-error")
	unchanged("malformed envelope", digest[:])

	if ie.f.decrypts != 3 {
		t.Fatalf("%d decryptions, want 3", ie.f.decrypts)
	}
	assertAbsent(t, ie, reviewSentinel)
}
