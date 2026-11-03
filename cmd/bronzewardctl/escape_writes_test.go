package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
			fmt.Fprint(w, `{"version":"v","apiRevision":3,"startMode":"normal"}`)
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

// TestRemoteTextInDraftSourceWrites: profile set and rm, and assignment set and rm, print their
// sentences escaped; after an unknown outcome their read-back callback prints what the draft shows
// of the entry, and that text from the server is escaped too. The sweep cannot run them, because it
// keeps no ETag for a draft.
func TestRemoteTextInDraftSourceWrites(t *testing.T) {
	profile, machine, fragment := "p"+tail, "m"+tail, "f"+tail
	doc := t.TempDir() + "/doc.yaml"
	if err := os.WriteFile(doc, []byte("machine:\n  nodeLabels:\n    BWSYNTH: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A draft whose state is not "open" keeps no ETag, so draft show answers an open one.
	body := func(state string) []byte {
		b, _ := json.Marshal(map[string]any{"id": "d1", "cluster": "c1", "title": "t", "state": state, "revision": 2, "entries": []any{
			map[string]any{"kind": "profile", "name": profile, "base": 1, "head": "prv_h", "revision": "prv_9" + tail},
			map[string]any{"kind": "assignment", "machine": machine, "base": 1, "head": "asr_h", "revision": "asr_9" + tail},
			map[string]any{"kind": "fragment", "name": fragment, "base": 1, "head": "frv_h", "revision": "frv_9" + tail},
		}})
		return b
	}
	// The answer to fragment set names the draft and the entry's revision, both remote text.
	update, _ := json.Marshal(map[string]any{"draft": "d" + tail, "entry": map[string]any{"kind": "fragment", "name": fragment, "base": 1, "head": "frv_h", "revision": "frv_8" + tail}})
	var unknown, plain atomic.Bool // a write is redirected, an outcome unknown, when set
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Bronzeward-Epoch", "ep_1")
		w.Header().Set("ETag", `"2-x"`)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/server":
			fmt.Fprint(w, `{"version":"v","apiRevision":3,"startMode":"normal"}`)
		case r.URL.Path == "/api/v1/identity":
			fmt.Fprint(w, newFiller(false).answer(apitypes.IdentityBody{}))
		case r.URL.Path == "/api/v1/drafts/d1" && r.Method == http.MethodGet && plain.Load():
			w.Write(body("open"))
		case r.URL.Path == "/api/v1/drafts/d1" && r.Method == http.MethodGet:
			w.Write(body("open" + tail))
		case unknown.Load():
			http.Redirect(w, r, "https://x.example.invalid/", http.StatusTemporaryRedirect)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/drafts/d1/fragments/"):
			w.Write(update)
		default:
			w.Write(body("open"))
		}
	}))
	defer srv.Close()
	automationContext(t, srv.URL)
	var o, e bytes.Buffer
	keepETag := func() {
		plain.Store(true)
		defer plain.Store(false)
		if got := run([]string{"draft", "show", "d1"}, &o, &e); got != 0 {
			t.Fatalf("draft show exited %d: %s", got, e.String())
		}
	}
	for _, c := range []struct {
		args []string
		done string // the sentence of a write that succeeded
		want string // after an unknown outcome
	}{
		{[]string{"profile", "set", profile, "--draft", "d1", "--fragments", "frv_1"},
			"Profile p" + tailEsc + " saved: pins frv_1 (draft revision 2).", "with profile p" + tailEsc + " at prv_9"},
		{[]string{"profile", "rm", profile, "--draft", "d1"},
			"Profile p" + tailEsc + " removed from draft d1 (draft revision 2).", "with profile p" + tailEsc + " at prv_9"},
		{[]string{"assignment", "set", machine, "--draft", "d1", "--profiles", "p"},
			"Assignment saved (draft revision 2).", "with assignment m" + tailEsc + " at asr_9"},
		{[]string{"assignment", "rm", machine, "--draft", "d1"},
			"Assignment m" + tailEsc + " removed from draft d1 (draft revision 2).", "with assignment m" + tailEsc + " at asr_9"},
		{[]string{"fragment", "set", fragment, "--draft", "d1", "--layer", "role", "--file", doc},
			"Fragment f" + tailEsc + " saved in draft d" + tailEsc + " (revision frv_8" + tailEsc + "; draft revision 2).", "with fragment f" + tailEsc + " at frv_9"},
		{[]string{"fragment", "rm", fragment, "--draft", "d1"},
			"Fragment f" + tailEsc + " removed from draft d1 (draft revision 2).", "with fragment f" + tailEsc + " at frv_9"},
	} {
		name := strings.Join(c.args[:2], " ")
		for _, lost := range []bool{false, true} {
			keepETag()
			unknown.Store(lost)
			o.Reset()
			e.Reset()
			got := run(c.args, &o, &e)
			if got != map[bool]int{false: 0, true: 3}[lost] {
				t.Errorf("%s (outcome unknown: %v) exited %d: %s", name, lost, got, e.String())
			}
			noRawControl(t, name, o.String()+e.String())
			if !lost && !strings.Contains(o.String(), c.done) {
				t.Errorf("%s: the answer %q lacks %q", name, o.String(), c.done)
			}
			if lost && !strings.Contains(o.String()+e.String(), c.want+tailEsc) {
				t.Errorf("%s: the read-back %q lacks %q", name, o.String()+e.String(), c.want+tailEsc)
			}
		}
	}
	unknown.Store(false)
}

