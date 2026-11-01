package main

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// pty opens a pseudo-terminal pair in canonical mode with echo off and returns its controller and
// the path of its terminal.
func pty(t *testing.T) (*os.File, string) {
	t.Helper()
	ctl, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { ctl.Close() })
	ioctl := func(req uintptr, arg unsafe.Pointer) {
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, ctl.Fd(), req, uintptr(arg)); errno != 0 {
			t.Fatalf("ioctl %#x: %v", req, errno)
		}
	}
	var unlock, n uint32
	ioctl(syscall.TIOCSPTLCK, unsafe.Pointer(&unlock))
	ioctl(syscall.TIOCGPTN, unsafe.Pointer(&n))
	var tio syscall.Termios
	ioctl(syscall.TCGETS, unsafe.Pointer(&tio))
	tio.Lflag &^= syscall.ECHO
	ioctl(syscall.TCSETS, unsafe.Pointer(&tio))
	return ctl, fmt.Sprintf("/dev/pts/%d", n)
}

// TestStdinByIdentity checks that an input path naming standard input under another name takes
// standard input as - does: marks are then not read from it, and no second input may read it. The
// path is /proc/self/fd/N for the test's pipe, the link /dev/stdin is for fd 0.
func TestStdinByIdentity(t *testing.T) {
	f := &draftFake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	f.rev, f.state = 3, "open"
	// The draft's ETag is kept, so only the refusal stops the send.
	var o, e bytes.Buffer
	if got := run([]string{"draft", "show", "drf_BWSYNTH"}, &o, &e); got != 0 {
		t.Fatalf("draft show: exit %d, stderr %q", got, e.String())
	}
	doc := filepath.Join(t.TempDir(), "doc.yaml")
	if err := os.WriteFile(doc, []byte("machine:\n  nodeLabels:\n    BWSYNTH-doc: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const notFree = "--marks reads from a prompt on a terminal or from standard input, and neither is free here; nothing was sent"
	for _, c := range []struct {
		name, input string
		args        func(alias string) []string
		says        string
	}{
		{"--file names standard input, with --marks", "machine:\n  nodeLabels:\n    BWSYNTH-doc: x\n",
			func(a string) []string { return []string{"--file", a, "--marks", "--no-input"} }, notFree},
		{"--file names standard input, with --token-stdin", "machine:\n  nodeLabels:\n    BWSYNTH-doc: x\n",
			func(a string) []string { return []string{"--file", a, "--token-stdin"} }, "--file and --token-stdin read standard input, which only one of them can"},
		{"--declarations names standard input, with --marks", "embedded: []\n",
			func(a string) []string { return []string{"--file", doc, "--declarations", a, "--marks", "--no-input"} }, notFree},
		{"--declarations names standard input, with --file -", "embedded: []\n",
			func(a string) []string { return []string{"--file", "-", "--declarations", a} }, "--file and --declarations read standard input, which only one of them can"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.WriteString(c.input); err != nil {
				t.Fatal(err)
			}
			w.Close()
			old := stdin
			stdin = r
			t.Cleanup(func() { stdin = old; r.Close() })
			var o, e bytes.Buffer
			got := run(append([]string{"fragment", "set", "worker-labels", "--draft", "drf_BWSYNTH", "--layer", "role"},
				c.args(fmt.Sprintf("/proc/self/fd/%d", r.Fd()))...), &o, &e)
			if got != 2 || !strings.Contains(e.String(), c.says) {
				t.Errorf("exit %d, stderr %q; want 2 and %q", got, e.String(), c.says)
			}
			if took := f.took(); len(took) != 0 {
				t.Errorf("sent %v", took)
			}
		})
	}
}

// TestTerminalInputs checks every input fragment set reads, each from a real terminal: a line that
// canonical mode cut short is refused, by its position only, and nothing is sent. The check is on
// the file opened, so a terminal named by its path is refused as standard input is. Each cut input
// is still valid, so without the check it would be sent.
func TestTerminalInputs(t *testing.T) {
	f := &draftFake{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	automationContext(t, srv.URL)
	f.rev, f.state = 3, "open"
	// The draft's ETag is kept, so only the refusal stops the send.
	var o, e bytes.Buffer
	if got := run([]string{"draft", "show", "drf_BWSYNTH"}, &o, &e); got != 0 {
		t.Fatalf("draft show: exit %d, stderr %q", got, e.String())
	}
	doc := filepath.Join(t.TempDir(), "doc.yaml")
	if err := os.WriteFile(doc, []byte("machine:\n  nodeLabels:\n    BWSYNTH-doc: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The kernel keeps 4095 bytes of the long line and drops the rest. Ctrl-D at the start of a line
	// ends the input, and an empty line ends the marks.
	long := "BWSYNTH-" + strings.Repeat("k", 5000)
	for _, c := range []struct {
		name  string
		args  func(tty string) []string
		stdin bool
		input string
		says  string
	}{
		{"--file names the terminal", func(tty string) []string { return []string{"--file", tty} }, false,
			"machine:\n  " + long + ": x\n\x04", "line 2 of the document is 4095 bytes or more, which a terminal can cut short"},
		{"--file - on a terminal", func(string) []string { return []string{"--file", "-"} }, true,
			"machine:\n  " + long + ": x\n\x04", "line 2 of the document is 4095 bytes or more, which a terminal can cut short"},
		{"--declarations names the terminal", func(tty string) []string { return []string{"--file", doc, "--declarations", tty} }, false,
			"embedded:\n- path: machine." + long + "\n  format: yaml\n\x04", "line 2 of the declarations file is 4095 bytes or more, which a terminal can cut short"},
		{"--marks from a terminal", func(string) []string { return []string{"--file", doc, "--marks"} }, true,
			"machine.files\nmachine." + long + "\n\n", "mark 2 is 4095 bytes or more, which a terminal can cut short"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctl, tty := pty(t)
			if c.stdin {
				in, err := os.OpenFile(tty, os.O_RDWR|syscall.O_NOCTTY, 0)
				if err != nil {
					t.Fatal(err)
				}
				old := stdin
				stdin = in
				t.Cleanup(func() { stdin = old; in.Close() })
			}
			if _, err := ctl.WriteString(c.input); err != nil {
				t.Fatal(err)
			}
			var o, e bytes.Buffer
			got := run(append([]string{"fragment", "set", "worker-labels", "--draft", "drf_BWSYNTH", "--layer", "role"}, c.args(tty)...), &o, &e)
			if got != 1 || !strings.Contains(e.String(), c.says) || strings.Contains(e.String(), "BWSYNTH-") {
				t.Errorf("exit %d, stderr %q; want 1 and %q, with no input text", got, e.String(), c.says)
			}
			if took := f.took(); len(took) != 0 {
				t.Errorf("sent %v", took)
			}
		})
	}
}
