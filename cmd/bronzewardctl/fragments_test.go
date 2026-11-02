package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
)

// setStdin makes standard input a file holding s, which is not a terminal.
func setStdin(t *testing.T, s string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	old := stdin
	stdin = f
	t.Cleanup(func() { stdin = old; f.Close() })
}

// TestFragmentEditing checks fragment set's inputs (decision T12 of #256): the document from a
// file or standard input, declarations as YAML, marks from standard input until an empty line,
// the refusals that send nothing, and fragment rm.
func TestFragmentEditing(t *testing.T) {
	f := &draftFake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	dir := t.TempDir()
	write := func(name, s string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	doc := write("doc.yaml", "machine:\n  nodeLabels:\n    BWSYNTH-doc: x\n")
	decl := write("refs.yaml", "embedded:\n- {path: machine.files, format: yaml}\n")
	step := func(name string, args []string, code int, out, errText string, writes ...string) {
		t.Helper()
		var o, e bytes.Buffer
		got := run(args, &o, &e)
		took := f.took()
		if got != code || !strings.Contains(o.String(), out) || !strings.Contains(e.String(), errText) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want %d, %q, %q", name, got, o.String(), e.String(), code, out, errText)
		}
		if strings.Join(took, "\n") != strings.Join(writes, "\n") {
			t.Errorf("%s: sent\n  %s\nwant\n  %s", name, strings.Join(took, "\n  "), strings.Join(writes, "\n  "))
		}
	}
	set := []string{"fragment", "set", "worker-labels", "--draft", "drf_BWSYNTH", "--layer", "role"}
	f.rev, f.state = 3, "open"
	step("draft show", []string{"draft", "show", "drf_BWSYNTH"}, 0, "drf_BWSYNTH", "")

	setStdin(t, "machine.nodeLabels.BWSYNTH-mark\r\nmachine.files\n\nnot a mark\n")
	step("a file, declarations and marks from standard input", append(set, "--file", doc, "--declarations", decl, "--marks"), 0,
		"Fragment worker-labels saved in draft drf_BWSYNTH (revision frv_BWSYNTH-new; draft revision 4).", "",
		`PUT /drafts/drf_BWSYNTH/fragments/worker-labels if-match="3-BWSYNTH" type=application/json body=`+
			`{"declarations":{"embedded":[{"format":"yaml","path":"machine.files"}]},"document":"machine:\n  nodeLabels:\n    BWSYNTH-doc: x\n",`+
			`"layer":"role","marks":["machine.nodeLabels.BWSYNTH-mark","machine.files"]}`)

	setStdin(t, "BWSYNTH-stdin: x\n")
	step("the document from standard input", append(set, "--file", "-"), 0, "draft revision 5", "",
		`PUT /drafts/drf_BWSYNTH/fragments/worker-labels if-match="4-BWSYNTH" type=application/json body={"document":"BWSYNTH-stdin: x\n","layer":"role"}`)

	step("marks with the document on standard input and no prompt", append(set, "--file", "-", "--marks", "--no-input"), 2, "",
		"--marks reads from a prompt on a terminal or from standard input, and neither is free here; nothing was sent")
	step("the document and the token both on standard input", append(set, "--file", "-", "--token-stdin"), 2, "",
		"--file and --token-stdin read standard input, which only one of them can")
	step("no --file", set, 2, "", "fragment set needs --draft, --layer and --file")
	step("a document over 1 MiB", append(set, "--file", write("big", strings.Repeat("a", maxBody+1))), 1, "",
		"the document is over the server's 1 MiB body limit; nothing was sent")
	step("a body over 1 MiB once encoded", append(set, "--file", write("quotes", strings.Repeat(`"`, maxBody/2+1))), 1, "",
		"bytes, over the server's 1 MiB body limit; nothing was sent")
	// Marks of 1024 bytes each, line feed included, so the body limit comes before the count limit.
	long := "machine.files." + strings.Repeat("k", 1009) + "\n"
	setStdin(t, strings.Repeat(long, maxBody/len(long)+1))
	step("marks over 1 MiB together", append(set, "--file", doc, "--marks"), 1, "", "the marks are over the server's 1 MiB body limit; nothing was sent")
	step("a document that is not UTF-8", append(set, "--file", write("bin", "\xff\xfe")), 1, "", "the document is not UTF-8 text; nothing was sent")
	// The decoders quote member names and some values; the refusals quote nothing from the file.
	for _, c := range []struct{ name, file, says string }{
		{"an unknown declarations member", "BWSYNTH-member: [x]\n", "a member the API does not define, or a value of the wrong type; nothing was sent"},
		{"a number where the API takes a string", "embedded:\n- {path: 4242424242, format: yaml}\n", "a member the API does not define, or a value of the wrong type; nothing was sent"},
		{"a number as a member name", "embedded: []\n4242424242: x\n", "a member the API does not define, or a value of the wrong type; nothing was sent"},
		{"a member name YAML cannot hold", "? [BWSYNTH-key, 4242424242]\n: x\n", "not YAML or JSON; nothing was sent"},
		{"a member name in another case", "Embedded:\n- {path: machine.BWSYNTH-4242, format: json}\n", "a member the API does not define, or a value of the wrong type; nothing was sent"},
		{"one member name in two cases", "Embedded:\n- {path: machine.BWSYNTH-4242, format: json}\nembedded:\n- {path: machine.files, format: yaml}\n",
			"a member the API does not define, or a value of the wrong type; nothing was sent"},
		{"a nested member name in another case", "embedded:\n- {Path: machine.BWSYNTH-4242, format: json}\n", "a member the API does not define, or a value of the wrong type; nothing was sent"},
		{"a declarations file over 1 MiB", "#" + strings.Repeat("a", maxBody) + "\n", "the declarations file is over the server's 1 MiB body limit; nothing was sent"},
		{"a declaration in a second YAML document", "{}\n---\nembedded:\n- {path: machine.BWSYNTH-4242, format: json}\n", "more than one YAML document; nothing was sent"},
	} {
		var o, e bytes.Buffer
		if got := run(append(set, "--file", doc, "--declarations", write("bad.yaml", c.file)), &o, &e); got != 1 || !strings.Contains(e.String(), c.says) ||
			strings.Contains(e.String(), "BWSYNTH-") || strings.Contains(e.String(), "4242") {
			t.Errorf("%s: exit %d, stderr %q; want 1 and %q, with nothing from the file", c.name, got, e.String(), c.says)
		}
		if took := f.took(); len(took) != 0 {
			t.Errorf("%s: sent %v", c.name, took)
		}
	}
	step("fragment rm", []string{"fragment", "rm", "worker-labels", "--draft", "drf_BWSYNTH"}, 0,
		"Fragment worker-labels removed from draft drf_BWSYNTH (draft revision 6).", "",
		`DELETE /drafts/drf_BWSYNTH/fragments/worker-labels if-match="5-BWSYNTH" type= body=`)
}

