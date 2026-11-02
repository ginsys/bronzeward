package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// importFake is draftFake with one ingestion, ing_BWSYNTH, and its operation op_BWSYNTH-ing: the
// claim's state, generation and lease, and the operation's events, state, result and error, each
// set by the test before a step.
type importFake struct {
	*draftFake
	mu         sync.Mutex
	claim      string
	gen        int
	lease      time.Time
	events     [][2]string // type and data
	state      string
	result     string
	problem    string
	onIngested func() // runs when an import start is accepted
	replayed   bool   // a continuation or takeover answers as a replay
	pagesGone  bool   // the event pages are refused
	others     int    // generations other clients take before a continuation or takeover lands
	mode       string // the staging mode, encrypted when empty
	cut        int    // the next stream ends after this many events, with no keep-alive
	noStream   bool   // the server answers the event stream with the JSON pages
	// moveAfterOp, when not 0, makes the epoch ep_BWSYNTH-2 for every answer from the read of the
	// operation after that many such reads on: the installation was restored. opReads counts them.
	// lapseOnOp lapses the lease at the first read of the operation, so no clock decides the order.
	moveAfterOp, opReads int
	lapseOnOp, noHeader  bool // noHeader leaves the epoch off every answer except the event streams
	ingReads             int  // the reads of the claim
	streams              int  // the event streams opened
}

const ingAccepted = `{"ingestion":"ing_BWSYNTH","operation":"op_BWSYNTH-ing"}`

// nothing, as a step's wanted standard output, wants it empty.
const nothing = "\x00nothing"

func (f *importFake) ingestion() string {
	// A review needs encrypted staging, so a transient import has none.
	mode, review := f.mode, "null"
	if mode == "" {
		mode, review = "encrypted", `"pending"`
	}
	return fmt.Sprintf(`{"id":"ing_BWSYNTH","kind":"import","mode":%q,"state":%q,"machine":"mch_BWSYNTH","draft":"drf_BWSYNTH",`+
		`"operation":"op_BWSYNTH-ing","review":%s,"ownerGeneration":%d,"leaseUntil":%q,"expiresAt":"2026-11-01T08:00:00Z","createdAt":"2026-11-01T06:00:00Z"}`,
		mode, f.claim, review, f.gen, f.lease.UTC().Format(time.RFC3339))
}

