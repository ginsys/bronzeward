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
// accepts its humans' tokens over plain http, with the token endpoint of provider.
func serveIssuer(t *testing.T) (*issuer.Issuer, *provider) {
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
	idp := &provider{}
	srv := &httptest.Server{Listener: l, Config: &http.Server{Handler: idp.wrap(iss)}}
	srv.Start()
	t.Cleanup(srv.Close)
	return iss, idp
}

// The seeded cluster's Talos cluster ID and its machine's SMBIOS UUID, which a Talos stand-in
// reports for it (standIn).
const (
	seedClusterID = "c2VlZC1jbHVzdGVyLWlkLWZvci10aGUtY2xpLXRlc3Q="
	seedUUID      = "0b5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5b"
)

// seeded names what seed recorded.
type seeded struct{ cluster, machine, draft string }

// seed records, as the fixture human h-all, a cluster, one machine in it and an empty open draft.
// Recording a cluster or a machine is human-only, so seed mints its own token and f.token stays
// the automation token. A draft entry needs the provider's keyed digest, and this server has no
// provider.
func (f *fixture) seed() seeded { return f.seedAs(firstCluster) }

// clusterIdent is what two seeded clusters must not share: the cluster's name and Talos cluster ID,
// its machine's SMBIOS UUID and node ID, and the suffix of the idempotency keys of the seeding writes.
type clusterIdent struct{ name, clusterID, uuid, nodeID, key, serial string }

// firstCluster is the cluster every test seeds; secondCluster is the one a journey of two clusters adds.
var (
	firstCluster  = clusterIdent{"lab-a", seedClusterID, seedUUID, standInNodeID, "0123456", "SN-1"}
	secondCluster = clusterIdent{"lab-b", "am91cm5leS1jbHVzdGVyLWItZm9yLWNsaS10ZXN0cy4=", "1c5a6c1e-2f3d-4e5f-8a9b-0c1d2e3f4a5c", "8x1SuC8Ege5BGXdAfTEff5iQnlWZLfv9h1LGMxA2pYkC", "bbbbbbb", "SN-2"}
)

// seedAs is seed for the cluster id names.
func (f *fixture) seedAs(id clusterIdent) seeded {
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
	s.cluster = create("/clusters", "k-seed-cluster-"+id.key,
		`{"name":"`+id.name+`","endpoint":"https://cp.example.test:6443","contract":"v1.14","talosClusterId":"`+id.clusterID+`"}`)
	s.machine = create("/machines", "k-seed-machine-"+id.key,
		`{"cluster":"`+s.cluster+`","smbiosUuid":"`+id.uuid+`","serial":"`+id.serial+`","platform":"container","talosEndpoint":"10.55.0.3"}`)
	s.draft = create("/drafts", "k-seed-draft-0"+id.key, `{"cluster":"`+s.cluster+`","title":"worker labels"}`)
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