// TestDeclarationsNull checks that null references and embedded, which the API takes, are accepted
// under their exact names only, and that an embedded entry without the required format is refused.
func TestDeclarationsNull(t *testing.T) {
	for _, c := range []struct {
		file string
		ok   bool
	}{
		{"embedded: null\n", true},
		{"references: null\n", true},
		{"references: null\nembedded:\n- {path: machine.files, format: yaml}\n", true},
		{"Embedded: null\n", false},
		{"embedded:\n- {path: machine.files}\n", false},
	} {
		p := filepath.Join(t.TempDir(), "d.yaml")
		if err := os.WriteFile(p, []byte(c.file), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := declarations(p); (err == nil) != c.ok {
			t.Errorf("%q: %v; want accepted %v", c.file, err, c.ok)
		}
	}
}

// TestLongMark checks that a mark longer than bufio.Scanner's default 64 KiB line is read whole:
// the API sets no length on one mark.
func TestLongMark(t *testing.T) {
	long := "machine.files." + strings.Repeat("k", 70<<10)
	setStdin(t, long+"\nmachine.files\n\n")
	m, err := (&env{}).readMarks(false)
	if err != nil || len(m) != 2 || m[0] != long || m[1] != "machine.files" {
		t.Errorf("%d marks, %v; want the 70 KiB mark and machine.files", len(m), err)
	}
}

// TestMarkTotal checks that all marks together are held to the body limit: marks of exactly
// maxBody bytes, line feeds included, are read, and one byte more is refused before the rest.
func TestMarkTotal(t *testing.T) {
	line := "machine.files." + strings.Repeat("k", 1009) + "\n" // 1024 bytes
	at := strings.Repeat(line, maxBody/len(line))
	if m, err := scanMarks(strings.NewReader(at), false, nil); err != nil || len(m) != maxBody/len(line) {
		t.Errorf("at the limit: %d marks, %v; want %d marks", len(m), err, maxBody/len(line))
	}
	if m, err := scanMarks(strings.NewReader(at+"x\n"), false, nil); err == nil || !strings.Contains(err.Error(), "the marks are over the server's 1 MiB body limit") {
		t.Errorf("one mark over the limit: %d marks, %v; want the refusal", len(m), err)
	}
}

// TestMarkCount checks that the marks are held to the 1024 that one request takes (MarkInput's
// maxItems), short marks far under the body limit included, from a terminal and from standard input.
func TestMarkCount(t *testing.T) {
	at := strings.Repeat("doc[0]/x\n", maxMarks)
	for _, terminal := range []bool{false, true} {
		if m, err := scanMarks(strings.NewReader(at), terminal, nil); err != nil || len(m) != maxMarks {
			t.Errorf("terminal=%v, at the limit: %d marks, %v; want %d marks", terminal, len(m), err, maxMarks)
		}
		if m, err := scanMarks(strings.NewReader(at+"doc[0]/x\n"), terminal, nil); err == nil || !strings.Contains(err.Error(), "more than 1024 marks were given") {
			t.Errorf("terminal=%v, one mark over the limit: %d marks, %v; want the refusal", terminal, len(m), err)
		}
	}
}

// TestMarkCarriageReturn checks that only a CRLF line end is dropped: any other carriage return is
// part of the mark, as a key may end in one, at the end of input too.
func TestMarkCarriageReturn(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"machine.nodeLabels.key\r\r\nmachine.files\r\n\n", []string{"machine.nodeLabels.key\r", "machine.files"}},
		{"machine.files\r\nmachine.nodeLabels.key\r", []string{"machine.files", "machine.nodeLabels.key\r"}},
		{"machine.files\r\n\r\nnot a mark\n", []string{"machine.files"}},
	} {
		setStdin(t, c.in)
		m, err := (&env{}).readMarks(false)
		if err != nil || !slices.Equal(m, c.want) {
			t.Errorf("%q: %q, %v; want %q", c.in, m, err, c.want)
		}
	}
}

