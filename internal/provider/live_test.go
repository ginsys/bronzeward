package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/ginsys/bronzeward/internal/baotest"
)

// The live tests run the committed policy files against a real OpenBao (mise run dev-bao, CI's
// go-db service; see internal/baotest). Each asserts a refusal, a positive control showing the
// token and the object work, and a mechanism-removed control: the same request with the policy
// widened by exactly the refused grant succeeds, so the refusal comes from the policy (or CAS),
// not from a broken token, path or key. The controls are logged, so -v output shows each one run.

var fixtureKeys = Keys{Baseline: "bw-baseline", Staging: "bw-staging", Digest: "bw-digest"}

// Extra grants, each exactly what one refusal withholds.
const (
	genCreateUpdate  = `path "secret/data/gen/*" { capabilities = ["create", "update"] }`
	genRead          = `path "secret/data/gen/*" { capabilities = ["read"] }`
	decryptBaseline  = `path "transit/decrypt/bw-baseline" { capabilities = ["update"] }`
	hmacDigest       = `path "transit/hmac/bw-digest" { capabilities = ["update"] }`
	encryptArtifact  = `path "transit/encrypt/bw-artifact" { capabilities = ["update"] }`
	decryptArtifact  = `path "transit/decrypt/bw-artifact" { capabilities = ["update"] }`
	readTransitKeys  = `path "transit/keys/*" { capabilities = ["read"] }`
	transitDigestEnc = `path "transit/encrypt/bw-digest" { capabilities = ["update"] }`
	transitDigestDec = `path "transit/decrypt/bw-digest" { capabilities = ["update"] }`
	decryptStaging   = `path "transit/decrypt/bw-staging" { capabilities = ["update"] }`
	hmacDigestAlgo   = `path "transit/hmac/bw-digest/sha2-256" { capabilities = ["update"] }`
)

func live(t *testing.T) *baotest.Bao {
	t.Helper()
	b := baotest.New(t)
	for name, hcl := range map[string]string{
		"gen-create-update": genCreateUpdate, "gen-read": genRead, "decrypt-baseline": decryptBaseline,
		"hmac-digest": hmacDigest, "encrypt-artifact": encryptArtifact, "decrypt-artifact": decryptArtifact,
		"read-transit-keys": readTransitKeys, "encrypt-digest": transitDigestEnc, "decrypt-digest": transitDigestDec,
		"decrypt-staging": decryptStaging, "hmac-digest-algorithm": hmacDigestAlgo,
	} {
		b.Policy(name, hcl)
	}
	return b
}

