//go:build !fixtureinterrupt

package api

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// The default build carries no fault hook: its test binary, which links this package and
// internal/seam, holds none of the strings the interrupt build prints or reads. The needles are
// joined at run time, so this file's own literals are not what the scan finds.
func TestDefaultBuildHoldsNoInterruptHook(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	join := func(p ...string) []byte { return []byte(strings.Join(p, "")) }
	// A positive control first: a string the package always carries, or an empty result proves nothing.
	if control := join("the run ", "stops"); !bytes.Contains(bin, control) {
		t.Fatalf("the control string %q is not in the test binary, so its scan reads nothing", control)
	}
	for _, needle := range [][]byte{
		join("fixture inter", "rupt: "),
		join("BW_INTERRUPT", "_CONTROL"),
		join("lose", "-reply"),
		join("/etc/bronzeward/", "interrupt"),
	} {
		if bytes.Contains(bin, needle) {
			t.Errorf("the default build holds %q", needle)
		}
	}
}
