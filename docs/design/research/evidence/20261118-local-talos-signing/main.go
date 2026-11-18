//go:build ignore

// Offline measurements for docs/design/research/20261118-local-talos-signing.md.
//
// Run from the repository root, so the module's pinned Talos machinery is used:
//
//	go run docs/design/research/evidence/20261118-local-talos-signing/main.go offline <dir>
//
// <dir> holds a synthetic secrets.yaml, controlplane.yaml, worker.yaml and talosconfig made with
// `talosctl gen secrets` and `talosctl gen config`. No key is printed: the program prints public
// certificate fields, fingerprints and gRPC codes, and an error from reading or decoding an
// input is replaced by a fixed line, since a decoder error can quote the rejected input. Not
// product code.
//
// The `extract` mode (ginsys/bronzeward#883) starts from the operator's talosconfig alone: it reads
// a control plane's machine configuration through it, takes the os CA key from what the node
// returned, signs a fresh admin certificate from that key and presents it. It prints only
// fingerprints, sizes and gRPC codes.
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
	"runtime"
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
	case len(os.Args) == 4 && os.Args[1] == "extract":
		extract(os.Args[2], os.Args[3])
	default:
		fmt.Fprintln(os.Stderr, "usage: main.go offline <dir> | main.go node <dir> <endpoint ip[:port]> | main.go extract <dir> <endpoint ip[:port]>")
		os.Exit(2)
	}
}

// must stops the program on an error and prints where, never the error: a decoder error can quote
// the rejected input, and the inputs here hold keys.
func must[T any](v T, err error) T {
	if err != nil {
		_, _, line, _ := runtime.Caller(1)
		fmt.Fprintf(os.Stderr, "fatal: the step at main.go:%d failed (error text withheld: it can quote its input)\n", line)
		os.Exit(1)
	}
	return v
}

// require stops the program with a fixed line when an input lacks something the program reads.
func require(ok bool, what string) {
	if !ok {
		fmt.Fprintln(os.Stderr, "fatal: the input lacks", what)
		os.Exit(1)
	}
}

// certOf parses the first PEM block of crt as a certificate.
func certOf(crt []byte) *stdx509.Certificate {
	b, _ := pem.Decode(crt)
	require(b != nil, "a PEM certificate")
	return must(stdx509.ParseCertificate(b.Bytes))
}

