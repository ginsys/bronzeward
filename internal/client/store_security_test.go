package client

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests run on every host: the tool is a shell script in the test's own directory, with
// synthetic output, so none reaches the system's security tool (ginsys/bronzeward#662).

// writeFakeTool writes an executable shell script that stands in for the security tool and returns
// its path. The script gets the tool's arguments and its standard input as the real tool does.
func writeFakeTool(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-security")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSecurityRunKeepsStandardErrorApart checks that a warning on standard error from a run that
// exited 0 is no part of the output a read takes as its value.
func TestSecurityRunKeepsStandardErrorApart(t *testing.T) {
	tool := writeFakeTool(t, `echo "BWSYNTH-value"; echo "BWSYNTH-warning" >&2`)
	out, err := runTool(context.Background(), tool, "", false, "find-generic-password")
	if err != nil || string(out) != "BWSYNTH-value\n" {
		t.Errorf("a run that exited 0 with a warning: %q, %v, want its standard output alone", out, err)
	}
}

// TestSecurityRunInputJoinsBothStreams checks that a security -i run keeps both streams, because
// that call reports a failed command in-band and exits 0.
func TestSecurityRunInputJoinsBothStreams(t *testing.T) {
	tool := writeFakeTool(t, `echo "BWSYNTH-out"; echo "security: BWSYNTH-failed" >&2`)
	out, err := runTool(context.Background(), tool, "", securityReportsInBand([]string{"-i"}), "-i")
	if err != nil || !strings.Contains(string(out), "BWSYNTH-out") || !strings.Contains(string(out), "security: BWSYNTH-failed") {
		t.Errorf("a security -i run that exited 0: %q, %v, want both streams", out, err)
	}
}

// TestSecurityReportsInBandOnlyForInput checks which calls keep the error text of a successful run.
func TestSecurityReportsInBandOnlyForInput(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"-i"}, true},
		{[]string{"find-generic-password", "-s", "x", "-w", "/k"}, false},
		{[]string{"default-keychain", "-d", "user"}, false},
		{[]string{"show-keychain-info", "/k"}, false},
		{[]string{"delete-generic-password", "-s", "x", "/k"}, false},
		{nil, false},
	} {
		if got := securityReportsInBand(c.args); got != c.want {
			t.Errorf("%v: %v, want %v", c.args, got, c.want)
		}
	}
}

// TestSecurityRunFailureKeepsStandardErrorOnly checks that a run that failed returns its standard
// error alone: a read prints the secret on standard output, and no error text may carry it.
func TestSecurityRunFailureKeepsStandardErrorOnly(t *testing.T) {
	tool := writeFakeTool(t, `echo "BWSYNTH-secret"; echo "BWSYNTH-error" >&2; exit 3`)
	out, err := runTool(context.Background(), tool, "", false, "find-generic-password")
	if err == nil || string(out) != "BWSYNTH-error\n" {
		t.Errorf("a run that failed: %q, %v, want its standard error alone and an error", out, err)
	}
}

// TestSecurityRunPassesStandardInput checks that the tool reads what the caller sends.
func TestSecurityRunPassesStandardInput(t *testing.T) {
	tool := writeFakeTool(t, `cat`)
	out, err := runTool(context.Background(), tool, "BWSYNTH-line\n", false, "-i")
	if err != nil || string(out) != "BWSYNTH-line\n" {
		t.Errorf("standard input: %q, %v, want it echoed", out, err)
	}
}

// TestSecurityRunStopsAtTheDeadline checks that a tool which does not answer is killed and reported
// as a timeout.
func TestSecurityRunStopsAtTheDeadline(t *testing.T) {
	tool := writeFakeTool(t, `exec sleep 30`)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := runTool(ctx, tool, "", false, "show-keychain-info"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a tool that does not answer: %v, want context.DeadlineExceeded", err)
	}
}

// TestSecurityOutcomeFollowsTheExitStatus checks that a tool which exited 0 is no timeout, although
// Run returned the context's error, and that a killed tool is one.
func TestSecurityOutcomeFollowsTheExitStatus(t *testing.T) {
	done := exec.Command("true")
	if err := done.Run(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out, err := securityOutcome(ctx, done.ProcessState, []byte("ok"), nil, false, context.Canceled); err != nil || string(out) != "ok" {
		t.Errorf("a tool that exited 0 after the deadline: %q, %v, want its output and no error", out, err)
	}
	killed := exec.Command("sleep", "60")
	if err := killed.Start(); err != nil {
		t.Fatal(err)
	}
	_ = killed.Process.Kill()
	_ = killed.Wait()
	if _, err := securityOutcome(ctx, killed.ProcessState, nil, nil, false, errors.New("signal: killed")); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a tool killed at the deadline: %v, want context.DeadlineExceeded", err)
	}
}
