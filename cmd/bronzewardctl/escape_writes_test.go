package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// noRawControl fails the test for each raw control character in text, a line feed apart.
func noRawControl(t *testing.T, what, text string) {
	t.Helper()
	for _, r := range text {
		if r != '\n' && unicode.IsControl(r) || strings.ContainsRune(bidi, r) {
			t.Errorf("%s holds the raw control character %U: %q", what, r, text)
			return
		}
	}
}

// TestRemoteTextInDraftDiscard: the sentence of a write shows the answer's text escaped. The sweep
// cannot run discard, because the draft's kept ETag is filed under the draft's own ID.
func TestRemoteTextInDraftDiscard(t *testing.T) {
	draft := func(state string) []byte {
		b, _ := json.Marshal(map[string]any{"id": "d1", "cluster": "c1", "title": "t", "state": state, "revision": 2, "entries": []any{}})
		return b
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Bronzeward-Epoch", "ep_1")
		w.Header().Set("ETag", `"2-x"`)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/server":
			fmt.Fprint(w, `{"version":"v","apiRevision":2,"startMode":"normal"}`)
		case r.URL.Path == "/api/v1/identity":
			fmt.Fprint(w, newFiller(false).answer(apitypes.IdentityBody{}))
		case r.Method == http.MethodPost:
			w.Write(draft("closed" + tail))
		default:
			w.Write(draft("open"))
		}
	}))
	defer srv.Close()
	automationContext(t, srv.URL)
	var o, e bytes.Buffer
	if got := run([]string{"draft", "show", "d1"}, &o, &e); got != 0 {
		t.Fatalf("draft show exited %d: %s", got, e.String())
	}
	o.Reset()
	if got := run([]string{"draft", "discard", "d1"}, &o, &e); got != 0 {
		t.Fatalf("draft discard exited %d: %s", got, e.String())
	}
	if want := "Draft d1 closed" + tailEsc + " (revision 2)."; !strings.Contains(o.String(), want) {
		t.Errorf("the answer %q lacks %q", o.String(), want)
	}
	noRawControl(t, "draft discard", o.String()+e.String())
}

// TestRemoteTextInDecodeError: a decoder error quotes the byte that the server chose, and the CLI
// shows it escaped. A time with a DEL after it is a valid JSON string and an invalid time.
func TestRemoteTextInDecodeError(t *testing.T) {
	var item map[string]any
	if err := json.Unmarshal([]byte(newFiller(false).answer(apitypes.FragmentRevisionBody{})), &item); err != nil {
		t.Fatal(err)
	}
	item["createdAt"] = "2026-10-26T10:00:00Z\x7f"
	b, _ := json.Marshal(map[string]any{"items": []any{item}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Bronzeward-Epoch", "ep_1")
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/server" {
			fmt.Fprint(w, `{"version":"v","apiRevision":2,"startMode":"normal"}`)
			return
		}
		w.Write(b)
	}))
	defer srv.Close()
	automationContext(t, srv.URL)
	var o, e bytes.Buffer
	if got := run([]string{"fragment", "history", "f1"}, &o, &e); got != 1 {
		t.Fatalf("fragment history exited %d, want 1: %s", got, e.String())
	}
	if !strings.Contains(e.String(), `\x7f`) {
		t.Errorf("the error %q does not show the DEL as \\x7f", e.String())
	}
	noRawControl(t, "the decode error", e.String())
}

// TestRemoteTextInTokenFilePath: a token file path comes from the contexts file, and login and
// logout name it in their sentences.
func TestRemoteTextInTokenFilePath(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	automationContext(t, srv.URL)
	path := t.TempDir() + "/t" + tail
	if err := client.UpdateConfig(func(c *client.Config) error {
		c.Contexts = append(c.Contexts, client.Context{Name: "idp", Server: srv.URL, Credential: "oidc",
			OIDC: &client.OIDC{Issuer: "https://idp.invalid", ClientID: "c", Audience: "a", TokenFile: path}})
		// A path below a file that is not a directory fails with ENOTDIR, whose error names the path.
		c.Contexts = append(c.Contexts, client.Context{Name: "bad", Server: srv.URL, Credential: "oidc",
			OIDC: &client.OIDC{Issuer: "https://idp.invalid", ClientID: "c", Audience: "a", TokenFile: "/dev/null/t" + tail}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Login is not run: with --no-input it refuses before it reads the path, and without it waits for
	// an identity provider. Its sentence passes through the same store.kept as logout's.
	for _, c := range [][]string{{"logout"}, {"whoami"}} {
		var o, e bytes.Buffer
		run(append([]string{"--context", "idp"}, c...), &o, &e)
		noRawControl(t, strings.Join(c, " "), o.String()+e.String())
		if c[0] == "logout" && !strings.Contains(o.String(), `\x1b`) {
			t.Errorf("logout %q does not name the path escaped", o.String())
		}
	}
	for _, c := range [][]string{{"logout"}, {"whoami"}} {
		var o, e bytes.Buffer
		run(append([]string{"--context", "bad"}, c...), &o, &e)
		noRawControl(t, "bad "+strings.Join(c, " "), o.String()+e.String())
		// Logout fails on the lock directory, whose path holds no escape; whoami reads the file.
		if c[0] == "whoami" && !strings.Contains(o.String()+e.String(), `\x1b`) {
			t.Errorf("%s: the output %q does not name the path escaped", strings.Join(c, " "), o.String()+e.String())
		}
	}
}

// TestRemoteTextInUnreadableAnswer: a write whose answer cannot be read prints the answer as served,
// but -o json, and only it, writes DEL, C1 and bidirectional controls as backslash-u escapes
// (client-contracts.md section 6 item 8); -o yaml prints the answer unchanged.
func TestRemoteTextInUnreadableAnswer(t *testing.T) {
	served := "{\"id\":[\"a\x7f\xc2\x85\xe2\x80\xaeb\"]}"
	escaped := "{\"id\":[\"a" + fmt.Sprintf(`\u%04x\u%04x\u%04x`, 0x7f, 0x85, 0x202e) + "b\"]}\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Bronzeward-Epoch", "ep_1")
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/server":
			fmt.Fprint(w, `{"version":"v","apiRevision":2,"startMode":"normal"}`)
		case "GET /api/v1/identity":
			fmt.Fprint(w, `{"kind":"service","principal":"svc_1"}`)
		case "POST /api/v1/clusters":
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, served)
		}
	}))
	defer srv.Close()
	automationContext(t, srv.URL)
	for o, want := range map[string]string{"json": escaped, "yaml": served + "\n", "table": ""} {
		var out, errb bytes.Buffer
		if code := run([]string{"-o", o, "cluster", "add", "--name", "lab-a"}, &out, &errb); code != 0 || out.String() != want {
			t.Errorf("-o %s: exit %d, stdout %q, want %q (stderr %q)", o, code, out.String(), want, errb.String())
		}
		noRawControl(t, "-o "+o+" stderr", errb.String())
	}
}
