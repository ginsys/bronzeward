package clitest

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

// The cases below run against the release archives' own binaries (ginsys/bronzeward#405): BW_BIN_DIR names the directory
// holding the packaged bronzeward and bronzewardctl, BW_EXPECT_VERSION the version the archives were built at. The CI
// job `packaged-cli` sets both and BW_REQUIRE_PACKAGED, which turns a missing variable into a failure; without them the
// cases skip, so `go test ./...` on a checkout runs the source build as before. TestSharedCases (TLS-01, TLS-02, COMP-01,
// COMP-02) takes the same directory through binaries, so the job runs those cases on the packaged binaries too.

// packaged returns the directory and version the packaged cases run against, or skips (fails, when required).
func packaged(t *testing.T) (dir, version string) {
	t.Helper()
	dir, version = os.Getenv("BW_BIN_DIR"), os.Getenv("BW_EXPECT_VERSION")
	if dir == "" || version == "" {
		if os.Getenv("BW_REQUIRE_PACKAGED") != "" {
			t.Fatal("BW_BIN_DIR and BW_EXPECT_VERSION must name the packaged binaries and the version they were built at")
		}
		t.Skip("BW_BIN_DIR and BW_EXPECT_VERSION are not set: no packaged binaries to run")
	}
	return dir, version
}

// TestPackagedVersion: both packaged binaries carry the version they were built at, the client prints its own and the
// server's, and with the server gone it prints its own and says the server's is unknown, with a failing exit.
func TestPackagedVersion(t *testing.T) {
	dir, version := packaged(t)
	f := start(t, "viewer")
	if f.bin != dir {
		t.Fatalf("the fixture runs %s, not the packaged directory %s", f.bin, dir)
	}
	f.want(f.ctl(nil, "", "context", "add", "lab", "--server", f.base, "--automation-token-file", f.tokenFile), 0, "added")

	r := f.ctl(nil, "", "version")
	f.want(r, 0)
	v := regexp.QuoteMeta(version)
	f.wantMatch(r, `(?m)^Client\s+bronzewardctl `+v+`, needs API revision \d+ or later$`)
	f.wantMatch(r, `(?m)^Server\s+`+v+`, API revision \d+, start mode normal\b`)

	var both struct {
		Client struct{ Version string }
		Server struct{ Version string }
	}
	r = f.ctl(nil, "", "version", "-o", "json")
	f.want(r, 0)
	if err := json.Unmarshal([]byte(r.out), &both); err != nil {
		t.Fatalf("version -o json: %v\n%s", err, r.out)
	}
	if both.Client.Version != version || both.Server.Version != version {
		t.Errorf("client %q, server %q: want both %q", both.Client.Version, both.Server.Version, version)
	}

	// The inaccessible server: its process is gone, the client still names its own version and fails.
	f.srv.stop()
	r = f.ctl(nil, "", "version")
	if r.code == 0 {
		t.Errorf("version against a server that is gone exited 0\nstdout: %s\nstderr: %s", r.out, r.err)
	}
	f.wantMatch(r, `(?m)^Client\s+bronzewardctl `+v+`, needs API revision \d+ or later$`)
	f.wantMatch(r, `(?m)^Server\s+unknown$`)
}