func (f *importFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	route := r.Method + " " + path
	f.mu.Lock()
	defer f.mu.Unlock()
	epoch := "ep_BWSYNTH-1"
	if f.moveAfterOp > 0 && f.opReads >= f.moveAfterOp {
		epoch = "ep_BWSYNTH-2"
	}
	if path == "/operations/op_BWSYNTH-ing" {
		f.opReads++
		if f.lapseOnOp && f.opReads == 1 {
			f.claim = "held"
		}
	}
	if !f.noHeader || strings.HasSuffix(path, "/events") {
		w.Header().Set("Bronzeward-Epoch", epoch)
	}
	w.Header().Set("Content-Type", "application/json")
	record := func() {
		body, _ := io.ReadAll(r.Body)
		f.draftFake.mu.Lock()
		f.writes = append(f.writes, fmt.Sprintf("%s if-match=%s body=%s", route, r.Header.Get("If-Match"), body))
		f.draftFake.mu.Unlock()
	}
	switch {
	case route == "POST /ingestions":
		record()
		if r.Header.Get("If-Match") != f.etag() {
			f.refuse(w)
			return
		}
		if f.onIngested != nil {
			f.onIngested()
		}
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, ingAccepted)
	case route == "POST /ingestions/ing_BWSYNTH/continuations", route == "POST /ingestions/ing_BWSYNTH/takeovers":
		record()
		if f.replayed {
			w.Header().Set("Idempotent-Replayed", "true")
		} else {
			// The server takes the claim at its next generation as it accepts the request.
			f.gen += f.others + 1
		}
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, ingAccepted)
	case route == "POST /ingestions/ing_BWSYNTH/abandonments":
		record()
		f.claim = "abandoned"
		fmt.Fprint(w, f.ingestion())
	case route == "GET /ingestions/ing_BWSYNTH":
		f.ingReads++
		fmt.Fprint(w, f.ingestion())
	case route == "GET /ingestions":
		fmt.Fprintf(w, `{"items":[%s]}`, f.ingestion())
	case path == "/operations/op_BWSYNTH-ing/events" && r.Header.Get("Accept") == "text/event-stream" && !f.noStream:
		w.Header().Set("Content-Type", "text/event-stream")
		f.streams++
		events, cut := f.events, f.cut > 0
		if cut {
			events, f.cut = events[:f.cut], 0
		}
		for i, e := range events {
			fmt.Fprintf(w, "id: %s:%d\nevent: %s\ndata: %s\n\n", epoch, i+1, e[0], e[1])
		}
		if !cut {
			fmt.Fprint(w, ": keep-alive\n\n")
		}
	case path == "/operations/op_BWSYNTH-ing/events" && f.pagesGone:
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"type":"urn:bronzeward:problem:forbidden","title":"BWSYNTH","status":403}`)
	case path == "/operations/op_BWSYNTH-ing/events":
		var items []string
		for i, e := range f.events {
			items = append(items, fmt.Sprintf(`{"id":"%s:%d","at":"2026-11-01T06:00:00Z","type":%q,"data":%s}`, epoch, i+1, e[0], e[1]))
		}
		fmt.Fprintf(w, `{"items":[%s]}`, strings.Join(items, ","))
	case path == "/operations/op_BWSYNTH-ing":
		fmt.Fprintf(w, `{"id":"op_BWSYNTH-ing","kind":"ingest","state":%q,"subject":{"draft":"drf_BWSYNTH","draftRevision":1},`+
			`"result":%s,"error":%s,"createdAt":"2026-11-01T06:00:00Z","epoch":%q,"lastEvent":%d}`, f.state, orNull(f.result), orNull(f.problem), epoch, len(f.events))
	default:
		f.draftFake.ServeHTTP(w, r)
	}
}

// set gives the claim and the operation for the next step.
func (f *importFake) set(claim string, gen int, lease time.Duration, state, result, problem string, events ...[2]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claim, f.gen, f.lease, f.state, f.result, f.problem, f.events = claim, gen, time.Now().Add(lease), state, result, problem, events
}

func ev(typ, data string) [2]string { return [2]string{typ, data} }

func orNull(s string) string {
	if s == "" {
		return "null"
	}
	return s
}

// TestImportWaitIgnoresReadsOfAnotherEpoch: once its stream ended, the watch reads the operation and
// then asks whether the lapsed lease decides. That question reads the claim and the operation again.
// When the epoch moved between the watch's read and these, they are of a restored installation, not
// of the events shown: the lease does not decide until the events are shown again (client-contracts.md
// section 8.5). The claim is read once more, so a decision that waited for the new epoch shows it.
func TestImportWaitIgnoresReadsOfAnotherEpoch(t *testing.T) {
	f := &importFake{draftFake: &draftFake{rev: 1, state: "open"}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	leaseMargin = 0
	t.Cleanup(func() { leaseMargin = 10 * time.Second })
	f.set("pending", 1, -time.Hour, "running", "", "", ev("continued", `{"type":"continued","generation":2}`))
	// The claim holds a lapsed lease from the start, but only the state held lets it decide. The
	// state changes at the watch's own read of the operation and the epoch moves after it, so only the
	// question asked after that read finds the claim held and answers of the new epoch. No clock
	// decides the order.
	f.mu.Lock()
	f.moveAfterOp, f.lapseOnOp = 1, true
	f.mu.Unlock()
	var o, e bytes.Buffer
	code := run([]string{"import", "continue", "ing_BWSYNTH", "-o", "json"}, &o, &e)
	f.mu.Lock()
	streams := f.streams
	f.mu.Unlock()
	if code != 3 || o.Len() != 0 || !strings.Contains(e.String(), "the outcome is unknown") {
		t.Errorf("exit %d, stdout %q, stderr %q; want 3, nothing and the unknown outcome", code, o.String(), e.String())
	}
	// The question after the watch's read finds a claim and an operation of the new epoch, so the lease
	// does not decide. The watch shows the events again from a second stream before it ends.
	if streams != 2 {
		t.Errorf("%d event streams were opened; want 2: the first answer of the question rests on reads of the new epoch", streams)
	}
}

// TestImportWaitDecidesOnAnswersWithoutEpoch: an answer without Bronzeward-Epoch is a server defect
// the client notes. It is no change of epoch, so the lapsed lease decides and the wait ends.
func TestImportWaitDecidesOnAnswersWithoutEpoch(t *testing.T) {
	f := &importFake{draftFake: &draftFake{rev: 1, state: "open"}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	leaseMargin = 0
	t.Cleanup(func() { leaseMargin = 10 * time.Second })
	f.set("held", 1, -time.Hour, "running", "", "", ev("continued", `{"type":"continued","generation":2}`))
	f.mu.Lock()
	f.noHeader = true
	f.mu.Unlock()
	var o, e bytes.Buffer
	code := run([]string{"import", "continue", "ing_BWSYNTH", "-o", "json"}, &o, &e)
	if code != 3 || o.Len() != 0 || !strings.Contains(e.String(), "the outcome is unknown") {
		t.Errorf("exit %d, stdout %q, stderr %q; want 3, nothing and the unknown outcome", code, o.String(), e.String())
	}
}

// TestImportLifecycle checks import start, continue, takeover, abandon and status (T15 of #256):
// each wait's end and exit code, the history before this command's generation ignored, ETAG-02's
// two cases, the lapsed lease of PROT-06, and the usage refusal of a review without encrypted
// staging.
func TestImportLifecycle(t *testing.T) {
	f := &importFake{draftFake: &draftFake{rev: 1, state: "open"}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	leaseMargin = 0
	t.Cleanup(func() { leaseMargin = 10 * time.Second })
	step := func(name string, args []string, code int, out, errText string, writes ...string) {
		t.Helper()
		var o, e bytes.Buffer
		got := run(args, &o, &e)
		took := f.took()
		okOut := strings.Contains(o.String(), out)
		if out == nothing {
			okOut = o.Len() == 0
		}
		if got != code || !okOut || !strings.Contains(e.String(), errText) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want %d, %q, %q", name, got, o.String(), e.String(), code, out, errText)
		}
		if strings.Join(took, "\n") != strings.Join(writes, "\n") {
			t.Errorf("%s: sent\n  %s\nwant\n  %s", name, strings.Join(took, "\n  "), strings.Join(writes, "\n  "))
		}
	}
	start := []string{"import", "start", "--machine", "mch_BWSYNTH", "--draft", "drf_BWSYNTH"}
	startBody := func(etag, extra string) string {
		return `POST /ingestions if-match=` + etag + ` body={"draft":"drf_BWSYNTH","kind":"import","machine":"mch_BWSYNTH",` + extra + `"source":"machine","staging":"encrypted"}`
	}

	step("--review with transient staging", append(start, "--review", "--staging", "transient"), 2, "", "--review needs encrypted staging")
	step("draft show", []string{"draft", "show", "drf_BWSYNTH"}, 0, "drf_BWSYNTH", "")
	f.set("paused", 1, time.Hour, "running", "", "", ev("started", `{"type":"started"}`), ev("staged", `{"type":"staged"}`), ev("paused", `{"type":"paused","generation":1}`))
	step("start --review waits until paused", append(start, "--review"), 0,
		"Import ing_BWSYNTH started (operation op_BWSYNTH-ing). Staging: encrypted. Review: requested.\nPaused for review (generation 1, expires ",
		"  staged\n  paused (generation 1)\n", startBody(`"1-BWSYNTH"`, `"review":true,`))

	f.set("released", 1, time.Hour, "succeeded", `{"draft":"drf_BWSYNTH","draftRevision":2,"importBaseRevision":"ibr_BWSYNTH"}`, "",
		ev("paused", `{"type":"paused","generation":1}`), ev("continued", `{"type":"continued","generation":2}`), ev("succeeded", `{"type":"succeeded"}`))
	f.draftFake.mu.Lock()
	f.rev = 2 // the import's own write
	f.draftFake.mu.Unlock()
	step("continue to success; ETAG-02 adopts revision 2", []string{"import", "continue", "ing_BWSYNTH"}, 0,
		"Continued import ing_BWSYNTH (operation op_BWSYNTH-ing).\nImport ing_BWSYNTH released into draft drf_BWSYNTH (revision 2), import base ibr_BWSYNTH.",
		"", `POST /ingestions/ing_BWSYNTH/continuations if-match= body={}`)
	step("the next write sends the adopted ETag", []string{"profile", "rm", "workers", "--draft", "drf_BWSYNTH"}, 0, "", "",
		`DELETE /drafts/drf_BWSYNTH/profiles/workers if-match="2-BWSYNTH" type= body=`)

	// ETAG-02 case 2: another author writes after the import's revision.
	f.onIngested = func() {
		f.draftFake.mu.Lock()
		f.rev += 2
		f.draftFake.mu.Unlock()
	}
	f.set("released", 1, time.Hour, "succeeded", `{"draft":"drf_BWSYNTH","draftRevision":4,"importBaseRevision":"ibr_BWSYNTH"}`, "",
		ev("succeeded", `{"type":"succeeded"}`))
	step("start overtaken after its revision: treated as a 412", start, 1, "released into draft drf_BWSYNTH (revision 4)",
		"draft drf_BWSYNTH is now revision 5: another write followed this import's revision 4. Its ETag is not kept.", startBody(`"3-BWSYNTH"`, ""))
	f.onIngested = nil
	step("so the next write is refused, not resent", []string{"profile", "rm", "workers", "--draft", "drf_BWSYNTH"}, 1, "", "you saw revision 3, it is now revision 5",
		`DELETE /drafts/drf_BWSYNTH/profiles/workers if-match="3-BWSYNTH" type= body=`)

	f.set("abandoned", 1, time.Hour, "failed", "", `{"type":"urn:bronzeward:problem:precondition-failed","title":"BWSYNTH","status":412}`,
		ev("paused", `{"type":"paused","generation":1}`), ev("continued", `{"type":"continued","generation":2}`), ev("failed", `{"type":"failed","code":"precondition-failed"}`))
	// The kept ETag is the one from before the draft moved, so the new start needs a draft show first.
	step("an overtaken continuation exits 4 and offers a draft show, then a new start", []string{"import", "continue", "ing_BWSYNTH"}, 4, "",
		"The draft changed after this import started, so the import was not applied and its claim is abandoned. "+
			"Read the draft's current revision, which keeps its ETag, then import again:\n"+
			"  bronzewardctl --context 'lab' draft show 'drf_BWSYNTH'\n"+
			"  bronzewardctl import start --machine 'mch_BWSYNTH' --draft 'drf_BWSYNTH' --review\n", `POST /ingestions/ing_BWSYNTH/continuations if-match= body={}`)

	f.set("held", 1, time.Hour, "running", "", "",
		ev("paused", `{"type":"paused","generation":1}`), ev("continued", `{"type":"continued","generation":2}`), ev("resume-failed", `{"type":"resume-failed","code":"dependency-unavailable"}`))
	// The lease read before the resume-failed can be stale, so the message names no deadline.
	step("resume-failed exits 1, offering status, takeover and abandon", []string{"import", "continue", "ing_BWSYNTH"}, 1, "",
		"The claim stays held until its lease lapses. Read its lease with the first command; once it lapsed, take it over, or abandon it now:\n"+
			"  bronzewardctl import status ing_BWSYNTH\n  bronzewardctl import takeover ing_BWSYNTH\n  bronzewardctl import abandon ing_BWSYNTH\n",
		`POST /ingestions/ing_BWSYNTH/continuations if-match= body={}`)

	f.set("held", 1, -time.Minute, "running", "", "", ev("continued", `{"type":"continued","generation":2}`))
	// Exit 3 leaves standard output empty, the accepted answer included (#256's output rule).
	step("a lease lapsed with the operation running exits 3 (PROT-06) with nothing on stdout", []string{"import", "continue", "ing_BWSYNTH", "-o", "json"}, 3, nothing,
		"or abandon it:\n  bronzewardctl import status ing_BWSYNTH\n  bronzewardctl import takeover ing_BWSYNTH\n", `POST /ingestions/ing_BWSYNTH/continuations if-match= body={}`)
	step("status of a lapsed claim offers takeover", []string{"import", "status", "ing_BWSYNTH"}, 0,
		"Its run stopped renewing the lease. Take it over to resume, or abandon it:\n  bronzewardctl import takeover ing_BWSYNTH", "")
	step("status lists with --state", []string{"import", "status", "--state", "held"}, 0, "ing_BWSYNTH  held   lapsed", "")

	f.set("paused", 2, time.Hour, "running", "", "", ev("paused", `{"type":"paused","generation":2}`), ev("taken-over", `{"type":"taken-over","generation":3}`),
		ev("paused", `{"type":"paused","generation":3}`))
	step("takeover waits for its own pause, not an earlier one", []string{"import", "takeover", "ing_BWSYNTH"}, 0, "Paused for review (generation 3,", "",
		`POST /ingestions/ing_BWSYNTH/takeovers if-match= body={}`)
	step("--no-wait", []string{"import", "takeover", "ing_BWSYNTH", "--no-wait"}, 0, "Follow it with: bronzewardctl import status ing_BWSYNTH", "",
		`POST /ingestions/ing_BWSYNTH/takeovers if-match= body={}`)
	step("abandon", []string{"import", "abandon", "ing_BWSYNTH"}, 0, "Import ing_BWSYNTH abandoned", "",
		`POST /ingestions/ing_BWSYNTH/abandonments if-match= body={}`)
	// The ingestion shows its review and staging, not its source or marks: the new start keeps the
	// first two and asks for the others again.
	step("status of an ended import offers a new start with its review", []string{"import", "status", "ing_BWSYNTH"}, 0,
		"Ended: abandoned. Import again:\n  bronzewardctl import start --machine 'mch_BWSYNTH' --draft 'drf_BWSYNTH' --review\n"+
			"  (the server keeps no document and no marks: if the import had --file or --marks, add them again)", "")

	// A replayed continuation or takeover started no run: the claim's generation already counts the
	// run the earlier request started, which the wait follows.
	f.replayed = true
	f.set("held", 2, time.Hour, "running", "", "",
		ev("paused", `{"type":"paused","generation":1}`), ev("continued", `{"type":"continued","generation":2}`), ev("resume-failed", `{"type":"resume-failed","code":"dependency-unavailable"}`))
	step("a replayed continuation follows the run it started", []string{"import", "continue", "ing_BWSYNTH"}, 1,
		"", "was not applied: its staged input could not be decrypted (resume-failed, dependency-unavailable)", `POST /ingestions/ing_BWSYNTH/continuations if-match= body={}`)
	// Its lease lapsed since: the backlog is shown before the lease is read, so the recorded
	// resume-failed decides the exit, not the lapsed lease.
	f.set("held", 2, -time.Minute, "running", "", "",
		ev("paused", `{"type":"paused","generation":1}`), ev("continued", `{"type":"continued","generation":2}`), ev("resume-failed", `{"type":"resume-failed","code":"dependency-unavailable"}`))
	step("a replayed continuation whose lease lapsed since still ends at its resume-failed", []string{"import", "continue", "ing_BWSYNTH"}, 1,
		"", "was not applied: its staged input could not be decrypted (resume-failed, dependency-unavailable)", `POST /ingestions/ing_BWSYNTH/continuations if-match= body={}`)
	f.cut = 2
	step("a stream cut before the resume-failed reconnects for it before the lapsed lease decides", []string{"import", "continue", "ing_BWSYNTH"}, 1,
		"", "was not applied: its staged input could not be decrypted (resume-failed, dependency-unavailable)", `POST /ingestions/ing_BWSYNTH/continuations if-match= body={}`)
	f.noStream = true
	step("the JSON pages are read for the resume-failed before the lapsed lease decides", []string{"import", "continue", "ing_BWSYNTH"}, 1,
		"", "was not applied: its staged input could not be decrypted (resume-failed, dependency-unavailable)", `POST /ingestions/ing_BWSYNTH/continuations if-match= body={}`)
	f.noStream = false
	f.set("paused", 3, -time.Minute, "running", "", "", ev("taken-over", `{"type":"taken-over","generation":3}`), ev("paused", `{"type":"paused","generation":3}`))
	step("a replayed takeover already paused ends at its pause", []string{"import", "takeover", "ing_BWSYNTH"}, 0, "Paused for review (generation 3,", "",
		`POST /ingestions/ing_BWSYNTH/takeovers if-match= body={}`)
	f.set("paused", 3, -time.Minute, "running", "", "", ev("taken-over", `{"type":"taken-over","generation":2}`), ev("paused", `{"type":"paused","generation":2}`),
		ev("marked", `{"type":"marked","generation":3,"paths":1}`), ev("paused", `{"type":"paused","generation":3}`))
	step("a replayed takeover marked since ends at the mark's pause", []string{"import", "takeover", "ing_BWSYNTH"}, 0, "Paused for review (generation 3,", "",
		`POST /ingestions/ing_BWSYNTH/takeovers if-match= body={}`)
	f.replayed = false

	// Text from the server cannot start a line of its own.
	f.set("held", 2, time.Hour, "running", "", "",
		ev("continued", `{"type":"continued","generation":3}`), ev("resume-failed", `{"type":"resume-failed","code":"x\nbronzewardctl: BWSYNTH forged"}`))
	step("an event's code is escaped", []string{"import", "continue", "ing_BWSYNTH"}, 1, "", `resume-failed: x\x0abronzewardctl: BWSYNTH forged`,
		`POST /ingestions/ing_BWSYNTH/continuations if-match= body={}`)
	f.set("ended\nBWSYNTH forged", 2, time.Hour, "failed", "", "")
	step("a state is escaped", []string{"import", "status", "ing_BWSYNTH"}, 0, `Ended: ended\x0aBWSYNTH forged. Import again:`, "")

	// A terminal operation decides the exit even when its last events cannot be read.
	f.pagesGone = true
	f.set("abandoned", 2, time.Hour, "failed", "", `{"type":"urn:bronzeward:problem:precondition-failed","title":"BWSYNTH","status":412}`,
		ev("continued", `{"type":"continued","generation":3}`))
	step("a failed drain keeps the failed import's exit 4", []string{"import", "continue", "ing_BWSYNTH"}, 4, "",
		"the operation's last events were not shown", `POST /ingestions/ing_BWSYNTH/continuations if-match= body={}`)
	f.pagesGone = false

	// Another client takes the claim over and pauses at generation 3 between the read before this
	// continuation and its arrival: the continuation runs at generation 4, and only its pause ends the wait.
	f.others = 1
	f.set("paused", 2, time.Hour, "running", "", "", ev("paused", `{"type":"paused","generation":2}`), ev("taken-over", `{"type":"taken-over","generation":3}`),
		ev("paused", `{"type":"paused","generation":3}`), ev("continued", `{"type":"continued","generation":4}`), ev("paused", `{"type":"paused","generation":4}`))
	step("another client's pause does not end this continuation's wait", []string{"import", "continue", "ing_BWSYNTH"}, 0, "Paused for review (generation 4,", "",
		`POST /ingestions/ing_BWSYNTH/continuations if-match= body={}`)
	f.others = 0

	// Transient staging cannot be taken over (the server refuses it), so nothing offers a takeover.
	f.mode = "transient"
	step("draft show reads the current revision", []string{"draft", "show", "drf_BWSYNTH"}, 0, "drf_BWSYNTH", "")
	f.set("held", 1, -time.Minute, "running", "", "", ev("started", `{"type":"started"}`))
	step("a lapsed transient import exits 3 offering no takeover", append(start, "--staging", "transient"), 3, nothing,
		"or abandon it (transient staging cannot be taken over):\n  bronzewardctl import status ing_BWSYNTH\n  bronzewardctl import abandon ing_BWSYNTH\n",
		strings.Replace(startBody(`"5-BWSYNTH"`, ""), `"encrypted"`, `"transient"`, 1))
	step("status of a lapsed transient claim offers abandon and a new start", []string{"import", "status", "ing_BWSYNTH"}, 0,
		"Transient staging cannot be taken over: abandon it and import again:\n  bronzewardctl import abandon ing_BWSYNTH\n"+
			"  bronzewardctl import start --machine 'mch_BWSYNTH' --draft 'drf_BWSYNTH' --staging transient\n", "")
	f.set("held", 1, time.Hour, "running", "", "")
	step("status of a live transient claim promises no takeover", []string{"import", "status", "ing_BWSYNTH"}, 0,
		"Its run holds the claim. Transient staging cannot be taken over; it can be abandoned now:", "")
	f.mode = ""

	// An empty --file names no document: it is refused, never read as the machine source.
	f.set("abandoned", 1, time.Hour, "failed", "", "")
	step("an empty --file is refused", append(start, "--file", ""), 2, "", "--file needs a path, or - for standard input")

	// An epoch change during the draft read after a success keeps no ETag: a restored draft can show
	// the same revision with other content (client-contracts.md §8.3).
	step("draft show before the epoch step", []string{"draft", "show", "drf_BWSYNTH"}, 0, "drf_BWSYNTH", "")
	f.onIngested = func() {
		f.draftFake.mu.Lock()
		f.rev, f.draftFake.epoch = 6, "ep_BWSYNTH-2"
		f.draftFake.mu.Unlock()
	}
	f.set("released", 1, time.Hour, "succeeded", `{"draft":"drf_BWSYNTH","draftRevision":6,"importBaseRevision":"ibr_BWSYNTH"}`, "",
		ev("succeeded", `{"type":"succeeded"}`))
	step("a success read back across an epoch change keeps no ETag", start, 1, "released into draft drf_BWSYNTH (revision 6)",
		"the server's epoch changed while draft drf_BWSYNTH was read again after this import. Its ETag is not kept.", startBody(`"5-BWSYNTH"`, ""))
	f.onIngested = nil
}
