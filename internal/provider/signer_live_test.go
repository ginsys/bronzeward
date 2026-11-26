package provider

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/internal/baotest"
	"github.com/ginsys/bronzeward/internal/id"
)

// The signer identity (persistence-api.md §3.4) against a real OpenBao: its policy as the provider
// holds it, its client's reads and writes of the CA key path, and the refusals that keep every other
// identity off that path and the signer off everything else. Each refusal is run beside the same
// request with the one withheld grant added, so it comes from the policy and not from a broken
// token or path.

const (
	caKeyRead      = `path "secret/data/access/talos-ca/*" { capabilities = ["read"] }`
	caKeyDelete    = `path "secret/data/access/talos-ca/*" { capabilities = ["delete"] }`
	caKeyList      = `path "secret/metadata/access/talos-ca/*" { capabilities = ["list"] }`
	caKeyMetaRead  = `path "secret/metadata/access/talos-ca/*" { capabilities = ["read"] }`
	genCreateOnlyS = `path "secret/data/gen/*" { capabilities = ["create"] }`
)

func signerGrants(b *baotest.Bao) {
	for name, hcl := range map[string]string{
		"ca-key-read": caKeyRead, "ca-key-delete": caKeyDelete, "ca-key-list": caKeyList,
		"ca-key-meta-read": caKeyMetaRead, "gen-create-only-s": genCreateOnlyS,
	} {
		b.Policy(name, hcl)
	}
}

func TestLiveSignerPolicyExact(t *testing.T) {
	b := live(t)
	policyExact(t, b, "bw-signer", map[string][]string{"secret/data/access/talos-ca/*": {"create", "read", "update"}}, []string{
		caKeyDelete, caKeyList, caKeyMetaRead, genRead, encryptArtifact, decryptArtifact,
		`path "secret/data/access/talos-ca/*" { capabilities = ["create", "read", "update", "delete"] }`,
	})
}

// ReadCAKeyVersion against a real OpenBao: one named version is read after a newer one exists, and a
// version that is deleted, destroyed or never written is ErrAbsent with no fallback to the latest.
// The control is ReadCAKey, which answers the latest whatever version the caller meant.
func TestLiveReadCAKeyVersion(t *testing.T) {
	b := live(t)
	signerGrants(b)
	ctx := t.Context()
	cl := id.New(id.Cluster)
	p, err := CAKeyPath(cl)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewSigner(b.Addr, tokenOf(b.Token("bw-signer")))
	if err != nil {
		t.Fatal(err)
	}
	first := NewCAKey([]byte(caText), []byte(keyText))
	second := NewCAKey([]byte(caText+"2"), []byte(keyText+"2"))
	v1, err := signer.WriteCAKey(ctx, cl, first, 0)
	if err != nil || v1.Version != 1 {
		t.Fatalf("the create: %+v, %v", v1, err)
	}
	if v2, err := signer.WriteCAKey(ctx, cl, second, 1); err != nil || v2.Version != 2 {
		t.Fatalf("the replacement: %+v, %v", v2, err)
	}

	// Version 1 is read as version 1 with its own created time, though version 2 is the latest;
	// "latest" answers version 2, which is the signing from latest this read exists to prevent.
	k, err := signer.ReadCAKeyVersion(ctx, cl, 1)
	if err != nil || k.Version() != v1 || string(k.Key()) != keyText {
		t.Fatalf("version 1 after version 2: %+v, %v", k.Version(), err)
	}
	if latest, err := signer.ReadCAKey(ctx, cl); err != nil || latest.Version().Version != 2 {
		t.Fatalf("control: the latest read: %+v, %v", latest.Version(), err)
	}
	if k2, err := signer.ReadCAKeyVersion(ctx, cl, 2); err != nil || k2.Version().Version != 2 || string(k2.Key()) != keyText+"2" {
		t.Fatalf("version 2: %+v, %v", k2.Version(), err)
	}

	// A version that was never written is absent, and the latest still reads.
	if _, err := signer.ReadCAKeyVersion(ctx, cl, 3); !errors.Is(err, ErrAbsent) {
		t.Fatalf("a never-written version 3: %v, want ErrAbsent", err)
	}

	// Soft-deleting version 1 (administrator) makes it absent; version 2 still reads.
	if status, body, err := b.Do(b.Admin(), http.MethodPost, "/v1/secret/delete/"+p, map[string]any{"versions": []int{1}}); err != nil || status/100 != 2 {
		t.Fatalf("control: the soft delete of version 1: status %d, %v: %s", status, err, body)
	}
	if _, err := signer.ReadCAKeyVersion(ctx, cl, 1); !errors.Is(err, ErrAbsent) {
		t.Fatalf("a soft-deleted version 1: %v, want ErrAbsent", err)
	}
	// Undeleting returns it, so the refusal came from the deletion and not from the read.
	if status, body, err := b.Do(b.Admin(), http.MethodPost, "/v1/secret/undelete/"+p, map[string]any{"versions": []int{1}}); err != nil || status/100 != 2 {
		t.Fatalf("control: the undelete of version 1: status %d, %v: %s", status, err, body)
	}
	if k, err := signer.ReadCAKeyVersion(ctx, cl, 1); err != nil || k.Version() != v1 {
		t.Fatalf("version 1 after the undelete: %+v, %v", k.Version(), err)
	}

	// Destroying version 1 makes it absent for good.
	if status, body, err := b.Do(b.Admin(), http.MethodPost, "/v1/secret/destroy/"+p, map[string]any{"versions": []int{1}}); err != nil || status/100 != 2 {
		t.Fatalf("control: the destroy of version 1: status %d, %v: %s", status, err, body)
	}
	if _, err := signer.ReadCAKeyVersion(ctx, cl, 1); !errors.Is(err, ErrAbsent) {
		t.Fatalf("a destroyed version 1: %v, want ErrAbsent", err)
	}
	if k, err := signer.ReadCAKeyVersion(ctx, cl, 2); err != nil || k.Version().Version != 2 {
		t.Fatalf("version 2 after version 1 was destroyed: %+v, %v", k.Version(), err)
	}
	t.Logf("version 1 read beside version 2; version 3, a deleted and a destroyed version 1 are ErrAbsent")
}

