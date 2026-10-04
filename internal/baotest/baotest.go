// Package baotest gives a test child tokens of a real OpenBao, made under the fixture's committed
// policy files (fixtures/openbao/policies), as dbtest gives it a database. The server is named by
// BW_TEST_BAO_ADDR and administered with BW_TEST_BAO_TOKEN (mise run dev-bao's root token
// locally, CI's go-db service); without the address a test skips, unless BW_REQUIRE_BAO=1, when
// it fails: CI's go-db job sets both, so a provider that never came up cannot pass as skipped
// tests. The policies are written under test-unique names and deleted, and the tokens revoked,
// when the test ends.
package baotest

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Keys are the Transit keys the fixture creates, bw-artifact as bin/up always has and the three
// ingestion keys.
var Keys = []string{"bw-artifact", "bw-baseline", "bw-staging", "bw-digest"}

// Policies are the fixture's policy files, by the name bin/up writes each under.
var Policies = []string{"bw-ingestion", "bw-compiler", "bw-executor", "bw-metadata", "bw-metadata-only", "bw-orphan-report"}

// Bao is one test's view of the server.
type Bao struct {
	t     testing.TB
	Addr  string
	admin string
	// prefix names this test's policies: bwtest-<random>-.
	prefix string
}

// New ensures KV v2 at secret/, Transit at transit/ and the four keys, and writes the fixture's
// policy files under this test's names.
func New(t testing.TB) *Bao {
	t.Helper()
	addr, admin := os.Getenv("BW_TEST_BAO_ADDR"), os.Getenv("BW_TEST_BAO_TOKEN")
	skip, fail := decide(addr, os.Getenv("BW_REQUIRE_BAO"))
	if fail {
		t.Fatal("BW_REQUIRE_BAO=1 but BW_TEST_BAO_ADDR is unset")
	}
	if skip {
		t.Skip("BW_TEST_BAO_ADDR unset")
	}
	if admin == "" {
		t.Fatal("BW_TEST_BAO_ADDR is set but BW_TEST_BAO_TOKEN is not")
	}
	var r [6]byte
	rand.Read(r[:])
	b := &Bao{t: t, Addr: strings.TrimSuffix(addr, "/"), admin: admin, prefix: "bwtest-" + hex.EncodeToString(r[:]) + "-"}
	b.ensureMount("secret", "kv", map[string]string{"version": "2"})
	b.ensureMount("transit", "transit", nil)
	for _, k := range Keys {
		b.ensureKey(k)
	}
	dir := policyDir(t)
	for _, name := range Policies {
		hcl, err := os.ReadFile(filepath.Join(dir, name+".hcl"))
		if err != nil {
			t.Fatal(err)
		}
		b.Policy(name, string(hcl))
	}
	return b
}

// Admin is the administrator's token.
func (b *Bao) Admin() string { return b.admin }

// Policy writes hcl under this test's name for name, deleted when the test ends.
func (b *Bao) Policy(name, hcl string) {
	b.t.Helper()
	full := b.prefix + name
	b.must(http.MethodPut, "/v1/sys/policies/acl/"+full, map[string]string{"policy": hcl}, nil)
	b.t.Cleanup(func() {
		if status, _, err := b.Do(b.admin, http.MethodDelete, "/v1/sys/policies/acl/"+full, nil); err != nil || status/100 != 2 {
			b.t.Errorf("deleting policy %s: status %d, %v", full, status, err)
		}
	})
}

// Name is the name name is written under on the server for this test.
func (b *Bao) Name(name string) string { return b.prefix + name }

