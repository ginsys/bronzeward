package discover

import (
	"testing"

	"github.com/ginsys/bronzeward/internal/talos"
)

// TestParseEndpointMatchesTalos holds the copy of the endpoint grammar to the original's answers,
// result and refusal alike, on the inputs that tell the two implementations apart.
func TestParseEndpointMatchesTalos(t *testing.T) {
	for _, in := range []string{
		"10.5.0.5", "10.5.0.5:50000", "10.5.0.5:1", "10.5.0.5:65535", "10.5.0.5:65536", "10.5.0.5:0", "10.5.0.5:080",
		"10.5.0.5:+80", "10.5.0.5:8_0", "10.5.0.5:", "10.5.0.5:80:90", ":80", "", " 10.5.0.5", "10.5.0.5 ", "10.5.0.5/32",
		"[::1]", "[::1]:50000", "[::1]:", "[::1]x", "[::1", "::1", "::1:50000", "[fe80::1%eth0]:50000", "fe80::1%eth0",
		"[10.5.0.5]", "[10.5.0.5]:80", "2001:db8::1", "[2001:db8::1]:6443", "node.example.org", "node.example.org:50000",
		"https://10.5.0.5", "user@10.5.0.5", "10.5.0.5:99999999999999999999", "[::ffff:10.5.0.5]:50000", "0.0.0.0", "256.1.1.1",
	} {
		want, werr := talos.ParseEndpoint(in)
		got, gerr := parseEndpoint(in)
		if got != want || (gerr == nil) != (werr == nil) {
			t.Errorf("%q: got %q, %v; the original gives %q, %v", in, got, gerr, want, werr)
		}
	}
}
