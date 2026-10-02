package talos

import (
	"bytes"
	"crypto/sha256"
)

// ConfigurationDigest is execution-recovery.md §1's configuration digest: SHA-256 over a
// configuration with its trailing newlines replaced by exactly one. It is the one function for
// every comparison (choice §10.2), over a read-back and over an artifact's plaintext alike.
func ConfigurationDigest(b []byte) [32]byte {
	h := sha256.New()
	h.Write(bytes.TrimRight(b, "\n"))
	h.Write([]byte{'\n'})
	return [32]byte(h.Sum(nil))
}
