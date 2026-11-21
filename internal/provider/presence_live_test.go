package provider

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/classify"
	"github.com/ginsys/bronzeward/internal/id"
)

// The access-presence identity (persistence-api.md §3.3 "Presence") against a real OpenBao: its
// policy as the provider holds it, its one read of a CA key's metadata classified as the route
// classifies it, and the refusals that keep it off the key and off everything else. Each refusal is
// run beside the same request with the one withheld grant added, so it comes from the policy and not
// from a broken token or path.

func TestLivePresencePolicyExact(t *testing.T) {
	b := live(t)
	policyExact(t, b, "bw-presence", map[string][]string{"secret/metadata/access/talos-ca/*": {"read"}}, []string{
		caKeyRead, caKeyDelete, caKeyList, genRead, encryptArtifact, decryptArtifact, readTransitKeys,
		`path "secret/metadata/access/talos-ca/*" { capabilities = ["read", "list"] }`,
		`path "secret/metadata/access/talos-ca/*" { capabilities = ["read", "delete"] }`,
	})
}

// latestOf classifies the latest version in the answer the presence client got, as the route does:
// the answer's current_version, a zero Created.
func latestOf(t *testing.T, a classify.Answer) classify.Result {
	t.Helper()
	if r := classify.AnswerReason(classify.KV, a); r != classify.None {
		t.Fatalf("the answer fails as a whole: %s", r)
	}
	v, ok := classify.CurrentVersion(a)
	if !ok {
		t.Fatal("the answer has no current_version")
	}
	return classify.Classify(classify.Dependency{Provider: classify.KV, Version: v}, a)
}