// Token is a child token of the administrator holding exactly the named policies (each written
// by New or Policy), without the default policy, with a five-minute TTL, revoked when the test
// ends.
func (b *Bao) Token(policies ...string) string {
	b.t.Helper()
	full := make([]string, len(policies))
	for i, p := range policies {
		full[i] = b.prefix + p
	}
	var out struct {
		Auth struct {
			ClientToken string `json:"client_token"`
		} `json:"auth"`
	}
	b.must(http.MethodPost, "/v1/auth/token/create", map[string]any{
		"policies": full, "no_default_policy": true, "ttl": "5m",
	}, &out)
	if out.Auth.ClientToken == "" {
		b.t.Fatal("token create returned no token")
	}
	tok := out.Auth.ClientToken
	b.t.Cleanup(func() {
		if status, _, err := b.Do(b.admin, http.MethodPost, "/v1/auth/token/revoke", map[string]string{"token": tok}); err != nil || status/100 != 2 {
			b.t.Errorf("revoking a test token: status %d, %v", status, err)
		}
	})
	return tok
}

var client = &http.Client{
	Timeout:       30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// Do sends one request as token and returns the status and body. in is encoded as JSON unless nil.
func (b *Bao) Do(token, method, path string, in any) (int, []byte, error) {
	var body io.Reader
	if in != nil {
		enc, err := json.Marshal(in)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(enc)
	}
	req, err := http.NewRequest(method, b.Addr+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("X-Vault-Token", token)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, out, err
}

// must sends as the administrator and fails the test unless the answer is 2xx.
func (b *Bao) must(method, path string, in, out any) {
	b.t.Helper()
	status, body, err := b.Do(b.admin, method, path, in)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, path, err)
	}
	if status/100 != 2 {
		b.t.Fatalf("%s %s: status %d: %s", method, path, status, body)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			b.t.Fatalf("%s %s: %v", method, path, err)
		}
	}
}

// ensureMount mounts typ at path unless a mount is there, which must then be of that type and
// options. A concurrent test may mount it first: the create's failure is followed by a read.
func (b *Bao) ensureMount(path, typ string, options map[string]string) {
	b.t.Helper()
	check := func() (bool, error) {
		status, body, err := b.Do(b.admin, http.MethodGet, "/v1/sys/mounts/"+path, nil)
		if err != nil {
			return false, err
		}
		if status != http.StatusOK {
			return false, nil
		}
		var m struct {
			Data struct {
				Type    string            `json:"type"`
				Options map[string]string `json:"options"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &m); err != nil {
			return false, err
		}
		if m.Data.Type != typ {
			return false, fmt.Errorf("%s/ is a %s mount, want %s", path, m.Data.Type, typ)
		}
		for k, v := range options {
			if m.Data.Options[k] != v {
				return false, fmt.Errorf("%s/ has option %s=%q, want %q", path, k, m.Data.Options[k], v)
			}
		}
		return true, nil
	}
	if ok, err := check(); err != nil {
		b.t.Fatal(err)
	} else if ok {
		return
	}
	status, body, err := b.Do(b.admin, http.MethodPost, "/v1/sys/mounts/"+path, map[string]any{"type": typ, "options": options})
	if err != nil {
		b.t.Fatal(err)
	}
	if ok, cerr := check(); cerr != nil || !ok {
		b.t.Fatalf("mounting %s/: status %d: %s (then %v)", path, status, body, cerr)
	}
}

// ensureKey creates a Transit key of the default type, aes256-gcm96, unless it exists: read first,
// created on 404.
func (b *Bao) ensureKey(name string) {
	b.t.Helper()
	status, body, err := b.Do(b.admin, http.MethodGet, "/v1/transit/keys/"+name, nil)
	if err != nil {
		b.t.Fatal(err)
	}
	switch status {
	case http.StatusOK:
		return
	case http.StatusNotFound:
		b.must(http.MethodPost, "/v1/transit/keys/"+name, map[string]any{}, nil)
	default:
		b.t.Fatalf("reading transit key %s: status %d: %s", name, status, body)
	}
}

// policyDir is fixtures/openbao/policies of the module the test runs in, found from the working
// directory up to go.mod.
func policyDir(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "fixtures", "openbao", "policies")
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}

func decide(addr, require string) (skip, fail bool) {
	switch {
	case addr != "":
		return false, false
	case require == "1":
		return false, true
	default:
		return true, false
	}
}
