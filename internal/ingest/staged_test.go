package ingest

import (
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/provider"
)

func staged() Staged {
	return Staged{
		Sanitized: newSanitized([]byte("machine:\n  token: !bwref s-a\n"), Declarations{
			References: map[string]Reference{"s-a": {Kind: provider.KindString, Version: 1}, "s-b": {Kind: provider.KindString, Version: 1}},
		}),
		Generations: map[string]string{"s-a": "gen/c/i/v1", "s-b": "gen/c/i/v2"},
		Baseline: Baseline{Ciphertext: "vault:v1:Y2lwaGVy", Digest: [32]byte{1}, DigestKey: "transit/bw-digest@v1",
			Configuration: [32]byte{2}},
	}
}

// Compilation §3 (encrypted staging): the envelope holds the sanitized document, its references
// and the baseline, so a taker has everything the draft transaction persists. Its digest is
// SHA-256 of the plaintext, and sealing the same content twice gives the same bytes.
func TestSeal(t *testing.T) {
	p, sum, err := staged().Seal()
	if err != nil {
		t.Fatal(err)
	}
	again, sum2, _ := staged().Seal()
	if string(p) != string(again) || sum != sum2 || sum != sha256.Sum256(p) {
		t.Fatal("sealing is not deterministic, or the digest is not the plaintext's")
	}
	for _, want := range []string{"!bwref s-a", "gen/c/i/v1", "gen/c/i/v2", "vault:v1:Y2lwaGVy", "transit/bw-digest@v1", `"s-b"`} {
		if !strings.Contains(string(p), want) {
			t.Errorf("the envelope lacks %s: %s", want, p)
		}
	}
	other := staged()
	other.Baseline.Configuration = [32]byte{3}
	if _, s, _ := other.Seal(); s == sum {
		t.Error("the configuration digest is not sealed")
	}
	for name, mutate := range map[string]func(*Staged){
		"no sanitized value":       func(s *Staged) { s.Sanitized = Sanitized{} },
		"no baseline ciphertext":   func(s *Staged) { s.Baseline.Ciphertext = "" },
		"no baseline digest key":   func(s *Staged) { s.Baseline.DigestKey = "" },
		"an undeclared generation": func(s *Staged) { s.Generations["s-z"] = "gen/c/i/v9" },
		"an empty generation path": func(s *Staged) { s.Generations["s-a"] = "" },
	} {
		s := staged()
		mutate(&s)
		if _, _, err := s.Seal(); err == nil {
			t.Errorf("%s: sealed", name)
		}
	}
	// A stream whose values were all declared before (a draft update) creates no generation.
	none := staged()
	none.Generations = nil
	if _, _, err := none.Seal(); err != nil {
		t.Errorf("no generations: %v", err)
	}
}
