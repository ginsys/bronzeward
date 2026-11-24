package clitest

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/ginsys/bronzeward/internal/client"
)

// TestContextWithoutScopesOrAudience: client-contracts.md §5 requires oidc.scopes and oidc.audience
// with oidc. A contexts file written before that, or by hand, still loads, so the person lists,
// selects and removes the context; a command that opens a client names what to add before any request.
func TestContextWithoutScopesOrAudience(t *testing.T) {
	f := start(t, "viewer")
	// A server that nothing answers: the refusal must come before any request.
	cfg := "current: old\ncontexts:\n" +
		"  - name: old\n    server: https://bronzeward.example.test\n    credential: oidc\n" +
		"    oidc:\n      issuer: https://idp.example.test\n      clientID: BWSYNTH-cli\n"
	if err := os.MkdirAll(filepath.Join(f.home, "bronzeward"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.home, "bronzeward", "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	f.want(f.ctl(nil, "", "context", "list"), 0, "old")
	f.want(f.ctl(nil, "", "whoami"), 1, "oidc.scopes is empty", "scopes: [openid, offline_access]", "oidc.audience is empty", "auth.oidc.audience")
	f.want(f.ctl(nil, "", "context", "use", "old"), 0, "Current context")
	f.want(f.ctl(nil, "", "context", "remove", "old"), 0, `Context "old" removed`)
}

// TestContextAddStoresScopesAndNeedsAnAudience: both ways of adding a context hold the scopes in
// the stored context, openid offline_access when the person names none, and both need an audience.
func TestContextAddStoresScopesAndNeedsAnAudience(t *testing.T) {
	f := start(t, "viewer")
	in := newInjector(t, f.base)
	serve := func(body string) {
		in.set(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/bronzeward-client.json" {
				return false
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
			return true
		}, nil)
	}
	stored := func(name string) []string {
		var cfg client.Config
		b, _ := os.ReadFile(filepath.Join(f.home, "bronzeward", "config.yaml"))
		if err := yaml.Unmarshal(b, &cfg); err != nil || cfg.Find(name) == nil || cfg.Find(name).OIDC == nil {
			t.Fatalf("no oidc context %q kept:\n%s", name, b)
		}
		return cfg.Find(name).OIDC.Scopes
	}
	base := []string{"openid", "offline_access"}

	// By hand: no --scopes stores the base scopes, a named --scopes is kept as typed.
	hand := []string{"--server", in.URL, "--certificate-authority", in.ca, "--issuer", "https://idp.example.invalid", "--client-id", "BWSYNTH-cli"}
	f.want(f.ctl(nil, "", append([]string{"context", "add", "noscopes"}, append(hand, "--audience", "bronzeward")...)...), 0, `Context "noscopes" added`)
	if got := stored("noscopes"); !slices.Equal(got, base) {
		t.Errorf("by hand with no --scopes kept %v, want %v", got, base)
	}
	f.want(f.ctl(nil, "", append([]string{"context", "add", "named"}, append(hand, "--audience", "bronzeward", "--scopes", "openid,groups")...)...), 0, `Context "named" added`)
	if got := stored("named"); !slices.Equal(got, []string{"openid", "groups"}) {
		t.Errorf("by hand with --scopes openid,groups kept %v", got)
	}
	f.want(f.ctl(nil, "", append([]string{"context", "add", "noaudience"}, hand...)...), 2, "oidc.audience is empty", "--audience")

	// --from: a file with no scopes member stores the base scopes; one with no audience is refused.
	serve(`{"issuer":"https://idp.example.invalid","audience":"bronzeward","cliClientId":"BWSYNTH-cli","webClientId":"BWSYNTH-web"}`)
	f.want(f.ctl(nil, "", "context", "add", "fromfile", "--from", in.URL, "--certificate-authority", in.ca), 0, `Context "fromfile" added`)
	if got := stored("fromfile"); !slices.Equal(got, base) {
		t.Errorf("--from a file with no scopes kept %v, want %v", got, base)
	}
	serve(`{"issuer":"https://idp.example.invalid","cliClientId":"BWSYNTH-cli","webClientId":"BWSYNTH-web"}`)
	f.want(f.ctl(nil, "", "context", "add", "noaud", "--from", in.URL, "--certificate-authority", in.ca), 1, "the file names no audience")
}

// TestMinimumRevisionIsTheHighestRouteTheCLICalls: the CLI declares the highest revision of any
// route it calls, 10 (a machine's name), and refuses a server below it with both revisions named
// before the command's own request (§7 items 1 and 3).
func TestMinimumRevisionIsTheHighestRouteTheCLICalls(t *testing.T) {
	f := start(t, "viewer")
	in := newInjector(t, f.base)
	f.want(f.ctl(nil, "", "context", "add", "inj", "--server", in.URL, "--certificate-authority", in.ca, "--automation-token-file", f.tokenFile), 0)
	serve := func(n int) {
		in.set(nil, func(r *http.Response) {
			if on("/server")(r.Request) {
				revisionAt(r, n)
			}
		})
	}
	serve(9)
	f.want(f.ctl(nil, "", "--context", "inj", "whoami"), 1, "API revision 9", "needs revision 10 or later")
	if slices.Contains(in.requests(), "GET /api/v1/identity") {
		t.Error("the identity read was sent after the refusal")
	}
	serve(10)
	f.want(f.ctl(nil, "", "--context", "inj", "whoami"), 0)
}
