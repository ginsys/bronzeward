//go:build darwin

package client

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// The tests here use the real security tool, on a keychain of their own with a synthetic password.
// They run only with BW_REQUIRE_KEYCHAIN=1 (CI's keychain job), because they make that keychain the
// default one and the only one searched while they run. Nothing reaches the operator's login
// keychain. They are skipped otherwise, as the Secret Service tests are without BW_REQUIRE_KEYRING.

const tempKeychainPassword = "BWSYNTH-keychain"

// securityOut runs the security tool and returns its output, failing the test when it fails and
// ok is false. It stops the tool after 20 seconds, so that a password dialog the tool waits on ends
// the call and not the whole test run; the output then says so.
func securityOut(t *testing.T, ok bool, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/security", args...)
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		out = append(out, "[no answer within 20 s: the tool may be waiting on a password dialog]"...)
	}
	if err != nil && ok {
		t.Fatalf("security %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// temporaryKeychain makes a keychain of its own the default one and the only one searched, unlocked
// with a synthetic password, and puts the operator's lists back afterwards.
func temporaryKeychain(t *testing.T) string {
	t.Helper()
	if os.Getenv("BW_REQUIRE_KEYCHAIN") != "1" {
		t.Skip("BW_REQUIRE_KEYCHAIN is not 1")
	}
	if _, err := os.Stat("/usr/bin/security"); err != nil {
		t.Fatalf("the security tool is missing, and BW_REQUIRE_KEYCHAIN=1: %v", err)
	}
	path := filepath.Join(t.TempDir(), "bwtest.keychain-db")
	var oldList []string
	for _, l := range strings.Split(securityOut(t, true, "list-keychains", "-d", "user"), "\n") {
		if l = strings.Trim(strings.TrimSpace(l), `"`); l != "" {
			oldList = append(oldList, l)
		}
	}
	oldDefault := strings.Trim(strings.TrimSpace(securityOut(t, true, "default-keychain", "-d", "user")), `"`)
	t.Cleanup(func() {
		securityOut(t, false, append([]string{"list-keychains", "-d", "user", "-s"}, oldList...)...)
		if oldDefault != "" {
			securityOut(t, false, "default-keychain", "-d", "user", "-s", oldDefault)
		}
		securityOut(t, false, "delete-keychain", path)
	})
	securityOut(t, true, "create-keychain", "-p", tempKeychainPassword, path)
	securityOut(t, true, "set-keychain-settings", path) // no automatic lock
	securityOut(t, true, "unlock-keychain", "-p", tempKeychainPassword, path)
	securityOut(t, true, "default-keychain", "-d", "user", "-s", path)
	securityOut(t, true, "list-keychains", "-d", "user", "-s", path)
	return path
}

// TestKeychainRoundTrip checks the wrapper against the real Keychain: what it writes go-keyring
// reads, what go-keyring writes it reads, and a missing item reads as none.
func TestKeychainRoundTrip(t *testing.T) {
	temporaryKeychain(t)
	const user, other = "BWSYNTH-a", "BWSYNTH-b"
	if v, found, err := keyringGet(user); err != nil || found {
		t.Fatalf("an empty keychain: %q, %v, %v; want none", v, found, err)
	}
	const secret = "{\"accessToken\":\"BWSYNTH-é'\\\"\",\n\"refreshToken\":\"BWSYNTH\"}"
	if err := keyringSet(user, secret); err != nil {
		t.Fatalf("keyringSet: %v", err)
	}
	if v, found, err := keyringGetHeld(user); err != nil || !found || v != secret {
		t.Errorf("keyringGetHeld: %q, %v, %v; want the secret back", v, found, err)
	}
	if v, err := keyring.Get(keyringService, user); err != nil || v != secret {
		t.Errorf("go-keyring read %q, %v of the wrapper's entry", v, err)
	}
	if err := keyring.Set(keyringService, other, secret+"2"); err != nil {
		t.Fatalf("go-keyring Set: %v", err)
	}
	if v, found, err := keyringGet(other); err != nil || !found || v != secret+"2" {
		t.Errorf("the wrapper read %q, %v, %v of go-keyring's entry", v, found, err)
	}
	if err := keyringSet(user, secret+"3"); err != nil {
		t.Fatalf("an overwrite: %v", err)
	}
	if v, _, err := keyringGet(user); err != nil || v != secret+"3" {
		t.Errorf("after an overwrite: %q, %v; want the new secret", v, err)
	}
	for _, u := range []string{user, other} {
		if err := keyringDelete(u); err != nil {
			t.Errorf("keyringDelete %s: %v", u, err)
		}
		if _, found, err := keyringGet(u); err != nil || found {
			t.Errorf("after keyringDelete %s: %v, %v; want none", u, found, err)
		}
	}
	if err := keyringDelete(user); err != nil {
		t.Errorf("removing a missing item: %v, want none", err)
	}
}

// TestKeychainEntrySize writes the largest entry the tool takes and logs its size, so the first
// run records the limit (about 2.9 KB of sign-in) a real Zitadel entry must stay under.
func TestKeychainEntrySize(t *testing.T) {
	temporaryKeychain(t)
	largest := 0
	for n := 2500; n <= 3100; n += 50 {
		err := keyringSet("BWSYNTH-size", strings.Repeat("a", n))
		if errors.Is(err, errEntryTooBig) {
			break
		} else if err != nil {
			t.Fatalf("%d bytes: %v", n, err)
		}
		largest = n
	}
	t.Logf("the largest sign-in written in one command: %d bytes (limit %d bytes of command line)", largest, securityLine)
	if largest < 2500 {
		t.Errorf("the largest entry is %d bytes; want at least 2500", largest)
	}
}

// TestKeychainSecondKeychainLocked checks that a locked keychain elsewhere in the search list is no
// concern: every call names the default keychain, so none touches the locked one and none waits on
// a dialog for it (client-contracts.md section 9.2 item 3).
func TestKeychainSecondKeychainLocked(t *testing.T) {
	path := temporaryKeychain(t)
	second := filepath.Join(t.TempDir(), "bwsecond.keychain-db")
	t.Cleanup(func() { securityOut(t, false, "delete-keychain", second) })
	securityOut(t, true, "create-keychain", "-p", tempKeychainPassword, second)
	securityOut(t, true, "list-keychains", "-d", "user", "-s", path, second)
	securityOut(t, true, "lock-keychain", second)
	const user = "BWSYNTH-second"
	for name, call := range map[string]func() error{
		"keyringSet":     func() error { return keyringSet(user, "BWSYNTH-v") },
		"keyringGetHeld": func() error { _, _, err := keyringGetHeld(user); return err },
		"keyringDelete":  func() error { return keyringDelete(user) },
	} {
		done := make(chan error, 1)
		go func() { done <- call() }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%s with a locked second keychain: %v, want it to work", name, err)
			}
		case <-time.After(2 * securityHeldBound):
			t.Fatalf("%s with a locked second keychain is still waiting after %v", name, 2*securityHeldBound)
		}
	}
}

