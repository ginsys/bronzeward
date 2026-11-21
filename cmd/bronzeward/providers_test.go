package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
	_, pub, _, _, _, err := providerClients(p)
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

// The executor client serve hands the executor decrypts a release's artifact under the executor
// identity's token, read from executorTokenFile (persistence-api §3.3): not ingestion's.
func TestProviderClientsExecutorToken(t *testing.T) {
	p, sent := tokenStandIn(t)
	_, _, exe, _, _, err := providerClients(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exe.DecryptArtifact(t.Context(), "vault:v1:AAAA", 1); err == nil {
		t.Fatal("the stand-in's 404 read as an artifact")
	}
	if tokens := sent(); len(tokens) != 1 || tokens[0] != "synthetic-executor-token" {
		t.Fatalf("sent %q, want the executor identity's token once", tokens)
	}
}

// The presence client is built only when presenceTokenFile is set, and then sends that identity's
// token and no other's (persistence-api §3.3); unset, the interface stays nil, not a typed nil, so
// the route answers 503 dependency-unavailable.
func TestProviderClientsPresenceToken(t *testing.T) {
	p, sent := tokenStandIn(t)
	_, _, _, _, none, err := providerClients(p)
	if err != nil {
		t.Fatal(err)
	}
	if none != nil {
		t.Fatalf("no presenceTokenFile built %T", none)
	}
	p.PresenceTokenFile = filepath.Join(t.TempDir(), "presence")
	writeFile(t, p.PresenceTokenFile, "synthetic-presence-token\n", 0o600)
	_, _, _, _, pr, err := providerClients(p)
	if err != nil || pr == nil {
		t.Fatalf("%v, %v", pr, err)
	}
	if _, err := pr.CAKeyMetadata(t.Context(), id.New(id.Cluster)); err != nil {
		t.Fatal(err)
	}
	if tokens := sent(); len(tokens) != 1 || tokens[0] != "synthetic-presence-token" {
		t.Fatalf("sent %q, want the presence identity's token once", tokens)
	}
	p.PresenceTokenFile = filepath.Join(t.TempDir(), "missing")
	if _, _, _, _, _, err := providerClients(p); err == nil || !strings.Contains(err.Error(), "provider.presenceTokenFile") {
		t.Fatalf("an unreadable presence token file: %v; want it named by its field", err)
	}
}
