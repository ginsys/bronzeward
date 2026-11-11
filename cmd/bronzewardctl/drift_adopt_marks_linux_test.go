//go:build linux

package main

import "testing"

// TestDriftAdoptObject_MarksRefusedBeforeTheIdentityRead: contract lines 950-951: arguments the command
// refuses (exit 2) print nothing. --marks with --no-input on a terminal has no input to read marks
// from, which the command knows before any request, so standard output stays empty in json and yaml.
func TestDriftAdoptObject_MarksRefusedBeforeTheIdentityRead(t *testing.T) {
	f := adoptChainServer(t)
	onPty(t, 24, 80)
	for _, o := range []string{"json", "yaml"} {
		before := len(f.log)
		code, out, errOut := runDrift(t, "-o", o, "--no-input", "drift", "adopt", "mch_BWSYNTH", "--draft", "drf_BWSYNTH", "--marks")
		if code != 2 || out != "" || errOut == "" || len(f.log) != before {
			t.Errorf("-o %s: exit %d, stdout %q, stderr %q, %d requests; want 2, nothing on standard output, the refusal on standard error and no request", o, code, out, errOut, len(f.log)-before)
		}
	}
}
