package talos

import "testing"

func TestParseEndpoint(t *testing.T) {
	for in, want := range map[string]string{
		"10.55.0.3":               "10.55.0.3:50000",
		"10.55.0.3:50000":         "10.55.0.3:50000",
		"10.55.0.3:1":             "10.55.0.3:1",
		"10.55.0.3:65535":         "10.55.0.3:65535",
		"[fd00::3]":               "[fd00::3]:50000",
		"[fd00::3]:50001":         "[fd00::3]:50001",
		"[FD00:0::3]":             "[fd00::3]:50000",
		"[::ffff:10.55.0.3]:5000": "[::ffff:10.55.0.3]:5000",
	} {
		got, err := ParseEndpoint(in)
		if err != nil || got != want {
			t.Errorf("ParseEndpoint(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestParseEndpointRefuses(t *testing.T) {
	for name, in := range map[string]string{
		"empty":                "",
		"dns name":             "node1.example.org",
		"dns name with port":   "node1.example.org:50000",
		"localhost":            "localhost",
		"unbracketed ipv6":     "fd00::3",
		"unbracketed ipv6 end": "fd00::3:50000",
		"ipv6 zone":            "[fe80::1%eth0]",
		"bracketed ipv4":       "[10.55.0.3]",
		"scheme":               "https://10.55.0.3:50000",
		"tcp scheme":           "tcp://10.55.0.3",
		"path":                 "10.55.0.3:50000/x",
		"user part":            "admin@10.55.0.3",
		"leading space":        " 10.55.0.3",
		"trailing space":       "10.55.0.3 ",
		"inner space":          "10.55.0.3: 50000",
		"newline":              "10.55.0.3\n",
		"port 0":               "10.55.0.3:0",
		"port too big":         "10.55.0.3:65536",
		"port negative":        "10.55.0.3:-1",
		"port not a number":    "10.55.0.3:http",
		"empty port":           "10.55.0.3:",
		"port leading plus":    "10.55.0.3:+50000",
		"port leading zero":    "10.55.0.3:050000",
		"ipv4 leading zero":    "10.055.0.3",
		"short ipv4":           "10.55.3",
		"two colons":           "10.55.0.3:50000:1",
		"empty brackets":       "[]:50000",
		"unclosed bracket":     "[fd00::3:50000",
	} {
		if got, err := ParseEndpoint(in); err == nil {
			t.Errorf("%s: ParseEndpoint(%q) = %q, want a refusal", name, in, got)
		}
	}
}
