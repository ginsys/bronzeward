package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ginsys/bronzeward/internal/talos/discover"
)

const talosconfigMarker = "synthetic-marker-in-the-file"

// discoverFixture writes a talosconfig whose key is talosconfigMarker and replaces the discovery with
// a stub that records what it was given. The stub is the whole of Talos here: the CLI's tests do
// not link the stand-in node (the closure test would refuse it); package discover's tests cover
// the reads.
type discoverCalls struct {
	file     string
	given    []string // the endpoint of each call
	contents []byte
}

func discoverFixture(t *testing.T, res discover.Result, err error) *discoverCalls {
	t.Helper()
	c := &discoverCalls{file: filepath.Join(t.TempDir(), "talosconfig")}
	if werr := os.WriteFile(c.file, []byte("context: a\ncontexts:\n  a:\n    key: "+talosconfigMarker+"\n"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	old := discoverCluster
	discoverCluster = func(_ context.Context, tc []byte, endpoint string) (discover.Result, error) {
		c.given, c.contents = append(c.given, endpoint), tc
		return res, err
	}
	t.Cleanup(func() { discoverCluster = old })
	return c
}

var twoMembers = discover.Result{
	ClusterID: "c-1", Version: "v1.13.6", Contract: "v1.13", Supported: true,
	KubernetesEndpoint: "https://10.0.0.1:6443",
	Members: []discover.Member{
		{NodeID: "n1", Hostname: "cp-1", Address: "10.0.0.1", Role: "controlplane", SMBIOSUUID: "11111111-2222-3333-4444-555555555555", Platform: "metal", Answered: true},
		{NodeID: "n2", Hostname: "w-1", Address: "10.0.0.2", Role: "worker", Platform: "container", Answered: true},
	},
}

// TestClusterDiscoverRequiresTalosconfig: the flag has no default lookup. Without it the command
// is a usage error that names the flag, before anything is read or dialled, whatever the
// environment holds.
func TestClusterDiscoverRequiresTalosconfig(t *testing.T) {
	c := discoverFixture(t, twoMembers, nil)
	t.Setenv("TALOSCONFIG", c.file)
	t.Setenv("HOME", filepath.Dir(c.file))
	for _, args := range [][]string{
		{"cluster", "discover"},
		{"cluster", "discover", "--endpoint", "10.0.0.1"},
		{"cluster", "discover", "--talosconfig", ""},
	} {
		code, out, errOut := runCtl(t, args...)
		if code != 2 || !strings.Contains(errOut, "--talosconfig") || out != "" {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
	if len(c.given) != 0 {
		t.Errorf("the discovery ran %d times without --talosconfig", len(c.given))
	}
}

func TestClusterDiscoverEndpointWhenTheFileNamesNone(t *testing.T) {
	c := discoverFixture(t, discover.Result{}, discover.ErrNoEndpoint)
	code, out, errOut := runCtl(t, "cluster", "discover", "--talosconfig", c.file)
	if code != 2 || !strings.Contains(errOut, "--endpoint") || out != "" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestClusterDiscoverPrintsCommands(t *testing.T) {
	c := discoverFixture(t, twoMembers, nil)
	code, out, errOut := runCtl(t, "cluster", "discover", "--talosconfig", c.file, "--endpoint", "10.0.0.1:50000")
	if code != 0 || errOut != "" {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if len(c.given) != 1 || c.given[0] != "10.0.0.1:50000" || !strings.Contains(string(c.contents), talosconfigMarker) {
		t.Fatalf("the discovery was given endpoints %q and %d bytes of the file", c.given, len(c.contents))
	}
	for _, want := range []string{
		"Talos cluster ID c-1, Talos v1.13.6, contract v1.13",
		"bronzewardctl cluster add --name <name> --endpoint 'https://10.0.0.1:6443' --contract 'v1.13' --talos-cluster-id 'c-1'",
		"bronzewardctl machine add --cluster <cluster id> --smbios-uuid '11111111-2222-3333-4444-555555555555' --platform 'metal' --talos-endpoint '10.0.0.1'",
		"bronzewardctl machine add --cluster <cluster id> --talos-node-id 'n2' --platform 'container' --talos-endpoint '10.0.0.2'",
		"bao kv put -mount=secret access/talos/<cluster id> 'talosconfig=@" + c.file + "'",
		"Nothing was sent to the server and no secret was written.",
		"talos-access-unavailable",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, talosconfigMarker) {
		t.Error("the output holds the talosconfig's key")
	}
	// A second discovery of the same cluster prints the same.
	if _, again, _ := runCtl(t, "cluster", "discover", "--talosconfig", c.file, "--endpoint", "10.0.0.1:50000"); again != out {
		t.Errorf("a second discovery prints:\n%s\nthe first printed:\n%s", again, out)
	}
}

// TestClusterDiscoverNamesTheResolvedAddress: when the endpoint was a DNS name, the output names the
// address that was dialled and the name it came from, and passes the name on unchanged. With an IP
// literal there is no such line.
func TestClusterDiscoverNamesTheResolvedAddress(t *testing.T) {
	res := twoMembers
	res.Endpoint, res.ResolvedFrom = "172.31.104.11:50000", "aquarak.office.ginsys.net"
	c := discoverFixture(t, res, nil)
	code, out, errOut := runCtl(t, "cluster", "discover", "--talosconfig", c.file, "--endpoint", "aquarak.office.ginsys.net")
	if code != 0 || errOut != "" {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if len(c.given) != 1 || c.given[0] != "aquarak.office.ginsys.net" {
		t.Errorf("the discovery was given endpoints %q", c.given)
	}
	if want := "Dialled 172.31.104.11:50000: the address that aquarak.office.ginsys.net resolved to, once. The first reads used that address. Each member was read at its own listed address."; !strings.Contains(out, want) {
		t.Errorf("no %q in:\n%s", want, out)
	}
	ip := discoverFixture(t, twoMembers, nil)
	if _, out, _ := runCtl(t, "cluster", "discover", "--talosconfig", ip.file, "--endpoint", "10.0.0.1"); strings.Contains(out, "Dialled") {
		t.Errorf("an IP literal prints a resolution line:\n%s", out)
	}
}

// TestClusterDiscoverRecordsNoMemberItCannotName: a member that did not answer, one that reported
// no platform, and an unsupported contract each get no command.
func TestClusterDiscoverRecordsNoMemberItCannotName(t *testing.T) {
	res := twoMembers
	res.Members = append(append([]discover.Member(nil), res.Members...),
		discover.Member{NodeID: "n3", Hostname: "w-2", Address: "10.0.0.3", Role: "worker"},
		discover.Member{NodeID: "n4", Hostname: "w-3", Address: "10.0.0.4", Role: "worker", Answered: true})
	c := discoverFixture(t, res, nil)
	_, out, _ := runCtl(t, "cluster", "discover", "--talosconfig", c.file)
	for _, want := range []string{"n3", "did not answer", "n4", "reported no platform"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if strings.Count(out, "bronzewardctl machine add") != 2 {
		t.Errorf("machine add lines for members that cannot be named:\n%s", out)
	}

	res.Supported = false
	discoverFixture(t, res, nil)
	_, out, _ = runCtl(t, "cluster", "discover", "--talosconfig", c.file)
	if strings.Contains(out, "bronzewardctl cluster add") || strings.Contains(out, "bao kv put") || !strings.Contains(out, "Not recorded: contract v1.13") {
		t.Errorf("an unsupported contract:\n%s", out)
	}
}

// TestClusterDiscoverWritesNothing: no request reaches a server, no context is needed, and a
// failure carries neither the file's key nor an exit code of a usage error.
func TestClusterDiscoverWritesNothing(t *testing.T) {
	var requests atomic.Int32 // a server the context would reach; it must see no request
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	t.Cleanup(srv.Close)
	automationContext(t, srv.URL)
	c := discoverFixture(t, discover.Result{}, os.ErrPermission)
	code, out, errOut := runCtl(t, "cluster", "discover", "--talosconfig", c.file)
	if code != 1 || out != "" || strings.Contains(errOut, talosconfigMarker) {
		t.Errorf("a failed discovery: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	discoverFixture(t, twoMembers, nil)
	runCtl(t, "cluster", "discover", "--talosconfig", c.file)
	if requests.Load() != 0 {
		t.Errorf("%d requests reached the server", requests.Load())
	}
	if code, _, errOut := runCtl(t, "cluster", "discover", "--talosconfig", filepath.Join(t.TempDir(), "absent")); code != 1 || strings.Contains(errOut, talosconfigMarker) {
		t.Errorf("an absent file: exit %d, stderr %q", code, errOut)
	}
	if code, _, errOut := runCtl(t, "-o", "json", "cluster", "discover", "--talosconfig", c.file); code != 2 || !strings.Contains(errOut, "table") {
		t.Errorf("-o json: exit %d, stderr %q", code, errOut)
	}
}

// TestClusterDiscoverBracketsAnIPv6Endpoint: the server's endpoint grammar takes an IPv6 literal
// only in brackets, so the printed machine add names the member's address that way.
func TestClusterDiscoverBracketsAnIPv6Endpoint(t *testing.T) {
	res := twoMembers
	res.Members = []discover.Member{
		{NodeID: "n1", Hostname: "cp-1", Address: "fd00::1", Role: "controlplane", Platform: "metal", Answered: true},
	}
	c := discoverFixture(t, res, nil)
	_, out, _ := runCtl(t, "cluster", "discover", "--talosconfig", c.file)
	if want := "--talos-endpoint '[fd00::1]'"; !strings.Contains(out, want) {
		t.Errorf("no %q in:\n%s", want, out)
	}
}

// TestClusterDiscoverRefusesAFileNameItCannotQuote: a name with a control character would print as
// a different file in the bao command, so the command refuses it before it reads anything.
func TestClusterDiscoverRefusesAFileNameItCannotQuote(t *testing.T) {
	c := discoverFixture(t, twoMembers, nil)
	odd := filepath.Join(filepath.Dir(c.file), "talos\tconfig")
	if err := os.Rename(c.file, odd); err != nil {
		t.Skipf("the file system takes no such name: %v", err)
	}
	code, out, errOut := runCtl(t, "cluster", "discover", "--talosconfig", odd)
	if code != 2 || out != "" || !strings.Contains(errOut, "--talosconfig") || len(c.given) != 0 {
		t.Errorf("exit %d, stdout %q, stderr %q, %d discoveries", code, out, errOut, len(c.given))
	}
}

// TestClusterDiscoverRefusesAFileNameThatIsNotUTF8: a name with an invalid byte prints as another
// name in the bao command, so it is refused like a name with a control character.
func TestClusterDiscoverRefusesAFileNameThatIsNotUTF8(t *testing.T) {
	c := discoverFixture(t, twoMembers, nil)
	odd := filepath.Join(filepath.Dir(c.file), "talos\xffconfig")
	if err := os.Rename(c.file, odd); err != nil {
		t.Skipf("the file system takes no such name: %v", err)
	}
	code, out, errOut := runCtl(t, "cluster", "discover", "--talosconfig", odd)
	if code != 2 || out != "" || !strings.Contains(errOut, "--talosconfig") || len(c.given) != 0 {
		t.Errorf("exit %d, stdout %q, stderr %q, %d discoveries", code, out, errOut, len(c.given))
	}
}
