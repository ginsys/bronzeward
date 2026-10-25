package client

import (
	"strings"
	"testing"
)

// TestLiveSaysItUsesAProxy checks §6 item 7: verbose output says a request goes through a proxy.
func TestLiveSaysItUsesAProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	var b strings.Builder
	Live(&Context{Name: "remote", Server: "https://bronzeward.example.invalid", Credential: "automation"}, &b)
	if !strings.Contains(b.String(), "bronzeward.example.invalid: through the proxy http://127.0.0.1:1") {
		t.Errorf("verbose output %q does not name the proxy", b.String())
	}
}
