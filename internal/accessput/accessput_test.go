package accessput

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

const secretDocument = "context: planted-talosconfig-bytes\n"

func cluster() string { return id.New(id.Cluster) }

func testToken(t *testing.T) Token {
	t.Helper()
	tok, err := ParseToken([]byte("s.planted-token-value\n"))
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// The one request: a POST to the cluster's data path, the token in its header, check-and-set 0 and
// the document as the secret's only key.
func TestPutSendsOneCreate(t *testing.T) {
	cl := cluster()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/secret/data/access/talos/"+cl || r.URL.RawQuery != "" {
			t.Errorf("request %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if got := r.Header.Get("X-Vault-Token"); got != "s.planted-token-value" {
			t.Errorf("token header = %q", got)
		}
		b, _ := io.ReadAll(r.Body)
		var got map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"options": map[string]any{"cas": float64(0)},
			"data":    map[string]any{"talosconfig": secretDocument},
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("body = %v, want %v", got, want)
		}
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"data":{"version":1}}`)
	}))
	defer srv.Close()
	if err := Put(context.Background(), srv.URL, testToken(t), cl, []byte(secretDocument)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Errorf("%d requests, want 1", calls.Load())
	}
}

// Each answer reads as one class, and no error carries the token, the document, the address or the
// server's own text.
func TestPutClasses(t *testing.T) {
	const serverText = "SERVER-TEXT-MUST-NOT-APPEAR"
	for name, tc := range map[string]struct {
		status int
		body   string
		want   error
	}{
		"created":          {http.StatusOK, `{}`, nil},
		"no content":       {http.StatusNoContent, ``, nil},
		"denied":           {http.StatusForbidden, `{"errors":["` + serverText + `"]}`, ErrDenied},
		"absent mount":     {http.StatusNotFound, `{"errors":["` + serverText + `"]}`, ErrAbsent},
		"present":          {http.StatusBadRequest, `{"errors":["check-and-set parameter did not match the current version"]}`, ErrPresent},
		"other 400":        {http.StatusBadRequest, `{"errors":["` + serverText + `"]}`, ErrUnexpected},
		"unparsable 400":   {http.StatusBadRequest, `not json ` + serverText, ErrUnexpected},
		"sealed":           {http.StatusServiceUnavailable, `{"errors":["` + serverText + `"]}`, ErrUnexpected},
		"server error":     {http.StatusInternalServerError, serverText, ErrUnexpected},
		"redirect":         {http.StatusTemporaryRedirect, ``, ErrUnexpected},
		"oversized answer": {http.StatusOK, strings.Repeat("x", maxResponse+1), ErrUnexpected},
	} {
		t.Run(name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if tc.status == http.StatusTemporaryRedirect {
					w.Header().Set("Location", "/elsewhere")
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			err := Put(context.Background(), srv.URL, testToken(t), cluster(), []byte(secretDocument))
			if !errors.Is(err, tc.want) && err != tc.want {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if hits.Load() != 1 {
				t.Errorf("%d requests, want 1: a redirect or a retry was followed", hits.Load())
			}
			if err == nil {
				return
			}
			for _, secret := range []string{"planted-token-value", "planted-talosconfig", serverText, srv.URL, strings.TrimPrefix(srv.URL, "http://")} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("the error carries %q: %v", secret, err)
				}
			}
		})
	}
}

// A redirect is not followed, so X-Vault-Token and the replayed body never reach another host.
func TestPutDoesNotFollowRedirect(t *testing.T) {
	var reached atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached.Store(true) }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	if err := Put(context.Background(), srv.URL, testToken(t), cluster(), []byte(secretDocument)); !errors.Is(err, ErrUnexpected) {
		t.Fatalf("err = %v", err)
	}
	if reached.Load() {
		t.Error("the redirect target received the request")
	}
}

func TestPutUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close()
	err := Put(context.Background(), addr, testToken(t), cluster(), []byte(secretDocument))
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("the error carries the address: %v", err)
	}
}

// A request that is refused before it is sent sends nothing.
func TestPutRefusesBeforeSending(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	for name, tc := range map[string]struct {
		addr    string
		tok     Token
		cluster string
	}{
		"zero token": {srv.URL, Token{}, cluster()}, "no cluster": {srv.URL, testToken(t), ""},
		"machine identifier":       {srv.URL, testToken(t), id.New(id.Machine)},
		"path in cluster":          {srv.URL, testToken(t), cluster() + "/../../sys/seal"},
		"query in cluster":         {srv.URL, testToken(t), cluster() + "?x=1"},
		"empty address":            {"", testToken(t), cluster()},
		"userinfo":                 {strings.Replace(srv.URL, "http://", "http://user:pw@", 1), testToken(t), cluster()},
		"plain http, non-loopback": {"http://openbao.example.net:8200", testToken(t), cluster()},
	} {
		t.Run(name, func(t *testing.T) {
			err := Put(context.Background(), tc.addr, tc.tok, tc.cluster, []byte(secretDocument))
			if err == nil {
				t.Fatal("no error")
			}
			if strings.Contains(err.Error(), "pw@") || strings.Contains(err.Error(), "planted") {
				t.Errorf("the error carries input: %v", err)
			}
		})
	}
	if hits.Load() != 0 {
		t.Errorf("%d requests were sent", hits.Load())
	}
}

// An empty document is refused before sending: the server refuses an empty talosconfig, and a
// create-only token cannot repair a path the empty write occupied.
func TestPutRefusesEmptyDocument(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	for name, doc := range map[string][]byte{"nil": nil, "empty": {}} {
		if err := Put(context.Background(), srv.URL, testToken(t), cluster(), doc); err == nil {
			t.Errorf("%s document: no error", name)
		}
	}
	if hits.Load() != 0 {
		t.Errorf("%d requests were sent", hits.Load())
	}
}

func TestParseAddress(t *testing.T) {
	for addr, ok := range map[string]bool{
		"https://openbao.example.net":       true,
		"https://openbao.example.net:8200/": true,
		"http://127.0.0.1:8200":             true,
		"http://localhost:8200":             true,
		"http://[::1]:8200":                 true,
		"http://openbao.example.net":        false,
		"http://10.0.0.1:8200":              false,
		"ftp://127.0.0.1":                   false,
		"https://u:p@openbao.example.net":   false,
		"https://openbao.example.net/v1":    false,
		"https://openbao.example.net?x=1":   false,
		"https://openbao.example.net?":      false,
		"https://openbao.example.net#x":     false,
		"https://":                          false,
		"openbao.example.net:8200":          false,
		"":                                  false,
	} {
		_, err := ParseAddress(addr)
		if (err == nil) != ok {
			t.Errorf("ParseAddress(%q) error = %v, want ok=%v", addr, err, ok)
		}
		if err != nil && (strings.Contains(err.Error(), "u:p") || strings.Contains(err.Error(), "openbao.example")) {
			t.Errorf("ParseAddress(%q) quotes the address: %v", addr, err)
		}
	}
}

func TestTokenNeverPrints(t *testing.T) {
	tok := testToken(t)
	for _, out := range []string{fmt.Sprint(tok), fmt.Sprintf("%v %+v %#v %s %q %x", tok, tok, tok, tok, tok, tok)} {
		if strings.Contains(out, "planted") || strings.Contains(out, "s.") {
			t.Errorf("the token prints: %q", out)
		}
	}
	// A struct holding the token in an unexported field is printed by reflection, which no method
	// intercepts; the pointer-backed value must still not show the string.
	nested := struct{ tok Token }{tok}
	for _, out := range []string{fmt.Sprintf("%v", nested), fmt.Sprintf("%+v", nested), fmt.Sprintf("%#v", nested)} {
		if strings.Contains(out, "planted") || strings.Contains(out, "s.p") {
			t.Errorf("the nested token prints: %q", out)
		}
	}
	b, err := json.Marshal(struct{ T Token }{tok})
	if err != nil || strings.Contains(string(b), "planted") {
		t.Errorf("the token marshals: %s %v", b, err)
	}
}

func TestParseToken(t *testing.T) {
	for in, ok := range map[string]bool{
		"s.abc":   true,
		"s.abc\n": true,
		"":        false,
		"\n":      false,
		"s.a\nb":  false,
		"s.a b":   false,
		"s.a\r\n": false,
		"s.é":     false,
		"s.a\x00": false,
	} {
		_, err := ParseToken([]byte(in))
		if (err == nil) != ok {
			t.Errorf("ParseToken(%q) error = %v, want ok=%v", in, err, ok)
		}
		if err != nil && strings.Contains(err.Error(), "s.") {
			t.Errorf("ParseToken(%q) quotes the token: %v", in, err)
		}
	}
}

func TestReadTokenStdinBound(t *testing.T) {
	if _, err := ReadTokenStdin(strings.NewReader("s." + strings.Repeat("a", MaxTokenFile))); err == nil {
		t.Error("a token over the bound was read")
	}
	if _, err := ReadTokenStdin(strings.NewReader("s." + strings.Repeat("a", MaxTokenFile-2))); err != nil {
		t.Errorf("a token at the bound was refused: %v", err)
	}
}

func TestReadTokenFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if tok, err := ReadTokenFile(write("ok", "s.abc\n", 0o600)); err != nil || tok.value() != "s.abc" {
		t.Errorf("owner-only file: %v", err)
	}
	for name, p := range map[string]string{
		"group-readable": write("g", "s.abc\n", 0o640),
		"world-readable": write("w", "s.abc\n", 0o604),
		"empty":          write("e", "", 0o600),
		"over the bound": write("big", strings.Repeat("a", MaxTokenFile+1), 0o600),
		"directory":      dir,
		"missing":        filepath.Join(dir, "none"),
	} {
		if _, err := ReadTokenFile(p); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Join(dir, "ok"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTokenFile(link); err == nil {
		t.Error("a symlink was followed")
	}
}
