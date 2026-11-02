package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// TestImportMark checks import mark's outcomes (T15 of #256, client-contracts.md §11.3): applied
// only on the paused event at the 202's generation, an earlier pause ignored; not applied, exit 1,
// on a 422 at once, a mark-refused event by position or by code, resume-failed and a later
// takeover, and on a success with no such pause; exit 4 on a failed operation; exit 3 on a lapsed
// lease and on a success whose last events were not shown; and no marks, nothing sent.
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
		// The import ended, so the draft is named, not the commands of a paused import, and the success
		// is reported as ended reports it.
		{"refused, then the operation succeeded: not applied", "released", "succeeded", `{"draft":"drf_BWSYNTH","draftRevision":2,"importBaseRevision":"ibr_BWSYNTH"}`, "",
			[][2]string{refusedAt("2"), ev("continued", `{"type":"continued","generation":3}`), ev("succeeded", `{"type":"succeeded"}`)}, 1,
			"Import ing_BWSYNTH released into draft drf_BWSYNTH (revision 2)",
			"was not applied: it was refused (422 validation-failed, the mark at position 0 (the first is 0)), and operation op_BWSYNTH-ing then released the import without it. " +
				"Read the draft before marking again:\n  bronzewardctl draft show 'drf_BWSYNTH'\n"},
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
	step("a success without the pause is not the mark applied", mark, 1, "Import ing_BWSYNTH released into draft drf_BWSYNTH (revision 2)",
		"was not applied: operation op_BWSYNTH-ing succeeded without pausing at the mark's generation 2", sent)
	if startedRecord(t, "ing_BWSYNTH", false) {
		t.Error("a mark run that ended in success kept the record of this client's start")
	}

	// A success whose last events were not shown can have paused at the mark's generation among them:
	// the outcome is unknown, so standard output stays empty. The stream ends after the marked event
	// and the event pages are refused.
	setStdin(t, "doc[0]/BWSYNTH\n")
	f.set("released", 3, time.Hour, "succeeded", `{"draft":"drf_BWSYNTH","draftRevision":2,"importBaseRevision":"ibr_BWSYNTH"}`, "",
		marked, ev("paused", `{"type":"paused","generation":2}`), ev("continued", `{"type":"continued","generation":3}`), ev("succeeded", `{"type":"succeeded"}`))
	f.mu.Lock()
	f.cut, f.pagesGone = 1, true
	f.mu.Unlock()
	step("a success with its last events not shown: unknown", mark, 3, nothing,
		"operation op_BWSYNTH-ing succeeded, but its last events were not shown, so whether the mark was applied before the release is unknown.", sent)
	f.mu.Lock()
	f.pagesGone = false
	f.mu.Unlock()

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

// TestImportMarkReportsSave: a failure to keep the epoch the mark's answers showed is reported
// beside the mark's own outcome. The configuration file is made unreadable as the mark is sent.
func TestImportMarkReportsSave(t *testing.T) {
	f, step := importSteps(t)
	f.onMark = func() {
		if err := os.WriteFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "bronzeward", "config.yaml"), []byte("{"), 0o600); err != nil {
			t.Error(err)
		}
	}
	setStdin(t, "doc[0]/BWSYNTH-not-a-path\n")
	step("a 422, then the configuration cannot be updated", []string{"import", "mark", "ing_BWSYNTH"}, 1, "", "keeping the epoch, start mode and maximum token lifetime",
		`POST /ingestions/ing_BWSYNTH/marks if-match= body={"marks":["doc[0]/BWSYNTH-not-a-path"]}`)
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

