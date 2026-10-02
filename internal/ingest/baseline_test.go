package ingest

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos"
)

// Compilation §2.3 step 8: the baseline encrypts the exact input, and records its keyed digest
// and its configuration digest (ER §1) over the same bytes.
func TestComputeBaseline(t *testing.T) {
	ctx := context.Background()
	in := "machine:\n  token: " + secretText + "\n\n"
	var encrypted []byte
	enc := func(_ context.Context, p []byte) (provider.Ciphertext, error) {
		encrypted = bytes.Clone(p)
		return "vault:v1:Y2lwaGVy", nil
	}
	f := &fakeHMAC{}
	b, err := ComputeBaseline(ctx, unresolved(in), enc, f.hmac)
	if err != nil {
		t.Fatal(err)
	}
	if string(encrypted) != in || len(f.inputs) != 1 || string(f.inputs[0]) != in {
		t.Fatalf("encrypted %q, digested %q; want the exact input", encrypted, f.inputs)
	}
	want, _ := f.hmac(ctx, []byte(in), 0)
	if b.Ciphertext != "vault:v1:Y2lwaGVy" || b.Digest != want.Sum || b.DigestKey != "transit/bw-digest@v1" ||
		b.Configuration != talos.ConfigurationDigest([]byte(in)) {
		t.Fatalf("baseline %+v", b)
	}
	if _, err := ComputeBaseline(ctx, Unresolved{}, enc, f.hmac); err == nil {
		t.Fatal("the zero Unresolved has a baseline")
	}
	fail := errors.New("down")
	if _, err := ComputeBaseline(ctx, unresolved(in), func(context.Context, []byte) (provider.Ciphertext, error) { return "", fail }, f.hmac); !errors.Is(err, fail) {
		t.Fatalf("a failed encryption: %v", err)
	}
	if _, err := ComputeBaseline(ctx, unresolved(in), enc, func(context.Context, []byte, int) (provider.Digest, error) { return provider.Digest{}, fail }); !errors.Is(err, fail) {
		t.Fatalf("a failed digest: %v", err)
	}
}
