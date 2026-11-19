package client

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// TestOnceWriteIsSentOnce: a Once write is sent once whatever answers it. Every failure but
// talos-access-unavailable may follow the route's store of the key, so it is an unknown outcome, a
// refusal that follows a lookup included; none is repeated, and the server's detail is never shown.
func TestOnceWriteIsSentOnce(t *testing.T) {
	const detail = "PLANTED-SERVER-WORDS"
	for _, tc := range []struct {
		name    string
		post    func(w http.ResponseWriter)
		unknown bool   // an unknown outcome, exit 3
		says    string // in the error
		never   string // not in the error
	}{
		{"a 500", func(w http.ResponseWriter) { problemAnswer(w, 500, "internal-error", `,"detail":"`+detail+`"`) }, true, "an attempt may have committed", detail},
		{"a 503 that is no problem", func(w http.ResponseWriter) { w.WriteHeader(503) }, true, "an attempt may have committed", ""},
		{"an epoch-superseded 503", func(w http.ResponseWriter) { problemAnswer(w, 503, "epoch-superseded", `,"detail":"`+detail+`"`) }, true, "an attempt may have committed", detail},
		{"a transient-conflict 503", func(w http.ResponseWriter) { problemAnswer(w, 503, "transient-conflict", `,"detail":"`+detail+`"`) }, true, "an attempt may have committed", detail},
		{"an epoch-mismatch 412", func(w http.ResponseWriter) {
			problemAnswer(w, 412, "epoch-mismatch", `,"epoch":"ep_BWSYNTH-2","detail":"`+detail+`"`)
		}, true, "the route may have stored the key", "No attempt of it can have committed anything"},
		{"a talos-access-unavailable 503", func(w http.ResponseWriter) {
			problemAnswer(w, 503, "talos-access-unavailable", `,"detail":"`+detail+`"`)
		}, false, "talos-access-unavailable", detail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{epoch: "ep_BWSYNTH-1", post: func(_ int, w http.ResponseWriter, _ *http.Request) { tc.post(w) }}
			c, notices, waits := writeClient(t, f, "ep_BWSYNTH-1")
			w := clusterWrite()
			w.Once = true
			_, err := c.Send(w)
			var u *Unknown
			if n := len(f.postsSeen()); n != 1 || len(*waits) != 0 || strings.Contains(notices.String(), "repeat") {
				t.Errorf("%d posts, waits %v, notices %q; want one post and no repeat", n, *waits, notices)
			}
			if got := errors.As(err, &u); got != tc.unknown || err == nil {
				t.Fatalf("error %v: unknown outcome %v, want %v", err, got, tc.unknown)
			}
			if !strings.Contains(err.Error(), tc.says) || tc.never != "" && strings.Contains(err.Error()+notices.String(), tc.never) {
				t.Errorf("error %q, notices %q; want %q and never %q", err, notices, tc.says, tc.never)
			}
		})
	}
}

// TestOnceWriteIsNotRepeatedAfterARenewal: a 401 that the sign-in's renewal can answer repeats an
// ordinary write once; a Once write is refused as it is, with one POST.
func TestOnceWriteIsNotRepeatedAfterARenewal(t *testing.T) {
	jwt := func(sub string) string {
		return "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://idp.example.invalid","sub":"`+sub+`"}`)) + ".BWSYNTH-sig"
	}
	first, renewed := jwt("h-author")+"1", jwt("h-author")+"2"
	for _, once := range []bool{false, true} {
		f := &fake{epoch: "ep_BWSYNTH-1", post: func(_ int, w http.ResponseWriter, _ *http.Request) { created(w) }}
		c, _, _ := writeClient(t, f, "ep_BWSYNTH-1")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.Header.Get("Authorization") == "Bearer "+first {
				w.Header().Set("Bronzeward-Epoch", "ep_BWSYNTH-1")
				problemAnswer(w, http.StatusUnauthorized, "unauthenticated", "")
				return
			}
			f.ServeHTTP(w, r)
		}))
		t.Cleanup(srv.Close)
		t.Setenv("BRONZEWARD_TOKEN_FILE", "")
		tokens := filepath.Join(t.TempDir(), "tokens")
		if err := WriteTokens(tokens, &Tokens{AccessToken: renewed, RefreshToken: "BWSYNTH-rt2"}); err != nil {
			t.Fatal(err)
		}
		c.Context.Server, c.Context.Credential = srv.URL, "oidc"
		c.Context.OIDC = &OIDC{Issuer: "https://idp.example.invalid", TokenFile: tokens}
		c.signIn = &signIn{store: openStore(c.Context, true), tokens: &Tokens{AccessToken: first, RefreshToken: "BWSYNTH-rt"}}
		c.token, c.service = first, ""
		w := clusterWrite()
		w.Once = once
		_, err := c.Send(w)
		if once && (err == nil || len(f.postsSeen()) != 0) {
			t.Errorf("a Once write: %v after %d accepted posts; want the 401 as the answer and no second post", err, len(f.postsSeen()))
		}
		if !once && (err != nil || len(f.postsSeen()) != 1) {
			t.Errorf("control, an ordinary write: %v after %d accepted posts; want the renewal to repeat it once", err, len(f.postsSeen()))
		}
	}
}