// TestReviewText: only the document's own line feeds start a line of the review. A line feed in a
// declared path, kind or format stays inside its declaration's line, where the pager shows it as
// \x0a, so it cannot forge a declaration line; and the pager's wrap never cuts that escape.
func TestReviewText(t *testing.T) {
	var rv apitypes.IngestionReviewBody
	if err := json.Unmarshal([]byte(`{"ingestion":"ing_BWSYNTH","document":"a: 1\nb: 2\n","declarations":{
		"references":{"doc[0]/a\n# declared references: 0":{"kind":"secret\n#   x","version":1}},
		"embedded":[{"path":"doc[0]/b\n#   y","format":"yaml\n# z"}]}}`), &rv); err != nil {
		t.Fatal(err)
	}
	want := []string{"a: 1", "b: 2", "", "# declared references: 1",
		`#   doc[0]/a\x0a# declared references: 0: secret\x0a#   x version 1`,
		"# declared embedded documents: 1",
		`#   doc[0]/b\x0a#   y: yaml\x0a# z`}
	if got := wrap(reviewText(rv), 200); !slices.Equal(got, want) {
		t.Errorf("the review shows\n%q\nwant\n%q", got, want)
	}
	// At 20 columns the escape of the path's line feed does not fit after the 19 columns before it,
	// so it starts the next screen line whole.
	rv.Declarations.References = map[string]apitypes.Reference{"doc[0]/aaaaaaaa\nb": {Kind: "secret", Version: 1}}
	rv.Declarations.Embedded = nil
	want = []string{"a: 1", "b: 2", "", "# declared reference", "s: 1", "#   doc[0]/aaaaaaaa", `\x0ab: secret versio`, "n 1", "# declared embedded ", "documents: 0"}
	if got := wrap(reviewText(rv), 20); !slices.Equal(got, want) {
		t.Errorf("at 20 columns the review shows\n%q\nwant\n%q", got, want)
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
	if err := page([]string{"one", "two", "three", "four", "five"}, 3, 20, strings.NewReader("ffbq"), &w); err != nil {
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
	if err := page([]string{"x"}, 3, 20, strings.NewReader(""), &w); err != nil || strings.Count(w.String(), clearScreen) != 1 {
		t.Errorf("the end of the keys does not quit: %v, %q", err, w.String())
	}
	// On a narrow terminal the key line is cut to the width, so it cannot wrap and scroll the page.
	w.Reset()
	if err := page([]string{"x"}, 3, 20, strings.NewReader("q"), &w); err != nil || !strings.HasSuffix(w.String(), "\r\n-- (end) -- space or") {
		t.Errorf("the key line is not cut to 20 columns: %v, %q", err, w.String())
	}
	// The pager takes the key that quits and nothing after it: marks typed or pasted with it stay for
	// the marks prompt.
	keys := strings.NewReader("fqdoc[0]/BWSYNTH\n")
	if err := page([]string{"x"}, 3, 20, keys, io.Discard); err != nil {
		t.Fatal(err)
	}
	if rest, _ := io.ReadAll(keys); string(rest) != "doc[0]/BWSYNTH\n" {
		t.Errorf("after the pager quit, the keys hold %q; want the marks typed after q", rest)
	}
	// A wide character takes two cells, so ten of them fill a line of 20.
	if got := wrap([]string{strings.Repeat("界", 11)}, 20); len(got) != 2 || got[0] != strings.Repeat("界", 10) {
		t.Errorf("wrap counts a wide character as one cell: %q", got)
	}

	// Each line is escaped as all remote text is, a line feed inside a line too, and no escape is cut.
	got := wrap([]string{"\x1b[31mred\rback\u009bcsi\tx\xe2\x80\xae\xff\nz", strings.Repeat("a", 45)}, 20)
	if want := []string{`\x1b[31mred\x0dback`, `\x9bcsi\x09x\` + "u202e", `\xff\x0az`, strings.Repeat("a", 20), strings.Repeat("a", 20), "aaaaa"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("wrap gives %q, want %q", got, want)
	}
}

// TestWrapCombiningMark: a combining mark takes no cell, so a wrap never splits it from the
// character before it (#546 part 3). At 20 columns, 19 letters and an accented e fill the line, and
// the accent stays on it; when the e itself starts the next line, the accent goes with it.
func TestWrapCombiningMark(t *testing.T) {
	acc := "é"
	for _, c := range []struct {
		name, in string
		want     []string
	}{
		{"the accent fits", strings.Repeat("a", 19) + acc, []string{strings.Repeat("a", 19) + acc}},
		{"the letter wraps, the accent goes with it", strings.Repeat("a", 20) + acc, []string{strings.Repeat("a", 20), acc}},
		{"several marks on one letter", strings.Repeat("a", 19) + "ẹ́̂", []string{strings.Repeat("a", 19) + "ẹ́̂"}},
		{"a mark that starts the line", "́" + strings.Repeat("a", 20), []string{"́" + strings.Repeat("a", 20)}},
	} {
		if got := wrap([]string{c.in}, 20); !slices.Equal(got, c.want) {
			t.Errorf("%s: wrap gives %q, want %q", c.name, got, c.want)
		}
	}
}

// TestWrapSpacingMark: a spacing mark (Mc) takes its own column, as wcwidth gives it, and a wrap
// still never splits it from the character before it. At 20 columns, 19 letters and a Devanagari
// syllable of two columns take 21, so the syllable moves whole to the next line.
func TestWrapSpacingMark(t *testing.T) {
	ka := "का" // U+0915 and the spacing mark U+093E
	if got, want := cells('ा'), 1; got != want {
		t.Errorf("a spacing mark takes %d cells, want %d", got, want)
	}
	if got := wrap([]string{strings.Repeat("a", 19) + ka}, 20); !slices.Equal(got, []string{strings.Repeat("a", 19), ka}) {
		t.Errorf("wrap gives %q; want the syllable whole on the next line", got)
	}
	if got := wrap([]string{strings.Repeat("a", 18) + ka}, 20); !slices.Equal(got, []string{strings.Repeat("a", 18) + ka}) {
		t.Errorf("wrap gives %q; want the syllable to fit the line", got)
	}
}

// TestWrapOversizedMarkGroup: a base with more marks than a row holds never has a mark start a
// screen line. The marks that do not fit show as their Go escape, and no row is wider than cols.
func TestWrapOversizedMarkGroup(t *testing.T) {
	got := wrap([]string{"क" + strings.Repeat("ा", 20)}, 20)
	if len(got) < 2 {
		t.Fatalf("wrap gives %q; want the marks that do not fit on a later row", got)
	}
	for i, l := range got {
		width := 0
		for _, r := range l {
			width += cells(r)
		}
		if width > 20 {
			t.Errorf("row %d is %d cells wide: %q", i, width, l)
		}
		if r, _ := utf8.DecodeRuneInString(l); unicode.IsMark(r) {
			t.Errorf("row %d starts with a mark: %q", i, l)
		}
	}
	if !strings.Contains(strings.Join(got, ""), `ा`) {
		t.Errorf("wrap gives %q; want the marks that do not fit as escapes", got)
	}
}

// TestPageHeightOnlyResize: a resize that changes only the height shows the same screen line, also
// when it is partway through a wrapped line (a Copilot finding on the first pull request).
func TestPageHeightOnlyResize(t *testing.T) {
	sizes := [][2]int{{4, 20}, {5, 20}}
	n := 0
	size := func() (int, int) {
		s := sizes[min(n, len(sizes)-1)]
		n++
		return s[0], s[1]
	}
	var w bytes.Buffer
	text := []string{strings.Repeat("a", 100), "z"}
	if err := pageOn(text, size, &scriptKeys{[]scriptStep{{' ', evKey}, {0, evResize}, {'q', evKey}}}, &w); err != nil {
		t.Fatal(err)
	}
	frames := strings.Split(w.String(), clearScreen)[1:]
	a := strings.Repeat("a", 20)
	if len(frames) != 3 || !strings.HasPrefix(frames[2], a+"\r\n"+a+"\r\nz\r\n") {
		t.Errorf("after a height-only resize the frames are %q; want the page to go on from the fourth screen line", frames)
	}
}

// TestPageUndersized: a resize below the smallest terminal shows a notice, not the page at the old
// size, ignores the paging keys, and shows the page again once the size is usable.
func TestPageUndersized(t *testing.T) {
	sizes := [][2]int{{6, 40}, {0, 0}, {6, 40}}
	n := 0
	size := func() (int, int) {
		s := sizes[min(n, len(sizes)-1)]
		n++
		return s[0], s[1]
	}
	var w bytes.Buffer
	text := []string{"one", "two", "three", "four", "five", "six", "seven"}
	keys := &scriptKeys{[]scriptStep{{0, evResize}, {' ', evKey}, {0, evResize}, {'q', evKey}}}
	if err := pageOn(text, size, keys, &w); err != nil {
		t.Fatal(err)
	}
	frames := strings.Split(w.String(), clearScreen)[1:]
	if len(frames) != 4 {
		t.Fatalf("%d frames, want 4: %q", len(frames), frames)
	}
	if frames[1] != "Window too small" || frames[2] != "Window too small" {
		t.Errorf("while too small the frames are %q and %q; want the notice only", frames[1], frames[2])
	}
	if frames[3] != frames[0] {
		t.Errorf("after the resize back the page is %q; want the first page %q (the space was ignored)", frames[3], frames[0])
	}
}

// scriptKeys is a keySource that plays steps, then ends the keys.
type scriptKeys struct{ steps []scriptStep }

type scriptStep struct {
	key byte
	ev  event
}

func (k *scriptKeys) next() (byte, event, error) {
	if len(k.steps) == 0 {
		return 0, evKey, io.EOF
	}
	s := k.steps[0]
	k.steps = k.steps[1:]
	return s.key, s.ev, nil
}

// TestPageResize: on a resize the pager reads the size again, wraps to it and shows the page that
// starts at the same line of the text (#546 part 2).
func TestPageResize(t *testing.T) {
	text := []string{"line 1", "line 2", "line 3", "line 4", "line 5", "line 6", "line 7", "line 8", "line 9", "line 10"}
	sizes := [][2]int{{5, 40}, {3, 40}}
	n := 0
	size := func() (int, int) {
		s := sizes[min(n, len(sizes)-1)]
		n++
		return s[0], s[1]
	}
	var w bytes.Buffer
	keys := &scriptKeys{[]scriptStep{{' ', evKey}, {0, evResize}, {'q', evKey}}}
	if err := pageOn(text, size, keys, &w); err != nil {
		t.Fatal(err)
	}
	frames := strings.Split(w.String(), clearScreen)[1:]
	want := []string{"line 1\r\nline 2\r\nline 3\r\nline 4\r\n-- lines 1-4 of 10", "line 5\r\nline 6\r\nline 7\r\nline 8\r\n-- lines 5-8 of 10",
		"line 5\r\nline 6\r\n-- lines 5-6 of 10"}
	if len(frames) != len(want) {
		t.Fatalf("%d frames, want %d: %q", len(frames), len(want), frames)
	}
	for i, fr := range frames {
		if !strings.HasPrefix(fr, want[i]) {
			t.Errorf("frame %d is %q, want it to start %q", i, fr, want[i])
		}
	}

	// A resize to fewer columns wraps again: the page still starts at its first line, and every
	// line fits the new width.
	sizes = [][2]int{{6, 40}, {6, 20}}
	n = 0
	w.Reset()
	long := []string{strings.Repeat("a", 30), strings.Repeat("b", 30), "c"}
	if err := pageOn(long, size, &scriptKeys{[]scriptStep{{0, evResize}, {'q', evKey}}}, &w); err != nil {
		t.Fatal(err)
	}
	frames = strings.Split(w.String(), clearScreen)[1:]
	if len(frames) != 2 || !strings.HasPrefix(frames[1], strings.Repeat("a", 20)+"\r\n"+strings.Repeat("a", 10)+"\r\n"+strings.Repeat("b", 20)) {
		t.Errorf("after a resize to 20 columns the page is %q", frames)
	}
}

// TestPageSignal: a signal ends the pager before it writes again.
func TestPageSignal(t *testing.T) {
	var w bytes.Buffer
	keys := &scriptKeys{[]scriptStep{{'f', evKey}, {0, evSignal}, {'f', evKey}}}
	err := pageOn([]string{"a", "b", "c", "d"}, func() (int, int) { return 3, 20 }, keys, &w)
	if err != errSignalled || strings.Count(w.String(), clearScreen) != 2 {
		t.Errorf("a signal: %v after %d frames; want errSignalled after 2", err, strings.Count(w.String(), clearScreen))
	}
}
