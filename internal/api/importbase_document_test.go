package api

import (
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

func importBaseDocumentCall(tok, rev string) call {
	return call{method: "GET", path: prefix + "/import-base-revisions/" + rev + "/document", token: tok}
}

// reviewedBase runs an import of twoSecrets plus the unmarked sentinel under the operator review,
// continued by the human named continuer, and returns the import base revision it wrote and its
// claim.
func (ie *ingestEnv) reviewedBase(t *testing.T, continuer string) (ibr, claim string) {
	t.Helper()
	return ie.reviewedBaseOf(t, continuer, map[string]any{"staging": "encrypted", "review": true,
		"document": twoSecrets + "  network:\n    hostname: " + importBaseSentinel + "\n", "marks": []string{labelMark}})
}

// reviewedBaseOf is reviewedBase for the request body over names: it starts the import, runs it to
// its pause, has the human named continuer continue it and runs the continuation.
func (ie *ingestEnv) reviewedBaseOf(t *testing.T, continuer string, over map[string]any) (ibr, claim string) {
	t.Helper()
	op, j := ie.startJob(t, over)
	ie.runWith(t, options{}, j)
	if r := ie.pausedRow(t, j.claim.ID); r.state != "paused" {
		t.Fatalf("the reviewed run did not pause: %+v", r)
	}
	ie.n++
	before := len(ie.runs())
	rec := ie.do(ie.api, continueCall(ie.human(continuer), fmt.Sprintf("k-continue-by-%08d", ie.n), j.claim.ID))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("continuation by %s: %d %s", continuer, rec.Code, rec.Body)
	}
	js := ie.runs()
	if len(js) != before+1 {
		t.Fatalf("%d jobs handed over, want %d", len(js), before+1)
	}
	ie.runWith(t, options{}, js[before])
	r := readOp(t, ie.db, op)
	ibr, _ = r.result["importBaseRevision"].(string)
	if r.state != "succeeded" || ibr == "" {
		t.Fatalf("operation %+v", r)
	}
	return ibr, j.claim.ID
}

// PA §9.2, choice §17.36 and the owner's decision of ginsys/bronzeward#858: the importer who
// continued their own review reads the sanitized document, under no-store, and only {id, machine,
// document}: no declarations, digest, baseline or generation path. The read records no act and the
// unmarked sentinel it answers reaches no log, no summary, no operation result and no review read.
func TestImportBaseDocumentAnswersImporter(t *testing.T) {
	ie := newIngestEnv(t, options{})
	ibr, claim := ie.reviewedBase(t, "h-author")
	var want string
	if err := ie.db.QueryRow(`SELECT document FROM import_base_revision WHERE id = $1`, ibr).Scan(&want); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(want, importBaseSentinel) || !strings.Contains(want, "!bwref") {
		t.Fatalf("control: the stored document must hold the sentinel and a reference: %q", want)
	}
	acts := count(t, ie.db, "SELECT count(*) FROM act")
	rec := ie.do(ie.api, importBaseDocumentCall(ie.human("h-author"), ibr))
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Bronzeward-Epoch") == "" {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	b := decode[map[string]any](t, rec, http.StatusOK)
	if len(b) != 3 || b["id"] != ibr || b["machine"] != ie.machine || b["document"] != want {
		t.Fatalf("body %s; want exactly id %s, machine %s and the stored document", rec.Body, ibr, ie.machine)
	}
	if n := count(t, ie.db, "SELECT count(*) FROM act"); n != acts {
		t.Fatalf("the read wrote %d act rows", n-acts)
	}
	if ie.logged(importBaseSentinel) {
		t.Error("a log line holds the sentinel")
	}
	// The sentinel is in the document and nowhere else a reader of the revision can look.
	summary := ie.do(ie.api, importBaseCall(ie.human("h-author"), ibr))
	if strings.Contains(summary.Body.String(), importBaseSentinel) {
		t.Errorf("the summary holds the sentinel: %s", summary.Body)
	}
	op := ie.do(ie.api, call{method: "GET", path: prefix + "/ingestions/" + claim, token: ie.human("h-author")})
	if strings.Contains(op.Body.String(), importBaseSentinel) {
		t.Errorf("the ingestion resource holds the sentinel: %s", op.Body)
	}
}

// PA §9.2, §10.3: a role other than author, and a service identity, are 403 forbidden; an unknown
// revision, another entity's identifier and a malformed one are 404; a query is 400. Every refusal
// is no-store and none holds the document's text.
func TestImportBaseDocumentRoleAndNotFound(t *testing.T) {
	ie := newIngestEnv(t, options{})
	ibr, _ := ie.reviewedBase(t, "h-author")
	for name, tok := range map[string]string{"service": ie.robot, "viewer": ie.human("h-viewer"), "publisher": ie.human("h-publisher"),
		"approver": ie.human("h-approver"), "recovery-admin": ie.human("h-recovery")} {
		rec := ie.refused(importBaseDocumentCall(tok, ibr))
		wantProblem(t, rec, http.StatusForbidden, "forbidden")
		if rec.Header().Get("Cache-Control") != "no-store" || strings.Contains(rec.Body.String(), importBaseSentinel) {
			t.Errorf("%s: %v %s", name, rec.Header(), rec.Body)
		}
	}
	author := ie.human("h-author")
	for _, rev := range []string{id.New(id.ImportBase), ie.draft, "not-an-id"} {
		rec := ie.refused(importBaseDocumentCall(author, rev))
		wantProblem(t, rec, http.StatusNotFound, "not-found")
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %v", rev, rec.Header())
		}
	}
	rec := ie.refused(call{method: "GET", path: prefix + "/import-base-revisions/" + ibr + "/document?x=1", token: author})
	wantProblem(t, rec, http.StatusBadRequest, "invalid-request")
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("query: %v", rec.Header())
	}
}

