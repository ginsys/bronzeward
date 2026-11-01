package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ginsys/bronzeward/internal/client"
)

// TestRepeatFlags checks each refusal of decision T10's repeat flags (#256), all before sending.
func TestRepeatFlags(t *testing.T) {
	key := "BWSYNTH-aaaaaaaaaaaaaaa"
	for _, c := range []struct {
		name         string
		f            repeatFlags
		takesIfMatch bool
		want         string // "" for a repeat, "first" for a first send, else the refusal
	}{
		{"none: a first send", repeatFlags{}, false, "first"},
		{"all three", repeatFlags{key, "ep_1", "svc_1", ""}, false, ""},
		{"all four where the route takes If-Match", repeatFlags{key, "ep_1", "svc_1", `"e"`}, true, ""},
		{"no key", repeatFlags{"", "ep_1", "svc_1", ""}, false, "a repeat needs --idempotency-key, --expect-epoch and --expect-principal"},
		{"no epoch", repeatFlags{key, "", "svc_1", ""}, false, "a repeat needs"},
		{"no principal", repeatFlags{key, "ep_1", "", ""}, false, "a repeat needs"},
		{"no if-match where the route takes one", repeatFlags{key, "ep_1", "svc_1", ""}, true, "and --if-match, each as the outcome-unknown block printed it"},
		{"if-match where the route takes none", repeatFlags{key, "ep_1", "svc_1", `"e"`}, false, "this route takes no If-Match"},
		{"a key not as printed", repeatFlags{"short", "ep_1", "svc_1", ""}, false, "give the key as printed"},
	} {
		r, err := c.f.repeat(c.takesIfMatch)
		var u usageError
		switch {
		case c.want == "first" && (r != nil || err != nil):
			t.Errorf("%s: %v, %v; want a first send", c.name, r, err)
		case c.want == "" && (err != nil || r == nil || r.Key != c.f.key || r.Epoch != c.f.epoch || r.Principal != c.f.principal):
			t.Errorf("%s: %v, %v; want the repeat", c.name, r, err)
		case c.want != "" && c.want != "first" && (!errors.As(err, &u) || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: %v; want a usage error saying %q", c.name, err, c.want)
		}
	}
}

// TestUnknownOutcomeOutput: on exit 3 standard output stays empty, whatever -o asks, and the T10
// block is on standard error; a repeat missing a flag exits 2 and sends nothing.
func TestUnknownOutcomeOutput(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-1")
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/server":
			fmt.Fprint(w, `{"version":"BWSYNTH","apiRevision":2,"startMode":"normal"}`)
		case "GET /api/v1/identity":
			fmt.Fprint(w, `{"kind":"service","principal":"svc_BWSYNTH"}`)
		case "GET /api/v1/clusters":
			fmt.Fprint(w, `{"items":[]}`)
		default:
			http.Redirect(w, r, "https://BWSYNTH.example.invalid/", http.StatusTemporaryRedirect)
		}
	}))
	defer srv.Close()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_STATE_HOME", home)
	tok := filepath.Join(home, "token")
	if err := os.WriteFile(tok, []byte("bwt_BWSYNTH.BWSYNTH-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.UpdateConfig(func(c *client.Config) error {
		c.Current, c.Contexts = "lab", []client.Context{{Name: "lab", Server: srv.URL, Credential: "automation", Automation: &client.Automation{TokenFile: tok}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, o := range []string{"table", "json", "yaml"} {
		var out, errb bytes.Buffer
		code := run([]string{"-o", o, "cluster", "add", "--name", "lab-a"}, &out, &errb)
		if code != 3 || out.Len() != 0 || !strings.Contains(errb.String(), "outcome unknown") || !strings.Contains(errb.String(), "--idempotency-key ") {
			t.Errorf("-o %s: exit %d, stdout %q, stderr %s; want 3, nothing and the block", o, code, out.String(), errb.String())
		}
	}
	mu.Lock()
	seen = nil
	mu.Unlock()
	var out, errb bytes.Buffer
	if code := run([]string{"cluster", "add", "--name", "lab-a", "--idempotency-key", "BWSYNTH-aaaaaaaaaaaaaaa"}, &out, &errb); code != 2 || out.Len() != 0 {
		t.Errorf("a repeat missing flags: exit %d, stdout %q", code, out.String())
	}
	if len(seen) != 0 {
		t.Errorf("a repeat missing flags sent %v", seen)
	}
}
