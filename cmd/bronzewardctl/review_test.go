package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// TestImportMark checks import mark's outcomes (T15 of #256, client-contracts.md §11.3): applied
// only on the paused event at the 202's generation, an earlier pause ignored; not applied, exit 1,
// on a 422 at once, a mark-refused event by position or by code, resume-failed and a later
// takeover, and on a success with no such pause; exit 4 on a failed operation; exit 3 on a lapsed
// lease; and no marks, nothing sent.
func TestImportMark(t *testing.T) {
	f, step := importSteps(t)
	mark := []string{"import", "mark", "ing_BWSYNTH"}
	sent := `POST /ingestions/ing_BWSYNTH/marks if-match= body={"marks":["doc[0]/BWSYNTH"]}`
	earlier, marked := ev("paused", `{"type":"paused","generation":1}`), ev("marked", `{"type":"marked","generation":2,"paths":1}`)

	setStdin(t, "doc[0]/BWSYNTH\n\n")
	f.set("paused", 2, time.Hour, "running", "", "", earlier, marked, ev("paused", `{"type":"paused","generation":2}`))
	step("paused at the mark's generation is applied", mark, 0,
		"Sent 1 path for import ing_BWSYNTH (operation op_BWSYNTH-ing, generation 2).\nMarked 1 path; re-staged and paused again (generation 2).", "  marked (generation 2)\n", sent)

	setStdin(t, "doc[0]/BWSYNTH\ndoc[0]/BWSYNTH-not-a-path\n")
	step("a mark that is not a path: 422 by position", mark, 1, "", "Refused: position 1.",
		`POST /ingestions/ing_BWSYNTH/marks if-match= body={"marks":["doc[0]/BWSYNTH","doc[0]/BWSYNTH-not-a-path"]}`)

	for _, c := range []struct{ name, problem, want string }{
		{"compilation refuses it: by position", `{"type":"urn:bronzeward:problem:validation-failed","status":422,"rule":"BWSYNTH-rule","position":0}`,
			"was not applied: it was refused (422 validation-failed, rule BWSYNTH-rule, the mark at position 0 (the first is 0)). The import is paused again at generation 2."},
		{"a dependency fails: by code", `{"type":"urn:bronzeward:problem:dependency-unavailable","status":503}`,
			"was not applied: it was refused (503 dependency-unavailable)."},
	} {
		setStdin(t, "doc[0]/BWSYNTH\n")
		f.set("paused", 2, time.Hour, "running", "", "", earlier, marked,
			ev("mark-refused", `{"type":"mark-refused","generation":2,"problem":`+c.problem+`}`), ev("paused", `{"type":"paused","generation":2}`))
		step(c.name, mark, 1, "", c.want, sent)
	}

	setStdin(t, "doc[0]/BWSYNTH\n")
	f.set("held", 2, time.Hour, "running", "", "", earlier, marked, ev("resume-failed", `{"type":"resume-failed","code":"dependency-unavailable"}`))
	step("resume-failed: not applied (PROT-05)", mark, 1, "",
		"the mark on import ing_BWSYNTH was not applied: its staged input could not be decrypted (resume-failed, dependency-unavailable)", sent)

	setStdin(t, "doc[0]/BWSYNTH\n")
	f.set("paused", 3, time.Hour, "running", "", "", marked, ev("taken-over", `{"type":"taken-over","generation":3}`), ev("paused", `{"type":"paused","generation":3}`))
	step("a later takeover: not applied", mark, 1, "", "was not applied: a takeover at generation 3 resumed the import without it", sent)

	// After the mark's marked event, a settlement at another generation is not the mark's: each case
	// below would end the other way if the follower took it.
	refusedAt := func(g string) [2]string {
		return ev("mark-refused", `{"type":"mark-refused","generation":`+g+`,"problem":{"type":"urn:bronzeward:problem:validation-failed","status":422,"position":0}}`)
	}
	setStdin(t, "doc[0]/BWSYNTH\n")
	f.set("paused", 2, time.Hour, "running", "", "", marked, refusedAt("1"), ev("paused", `{"type":"paused","generation":2}`))
	step("a refusal at another generation is not the mark's", mark, 0, "Marked 1 path; re-staged and paused again (generation 2).", "", sent)
	setStdin(t, "doc[0]/BWSYNTH\n")
	f.set("paused", 2, time.Hour, "running", "", "", marked, ev("paused", `{"type":"paused","generation":1}`), refusedAt("2"))
	step("a pause at another generation is not the mark's", mark, 1, "", "was not applied: it was refused (422 validation-failed, the mark at position 0", sent)

	// An operation read terminal ends the import whatever the run settled before it, and ends this
	// client's record of the import (§8.3). The draft is at the import's revision 2.
	f.draftFake.mu.Lock()
	f.rev = 2
	f.draftFake.mu.Unlock()
	for _, c := range []struct {
		name, state, op, result, problem string
		settle                           [][2]string
		code                             int
		out, err                         string
	}{
		{"paused, then the operation failed: exit 4", "abandoned", "failed", "", `{"type":"urn:bronzeward:problem:ingestion-abandoned","title":"BWSYNTH","status":409}`,
			[][2]string{ev("paused", `{"type":"paused","generation":2}`), ev("failed", `{"type":"failed","code":"ingestion-abandoned"}`)}, 4, "", "(operation op_BWSYNTH-ing) failed"},
		{"refused, then the operation succeeded: not applied", "released", "succeeded", `{"draft":"drf_BWSYNTH","draftRevision":2,"importBaseRevision":"ibr_BWSYNTH"}`, "",
			[][2]string{refusedAt("2"), ev("continued", `{"type":"continued","generation":3}`), ev("succeeded", `{"type":"succeeded"}`)}, 1, "", "was not applied: it was refused"},
		{"paused, then continued and released: applied", "released", "succeeded", `{"draft":"drf_BWSYNTH","draftRevision":2,"importBaseRevision":"ibr_BWSYNTH"}`, "",
			[][2]string{ev("paused", `{"type":"paused","generation":2}`), ev("continued", `{"type":"continued","generation":3}`), ev("succeeded", `{"type":"succeeded"}`)}, 0,
			"Marked 1 path; re-staged and paused at generation 2, and released since.\nImport ing_BWSYNTH released into draft drf_BWSYNTH (revision 2)", ""},
	} {
		if !startedRecord(t, "ing_BWSYNTH", true) {
			t.Fatal("the record of this client's start was not kept")
		}
		setStdin(t, "doc[0]/BWSYNTH\n")
		f.set(c.state, 3, time.Hour, c.op, c.result, c.problem, append([][2]string{marked}, c.settle...)...)
		step(c.name, mark, c.code, c.out, c.err, sent)
		if startedRecord(t, "ing_BWSYNTH", false) {
			t.Errorf("%s: the record of this client's start was kept", c.name)
		}
	}

	// That success ends the import, so it also ends this client's record of having started it (§8.3).
	if !startedRecord(t, "ing_BWSYNTH", true) {
		t.Fatal("the record of this client's start was not kept")
	}
	setStdin(t, "doc[0]/BWSYNTH\n")
	f.set("released", 3, time.Hour, "succeeded", `{"draft":"drf_BWSYNTH","draftRevision":2,"importBaseRevision":"ibr_BWSYNTH"}`, "",
		marked, ev("continued", `{"type":"continued","generation":3}`), ev("succeeded", `{"type":"succeeded"}`))
	step("a success without the pause is not the mark applied", mark, 1, "", "was not applied: operation op_BWSYNTH-ing succeeded without pausing at the mark's generation 2", sent)
	if startedRecord(t, "ing_BWSYNTH", false) {
		t.Error("a mark run that ended in success kept the record of this client's start")
	}

	setStdin(t, "doc[0]/BWSYNTH\n")
	f.set("abandoned", 2, time.Hour, "failed", "", `{"type":"urn:bronzeward:problem:validation-failed","title":"BWSYNTH","status":422}`,
		marked, ev("failed", `{"type":"failed","code":"validation-failed"}`))
	step("a failed operation exits 4", mark, 4, "", "(operation op_BWSYNTH-ing) failed", sent)

	setStdin(t, "doc[0]/BWSYNTH\n")
	f.set("held", 2, -time.Minute, "running", "", "", earlier, marked)
	// The outcome is unknown, so standard output stays empty (#256's output rule).
	step("a lapsed lease: unknown (PROT-06)", mark, 3, nothing, "the mark on import ing_BWSYNTH: its claim is held and its lease lapsed", sent)

	setStdin(t, "\n")
	step("no marks: nothing sent", mark, 2, "", "no marks were given; nothing was sent")
}

