package talos

import (
	"encoding/base64"
	"strings"
	"testing"
)

// PA §7.3: a Talos cluster ID is 32 bytes in padded base64. Talos before v1.14 prints the URL-safe
// alphabet and v1.14 and later the standard one, and CanonicalClusterID gives both the standard
// spelling, so one cluster is one stored value. Anything else is refused, never repaired.
func TestCanonicalClusterID(t *testing.T) {
	const (
		std     = "8TMwqXnWOTdw7xFDHSn+f6JMbBQrSWAuyzCfGIRVSL0="
		urlSafe = "8TMwqXnWOTdw7xFDHSn-f6JMbBQrSWAuyzCfGIRVSL0="
		// both holds "+" and "-" and ends in a valid final character, so only the mix refuses it:
		// reading "-" as "+" would give a value that encodes back to itself.
		both  = "8TMwqXnWOTdw7xFDHSn+f6JMbBQrSWAuy-CfGIRVSL0="
		plain = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	)
	for name, c := range map[string]struct {
		in   string
		want string
		ok   bool
	}{
		"standard spelling is kept":          {std, std, true},
		"URL-safe spelling becomes standard": {urlSafe, std, true},
		"the same in both alphabets is kept": {plain, plain, true},
		"a slash is the standard spelling":   {"8TMwqXnWOTdw7xFDHSn/f6JMbBQrSWAuyzCfGIRVSL0=", "8TMwqXnWOTdw7xFDHSn/f6JMbBQrSWAuyzCfGIRVSL0=", true},
		"an underscore becomes a slash":      {"8TMwqXnWOTdw7xFDHSn_f6JMbBQrSWAuyzCfGIRVSL0=", "8TMwqXnWOTdw7xFDHSn/f6JMbBQrSWAuyzCfGIRVSL0=", true},
		"both alphabets in one value":        {both, "", false},
		"empty":                              {"", "", false},
		"31 bytes":                           {base64.StdEncoding.EncodeToString(make([]byte, 31)), "", false},
		"33 bytes":                           {base64.StdEncoding.EncodeToString(make([]byte, 33)), "", false},
		"no padding":                         {strings.TrimSuffix(std, "="), "", false},
		"nonzero padding bits":               {"8TMwqXnWOTdw7xFDHSn+f6JMbBQrSWAuyzCfGIRVSL1=", "", false},
		"a line break inside":                {std[:20] + "\n" + std[20:], "", false},
		"a trailing line break":              {std + "\n", "", false},
		"a leading space":                    {" " + std, "", false},
		"text that is not base64":            {"not a cluster id", "", false},
		"one padding character too many":     {std + "=", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := CanonicalClusterID(c.in)
			if got != c.want || ok != c.ok {
				t.Fatalf("CanonicalClusterID(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
			}
		})
	}
}

// Both spellings of every 32-byte value meet at the one standard spelling, and the standard one is
// its own canonical form, over inputs that use "+", "/", "-" and "_".
func TestCanonicalClusterIDRoundTrip(t *testing.T) {
	seen := map[byte]bool{}
	for i := 0; i < 4096 && len(seen) < 4; i++ {
		b := make([]byte, 32)
		for j := range b {
			b[j] = byte(i*31 + j*7)
		}
		std, url := base64.StdEncoding.EncodeToString(b), base64.URLEncoding.EncodeToString(b)
		for _, ch := range "+/" {
			if strings.ContainsRune(std, ch) {
				seen[byte(ch)] = true
			}
		}
		for _, in := range []string{std, url} {
			if got, ok := CanonicalClusterID(in); !ok || got != std {
				t.Fatalf("CanonicalClusterID(%q) = %q, %v; want %q", in, got, ok, std)
			}
		}
	}
	if !seen['+'] || !seen['/'] {
		t.Fatalf("the inputs did not use both \"+\" and \"/\": %v", seen)
	}
}