// currentContext is the talosconfig's current context.
func currentContext(tc *clientconfig.Config) *clientconfig.Context {
	c := tc.Contexts[tc.Context]
	require(c != nil, "the talosconfig's current context")
	return c
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
	c := certOf(p.Crt)
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
	require(cpCA != nil && bundle.Certs != nil && bundle.Certs.OS != nil, "an os CA in the control plane configuration and the bundle")
	tc := must(clientconfig.Open(filepath.Join(dir, "talosconfig")))
	tcCtx := currentContext(tc)
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

// extract starts from the operator's talosconfig and nothing else a first key would give. <dir>
// holds the fixture's talosconfig, and secrets.yaml only to compare the CA certificate. The key that
// signs in step 3 comes from the node's answer, never from a local file.
func extract(dir, endpoint string) {
	start := time.Now()
	fmt.Printf("run at %s, endpoint %s\n", start.UTC().Format(time.RFC3339), endpoint)
	opBytes := must(os.ReadFile(filepath.Join(dir, "talosconfig")))
	tc := must(clientconfig.FromBytes(opBytes))
	tcCtx := currentContext(tc)
	ca := must(base64.StdEncoding.DecodeString(tcCtx.CA))
	bundle := must(secrets.LoadBundle(filepath.Join(dir, "secrets.yaml")))
	require(bundle.Certs != nil && bundle.Certs.OS != nil, "an os CA in the bundle")
	fmt.Printf("operator talosconfig client certificate subject: %s\n", subjectOf(tcCtx.Crt))

	// 1. The one read: the control plane's machine configuration, through the operator's talosconfig.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r, err := talos.Dial(ctx, opBytes, endpoint)
	if err != nil {
		fmt.Println("1 DIAL FAILED with the operator's talosconfig")
		os.Exit(1)
	}
	c, err := r.MachineConfig(ctx)
	r.Close() //nolint:errcheck
	if err != nil {
		fmt.Printf("1 operator talosconfig: MachineConfig failed (code %s)\n", status.Code(err))
		os.Exit(1)
	}
	fmt.Printf("1 operator talosconfig: MachineConfig: ok %d bytes\n", len(c.Bytes()))

	// 2. What the answer carries. Only presence, a size and fingerprints are printed.
	got := must(configloader.NewFromBytes(c.Bytes())).Machine().Security().IssuingCA()
	if got == nil || len(got.Crt) == 0 || len(got.Key) == 0 {
		fmt.Printf("2 STOP: the fetched configuration carries no os CA key (CA present=%v, key present=%v); nothing was signed\n",
			got != nil && len(got.Crt) > 0, got != nil && len(got.Key) > 0)
		os.Exit(3)
	}
	fmt.Printf("2 fetched configuration: os CA certificate %s, key present=true (%d bytes); CA certificate equals the bundle's=%v, the talosconfig's=%v\n",
		fp(got.Crt), len(got.Key), fp(got.Crt) == fp(bundle.Certs.OS.Crt), fp(got.Crt) == fp(ca))

	// 3. Sign from the extracted key and present the certificate.
	now := time.Now()
	fresh := must(secrets.NewAdminCertificateAndKey(now, got, role.MakeSet(role.Admin), time.Hour))
	describe("3 os:admin signed from the extracted key", fresh, ca, now)
	attempt("3 os:admin signed from the extracted key", fresh, ca, endpoint, true)

	// 4. The same read as os:reader, signed from the extracted key.
	reader := must(secrets.NewAdminCertificateAndKey(now, got, role.MakeSet(role.Reader), time.Hour))
	attempt("4 os:reader signed from the extracted key, same MachineConfig read", reader, ca, endpoint, true)

	// 5. Negative control: an admin certificate signed by a fresh unrelated CA key, presented the
	// same way. If the node accepted it, step 3 would not show that the node checks the issuer.
	foreign := must(secrets.NewTalosCA(now))
	unrelated := must(secrets.NewAdminCertificateAndKey(now, &x509.PEMEncodedCertificateAndKey{Crt: foreign.CrtPEM, Key: foreign.KeyPEM}, role.MakeSet(role.Admin), time.Hour))
	attempt("5 os:admin signed by a fresh unrelated CA key (negative control)", unrelated, ca, endpoint, true)
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
	require(cpCA != nil && bundle.Certs != nil && bundle.Certs.OS != nil, "an os CA in the control plane configuration and the bundle")
	tcCtx := currentContext(tc)
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
	// 5. NotBefore 5 s ahead of a fresh clock read: refused before it, accepted after it. Each
	// presentation prints its own offset from NotBefore (negative: before it).
	t5 := time.Now()
	ahead := signed(cpCA, t5.Add(5*time.Second), admin, time.Hour)
	nb5, _ := window(ahead)
	fmt.Printf("5 certificate signed at %s, NotBefore %s\n", t5.UTC().Format("15:04:05.000"), nb5.UTC().Format("15:04:05"))
	p5a := time.Now()
	attempt(fmt.Sprintf("5a presented %.1f s from NotBefore (before it)", p5a.Sub(nb5).Seconds()), ahead, ca, endpoint, false)
	time.Sleep(time.Until(nb5.Add(2 * time.Second)))
	attempt(fmt.Sprintf("5b same certificate, presented %.1f s after NotBefore", time.Since(nb5).Seconds()), ahead, ca, endpoint, false)
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
	_, na9 := window(short)
	held := must(talos.Dial(ctx, talosconfigFor(short, ca, endpoint), endpoint))
	report(ctx, fmt.Sprintf("9a 60 s certificate, first use %.1f s before NotAfter", time.Until(na9).Seconds()), held, false)
	time.Sleep(time.Until(na9.Add(5 * time.Second)))
	report(ctx, fmt.Sprintf("9b same connection, %.1f s after NotAfter", time.Since(na9).Seconds()), held, false)
	attempt(fmt.Sprintf("9c new connection with the same certificate, %.1f s after NotAfter", time.Since(na9).Seconds()), short, ca, endpoint, false)
	held.Close() //nolint:errcheck
}

// window is the validity window the certificate carries (NotBefore and NotAfter, whole seconds).
func window(p *x509.PEMEncodedCertificateAndKey) (notBefore, notAfter time.Time) {
	c := certOf(p.Crt)
	return c.NotBefore, c.NotAfter
}

func serial(crt []byte) string {
	s := certOf(crt).SerialNumber.Text(16)
	if len(s) > 12 {
		s = s[:12] + "..."
	}
	return s
}

func subjectOf(b64 string) string {
	raw := must(base64.StdEncoding.DecodeString(strings.TrimSpace(b64)))
	c := certOf(raw)
	return fmt.Sprintf("O=%v validity %s .. %s", c.Subject.Organization, c.NotBefore.UTC().Format(time.RFC3339), c.NotAfter.UTC().Format(time.RFC3339))
}
