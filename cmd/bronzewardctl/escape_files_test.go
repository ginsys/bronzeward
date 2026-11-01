package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/client"
	"github.com/ginsys/bronzeward/internal/client/apitypes"
)

// TestRemoteTextInAutomationTokenPath: an automation token file path comes from the contexts file,
// and the error of a path below a file that is not a directory names it.
func TestRemoteTextInAutomationTokenPath(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	automationContext(t, srv.URL)
	if err := client.UpdateConfig(func(c *client.Config) error {
		c.Contexts = append(c.Contexts, client.Context{Name: "auto", Server: srv.URL, Credential: "automation",
			Automation: &client.Automation{TokenFile: "/dev/null/t" + tail}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var o, e bytes.Buffer
	if got := run([]string{"--context", "auto", "whoami"}, &o, &e); got == 0 {
		t.Fatalf("whoami exited 0 with an unreadable token file: %s", o.String())
	}
	noRawControl(t, "whoami", o.String()+e.String())
	if !strings.Contains(e.String(), `\x1b`) {
		t.Errorf("the error %q does not name the path escaped", e.String())
	}
}

// TestRemoteTextInContextsFileError: the decoder of the contexts file quotes a value of the file,
// and the CLI shows the error escaped, line feeds included.
func TestRemoteTextInContextsFileError(t *testing.T) {
	automationContext(t, "http://127.0.0.1:1")
	p, err := client.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	file := "current: lab\ncontexts:\n  - name: lab\n    server: http://127.0.0.1:1\n    credential: automation\n" +
		`    maxTokenLifetimeSeconds: "\x1b[2J\x0a\x7f"` + "\n"
	if err := os.WriteFile(p, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	var o, e bytes.Buffer
	if got := run([]string{"context", "list"}, &o, &e); got == 0 {
		t.Fatalf("context list exited 0 on a file that does not decode: %s", o.String())
	}
	noRawControl(t, "context list", o.String()+e.String())
	if strings.Count(e.String(), "\n") != 1 {
		t.Errorf("the error %q holds a line feed that the file chose", e.String())
	}
	if !strings.Contains(e.String(), `\x1b`) {
		t.Errorf("the error %q does not show the ESC escaped", e.String())
	}
}

// TestRemoteTextInProfileSet: the sentence of a successful profile set shows the revision that the
// server's fragment list resolved, escaped.
func TestRemoteTextInProfileSet(t *testing.T) {
	var frag map[string]any
	if err := json.Unmarshal([]byte(newFiller(false).answer(apitypes.FragmentBody{})), &frag); err != nil {
		t.Fatal(err)
	}
	frag["name"], frag["cluster"], frag["revision"] = "f", "c1", "frv_1"+tail
	list, _ := json.Marshal(map[string]any{"items": []any{frag}})
	draft, _ := json.Marshal(map[string]any{"id": "d1", "cluster": "c1", "title": "t", "state": "open", "revision": 2, "entries": []any{}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Bronzeward-Epoch", "ep_1")
		w.Header().Set("ETag", `"2-x"`)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/server":
			fmt.Fprint(w, `{"version":"v","apiRevision":2,"startMode":"normal"}`)
		case r.URL.Path == "/api/v1/identity":
			fmt.Fprint(w, newFiller(false).answer(apitypes.IdentityBody{}))
		case r.URL.Path == "/api/v1/fragments":
			w.Write(list)
		case r.Method == http.MethodPut:
			fmt.Fprint(w, `{}`)
		default:
			w.Write(draft)
		}
	}))
	defer srv.Close()
	automationContext(t, srv.URL)
	var o, e bytes.Buffer
	if got := run([]string{"draft", "show", "d1"}, &o, &e); got != 0 {
		t.Fatalf("draft show exited %d: %s", got, e.String())
	}
	o.Reset()
	if got := run([]string{"profile", "set", "p", "--draft", "d1", "--fragments", "f"}, &o, &e); got != 0 {
		t.Fatalf("profile set exited %d: %s", got, e.String())
	}
	if want := "pins f frv_1" + tailEsc; !strings.Contains(o.String(), want) {
		t.Errorf("the answer %q lacks %q", o.String(), want)
	}
	noRawControl(t, "profile set", o.String()+e.String())
}
