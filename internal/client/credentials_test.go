package client

import (
	"encoding/base64"
	"testing"
)

// TestSameIdentity checks the identity-revoked clear's match (§9.6): the refused token itself, or
// readable claims naming the same subject at the same issuer. A token whose claims cannot be read
// matches nothing but itself, so a document without an access token is never taken for the refused
// sign-in.
func TestSameIdentity(t *testing.T) {
	// jwt is an unsigned synthetic token with the given claims; sameIdentity only reads them.
	jwt := func(claims string) string {
		return "e30." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".BWSYNTH-sig"
	}
	refused := jwt(`{"iss":"https://idp.example.invalid","sub":"h-denied","jti":"1"}`)
	for _, c := range []struct {
		name            string
		stored, refused string
		want            bool
	}{
		{"the refused token itself, unreadable", "BWSYNTH-opaque", "BWSYNTH-opaque", true},
		{"a different unreadable one", "BWSYNTH-other", "BWSYNTH-opaque", false},
		{"no access token", "", "BWSYNTH-opaque", false},
		{"no access token on either side", "", "", false},
		{"a renewed token for the same subject and issuer", jwt(`{"iss":"https://idp.example.invalid","sub":"h-denied","jti":"2"}`), refused, true},
		{"another subject at the same issuer", jwt(`{"iss":"https://idp.example.invalid","sub":"h-viewer"}`), refused, false},
		{"the same subject at another issuer", jwt(`{"iss":"https://other.example.invalid","sub":"h-denied"}`), refused, false},
		{"an unreadable token against a readable one", "BWSYNTH-opaque", refused, false},
	} {
		if got := sameIdentity(c.stored, c.refused); got != c.want {
			t.Errorf("%s: sameIdentity = %v, want %v", c.name, got, c.want)
		}
	}
}
