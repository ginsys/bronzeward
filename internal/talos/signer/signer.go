// Package signer turns a first key into a cluster's Talos os CA key and tries that key at a node
// before anything stores it (persistence-api.md §3.4). A first key is a control plane machine
// configuration, a secrets bundle or an `os:admin` talosconfig. Extract takes the CA certificate and
// key from it, for a talosconfig by one read of a control plane's machine configuration through the
// talosconfig; Try signs one short `os:reader` certificate from the key and makes one Version read at
// the endpoint.
//
// Everything stays in memory: no document, key or certificate is written to a file, logged, returned
// or quoted. An error is this package's own words, never the machinery's, whose decoders quote the
// input they refuse. The package's source is held to an allowlist of imports by guard_test.go: it
// holds the one use of the machinery's secrets package outside the fixture tooling, and none of the
// machinery's client.
package signer

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos"
	"github.com/siderolabs/crypto/x509"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/role"
	"go.yaml.in/yaml/v4"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Kind is the kind of a first key (persistence-api.md §3.4).
type Kind string

const (
	MachineConfig Kind = "machine-config"
	SecretsBundle Kind = "secrets-bundle"
	Talosconfig   Kind = "talosconfig"
)

// The rules of a first key refused (persistence-api.md §3.4), each a `rule` of a 422.
const (
	RuleUnusable         = "first-key-unusable"
	RuleNoCAKey          = "first-key-no-ca-key"
	RuleRoleInsufficient = "first-key-role-insufficient"
	RuleNoControlPlane   = "first-key-endpoint-no-control-plane"
)

// Refusal is a first key refused by one of the rules above. It names the rule and nothing of the
// document.
type Refusal struct{ Rule string }

func (e *Refusal) Error() string { return "signer: the first key is refused: " + e.Rule }

// ErrUnavailable is a failed try, or any other failed Talos read: the endpoint did not answer or did
// not accept the certificate. Talos reports both as Unavailable (persistence-api.md §3.3), so it does
// not say which.
var ErrUnavailable = errors.New("signer: the endpoint did not answer, or did not accept a certificate signed from the key")

// tryLifetime is the validity of the certificate Try signs: one Version read's, with room for a slow
// dial.
var tryLifetime = 2 * time.Minute

// Extract is the CA certificate and key a first key holds. The endpoint is dialled only for a
// talosconfig, whose own endpoints are ignored; the document is never kept.
func Extract(ctx context.Context, kind Kind, document []byte, endpoint string) (provider.CAKey, error) {
	var ca *x509.PEMEncodedCertificateAndKey
	switch kind {
	case MachineConfig:
		cfg, ok := issuingCA(document)
		if !ok {
			return provider.CAKey{}, &Refusal{RuleUnusable}
		}
		ca = cfg
	case SecretsBundle:
		b, ok := osCA(document)
		if !ok {
			return provider.CAKey{}, &Refusal{RuleUnusable}
		}
		ca = b
	case Talosconfig:
		return fromTalosconfig(ctx, document, endpoint)
	default:
		return provider.CAKey{}, &Refusal{RuleUnusable}
	}
	if ca == nil || len(ca.Crt) == 0 || len(ca.Key) == 0 {
		return provider.CAKey{}, &Refusal{RuleNoCAKey}
	}
	return provider.NewCAKey(ca.Crt, ca.Key), nil
}

// fromTalosconfig reads a control plane's machine configuration through the talosconfig at endpoint
// and keeps the os CA key from the answer. The read needs `os:admin`.
func fromTalosconfig(ctx context.Context, document []byte, endpoint string) (provider.CAKey, error) {
	r, err := talos.Dial(ctx, document, endpoint)
	if err != nil {
		return provider.CAKey{}, &Refusal{RuleUnusable}
	}
	defer r.Close() //nolint:errcheck
	c, err := r.MachineConfig(ctx)
	switch {
	case status.Code(err) == codes.PermissionDenied:
		return provider.CAKey{}, &Refusal{RuleRoleInsufficient}
	case err != nil:
		return provider.CAKey{}, ErrUnavailable
	}
	ca, ok := issuingCA(c.Bytes())
	if !ok {
		return provider.CAKey{}, ErrUnavailable
	}
	if ca == nil || len(ca.Crt) == 0 || len(ca.Key) == 0 {
		return provider.CAKey{}, &Refusal{RuleNoControlPlane}
	}
	return provider.NewCAKey(ca.Crt, ca.Key), nil
}

// issuingCA is the os CA of a machine configuration, nil when it holds none. ok is false for a
// document that is not a machine configuration: the machinery panics on some malformed input, and
// that is the document's refusal.
func issuingCA(document []byte) (ca *x509.PEMEncodedCertificateAndKey, ok bool) {
	defer func() {
		if recover() != nil {
			ca, ok = nil, false
		}
	}()
	cfg, err := configloader.NewFromBytes(document)
	if err != nil || cfg.Machine() == nil {
		return nil, false
	}
	return cfg.Machine().Security().IssuingCA(), true
}

// osCA is the os CA of a secrets bundle, decoded as the machinery decodes the file talosctl writes.
func osCA(document []byte) (ca *x509.PEMEncodedCertificateAndKey, ok bool) {
	defer func() {
		if recover() != nil {
			ca, ok = nil, false
		}
	}()
	var b secrets.Bundle
	if yaml.NewDecoder(bytes.NewReader(document)).Decode(&b) != nil || b.Certs == nil || b.Certs.OS == nil {
		return nil, false
	}
	return b.Certs.OS, true
}

// Try signs one `os:reader` certificate from ca, valid from now for tryLifetime, and makes one
// Version read at endpoint with it. Nil means a node answered; the key is trustworthy only then.
// The connection ends no later than the certificate does (persistence-api.md §3.4): a node that
// holds the read open is ErrUnavailable once the certificate has lapsed.
func Try(ctx context.Context, ca provider.CAKey, endpoint string, now time.Time) error {
	pair := &x509.PEMEncodedCertificateAndKey{Crt: ca.Crt(), Key: ca.Key()}
	life := tryLifetime
	cert, err := secrets.NewAdminCertificateAndKey(now, pair, role.MakeSet(role.Reader), life)
	if err != nil {
		return &Refusal{RuleUnusable}
	}
	ctx, cancel := context.WithDeadline(ctx, now.Add(life))
	defer cancel()
	doc, err := clientconfig.NewConfig("bronzeward", []string{endpoint}, ca.Crt(), cert).Bytes()
	if err != nil {
		return ErrUnavailable
	}
	r, err := talos.Dial(ctx, doc, endpoint)
	if err != nil {
		return ErrUnavailable
	}
	defer r.Close() //nolint:errcheck
	if _, err := r.Version(ctx); err != nil {
		return ErrUnavailable
	}
	return nil
}
