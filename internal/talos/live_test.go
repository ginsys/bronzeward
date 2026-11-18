package talos

import (
	"bufio"
	"bytes"
	"context"
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
// TestLiveRoleProbe needs the control plane's endpoint (10.55.0.2): Talos generates client
// configurations only on a control plane node. They skip without those two variables; CI has no
// fixture. The test reads the talosconfig file
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

// digest is the configuration digest in hex, for comparison and the log.
func digest(b []byte) string {
	sum := ConfigurationDigest(b)
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

// TestLiveApply sends the worker's own configuration back to it in no-reboot mode: an accepted
// apply of an unchanged document, after which the read is byte-identical. It then sends the same
// document with an unknown machine type, which the node refuses as validation (E4 row 022): the
// class is a rejection, and the read is unchanged again, its resource version too. The
// unrecognised-InvalidArgument and transport classes have no live trigger and stay on the stand-in
// node.
func TestLiveApply(t *testing.T) {
	tc, ep := liveTarget(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	r, err := Dial(ctx, tc, ep)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	before, err := r.MachineConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, err := DialApplier(ctx, tc, ep)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	send := func(b []byte) Response {
		sctx, scancel := context.WithTimeout(ctx, 30*time.Second)
		defer scancel()
		resp, err := a.ApplyNoReboot(sctx, b)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if got := send(before.Bytes()); got.Class != ClassAccepted || !got.Accounts() {
		t.Fatalf("an unchanged configuration: %+v", got)
	}
	// The resource version the rejection must leave alone is the one after the accepted apply.
	accepted, err := r.MachineConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bogus := bytes.Replace(before.Bytes(), []byte("type: worker"), []byte("type: bogus"), 1)
	if bytes.Equal(bogus, before.Bytes()) {
		t.Fatal("control: the configuration holds no `type: worker` to corrupt")
	}
	got := send(bogus)
	t.Logf("validation refusal answered %+v", got)
	if got.Class != ClassRejected {
		// Not refused: the node may hold the corrupted document, and later live tests share it. Put
		// the original back before failing.
		t.Logf("restoring the original configuration: %+v", send(before.Bytes()))
	}
	if got.Class != ClassRejected || !got.Accounts() {
		t.Fatalf("an unknown machine type: %+v; want a rejection", got)
	}
	after, err := r.MachineConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatalf("the node's configuration changed: digest %s, then %s", digest(before.Bytes()), digest(after.Bytes()))
	}
	if after.ResourceVersion() != accepted.ResourceVersion() || after.ResourceVersion() == "" {
		t.Fatalf("the rejection moved the resource version: %q, then %q", accepted.ResourceVersion(), after.ResourceVersion())
	}
}

// activationLabel is the node label the activation evidence applies (ginsys/bronzeward#714). It
// cannot collide with the scenarios' labels (`tier`, `bw-s2`).
const activationLabel = "bw-act"

// TestLiveApplyLabelDocument applies the worker's own configuration with activationLabel added to
// its KubeNodeConfig document, in no-reboot mode (E1), and reads the machine configuration
// before and after (Read A, E2). Class accepted is the proof that the node named NO_REBOOT:
// classifyApply admits nothing else, so another mode would show as unrecognised with code OK, and
// the test would fail on it rather than widen the check. The label resource (Read B) and the
// Kubernetes Node (Read C) are read by the fixture run with talosctl and kubectl, not here.
// A deferred restore puts the original configuration back, so a failed assertion still leaves the
// node as it was. It needs a worker whose one KubeNodeConfig document holds no labels yet.
func TestLiveApplyLabelDocument(t *testing.T) {
	tc, ep := liveTarget(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	r, err := Dial(ctx, tc, ep)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	before, err := r.MachineConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original := before.Bytes()
	// The label goes into the worker's own KubeNodeConfig document: a second one is refused as a
	// duplicate. The control: exactly one such document, and it holds no labels yet.
	const kindLine = "\nkind: KubeNodeConfig\n"
	if n := bytes.Count(original, []byte(kindLine)); n != 1 {
		t.Fatalf("control: the worker's configuration holds %d KubeNodeConfig documents; want exactly one", n)
	}
	at := bytes.Index(original, []byte(kindLine)) + len(kindLine)
	end := len(original)
	if next := bytes.Index(original[at:], []byte("\n---")); next >= 0 {
		end = at + next
	}
	if bytes.Contains(original[at:end], []byte("labels:")) {
		t.Fatal("control: the worker's KubeNodeConfig document already holds labels")
	}
	a, err := DialApplier(ctx, tc, ep)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	send := func(sendCtx context.Context, b []byte) Response {
		sctx, scancel := context.WithTimeout(sendCtx, 30*time.Second)
		defer scancel()
		resp, err := a.ApplyNoReboot(sctx, b)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	// A defer, not t.Cleanup: it runs before the two Close calls above, which a Cleanup would follow.
	// The test context may have ended by then, so a fresh one bounds the restore and its read.
	defer func() {
		rctx, rcancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer rcancel()
		restored := send(rctx, original)
		t.Logf("restore of the original configuration: %+v", restored)
		if restored.Class != ClassAccepted {
			t.Errorf("the original configuration was not restored: %+v", restored)
			return
		}
		deadline := time.Now().Add(time.Minute)
		for {
			got, err := r.MachineConfig(rctx)
			if err == nil && bytes.Equal(got.Bytes(), original) {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("the node did not return to its original configuration within a minute (read error %v)", err)
				return
			}
			time.Sleep(time.Second)
		}
	}()

	// A new slice, so building it cannot write into original, which the restore sends back.
	labels := "labels:\n  " + activationLabel + ": \"yes\"\n"
	planned := make([]byte, 0, len(original)+len(labels))
	planned = append(planned, original[:at]...)
	planned = append(planned, labels...)
	planned = append(planned, original[at:]...)
	got := send(ctx, planned)
	t.Logf("apply of the label document answered %+v (accepted means the node named NO_REBOOT, one message, no error)", got)
	if got.Class != ClassAccepted || !got.Accounts() {
		t.Fatalf("the label document: %+v; want accepted", got)
	}

	// Read A: the stored configuration holds the label and its resource version moved. The node may
	// take a moment to store it, so the read repeats for up to 30 seconds and logs when it showed.
	start := time.Now()
	deadline := start.Add(30 * time.Second)
	for {
		after, err := r.MachineConfig(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(after.Bytes(), []byte(activationLabel)) {
			t.Logf("Read A: the label showed %s after the apply; resource version %q to %q, digest %s to %s",
				time.Since(start).Round(time.Millisecond), before.ResourceVersion(), after.ResourceVersion(),
				digest(original), digest(after.Bytes()))
			if after.ResourceVersion() == before.ResourceVersion() {
				t.Fatalf("the label is stored but the resource version did not move: %q", after.ResourceVersion())
			}
			// The restore follows at once, so the fixture run reads Reads B and C while the label
			// is held: BW_TEST_ACTIVATION_HOLD names how long (a Go duration, none by default).
			if hold := os.Getenv("BW_TEST_ACTIVATION_HOLD"); hold != "" {
				d, err := time.ParseDuration(hold)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("holding the label for %s", d)
				time.Sleep(d)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Read A: the stored configuration holds no %q 30 seconds after an accepted apply", activationLabel)
		}
		time.Sleep(time.Second)
	}
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
