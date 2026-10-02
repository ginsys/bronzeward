package talos

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

// The live tests read the fixture's worker (fixtures/bin/up) directly at its own endpoint, by hand:
//
//	BW_TEST_TALOSCONFIG=fixtures/.state/talosconfig BW_TEST_TALOS_ENDPOINT=10.55.0.3 \
//	go test -count=1 -v -run Live ./internal/talos
//
// They skip without those two variables; CI has no fixture. The test reads the talosconfig file
// and passes its bytes, as the provider read hands them over; the package never reads a path.

func liveTarget(t *testing.T) ([]byte, string) {
	t.Helper()
	tc, ep := os.Getenv("BW_TEST_TALOSCONFIG"), os.Getenv("BW_TEST_TALOS_ENDPOINT")
	if tc == "" || ep == "" {
		t.Skip("BW_TEST_TALOSCONFIG and BW_TEST_TALOS_ENDPOINT unset")
	}
	if !filepath.IsAbs(tc) {
		// Relative to the module root, as the command above names it.
		tc = filepath.Join(moduleRoot(t), tc)
	}
	b, err := os.ReadFile(tc)
	if err != nil {
		t.Fatal(err)
	}
	return b, ep
}

// digest is execution-recovery.md §1's configuration digest: SHA-256 over the read-back with its
// trailing newlines replaced by one.
func digest(b []byte) string {
	sum := sha256.Sum256(append(bytes.TrimRight(b, "\n"), '\n'))
	return hex.EncodeToString(sum[:])
}

func pinnedTalosVersion(t *testing.T) string {
	t.Helper()
	f, err := os.Open(filepath.Join(moduleRoot(t), "fixtures", "versions.env"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if v, ok := strings.CutPrefix(s.Text(), "TALOS_VERSION="); ok {
			return v
		}
	}
	t.Fatal("no TALOS_VERSION in fixtures/versions.env")
	return ""
}

func TestLiveRead(t *testing.T) {
	tc, ep := liveTarget(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	r, err := Dial(ctx, tc, ep)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	v, err := r.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := pinnedTalosVersion(t); v != want {
		t.Fatalf("Version %s, want the pin %s", v, want)
	}
	a, err := r.MachineConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.MachineConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) || a.ResourceVersion() != b.ResourceVersion() || a.ResourceVersion() == "" {
		t.Fatalf("two reads differ: resource versions %q, %q", a.ResourceVersion(), b.ResourceVersion())
	}
	if !bytes.Contains(a.Bytes(), []byte("version: v1alpha1")) {
		t.Fatal("the read holds no version: v1alpha1")
	}
	if digest(a.Bytes()) != digest(b.Bytes()) {
		t.Fatal("digest unstable")
	}
	t.Logf("node %s: Talos %s, resource version %s, %d bytes, digest %s", ep, v, a.ResourceVersion(), len(a.Bytes()), digest(a.Bytes()))
}

func TestLiveUnreachable(t *testing.T) {
	tc, _ := liveTarget(t)
	// 192.0.2.1 is TEST-NET-1 (RFC 5737): nothing answers there.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	start := time.Now()
	r, err := Dial(ctx, tc, "192.0.2.1")
	if err == nil {
		defer r.Close()
		_, err = r.MachineConfig(ctx)
	}
	took := time.Since(start)
	if err == nil {
		t.Fatal("an unreachable node answered")
	}
	if took > 10*time.Second {
		t.Fatalf("the refusal took %s", took)
	}
	t.Logf("unreachable: refused after %s", took.Round(time.Millisecond))
}

// TestLiveRoleProbe settles which Talos role ingestion's read needs: talosconfigs for os:reader
// and os:operator, generated through the fixture's os:admin config, each try MachineConfig. It
// reports; it asserts only that os:admin reads.
func TestLiveRoleProbe(t *testing.T) {
	tc, ep := liveTarget(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cfg, err := clientconfig.FromBytes(tc)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := client.New(ctx, client.WithConfig(cfg), client.WithEndpoints(ep))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	configs := map[string][]byte{}
	for _, role := range []string{"os:reader", "os:operator", "os:admin"} {
		resp, err := admin.GenerateClientConfiguration(ctx, &machine.GenerateClientConfigurationRequest{
			Roles:  []string{role},
			CrtTtl: durationpb.New(10 * time.Minute),
		})
		if err != nil {
			t.Fatalf("%s: generating a talosconfig: %v", role, err)
		}
		msgs := resp.GetMessages()
		if len(msgs) != 1 || len(msgs[0].GetTalosconfig()) == 0 {
			t.Fatalf("%s: %d answers", role, len(msgs))
		}
		configs[role] = msgs[0].GetTalosconfig()
	}
	// probe reads the version and the machine configuration with a talosconfig.
	probe := func(tc []byte) (verr, merr error) {
		r, err := Dial(ctx, tc, ep)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		_, verr = r.Version(ctx)
		_, merr = r.MachineConfig(ctx)
		return verr, merr
	}
	// PA §3.3, §16: a reader or operator reads the version but is refused the MachineConfig
	// resource with PermissionDenied, so the credential must be os:admin.
	refused := func(verr, merr error) bool { return verr == nil && status.Code(merr) == codes.PermissionDenied }
	for _, role := range []string{"os:reader", "os:operator"} {
		verr, merr := probe(configs[role])
		t.Logf("role probe %s: Version error=%v; MachineConfig error=%v", role, verr, merr)
		if !refused(verr, merr) {
			t.Errorf("%s: Version error %v, MachineConfig error %v; want the version and PermissionDenied", role, verr, merr)
		}
	}
	// The control, on the raw os:admin results: the same assertion with the os:admin configuration
	// in the os:reader slot must fail, so a pass above is the role's refusal, not the probe's.
	verr, merr := probe(configs["os:admin"])
	if refused(verr, merr) {
		t.Fatalf("control: the os:admin configuration passed as refused (MachineConfig %v); the assertion cannot fail", merr)
	}
	if verr != nil || merr != nil {
		t.Fatalf("os:admin cannot read (Version %v, MachineConfig %v): the probe is broken", verr, merr)
	}
}
