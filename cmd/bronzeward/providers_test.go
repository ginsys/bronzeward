package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ginsys/bronzeward/internal/config"
	"github.com/ginsys/bronzeward/internal/id"
	"github.com/ginsys/bronzeward/internal/provider"
)

// The metadata client serve hands the dependency monitor sends the metadata identity's token, read
// from metadataTokenFile, on both of its requests (dependency monitor §4, §10.1 item 5): not
// ingestion's, not the compiler's. The tokens are synthetic and distinct, so a client built from
// another identity's file is told apart by the token it sends.
func TestProviderClientsMetadataToken(t *testing.T) {
	var mu sync.Mutex
	var tokens []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		tokens = append(tokens, r.Header.Get("X-Vault-Token"))
		mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	p := &config.Provider{Address: srv.URL,
		Keys: config.ProviderKeys{Baseline: "bw-baseline", Staging: "bw-staging", Digest: "bw-digest", Artifact: "bw-artifact"}}
	for field, target := range map[string]*string{"ingestion": &p.IngestionTokenFile, "compiler": &p.CompilerTokenFile,
		"metadata": &p.MetadataTokenFile} {
		*target = filepath.Join(dir, field)
		writeFile(t, *target, "synthetic-"+field+"-token\n", 0o600)
	}
	_, pub, err := providerClients(p)
	if err != nil {
		t.Fatal(err)
	}
	gp, err := provider.ParseGenerationPath("gen/" + id.New(id.Cluster) + "/" + id.New(id.Ingestion) + "/v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pub.Meta.KV(t.Context(), gp); err != nil {
		t.Fatal(err)
	}
	if _, err := pub.Meta.Transit(t.Context(), "bw-artifact"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(tokens) != 2 {
		t.Fatalf("%d requests, want 2", len(tokens))
	}
	for i, tok := range tokens {
		if tok != "synthetic-metadata-token" {
			t.Errorf("request %d sent %q, want the metadata identity's token", i, tok)
		}
	}
}
