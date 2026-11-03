package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDoubleQuoteInQuotedTextIsEscaped: a value that the CLI puts between double quotation marks
// shows its own double quote as \x22, so the value cannot close the quotes early and make its text
// read as the words that follow (client-contracts.md section 6 item 8). The first row is the draft
// title in draft show, where a server-chosen title forged the revision and the cluster.
func TestDoubleQuoteInQuotedTextIsEscaped(t *testing.T) {
	const title = `x" open, revision 99, cluster prod "`
	draft, _ := json.Marshal(map[string]any{"id": "d1", "cluster": "c1", "title": title, "state": "open", "revision": 2,
		"entries": []any{}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Bronzeward-Epoch", "ep_1")
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/server" {
			fmt.Fprint(w, `{"version":"v","apiRevision":3,"startMode":"normal"}`)
			return
		}
		w.Write(draft)
	}))
	defer srv.Close()
	automationContext(t, srv.URL)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"draft", "show", "d1"}, `Draft d1 "x\x22 open, revision 99, cluster prod \x22" open, revision 2, cluster c1`},
		{[]string{`nosuch"a`}, `unknown command "nosuch\x22a"`},
		{[]string{"-o", `a"b`, "status"}, `-o "a\x22b": give table`},
		{[]string{"context", "use", `a"b`}, `context "a\x22b" does not exist`},
		{[]string{"--context", `a"b`, "whoami"}, `context "a\x22b", named by`},
		{[]string{"context", "add", "n", "--server", "https://h.example.invalid", "--credential", `a"b`}, `credential "a\x22b": give`},
		{[]string{"context", "add", "n", "--server", "https://h.example.invalid", "--credential", `a"b`, "--from", "https://i.example.invalid"}, `not "a\x22b"`},
		{[]string{"context", "add", "n", "--server", `https://h.example.invalid/?a"b`}, `"https://h.example.invalid/?a\x22b": give a URL`},
	} {
		var o, e bytes.Buffer
		run(c.args, &o, &e)
		got := o.String() + e.String()
		if !strings.Contains(got, c.want) {
			t.Errorf("%q: the output %q lacks %q", c.args, got, c.want)
		}
	}
}