// The owner's decision of ginsys/bronzeward#858: another author is refused 403 forbidden with the
// rule not-the-importer, whatever the review state, so the refusal tells them nothing about it; the
// importer is refused 409 conflict with the rule import-not-reviewed when no review of theirs
// continued: an import made without review, an import continued by another author, a continued
// claim with no recorded continuer, a file claim of a kind other than import and a claim with no
// recorded source (a source machine claim is read by any author: its own test). Control: the same
// revision answers the importer 200 before each change.
func TestImportBaseDocumentRefusals(t *testing.T) {
	ie := newIngestEnv(t, options{})
	importer, other := ie.human("h-author"), ie.human("h-all")
	// problemRule checks a refusal: its status and code, no-store, and none of the document's text.
	problemRule := func(rec *httptest.ResponseRecorder, status int, code string) map[string]any {
		t.Helper()
		p := wantProblem(t, rec, status, code)
		if rec.Header().Get("Cache-Control") != "no-store" || strings.Contains(rec.Body.String(), importBaseSentinel) {
			t.Fatalf("%v %s", rec.Header(), rec.Body)
		}
		return p
	}
	// stripped is a problem document without its request identifier, so two refusals compare.
	stripped := func(p map[string]any) map[string]any {
		q := maps.Clone(p)
		delete(q, "instance")
		return q
	}
	notReviewed := func(ibr string) {
		t.Helper()
		p := problemRule(ie.refused(importBaseDocumentCall(importer, ibr)), http.StatusConflict, "conflict")
		if p["rule"] != "import-not-reviewed" {
			t.Fatalf("problem %v", p)
		}
	}

	// An import made without review: transient staging, never a claim with a review.
	plain := ie.importedBase(t)
	notReviewed(plain)
	plainOther := stripped(problemRule(ie.refused(importBaseDocumentCall(other, plain)), http.StatusForbidden, "forbidden"))
	if plainOther["rule"] != "not-the-importer" {
		t.Fatalf("problem %v", plainOther)
	}

	// Continued by another author: shown to no one.
	byOther, _ := ie.reviewedBase(t, "h-all")
	// Control: its importer is h-author, so an author other than h-author is the importer's peer.
	notReviewed(byOther)
	otherOther := stripped(problemRule(ie.refused(importBaseDocumentCall(other, byOther)), http.StatusForbidden, "forbidden"))
	if !reflect.DeepEqual(otherOther, plainOther) {
		t.Errorf("another identity's refusal differs by review state: %v and %v", plainOther, otherOther)
	}

	// Reviewed by the importer: control, then each way the record can fail to say so.
	ibr, _ := ie.reviewedBase(t, "h-author")
	if rec := ie.do(ie.api, importBaseDocumentCall(importer, ibr)); rec.Code != http.StatusOK {
		t.Fatalf("control: %d %s", rec.Code, rec.Body)
	}
	reviewedOther := stripped(problemRule(ie.refused(importBaseDocumentCall(other, ibr)), http.StatusForbidden, "forbidden"))
	if !reflect.DeepEqual(reviewedOther, plainOther) {
		t.Errorf("another identity's refusal differs by review state: %v and %v", plainOther, reviewedOther)
	}
	for name, mutate := range map[string]string{
		"no recorded continuer": `UPDATE staging_claim SET continued_by = NULL WHERE id = $1`,
		"a file drift adoption": `UPDATE staging_claim SET kind = 'drift-adoption' WHERE id = $1`,
		"no recorded source":    `UPDATE staging_claim SET source = NULL WHERE id = $1`,
		"a pending review":      `UPDATE staging_claim SET continued_by = NULL, review = 'pending' WHERE id = $1`,
	} {
		t.Run(name, func(t *testing.T) {
			ibr, claim := ie.reviewedBase(t, "h-author")
			if rec := ie.do(ie.api, importBaseDocumentCall(importer, ibr)); rec.Code != http.StatusOK {
				t.Fatalf("control: %d %s", rec.Code, rec.Body)
			}
			if _, err := ie.db.Exec(mutate, claim); err != nil {
				t.Fatal(err)
			}
			notReviewed(ibr)
		})
	}
}

