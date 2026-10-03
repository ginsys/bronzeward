//go:build fixtureinterrupt

package seam

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The kill case runs in a child process: the test binary again, told by the environment to call
// At and exit 0 if it returns.
func TestMain(m *testing.M) {
	if f := os.Getenv("BW_SEAM_CHILD_CONTROL"); f != "" {
		Control = f
		At(os.Getenv("BW_SEAM_CHILD_STEP"))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func control(t *testing.T, content string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "interrupt")
	if err := os.WriteFile(f, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func child(t *testing.T, file, step string) (*exec.ExitError, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "BW_SEAM_CHILD_CONTROL="+file, "BW_SEAM_CHILD_STEP="+step)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		t.Fatal(err)
	}
	return ee, stderr.String()
}

// The step the control file names, with kill, ends the process at once with status 137, saying
// where first: not by a signal, which a container's PID 1 cannot send itself.
func TestKill(t *testing.T) {
	ee, stderr := child(t, control(t, "guard kill\n"), "guard")
	if ee == nil {
		t.Fatal("the process returned from At")
	}
	if ws, ok := ee.Sys().(syscall.WaitStatus); !ok || !ws.Exited() || ws.ExitStatus() != 137 {
		t.Fatalf("the process ended %v, not by exiting 137", ee)
	}
	if !strings.Contains(stderr, "fixture interrupt: kill at guard\n") {
		t.Fatalf("stderr %q", stderr)
	}
}

// Any other step, an absent file, or a file that is not "<step> kill|stall" is no interruption.
func TestNoInterruption(t *testing.T) {
	for name, content := range map[string]string{
		"another step":   "parse kill\n",
		"another action": "guard pause\n",
		"extra fields":   "guard kill now\n",
		"empty":          "",
	} {
		ee, stderr := child(t, control(t, content), "guard")
		if ee != nil || stderr != "" {
			t.Errorf("%s: %v, stderr %q", name, ee, stderr)
		}
	}
	if ee, stderr := child(t, filepath.Join(t.TempDir(), "absent"), "guard"); ee != nil || stderr != "" {
		t.Errorf("absent file: %v, stderr %q", ee, stderr)
	}
}

// stall blocks at the step until the file no longer names it.
func TestStall(t *testing.T) {
	Control = control(t, "staged stall\n")
	done := make(chan struct{})
	go func() {
		At("staged")
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("At returned while the file named the step")
	case <-time.After(300 * time.Millisecond):
	}
	if err := os.Remove(Control); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("At did not return once the file was removed")
	}
}
