package signer_test

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ginsys/bronzeward/internal/provider"
	"github.com/ginsys/bronzeward/internal/talos"
	"github.com/ginsys/bronzeward/internal/talos/signer"
	"github.com/ginsys/bronzeward/internal/talos/talostest"
	cryptox509 "github.com/siderolabs/crypto/x509"
	"github.com/siderolabs/talos/pkg/machinery/role"
	"go.yaml.in/yaml/v4"
)

// parsed is the certificate a Cert holds, read back from its own Config document: what the node
// will check, as against what Sign was asked for.
func parsed(t *testing.T, c signer.Cert) *x509.Certificate {
	t.Helper()
	doc, err := c.Config("127.0.0.1:50000")
	if err != nil {
		t.Fatal(err)
	}
	var tc struct {
		Contexts map[string]struct {
			Crt string `yaml:"crt"`
		} `yaml:"contexts"`
	}
	if err := yaml.Unmarshal(doc, &tc); err != nil || len(tc.Contexts) != 1 {
		t.Fatalf("the talosconfig: %v, %d contexts", err, len(tc.Contexts))
	}
	for _, ctx := range tc.Contexts {
		raw, err := base64.StdEncoding.DecodeString(ctx.Crt)
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode(raw)
		if block == nil {
			t.Fatal("the certificate is not PEM")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		return cert
	}
	return nil
}

func storedKey(ca *cryptox509.PEMEncodedCertificateAndKey) provider.CAKey {
	return provider.NewCAKey(ca.Crt, ca.Key)
}

func contextTimeout(t *testing.T, d time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(t.Context(), d)
}

// A certificate carries the role the use needs and lives for the lifetime asked and no more, read
// from the issued certificate (persistence-api.md §3.4).
func TestSignRoleAndLifetime(t *testing.T) {
	ca := newCA(t)
	k := storedKey(ca)
	now := time.Now().Truncate(time.Second)
	for _, r := range []role.Role{role.Reader, role.Admin} {
		c, err := signer.Sign(k, r, now, 90*time.Second, 5*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		cert := parsed(t, c)
		if c.Role() != r || len(cert.Subject.Organization) != 1 || cert.Subject.Organization[0] != string(r) {
			t.Errorf("%s: role %q, organization %q", r, c.Role(), cert.Subject.Organization)
		}
		if got := cert.NotAfter.Sub(cert.NotBefore); got < 90*time.Second || got > 91*time.Second {
			t.Errorf("%s: valid for %s, want the 90s asked for", r, got)
		}
		if !c.NotAfter().Equal(cert.NotAfter) || c.Serial() != cert.SerialNumber.Text(16) {
			t.Errorf("%s: NotAfter %s and serial %s are not the certificate's own", r, c.NotAfter(), c.Serial())
		}
	}
}

// A certificate states its validity to the whole second, so a lifetime with a fraction is covered by
// rounding the end up: the certificate is never over before now+life, and never a second past it.
func TestSignCoversFractionalLifetime(t *testing.T) {
	k := storedKey(newCA(t))
	base := time.Now().Truncate(time.Second)
	for name, c := range map[string]struct{ now, life time.Duration }{
		"under a second, late in the second": {900 * time.Millisecond, 500 * time.Millisecond},
		"under a second, early":              {100 * time.Millisecond, 500 * time.Millisecond},
		"fraction on both":                   {250 * time.Millisecond, 90*time.Second + 500*time.Millisecond},
		"whole":                              {0, 90 * time.Second},
	} {
		now := base.Add(c.now)
		cert, err := signer.Sign(k, role.Admin, now, c.life, time.Hour)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		end := now.Add(c.life)
		if got := parsed(t, cert).NotAfter; got.Before(end) || got.Sub(end) >= time.Second || !cert.NotAfter().Equal(got) {
			t.Errorf("%s: NotAfter %s for an end of %s, want it at the next whole second at most", name, got, end)
		}
	}
}

// A lifetime so long that its rounded end does not fit a time.Duration is refused: the duration would
// saturate and the certificate end before the lifetime asked for.
func TestSignRefusesAnUnrepresentableEnd(t *testing.T) {
	k := storedKey(newCA(t))
	now := time.Date(2026, 11, 19, 0, 0, 0, int(100*time.Millisecond), time.UTC)
	huge := time.Duration(9223372036) * time.Second
	if _, err := signer.Sign(k, role.Admin, now, huge, huge); !errors.Is(err, signer.ErrLifetime) {
		t.Fatalf("a 292-year lifetime: %v, want ErrLifetime", err)
	}
}

// A lifetime above the maximum, or not above zero, and a role outside the two, are refused; none is
// cut down to fit.
func TestSignRefusals(t *testing.T) {
	k := storedKey(newCA(t))
	now := time.Now()
	for name, c := range map[string]struct {
		r         role.Role
		life, max time.Duration
		want      error
	}{
		"above the maximum": {role.Admin, 6 * time.Minute, 5 * time.Minute, signer.ErrLifetime},
		"zero":              {role.Admin, 0, 5 * time.Minute, signer.ErrLifetime},
		"negative":          {role.Admin, -time.Second, 5 * time.Minute, signer.ErrLifetime},
		"operator":          {role.Operator, time.Minute, 5 * time.Minute, signer.ErrRole},
		"no role":           {"", time.Minute, 5 * time.Minute, signer.ErrRole},
	} {
		if _, err := signer.Sign(k, c.r, now, c.life, c.max); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	// The maximum itself is allowed.
	if _, err := signer.Sign(k, role.Admin, now, 5*time.Minute, 5*time.Minute); err != nil {
		t.Errorf("a lifetime equal to the maximum: %v", err)
	}
	// A key that cannot sign is the signer's own refusal and quotes nothing.
	_, err := signer.Sign(provider.NewCAKey([]byte("CA-CERT-MARKER"), []byte("CA-KEY-MARKER")), role.Admin, now, time.Minute, time.Hour)
	if !errors.Is(err, signer.ErrSign) || strings.Contains(err.Error(), "MARKER") {
		t.Errorf("an unusable key: %v", err)
	}
}

// Every certificate has its own serial, so a certificate signed again is not the one signed before
// (execution-recovery.md §3.2 comparison 3).
func TestSignSerialsDiffer(t *testing.T) {
	k := storedKey(newCA(t))
	seen := map[string]bool{}
	now := time.Now()
	for range 20 {
		c, err := signer.Sign(k, role.Admin, now, time.Minute, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if seen[c.Serial()] {
			t.Fatalf("serial %s was issued twice", c.Serial())
		}
		seen[c.Serial()] = true
	}
}

// Neither the certificate, nor its key, nor the CA key it was signed from renders through any verb or
// marshaller.
func TestCertDoesNotRender(t *testing.T) {
	ca := newCA(t)
	c, err := signer.Sign(storedKey(ca), role.Admin, time.Now(), time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	type nested struct{ c signer.Cert }
	type exported struct{ C signer.Cert }
	secretsText := []string{"BEGIN", "PRIVATE KEY", string(ca.Key)}
	for _, x := range []any{c, &c, nested{c}, exported{c}, []signer.Cert{c}, map[string]signer.Cert{"k": c}} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			out := fmt.Sprintf(verb, x)
			for _, s := range secretsText {
				if strings.Contains(out, s) {
					t.Errorf("%s of %T contains %q", verb, x, s)
				}
			}
		}
	}
	if _, err := json.Marshal(c); err == nil {
		t.Error("json.Marshal(Cert) succeeded")
	}
	if _, err := json.Marshal(exported{c}); err == nil {
		t.Error("json.Marshal of a struct holding a Cert succeeded")
	}
	if _, err := yaml.Marshal(c); err == nil {
		t.Error("yaml.Marshal(Cert) succeeded")
	}
}

// The certificate Sign makes is accepted by a node whose certificate authority is the CA it was
// signed from, through the talosconfig Config builds.
func TestCertDialsANode(t *testing.T) {
	ca := newCA(t)
	n := talostest.Serve(t, talostest.NewPKIFromCA(t, ca))
	c, err := signer.Sign(storedKey(ca), role.Admin, time.Now(), time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := c.Config(n.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	r, err := talos.Dial(ctx(t), doc, n.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close() //nolint:errcheck
	if v, err := r.Version(ctx(t)); err != nil || v == "" {
		t.Fatalf("Version through the signed certificate: %q, %v", v, err)
	}
}

// No connection outlives its certificate (persistence-api.md §3.4): a request the node holds open is
// ended at NotAfter. The control makes the same request without Bound and shows the node still
// holding it past NotAfter, so the test can fail.
func TestNoConnectionOutlivesNotAfter(t *testing.T) {
	ca := newCA(t)
	n := talostest.Serve(t, talostest.NewPKIFromCA(t, ca))
	n.StallVersion(true)
	// Each scenario signs its own certificate, so the control does not use up the lifetime the bound
	// connection is tested against.
	session := func() (signer.Cert, talos.Reader) {
		t.Helper()
		c, err := signer.Sign(storedKey(ca), role.Reader, time.Now(), 3*time.Second, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := c.Config(n.Endpoint)
		if err != nil {
			t.Fatal(err)
		}
		r, err := talos.Dial(t.Context(), doc, n.Endpoint)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.Close() }) //nolint:errcheck
		return c, r
	}

	// The control: unbound, the held request is still held a second after NotAfter, and only the
	// caller's own deadline ends it.
	c, unbound := session()
	until := time.Until(c.NotAfter()) + time.Second
	held, cancel := contextTimeout(t, until)
	start := time.Now()
	_, err := unbound.Version(held)
	cancel()
	if err == nil || time.Since(start) < until-100*time.Millisecond {
		t.Fatalf("control: the unbound request ended after %s with %v, before the caller's deadline", time.Since(start), err)
	}

	// Bound: the same held request ends at NotAfter, and a request made after it fails at once.
	c, r := session()
	bound := talos.Bound(r, c.NotAfter())
	defer bound.Close() //nolint:errcheck
	long, cancel := contextTimeout(t, time.Minute)
	defer cancel()
	if _, err := bound.Version(long); err == nil {
		t.Fatal("the held request was answered")
	}
	if over := time.Since(c.NotAfter()); over < 0 || over > 500*time.Millisecond {
		t.Errorf("the held request ended %s after NotAfter, want it at NotAfter", over)
	}
	start = time.Now()
	if _, err := bound.Version(long); err == nil || time.Since(start) > 500*time.Millisecond {
		t.Errorf("a request after NotAfter: %v after %s, want a refusal at once", err, time.Since(start))
	}
}
