package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestHelpOutputFailed: --help prints the usage text, which is output like any other. When standard
// output refuses it, the note names the OS error and the exit is 6, not the 0 of a help that was shown.
func TestHelpOutputFailed(t *testing.T) {
	var errb bytes.Buffer
	if code := run([]string{"--help"}, failsWith{syscall.ENOSPC}, &errb); code != 6 || !strings.Contains(errb.String(), "no space left on device") {
		t.Errorf("--help on a full disk: exit %d, stderr %q; want 6 and the OS error named", code, errb.String())
	}
	var out bytes.Buffer
	errb.Reset()
	if code := run([]string{"--help"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "usage: bronzewardctl") || errb.Len() != 0 {
		t.Errorf("--help on a working terminal: exit %d, stdout %q, stderr %q; want 0 and the usage text", code, out.String(), errb.String())
	}
}

// partialWriter takes one byte of its first write and then fails it; every later write works. It is
// the shape of a table whose first flush fails and whose later flushes find the writer healthy.
type partialWriter struct {
	calls int
	got   bytes.Buffer
}

func (w *partialWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == 1 {
		w.got.WriteByte(p[0])
		return 1, errors.New("write refused")
	}
	w.got.Write(p)
	return len(p), nil
}

// TestTableKeepsFirstWriteError: a table is written through a tabwriter, which flushes in pieces and
// returns an error from each flush after the first one, so its own error can repeat the prefix of what
// was already written. newTable keeps the first write error and stops writing after it: the flush
// returns that error alone, and the sink holds exactly the bytes of the failed write.
func TestTableKeepsFirstWriteError(t *testing.T) {
	pw := &partialWriter{}
	e := &env{out: pw}
	w, flush := e.newTable()
	_, _ = w.Write([]byte("a\tb\nc\nd\te\n"))
	err := flush()
	if err == nil || err.Error() != "write refused" {
		t.Errorf("flush returned %v; want the first write error alone", err)
	}
	if pw.got.Len() != 1 || pw.calls != 1 {
		t.Errorf("the sink got %d bytes in %d writes; want 1 byte in 1 write (nothing after the failure)", pw.got.Len(), pw.calls)
	}
	ok := &partialWriter{calls: 1}
	e = &env{out: ok}
	w, flush = e.newTable()
	_, _ = w.Write([]byte("a\tb\n"))
	if err := flush(); err != nil || ok.got.Len() == 0 {
		t.Errorf("a healthy sink: flush returned %v, sink holds %q; want no error and the table", err, ok.got.String())
	}
}

// TestPageOutTagsFailedWrite: a write the terminal refuses through the pager's output is a failed
// output, tagged like the table guard tags it, so the exit is 6 and the OS error stays visible.
func TestPageOutTagsFailedWrite(t *testing.T) {
	o := &pageOut{w: failsWith{syscall.EIO}}
	_, err := o.Write([]byte("x\n"))
	var tagged outputFailed
	if !errors.As(err, &tagged) || !errors.Is(err, syscall.EIO) {
		t.Fatalf("error %v; want an outputFailed that wraps EIO", err)
	}
	var out, errOut bytes.Buffer
	if code := report(err, &out, &errOut); code != 6 {
		t.Errorf("exit %d (stderr %q); want 6", code, errOut.String())
	}
}

// TestContextRemoveOutputFailedBesideCleanup: context remove deletes the context and then cleans up
// local state. When the cleanup fails, the line that says so is still output; if standard output
// refuses it, the write error stands beside the cleanup's and the exit is 6, with the OS error named.
// With a working standard output the cleanup failure alone is the error (exit 1).
func TestContextRemoveOutputFailedBesideCleanup(t *testing.T) {
	for _, c := range []struct {
		name string
		out  func() (w *bytes.Buffer, fails bool)
		code int
	}{
		{"standard output works", func() (*bytes.Buffer, bool) { return &bytes.Buffer{}, false }, 1},
		{"standard output full", func() (*bytes.Buffer, bool) { return nil, true }, 6},
	} {
		automationContext(t, "https://bw.example.invalid")
		state := filepath.Join(os.Getenv("XDG_STATE_HOME"), "bronzeward")
		if err := os.MkdirAll(state, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(state, "state.yaml"), []byte("contexts: [unclosed\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var errb bytes.Buffer
		buf, fails := c.out()
		var code int
		if fails {
			code = run([]string{"context", "remove", "lab"}, failsWith{syscall.ENOSPC}, &errb)
		} else {
			code = run([]string{"context", "remove", "lab"}, buf, &errb)
		}
		if code != c.code {
			t.Errorf("%s: exit %d (stderr %q); want %d", c.name, code, errb.String(), c.code)
		}
		if fails && !strings.Contains(errb.String(), "no space left on device") {
			t.Errorf("%s: stderr %q; want the write error named", c.name, errb.String())
		}
	}
}
