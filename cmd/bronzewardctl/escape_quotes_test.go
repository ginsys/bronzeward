package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/client"
)

// TestQuotedTextIsEscaped: a value that the CLI puts between quotation marks shows a tab as \x09
// and keeps a backslash single (client-contracts.md section 6 item 8), as the %q verb would not.
// One row per message that quotes text from the contexts file, a flag or a redirect.
func TestQuotedTextIsEscaped(t *testing.T) {
	const raw, shown = "a\tb\\c", `a\x09b\c`
	// Go refuses a redirect URL with an ASCII control, so the redirect rows use a C1 control.
	const loc, locShown = "a\xc2\x85b\\c", `a\x85b\c`
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer live.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://h.example.invalid/"+loc)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	automationContext(t, redirect.URL)
	if err := client.UpdateConfig(func(c *client.Config) error {
		c.Contexts = append(c.Contexts, client.Context{Name: `b\c`, Server: "https://h.example.invalid", Credential: "automation"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"nosuch" + raw}, `unknown command "nosuch` + shown + `"`},
		{[]string{"-o", raw, "status"}, `-o "` + shown + `": give table`},
		{[]string{"context", "use", raw}, `context "` + shown + `" does not exist`},
		{[]string{"--context", raw, "whoami"}, `context "` + shown + `", named by`},
		{[]string{"context", "add", `b\c`, "--server", live.URL}, `context "b\c" exists`},
		{[]string{"context", "add", raw, "--server", "https://h.example.invalid"}, `context name "` + shown + `": give`},
		{[]string{"context", "add", "n", "--server", "https://h.example.invalid", "--credential", raw}, `credential "` + shown + `": give`},
		{[]string{"context", "add", "n", "--server", "https://h.example.invalid", "--credential", raw, "--from", "https://i.example.invalid"}, `not "` + shown + `"`},
		{[]string{"context", "add", "n", "--server", "https://h.example.invalid/?" + raw}, `"https://h.example.invalid/?` + shown + `": give a URL`},
		{[]string{"context", "add", "n", "--server", redirect.URL}, `redirecting to "https://h.example.invalid/` + locShown + `"`},
		{[]string{"whoami"}, `redirecting to "https://h.example.invalid/` + locShown + `"`},
		{[]string{"cluster", "add", "--name", "n", "--endpoint", "https://e", "--contract", "c", "--talos-cluster-id", "t"}, `redirecting to "https://h.example.invalid/` + locShown + `"`},
	} {
		var o, e bytes.Buffer
		run(c.args, &o, &e)
		if got := o.String() + e.String(); !strings.Contains(got, c.want) {
			t.Errorf("%q: the output %q lacks %q", c.args, got, c.want)
		}
		noRawControl(t, strings.Join(c.args, " "), o.String()+e.String())
	}
}