func ingestionAs(t *testing.T, b *baotest.Bao, tok string) *Ingestion {
	t.Helper()
	i, err := NewIngestion(b.Addr, tokenOf(tok), fixtureKeys)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

// raw sends one request as tok through this package's transport, so a refusal is classified the
// way the role type would classify it. The role type has no such call: these are the calls a
// policy must refuse.
func raw(t *testing.T, b *baotest.Bao, tok, method, path string, in any) (*response, error) {
	t.Helper()
	c, err := newClient(b.Addr, tokenOf(tok))
	if err != nil {
		t.Fatal(err)
	}
	var body []byte
	if in != nil {
		if body, err = json.Marshal(in); err != nil {
			t.Fatal(err)
		}
	}
	return c.do(context.Background(), method, path, body, false)
}

func decrypt(t *testing.T, b *baotest.Bao, tok, key string, ct Ciphertext) ([]byte, error) {
	t.Helper()
	r, err := raw(t, b, tok, http.MethodPost, "/v1/transit/decrypt/"+key, map[string]string{"ciphertext": string(ct)})
	if err != nil {
		return nil, err
	}
	var out struct {
		Data struct {
			Plaintext string `json:"plaintext"`
		} `json:"data"`
	}
	if err := r.decode(&out); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.DecodeString(out.Data.Plaintext)
}

func encrypt(t *testing.T, b *baotest.Bao, tok, key string, plain []byte) (Ciphertext, error) {
	t.Helper()
	r, err := raw(t, b, tok, http.MethodPost, "/v1/transit/encrypt/"+key, map[string]string{"plaintext": base64.StdEncoding.EncodeToString(plain)})
	if err != nil {
		return "", err
	}
	var out struct {
		Data struct {
			Ciphertext Ciphertext `json:"ciphertext"`
		} `json:"data"`
	}
	if err := r.decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Data.Ciphertext, nil
}

func hmacAs(t *testing.T, b *baotest.Bao, tok, path string, input []byte) ([]byte, error) {
	t.Helper()
	r, err := raw(t, b, tok, http.MethodPost, path, map[string]string{"input": base64.StdEncoding.EncodeToString(input), "algorithm": "sha2-256"})
	if err != nil {
		return nil, err
	}
	var out struct {
		Data struct {
			HMAC string `json:"hmac"`
		} `json:"data"`
	}
	if err := r.decode(&out); err != nil {
		t.Fatal(err)
	}
	_, rest, err := versioned(out.Data.HMAC)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.DecodeString(rest)
}

// readVersion reads one version's stored data as tok.
func readVersion(t *testing.T, b *baotest.Bao, tok string, p GenerationPath, version string) (map[string]any, error) {
	t.Helper()
	r, err := raw(t, b, tok, http.MethodGet, kvDataPath(p)+"?version="+version, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data struct {
			Data map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := r.decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Data.Data, nil
}

// writeNoCAS is a KV v2 write without cas, as tok; it returns the version written.
func writeNoCAS(t *testing.T, b *baotest.Bao, tok string, p GenerationPath, value string) (int, error) {
	t.Helper()
	r, err := raw(t, b, tok, http.MethodPost, kvDataPath(p), map[string]any{"data": map[string]any{"kind": "string", "value": value}})
	if err != nil {
		return 0, err
	}
	var out struct {
		Data struct {
			Version int `json:"version"`
		} `json:"data"`
	}
	if err := r.decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Data.Version, nil
}

func mustDeny(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("%s: %v, want ErrDenied", what, err)
	}
	t.Logf("refused: %s", what)
}

// deniedWithout asserts that call is refused as a token holding policies, and succeeds as one
// holding policies plus grant: the refusal is grant's absence, not a broken path or key.
func deniedWithout(t *testing.T, b *baotest.Bao, what string, policies []string, grant string, call func(tok string) error) {
	t.Helper()
	mustDeny(t, what, call(b.Token(policies...)))
	if err := call(b.Token(append(policies, grant)...)); err != nil {
		t.Fatalf("mechanism removed: %s with %s added: %v", what, grant, err)
	}
	t.Logf("mechanism removed: %s succeeds with %s added", what, grant)
}

// A replace of an existing generation is refused to ingestion; CAS alone refuses even the
// administrator; without the policy's create-only grant the same path is replaced.
func TestLiveReplaceRefused(t *testing.T) {
	b := live(t)
	ctx := t.Context()
	ing, comp := b.Token("bw-ingestion"), b.Token("bw-compiler")
	i := ingestionAs(t, b, ing)
	p := newPath(t)
	original := "original-" + NewValueID()
	g, err := i.CreateGeneration(ctx, p, mustValue(t, KindString, original))
	if err != nil || g.Version != 1 {
		t.Fatalf("positive control: ingestion's create: %+v, %v", g, err)
	}
	t.Logf("positive control: ingestion created %s as version 1", p)

	_, err = i.CreateGeneration(ctx, p, mustValue(t, KindString, "replacement"))
	switch {
	case errors.Is(err, ErrDenied):
		t.Logf("assumption 2: ingestion's cas=0 create on an existing path is refused by the ACL (403, ErrDenied)")
	case errors.Is(err, ErrExists):
		t.Logf("assumption 2 false: ingestion's cas=0 create on an existing path is refused by CAS (400, ErrExists), not the ACL")
	default:
		t.Fatalf("ingestion replaced or failed otherwise: %v", err)
	}
	// The ACL alone: the same cas=0 create with the policy plus update reaches check-and-set.
	if _, err := ingestionAs(t, b, b.Token("bw-ingestion", "gen-create-update")).CreateGeneration(ctx, p, mustValue(t, KindString, "replacement")); !errors.Is(err, ErrExists) {
		t.Fatalf("ingestion's policy plus update, cas=0: %v, want ErrExists", err)
	}
	t.Logf("mechanism removed: with update added, ingestion's cas=0 create is refused by CAS (ErrExists) instead")

	// CAS on its own: the administrator's cas=0 create on the same path.
	if _, err := ingestionAs(t, b, b.Admin()).CreateGeneration(ctx, p, mustValue(t, KindString, "admin-cas0")); !errors.Is(err, ErrExists) {
		t.Fatalf("the administrator's cas=0 create: %v, want ErrExists", err)
	}
	t.Logf("refused: the administrator's cas=0 create on an existing path (ErrExists, CAS alone)")
	// Control: the path is not cas_required; the administrator writes version 2 without cas.
	if v, err := writeNoCAS(t, b, b.Admin(), p, "admin-v2"); err != nil || v != 2 {
		t.Fatalf("control: the administrator's write without cas: version %d, %v", v, err)
	}
	t.Logf("control: the administrator's write without cas made version 2, so create-only is the policy's")

	// Mechanism removed: create and update on gen/*, no cas.
	if v, err := writeNoCAS(t, b, b.Token("gen-create-update"), p, "replaced"); err != nil || v != 3 {
		t.Fatalf("mechanism removed: create+update without cas: version %d, %v", v, err)
	}
	t.Logf("mechanism removed: a token with create+update replaced the path as version 3")

	got, err := readVersion(t, b, comp, p, "1")
	if err != nil || got["kind"] != "string" || got["value"] != original {
		t.Fatalf("the compiler's read of version 1: %v, %v", got, err)
	}
	t.Logf("the compiler read version 1 and got the original value")
}

func TestLiveExecutorCannotDecryptBaseline(t *testing.T) {
	b := live(t)
	ctx := t.Context()
	ing, comp, exec := b.Token("bw-ingestion"), b.Token("bw-compiler"), b.Token("bw-executor")
	i := ingestionAs(t, b, ing)
	baseline := []byte("baseline-" + NewValueID())
	ct, err := i.EncryptBaseline(ctx, baseline)
	if err != nil {
		t.Fatal(err)
	}

	_, err = decrypt(t, b, exec, "bw-baseline", ct)
	mustDeny(t, "the executor decrypting a baseline", err)

	if got, err := decrypt(t, b, b.Admin(), "bw-baseline", ct); err != nil || !bytes.Equal(got, baseline) {
		t.Fatalf("control: the administrator's decrypt: %v", err)
	}
	t.Logf("control: the administrator decrypted the baseline to the original bytes")
	art, err := encrypt(t, b, comp, "bw-artifact", []byte("artifact"))
	if err != nil {
		t.Fatalf("control: the compiler's artifact encrypt: %v", err)
	}
	if got, err := decrypt(t, b, exec, "bw-artifact", art); err != nil || string(got) != "artifact" {
		t.Fatalf("control: the executor's artifact decrypt: %v", err)
	}
	t.Logf("control: the executor decrypted a compiler-made artifact")
	if got, err := decrypt(t, b, b.Token("bw-executor", "decrypt-baseline"), "bw-baseline", ct); err != nil || !bytes.Equal(got, baseline) {
		t.Fatalf("mechanism removed: executor plus decrypt on bw-baseline: %v", err)
	}
	t.Logf("mechanism removed: the executor policy plus decrypt on bw-baseline decrypted it")

	stg, err := i.EncryptStaging(ctx, []byte("envelope"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ what, policy, key, grant string }{
		{"ingestion decrypting a baseline", "bw-ingestion", "bw-baseline", "decrypt-baseline"},
		{"the compiler decrypting a baseline", "bw-compiler", "bw-baseline", "decrypt-baseline"},
		{"the executor decrypting a staged envelope", "bw-executor", "bw-staging", "decrypt-staging"},
		{"the compiler decrypting a staged envelope", "bw-compiler", "bw-staging", "decrypt-staging"},
	} {
		in := map[string]Ciphertext{"bw-baseline": ct, "bw-staging": stg}[c.key]
		deniedWithout(t, b, c.what, []string{c.policy}, c.grant, func(tok string) error {
			_, err := decrypt(t, b, tok, c.key, in)
			return err
		})
	}
}

func TestLiveIngestionCannotReadSecret(t *testing.T) {
	b := live(t)
	ing := b.Token("bw-ingestion")
	p := newPath(t)
	value := "value-" + NewValueID()
	if _, err := ingestionAs(t, b, ing).CreateGeneration(t.Context(), p, mustValue(t, KindString, value)); err != nil {
		t.Fatal(err)
	}
	_, err := readVersion(t, b, ing, p, "1")
	mustDeny(t, "ingestion reading its own new generation", err)
	if got, err := readVersion(t, b, b.Token("bw-compiler"), p, "1"); err != nil || got["value"] != value {
		t.Fatalf("control: the compiler's read: %v, %v", got, err)
	}
	t.Logf("control: the compiler read it")
	if got, err := readVersion(t, b, b.Token("bw-ingestion", "gen-read"), p, "1"); err != nil || got["value"] != value {
		t.Fatalf("mechanism removed: ingestion plus read: %v, %v", got, err)
	}
	t.Logf("mechanism removed: the ingestion policy plus read on gen/* read it")
}

// D2: the digest is Transit hmac under bw-digest, deterministic, keyed, and the key is
// ingestion's alone and used for nothing else.
func TestLiveDigestKeyIngestionOnly(t *testing.T) {
	b := live(t)
	ctx := t.Context()
	ing := b.Token("bw-ingestion")
	i := ingestionAs(t, b, ing)
	x, y := []byte("low-entropy-"+NewValueID()), []byte("other-"+NewValueID())
	d1, err := i.Digest(ctx, x, 0)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := i.Digest(ctx, x, d1.Version)
	if err != nil {
		t.Fatal(err)
	}
	d3, err := i.Digest(ctx, y, 0)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 || d1.Sum == d3.Sum || d1.Sum == sha256.Sum256(x) {
		t.Fatalf("not a deterministic keyed digest: %+v %+v %+v", d1, d2, d3)
	}
	r, err := raw(t, b, b.Token("read-transit-keys"), http.MethodGet, "/v1/transit/keys/bw-digest", nil)
	if err != nil {
		t.Fatal(err)
	}
	var key struct {
		Data struct {
			Type string `json:"type"`
		} `json:"data"`
	}
	if err := r.decode(&key); err != nil {
		t.Fatal(err)
	}
	t.Logf("assumption 1: transit/hmac/bw-digest with input (base64), algorithm sha2-256 and key_version in the body "+
		"answered vault:v%d:<32 bytes> on a %s key; equal for equal input, unequal for other input, not sha256(input); KeyRef %s",
		d1.Version, key.Data.Type, d1.KeyRef())

	hmacOn := func(path string) func(string) error {
		return func(tok string) error { _, err := hmacAs(t, b, tok, path, x); return err }
	}
	for name, policy := range map[string]string{
		"the compiler": "bw-compiler", "the executor": "bw-executor", "the fixture metadata policy": "bw-metadata-only",
	} {
		deniedWithout(t, b, name+" computing an HMAC with bw-digest", []string{policy}, "hmac-digest", hmacOn("/v1/transit/hmac/bw-digest"))
	}
	deniedWithout(t, b, "ingestion's HMAC on the algorithm-suffixed path", []string{"bw-ingestion"}, "hmac-digest-algorithm", hmacOn("/v1/transit/hmac/bw-digest/sha2-256"))
	deniedWithout(t, b, "ingestion encrypting with bw-digest", []string{"bw-ingestion"}, "encrypt-digest", func(tok string) error {
		_, err := encrypt(t, b, tok, "bw-digest", x)
		return err
	})
	ct, err := encrypt(t, b, b.Token("encrypt-digest"), "bw-digest", x)
	if err != nil {
		t.Fatal(err)
	}
	deniedWithout(t, b, "ingestion decrypting with bw-digest", []string{"bw-ingestion"}, "decrypt-digest", func(tok string) error {
		_, err := decrypt(t, b, tok, "bw-digest", ct)
		return err
	})

	sum, err := hmacAs(t, b, b.Token("bw-compiler", "hmac-digest"), "/v1/transit/hmac/bw-digest", x)
	if err != nil || !bytes.Equal(sum, d1.Sum[:]) {
		t.Fatalf("mechanism removed: compiler plus hmac on bw-digest: %v", err)
	}
	t.Logf("mechanism removed: the compiler policy plus hmac on bw-digest computed the same digest")
}

// Claims 1 and 9: ingestion round-trips its own staging envelope.
func TestLiveIngestionStagingRoundTrip(t *testing.T) {
	b := live(t)
	i := ingestionAs(t, b, b.Token("bw-ingestion"))
	envelope := []byte(`{"sanitized":"…","baseline":"vault:v1:…"}` + NewValueID())
	ct, err := i.EncryptStaging(t.Context(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := ct.KeyVersion(); err != nil || v < 1 {
		t.Fatalf("KeyVersion %d, %v", v, err)
	}
	got, err := i.DecryptStaging(t.Context(), ct)
	if err != nil || !bytes.Equal(got, envelope) {
		t.Fatalf("round trip: %v", err)
	}
	t.Logf("ingestion encrypted and decrypted its staging envelope")
}

func TestLiveIngestionCannotDecryptArtifact(t *testing.T) {
	b := live(t)
	ing := b.Token("bw-ingestion")
	art, err := encrypt(t, b, b.Token("bw-compiler"), "bw-artifact", []byte("artifact"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = decrypt(t, b, ing, "bw-artifact", art)
	mustDeny(t, "ingestion decrypting an artifact", err)
	if got, err := decrypt(t, b, b.Token("bw-executor"), "bw-artifact", art); err != nil || string(got) != "artifact" {
		t.Fatalf("control: the executor's decrypt: %v", err)
	}
	t.Logf("control: the executor decrypted it")
	if got, err := decrypt(t, b, b.Token("bw-ingestion", "decrypt-artifact"), "bw-artifact", art); err != nil || string(got) != "artifact" {
		t.Fatalf("mechanism removed: ingestion plus decrypt on bw-artifact: %v", err)
	}
	t.Logf("mechanism removed: the ingestion policy plus decrypt on bw-artifact decrypted it")
	deniedWithout(t, b, "ingestion encrypting with the artifact key", []string{"bw-ingestion"}, "encrypt-artifact", func(tok string) error {
		_, err := encrypt(t, b, tok, "bw-artifact", []byte("x"))
		return err
	})
}