func TestLiveSignerIdentity(t *testing.T) {
	b := live(t)
	signerGrants(b)
	ctx := t.Context()
	cl := id.New(id.Cluster)
	p, err := CAKeyPath(cl)
	if err != nil {
		t.Fatal(err)
	}
	dataPath := "/v1/secret/data/" + p
	signerAs := func(tok string) *Signer {
		s, err := NewSigner(b.Addr, tokenOf(tok))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	signer := signerAs(b.Token("bw-signer"))
	first := NewCAKey([]byte(caText), []byte(keyText))
	second := NewCAKey([]byte(caText+"2"), []byte(keyText+"2"))

	// An unwritten path is absent, and a create-only write makes version 1.
	if _, err := signer.ReadCAKey(ctx, cl); !errors.Is(err, ErrAbsent) {
		t.Fatalf("an unwritten path: %v, want ErrAbsent", err)
	}
	v1, err := signer.WriteCAKey(ctx, cl, first, 0)
	if err != nil || v1.Version != 1 || v1.Path != p || v1.CreatedTime.IsZero() {
		t.Fatalf("the create: %+v, %v", v1, err)
	}
	k, err := signer.ReadCAKey(ctx, cl)
	if err != nil || string(k.Crt()) != caText || string(k.Key()) != keyText || k.Version() != v1 {
		t.Fatalf("the read: version %+v, %v", k.Version(), err)
	}
	t.Logf("the signer created and read version 1 of %s", p)

	// A second create-only write replaces nothing, and a stale cas replaces nothing: both are the
	// check-and-set refusal, which the signer's update grant lets through to the engine.
	if _, err := signer.WriteCAKey(ctx, cl, second, 0); !errors.Is(err, ErrExists) {
		t.Fatalf("a create over version 1: %v, want ErrExists", err)
	}
	v2, err := signer.WriteCAKey(ctx, cl, second, 1)
	if err != nil || v2.Version != 2 {
		t.Fatalf("the replacement at cas 1: %+v, %v", v2, err)
	}
	if _, err := signer.WriteCAKey(ctx, cl, first, 1); !errors.Is(err, ErrExists) {
		t.Fatalf("a replacement at the stale cas 1: %v, want ErrExists", err)
	}
	if k, err := signer.ReadCAKey(ctx, cl); err != nil || k.Version().Version != 2 || string(k.Key()) != keyText+"2" {
		t.Fatalf("after the refused writes: version %+v, %v", k.Version(), err)
	}
	t.Logf("cas 0 over version 1 and a stale cas are ErrExists; the replacement made version 2")

	// The signer is refused everything but that path's data: no list, no delete, no metadata, no
	// other secret and no Transit. Each is refused to the signer and succeeds with the grant added.
	other := id.New(id.Cluster)
	otherPath, err := CAKeyPath(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signerAs(b.Admin()).WriteCAKey(ctx, other, first, 0); err != nil {
		t.Fatalf("control: the administrator's create: %v", err)
	}
	deniedWithout(t, b, "the signer listing the CA key paths", []string{"bw-signer"}, "ca-key-list", func(tok string) error {
		_, err := raw(t, b, tok, "LIST", "/v1/secret/metadata/access/talos-ca", nil)
		return err
	})
	deniedWithout(t, b, "the signer reading a CA key's metadata", []string{"bw-signer"}, "ca-key-meta-read", func(tok string) error {
		_, err := raw(t, b, tok, http.MethodGet, "/v1/secret/metadata/"+otherPath, nil)
		return err
	})
	deniedWithout(t, b, "the signer deleting a CA key", []string{"bw-signer"}, "ca-key-delete", func(tok string) error {
		_, err := raw(t, b, tok, http.MethodDelete, "/v1/secret/data/"+otherPath, nil)
		return err
	})
	deniedWithout(t, b, "the signer creating a generation", []string{"bw-signer"}, "gen-create-only-s", func(tok string) error {
		fresh, err := NewGenerationPath(id.New(id.Cluster), id.New(id.Ingestion), NewValueID())
		if err != nil {
			t.Fatal(err)
		}
		removeAtEnd(t, b, fresh.String())
		_, err = writeNoCAS(t, b, tok, fresh, "synthetic")
		return err
	})
	deniedWithout(t, b, "the signer encrypting under the artifact key", []string{"bw-signer"}, "encrypt-artifact", func(tok string) error {
		_, err := encrypt(t, b, tok, "bw-artifact", []byte("x"))
		return err
	})

	// Every identity but the signer and the compiler is refused a read of the CA key path, each beside
	// the same request with a read grant added; the compiler reads it to render (compilation.md §1,
	// choice §16.40). Every identity but the signer is refused a write.
	for _, who := range []string{"bw-ingestion", "bw-executor", "bw-metadata", "bw-orphan-report"} {
		deniedWithout(t, b, who+" reading the CA key", []string{who}, "ca-key-read", func(tok string) error {
			_, err := raw(t, b, tok, http.MethodGet, dataPath, nil)
			return err
		})
	}
	if _, err := raw(t, b, b.Token("bw-compiler"), http.MethodGet, dataPath, nil); err != nil {
		t.Fatalf("the compiler reading the CA key: %v", err)
	}
	for _, who := range []string{"bw-ingestion", "bw-compiler", "bw-executor", "bw-metadata", "bw-orphan-report"} {
		_, err := raw(t, b, b.Token(who), http.MethodPost, dataPath, map[string]any{"data": map[string]string{"ca": "x", "key": "y"}})
		mustDeny(t, who+" writing the CA key", err)
	}
	// The metadata identity's grant on secret/metadata/* reaches a CA key's metadata, which holds
	// versions and times and no value; it is the one grant of another identity that touches the path.
	md, err := raw(t, b, b.Token("bw-metadata"), http.MethodGet, "/v1/secret/metadata/"+p, nil)
	if err != nil {
		t.Fatalf("the metadata identity's read of the CA key's metadata: %v", err)
	}
	if strings.Contains(string(md.body), "CA-KEY-MARKER") {
		t.Fatal("the metadata answer carries the key")
	}
	t.Logf("the metadata identity reads the CA key's metadata (versions and times) and no value")
}
