package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
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
	out.Reset()
	if err := run([]string{"mint", "-key", key, "-issuer", "http://issuer.test", "-human", "h-approver", "-without-group", "bw-approvers"}, &out); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.TrimSpace(out.String()), "."); n != 2 {
		t.Fatalf("mint -without-group printed %q", out.String())
	}
	if err := run([]string{"mint", "-key", key, "-issuer", "http://issuer.test", "-human", "h-approver", "-without-group", "bw-approvers",
		"-defect", "expired"}, io.Discard); err == nil {
		t.Fatal("-without-group with a defect accepted")
	}
}

// serve refuses a device sign-in for a human it does not know, before it listens.
func TestServeUnknownDeviceHuman(t *testing.T) {
	key := filepath.Join(t.TempDir(), "key.json")
	if err := run([]string{"keygen", "-out", key}, io.Discard); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"serve", "-key", key, "-issuer", "http://issuer.test", "-device-human", "h-nobody"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "unknown human") {
		t.Fatalf("serve -device-human h-nobody: %v", err)
	}
}

// -age issues the token in the past, so its expiry comes sooner: the browser cases for a stream the
// token's expiry ends need that without waiting the whole lifetime out.
func TestMintAge(t *testing.T) {
	key := filepath.Join(t.TempDir(), "key.json")
	if err := run([]string{"keygen", "-out", key}, io.Discard); err != nil {
		t.Fatal(err)
	}
	expiry := func(extra ...string) int64 {
		t.Helper()
		var out bytes.Buffer
		if err := run(append([]string{"mint", "-key", key, "-issuer", "http://issuer.test", "-human", "h-author"}, extra...), &out); err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(strings.TrimSpace(out.String()), ".")
		raw, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			t.Fatal(err)
		}
		var claims struct {
			Exp int64 `json:"exp"`
		}
		if err := json.Unmarshal(raw, &claims); err != nil {
			t.Fatal(err)
		}
		return claims.Exp
	}
	if got := expiry() - expiry("-age", "4m30s"); got < 269 || got > 271 {
		t.Fatalf("-age 4m30s moved the expiry by %d s, want 270", got)
	}
}