// machineBase runs an import of the machine's own configuration, started with over on top of a
// machine source, and returns the import base revision it wrote and its claim.
func (me *machineEnv) machineBase(t *testing.T, over map[string]any) (ibr, claim string) {
	t.Helper()
	body := map[string]any{"source": "machine", "document": nil, "staging": "encrypted"}
	maps.Copy(body, over)
	op, j := me.startJob(t, body)
	me.runWith(t, options{}, j)
	r := readOp(t, me.db, op)
	ibr, _ = r.result["importBaseRevision"].(string)
	if r.state != "succeeded" || ibr == "" {
		t.Fatalf("operation %+v", r)
	}
	return ibr, j.claim.ID
}

// The owner's decision of 2026-11-21, 20:36 CET (BW-node-config-readers-1, answer B, reversing
// ginsys/bronzeward#902): the author role may read a node's configuration, so any `author` reads the
// stored text of an import whose claim records the source machine, with or without a review, whoever
// started it, and a drift adoption (a claim of the same source) is read the same way. The answer is
// exactly {id, machine, document} under no-store, and records no act. Control: each claim records
// the source it was started with.
func TestImportBaseDocumentAnswersAnyAuthorForMachineSource(t *testing.T) {
	me := newMachineEnv(t)
	starter, other := me.human("h-author"), me.human("h-all")
	withReview := map[string]any{"source": "machine", "document": nil, "staging": "encrypted", "review": true}
	reviewed, reviewedClaim := me.reviewedBaseOf(t, "h-author", withReview)
	// A review continued by a second author does not change who reads the text.
	byOther, _ := me.reviewedBaseOf(t, "h-all", withReview)
	plain, plainClaim := me.machineBase(t, nil)
	adoption, adoptionClaim := me.machineBase(t, nil)
	if _, err := me.db.Exec(`UPDATE staging_claim SET kind = 'drift-adoption' WHERE id = $1`, adoptionClaim); err != nil {
		t.Fatal(err)
	}
	for _, claim := range []string{reviewedClaim, plainClaim, adoptionClaim} {
		var got string
		if err := me.db.QueryRow(`SELECT coalesce(source, '') FROM staging_claim WHERE id = $1`, claim).Scan(&got); err != nil || got != "machine" {
			t.Fatalf("claim %s records source %q, %v; want machine", claim, got, err)
		}
	}
	acts := count(t, me.db, "SELECT count(*) FROM act")
	for name, ibr := range map[string]string{"reviewed": reviewed, "continued by another author": byOther, "no review": plain, "a drift adoption": adoption} {
		var stored string
		if err := me.db.QueryRow(`SELECT document FROM import_base_revision WHERE id = $1`, ibr).Scan(&stored); err != nil || stored == "" {
			t.Fatalf("%s: control: the stored document is %q, %v", name, stored, err)
		}
		for who, tok := range map[string]string{"the starter": starter, "another author": other} {
			rec := me.do(me.api, importBaseDocumentCall(tok, ibr))
			if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%s, %s: %d %v %s", name, who, rec.Code, rec.Header(), rec.Body)
			}
			b := decode[map[string]any](t, rec, http.StatusOK)
			if len(b) != 3 || b["id"] != ibr || b["machine"] != me.machine || b["document"] != stored {
				t.Errorf("%s, %s: body %s; want exactly id, machine and the stored document", name, who, rec.Body)
			}
		}
	}
	if n := count(t, me.db, "SELECT count(*) FROM act"); n != acts {
		t.Errorf("the reads wrote %d act rows", n-acts)
	}
}

// A file import keeps the rule of 2026-11-18 (ginsys/bronzeward#858): only its importer after their
// own review reads it, so another author is refused 403 not-the-importer for it while the same
// author reads a machine import of the same environment; a claim with no recorded source reads
// nothing, 409 import-not-reviewed for its author and 403 for another (the check fails closed).
func TestImportBaseDocumentKeepsFileRuleBesideMachineRead(t *testing.T) {
	me := newMachineEnv(t)
	importer, other := me.human("h-author"), me.human("h-all")
	file, fileClaim := me.reviewedBase(t, "h-author")
	machine, _ := me.machineBase(t, nil)
	if rec := me.do(me.api, importBaseDocumentCall(other, machine)); rec.Code != http.StatusOK {
		t.Fatalf("control: another author reads the machine import: %d %s", rec.Code, rec.Body)
	}
	p := wantProblem(t, me.refused(importBaseDocumentCall(other, file)), http.StatusForbidden, "forbidden")
	if p["rule"] != "not-the-importer" {
		t.Errorf("file import, another author: problem %v", p)
	}
	if _, err := me.db.Exec(`UPDATE staging_claim SET source = NULL WHERE id = $1`, fileClaim); err != nil {
		t.Fatal(err)
	}
	rec := me.refused(importBaseDocumentCall(importer, file))
	p = wantProblem(t, rec, http.StatusConflict, "conflict")
	if p["rule"] != "import-not-reviewed" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("no recorded source, its author: %v %v", p, rec.Header())
	}
	p = wantProblem(t, me.refused(importBaseDocumentCall(other, file)), http.StatusForbidden, "forbidden")
	if p["rule"] != "not-the-importer" {
		t.Errorf("no recorded source, another author: problem %v", p)
	}
}
