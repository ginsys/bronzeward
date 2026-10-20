package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/siderolabs/talos/pkg/machinery/config/configpatcher"
)

func TestBronzewardCompositionOrder(t *testing.T) {
	root := t.TempDir()
	paths := []string{"all/10.yaml.tpl", "all/20.yaml.tpl", "worker/10.yaml.tpl", "node/fixture-node/10.yaml.tpl"}
	names := []string{"all-first", "all-last", "role", "node"}
	for i, path := range paths {
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("machine:\n  network:\n    hostname: "+names[i]+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := &PatchContext{PatchesDir: root, Node: &Node{Host: "fixture-node", Role: RoleWorker}}
	patches, secrets, err := ctx.Load()
	if err != nil || len(patches) != 4 || len(secrets) != 0 {
		t.Fatalf("load: patches=%d secrets=%d error=%v", len(patches), len(secrets), err)
	}
	for i, name := range names {
		out, err := configpatcher.Apply(configpatcher.WithBytes([]byte(minimalMachineConfig)), patches[:i+1])
		if err != nil {
			t.Fatal(err)
		}
		data, err := out.Bytes()
		if err != nil || !bytes.Contains(data, []byte("hostname: "+name)) {
			t.Fatalf("prefix %d did not produce expected hostname: %v", i+1, err)
		}
	}
}

func TestBronzewardTemplateInputCanChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.yaml.tpl")
	if err := os.WriteFile(path, []byte("hostname: {{ env \"BW_FIXTURE_HOST\" }}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BW_FIXTURE_HOST", "first")
	a, err := RenderTemplate(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BW_FIXTURE_HOST", "second")
	b, err := RenderTemplate(path, nil)
	if err != nil || string(a) != "hostname: first\n" || string(b) != "hostname: second\n" {
		t.Fatalf("environment variation not reproduced: %v", err)
	}
}
