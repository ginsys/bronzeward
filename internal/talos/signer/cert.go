package signer

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ginsys/bronzeward/internal/provider"
	cryptox509 "github.com/siderolabs/crypto/x509"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/role"
)

// Sign's refusals are this package's own words, never the machinery's.
var (
	// ErrRole is a role that is not one a use needs: only os:reader and os:admin are signed.
	ErrRole = errors.New("signer: a certificate is signed only for the os:reader or os:admin role")
	// ErrLifetime is a lifetime that is not positive, or that is above the configured maximum. It is
	// never cut down to the maximum: a lifetime that does not cover its use would fail the use in the
	// middle.
	ErrLifetime = errors.New("signer: the lifetime is not above zero and within the maximum")
	// ErrSign is a CA key that cannot sign.
	ErrSign = errors.New("signer: the CA key could not sign a certificate")
)

// Cert is the client certificate and key signed for one use, with what the use's evidence records
// (persistence-api.md §3.3): the CA key version it was signed from, its serial and its NotAfter. It
// lives only in the memory of the process that signed it, for the use. It does not render: every fmt
// verb prints a placeholder, and the PEM texts sit behind pointers to strings for the reason
// provider.CAKey's do. Config is the one way out.
type Cert struct {
	ca       provider.CAKeyVersion
	role     role.Role
	serial   string
	notAfter time.Time
	caCrt    *string // the CA certificate, public, behind a pointer so a verb prints no text from it
	crt, key *string
}

// Sign signs a client certificate from k for r, valid from now for life, and refuses a life above
// max. A certificate states whole seconds: NotBefore drops the fraction of now, and NotAfter is the
// next whole second at or after now+life, so it never ends before the use it covers. max bounds the
// issued certificate's own span, NotAfter minus NotBefore: a lifetime whose certificate would span
// more is refused, never shortened, so a lifetime within two seconds of max can be refused. The
// node's clock is not read. Only os:reader and os:admin are signed. The certificate's serial and
// NotAfter are read back from the certificate itself, so a record names what the node will check
// and not what was asked for.
func Sign(k provider.CAKey, r role.Role, now time.Time, life, max time.Duration) (Cert, error) {
	if r != role.Reader && r != role.Admin {
		return Cert{}, ErrRole
	}
	if life <= 0 || life > max {
		return Cert{}, ErrLifetime
	}
	// A certificate states its validity to the whole second and drops the fraction, which would end it
	// before the use it covers. So its end is rounded up to the next whole second: never early, and at
	// most a second late.
	end := now.Add(life)
	if !end.Equal(end.Truncate(time.Second)) {
		end = end.Truncate(time.Second).Add(time.Second)
	}
	// A duration that cannot hold the rounded end saturates, and the certificate would end early.
	ttl := end.Sub(now)
	if !now.Add(ttl).Equal(end) {
		return Cert{}, ErrLifetime
	}
	pair, err := secrets.NewAdminCertificateAndKey(now, &cryptox509.PEMEncodedCertificateAndKey{Crt: k.Crt(), Key: k.Key()},
		role.MakeSet(r), ttl)
	if err != nil {
		return Cert{}, ErrSign
	}
	block, _ := pem.Decode(pair.Crt)
	if block == nil {
		return Cert{}, ErrSign
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return Cert{}, ErrSign
	}
	if parsed.NotAfter.Sub(parsed.NotBefore) > max {
		return Cert{}, ErrLifetime
	}
	caCrt, crt, key := string(k.Crt()), string(pair.Crt), string(pair.Key)
	return Cert{ca: k.Version(), role: r, serial: parsed.SerialNumber.Text(16), notAfter: parsed.NotAfter.UTC(),
		caCrt: &caCrt, crt: &crt, key: &key}, nil
}

// CA is the identity of the CA key version the certificate was signed from: its path, version and
// created_time. It is zero for a key that has not been stored.
func (c Cert) CA() provider.CAKeyVersion { return c.ca }

// Role is the Talos role the certificate carries.
func (c Cert) Role() role.Role { return c.role }

// Serial is the certificate's serial number in lower-case hexadecimal.
func (c Cert) Serial() string { return c.serial }

// NotAfter is when the certificate lapses, to the whole second, as the certificate states it.
func (c Cert) NotAfter() time.Time { return c.notAfter }

// Config is a talosconfig document for endpoint holding the CA certificate and this client
// certificate and key, built in memory for internal/talos's dialers, which read nothing else from
// it. It is never written to a file. The caller clears the returned bytes after the dial.
func (c Cert) Config(endpoint string) ([]byte, error) {
	if c.caCrt == nil || c.crt == nil || c.key == nil {
		return nil, ErrSign
	}
	doc, err := clientconfig.NewConfig("bronzeward", []string{endpoint}, []byte(*c.caCrt),
		&cryptox509.PEMEncodedCertificateAndKey{Crt: []byte(*c.crt), Key: []byte(*c.key)}).Bytes()
	if err != nil {
		return nil, ErrSign
	}
	return doc, nil
}

const certText = "[talos certificate]"

var errCertRender = errors.New("signer: a talos certificate is not marshalled")

func (Cert) Format(f fmt.State, _ rune)   { io.WriteString(f, certText) }
func (Cert) MarshalJSON() ([]byte, error) { return nil, errCertRender }
func (Cert) MarshalText() ([]byte, error) { return nil, errCertRender }
func (Cert) MarshalYAML() (any, error)    { return nil, errCertRender }
