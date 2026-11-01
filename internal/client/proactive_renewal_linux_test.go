package client

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// expiringJWT is an access token with 10 s of life left, so Open renews it (§9.5 item 1).
func expiringJWT() string {
	exp := strconv.FormatInt(time.Now().Add(10*time.Second).Unix(), 10)
	return "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://idp.example.invalid","sub":"h-viewer","exp":`+exp+`}`)) + ".BWSYNTH-sig"
}

// TestProactiveRenewalLockWaitContinues checks the outcome that client-contracts.md §9.2 item 3 and
// §9.5 item 3 share for a proactive renewal (ginsys/bronzeward#480): a renewal lock that another
// process holds past lockBound is a failed renewal. Open shows a notice and the command goes on
// with the current token, and the stored sign-in is unchanged.
func TestProactiveRenewalLockWaitContinues(t *testing.T) {
	bound, late := lockBound, lateReleased
	t.Cleanup(func() { lockBound, lateReleased = bound, late })
	released := make(chan *os.File, 1)
	lockBound, lateReleased = 100*time.Millisecond, func(f *os.File) { released <- f }
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("BRONZEWARD_TOKEN_FILE", "")
	p := filepath.Join(home, "tokens")
	current := &Tokens{AccessToken: expiringJWT(), RefreshToken: "BWSYNTH-rt"}
	if err := WriteTokens(p, current); err != nil {
		t.Fatal(err)
	}
	held, err := os.OpenFile(p+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	x := Context{Name: "lab", Server: "https://bw.example.invalid", Credential: "oidc",
		OIDC: &OIDC{Issuer: "https://idp.example.invalid", ClientID: "BWSYNTH-cli", TokenFile: p}}
	var notices bytes.Buffer
	c, err := Open(&x, TokenInput{NoInput: true}, &notices, nil)
	if err != nil {
		held.Close()
		t.Fatalf("Open under a held renewal lock: %v; want the command to go on", err)
	}
	for _, want := range []string{
		"did not release it within four minutes, so the stored sign-in was not changed",
		"the current token stays in use until the server refuses it",
	} {
		if !strings.Contains(notices.String(), want) {
			t.Errorf("the notice %q lacks %q", notices.String(), want)
		}
	}
	if c.token != current.AccessToken {
		t.Errorf("the command uses another token than the current one")
	}
	held.Close()
	select {
	case f := <-released:
		f.Close()
	case <-time.After(10 * time.Second):
		t.Fatal("the given-up wait did not take and release the lock within 10 s")
	}
	if s, err := ReadTokens(p); err != nil || *s != *current {
		t.Errorf("the stored sign-in after the failed renewal: %v, %v; want it unchanged", s, err)
	}
}

// TestProactiveRenewalStoreTimeoutContinues is the same outcome for a credential store that does
// not answer under the lock: the read under the lock fails, Open shows a notice that carries the
// store's own words, and the command goes on with the current token.
func TestProactiveRenewalStoreTimeoutContinues(t *testing.T) {
	offered, get, getHeld, set, del := secretServiceOffered, keyringGetCall, keyringGetHeldCall, keyringSetCall, keyringDeleteCall
	t.Cleanup(func() {
		secretServiceOffered, keyringGetCall, keyringGetHeldCall, keyringSetCall, keyringDeleteCall = offered, get, getHeld, set, del
	})
	secretServiceOffered = func() (bool, error) { return true, nil }
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("BRONZEWARD_TOKEN_FILE", "")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(home, "bus"))
	x := Context{Name: "store", Server: "https://bw.example.invalid", Credential: "oidc",
		OIDC: &OIDC{Issuer: "https://idp.example.invalid", ClientID: "BWSYNTH-cli"}}
	current := Tokens{AccessToken: expiringJWT(), RefreshToken: "BWSYNTH-rt"}
	b, err := json.Marshal(entry{Server: x.Server, Issuer: x.OIDC.Issuer, Tokens: current})
	if err != nil {
		t.Fatal(err)
	}
	keyringGetCall = func(string, string) (string, error) { return string(b), nil }
	keyringGetHeldCall = func(string, string) (string, error) { return "", errStoreSlow }
	var writes int
	keyringSetCall = func(string, string, string) error { writes++; return nil }
	keyringDeleteCall = func(string, string) error { writes++; return nil }
	var notices bytes.Buffer
	c, err := Open(&x, TokenInput{}, &notices, nil)
	if err != nil {
		t.Fatalf("Open with a store that does not answer: %v; want the command to go on", err)
	}
	for _, want := range []string{errStoreSlow.Error(), "the current token stays in use until the server refuses it"} {
		if !strings.Contains(notices.String(), want) {
			t.Errorf("the notice %q lacks %q", notices.String(), want)
		}
	}
	if c.token != current.AccessToken || writes != 0 {
		t.Errorf("token kept: %v, %d writes or deletions; want the current token and none", c.token == current.AccessToken, writes)
	}
}

// TestProactiveRenewalUnsettledWriteStatesOutcome checks the words of a store write that got no
// answer: the notice of a proactive renewal carries them, so the operator knows the rotated pair
// may still be kept.
func TestProactiveRenewalUnsettledWriteStatesOutcome(t *testing.T) {
	err := unsettled(errStoreSlow, true, "written")
	if err == nil || !strings.Contains(err.Error(), "the sign-in may still be written after this command ends") || errors.Is(err, errStoreSlow) {
		t.Errorf("unsettled write: %v; want the outcome stated", err)
	}
}