// TestMarkRefusals checks that a mark the CLI cannot send as given is refused by its position,
// never its text (PA §9.3): one that is not UTF-8, which json.Marshal would turn into U+FFFD, and a
// terminal line long enough that canonical mode may have cut it short. The same line from a file
// or a pipe is taken.
func TestMarkRefusals(t *testing.T) {
	long := "machine.files." + strings.Repeat("k", terminalLine)
	for _, c := range []struct {
		name, in string
		terminal bool
		says     string
	}{
		{"not UTF-8", "machine.files\nmachine.nodeLabels.BWSYNTH-\xff\n\n", false, "mark 2 is not UTF-8 text; nothing was sent"},
		{"a long terminal line", "machine.files\n" + long + "\n\n", true, "mark 2 is 4095 bytes or more, which a terminal can cut short"},
		{"one byte under the terminal limit", "machine.files\n" + long[:terminalLine-1] + "\n\n", true, ""},
		// With CR translation off, a cut line can end in a carriage return the user typed mid-line.
		{"a cut terminal line ending in a carriage return", "machine.files\n" + long[:terminalLine-1] + "\r\n\n", true, "mark 2 is 4095 bytes or more"},
		{"a long line from a pipe", "machine.files\n" + long + "\n\n", false, ""},
	} {
		m, err := scanMarks(strings.NewReader(c.in), c.terminal, nil)
		switch {
		case c.says == "" && (err != nil || len(m) != 2):
			t.Errorf("%s: %d marks, %v; want 2 marks", c.name, len(m), err)
		case c.says != "" && (err == nil || !strings.Contains(err.Error(), c.says) || strings.Contains(err.Error(), "BWSYNTH") ||
			strings.Contains(err.Error(), "kkkk")):
			t.Errorf("%s: %d marks, %v; want the refusal %q, with no mark text", c.name, len(m), err, c.says)
		}
	}
}

// TestDocumentTerminalLines checks that a document from a terminal is refused with a line long
// enough that canonical mode may have cut it short, by line number only. From a file or a pipe the
// same document is taken.
func TestDocumentTerminalLines(t *testing.T) {
	long := "BWSYNTH-" + strings.Repeat("k", terminalLine)
	for _, c := range []struct {
		name, doc string
		terminal  bool
		says      string
	}{
		{"a long terminal line", "machine:\n  " + long + ": x\n", true, "line 2 of the document is 4095 bytes or more, which a terminal can cut short"},
		{"a long last line with no line feed", "machine:\n" + long, true, "line 2 of the document is 4095 bytes or more"},
		{"one byte under the terminal limit", "machine:\n" + long[:terminalLine-1] + "\n", true, ""},
		{"a long line from a pipe", "machine:\n  " + long + ": x\n", false, ""},
	} {
		err := terminalLines([]byte(c.doc), c.terminal, "the document")
		switch {
		case c.says == "" && err != nil:
			t.Errorf("%s: %v; want the document taken", c.name, err)
		case c.says != "" && (err == nil || !strings.Contains(err.Error(), c.says) || strings.Contains(err.Error(), "BWSYNTH") ||
			strings.Contains(err.Error(), "kkkk")):
			t.Errorf("%s: %v; want the refusal %q, with no document text", c.name, err, c.says)
		}
	}
}