func TestLivePresenceIdentity(t *testing.T) {
	b := live(t)
	signerGrants(b)
	ctx := t.Context()
	cl := id.New(id.Cluster)
	p, err := CAKeyPath(cl)
	if err != nil {
		t.Fatal(err)
	}
	presence, err := NewPresence(b.Addr, tokenOf(b.Token("bw-presence")))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := NewSigner(b.Addr, tokenOf(b.Admin()))
	if err != nil {
		t.Fatal(err)
	}

	// An unwritten path is a 404 whose body is an empty errors list, which the classifier reads as
	// absent and the route as "never written".
	a, err := presence.CAKeyMetadata(ctx, cl)
	if err != nil || a.Status != http.StatusNotFound || classify.AnswerReason(classify.KV, a) != classify.Absent || !classify.EmptyErrors(a) {
		t.Fatalf("an unwritten path: status %d, empty errors list %v, %v", a.Status, classify.EmptyErrors(a), err)
	}

	// Two versions: the read is the latest's, held, and the answer carries no key. The control is the
	// administrator's data read, which does carry it, so the scan means something.
	first := NewCAKey([]byte(caText), []byte(keyText))
	second := NewCAKey([]byte(caText+"2"), []byte(keyText+"2"))
	v1, err := admin.WriteCAKey(ctx, cl, first, 0)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := admin.WriteCAKey(ctx, cl, second, 1)
	if err != nil {
		t.Fatal(err)
	}
	a, err = presence.CAKeyMetadata(ctx, cl)
	if err != nil || a.Status != http.StatusOK {
		t.Fatalf("the metadata read: status %d, %v", a.Status, err)
	}
	r := latestOf(t, a)
	if r.Class != classify.Retained || r.Reason != classify.None || !r.Created.Equal(v2.CreatedTime) || r.Created.Equal(v1.CreatedTime) {
		t.Fatalf("two versions stored: %s/%q created %s, want retained at version 2's %s", r.Class, r.Reason, r.Created, v2.CreatedTime)
	}
	if v, _ := classify.CurrentVersion(a); v != 2 {
		t.Fatalf("current version %d, want 2", v)
	}
	if strings.Contains(string(a.Body.Bytes()), "CA-KEY-MARKER") || strings.Contains(string(a.Body.Bytes()), "CA-CERT-MARKER") {
		t.Fatal("the metadata answer carries the key")
	}
	data, err := raw(t, b, b.Admin(), http.MethodGet, "/v1/secret/data/"+p, nil)
	if err != nil || !strings.Contains(string(data.body), "CA-KEY-MARKER") {
		t.Fatalf("control: the administrator's data read does not carry the marker: %v", err)
	}
	t.Logf("the presence read answered version 2 retained, created %s, and no key", r.Created)

	// The latest version deleted, then undeleted, then destroyed, on a real clock. Right after the
	// delete the version's deletion_time can sit in the provider's own second, which is undecidable
	// and never "retained", so the read polls until it is decided.
	if _, err := raw(t, b, b.Admin(), http.MethodDelete, "/v1/secret/data/"+p, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		a, err = presence.CAKeyMetadata(ctx, cl)
		if err != nil {
			t.Fatal(err)
		}
		r = latestOf(t, a)
		if r.Class == classify.Retained {
			t.Fatalf("a deleted latest version read as retained: %s/%q", r.Class, r.Reason)
		}
		if r.Class == classify.Blocked && r.Reason == classify.SoftDeleted {
			break
		}
		if r.Reason != classify.DeletionTimeUndecidable || time.Now().After(deadline) {
			t.Fatalf("after the delete: %s/%q", r.Class, r.Reason)
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Logf("a deleted latest version reads blocked soft-deleted")
	if _, err := raw(t, b, b.Admin(), http.MethodPost, "/v1/secret/undelete/"+p, map[string]any{"versions": []int{2}}); err != nil {
		t.Fatal(err)
	}
	a, err = presence.CAKeyMetadata(ctx, cl)
	if err != nil {
		t.Fatal(err)
	}
	if r = latestOf(t, a); r.Class != classify.Retained {
		t.Fatalf("after the undelete: %s/%q, want retained", r.Class, r.Reason)
	}
	if _, err := raw(t, b, b.Admin(), http.MethodPost, "/v1/secret/destroy/"+p, map[string]any{"versions": []int{2}}); err != nil {
		t.Fatal(err)
	}
	a, err = presence.CAKeyMetadata(ctx, cl)
	if err != nil {
		t.Fatal(err)
	}
	if r = latestOf(t, a); r.Class != classify.Lost || r.Reason != classify.Destroyed {
		t.Fatalf("after the destroy: %s/%q, want lost destroyed", r.Class, r.Reason)
	}
	t.Logf("undelete reads retained and destroy reads lost destroyed")

	// The identity is refused everything but that path's metadata: no data read, no list, no delete,
	// no write, no other secret and no Transit. Each is refused to it and succeeds with the grant added.
	other := id.New(id.Cluster)
	otherPath, err := CAKeyPath(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.WriteCAKey(ctx, other, first, 0); err != nil {
		t.Fatalf("control: the administrator's create: %v", err)
	}
	if got, err := presence.CAKeyMetadata(ctx, other); err != nil || got.Status != http.StatusOK {
		t.Fatalf("control: the presence read of the written key: status %d, %v", got.Status, err)
	}
	deniedWithout(t, b, "the presence identity reading a CA key's data", []string{"bw-presence"}, "ca-key-read", func(tok string) error {
		_, err := raw(t, b, tok, http.MethodGet, "/v1/secret/data/"+otherPath, nil)
		return err
	})
	deniedWithout(t, b, "the presence identity listing the CA key paths", []string{"bw-presence"}, "ca-key-list", func(tok string) error {
		_, err := raw(t, b, tok, "LIST", "/v1/secret/metadata/access/talos-ca", nil)
		return err
	})
	deniedWithout(t, b, "the presence identity deleting a CA key", []string{"bw-presence"}, "ca-key-delete", func(tok string) error {
		_, err := raw(t, b, tok, http.MethodDelete, "/v1/secret/data/"+otherPath, nil)
		return err
	})
	gen := newPath(t)
	removeAtEnd(t, b, gen.String())
	if _, err := writeNoCAS(t, b, b.Admin(), gen, "synthetic"); err != nil {
		t.Fatalf("control: the administrator's generation create: %v", err)
	}
	deniedWithout(t, b, "the presence identity reading a generation", []string{"bw-presence"}, "gen-read", func(tok string) error {
		_, err := raw(t, b, tok, http.MethodGet, kvDataPath(gen), nil)
		return err
	})
	deniedWithout(t, b, "the presence identity reading a Transit key", []string{"bw-presence"}, "read-transit-keys", func(tok string) error {
		_, err := raw(t, b, tok, http.MethodGet, "/v1/transit/keys/bw-artifact", nil)
		return err
	})
	deniedWithout(t, b, "the presence identity encrypting under the artifact key", []string{"bw-presence"}, "encrypt-artifact", func(tok string) error {
		_, err := encrypt(t, b, tok, "bw-artifact", []byte("x"))
		return err
	})
	sealed, err := encrypt(t, b, b.Admin(), "bw-artifact", []byte("x"))
	if err != nil {
		t.Fatalf("control: the administrator's encrypt: %v", err)
	}
	deniedWithout(t, b, "the presence identity decrypting under the artifact key", []string{"bw-presence"}, "decrypt-artifact", func(tok string) error {
		_, err := decrypt(t, b, tok, "bw-artifact", sealed)
		return err
	})
	// A write to the metadata path itself is refused too: the grant is read alone.
	mustDeny(t, "the presence identity writing the CA key's metadata", func() error {
		_, err := raw(t, b, b.Token("bw-presence"), http.MethodPost, "/v1/secret/metadata/"+otherPath, map[string]any{"max_versions": 1})
		return err
	}())
	// And the other identities' tokens are refused the metadata path. The metadata identity is left
	// out: its own grant on secret/metadata/* already reads it.
	for _, who := range []string{"bw-ingestion", "bw-compiler", "bw-executor", "bw-signer", "bw-orphan-report"} {
		deniedWithout(t, b, who+" reading a CA key's metadata", []string{who}, "ca-key-meta-read", func(tok string) error {
			_, err := raw(t, b, tok, http.MethodGet, "/v1/secret/metadata/"+otherPath, nil)
			return err
		})
	}
}
