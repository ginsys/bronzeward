package monitor

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/provider"
)

// request is one request a provider stand-in received.
type request struct{ method, path, token string }

// standIn answers each monitored object's metadata path with its answer and records every request.
type standIn struct {
	mu       sync.Mutex
	answers  map[string]classify.Answer
	requests []request
}

func (s *standIn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, request{r.Method, r.URL.Path, r.Header.Get("X-Vault-Token")})
	a, ok := s.answers[r.URL.Path]
	s.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
	w.WriteHeader(a.Status)
	w.Write(a.Body.Bytes())
}

// tokenFile writes a synthetic token to a 0600 file and reads it as the provider package does.
func tokenFile(t *testing.T, value string) provider.Token {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := provider.ReadTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// Dependency monitor §10.1 item 5: a pass through the real metadata client asks the provider for
// each monitored dependency by name, with GET only and the metadata identity's token on every
// request, and classifies both retained from those answers. Control: the stand-in answers 404 to
// any other path, which would classify unknown.
func TestPassMetadataIdentity(t *testing.T) {
	f := seed(t)
	kvPath, keyPath := "/v1/secret/metadata/"+f.kv, "/v1/transit/keys/"+f.key
	s := &standIn{answers: map[string]classify.Answer{kvPath: kvAnswer(t, f, nil), keyPath: transitAnswer(t, f)}}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	meta, err := provider.NewMetadata(srv.URL, tokenFile(t, "synthetic-metadata-token"))
	if err != nil {
		t.Fatal(err)
	}
	l := &logs{}
	pass(t, New(f.db, meta, Defaults(), l.logf, io.Discard))
	for _, dep := range []string{f.depKV, f.depKey} {
		if st := statusOf(t, f.db, dep); st.class != "retained" {
			t.Fatalf("%s classified %s/%s, want retained", dep, st.class, st.reason.String)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var paths []string
	for _, q := range s.requests {
		if q.method != http.MethodGet || q.token != "synthetic-metadata-token" {
			t.Errorf("sent %s %s with token %q, want GET with the metadata identity's", q.method, q.path, q.token)
		}
		paths = append(paths, q.path)
	}
	slices.Sort(paths)
	if want := []string{kvPath, keyPath}; !slices.Equal(paths, want) {
		t.Fatalf("asked %q, want %q", paths, want)
	}
}
