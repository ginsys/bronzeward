package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
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

	setStdin(t, "doc[0]/BWSYNTH\n")
	f.set("released", 3, time.Hour, "succeeded", `{"draft":"drf_BWSYNTH","draftRevision":2,"importBaseRevision":"ibr_BWSYNTH"}`, "",
		marked, ev("continued", `{"type":"continued","generation":3}`), ev("succeeded", `{"type":"succeeded"}`))
	step("a success without the pause is not the mark applied", mark, 1, "", "was not applied: operation op_BWSYNTH-ing succeeded without pausing at the mark's generation 2", sent)

	setStdin(t, "doc[0]/BWSYNTH\n")
	f.set("abandoned", 2, time.Hour, "failed", "", `{"type":"urn:bronzeward:problem:validation-failed","title":"BWSYNTH","status":422}`,
		marked, ev("failed", `{"type":"failed","code":"validation-failed"}`))
	step("a failed operation exits 4", mark, 4, "", "(operation op_BWSYNTH-ing) failed", sent)

	setStdin(t, "doc[0]/BWSYNTH\n")
	f.set("held", 2, -time.Minute, "running", "", "", earlier, marked)
	step("a lapsed lease: unknown (PROT-06)", mark, 3, "", "the mark on import ing_BWSYNTH: its claim is held and its lease lapsed", sent)

	setStdin(t, "\n")
	step("no marks: nothing sent", mark, 2, "", "no marks were given; nothing was sent")
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

	got := wrap("\x1b[31mred\rback\u009bcsi\tx\n"+strings.Repeat("a", 45), 20)
	if want := []string{"^[[31mred^Mback?csi ", "x", strings.Repeat("a", 20), strings.Repeat("a", 20), "aaaaa"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("wrap gives %q, want %q", got, want)
	}
}
