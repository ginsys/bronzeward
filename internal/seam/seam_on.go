//go:build fixtureinterrupt

package seam

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Control is the file the fixture writes to choose the point: one line, "<step> kill",
// "<step> stall" or "<step> lose-reply". It is read at every call, so the fixture changes it between
// runs without restarting the instance. A process test, which cannot write under /etc, names its
// own file in BW_INTERRUPT_CONTROL.
var Control = controlFile()

func controlFile() string {
	if f := os.Getenv("BW_INTERRUPT_CONTROL"); f != "" {
		return f
	}
	return "/etc/bronzeward/interrupt"
}

// Lost reports whether Control names step with lose-reply: the step's work is done and committed,
// and the caller answers as if the reply to its COMMIT was lost. The recovery entry is the one step
// that asks.
func Lost(step string) bool {
	action, ok := wanted(step)
	if ok && action == "lose-reply" {
		fmt.Fprintf(os.Stderr, "fixture interrupt: lose-reply at %s\n", step)
	}
	return ok && action == "lose-reply"
}

// At interrupts the process at step when Control names it: kill ends the process at once with
// status 137, nothing deferred run and no shutdown begun, as an operator's kill -9 would end it
// (a signal would not: the kernel ignores a SIGKILL that a container's PID 1 sends itself); stall
// blocks the calling goroutine, the rest of the process (its heartbeat included) running on, until
// the file no longer names the step. Each says where on stderr first.
func At(step string) {
	action, ok := wanted(step)
	if !ok || action == "lose-reply" { // lose-reply is read by Lost, not here
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
	if len(f) != 2 || f[0] != step || (f[1] != "kill" && f[1] != "stall" && f[1] != "lose-reply") {
		return "", false
	}
	return f[1], true
}
