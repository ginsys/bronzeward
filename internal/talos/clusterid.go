package talos

import "encoding/base64"

// CanonicalClusterID returns the standard spelling of a Talos cluster ID, and whether s is one.
// A cluster ID is 32 bytes in padded base64. Talos before v1.14 prints the URL-safe alphabet and
// v1.14 and later the standard one, so one cluster can be read in either spelling. The server
// stores and compares the standard one (persistence-api.md §7.3), which makes one cluster one value.
// A value that is neither spelling, mixes the two, has another length, carries nonzero padding bits
// or holds characters the decoder would skip is refused: the bytes must encode back to exactly what
// was given.
func CanonicalClusterID(s string) (string, bool) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding} {
		b, err := enc.DecodeString(s)
		if err == nil && len(b) == 32 && enc.EncodeToString(b) == s {
			return base64.StdEncoding.EncodeToString(b), true
		}
	}
	return "", false
}
