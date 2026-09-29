package main

import (
	"bytes"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ginsys/bronzeward/fixtures/oidc/issuer"
)

func TestKeygenMintDefects(t *testing.T) {
	key := filepath.Join(t.TempDir(), "key.json")
	if err := run([]string{"keygen", "-out", key}, io.Discard); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"mint", "-key", key, "-issuer", "http://issuer.test", "-human", "h-author"}, &out); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.TrimSpace(out.String()), "."); n != 2 {
		t.Fatalf("mint printed %q", out.String())
	}
	out.Reset()
	if err := run([]string{"defects"}, &out); err != nil || !slices.Equal(strings.Fields(out.String()), issuer.Defects) {
		t.Fatalf("defects: %q, %v", out.String(), err)
	}
	if err := run([]string{"mint", "-key", key, "-issuer", "http://issuer.test", "-human", "h-author", "-defect", "bogus"}, io.Discard); err == nil {
		t.Fatal("unknown defect accepted")
	}
}
