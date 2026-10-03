package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/ginsys/bronzeward/internal/provider"
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

// errEnvelope refuses a staged payload that is not a complete envelope. Its text names no part
// of the payload.
var errEnvelope = errors.New("ingest: the staged payload is not a complete envelope")

// Open is a resume's half of Seal (compilation §3.1): it checks plaintext against sum, the
// digest stored beside the payload, refuses anything but a complete version-1 envelope, checks
// the sanitized stream as authored, and only then constructs the sanitized value. It cannot run
// the guard: the extracted values are in the provider. Every declared name must have the
// generation the draft transaction records for it, and every generation a declared name.
func Open(plaintext []byte, sum [32]byte) (Staged, error) {
	if sha256.Sum256(plaintext) != sum {
		return Staged{}, errors.New("ingest: the staged payload does not match its digest")
	}
	var e envelope
	dec := json.NewDecoder(bytes.NewReader(plaintext))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return Staged{}, errEnvelope
	}
	// More reports false before a stray ] or }; only the end of input ends the envelope.
	if _, err := dec.Token(); err != io.EOF {
		return Staged{}, errEnvelope
	}
	if e.Version != 1 || e.Documents == "" || e.Baseline.Ciphertext == "" || e.Baseline.DigestKey == "" {
		return Staged{}, errEnvelope
	}
	digest, err1 := digest32(e.Baseline.Digest)
	conf, err2 := digest32(e.Baseline.Configuration)
	if err1 != nil || err2 != nil {
		return Staged{}, errEnvelope
	}
	docs, err := parseStream([]byte(e.Documents))
	if err != nil {
		return Staged{}, errEnvelope
	}
	if err := validate(docs, e.Declarations); err != nil {
		return Staged{}, errEnvelope
	}
	if len(e.Generations) != len(e.Declarations.References) {
		return Staged{}, errEnvelope
	}
	for name, path := range e.Generations {
		if _, ok := e.Declarations.References[name]; !ok || path == "" {
			return Staged{}, errEnvelope
		}
	}
	return Staged{
		Sanitized:   newSanitized([]byte(e.Documents), e.Declarations),
		Generations: e.Generations,
		Baseline: Baseline{Ciphertext: provider.Ciphertext(e.Baseline.Ciphertext), Digest: digest,
			DigestKey: e.Baseline.DigestKey, Configuration: conf},
	}, nil
}

func digest32(s string) ([32]byte, error) {
	var d [32]byte
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(d) {
		return d, errEnvelope
	}
	copy(d[:], b)
	return d, nil
}