// TestProtectedOnLinuxOnly checks that the protected commands refuse outside Linux (#256
// BW-256-env-1), where the guard is not yet verified.
func TestProtectedOnLinuxOnly(t *testing.T) {
	if err := protectedOn("linux"); err != nil {
		t.Errorf("linux: %v", err)
	}
	for _, goos := range []string{"darwin", "freebsd", "windows"} {
		if err := protectedOn(goos); err == nil || !strings.Contains(err.Error(), "only on Linux so far; nothing was read or sent") {
			t.Errorf("%s: %v; want the refusal", goos, err)
		}
	}
}

// TestTerminalMarkLineEnds: a mark typed at the prompt ends at a carriage return, a line feed or
// both, because Enter typed ahead while the pager had the terminal raw arrives as a carriage return
// (#546 part 5). From a file or a pipe only the line feed ends it (TestMarkCarriageReturn).
func TestTerminalMarkLineEnds(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"doc[0]/a\r\r", []string{"doc[0]/a"}},
		{"doc[0]/a\r\n\r\n", []string{"doc[0]/a"}},
		{"doc[0]/a\n\n", []string{"doc[0]/a"}},
		{"doc[0]/a\rdoc[0]/b\r\n\n", []string{"doc[0]/a", "doc[0]/b"}},
		{"doc[0]/a\r\rnot a mark\r", []string{"doc[0]/a"}},
		{"doc[0]/a\r", []string{"doc[0]/a"}},
		{"doc[0]/a\rb\n\r\n", []string{"doc[0]/a", "b"}},
	} {
		m, err := scanMarks(strings.NewReader(c.in), true, nil)
		if err != nil || !slices.Equal(m, c.want) {
			t.Errorf("%q: %q, %v; want %q", c.in, m, err, c.want)
		}
	}
}

// TestTerminalMarkLineEndsInChunks: the same bytes give the same marks however the terminal's reads
// cut them, a CRLF cut between its carriage return and its line feed included.
func TestTerminalMarkLineEndsInChunks(t *testing.T) {
	in := "doc[0]/a\r\ndoc[0]/b\r\n\r\nnot a mark\n"
	whole, err := scanMarks(strings.NewReader(in), true, nil)
	if err != nil || !slices.Equal(whole, []string{"doc[0]/a", "doc[0]/b"}) {
		t.Fatalf("whole: %q, %v", whole, err)
	}
	if got, err := scanMarks(iotest.OneByteReader(strings.NewReader(in)), true, nil); err != nil || !slices.Equal(got, whole) {
		t.Errorf("one byte at a time: %q, %v; want %q", got, err, whole)
	}
	if got, err := scanMarks(iotest.HalfReader(strings.NewReader(in)), true, nil); err != nil || !slices.Equal(got, whole) {
		t.Errorf("half reads: %q, %v; want %q", got, err, whole)
	}
}

// readsOf returns the chunks as separate reads, then fails the next read: a scanner that reads
// again, although the marks it has are complete, ends in that error, as a terminal would wait for
// another key. The last chunk comes with io.EOF when eof is set.
type readsOf struct {
	chunks []string
	eof    bool
}

func (r *readsOf) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, errors.New("read again after the marks were complete")
	}
	n := copy(p, r.chunks[0])
	r.chunks = r.chunks[1:]
	if r.eof && len(r.chunks) == 0 {
		return n, io.EOF
	}
	return n, nil
}

// TestTerminalMarkLineEndsSplitCRLF: a read that starts with the line feed of a CRLF is split to
// the end in the same call, so the mark that came with it is taken without a further read, and
// with the end of input too (#546 part 5).
func TestTerminalMarkLineEndsSplitCRLF(t *testing.T) {
	for _, eof := range []bool{false, true} {
		r := &readsOf{chunks: []string{"doc[0]/a\r", "\ndoc[0]/b\r\r"}, eof: eof}
		got, err := scanMarks(r, true, nil)
		if err != nil || !slices.Equal(got, []string{"doc[0]/a", "doc[0]/b"}) {
			t.Errorf("eof=%v: %q, %v; want both marks", eof, got, err)
		}
	}
}

