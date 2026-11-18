//go:build ignore

// Offline measurements for docs/design/research/20261118-local-talos-signing.md.
//
// Run from the repository root, so the module's pinned Talos machinery is used:
//
//	go run docs/design/research/evidence/20261118-local-talos-signing/main.go offline <dir>
//
// <dir> holds a synthetic secrets.yaml, controlplane.yaml, worker.yaml and talosconfig made with
// `talosctl gen secrets` and `talosctl gen config`. No key is ever printed. Not product code.
package main

import (
	"context"
	"crypto/sha256"
	stdx509 "crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ginsys/bronzeward/internal/talos"
	"github.com/siderolabs/crypto/x509"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/role"
	"google.golang.org/grpc/status"
)

func main() {
	switch {
	case len(os.Args) == 3 && os.Args[1] == "offline":
		offline(os.Args[2])
	case len(os.Args) == 4 && os.Args[1] == "node":
		node(os.Args[2], os.Args[3])
	default:
		fmt.Fprintln(os.Stderr, "usage: main.go offline <dir> | main.go node <dir> <endpoint ip[:port]>")
		os.Exit(2)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
	return v
}

func fp(crt []byte) string {
	b, _ := pem.Decode(crt)
	if b == nil {
		return "no-pem"
	}
	s := sha256.Sum256(b.Bytes)
	return hex.EncodeToString(s[:8])
}

// describe verifies the certificate against the CA and prints what a node would read from it.
func describe(label string, p *x509.PEMEncodedCertificateAndKey, ca []byte, call time.Time) {
	b, _ := pem.Decode(p.Crt)
	c := must(stdx509.ParseCertificate(b.Bytes))
	pool := stdx509.NewCertPool()
	pool.AppendCertsFromPEM(ca)
	_, verr := c.Verify(stdx509.VerifyOptions{
		Roots: pool, KeyUsages: []stdx509.ExtKeyUsage{stdx509.ExtKeyUsageClientAuth}, CurrentTime: call.Add(time.Second),
	})
	fmt.Printf("%s: O=%v CN=%q isCA=%v keyUsage=%d eku=%v notBefore-call=%s notAfter-notBefore=%s keyAlg=%s chainVerifies=%v\n",
		label, c.Subject.Organization, c.Subject.CommonName, c.IsCA, c.KeyUsage, c.ExtKeyUsage,
		c.NotBefore.Sub(call).Round(time.Second), c.NotAfter.Sub(c.NotBefore).Round(time.Second), c.PublicKeyAlgorithm, verr == nil)
}

func sign(label string, ca *x509.PEMEncodedCertificateAndKey, now time.Time, roles role.Set, ttl time.Duration) {
	p, err := secrets.NewAdminCertificateAndKey(now, ca, roles, ttl)
	if err != nil {
		fmt.Printf("%s: REFUSED: %v\n", label, err)
		return
	}
	describe(label, p, ca.Crt, time.Now())
}

func offline(dir string) {
	now := time.Now()
	fmt.Printf("machinery: github.com/siderolabs/talos/pkg/machinery (module as pinned in go.mod)\nrun at %s\n", now.UTC().Format(time.RFC3339))

	// Sources of the signer.
	cp := must(configloader.NewFromBytes(must(os.ReadFile(filepath.Join(dir, "controlplane.yaml")))))
	wk := must(configloader.NewFromBytes(must(os.ReadFile(filepath.Join(dir, "worker.yaml")))))
	cpCA := cp.Machine().Security().IssuingCA()
	wkCA := wk.Machine().Security().IssuingCA()
	bundle := must(secrets.LoadBundle(filepath.Join(dir, "secrets.yaml")))
	tc := must(clientconfig.Open(filepath.Join(dir, "talosconfig")))
	tcCtx := tc.Contexts[tc.Context]
	tcCA := must(base64.StdEncoding.DecodeString(tcCtx.CA))

	fmt.Printf("source controlplane.yaml: CA certificate %s, key present=%v\n", fp(cpCA.Crt), len(cpCA.Key) > 0)
	fmt.Printf("source worker.yaml: CA certificate present=%v, key present=%v\n", wkCA != nil && len(wkCA.Crt) > 0, wkCA != nil && len(wkCA.Key) > 0)
	fmt.Printf("source secrets.yaml Certs.OS: CA certificate %s, key present=%v\n", fp(bundle.Certs.OS.Crt), len(bundle.Certs.OS.Key) > 0)
	fmt.Printf("source talosconfig: CA certificate %s (no key field exists)\n", fp(tcCA))
	fmt.Printf("same CA across the three sources: %v\n", fp(cpCA.Crt) == fp(bundle.Certs.OS.Crt) && fp(tcCA) == fp(cpCA.Crt))
	fmt.Printf("talosconfig client cert subject: %s\n", subjectOf(tcCtx.Crt))

	// Signing from each source, os:admin, 1h.
	sign("sign controlplane.yaml os:admin 1h", cpCA, now, role.MakeSet(role.Admin), time.Hour)
	sign("sign secrets.yaml os:admin 1h", bundle.Certs.OS, now, role.MakeSet(role.Admin), time.Hour)
	sign("sign talosconfig CA only (no key)", &x509.PEMEncodedCertificateAndKey{Crt: tcCA}, now, role.MakeSet(role.Admin), time.Hour)
	if wkCA != nil {
		sign("sign worker.yaml os:admin 1h", wkCA, now, role.MakeSet(role.Admin), time.Hour)
	}

	// Role sets.
	for _, rs := range []struct {
		name string
		set  role.Set
	}{
		{"os:admin", role.MakeSet(role.Admin)},
		{"os:reader", role.MakeSet(role.Reader)},
		{"os:operator", role.MakeSet(role.Operator)},
		{"os:reader+os:etcd:backup", role.MakeSet(role.Reader, role.EtcdBackup)},
		{"empty set", role.Zero},
		{"unknown os:bogus (Parse)", func() role.Set { s, _ := role.Parse([]string{"os:bogus"}); return s }()},
	} {
		sign("roles "+rs.name, cpCA, now, rs.set, time.Hour)
	}

	// Lifetime: no maximum; and the clock the caller passes decides NotBefore.
	sign("ttl 60s", cpCA, now, role.MakeSet(role.Admin), 60*time.Second)
	sign("ttl 87600h (ten years, the CA's own)", cpCA, now, role.MakeSet(role.Admin), 87600*time.Hour)
	sign("ttl 1000000h (about 114 years)", cpCA, now, role.MakeSet(role.Admin), 1000000*time.Hour)
	sign("currentTime = now+5s (skew ahead)", cpCA, now.Add(5*time.Second), role.MakeSet(role.Admin), time.Hour)
	sign("currentTime = now-1h (back-dated)", cpCA, now.Add(-time.Hour), role.MakeSet(role.Admin), 2*time.Hour)
	sign("currentTime = now-2h, ttl 1h (already expired)", cpCA, now.Add(-2*time.Hour), role.MakeSet(role.Admin), time.Hour)

	// A foreign CA signs a certificate that does not chain to the cluster's CA.
	foreign := must(secrets.NewTalosCA(now))
	fp2 := &x509.PEMEncodedCertificateAndKey{Crt: foreign.CrtPEM, Key: foreign.KeyPEM}
	p := must(secrets.NewAdminCertificateAndKey(now, fp2, role.MakeSet(role.Admin), time.Hour))
	describe("foreign CA os:admin, verified against the cluster CA", p, cpCA.Crt, now)

	// Serial numbers and key reuse between two signatures.
	a := must(secrets.NewAdminCertificateAndKey(now, cpCA, role.MakeSet(role.Admin), time.Hour))
	b := must(secrets.NewAdminCertificateAndKey(now, cpCA, role.MakeSet(role.Admin), time.Hour))
	fmt.Printf("two signatures with identical inputs: certificates equal=%v keys equal=%v serials %s / %s\n",
		string(a.Crt) == string(b.Crt), string(a.Key) == string(b.Key), serial(a.Crt), serial(b.Crt))
}

// talosconfigFor is a talosconfig document whose one context holds cert and the cluster CA.
func talosconfigFor(cert *x509.PEMEncodedCertificateAndKey, ca []byte, endpoint string) []byte {
	return must(clientconfig.NewConfig("evidence", []string{endpoint}, ca, cert).Bytes())
}

// attempt dials with cert, reads the node's version and, where asked, its machine configuration,
// and prints what the node answered. The configuration's content is never printed, only its size.
func attempt(label string, cert *x509.PEMEncodedCertificateAndKey, ca []byte, endpoint string, readConfig bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r, err := talos.Dial(ctx, talosconfigFor(cert, ca, endpoint), endpoint)
	if err != nil {
		fmt.Printf("%s: DIAL FAILED: %v\n", label, err)
		return
	}
	defer r.Close() //nolint:errcheck
	report(ctx, label, r, readConfig)
}

func report(ctx context.Context, label string, r talos.Reader, readConfig bool) {
	v, err := r.Version(ctx)
	if err != nil {
		fmt.Printf("%s: Version: %v (code %s)\n", label, err, status.Code(err))
	} else {
		fmt.Printf("%s: Version: ok %s\n", label, v)
	}
	if !readConfig {
		return
	}
	c, err := r.MachineConfig(ctx)
	if err != nil {
		fmt.Printf("%s: MachineConfig: %v (code %s)\n", label, err, status.Code(err))
		return
	}
	fmt.Printf("%s: MachineConfig: ok %d bytes\n", label, len(c.Bytes()))
}

// node presents locally signed certificates to the node at endpoint and records its answers.
// <dir> holds the fixture's controlplane.yaml, talos-secrets.yaml as secrets.yaml, and talosconfig.
func node(dir, endpoint string) {
	now := time.Now()
	fmt.Printf("run at %s, endpoint %s\n", now.UTC().Format(time.RFC3339), endpoint)
	cp := must(configloader.NewFromBytes(must(os.ReadFile(filepath.Join(dir, "controlplane.yaml")))))
	cpCA := cp.Machine().Security().IssuingCA()
	bundle := must(secrets.LoadBundle(filepath.Join(dir, "secrets.yaml")))
	opBytes := must(os.ReadFile(filepath.Join(dir, "talosconfig")))
	tc := must(clientconfig.FromBytes(opBytes))
	tcCtx := tc.Contexts[tc.Context]
	ca := must(base64.StdEncoding.DecodeString(tcCtx.CA))
	fmt.Printf("CA certificates equal across control plane config, bundle and talosconfig: %v\n",
		fp(cpCA.Crt) == fp(bundle.Certs.OS.Crt) && fp(ca) == fp(cpCA.Crt))

	admin := role.MakeSet(role.Admin)
	signed := func(src *x509.PEMEncodedCertificateAndKey, at time.Time, rs role.Set, ttl time.Duration) *x509.PEMEncodedCertificateAndKey {
		return must(secrets.NewAdminCertificateAndKey(at, src, rs, ttl))
	}

	// 1-2. Both CA sources, os:admin, 1h.
	attempt("1 os:admin, CA from control plane config", signed(cpCA, now, admin, time.Hour), ca, endpoint, true)
	attempt("2 os:admin, CA from secrets bundle", signed(bundle.Certs.OS, now, admin, time.Hour), ca, endpoint, true)
	// 3. Reader.
	attempt("3 os:reader", signed(cpCA, now, role.MakeSet(role.Reader), time.Hour), ca, endpoint, true)
	// 4. Foreign CA.
	foreign := must(secrets.NewTalosCA(now))
	attempt("4 os:admin signed by a foreign CA", signed(&x509.PEMEncodedCertificateAndKey{Crt: foreign.CrtPEM, Key: foreign.KeyPEM}, now, admin, time.Hour), ca, endpoint, true)
	// 5. NotBefore 5 s ahead: refused now, accepted once the node clock passes it.
	ahead := signed(cpCA, now.Add(5*time.Second), admin, time.Hour)
	attempt("5a NotBefore +5 s, presented at +0 s", ahead, ca, endpoint, false)
	time.Sleep(7 * time.Second)
	attempt("5b same certificate, presented at +7 s", ahead, ca, endpoint, false)
	// 6. Already expired.
	attempt("6 expired (signed at now-2h, ttl 1h)", signed(cpCA, now.Add(-2*time.Hour), admin, time.Hour), ca, endpoint, false)
	// 7. Back-dated, still valid.
	attempt("7 back-dated (signed at now-1h, ttl 2h)", signed(cpCA, now.Add(-time.Hour), admin, 2*time.Hour), ca, endpoint, false)
	// 8. The operator's own certificate and a local one, held at the same time.
	local := signed(cpCA, time.Now(), admin, time.Hour)
	ctx := context.Background()
	opR := must(talos.Dial(ctx, opBytes, endpoint))
	locR := must(talos.Dial(ctx, talosconfigFor(local, ca, endpoint), endpoint))
	report(ctx, "8 operator's own certificate", opR, true)
	report(ctx, "8 local certificate, same moment", locR, true)
	opR.Close()  //nolint:errcheck
	locR.Close() //nolint:errcheck
	// 9. A 60 s certificate: used, held past NotAfter, then a new connection.
	short := signed(cpCA, time.Now(), admin, 60*time.Second)
	held := must(talos.Dial(ctx, talosconfigFor(short, ca, endpoint), endpoint))
	t0 := time.Now()
	report(ctx, "9a 60 s certificate, first use", held, false)
	time.Sleep(time.Until(t0.Add(65 * time.Second)))
	report(ctx, "9b same connection, 5 s past NotAfter", held, false)
	attempt("9c new connection with the same certificate, 5 s past NotAfter", short, ca, endpoint, false)
	held.Close() //nolint:errcheck
}

func serial(crt []byte) string {
	b, _ := pem.Decode(crt)
	c := must(stdx509.ParseCertificate(b.Bytes))
	s := c.SerialNumber.Text(16)
	if len(s) > 12 {
		s = s[:12] + "..."
	}
	return s
}

func subjectOf(b64 string) string {
	raw := must(base64.StdEncoding.DecodeString(strings.TrimSpace(b64)))
	blk, _ := pem.Decode(raw)
	c := must(stdx509.ParseCertificate(blk.Bytes))
	return fmt.Sprintf("O=%v validity %s .. %s", c.Subject.Organization, c.NotBefore.UTC().Format(time.RFC3339), c.NotAfter.UTC().Format(time.RFC3339))
}
