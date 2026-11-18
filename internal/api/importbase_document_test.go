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
// claim with no recorded continuer, and a drift adoption. Control: the same revision answers the
// importer 200 before each change.
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
		"a drift adoption":      `UPDATE staging_claim SET kind = 'drift-adoption' WHERE id = $1`,
		"a machine source":      `UPDATE staging_claim SET source = 'machine' WHERE id = $1`,
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

// The owner's decision of ginsys/bronzeward#858 limits the text to the person who provided the
// configuration file, and a source machine import has none (ginsys/bronzeward#902). The same
// author, having continued the review of both, reads the file import and is refused the machine
// import with 409 import-not-reviewed; another author is still 403 not-the-importer for either.
// Control: each claim records the source it was started with.
func TestImportBaseDocumentRefusesMachineSource(t *testing.T) {
	me := newMachineEnv(t)
	importer, other := me.human("h-author"), me.human("h-all")
	file, fileClaim := me.reviewedBase(t, "h-author")
	machine, machineClaim := me.reviewedBaseOf(t, "h-author", map[string]any{"source": "machine", "document": nil,
		"staging": "encrypted", "review": true})
	for claim, want := range map[string]string{fileClaim: "document", machineClaim: "machine"} {
		var got string
		if err := me.db.QueryRow(`SELECT coalesce(source, '') FROM staging_claim WHERE id = $1`, claim).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("claim %s records source %q, want %q", claim, got, want)
		}
	}
	if rec := me.do(me.api, importBaseDocumentCall(importer, file)); rec.Code != http.StatusOK {
		t.Fatalf("the file import: %d %s", rec.Code, rec.Body)
	}
	var stored string
	if err := me.db.QueryRow(`SELECT document FROM import_base_revision WHERE id = $1`, machine).Scan(&stored); err != nil || stored == "" {
		t.Fatalf("control: the machine import's stored document is %q, %v", stored, err)
	}
	rec := me.refused(importBaseDocumentCall(importer, machine))
	p := wantProblem(t, rec, http.StatusConflict, "conflict")
	if p["rule"] != "import-not-reviewed" || rec.Header().Get("Cache-Control") != "no-store" || strings.Contains(rec.Body.String(), stored) {
		t.Fatalf("%v %v %s", p, rec.Header(), rec.Body)
	}
	for name, ibr := range map[string]string{"file": file, "machine": machine} {
		p := wantProblem(t, me.refused(importBaseDocumentCall(other, ibr)), http.StatusForbidden, "forbidden")
		if p["rule"] != "not-the-importer" {
			t.Errorf("%s import, another author: problem %v", name, p)
		}
	}
}
