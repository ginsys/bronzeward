package provider

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/ginsys/bronzeward/internal/id"
)

// The stand-in tests pin this package's half of the contract against an httptest server: the
// requests it sends, what it accepts back, and that no failure carries what it was sending. The
// live tests (live_test.go) are where a wrong assumption about OpenBao itself would surface.

type request struct {
	method, path, token string
	body                []byte
}

// recorder keeps every request, under a mutex: the handler runs on the server's goroutine.
type recorder struct {
	mu   sync.Mutex
	reqs []request
}

func (r *recorder) add(q request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, q)
}

func (r *recorder) all() []request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]request(nil), r.reqs...)
}

func (r *recorder) last() request {
	all := r.all()
	if len(all) == 0 {
		return request{}
	}
	return all[len(all)-1]
}

var testKeys = Keys{Baseline: "k-baseline", Staging: "k-staging", Digest: "k-digest"}

const testToken = "s.stand-in-token-value"

func tokenOf(s string) Token { return Token{p: &s} }

// standIn returns an Ingestion pointed at handler, and the recorder it fills. The handler sees
// the body again after it is recorded, so a test asserting an error does not quote the request
// runs against a request that held it.
func standIn(t *testing.T, keys Keys, handler http.HandlerFunc) (*Ingestion, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the request body: %v", err)
		}
		rec.add(request{r.Method, r.URL.Path, r.Header.Get("X-Vault-Token"), body})
		r.Body = io.NopCloser(bytes.NewReader(body))
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	i, err := NewIngestion(srv.URL, tokenOf(testToken), keys)
	if err != nil {
		t.Fatal(err)
	}
	return i, rec
}

func respond(t *testing.T, w http.ResponseWriter, status int, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Errorf("encoding the stand-in response: %v", err)
	}
}

func data(fields map[string]any) map[string]any { return map[string]any{"data": fields} }

func newPath(t *testing.T) GenerationPath {
	t.Helper()
	p, err := NewGenerationPath(id.New(id.Cluster), id.New(id.Ingestion), NewValueID())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func mustValue(t *testing.T, k Kind, v any) Value {
	t.Helper()
	val, err := NewValue(k, v)
	if err != nil {
		t.Fatal(err)
	}
	return val
}
