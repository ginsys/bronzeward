package client

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// How the output of one run of the macOS security tool is read (ginsys/bronzeward#662). The file has
// no build tag so that its tests run on every host; only the macOS Keychain store calls it.

// securityReportsInBand reports whether a call is the one that reports a failed command in its own
// output: security -i runs the command it reads on standard input, and when that command fails the
// tool prints the error and may still exit 0. Its caller needs the error text of a successful run.
// Which stream carries that text on a real Mac is not known, so the call keeps both streams.
func securityReportsInBand(args []string) bool { return len(args) == 1 && args[0] == "-i" }

// runTool runs tool with stdin and returns its output. At the deadline it kills the tool and returns
// context.DeadlineExceeded; a tool that exited 0 is no failure, whatever the clock says. A run that
// failed returns the tool's standard error only: a read prints the secret on standard output, and no
// error text may carry it. A run that succeeded returns its standard output alone, so that a warning
// on standard error never becomes part of a value that is read. With joined it returns both streams.
func runTool(ctx context.Context, tool, stdin string, joined bool, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.WaitDelay = time.Second
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	return securityOutcome(ctx, cmd.ProcessState, out.Bytes(), errOut.Bytes(), joined, err)
}

// securityOutcome is the result of one run, given the state of the tool's process. The exit status
// of the tool decides: Run returns the context's error even for a tool that exited 0, when the
// deadline came while the tool was being reaped, and a write that was carried out is no timeout.
func securityOutcome(ctx context.Context, state *os.ProcessState, out, errOut []byte, joined bool, err error) ([]byte, error) {
	switch {
	case state != nil && state.Success():
		if joined {
			return append(out, errOut...), nil
		}
		return out, nil
	case ctx.Err() != nil:
		return errOut, context.DeadlineExceeded
	}
	return errOut, err
}