// TestKeychainLockedRefused locks the keychain and checks that every call under the renewal lock
// is refused at once with nothing written or removed, and that the entry reads again once the
// keychain is unlocked. It logs what show-keychain-info answers for each state.
func TestKeychainLockedRefused(t *testing.T) {
	path := temporaryKeychain(t)
	const user = "BWSYNTH-locked"
	if err := keyringSet(user, "BWSYNTH-v"); err != nil {
		t.Fatal(err)
	}
	t.Logf("show-keychain-info, unlocked: %q", securityOut(t, false, "show-keychain-info", path))
	securityOut(t, true, "lock-keychain", path)
	t.Logf("show-keychain-info, locked: %q", securityOut(t, false, "show-keychain-info", path))
	for name, call := range map[string]func() error{
		"keyringGetHeld": func() error { _, _, err := keyringGetHeld(user); return err },
		"keyringSet":     func() error { return keyringSet(user, "BWSYNTH-v2") },
		"keyringDelete":  func() error { return keyringDelete(user) },
	} {
		done := make(chan error, 1)
		go func() { done <- call() }()
		select {
		case err := <-done:
			if !errors.Is(err, errStoreLocked) {
				t.Errorf("%s on a locked keychain: %v, want errStoreLocked", name, err)
			}
		case <-time.After(2 * securityHeldBound):
			t.Fatalf("%s on a locked keychain is still waiting after %v", name, 2*securityHeldBound)
		}
	}
	securityOut(t, true, "unlock-keychain", "-p", tempKeychainPassword, path)
	if v, found, err := keyringGet(user); err != nil || !found || v != "BWSYNTH-v" {
		t.Errorf("after the refusals the entry reads %q, %v, %v; want it unchanged", v, found, err)
	}
}
