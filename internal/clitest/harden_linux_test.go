package clitest

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"
)

// checkHardened checks that the CLI, waiting for a token on standard input, has no core size and
// is not dumpable (client-contracts.md §11.2 item 5). A process that is not dumpable has its /proc
// environment owned by root, which root itself can still read, so that part needs another user.
func (f *fixture) checkHardened() {
	f.t.Helper()
	cmd := f.command(nil, "whoami", "--token-stdin")
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
