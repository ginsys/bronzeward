package providers

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func fixtureSOPS(t *testing.T, script string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sops"), []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	return filepath.Join(root, "bundle.yaml")
}

func TestBronzewardEncryptedWriteBranch(t *testing.T) {
	path := fixtureSOPS(t, "printf 'ENC[synthetic stub output]'\n")
	err := NewFilesystemSecretsProvider(path, nil).Put("fixture", []byte("synthetic input"))
	data, readErr := os.ReadFile(path)
	if err != nil || readErr != nil || string(data) != "ENC[synthetic stub output]" {
		t.Fatalf("successful subprocess output not stored: put=%v read=%v", err, readErr)
	}
	// This exercises the subprocess branch, not cryptographic encryption.
}

func TestBronzewardEncryptionFailureMustRefusePersistence(t *testing.T) {
	path := fixtureSOPS(t, "printf 'called' > \"$BW_SOPS_CALLED\"\nexit 42\n")
	called := filepath.Join(filepath.Dir(path), "called")
	t.Setenv("BW_SOPS_CALLED", called)
	payload := []byte("bronzeward: synthetic fixture value\n")
	err := NewFilesystemSecretsProvider(path, nil).Put("fixture", payload)
	if data, e := os.ReadFile(called); e != nil || string(data) != "called" {
		t.Fatalf("failing encryption stub was not executed: %v", e)
	}
	data, readErr := os.ReadFile(path)
	if err == nil && readErr == nil && bytes.Equal(data, payload) {
		t.Fatal("BW_PLAINTEXT_PERSISTED: failing encryption returned success and wrote the original bytes")
	}
	if err == nil || !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("unexpected failure outcome: put=%v read=%v", err, readErr)
	}
}
