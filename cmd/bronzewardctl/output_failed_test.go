package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"github.com/ginsys/bronzeward/internal/client"
)

// TestReportOutputFailed: a failed write of the output exits 6 when nothing more specific applies,
// and every more specific code stays when the same error is joined with it. The order of the cases in
// report is the precedence: 130, usage 2, 3, 4, 5, 2, then the server's refusal (1), then 6.
func TestReportOutputFailed(t *testing.T) {
	write := outputFailed{fmt.Errorf("write /dev/stdout: %w", syscall.ENOSPC)}
	refusal := &client.Problem{Status: 403, Doc: map[string]any{"title": "Forbidden"}, Route: "GET /x", Where: "reading x"}
	for _, c := range []struct {
		name string
		err  error
		code int
	}{
		{"alone", write, 6},
		{"inside the error of a command", fmt.Errorf("printing the list: %w", write), 6},
		{"joined with a local failure", errors.Join(errors.New("saving the state failed"), write), 6},
		{"beside a server refusal", errors.Join(refusal, write), 1},
		{"a server refusal alone", refusal, 1},
		{"beside an unknown outcome", errors.Join(outcomeUnknown{errors.New("unknown")}, write), 3},
		{"beside a failed operation", errors.Join(operationFailed{errors.New("failed")}, write), 4},
		{"beside an operation not terminal", errors.Join(notTerminal{errors.New("running")}, write), 5},
		{"beside an approval not confirmed", errors.Join(notConfirmed{errors.New("no")}, write), 2},
		{"beside a stop by Ctrl-C", errors.Join(interrupted{errors.New("stopped")}, write), 130},
		{"beside a usage error", errors.Join(usageError{errors.New("bad flag")}, write), 2},
	} {
		var out, errOut bytes.Buffer
		if got := report(c.err, &out, &errOut); got != c.code {
			t.Errorf("%s: exit %d, want %d (stderr %q)", c.name, got, c.code, errOut.String())
		}
	}
	// The note on standard error is the OS error itself.
	var out, errOut bytes.Buffer
	report(write, &out, &errOut)
	if !strings.Contains(errOut.String(), "no space left on device") || out.Len() != 0 {
		t.Errorf("stdout %q, stderr %q; want the write error on standard error only", out.String(), errOut.String())
	}
}

// failsWith is a writer whose every write fails with err.
type failsWith struct{ err error }

func (w failsWith) Write([]byte) (int, error) { return 0, w.err }

// TestClosedPipeEndsBySIGPIPE: a closed pipe on standard output is not a failed write. Go ends the
// process by SIGPIPE before the write returns, so the command has no exit code and prints no note,
// in -o table and in -o json (operator-experience.md §5.7). It needs a process of its own: the test
// binary starts itself as bronzewardctl (see TestMain), with standard output on a pipe nobody reads.
func TestClosedPipeEndsBySIGPIPE(t *testing.T) {
	for _, args := range []string{"context list", "context list -o json"} {
		pr, pw, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		pr.Close() // the reader stopped reading
		home := t.TempDir()
		cmd := exec.Command(os.Args[0])
		cmd.Env = []string{"BW_PIPE_HELPER=" + args, "HOME=" + home, "XDG_CONFIG_HOME=" + home, "PATH=" + os.Getenv("PATH")}
		cmd.Stdout = pw // an *os.File is the process's descriptor 1, with no copy between
		runErr := cmd.Run()
		pw.Close()
		var exit *exec.ExitError
		if !errors.As(runErr, &exit) || !exit.Sys().(syscall.WaitStatus).Signaled() || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGPIPE {
			t.Errorf("%q on a closed pipe ended with %v; want SIGPIPE, no exit code", args, runErr)
		}
	}
}

// TestTableGuardTagsFailedWrite: standard output is the one place that tags a failed write, in
// -o table (guarded) and in -o json and yaml (raw), and the tag keeps the OS error visible to
// errors.Is. A write that works is not tagged.
func TestTableGuardTagsFailedWrite(t *testing.T) {
	for _, mode := range []string{"table", "json", "yaml"} {
		e := &env{output: mode}
		bad := tableGuard{e, failsWith{syscall.ENOSPC}, failsWith{syscall.ENOSPC}}
		_, err := bad.Write([]byte("x\n"))
		var tagged outputFailed
		if !errors.As(err, &tagged) || !errors.Is(err, syscall.ENOSPC) {
			t.Errorf("-o %s: error %v; want an outputFailed that wraps ENOSPC", mode, err)
		}
		good := tableGuard{e, io.Discard, io.Discard}
		if n, err := good.Write([]byte("x\n")); n != 2 || err != nil {
			t.Errorf("-o %s: a working write gave %d, %v; want 2 and no error", mode, n, err)
		}
	}
}
