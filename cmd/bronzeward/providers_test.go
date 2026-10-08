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

// tokenStandIn is a provider configuration whose token files hold synthetic, distinct tokens, one
// per identity, pointed at a stand-in that answers 404 and records the token of each request: a
// client built from another identity's file is told apart by the token it sends.
func tokenStandIn(t *testing.T) (*config.Provider, func() []string) {
	t.Helper()
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
		"metadata": &p.MetadataTokenFile, "executor": &p.ExecutorTokenFile} {
		*target = filepath.Join(dir, field)
		writeFile(t, *target, "synthetic-"+field+"-token\n", 0o600)
	}
	return p, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), tokens...)
	}
}

// The metadata client serve hands the dependency monitor sends the metadata identity's token, read
// from metadataTokenFile, on both of its requests (dependency monitor §4, §10.1 item 5): not
// ingestion's, not the compiler's.
func TestProviderClientsMetadataToken(t *testing.T) {
	p, sent := tokenStandIn(t)
	_, pub, _, err := providerClients(p)
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
	tokens := sent()
	if len(tokens) != 2 {
		t.Fatalf("%d requests, want 2", len(tokens))
	}
	for i, tok := range tokens {
		if tok != "synthetic-metadata-token" {
			t.Errorf("request %d sent %q, want the metadata identity's token", i, tok)
		}
	}
}

// The executor client serve hands observations reads the Talos access under the executor
// identity's token, read from executorTokenFile (persistence-api §3.3): not ingestion's.
func TestProviderClientsExecutorToken(t *testing.T) {
	p, sent := tokenStandIn(t)
	_, _, exe, err := providerClients(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exe.TalosAccess(t.Context(), id.New(id.Cluster)); err == nil {
		t.Fatal("the stand-in's 404 read as a version")
	}
	if tokens := sent(); len(tokens) != 1 || tokens[0] != "synthetic-executor-token" {
		t.Fatalf("sent %q, want the executor identity's token once", tokens)
	}
}