// TestTerminalMarkLimitsInChunks: the limits hold for the bytes as the terminal gave them, however
// the reads cut a CRLF: a mark of terminalLine-1 bytes with its carriage return is refused whole or
// cut, and the line feed that was dropped counts toward the body limit.
func TestTerminalMarkLimitsInChunks(t *testing.T) {
	long := strings.Repeat("a", terminalLine-1) + "\r\n\n"
	ok := strings.Repeat("a", terminalLine-2) + "\r\n\n"
	for name, wrap := range map[string]func(io.Reader) io.Reader{
		"whole": func(r io.Reader) io.Reader { return r },
		"byte":  iotest.OneByteReader,
		"half":  iotest.HalfReader,
	} {
		if _, err := scanMarks(wrap(strings.NewReader(long)), true, nil); err == nil || !strings.Contains(err.Error(), "a terminal can cut short") {
			t.Errorf("%s: %d-byte mark with its CR: %v; want it refused as cut short", name, terminalLine-1, err)
		}
		if got, err := scanMarks(wrap(strings.NewReader(ok)), true, nil); err != nil || len(got) != 1 {
			t.Errorf("%s: %d-byte mark with its CR: %q, %v; want it taken", name, terminalLine-2, got, err)
		}
	}
	// Every byte read is either in a line or counted as dropped.
	in := "a\r\nb\r\n\r\n"
	for name, wrap := range map[string]func(io.Reader) io.Reader{"whole": func(r io.Reader) io.Reader { return r }, "byte": iotest.OneByteReader} {
		dropped := 0
		s := bufio.NewScanner(wrap(strings.NewReader(in)))
		s.Split(terminalMarkLines(&dropped))
		total := 0
		for s.Scan() {
			total += len(s.Bytes()) + dropped
			dropped = 0
		}
		total += dropped // a line feed dropped after the last line
		if total != len(in) {
			t.Errorf("%s: %d of %d bytes accounted for", name, total, len(in))
		}
	}
}

// TestTerminalLineAcrossCarriageReturns: a carriage return typed while the pager had the terminal
// raw ends a mark but not the terminal's line, so the limit counts the whole line, carriage returns
// included (#580). A line feed ends the line, also one dropped from a CRLF that the reads split.
func TestTerminalLineAcrossCarriageReturns(t *testing.T) {
	first := "doc[0]/a\r"
	cut := first + strings.Repeat("k", terminalLine-len(first)) + "\n\n"
	under := first + strings.Repeat("k", terminalLine-len(first)-1) + "\n\n"
	after := first + "\n" + strings.Repeat("k", terminalLine-1) + "\n\n"
	// The terminal keeps the first terminalLine bytes of a line, so a mark that ends before them is
	// whole, though the empty mark after it takes the line to terminalLine.
	whole := strings.Repeat("k", terminalLine-2) + "\r\r\n"
	for name, wrap := range map[string]func(io.Reader) io.Reader{
		"whole": func(r io.Reader) io.Reader { return r },
		"byte":  iotest.OneByteReader, // reads that end at each carriage return, and split each CRLF
	} {
		if _, err := scanMarks(wrap(strings.NewReader(cut)), true, nil); err == nil || !strings.Contains(err.Error(), "mark 2 is 4095 bytes or more") {
			t.Errorf("%s: a %d-byte line split by a CR: %v; want mark 2 refused as cut short", name, terminalLine, err)
		}
		if got, err := scanMarks(wrap(strings.NewReader(under)), true, nil); err != nil || len(got) != 2 {
			t.Errorf("%s: a %d-byte line split by a CR: %q, %v; want both marks", name, terminalLine-1, got, err)
		}
		if got, err := scanMarks(wrap(strings.NewReader(after)), true, nil); err != nil || len(got) != 2 {
			t.Errorf("%s: a %d-byte line after a CRLF: %q, %v; want both marks", name, terminalLine-1, got, err)
		}
		if got, err := scanMarks(wrap(strings.NewReader(whole)), true, nil); err != nil || len(got) != 1 {
			t.Errorf("%s: a mark that ends at byte %d, then an empty mark: %q, %v; want the mark", name, terminalLine-1, got, err)
		}
	}
	if got, err := scanMarks(strings.NewReader(cut), false, nil); err != nil || len(got) != 1 {
		t.Errorf("the same bytes from a pipe: %q, %v; want one mark", got, err)
	}
}
