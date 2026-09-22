// Package baseline is Phase-0 evidence code for the secret-ingress feasibility experiment
// (ginsys/bronzeward issue 2). It is not the v1 implementation.
//
// Design §7.1 forbids retaining an observed configuration in a plaintext draft to redact later,
// because backups and history would already hold it. Retaining it encrypted is the permitted way
// to keep the exact bytes, and this is that: the input as transit ciphertext, plus the digest of
// what went in.
//
// The verification is the part issue 2's acceptance criteria had wrong, and the correction matters
// enough to state here. Transit uses a fresh nonce per call, so encrypting identical input twice
// produces different ciphertext — a byte-for-byte comparison of ciphertext across runs fails for a
// *correct* implementation. What actually establishes that the exact configuration was retained is
// two assertions kept separate:
//
//   - per run, sha256(decrypt(ciphertext)) equals the recorded digest of the input, and
//   - across runs over the same input, those recorded input digests are equal.
//
// Make performs the first before it returns, so a baseline that cannot be decrypted back to its
// input is never written anywhere. An encrypted baseline nobody can recover is not a baseline; it
// is a plaintext configuration that has been thrown away.
package baseline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Cipher is the provider capability a baseline needs. The experiment's implementation is OpenBao's
// transit engine; nothing here selects an encryptor for v1.
type Cipher interface {
	Encrypt(ctx context.Context, keyName string, plaintext []byte) (string, error)
	Decrypt(ctx context.Context, keyName, ciphertext string) ([]byte, error)
}

// Baseline is the retained configuration and everything needed to check it, and nothing else. It
// is written to disk and read back by a different process, so every field here ends up in an
// evidence bundle: none of them may be secret.
type Baseline struct {
	// RunID identifies the run that made it.
	RunID string `json:"run_id"`
	// KeyName is the transit key used.
	KeyName string `json:"key_name"`
	// InputSHA256 is the digest of the configuration that was encrypted. It is what makes two
	// runs comparable without either holding the other's plaintext.
	InputSHA256 string `json:"input_sha256"`
	// InputBytes is the input's length, which catches a truncation that happened to preserve a
	// prefix — not a strong check on its own, and free alongside the digest.
	InputBytes int `json:"input_bytes"`
	// Ciphertext is transit's output. It differs between runs over identical input, by design.
	Ciphertext string `json:"ciphertext"`
	// CreatedAt is this program's clock. The provider's own timestamps are collected separately,
	// out of band, precisely because this one is the prototype's.
	CreatedAt time.Time `json:"created_at"`
}

// Make encrypts input and proves the result decrypts back to it before returning.
func Make(ctx context.Context, c Cipher, keyName, runID string, input []byte) (Baseline, error) {
	switch {
	case c == nil:
		return Baseline{}, fmt.Errorf("baseline: no cipher")
	case keyName == "":
		return Baseline{}, fmt.Errorf("baseline: no key name")
	case runID == "":
		return Baseline{}, fmt.Errorf("baseline: no run id")
	case len(input) == 0:
		// An empty baseline would verify perfectly and retain nothing.
		return Baseline{}, fmt.Errorf("baseline: the input is empty; there is nothing to retain")
	}

	sum := sha256.Sum256(input)
	b := Baseline{
		RunID:       runID,
		KeyName:     keyName,
		InputSHA256: hex.EncodeToString(sum[:]),
		InputBytes:  len(input),
		CreatedAt:   time.Now(),
	}

	ciphertext, err := c.Encrypt(ctx, keyName, input)
	if err != nil {
		return Baseline{}, fmt.Errorf("baseline: encrypting: %w", err)
	}
	b.Ciphertext = ciphertext

	// The round trip, before this value can reach anyone. A baseline that cannot be decrypted is
	// worse than no baseline: the configuration is gone and the record says it was kept.
	if err := b.Verify(ctx, c); err != nil {
		return Baseline{}, err
	}
	return b, nil
}

// Verify decrypts the ciphertext and checks it against the recorded digest and length.
//
// It never compares plaintext to plaintext and never returns the decrypted bytes. The comparison
// is between digests, so a failure message can name what went wrong without quoting any of it.
func (b Baseline) Verify(ctx context.Context, c Cipher) error {
	if c == nil {
		return fmt.Errorf("baseline: no cipher")
	}
	if b.Ciphertext == "" || b.InputSHA256 == "" {
		return fmt.Errorf("baseline: incomplete; it holds %s", b.missing())
	}
	// A recorded digest that is not a SHA-256 cannot be compared against, and the mismatch message
	// below abbreviates it; a truncated or hand-edited record would otherwise panic there instead of
	// being reported as the malformed record it is.
	if raw, err := hex.DecodeString(b.InputSHA256); err != nil || len(raw) != sha256.Size {
		return fmt.Errorf("baseline: run %s records an input digest that is not a SHA-256", b.RunID)
	}

	plaintext, err := c.Decrypt(ctx, b.KeyName, b.Ciphertext)
	if err != nil {
		return fmt.Errorf("baseline: decrypting run %s: %w", b.RunID, err)
	}

	if len(plaintext) != b.InputBytes {
		return fmt.Errorf("baseline: run %s decrypts to %d bytes, recorded as %d",
			b.RunID, len(plaintext), b.InputBytes)
	}
	sum := sha256.Sum256(plaintext)
	if got := hex.EncodeToString(sum[:]); got != b.InputSHA256 {
		return fmt.Errorf("baseline: run %s decrypts to digest %s, recorded as %s",
			b.RunID, got[:12], b.InputSHA256[:12])
	}
	return nil
}

// missing names which required field is absent, for an error that does not quote the record.
func (b Baseline) missing() string {
	switch {
	case b.Ciphertext == "" && b.InputSHA256 == "":
		return "neither a ciphertext nor an input digest"
	case b.Ciphertext == "":
		return "an input digest but no ciphertext"
	default:
		return "a ciphertext but no input digest"
	}
}

// SameInput reports whether two baselines were made from identical configurations.
//
// This is the cross-run assertion, and it is deliberately not a comparison of ciphertext. Transit
// nonces differ per call; two baselines of the same input have different ciphertext and the same
// input digest, and it is the digest that carries the claim.
func SameInput(a, b Baseline) bool {
	return a.InputSHA256 != "" && a.InputSHA256 == b.InputSHA256 && a.InputBytes == b.InputBytes
}

// Save writes a baseline as JSON.
func Save(path string, b Baseline) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("baseline: creating %s: %w", filepath.Dir(path), err)
	}
	body, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("baseline: encoding: %w", err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("baseline: writing %s: %w", path, err)
	}
	return nil
}

// Load reads a baseline back. It is a separate entry point from Make because the verification that
// matters is done by a process that is not the one that produced the ciphertext.
func Load(path string) (Baseline, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Baseline{}, fmt.Errorf("baseline: reading %s: %w", path, err)
	}
	var b Baseline
	if err := json.Unmarshal(body, &b); err != nil {
		return Baseline{}, fmt.Errorf("baseline: parsing %s: %w", path, err)
	}
	return b, nil
}
