//go:build fixtureinterrupt

package seam

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Control is the file the fixture writes to choose the point: one line, "<step> kill" or
// "<step> stall". It is read at every call, so the fixture changes it between runs without
// restarting the instance.
var Control = "/etc/bronzeward/interrupt"

// At interrupts the process at step when Control names it: kill ends the process at once with
// status 137, nothing deferred run and no shutdown begun, as an operator's kill -9 would end it
// (a signal would not: the kernel ignores a SIGKILL that a container's PID 1 sends itself); stall
// blocks the calling goroutine, the rest of the process (its heartbeat included) running on, until
// the file no longer names the step. Each says where on stderr first.
func At(step string) {
	action, ok := wanted(step)
	if !ok {
		return
	}
	fmt.Fprintf(os.Stderr, "fixture interrupt: %s at %s\n", action, step)
	if action == "kill" {
		os.Exit(137)
	}
	for {
		time.Sleep(50 * time.Millisecond)
		if _, ok := wanted(step); !ok {
			fmt.Fprintf(os.Stderr, "fixture interrupt: released at %s\n", step)
			return
		}
	}
}

// wanted is the action Control names for step, if it names step and a known action.
func wanted(step string) (string, bool) {
	b, err := os.ReadFile(Control)
	if err != nil {
		return "", false
	}
	f := strings.Fields(string(b))
	if len(f) != 2 || f[0] != step || (f[1] != "kill" && f[1] != "stall") {
		return "", false
	}
	return f[1], true
}
