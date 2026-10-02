package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// Staged is an encrypted staging claim's envelope (compilation.md §3): the sanitized document,
// its declarations, the generation each extracted value was created at, and the baseline, so that
// a taker has everything the draft transaction persists.
type Staged struct {
	Sanitized   Sanitized
	Generations map[string]string // logical name -> generation path
	Baseline    Baseline
}

// envelope is Staged's encoding. Map members are sorted by encoding/json, so equal content seals
// to equal bytes.
type envelope struct {
	Version      int               `json:"version"`
	Documents    string            `json:"documents"`
	Declarations Declarations      `json:"declarations"`
	Generations  map[string]string `json:"generations"`
	Baseline     envelopeBaseline  `json:"baseline"`
}

type envelopeBaseline struct {
	Ciphertext    string `json:"ciphertext"`
	Digest        string `json:"digest"`
	DigestKey     string `json:"digestKey"`
	Configuration string `json:"configuration"`
}

// Seal checks s and returns the envelope plaintext, to be encrypted under the staging key, and
// its SHA-256, which the claim stores as payload_digest. Every generation must name a declared
// reference; a value declared before this ingestion has none.
func (s Staged) Seal() (plaintext []byte, sum [32]byte, err error) {
	if err := s.Sanitized.Check(); err != nil {
		return nil, sum, err
	}
	if s.Baseline.Ciphertext == "" || s.Baseline.DigestKey == "" {
		return nil, sum, errors.New("ingest: an import's envelope requires its baseline")
	}
	decl := s.Sanitized.Declarations()
	gens := map[string]string{}
	for name, path := range s.Generations {
		if _, ok := decl.References[name]; !ok {
			return nil, sum, fmt.Errorf("ingest: generation %q is for an undeclared name", name)
		}
		if path == "" {
			return nil, sum, fmt.Errorf("ingest: generation %q has no path", name)
		}
		gens[name] = path
	}
	b, err := json.Marshal(envelope{
		Version:      1,
		Documents:    string(s.Sanitized.Documents()),
		Declarations: decl,
		Generations:  gens,
		Baseline: envelopeBaseline{
			Ciphertext:    string(s.Baseline.Ciphertext),
			Digest:        hex.EncodeToString(s.Baseline.Digest[:]),
			DigestKey:     s.Baseline.DigestKey,
			Configuration: hex.EncodeToString(s.Baseline.Configuration[:]),
		},
	})
	if err != nil {
		return nil, sum, errors.New("ingest: the envelope could not be encoded")
	}
	return b, sha256.Sum256(b), nil
}