// startedRecord says whether the lab context keeps ing as an import this client started; with keep,
// it records that first.
func startedRecord(t *testing.T, ing string, keep bool) bool {
	t.Helper()
	cfg, err := client.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	x := cfg.Find("lab")
	if x == nil {
		t.Fatal("no context lab")
	}
	if keep {
		if err := client.KeepStarted(x, ing, true); err != nil {
			t.Fatal(err)
		}
	}
	ok, err := client.Started(x, ing)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// TestImportReviewRefused checks that import review reads nothing unless standard input and output
// are a terminal (PROT-02), and that it has no -o json form and takes no --no-input.
func TestImportReviewRefused(t *testing.T) {
	f, step := importSteps(t)
	review := []string{"import", "review", "ing_BWSYNTH"}
	step("standard output not a terminal (PROT-02)", review, 2, "", "only when standard input and standard output are a terminal, with no override; nothing was read or written")
	step("-o json", append(review, "-o", "json"), 2, "", "has no -o json or yaml form")
	step("--no-input", append(review, "--no-input"), 2, "", "takes neither --no-input nor --token-stdin")
	if f.reviews != 0 {
		t.Errorf("the review was read %d times", f.reviews)
	}
}

// TestReviewText: only the document's own line feeds start a line of the review. A declared path,
// kind or format is escaped whole, so a line feed in one cannot forge a declaration line.
func TestReviewText(t *testing.T) {
	var rv apitypes.IngestionReviewBody
	if err := json.Unmarshal([]byte(`{"ingestion":"ing_BWSYNTH","document":"a: 1\nb: 2\n","declarations":{
		"references":{"doc[0]/a\n# declared references: 0":{"kind":"secret\n#   x","version":1}},
		"embedded":[{"path":"doc[0]/b\n#   y","format":"yaml\n# z"}]}}`), &rv); err != nil {
		t.Fatal(err)
	}
	want := "a: 1\nb: 2\n\n# declared references: 1\n" +
		`#   doc[0]/a\x0a# declared references: 0: secret\x0a#   x version 1` + "\n" +
		"# declared embedded documents: 1\n" +
		`#   doc[0]/b\x0a#   y: yaml\x0a# z` + "\n"
	if got := reviewText(rv); got != want {
		t.Errorf("reviewText gives\n%q\nwant\n%q", got, want)
	}
}

// TestRawFile: import review finds the terminal beneath the table guard that run puts on standard
// output, and no file when standard output is none.
func TestRawFile(t *testing.T) {
	e := &env{output: "table"}
	e.out = tableGuard{e, os.Stdout, client.GuardWriter(os.Stdout)}
	if e.rawFile() != os.Stdout {
		t.Error("the file beneath the table guard is not found")
	}
	e.out = tableGuard{e, &bytes.Buffer{}, client.GuardWriter(&bytes.Buffer{})}
	if e.rawFile() != nil {
		t.Error("a buffer beneath the table guard is taken for a file")
	}
}

// TestPage checks the pager's keys and its rendering: forward, back and quit, every line wrapped,
// none cut off, and control characters shown, not sent.
func TestPage(t *testing.T) {
	var w bytes.Buffer
	if err := page("one\ntwo\nthree\nfour\nfive\n", 3, 20, strings.NewReader("ffbq"), &w); err != nil {
		t.Fatal(err)
	}
	frames := strings.Split(w.String(), clearScreen)[1:]
	want := []string{"one\r\ntwo\r\n-- lines 1-2 of 5", "three\r\nfour\r\n-- lines 3-4 of 5", "five\r\n~\r\n-- (end)", "three\r\nfour\r\n-- lines 3-4 of 5"}
	if len(frames) != len(want) {
		t.Fatalf("%d frames, want %d: %q", len(frames), len(want), frames)
	}
	for i, fr := range frames {
		if !strings.HasPrefix(fr, want[i]) {
			t.Errorf("frame %d is %q, want it to start %q", i, fr, want[i])
		}
	}
	w.Reset()
	if err := page("x", 3, 20, strings.NewReader(""), &w); err != nil || strings.Count(w.String(), clearScreen) != 1 {
		t.Errorf("the end of the keys does not quit: %v, %q", err, w.String())
	}
	// On a narrow terminal the key line is cut to the width, so it cannot wrap and scroll the page.
	w.Reset()
	if err := page("x", 3, 20, strings.NewReader("q"), &w); err != nil || !strings.HasSuffix(w.String(), "\r\n-- (end) -- space or") {
		t.Errorf("the key line is not cut to 20 columns: %v, %q", err, w.String())
	}
	// A wide character takes two cells, so ten of them fill a line of 20.
	if got := wrap(strings.Repeat("界", 11), 20); len(got) != 2 || got[0] != strings.Repeat("界", 10) {
		t.Errorf("wrap counts a wide character as one cell: %q", got)
	}

	// Each line is escaped as all remote text is, the line feeds alone kept, and no escape is cut.
	got := wrap("\x1b[31mred\rback\u009bcsi\tx\xe2\x80\xae\xff\n"+strings.Repeat("a", 45), 20)
	if want := []string{`\x1b[31mred\x0dback`, `\x9bcsi\x09x\` + "u202e", `\xff`, strings.Repeat("a", 20), strings.Repeat("a", 20), "aaaaa"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("wrap gives %q, want %q", got, want)
	}
}
