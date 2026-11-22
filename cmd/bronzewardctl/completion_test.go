package main

import (
	"strings"
	"testing"
)

// TestCompletionListsTheTable: each shell's script holds every top-level command, every verb of every group
// and every flag help shows for a leaf, and none of the flags a leaf refuses. The shells themselves are tried
// by scripts/test/completion-shells-test.sh.
func TestCompletionListsTheTable(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		code, out, errOut := runDrift(t, "completion", shell)
		if code != 0 || errOut != "" {
			t.Fatalf("completion %s: exit %d, stderr %q", shell, code, errOut)
		}
		for _, name := range topLevel() {
			if !strings.Contains(out, name) {
				t.Errorf("completion %s lacks the command %s", shell, name)
			}
		}
		for _, x := range cmdTable {
			if x.synopsis == "" {
				for _, v := range verbsOf(x.path) {
					if !strings.Contains(out, v) {
						t.Errorf("completion %s lacks the verb %s of %s", shell, v, x.path)
					}
				}
				continue
			}
			fs, err := probeFlags(x.path)
			if err != nil {
				t.Fatalf("%s: %v", x.path, err)
			}
			for _, f := range visibleFlags(&x, fs) {
				if !strings.Contains(out, f.Name) {
					t.Errorf("completion %s lacks --%s of %s", shell, f.Name, x.path)
				}
			}
		}
	}
	// A flag a leaf refuses is in no leaf's own list: cluster onboard registers --no-wait to refuse it.
	_, bash, _ := runDrift(t, "completion", "bash")
	for _, line := range strings.Split(bash, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), `"cluster onboard")`) && strings.Contains(line, "--no-wait") {
			t.Errorf("the bash list of cluster onboard offers --no-wait, which it refuses: %s", line)
		}
	}
}

// TestCompletionNeedsAShell: no shell, and one that has no script, are usage errors with the help of completion.
func TestCompletionNeedsAShell(t *testing.T) {
	for _, args := range [][]string{{"completion"}, {"completion", "powershell"}, {"completion", "bash", "zsh"}} {
		code, out, errOut := runDrift(t, args...)
		if code != 2 || out != "" || !strings.Contains(errOut, "usage: bronzewardctl completion bash|zsh|fish") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}
