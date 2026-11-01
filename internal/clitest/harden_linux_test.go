package clitest

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"
)

// checkHardened checks that the CLI, waiting for a token on standard input, has no core size and
// is not dumpable (client-contracts.md §11.2 item 5).
func (f *fixture) checkHardened() {
	f.t.Helper()
	f.hardened(nil, "whoami", "--token-stdin")
}

// hardened starts bronzewardctl with env and args, and checks, while it waits on standard input,
// that its core size limit is 0 and its /proc environment refuses the read: a process that is not
// dumpable has it owned by root. Root itself can still read it, so that part needs another user.
func (f *fixture) hardened(env []string, args ...string) {
	f.t.Helper()
	cmd := f.command(env, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		f.t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		f.t.Fatal(err)
	}
	defer cmd.Wait()
	defer stdin.Close()
	proc := fmt.Sprint("/proc/", cmd.Process.Pid)
	zero := regexp.MustCompile(`Max core file size +0 +0 `)
	var limits []byte
	for i := 0; i < 100 && !zero.Match(limits); i++ {
		time.Sleep(50 * time.Millisecond)
		limits, _ = os.ReadFile(proc + "/limits")
	}
	if !zero.Match(limits) {
		f.t.Errorf("the waiting CLI's limits:\n%s", limits)
	}
	if _, err := os.ReadFile(proc + "/environ"); os.Geteuid() != 0 && !errors.Is(err, os.ErrPermission) {
		f.t.Errorf("the waiting CLI's environment read: %v, want a permission error", err)
	}
}

// TestFragmentHardened checks the guard (decision T13 of #256) where a protected input is read:
// fragment set, started with GOTRACEBACK=all and waiting for its document on standard input, has
// no core size and is not dumpable. That the traceback level is none after the re-exec is
// cmd/bronzewardctl TestHarden's: a same-user test cannot read the environment of a process that
// is not dumpable.
func TestFragmentHardened(t *testing.T) {
	f := start(t, "viewer,author")
	f.want(f.ctl(nil, "", "context", "add", "lab", "--server", f.base, "--automation-token-file", f.tokenFile), 0)
	f.hardened([]string{"GOTRACEBACK=all"}, "fragment", "set", "worker-labels", "--draft", "drf_BWSYNTH", "--layer", "role", "--file", "-")
}
