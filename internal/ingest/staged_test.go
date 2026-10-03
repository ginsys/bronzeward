package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
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

// sealedImport is the envelope an import run seals: multiDoc extracted with one mark, a
// generation per minted name, and a baseline.
func sealedImport(t *testing.T) (Staged, []byte, [32]byte) {
	t.Helper()
	c, err := Extract(request(t, multiDoc, "doc[0]/machine/nodeLabels/a~1b~0c.d"))
	if err != nil {
		t.Fatal(err)
	}
	s, calls := commitAll(t, c)
	gens := map[string]string{}
	for i, cr := range calls {
		gens[cr.name] = fmt.Sprintf("gen/c/i/v%d", i)
	}
	st := Staged{Sanitized: s, Generations: gens, Baseline: Baseline{Ciphertext: "vault:v1:Y2lwaGVy", Digest: [32]byte{1},
		DigestKey: "transit/bw-digest@v1", Configuration: [32]byte{2}}}
	p, sum, err := st.Seal()
	if err != nil {
		t.Fatal(err)
	}
	return st, p, sum
}

// Compilation §3.1: a resume refuses a payload that is not a complete envelope, checks its
// digest, and only then constructs the sanitized value. What it opens is what was sealed.
func TestOpen(t *testing.T) {
	st, p, sum := sealedImport(t)
	got, err := Open(p, sum)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Sanitized.Documents()) != string(st.Sanitized.Documents()) ||
		!reflect.DeepEqual(got.Sanitized.Declarations(), st.Sanitized.Declarations()) ||
		!maps.Equal(got.Generations, st.Generations) || got.Baseline != st.Baseline {
		t.Fatalf("opened %+v, sealed %+v", got, st)
	}
	if again, _, _ := got.Seal(); string(again) != string(p) {
		t.Error("the opened envelope does not seal to the same bytes")
	}

	// A complete envelope under another digest: only the digest check can refuse it.
	other := sum
	other[0] ^= 1
	if _, err := Open(bytes.Clone(p), other); err == nil {
		t.Error("a payload that does not match its digest opened")
	}

	// Each case edits the decoded envelope and re-digests it, so only the structural check refuses.
	for name, edit := range map[string]func(m map[string]any){
		"an unknown member":         func(m map[string]any) { m["extra"] = true },
		"version 2":                 func(m map[string]any) { m["version"] = 2 },
		"no documents":              func(m map[string]any) { m["documents"] = "" },
		"no baseline ciphertext":    func(m map[string]any) { m["baseline"].(map[string]any)["ciphertext"] = "" },
		"no baseline digest key":    func(m map[string]any) { m["baseline"].(map[string]any)["digestKey"] = "" },
		"a short baseline digest":   func(m map[string]any) { m["baseline"].(map[string]any)["digest"] = "0102" },
		"a configuration not hex":   func(m map[string]any) { m["baseline"].(map[string]any)["configuration"] = strings.Repeat("zz", 32) },
		"an undeclared generation":  func(m map[string]any) { m["generations"].(map[string]any)["s-z"] = "gen/c/i/v9" },
		"a name with no generation": func(m map[string]any) { m["generations"] = map[string]any{} },
		"a generation under another name": func(m map[string]any) { // as many generations as names
			g := m["generations"].(map[string]any)
			for k, v := range g {
				delete(g, k)
				g["s-z"] = v
				break
			}
		},
		"an empty generation path": func(m map[string]any) {
			for k := range m["generations"].(map[string]any) {
				m["generations"].(map[string]any)[k] = ""
			}
		},
		"a document that does not parse": func(m map[string]any) { m["documents"] = "machine: [" + secretText },
		"an undeclared reference": func(m map[string]any) {
			m["documents"] = m["documents"].(string) + "---\nmachine:\n  token: !bwref s-undeclared\n"
		},
		"not an object": func(m map[string]any) { clear(m) },
	} {
		t.Run(name, func(t *testing.T) {
			var m map[string]any
			if err := json.Unmarshal(p, &m); err != nil {
				t.Fatal(err)
			}
			edit(m)
			b, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if name == "not an object" {
				b = []byte(`["` + secretText + `"]`)
			}
			_, err = Open(b, sha256.Sum256(b))
			if err == nil {
				t.Fatal("opened")
			}
			if strings.Contains(fmt.Sprintf("%v %+v", err, err), secretText) {
				t.Errorf("the refusal quotes the payload: %v", err)
			}
		})
	}

	// A complete envelope followed by anything but whitespace is not one envelope, under its own
	// digest; trailing whitespace is.
	for _, tail := range []string{"]", "}", "]garbage", " {}", "\n" + `"` + secretText + `"`} {
		b := append(bytes.Clone(p), tail...)
		if _, err := Open(b, sha256.Sum256(b)); err == nil {
			t.Errorf("an envelope followed by %q opened", tail)
		}
	}
	b := append(bytes.Clone(p), " \n"...)
	if _, err := Open(b, sha256.Sum256(b)); err != nil {
		t.Errorf("an envelope followed by whitespace: %v", err)
	}
}
