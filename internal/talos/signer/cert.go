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
	// ErrLifetime is a lifetime that is not positive, or above the maximum less the two seconds a
	// certificate's whole-second encoding can add to its span. It is never cut down to fit: a
	// lifetime that does not cover its use would fail the use in the middle.
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

// RoundingSlack is the room a certificate's whole-second encoding can add to its span from the
// signing instant: NotAfter is rounded up (under a second). A use may ask for at most the configured
// maximum less this. It is kept at two seconds, with a second to spare, so the plan refusal, the
// startup minimum and the specification's figure stay as they are.
const RoundingSlack = 2 * time.Second

// notBeforeBackdate is how far before the signing instant a certificate's NotBefore lies: a fixed
// allowance for a node whose clock is behind the signer's (persistence-api.md §3.4). The node's clock
// is not read, and the value is chosen, not measured.
const notBeforeBackdate = 300 * time.Second

// Sign signs a client certificate from k for r, valid from now for life. max bounds how far past now
// the certificate reaches, NotAfter minus now, which can exceed life by under RoundingSlack: so a
// life above max minus RoundingSlack is refused up front, at any now, and never shortened. The
// certificate is also valid from the whole second 300 s before now, an allowance for a node whose
// clock is behind; it adds no valid time after now and is not part of the span max bounds. A
// certificate states whole seconds: NotAfter is the next whole second at or after now+life, so it
// never ends before the use it covers. The node's clock is not read. Only os:reader and os:admin
// are signed. The certificate's serial and NotAfter are read
// back from the certificate itself, so a record names what the node will check and not what was
// asked for.
func Sign(k provider.CAKey, r role.Role, now time.Time, life, max time.Duration) (Cert, error) {
	if r != role.Reader && r != role.Admin {
		return Cert{}, ErrRole
	}
	// max is compared before the subtraction: a maximum at the bottom of a time.Duration would wrap.
	if life <= 0 || max <= RoundingSlack || life > max-RoundingSlack {
		return Cert{}, ErrLifetime
	}
	end := now.Add(life)
	if !end.Equal(end.Truncate(time.Second)) {
		end = end.Truncate(time.Second).Add(time.Second)
	}
	pair, err := secrets.NewAdminCertificateAndKey(now.Add(-notBeforeBackdate), &cryptox509.PEMEncodedCertificateAndKey{Crt: k.Crt(), Key: k.Key()},
		role.MakeSet(r), end.Sub(now)+notBeforeBackdate)
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
	// An assertion: the up-front check leaves room for the encoding, so this never fires. The span is
	// measured from the signing instant: NotBefore's allowance is outside the maximum.
	if parsed.NotAfter.After(now.Add(max)) || parsed.NotAfter.Before(now.Add(life)) {
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
