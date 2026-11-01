package client

import "testing"

// TestStoreKeptIsEscaped: kept names the token file and the reason for no store in the sentences of
// login, logout and every command that needs a sign-in. A path and a keyring error are text from
// outside, so kept shows them escaped (client-contracts.md section 6 item 8). The cases are the three
// places that kept can be.
func TestStoreKeptIsEscaped(t *testing.T) {
	for _, c := range []struct {
		name string
		s    *store
		want string
	}{
		{"a token file", &store{file: "/p/t\x1b[31m\nFORGED"}, `the token file /p/t\x1b[31m\x0aFORGED`},
		{"no store", &store{none: "no keyring\x1b]0;x\x07\nFORGED"}, `this process's memory only, since no keyring\x1b]0;x\x07\x0aFORGED`},
		// The negative control: the credential store has no text from outside.
		{"the credential store", &store{}, "the system credential store"},
	} {
		if got := c.s.kept(); got != c.want {
			t.Errorf("%s: kept is %q, want %q", c.name, got, c.want)
		}
	}
}
