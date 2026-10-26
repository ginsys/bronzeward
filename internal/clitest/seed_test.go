package clitest

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/fixtures/oidc/issuer"
)

// serveIssuer serves the fixture issuer on loopback for the test's lifetime, so the server
// accepts its humans' tokens over plain http.
func serveIssuer(t *testing.T) *issuer.Issuer {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	key, err := issuer.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	iss, err := issuer.New("http://"+l.Addr().String(), "bronzeward", key)
	if err != nil {
		t.Fatal(err)
	}
	srv := &httptest.Server{Listener: l, Config: &http.Server{Handler: iss.Handler()}}
	srv.Start()
	t.Cleanup(srv.Close)
	return iss
}

// seeded names what seed recorded.
type seeded struct{ cluster, machine, draft string }

// seed records, as the fixture human h-all, a cluster, one machine in it and an empty open draft.
// Recording a cluster or a machine is human-only, so seed mints its own token and f.token stays
// the automation token. A draft entry needs the provider's keyed digest, and this server has no
// provider.
func (f *fixture) seed() seeded {
	f.t.Helper()
	human, err := f.iss.Mint("h-all", "", time.Now())
	if err != nil {
		f.t.Fatal(err)
	}
	create := func(path, key, body string) string {
		f.t.Helper()
		q, _ := http.NewRequest(http.MethodPost, f.base+"/api/v1"+path, strings.NewReader(body))
		q.Header.Set("Authorization", "Bearer "+human)
		q.Header.Set("Content-Type", "application/json")
		q.Header.Set("Idempotency-Key", key)
		var v struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(f.do(q, http.StatusCreated), &v); err != nil {
			f.t.Fatalf("seed: POST %s: %v", path, err)
		}
		return v.ID
	}
	var s seeded
	s.cluster = create("/clusters", "k-seed-cluster-0123456",
		`{"name":"lab-a","endpoint":"https://cp.example.test:6443","contract":"v1.13","talosClusterId":"c2VlZC1jbHVzdGVyLWlkLWZvci10aGUtY2xpLXRlc3Q="}`)
	s.machine = create("/machines", "k-seed-machine-0123456",
		`{"cluster":"`+s.cluster+`","smbiosUuid":"0b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b","serial":"SN-1","platform":"container","talosEndpoint":"10.55.0.3"}`)
	s.draft = create("/drafts", "k-seed-draft-00123456", `{"cluster":"`+s.cluster+`","title":"worker labels"}`)
	return s
}

func TestSeed(t *testing.T) {
	f := start(t, "viewer")
	before := f.acts()
	s := f.seed()
	if !strings.HasPrefix(s.cluster, "cl_") || !strings.HasPrefix(s.machine, "mch_") || !strings.HasPrefix(s.draft, "drf_") {
		t.Fatalf("seeded %+v", s)
	}
	if f.acts() == before {
		t.Error("seed recorded no act")
	}
}
