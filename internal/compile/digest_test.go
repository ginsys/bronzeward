package compile

import (
	"testing"

	"github.com/ginsys/bronzeward/internal/ingest"
	"github.com/ginsys/bronzeward/internal/talos"
)

// A materialized configuration's digest is execution and recovery's configuration digest over
// its plaintext, the one publication records beside the ciphertext (persistence-api.md §1.1
// item 2); a configuration never composed has none.
func TestMaterializedDigest(t *testing.T) {
	m, err := Compose(resolved(t, string(generatedBase(t)), ingest.Declarations{}, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if want := talos.ConfigurationDigest(m.bytes()); got != want {
		t.Fatalf("digest %x, want %x", got, want)
	}
	if _, err := (Materialized{}).Digest(); err == nil {
		t.Fatal("a configuration never composed has a digest")
	}
}
