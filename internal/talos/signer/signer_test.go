package signer_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos/signer"
	"github.com/ginsys/bronzeward/internal/talos/talostest"
	"github.com/siderolabs/crypto/x509"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"google.golang.org/grpc/codes"
)

func newCA(t *testing.T) *x509.PEMEncodedCertificateAndKey {
	t.Helper()
	ca, err := secrets.NewTalosCA(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return &x509.PEMEncodedCertificateAndKey{Crt: ca.CrtPEM, Key: ca.KeyPEM}
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func controlPlane(ca *x509.PEMEncodedCertificateAndKey) []byte {
	return []byte("version: v1alpha1\nmachine:\n  type: controlplane\n  ca:\n    crt: " + b64(ca.Crt) + "\n    key: " + b64(ca.Key) + "\n")
}

// worker is a worker's configuration: it holds the CA certificate and no key.
func worker(ca *x509.PEMEncodedCertificateAndKey) []byte {
	return []byte("version: v1alpha1\nmachine:\n  type: worker\n  ca:\n    crt: " + b64(ca.Crt) + "\n")
}

func bundle(ca *x509.PEMEncodedCertificateAndKey) []byte {
	return []byte("certs:\n  os:\n    crt: " + b64(ca.Crt) + "\n    key: " + b64(ca.Key) + "\n")
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

func rule(t *testing.T, err error) string {
	t.Helper()
	var r *signer.Refusal
	if !errors.As(err, &r) {
		t.Fatalf("not a refusal: %v", err)
	}
	return r.Rule
}

func TestExtractDocuments(t *testing.T) {
	ca := newCA(t)
	for _, tc := range []struct {
		name string
		kind signer.Kind
		doc  []byte
	}{
		{"machine configuration", signer.MachineConfig, controlPlane(ca)},
		{"secrets bundle", signer.SecretsBundle, bundle(ca)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, err := signer.Extract(ctx(t), tc.kind, tc.doc, "127.0.0.1:1")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(k.Crt(), ca.Crt) || !bytes.Equal(k.Key(), ca.Key) {
				t.Fatal("the extracted key is not the document's CA")
			}
		})
	}
}

func TestExtractRefusals(t *testing.T) {
	ca := newCA(t)
	noKey := []byte("certs:\n  os:\n    crt: " + b64(ca.Crt) + "\n")
	for _, tc := range []struct {
		name string
		kind signer.Kind
		doc  []byte
		want string
	}{
		{"worker configuration holds no key", signer.MachineConfig, worker(ca), signer.RuleNoCAKey},
		{"bundle os without a key", signer.SecretsBundle, noKey, signer.RuleNoCAKey},
		{"bundle without os", signer.SecretsBundle, []byte("certs:\n  etcd:\n    crt: " + b64(ca.Crt) + "\n"), signer.RuleUnusable},
		{"empty machine configuration", signer.MachineConfig, nil, signer.RuleUnusable},
		{"empty bundle", signer.SecretsBundle, nil, signer.RuleUnusable},
		{"text", signer.MachineConfig, []byte("not a configuration"), signer.RuleUnusable},
		{"malformed yaml", signer.SecretsBundle, []byte("certs: [\n"), signer.RuleUnusable},
		{"null document", signer.MachineConfig, []byte("null"), signer.RuleUnusable},
		{"binary", signer.MachineConfig, []byte{0, 1, 2, 0xff, 0xfe}, signer.RuleUnusable},
		{"unknown kind", signer.Kind("pem"), controlPlane(ca), signer.RuleUnusable},
		{"talosconfig that is not one", signer.Talosconfig, []byte("not a talosconfig"), signer.RuleUnusable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := signer.Extract(ctx(t), tc.kind, tc.doc, "127.0.0.1:1")
			if got := rule(t, err); got != tc.want {
				t.Fatalf("rule %q, want %q", got, tc.want)
			}
		})
	}
}

// A refusal names its rule and nothing of the document, whatever the machinery's decoder would
// have quoted.
func TestRefusalsQuoteNothing(t *testing.T) {
	const secret = "ZZ-secret-marker-ZZ"
	for _, doc := range []string{
		"machine: " + secret + "\n  bad: [\n",
		"version: v1alpha1\nmachine:\n  type: " + secret + "\n",
		"certs: " + secret + "\n",
	} {
		for _, k := range []signer.Kind{signer.MachineConfig, signer.SecretsBundle} {
			_, err := signer.Extract(ctx(t), k, []byte(doc), "127.0.0.1:1")
			if err == nil {
				t.Fatalf("%s accepted %q", k, doc)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("%s: the error quotes the document: %v", k, err)
			}
		}
	}
}

func TestExtractTalosconfig(t *testing.T) {
	ca := newCA(t)
	pki := talostest.NewPKIFromCA(t, ca)

	t.Run("an os:admin talosconfig reads the control plane's key", func(t *testing.T) {
		n := talostest.Serve(t, pki)
		n.SetMachineConfig(t, controlPlane(ca))
		k, err := signer.Extract(ctx(t), signer.Talosconfig, pki.Talosconfig("a:1", "", ""), n.Endpoint)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(k.Key(), ca.Key) {
			t.Fatal("the key is not the control plane's")
		}
	})

	t.Run("the talosconfig's own endpoints are ignored", func(t *testing.T) {
		n := talostest.Serve(t, pki)
		n.SetMachineConfig(t, controlPlane(ca))
		if _, err := signer.Extract(ctx(t), signer.Talosconfig, pki.Talosconfig("127.0.0.1:1", "", ""), n.Endpoint); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a role that may not read the configuration is refused", func(t *testing.T) {
		n := talostest.Serve(t, pki)
		n.SetMachineConfig(t, controlPlane(ca))
		n.Fail(talostest.MachineConfigType, codes.PermissionDenied)
		_, err := signer.Extract(ctx(t), signer.Talosconfig, pki.Talosconfig("a:1", "", ""), n.Endpoint)
		if got := rule(t, err); got != signer.RuleRoleInsufficient {
			t.Fatalf("rule %q", got)
		}
	})

	t.Run("a worker's configuration holds no key", func(t *testing.T) {
		n := talostest.Serve(t, pki)
		n.SetMachineConfig(t, worker(ca))
		_, err := signer.Extract(ctx(t), signer.Talosconfig, pki.Talosconfig("a:1", "", ""), n.Endpoint)
		if got := rule(t, err); got != signer.RuleNoControlPlane {
			t.Fatalf("rule %q", got)
		}
	})

	t.Run("a node with no configuration is unavailable, not a refusal", func(t *testing.T) {
		n := talostest.Serve(t, pki)
		_, err := signer.Extract(ctx(t), signer.Talosconfig, pki.Talosconfig("a:1", "", ""), n.Endpoint)
		if !errors.Is(err, signer.ErrUnavailable) {
			t.Fatalf("err %v", err)
		}
	})

	t.Run("a node that does not trust the talosconfig is unavailable", func(t *testing.T) {
		n := talostest.Serve(t, pki)
		n.SetMachineConfig(t, controlPlane(ca))
		other := talostest.NewPKIFromCA(t, newCA(t))
		_, err := signer.Extract(ctx(t), signer.Talosconfig, other.Talosconfig("a:1", "", ""), n.Endpoint)
		if !errors.Is(err, signer.ErrUnavailable) {
			t.Fatalf("err %v", err)
		}
	})
}

func TestTry(t *testing.T) {
	ca := newCA(t)
	pki := talostest.NewPKIFromCA(t, ca)
	n := talostest.Serve(t, pki)

	t.Run("a key the node trusts", func(t *testing.T) {
		k, err := signer.Extract(ctx(t), signer.SecretsBundle, bundle(ca), n.Endpoint)
		if err != nil {
			t.Fatal(err)
		}
		if err := signer.Try(ctx(t), k, n.Endpoint, time.Now()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a key of another certificate authority is unavailable", func(t *testing.T) {
		k, err := signer.Extract(ctx(t), signer.SecretsBundle, bundle(newCA(t)), n.Endpoint)
		if err != nil {
			t.Fatal(err)
		}
		if err := signer.Try(ctx(t), k, n.Endpoint, time.Now()); !errors.Is(err, signer.ErrUnavailable) {
			t.Fatalf("err %v", err)
		}
	})

	t.Run("an endpoint that does not answer is unavailable", func(t *testing.T) {
		k, _ := signer.Extract(ctx(t), signer.SecretsBundle, bundle(ca), n.Endpoint)
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := signer.Try(c, k, "127.0.0.1:1", time.Now()); !errors.Is(err, signer.ErrUnavailable) {
			t.Fatalf("err %v", err)
		}
	})

	t.Run("a node that holds the read open does not outlive the certificate", func(t *testing.T) {
		defer signer.SetTryLifetime(2 * time.Second)()
		held := talostest.Serve(t, pki)
		held.StallVersion(true)
		k, err := signer.Extract(ctx(t), signer.SecretsBundle, bundle(ca), held.Endpoint)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		// The caller's context allows far longer than the certificate lives.
		if err := signer.Try(ctx(t), k, held.Endpoint, start); !errors.Is(err, signer.ErrUnavailable) {
			t.Fatalf("err %v", err)
		}
		if d := time.Since(start); d > 6*time.Second {
			t.Fatalf("the try lasted %v, past the certificate's two seconds", d)
		}
	})

	t.Run("a certificate authority key that cannot sign is refused", func(t *testing.T) {
		bad := provider.NewCAKey(ca.Crt, []byte("not a key"))
		if got := rule(t, signer.Try(ctx(t), bad, n.Endpoint, time.Now())); got != signer.RuleUnusable {
			t.Fatalf("rule %q", got)
		}
	})
}
