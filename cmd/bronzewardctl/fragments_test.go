package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		"--file - and --token-stdin both read standard input")
	step("no --file", set, 2, "", "fragment set needs --draft, --layer and --file")
	step("a document over 1 MiB", append(set, "--file", write("big", strings.Repeat("a", maxBody+1))), 1, "",
		"the document is over the server's 1 MiB body limit; nothing was sent")
	step("a body over 1 MiB once encoded", append(set, "--file", write("quotes", strings.Repeat(`"`, maxBody/2+1))), 1, "",
		"over the server's 1 MiB limit; nothing was sent")
	step("a document that is not UTF-8", append(set, "--file", write("bin", "\xff\xfe")), 1, "", "the document is not UTF-8 text; nothing was sent")
	// The decoders quote member names and some values; the refusals quote nothing from the file.
	for _, c := range []struct{ name, file, says string }{
		{"an unknown declarations member", "BWSYNTH-member: [x]\n", "a member the API does not define, or a value of the wrong type; nothing was sent"},
		{"a number where the API takes a string", "embedded:\n- {path: 4242424242, format: yaml}\n", "a member the API does not define, or a value of the wrong type; nothing was sent"},
		{"a number as a member name", "embedded: []\n4242424242: x\n", "a member the API does not define, or a value of the wrong type; nothing was sent"},
		{"a member name YAML cannot hold", "? [BWSYNTH-key, 4242424242]\n: x\n", "not YAML or JSON; nothing was sent"},
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
