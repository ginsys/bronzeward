package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const referencePath = "../../docs/operator/clients/cli-reference.md"

// TestCLIReference holds docs/operator/clients/cli-reference.md to the command table: `mise run generate` sets
// BRONZEWARD_WRITE_REFERENCE=1 to write it, and every other run, `mise run go` included, fails when the file
// differs from what the table makes.
func TestCLIReference(t *testing.T) {
	want, err := referenceText()
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range cmdTable {
		hashes := "##"
		if strings.Contains(x.path, " ") {
			hashes = "###"
		}
		if !strings.Contains(want, "\n"+hashes+" "+x.path+"\n") {
			t.Errorf("the reference has no section for %q", x.path)
		}
	}
	if os.Getenv("BRONZEWARD_WRITE_REFERENCE") == "1" {
		if err := os.MkdirAll(filepath.Dir(referencePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(referencePath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(referencePath)
	if err != nil {
		t.Fatalf("%v: run mise run generate", err)
	}
	if string(got) != want {
		t.Errorf("docs/operator/clients/cli-reference.md is stale: run mise run generate")
	}
}
