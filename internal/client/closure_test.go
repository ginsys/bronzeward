package client_test

import (
	"bytes"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// The client's packages, whose dependency closure must hold none of the server's packages
// listed in forbidden (decision T2 of the client's ticket), so bronzewardctl never links the
// server's API, database, provider or Talos packages, or the PostgreSQL driver.
var clientPatterns = []string{
	"github.com/ginsys/bronzeward/cmd/bronzewardctl/...",
	"github.com/ginsys/bronzeward/internal/client/...",
}

// forbidden reports whether a package in a client's closure is one T2 excludes.
func forbidden(pkg string) bool {
	for _, prefix := range []string{
		"github.com/ginsys/bronzeward/internal/api",
		"github.com/ginsys/bronzeward/internal/database",
		"github.com/ginsys/bronzeward/internal/provider",
		"github.com/ginsys/bronzeward/internal/talos",
		"github.com/jackc/pgx",
	} {
		if pkg == prefix || strings.HasPrefix(pkg, prefix+"/") {
			return true
		}
	}
	return false
}

// closure lists every package the patterns' packages and their tests depend on.
func closure(t *testing.T, patterns ...string) []string {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list", "-deps", "-test", "-f", "{{.ImportPath}}"}, patterns...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, stderr.Bytes())
	}
	// -test lists a package recompiled for a test as "path [path.test]"; the path alone is kept.
	var pkgs []string
	for line := range strings.Lines(string(out)) {
		path, _, _ := strings.Cut(strings.TrimSpace(line), " ")
		pkgs = append(pkgs, path)
	}
	return pkgs
}

func TestClientClosureExcludesServerPackages(t *testing.T) {
	pkgs := closure(t, clientPatterns...)
	// The listing names this package and the generated types, so the check below is not run on
	// an empty closure.
	for _, want := range []string{
		"github.com/ginsys/bronzeward/internal/client",
		"github.com/ginsys/bronzeward/internal/client/apitypes",
	} {
		if !slices.Contains(pkgs, want) {
			t.Fatalf("go list -deps %v: %s missing from the closure", clientPatterns, want)
		}
	}
	for _, p := range pkgs {
		if forbidden(p) {
			t.Errorf("the client's closure holds %s", p)
		}
	}
}

// The check finds the excluded packages where they are linked: the server's API package depends
// on all of them.
func TestForbiddenFindsServerPackages(t *testing.T) {
	var found []string
	for _, p := range closure(t, "github.com/ginsys/bronzeward/internal/api") {
		if forbidden(p) {
			found = append(found, p)
		}
	}
	for _, want := range []string{
		"github.com/ginsys/bronzeward/internal/api",
		"github.com/ginsys/bronzeward/internal/database",
		"github.com/ginsys/bronzeward/internal/provider",
		"github.com/ginsys/bronzeward/internal/talos",
		"github.com/jackc/pgx/v5",
	} {
		if !slices.Contains(found, want) {
			t.Errorf("forbidden misses %s in internal/api's closure", want)
		}
	}
}
