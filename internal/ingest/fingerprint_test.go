package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/ginsys/bronzeward/internal/baotest"
	"github.com/ginsys/bronzeward/internal/provider"
)

func unresolved(s string) Unresolved {
	b := []byte(s)
	return Unresolved{b: &b}
}

// fakeHMAC is SHA-256 over the version and the input, recording each input; version 0 answers
// as version 1, the latest.
type fakeHMAC struct{ inputs [][]byte }

func (f *fakeHMAC) hmac(_ context.Context, input []byte, version int) (provider.Digest, error) {
	f.inputs = append(f.inputs, bytes.Clone(input))
	if version == 0 {
		version = 1
	}
	return provider.Digest{Key: "bw-digest", Version: version, Sum: sha256.Sum256(append([]byte(strconv.Itoa(version)+":"), input...))}, nil
}

// PA §7.1: an ingestion's fingerprint is keyed and covers the document, and a retry recomputes
// under the recorded version.
func TestFingerprint(t *testing.T) {
	ctx := context.Background()
	f := &fakeHMAC{}
	a, err := Fingerprint(ctx, f.hmac, []byte("m"), unresolved("x: 1\n"), 0)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Fingerprint(ctx, f.hmac, []byte("m"), unresolved("x: 2\n"), 0)
	if a.Sum == b.Sum {
		t.Fatal("the document is not covered")
	}
	// The length prefix keeps the boundary: material "mx" with document ": 1\n" is another request.
	c, _ := Fingerprint(ctx, f.hmac, []byte("mx"), unresolved(": 1\n"), 0)
	if a.Sum == c.Sum {
		t.Fatal("the boundary between the material and the document is not covered")
	}
	d, _ := Fingerprint(ctx, f.hmac, []byte("m"), unresolved("x: 1\n"), 3)
	if d.Version != 3 || d.Sum == a.Sum {
		t.Fatalf("version 3 not honoured: %+v", d)
	}
	if _, err := Fingerprint(ctx, f.hmac, []byte("m"), Unresolved{}, 0); err == nil {
		t.Fatal("the zero Unresolved was fingerprinted")
	}
	failing := func(context.Context, []byte, int) (provider.Digest, error) {
		return provider.Digest{}, errors.New("down")
	}
	if _, err := Fingerprint(ctx, failing, []byte("m"), unresolved("x: 1\n"), 0); err == nil {
		t.Fatal("a failed digest was not reported")
	}
}

// Through the provider: the fingerprint is Transit hmac under bw-digest.
func TestFingerprintLive(t *testing.T) {
	b := baotest.New(t)
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(b.Token("bw-ingestion")), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := provider.ReadTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	i, err := provider.NewIngestion(b.Addr, tok, provider.Keys{Baseline: "bw-baseline", Staging: "bw-staging", Digest: "bw-digest"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := Fingerprint(context.Background(), i.Digest, []byte("m"), unresolved("x: 1\n"), 0)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Fingerprint(context.Background(), i.Digest, []byte("m"), unresolved("x: 1\n"), d.Version)
	if err != nil || again != d || d.Key != "bw-digest" || d.Version < 1 {
		t.Fatalf("%s, again %s: %v", d.KeyRef(), again.KeyRef(), err)
	}
}
