package client

import "testing"

// TestSameIdentity checks the identity-revoked clear's match (§9.6): the refused token itself, or
// readable claims naming the same subject at the same issuer. A token whose claims cannot be read
// matches nothing but itself, so a document without an access token is never taken for the refused
// sign-in.
func TestSameIdentity(t *testing.T) {
	for _, c := range []struct {
		name            string
		stored, refused string
		want            bool
	}{
		{"the refused token itself, unreadable", "BWSYNTH-opaque", "BWSYNTH-opaque", true},
		{"a different unreadable one", "BWSYNTH-other", "BWSYNTH-opaque", false},
		{"no access token", "", "BWSYNTH-opaque", false},
		{"no access token on either side", "", "", false},
	} {
		if got := sameIdentity(c.stored, c.refused); got != c.want {
			t.Errorf("%s: sameIdentity = %v, want %v", c.name, got, c.want)
		}
	}
}
