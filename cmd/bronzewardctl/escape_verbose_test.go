package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestVerboseProxyIsEscaped: with -v, every command that sends a request names the proxy it goes
// through, and the proxy URL is text from the environment: here it holds a C1 control and a
// right-to-left override. The text passes the guard on standard error, and no command shows it raw
// (client-contracts.md section 6 item 8). The server name is not loopback, so Go uses the proxy;
// TestMain's proxy refuses at once.
func TestVerboseProxyIsEscaped(t *testing.T) {
	automationContext(t, "https://bw.example.invalid")
	want := `BWSYNTH\x9b` + `\u` + `202eEND`
	var quiet []string
	for _, c := range commands {
		var o, e bytes.Buffer
		run(append([]string{"-v"}, c...), &o, &e)
		name := strings.Join(c, " ")
		noRawControl(t, name, o.String()+e.String())
		if !strings.Contains(e.String(), "through the proxy") {
			quiet = append(quiet, name)
		} else if !strings.Contains(e.String(), want) {
			t.Errorf("%s: the -v output %q does not show the proxy URL escaped as %q", name, e.String(), want)
		}
	}
	// A command that sends no request has no proxy to name.
	if got := strings.Join(quiet, ","); got != "context list" {
		t.Errorf("commands that named no proxy: %q, want only context list", got)
	}
}
