package provider

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/accessput"
	"github.com/ginsys/bronzeward/internal/baotest"
	"github.com/ginsys/bronzeward/internal/id"
)

// The onboarding identity (fixtures/openbao/policies/bw-onboard.hcl) against a real OpenBao: the CLI's
// write package creates the secret the ingestion identity reads, and the token that wrote it can
// do nothing else with the path.
func TestLiveAccessPut(t *testing.T) {
	b := baotest.New(t)
	cl := id.New(id.Cluster)
	p, err := TalosAccessPath(cl)
	if err != nil {
		t.Fatal(err)
	}
	dataPath := "/v1/secret/data/" + p
	doc := "context: a\ncontexts:\n  a:\n    key: synthetic-" + NewValueID() + "\n"
	put := func(tok string, document string) error {
		parsed, err := accessput.ParseToken([]byte(tok))
		if err != nil {
			t.Fatal(err)
		}
		return accessput.Put(t.Context(), b.Addr, parsed, cl, []byte(document))
	}
	onboard := b.Token("bw-onboard")

	if err := put(onboard, doc); err != nil {
		t.Fatalf("the onboarding token's create: %v", err)
	}

	// The server's own reader accepts exactly what the CLI wrote.
	a, err := ingestionAs(t, b, b.Token("bw-ingestion")).TalosAccess(t.Context(), cl)
	if err != nil {
		t.Fatalf("ingestion's read of the CLI's write: %v", err)
	}
	if v := a.Version(); string(a.Talosconfig()) != doc || v.Path != p || v.Version != 1 {
		t.Fatalf("ingestion read version %+v", v)
	}

	// The token that wrote it can do nothing else: no read, no list, no delete, no metadata, no
	// update. A write to an existing path is an update to OpenBao's ACL, so a rerun with the same
	// token reads as denied, never as a replacement.
	for name, req := range map[string]struct {
		method, path string
		in           any
	}{
		"read":              {http.MethodGet, dataPath, nil},
		"list":              {"LIST", "/v1/secret/metadata/access/talos", nil},
		"read metadata":     {http.MethodGet, "/v1/secret/metadata/" + p, nil},
		"delete":            {http.MethodDelete, dataPath, nil},
		"delete metadata":   {http.MethodDelete, "/v1/secret/metadata/" + p, nil},
		"update":            {http.MethodPost, dataPath, map[string]any{"data": map[string]string{"talosconfig": "replaced"}}},
		"another namespace": {http.MethodPost, "/v1/secret/data/gen/" + cl + "/x/y", map[string]any{"data": map[string]string{"a": "b"}}},
	} {
		status, _, err := b.Do(onboard, req.method, req.path, req.in)
		if err != nil || status != http.StatusForbidden {
			t.Errorf("onboarding token, %s: status %d, %v; want 403", name, status, err)
		}
	}
	if err := put(onboard, "replaced"); !errors.Is(err, accessput.ErrDenied) {
		t.Errorf("a rerun with the create-only token: %v, want ErrDenied", err)
	}

	// An identity with no grant on the path is denied, and the error names no byte of the write.
	err = put(b.Token("bw-ingestion"), doc)
	if !errors.Is(err, accessput.ErrDenied) {
		t.Errorf("the ingestion token's create: %v, want ErrDenied", err)
	}
	if err != nil && strings.Contains(err.Error(), "synthetic-") {
		t.Errorf("the error carries the document: %v", err)
	}

	// With the right to update, a second create still replaces nothing: check-and-set 0 refuses
	// it, and the write package reads that as present.
	b.Policy("onboard-update", `path "secret/data/access/talos/*" { capabilities = ["create", "update"] }`)
	if err := put(b.Token("onboard-update"), "replaced"); !errors.Is(err, accessput.ErrPresent) {
		t.Errorf("a create over an existing secret with update granted: %v, want ErrPresent", err)
	}

	// Whatever happened above, the stored version is still the first one, unchanged.
	again, err := ingestionAs(t, b, b.Token("bw-ingestion")).TalosAccess(t.Context(), cl)
	if err != nil {
		t.Fatal(err)
	}
	if string(again.Talosconfig()) != doc || again.Version().Version != 1 {
		t.Fatalf("the stored secret changed: version %d", again.Version().Version)
	}
}
