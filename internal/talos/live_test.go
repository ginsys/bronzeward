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
	"google.golang.org/protobuf/types/known/durationpb"
)

// The live tests read the fixture's node (fixtures/bin/up), by hand:
//
//	BW_TEST_TALOSCONFIG=fixtures/.state/talosconfig BW_TEST_TALOS_ENDPOINT=10.55.0.2 \
//	BW_TEST_TALOS_NODE=10.55.0.3 go test -count=1 -v -run Live ./internal/talos
//
// They skip without those three variables; CI has no fixture.

func liveTarget(t *testing.T) (string, Target) {
	t.Helper()
	tc, ep, node := os.Getenv("BW_TEST_TALOSCONFIG"), os.Getenv("BW_TEST_TALOS_ENDPOINT"), os.Getenv("BW_TEST_TALOS_NODE")
	if tc == "" || ep == "" || node == "" {
		t.Skip("BW_TEST_TALOSCONFIG, BW_TEST_TALOS_ENDPOINT and BW_TEST_TALOS_NODE unset")
	}
	if !filepath.IsAbs(tc) {
		// Relative to the module root, as the command above names it.
		tc = filepath.Join(moduleRoot(t), tc)
	}
	return tc, Target{Endpoint: ep, Node: node}
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
	tc, target := liveTarget(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	r, err := Dial(ctx, tc, target)
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
	t.Logf("node %s: Talos %s, resource version %s, %d bytes, digest %s", target.Node, v, a.ResourceVersion(), len(a.Bytes()), digest(a.Bytes()))
}

func TestLiveUnreachable(t *testing.T) {
	tc, _ := liveTarget(t)
	// 192.0.2.1 is TEST-NET-1 (RFC 5737): nothing answers there.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	start := time.Now()
	r, err := Dial(ctx, tc, Target{Endpoint: "192.0.2.1", Node: "192.0.2.1"})
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
	tc, target := liveTarget(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	admin, err := client.New(ctx, client.WithConfigFromFile(tc), client.WithEndpoints(target.Endpoint))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	dir := t.TempDir()
	for _, role := range []string{"os:reader", "os:operator", "os:admin"} {
		resp, err := admin.GenerateClientConfiguration(client.WithNode(ctx, target.Endpoint), &machine.GenerateClientConfigurationRequest{
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
		path := filepath.Join(dir, strings.ReplaceAll(role, ":", "-"))
		if err := os.WriteFile(path, msgs[0].GetTalosconfig(), 0o600); err != nil {
			t.Fatal(err)
		}
		r, err := Dial(ctx, path, target)
		if err != nil {
			t.Fatal(err)
		}
		_, verr := r.Version(ctx)
		_, merr := r.MachineConfig(ctx)
		r.Close()
		t.Logf("role probe %s: Version error=%v; MachineConfig error=%v", role, verr, merr)
		if role == "os:admin" && (verr != nil || merr != nil) {
			t.Fatal("os:admin cannot read: the probe is broken")
		}
	}
}
