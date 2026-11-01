package main

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"os"
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

// TestDocumentFromTerminalFile checks that a document read from a terminal is refused with a line
// that canonical mode cut short, whatever name --file gives the terminal: the check is on the file
// opened, not on "-".
func TestDocumentFromTerminalFile(t *testing.T) {
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
	o.Reset()
	e.Reset()
	ctl, tty := pty(t)
	// The kernel keeps 4095 bytes of the long line and drops the rest. Ctrl-D at the start of a line
	// ends the input.
	if _, err := ctl.WriteString("machine:\n  BWSYNTH-" + strings.Repeat("k", 5000) + ": x\n\x04"); err != nil {
		t.Fatal(err)
	}
	got := run([]string{"fragment", "set", "worker-labels", "--draft", "drf_BWSYNTH", "--layer", "role", "--file", tty}, &o, &e)
	if want := "line 2 of the document is 4095 bytes or more, which a terminal can cut short"; got != 1 || !strings.Contains(e.String(), want) ||
		strings.Contains(e.String(), "BWSYNTH-") {
		t.Errorf("exit %d, stderr %q; want 1 and %q, with no document text", got, e.String(), want)
	}
	if took := f.took(); len(took) != 0 {
		t.Errorf("sent %v", took)
	}
}