// TestOwnArgumentInParserError: the flag parser quotes an argument as given, and the CLI shows the
// line feed in it escaped, so that an argument cannot forge a line of its own.
func TestOwnArgumentInParserError(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"an unknown flag", []string{"--bad\nFORGED"}, `flag provided but not defined: -bad\x0aFORGED`},
		// The parser quotes a rejected value with %q (`\t`, `\\`); the contract's form is `\x09` and `\`.
		{"an int flag", []string{"cluster", "list", "--limit", "a\tb\\c\nFORGED"}, `invalid value "a\x09b\c\x0aFORGED" for flag -limit: parse error`},
		{"a bool flag", []string{"--no-input=x\ty\\z"}, `invalid boolean value "x\x09y\z" for -no-input: parse error`},
		{"an int flag with a double quote", []string{"cluster", "list", "--limit", `a"b`}, `invalid value "a\x22b" for flag -limit: parse error`},
		{"a bool flag with a double quote", []string{`--no-input=a"b`}, `invalid boolean value "a\x22b" for -no-input: parse error`},
		{"a global flag after the command", []string{"cluster", "list", "-o", "x\ty"}, `-o "x\x09y": give table, json or yaml`},
		{"a flag without its value", []string{"cluster", "list", "--limit"}, `flag needs an argument: -limit`},
	} {
		var o, e bytes.Buffer
		if got := run(c.args, &o, &e); got != 2 {
			t.Errorf("%s: exit %d, want 2: %s", c.name, got, e.String())
		}
		if strings.Contains(e.String(), "\nFORGED") || !strings.Contains(e.String(), c.want) {
			t.Errorf("%s: the usage error %q lacks %q, or an argument started a line", c.name, e.String(), c.want)
		}
		noRawControl(t, c.name, e.String())
	}
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
			fmt.Fprint(w, `{"version":"v","apiRevision":3,"startMode":"normal"}`)
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
	dir := t.TempDir()
	path := dir + "/t" + tail
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
	// an identity provider. Logout names the path through store.gone; what store.kept shows for a
	// path is pinned by TestStoreKeptIsEscaped in internal/client.
	for _, c := range [][]string{{"logout"}, {"whoami"}} {
		var o, e bytes.Buffer
		run(append([]string{"--context", "idp"}, c...), &o, &e)
		noRawControl(t, strings.Join(c, " "), o.String()+e.String())
		// The guard escapes every control but the line feed, so the line feed of the path shows as
		// \x0a only if store.gone escaped it at the source; the sentence is one line.
		if want := "the token file " + dir + "/t" + tailEsc + " does not exist"; c[0] == "logout" &&
			(!strings.Contains(o.String(), want) || strings.Count(o.String(), "\n") != 1) {
			t.Errorf("logout %q lacks the sentence %q on one line", o.String(), want)
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

// TestRemoteTextInSignInClaims: whoami in an oidc context shows the subject and the issuer that
// the client reads from the access token, text the identity provider chose, escaped and on one line.
func TestRemoteTextInSignInClaims(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Bronzeward-Epoch", "ep_1")
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/server" {
			fmt.Fprint(w, `{"version":"v","apiRevision":3,"startMode":"normal"}`)
			return
		}
		fmt.Fprint(w, newFiller(false).answer(apitypes.IdentityBody{}))
	}))
	defer srv.Close()
	automationContext(t, srv.URL)
	enc := base64.RawURLEncoding.EncodeToString
	claims, _ := json.Marshal(map[string]any{"sub": "s" + tail, "iss": "https://idp.invalid/" + tail, "exp": time.Now().Add(time.Hour).Unix()})
	token := enc([]byte(`{"alg":"none"}`)) + "." + enc(claims) + "." + enc([]byte("sig"))
	path := t.TempDir() + "/tokens"
	doc, _ := json.Marshal(map[string]string{"accessToken": token})
	if err := os.WriteFile(path, doc, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.UpdateConfig(func(c *client.Config) error {
		c.Contexts = append(c.Contexts, client.Context{Name: "idp", Server: srv.URL, Credential: "oidc",
			OIDC: &client.OIDC{Issuer: "https://idp.invalid", ClientID: "c", Audience: "a", TokenFile: path}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var o, e bytes.Buffer
	if got := run([]string{"--context", "idp", "whoami"}, &o, &e); got != 0 {
		t.Fatalf("whoami exited %d: %s", got, e.String())
	}
	want := "subject s" + tailEsc + ", issuer https://idp.invalid/" + tailEsc + ", expires "
	if !strings.Contains(o.String(), want) || strings.Contains(o.String(), "\n\r") {
		t.Errorf("whoami %q lacks the token's reading %q", o.String(), want)
	}
	if lines := strings.Count(o.String(), "\n"); lines != 7 {
		t.Errorf("whoami printed %d lines, want 7: %q", lines, o.String())
	}
	noRawControl(t, "whoami", o.String()+e.String())
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
			fmt.Fprint(w, `{"version":"v","apiRevision":3,"startMode":"normal"}`)
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
