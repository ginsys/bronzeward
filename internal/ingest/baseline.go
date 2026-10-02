package ingest

import (
	"context"
	"errors"

	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos"
)

// Baseline is compilation.md §2.3 step 8 for an import: the exact input encrypted under the
// baseline key, its keyed digest with the key that computed it, and its configuration digest
// (execution-recovery.md §1, choice §16.26). The draft transaction persists it with the draft.
type Baseline struct {
	Ciphertext    provider.Ciphertext
	Digest        [32]byte
	DigestKey     string // provider.Digest.KeyRef()
	Configuration [32]byte
}

// ComputeBaseline performs step 8 over u, encrypting through enc (the baseline key's encryption,
// (*provider.Ingestion).EncryptBaseline) and digesting through h under the latest key version.
func ComputeBaseline(ctx context.Context, u Unresolved, enc func(context.Context, []byte) (provider.Ciphertext, error), h HMAC) (Baseline, error) {
	in := u.bytes()
	if len(in) == 0 {
		return Baseline{}, ErrEmptyInput
	}
	ct, err := enc(ctx, in)
	if err != nil {
		return Baseline{}, errors.Join(errors.New("ingest: the baseline could not be encrypted"), err)
	}
	d, err := h(ctx, in, 0)
	if err != nil {
		return Baseline{}, errors.Join(errors.New("ingest: the baseline digest could not be computed"), err)
	}
	return Baseline{Ciphertext: ct, Digest: d.Sum, DigestKey: d.KeyRef(), Configuration: talos.ConfigurationDigest(in)}, nil
}
