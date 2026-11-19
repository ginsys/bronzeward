package signer

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// This package holds a cluster's CA key in memory and signs from it, so its source is held to a
// closed list of imports: nothing that writes a file, logs, prints, speaks http or runs a process
// can be added without this test failing and the list being edited in review. The machinery's
// client is not on it: the package reaches a node only through internal/talos's reader.
//
// crypto/x509 and encoding/pem only parse the certificate Sign has just made, to read its serial and
// NotAfter back; fmt and io only implement the Format method that keeps a Cert from rendering.
var allowedImports = []string{
	"bytes",
	"context",
	"crypto/x509",
	"encoding/pem",
	"errors",
	"fmt",
	"io",
	"time",
	"github.com/ginsys/bronzeward/internal/provider",
	"github.com/ginsys/bronzeward/internal/talos",
	"github.com/siderolabs/crypto/x509",
	"github.com/siderolabs/talos/pkg/machinery/client/config",
	"github.com/siderolabs/talos/pkg/machinery/config/configloader",
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets",
	"github.com/siderolabs/talos/pkg/machinery/role",
	"go.yaml.in/yaml/v4",
	"google.golang.org/grpc/codes",
	"google.golang.org/grpc/status",
}

// stray lists the imports of the named sources that are not in allowed.
func stray(t *testing.T, sources map[string]string, allowed []string) []string {
	t.Helper()
	var out []string
	for name, src := range sources {
		f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(allowed, p) {
				out = append(out, name+": "+p)
			}
		}
	}
	slices.Sort(out)
	return out
}

func TestImportsAreAllowlisted(t *testing.T) {
	// The control: a source that writes a file, logs and uses the machinery's client is caught.
	control := map[string]string{"c.go": `package signer
import (
	"os"
	"log"
	"github.com/siderolabs/talos/pkg/machinery/client"
)`}
	if got := stray(t, control, allowedImports); len(got) != 3 {
		t.Fatalf("the control found %d strays, want 3: %v", len(got), got)
	}

	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	real := map[string]string{}
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		b, err := os.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		real[n] = string(b)
	}
	if len(real) == 0 {
		t.Fatal("no source files read")
	}
	// The read saw the package: signer.go imports the secrets package, so the allowlist was used.
	if !strings.Contains(real["signer.go"], "generate/secrets") {
		t.Fatal("the read did not reach signer.go")
	}
	if got := stray(t, real, allowedImports); len(got) != 0 {
		t.Fatalf("imports outside the allowlist: %v", got)
	}
}
